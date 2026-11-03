import type * as Go from '../bindings/awesomeProject/app'
import type * as Models from '../bindings/awesomeProject/internal/application/models'
import type * as Config from '../bindings/awesomeProject/internal/config/models'
import type { Connection as GoConnection } from '../bindings/awesomeProject/internal/transport/models'
import type { Snapshot } from '../bindings/awesomeProject/internal/gitstatus/models'
import type { API, Connection, Project, Profile, Status, BatchItem, BatchPreview, BatchResult, QueueState, GitSnapshot, NavigationStatus, Preview } from './bridge'

type RawAPI = typeof Go

function enumValue<const T extends readonly string[]>(value: string, values: T): T[number] {
 if (!values.includes(value)) throw new Error(`Неизвестное значение backend: ${value}`)
 return value as T[number]
}
const direction = (value: string) => enumValue(value, ['upload', 'download'])
const kind = (value: string) => enumValue(value, ['file', 'directory'])
const transferStatus = (value: string) => enumValue(value, ['waiting', 'running', 'completed', 'failed', 'cancelled'])

function connection(value: GoConnection): Connection {
 return { ...value }
}
function profile(value: Config.Profile): Profile {
 return { ...value, connection: connection(value.connection), excludes: value.excludes ?? [] }
}
function project(value: Config.Project): Project {
 return { ...value, searchExcludedDirectories: value.searchExcludedDirectories ?? [], searchIncludedDirectories: value.searchIncludedDirectories ?? [],
  connections: (value.connections ?? []).map(server => ({ ...server, connection: connection(server.connection), mappings: server.mappings ?? [], excludes: server.excludes ?? [] })) }
}
function status(value: Models.Status): Status {
 return { ...value, settings: { ...value.settings, projects: (value.settings.projects ?? []).map(project),
  profile: value.settings.profile ? profile(value.settings.profile) : undefined,
  activeProjectID: value.settings.activeProjectID ?? '', activeConnectionID: value.settings.activeConnectionID ?? '', activeMappingID: value.settings.activeMappingID ?? '' } }
}
function batchItem(value: Models.BatchItem): BatchItem {
 return { ...value, kind: kind(value.kind) }
}
function batchPreview(value: Models.BatchPreview): BatchPreview {
 return { ...value, direction: direction(value.direction), items: (value.items ?? []).map(batchItem) }
}
function batchResult(value: Models.BatchResult): BatchResult {
 return { items: (value.items ?? []).map(item => ({ ...item, kind: kind(item.kind), status: enumValue(item.status, ['completed', 'skipped', 'failed', 'cancelled']) })) }
}
function queue(value: Models.QueueState): QueueState {
 return { ...value, jobs: (value.jobs ?? []).map(job => ({ ...job, direction: direction(job.direction), status: transferStatus(job.status),
  items: (job.items ?? []).map(item => ({ ...item, kind: kind(item.kind), status: enumValue(item.status, ['waiting', 'running', 'completed', 'failed', 'cancelled', 'skipped']) })) })) }
}
function git(value: Snapshot): GitSnapshot {
 return { ...value, files: (value.files ?? []).map(file => ({ ...file, status: enumValue(file.status, ['clean', 'ignored', 'untracked', 'added', 'modified', 'deleted', 'conflict']) })) }
}
function navigation(value: Models.NavigationStatus): NavigationStatus {
 return { ...value, state: enumValue(value.state, ['disconnected', 'connecting', 'connected']) }
}
function preview(value: Models.Preview): Preview { return { ...value, direction: direction(value.direction) } }

// Keep generated wire types checked against the UI contract. Go slices can be
// null and Go string fields do not carry the UI's literal unions.
export function createBackend(raw: RawAPI): API {
 return {
  ...raw,
  GetStatus: async () => status(await raw.GetStatus()),
  SaveProjectName: async name => status(await raw.SaveProjectName(name)),
  SaveProfile: async value => status(await raw.SaveProfile(value)),
  SaveProject: async value => status(await raw.SaveProject(value)),
  SelectProject: async (...args) => status(await raw.SelectProject(...args)),
  CreateImportedProject: async value => status(await raw.CreateImportedProject(value)),
  SetDirectorySearchExcluded: async (...args) => status(await raw.SetDirectorySearchExcluded(...args)),
  PreviewPhpStormProject: async root => {
   const value = await raw.PreviewPhpStormProject(root)
   return { ...value, project: project(value.project), warnings: value.warnings ?? [], aliasConnectionIDs: value.aliasConnectionIDs ?? [] }
  },
  DiscoverSSH: async path => { const value = await raw.DiscoverSSH(path); return { ...value, aliases: value.aliases ?? [], warnings: value.warnings ?? [] } },
  EvaluateSSH: async (...args) => { const value = await raw.EvaluateSSH(...args); return { ...value, identityFiles: value.identityFiles ?? [] } },
  ListProjectLocal: async path => await raw.ListProjectLocal(path) ?? [],
  ListLocal: async path => await raw.ListLocal(path) ?? [],
  ListRemote: async path => await raw.ListRemote(path) ?? [],
  SearchProjectFiles: async (...args) => { const value = await raw.SearchProjectFiles(...args); return { ...value, files: value.files ?? [] } },
  GetGitStatus: async id => git(await raw.GetGitStatus(id)),
  GetGitBranches: async id => await raw.GetGitBranches(id) ?? [],
  GetNavigationStatus: async () => navigation(await raw.GetNavigationStatus()),
  PreviewTransfer: async (...args) => preview(await raw.PreviewTransfer(...args)),
  PreviewBatch: async (...args) => batchPreview(await raw.PreviewBatch(...args)),
  PreviewBatchForServer: async (...args) => batchPreview(await raw.PreviewBatchForServer(...args)),
  PreviewDropTransfer: async (...args) => batchPreview(await raw.PreviewDropTransfer(...args)),
  PreviewQueueRetry: async id => batchPreview(await raw.PreviewQueueRetry(id)),
  ExecuteBatch: async (...args) => batchResult(await raw.ExecuteBatch(...args)),
  GetQueue: async () => queue(await raw.GetQueue()),
  EnqueueBatch: async (...args) => queue(await raw.EnqueueBatch(...args)),
 }
}
