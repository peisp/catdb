// editor/sqlContext — cursor-context analysis for SQL completion.
//
// A port of the dbx completion engine's context detection (regex/string-scan
// based, no AST). Answers: where is the cursor inside the statement (table
// position? column position? INSERT column list? JOIN ON?…), what identifier
// is being typed (with dotted qualifier), and which tables the statement
// references (FROM/JOIN + aliases + CTEs + subquery aliases).
//
// The identifier-quote character is dialect-configurable: getSqlContext takes
// it as a parameter (backtick for MySQL, double quote for Postgres/ANSI) and
// installs it module-wide for the duration of the call — all helpers run
// synchronously inside it. When the quote is `"`, double quotes are lexed as
// identifiers instead of strings, matching ANSI semantics.
//
// Pure string functions with no CodeMirror dependency so the whole engine is
// testable headless. Consumed by sqlCompletion.ts.

/** A table referenced by the statement (FROM/JOIN/UPDATE/INTO, CTE, subquery). */
export interface RefTable {
  name: string
  db?: string
  alias?: string
  /** Columns known without a catalog lookup (CTE / subquery projections). */
  columns?: string[]
}

export type ClauseKind =
  | 'table' // after FROM/JOIN/INTO/UPDATE/DESCRIBE — expecting a table name
  | 'use' // after USE — expecting a database name
  | 'insert-columns' // inside INSERT INTO t ( … )
  | 'values' // inside VALUES ( … )
  | 'set' // inside UPDATE … SET …
  | 'on' // inside JOIN … ON …
  | 'select-list' // between SELECT and FROM
  | 'order-group' // inside ORDER BY / GROUP BY
  | 'column' // other column-expecting position (WHERE …)
  | 'generic'

export interface SqlContext {
  /** Cursor is inside a string literal or comment — never complete. */
  suppressed: boolean
  /** The word being typed (may be empty). */
  prefix: string
  /** Document offset where the prefix starts (completion replace-from). */
  from: number
  /** Prefix was opened with a backtick. */
  quoted: boolean
  /** Dotted parts typed before the prefix (`db.table.` → ['db','table']). */
  qualifier: string[]
  clause: ClauseKind
  /** Last full word before the token, lowercased. */
  lastWord: string
  /** Table completions should append a generated alias (FROM/JOIN position). */
  autoAlias: boolean
  /** Cursor right after `FROM tbl ` — the "name an alias here" slot. */
  aliasSlot: { table: string } | null
  /** INSERT INTO target while inside its column list. */
  insertTarget: { db?: string; table: string; listed: string[] } | null
  /** UPDATE target while inside its SET clause. */
  updateTarget: { db?: string; table: string; assigned: string[] } | null
  /** `col =` / `col >` … immediately before the token. */
  comparison: { column: string } | null
  /** Tables in scope for this statement. */
  refs: RefTable[]
  /** SELECT-list aliases (for ORDER BY / GROUP BY completion). */
  selectAliases: string[]
  statement: string
}

// Identifier-quote state — installed by getSqlContext, read by every helper.
let IDQ = '`'
let IDENT = identPattern(IDQ)
let REF_RE = makeRefRe()

function identPattern(q: string): string {
  return `${q}[^${q}]+${q}|[A-Za-z_][\\w$]*`
}

function makeRefRe(): RegExp {
  return new RegExp(
    String.raw`\b(?:from|join|update|into)\s+(${IDENT})(?:\s*\.\s*(${IDENT}))?(?:\s+(?:as\s+)?(${IDENT}))?`,
    'gi',
  )
}

/** Install the dialect's identifier-quote character (idempotent). */
function setIdentQuote(q: string) {
  const next = q === '"' ? '"' : '`'
  if (next === IDQ) return
  IDQ = next
  IDENT = identPattern(IDQ)
  REF_RE = makeRefRe()
}

/** Keywords that must never be mistaken for a table alias. */
export const ALIAS_BLOCKLIST = new Set([
  'where', 'group', 'order', 'having', 'limit', 'offset', 'join', 'inner',
  'left', 'right', 'outer', 'cross', 'natural', 'straight_join', 'on', 'using',
  'set', 'values', 'as', 'union', 'select', 'and', 'or', 'not', 'when', 'then',
  'else', 'end', 'is', 'in', 'like', 'between', 'asc', 'desc', 'for', 'into',
  'from', 'with', 'partition', 'window', 'force', 'use', 'ignore',
])

