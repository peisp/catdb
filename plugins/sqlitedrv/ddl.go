package sqlitedrv

import (
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// DDL generation for SQLite. Two structural constraints shape this file:
//   - Secondary indexes cannot appear inside CREATE TABLE; they are emitted as
//     separate CREATE INDEX statements after it (modernc.org/sqlite executes
//     multi-statement strings, and the script paths split on ';' anyway).
//   - ALTER TABLE only supports ADD/DROP/RENAME COLUMN. Column redefinition,
//     primary-key and foreign-key changes need a full table rebuild, which a
//     pure Dialect (no connection) cannot generate — those return errors.

// quoteString renders a SQLite string literal: single quotes doubled, no
// backslash escaping (SQLite treats backslash literally).
func quoteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

var defaultKeywords = map[string]bool{
	"NULL": true, "CURRENT_TIMESTAMP": true, "CURRENT_DATE": true,
	"CURRENT_TIME": true, "TRUE": true, "FALSE": true,
}

// formatDefaultExpr renders the right-hand side of `DEFAULT …` from the raw
// (unquoted) value carried in ColumnMeta.Default.
func formatDefaultExpr(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "''"
	}
	if defaultKeywords[strings.ToUpper(trimmed)] {
		return strings.ToUpper(trimmed)
	}
	if strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")") {
		return trimmed
	}
	if isNumericLiteral(trimmed) {
		return trimmed
	}
	return quoteString(trimmed)
}

// isNumericLiteral matches integers, decimals and scientific notation
// (incl. negative), same shape as the MySQL dialect uses.
func isNumericLiteral(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	digits := func() bool {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		return i > start
	}
	if !digits() {
		return false
	}
	if i < len(s) && s[i] == '.' {
		i++
		if !digits() {
			return false
		}
	}
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && s[i] == '-' {
			i++
		}
		if !digits() {
			return false
		}
	}
	return i == len(s)
}

// columnDefBody renders everything after the column name. SQLite has no
// column comments; Comment is dropped. inlinePK marks the single-column
// INTEGER PRIMARY KEY [AUTOINCREMENT] form (the rowid alias).
func columnDefBody(c dbdriver.ColumnMeta, inlinePK bool) string {
	typ := c.NativeType
	if inlinePK && c.IsAutoIncrement {
		// AUTOINCREMENT is only legal on exactly "INTEGER PRIMARY KEY".
		typ = "INTEGER"
	}
	parts := []string{typ}
	if inlinePK {
		parts = append(parts, "PRIMARY KEY")
		if c.IsAutoIncrement {
			parts = append(parts, "AUTOINCREMENT")
		}
	}
	if !c.Nullable && !inlinePK {
		parts = append(parts, "NOT NULL")
	}
	if c.Default != nil {
		parts = append(parts, "DEFAULT "+formatDefaultExpr(*c.Default))
	}
	return strings.Join(parts, " ")
}

func (d dialect) fullColumnDef(c dbdriver.ColumnMeta, inlinePK bool) string {
	return d.QuoteIdentifier(c.Name) + " " + columnDefBody(c, inlinePK)
}

// indexColsSpec renders "(col [ASC|DESC], …)".
func (d dialect) indexColsSpec(cols []dbdriver.IndexColumn) string {
	out := make([]string, len(cols))
	for i, c := range cols {
		spec := d.QuoteIdentifier(c.Name)
		if o := strings.ToUpper(c.Order); o == "ASC" || o == "DESC" {
			spec += " " + o
		}
		out[i] = spec
	}
	return "(" + strings.Join(out, ", ") + ")"
}

// createIndexStmt renders a standalone CREATE INDEX. In SQLite the schema
// qualifier goes on the index name; the ON table must stay unqualified.
func (d dialect) createIndexStmt(schema, table string, ix dbdriver.IndexInfo) string {
	kw := "CREATE INDEX"
	if ix.Unique {
		kw = "CREATE UNIQUE INDEX"
	}
	name := d.QuoteIdentifier(ix.Name)
	if schema != "" {
		name = d.QuoteIdentifier(schema) + "." + name
	}
	return fmt.Sprintf("%s %s ON %s %s;", kw, name, d.QuoteIdentifier(table), d.indexColsSpec(ix.Columns))
}

// fkSpec renders "CONSTRAINT name FOREIGN KEY (…) REFERENCES tbl (…)…".
// SQLite FKs always reference a table in the same schema — no qualifier.
func (d dialect) fkSpec(fk dbdriver.ForeignKeyInfo) string {
	quoteAll := func(names []string) string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = d.QuoteIdentifier(n)
		}
		return strings.Join(out, ", ")
	}
	s := ""
	if fk.Name != "" {
		s = "CONSTRAINT " + d.QuoteIdentifier(fk.Name) + " "
	}
	s += fmt.Sprintf("FOREIGN KEY (%s) REFERENCES %s (%s)",
		quoteAll(fk.Columns), d.QuoteIdentifier(fk.ReferencedTable), quoteAll(fk.ReferencedColumns))
	if u := strings.ToUpper(fk.OnUpdate); u != "" && u != "NO ACTION" {
		s += " ON UPDATE " + u
	}
	if u := strings.ToUpper(fk.OnDelete); u != "" && u != "NO ACTION" {
		s += " ON DELETE " + u
	}
	return s
}

