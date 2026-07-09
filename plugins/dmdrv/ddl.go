package dmdrv

import (
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// DDL generation for DM — CREATE TABLE from a TableSchema and ALTER
// statements from a schemadiff ChangeSet. DM's DDL surface is Oracle-shaped
// with a few MySQL conveniences that this renderer leans on:
//   - comments are separate COMMENT ON statements, never inline;
//   - indexes are schema-level objects (CREATE INDEX, not a table clause);
//   - ALTER TABLE … MODIFY takes a full column definition (MySQL-style),
//     so one statement reconciles type/nullability/default;
//   - auto-increment is IDENTITY(1,1) and cannot be added to an existing
//     column — identity is only emitted in CREATE/ADD contexts;
//   - columns cannot be repositioned — ColumnPosition is ignored;
//   - ALTER TABLE … DROP PRIMARY KEY works without the constraint name.

// quoteString renders a DM string literal: single quotes doubled, backslash
// is literal (ANSI semantics).
func quoteString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// defaultKeywords pass through unquoted when found as a DEFAULT value.
var defaultKeywords = map[string]bool{
	"NULL": true, "CURRENT_TIMESTAMP": true, "CURRENT_DATE": true,
	"CURRENT_TIME": true, "SYSDATE": true, "NOW()": true, "TRUE": true, "FALSE": true,
	"CURRENT_USER": true, "LOCALTIMESTAMP": true,
}

// formatDefaultExpr renders the right-hand side of `DEFAULT …`. Values read
// back from the dictionary arrive as expressions and pass through verbatim;
// bare user input is quoted unless it is a keyword or numeric literal.
func formatDefaultExpr(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "''"
	}
	if defaultKeywords[strings.ToUpper(trimmed)] {
		return trimmed
	}
	// Expressions: function calls, parenthesized — keep verbatim.
	if strings.Contains(trimmed, "(") && strings.HasSuffix(trimmed, ")") {
		return trimmed
	}
	// Already-quoted literals read back from the dictionary.
	if strings.HasPrefix(trimmed, "'") && strings.HasSuffix(trimmed, "'") {
		return trimmed
	}
	if isNumericLiteral(trimmed) {
		return trimmed
	}
	return quoteString(trimmed)
}

// isNumericLiteral matches integers, decimals and scientific notation
// (incl. negative) — same acceptance as the MySQL/Postgres renderers.
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

// columnDefBody renders everything after the column name. withIdentity is
// false in MODIFY contexts — DM cannot turn an existing column into an
// identity column. Comments are NOT part of the definition — callers emit
// COMMENT ON separately.
func columnDefBody(c dbdriver.ColumnMeta, withIdentity bool) string {
	parts := []string{c.NativeType}
	if c.IsAutoIncrement && withIdentity {
		parts = append(parts, "IDENTITY(1,1)")
	}
	// Identity columns own their value generation — no DEFAULT.
	if c.Default != nil && !c.IsAutoIncrement {
		parts = append(parts, "DEFAULT "+formatDefaultExpr(*c.Default))
	}
	if c.Nullable {
		parts = append(parts, "NULL")
	} else {
		parts = append(parts, "NOT NULL")
	}
	return strings.Join(parts, " ")
}

func (d dialect) fullColumnDef(c dbdriver.ColumnMeta, withIdentity bool) string {
	return d.QuoteIdentifier(c.Name) + " " + columnDefBody(c, withIdentity)
}

// indexColumnList renders the parenthesized key list. ASC is the default and
// omitted.
func (d dialect) indexColumnList(ix dbdriver.IndexInfo) string {
	cols := make([]string, len(ix.Columns))
	for i, c := range ix.Columns {
		spec := d.QuoteIdentifier(c.Name)
		if strings.EqualFold(c.Order, "DESC") {
			spec += " DESC"
		}
		cols[i] = spec
	}
	return "(" + strings.Join(cols, ", ") + ")"
}

