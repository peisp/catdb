package dmdrv

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"catdb/internal/dbdriver"
)

// editor implements dbdriver.Editor for DM.
//
// Safety rules (CLAUDE.md #4) match mysqldrv: UPDATE/DELETE are keyed on the
// PK (or chosen unique index); tables with neither surface an empty key list
// and are treated read-only by the core layer. All Build* output is
// parameterized with ? placeholders.
type editor struct {
	db      *sql.DB
	dialect dialect
}

// PrimaryKeys returns the table's primary-key columns in key order, or the
// best unique index (fully NOT NULL, fewest columns, name tiebreak) if no PK
// exists. ([], nil) → read-only.
func (e editor) PrimaryKeys(ctx context.Context, db, schema, table string) ([]string, error) {
	s := resolveSchema(db, schema)
	if s == "" || table == "" {
		return nil, fmt.Errorf("dmdrv: PrimaryKeys requires schema and table")
	}
	cols, err := primaryKeyColumns(ctx, e.db, s, table)
	if err != nil {
		return nil, err
	}
	if len(cols) > 0 {
		return cols, nil
	}
	return firstUniqueIndex(ctx, e.db, s, table)
}

func primaryKeyColumns(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	const q = `SELECT cc.COLUMN_NAME
	             FROM ALL_CONSTRAINTS c
	             JOIN ALL_CONS_COLUMNS cc
	               ON cc.OWNER = c.OWNER AND cc.CONSTRAINT_NAME = c.CONSTRAINT_NAME AND cc.TABLE_NAME = c.TABLE_NAME
	            WHERE c.OWNER = ? AND c.TABLE_NAME = ? AND c.CONSTRAINT_TYPE = 'P'
	            ORDER BY cc.POSITION`
	rows, err := db.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: pk columns: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// firstUniqueIndex picks one unique index whose key columns are all NOT NULL,
// keyed on the smallest number of columns; ties broken alphabetically.
// Returns (nil, nil) when none exists.
func firstUniqueIndex(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	const q = `SELECT i.INDEX_NAME, ic.COLUMN_NAME, tc.NULLABLE
	             FROM ALL_INDEXES i
	             JOIN ALL_IND_COLUMNS ic
	               ON ic.INDEX_OWNER = i.OWNER AND ic.INDEX_NAME = i.INDEX_NAME
	             JOIN ALL_TAB_COLUMNS tc
	               ON tc.OWNER = i.TABLE_OWNER AND tc.TABLE_NAME = i.TABLE_NAME AND tc.COLUMN_NAME = ic.COLUMN_NAME
	            WHERE i.TABLE_OWNER = ? AND i.TABLE_NAME = ? AND i.UNIQUENESS = 'UNIQUE'
	            ORDER BY i.INDEX_NAME, ic.COLUMN_POSITION`
	rows, err := db.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, fmt.Errorf("dmdrv: unique index lookup: %w", err)
	}
	defer rows.Close()

	type entry struct {
		cols    []string
		anyNull bool
	}
	indexes := make(map[string]*entry)
	var order []string
	for rows.Next() {
		var name, column, nullable string
		if err := rows.Scan(&name, &column, &nullable); err != nil {
			return nil, err
		}
		ix, ok := indexes[name]
		if !ok {
			ix = &entry{}
			indexes[name] = ix
			order = append(order, name)
		}
		ix.cols = append(ix.cols, column)
		if strings.EqualFold(nullable, "Y") {
			ix.anyNull = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var candidates []struct {
		name string
		cols []string
	}
	for _, n := range order {
		ix := indexes[n]
		if ix.anyNull {
			continue
		}
		candidates = append(candidates, struct {
			name string
			cols []string
		}{n, ix.cols})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if len(candidates[i].cols) != len(candidates[j].cols) {
			return len(candidates[i].cols) < len(candidates[j].cols)
		}
		return candidates[i].name < candidates[j].name
	})
	if len(candidates) == 0 {
		return nil, nil
	}
	return candidates[0].cols, nil
}

// BuildInsert constructs an INSERT with sorted column order (map iteration
// is unstable) and ? placeholders.
func (e editor) BuildInsert(db, schema, table string, row map[string]any) (string, []any, error) {
	if table == "" {
		return "", nil, fmt.Errorf("dmdrv: BuildInsert table is empty")
	}
	if len(row) == 0 {
		return "", nil, fmt.Errorf("dmdrv: BuildInsert row is empty")
	}
	cols := mapKeysSorted(row)
	args := make([]any, 0, len(cols))
	quoted := make([]string, 0, len(cols))
	placeholders := make([]string, 0, len(cols))
	for _, c := range cols {
		quoted = append(quoted, e.dialect.QuoteIdentifier(c))
		placeholders = append(placeholders, "?")
		args = append(args, row[c])
	}
	sqlText := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)",
		e.qualify(db, schema, table),
		strings.Join(quoted, ", "),
		strings.Join(placeholders, ", "),
	)
	return sqlText, args, nil
}

