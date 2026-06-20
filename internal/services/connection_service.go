package services

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"catdb/internal/core/session"
	"catdb/internal/dbdriver"
	"catdb/internal/registry"
	"catdb/internal/storage"
	"catdb/internal/tunnel"
	"catdb/wailsbridge"
)

// ConnectionService is the Wails Service that owns connection profiles and
// the live-connection lifecycle. Per ARCHITECTURE.md §2 it stays THIN:
// validates input, calls into storage/session, no business logic.
type ConnectionService struct {
	store   *storage.Store
	secrets *storage.Secrets
	mgr     *session.Manager
}

// NewConnectionService wires the dependencies. None are optional.
func NewConnectionService(store *storage.Store, secrets *storage.Secrets, mgr *session.Manager) *ConnectionService {
	return &ConnectionService{store: store, secrets: secrets, mgr: mgr}
}

func (s *ConnectionService) ServiceName() string { return "ConnectionService" }

// --- driver introspection ---

// DriverInfo describes one registered driver to the front-end.
type DriverInfo struct {
	Name         string                    `json:"name"`
	Version      string                    `json:"version"`
	Capabilities dbdriver.Capabilities     `json:"capabilities"`
	Schema       []dbdriver.ConnParamField `json:"schema"`
}

// ListDrivers returns the set of registered drivers and their connection
// schemas. The connection form is rendered from this data — no front-end
// change is needed when a new driver lands.
func (s *ConnectionService) ListDrivers(_ context.Context) []DriverInfo {
	drivers := registry.List()
	out := make([]DriverInfo, 0, len(drivers))
	for _, d := range drivers {
		out = append(out, DriverInfo{
			Name:         d.Name(),
			Version:      d.Version(),
			Capabilities: d.Capabilities(),
			Schema:       d.ConnectionSchema(),
		})
	}
	return out
}

// --- profiles & groups ---

// ConnectionDraft is what the front-end sends to Save/Test. Secrets are
// passed via dedicated fields so we never persist them into the SQLite blob.
type ConnectionDraft struct {
	ID        string              `json:"id,omitempty"`
	Name      string              `json:"name"`
	Driver    string              `json:"driver"`
	GroupID   string              `json:"groupId,omitempty"`
	Host      string              `json:"host"`
	Port      int                 `json:"port"`
	User      string              `json:"user"`
	Database  string              `json:"database,omitempty"`
	Params    map[string]string   `json:"params,omitempty"`
	SSL       *dbdriver.SSLConfig `json:"ssl,omitempty"`
	SSHTunnel *dbdriver.SSHConfig `json:"sshTunnel,omitempty"`

	Password       string `json:"password,omitempty"`
	SSHPassword    string `json:"sshPassword,omitempty"`
	SSHKeyPassword string `json:"sshKeyPassword,omitempty"`
}

// ListConnections returns every saved profile (no secrets).
func (s *ConnectionService) ListConnections(ctx context.Context) ([]storage.ConnectionProfile, error) {
	return s.store.ListConnections(ctx)
}

// GetConnection returns one profile (no secret).
func (s *ConnectionService) GetConnection(ctx context.Context, id string) (storage.ConnectionProfile, error) {
	return s.store.GetConnection(ctx, id)
}

// SaveConnection upserts a profile and writes secrets to the keyring.
// New profiles get a fresh UUID. Returns the persisted profile (with ID +
// timestamps populated).
func (s *ConnectionService) SaveConnection(ctx context.Context, d ConnectionDraft) (storage.ConnectionProfile, error) {
	if err := s.validateDraft(d, true); err != nil {
		return storage.ConnectionProfile{}, err
	}
	prof := storage.ConnectionProfile{
		ID:        d.ID,
		Name:      d.Name,
		Driver:    d.Driver,
		GroupID:   d.GroupID,
		Host:      d.Host,
		Port:      d.Port,
		User:      d.User,
		Database:  d.Database,
		Params:    d.Params,
		SSL:       d.SSL,
		SSHTunnel: cloneSSHWithoutSecrets(d.SSHTunnel),
	}
	saved, err := s.store.SaveConnection(ctx, prof)
	if err != nil {
		return storage.ConnectionProfile{}, err
	}

	secret := storage.Secret{
		Password:       d.Password,
		SSHPassword:    d.SSHPassword,
		SSHKeyPassword: d.SSHKeyPassword,
	}
	if err := s.secrets.Save(saved.ID, secret); err != nil {
		return storage.ConnectionProfile{}, fmt.Errorf("ConnectionService: persist secret: %w", err)
	}
	return saved, nil
}

