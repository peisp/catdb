// prompts — singleton text-input prompt for flows where Wails has no native
// equivalent (rename, etc.). Wails v3 only ships file / info / warn / error
// dialogs, so a simple "ask for a string" flow needs an in-app overlay.
//
// Architecture mirrors the context-menu singletons in this folder: a module-
// level reactive ref holds the pending request, and PromptOverlay (mounted
// once in AppShell) watches that ref and renders the modal. Callers await
// `openTextPrompt(...)` which resolves with the trimmed string or `null`.
import { ref } from 'vue'

export interface TextPromptOptions {
  title: string
  /** Optional label shown above the input. */
  label?: string
  /** Initial input value (will be selected on open). */
  initial: string
  okText?: string
  cancelText?: string
  /** Return an error message string to block confirmation, or null to allow. */
  validate?: (value: string) => string | null
}

interface PendingPrompt extends TextPromptOptions {
  resolve: (result: string | null) => void
}

export const currentPrompt = ref<PendingPrompt | null>(null)

export function openTextPrompt(opts: TextPromptOptions): Promise<string | null> {
  // If another prompt is open, cancel it first so the resolver stays attached
  // to exactly one in-flight overlay.
  if (currentPrompt.value) {
    const prev = currentPrompt.value
    currentPrompt.value = null
    prev.resolve(null)
  }
  return new Promise((resolve) => {
    currentPrompt.value = { ...opts, resolve }
  })
}

export function resolveCurrentPrompt(result: string | null) {
  const p = currentPrompt.value
  if (!p) return
  currentPrompt.value = null
  p.resolve(result)
}
