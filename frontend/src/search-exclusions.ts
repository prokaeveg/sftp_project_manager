import type { Project } from './bridge'

export function directorySearchExcluded(project: Project, relative: string): boolean {
 const value = relative.toLowerCase()
 if (value === '.') return false
 if (project.searchExcludedDirectories?.some(p => value === p.toLowerCase() || value.startsWith(p.toLowerCase() + '/'))) return true
 if (!project.excludeDotDirectories) return false
 let current = ''
 for (const segment of value.split('/')) {
  current = current ? `${current}/${segment}` : segment
  if (segment.startsWith('.') && !project.searchIncludedDirectories?.some(p => p.toLowerCase() === current)) return true
 }
 return false
}