// DeleteConnection removes the profile, its secret, and closes any live
// connection. Idempotent regarding secrets / live state.
func (s *ConnectionService) DeleteConnection(ctx context.Context, id string) error {
	_ = s.mgr.Close(id)
	if err := s.store.DeleteConnection(ctx, id); err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			return err
		}
		return err
	}
	_ = s.secrets.Delete(id)
	return nil
}

// ListGroups returns the sidebar's folder list.
func (s *ConnectionService) ListGroups(ctx context.Context) ([]storage.Group, error) {
	return s.store.ListGroups(ctx)
}

// SaveGroup upserts a group.
func (s *ConnectionService) SaveGroup(ctx context.Context, g storage.Group) (storage.Group, error) {
	if g.Name == "" {
		return storage.Group{}, fmt.Errorf("ConnectionService: group name is required")
	}
	return s.store.SaveGroup(ctx, g)
}

// DeleteGroup removes a group. Refuses non-empty groups
// (storage.ErrGroupNotEmpty) — the user must move members elsewhere first.
func (s *ConnectionService) DeleteGroup(ctx context.Context, id string) error {
	return s.store.DeleteGroup(ctx, id)
}

// MoveConnection reassigns a connection to a different group, or detaches
// it (groupID == "" → 未分组). Used by the sidebar's drag-to-group flow.
// This is intentionally separate from SaveConnection so the move never
// reads/writes secrets — only group_id changes.
func (s *ConnectionService) MoveConnection(ctx context.Context, id, groupID string) error {
	if id == "" {
		return fmt.Errorf("ConnectionService: connection id is required")
	}
	return s.store.MoveConnection(ctx, id, groupID)
}

// --- runtime ---

// TestConnection opens a Connection from the draft, calls Ping, and closes —
// nothing is persisted. Used by the form's "Test" button.
func (s *ConnectionService) TestConnection(ctx context.Context, d ConnectionDraft) error {
	if err := s.validateDraft(d, false); err != nil {
		return err
	}
	return s.openAndPing(ctx, d)
}

// Connect opens (or returns the cached) live Connection for id.
func (s *ConnectionService) Connect(ctx context.Context, id string) error {
	_, err := s.mgr.Open(ctx, id)
	if err != nil {
		// If the host key is not yet trusted, offer the user a trust dialog,
		// write the key on acceptance, and retry.
		var ukErr *tunnel.ErrUnknownHostKey
		if errors.As(err, &ukErr) {
			if acceptUnknownHostKey(ukErr) {
				if addErr := tunnel.AddHostKey(ukErr.KnownHostsPath, ukErr.Info.Host, ukErr.PublicKey()); addErr != nil {
					return fmt.Errorf("ConnectionService: %w", addErr)
				}
				_, err = s.mgr.Open(ctx, id)
			}
		}
		// Show host key mismatch as a warning dialog.
		var mmErr *tunnel.ErrHostKeyMismatch
		if errors.As(err, &mmErr) {
			showHostKeyMismatch(mmErr)
		}
	}
	return err
}

// Disconnect closes a live Connection. Idempotent.
func (s *ConnectionService) Disconnect(_ context.Context, id string) error {
	return s.mgr.Close(id)
}

// Ping pings a live Connection.
func (s *ConnectionService) Ping(ctx context.Context, id string) error {
	c, err := s.mgr.Get(id)
	if err != nil {
		return err
	}
	return c.Ping(ctx)
}

// GetServerInfo returns runtime metadata (version, current user) for a live
// Connection. Returns ErrNotOpen if the connection is not active.
func (s *ConnectionService) GetServerInfo(ctx context.Context, id string) (dbdriver.ServerInfo, error) {
	c, err := s.mgr.Get(id)
	if err != nil {
		return dbdriver.ServerInfo{}, err
	}
	return c.ServerInfo(ctx)
}

// IsConnected reports whether a Connection is live.
func (s *ConnectionService) IsConnected(_ context.Context, id string) bool {
	return s.mgr.IsOpen(id)
}

// ConnectedIDs returns the set of currently-open connection IDs.
func (s *ConnectionService) ConnectedIDs(_ context.Context) []string {
	return s.mgr.OpenIDs()
}

// --- internals ---

