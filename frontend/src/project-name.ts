export function projectNameFromRoot(currentName: string, root: string): string {
 if (currentName.trim()) return currentName
 const normalized = root.trim().replace(/\\/g, '/').replace(/\/+$/, '')
 if (!normalized || /^[a-z]:$/i.test(normalized) || /^\/\/[^/]+\/[^/]+$/.test(normalized)) return currentName
 return normalized.split('/').pop() || currentName
}
