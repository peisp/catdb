package services

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"catdb/internal/core/session"
	"catdb/internal/dbdriver"
	"catdb/internal/registry"
)

// MetadataService is the Wails Service that drives the object tree, the
// structure viewer, and autocomplete. It stays THIN: validates input and
// forwards to the driver's Metadata.
type MetadataService struct {
	mgr *session.Manager
}

// NewMetadataService wires the session manager dependency.
func NewMetadataService(mgr *session.Manager) *MetadataService {
	return &MetadataService{mgr: mgr}
}

func (s *MetadataService) ServiceName() string { return "MetadataService" }

// resolveMeta makes sure the connection is open and gives back its Metadata.
// All Service methods funnel through here so the error story stays one place.
func (s *MetadataService) resolveMeta(ctx context.Context, connID string) (dbdriver.Metadata, error) {
	if connID == "" {
		return nil, fmt.Errorf("MetadataService: connID is required")
	}
	conn, err := s.mgr.Get(connID)
	if err != nil {
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return nil, err
		}
	}
	m := conn.Metadata()
	if m == nil {
		return nil, fmt.Errorf("MetadataService: connection has no metadata adapter")
	}
	return m, nil
}

func (s *MetadataService) resolveDialect(ctx context.Context, connID string) (dbdriver.Dialect, error) {
	name, err := s.mgr.DriverName(ctx, connID)
	if err != nil {
		return nil, err
	}
	d, err := registry.Get(name)
	if err != nil {
		return nil, err
	}
	return d.Dialect(), nil
}

func (s *MetadataService) ListDatabases(ctx context.Context, connID string) ([]string, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListDatabases(ctx)
}

// ListSchemas returns the schemas under db. Empty for databases without a
// schema level (Capabilities.Schemas == false, e.g. MySQL).
func (s *MetadataService) ListSchemas(ctx context.Context, connID, db string) ([]string, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListSchemas(ctx, db)
}

func (s *MetadataService) ListTables(ctx context.Context, connID, db, schema string) ([]dbdriver.TableInfo, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListTables(ctx, db, schema)
}

func (s *MetadataService) ListViews(ctx context.Context, connID, db, schema string) ([]dbdriver.ViewInfo, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListViews(ctx, db, schema)
}

func (s *MetadataService) ListColumns(ctx context.Context, connID, db, schema, table string) ([]dbdriver.ColumnMeta, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListColumns(ctx, db, schema, table)
}

func (s *MetadataService) ListIndexes(ctx context.Context, connID, db, schema, table string) ([]dbdriver.IndexInfo, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListIndexes(ctx, db, schema, table)
}

func (s *MetadataService) ListForeignKeys(ctx context.Context, connID, db, schema, table string) ([]dbdriver.ForeignKeyInfo, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListForeignKeys(ctx, db, schema, table)
}

func (s *MetadataService) ListRoutines(ctx context.Context, connID, db, schema string) ([]dbdriver.RoutineInfo, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListRoutines(ctx, db, schema)
}

func (s *MetadataService) GetCreateTable(ctx context.Context, connID, db, schema, table string) (string, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return "", err
	}
	return m.GetCreateTable(ctx, db, schema, table)
}

// TableSummary bundles columns/indexes/FKs into one round-trip — handy for
// the structure viewer so it can render the whole panel without sequencing
// three calls from the front-end.
type TableSummary struct {
	Columns     []dbdriver.ColumnMeta     `json:"columns"`
	Indexes     []dbdriver.IndexInfo      `json:"indexes"`
	ForeignKeys []dbdriver.ForeignKeyInfo `json:"foreignKeys"`
}

func (s *MetadataService) GetTableSummary(ctx context.Context, connID, db, schema, table string) (TableSummary, error) {
	var empty TableSummary
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return empty, err
	}
	cols, err := m.ListColumns(ctx, db, schema, table)
	if err != nil {
		return empty, err
	}
	ix, err := m.ListIndexes(ctx, db, schema, table)
	if err != nil {
		return empty, err
	}
	fk, err := m.ListForeignKeys(ctx, db, schema, table)
	if err != nil {
		return empty, err
	}
	return TableSummary{Columns: cols, Indexes: ix, ForeignKeys: fk}, nil
}

// AutocompleteSnapshot is the cache the front-end hands to CodeMirror's
// schemaCompletionSource. We ship it as one big map per refresh — small in
// practice and cleaner than incremental updates.
type AutocompleteSnapshot struct {
	Database string              `json:"database"`
	Tables   []AutocompleteTable `json:"tables"`
}

type AutocompleteTable struct {
	Name string `json:"name"`
	// Kind is "table" or "view" — lets the editor render a distinct icon and
	// treat views as query-able relations in completion.
	Kind    string               `json:"kind"`
	Columns []AutocompleteColumn `json:"columns"`
}

