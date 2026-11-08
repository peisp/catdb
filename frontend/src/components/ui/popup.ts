// 浮层定位:锚点矩形 + 浮层尺寸 → fixed 坐标。竖向溢出翻转,四边 8px 收拢。
export type Placement =
  | 'top'
  | 'top-start'
  | 'top-end'
  | 'bottom'
  | 'bottom-start'
  | 'bottom-end'

export function positionPopup(
  anchor: DOMRect,
  popup: { width: number; height: number },
  placement: Placement = 'bottom',
  gap = 6,
): { left: number; top: number } {
  const vw = window.innerWidth
  const vh = window.innerHeight

  let top = placement.startsWith('top') ? anchor.top - gap - popup.height : anchor.bottom + gap
  // 溢出翻转
  if (placement.startsWith('bottom') && top + popup.height > vh - 8 && anchor.top - gap - popup.height >= 8) {
    top = anchor.top - gap - popup.height
  } else if (placement.startsWith('top') && top < 8 && anchor.bottom + gap + popup.height <= vh - 8) {
    top = anchor.bottom + gap
  }

  let left: number
  if (placement.endsWith('-start')) left = anchor.left
  else if (placement.endsWith('-end')) left = anchor.right - popup.width
  else left = anchor.left + anchor.width / 2 - popup.width / 2

  left = Math.min(Math.max(left, 8), Math.max(8, vw - popup.width - 8))
  top = Math.min(Math.max(top, 8), Math.max(8, vh - popup.height - 8))
  return { left, top }
}
