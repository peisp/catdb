// Package sqlclass is the AI Agent safety model's statement classifier
// (AGENT_DESIGN.md §5 gate 2). It answers one question about a piece of SQL:
// how dangerous is it — read, write_dml, ddl, admin, or (when the lexer can't
// tell) unknown.
//
// Direction is strict on purpose: this gate can only ever be MORE conservative,
// never less. Anything the lexer cannot confidently place falls to
// dbdriver.ClassUnknown, which is treated as the highest risk (== admin) by the
// surrounding gates. We do NOT run a full cross-dialect SQL parser (too costly,
// too fragile); we do lexical analysis — skip comments and string/identifier
// literals, track parenthesis depth, peel the WITH/EXPLAIN prefixes — and take
// the first meaningful keyword. Lexical ambiguity resolves to UNKNOWN.
//
// Drivers may override dialect-specific statements via the optional
// dbdriver.StatementClassifier extension; a nil/unknown override hands the
// statement back to this generic classifier.
package sqlclass

import (
	"strings"

	"catdb/internal/core/sqlscript"
	"catdb/internal/dbdriver"
)

// Classified pairs one split statement with its verdict.
type Classified struct {
	SQL string                           `json:"sql"`
	C   dbdriver.StatementClassification `json:"c"`
}

// ClassifyStatement classifies a single SQL statement lexically.
//
// Judgment calls (all err toward stricter):
//   - A MySQL executable comment (/*! ... */) is NOT a real comment — its body
//     executes. Rather than parse it, the whole statement falls to UNKNOWN.
//   - INTO OUTFILE / INTO DUMPFILE writes the server filesystem → ADMIN.
//   - EXPLAIN <stmt> is READ (it does not execute); EXPLAIN ANALYZE <stmt> is
//     classified by the inner statement (some engines truly run it).
//   - A writable CTE (WITH x AS (DELETE …) …) takes the riskiest verb among all
//     CTE bodies and the main statement.
//   - An unrecognized leading keyword → UNKNOWN, with the keyword kept as Verb
//     for audit; an empty/verbless statement → UNKNOWN with empty Verb.
func ClassifyStatement(sql string) dbdriver.StatementClassification {
	toks, sawExec := tokenize(sql)
	if sawExec {
		// Executable comment present: body participates as SQL. We do not
		// interpret it — strictest safe verdict.
		return unknownCls("")
	}
	return classify(toks, 0)
}

// Riskier returns the more dangerous of two classes.
//
// Ordering: read < write_dml < ddl < admin < unknown. AGENT_DESIGN.md §5 puts
// UNKNOWN and ADMIN at the same (highest) risk *level* — both are rejected for
// the Agent. We rank UNKNOWN one notch above ADMIN so it wins ties, which keeps
// the strict direction visible; downstream gates treat both as "highest" and
// reject either, so the exact tie-break is immaterial to safety.
func Riskier(a, b dbdriver.StatementClass) dbdriver.StatementClass {
	if rank(b) > rank(a) {
		return b
	}
	return a
}

// ClassifyScript splits sqlText with rules (reusing core/sqlscript) and
// classifies each statement. If override is non-nil and returns a class other
// than ClassUnknown for a statement, that verdict is adopted; otherwise the
// generic classifier runs. batch is the whole-script verdict: the riskiest
// statement's classification (its Verb and MissingWhere come along), which is
// what gate 3 (session grant) checks the batch against. An all-comment/empty
// script yields a READ baseline batch.
func ClassifyScript(sqlText string, rules dbdriver.ScriptRules, override dbdriver.StatementClassifier) (stmts []Classified, batch dbdriver.StatementClassification) {
	parts := sqlscript.Split(sqlText, rules)
	batch = dbdriver.StatementClassification{Class: dbdriver.ClassRead}
	for _, p := range parts {
		c := classifyOne(p, override)
		stmts = append(stmts, Classified{SQL: p, C: c})
		if rank(c.Class) > rank(batch.Class) {
			batch = c
		}
	}
	return stmts, batch
}

// classifyOne applies the override-first-then-generic pipeline to one statement.
func classifyOne(sql string, override dbdriver.StatementClassifier) dbdriver.StatementClassification {
	if override != nil {
		if c := override.ClassifyStatement(sql); c.Class != dbdriver.ClassUnknown {
			return c
		}
	}
	return ClassifyStatement(sql)
}

// ---- core classification over a token slice --------------------------------

// classify classifies the statement represented by toks. base is the paren
// depth of the statement's top level (0 for a whole statement; >0 for a CTE
// body slice whose tokens keep their original absolute depth).
func classify(toks []token, base int) dbdriver.StatementClassification {
	fi := firstWord(toks)
	if fi < 0 {
		return unknownCls("")
	}
	switch w := toks[fi].word; w {
	case "explain":
		return classifyExplain(toks, fi, base)
	case "with":
		return classifyWith(toks, fi, base)
	default:
		return classifyVerb(toks, w, base)
	}
}

