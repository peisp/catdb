package mysqldrv

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// metadata implements dbdriver.Metadata against information_schema.
//
// Why information_schema and not SHOW *: information_schema is the portable
// path and gives us precise filterable rows. SHOW CREATE TABLE is the one
// exception — it's what the UI's DDL pane actually wants to display.
type metadata struct {
	db *sql.DB
	// mariadb: the server is MariaDB (sniffed once in Open). MariaDB >=10.2.7
	// stores COLUMN_DEFAULT as SQL expression text where MySQL reports bare
	// values; normalizeColumnDefault canonicalizes based on this flag.
	mariadb bool
}

// On MySQL, "schema" is the same concept as "database". We accept either
// position (db or schema) — whichever the caller supplied — and never expose
// a separate schema level. The Capabilities flag (Schemas=false) tells the
// front-end to render only the database level.
func resolveDB(db, schema string) string {
	if schema != "" {
		return schema
	}
	return db
}

// normalizeColumnDefault canonicalizes a COLUMN_DEFAULT to the MySQL bare-value
// convention that schema-diff and ddl.go's formatDefaultExpr expect. MariaDB
// (>=10.2.7) stores defaults as SQL expression text: string literals arrive
// quoted and escaped ('abc', doubled inner quotes, 'a\\b'), keyword defaults
// lowercase with parens (current_timestamp()), and "no default" on a nullable
// column is the literal string NULL. MySQL values pass through untouched —
// there a bare NULL string is a genuine user default of the four characters
// "NULL".
func normalizeColumnDefault(raw sql.NullString, mariadb bool) *string {
	if !raw.Valid {
		return nil
	}
	s := raw.String
	if mariadb {
		switch {
		case s == "NULL":
			return nil
		case len(s) >= 2 && strings.HasPrefix(s, "'") && strings.HasSuffix(s, "'"):
			s = unescapeSQLString(s[1 : len(s)-1])
		default:
			if ts, ok := canonicalCurrentTimestamp(s); ok {
				s = ts
			}
		}
	}
	return &s
}