// createIndexSQL renders "CREATE [UNIQUE] INDEX name ON table (…)". The
// index lives in the table's schema; DM has no USING clause — BTREE is the
// only method this renderer emits.
func (d dialect) createIndexSQL(schema, table string, ix dbdriver.IndexInfo) string {
	kw := "CREATE INDEX"
	if ix.Unique {
		kw = "CREATE UNIQUE INDEX"
	}
	return fmt.Sprintf("%s %s ON %s %s;", kw, d.QuoteIdentifier(ix.Name),
		dbdriver.QualifyTable(d, schema, "", table), d.indexColumnList(ix))
}

// fkSpec renders "CONSTRAINT name FOREIGN KEY (…) REFERENCES … (…)[ ON DELETE …]".
// NO ACTION is DM's default and is omitted (metadata maps it to "").
func (d dialect) fkSpec(fk dbdriver.ForeignKeyInfo) string {
	quoteAll := func(names []string) string {
		out := make([]string, len(names))
		for i, n := range names {
			out[i] = d.QuoteIdentifier(n)
		}
		return strings.Join(out, ", ")
	}
	refTable := dbdriver.QualifyTable(d, fk.ReferencedSchema, "", fk.ReferencedTable)
	s := fmt.Sprintf("CONSTRAINT %s FOREIGN KEY (%s) REFERENCES %s (%s)",
		d.QuoteIdentifier(fk.Name), quoteAll(fk.Columns), refTable, quoteAll(fk.ReferencedColumns))
	if u := strings.ToUpper(fk.OnUpdate); u != "" && u != "NO ACTION" {
		s += " ON UPDATE " + u
	}
	if u := strings.ToUpper(fk.OnDelete); u != "" && u != "NO ACTION" {
		s += " ON DELETE " + u
	}
	return s
}

// GenerateCreateTable emits the CREATE TABLE statement followed by CREATE
// INDEX / COMMENT ON statements as needed, newline-joined. Consumers split
// multi-statement scripts through core/sqlscript before execution.
func (d dialect) GenerateCreateTable(t dbdriver.TableSchema) (string, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return "", fmt.Errorf("dmdrv: GenerateCreateTable: table name is empty")
	}
	if len(t.Columns) == 0 {
		return "", fmt.Errorf("dmdrv: GenerateCreateTable: table %q has no columns", name)
	}

	fq := dbdriver.QualifyTable(d, t.Schema, "", name)
	var lines []string
	for _, c := range t.Columns {
		lines = append(lines, "  "+d.fullColumnDef(c, true))
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
	if len(pk) > 0 {
		quoted := make([]string, len(pk))
		for i, n := range pk {
			quoted[i] = d.QuoteIdentifier(n)
		}
		lines = append(lines, fmt.Sprintf("  PRIMARY KEY (%s)", strings.Join(quoted, ", ")))
	}

	for _, fk := range t.ForeignKeys {
		if strings.TrimSpace(fk.Name) == "" || len(fk.Columns) == 0 ||
			strings.TrimSpace(fk.ReferencedTable) == "" || len(fk.ReferencedColumns) == 0 {
			continue
		}
		lines = append(lines, "  "+d.fkSpec(fk))
	}

	stmts := []string{fmt.Sprintf("CREATE TABLE %s (\n%s\n);", fq, strings.Join(lines, ",\n"))}

	for _, ix := range t.Indexes {
		if ix.Primary || strings.TrimSpace(ix.Name) == "" || len(ix.Columns) == 0 {
			continue
		}
		stmts = append(stmts, d.createIndexSQL(t.Schema, name, ix))
	}
	if t.Comment != "" {
		stmts = append(stmts, fmt.Sprintf("COMMENT ON TABLE %s IS %s;", fq, quoteString(t.Comment)))
	}
	for _, c := range t.Columns {
		if c.Comment != "" {
			stmts = append(stmts, d.commentOnColumn(fq, c.Name, c.Comment))
		}
	}
	return strings.Join(stmts, "\n"), nil
}

func (d dialect) commentOnColumn(fqTable, column, comment string) string {
	val := "NULL"
	if comment != "" {
		val = quoteString(comment)
	}
	return fmt.Sprintf("COMMENT ON COLUMN %s.%s IS %s;", fqTable, d.QuoteIdentifier(column), val)
}

