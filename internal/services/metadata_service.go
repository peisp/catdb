package services

import (
	"context"
	"fmt"

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

func (s *MetadataService) ListTables(ctx context.Context, connID, db string) ([]dbdriver.TableInfo, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListTables(ctx, db, "")
}

func (s *MetadataService) ListViews(ctx context.Context, connID, db string) ([]dbdriver.ViewInfo, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListViews(ctx, db, "")
}

func (s *MetadataService) ListColumns(ctx context.Context, connID, db, table string) ([]dbdriver.ColumnMeta, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListColumns(ctx, db, "", table)
}

func (s *MetadataService) ListIndexes(ctx context.Context, connID, db, table string) ([]dbdriver.IndexInfo, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListIndexes(ctx, db, "", table)
}

func (s *MetadataService) ListForeignKeys(ctx context.Context, connID, db, table string) ([]dbdriver.ForeignKeyInfo, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListForeignKeys(ctx, db, "", table)
}

func (s *MetadataService) ListRoutines(ctx context.Context, connID, db string) ([]dbdriver.RoutineInfo, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return nil, err
	}
	return m.ListRoutines(ctx, db, "")
}

func (s *MetadataService) GetCreateTable(ctx context.Context, connID, db, table string) (string, error) {
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return "", err
	}
	return m.GetCreateTable(ctx, db, "", table)
}

// TableSummary bundles columns/indexes/FKs into one round-trip — handy for
// the structure viewer so it can render the whole panel without sequencing
// three calls from the front-end.
type TableSummary struct {
	Columns     []dbdriver.ColumnMeta     `json:"columns"`
	Indexes     []dbdriver.IndexInfo      `json:"indexes"`
	ForeignKeys []dbdriver.ForeignKeyInfo `json:"foreignKeys"`
}

func (s *MetadataService) GetTableSummary(ctx context.Context, connID, db, table string) (TableSummary, error) {
	var empty TableSummary
	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return empty, err
	}
	cols, err := m.ListColumns(ctx, db, "", table)
	if err != nil {
		return empty, err
	}
	ix, err := m.ListIndexes(ctx, db, "", table)
	if err != nil {
		return empty, err
	}
	fk, err := m.ListForeignKeys(ctx, db, "", table)
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
func (s *MetadataService) AutocompleteFor(ctx context.Context, connID, db string) (AutocompleteSnapshot, error) {
	const maxColumnFetch = 500
	var snap AutocompleteSnapshot
	snap.Database = db

	m, err := s.resolveMeta(ctx, connID)
	if err != nil {
		return snap, err
	}
	tables, err := m.ListTables(ctx, db, "")
	if err != nil {
		return snap, err
	}
	for i, t := range tables {
		entry := AutocompleteTable{Name: t.Name}
		if i < maxColumnFetch {
			cols, err := m.ListColumns(ctx, db, "", t.Name)
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

// BrowseTable runs `SELECT * FROM db.table LIMIT … OFFSET …` and returns the
// rows + columns + primary-key info needed by the data browser.
//
// Pass limit < 0 to fetch all rows (no LIMIT/OFFSET clause). limit == 0 is
// reserved as "use default" and resolves to 200.
func (s *MetadataService) BrowseTable(ctx context.Context, connID, db, table string, limit, offset int) (BrowseResult, error) {
	var empty BrowseResult
	if connID == "" || db == "" || table == "" {
		return empty, fmt.Errorf("MetadataService: connID, db and table are required")
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
	base := fmt.Sprintf("SELECT * FROM %s.%s", dia.QuoteIdentifier(db), dia.QuoteIdentifier(table))
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
		if pk, perr := ed.PrimaryKeys(ctx, db, "", table); perr == nil {
			out.PrimaryKey = pk
			out.HasUniqueKey = len(pk) > 0
		}
	}
	return out, nil
}
