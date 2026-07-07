package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"catdb/internal/dbdriver"
)

// ErrNotFound is returned when the requested record does not exist.
var ErrNotFound = errors.New("storage: not found")

// ErrGroupNotEmpty is returned when DeleteGroup is called on a group that
// still has member connections. Callers must move/remove the members first.
var ErrGroupNotEmpty = errors.New("storage: group is not empty")

// Store is the SQLite-backed repository. Safe for concurrent use; SQLite's
// single-writer model is fine for the volume we ever expect here.
type Store struct {
	db   *sql.DB
	path string
	mu   sync.Mutex // serializes writes for SQLite
}

// Open opens (or creates) the SQLite database at path and runs schema
// migrations to the latest version. Pass an empty path to use DefaultDBPath().
func Open(path string) (*Store, error) {
	if path == "" {
		def, err := DefaultDBPath()
		if err != nil {
			return nil, err
		}
		path = def
	}
	// _pragma forces journal_mode/foreign_keys at open time.
	dsn := "file:" + path + "?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // simpler than juggling WAL contention from many goroutines
	s := &Store{db: db, path: path}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Path returns the on-disk path of the SQLite file. Useful for tests + diagnostics.
func (s *Store) Path() string { return s.path }

// Close shuts down the SQLite handle.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *Store) migrate(ctx context.Context) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS schema_version (
			version INTEGER PRIMARY KEY,
			applied_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS connection_group (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS connection (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			driver TEXT NOT NULL,
			group_id TEXT,
			host TEXT NOT NULL DEFAULT '',
			port INTEGER NOT NULL DEFAULT 0,
			user TEXT NOT NULL DEFAULT '',
			"database" TEXT NOT NULL DEFAULT '',
			params_json TEXT NOT NULL DEFAULT '',
			ssl_json TEXT NOT NULL DEFAULT '',
			ssh_json TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (group_id) REFERENCES connection_group(id) ON DELETE SET NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_connection_group ON connection(group_id)`,
		`CREATE INDEX IF NOT EXISTS idx_connection_driver ON connection(driver)`,
		`CREATE TABLE IF NOT EXISTS app_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS saved_query (
			id TEXT PRIMARY KEY,
			conn_id TEXT NOT NULL,
			db_name TEXT NOT NULL DEFAULT '',
			schema_name TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL,
			sql_text TEXT NOT NULL,
			sort_order INTEGER NOT NULL DEFAULT 0,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (conn_id) REFERENCES connection(id) ON DELETE CASCADE
		)`,
		`CREATE INDEX IF NOT EXISTS idx_saved_query_scope ON saved_query(conn_id, db_name)`,
	}
	for _, stmt := range stmts {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("storage: migrate: %w", err)
		}
	}
	// Additive column for databases created before schema_name existed.
	// SQLite has no ADD COLUMN IF NOT EXISTS — probe the schema instead.
	var hasSchemaCol int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM pragma_table_info('saved_query') WHERE name='schema_name'`,
	).Scan(&hasSchemaCol); err != nil {
		return fmt.Errorf("storage: migrate: %w", err)
	}
	if hasSchemaCol == 0 {
		if _, err := s.db.ExecContext(ctx,
			`ALTER TABLE saved_query ADD COLUMN schema_name TEXT NOT NULL DEFAULT ''`,
		); err != nil {
			return fmt.Errorf("storage: migrate: %w", err)
		}
	}
	_, _ = s.db.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_version(version, applied_at) VALUES (2, ?)`,
		time.Now().Unix(),
	)
	return nil
}

// --- groups ---

// ListGroups returns all groups sorted by SortOrder then Name.
func (s *Store) ListGroups(ctx context.Context) ([]Group, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, sort_order, created_at FROM connection_group ORDER BY sort_order, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Group
	for rows.Next() {
		var g Group
		var created int64
		if err := rows.Scan(&g.ID, &g.Name, &g.SortOrder, &created); err != nil {
			return nil, err
		}
		g.CreatedAt = time.Unix(created, 0)
		out = append(out, g)
	}
	return out, rows.Err()
}

// SaveGroup inserts or updates a group. If g.ID is empty a fresh UUID is
// assigned and written back into the returned Group.
func (s *Store) SaveGroup(ctx context.Context, g Group) (Group, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if g.Name == "" {
		return Group{}, fmt.Errorf("storage: group name is required")
	}
	if g.ID == "" {
		g.ID = uuid.NewString()
		g.CreatedAt = time.Now()
		_, err := s.db.ExecContext(ctx,
			`INSERT INTO connection_group(id, name, sort_order, created_at) VALUES (?,?,?,?)`,
			g.ID, g.Name, g.SortOrder, g.CreatedAt.Unix())
		if err != nil {
			return Group{}, err
		}
		return g, nil
	}
	res, err := s.db.ExecContext(ctx,
		`UPDATE connection_group SET name=?, sort_order=? WHERE id=?`,
		g.Name, g.SortOrder, g.ID)
	if err != nil {
		return Group{}, err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return Group{}, ErrNotFound
	}
	return g, nil
}

// DeleteGroup removes the group. Refuses with ErrGroupNotEmpty if any
// connection still references it — the sidebar surfaces the 删除 entry only
// for empty groups, and this is the backstop that enforces the same rule
// against direct API misuse. (The schema's ON DELETE SET NULL stays in
// place for resilience against ad-hoc admin actions.)
func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var count int
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM connection WHERE group_id = ?`, id).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return ErrGroupNotEmpty
	}
	res, err := s.db.ExecContext(ctx, `DELETE FROM connection_group WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MoveConnection reassigns a connection's group. An empty groupID detaches
// the connection (group_id NULL → renders under 未分组 in the sidebar).
// Touches only group_id + updated_at — secrets and other fields are left
// alone, so drag-and-drop never round-trips the password.
func (s *Store) MoveConnection(ctx context.Context, id, groupID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx,
		`UPDATE connection SET group_id=?, updated_at=? WHERE id=?`,
		nilIfEmpty(groupID), time.Now().Unix(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- connections ---

// ListConnections returns every saved profile, ordered by name.
func (s *Store) ListConnections(ctx context.Context) ([]ConnectionProfile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, driver, group_id, host, port, user, "database",
		       params_json, ssl_json, ssh_json, created_at, updated_at
		FROM connection ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ConnectionProfile
	for rows.Next() {
		p, err := scanConnection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetConnection returns one profile by ID.
func (s *Store) GetConnection(ctx context.Context, id string) (ConnectionProfile, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, driver, group_id, host, port, user, "database",
		       params_json, ssl_json, ssh_json, created_at, updated_at
		FROM connection WHERE id=?`, id)
	p, err := scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return ConnectionProfile{}, ErrNotFound
	}
	return p, err
}

