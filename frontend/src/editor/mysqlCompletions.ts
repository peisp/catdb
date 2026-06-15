// MySQL-flavored completions layered on top of @codemirror/lang-sql's built-in
// keyword + schema sources. Two flavors:
//   * functions: built-in scalar/aggregate functions (NOW, COUNT, …) — these
//     don't ship inside the SQL dialect's keyword list, so completion would
//     otherwise miss them entirely.
//   * snippets: tab-stop templates for the statement skeletons we type 100x
//     a day (SELECT … FROM … WHERE, INSERT INTO …, JOIN …).
//
// Both register through @codemirror/autocomplete's CompletionSource API and
// are wired into the editor by `SqlEditor.vue`.
import type { Completion, CompletionContext, CompletionResult, CompletionSource } from '@codemirror/autocomplete'
import { snippetCompletion } from '@codemirror/autocomplete'

// Common MySQL functions. Curated rather than exhaustive — the goal is the
// 90th-percentile lookup, not the manual. `info` shows in the popup detail.
const MYSQL_FUNCTIONS: Array<{ label: string; detail: string; info?: string }> = [
  // aggregates
  { label: 'COUNT', detail: 'aggregate', info: 'COUNT(expr) — number of non-NULL rows' },
  { label: 'SUM', detail: 'aggregate', info: 'SUM(expr) — sum of expr' },
  { label: 'AVG', detail: 'aggregate', info: 'AVG(expr) — average of expr' },
  { label: 'MIN', detail: 'aggregate', info: 'MIN(expr)' },
  { label: 'MAX', detail: 'aggregate', info: 'MAX(expr)' },
  { label: 'GROUP_CONCAT', detail: 'aggregate', info: 'GROUP_CONCAT([DISTINCT] expr [ORDER BY …] [SEPARATOR str])' },
  // string
  { label: 'CONCAT', detail: 'string', info: 'CONCAT(str1, str2, …) — concatenate strings' },
  { label: 'CONCAT_WS', detail: 'string', info: 'CONCAT_WS(sep, str1, str2, …)' },
  { label: 'SUBSTRING', detail: 'string', info: 'SUBSTRING(str, pos[, len])' },
  { label: 'LENGTH', detail: 'string', info: 'LENGTH(str) — byte length' },
  { label: 'CHAR_LENGTH', detail: 'string', info: 'CHAR_LENGTH(str) — character length' },
  { label: 'TRIM', detail: 'string' },
  { label: 'LTRIM', detail: 'string' },
  { label: 'RTRIM', detail: 'string' },
  { label: 'LOWER', detail: 'string' },
  { label: 'UPPER', detail: 'string' },
  { label: 'REPLACE', detail: 'string', info: 'REPLACE(str, from, to)' },
  { label: 'LEFT', detail: 'string', info: 'LEFT(str, len)' },
  { label: 'RIGHT', detail: 'string', info: 'RIGHT(str, len)' },
  { label: 'LOCATE', detail: 'string', info: 'LOCATE(substr, str[, pos])' },
  { label: 'INSTR', detail: 'string', info: 'INSTR(str, substr)' },
  { label: 'LPAD', detail: 'string' },
  { label: 'RPAD', detail: 'string' },
  { label: 'FORMAT', detail: 'string', info: 'FORMAT(num, decimals)' },
  // numeric
  { label: 'ROUND', detail: 'numeric', info: 'ROUND(x[, d])' },
  { label: 'FLOOR', detail: 'numeric' },
  { label: 'CEIL', detail: 'numeric' },
  { label: 'CEILING', detail: 'numeric' },
  { label: 'ABS', detail: 'numeric' },
  { label: 'MOD', detail: 'numeric' },
  { label: 'POWER', detail: 'numeric' },
  { label: 'RAND', detail: 'numeric' },
  { label: 'GREATEST', detail: 'numeric' },
  { label: 'LEAST', detail: 'numeric' },
  // date/time
  { label: 'NOW', detail: 'datetime', info: 'NOW() — current DATETIME' },
  { label: 'CURDATE', detail: 'datetime' },
  { label: 'CURTIME', detail: 'datetime' },
  { label: 'CURRENT_TIMESTAMP', detail: 'datetime' },
  { label: 'UNIX_TIMESTAMP', detail: 'datetime', info: 'UNIX_TIMESTAMP([date])' },
  { label: 'FROM_UNIXTIME', detail: 'datetime', info: 'FROM_UNIXTIME(ts[, format])' },
  { label: 'DATE', detail: 'datetime' },
  { label: 'DATE_FORMAT', detail: 'datetime', info: 'DATE_FORMAT(date, format)' },
  { label: 'STR_TO_DATE', detail: 'datetime', info: 'STR_TO_DATE(str, format)' },
  { label: 'DATE_ADD', detail: 'datetime', info: 'DATE_ADD(date, INTERVAL n unit)' },
  { label: 'DATE_SUB', detail: 'datetime', info: 'DATE_SUB(date, INTERVAL n unit)' },
  { label: 'DATEDIFF', detail: 'datetime', info: 'DATEDIFF(date1, date2) — days between' },
  { label: 'TIMESTAMPDIFF', detail: 'datetime', info: 'TIMESTAMPDIFF(unit, dt1, dt2)' },
  { label: 'TIMESTAMPADD', detail: 'datetime' },
  { label: 'YEAR', detail: 'datetime' },
  { label: 'MONTH', detail: 'datetime' },
  { label: 'DAY', detail: 'datetime' },
  { label: 'HOUR', detail: 'datetime' },
  { label: 'MINUTE', detail: 'datetime' },
  { label: 'SECOND', detail: 'datetime' },
  // null/cond
  { label: 'IFNULL', detail: 'control', info: 'IFNULL(expr, alt)' },
  { label: 'NULLIF', detail: 'control', info: 'NULLIF(a, b) — NULL if a=b' },
  { label: 'COALESCE', detail: 'control', info: 'COALESCE(a, b, …) — first non-NULL' },
  { label: 'IF', detail: 'control', info: 'IF(cond, a, b)' },
  // json
  { label: 'JSON_EXTRACT', detail: 'json' },
  { label: 'JSON_UNQUOTE', detail: 'json' },
  { label: 'JSON_OBJECT', detail: 'json' },
  { label: 'JSON_ARRAY', detail: 'json' },
  // casts
  { label: 'CAST', detail: 'cast', info: 'CAST(expr AS type)' },
  { label: 'CONVERT', detail: 'cast', info: 'CONVERT(expr, type)' },
  // misc
  { label: 'VERSION', detail: 'system' },
  { label: 'DATABASE', detail: 'system' },
  { label: 'USER', detail: 'system' },
  { label: 'LAST_INSERT_ID', detail: 'system' },
  { label: 'UUID', detail: 'system' },
]

