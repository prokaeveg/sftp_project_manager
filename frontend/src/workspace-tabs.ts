import type { FileComparison, FileContent } from './bridge'

export type WorkspaceTab =
 | { kind: 'file'; key: string; file: FileContent }
 | { kind: 'comparison'; key: string; comparison: FileComparison }

export function comparisonKey(projectID: string, relative: string, source: 'git' | 'ssh', target: string): string {
 return JSON.stringify(['comparison', projectID, relative, source, target])
}

export function putComparisonTab(tabs: WorkspaceTab[], key: string, comparison: FileComparison) {
 const tab = tabs.find(tab => tab.key === key)
 if (tab?.kind === 'comparison') tab.comparison = comparison
 else tabs.push({ kind: 'comparison', key, comparison })
}
