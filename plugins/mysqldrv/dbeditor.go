package mysqldrv

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// metadata implements the optional dbdriver.DatabaseEditor extension: the
// create/alter-database UI. MySQL's native options are the character set and
// collation; the collation choices depend on the selected charset.

var _ dbdriver.DatabaseEditor = metadata{}

func (m metadata) DatabaseOptionFields(ctx context.Context) ([]dbdriver.DatabaseOptionField, error) {
	const qCharsets = `SELECT CHARACTER_SET_NAME, DEFAULT_COLLATE_NAME
	                     FROM information_schema.CHARACTER_SETS
	                    ORDER BY CHARACTER_SET_NAME`
	rows, err := m.db.QueryContext(ctx, qCharsets)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list charsets: %w", err)
	}
	defer rows.Close()
	var (
		charsets  []string
		defaultBy = map[string]string{}
	)
	for rows.Next() {
		var name, def string
		if err := rows.Scan(&name, &def); err != nil {
			return nil, err
		}
		charsets = append(charsets, name)
		defaultBy[name] = def
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	const qCollations = `SELECT COLLATION_NAME, CHARACTER_SET_NAME
	                       FROM information_schema.COLLATIONS
	                      WHERE COLLATION_NAME IS NOT NULL
	                      ORDER BY CHARACTER_SET_NAME, COLLATION_NAME`
	cRows, err := m.db.QueryContext(ctx, qCollations)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: list collations: %w", err)
	}
	defer cRows.Close()
	optionsBy := map[string][]string{}
	for cRows.Next() {
		// MariaDB's information_schema.COLLATIONS has rows with a NULL
		// CHARACTER_SET_NAME (charset-agnostic collations); MySQL never does.
		// Use NullString and skip the unattached ones.
		var name string
		var charset sql.NullString
		if err := cRows.Scan(&name, &charset); err != nil {
			return nil, err
		}
		if !charset.Valid {
			continue
		}
		optionsBy[charset.String] = append(optionsBy[charset.String], name)
	}
	if err := cRows.Err(); err != nil {
		return nil, err
	}

	return []dbdriver.DatabaseOptionField{
		{Key: "charset", Label: "Charset", Options: charsets, Default: "utf8mb4"},
		{Key: "collation", Label: "Collation", DependsOn: "charset", OptionsBy: optionsBy, DefaultBy: defaultBy},
	}, nil
}

func (m metadata) GetDatabaseOptions(ctx context.Context, db string) (map[string]string, error) {
	const q = `SELECT DEFAULT_CHARACTER_SET_NAME, DEFAULT_COLLATION_NAME
	             FROM information_schema.SCHEMATA
	            WHERE SCHEMA_NAME = ?`
	var charset, collation string
	err := m.db.QueryRowContext(ctx, q, db).Scan(&charset, &collation)
	if err != nil {
		return nil, fmt.Errorf("mysqldrv: database options: %w", err)
	}
	return map[string]string{"charset": charset, "collation": collation}, nil
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

func databaseDDL(verb, name string, opts map[string]string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("mysqldrv: database name is empty")
	}
	charset, collation := opts["charset"], opts["collation"]
	if !validOptionName(charset) || !validOptionName(collation) {
		return "", fmt.Errorf("mysqldrv: invalid charset/collation name")
	}
	parts := []string{verb + " " + (dialect{}).QuoteIdentifier(name)}
	if charset != "" {
		parts = append(parts, "CHARACTER SET = "+charset)
	}
	if collation != "" {
		parts = append(parts, "COLLATE = "+collation)
	}
	return strings.Join(parts, " "), nil
}

func (m metadata) CreateDatabaseSQL(name string, opts map[string]string) (string, error) {
	return databaseDDL("CREATE DATABASE", name, opts)
}

func (m metadata) AlterDatabaseSQL(name string, opts map[string]string) (string, error) {
	if len(opts) == 0 {
		return "", fmt.Errorf("mysqldrv: no database options changed")
	}
	return databaseDDL("ALTER DATABASE", name, opts)
}