func classifyVerb(toks []token, verb string, base int) dbdriver.StatementClassification {
	res := dbdriver.StatementClassification{Class: classFor(verb), Verb: dbdriver.StatementVerb(verb)}
	// SELECT ... INTO OUTFILE/DUMPFILE writes the server filesystem → ADMIN.
	if hasIntoFile(toks, base) {
		res.Class = dbdriver.ClassAdmin
	}
	if verb == "update" || verb == "delete" {
		res.MissingWhere = !hasTopLevelWhere(toks, base)
	}
	return res
}

func classifyExplain(toks []token, fi, base int) dbdriver.StatementClassification {
	analyze := false
	innerStart := -1
	for k := fi + 1; k < len(toks); k++ {
		if toks[k].word == "" {
			continue
		}
		if toks[k].word == "analyze" {
			analyze = true
			continue
		}
		if isStmtStart(toks[k].word) {
			innerStart = k
			break
		}
		// Other words are EXPLAIN options (FORMAT, VERBOSE, JSON, …) — ignore.
	}
	if analyze {
		if innerStart >= 0 {
			// EXPLAIN ANALYZE truly executes: classify by the inner statement.
			return classify(toks[innerStart:], base)
		}
		return unknownCls("explain")
	}
	// Plain EXPLAIN does not execute the statement.
	return dbdriver.StatementClassification{Class: dbdriver.ClassRead, Verb: "explain"}
}

// classifyWith peels a WITH [RECURSIVE] cte1 AS (body1) [, …] prefix, scanning
// every CTE body's leading verb and the main statement, and returns the
// riskiest verdict among them (a writable CTE must not hide behind a SELECT).
func classifyWith(toks []token, fi, base int) dbdriver.StatementClassification {
	depth0 := toks[fi].depth
	var cands []dbdriver.StatementClassification
	i := fi + 1
	for {
		// The CTE body opens at the first '(' at depth0 *after* the AS keyword.
		// (A column list "cte (a, b) AS (…)" also parenthesizes, but its parens
		// sit at depth0 before AS — anchoring on AS skips it.)
		asIdx := indexWordAtDepth(toks, i, "as", depth0)
		if asIdx < 0 {
			break // malformed WITH
		}
		openIdx := indexPunctAtDepth(toks, asIdx+1, '(', depth0)
		if openIdx < 0 {
			break
		}
		closeIdx := indexPunctAtDepth(toks, openIdx+1, ')', depth0)
		if closeIdx < 0 {
			break
		}
		cands = append(cands, classify(toks[openIdx+1:closeIdx], depth0+1))

		next := closeIdx + 1
		if next < len(toks) && toks[next].punct == ',' && toks[next].depth == depth0 {
			i = next + 1
			continue // another CTE
		}
		// Whatever follows the last CTE is the main statement.
		if next < len(toks) {
			cands = append(cands, classify(toks[next:], depth0))
		}
		break
	}
	if len(cands) == 0 {
		return unknownCls("with")
	}
	best := cands[0]
	for _, c := range cands[1:] {
		if rank(c.Class) > rank(best.Class) {
			best = c
		}
	}
	return best
}

// ---- verb tables -----------------------------------------------------------

var (
	readVerbs  = set("select", "show", "explain", "desc", "describe", "table", "values", "help")
	writeVerbs = set("insert", "update", "delete", "replace", "merge")
	ddlVerbs   = set("create", "alter", "drop", "truncate", "rename")
	adminVerbs = set(
		"grant", "revoke", "set", "kill", "shutdown", "load", "use", "call",
		"lock", "unlock", "flush", "reset", "analyze", "optimize", "repair",
		"start", "begin", "commit", "rollback", "savepoint", "release", "xa",
		"purge", "change", "stop", "install", "uninstall", "handler", "prepare",
		"execute", "deallocate", "do", "copy",
	)
)

func classFor(verb string) dbdriver.StatementClass {
	switch {
	case readVerbs[verb]:
		return dbdriver.ClassRead
	case writeVerbs[verb]:
		return dbdriver.ClassWriteDML
	case ddlVerbs[verb]:
		return dbdriver.ClassDDL
	case adminVerbs[verb]:
		return dbdriver.ClassAdmin
	default:
		return dbdriver.ClassUnknown
	}
}

// isStmtStart reports whether verb can begin a (sub)statement — a known verb or
// the WITH prefix — used to find the inner statement after EXPLAIN [ANALYZE].
func isStmtStart(verb string) bool {
	return verb == "with" || classFor(verb) != dbdriver.ClassUnknown
}

func rank(c dbdriver.StatementClass) int {
	switch c {
	case dbdriver.ClassRead:
		return 0
	case dbdriver.ClassWriteDML:
		return 1
	case dbdriver.ClassDDL:
		return 2
	case dbdriver.ClassAdmin:
		return 3
	default: // ClassUnknown
		return 4
	}
}

func unknownCls(verb string) dbdriver.StatementClassification {
	return dbdriver.StatementClassification{Class: dbdriver.ClassUnknown, Verb: dbdriver.StatementVerb(verb)}
}

