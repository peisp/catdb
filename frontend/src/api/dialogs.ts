// api/dialogs — thin wrappers over Wails native dialogs so stores/components
// never import @wailsio/runtime directly (CLAUDE.md #1).
import { Dialogs } from '@wailsio/runtime'
import { t } from '../i18n'
import { openConfirm } from './confirms'

// Windows: Wails v3's native message dialog (Win32 MessageBoxW) ignores custom
// button labels and renders a single "OK", so multi-button confirms are routed
// to an in-app modal (ConfirmOverlay). macOS keeps the native NSAlert path.
const useInAppConfirm = !navigator.platform.includes('Mac')

/** One button of a confirm dialog. `value` is a stable, locale-independent id. */
export interface ConfirmButton<V extends string = string> {
  value: V
  label: string
  isCancel?: boolean
  isDefault?: boolean
}

export interface ConfirmOptions<V extends string> {
  kind?: 'warning' | 'error' | 'info'
  title: string
  message: string
  buttons: ConfirmButton<V>[]
}

/**
 * Native confirm dialog that returns the chosen button's STABLE `value`, never
 * its (localized) label.
 *
 * Wails resolves dialogs with the *label text* of the clicked button — there is
 * no per-button id — so deciding "which button" inevitably means matching that
 * text. This helper does that match ONCE, here, against the same labels it
 * passed in, then hands back the stable value. Call sites compare against
 * `value` and so never break when a label is translated. Returns null if the
 * dialog is dismissed without matching a button.
 */
export async function confirm<V extends string>(opts: ConfirmOptions<V>): Promise<V | null> {
  if (useInAppConfirm) return openConfirm(opts)
  const fn =
    opts.kind === 'error' ? Dialogs.Error : opts.kind === 'info' ? Dialogs.Info : Dialogs.Warning
  const label = await fn({
    Title: opts.title,
    Message: opts.message,
    Buttons: opts.buttons.map((b) => ({ Label: b.label, IsCancel: b.isCancel, IsDefault: b.isDefault })),
  })
  return opts.buttons.find((b) => b.label === label)?.value ?? null
}

export type CloseChoice = 'save' | 'discard' | 'cancel'

/**
 * Ask the user what to do with an unsaved query tab on close. Returns which
 * button they picked. Uses a native 3-button warning dialog.
 */
export async function confirmCloseUnsaved(title: string): Promise<CloseChoice> {
  const choice = await confirm({
    title: t('dialogs.unsavedTitle'),
    message: t('dialogs.unsavedMessage', { title }),
    buttons: [
      { value: 'cancel', label: t('common.cancel'), isCancel: true },
      { value: 'discard', label: t('dialogs.dontSave') },
      { value: 'save', label: t('common.save'), isDefault: true },
    ],
  })
  return choice ?? 'cancel'
}
