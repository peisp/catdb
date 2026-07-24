package storage

import (
	"time"

	"catdb/internal/dbdriver"
)

// ConnectionProfile is the persisted, non-secret half of a connection. The
// password lives in the keyring under the same ID.
//
// Use time.Time RFC3339 in JSON; the SQLite columns are stored as INTEGER
// unix-epoch seconds for trivial portability.
type ConnectionProfile struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Driver  string `json:"driver"`
	GroupID string `json:"groupId,omitempty"`
	// Environment is the deployment-tier label (dev/test/staging/prod, "" =
	// unmarked). Non-secret; it is the data basis for the AI Agent safety
	// model's environment gate — prod connections are hard read-only for the
	// Agent (docs/AGENT_DESIGN.md §5 gate 1).
	Environment string              `json:"environment"`
	Host        string              `json:"host"`
	Port        int                 `json:"port"`
	User        string              `json:"user"`
	Database    string              `json:"database,omitempty"`
	Params      map[string]string   `json:"params,omitempty"`
	SSL         *dbdriver.SSLConfig `json:"ssl,omitempty"`
	SSHTunnel   *dbdriver.SSHConfig `json:"sshTunnel,omitempty"`
	CreatedAt   time.Time           `json:"createdAt"`
	UpdatedAt   time.Time           `json:"updatedAt"`
}

// Group is a logical folder for connections in the sidebar tree.
type Group struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	SortOrder int       `json:"sortOrder"`
	CreatedAt time.Time `json:"createdAt"`
}

// SavedQuery is a named SQL snippet shown under the object tree's 「查询」
// group. Scoped by (ConnID, DBName, SchemaName) — SchemaName is "" for
// drivers without a schema level (MySQL). SQL text holds no secrets so it
// lives in SQLite alongside the connection profile.
type SavedQuery struct {
	ID         string    `json:"id"`
	ConnID     string    `json:"connId"`
	DBName     string    `json:"dbName"`
	SchemaName string    `json:"schemaName"`
	Name       string    `json:"name"`
	SQLText    string    `json:"sqlText"`
	SortOrder  int       `json:"sortOrder"`
	CreatedAt  time.Time `json:"createdAt"`
	UpdatedAt  time.Time `json:"updatedAt"`
}

// ToDBDriverConfig converts a stored profile + plaintext password into the
// generic ConnConfig the driver layer expects. Passwords inside SSHTunnel
// (SSH password / private-key passphrase) are kept as-is — those round-trip
// through the keyring as part of the same blob (see secrets.go).
func (p ConnectionProfile) ToDBDriverConfig(password string) dbdriver.ConnConfig {
	cfg := dbdriver.ConnConfig{
		Host:     p.Host,
		Port:     p.Port,
		User:     p.User,
		Password: password,
		Database: p.Database,
		Params:   p.Params,
	}
	if p.SSL != nil {
		ssl := *p.SSL
		cfg.SSL = &ssl
	}
	if p.SSHTunnel != nil {
		ssh := *p.SSHTunnel
		cfg.SSHTunnel = &ssh
	}
	return cfg
}
