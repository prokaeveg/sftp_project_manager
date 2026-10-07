import type { FileEntry } from './bridge'

export interface TreeNode {
 entry: FileEntry;
 children: TreeNode[];
 loaded: boolean;
 expanded: boolean;
 loading: boolean;
 error: string
}
export function treeNode(entry: FileEntry): TreeNode {
 return { entry, children: [], loaded: false, expanded: false, loading: false, error: '' }
}
export function treeRoot(): TreeNode {
 return treeNode({ name: 'Корень маппинга', relativePath: '.', size: 0, isDir: true, isLink: false, excluded: false })
}
export type DirectoryLoader = (relative: string) => Promise<FileEntry[]>

export function selectEntry(selection: Set<string>, entry: FileEntry, checked: boolean): void {
 if (entry.isLink || entry.excluded) return
 if (checked) selection.add(entry.relativePath)
 else selection.delete(entry.relativePath)
}

export type TreeSelectionEvent = Pick<MouseEvent | KeyboardEvent, 'ctrlKey' | 'metaKey' | 'shiftKey'>

/**
 * Select a visible tree entry using the usual plain, Ctrl/Cmd, and Shift rules.
 * The returned path is the anchor to pass to the next call.
 */
export function selectTreeEntry(
 selection: Set<string>,
 root: TreeNode,
 entry: FileEntry,
 event: TreeSelectionEvent,
 anchor: string | null = null
): string | null {
 if (entry.isLink || entry.excluded) return anchor

 const visible: FileEntry[] = []
 const visit = (node: TreeNode) => {
  visible.push(node.entry)
  if (node.expanded) node.children.forEach(visit)
 }
 visit(root)

 const targetIndex = visible.findIndex(item => item.relativePath === entry.relativePath)
 if (targetIndex < 0) return anchor

 const additive = event.ctrlKey || event.metaKey
 if (event.shiftKey) {
  const anchorIndex = anchor == null ? -1 : visible.findIndex(item => item.relativePath === anchor)
  const start = anchorIndex < 0 ? targetIndex : Math.min(anchorIndex, targetIndex)
  const end = anchorIndex < 0 ? targetIndex : Math.max(anchorIndex, targetIndex)
  if (!additive) selection.clear()
  for (let index = start; index <= end; index++) {
   const item = visible[index]
   if (!item.isLink && !item.excluded) selection.add(item.relativePath)
  }
  return anchorIndex < 0 ? entry.relativePath : anchor
 }

 if (additive) {
  if (selection.has(entry.relativePath)) selection.delete(entry.relativePath)
  else selection.add(entry.relativePath)
  return anchor ?? entry.relativePath
 }

 selection.clear()
 selection.add(entry.relativePath)
 return entry.relativePath
}

// Convert a project-root-relative local path into the active mapping's path space.
// Reject parent/sibling selections so a transfer can never silently target the
// wrong remote location when the mapping is narrower than the project root.
export function toMappingPath(projectRelative: string, mappingLocalPath: string): string {
 const clean = (value: string) => {
  const normalized = value.replace(/\\/g, '/')
  if (normalized.startsWith('/') || /^[a-zA-Z]:/.test(normalized)) throw new Error('Путь не относится к папке проекта. Выберите файл или папку внутри выбранного маппинга.')
  const parts = normalized.split('/').filter(part => part && part !== '.')
  if (parts.some(part => part === '..')) throw new Error('Путь выходит за пределы проекта. Выберите файл или папку внутри выбранного маппинга.')
  return parts.join('/')
 }
 const path = clean(projectRelative), mapping = clean(mappingLocalPath)
 if (!mapping) return path || '.'
 if (path === mapping) return '.'
 if (path.startsWith(`${mapping}/`)) return path.slice(mapping.length + 1)
 throw new Error(`Путь «${projectRelative}» находится вне маппинга «${mappingLocalPath}». Выберите файл или папку внутри выбранной локальной папки.`)
}

// Convert a server-root-relative path through a mapping into a project path.
// Paths outside the mapping stay browsable but cannot be used for transfers.
export function fromDeploymentPath(serverRelative: string, mappingDeploymentPath: string, mappingLocalPath: string): string {
 const clean = (value: string) => {
  const normalized = value.replace(/\\/g, '/')
  if (normalized.startsWith('/') || /^[a-zA-Z]:/.test(normalized)) throw new Error('Путь маппинга должен быть относительным.')
  const parts = normalized.split('/').filter(part => part && part !== '.')
  if (parts.some(part => part === '..')) throw new Error('Путь выходит за пределы корня.')
  return parts.join('/')
 }
 const remote = clean(serverRelative), deployment = clean(mappingDeploymentPath), local = clean(mappingLocalPath)
 let suffix = ''
 if (!deployment) suffix = remote
 else if (remote === deployment) suffix = ''
 else if (remote.startsWith(`${deployment}/`)) suffix = remote.slice(deployment.length + 1)
 else throw new Error(`Путь «${serverRelative}» вне маппинга «${mappingDeploymentPath}».`)
 return [local, suffix].filter(Boolean).join('/') || '.'
}

// A successful refresh discards descendant caches, including paths removed on disk.
// On failure keep the previous listing, with an explicit error and a retry action.
export async function loadDirectory(node: TreeNode, loader: DirectoryLoader): Promise<void> {
 if (node.loading || !node.entry.isDir || node.entry.isLink || node.entry.excluded) return
 node.loading = true; node.error = ''; node.expanded = true
 try {
  const entries = await loader(node.entry.relativePath)
  node.children = (entries ?? []).map(treeNode)
  node.loaded = true
 } catch (error) {
  node.error = String(error)
  throw error
 } finally { node.loading = false }
}

export async function toggleDirectory(node: TreeNode, loader: DirectoryLoader): Promise<void> {
 if (node.loading || !node.entry.isDir || node.entry.isLink || node.entry.excluded) return
 if (node.expanded) { node.expanded = false; return }
 node.expanded = true
 if (!node.loaded || node.error) await loadDirectory(node, loader)
}
