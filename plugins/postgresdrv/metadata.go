package postgresdrv

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"catdb/internal/dbdriver"
)

// metadata implements dbdriver.Metadata against pg_catalog.
//
// Databases are isolation boundaries in Postgres, so every (db, schema, …)
// read routes to the per-database pool via connection.poolFor — expanding a
// sibling database in the object tree transparently opens its pool.
type metadata struct {
	c *connection
}

// poolFor routes a metadata read to the pool of the database it addresses.
func (m metadata) poolFor(ctx context.Context, db string) (*pgxpool.Pool, error) {
	return m.c.poolFor(ctx, db)
}

// resolveSchema picks the namespace for a (db, schema) pair: the schema when
// given (the normal case — Capabilities.Schemas is true so callers pass it),
// falling back to public.
func resolveSchema(_, schema string) string {
	if s := strings.TrimSpace(schema); s != "" {
		return s
	}
	return "public"
}

func (m metadata) ListDatabases(ctx context.Context) ([]string, error) {
	pool, err := m.poolFor(ctx, "")
	if err != nil {
		return nil, err
	}
	const q = `SELECT datname FROM pg_database
	            WHERE NOT datistemplate AND datallowconn
	            ORDER BY datname`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list databases: %w", err)
	}
	return scanStrings(rows)
}

func (m metadata) ListSchemas(ctx context.Context, db string) ([]string, error) {
	pool, err := m.poolFor(ctx, db)
	if err != nil {
		return nil, err
	}
	const q = `SELECT nspname
	             FROM pg_namespace
	            WHERE nspname !~ '^pg_(toast|temp)' AND nspname <> 'pg_catalog' AND nspname <> 'information_schema'
	            ORDER BY (nspname <> 'public'), nspname`
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list schemas: %w", err)
	}
	return scanStrings(rows)
}