// SaveConnection inserts or updates a profile. If p.ID is empty a fresh UUID
// is assigned; CreatedAt/UpdatedAt are managed here.
func (s *Store) SaveConnection(ctx context.Context, p ConnectionProfile) (ConnectionProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p.Name == "" {
		return ConnectionProfile{}, fmt.Errorf("storage: connection name is required")
	}
	if p.Driver == "" {
		return ConnectionProfile{}, fmt.Errorf("storage: driver is required")
	}
	paramsJSON, _ := json.Marshal(p.Params)
	sslJSON := jsonOrEmpty(p.SSL)
	sshJSON := jsonOrEmpty(p.SSHTunnel)
	now := time.Now()

	if p.ID == "" {
		p.ID = uuid.NewString()
		p.CreatedAt = now
		p.UpdatedAt = now
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO connection(id, name, driver, group_id, host, port, user, "database",
			                       params_json, ssl_json, ssh_json, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			p.ID, p.Name, p.Driver, nilIfEmpty(p.GroupID),
			p.Host, p.Port, p.User, p.Database,
			string(paramsJSON), sslJSON, sshJSON,
			p.CreatedAt.Unix(), p.UpdatedAt.Unix())
		if err != nil {
			return ConnectionProfile{}, err
		}
		return p, nil
	}

	p.UpdatedAt = now
	res, err := s.db.ExecContext(ctx, `
		UPDATE connection SET name=?, driver=?, group_id=?, host=?, port=?, user=?, "database"=?,
		                     params_json=?, ssl_json=?, ssh_json=?, updated_at=?
		WHERE id=?`,
		p.Name, p.Driver, nilIfEmpty(p.GroupID),
		p.Host, p.Port, p.User, p.Database,
		string(paramsJSON), sslJSON, sshJSON, p.UpdatedAt.Unix(),
		p.ID)
	if err != nil {
		return ConnectionProfile{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ConnectionProfile{}, ErrNotFound
	}
	return p, nil
}

// --- settings (key/value blobs, e.g. skipped update version) ---

// GetSetting returns the stored value for key, or "" if not set.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	row := s.db.QueryRowContext(ctx, `SELECT value FROM app_settings WHERE key=?`, key)
	var v string
	if err := row.Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// SetSetting upserts the value for key.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO app_settings(key, value) VALUES(?, ?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		key, value)
	return err
}

