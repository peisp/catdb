package dmdrv

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// metadata implements dbdriver.Metadata against DM's Oracle-compatible
// dictionary views (ALL_TABLES / ALL_TAB_COLUMNS / ALL_CONSTRAINTS / …) plus
// the native SYSOBJECTS/SYSCOLUMNS tables where the compat views fall short
// (schema list, identity flag).
//
// DM has one database per instance and any schema is addressable from one
// session, so — like MySQL — the schema level is collapsed into the database
// position (Capabilities.Schemas=false): ListDatabases returns the schemas.
type metadata struct {
	db *sql.DB
}

// resolveSchema accepts the namespace from either position (db or schema) —
// whichever the caller supplied — mirroring mysqldrv's resolveDB.
func resolveSchema(db, schema string) string {
	if schema != "" {
		return schema
	}
	return db
}

func (m metadata) ListDatabases(ctx context.Context) ([]string, error) {
	// Schemas are the navigable namespace level (see type comment). One user
	// can own several schemas, so SYSOBJECTS — not ALL_USERS — is the source.
	const q = `SELECT NAME FROM SYSOBJECTS WHERE TYPE$ = 'SCH' ORDER BY NAME`
	rows, err := m.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list schemas: %w", err)
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
	// The schema level is collapsed into the database position.
	return nil, nil
}

