package postgresdrv

import (
	"context"
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// metadata implements the optional dbdriver.DatabaseEditor extension with
// PostgreSQL's native CREATE DATABASE options: owner, template, encoding,
// LC_COLLATE, LC_CTYPE and tablespace. Encoding/locales/template are fixed
// after creation (PostgreSQL cannot change them — dump + recreate is the
// only path); owner and tablespace remain alterable.

var _ dbdriver.DatabaseEditor = metadata{}

func (m metadata) DatabaseOptionFields(ctx context.Context) ([]dbdriver.DatabaseOptionField, error) {
	owners, err := m.queryStrings(ctx, `SELECT rolname FROM pg_roles WHERE rolname !~ '^pg_' ORDER BY rolname`)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list roles: %w", err)
	}
	templates, err := m.queryStrings(ctx, `SELECT datname FROM pg_database WHERE datistemplate ORDER BY datname`)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list templates: %w", err)
	}
	// Enumerate the encodings this server can convert between — a stable,
	// server-derived stand-in for a "list of encodings" catalog PG lacks.
	encodings, err := m.queryStrings(ctx, `SELECT DISTINCT pg_encoding_to_char(conforencoding)
	                                         FROM pg_conversion
	                                        WHERE pg_encoding_to_char(conforencoding) <> ''
	                                        ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list encodings: %w", err)
	}
	// libc locale names usable as LC_COLLATE / LC_CTYPE.
	collates, err := m.queryStrings(ctx, `SELECT DISTINCT collcollate FROM pg_collation
	                                       WHERE collprovider = 'c' AND collcollate <> ''
	                                       ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list collations: %w", err)
	}
	ctypes, err := m.queryStrings(ctx, `SELECT DISTINCT collctype FROM pg_collation
	                                     WHERE collprovider = 'c' AND collctype <> ''
	                                     ORDER BY 1`)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list ctypes: %w", err)
	}
	tablespaces, err := m.queryStrings(ctx, `SELECT spcname FROM pg_tablespace
	                                          WHERE spcname <> 'pg_global'
	                                          ORDER BY spcname`)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: list tablespaces: %w", err)
	}

	return []dbdriver.DatabaseOptionField{
		{Key: "owner", Label: "Owner", Options: owners},
		{Key: "template", Label: "Template", Options: templates, FixedOnAlter: true},
		{Key: "encoding", Label: "Encoding", Options: encodings, Default: "UTF8", FixedOnAlter: true},
		{Key: "collation", Label: "Collation (LC_COLLATE)", Options: collates, FixedOnAlter: true},
		{Key: "ctype", Label: "Character type (LC_CTYPE)", Options: ctypes, FixedOnAlter: true},
		{Key: "tablespace", Label: "Tablespace", Options: tablespaces},
	}, nil
}

// queryStrings runs a catalog query on the default pool — pg_database,
// pg_roles and pg_tablespace are shared catalogs, identical from any database.
func (m metadata) queryStrings(ctx context.Context, q string) ([]string, error) {
	pool, err := m.poolFor(ctx, "")
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, q)
	if err != nil {
		return nil, err
	}
	return scanStrings(rows)
}

func (m metadata) GetDatabaseOptions(ctx context.Context, db string) (map[string]string, error) {
	const q = `SELECT pg_get_userbyid(d.datdba),
	                  pg_encoding_to_char(d.encoding),
	                  d.datcollate,
	                  d.datctype,
	                  t.spcname
	             FROM pg_database d
	             JOIN pg_tablespace t ON t.oid = d.dattablespace
	            WHERE d.datname = $1`
	pool, err := m.poolFor(ctx, "")
	if err != nil {
		return nil, err
	}
	var owner, encoding, collate, ctype, tablespace string
	err = pool.QueryRow(ctx, q, db).Scan(&owner, &encoding, &collate, &ctype, &tablespace)
	if err != nil {
		return nil, fmt.Errorf("postgresdrv: database options: %w", err)
	}
	return map[string]string{
		"owner":      owner,
		"encoding":   encoding,
		"collation":  collate,
		"ctype":      ctype,
		"tablespace": tablespace,
	}, nil
}

func (m metadata) CreateDatabaseSQL(name string, opts map[string]string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("postgresdrv: database name is empty")
	}
	d := dialect{}
	s := "CREATE DATABASE " + d.QuoteIdentifier(name)
	if v := opts["owner"]; v != "" {
		s += " OWNER " + d.QuoteIdentifier(v)
	}
	template := opts["template"]
	// A collation/ctype differing from the template's requires template0;
	// default to it when locales are set and no template was chosen.
	if template == "" && (opts["collation"] != "" || opts["ctype"] != "") {
		template = "template0"
	}
	if template != "" {
		s += " TEMPLATE " + d.QuoteIdentifier(template)
	}
	if v := opts["encoding"]; v != "" {
		s += " ENCODING " + quoteString(v)
	}
	if v := opts["collation"]; v != "" {
		s += " LC_COLLATE " + quoteString(v)
	}
	if v := opts["ctype"]; v != "" {
		s += " LC_CTYPE " + quoteString(v)
	}
	if v := opts["tablespace"]; v != "" {
		s += " TABLESPACE " + d.QuoteIdentifier(v)
	}
	return s, nil
}

// AlterDatabaseSQL renders one statement per changed option. Only owner and
// tablespace are alterable in PostgreSQL.
func (m metadata) AlterDatabaseSQL(name string, opts map[string]string) (string, error) {
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("postgresdrv: database name is empty")
	}
	d := dialect{}
	var stmts []string
	if v := opts["owner"]; v != "" {
		stmts = append(stmts, "ALTER DATABASE "+d.QuoteIdentifier(name)+" OWNER TO "+d.QuoteIdentifier(v)+";")
	}
	if v := opts["tablespace"]; v != "" {
		stmts = append(stmts, "ALTER DATABASE "+d.QuoteIdentifier(name)+" SET TABLESPACE "+d.QuoteIdentifier(v)+";")
	}
	for _, key := range []string{"encoding", "collation", "ctype", "template"} {
		if opts[key] != "" {
			return "", fmt.Errorf("postgresdrv: PostgreSQL cannot change the %s of an existing database", key)
		}
	}
	if len(stmts) == 0 {
		return "", fmt.Errorf("postgresdrv: no alterable database options changed")
	}
	return strings.Join(stmts, "\n"), nil
}
