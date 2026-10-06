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
