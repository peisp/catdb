package sqlitedrv

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"catdb/internal/dbdriver"
)

// metadata implements dbdriver.Metadata against sqlite_master and the pragma
// table-valued functions (pragma_table_info(…) etc.), which — unlike bare
// PRAGMA statements — accept bound parameters and a schema argument.
type metadata struct {
	db *sql.DB
}

// resolveDB picks the target schema for a (db, schema) pair. SQLite has no
// schema level below the database ("main" / attached names), so whichever the
// caller supplied wins; empty means "main".
func resolveDB(db, schema string) string {
	if db != "" {
		return db
	}
	if schema != "" {
		return schema
	}
	return "main"
}

func (m metadata) ListDatabases(ctx context.Context) ([]string, error) {
	rows, err := m.db.QueryContext(ctx, "PRAGMA database_list")
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: list databases: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var seq int
		var name string
		var file sql.NullString
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return nil, err
		}
		if name == "temp" {
			continue
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (m metadata) ListSchemas(_ context.Context, _ string) ([]string, error) {
	// SQLite has no schema level distinct from the database.
	return nil, nil
}

func (m metadata) ListTables(ctx context.Context, db, schema string) ([]dbdriver.TableInfo, error) {
	d := resolveDB(db, schema)
	dia := dialect{}
	q := fmt.Sprintf(`SELECT name FROM %s.sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%%' ORDER BY name`,
		dia.QuoteIdentifier(d))
	rows, err := m.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: list tables: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.TableInfo
	for rows.Next() {
		var t dbdriver.TableInfo
		t.Schema = d
		if err := rows.Scan(&t.Name); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (m metadata) ListViews(ctx context.Context, db, schema string) ([]dbdriver.ViewInfo, error) {
	d := resolveDB(db, schema)
	dia := dialect{}
	q := fmt.Sprintf(`SELECT name FROM %s.sqlite_master WHERE type='view' ORDER BY name`, dia.QuoteIdentifier(d))
	rows, err := m.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: list views: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.ViewInfo
	for rows.Next() {
		var v dbdriver.ViewInfo
		v.Schema = d
		if err := rows.Scan(&v.Name); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// reViewBody strips the CREATE VIEW … AS prefix so the map carries just the
// SELECT body, matching what information_schema-based drivers return.
var reViewBody = regexp.MustCompile(`(?is)^\s*CREATE\s+(?:TEMP\s+|TEMPORARY\s+)?VIEW\s+.*?\s+AS\s+(.*)$`)

func (m metadata) ListViewDefinitions(ctx context.Context, db, schema string) (map[string]string, error) {
	d := resolveDB(db, schema)
	dia := dialect{}
	q := fmt.Sprintf(`SELECT name, sql FROM %s.sqlite_master WHERE type='view'`, dia.QuoteIdentifier(d))
	rows, err := m.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: list view definitions: %w", err)
	}
	defer rows.Close()
	defs := map[string]string{}
	for rows.Next() {
		var name string
		var ddl sql.NullString
		if err := rows.Scan(&name, &ddl); err != nil {
			return nil, err
		}
		body := ddl.String
		if mch := reViewBody.FindStringSubmatch(body); mch != nil {
			body = mch[1]
		}
		defs[name] = body
	}
	return defs, rows.Err()
}

// reTypeParams extracts "(N[,M])" from a declared type.
var reTypeParams = regexp.MustCompile(`\(\s*(\d+)\s*(?:,\s*(\d+)\s*)?\)`)

// unquoteDefault converts PRAGMA's dflt_value (a SQL literal) into the raw
// value ColumnMeta.Default carries — 'abc' → abc — so DDL generation can
// re-quote it uniformly across drivers.
func unquoteDefault(lit string) string {
	s := strings.TrimSpace(lit)
	if len(s) >= 2 && s[0] == '\'' && s[len(s)-1] == '\'' {
		return strings.ReplaceAll(s[1:len(s)-1], "''", "'")
	}
	return s
}

func (m metadata) ListColumns(ctx context.Context, db, schema, table string) ([]dbdriver.ColumnMeta, error) {
	d := resolveDB(db, schema)
	if table == "" {
		return nil, fmt.Errorf("sqlitedrv: ListColumns requires a table")
	}
	const q = `SELECT name, type, "notnull", dflt_value, pk FROM pragma_table_info(?, ?) ORDER BY cid`
	rows, err := m.db.QueryContext(ctx, q, table, d)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: list columns: %w", err)
	}
	defer rows.Close()

	dia := dialect{}
	var out []dbdriver.ColumnMeta
	pkCount := 0
	for rows.Next() {
		var (
			c          dbdriver.ColumnMeta
			notNull    int
			defaultVal sql.NullString
			pk         int
		)
		if err := rows.Scan(&c.Name, &c.NativeType, &notNull, &defaultVal, &pk); err != nil {
			return nil, err
		}
		c.LogicalType = dia.MapType(c.NativeType)
		c.Nullable = notNull == 0
		if defaultVal.Valid {
			s := unquoteDefault(defaultVal.String)
			c.Default = &s
		}
		c.IsPrimaryKey = pk > 0
		if pk > 0 {
			pkCount++
		}
		if mch := reTypeParams.FindStringSubmatch(c.NativeType); mch != nil {
			n, _ := strconv.ParseInt(mch[1], 10, 64)
			if affinityOf(c.NativeType) == "TEXT" {
				c.Length = n
			} else {
				c.Precision = n
				if mch[2] != "" {
					c.Scale, _ = strconv.ParseInt(mch[2], 10, 64)
				}
			}
		}
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// AUTOINCREMENT lives only in the table's DDL text: it applies to the
	// single INTEGER PRIMARY KEY column when the keyword is present.
	if pkCount == 1 {
		ddl, err := m.tableDDL(ctx, d, table)
		if err == nil && strings.Contains(strings.ToUpper(ddl), "AUTOINCREMENT") {
			for i := range out {
				if out[i].IsPrimaryKey && affinityOf(out[i].NativeType) == "INTEGER" {
					out[i].IsAutoIncrement = true
				}
			}
		}
	}
	return out, nil
}

func (m metadata) tableDDL(ctx context.Context, d, table string) (string, error) {
	dia := dialect{}
	q := fmt.Sprintf(`SELECT sql FROM %s.sqlite_master WHERE type IN ('table','view') AND name=?`, dia.QuoteIdentifier(d))
	var ddl sql.NullString
	if err := m.db.QueryRowContext(ctx, q, table).Scan(&ddl); err != nil {
		return "", err
	}
	return ddl.String, nil
}

func (m metadata) ListIndexes(ctx context.Context, db, schema, table string) ([]dbdriver.IndexInfo, error) {
	d := resolveDB(db, schema)
	if table == "" {
		return nil, fmt.Errorf("sqlitedrv: ListIndexes requires a table")
	}

	// PRIMARY first: INTEGER PRIMARY KEY (rowid alias) has no real index, so
	// synthesize the entry from table_info's pk ordinals like other drivers
	// report it.
	pkCols, err := m.primaryKeyColumns(ctx, d, table)
	if err != nil {
		return nil, err
	}
	var out []dbdriver.IndexInfo
	if len(pkCols) > 0 {
		ix := dbdriver.IndexInfo{Name: "PRIMARY", Unique: true, Primary: true}
		for _, c := range pkCols {
			ix.Columns = append(ix.Columns, dbdriver.IndexColumn{Name: c})
		}
		out = append(out, ix)
	}

	const q = `SELECT name, "unique", origin, partial FROM pragma_index_list(?, ?) ORDER BY seq`
	rows, err := m.db.QueryContext(ctx, q, table, d)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: list indexes: %w", err)
	}
	defer rows.Close()
	type entry struct {
		name   string
		unique bool
	}
	var entries []entry
	for rows.Next() {
		var (
			name    string
			unique  int
			origin  string
			partial int
		)
		if err := rows.Scan(&name, &unique, &origin, &partial); err != nil {
			return nil, err
		}
		if origin == "pk" {
			continue // covered by the synthesized PRIMARY entry
		}
		entries = append(entries, entry{name: name, unique: unique == 1})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for _, e := range entries {
		cols, err := m.indexColumns(ctx, d, e.name)
		if err != nil {
			return nil, err
		}
		out = append(out, dbdriver.IndexInfo{Name: e.name, Columns: cols, Unique: e.unique})
	}
	return out, nil
}

// indexColumns reads the key columns (and sort direction) of one index.
func (m metadata) indexColumns(ctx context.Context, d, index string) ([]dbdriver.IndexColumn, error) {
	const q = `SELECT name, "desc" FROM pragma_index_xinfo(?, ?) WHERE key=1 ORDER BY seqno`
	rows, err := m.db.QueryContext(ctx, q, index, d)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: index columns: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.IndexColumn
	for rows.Next() {
		var name sql.NullString // NULL for expression index members
		var desc int
		if err := rows.Scan(&name, &desc); err != nil {
			return nil, err
		}
		order := "ASC"
		if desc == 1 {
			order = "DESC"
		}
		out = append(out, dbdriver.IndexColumn{Name: name.String, Order: order})
	}
	return out, rows.Err()
}

func (m metadata) primaryKeyColumns(ctx context.Context, d, table string) ([]string, error) {
	const q = `SELECT name, pk FROM pragma_table_info(?, ?) WHERE pk > 0 ORDER BY pk`
	rows, err := m.db.QueryContext(ctx, q, table, d)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: pk columns: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var name string
		var pk int
		if err := rows.Scan(&name, &pk); err != nil {
			return nil, err
		}
		out = append(out, name)
	}
	return out, rows.Err()
}

func (m metadata) ListForeignKeys(ctx context.Context, db, schema, table string) ([]dbdriver.ForeignKeyInfo, error) {
	d := resolveDB(db, schema)
	if table == "" {
		return nil, fmt.Errorf("sqlitedrv: ListForeignKeys requires a table")
	}
	// SQLite FKs are unnamed at the pragma level; synthesize stable names from
	// the pragma's constraint id so diffs between two SQLite databases align.
	const q = `SELECT id, "table", "from", "to", on_update, on_delete FROM pragma_foreign_key_list(?, ?) ORDER BY id, seq`
	rows, err := m.db.QueryContext(ctx, q, table, d)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: list foreign keys: %w", err)
	}
	defer rows.Close()
	byID := make(map[int]*dbdriver.ForeignKeyInfo)
	var order []int
	for rows.Next() {
		var (
			id       int
			refTable string
			from     string
			to       sql.NullString
			onUpdate string
			onDelete string
		)
		if err := rows.Scan(&id, &refTable, &from, &to, &onUpdate, &onDelete); err != nil {
			return nil, err
		}
		fk, ok := byID[id]
		if !ok {
			fk = &dbdriver.ForeignKeyInfo{
				Name:            fmt.Sprintf("fk_%s_%d", table, id),
				ReferencedTable: refTable,
				OnUpdate:        onUpdate,
				OnDelete:        onDelete,
			}
			byID[id] = fk
			order = append(order, id)
		}
		fk.Columns = append(fk.Columns, from)
		fk.ReferencedColumns = append(fk.ReferencedColumns, to.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.Ints(order)
	out := make([]dbdriver.ForeignKeyInfo, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

func (m metadata) ListRoutines(ctx context.Context, db, schema string) ([]dbdriver.RoutineInfo, error) {
	d := resolveDB(db, schema)
	dia := dialect{}
	q := fmt.Sprintf(`SELECT name, sql FROM %s.sqlite_master WHERE type='trigger' ORDER BY name`, dia.QuoteIdentifier(d))
	rows, err := m.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: list triggers: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.RoutineInfo
	for rows.Next() {
		var r dbdriver.RoutineInfo
		var ddl sql.NullString
		r.Schema = d
		r.Type = "TRIGGER"
		if err := rows.Scan(&r.Name, &ddl); err != nil {
			return nil, err
		}
		r.Definition = ddl.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func (m metadata) GetCreateTable(ctx context.Context, db, schema, table string) (string, error) {
	d := resolveDB(db, schema)
	if table == "" {
		return "", fmt.Errorf("sqlitedrv: GetCreateTable requires a table")
	}
	ddl, err := m.tableDDL(ctx, d, table)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("sqlitedrv: table %s.%s not found", d, table)
		}
		return "", fmt.Errorf("sqlitedrv: get create table: %w", err)
	}
	return ddl, nil
}
