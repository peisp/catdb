// Package session owns the lifecycle of LIVE database connections.
//
// Persistence is somebody else's problem (storage/). This package only deals
// with already-saved profiles that the user wants to open right now.
//
// Multi-window concurrency note (ARCHITECTURE.md §6.3): right now Manager
// returns one shared Connection per connID. The per-window transaction
// isolation lives on top of this — Begin() will lease out a dedicated
// transactional connection — but lands with the query layer in M2/M3.
package session

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"catdb/internal/dbdriver"
	"catdb/internal/registry"
	"catdb/internal/storage"
)

// ErrNotOpen is returned by Get when the connection has not been opened.
var ErrNotOpen = errors.New("session: connection not open")

// Manager owns the set of currently-open connections.
type Manager struct {
	store   *storage.Store
	secrets *storage.Secrets

	mu     sync.Mutex
	active map[string]dbdriver.Connection
}

// NewManager wires up the dependencies. store + secrets are required —
// callers should usually pass storage.Open(...) and storage.NewSecrets("catdb").
func NewManager(store *storage.Store, secrets *storage.Secrets) *Manager {
	return &Manager{
		store:   store,
		secrets: secrets,
		active:  make(map[string]dbdriver.Connection),
	}
}

// Open materializes a Connection for the given profile ID. If one is already
// open for that ID, the cached instance is returned unchanged.
//
// Errors from storage (profile missing) or the driver (network / auth /
// host-key) bubble up unchanged so the UI can show a useful message.
func (m *Manager) Open(ctx context.Context, connID string) (dbdriver.Connection, error) {
	m.mu.Lock()
	if c, ok := m.active[connID]; ok {
		m.mu.Unlock()
		return c, nil
	}
	m.mu.Unlock()

	prof, err := m.store.GetConnection(ctx, connID)
	if err != nil {
		return nil, err
	}
	d, err := registry.Get(prof.Driver)
	if err != nil {
		return nil, err
	}

	secret, err := m.secrets.Load(connID)
	if err != nil && !errors.Is(err, storage.ErrSecretNotFound) {
		return nil, err
	}

	cfg := prof.ToDBDriverConfig(secret.Password)
	if cfg.SSHTunnel != nil {
		if secret.SSHPassword != "" {
			cfg.SSHTunnel.Password = secret.SSHPassword
		}
		if secret.SSHKeyPassword != "" {
			cfg.SSHTunnel.PrivateKeyPass = secret.SSHKeyPassword
		}
	}

	conn, err := d.Open(ctx, cfg)
	if err != nil {
		return nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Race: another caller may have opened concurrently. Close ours and reuse.
	if existing, ok := m.active[connID]; ok {
		_ = conn.Close()
		return existing, nil
	}
	m.active[connID] = conn
	return conn, nil
}

// OpenDedicated opens a NEW physical connection for connID, bypassing the
// shared cache. The caller owns the returned Connection and must Close it.
// Long-running exclusive work (data sync's write transactions, bulk loads)
// uses this so it never blocks the shared connection other windows are on
// (CLAUDE.md rule 9).
func (m *Manager) OpenDedicated(ctx context.Context, connID string) (dbdriver.Connection, error) {
	prof, err := m.store.GetConnection(ctx, connID)
	if err != nil {
		return nil, err
	}
	d, err := registry.Get(prof.Driver)
	if err != nil {
		return nil, err
	}
	secret, err := m.secrets.Load(connID)
	if err != nil && !errors.Is(err, storage.ErrSecretNotFound) {
		return nil, err
	}
	cfg := prof.ToDBDriverConfig(secret.Password)
	if cfg.SSHTunnel != nil {
		if secret.SSHPassword != "" {
			cfg.SSHTunnel.Password = secret.SSHPassword
		}
		if secret.SSHKeyPassword != "" {
			cfg.SSHTunnel.PrivateKeyPass = secret.SSHKeyPassword
		}
	}
	return d.Open(ctx, cfg)
}

// Get returns an already-open connection without touching storage or the
// driver. Returns ErrNotOpen if Open has not been called for the ID.
func (m *Manager) Get(connID string) (dbdriver.Connection, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if c, ok := m.active[connID]; ok {
		return c, nil
	}
	return nil, ErrNotOpen
}

// IsOpen reports whether a connection is live.
func (m *Manager) IsOpen(connID string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.active[connID]
	return ok
}

// Close releases the live connection for connID. Idempotent.
func (m *Manager) Close(connID string) error {
	m.mu.Lock()
	c, ok := m.active[connID]
	if ok {
		delete(m.active, connID)
	}
	m.mu.Unlock()
	if !ok {
		return nil
	}
	if err := c.Close(); err != nil {
		return fmt.Errorf("session: close %s: %w", connID, err)
	}
	return nil
}

// CloseAll shuts every live connection. Called from app shutdown.
// First error is returned but all connections are attempted.
func (m *Manager) CloseAll() error {
	m.mu.Lock()
	conns := m.active
	m.active = make(map[string]dbdriver.Connection)
	m.mu.Unlock()

	var firstErr error
	for id, c := range conns {
		if err := c.Close(); err != nil && firstErr == nil {
			firstErr = fmt.Errorf("session: close %s: %w", id, err)
		}
	}
	return firstErr
}

// OpenIDs returns the set of currently-open connection IDs (snapshot).
func (m *Manager) OpenIDs() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.active))
	for id := range m.active {
		out = append(out, id)
	}
	return out
}

// DriverName returns the driver this connection was saved under. Used by
// services that need the Dialect (paginate, identifier-quoting) but only
// have a connID in hand.
func (m *Manager) DriverName(ctx context.Context, connID string) (string, error) {
	prof, err := m.store.GetConnection(ctx, connID)
	if err != nil {
		return "", err
	}
	return prof.Driver, nil
}
