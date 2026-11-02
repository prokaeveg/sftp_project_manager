export function recentBranches(root: string): string[] {
 try { const data: unknown = JSON.parse(localStorage.getItem(`git-compare:${root}`) ?? '[]'); return Array.isArray(data) ? data.filter((v): v is string => typeof v === 'string').slice(0, 5) : [] } catch { return [] }
}
export function rememberBranch(root: string, ref: string) {
 try { localStorage.setItem(`git-compare:${root}`, JSON.stringify([ref, ...recentBranches(root).filter(v => v !== ref)].slice(0, 5))) } catch { /* Comparison remains usable without storage. */ }
}
