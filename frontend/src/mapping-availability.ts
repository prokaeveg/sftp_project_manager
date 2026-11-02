import type { Mapping, ProjectConnection } from './bridge'
import { toMappingPath } from './file-tree.ts'

export function matchingMapping(connection: Pick<ProjectConnection, 'mappings'>, paths: string[]): Mapping | undefined {
 if (!paths.length) return undefined
 return (connection.mappings ?? []).filter(mapping => paths.every(path => {
  try { toMappingPath(path, mapping.localPath); return true } catch { return false }
 })).sort((a, b) => depth(b.localPath) - depth(a.localPath))[0]
}

function depth(path: string) { return path.split(/[\\/]/).filter(part => part && part !== '.').length }

export function eligibleConnections<T extends Pick<ProjectConnection, 'mappings'>>(connections: T[], paths: string[]): T[] {
 return connections.filter(connection => !!matchingMapping(connection, paths))
}

export function eligibleCopyConnections<T extends Pick<ProjectConnection, 'mappings'>>(connections: T[], paths: string[]): T[] {
 return paths.length ? connections.filter(connection => paths.every(path => !!matchingMapping(connection, [path]))) : []
}
