package mysqldrv

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

// editor implements dbdriver.Editor for MySQL.
//
// Safety rules (CLAUDE.md #4):
//   - UPDATE/DELETE MUST be keyed on the PK (or chosen unique key); no
//     unkeyed writes — those would fan out on shared schemas.
//   - PrimaryKeys returns the primary key, or — if absent — the first unique
//     non-null index. Tables that have *neither* surface an empty slice, and
//     the calling Service treats them as read-only.
//   - All Build* methods produce parameterized SQL (placeholders + args),
//     never string-formatted user values.
type editor struct {
	db      *sql.DB
	dialect dialect
}

// PrimaryKeys returns the table's primary-key columns in ordinal order, or
// the first unique non-null index if no PK exists. Returns ([], nil) when
// the table has no usable key — that's the read-only signal.
func (e editor) PrimaryKeys(ctx context.Context, db, schema, table string) ([]string, error) {
	d := resolveDB(db, schema)
	if d == "" || table == "" {
		return nil, fmt.Errorf("mysqldrv: PrimaryKeys requires db and table")
	}
	cols, err := primaryKeyColumns(ctx, e.db, d, table)
	if err != nil {
		return nil, err
	}
	if len(cols) > 0 {
		return cols, nil
	}
	// Fall back to a unique non-null index. Returns ("", nil) if none exists.
	cols, err = firstUniqueIndex(ctx, e.db, d, table)
	if err != nil {
		return nil, err
	}
	return cols, nil
}

func primaryKeyColumns(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	const q = `SELECT COLUMN_NAME
	             FROM information_schema.KEY_COLUMN_USAGE
	            WHERE TABLE_SCHEMA=? AND TABLE_NAME=? AND CONSTRAINT_NAME='PRIMARY'
	            ORDER BY ORDINAL_POSITION`
	rows, err := db.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: pk columns: %w", err)
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

// firstUniqueIndex picks one unique, non-null index keyed on the smallest
// number of columns; ties broken alphabetically. Returns nil when none exists.
func firstUniqueIndex(ctx context.Context, db *sql.DB, schema, table string) ([]string, error) {
	const q = `SELECT s.INDEX_NAME, s.COLUMN_NAME, s.SEQ_IN_INDEX, c.IS_NULLABLE
	             FROM information_schema.STATISTICS s
	             JOIN information_schema.COLUMNS c
	               ON c.TABLE_SCHEMA=s.TABLE_SCHEMA
	              AND c.TABLE_NAME=s.TABLE_NAME
	              AND c.COLUMN_NAME=s.COLUMN_NAME
	            WHERE s.TABLE_SCHEMA=? AND s.TABLE_NAME=? AND s.NON_UNIQUE=0
	            ORDER BY s.INDEX_NAME, s.SEQ_IN_INDEX`
	rows, err := db.QueryContext(ctx, q, schema, table)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: unique index lookup: %w", err)
	}
	defer rows.Close()

	type entry struct {
		cols    []string
		anyNull bool
	}
	indexes := make(map[string]*entry)
	var order []string
	for rows.Next() {
		var (
			name, column string
			seq          int
			nullable     string
		)
		if err := rows.Scan(&name, &column, &seq, &nullable); err != nil {
			return nil, err
		}
		ix, ok := indexes[name]
		if !ok {
			ix = &entry{}
			indexes[name] = ix
			order = append(order, name)
		}
		ix.cols = append(ix.cols, column)
		if strings.EqualFold(nullable, "YES") {
			ix.anyNull = true
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Filter to fully NOT NULL unique indexes, then sort: shortest first,
	// alphabetical tiebreak.
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

// BuildInsert constructs an INSERT into `table` using ordered column names
// from the supplied row. Map iteration is unstable so we sort the column
// list for deterministic SQL (helps tests + logs).
func (e editor) BuildInsert(table string, row map[string]any) (string, []any, error) {
	if table == "" {
		return "", nil, fmt.Errorf("mysqldrv: BuildInsert table is empty")
	}
	if len(row) == 0 {
		return "", nil, fmt.Errorf("mysqldrv: BuildInsert row is empty")
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
		quoteTable(e.dialect, table),
		strings.Join(quoted, ", "),
		strings.Join(placeholders, ", "),
	)
	return sqlText, args, nil
}

// BuildUpdate constructs an UPDATE keyed on pk. Both maps must be non-empty.
func (e editor) BuildUpdate(table string, pk, changes map[string]any) (string, []any, error) {
	if table == "" {
		return "", nil, fmt.Errorf("mysqldrv: BuildUpdate table is empty")
	}
	if len(pk) == 0 {
		return "", nil, fmt.Errorf("mysqldrv: BuildUpdate requires a primary key (refusing to issue keyless UPDATE)")
	}
	if len(changes) == 0 {
		return "", nil, fmt.Errorf("mysqldrv: BuildUpdate has no changes")
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
		quoteTable(e.dialect, table),
		strings.Join(setClauses, ", "),
		strings.Join(whereClauses, " AND "),
	)
	return sqlText, args, nil
}

// BuildDelete constructs a DELETE keyed on pk. Refuses an empty pk.
func (e editor) BuildDelete(table string, pk map[string]any) (string, []any, error) {
	if table == "" {
		return "", nil, fmt.Errorf("mysqldrv: BuildDelete table is empty")
	}
	if len(pk) == 0 {
		return "", nil, fmt.Errorf("mysqldrv: BuildDelete requires a primary key (refusing to issue keyless DELETE)")
	}
	whereClauses, whereArgs := pkWhere(e.dialect, pk)
	sqlText := fmt.Sprintf("DELETE FROM %s WHERE %s",
		quoteTable(e.dialect, table),
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

// quoteTable handles both "table" and "db.table" forms.
func quoteTable(d dialect, table string) string {
	if i := strings.Index(table, "."); i > 0 {
		left := table[:i]
		right := table[i+1:]
		return d.QuoteIdentifier(left) + "." + d.QuoteIdentifier(right)
	}
	return d.QuoteIdentifier(table)
}
