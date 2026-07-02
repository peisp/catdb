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
	Name    string   `json:"name"`
	Columns []string `json:"columns"`
}

// AutocompleteFor returns the table+column map for one database. Capped to a
// generous-but-finite number of tables so the IPC payload is bounded; tables
// past the cap fall back to "table name only, no columns" — better than
// nothing for completion.
func (s *MetadataService) AutocompleteFor(ctx context.Context, connID, db, schema string) (AutocompleteSnapshot, error) {
	const maxColumnFetch = 500
	var snap AutocompleteSnapshot
	snap.Database = db

	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return snap, err
	}
	tables, err := m.ListTables(ctx, db, schema)
	if err != nil {
		return snap, err
	}
	for i, t := range tables {
		entry := AutocompleteTable{Name: t.Name}
		if i < maxColumnFetch {
			cols, err := m.ListColumns(ctx, db, schema, t.Name)
			if err == nil {
				entry.Columns = make([]string, len(cols))
				for j, c := range cols {
					entry.Columns[j] = c.Name
				}
			}
		}
		snap.Tables = append(snap.Tables, entry)
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
	if ed := conn.Editor(); ed != nil {
		if pk, perr := ed.PrimaryKeys(ctx, db, schema, table); perr == nil {
			out.PrimaryKey = pk
			out.HasUniqueKey = len(pk) > 0
		}
	}
	return out, nil
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
