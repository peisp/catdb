// api/dialogs — thin wrappers over Wails native dialogs so stores/components
// never import @wailsio/runtime directly (CLAUDE.md #1).
import { Dialogs } from '@wailsio/runtime'
import { t } from '../i18n'

export type CloseChoice = 'save' | 'discard' | 'cancel'

/**
 * Ask the user what to do with an unsaved query tab on close. Returns which
 * button they picked. Uses a native 3-button warning dialog.
 */
export async function confirmCloseUnsaved(title: string): Promise<CloseChoice> {
  const saveLabel = t('common.save')
  const dontSaveLabel = t('dialogs.dontSave')
  const btn = await Dialogs.Warning({
    Title: t('dialogs.unsavedTitle'),
    Message: t('dialogs.unsavedMessage', { title }),
    Buttons: [
      { Label: t('common.cancel'), IsCancel: true },
      { Label: dontSaveLabel },
      { Label: saveLabel, IsDefault: true },
    ],
  })
  if (btn === saveLabel) return 'save'
  if (btn === dontSaveLabel) return 'discard'
  return 'cancel'
}