func (m metadata) ListTables(ctx context.Context, db, schema string) ([]dbdriver.TableInfo, error) {
	s := resolveSchema(db, schema)
	if s == "" {
		return nil, fmt.Errorf("dmdrv: ListTables requires a schema name")
	}
	const q = `SELECT t.TABLE_NAME,
	                  NVL(c.COMMENTS, ''),
	                  NVL(t.NUM_ROWS, 0),
	                  NVL(CAST(o.CREATED AS VARCHAR), ''),
	                  NVL(CAST(o.LAST_DDL_TIME AS VARCHAR), '')
	             FROM ALL_TABLES t
	             LEFT JOIN ALL_TAB_COMMENTS c
	               ON c.OWNER = t.OWNER AND c.TABLE_NAME = t.TABLE_NAME AND c.TABLE_TYPE = 'TABLE'
	             LEFT JOIN ALL_OBJECTS o
	               ON o.OWNER = t.OWNER AND o.OBJECT_NAME = t.TABLE_NAME AND o.OBJECT_TYPE = 'TABLE'
	            WHERE t.OWNER = ?
	            ORDER BY t.TABLE_NAME`
	rows, err := m.db.QueryContext(ctx, q, s)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list tables: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.TableInfo
	for rows.Next() {
		var t dbdriver.TableInfo
		t.Schema = s
		if err := rows.Scan(&t.Name, &t.Comment, &t.Rows, &t.CreateTime, &t.UpdateTime); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (m metadata) ListViews(ctx context.Context, db, schema string) ([]dbdriver.ViewInfo, error) {
	s := resolveSchema(db, schema)
	if s == "" {
		return nil, fmt.Errorf("dmdrv: ListViews requires a schema name")
	}
	const q = `SELECT v.VIEW_NAME, NVL(c.COMMENTS, '')
	             FROM ALL_VIEWS v
	             LEFT JOIN ALL_TAB_COMMENTS c
	               ON c.OWNER = v.OWNER AND c.TABLE_NAME = v.VIEW_NAME AND c.TABLE_TYPE = 'VIEW'
	            WHERE v.OWNER = ?
	            ORDER BY v.VIEW_NAME`
	rows, err := m.db.QueryContext(ctx, q, s)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list views: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.ViewInfo
	for rows.Next() {
		var v dbdriver.ViewInfo
		v.Schema = s
		if err := rows.Scan(&v.Name, &v.Comment); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

func (m metadata) ListViewDefinitions(ctx context.Context, db, schema string) (map[string]string, error) {
	s := resolveSchema(db, schema)
	if s == "" {
		return nil, fmt.Errorf("dmdrv: ListViewDefinitions requires a schema name")
	}
	const q = `SELECT VIEW_NAME, TEXT FROM ALL_VIEWS WHERE OWNER = ?`
	rows, err := m.db.QueryContext(ctx, q, s)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list view definitions: %w", err)
	}
	defer rows.Close()
	defs := map[string]string{}
	for rows.Next() {
		var name string
		var def sql.NullString
		if err := rows.Scan(&name, &def); err != nil {
			return nil, err
		}
		defs[name] = def.String
	}
	return defs, rows.Err()
}

// columnsQuery serves both the per-table and bulk variants; the trailing
// filter and ORDER BY are appended by the caller. The identity flag comes
// from the native SYSCOLUMNS.INFO2 bit 0x01 — the Oracle-compat views don't
// carry it.
const columnsQuery = `SELECT c.TABLE_NAME,
       c.COLUMN_NAME,
       c.DATA_TYPE,
       NVL(c.DATA_LENGTH, 0),
       NVL(c.DATA_PRECISION, 0),
       NVL(c.DATA_SCALE, 0),
       c.NULLABLE,
       c.DATA_DEFAULT,
       NVL(cm.COMMENTS, ''),
       CASE WHEN pk.COLUMN_NAME IS NULL THEN 0 ELSE 1 END,
       CASE WHEN ident.NAME IS NULL THEN 0 ELSE 1 END
  FROM ALL_TAB_COLUMNS c
  LEFT JOIN ALL_COL_COMMENTS cm
    ON cm.OWNER = c.OWNER AND cm.TABLE_NAME = c.TABLE_NAME AND cm.COLUMN_NAME = c.COLUMN_NAME
  LEFT JOIN (SELECT cc.TABLE_NAME, cc.COLUMN_NAME
               FROM ALL_CONSTRAINTS ac
               JOIN ALL_CONS_COLUMNS cc
                 ON cc.OWNER = ac.OWNER AND cc.CONSTRAINT_NAME = ac.CONSTRAINT_NAME AND cc.TABLE_NAME = ac.TABLE_NAME
              WHERE ac.OWNER = ? AND ac.CONSTRAINT_TYPE = 'P') pk
    ON pk.TABLE_NAME = c.TABLE_NAME AND pk.COLUMN_NAME = c.COLUMN_NAME
  LEFT JOIN (SELECT tab.NAME AS TABLE_NAME, col.NAME
               FROM SYSCOLUMNS col
               JOIN SYSOBJECTS tab ON col.ID = tab.ID AND tab.TYPE$ = 'SCHOBJ'
               JOIN SYSOBJECTS sch ON tab.SCHID = sch.ID AND sch.TYPE$ = 'SCH'
              WHERE sch.NAME = ? AND col.INFO2 & 0x01 = 0x01) ident
    ON ident.TABLE_NAME = c.TABLE_NAME AND ident.NAME = c.COLUMN_NAME
 WHERE c.OWNER = ?`

func (m metadata) ListColumns(ctx context.Context, db, schema, table string) ([]dbdriver.ColumnMeta, error) {
	s := resolveSchema(db, schema)
	if s == "" || table == "" {
		return nil, fmt.Errorf("dmdrv: ListColumns requires schema and table")
	}
	rows, err := m.db.QueryContext(ctx, columnsQuery+` AND c.TABLE_NAME = ? ORDER BY c.COLUMN_ID`,
		s, s, s, table)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list columns: %w", err)
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

// scanColumn maps one columnsQuery row. The native type is recomposed from
// the bare dictionary name plus length/precision params (see composeType).
func scanColumn(rows *sql.Rows) (table string, c dbdriver.ColumnMeta, err error) {
	var (
		dataType   string
		nullable   string
		defaultVal sql.NullString
		primary    int
		identity   int
	)
	if err = rows.Scan(&table, &c.Name, &dataType, &c.Length, &c.Precision, &c.Scale,
		&nullable, &defaultVal, &c.Comment, &primary, &identity); err != nil {
		return "", c, err
	}
	c.NativeType = composeType(dataType, c.Length, c.Precision, c.Scale)
	c.LogicalType = dialect{}.MapType(dataType)
	c.Nullable = strings.EqualFold(nullable, "Y")
	c.IsPrimaryKey = primary == 1
	c.IsAutoIncrement = identity == 1
	if defaultVal.Valid && !c.IsAutoIncrement {
		if v := strings.TrimSpace(defaultVal.String); v != "" {
			c.Default = &v
		}
	}
	return table, c, nil
}

// composeType rebuilds the parameterized native type from ALL_TAB_COLUMNS'
// bare DATA_TYPE plus DATA_LENGTH/PRECISION/SCALE — char/binary types carry
// a length, exact numerics carry precision[,scale]. Types the dictionary
// already reports parameterized ("TIMESTAMP(6)") pass through untouched.
func composeType(dataType string, length, precision, scale int64) string {
	t := strings.ToUpper(strings.TrimSpace(dataType))
	if strings.Contains(t, "(") {
		return t
	}
	switch t {
	case "CHAR", "CHARACTER", "VARCHAR", "VARCHAR2", "NCHAR", "NVARCHAR", "NVARCHAR2",
		"BINARY", "VARBINARY", "RAW":
		if length > 0 {
			return fmt.Sprintf("%s(%d)", t, length)
		}
	case "NUMBER", "NUMERIC", "DECIMAL", "DEC":
		if precision > 0 && scale > 0 {
			return fmt.Sprintf("%s(%d,%d)", t, precision, scale)
		}
		if precision > 0 {
			return fmt.Sprintf("%s(%d)", t, precision)
		}
	}
	return t
}

// indexesQuery serves both the per-table and bulk variants. The PK flag is
// resolved by matching the constraint's backing index name; INDEX_TYPE
// NORMAL folds to BTREE so schemadiff's default-type rule holds.
const indexesQuery = `SELECT i.TABLE_NAME,
       i.INDEX_NAME,
       CASE WHEN i.UNIQUENESS = 'UNIQUE' THEN 1 ELSE 0 END,
       CASE WHEN pk.INDEX_NAME IS NULL THEN 0 ELSE 1 END,
       NVL(i.INDEX_TYPE, ''),
       ic.COLUMN_NAME,
       NVL(ic.DESCEND, '')
  FROM ALL_INDEXES i
  JOIN ALL_IND_COLUMNS ic
    ON ic.INDEX_OWNER = i.OWNER AND ic.INDEX_NAME = i.INDEX_NAME AND ic.TABLE_NAME = i.TABLE_NAME
  LEFT JOIN (SELECT TABLE_NAME, INDEX_NAME
               FROM ALL_CONSTRAINTS
              WHERE OWNER = ? AND CONSTRAINT_TYPE = 'P') pk
    ON pk.TABLE_NAME = i.TABLE_NAME AND pk.INDEX_NAME = i.INDEX_NAME
 WHERE i.TABLE_OWNER = ?`

func (m metadata) ListIndexes(ctx context.Context, db, schema, table string) ([]dbdriver.IndexInfo, error) {
	s := resolveSchema(db, schema)
	if s == "" || table == "" {
		return nil, fmt.Errorf("dmdrv: ListIndexes requires schema and table")
	}
	rows, err := m.db.QueryContext(ctx, indexesQuery+` AND i.TABLE_NAME = ? ORDER BY i.INDEX_NAME, ic.COLUMN_POSITION`,
		s, s, table)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list indexes: %w", err)
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

func scanIndexes(rows *sql.Rows) (map[tableKey]*dbdriver.IndexInfo, []tableKey, error) {
	defer rows.Close()
	byKey := map[tableKey]*dbdriver.IndexInfo{}
	var order []tableKey
	for rows.Next() {
		var (
			table   string
			name    string
			unique  int
			primary int
			idxType string
			column  string
			descend string
		)
		if err := rows.Scan(&table, &name, &unique, &primary, &idxType, &column, &descend); err != nil {
			return nil, nil, err
		}
		k := tableKey{table, name}
		ix, ok := byKey[k]
		if !ok {
			if strings.EqualFold(idxType, "NORMAL") || idxType == "" {
				idxType = "BTREE"
			}
			ix = &dbdriver.IndexInfo{
				Name:    name,
				Unique:  unique == 1,
				Primary: primary == 1,
				Type:    strings.ToUpper(idxType),
			}
			byKey[k] = ix
			order = append(order, k)
		}
		dir := "ASC"
		if strings.EqualFold(descend, "DESC") {
			dir = "DESC"
		}
		ix.Columns = append(ix.Columns, dbdriver.IndexColumn{Name: column, Order: dir})
	}
	return byKey, order, rows.Err()
}

// foreignKeysQuery serves both the per-table and bulk variants. DM's
// ALL_CONSTRAINTS carries only DELETE_RULE (Oracle shape) — ON UPDATE is not
// surfaced. "NO ACTION" maps to "" so schemadiff's "empty ≅ default" holds.
const foreignKeysQuery = `SELECT a.TABLE_NAME,
       a.CONSTRAINT_NAME,
       ac.COLUMN_NAME,
       r.OWNER,
       r.TABLE_NAME,
       rc.COLUMN_NAME,
       NVL(a.DELETE_RULE, '')
  FROM ALL_CONSTRAINTS a
  JOIN ALL_CONS_COLUMNS ac
    ON ac.OWNER = a.OWNER AND ac.CONSTRAINT_NAME = a.CONSTRAINT_NAME AND ac.TABLE_NAME = a.TABLE_NAME
  JOIN ALL_CONSTRAINTS r
    ON r.OWNER = a.R_OWNER AND r.CONSTRAINT_NAME = a.R_CONSTRAINT_NAME
  JOIN ALL_CONS_COLUMNS rc
    ON rc.OWNER = r.OWNER AND rc.CONSTRAINT_NAME = r.CONSTRAINT_NAME AND rc.POSITION = ac.POSITION
 WHERE a.CONSTRAINT_TYPE = 'R' AND a.OWNER = ?`

func (m metadata) ListForeignKeys(ctx context.Context, db, schema, table string) ([]dbdriver.ForeignKeyInfo, error) {
	s := resolveSchema(db, schema)
	if s == "" || table == "" {
		return nil, fmt.Errorf("dmdrv: ListForeignKeys requires schema and table")
	}
	rows, err := m.db.QueryContext(ctx, foreignKeysQuery+` AND a.TABLE_NAME = ? ORDER BY a.CONSTRAINT_NAME, ac.POSITION`,
		s, table)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list foreign keys: %w", err)
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

func scanForeignKeys(rows *sql.Rows) (map[tableKey]*dbdriver.ForeignKeyInfo, []tableKey, error) {
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
			onDelete  string
		)
		if err := rows.Scan(&table, &name, &column, &refSchema, &refTable, &refColumn, &onDelete); err != nil {
			return nil, nil, err
		}
		k := tableKey{table, name}
		fk, ok := byKey[k]
		if !ok {
			fk = &dbdriver.ForeignKeyInfo{
				Name:             name,
				ReferencedSchema: refSchema,
				ReferencedTable:  refTable,
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

// fkAction folds the dictionary's default NO ACTION onto "".
func fkAction(rule string) string {
	u := strings.ToUpper(strings.TrimSpace(rule))
	if u == "" || u == "NO ACTION" {
		return ""
	}
	return u
}

// ListRoutines serves procedures, functions and triggers from ALL_SOURCE in
// one query — line-ordered source text is concatenated per object.
func (m metadata) ListRoutines(ctx context.Context, db, schema string) ([]dbdriver.RoutineInfo, error) {
	s := resolveSchema(db, schema)
	if s == "" {
		return nil, fmt.Errorf("dmdrv: ListRoutines requires a schema name")
	}
	const q = `SELECT NAME, TYPE, TEXT
	             FROM ALL_SOURCE
	            WHERE OWNER = ? AND TYPE IN ('PROCEDURE', 'FUNCTION', 'TRIGGER')
	            ORDER BY TYPE, NAME, LINE`
	rows, err := m.db.QueryContext(ctx, q, s)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: list routines: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.RoutineInfo
	for rows.Next() {
		var name, typ string
		var text sql.NullString
		if err := rows.Scan(&name, &typ, &text); err != nil {
			return nil, err
		}
		typ = strings.ToUpper(strings.TrimSpace(typ))
		if n := len(out); n > 0 && out[n-1].Name == name && out[n-1].Type == typ {
			out[n-1].Definition += text.String
			continue
		}
		out = append(out, dbdriver.RoutineInfo{
			Name:       name,
			Schema:     s,
			Type:       typ,
			Definition: text.String,
		})
	}
	return out, rows.Err()
}

// GetCreateTable returns DM's native CREATE TABLE text via the TABLEDEF
// system function (reflects the latest ALTERs).
func (m metadata) GetCreateTable(ctx context.Context, db, schema, table string) (string, error) {
	s := resolveSchema(db, schema)
	if s == "" || table == "" {
		return "", fmt.Errorf("dmdrv: GetCreateTable requires schema and table")
	}
	var ddl string
	if err := m.db.QueryRowContext(ctx, "SELECT TABLEDEF(?, ?)", s, table).Scan(&ddl); err != nil {
		return "", fmt.Errorf("dmdrv: TABLEDEF: %w", err)
	}
	return ddl, nil
}
