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
	"fmt"
	"net"
	"os"
	"path/filepath"
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
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("ssh: known_hosts %s: %w (host-key verification is required — supply KnownHostsPath or accept the host into ~/.ssh/known_hosts first)", path, err)
	}
	cb, err := knownhosts.New(path)
	if err != nil {
		return nil, fmt.Errorf("ssh: load known_hosts %s: %w", path, err)
	}
	return cb, nil
}