// ---- token helpers ---------------------------------------------------------

func firstWord(toks []token) int {
	for i := range toks {
		if toks[i].word != "" {
			return i
		}
	}
	return -1
}

func indexWordAtDepth(toks []token, from int, word string, depth int) int {
	for k := from; k < len(toks); k++ {
		if toks[k].word == word && toks[k].depth == depth {
			return k
		}
	}
	return -1
}

func indexPunctAtDepth(toks []token, from int, p byte, depth int) int {
	for k := from; k < len(toks); k++ {
		if toks[k].punct == p && toks[k].depth == depth {
			return k
		}
	}
	return -1
}

// hasTopLevelWhere reports whether a WHERE keyword appears at the statement's
// top level (paren depth == base) — not inside a subquery, string, or comment.
func hasTopLevelWhere(toks []token, base int) bool {
	return indexWordAtDepth(toks, 0, "where", base) >= 0
}

// hasIntoFile reports a top-level INTO OUTFILE / INTO DUMPFILE clause.
func hasIntoFile(toks []token, base int) bool {
	for k := 0; k < len(toks)-1; k++ {
		if toks[k].word == "into" && toks[k].depth == base {
			nx := nextWord(toks, k+1)
			if nx >= 0 && (toks[nx].word == "outfile" || toks[nx].word == "dumpfile") {
				return true
			}
		}
	}
	return false
}

func nextWord(toks []token, from int) int {
	for k := from; k < len(toks); k++ {
		if toks[k].word != "" {
			return k
		}
	}
	return -1
}

func set(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// ---- tokenizer -------------------------------------------------------------

// token is a lexical unit relevant to classification: a lowercased keyword/word,
// or one of the structural punctuation bytes '(' ')' ','. String literals and
// quoted identifiers are consumed and dropped (their contents never influence
// classification). depth is the paren nesting at the token's position.
type token struct {
	word  string // non-empty ⇒ a word token (lowercased)
	punct byte   // '(', ')' or ',' ⇒ a punctuation token; 0 otherwise
	depth int
}

// tokenize lexes sql. sawExec is set when a MySQL executable comment (/*! … */)
// is seen outside of a string — the caller treats the whole statement as
// UNKNOWN. Generic MySQL-ish lexical rules apply (backtick identifiers, '#'
// line comments, backslash escapes) since they only affect what we skip.
func tokenize(sql string) (toks []token, sawExec bool) {
	n := len(sql)
	depth := 0
	for i := 0; i < n; {
		c := sql[i]
		switch {
		case isSpace(c):
			i++
		case c == '-' && i+1 < n && sql[i+1] == '-' && (i+2 >= n || isSpace(sql[i+2])):
			i = skipToEOL(sql, i)
		case c == '#':
			i = skipToEOL(sql, i)
		case c == '/' && i+1 < n && sql[i+1] == '*':
			if i+2 < n && sql[i+2] == '!' {
				sawExec = true
			}
			i = skipBlockComment(sql, i+2)
		case c == '\'' || c == '"' || c == '`':
			i = skipQuoted(sql, i, c)
		case c == '(':
			toks = append(toks, token{punct: '(', depth: depth})
			depth++
			i++
		case c == ')':
			if depth > 0 {
				depth--
			}
			toks = append(toks, token{punct: ')', depth: depth})
			i++
		case c == ',':
			toks = append(toks, token{punct: ',', depth: depth})
			i++
		case isWordByte(c):
			j := i + 1
			for j < n && isWordByte(sql[j]) {
				j++
			}
			toks = append(toks, token{word: strings.ToLower(sql[i:j]), depth: depth})
			i = j
		default:
			i++ // operators, '=', ';', '*', … — not needed for classification
		}
	}
	return toks, sawExec
}

func skipToEOL(s string, i int) int {
	for i < len(s) && s[i] != '\n' {
		i++
	}
	return i
}

func skipBlockComment(s string, i int) int {
	n := len(s)
	for i < n {
		if s[i] == '*' && i+1 < n && s[i+1] == '/' {
			return i + 2
		}
		i++
	}
	return n
}

// skipQuoted consumes a quoted literal/identifier starting at the opening quote
// q. Doubled quotes escape for all kinds; backslash escapes apply to ' and "
// (not backtick). Returns the index past the closing quote (or EOF).
func skipQuoted(s string, i int, q byte) int {
	n := len(s)
	i++ // past opening quote
	for i < n {
		c := s[i]
		if c == '\\' && q != '`' && i+1 < n {
			i += 2
			continue
		}
		if c == q {
			if i+1 < n && s[i+1] == q {
				i += 2
				continue
			}
			return i + 1
		}
		i++
	}
	return n
}

func isWordByte(c byte) bool {
	return c == '_' ||
		(c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') ||
		(c >= '0' && c <= '9')
}

func isSpace(c byte) bool {
	switch c {
	case ' ', '\t', '\n', '\r', '\f', '\v':
		return true
	}
	return false
}