// AutocompleteColumn carries the per-column detail the editor shows in the
// completion popup: native type (e.g. "VARCHAR(255)"), primary-key membership,
// NOT NULL, and the column comment.
type AutocompleteColumn struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	PK      bool   `json:"pk,omitempty"`
	NotNull bool   `json:"notNull,omitempty"`
	Comment string `json:"comment,omitempty"`
}

// AutocompleteFor returns the table+column map for one database (tables first,
// then views). Column fetching is capped to a generous-but-finite number of
// relations so the IPC payload is bounded; relations past the cap fall back to
// "name only, no columns" — better than nothing for completion.
func (s *MetadataService) AutocompleteFor(ctx context.Context, connID, db, schema string) (AutocompleteSnapshot, error) {
	const maxColumnFetch = 500
	var snap AutocompleteSnapshot
	snap.Database = db

	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return snap, err
	}

	fetched := 0
	addRelation := func(name, kind string) {
		entry := AutocompleteTable{Name: name, Kind: kind}
		if fetched < maxColumnFetch {
			if cols, err := m.ListColumns(ctx, db, schema, name); err == nil {
				entry.Columns = make([]AutocompleteColumn, len(cols))
				for j, c := range cols {
					entry.Columns[j] = AutocompleteColumn{
						Name:    c.Name,
						Type:    c.NativeType,
						PK:      c.IsPrimaryKey,
						NotNull: !c.Nullable,
						Comment: c.Comment,
					}
				}
			}
			fetched++
		}
		snap.Tables = append(snap.Tables, entry)
	}

	tables, err := m.ListTables(ctx, db, schema)
	if err != nil {
		return snap, err
	}
	for _, t := range tables {
		addRelation(t.Name, "table")
	}
	// Views are query-able like tables; include them so `SELECT … FROM <view>`
	// completes. Best-effort — a driver without views just returns nothing.
	if views, err := m.ListViews(ctx, db, schema); err == nil {
		for _, v := range views {
			addRelation(v.Name, "view")
		}
	}
	return snap, nil
}

// BrowseResult is the one-shot pageful BrowseTable returns.
type BrowseResult struct {
	Columns      []dbdriver.ColumnMeta `json:"columns"`
	Rows         [][]any               `json:"rows"`
	PrimaryKey   []string              `json:"primaryKey"`
	HasUniqueKey bool                  `json:"hasUniqueKey"`
	// SQL is the dialect-paginated statement that actually ran. Surfaced to
	// the UI so users can see/copy what catdb executed on their behalf.
	SQL string `json:"sql"`
}

// BrowseTable runs `SELECT * FROM db.table [WHERE …] [ORDER BY …] LIMIT … OFFSET …`
// and returns the rows + columns + primary-key info needed by the data browser.
//
// Pass orderBy to request an ORDER BY clause (the column name is quoted via the
// active Dialect). orderDir defaults to "ASC" when empty; valid values are
// "ASC" and "DESC" (case-insensitive).
// whereClause and orderByClause are raw SQL snippets injected directly after
// WHERE and ORDER BY respectively — supplied by the FilterBar component.
// When orderByClause is non-empty it takes precedence over the simple
// orderBy/orderDir pair.
// Pass limit < 0 to fetch all rows (no LIMIT/OFFSET clause). limit == 0 is
// reserved as "use default" and resolves to 200.
func (s *MetadataService) BrowseTable(ctx context.Context, connID, db, schema, table, orderBy, orderDir string, limit, offset int, whereClause, orderByClause string) (BrowseResult, error) {
	var empty BrowseResult
	if connID == "" || table == "" || (db == "" && schema == "") {
		return empty, fmt.Errorf("MetadataService: connID, table and db (or schema) are required")
	}
	unlimited := limit < 0
	if limit == 0 {
		limit = 200
	}
	if offset < 0 {
		offset = 0
	}
	conn, err := s.mgr.Get(connID)
	if err != nil {
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return empty, err
		}
	}
	dia, err := s.resolveDialect(ctx, connID)
	if err != nil {
		return empty, err
	}
	q := conn.Querier()
	if q == nil {
		return empty, fmt.Errorf("MetadataService: connection has no querier")
	}
	base := fmt.Sprintf("SELECT * FROM %s", dbdriver.QualifyTable(dia, db, schema, table))

	if whereClause != "" {
		base = fmt.Sprintf("%s WHERE %s", base, whereClause)
	}

	if orderByClause != "" {
		base = fmt.Sprintf("%s ORDER BY %s", base, orderByClause)
	} else if orderBy != "" {
		dir := strings.ToUpper(orderDir)
		if dir != "DESC" {
			dir = "ASC"
		}
		base = fmt.Sprintf("%s ORDER BY %s %s", base, dia.QuoteIdentifier(orderBy), dir)
	}
	var paginated string
	if unlimited {
		paginated = base
	} else {
		paginated = dia.Paginate(base, limit, offset)
	}
	rs, err := q.Query(ctx, paginated)
	if err != nil {
		return empty, err
	}
	defer rs.Close()
	// Cap the in-memory fetch even when "all rows" is requested — a runaway
	// table would otherwise blow up the renderer.
	fetchN := limit
	if unlimited {
		fetchN = 1000000
	}
	rows, _, err := rs.Next(fetchN)
	if err != nil {
		return empty, err
	}
	out := BrowseResult{Columns: rs.Columns(), Rows: rows, SQL: paginated}

	// Enrich column metadata with full COLUMN_TYPE from information_schema
	// when available (MySQL). The scanner only gives bare DatabaseTypeName()
	// which lacks precision/parameters.
	if len(out.Columns) > 0 {
		if cols, err := enrichColumnTypes(ctx, q, db, schema, table, out.Columns); err == nil {
			out.Columns = cols
		}
	}

	if ed := conn.Editor(); ed != nil {
		if pk, perr := ed.PrimaryKeys(ctx, db, schema, table); perr == nil {
			out.PrimaryKey = pk
			out.HasUniqueKey = len(pk) > 0
		}
	}
	return out, nil
}