const TABLE_TRIGGERS = new Set(['from', 'join', 'into', 'update', 'table', 'describe'])

function unquote(s: string): string {
  return s[0] === IDQ ? s.slice(1, -1) : s
}

// ── Lexical scan ─────────────────────────────────────────────────────────────

interface LexState {
  suppressed: boolean
  stmtStart: number
  stmtEnd: number
}

/**
 * One forward scan up to `pos`: tracks string/comment state (for suppression)
 * and the last top-level `;` (statement start), then scans forward for the
 * statement end so the FROM clause is visible even with the cursor in the
 * SELECT list.
 */
export function lexAt(doc: string, pos: number): LexState {
  let stmtStart = 0
  let inS = false, inD = false, inB = false, inLine = false, inBlock = false
  for (let i = 0; i < pos; i++) {
    const ch = doc[i]
    const next = doc[i + 1]
    if (inLine) { if (ch === '\n') inLine = false; continue }
    if (inBlock) { if (ch === '*' && next === '/') { inBlock = false; i++ } continue }
    if (inS) { if (ch === '\\') i++; else if (ch === "'") inS = false; continue }
    if (inD) { if (ch === '\\') i++; else if (ch === '"') inD = false; continue }
    if (inB) { if (ch === IDQ) inB = false; continue }
    if (ch === '-' && next === '-') { inLine = true; i++; continue }
    if (ch === '#') { inLine = true; continue }
    if (ch === '/' && next === '*') { inBlock = true; i++; continue }
    if (ch === "'") { inS = true; continue }
    if (ch === IDQ) { inB = true; continue }
    if (ch === '"') { inD = true; continue }
    if (ch === ';') stmtStart = i + 1
  }
  let stmtEnd = doc.length
  let q: string | null = null
  for (let i = pos; i < doc.length; i++) {
    const ch = doc[i]
    if (q) { if (ch === q) q = null; continue }
    if (ch === "'" || ch === '"' || ch === '`') { q = ch; continue }
    if (ch === ';') { stmtEnd = i; break }
  }
  // Inside a backtick identifier is fine (completing a quoted name); strings
  // and comments suppress completion entirely.
  return { suppressed: inS || inD || inLine || inBlock, stmtStart, stmtEnd }
}

// ── Trailing token ───────────────────────────────────────────────────────────

interface TrailingToken {
  prefix: string
  quoted: boolean
  /** Offset (within `before`) where the prefix starts. */
  start: number
  /** Offset where the whole dotted token starts (first qualifier part). */
  tokenStart: number
  qualifier: string[]
}

function readPartBack(s: string, end: number): { text: string; quoted: boolean; start: number } | null {
  if (s[end - 1] === IDQ) {
    const open = s.lastIndexOf(IDQ, end - 2)
    if (open < 0) return null
    return { text: s.slice(open + 1, end - 1), quoted: true, start: open }
  }
  let j = end
  while (j > 0 && /[\w$]/.test(s[j - 1])) j--
  if (j === end) return null
  return { text: s.slice(j, end), quoted: false, start: j }
}

/** Parse the identifier token ending at the cursor, dot-qualifier aware. */
export function parseTrailingToken(before: string): TrailingToken {
  let prefix = ''
  let quoted = false
  let start = before.length
  if (before[before.length - 1] !== '.') {
    const p = readPartBack(before, before.length)
    if (p) {
      prefix = p.text
      start = p.start
      quoted = p.quoted
      // Open (unterminated) identifier quote before a plain word: `us|
      if (!p.quoted && before[p.start - 1] === IDQ) {
        quoted = true
        start = p.start - 1
        // prefix stays the inner text; `from` points at the quote so the
        // whole quoted token is replaced on apply.
      }
    }
  }
  const qualifier: string[] = []
  let end = start
  while (before[end - 1] === '.') {
    const q = readPartBack(before, end - 1)
    if (!q) break
    qualifier.unshift(q.text)
    end = q.start
  }
  return { prefix, quoted, start, tokenStart: end, qualifier }
}

// ── Paren/quote-aware helpers ────────────────────────────────────────────────

