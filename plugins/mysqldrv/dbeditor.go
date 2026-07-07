package mysqldrv

import (
	"context"
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// metadata implements the optional dbdriver.DatabaseEditor extension: the
// create/alter-database UI (charset + collation pickers, DDL rendering).

func (m metadata) ListCharsets(ctx context.Context) ([]dbdriver.CharsetInfo, error) {
	const q = `SELECT CHARACTER_SET_NAME, DEFAULT_COLLATE_NAME
	             FROM information_schema.CHARACTER_SETS
	            ORDER BY CHARACTER_SET_NAME`
	rows, err := m.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list charsets: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.CharsetInfo
	for rows.Next() {
		var c dbdriver.CharsetInfo
		if err := rows.Scan(&c.Name, &c.DefaultCollation); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (m metadata) ListCollations(ctx context.Context) ([]dbdriver.CollationInfo, error) {
	const q = `SELECT COLLATION_NAME, CHARACTER_SET_NAME
	             FROM information_schema.COLLATIONS
	            WHERE COLLATION_NAME IS NOT NULL
	            ORDER BY CHARACTER_SET_NAME, COLLATION_NAME`
	rows, err := m.db.QueryContext(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list collations: %w", err)
	}
	defer rows.Close()
	var out []dbdriver.CollationInfo
	for rows.Next() {
		var c dbdriver.CollationInfo
		if err := rows.Scan(&c.Name, &c.Charset); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (m metadata) GetDatabaseOptions(ctx context.Context, db string) (dbdriver.DatabaseOptions, error) {
	const q = `SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME
	             FROM information_schema.SCHEMATA
	            WHERE SCHEMA_NAME = ?`
	var opts dbdriver.DatabaseOptions
	err := m.db.QueryRowContext(ctx, q, db).Scan(&opts.Charset, &opts.Collation)
	if err != nil {
		return dbdriver.DatabaseOptions{}, fmt.Errorf("mysqldrv: database options: %w", err)
	}
	return opts, nil
}

// Charset/collation names come from the fixed server-provided lists above,
// never user free-text, and MySQL accepts them as barewords — matching what
// SHOW CREATE DATABASE emits. Still, reject anything non-bareword.
func validOptionName(s string) bool {
	for _, r := range s {
		if !(r == '_' || (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

func databaseDDL(verb, name string, opts dbdriver.DatabaseOptions) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("mysqldrv: database name is empty")
	}
	if !validOptionName(opts.Charset) || !validOptionName(opts.Collation) {
		return "", fmt.Errorf("mysqldrv: invalid charset/collation name")
	}
	parts := []string{verb + " " + (dialect{}).QuoteIdentifier(name)}
	if opts.Charset != "" {
		parts = append(parts, "CHARACTER SET = "+opts.Charset)
	}
	if opts.Collation != "" {
		parts = append(parts, "COLLATE = "+opts.Collation)
	}
	return strings.Join(parts, " "), nil
}

func (m metadata) CreateDatabaseSQL(name string, opts dbdriver.DatabaseOptions) (string, error) {
	return databaseDDL("CREATE DATABASE", name, opts)
}

func (m metadata) AlterDatabaseSQL(name string, opts dbdriver.DatabaseOptions) (string, error) {
	return databaseDDL("ALTER DATABASE", name, opts)
}
