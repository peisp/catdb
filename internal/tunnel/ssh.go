// Package tunnel implements the SSH jump host used by the MySQL (and future)
// drivers. The goal is small and reusable: take an SSHConfig and return a
// *ssh.Client plus a DialContext function the driver can register.
//
// Key rules (see ARCHITECTURE.md §6.2 and CLAUDE.md #8):
//   - Host keys are ALWAYS verified. InsecureIgnoreHostKey is never used.
//     Empty known_hosts path → fall back to the user's ~/.ssh/known_hosts.
//   - Auth methods are tried in order: explicit private key → password →
//     ssh-agent. The first successful one is used.
//   - Tunneling pgx/Postgres also needs LookupFunc — that lives in
//     plugins/postgresdrv/ later; this file stays SQL-driver-agnostic.
package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"

	"catdb/internal/dbdriver"
)

// DialContextFunc matches the signature expected by mysql.RegisterDialContext.
type DialContextFunc func(ctx context.Context, addr string) (net.Conn, error)

// Tunnel bundles an open *ssh.Client with a Dial function that routes through
// it. Close() shuts the SSH connection — callers are responsible for closing
// any database connections that were dialed through it first.
type Tunnel struct {
	Client *ssh.Client
	Dial   DialContextFunc
}

// Close terminates the underlying SSH connection. Safe to call more than once.
func (t *Tunnel) Close() error {
	if t == nil || t.Client == nil {
		return nil
	}
	err := t.Client.Close()
	t.Client = nil
	return err
}

// --- host-key types -----------------------------------------------------------

// HostKeyInfo describes an SSH host key shown to the user for trust-on-first-use
// confirmation. Fingerprint is SHA256:Base64 (the standard OpenSSH format).
type HostKeyInfo struct {
	Host        string `json:"host"`
	KeyType     string `json:"keyType"`
	Fingerprint string `json:"fingerprint"` // e.g. "SHA256:xxxx"
}

// ErrUnknownHostKey is returned when the server's host key is not present in
// any known_hosts file. Callers should present the Info to the user and, on
// acceptance, call AddHostKey and retry the connection.
type ErrUnknownHostKey struct {
	Info           HostKeyInfo
	KnownHostsPath string // the file that was checked; caller should write here
	key            ssh.PublicKey
}

func (e *ErrUnknownHostKey) Error() string {
	return fmt.Sprintf("ssh: unknown host key for %s: %s %s — add it to known_hosts to trust this host",
		e.Info.Host, e.Info.KeyType, e.Info.Fingerprint)
}

// PublicKey returns the raw public key the server presented. Callers can pass
// this to AddHostKey after the user accepts.
func (e *ErrUnknownHostKey) PublicKey() ssh.PublicKey { return e.key }

// ErrHostKeyMismatch is returned when the server's host key does not match the
// key stored in known_hosts for this host. This may indicate a MITM attack or
// a legitimate host-key change.
type ErrHostKeyMismatch struct {
	Info      HostKeyInfo
	KnownKeys []knownhosts.KnownKey
}

func (e *ErrHostKeyMismatch) Error() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("ssh: host key mismatch for %s: server presented %s %s; known keys:",
		e.Info.Host, e.Info.KeyType, e.Info.Fingerprint))
	for i, kk := range e.KnownKeys {
		fmt.Fprintf(&b, "\n  [%d] %s:%d %s",
			i, kk.Filename, kk.Line, ssh.FingerprintSHA256(kk.Key))
	}
	return b.String()
}

// --- public API ---------------------------------------------------------------

// Open establishes an SSH connection to cfg and returns a Tunnel. The caller
// owns the Tunnel and MUST Close it (after closing any DB connections that
// rely on it).
//
// ctx applies to the TCP dial + initial SSH handshake. The handshake timeout
// also enforces a hard cap to avoid hanging on a stalled server.
func Open(ctx context.Context, cfg *dbdriver.SSHConfig) (*Tunnel, error) {
	if cfg == nil {
		return nil, fmt.Errorf("ssh: nil config")
	}
	if cfg.Host == "" {
		return nil, fmt.Errorf("ssh: host is required")
	}
	port := cfg.Port
	if port == 0 {
		port = 22
	}
	if cfg.User == "" {
		return nil, fmt.Errorf("ssh: user is required")
	}

	auth, err := buildAuth(cfg)
	if err != nil {
		return nil, err
	}
	if len(auth) == 0 {
		return nil, fmt.Errorf("ssh: no authentication method provided (need password, private key, or ssh-agent)")
	}

	hostKeyCb, err := buildHostKeyCallback(cfg.KnownHostsPath)
	if err != nil {
		return nil, err
	}

	sshCfg := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: hostKeyCb,
		Timeout:         15 * time.Second,
		// Match OpenSSH preference order so we negotiate the same host key
		// type that would be stored by `ssh` on the command line.  Without
		// this the Go x/crypto defaults (ECDSA-first) can pick a different
		// key than what OpenSSH (ED25519-first) stored, causing a spurious
		// "key mismatch".
		HostKeyAlgorithms: []string{
			ssh.KeyAlgoED25519,
			ssh.KeyAlgoECDSA256,
			ssh.KeyAlgoECDSA384,
			ssh.KeyAlgoECDSA521,
			ssh.KeyAlgoRSASHA512,
			ssh.KeyAlgoRSASHA256,
			ssh.KeyAlgoRSA,
		},
	}

	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", port))

	// Dial with ctx so cancellation aborts the connect attempt.
	var d net.Dialer
	netConn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("ssh: dial %s: %w", addr, err)
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(netConn, addr, sshCfg)
	if err != nil {
		_ = netConn.Close()
		return nil, fmt.Errorf("ssh: handshake: %w", err)
	}
	client := ssh.NewClient(sshConn, chans, reqs)

	dial := func(ctx context.Context, target string) (net.Conn, error) {
		// ssh.Client.Dial is not context-aware natively. We approximate cancel
		// by spawning the dial in a goroutine and returning the first to land.
		type result struct {
			conn net.Conn
			err  error
		}
		ch := make(chan result, 1)
		go func() {
			c, e := client.Dial("tcp", target)
			ch <- result{c, e}
		}()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case r := <-ch:
			return r.conn, r.err
		}
	}

	return &Tunnel{Client: client, Dial: dial}, nil
}

