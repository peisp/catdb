package postgresdrv

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"catdb/internal/dbdriver"
)

// editor implements dbdriver.Editor for PostgreSQL.
//
// Safety rules (CLAUDE.md #4) match mysqldrv: UPDATE/DELETE are keyed on the
// PK (or chosen unique index); tables with neither surface an empty key list
// and are treated read-only by the core layer. All Build* output is
// parameterized with $n placeholders.
type editor struct {
	c       *connection
	dialect dialect
}

// PrimaryKeys returns the table's primary-key columns in key order, or the
// best unique index (fully NOT NULL, no expressions/predicates, fewest
// columns, name tiebreak) if no PK exists. ([], nil) → read-only.
func (e editor) PrimaryKeys(ctx context.Context, db, schema, table string) ([]string, error) {
	ns := resolveSchema(db, schema)
	if table == "" {
		return nil, fmt.Errorf("postgresdrv: PrimaryKeys requires a table")
	}
	pool, err := e.c.poolFor(ctx, db)
	if err != nil {
		return nil, err
	}
	const q = `SELECT a.attname
	             FROM pg_index i
	             JOIN pg_class c ON c.oid = i.indrelid
	             JOIN pg_namespace n ON n.oid = c.relnamespace
	             CROSS JOIN LATERAL unnest(i.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
	             JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
	            WHERE n.nspname = $1 AND c.relname = $2 AND i.indisprimary
	            ORDER BY k.ord`
	rows, err := pool.Query(ctx, q, ns, table)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: pk columns: %w", err)
	}
	cols, err := scanStrings(rows)
	if err != nil {
		return nil, err
	}
	if len(cols) > 0 {
		return cols, nil
	}
	return e.firstUniqueIndex(ctx, pool, ns, table)
}

// firstUniqueIndex picks one valid unique index whose key columns are all
// NOT NULL plain columns. Partial (indpred) and expression (indexprs)
// indexes are excluded — they don't identify a single row unconditionally.
func (e editor) firstUniqueIndex(ctx context.Context, pool *pgxpool.Pool, ns, table string) ([]string, error) {
	const q = `SELECT i.relname, a.attname, a.attnotnull
	             FROM pg_index ix
	             JOIN pg_class i ON i.oid = ix.indexrelid
	             JOIN pg_class c ON c.oid = ix.indrelid
	             JOIN pg_namespace n ON n.oid = c.relnamespace
	             CROSS JOIN LATERAL unnest(ix.indkey::int2[]) WITH ORDINALITY AS k(attnum, ord)
	             LEFT JOIN pg_attribute a ON a.attrelid = c.oid AND a.attnum = k.attnum
	            WHERE n.nspname = $1 AND c.relname = $2
	              AND ix.indisunique AND NOT ix.indisprimary
	              AND ix.indpred IS NULL AND ix.indexprs IS NULL AND ix.indisvalid
	            ORDER BY i.relname, k.ord`
	rows, err := pool.Query(ctx, q, ns, table)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: unique index lookup: %w", err)
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
			name    string
			column  *string
			notNull *bool
		)
		if err := rows.Scan(&name, &column, &notNull); err != nil {
			return nil, err
		}
		ix, ok := indexes[name]
		if !ok {
			ix = &entry{}
			indexes[name] = ix
			order = append(order, name)
		}
		if column == nil || notNull == nil || !*notNull {
			ix.anyNull = true
			continue
		}
		ix.cols = append(ix.cols, *column)
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
		if ix.anyNull || len(ix.cols) == 0 {
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
// is unstable) and $n placeholders.
func (e editor) BuildInsert(db, schema, table string, row map[string]any) (string, []any, error) {
	if table == "" {
		return "", nil, fmt.Errorf("postgresdrv: BuildInsert table is empty")
	}
	if len(row) == 0 {
		return "", nil, fmt.Errorf("postgresdrv: BuildInsert row is empty")
	}
	cols := mapKeysSorted(row)
	args := make([]any, 0, len(cols))
	quoted := make([]string, 0, len(cols))
	placeholders := make([]string, 0, len(cols))
	for _, c := range cols {
		quoted = append(quoted, e.dialect.QuoteIdentifier(c))
		placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)+1))
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
		return "", nil, fmt.Errorf("postgresdrv: BuildUpdate table is empty")
	}
	if len(pk) == 0 {
		return "", nil, fmt.Errorf("postgresdrv: BuildUpdate requires a primary key (refusing to issue keyless UPDATE)")
	}
	if len(changes) == 0 {
		return "", nil, fmt.Errorf("postgresdrv: BuildUpdate has no changes")
	}
	changeCols := mapKeysSorted(changes)
	setClauses := make([]string, 0, len(changeCols))
	args := make([]any, 0, len(changeCols)+len(pk))
	for _, c := range changeCols {
		setClauses = append(setClauses, fmt.Sprintf("%s = $%d", e.dialect.QuoteIdentifier(c), len(args)+1))
		args = append(args, changes[c])
	}
	whereClauses, args := pkWhere(e.dialect, pk, args)

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
		return "", nil, fmt.Errorf("postgresdrv: BuildDelete table is empty")
	}
	if len(pk) == 0 {
		return "", nil, fmt.Errorf("postgresdrv: BuildDelete requires a primary key (refusing to issue keyless DELETE)")
	}
	whereClauses, args := pkWhere(e.dialect, pk, nil)
	sqlText := fmt.Sprintf("DELETE FROM %s WHERE %s",
		e.qualify(db, schema, table),
		strings.Join(whereClauses, " AND "),
	)
	return sqlText, args, nil
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

// pkWhere appends the key predicates to args, numbering placeholders after
// the ones already consumed. NULL key parts render as IS NULL.
func pkWhere(d dialect, pk map[string]any, args []any) ([]string, []any) {
	cols := mapKeysSorted(pk)
	clauses := make([]string, 0, len(cols))
	for _, c := range cols {
		v := pk[c]
		if v == nil {
			clauses = append(clauses, fmt.Sprintf("%s IS NULL", d.QuoteIdentifier(c)))
			continue
		}
		clauses = append(clauses, fmt.Sprintf("%s = $%d", d.QuoteIdentifier(c), len(args)+1))
		args = append(args, v)
	}
	return clauses, args
}

// qualify renders the quoted table reference. Postgres ignores the db level
// (a connection is bound to one database) — the result is "schema"."table".
func (e editor) qualify(db, schema, table string) string {
	return dbdriver.QualifyTable(e.dialect, "", resolveSchema(db, schema), table)
}