// GenerateAlterTable renders a ChangeSet into DDL statements in safe
// execution order: columns → primary key → indexes → foreign keys → options.
// Column positions (FIRST/AFTER) are ignored — DM cannot reorder columns.
func (d dialect) GenerateAlterTable(db, schema, table string, cs dbdriver.ChangeSet) ([]string, error) {
	if strings.TrimSpace(table) == "" {
		return nil, fmt.Errorf("dmdrv: GenerateAlterTable: table name is empty")
	}
	ns := resolveSchema(db, schema)
	fq := dbdriver.QualifyTable(d, ns, "", table)
	var stmts []string

	for _, ch := range cs.Columns {
		switch ch.Kind {
		case dbdriver.ColumnDrop:
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", fq, d.QuoteIdentifier(ch.Name)))
		case dbdriver.ColumnAdd:
			if ch.Column == nil {
				return nil, fmt.Errorf("dmdrv: add column change without definition")
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s;", fq, d.fullColumnDef(*ch.Column, true)))
			if ch.Column.Comment != "" {
				stmts = append(stmts, d.commentOnColumn(fq, ch.Column.Name, ch.Column.Comment))
			}
		case dbdriver.ColumnModify:
			if ch.Column == nil {
				return nil, fmt.Errorf("dmdrv: modify column change without definition")
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s MODIFY %s;", fq, d.fullColumnDef(*ch.Column, false)))
			stmts = append(stmts, d.commentOnColumn(fq, ch.Column.Name, ch.Column.Comment))
		case dbdriver.ColumnRename:
			if ch.Column == nil {
				return nil, fmt.Errorf("dmdrv: rename column change without definition")
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s RENAME COLUMN %s TO %s;",
				fq, d.QuoteIdentifier(ch.Name), d.QuoteIdentifier(ch.Column.Name)))
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s MODIFY %s;", fq, d.fullColumnDef(*ch.Column, false)))
			stmts = append(stmts, d.commentOnColumn(fq, ch.Column.Name, ch.Column.Comment))
		default:
			return nil, fmt.Errorf("dmdrv: unknown column change kind %q", ch.Kind)
		}
	}

	if pk := cs.PrimaryKey; pk != nil {
		if pk.Drop {
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s DROP PRIMARY KEY;", fq))
		}
		if len(pk.Columns) > 0 {
			quoted := make([]string, len(pk.Columns))
			for i, n := range pk.Columns {
				quoted[i] = d.QuoteIdentifier(n)
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD PRIMARY KEY (%s);", fq, strings.Join(quoted, ", ")))
		}
	}

	for _, ch := range cs.Indexes {
		switch ch.Kind {
		case "drop":
			stmts = append(stmts, fmt.Sprintf("DROP INDEX %s;", dbdriver.QualifyTable(d, ns, "", ch.Name)))
		case "add":
			if ch.Index == nil {
				return nil, fmt.Errorf("dmdrv: add index change without definition")
			}
			stmts = append(stmts, d.createIndexSQL(ns, table, *ch.Index))
		default:
			return nil, fmt.Errorf("dmdrv: unknown index change kind %q", ch.Kind)
		}
	}

	for _, ch := range cs.ForeignKeys {
		switch ch.Kind {
		case "drop":
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s DROP CONSTRAINT %s;", fq, d.QuoteIdentifier(ch.Name)))
		case "add":
			if ch.ForeignKey == nil {
				return nil, fmt.Errorf("dmdrv: add foreign-key change without definition")
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD %s;", fq, d.fkSpec(*ch.ForeignKey)))
		default:
			return nil, fmt.Errorf("dmdrv: unknown foreign-key change kind %q", ch.Kind)
		}
	}

	for _, opt := range cs.Options {
		switch opt.Name {
		case "comment":
			val := "NULL"
			if opt.Value != "" {
				val = quoteString(opt.Value)
			}
			stmts = append(stmts, fmt.Sprintf("COMMENT ON TABLE %s IS %s;", fq, val))
		default:
			return nil, fmt.Errorf("dmdrv: unknown table option %q", opt.Name)
		}
	}
	return stmts, nil
}