// GenerateCreateTable emits CREATE TABLE plus one CREATE INDEX per secondary
// index, ';'-joined. Comments and driver-foreign Options are dropped (SQLite
// has neither).
func (d dialect) GenerateCreateTable(t dbdriver.TableSchema) (string, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return "", fmt.Errorf("sqlitedrv: GenerateCreateTable: table name is empty")
	}
	if len(t.Columns) == 0 {
		return "", fmt.Errorf("sqlitedrv: GenerateCreateTable: table %q has no columns", name)
	}

	// PK: explicit list wins; otherwise collect per-column flags in order.
	pk := t.PrimaryKey
	if len(pk) == 0 {
		for _, c := range t.Columns {
			if c.IsPrimaryKey {
				pk = append(pk, c.Name)
			}
		}
	}
	// Single-column PK is rendered inline so the INTEGER PRIMARY KEY rowid
	// alias (and AUTOINCREMENT) works.
	inlinePKCol := ""
	if len(pk) == 1 {
		inlinePKCol = pk[0]
	}

	fq := dbdriver.QualifyTable(d, t.Schema, "", name)
	var lines []string
	for _, c := range t.Columns {
		lines = append(lines, "  "+d.fullColumnDef(c, c.Name == inlinePKCol))
	}
	if len(pk) > 1 {
		quoted := make([]string, len(pk))
		for i, n := range pk {
			quoted[i] = d.QuoteIdentifier(n)
		}
		lines = append(lines, fmt.Sprintf("  PRIMARY KEY (%s)", strings.Join(quoted, ", ")))
	}

	var indexStmts []string
	for _, ix := range t.Indexes {
		if ix.Primary || strings.TrimSpace(ix.Name) == "" || len(ix.Columns) == 0 {
			continue
		}
		// sqlite_autoindex_* names are reserved — they come from inline UNIQUE
		// constraints on the source table, so render them back as one.
		if ix.Unique && strings.HasPrefix(ix.Name, "sqlite_autoindex_") {
			lines = append(lines, "  UNIQUE "+d.indexColsSpec(ix.Columns))
			continue
		}
		indexStmts = append(indexStmts, d.createIndexStmt(t.Schema, name, ix))
	}
	for _, fk := range t.ForeignKeys {
		if len(fk.Columns) == 0 || strings.TrimSpace(fk.ReferencedTable) == "" || len(fk.ReferencedColumns) == 0 {
			continue
		}
		lines = append(lines, "  "+d.fkSpec(fk))
	}

	out := fmt.Sprintf("CREATE TABLE %s (\n%s\n);", fq, strings.Join(lines, ",\n"))
	if len(indexStmts) > 0 {
		out += "\n" + strings.Join(indexStmts, "\n")
	}
	return out, nil
}

// GenerateAlterTable renders the subset of changes SQLite's ALTER TABLE can
// express. Column redefinition (modify), primary-key and foreign-key changes
// require a table rebuild and return an error instead of wrong DDL. Comment
// options are silently dropped — SQLite has no comments, so there is nothing
// to change.
func (d dialect) GenerateAlterTable(db, schema, table string, cs dbdriver.ChangeSet) ([]string, error) {
	if strings.TrimSpace(table) == "" {
		return nil, fmt.Errorf("sqlitedrv: GenerateAlterTable: table name is empty")
	}
	ns := resolveDB(db, schema)
	fq := dbdriver.QualifyTable(d, ns, "", table)
	var stmts []string

	for _, ch := range cs.Columns {
		switch ch.Kind {
		case dbdriver.ColumnDrop:
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", fq, d.QuoteIdentifier(ch.Name)))
		case dbdriver.ColumnAdd:
			if ch.Column == nil {
				return nil, fmt.Errorf("sqlitedrv: add column change without definition")
			}
			if ch.Column.IsPrimaryKey {
				return nil, fmt.Errorf("sqlitedrv: SQLite cannot add a PRIMARY KEY column to an existing table")
			}
			// Position clauses are not supported; SQLite always appends.
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", fq, d.fullColumnDef(*ch.Column, false)))
		case dbdriver.ColumnRename:
			if ch.Column == nil {
				return nil, fmt.Errorf("sqlitedrv: rename column change without definition")
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s;",
				fq, d.QuoteIdentifier(ch.Name), d.QuoteIdentifier(ch.Column.Name)))
		case dbdriver.ColumnModify:
			return nil, fmt.Errorf("sqlitedrv: SQLite cannot modify column %q in place (requires a table rebuild)", ch.Name)
		default:
			return nil, fmt.Errorf("sqlitedrv: unknown column change kind %q", ch.Kind)
		}
	}

	if cs.PrimaryKey != nil {
		return nil, fmt.Errorf("sqlitedrv: SQLite cannot change a table's primary key (requires a table rebuild)")
	}

	for _, ch := range cs.Indexes {
		switch ch.Kind {
		case "drop":
			name := d.QuoteIdentifier(ch.Name)
			if ns != "" {
				name = d.QuoteIdentifier(ns) + "." + name
			}
			stmts = append(stmts, "DROP INDEX "+name+";")
		case "add":
			if ch.Index == nil {
				return nil, fmt.Errorf("sqlitedrv: add index change without definition")
			}
			stmts = append(stmts, d.createIndexStmt(ns, table, *ch.Index))
		default:
			return nil, fmt.Errorf("sqlitedrv: unknown index change kind %q", ch.Kind)
		}
	}

	if len(cs.ForeignKeys) > 0 {
		return nil, fmt.Errorf("sqlitedrv: SQLite cannot add or drop foreign keys on an existing table (requires a table rebuild)")
	}

	for _, opt := range cs.Options {
		switch opt.Name {
		case "comment":
			// SQLite has no table comments — nothing to alter.
		default:
			return nil, fmt.Errorf("sqlitedrv: unknown table option %q", opt.Name)
		}
	}
	return stmts, nil
}