// Build static Completion objects once. We tag with `type: 'function'` so
// CodeMirror renders the function icon, and `boost` slightly negative so
// schema-derived columns/tables win on ambiguous matches.
const FUNCTION_COMPLETIONS: readonly Completion[] = MYSQL_FUNCTIONS.map((f) => ({
  label: f.label,
  type: 'function',
  detail: f.detail,
  info: f.info,
  boost: -2,
  apply: f.label + '(',
}))

// Statement skeletons. `#{n:placeholder}` are CodeMirror snippet tab stops;
// `#{}` is the final cursor position.
const SNIPPETS: readonly Completion[] = [
  snippetCompletion('SELECT ${columns} FROM ${table}${}', {
    label: 'select',
    detail: 'SELECT … FROM …',
    type: 'keyword',
    boost: 2,
  }),
  snippetCompletion('SELECT ${columns}\nFROM ${table}\nWHERE ${condition}${}', {
    label: 'selectw',
    detail: 'SELECT … FROM … WHERE',
    type: 'keyword',
    boost: 1,
  }),
  snippetCompletion('SELECT COUNT(*) FROM ${table}${}', {
    label: 'count',
    detail: 'SELECT COUNT(*) FROM …',
    type: 'keyword',
  }),
  snippetCompletion('INSERT INTO ${table} (${columns})\nVALUES (${values})${}', {
    label: 'insert',
    detail: 'INSERT INTO …',
    type: 'keyword',
  }),
  snippetCompletion('UPDATE ${table}\nSET ${col} = ${value}\nWHERE ${condition}${}', {
    label: 'update',
    detail: 'UPDATE … SET … WHERE',
    type: 'keyword',
  }),
  snippetCompletion('DELETE FROM ${table}\nWHERE ${condition}${}', {
    label: 'delete',
    detail: 'DELETE FROM … WHERE',
    type: 'keyword',
  }),
  snippetCompletion('JOIN ${table} ON ${left} = ${right}${}', {
    label: 'join',
    detail: 'JOIN … ON …',
    type: 'keyword',
  }),
  snippetCompletion('LEFT JOIN ${table} ON ${left} = ${right}${}', {
    label: 'leftjoin',
    detail: 'LEFT JOIN … ON …',
    type: 'keyword',
  }),
  snippetCompletion('GROUP BY ${columns}${}', {
    label: 'groupby',
    detail: 'GROUP BY …',
    type: 'keyword',
  }),
  snippetCompletion('ORDER BY ${columns} ${direction}${}', {
    label: 'orderby',
    detail: 'ORDER BY … ASC|DESC',
    type: 'keyword',
  }),
  snippetCompletion('CREATE TABLE ${name} (\n  id BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,\n  ${cols}\n) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4${}', {
    label: 'createtable',
    detail: 'CREATE TABLE …',
    type: 'keyword',
  }),
  snippetCompletion('CASE WHEN ${cond} THEN ${value} ELSE ${other} END${}', {
    label: 'case',
    detail: 'CASE WHEN … THEN … ELSE … END',
    type: 'keyword',
  }),
]

// Regex of the identifier/word prefix we use for matching. Matches the
// dot-aware boundary the schema source uses so we don't fire inside a
// dotted path like `tbl.col` — schema completion handles that better.
const WORD_RE = /[\w$]+$/

/**
 * Completion source contributing MySQL functions and SQL snippets. Returns
 * null when the cursor isn't on an identifier-shaped token, so it doesn't
 * spam suggestions in the middle of strings, comments, or operators.
 */
export const mysqlExtraCompletions: CompletionSource = (ctx: CompletionContext): CompletionResult | null => {
  const word = ctx.matchBefore(WORD_RE)
  if (!word) return null
  // Don't fire when there's no prefix unless the user explicitly asked
  // (Ctrl+Space). Lots of noise otherwise on every keystroke at line start.
  if (word.from === word.to && !ctx.explicit) return null
  // Skip if the previous char is a dot — let schema source handle column
  // completion after `table.`.
  if (word.from > 0 && ctx.state.doc.sliceString(word.from - 1, word.from) === '.') {
    return null
  }
  return {
    from: word.from,
    options: [...FUNCTION_COMPLETIONS, ...SNIPPETS],
    validFor: WORD_RE,
  }
}
