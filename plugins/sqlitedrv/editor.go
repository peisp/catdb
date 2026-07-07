package sqlitedrv

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	"catdb/internal/dbdriver"
)

// editor implements dbdriver.Editor for SQLite. Same safety rules as the
// other drivers (CLAUDE.md #4): UPDATE/DELETE keyed on the PK or a unique
// non-null index, parameterized SQL only, no key → read-only.
type editor struct {
	db      *sql.DB
	dialect dialect
}

func (e editor) PrimaryKeys(ctx context.Context, db, schema, table string) ([]string, error) {
	d := resolveDB(db, schema)
	if table == "" {
		return nil, fmt.Errorf("sqlitedrv: PrimaryKeys requires a table")
	}
	m := metadata{db: e.db}
	cols, err := m.primaryKeyColumns(ctx, d, table)
	if err != nil {
		return nil, err
	}
	if len(cols) > 0 {
		return cols, nil
	}
	return e.firstUniqueIndex(ctx, d, table)
}

// firstUniqueIndex picks one full (non-partial) unique index whose columns are
// all NOT NULL, preferring the fewest columns, name as tiebreak. Returns
// (nil, nil) when none qualifies — the read-only signal.
func (e editor) firstUniqueIndex(ctx context.Context, d, table string) ([]string, error) {
	notNull := map[string]bool{}
	const colQ = `SELECT name, "notnull" FROM pragma_table_info(?, ?)`
	rows, err := e.db.QueryContext(ctx, colQ, table, d)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: unique index lookup: %w", err)
	}
	for rows.Next() {
		var name string
		var nn int
		if err := rows.Scan(&name, &nn); err != nil {
			rows.Close()
			return nil, err
		}
		notNull[name] = nn == 1
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	const ixQ = `SELECT name FROM pragma_index_list(?, ?) WHERE "unique"=1 AND partial=0 ORDER BY name`
	ixRows, err := e.db.QueryContext(ctx, ixQ, table, d)
	if err != nil {
		return nil, fmt.Errorf("sqlitedrv: unique index lookup: %w", err)
	}
	var names []string
	for ixRows.Next() {
		var n string
		if err := ixRows.Scan(&n); err != nil {
			ixRows.Close()
			return nil, err
		}
		names = append(names, n)
	}
	if err := ixRows.Err(); err != nil {
		ixRows.Close()
		return nil, err
	}
	ixRows.Close()

	m := metadata{db: e.db}
	type candidate struct {
		name string
		cols []string
	}
	var candidates []candidate
	for _, n := range names {
		ixCols, err := m.indexColumns(ctx, d, n)
		if err != nil {
			return nil, err
		}
		usable := len(ixCols) > 0
		cols := make([]string, 0, len(ixCols))
		for _, c := range ixCols {
			if c.Name == "" || !notNull[c.Name] {
				usable = false
				break
			}
			cols = append(cols, c.Name)
		}
		if usable {
			candidates = append(candidates, candidate{name: n, cols: cols})
		}
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

func (e editor) BuildInsert(db, schema, table string, row map[string]any) (string, []any, error) {
	if table == "" {
		return "", nil, fmt.Errorf("sqlitedrv: BuildInsert table is empty")
	}
	if len(row) == 0 {
		return "", nil, fmt.Errorf("sqlitedrv: BuildInsert row is empty")
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

func (e editor) BuildUpdate(db, schema, table string, pk, changes map[string]any) (string, []any, error) {
	if table == "" {
		return "", nil, fmt.Errorf("sqlitedrv: BuildUpdate table is empty")
	}
	if len(pk) == 0 {
		return "", nil, fmt.Errorf("sqlitedrv: BuildUpdate requires a primary key (refusing to issue keyless UPDATE)")
	}
	if len(changes) == 0 {
		return "", nil, fmt.Errorf("sqlitedrv: BuildUpdate has no changes")
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

func (e editor) BuildDelete(db, schema, table string, pk map[string]any) (string, []any, error) {
	if table == "" {
		return "", nil, fmt.Errorf("sqlitedrv: BuildDelete table is empty")
	}
	if len(pk) == 0 {
		return "", nil, fmt.Errorf("sqlitedrv: BuildDelete requires a primary key (refusing to issue keyless DELETE)")
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

func (e editor) qualify(db, schema, table string) string {
	return dbdriver.QualifyTable(e.dialect, resolveDB(db, schema), "", table)
}