// enrichColumnTypes queries information_schema.COLUMNS for full COLUMN_TYPE
// strings and merges them into the ColumnMeta slice. This gives table browsing
// proper type precision (e.g. "varchar(255)" instead of "VARCHAR").
//
// ponytail: MySQL-specific query. Non-MySQL databases fail gracefully and
// return the original columns unchanged.
func enrichColumnTypes(ctx context.Context, q dbdriver.Querier, db, schema, table string, cols []dbdriver.ColumnMeta) ([]dbdriver.ColumnMeta, error) {
	schemaName := schema
	if schemaName == "" {
		schemaName = db
	}
	if schemaName == "" {
		return cols, nil
	}
	rs, err := q.Query(ctx,
		"SELECT COLUMN_NAME, COLUMN_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = ? AND TABLE_NAME = ?",
		schemaName, table)
	if err != nil {
		return cols, nil // non-MySQL DB, skip enrichment
	}
	defer rs.Close()
	typeMap := make(map[string]string, len(cols))
	for {
		batch, done, err := rs.Next(200)
		if err != nil {
			return cols, nil
		}
		for _, row := range batch {
			if len(row) >= 2 {
				if name, ok := row[0].(string); ok {
					if ct, ok := row[1].(string); ok {
						typeMap[strings.ToLower(name)] = ct
					}
				}
			}
		}
		if done {
			break
		}
	}
	changed := false
	for i := range cols {
		if ct, ok := typeMap[strings.ToLower(cols[i].Name)]; ok {
			cols[i].NativeType = ct
			changed = true
		}
	}
	if !changed {
		return cols, nil
	}
	return cols, nil
}

// CountTableRows runs `SELECT COUNT(*) FROM db.table [WHERE …]` so the data
// browser can show the exact total on demand (it's a potentially slow scan,
// hence user-triggered rather than automatic). whereClause is the same raw
// FilterBar snippet BrowseTable accepts.
func (s *MetadataService) CountTableRows(ctx context.Context, connID, db, schema, table, whereClause string) (int64, error) {
	if connID == "" || table == "" || (db == "" && schema == "") {
		return 0, fmt.Errorf("MetadataService: connID, table and db (or schema) are required")
	}
	conn, err := s.mgr.Get(connID)
	if err != nil {
		conn, err = s.mgr.Open(ctx, connID)
		if err != nil {
			return 0, err
		}
	}
	dia, err := s.resolveDialect(ctx, connID)
	if err != nil {
		return 0, err
	}
	q := conn.Querier()
	if q == nil {
		return 0, fmt.Errorf("MetadataService: connection has no querier")
	}
	stmt := fmt.Sprintf("SELECT COUNT(*) FROM %s", dbdriver.QualifyTable(dia, db, schema, table))
	if whereClause != "" {
		stmt = fmt.Sprintf("%s WHERE %s", stmt, whereClause)
	}
	rs, err := q.Query(ctx, stmt)
	if err != nil {
		return 0, err
	}
	defer rs.Close()
	rows, _, err := rs.Next(1)
	if err != nil {
		return 0, err
	}
	if len(rows) == 0 || len(rows[0]) == 0 {
		return 0, fmt.Errorf("MetadataService: count returned no rows")
	}
	switch v := rows[0][0].(type) {
	case int64:
		return v, nil
	case uint64:
		return int64(v), nil
	case []byte:
		n, perr := strconv.ParseInt(string(v), 10, 64)
		if perr != nil {
			return 0, fmt.Errorf("MetadataService: parse count: %w", perr)
		}
		return n, nil
	case string:
		n, perr := strconv.ParseInt(v, 10, 64)
		if perr != nil {
			return 0, fmt.Errorf("MetadataService: parse count: %w", perr)
		}
		return n, nil
	default:
		return 0, fmt.Errorf("MetadataService: unexpected count type %T", v)
	}
}