// unescapeSQLString reverses MySQL string-literal escaping inside an already
// unquoted default: doubled quotes and the backslash escapes quoteString emits.
func unescapeSQLString(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\\' && i+1 < len(s):
			i++
			switch s[i] {
			case 'n':
				b.WriteByte('\n')
			case 't':
				b.WriteByte('\t')
			case 'r':
				b.WriteByte('\r')
			case '0':
				b.WriteByte(0)
			case 'b':
				b.WriteByte('\b')
			case 'Z':
				b.WriteByte(26)
			default: // \\ \' \" and unknown escapes → the char itself
				b.WriteByte(s[i])
			}
		case s[i] == '\'' && i+1 < len(s) && s[i+1] == '\'':
			b.WriteByte('\'')
			i++
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// canonicalCurrentTimestamp maps MariaDB's lowercase function form onto the
// keyword form MySQL reports: current_timestamp() → CURRENT_TIMESTAMP,
// current_timestamp(6) → CURRENT_TIMESTAMP(6).
func canonicalCurrentTimestamp(s string) (string, bool) {
	up := strings.ToUpper(strings.TrimSpace(s))
	if up == "CURRENT_TIMESTAMP" || up == "CURRENT_TIMESTAMP()" {
		return "CURRENT_TIMESTAMP", true
	}
	if inner, ok := strings.CutPrefix(up, "CURRENT_TIMESTAMP("); ok && strings.HasSuffix(inner, ")") {
		digits := strings.TrimSuffix(inner, ")")
		if digits != "" && strings.Trim(digits, "0123456789") == "" {
			return up, true
		}
	}
	return "", false
}

func (m metadata) ListDatabases(ctx context.Context) ([]string, error) {
	const q = `SELECT SCHEMA_NAME
	             FROM information_schema.SCHEMATA
	            ORDER BY SCHEMA_NAME`
	rows, err := m.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list databases: %w", err)
	}
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

func (m metadata) ListSchemas(_ context.Context, _ string) ([]string, error) {
	// MySQL has no schemas distinct from databases.
	return nil, nil
}

func (m metadata) ListTables(ctx context.Context, db, schema string) ([]dbdriver.TableInfo, error) {
	d := resolveDB(db, schema)
	if d == "" {
		return nil, fmt.Errorf("mysqldrv: ListTables requires a database name")
	}
	const q = `SELECT TABLE_NAME, IFNULL(ENGINE,''), IFNULL(TABLE_COMMENT,''), IFNULL(TABLE_ROWS,0),
	                  IFNULL(DATA_LENGTH,0) + IFNULL(INDEX_LENGTH,0), IFNULL(CAST(CREATE_TIME AS CHAR),''), IFNULL(CAST(UPDATE_TIME AS CHAR),''), IFNULL(TABLE_COLLATION,'')
	             FROM information_schema.TABLES
	            WHERE TABLE_SCHEMA=? AND TABLE_TYPE='BASE TABLE'
	            ORDER BY TABLE_NAME`
	rows, err := m.db.QueryContext(ctx, q, d)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list tables: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.TableInfo
	for rows.Next() {
		var t dbdriver.TableInfo
		var engine, collation string
		t.Schema = d
		if err := rows.Scan(&t.Name, &engine, &t.Comment, &t.Rows,
			&t.DataLength, &t.CreateTime, &t.UpdateTime, &collation); err != nil {
			return nil, err
		}
		if engine != "" || collation != "" {
			t.Options = map[string]string{"engine": engine, "collation": collation}
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (m metadata) ListViews(ctx context.Context, db, schema string) ([]dbdriver.ViewInfo, error) {
	d := resolveDB(db, schema)
	if d == "" {
		return nil, fmt.Errorf("mysqldrv: ListViews requires a database name")
	}
	// information_schema.VIEWS doesn't carry a comment; use TABLES for that.
	const q = `SELECT t.TABLE_NAME, IFNULL(t.TABLE_COMMENT,'')
	             FROM information_schema.TABLES t
	            WHERE t.TABLE_SCHEMA=? AND t.TABLE_TYPE='VIEW'
	            ORDER BY t.TABLE_NAME`
	rows, err := m.db.QueryContext(ctx, q, d)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list views: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.ViewInfo
	for rows.Next() {
		var v dbdriver.ViewInfo
		v.Schema = d
		if err := rows.Scan(&v.Name, &v.Comment); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m metadata) ListViewDefinitions(ctx context.Context, db, schema string) (map[string]string, error) {
	d := resolveDB(db, schema)
	if d == "" {
		return nil, fmt.Errorf("mysqldrv: ListViewDefinitions requires a database name")
	}
	const q = `SELECT TABLE_NAME, VIEW_DEFINITION FROM information_schema.VIEWS WHERE TABLE_SCHEMA=?`
	rows, err := m.db.QueryContext(ctx, q, d)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list view definitions: %w", err)
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

func (m metadata) ListColumns(ctx context.Context, db, schema, table string) ([]dbdriver.ColumnMeta, error) {
	d := resolveDB(db, schema)
	if d == "" || table == "" {
		return nil, fmt.Errorf("mysqldrv: ListColumns requires db and table")
	}
	const q = `SELECT COLUMN_NAME, COLUMN_TYPE, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT,
	                  IFNULL(CHARACTER_MAXIMUM_LENGTH,0), IFNULL(NUMERIC_PRECISION,0), IFNULL(NUMERIC_SCALE,0),
	                  COLUMN_KEY, EXTRA, IFNULL(COLUMN_COMMENT,'')
	             FROM information_schema.COLUMNS
	            WHERE TABLE_SCHEMA=? AND TABLE_NAME=?
	            ORDER BY ORDINAL_POSITION`
	rows, err := m.db.QueryContext(ctx, q, d, table)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list columns: %w", err)
	}
	defer rows.Close()

	dia := dialect{}
	var out []dbdriver.ColumnMeta
	for rows.Next() {
		var (
			c          dbdriver.ColumnMeta
			columnType string
			dataType   string
			isNullable string
			defaultVal sql.NullString
			length     int64
			precision  int64
			scale      int64
			columnKey  string
			extra      string
			comment    string
		)
		if err := rows.Scan(&c.Name, &columnType, &dataType, &isNullable, &defaultVal,
			&length, &precision, &scale, &columnKey, &extra, &comment); err != nil {
			return nil, err
		}
		c.NativeType = columnType
		c.LogicalType = dia.MapType(dataType)
		c.Nullable = strings.EqualFold(isNullable, "YES")
		c.Length = length
		c.Precision = precision
		c.Scale = scale
		c.Default = normalizeColumnDefault(defaultVal, m.mariadb)
		c.IsPrimaryKey = strings.EqualFold(columnKey, "PRI")
		c.IsAutoIncrement = strings.Contains(strings.ToLower(extra), "auto_increment")
		c.Comment = comment
		out = append(out, c)
	}
	return out, rows.Err()
}

func (m metadata) ListIndexes(ctx context.Context, db, schema, table string) ([]dbdriver.IndexInfo, error) {
	d := resolveDB(db, schema)
	if d == "" || table == "" {
		return nil, fmt.Errorf("mysqldrv: ListIndexes requires db and table")
	}
	const q = `SELECT INDEX_NAME, COLUMN_NAME, NON_UNIQUE, INDEX_TYPE, SEQ_IN_INDEX, COLLATION, INDEX_COMMENT
	             FROM information_schema.STATISTICS
	            WHERE TABLE_SCHEMA=? AND TABLE_NAME=?
	            ORDER BY INDEX_NAME, SEQ_IN_INDEX`
	rows, err := m.db.QueryContext(ctx, q, d, table)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list indexes: %w", err)
	}
	defer rows.Close()
	byName := make(map[string]*dbdriver.IndexInfo)
	var order []string
	for rows.Next() {
		var (
			name      string
			column    string
			nonUnique int
			idxType   string
			seq       int
			collation sql.NullString
			comment   sql.NullString
		)
		if err := rows.Scan(&name, &column, &nonUnique, &idxType, &seq, &collation, &comment); err != nil {
			return nil, err
		}
		ix, ok := byName[name]
		if !ok {
			ix = &dbdriver.IndexInfo{
				Name:    name,
				Unique:  nonUnique == 0,
				Primary: name == "PRIMARY",
				Type:    idxType,
				Comment: comment.String,
			}
			byName[name] = ix
			order = append(order, name)
		}
		// COLLATION: 'A' = ascending, 'D' = descending, NULL = not sorted (e.g. HASH).
		dir := ""
		if collation.Valid {
			switch collation.String {
			case "A":
				dir = "ASC"
			case "D":
				dir = "DESC"
			}
		}
		ix.Columns = append(ix.Columns, dbdriver.IndexColumn{Name: column, Order: dir})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]dbdriver.IndexInfo, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

func (m metadata) ListForeignKeys(ctx context.Context, db, schema, table string) ([]dbdriver.ForeignKeyInfo, error) {
	d := resolveDB(db, schema)
	if d == "" || table == "" {
		return nil, fmt.Errorf("mysqldrv: ListForeignKeys requires db and table")
	}
	// Join KEY_COLUMN_USAGE (the per-column FK) with REFERENTIAL_CONSTRAINTS
	// (the parent table + ON UPDATE/DELETE rules) on CONSTRAINT_NAME.
	const q = `SELECT k.CONSTRAINT_NAME, k.COLUMN_NAME,
	                  k.REFERENCED_TABLE_SCHEMA, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME,
	                  IFNULL(r.UPDATE_RULE,''), IFNULL(r.DELETE_RULE,''), k.ORDINAL_POSITION
	             FROM information_schema.KEY_COLUMN_USAGE k
	             LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r
	                    ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
	                   AND r.CONSTRAINT_NAME   = k.CONSTRAINT_NAME
	            WHERE k.TABLE_SCHEMA=? AND k.TABLE_NAME=?
	              AND k.REFERENCED_TABLE_NAME IS NOT NULL
	            ORDER BY k.CONSTRAINT_NAME, k.ORDINAL_POSITION`
	rows, err := m.db.QueryContext(ctx, q, d, table)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list foreign keys: %w", err)
	}
	defer rows.Close()
	byName := make(map[string]*dbdriver.ForeignKeyInfo)
	var order []string
	for rows.Next() {
		var (
			name      string
			column    string
			refSchema sql.NullString
			refTable  sql.NullString
			refColumn sql.NullString
			onUpdate  string
			onDelete  string
			ord       int
		)
		if err := rows.Scan(&name, &column, &refSchema, &refTable, &refColumn,
			&onUpdate, &onDelete, &ord); err != nil {
			return nil, err
		}
		fk, ok := byName[name]
		if !ok {
			fk = &dbdriver.ForeignKeyInfo{
				Name:             name,
				ReferencedSchema: refSchema.String,
				ReferencedTable:  refTable.String,
				OnUpdate:         onUpdate,
				OnDelete:         onDelete,
			}
			byName[name] = fk
			order = append(order, name)
		}
		fk.Columns = append(fk.Columns, column)
		fk.ReferencedColumns = append(fk.ReferencedColumns, refColumn.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]dbdriver.ForeignKeyInfo, 0, len(order))
	for _, n := range order {
		out = append(out, *byName[n])
	}
	return out, nil
}

func (m metadata) ListRoutines(ctx context.Context, db, schema string) ([]dbdriver.RoutineInfo, error) {
	d := resolveDB(db, schema)
	if d == "" {
		return nil, fmt.Errorf("mysqldrv: ListRoutines requires a database name")
	}
	// Procedures + functions from ROUTINES, triggers from TRIGGERS.
	const q1 = `SELECT ROUTINE_NAME, ROUTINE_TYPE, IFNULL(ROUTINE_DEFINITION,''), IFNULL(ROUTINE_COMMENT,'')
	              FROM information_schema.ROUTINES
	             WHERE ROUTINE_SCHEMA=?
	             ORDER BY ROUTINE_TYPE, ROUTINE_NAME`
	rows, err := m.db.QueryContext(ctx, q1, d)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list routines: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.RoutineInfo
	for rows.Next() {
		var r dbdriver.RoutineInfo
		r.Schema = d
		if err := rows.Scan(&r.Name, &r.Type, &r.Definition, &r.Comment); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	const q2 = `SELECT TRIGGER_NAME, IFNULL(ACTION_STATEMENT,'')
	              FROM information_schema.TRIGGERS
	             WHERE TRIGGER_SCHEMA=?
	             ORDER BY TRIGGER_NAME`
	tRows, err := m.db.QueryContext(ctx, q2, d)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list triggers: %w", err)
	}
	defer tRows.Close()
	for tRows.Next() {
		var r dbdriver.RoutineInfo
		r.Schema = d
		r.Type = "TRIGGER"
		if err := tRows.Scan(&r.Name, &r.Definition); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, tRows.Err()
}

func (m metadata) GetCreateTable(ctx context.Context, db, schema, table string) (string, error) {
	d := resolveDB(db, schema)
	if d == "" || table == "" {
		return "", fmt.Errorf("mysqldrv: GetCreateTable requires db and table")
	}
	// SHOW CREATE TABLE returns 2 columns: Table, Create Table.
	dia := dialect{}
	q := fmt.Sprintf("SHOW CREATE TABLE %s.%s", dia.QuoteIdentifier(d), dia.QuoteIdentifier(table))
	rows, err := m.db.QueryContext(ctx, q)
	if err != nil {
		return "", fmt.Errorf("mysqldrv: SHOW CREATE TABLE: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return "", fmt.Errorf("mysqldrv: table %s.%s not found", d, table)
	}
	var ignored, ddl string
	if err := rows.Scan(&ignored, &ddl); err != nil {
		return "", err
	}
	return ddl, nil
}
