export function submenuPosition(anchor: { left: number; right: number; top: number }, width: number, height: number, viewportWidth: number, viewportHeight: number) {
 const left = anchor.right + width <= viewportWidth - 8 ? anchor.right : anchor.left - width
 return { left: Math.max(8, Math.min(left, viewportWidth - width - 8)), top: Math.max(8, Math.min(anchor.top, viewportHeight - height - 8)) }
}
