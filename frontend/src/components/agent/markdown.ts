// Markdown + SQL-block rendering for the agent chat panel.
//
// Finalized assistant turns render as markdown, but ```sql fenced blocks are
// pulled out and rendered by AgentSqlBlock.vue so they carry the copy / insert
// / open-in-new-tab actions (AGENT_DESIGN.md §10.4, Ask-mode core exit). This
// module owns (a) the shared MarkdownIt instance and (b) the segmenter that
// splits a message into ordered prose / sql segments.
import MarkdownIt from 'markdown-it'

const md = new MarkdownIt({
  html: false, // never trust model output as raw HTML
  linkify: true,
  breaks: true,
})

export type Segment =
  | { kind: 'md'; html: string }
  | { kind: 'sql'; content: string; open?: boolean }

/**
 * Segment a message into ordered prose / sql parts by walking MarkdownIt's
 * TOKEN stream — never by regex. Streaming and finalized text go through the
 * same real CommonMark parse, so partial input can't segment differently from
 * the finished message (unmatched backticks render literally, an unclosed
 * fence is auto-closed at EOF — both per spec, both self-correct as more text
 * arrives).
 *
 * Top-level ```sql fences become { kind: 'sql' } segments (rendered by
 * AgentSqlBlock with the copy / insert / open actions); everything else is
 * rendered to HTML in place. While streaming, a trailing unclosed sql fence is
 * marked open so the actions appear only once the fence closes.
 *
 * Any parser surprise degrades to escaped plain text — a render must never
 * break the component.
 */
export function segmentMarkdown(src: string, streaming: boolean): Segment[] {
  const text = src ?? ''
  try {
    const tokens = md.parse(text, {})
    const out: Segment[] = []
    let buf: (typeof tokens)[number][] = []
    const flush = () => {
      if (buf.length) {
        out.push({ kind: 'md', html: md.renderer.render(buf, md.options, {}) })
        buf = []
      }
    }
    for (let i = 0; i < tokens.length; i++) {
      const tok = tokens[i]
      if (tok.type === 'fence' && tok.info.trim().toLowerCase() === 'sql') {
        flush()
        const isTail = i === tokens.length - 1
        const closed = /```\s*$/.test(text) || !isTail
        out.push({
          kind: 'sql',
          content: tok.content.replace(/\s+$/, ''),
          open: streaming && !closed,
        })
      } else {
        buf.push(tok)
      }
    }
    flush()
    if (out.length === 0) out.push({ kind: 'md', html: '' })
    return out
  } catch {
    return [{ kind: 'md', html: '<p>' + escapeHtml(text) + '</p>' }]
  }
}

// Lightweight SQL keyword highlighter. Escapes first (safe), then wraps a
// fixed keyword set — no full parser, no external highlighter dependency.
const KEYWORDS = new Set([
  'select', 'from', 'where', 'insert', 'into', 'values', 'update', 'set',
  'delete', 'create', 'alter', 'drop', 'table', 'view', 'index', 'join',
  'inner', 'left', 'right', 'outer', 'on', 'group', 'by', 'order', 'having',
  'limit', 'offset', 'as', 'and', 'or', 'not', 'null', 'is', 'in', 'like',
  'between', 'distinct', 'count', 'sum', 'avg', 'min', 'max', 'union', 'all',
  'asc', 'desc', 'primary', 'key', 'foreign', 'references', 'default',
  'unique', 'constraint', 'with', 'case', 'when', 'then', 'else', 'end',
  'explain', 'show', 'describe', 'desc', 'use', 'truncate', 'replace',
])

function escapeHtml(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
}

/** Return SQL as HTML with keywords wrapped in <span class="kw">. */
export function highlightSql(sql: string): string {
  return escapeHtml(sql).replace(/\b([A-Za-z_]+)\b/g, (word) =>
    KEYWORDS.has(word.toLowerCase()) ? `<span class="kw">${word}</span>` : word,
  )
}
