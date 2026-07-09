// Package sqlitedrv is the SQLite driver plugin. It implements
// dbdriver.Driver on top of modernc.org/sqlite (pure Go, no CGO — the same
// library the app's own config store uses) through database/sql.
//
// SQLite is embedded: there is no host/port/user/SSL/SSH — the connection
// form is just a file path plus a few open-mode knobs. Registration is
// automatic via plugins/plugins_sqlite.go.
package sqlitedrv

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"catdb/internal/dbdriver"
	"catdb/internal/registry"
)

func init() {
	registry.Register(driver{})
}

const driverName = "sqlite"

type driver struct{}

func (driver) Name() string    { return driverName }
func (driver) Version() string { return "0.1.0" }

func (driver) Capabilities() dbdriver.Capabilities {
	return dbdriver.Capabilities{
		Schemas:          false, // main/attached databases only, no schema level
		StoredProcedures: false,
		Triggers:         true,
		Views:            true,
		Transactions:     true,
		ExplainPlan:      true,  // EXPLAIN QUERY PLAN
		DatabaseEditor:   false, // no CREATE/ALTER DATABASE — a database is a file
	}
}

func (driver) Dialect() dbdriver.Dialect { return dialect{} }

// ConnectionSchema describes the form fields the front-end renders. The
// "database" key doubles as the file path (it lands in ConnConfig.Database);
// params.* are SQLite open options.
func (driver) ConnectionSchema() []dbdriver.ConnParamField {
	return []dbdriver.ConnParamField{
		{Key: "database", Label: "Database file", Type: "text", Required: true, Group: "general",
			Help: "Path to the SQLite database file; use :memory: for an in-memory database"},
		{Key: "params.mode", Label: "Open mode", Type: "select", Default: "rwc",
			Options: []string{"rwc", "rw", "ro"}, Group: "advanced",
			Help: "rwc: read-write, create if missing; rw: read-write, must exist; ro: read-only"},
		{Key: "params.busyTimeout", Label: "Busy timeout (ms)", Type: "number", Default: "5000", Group: "advanced",
			Help: "Milliseconds to wait when another connection holds the write lock"},
		{Key: "params.foreignKeys", Label: "Enforce foreign keys", Type: "bool", Default: "true", Group: "advanced",
			Help: "Enable foreign-key constraint enforcement (PRAGMA foreign_keys)"},
	}
}

// Open builds the modernc DSN, opens the pool, and pings it through ctx.
func (driver) Open(ctx context.Context, cfg dbdriver.ConnConfig) (dbdriver.Connection, error) {
	dsn, memory, err := buildDSN(cfg)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: sql.Open: %w", err)
	}
	if memory {
		// Without a shared pool cap each pooled connection would open its own
		// private in-memory database.
		db.SetMaxOpenConns(1)
	} else {
		// SQLite allows many readers and one writer; a small pool plus
		// busy_timeout covers the desktop use case.
		db.SetMaxOpenConns(4)
		db.SetMaxIdleConns(2)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitedrv: ping: %w", err)
	}
	return &connection{db: db}, nil
}

// buildDSN renders the modernc.org/sqlite URI. Pragmas ride along as
// _pragma=name(value) query options applied to every pooled connection.
func buildDSN(cfg dbdriver.ConnConfig) (dsn string, memory bool, err error) {
	path := strings.TrimSpace(cfg.Database)
	if path == "" {
		return "", false, fmt.Errorf("sqlitedrv: database file path is required")
	}

	var opts []string
	busy := 5000
	if v := cfg.Params["busyTimeout"]; v != "" {
		if n, perr := strconv.Atoi(v); perr == nil && n >= 0 {
			busy = n
		}
	}
	opts = append(opts, fmt.Sprintf("_pragma=busy_timeout(%d)", busy))
	if fk := cfg.Params["foreignKeys"]; fk == "" || fk == "true" || fk == "1" {
		opts = append(opts, "_pragma=foreign_keys(1)")
	}

	if path == ":memory:" || cfg.Params["mode"] == "memory" {
		// cache=shared keeps a single database even if a second connection
		// slips through; the pool is still capped to 1.
		return "file::memory:?cache=shared&" + strings.Join(opts, "&"), true, nil
	}

	switch mode := cfg.Params["mode"]; mode {
	case "", "rwc":
		// modernc's default already is rwc; omit.
	case "ro", "rw":
		opts = append([]string{"mode=" + mode}, opts...)
	default:
		return "", false, fmt.Errorf("sqlitedrv: unknown open mode %q", mode)
	}

	// URI form: forward slashes, and escape the three characters that would
	// break the opaque path part.
	p := filepath.ToSlash(path)
	p = strings.NewReplacer("%", "%25", "#", "%23", "?", "%3F").Replace(p)
	return "file:" + p + "?" + strings.Join(opts, "&"), false, nil
}
