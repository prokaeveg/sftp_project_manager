export interface PanelSizes { left: number; right: number }
export function readPanelSizes(raw: string | null): PanelSizes {
 try {
  const value = JSON.parse(raw ?? 'null')
  if (value && Number.isFinite(value.left) && value.left > 0 && Number.isFinite(value.right) && value.right > 0) return { left: value.left, right: value.right }
 } catch { /* Use defaults for damaged preferences. */ }
 return { left: 260, right: 300 }
}

// Width excludes outer padding and both splitter tracks.
export function fitPanels(width: number, preferred: PanelSizes): PanelSizes {
 const left = Math.max(175, preferred.left), right = Math.max(195, preferred.right)
 const spare = Math.max(0, width - 230 - 175 - 195)
 const extra = left - 175 + right - 195
 const scale = extra > spare ? spare / extra : 1
 return { left: 175 + (left - 175) * scale, right: 195 + (right - 195) * scale }
}