/** Index of the `)` matching the `(` at `open`, or -1. Quote-aware. */
export function findMatchingParen(s: string, open: number): number {
  let depth = 0
  let q: string | null = null
  for (let i = open; i < s.length; i++) {
    const ch = s[i]
    if (q) { if (ch === q) q = null; continue }
    if (ch === "'" || ch === '"' || ch === '`') { q = ch; continue }
    if (ch === '(') depth++
    else if (ch === ')') { depth--; if (depth === 0) return i }
  }
  return -1
}

/** Split on top-level commas (depth 0, quote-aware). */
function splitTopLevel(s: string): string[] {
  const out: string[] = []
  let depth = 0
  let q: string | null = null
  let last = 0
  for (let i = 0; i < s.length; i++) {
    const ch = s[i]
    if (q) { if (ch === q) q = null; continue }
    if (ch === "'" || ch === '"' || ch === '`') { q = ch; continue }
    if (ch === '(') depth++
    else if (ch === ')') depth--
    else if (ch === ',' && depth === 0) { out.push(s.slice(last, i)); last = i + 1 }
  }
  out.push(s.slice(last))
  return out
}

/** First index of `word` at paren depth 0 (quote-aware, case-insensitive). */
function topLevelWordIndex(s: string, word: string): number {
  const re = new RegExp(String.raw`\b${word}\b`, 'gi')
  let m: RegExpExecArray | null
  while ((m = re.exec(s))) {
    const i = m.index
    // check depth & quote state up to i
    let depth = 0
    let q: string | null = null
    for (let j = 0; j < i; j++) {
      const ch = s[j]
      if (q) { if (ch === q) q = null; continue }
      if (ch === "'" || ch === '"' || ch === '`') { q = ch; continue }
      if (ch === '(') depth++
      else if (ch === ')') depth--
    }
    if (depth === 0 && !q) return i
  }
  return -1
}

// ── Referenced tables ────────────────────────────────────────────────────────

function extractRefsWithIndex(stmt: string): Array<{ ref: RefTable; index: number }> {
  const out: Array<{ ref: RefTable; index: number }> = []
  REF_RE.lastIndex = 0
  let m: RegExpExecArray | null
  while ((m = REF_RE.exec(stmt))) {
    const first = unquote(m[1])
    const second = m[2] ? unquote(m[2]) : undefined
    let alias = m[3] ? unquote(m[3]) : undefined
    if (alias && ALIAS_BLOCKLIST.has(alias.toLowerCase())) alias = undefined
    const ref: RefTable = second ? { db: first, name: second, alias } : { name: first, alias }
    out.push({ ref, index: m.index })
  }
  return out
}

/** FROM/JOIN/UPDATE/INTO tables (with optional db prefix and alias). */
export function extractReferencedTables(stmt: string): RefTable[] {
  return extractRefsWithIndex(stmt).map((r) => r.ref)
}

/** Column names a SELECT body projects (aliases win; `*` is skipped). */
export function extractSelectColumnNames(body: string): string[] {
  const selIdx = topLevelWordIndex(body, 'select')
  if (selIdx < 0) return []
  let list = body.slice(selIdx + 6)
  const fromIdx = topLevelWordIndex(list, 'from')
  if (fromIdx >= 0) list = list.slice(0, fromIdx)
  const out: string[] = []
  for (let expr of splitTopLevel(list)) {
    expr = expr.trim().replace(/^distinct\s+/i, '')
    if (!expr || expr === '*' || expr.endsWith('*')) continue
    const alias = extractExprAlias(expr)
    if (alias) { out.push(alias); continue }
    // plain (possibly dotted) column reference → last part
    const m = new RegExp(String.raw`^(?:(?:${IDENT})\s*\.\s*)?(${IDENT})$`).exec(expr)
    if (m) out.push(unquote(m[1]))
  }
  return out
}

/** Explicit `AS x` / implicit trailing-word alias of one select expression. */
function extractExprAlias(expr: string): string | null {
  const explicit = new RegExp(String.raw`\bas\s+(${IDENT})\s*$`, 'i').exec(expr)
  if (explicit) return unquote(explicit[1])
  const implicit = new RegExp(String.raw`(?:^|[\s)])(${IDENT})\s*$`).exec(expr)
  if (!implicit) return null
  const candidate = unquote(implicit[1])
  if (ALIAS_BLOCKLIST.has(candidate.toLowerCase())) return null
  const rest = expr.slice(0, expr.length - implicit[1].length).trim()
  // `users.name` / bare `name` has no implicit alias; `COUNT(*) cnt` does.
  if (!rest || /^[A-Za-z_][\w$]*(\s*\.\s*[A-Za-z_][\w$]*)?$/.test(expr.trim())) return null
  return candidate
}

