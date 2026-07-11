package mysqldrv

import (
	"fmt"
	"strings"

	"catdb/internal/dbdriver"
)

// DDL generation for MySQL — CREATE TABLE from a TableSchema and ALTER TABLE
// from a schemadiff ChangeSet. Ported from the front-end alterPlan.ts engine
// (same quoting, same DEFAULT handling, same DROP+ADD pairing for indexes and
// FKs) so the structure editor migration is behavior-preserving.

// quoteString renders a MySQL string literal: single quotes, escape \ and ',
// plus \n/\r/\t.
func quoteString(s string) string {
	r := strings.NewReplacer(
		`\`, `\\`,
		`'`, `''`,
		"\n", `\n`,
		"\r", `\r`,
		"\t", `\t`,
	)
	return "'" + r.Replace(s) + "'"
}

// defaultKeywords pass through unquoted when found as a DEFAULT value.
var defaultKeywords = map[string]bool{
	"NULL": true, "CURRENT_TIMESTAMP": true, "CURRENT_DATE": true,
	"CURRENT_TIME": true, "NOW()": true, "UUID()": true, "TRUE": true, "FALSE": true,
}

// formatDefaultExpr renders the right-hand side of `DEFAULT …`.
func formatDefaultExpr(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "''"
	}
	up := strings.ToUpper(trimmed)
	if defaultKeywords[up] {
		return up
	}
	// CURRENT_TIMESTAMP(6) — fractional-seconds form, also a bare keyword.
	if rest, ok := strings.CutPrefix(up, "CURRENT_TIMESTAMP("); ok &&
		strings.HasSuffix(rest, ")") &&
		strings.Trim(strings.TrimSuffix(rest, ")"), "0123456789") == "" {
		return up
	}
	// Functional defaults like (CURRENT_TIMESTAMP) or (UUID()) — keep verbatim.
	if strings.HasPrefix(trimmed, "(") && strings.HasSuffix(trimmed, ")") {
		return trimmed
	}
	if isNumericLiteral(trimmed) {
		return trimmed
	}
	return quoteString(trimmed)
}

// isNumericLiteral matches integers, decimals and scientific notation
// (incl. negative), mirroring alterPlan.ts's /^-?\d+(\.\d+)?(e-?\d+)?$/i.
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

// columnDefBody renders everything after the column name (used verbatim in
// ADD / MODIFY / CHANGE and in CREATE TABLE). PRIMARY KEY is emitted as a
// separate constraint, never here.
func columnDefBody(c dbdriver.ColumnMeta) string {
	parts := []string{c.NativeType}
	if c.Nullable {
		parts = append(parts, "NULL")
	} else {
		parts = append(parts, "NOT NULL")
	}
	if c.Default != nil {
		parts = append(parts, "DEFAULT "+formatDefaultExpr(*c.Default))
	}
	if c.IsAutoIncrement {
		parts = append(parts, "AUTO_INCREMENT")
	}
	if c.Comment != "" {
		parts = append(parts, "COMMENT "+quoteString(c.Comment))
	}
	return strings.Join(parts, " ")
}

func (d dialect) fullColumnDef(c dbdriver.ColumnMeta) string {
	return d.QuoteIdentifier(c.Name) + " " + columnDefBody(c)
}

func positionClause(d dialect, p *dbdriver.ColumnPosition) string {
	if p == nil {
		return ""
	}
	if p.First {
		return " FIRST"
	}
	return " AFTER " + d.QuoteIdentifier(p.After)
}

// indexSpec renders the shared part of an index definition:
// "KEYWORD `name` (`col` [ASC|DESC], …)[ USING HASH][ COMMENT '…']".
// FULLTEXT/SPATIAL are index kinds (keyword prefix), not USING types.
func (d dialect) indexSpec(ix dbdriver.IndexInfo) string {
	cols := make([]string, len(ix.Columns))
	for i, c := range ix.Columns {
		spec := d.QuoteIdentifier(c.Name)
		if o := strings.ToUpper(c.Order); o == "ASC" || o == "DESC" {
			spec += " " + o
		}
		cols[i] = spec
	}
	typ := strings.ToUpper(ix.Type)
	kw := "INDEX"
	using := ""
	switch typ {
	case "FULLTEXT":
		kw = "FULLTEXT INDEX"
	case "SPATIAL":
		kw = "SPATIAL INDEX"
	case "", "BTREE":
		// default access method — no USING clause
	default:
		using = " USING " + typ
	}
	if ix.Unique {
		kw = "UNIQUE INDEX"
	}
	s := fmt.Sprintf("%s %s (%s)%s", kw, d.QuoteIdentifier(ix.Name), strings.Join(cols, ", "), using)
	if ix.Comment != "" {
		s += " COMMENT " + quoteString(ix.Comment)
	}
	return s
}