func scanStrings(rows pgx.Rows) ([]string, error) {
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

func (m metadata) ListTables(ctx context.Context, db, schema string) ([]dbdriver.TableInfo, error) {
	ns := resolveSchema(db, schema)
	pool, err := m.poolFor(ctx, db)
	if err != nil {
		return nil, err
	}
	const q = `SELECT c.relname,
	                  COALESCE(obj_description(c.oid, 'pg_class'), ''),
	                  GREATEST(c.reltuples::bigint, 0),
	                  COALESCE(pg_total_relation_size(c.oid), 0)
	             FROM pg_class c
	             JOIN pg_namespace n ON n.oid = c.relnamespace
	            WHERE n.nspname = $1 AND c.relkind IN ('r', 'p')
	            ORDER BY c.relname`
	rows, err := pool.Query(ctx, q, ns)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list tables: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.TableInfo
	for rows.Next() {
		var t dbdriver.TableInfo
		t.Schema = ns
		if err := rows.Scan(&t.Name, &t.Comment, &t.Rows, &t.DataLength); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (m metadata) ListViews(ctx context.Context, db, schema string) ([]dbdriver.ViewInfo, error) {
	ns := resolveSchema(db, schema)
	pool, err := m.poolFor(ctx, db)
	if err != nil {
		return nil, err
	}
	const q = `SELECT c.relname, COALESCE(obj_description(c.oid, 'pg_class'), '')
	             FROM pg_class c
	             JOIN pg_namespace n ON n.oid = c.relnamespace
	            WHERE n.nspname = $1 AND c.relkind IN ('v', 'm')
	            ORDER BY c.relname`
	rows, err := pool.Query(ctx, q, ns)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list views: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.ViewInfo
	for rows.Next() {
		var v dbdriver.ViewInfo
		v.Schema = ns
		if err := rows.Scan(&v.Name, &v.Comment); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m metadata) ListViewDefinitions(ctx context.Context, db, schema string) (map[string]string, error) {
	ns := resolveSchema(db, schema)
	pool, err := m.poolFor(ctx, db)
	if err != nil {
		return nil, err
	}
	const q = `SELECT c.relname, pg_get_viewdef(c.oid, true)
	             FROM pg_class c
	             JOIN pg_namespace n ON n.oid = c.relnamespace
	            WHERE n.nspname = $1 AND c.relkind IN ('v', 'm')`
	rows, err := pool.Query(ctx, q, ns)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list view definitions: %w", err)
	}
	defer rows.Close()
	defs := map[string]string{}
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return nil, err
		}
		defs[name] = def
	}
	return defs, rows.Err()
}

// columnsQuery serves both the per-table and bulk variants; when table is ""
// the filter drops away and c.relname is still selected for grouping.
const columnsQuery = `SELECT c.relname,
       a.attname,
       format_type(a.atttypid, a.atttypmod),
       t.typname,
       NOT a.attnotnull,
       pg_get_expr(ad.adbin, ad.adrelid),
       a.attidentity <> '',
       COALESCE(col_description(a.attrelid, a.attnum), ''),
       COALESCE(i.indisprimary, false),
       CASE WHEN t.typname IN ('varchar', 'bpchar') AND a.atttypmod > 4 THEN a.atttypmod - 4 ELSE 0 END,
       CASE WHEN t.typname = 'numeric' AND a.atttypmod > 4 THEN ((a.atttypmod - 4) >> 16) & 65535 ELSE 0 END,
       CASE WHEN t.typname = 'numeric' AND a.atttypmod > 4 THEN (a.atttypmod - 4) & 65535 ELSE 0 END
  FROM pg_attribute a
  JOIN pg_class c ON c.oid = a.attrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_type t ON t.oid = a.atttypid
  LEFT JOIN pg_attrdef ad ON ad.adrelid = a.attrelid AND ad.adnum = a.attnum
  LEFT JOIN pg_index i ON i.indrelid = a.attrelid AND i.indisprimary AND a.attnum = ANY(i.indkey)
 WHERE n.nspname = $1 AND c.relkind IN ('r', 'p') AND a.attnum > 0 AND NOT a.attisdropped`

func (m metadata) ListColumns(ctx context.Context, db, schema, table string) ([]dbdriver.ColumnMeta, error) {
	ns := resolveSchema(db, schema)
	pool, err := m.poolFor(ctx, db)
	if err != nil {
		return nil, err
	}
	if table == "" {
		return nil, fmt.Errorf("postgresdrv: ListColumns requires a table")
	}
	rows, err := pool.Query(ctx, columnsQuery+` AND c.relname = $2 ORDER BY a.attnum`, ns, table)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list columns: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.ColumnMeta
	for rows.Next() {
		_, c, err := scanColumn(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// scanColumn maps one columnsQuery row. Identity/serial columns are flagged
// auto-increment and their nextval() default is suppressed — it is an
// implementation detail that would otherwise show up as diff noise.
func scanColumn(rows pgx.Rows) (table string, c dbdriver.ColumnMeta, err error) {
	var (
		nativeType string
		baseType   string
		defaultVal *string
		identity   bool
		primary    bool
	)
	if err = rows.Scan(&table, &c.Name, &nativeType, &baseType, &c.Nullable, &defaultVal,
		&identity, &c.Comment, &primary, &c.Length, &c.Precision, &c.Scale); err != nil {
		return "", c, err
	}
	c.NativeType = nativeType
	c.LogicalType = dialect{}.MapType(baseType)
	c.IsPrimaryKey = primary
	serial := defaultVal != nil && strings.HasPrefix(*defaultVal, "nextval(")
	c.IsAutoIncrement = identity || serial
	if defaultVal != nil && !c.IsAutoIncrement {
		c.Default = defaultVal
	}
	return table, c, nil
}

// indexesQuery serves both the per-table and bulk variants.
const indexesQuery = `SELECT c.relname,
       i.relname,
       am.amname,
       ix.indisunique,
       ix.indisprimary,
       pg_get_indexdef(ix.indexrelid, k.n, true),
       (ix.indoption[k.n-1] & 1) = 1,
       COALESCE(obj_description(ix.indexrelid, 'pg_class'), '')
  FROM pg_index ix
  JOIN pg_class i ON i.oid = ix.indexrelid
  JOIN pg_class c ON c.oid = ix.indrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_am am ON am.oid = i.relam
  CROSS JOIN LATERAL generate_series(1, ix.indnkeyatts) AS k(n)
 WHERE n.nspname = $1`

func (m metadata) ListIndexes(ctx context.Context, db, schema, table string) ([]dbdriver.IndexInfo, error) {
	ns := resolveSchema(db, schema)
	pool, err := m.poolFor(ctx, db)
	if err != nil {
		return nil, err
	}
	if table == "" {
		return nil, fmt.Errorf("postgresdrv: ListIndexes requires a table")
	}
	rows, err := pool.Query(ctx, indexesQuery+` AND c.relname = $2 ORDER BY i.relname, k.n`, ns, table)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list indexes: %w", err)
	}
	grouped, order, err := scanIndexes(rows)
	if err != nil {
		return nil, err
	}
	out := make([]dbdriver.IndexInfo, 0, len(order))
	for _, k := range order {
		out = append(out, *grouped[k])
	}
	return out, nil
}

type tableKey struct{ table, name string }

func scanIndexes(rows pgx.Rows) (map[tableKey]*dbdriver.IndexInfo, []tableKey, error) {
	defer rows.Close()
	byKey := map[tableKey]*dbdriver.IndexInfo{}
	var order []tableKey
	for rows.Next() {
		var (
			table   string
			name    string
			am      string
			unique  bool
			primary bool
			colExpr string
			desc    bool
			comment string
		)
		if err := rows.Scan(&table, &name, &am, &unique, &primary, &colExpr, &desc, &comment); err != nil {
			return nil, nil, err
		}
		k := tableKey{table, name}
		ix, ok := byKey[k]
		if !ok {
			ix = &dbdriver.IndexInfo{
				Name:    name,
				Unique:  unique,
				Primary: primary,
				Type:    strings.ToUpper(am),
				Comment: comment,
			}
			byKey[k] = ix
			order = append(order, k)
		}
		// Only btree entries are ordered; other access methods have no direction.
		dir := ""
		if am == "btree" {
			if desc {
				dir = "DESC"
			} else {
				dir = "ASC"
			}
		}
		// pg_get_indexdef returns the bare column name (or an expression for
		// functional indexes — carried through as-is).
		ix.Columns = append(ix.Columns, dbdriver.IndexColumn{Name: strings.Trim(colExpr, `"`), Order: dir})
	}
	return byKey, order, rows.Err()
}

// foreignKeysQuery serves both the per-table and bulk variants.
const foreignKeysQuery = `SELECT c.relname,
       con.conname,
       att.attname,
       fn.nspname,
       fc.relname,
       fatt.attname,
       con.confupdtype,
       con.confdeltype
  FROM pg_constraint con
  JOIN pg_class c ON c.oid = con.conrelid
  JOIN pg_namespace n ON n.oid = c.relnamespace
  JOIN pg_class fc ON fc.oid = con.confrelid
  JOIN pg_namespace fn ON fn.oid = fc.relnamespace
  CROSS JOIN LATERAL unnest(con.conkey::int2[], con.confkey::int2[]) WITH ORDINALITY AS u(k, fk, ord)
  JOIN pg_attribute att ON att.attrelid = con.conrelid AND att.attnum = u.k
  JOIN pg_attribute fatt ON fatt.attrelid = con.confrelid AND fatt.attnum = u.fk
 WHERE con.contype = 'f' AND n.nspname = $1`

func (m metadata) ListForeignKeys(ctx context.Context, db, schema, table string) ([]dbdriver.ForeignKeyInfo, error) {
	ns := resolveSchema(db, schema)
	pool, err := m.poolFor(ctx, db)
	if err != nil {
		return nil, err
	}
	if table == "" {
		return nil, fmt.Errorf("postgresdrv: ListForeignKeys requires a table")
	}
	rows, err := pool.Query(ctx, foreignKeysQuery+` AND c.relname = $2 ORDER BY con.conname, u.ord`, ns, table)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list foreign keys: %w", err)
	}
	grouped, order, err := scanForeignKeys(rows)
	if err != nil {
		return nil, err
	}
	out := make([]dbdriver.ForeignKeyInfo, 0, len(order))
	for _, k := range order {
		out = append(out, *grouped[k])
	}
	return out, nil
}

func scanForeignKeys(rows pgx.Rows) (map[tableKey]*dbdriver.ForeignKeyInfo, []tableKey, error) {
	defer rows.Close()
	byKey := map[tableKey]*dbdriver.ForeignKeyInfo{}
	var order []tableKey
	for rows.Next() {
		var (
			table     string
			name      string
			column    string
			refSchema string
			refTable  string
			refColumn string
			onUpdate  string
			onDelete  string
		)
		if err := rows.Scan(&table, &name, &column, &refSchema, &refTable, &refColumn, &onUpdate, &onDelete); err != nil {
			return nil, nil, err
		}
		k := tableKey{table, name}
		fk, ok := byKey[k]
		if !ok {
			fk = &dbdriver.ForeignKeyInfo{
				Name:             name,
				ReferencedSchema: refSchema,
				ReferencedTable:  refTable,
				OnUpdate:         fkAction(onUpdate),
				OnDelete:         fkAction(onDelete),
			}
			byKey[k] = fk
			order = append(order, k)
		}
		fk.Columns = append(fk.Columns, column)
		fk.ReferencedColumns = append(fk.ReferencedColumns, refColumn)
	}
	return byKey, order, rows.Err()
}

// fkAction maps pg_constraint.confupdtype/confdeltype codes. NO ACTION (the
// Postgres default) maps to "" so schemadiff's "empty ≅ default" rule holds.
func fkAction(code string) string {
	switch code {
	case "r":
		return "RESTRICT"
	case "c":
		return "CASCADE"
	case "n":
		return "SET NULL"
	case "d":
		return "SET DEFAULT"
	default: // "a" — NO ACTION
		return ""
	}
}

func (m metadata) ListRoutines(ctx context.Context, db, schema string) ([]dbdriver.RoutineInfo, error) {
	ns := resolveSchema(db, schema)
	pool, err := m.poolFor(ctx, db)
	if err != nil {
		return nil, err
	}
	const q = `SELECT p.proname,
	                  CASE p.prokind WHEN 'p' THEN 'PROCEDURE' ELSE 'FUNCTION' END,
	                  pg_get_functiondef(p.oid),
	                  COALESCE(obj_description(p.oid, 'pg_proc'), '')
	             FROM pg_proc p
	             JOIN pg_namespace n ON n.oid = p.pronamespace
	            WHERE n.nspname = $1 AND p.prokind IN ('f', 'p')
	            ORDER BY 2, 1`
	rows, err := pool.Query(ctx, q, ns)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list routines: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.RoutineInfo
	for rows.Next() {
		var r dbdriver.RoutineInfo
		r.Schema = ns
		if err := rows.Scan(&r.Name, &r.Type, &r.Definition, &r.Comment); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	const q2 = `SELECT t.tgname, pg_get_triggerdef(t.oid)
	              FROM pg_trigger t
	              JOIN pg_class c ON c.oid = t.tgrelid
	              JOIN pg_namespace n ON n.oid = c.relnamespace
	             WHERE n.nspname = $1 AND NOT t.tgisinternal
	             ORDER BY t.tgname`
	tRows, err := pool.Query(ctx, q2, ns)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list triggers: %w", err)
	}
	defer tRows.Close()
	for tRows.Next() {
		var r dbdriver.RoutineInfo
		r.Schema = ns
		r.Type = "TRIGGER"
		if err := tRows.Scan(&r.Name, &r.Definition); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, tRows.Err()
}

// GetCreateTable reconstructs the CREATE TABLE DDL from the catalog —
// PostgreSQL has no SHOW CREATE TABLE equivalent.
func (m metadata) GetCreateTable(ctx context.Context, db, schema, table string) (string, error) {
	ns := resolveSchema(db, schema)
	pool, err := m.poolFor(ctx, db)
	if err != nil {
		return "", err
	}
	if table == "" {
		return "", fmt.Errorf("postgresdrv: GetCreateTable requires a table")
	}
	cols, err := m.ListColumns(ctx, db, schema, table)
	if err != nil {
		return "", err
	}
	if len(cols) == 0 {
		return "", fmt.Errorf("postgresdrv: table %s.%s not found", ns, table)
	}
	ixs, err := m.ListIndexes(ctx, db, schema, table)
	if err != nil {
		return "", err
	}
	fks, err := m.ListForeignKeys(ctx, db, schema, table)
	if err != nil {
		return "", err
	}
	var comment string
	const q = `SELECT COALESCE(obj_description(c.oid, 'pg_class'), '')
	             FROM pg_class c
	             JOIN pg_namespace n ON n.oid = c.relnamespace
	            WHERE n.nspname = $1 AND c.relname = $2`
	if err := pool.QueryRow(ctx, q, ns, table).Scan(&comment); err != nil {
		return "", fmt.Errorf("postgresdrv: table comment: %w", err)
	}
	return dialect{}.GenerateCreateTable(dbdriver.TableSchema{
		Name:        table,
		Schema:      ns,
		Columns:     cols,
		Indexes:     ixs,
		ForeignKeys: fks,
		Comment:     comment,
	})
}
