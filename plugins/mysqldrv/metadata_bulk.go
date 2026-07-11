package mysqldrv

// Bulk (whole-schema) metadata reads — the dbdriver.BulkMetadata optional
// extension. Structure sync compares every table of a database; fetching
// columns/indexes/FKs per table costs ~3N information_schema round-trips,
// which dominates wall-clock on remote or tunneled connections. These
// variants drop the TABLE_NAME filter and group results by table, so the
// whole schema is served in three queries total.
//
// The SELECT lists and row mapping deliberately mirror the per-table methods
// in metadata.go — keep both in sync when a column is added there.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

var _ dbdriver.BulkMetadata = metadata{}

func (m metadata) ListAllColumns(ctx context.Context, db, schema string) (map[string][]dbdriver.ColumnMeta, error) {
	d := resolveDB(db, schema)
	if d == "" {
		return nil, fmt.Errorf("mysqldrv: ListAllColumns requires a database name")
	}
	const q = `SELECT TABLE_NAME, COLUMN_NAME, COLUMN_TYPE, DATA_TYPE, IS_NULLABLE, COLUMN_DEFAULT,
	                  IFNULL(CHARACTER_MAXIMUM_LENGTH,0), IFNULL(NUMERIC_PRECISION,0), IFNULL(NUMERIC_SCALE,0),
	                  COLUMN_KEY, EXTRA, IFNULL(COLUMN_COMMENT,'')
	             FROM information_schema.COLUMNS
	            WHERE TABLE_SCHEMA=?
	            ORDER BY TABLE_NAME, ORDINAL_POSITION`
	rows, err := m.db.QueryContext(ctx, q, d)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list all columns: %w", err)
	}
	defer rows.Close()

	dia := dialect{}
	out := map[string][]dbdriver.ColumnMeta{}
	for rows.Next() {
		var (
			table      string
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
		if err := rows.Scan(&table, &c.Name, &columnType, &dataType, &isNullable, &defaultVal,
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
		out[table] = append(out[table], c)
	}
	return out, rows.Err()
}

func (m metadata) ListAllIndexes(ctx context.Context, db, schema string) (map[string][]dbdriver.IndexInfo, error) {
	d := resolveDB(db, schema)
	if d == "" {
		return nil, fmt.Errorf("mysqldrv: ListAllIndexes requires a database name")
	}
	const q = `SELECT TABLE_NAME, INDEX_NAME, COLUMN_NAME, NON_UNIQUE, INDEX_TYPE, SEQ_IN_INDEX, COLLATION, INDEX_COMMENT
	             FROM information_schema.STATISTICS
	            WHERE TABLE_SCHEMA=?
	            ORDER BY TABLE_NAME, INDEX_NAME, SEQ_IN_INDEX`
	rows, err := m.db.QueryContext(ctx, q, d)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list all indexes: %w", err)
	}
	defer rows.Close()

	type key struct{ table, name string }
	byKey := map[key]*dbdriver.IndexInfo{}
	orderPerTable := map[string][]string{}
	for rows.Next() {
		var (
			table     string
			name      string
			column    string
			nonUnique int
			idxType   string
			seq       int
			collation sql.NullString
			comment   sql.NullString
		)
		if err := rows.Scan(&table, &name, &column, &nonUnique, &idxType, &seq, &collation, &comment); err != nil {
			return nil, err
		}
		k := key{table, name}
		ix, ok := byKey[k]
		if !ok {
			ix = &dbdriver.IndexInfo{
				Name:    name,
				Unique:  nonUnique == 0,
				Primary: name == "PRIMARY",
				Type:    idxType,
				Comment: comment.String,
			}
			byKey[k] = ix
			orderPerTable[table] = append(orderPerTable[table], name)
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
	out := map[string][]dbdriver.IndexInfo{}
	for table, names := range orderPerTable {
		list := make([]dbdriver.IndexInfo, 0, len(names))
		for _, n := range names {
			list = append(list, *byKey[key{table, n}])
		}
		out[table] = list
	}
	return out, nil
}

func (m metadata) ListAllForeignKeys(ctx context.Context, db, schema string) (map[string][]dbdriver.ForeignKeyInfo, error) {
	d := resolveDB(db, schema)
	if d == "" {
		return nil, fmt.Errorf("mysqldrv: ListAllForeignKeys requires a database name")
	}
	const q = `SELECT k.TABLE_NAME, k.CONSTRAINT_NAME, k.COLUMN_NAME,
	                  k.REFERENCED_TABLE_SCHEMA, k.REFERENCED_TABLE_NAME, k.REFERENCED_COLUMN_NAME,
	                  IFNULL(r.UPDATE_RULE,''), IFNULL(r.DELETE_RULE,''), k.ORDINAL_POSITION
	             FROM information_schema.KEY_COLUMN_USAGE k
	             LEFT JOIN information_schema.REFERENTIAL_CONSTRAINTS r
	                    ON r.CONSTRAINT_SCHEMA = k.CONSTRAINT_SCHEMA
	                   AND r.CONSTRAINT_NAME   = k.CONSTRAINT_NAME
	            WHERE k.TABLE_SCHEMA=?
	              AND k.REFERENCED_TABLE_NAME IS NOT NULL
	            ORDER BY k.TABLE_NAME, k.CONSTRAINT_NAME, k.ORDINAL_POSITION`
	rows, err := m.db.QueryContext(ctx, q, d)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list all foreign keys: %w", err)
	}
	defer rows.Close()

	type key struct{ table, name string }
	byKey := map[key]*dbdriver.ForeignKeyInfo{}
	orderPerTable := map[string][]string{}
	for rows.Next() {
		var (
			table     string
			name      string
			column    string
			refSchema sql.NullString
			refTable  sql.NullString
			refColumn sql.NullString
			onUpdate  string
			onDelete  string
			ord       int
		)
		if err := rows.Scan(&table, &name, &column, &refSchema, &refTable, &refColumn,
			&onUpdate, &onDelete, &ord); err != nil {
			return nil, err
		}
		k := key{table, name}
		fk, ok := byKey[k]
		if !ok {
			fk = &dbdriver.ForeignKeyInfo{
				Name:             name,
				ReferencedSchema: refSchema.String,
				ReferencedTable:  refTable.String,
				OnUpdate:         onUpdate,
				OnDelete:         onDelete,
			}
			byKey[k] = fk
			orderPerTable[table] = append(orderPerTable[table], name)
		}
		fk.Columns = append(fk.Columns, column)
		fk.ReferencedColumns = append(fk.ReferencedColumns, refColumn.String)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := map[string][]dbdriver.ForeignKeyInfo{}
	for table, names := range orderPerTable {
		list := make([]dbdriver.ForeignKeyInfo, 0, len(names))
		for _, n := range names {
			list = append(list, *byKey[key{table, n}])
		}
		out[table] = list
	}
	return out, nil
}