/** Aliases assigned in the SELECT list (for ORDER BY / GROUP BY). */
export function extractSelectAliases(stmt: string): string[] {
  const selIdx = topLevelWordIndex(stmt, 'select')
  if (selIdx < 0) return []
  let list = stmt.slice(selIdx + 6)
  const fromIdx = topLevelWordIndex(list, 'from')
  if (fromIdx >= 0) list = list.slice(0, fromIdx)
  const out: string[] = []
  for (const expr of splitTopLevel(list)) {
    const a = extractExprAlias(expr.trim())
    if (a) out.push(a)
  }
  return out
}

interface NamedRef {
  ref: RefTable
  /** Body span `( … )` — refs inside belong to the inner query's scope. */
  bodyStart: number
  bodyEnd: number
}

/** WITH-clause CTEs: name + projected columns + body span. */
function cteDefsWithSpans(stmt: string): NamedRef[] {
  const out: NamedRef[] = []
  const withM = /\bwith\b/i.exec(stmt)
  if (!withM) return out
  let i = withM.index + 4
  const rec = /^\s*recursive\b/i.exec(stmt.slice(i))
  if (rec) i += rec[0].length
  for (;;) {
    while (i < stmt.length && /[\s,;]/.test(stmt[i])) i++
    const nameM = /^([A-Za-z_][\w$]*)/.exec(stmt.slice(i))
    if (!nameM) break
    const name = nameM[1]
    if (ALIAS_BLOCKLIST.has(name.toLowerCase())) break
    i += name.length
    while (i < stmt.length && /\s/.test(stmt[i])) i++
    let columns: string[] | undefined
    // optional explicit column list
    if (stmt[i] === '(') {
      const close = findMatchingParen(stmt, i)
      if (close < 0) break
      const inner = stmt.slice(i + 1, close)
      if (!/\bselect\b/i.test(inner)) {
        columns = splitTopLevel(inner).map((c) => unquote(c.trim())).filter(Boolean)
        i = close + 1
        while (i < stmt.length && /\s/.test(stmt[i])) i++
      }
    }
    const asM = /^as\b/i.exec(stmt.slice(i))
    if (asM) {
      i += 2
      while (i < stmt.length && /\s/.test(stmt[i])) i++
    }
    if (stmt[i] !== '(') break
    const close = findMatchingParen(stmt, i)
    if (close < 0) break
    if (!columns) columns = extractSelectColumnNames(stmt.slice(i + 1, close))
    out.push({ ref: { name, alias: name, columns }, bodyStart: i, bodyEnd: close })
    i = close + 1
    // continue only across `, next_cte AS (`
    const nextM = /^\s*,/.exec(stmt.slice(i))
    if (!nextM) break
  }
  return out
}

/** WITH-clause CTEs: name + projected columns. */
export function extractCteDefinitions(stmt: string): RefTable[] {
  return cteDefsWithSpans(stmt).map((n) => n.ref)
}