// fkSpec renders "CONSTRAINT `name` FOREIGN KEY (…) REFERENCES … (…)[ ON UPDATE …][ ON DELETE …]".
// RESTRICT is MySQL's default and is omitted.
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
	if u := strings.ToUpper(fk.OnUpdate); u != "" && u != "RESTRICT" {
		s += " ON UPDATE " + u
	}
	if u := strings.ToUpper(fk.OnDelete); u != "" && u != "RESTRICT" {
		s += " ON DELETE " + u
	}
	return s
}

// GenerateCreateTable emits a full CREATE TABLE statement for the schema.
func (d dialect) GenerateCreateTable(t dbdriver.TableSchema) (string, error) {
	name := strings.TrimSpace(t.Name)
	if name == "" {
		return "", fmt.Errorf("mysqldrv: GenerateCreateTable: table name is empty")
	}
	if len(t.Columns) == 0 {
		return "", fmt.Errorf("mysqldrv: GenerateCreateTable: table %q has no columns", name)
	}

	fq := dbdriver.QualifyTable(d, t.Schema, "", name)
	var lines []string
	for _, c := range t.Columns {
		lines = append(lines, "  "+d.fullColumnDef(c))
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

	for _, ix := range t.Indexes {
		if ix.Primary || strings.TrimSpace(ix.Name) == "" || len(ix.Columns) == 0 {
			continue
		}
		lines = append(lines, "  "+d.indexSpec(ix))
	}
	for _, fk := range t.ForeignKeys {
		if strings.TrimSpace(fk.Name) == "" || len(fk.Columns) == 0 ||
			strings.TrimSpace(fk.ReferencedTable) == "" || len(fk.ReferencedColumns) == 0 {
			continue
		}
		lines = append(lines, "  "+d.fkSpec(fk))
	}

	tail := ""
	if engine := t.Options["engine"]; engine != "" {
		tail += " ENGINE=" + engine
	}
	if charset := t.Options["charset"]; charset != "" {
		tail += " DEFAULT CHARSET=" + charset
	}
	if t.Comment != "" {
		tail += " COMMENT=" + quoteString(t.Comment)
	}
	return fmt.Sprintf("CREATE TABLE %s (\n%s\n)%s;", fq, strings.Join(lines, ",\n"), tail), nil
}

// GenerateAlterTable renders a ChangeSet into ALTER statements in safe
// execution order: columns → primary key → indexes → foreign keys → options.
func (d dialect) GenerateAlterTable(db, schema, table string, cs dbdriver.ChangeSet) ([]string, error) {
	if strings.TrimSpace(table) == "" {
		return nil, fmt.Errorf("mysqldrv: GenerateAlterTable: table name is empty")
	}
	fq := dbdriver.QualifyTable(d, db, schema, table)
	var stmts []string

	for _, ch := range cs.Columns {
		switch ch.Kind {
		case dbdriver.ColumnDrop:
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s DROP COLUMN %s;", fq, d.QuoteIdentifier(ch.Name)))
		case dbdriver.ColumnAdd:
			if ch.Column == nil {
				return nil, fmt.Errorf("mysqldrv: add column change without definition")
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s%s;", fq, d.fullColumnDef(*ch.Column), positionClause(d, ch.Position)))
		case dbdriver.ColumnModify:
			if ch.Column == nil {
				return nil, fmt.Errorf("mysqldrv: modify column change without definition")
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s MODIFY COLUMN %s%s;", fq, d.fullColumnDef(*ch.Column), positionClause(d, ch.Position)))
		case dbdriver.ColumnRename:
			if ch.Column == nil {
				return nil, fmt.Errorf("mysqldrv: rename column change without definition")
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s CHANGE COLUMN %s %s%s;", fq, d.QuoteIdentifier(ch.Name), d.fullColumnDef(*ch.Column), positionClause(d, ch.Position)))
		default:
			return nil, fmt.Errorf("mysqldrv: unknown column change kind %q", ch.Kind)
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
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s DROP INDEX %s;", fq, d.QuoteIdentifier(ch.Name)))
		case "add":
			if ch.Index == nil {
				return nil, fmt.Errorf("mysqldrv: add index change without definition")
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD %s;", fq, d.indexSpec(*ch.Index)))
		default:
			return nil, fmt.Errorf("mysqldrv: unknown index change kind %q", ch.Kind)
		}
	}

	for _, ch := range cs.ForeignKeys {
		switch ch.Kind {
		case "drop":
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s DROP FOREIGN KEY %s;", fq, d.QuoteIdentifier(ch.Name)))
		case "add":
			if ch.ForeignKey == nil {
				return nil, fmt.Errorf("mysqldrv: add foreign-key change without definition")
			}
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s ADD %s;", fq, d.fkSpec(*ch.ForeignKey)))
		default:
			return nil, fmt.Errorf("mysqldrv: unknown foreign-key change kind %q", ch.Kind)
		}
	}

	for _, opt := range cs.Options {
		switch opt.Name {
		case "comment":
			stmts = append(stmts, fmt.Sprintf("ALTER TABLE %s COMMENT = %s;", fq, quoteString(opt.Value)))
		default:
			return nil, fmt.Errorf("mysqldrv: unknown table option %q", opt.Name)
		}
	}
	return stmts, nil
}
