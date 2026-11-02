import type { GitFile, GitState } from './bridge'
import { treeNode, treeRoot, type TreeNode } from './file-tree.ts'

export const gitLabels: Record<GitState, string> = { ignored: 'Игнорируется Git', clean: 'Без изменений', untracked: 'Не отслеживается', added: 'Добавлен в Git', modified: 'Изменён', deleted: 'Удалён', conflict: 'Конфликт' }
const priority: Record<GitState, number> = { ignored: -1, clean: 0, untracked: 1, added: 2, modified: 3, deleted: 4, conflict: 5 }
export function gitStatusMap(files: GitFile[]): Record<string, GitState> {
 const result: Record<string, GitState> = Object.create(null)
 const explicit = new Set(files.map(file => file.path))
 for (const file of files) result[file.path] = file.status
 for (const file of files) {
  if (file.status === 'ignored') continue
  const parts = file.path.split('/'); parts.pop()
  while (parts.length) {
   const directory = parts.join('/')
   if (!explicit.has(directory) && (!result[directory] || priority[file.status] > priority[result[directory]])) result[directory] = file.status
   parts.pop()
  }
 }
 return result
}
// Git reports ignored directories once; loaded descendants inherit that status.
export function gitStatusForPath(statuses: Record<string, GitState> | undefined, path: string): GitState | undefined {
 if (!statuses) return undefined
 if (statuses[path]) return statuses[path]
 const parts = path.split('/'); parts.pop()
 while (parts.length) {
  if (statuses[parts.join('/')] === 'ignored') return 'ignored'
  parts.pop()
 }
 return undefined
}
export function gitChangesTree(files: GitFile[], mode: 'list' | 'tree', group: 'changes' | 'unversioned' = 'changes'): TreeNode {
 const root = treeRoot(); root.loaded = true; root.expanded = true
 const directories = new Map<string, TreeNode>([['', root]])
 const changes = files.filter(file => group === 'unversioned' ? file.status === 'untracked' : file.status !== 'clean' && file.status !== 'untracked' && file.status !== 'ignored').sort((a,b) => a.path.localeCompare(b.path))
 for (const file of changes) {
  const parts = file.path.split('/'); const name = parts.pop()!
  let parent = root, prefix = ''
  if (mode === 'tree') for (const part of parts) {
   prefix = prefix ? `${prefix}/${part}` : part
   let node = directories.get(prefix)
   if (!node) {
    node = treeNode({ name: part, relativePath: prefix, size: 0, isDir: true, isLink: false, excluded: false })
    node.loaded = true; node.expanded = true; parent.children.push(node); directories.set(prefix,node)
   }
   parent = node
  }
  parent.children.push(treeNode({ name: mode === 'list' ? file.path : name, relativePath: file.path, size: 0, isDir: !!file.isDir, isLink: false, excluded: false, missing: file.status === 'deleted' }))
 }
 function sort(node: TreeNode) { node.children.sort((a,b) => Number(b.entry.isDir)-Number(a.entry.isDir) || a.entry.name.localeCompare(b.entry.name)); node.children.forEach(sort) }
 sort(root)
 return root
}