// AddHostKey appends the host's public key to the known_hosts file. If path is
// empty, ~/.ssh/known_hosts is used. The .ssh directory is created if it does
// not exist.
func AddHostKey(path string, host string, key ssh.PublicKey) error {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("ssh: resolve home dir: %w", err)
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return fmt.Errorf("ssh: create %s: %w", dir, err)
	}

	line := knownhosts.Line([]string{host}, key)

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return fmt.Errorf("ssh: open known_hosts %s: %w", path, err)
	}
	defer f.Close()

	// Ensure the line starts on its own line if the file already has content
	// without a trailing newline.
	if info, err := f.Stat(); err == nil && info.Size() > 0 {
		// Read the last byte to check for trailing newline.
		buf := make([]byte, 1)
		// Use os.File.ReadAt which is available; position at last byte.
		if _, err := f.ReadAt(buf, info.Size()-1); err == nil && buf[0] != '\n' {
			if _, err := f.WriteString("\n"); err != nil {
				return fmt.Errorf("ssh: write known_hosts: %w", err)
			}
		}
	}

	if _, err := f.WriteString(line + "\n"); err != nil {
		return fmt.Errorf("ssh: write known_hosts: %w", err)
	}
	return nil
}

// --- internals ----------------------------------------------------------------

func buildAuth(cfg *dbdriver.SSHConfig) ([]ssh.AuthMethod, error) {
	var auths []ssh.AuthMethod

	if cfg.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if cfg.PrivateKeyPass != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(cfg.PrivateKey), []byte(cfg.PrivateKeyPass))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(cfg.PrivateKey))
		}
		if err != nil {
			return nil, fmt.Errorf("ssh: parse private key: %w", err)
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}

	if cfg.Password != "" {
		auths = append(auths, ssh.Password(cfg.Password))
	}

	if cfg.UseAgent {
		sock := os.Getenv("SSH_AUTH_SOCK")
		if sock == "" {
			return nil, fmt.Errorf("ssh: agent auth requested but SSH_AUTH_SOCK is empty")
		}
		conn, err := net.Dial("unix", sock)
		if err != nil {
			return nil, fmt.Errorf("ssh: connect to agent: %w", err)
		}
		ag := agent.NewClient(conn)
		auths = append(auths, ssh.PublicKeysCallback(ag.Signers))
	}

	return auths, nil
}

func buildHostKeyCallback(path string) (ssh.HostKeyCallback, error) {
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("ssh: resolve home dir: %w", err)
		}
		path = filepath.Join(home, ".ssh", "known_hosts")
	}

	resolvedPath := path
	baseCb, err := knownhosts.New(path)
	if err != nil {
		// If the known_hosts file simply doesn't exist, create a callback
		// that treats every host as unknown (triggers TOFU).  The file will
		// be created by AddHostKey on user acceptance.
		if os.IsNotExist(err) {
			return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
				return &ErrUnknownHostKey{
					Info: HostKeyInfo{
						Host:        hostname,
						KeyType:     key.Type(),
						Fingerprint: ssh.FingerprintSHA256(key),
					},
					key:            key,
					KnownHostsPath: resolvedPath,
				}
			}, nil
		}
		return nil, fmt.Errorf("ssh: load known_hosts %s: %w", path, err)
	}

	// Wrap the knownhosts callback so that when verification fails we
	// return typed errors (ErrUnknownHostKey / ErrHostKeyMismatch) that
	// carry the presented key. Callers upstream can inspect these and
	// offer a trust-on-first-use flow.
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		err := baseCb(hostname, remote, key)
		if err == nil {
			return nil
		}

		info := HostKeyInfo{
			Host:        hostname,
			KeyType:     key.Type(),
			Fingerprint: ssh.FingerprintSHA256(key),
		}

		var keyErr *knownhosts.KeyError
		if errors.As(err, &keyErr) {
			if len(keyErr.Want) == 0 {
				return &ErrUnknownHostKey{Info: info, key: key, KnownHostsPath: resolvedPath}
			}
			return &ErrHostKeyMismatch{Info: info, KnownKeys: keyErr.Want}
		}

		return err
	}, nil
}
