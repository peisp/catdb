// api/dialogs — thin wrappers over Wails native dialogs so stores/components
// never import @wailsio/runtime directly (CLAUDE.md #1).
import { Dialogs } from '@wailsio/runtime'

export type CloseChoice = 'save' | 'discard' | 'cancel'

/**
 * Ask the user what to do with an unsaved query tab on close. Returns which
 * button they picked. Uses a native 3-button warning dialog.
 */
export async function confirmCloseUnsaved(title: string): Promise<CloseChoice> {
  const btn = await Dialogs.Warning({
    Title: '未保存的更改',
    Message: `查询「${title}」有未保存的更改，是否保存？`,
    Buttons: [
      { Label: '取消', IsCancel: true },
      { Label: '不保存' },
      { Label: '保存', IsDefault: true },
    ],
  })
  if (btn === '保存') return 'save'
  if (btn === '不保存') return 'discard'
  return 'cancel'
}
