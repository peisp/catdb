// Shared @table mention completion (§10.3) — used by the composer and the
// edit-resend box. Tracks the identifier token after '@' at the caret,
// filters the table list (debounced 150ms), and completes the token in place
// so "@订单表" stays part of the sentence.
import { computed, ref, watch, type Ref } from 'vue'

/** Scan text for @tokens naming known tables (case-insensitive, canonical
 *  casing returned, deduped) — the send-time `mentions` argument. */
export function extractMentions(v: string, tables: string[]): string[] {
  if (tables.length === 0) return []
  const byLower = new Map(tables.map((t) => [t.toLowerCase(), t]))
  const out: string[] = []
  for (const m of v.matchAll(/@([\p{L}\p{N}_$]+)/gu)) {
    const hit = byLower.get(m[1].toLowerCase())
    if (hit && !out.includes(hit)) out.push(hit)
  }
  return out
}

export function useMentionCompletion(
  text: Ref<string>,
  taRef: Ref<HTMLTextAreaElement | null>,
  tables: () => string[],
  needTables?: () => void,
) {
  const menuOpen = ref(false)
  const query = ref('') // live token after '@' (drives caret tracking)
  const debouncedQuery = ref('') // filter input, updated 150ms after query
  const activeIndex = ref(0)
  let atStart = -1 // index of the '@' in text that opened the menu
  let debounceTimer: ReturnType<typeof setTimeout> | null = null

  watch(query, (q) => {
    if (debounceTimer) clearTimeout(debounceTimer)
    debounceTimer = setTimeout(() => { debouncedQuery.value = q }, 150)
  })

  const filtered = computed(() => {
    const q = debouncedQuery.value.toLowerCase()
    return tables()
      .filter((t) => !q || t.toLowerCase().includes(q))
      .slice(0, 50)
  })

  function refreshMention() {
    const el = taRef.value
    if (!el) { menuOpen.value = false; return }
    const pos = el.selectionStart ?? text.value.length
    const before = text.value.slice(0, pos)
    // '@' anywhere, followed by identifier chars up to the caret — mid-word
    // triggers too ("查@订"), matching extractMentions' send-time scan.
    const m = /@([\p{L}\p{N}_$]*)$/u.exec(before)
    if (m) {
      atStart = pos - m[1].length - 1
      query.value = m[1]
      if (!menuOpen.value) {
        activeIndex.value = 0
        debouncedQuery.value = m[1]
        if (tables().length === 0) needTables?.()
      }
      menuOpen.value = true
    } else {
      menuOpen.value = false
    }
  }

  function chooseTable(name: string) {
    // Complete the token in place: "@订" → "@订单表 ", staying in the sentence.
    const el = taRef.value
    const pos = el?.selectionStart ?? text.value.length
    const start = atStart >= 0 ? atStart : pos
    const before = text.value.slice(0, start)
    const after = text.value.slice(pos)
    const inserted = '@' + name + ' '
    text.value = before + inserted + after
    menuOpen.value = false
    query.value = ''
    requestAnimationFrame(() => {
      const e = taRef.value
      if (e) { const c = (before + inserted).length; e.focus(); e.setSelectionRange(c, c) }
    })
  }

  function closeMenu() {
    menuOpen.value = false
  }

  /** Menu-owned keys (arrows / Enter / Tab / Esc). True = consumed. */
  function onMenuKeydown(ev: KeyboardEvent): boolean {
    if (!menuOpen.value) return false
    if (filtered.value.length > 0) {
      if (ev.key === 'ArrowDown') { ev.preventDefault(); activeIndex.value = (activeIndex.value + 1) % filtered.value.length; return true }
      if (ev.key === 'ArrowUp') { ev.preventDefault(); activeIndex.value = (activeIndex.value - 1 + filtered.value.length) % filtered.value.length; return true }
      if (ev.key === 'Enter' || ev.key === 'Tab') { ev.preventDefault(); const t = filtered.value[activeIndex.value]; if (t) chooseTable(t); return true }
    }
    if (ev.key === 'Escape') { ev.preventDefault(); menuOpen.value = false; return true }
    return false
  }

  // Clamp the active row when the filter list shrinks.
  watch(filtered, (f) => { if (activeIndex.value >= f.length) activeIndex.value = 0 })

  return { menuOpen, filtered, activeIndex, refreshMention, chooseTable, closeMenu, onMenuKeydown }
}
