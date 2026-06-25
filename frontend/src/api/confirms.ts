// confirms — singleton in-app confirm modal for Windows, where Wails v3's
// native MessageBoxW ignores custom button labels and collapses every
// multi-button dialog to a single "OK". macOS keeps the native NSAlert path
// (see api/dialogs.ts). Architecture mirrors api/prompts.ts: a module-level
// reactive ref holds the pending request and ConfirmOverlay (mounted once in
// App.vue, so it covers every window) watches that ref and renders the modal.
import { ref } from 'vue'
import type { ConfirmOptions } from './dialogs'

interface PendingConfirm {
  opts: ConfirmOptions<string>
  resolve: (value: string | null) => void
}

export const currentConfirm = ref<PendingConfirm | null>(null)

export function openConfirm<V extends string>(opts: ConfirmOptions<V>): Promise<V | null> {
  // If another confirm is open, cancel it first so the resolver stays attached
  // to exactly one in-flight overlay.
  if (currentConfirm.value) {
    const prev = currentConfirm.value
    currentConfirm.value = null
    prev.resolve(null)
  }
  return new Promise<V | null>((resolve) => {
    currentConfirm.value = {
      opts: opts as ConfirmOptions<string>,
      resolve: resolve as (value: string | null) => void,
    }
  })
}

export function resolveCurrentConfirm(value: string | null) {
  const c = currentConfirm.value
  if (!c) return
  currentConfirm.value = null
  c.resolve(value)
}