func (s *ConnectionService) validateDraft(d ConnectionDraft, requireName bool) error {
	if requireName && d.Name == "" {
		return fmt.Errorf("ConnectionService: name is required")
	}
	if d.Driver == "" {
		return fmt.Errorf("ConnectionService: driver is required")
	}
	if _, err := registry.Get(d.Driver); err != nil {
		return err
	}
	if d.Host == "" {
		return fmt.Errorf("ConnectionService: host is required")
	}
	if d.User == "" {
		return fmt.Errorf("ConnectionService: user is required")
	}
	return nil
}

func (s *ConnectionService) draftToConfig(d ConnectionDraft) dbdriver.ConnConfig {
	cfg := dbdriver.ConnConfig{
		Host:     d.Host,
		Port:     d.Port,
		User:     d.User,
		Password: d.Password,
		Database: d.Database,
		Params:   d.Params,
		SSL:      d.SSL,
	}
	if d.SSHTunnel != nil {
		ssh := *d.SSHTunnel
		if d.SSHPassword != "" {
			ssh.Password = d.SSHPassword
		}
		if d.SSHKeyPassword != "" {
			ssh.PrivateKeyPass = d.SSHKeyPassword
		}
		cfg.SSHTunnel = &ssh
	}
	return cfg
}

func cloneSSHWithoutSecrets(in *dbdriver.SSHConfig) *dbdriver.SSHConfig {
	if in == nil {
		return nil
	}
	out := *in
	// Strip password + key passphrase before persisting to SQLite. Those go
	// in the keyring blob.
	out.Password = ""
	out.PrivateKeyPass = ""
	return &out
}

// --- host key helpers ---------------------------------------------------------

// openAndPing opens a driver connection from a draft, pings it, and closes.
// Used by TestConnection so the host-key trust flow can retry without code
// duplication.
func (s *ConnectionService) openAndPing(ctx context.Context, d ConnectionDraft) error {
	driver, err := registry.Get(d.Driver)
	if err != nil {
		return err
	}
	cfg := s.draftToConfig(d)
	tctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	conn, err := driver.Open(tctx, cfg)
	if err != nil {
		// Catch unknown host key, offer trust dialog, retry once.
		var ukErr *tunnel.ErrUnknownHostKey
		if errors.As(err, &ukErr) {
			if acceptUnknownHostKey(ukErr) {
				if addErr := tunnel.AddHostKey(ukErr.KnownHostsPath, ukErr.Info.Host, ukErr.PublicKey()); addErr != nil {
					return fmt.Errorf("ConnectionService: %w", addErr)
				}
				tctx2, cancel2 := context.WithTimeout(ctx, 20*time.Second)
				defer cancel2()
				conn, err = driver.Open(tctx2, cfg)
				if err != nil {
					return err
				}
				defer conn.Close()
				return conn.Ping(tctx2)
			}
		}
		// Show host key mismatch as a warning dialog.
		var mmErr *tunnel.ErrHostKeyMismatch
		if errors.As(err, &mmErr) {
			showHostKeyMismatch(mmErr)
		}
		return err
	}
	defer conn.Close()
	return conn.Ping(tctx)
}

// acceptUnknownHostKey shows a native confirmation dialog for the host key
// and returns true if the user chose to trust it.
func acceptUnknownHostKey(e *tunnel.ErrUnknownHostKey) bool {
	msg := fmt.Sprintf(
		"The authenticity of host '%s' can't be established.\n\n"+
			"Key type: %s\n"+
			"Fingerprint: %s\n\n"+
			"Are you sure you want to continue connecting?\n\n"+
			"(The key will be saved to known_hosts.)",
		e.Info.Host, e.Info.KeyType, e.Info.Fingerprint,
	)
	return wailsbridge.Question("SSH Host Key Verification", msg)
}

// showHostKeyMismatch warns the user that the server's host key does not
// match what is stored in known_hosts (possible MITM attack or key rotation).
func showHostKeyMismatch(e *tunnel.ErrHostKeyMismatch) {
	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"WARNING: The host key for '%s' has changed!\n\n"+
			"Server presented: %s\n"+
			"Fingerprint: %s\n\n"+
			"This could mean someone is intercepting your connection (MITM attack),\n"+
			"or the server's host key was changed legitimately.\n\n"+
			"Known keys on file:\n",
		e.Info.Host, e.Info.KeyType, e.Info.Fingerprint,
	))
	for _, kk := range e.KnownKeys {
		b.WriteString(fmt.Sprintf("  %s:%d  %s\n",
			kk.Filename, kk.Line, gossh.FingerprintSHA256(kk.Key)))
	}
	b.WriteString("\nTo trust the new key, remove the old entries from known_hosts and reconnect.")
	wailsbridge.Error("SSH Host Key Mismatch", b.String())
}