/** Derived tables: `( SELECT … ) alias` → alias + projected columns + span. */
function subqueryRefsWithSpans(stmt: string): NamedRef[] {
  const out: NamedRef[] = []
  const re = /\(\s*select\b/gi
  let m: RegExpExecArray | null
  while ((m = re.exec(stmt))) {
    const open = m.index
    const close = findMatchingParen(stmt, open)
    if (close < 0) continue
    const after = stmt.slice(close + 1)
    const am = new RegExp(String.raw`^\s*(?:as\s+)?(${IDENT})`, 'i').exec(after)
    if (!am) continue
    const alias = unquote(am[1])
    if (ALIAS_BLOCKLIST.has(alias.toLowerCase())) continue
    out.push({
      ref: { name: alias, alias, columns: extractSelectColumnNames(stmt.slice(open + 1, close)) },
      bodyStart: open,
      bodyEnd: close,
    })
  }
  return out
}

export function extractSubqueryRefs(stmt: string): RefTable[] {
  return subqueryRefsWithSpans(stmt).map((n) => n.ref)
}

/**
 * Tables in scope at `cursor`: FROM/JOIN refs merged with CTEs and subquery
 * aliases. Refs inside a CTE/subquery body belong to the INNER query — they're
 * dropped unless the cursor sits inside that body too (so `WITH x AS (SELECT …
 * FROM users) SELECT | FROM x` scopes to `x`, not `users`).
 */
export function referencedTables(stmt: string, cursor?: number): RefTable[] {
  const named = [...cteDefsWithSpans(stmt), ...subqueryRefsWithSpans(stmt)]
  const outOfScope = (i: number) =>
    named.some(
      (n) =>
        i > n.bodyStart && i < n.bodyEnd &&
        !(cursor !== undefined && cursor > n.bodyStart && cursor < n.bodyEnd),
    )
  const refs = extractRefsWithIndex(stmt)
    .filter((r) => !outOfScope(r.index))
    .map((r) => r.ref)
  for (const n of named) {
    const idx = refs.findIndex((r) => !r.db && r.name.toLowerCase() === n.ref.name.toLowerCase())
    // A FROM ref naming a CTE resolves to the CTE's columns, not the catalog.
    if (idx >= 0) refs[idx] = { ...refs[idx], columns: n.ref.columns }
    else refs.push(n.ref)
  }
  return refs
}

// ── Clause detection ─────────────────────────────────────────────────────────

/** Whether `before` (text up to the token) sits between SELECT and FROM. */
export function isInSelectList(before: string): boolean {
  const stack: boolean[] = [false]
  let q: string | null = null
  let word = ''
  const feed = (w: string) => {
    const lw = w.toLowerCase()
    if (lw === 'select') stack[stack.length - 1] = true
    else if (['from', 'where', 'group', 'order', 'having', 'limit', 'set', 'values', 'on', 'union'].includes(lw)) {
      stack[stack.length - 1] = false
    }
  }
  for (let i = 0; i < before.length; i++) {
    const ch = before[i]
    if (q) { if (ch === q) q = null; continue }
    if (ch === "'" || ch === '"' || ch === '`') { q = ch; word = ''; continue }
    if (/[\w$]/.test(ch)) { word += ch; continue }
    if (word) { feed(word); word = '' }
    if (ch === '(') stack.push(false)
    else if (ch === ')') { if (stack.length > 1) stack.pop() }
  }
  if (word) feed(word)
  return stack[stack.length - 1]
}

const COLUMN_KEYWORDS = new Set([
  'where', 'and', 'or', 'on', 'not', 'in', 'like', 'between', 'having', 'then',
  'when', 'else', 'by', 'select', 'distinct', 'is', 'case', 'coalesce', 'set',
])

// ── Master ───────────────────────────────────────────────────────────────────

export function getSqlContext(doc: string, pos: number, identQuote = '`'): SqlContext {
  setIdentQuote(identQuote)
  const { suppressed, stmtStart, stmtEnd } = lexAt(doc, pos)
  const statement = doc.slice(stmtStart, stmtEnd)
  const beforeCursor = doc.slice(stmtStart, pos)
  const tok = parseTrailingToken(beforeCursor)
  const beforeToken = beforeCursor.slice(0, tok.tokenStart)
  const lastWord = (/([A-Za-z_][\w$]*)\s*$/.exec(beforeToken)?.[1] ?? '').toLowerCase()

  const base: SqlContext = {
    suppressed,
    prefix: tok.prefix,
    from: stmtStart + tok.start,
    quoted: tok.quoted,
    qualifier: tok.qualifier,
    clause: 'generic',
    lastWord,
    autoAlias: false,
    aliasSlot: null,
    insertTarget: null,
    updateTarget: null,
    comparison: null,
    refs: [],
    selectAliases: [],
    statement,
  }
  if (suppressed) return base

  base.refs = referencedTables(statement, pos - stmtStart)

  // INSERT INTO t (col, …|  — inside the column list, before the `)`
  const insertM = new RegExp(
    String.raw`\binsert\s+(?:ignore\s+)?into\s+(${IDENT})(?:\s*\.\s*(${IDENT}))?\s*\(([^)]*)$`,
    'i',
  ).exec(beforeToken)
  // VALUES ( …|
  const inValues = /\bvalues\s*\([^)]*$/i.test(beforeToken)

  // UPDATE t [alias] SET …| (until WHERE)
  let updateTarget: SqlContext['updateTarget'] = null
  const updM = new RegExp(
    String.raw`\bupdate\s+(${IDENT})(?:\s*\.\s*(${IDENT}))?(?:\s+(?:as\s+)?[A-Za-z_][\w$]*)?\s+set\b`,
    'i',
  ).exec(beforeToken)
  if (updM) {
    const tail = beforeToken.slice(updM.index + updM[0].length)
    if (!/\bwhere\b/i.test(tail)) {
      const assigned = [...tail.matchAll(new RegExp(`(${IDENT})\\s*=`, 'g'))].map((m) => unquote(m[1]))
      updateTarget = updM[2]
        ? { db: unquote(updM[1]), table: unquote(updM[2]), assigned }
        : { table: unquote(updM[1]), assigned }
    }
  }

  // JOIN … ON …| (also `ON … AND …|`)
  let onCtx = false
  const joinIdx = beforeToken.toLowerCase().lastIndexOf('join ')
  if (joinIdx >= 0) {
    const tail = beforeToken.slice(joinIdx)
    onCtx = /\bon\b/i.test(tail) && /\b(on|and|or)\s*$/i.test(beforeToken)
  }

  // ORDER BY / GROUP BY …|
  let orderGroup = false
  const lower = beforeToken.toLowerCase()
  const obIdx = Math.max(lower.lastIndexOf('order by'), lower.lastIndexOf('group by'))
  if (obIdx >= 0) {
    const tail = lower.slice(obIdx + 8)
    orderGroup = !/\b(where|having|limit|offset|from|join|union|select|set)\b/.test(tail)
  }

  // table position?
  let tableTrigger = TABLE_TRIGGERS.has(lastWord)
  let tableList = false
  if (!tableTrigger && /,\s*$/.test(beforeToken)) {
    const kwIdx = Math.max(
      ...['from', 'join', 'update', 'into'].map((k) => lower.lastIndexOf(k + ' ')),
    )
    const stopIdx = Math.max(
      ...['where', 'set', 'on', 'having', 'group', 'order', 'select', 'values', '('].map((k) =>
        lower.lastIndexOf(k),
      ),
    )
    tableList = kwIdx >= 0 && kwIdx > stopIdx
    tableTrigger = tableList
  }
  base.autoAlias = lastWord === 'from' || lastWord === 'join' || tableList

  // alias slot: `FROM tbl |` (a table just typed, nothing after it yet)
  const slotM = new RegExp(
    String.raw`\b(?:from|join)\s+(${IDENT})(?:\s*\.\s*(${IDENT}))?\s+$`,
    'i',
  ).exec(beforeToken)
  if (slotM) {
    const table = slotM[2] ? unquote(slotM[2]) : unquote(slotM[1])
    if (!ALIAS_BLOCKLIST.has(table.toLowerCase())) base.aliasSlot = { table }
  }

  // comparison: `col = |`, `t.col > |`
  const cmpM = new RegExp(
    String.raw`(${IDENT}(?:\s*\.\s*(?:${IDENT}))?)\s*(?:=|!=|<>|<=|>=|<|>)\s*$`,
  ).exec(beforeToken)
  if (cmpM) base.comparison = { column: cmpM[1].split(IDQ).join('') }

  // clause priority (mirrors dbx detectCompletionContextKind)
  if (lastWord === 'use') base.clause = 'use'
  else if (insertM) {
    base.clause = 'insert-columns'
    const listed = insertM[3].split(',').map((s) => unquote(s.trim())).filter(Boolean)
    base.insertTarget = insertM[2]
      ? { db: unquote(insertM[1]), table: unquote(insertM[2]), listed }
      : { table: unquote(insertM[1]), listed }
  } else if (inValues) base.clause = 'values'
  else if (updateTarget && lastWord !== 'update') {
    base.clause = 'set'
    base.updateTarget = updateTarget
  } else if (onCtx) base.clause = 'on'
  else if (tableTrigger) base.clause = 'table'
  else if (orderGroup) {
    base.clause = 'order-group'
    base.selectAliases = extractSelectAliases(statement)
  } else if (isInSelectList(beforeToken)) base.clause = 'select-list'
  else if (
    COLUMN_KEYWORDS.has(lastWord) ||
    /[=<>!+\-*/%(,]\s*$/.test(beforeToken)
  ) base.clause = 'column'

  return base
}