// DeleteConnection removes a profile by ID.
func (s *Store) DeleteConnection(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM connection WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- saved queries ---

// ListSavedQueries returns the saved queries scoped to a connection +
// database + schema ("" for schema-less databases), ordered by sort_order
// then name.
func (s *Store) ListSavedQueries(ctx context.Context, connID, db, schema string) ([]SavedQuery, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, conn_id, db_name, schema_name, name, sql_text, sort_order, created_at, updated_at
		FROM saved_query WHERE conn_id=? AND db_name=? AND schema_name=? ORDER BY sort_order, name`,
		connID, db, schema)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SavedQuery
	for rows.Next() {
		var q SavedQuery
		var created, updated int64
		if err := rows.Scan(&q.ID, &q.ConnID, &q.DBName, &q.SchemaName, &q.Name, &q.SQLText, &q.SortOrder, &created, &updated); err != nil {
			return nil, err
		}
		q.CreatedAt = time.Unix(created, 0)
		q.UpdatedAt = time.Unix(updated, 0)
		out = append(out, q)
	}
	return out, rows.Err()
}

// SaveSavedQuery inserts or updates a saved query. If q.ID is empty a fresh
// UUID is assigned; CreatedAt/UpdatedAt are managed here. On update only
// name/sql_text/sort_order/updated_at change — conn_id/db_name/schema_name
// are immutable.
func (s *Store) SaveSavedQuery(ctx context.Context, q SavedQuery) (SavedQuery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if q.Name == "" {
		return SavedQuery{}, fmt.Errorf("storage: saved query name is required")
	}
	if q.ConnID == "" {
		return SavedQuery{}, fmt.Errorf("storage: saved query connId is required")
	}
	now := time.Now()
	if q.ID == "" {
		q.ID = uuid.NewString()
		q.CreatedAt = now
		q.UpdatedAt = now
		_, err := s.db.ExecContext(ctx, `
			INSERT INTO saved_query(id, conn_id, db_name, schema_name, name, sql_text, sort_order, created_at, updated_at)
			VALUES (?,?,?,?,?,?,?,?,?)`,
			q.ID, q.ConnID, q.DBName, q.SchemaName, q.Name, q.SQLText, q.SortOrder,
			q.CreatedAt.Unix(), q.UpdatedAt.Unix())
		if err != nil {
			return SavedQuery{}, err
		}
		return q, nil
	}
	q.UpdatedAt = now
	res, err := s.db.ExecContext(ctx, `
		UPDATE saved_query SET name=?, sql_text=?, sort_order=?, updated_at=? WHERE id=?`,
		q.Name, q.SQLText, q.SortOrder, q.UpdatedAt.Unix(), q.ID)
	if err != nil {
		return SavedQuery{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return SavedQuery{}, ErrNotFound
	}
	return q, nil
}

// DeleteSavedQuery removes a saved query by ID.
func (s *Store) DeleteSavedQuery(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.db.ExecContext(ctx, `DELETE FROM saved_query WHERE id=?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanConnection(r rowScanner) (ConnectionProfile, error) {
	var (
		p          ConnectionProfile
		groupID    sql.NullString
		paramsJSON string
		sslJSON    string
		sshJSON    string
		created    int64
		updated    int64
	)
	if err := r.Scan(&p.ID, &p.Name, &p.Driver, &groupID, &p.Host, &p.Port, &p.User, &p.Database,
		&paramsJSON, &sslJSON, &sshJSON, &created, &updated); err != nil {
		return ConnectionProfile{}, err
	}
	p.GroupID = groupID.String
	p.CreatedAt = time.Unix(created, 0)
	p.UpdatedAt = time.Unix(updated, 0)
	if paramsJSON != "" {
		_ = json.Unmarshal([]byte(paramsJSON), &p.Params)
	}
	if sslJSON != "" {
		p.SSL = &dbdriver.SSLConfig{}
		if err := json.Unmarshal([]byte(sslJSON), p.SSL); err != nil {
			return ConnectionProfile{}, err
		}
	}
	if sshJSON != "" {
		p.SSHTunnel = &dbdriver.SSHConfig{}
		if err := json.Unmarshal([]byte(sshJSON), p.SSHTunnel); err != nil {
			return ConnectionProfile{}, err
		}
	}
	return p, nil
}

func jsonOrEmpty(v any) string {
	if v == nil {
		return ""
	}
	b, err := json.Marshal(v)
	if err != nil || string(b) == "null" {
		return ""
	}
	return string(b)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