// BuildUpdate constructs an UPDATE keyed on pk. Both maps must be non-empty.
func (e editor) BuildUpdate(db, schema, table string, pk, changes map[string]any) (string, []any, error) {
	if table == "" {
		return "", nil, fmt.Errorf("dmdrv: BuildUpdate table is empty")
	}
	if len(pk) == 0 {
		return "", nil, fmt.Errorf("dmdrv: BuildUpdate requires a primary key (refusing to issue keyless UPDATE)")
	}
	if len(changes) == 0 {
		return "", nil, fmt.Errorf("dmdrv: BuildUpdate has no changes")
	}
	changeCols := mapKeysSorted(changes)
	setClauses := make([]string, 0, len(changeCols))
	args := make([]any, 0, len(changeCols)+len(pk))
	for _, c := range changeCols {
		setClauses = append(setClauses, fmt.Sprintf("%s = ?", e.dialect.QuoteIdentifier(c)))
		args = append(args, changes[c])
	}
	whereClauses, whereArgs := pkWhere(e.dialect, pk)
	args = append(args, whereArgs...)

	sqlText := fmt.Sprintf("UPDATE %s SET %s WHERE %s",
		e.qualify(db, schema, table),
		strings.Join(setClauses, ", "),
		strings.Join(whereClauses, " AND "),
	)
	return sqlText, args, nil
}

// BuildDelete constructs a DELETE keyed on pk. Refuses an empty pk.
func (e editor) BuildDelete(db, schema, table string, pk map[string]any) (string, []any, error) {
	if table == "" {
		return "", nil, fmt.Errorf("dmdrv: BuildDelete table is empty")
	}
	if len(pk) == 0 {
		return "", nil, fmt.Errorf("dmdrv: BuildDelete requires a primary key (refusing to issue keyless DELETE)")
	}
	whereClauses, whereArgs := pkWhere(e.dialect, pk)
	sqlText := fmt.Sprintf("DELETE FROM %s WHERE %s",
		e.qualify(db, schema, table),
		strings.Join(whereClauses, " AND "),
	)
	return sqlText, whereArgs, nil
}

// --- helpers ---

func mapKeysSorted(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func pkWhere(d dialect, pk map[string]any) ([]string, []any) {
	cols := mapKeysSorted(pk)
	clauses := make([]string, 0, len(cols))
	args := make([]any, 0, len(cols))
	for _, c := range cols {
		v := pk[c]
		if v == nil {
			clauses = append(clauses, fmt.Sprintf("%s IS NULL", d.QuoteIdentifier(c)))
			continue
		}
		clauses = append(clauses, fmt.Sprintf("%s = ?", d.QuoteIdentifier(c)))
		args = append(args, v)
	}
	return clauses, args
}

// qualify renders the quoted table reference. DM collapses schema into the
// database level (resolveSchema), so the result is "schema"."table" or "table".
func (e editor) qualify(db, schema, table string) string {
	return dbdriver.QualifyTable(e.dialect, resolveSchema(db, schema), "", table)
}
