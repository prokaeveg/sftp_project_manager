<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { backend, type AuthPrompt, type BatchPreview, type FileEntry, type FileContent, type NavigationStatus, type PhpStormPreview, type GitSnapshot, type Project, type ProjectConnection, type QueueState, type SSHDiscovery, type SSHEffective, type Status } from './bridge'
import FileTree from './components/FileTree.vue'
import ResizableWorkspace from './components/ResizableWorkspace.vue'
import TransferQueue from './components/TransferQueue.vue'
import { eligibleConnections, eligibleCopyConnections, matchingMapping } from './mapping-availability'
import { projectNameFromRoot } from './project-name'
import { recentBranches } from './git-recent'
import TransferMenu from './components/TransferMenu.vue'
import FileWorkspace from './components/FileWorkspace.vue'
import ProjectOpenDialog from './components/ProjectOpenDialog.vue'
import { directorySearchExcluded } from './search-exclusions'
import { formatBytes } from './format-bytes'
import { gitStatusMap, gitChangesTree } from './git-status'
import { fromDeploymentPath, loadDirectory, revealTreeEntry, selectTreeEntry, toggleDirectory, toMappingPath, treeRoot, type TreeNode } from './file-tree'

function emptyProject(): Project { return { id: '', name: '', color: '#517bbd', glowSize: 100, localRoot: '', connections: [] } }
function emptyConnection(): ProjectConnection {
 return { id: crypto.randomUUID(), name: 'Новый сервер', rootPath: '/', connection: { mode: 'ssh_alias', alias: '', configPath: '', host: '', user: '', port: 22, identityFile: '', knownHostsFile: '' }, mappings: [], excludes: ['.git/**', '**/node_modules/**', '**/.env', '**/.env.*'] }
}
const status = ref<Status>(), project = reactive<Project>(emptyProject()), draft = reactive<Project>(emptyProject())
const error = ref(''), notice = ref(''), busy = ref(false), choosingDirectory = ref(false)
const phpStormImport = ref<PhpStormPreview>()
const importServers = ref<string[]>([])
const importSelectionCount = computed(() => phpStormImport.value?.project.connections.filter(c => importServers.value.includes(c.id)).length ?? 0)
const allImportServersSelected = computed(() => !!phpStormImport.value?.project.connections.length && importSelectionCount.value === phpStormImport.value.project.connections.length)
function toggleAllImportServers(event: Event) {
 importServers.value = (event.target as HTMLInputElement).checked ? phpStormImport.value?.project.connections.map(c => c.id) ?? [] : []
}
const importEnabled = ref(false)
const git = ref<GitSnapshot>({ available: false, files: [] })
const gitError = ref(''), gitLoading = ref(false), localTab = ref<'project' | 'changes'>('project')
const gitMode = ref<'list' | 'tree'>('tree')
const gitStatuses = computed(() => gitStatusMap(git.value.files))
const changedFiles = computed(() => git.value.files.filter(file => file.status !== 'clean' && file.status !== 'untracked' && file.status !== 'ignored'))
const unversionedFiles = computed(() => git.value.files.filter(file => file.status === 'untracked'))
const unversionedStatuses = computed(() => gitStatusMap(unversionedFiles.value))
const unversionedOpen = ref(false)
const collapsedGitDirectories = reactive(new Set<string>())
const changesTree = computed(() => {
 const root = gitChangesTree(git.value.files, gitMode.value)
 const apply = (node: TreeNode) => { if (collapsedGitDirectories.has(node.entry.relativePath)) node.expanded = false; node.children.forEach(apply) }
 apply(root); return root
})
const unversionedTree = computed(() => {
 const root = gitChangesTree(git.value.files, gitMode.value, 'unversioned')
 const apply = (node: TreeNode) => { if (collapsedGitDirectories.has(node.entry.relativePath)) node.expanded = false; node.children.forEach(apply) }
 apply(root); return root
})
const changesSelectionTree = computed(() => {
 const root = treeRoot(); root.loaded = true; root.expanded = true
 root.children = [...changesTree.value.children, ...(unversionedOpen.value ? unversionedTree.value.children : [])]
 return root
})
watch(unversionedOpen, open => {
 if (!open) for (const file of unversionedFiles.value) selections.local.delete(file.path)
})
let gitPollingEnabled = false
let gitRequest = 0, gitTimer: ReturnType<typeof setTimeout> | undefined
const preview = ref<BatchPreview>()
const previewSelectionSide = ref<'local' | 'remote'>()
const queue = ref<QueueState>({ paused: false, jobs: [] }), queueError = ref(''), queueBusy = ref(false), queuePanelOpen = ref(false)
const navigation = ref<NavigationStatus>({ state: 'disconnected', projectID: '', connectionID: '' })
let queuePollTimer: ReturnType<typeof setTimeout> | undefined
let queuePollTask: Promise<void> | undefined
let queuePollingEnabled = false
let authPollingEnabled = false
let authPollTimer: ReturnType<typeof setTimeout> | undefined
let authPollTask: Promise<void> | undefined
let authPollRevision = 0
let answeredAuthPromptID = ''
let queueSnapshotRevision = 0
let navigationRevision = 0
let navigationRequestSequence = 0
const overwrite = ref(false), includeEnvironment = ref(false)
const wizard = ref(false), settingsOpen = ref(false), editConnection = ref(0), editMapping = ref(0)
const settingsDialog = ref<HTMLDialogElement>(), previewDialog = ref<HTMLDialogElement>(), settingsTrigger = ref<HTMLElement>()
const projectToOpen = ref<{ id: string; name: string }>(), projectOpening = ref(false), projectOpenError = ref('')
let projectSelectTrigger: HTMLElement | undefined
const authDialog = ref<HTMLDialogElement>(), authAnswerInput = ref<HTMLInputElement>()
const authPrompt = ref<AuthPrompt | null>(null)
const authAnswer = ref(''), authRemember = ref(false), authPromptError = ref(''), authSubmitting = ref(false)
const trees = reactive({ local: treeRoot(), remote: treeRoot() })
const selections = reactive({ local: new Set<string>(), remote: new Set<string>() })
const selectionAnchors: Record<'local' | 'remote', string | null | undefined> = { local: undefined, remote: undefined }
const transferMenu = ref<{ side: 'local' | 'remote'; x: number; y: number; file?: FileEntry }>()
const fileWorkspace = ref<InstanceType<typeof FileWorkspace>>()
const activeFile = ref<FileContent>()
const revealPath = computed(() => {
 const file = activeFile.value
 if (file && file.projectID === project.id) {
  if (!file.connectionID) return file.relativePath
  const source = savedProject.value?.connections.find(connection => connection.id === file.connectionID)
  const mappings = [...(source?.mappings ?? [])].sort((a,b) => b.deploymentPath.length - a.deploymentPath.length)
  for (const mapping of mappings) {
   try { return fromDeploymentPath(file.relativePath, mapping.deploymentPath, mapping.localPath) } catch { /* Try the next source mapping. */ }
  }
  return undefined
 }
 const selected = selections.local.size === 1 ? [...selections.local][0] : undefined
 return selected && selected !== '.' ? selected : undefined
})
async function revealCurrentFile() {
 const relative = revealPath.value
 if (!relative || busy.value) return
 await run(async () => {
  selectLocalTab('project')
  const node = await revealTreeEntry(trees.local, relative, path => backend().ListProjectLocal(path))
  selections.local.clear(); selections.local.add(node.entry.relativePath); selectionAnchors.local = node.entry.relativePath
  await nextTick()
  const row = Array.from(document.querySelectorAll<HTMLElement>('[data-testid="local-pane"] [data-tree-path]')).find(element => element.dataset.treePath === node.entry.relativePath)
  row?.scrollIntoView({ block: 'nearest', inline: 'nearest' }); row?.focus({ preventScroll: true })
 })
}

const discovery = ref<SSHDiscovery>(), importedAlias = ref(''), effective = ref<SSHEffective>(), trustConfig = ref(false)
const projects = computed(() => status.value?.settings.projects ?? [])
const savedProject = computed(() => projects.value.find(p => p.id === status.value?.settings.activeProjectID))
const quickConnection = computed(() => savedProject.value?.connections.find(c => c.id === savedProject.value?.defaultConnectionID) ?? savedConnection.value)
const savedConnection = computed(() => savedProject.value?.connections.find(c => c.id === status.value?.settings.activeConnectionID))
const savedDraftConnection = computed(() => savedProject.value?.connections.find(c => c.id === draftConnection.value?.id))
const canForgetSavedPassword = computed(() => !!savedDraftConnection.value && (draftConnection.value?.connection.authMode === 'password' || savedDraftConnection.value.connection.authMode === 'password'))
const navigationMatchesActive = computed(() => navigation.value.projectID === status.value?.settings.activeProjectID && navigation.value.connectionID === status.value?.settings.activeConnectionID && !!navigation.value.projectID && !!navigation.value.connectionID)
const navigationState = computed(() => navigationMatchesActive.value ? navigation.value.state : 'disconnected')
const navigationLabel = computed(() => navigationState.value === 'connected' ? 'Подключён' : navigationState.value === 'connecting' ? 'Подключение…' : 'Отключён')
const draftConnection = computed(() => draft.connections[editConnection.value]!)
const queueRunning = computed(() => queue.value.jobs.some(job => job.status === 'running'))
const queueCount = computed(() => queue.value.jobs.filter(job => job.status === 'waiting' || job.status === 'running').length)
const queueProblems = computed(() => queueError.value ? 'ошибка' : (queue.value.jobs.filter(job => job.status === 'failed').length ? `${queue.value.jobs.filter(job => job.status === 'failed').length} ошибок` : ''))
const previewActionCount = computed(() => preview.value?.items.filter(item => !item.skippedReason).length ?? 0)
const previewConflictCount = computed(() => preview.value?.items.filter(item => item.kind === 'file' && item.exists && !item.skippedReason).length ?? 0)
const previewEnvironmentCount = computed(() => preview.value?.items.filter(item => { const name = item.relativePath.split('/').at(-1)?.toLowerCase() ?? ''; return item.kind === 'file' && !item.skippedReason && (name === '.env' || name.startsWith('.env.')) }).length ?? 0)
const excludesText = computed({ get: () => (draftConnection.value?.excludes ?? []).join('\n'), set: (value: string) => { if (draftConnection.value) draftConnection.value.excludes = value.replace(/\r\n/g, '\n').split('\n') } })

function copy<T>(value: T): T { return JSON.parse(JSON.stringify(value)) }
function replaceProject(target: Project, value: Project) {
 target.glowSize = value.glowSize ?? 100
 Object.assign(target, copy(value)); target.color = /^#[0-9a-f]{6}$/i.test(value.color ?? '') ? value.color : '#517bbd'; target.defaultConnectionID = value.defaultConnectionID
 target.excludeDotDirectories = value.excludeDotDirectories
 target.searchExcludedDirectories = value.searchExcludedDirectories ? [...value.searchExcludedDirectories] : undefined
 target.searchIncludedDirectories = value.searchIncludedDirectories ? [...value.searchIncludedDirectories] : undefined
}
function resetTrees() { transferMenu.value = undefined; selectionAnchors.local = undefined; selectionAnchors.remote = undefined; trees.local = treeRoot(); trees.remote = treeRoot(); selections.local.clear(); selections.remote.clear(); preview.value = undefined }
function setNavigationDisconnected(projectID = status.value?.settings.activeProjectID ?? '', connectionID = status.value?.settings.activeConnectionID ?? '') {
 navigation.value = { state: 'disconnected', projectID, connectionID }
}
async function syncNavigationStatus(revision = navigationRevision) {
 const requestRevision = navigationRevision
 const requestSequence = ++navigationRequestSequence
 try {
  const snapshot = await backend().GetNavigationStatus()
  if (revision !== navigationRevision || requestRevision !== navigationRevision || requestSequence !== navigationRequestSequence) return
  const activeProjectID = status.value?.settings.activeProjectID ?? ''
  const activeConnectionID = status.value?.settings.activeConnectionID ?? ''
  if (snapshot.projectID === activeProjectID && snapshot.connectionID === activeConnectionID) {
   navigation.value = snapshot
  } else if (snapshot.state === 'disconnected' && !snapshot.projectID && !snapshot.connectionID) {
   setNavigationDisconnected(activeProjectID, activeConnectionID)
  }
 } catch {
  // Status polling must not interrupt navigation or surface a background error.
 }
}
function nameRemoteRoot() { const path = savedConnection.value?.rootPath?.replace(/\/+$/, ''); trees.remote.entry.name = path?.split('/').at(-1) || savedConnection.value?.name || 'Сервер' }
async function run(action: () => Promise<void>) {
 if (busy.value) return
 busy.value = true; error.value = ''; notice.value = ''
 try { await action() } catch (e) { error.value = explainError(e) } finally { busy.value = false }
}
function explainError(errorValue: unknown): string {
 const message = String(errorValue)
 if (message.includes('remove remote temporary file') || message.includes('remove local temporary file')) return `Не удалось удалить временный файл: ${message}`
 if (message.includes('destination already exists')) return 'Файл уже существует. Разрешите замену в окне проверки передачи.'
 if (message.includes('not a regular file')) return 'Можно передавать только обычные файлы. Ссылки и специальные файлы недоступны.'
 if (message.includes('not a real directory')) return 'Папка назначения отсутствует или является ссылкой. Проверьте путь.'
 if (message.includes('posix-rename@openssh.com')) return 'Сервер не поддерживает безопасную замену файла. Предыдущий файл сохранён.'
 if (message.includes('changed during transfer') || message.includes('metadata differs')) return 'Файл изменился во время передачи. Проверьте источник и назначение.'
 return message
}
function pruneChangedSelection() {
 if (localTab.value !== 'changes') return
 const selectable = new Set(git.value.files.filter(file => file.status !== 'clean' && file.status !== 'ignored' && file.status !== 'deleted' && !file.isDir).map(file => file.path))
 for (const path of selections.local) if (!selectable.has(path)) selections.local.delete(path)
}
async function refreshGit() {
 const projectID = project.id
 if (!projectID || gitLoading.value) return
 const request = ++gitRequest
 gitLoading.value = true; gitError.value = ''
 try {
  const api = backend()
  if (!api?.GetGitStatus) { git.value = { available: false, message: 'Git-статус доступен в приложении Wails', files: [] }; return }
  const result = await api.GetGitStatus(projectID)
  if (request === gitRequest && project.id === projectID) { git.value = result; pruneChangedSelection() }
 } catch (e) {
  if (request === gitRequest && project.id === projectID) { git.value = { available: false, files: [] }; gitError.value = String(e) }
 } finally { if (request === gitRequest) gitLoading.value = false }
}
function pollGit() {
 if (!gitPollingEnabled) return
 gitTimer = setTimeout(async () => {
  if (!document.hidden && !busy.value && project.id) await refreshGit()
  pollGit()
 }, 5000)
}
watch(() => [project.id, project.localRoot], () => {
 gitRequest++; gitLoading.value = false; git.value = { available: false, files: [] }; gitError.value = ''
 collapsedGitDirectories.clear(); unversionedOpen.value = false; localTab.value = 'project'; void refreshGit()
})
function cycleToolWindow() {
 selectLocalTab(localTab.value === 'project' ? 'changes' : 'project')
 nextTick(() => document.getElementById(`${localTab.value}-tab`)?.focus())
}
function selectLocalTab(tab: 'project' | 'changes') {
 localTab.value = tab; selections.local.clear(); selectionAnchors.local = undefined; transferMenu.value = undefined
 if (tab === 'changes') void refreshGit()
}
function toggleChangedDirectory(node: TreeNode) {
 if (node.expanded) collapsedGitDirectories.add(node.entry.relativePath)
 else collapsedGitDirectories.delete(node.entry.relativePath)
}
watch(gitMode, () => { selections.local.clear(); selectionAnchors.local = undefined; transferMenu.value = undefined })
async function loadLocalRoot() {
 if (!project.id) return
 trees.local = treeRoot(); trees.local.entry.name = project.name
 await loadDirectory(trees.local, path => backend().ListProjectLocal(path))
}
function refreshDraftFromProject() {
 replaceProject(draft, project)
 if (!draft.defaultConnectionID && draft.connections.length) draft.defaultConnectionID = draft.connections[0].id
 editConnection.value = Math.max(0, draft.connections.findIndex(c => c.id === status.value?.settings.activeConnectionID))
 editMapping.value = Math.max(0, draftConnection.value?.mappings.findIndex(m => m.id === status.value?.settings.activeMappingID) ?? 0)
 if (editMapping.value < 0) editMapping.value = 0
 discovery.value = undefined; importedAlias.value = ''; effective.value = undefined; trustConfig.value = false
}
async function openSettings() {
 wizard.value = false; settingsTrigger.value = document.activeElement as HTMLElement
 refreshDraftFromProject(); settingsOpen.value = true; error.value = ''
 await nextTick(); settingsDialog.value?.showModal()
}
async function beginProject() {
 settingsTrigger.value = document.activeElement as HTMLElement
 phpStormImport.value = undefined; importServers.value = []; importEnabled.value = false
 replaceProject(draft, emptyProject()); wizard.value = true; settingsOpen.value = true; error.value = ''
 draft.color = '#' + Array.from(crypto.getRandomValues(new Uint8Array(3)), value => (80 + value % 128).toString(16)).join('')
 await nextTick(); settingsDialog.value?.showModal()
}
function closeSettings(discard = true) {
 if (discard) refreshDraftFromProject()
 settingsDialog.value?.close(); settingsOpen.value = false; wizard.value = false
 nextTick(() => settingsTrigger.value?.focus())
}
function onSettingsClose() {
 // A queued close event may arrive after the same dialog has been reopened.
 if (settingsDialog.value?.open) return
 if (settingsOpen.value) { refreshDraftFromProject(); settingsOpen.value = false; wizard.value = false; nextTick(() => settingsTrigger.value?.focus()) }
}
function cancelSettings(event: Event) { if (busy.value) { event.preventDefault(); return }; closeSettings() }
function startNewProject() { void beginProject() }
function addConnection() {
 const connection = emptyConnection(); draft.connections.push(connection)
 editConnection.value = draft.connections.length - 1; editMapping.value = 0
 if (!draft.defaultConnectionID) draft.defaultConnectionID = connection.id
}
function removeConnection() {
 const [removed] = draft.connections.splice(editConnection.value, 1)
 if (removed && draft.defaultConnectionID === removed.id) draft.defaultConnectionID = draft.connections[0]?.id
 editConnection.value = Math.max(0, Math.min(editConnection.value, draft.connections.length - 1)); editMapping.value = 0
}
function addMapping() { draftConnection.value.mappings.push({ id: crypto.randomUUID(), localPath: '', deploymentPath: '.' }); editMapping.value = draftConnection.value.mappings.length - 1 }
function removeMapping(index: number) { draftConnection.value.mappings.splice(index, 1); editMapping.value = 0 }
function scrollToServers() { document.getElementById('server-settings')?.scrollIntoView({ behavior: 'smooth' }) }
async function chooseLocalDirectory() {
 if (busy.value) return
 choosingDirectory.value = true
 try { await run(async () => { const selected = await backend().SelectLocalDirectory(draft.localRoot); if (selected) { draft.localRoot = selected; fillProjectName() } }) }
 finally { choosingDirectory.value = false }
}
function fillProjectName() { draft.name = projectNameFromRoot(draft.name, draft.localRoot) }
watch(() => draft.localRoot, () => { phpStormImport.value = undefined; importServers.value = [] })
watch(importEnabled, () => { phpStormImport.value = undefined; importServers.value = [] })
async function previewPhpStormImport() {
 await run(async () => {
  phpStormImport.value = undefined; importServers.value = []
  const sourceRoot = draft.localRoot
  const result = await backend().PreviewPhpStormProject(sourceRoot)
  if (!settingsOpen.value || !wizard.value || draft.localRoot !== sourceRoot) return
  draft.localRoot = result.project.localRoot
  if (!draft.name.trim()) draft.name = result.project.name
  await nextTick()
  phpStormImport.value = result
  importServers.value = result.project.connections.filter(c => !result.aliasConnectionIDs.includes(c.id)).map(c => c.id)
 })
}
async function saveProject() {
 await run(async () => {
  const payload = copy(draft)
  if (wizard.value && importEnabled.value) {
   if (!phpStormImport.value || phpStormImport.value.project.localRoot !== draft.localRoot) throw new Error('Сначала прочитайте настройки выбранного проекта PhpStorm.')
   payload.connections = copy(phpStormImport.value.project.connections.filter(c => importServers.value.includes(c.id)))
   payload.defaultConnectionID = payload.connections.some(c => c.id === phpStormImport.value?.project.defaultConnectionID) ? phpStormImport.value.project.defaultConnectionID : undefined
  }
  for (const connection of payload.connections) connection.excludes = (connection.excludes ?? []).filter(Boolean)
  if (!payload.defaultConnectionID && payload.connections.length) payload.defaultConnectionID = payload.connections[0].id
  status.value = wizard.value && importEnabled.value ? await backend().CreateImportedProject(payload) : await backend().SaveProject(payload)
  navigationRevision++; setNavigationDisconnected()
  replaceProject(project, projects.value.find(p => p.id === status.value?.settings.activeProjectID) ?? payload)
  resetTrees(); nameRemoteRoot(); closeSettings(false); await loadLocalRoot()
  await syncNavigationStatus()
  notice.value = 'Проект сохранён.'
 })
}
async function pollAuthPrompt() {
 if (authPollTask) return authPollTask
 const revision = authPollRevision
 authPollTask = (async () => {
  const api = backend()
  if (!api?.GetAuthPrompt || authSubmitting.value) return
  try {
   const prompt = await api.GetAuthPrompt()
   if (!authPollingEnabled || revision !== authPollRevision || authSubmitting.value) return
   if (!prompt) {
    if (authPrompt.value) { authPrompt.value = null; authAnswer.value = ''; authRemember.value = false; authPromptError.value = ''; authDialog.value?.close() }
    return
   }
   if (prompt.id === answeredAuthPromptID) return
   if (authPrompt.value?.id !== prompt.id) {
    authAnswer.value = ''; authRemember.value = false; authPromptError.value = ''
    authPrompt.value = { id: prompt.id, prompt: prompt.prompt, canRemember: prompt.canRemember }
   }
  } catch {
   // Background auth polling must not interrupt the operation waiting for input.
  }
 })()
 try { await authPollTask } finally { authPollTask = undefined }
}
function scheduleAuthPoll() {
 if (authPollingEnabled) authPollTimer = setTimeout(() => { void pollAuthPrompt().finally(scheduleAuthPoll) }, 250)
}
async function submitAuthPrompt(cancelled = false) {
 const prompt = authPrompt.value, api = backend()
 if (!prompt || authSubmitting.value) return
 if (!api?.RespondAuthPrompt) { authPromptError.value = 'Ответ SSH-аутентификации недоступен.'; return }
 authSubmitting.value = true; authPromptError.value = ''
 const answer = cancelled ? '' : authAnswer.value
 const remember = !cancelled && prompt.canRemember && authRemember.value
 authAnswer.value = ''
 authRemember.value = false
 authPollRevision++
 try {
  await api.RespondAuthPrompt(prompt.id, answer, cancelled, remember)
  answeredAuthPromptID = prompt.id
  if (authPrompt.value?.id === prompt.id) authPrompt.value = null
  authDialog.value?.close()
 } catch (e) {
  authPromptError.value = String(e)
  await nextTick(); if (!authDialog.value?.open) authDialog.value?.showModal()
  await nextTick(); authAnswerInput.value?.focus()
 } finally {
  authSubmitting.value = false
  void pollAuthPrompt()
 }
}
function cancelAuthPrompt(event?: Event) { event?.preventDefault(); void submitAuthPrompt(true) }
async function forgetSavedPassword() {
 const projectID = savedProject.value?.id
 const connectionID = savedDraftConnection.value?.id
 if (!projectID || !connectionID || !canForgetSavedPassword.value) return
 await run(async () => {
  const api = backend()
  if (!api.ForgetSavedPassword) throw new Error('Удаление сохранённого пароля недоступно.')
  await api.ForgetSavedPassword(projectID, connectionID)
  notice.value = 'Сохранённый пароль удалён.'
 })
}
function requestProjectOpen(event: Event) {
 const select = event.target as HTMLSelectElement
 const selected = projects.value.find(p => p.id === select.value)
 select.value = project.id
 if (!selected || selected.id === project.id || busy.value || projectOpening.value) return
 projectSelectTrigger = select; projectOpenError.value = ''
 projectToOpen.value = { id: selected.id, name: selected.name }
}
async function openSelectedProject(choice: 'current' | 'new' | 'cancel') {
 if (projectOpening.value || !projectToOpen.value) return
 if (choice === 'cancel') {
  projectToOpen.value = undefined
  await nextTick(); projectSelectTrigger?.focus()
  return
 }
 projectOpening.value = true; projectOpenError.value = ''
 try {
  const result = await backend().OpenProject(projectToOpen.value.id, choice)
  status.value = result.status
  projectToOpen.value = undefined
  if (result.action === 'current') {
   await run(async () => {
    navigationRevision++; setNavigationDisconnected(result.status.settings.activeProjectID, '')
    const active = savedProject.value
    if (active) replaceProject(project, active)
    refreshDraftFromProject(); resetTrees(); nameRemoteRoot(); await loadLocalRoot()
    await syncNavigationStatus()
   })
  } else if (result.action === 'focused') notice.value = 'Проект уже открыт в другом окне.'
 } catch (e) { projectOpenError.value = String(e) }
 finally { projectOpening.value = false }
}
async function refreshProjectCatalog() {
 if (busy.value || settingsOpen.value || projectOpening.value || !status.value) return
 const before = status.value
 try {
  const updated = await backend().GetStatus()
  if (status.value === before && !busy.value && !settingsOpen.value && !projectOpening.value && JSON.stringify(updated.settings) !== JSON.stringify(before.settings)) status.value = updated
 } catch { /* Keep the current workspace on a background refresh failure. */ }
}
async function selectConnection(id: string) {
 const connection = savedProject.value?.connections.find(c => c.id === id), mapping = connection?.mappings[0]
 if (savedProject.value && connection) await selectProfile(savedProject.value.id, connection.id, mapping?.id ?? '')
}
async function selectProfile(projectID: string, connectionID: string, mappingID: string) {
 await run(async () => {
  navigationRevision++
  const sameNavigationServer = navigation.value.projectID === projectID && navigation.value.connectionID === connectionID
  if (!sameNavigationServer) setNavigationDisconnected(projectID, connectionID)
  status.value = await backend().SelectProject(projectID, connectionID, mappingID)
 trees.remote = treeRoot(); nameRemoteRoot(); selections.remote.clear(); selections.local.clear(); selectionAnchors.remote = undefined; selectionAnchors.local = undefined; closePreview(); await loadLocalRoot()
  await syncNavigationStatus()
 })
}
async function refreshLocalPanel() {
 if (localTab.value === 'project') await refreshLocal()
 await refreshGit()
}
async function refreshLocal() {
 if (!project.id) return
 await run(async () => { selections.local.clear(); selectionAnchors.local = undefined; closePreview(); await loadDirectory(trees.local, path => backend().ListProjectLocal(path)) })
}
async function connectRemote() {
 if (busy.value || queueRunning.value || !savedConnection.value) return
 nameRemoteRoot()
 navigationRevision++
 navigation.value = { state: 'connecting', projectID: status.value?.settings.activeProjectID ?? '', connectionID: status.value?.settings.activeConnectionID ?? '' }
 await run(async () => { selections.remote.clear(); selectionAnchors.remote = undefined; closePreview(); await loadDirectory(trees.remote, path => backend().ListRemote(path)) })
 await syncNavigationStatus()
}
async function disconnectRemote() {
 if (busy.value || queueRunning.value || !savedConnection.value) return
 navigationRevision++; setNavigationDisconnected()
 await run(async () => {
  await backend().DisconnectRemote()
  trees.remote = treeRoot(); nameRemoteRoot(); selections.remote.clear(); selectionAnchors.remote = undefined; closePreview()
 })
 navigationRevision++; setNavigationDisconnected()
 await syncNavigationStatus()
}
async function reconnectRemote() {
 if (busy.value || queueRunning.value || !savedConnection.value) return
 navigationRevision++
 setNavigationDisconnected()
 trees.remote = treeRoot(); nameRemoteRoot(); selections.remote.clear(); selectionAnchors.remote = undefined; closePreview()
 await run(async () => {
  await backend().DisconnectRemote()
  navigation.value = { state: 'connecting', projectID: status.value?.settings.activeProjectID ?? '', connectionID: status.value?.settings.activeConnectionID ?? '' }
  await loadDirectory(trees.remote, path => backend().ListRemote(path))
 })
 await syncNavigationStatus()
}
async function treeAction(side: 'local' | 'remote', node: TreeNode, refresh = false) {
 if (busy.value || (side === 'remote' && queueRunning.value)) return
 await run(async () => {
  const loader = (path: string) => side === 'local' ? backend().ListProjectLocal(path) : backend().ListRemote(path)
  if (refresh) { selections[side].clear(); selectionAnchors[side] = undefined; preview.value = undefined; await loadDirectory(node, loader) }
  else await toggleDirectory(node, loader)
 })
 if (side === 'remote') await syncNavigationStatus()
}
function selectFile(side: 'local' | 'remote', file: FileEntry, event: MouseEvent | KeyboardEvent) {
 transferMenu.value = undefined
 selectionAnchors[side] = selectTreeEntry(selections[side], side === 'local' && localTab.value === 'changes' ? changesSelectionTree.value : trees[side], file, event, selectionAnchors[side])
 if (side === 'local') pruneChangedSelection()
 preview.value = undefined
}
const draggedFile = ref<{ side: 'local' | 'remote'; path: string; projectID: string; connectionID?: string }>()
function startFileDrag(side: 'local' | 'remote', file: FileEntry, event: DragEvent) {
 if (busy.value || queueRunning.value || file.excluded || file.isLink || file.missing || file.relativePath === '.' || (side === 'remote' && !savedConnection.value)) { event.preventDefault(); return }
 transferMenu.value = undefined
 draggedFile.value = { side, path: file.relativePath, projectID: project.id, connectionID: savedConnection.value?.id }
 if (event.dataTransfer) { event.dataTransfer.effectAllowed = 'copy'; event.dataTransfer.setData('application/x-sftp-project-file', file.relativePath) }
}
async function dropFileOn(side: 'local' | 'remote', target: FileEntry) {
 const source = draggedFile.value; draggedFile.value = undefined
 const connectionID = savedConnection.value?.id
 if (!source || source.side === side || source.projectID !== project.id || !connectionID || source.connectionID !== connectionID || !target.isDir || target.isLink || target.excluded || busy.value || queueRunning.value) return
 const direction = source.side === 'local' ? 'upload' : 'download'
 await run(async () => {
  preview.value = undefined; overwrite.value = false; includeEnvironment.value = false
  previewSelectionSide.value = source.side
  preview.value = await backend().PreviewDropTransfer(source.projectID, connectionID, direction, source.path, target.relativePath)
  await nextTick(); previewDialog.value?.showModal()
 })
}
watch([() => project.id, savedConnection, busy, queueRunning], () => { draggedFile.value = undefined })
function openTransferMenu(side: 'local' | 'remote', event: MouseEvent, file?: FileEntry) {
 if (file?.missing) return
 if (busy.value || queueRunning.value) return
 if (file && (file.excluded || file.isLink)) { preview.value = undefined; transferMenu.value = { side, x: event.clientX, y: event.clientY, file }; return }
 if (file && !selections[side].has(file.relativePath)) {
  selections[side].clear(); selections[side].add(file.relativePath); selectionAnchors[side] = file.relativePath
 }
 if (!selections[side].size) return
 preview.value = undefined
 transferMenu.value = { side, x: event.clientX, y: event.clientY, file }
}
const contextActionConnections = computed(() => {
 const menu = transferMenu.value
 if (!menu) return []
 try { return eligibleConnections(savedProject.value?.connections ?? [], projectSelectionPaths(menu.side)) }
 catch { return [] }
})
const contextCopyConnections = computed(() => {
 const menu = transferMenu.value
 if (!menu) return []
 const paths = menu.file && !selections[menu.side].has(menu.file.relativePath) ? [menu.file.relativePath] : [...selections[menu.side]]
 try { return eligibleCopyConnections(savedProject.value?.connections ?? [], projectSelectionPaths(menu.side, paths)) }
 catch { return [] }
})
function availableConnections(side: 'local' | 'remote') {
 try { return eligibleConnections(savedProject.value?.connections ?? [], projectSelectionPaths(side)) } catch { return [] }
}
function preferredConnection(connections: ProjectConnection[]) { return connections.find(c => c.id === quickConnection.value?.id) ?? connections[0] }
const localQuickConnection = computed(() => preferredConnection(availableConnections('local')))
const remoteQuickConnection = computed(() => preferredConnection(availableConnections('remote')))
async function copySelectedPaths(kind: 'local' | 'project' | 'server', targetID = '') {
 const menu = transferMenu.value; transferMenu.value = undefined
 if (!menu || busy.value) return
 const paths = menu.file && !selections[menu.side].has(menu.file.relativePath) ? [menu.file.relativePath] : [...selections[menu.side]]
 const sourceID = menu.side === 'remote' ? savedConnection.value?.id ?? '' : ''
 await run(async () => {
  await backend().CopyPaths(project.id, menu.side, sourceID, targetID, paths, kind)
  notice.value = paths.length === 1 ? 'Путь скопирован.' : `Скопировано путей: ${paths.length}.`
 })
}
async function refreshContextDirectory() {
 const menu = transferMenu.value; transferMenu.value = undefined
 if (!menu?.file?.isDir || menu.file.isLink || menu.file.excluded) return
 const find = (node: TreeNode): TreeNode | undefined => {
  if (node.entry.relativePath === menu.file!.relativePath) return node
  for (const child of node.children) { const found = find(child); if (found) return found }
 }
 const node = find(trees[menu.side])
 if (node) await treeAction(menu.side, node, true)
 if (menu.side === 'local') void refreshGit()
}
async function setDirectorySearchExcluded(excluded: boolean) {
 const file = transferMenu.value?.file; transferMenu.value = undefined
 if (!file?.isDir || busy.value) return
 await run(async () => {
  status.value = await backend().SetDirectorySearchExcluded(project.id, file.relativePath, excluded)
  if (savedProject.value) replaceProject(project, savedProject.value)
  refreshDraftFromProject()
  function updateMarks(node: TreeNode) {
   if (node.entry.isDir) {
    node.entry.searchExcluded = directorySearchExcluded(project, node.entry.relativePath)
   }
   node.children.forEach(updateMarks)
  }
  updateMarks(trees.local)
 })
}
async function openFile(side: 'local' | 'remote', relative: string) {
 if (side === 'remote' && queueRunning.value) return
 await run(async () => { await fileWorkspace.value?.openFile(side, relative, side === 'remote' ? savedConnection.value?.id : '') })
 if (side === 'remote') await syncNavigationStatus()
}
async function refreshFile(side: 'local' | 'remote', relative: string, connectionID: string) {
 if (side === 'remote' && queueRunning.value) return
 await run(async () => { await fileWorkspace.value?.refreshFile(side, relative, connectionID) })
 if (side === 'remote') await syncNavigationStatus()
}
async function compareGitSelected(ref?: string) {
 const menu = transferMenu.value; transferMenu.value = undefined
 if (!menu?.file || menu.side !== 'local' || menu.file.isDir || selections.local.size !== 1 || busy.value) return
 await fileWorkspace.value?.openGitCompare(menu.file.relativePath, ref)
}
async function compareSelected(connectionID: string) {
 const menu = transferMenu.value; transferMenu.value = undefined
 if (!menu?.file || menu.file.isDir || selections[menu.side].size !== 1 || busy.value || queueRunning.value) return
 let relative: string
 try { relative = projectSelectionPaths(menu.side)[0]! } catch (e) { error.value = explainError(e); return }
 await run(async () => { await fileWorkspace.value?.compareFile(relative, connectionID) })
 await syncNavigationStatus()
}
function projectSelectionPaths(side: 'local' | 'remote', paths = [...selections[side]]): string[] {
 if (side === 'local') return paths
 const connection = savedConnection.value
 if (!connection) return []
 return paths.map(path => {
  const mappings = [...connection.mappings].sort((left, right) => right.deploymentPath.split(/[\\/]/).filter(Boolean).length - left.deploymentPath.split(/[\\/]/).filter(Boolean).length)
  for (const mapping of mappings) {
   try { return fromDeploymentPath(path, mapping.deploymentPath, mapping.localPath) } catch { /* try the next configured mapping */ }
  }
  throw new Error(`Путь «${path}» находится вне всех маппингов сервера. Его можно просматривать, но нельзя передавать.`)
 })
}
async function transferSelection(side: 'local' | 'remote', direction: 'upload' | 'download', connectionID?: string) {
 transferMenu.value = undefined
 if (busy.value || queueRunning.value || !selections[side].size) return
 let paths: string[]
 try { paths = projectSelectionPaths(side) } catch (e) { error.value = explainError(e); return }
 if (!paths.length) return
 const connection = connectionID ? savedProject.value?.connections.find(c => c.id === connectionID) : preferredConnection(eligibleConnections(savedProject.value?.connections ?? [], paths))
 if (!connection) { error.value = 'Добавьте сервер в настройки проекта.'; return }
 if (!connection.mappings.length) { error.value = 'Добавьте маппинг к серверу, чтобы передавать файлы.'; return }
 const mapping = matchingMapping(connection, paths)
 if (!mapping) { error.value = 'Выбранные файлы должны находиться внутри одного маппинга выбранного сервера.'; return }
 const relatives = paths.map(path => toMappingPath(path, mapping.localPath))
 await run(async () => {
  preview.value = undefined; overwrite.value = false; includeEnvironment.value = false
  previewSelectionSide.value = side
  preview.value = await backend().PreviewBatchForServer(project.id, connection.id, mapping.id, direction, relatives)
  await nextTick(); previewDialog.value?.showModal()
 })
}
function clearSelection(side: 'local' | 'remote') { transferMenu.value = undefined; selectionAnchors[side] = undefined; selections[side].clear(); preview.value = undefined }
function transferAction(item: BatchPreview['items'][number]) { return item.skippedReason ? `Пропустить: ${item.skippedReason}` : item.kind === 'directory' ? (item.exists ? 'Папка существует' : 'Создать папку') : item.exists ? (overwrite.value ? 'Заменить' : 'Пропустить существующий') : 'Передать' }
function closePreview() { previewDialog.value?.close(); preview.value = undefined }
function cancelPreview(event: Event) { if (busy.value) { event.preventDefault(); return }; preview.value = undefined }
async function executeBatch() {
 const plan = preview.value; if (!plan) return
 if (queueBusy.value) return
 if (queueRunning.value) { error.value = 'Дождитесь завершения текущей передачи, чтобы построить новый план.'; return }
 error.value = ''
 try {
  const result = await mutateQueue(() => backend().EnqueueBatch(plan.token, overwrite.value ? 'overwrite' : 'skip', includeEnvironment.value))
  if (!result) return
  queuePanelOpen.value = true
  previewDialog.value?.close(); preview.value = undefined
  if (previewSelectionSide.value) { selections[previewSelectionSide.value].clear(); selectionAnchors[previewSelectionSide.value] = undefined }
  notice.value = 'Передача добавлена в очередь.'
 } catch (e) { error.value = explainError(e) }
}
async function fetchQueueSnapshot(force = false) {
 if (queueBusy.value && !force) return
 if (queuePollTask) return queuePollTask
 const revision = queueSnapshotRevision
 queuePollTask = (async () => {
  try {
   const snapshot = await backend().GetQueue()
   if (revision === queueSnapshotRevision) { queue.value = snapshot; queueError.value = '' }
  } catch (e) {
   if (revision === queueSnapshotRevision) queueError.value = explainError(e)
  }
 })()
 try { await queuePollTask } finally { queuePollTask = undefined }
}
async function refreshQueueSnapshot() {
 await fetchQueueSnapshot(true)
 await fetchQueueSnapshot(true)
}
function isQueueState(value: unknown): value is QueueState {
 return !!value && typeof value === 'object' && 'paused' in value && 'jobs' in value && Array.isArray((value as QueueState).jobs)
}
async function pollQueue() {
 await fetchQueueSnapshot()
 await syncNavigationStatus()
 if (queuePollingEnabled) queuePollTimer = setTimeout(() => { void pollQueue() }, 700)
}
async function mutateQueue<T>(action: () => Promise<T>): Promise<T | undefined> {
 if (queueBusy.value) return
 queueBusy.value = true; queueSnapshotRevision++
 try {
  const result = await action()
  if (isQueueState(result)) queue.value = result
  queueSnapshotRevision++
  await refreshQueueSnapshot()
  return result
 } finally { queueSnapshotRevision++; queueBusy.value = false }
}
async function pauseQueue(paused: boolean) {
 queueError.value = ''
 try { await mutateQueue(async () => { await backend().SetQueuePaused(paused); queue.value = { ...queue.value, paused } }) }
 catch (e) { queueError.value = explainError(e) }
}
async function cancelQueueJob(id: string) {
 queueError.value = ''
 try { await mutateQueue(() => backend().CancelQueueJob(id)) }
 catch (e) { queueError.value = explainError(e) }
}
async function clearFinishedQueue() {
 queueError.value = ''
 try { await mutateQueue(() => backend().ClearFinishedQueue()) }
 catch (e) { queueError.value = explainError(e) }
}
async function retryQueueJob(id: string) {
 if (queueBusy.value || busy.value || queueRunning.value) return
 queueBusy.value = true; queueSnapshotRevision++; queueError.value = ''; error.value = ''
 try {
  await run(async () => {
   if (!queue.value.paused) { await backend().SetQueuePaused(true); queue.value = { ...queue.value, paused: true } }
   preview.value = await backend().PreviewQueueRetry(id)
   previewSelectionSide.value = undefined
   overwrite.value = false; includeEnvironment.value = false
   await nextTick(); previewDialog.value?.showModal()
  })
  queueSnapshotRevision++
  await refreshQueueSnapshot()
 } finally { queueBusy.value = false }
}
async function discoverAliases() {
 discovery.value = undefined; importedAlias.value = ''; effective.value = undefined
 await run(async () => { discovery.value = await backend().DiscoverSSH(draftConnection.value.connection.configPath) })
}
function importAlias() {
 const result = discovery.value, alias = importedAlias.value
 if (!result?.aliases.some(item => item.name === alias)) return
 draftConnection.value.connection.alias = alias; draftConnection.value.connection.configPath = result.configPath
 if (draftConnection.value.name === 'Новый сервер') draftConnection.value.name = alias
}
async function evaluateSSH() {
 if (!trustConfig.value || draftConnection.value.connection.mode !== 'ssh_alias' || !draftConnection.value.connection.alias || busy.value || queueRunning.value) return
 effective.value = undefined
 await run(async () => { effective.value = await backend().EvaluateSSH({ ...draftConnection.value.connection }, trustConfig.value) })
}
async function cancel() { try { await backend().CancelOperation() } catch (e) { error.value = String(e) } }

watch(() => [draftConnection.value?.id, draftConnection.value?.connection.mode, draftConnection.value?.connection.configPath, draftConnection.value?.connection.alias], () => {
 discovery.value = undefined; importedAlias.value = ''; effective.value = undefined; trustConfig.value = false
})
watch(() => authPrompt.value?.id, async id => {
 authAnswer.value = ''; authRemember.value = false; authPromptError.value = ''
 if (!id) return
 await nextTick(); if (!authDialog.value?.open) authDialog.value?.showModal()
 await nextTick(); authAnswerInput.value?.focus()
})

watch([busy, queueRunning, savedConnection, savedProject], () => { transferMenu.value = undefined })
onMounted(async () => {
 window.addEventListener('focus', refreshProjectCatalog)
 gitPollingEnabled = true; pollGit()
 queuePollingEnabled = true
 authPollingEnabled = true
 void pollQueue()
 void pollAuthPrompt().finally(scheduleAuthPoll)
 await run(async () => {
  status.value = await backend().GetStatus()
  const active = savedProject.value
  if (!active) { await beginProject(); return }
  replaceProject(project, active); refreshDraftFromProject(); nameRemoteRoot(); await loadLocalRoot()
 })
})
onUnmounted(() => { window.removeEventListener('focus', refreshProjectCatalog); gitPollingEnabled = false; gitRequest++; if (gitTimer) clearTimeout(gitTimer); queuePollingEnabled = false; authPollingEnabled = false; authPollRevision++; authAnswer.value = ''; authRemember.value = false; if (queuePollTimer) clearTimeout(queuePollTimer); if (authPollTimer) clearTimeout(authPollTimer) })
</script>

<template>
 <main class="app-shell project-glow" :style="{ '--project-color': project.color ?? '#517bbd', '--glow-width': `${760 * (project.glowSize ?? 100) / 100}px`, '--glow-height': `${360 * (project.glowSize ?? 100) / 100}px` }">
  <ProjectOpenDialog v-if="projectToOpen" :project-name="projectToOpen.name" :busy="projectOpening" :error="projectOpenError" @choose="openSelectedProject" />
  <header class="topbar">
   <div class="brand-mark">⇄</div><strong class="brand">SFTP проекты</strong>
   <label class="header-project">Проект <select data-testid="project-select" :value="project.id" :disabled="busy || !projects.length" @change="requestProjectOpen"><option v-for="item in projects" :key="item.id" :value="item.id">{{ item.name }}</option></select></label>
   <button data-testid="create-project" class="quiet" :disabled="busy" @click="startNewProject">＋ Новый проект</button>
   <span class="top-spacer"></span><button data-testid="queue-toggle" type="button" class="quiet" :aria-expanded="queuePanelOpen" @click="queuePanelOpen = !queuePanelOpen">⇅ Очередь<span v-if="queueCount" class="queue-count" :title="`${queueCount} выполняется или ожидает`">{{ queueCount }}</span><span v-if="queueProblems" class="queue-count queue-count-error">{{ queueProblems }}</span></button><button ref="settingsTrigger" data-testid="settings-open" class="quiet" :disabled="busy || !project.id" @click="openSettings">⚙ Настройки</button>
   <button v-if="busy && !settingsOpen && !wizard" class="cancel-button" @click="cancel">Отменить операцию</button>
  </header>
  <div v-if="error && !settingsOpen && !wizard && !preview" class="toast error" role="alert">{{ error }}<button aria-label="Закрыть ошибку" @click="error = ''">×</button></div>
  <div v-if="notice" class="toast" role="status">{{ notice }}<button aria-label="Закрыть сообщение" @click="notice = ''">×</button></div>
  <div v-if="status && project.id" class="workspace-shell">
   <nav class="tool-window-bar" role="tablist" aria-label="Панели проекта" aria-orientation="vertical" data-testid="tool-window-bar" @keydown.down.prevent="cycleToolWindow" @keydown.up.prevent="cycleToolWindow">
    <button id="project-tab" class="tool-window-button" role="tab" :aria-selected="localTab === 'project'" :tabindex="localTab === 'project' ? 0 : -1" aria-controls="project-files-panel" aria-label="Проект" title="Проект" data-testid="project-tab" @click="selectLocalTab('project')"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><rect x="3.5" y="4" width="17" height="12.5" rx="1.5"/><path d="M9 20h6M12 16.5V20M2 20h20"/></svg></button>
    <button id="changes-tab" class="tool-window-button" role="tab" :aria-selected="localTab === 'changes'" :tabindex="localTab === 'changes' ? 0 : -1" aria-controls="git-changes-panel" aria-label="Изменения Git" :title="`Изменения Git${git.available ? ' · ' + changedFiles.length : ''}`" data-testid="changes-tab" @click="selectLocalTab('changes')"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><circle cx="7" cy="5" r="2.3"/><circle cx="7" cy="19" r="2.3"/><circle cx="17" cy="5" r="2.3"/><path d="M7 7.5v9M17 7.5v2c0 3-2 4-5 4H7"/></svg><span v-if="git.available && changedFiles.length" class="tool-window-badge">{{ changedFiles.length }}</span></button>
   </nav>
  <ResizableWorkspace>
   <template #left>
   <section class="pane local-pane" data-testid="local-pane">
    <header class="pane-header local-pane-heading"><h1>{{ localTab === 'project' ? 'Проект' : 'Изменения Git' }}</h1></header>
    <div class="local-actions-slot"><div class="local-actions" role="toolbar" aria-label="Действия с файлами проекта" data-testid="local-actions">
     <div class="local-upload-actions"><button class="primary small local-upload" data-testid="local-upload" :title="`Загрузить на ${localQuickConnection?.name ?? 'сервер по умолчанию'}`" :aria-label="`Загрузить на ${localQuickConnection?.name ?? 'сервер по умолчанию'}`" :disabled="busy || queueRunning || !selections.local.size || !localQuickConnection" @click="transferSelection('local', 'upload')">Загрузить на {{ localQuickConnection?.name ?? 'сервер' }}</button><button v-if="(savedProject?.connections.length ?? 0) > 1" class="quiet small transfer-server-button" title="Выбрать сервер для загрузки" aria-label="Выбрать сервер для загрузки" :disabled="busy || queueRunning || !selections.local.size" @click="openTransferMenu('local', $event)">▾</button></div>
     <button class="link-button local-clear" data-testid="local-clear-selection" :disabled="busy || !selections.local.size" @click="clearSelection('local')">Снять выбор</button>
     <button class="icon-button local-refresh" data-testid="refresh-local-git" :title="localTab === 'changes' ? 'Обновить Git-статус' : 'Обновить файлы и Git-статус'" :aria-label="localTab === 'changes' ? 'Обновить Git-статус' : 'Обновить файлы и Git-статус'" :disabled="busy || gitLoading" @click="refreshLocalPanel">⟳</button>
     <button class="icon-button reveal-file-button" data-testid="reveal-file" title="Показать открытый файл в дереве проекта" aria-label="Показать файл в дереве проекта" :disabled="busy || !revealPath" @click="revealCurrentFile"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" aria-hidden="true"><circle cx="12" cy="12" r="5"/><path d="M12 2v6m0 8v6M2 12h6m8 0h6"/></svg></button>
    </div></div>
    <div v-show="localTab === 'project'" id="project-files-panel" role="tabpanel" aria-labelledby="project-tab" class="tree-scroll"><p v-if="trees.local.loading && !trees.local.loaded" class="empty-state">Загрузка файлов проекта…</p><p v-else-if="trees.local.error && !trees.local.children.length" class="empty-state" role="alert">{{ trees.local.error }} <button @click="refreshLocal">Повторить</button></p><ul class="file-tree tree-root"><FileTree :root-path="project.localRoot" :node="trees.local" :busy="busy" :git-statuses="git.available ? gitStatuses : undefined" side="local" :drag-side="draggedFile?.side" @drag-start="(file, event) => startFileDrag('local', file, event)" @drag-end="draggedFile = undefined" @drop-file="dropFileOn('local', $event)" :selected="selections.local" @toggle="treeAction('local', $event)" @refresh="treeAction('local', $event, true)" @select="(file, event) => selectFile('local', file, event)" @context="(file, event) => openTransferMenu('local', event, file)" @open="openFile('local', $event.relativePath)" /></ul></div>
    <div v-if="localTab === 'changes'" id="git-changes-panel" role="tabpanel" aria-labelledby="changes-tab" class="git-panel">
     <div class="git-view-toolbar"><span>{{ changedFiles.length }} файлов</span><div role="group" aria-label="Отображение изменений"><button data-testid="git-view-list" :aria-pressed="gitMode === 'list'" @click="gitMode = 'list'">Список</button><button data-testid="git-view-tree" :aria-pressed="gitMode === 'tree'" @click="gitMode = 'tree'">Дерево</button></div></div>
     <p v-if="gitError" class="empty-state error" role="alert">{{ gitError }} <button :disabled="gitLoading" @click="refreshGit">Повторить</button></p>
     <p v-else-if="!git.available && !gitLoading" class="empty-state">{{ git.message || 'Git-репозиторий не найден' }}</p>
     <p v-else-if="git.available && !changedFiles.length" class="empty-state" data-testid="git-clean">Нет изменений в отслеживаемых файлах</p>
     <div v-if="git.available" class="tree-scroll git-sections">
     <div v-if="changedFiles.length" data-testid="git-changes"><ul class="file-tree"><FileTree v-for="node in changesTree.children" :key="node.entry.relativePath" :node="node" :busy="busy" side="local" :drag-side="draggedFile?.side" @drag-start="(file, event) => startFileDrag('local', file, event)" @drag-end="draggedFile = undefined" @drop-file="dropFileOn('local', $event)" :selected="selections.local" :git-statuses="gitStatuses" changes-view @toggle="toggleChangedDirectory" @select="(file, event) => selectFile('local', file, event)" @context="(file, event) => openTransferMenu('local', event, file)" @open="openFile('local', $event.relativePath)" /></ul></div>
     <details class="unversioned-group" data-testid="git-unversioned" :open="unversionedOpen" @toggle="unversionedOpen = ($event.target as HTMLDetailsElement).open">
      <summary>Unversioned files <span>{{ unversionedFiles.length }}</span></summary>
      <p v-if="!unversionedFiles.length" class="tree-message">Нет неотслеживаемых файлов</p>
      <ul v-else class="file-tree" data-testid="git-unversioned-files"><FileTree v-for="node in unversionedTree.children" :key="node.entry.relativePath" :node="node" :busy="busy" side="local" :drag-side="draggedFile?.side" @drag-start="(file, event) => startFileDrag('local', file, event)" @drag-end="draggedFile = undefined" @drop-file="dropFileOn('local', $event)" :selected="selections.local" :git-statuses="unversionedStatuses" changes-view @toggle="toggleChangedDirectory" @select="(file, event) => selectFile('local', file, event)" @context="(file, event) => openTransferMenu('local', event, file)" @open="openFile('local', $event.relativePath)" /></ul>
     </details>
     </div>
    </div>
   </section>
   </template>
   <FileWorkspace @active-file-change="activeFile = $event" ref="fileWorkspace" :project="project" :busy="busy" :remote-busy="queueRunning" @open-local="openFile('local', $event)" @refresh="refreshFile" />
   <template #right>
   <section class="pane remote-pane" data-testid="remote-pane">
    <header class="pane-header remote-header"><div><h1>Сервер</h1><div class="root-path" :title="savedConnection?.rootPath || 'Сервер не выбран'">{{ savedConnection?.rootPath || 'Сервер не выбран' }}</div></div>
     <label class="header-select">Сервер <select data-testid="server-select" :value="status.settings.activeConnectionID ?? ''" :disabled="busy || !savedProject?.connections.length" @change="selectConnection(($event.target as HTMLSelectElement).value)"><option v-for="connection in savedProject?.connections" :key="connection.id" :value="connection.id">{{ connection.name }}</option></select></label>
     <button class="quiet compact" :disabled="busy || !project.id" @click="openSettings" aria-label="Настройки серверов">⚙</button>
     <div v-if="savedConnection" class="navigation-actions">
      <span data-testid="navigation-status" class="navigation-status" :class="`navigation-${navigationState}`" :title="navigationLabel">{{ navigationLabel }}</span>
      <button data-testid="disconnect-server" class="icon-button navigation-icon" title="Отключить сервер" aria-label="Отключить сервер" :disabled="busy || queueRunning || navigationState === 'disconnected'" @click="disconnectRemote">⏻</button>
      <button data-testid="reconnect-server" class="icon-button navigation-icon" title="Переподключиться и обновить папку" aria-label="Переподключиться и обновить папку" :disabled="busy || queueRunning" @click="reconnectRemote">↻</button>
      <button data-testid="connect-server" class="primary small" :disabled="busy || queueRunning" @click="connectRemote">{{ navigationState === 'connecting' ? 'Подключение…' : navigationState === 'connected' ? 'Обновить' : 'Подключиться' }}</button>
     </div>
    </header>
    <div v-if="savedConnection" class="tree-toolbar"><span>{{ savedConnection.name }} · {{ selections.remote.size }} выбрано</span><span class="toolbar-spacer"></span><button class="link-button" :disabled="busy || !selections.remote.size" @click="clearSelection('remote')">Снять выбор</button><button class="primary small" :disabled="busy || queueRunning || !selections.remote.size || !trees.remote.loaded || !remoteQuickConnection" @click="transferSelection('remote', 'download')">Скачать с {{ remoteQuickConnection?.name }}</button><button v-if="(savedProject?.connections.length ?? 0) > 1" class="quiet small transfer-server-button" :disabled="busy || queueRunning || !selections.remote.size" @click="openTransferMenu('remote', $event)">Скачать с… ▸</button></div>
    <div class="tree-scroll">
     <div v-if="!savedConnection" class="empty-server"><div class="empty-icon">⌁</div><h2>Сервер не настроен</h2><p>Добавьте SSH- или SFTP-сервер, чтобы просматривать и передавать файлы проекта.</p><button data-testid="add-server" class="primary" :disabled="busy" @click="openSettings">＋ Добавить сервер</button></div>
     <template v-else><div v-if="!trees.remote.loaded && !trees.remote.loading" class="empty-server disconnected-state"><div class="empty-icon">⌁</div><h2>Сервер не подключён</h2><p>Подключитесь, чтобы открыть <strong>{{ savedConnection.rootPath }}</strong>.</p></div><p v-if="trees.remote.error" class="empty-state" role="alert">{{ trees.remote.error }} <button :disabled="busy || queueRunning" @click="connectRemote">Повторить</button></p><ul class="file-tree tree-root"><FileTree :node="trees.remote" :busy="busy || queueRunning" side="remote" :drag-side="draggedFile?.side" @drag-start="(file, event) => startFileDrag('remote', file, event)" @drag-end="draggedFile = undefined" @drop-file="dropFileOn('remote', $event)" :selected="selections.remote" @toggle="treeAction('remote', $event)" @refresh="treeAction('remote', $event, true)" @select="(file, event) => selectFile('remote', file, event)" @context="(file, event) => openTransferMenu('remote', event, file)" @open="openFile('remote', $event.relativePath)" /></ul></template>
    </div>
   </section>
   </template>
  </ResizableWorkspace>
  </div>
  <TransferMenu :can-compare-git="git.available && transferMenu?.side === 'local' && !!transferMenu.file && !transferMenu.file.isDir && !transferMenu.file.isLink && !transferMenu.file.excluded && !transferMenu.file.missing && selections.local.size === 1" :recent-git="recentBranches(savedProject?.localRoot ?? '')" @compare-git="compareGitSelected" can-copy :can-transfer="!transferMenu?.file?.excluded && !transferMenu?.file?.isLink" :copy-connection-id="transferMenu?.side === 'remote' ? savedConnection?.id : quickConnection?.id" @copy-path="copySelectedPaths" :can-refresh-directory="!!transferMenu?.file?.isDir && !transferMenu.file.excluded && !transferMenu.file.isLink" @refresh-directory="refreshContextDirectory" v-if="transferMenu" :x="transferMenu.x" :y="transferMenu.y" :connections="savedProject?.connections ?? []" :action-connections="contextActionConnections" :copy-connections="contextCopyConnections" :current-id="quickConnection?.id" :can-exclude-directory="transferMenu.side === 'local' && !transferMenu.file?.isLink && !!transferMenu.file?.isDir && transferMenu.file.relativePath !== '.'" :search-excluded="transferMenu.file?.searchExcluded" @exclude-directory="setDirectorySearchExcluded" :can-compare="!!transferMenu.file && !transferMenu.file.excluded && !transferMenu.file.isLink && !transferMenu.file.isDir && selections[transferMenu.side].size === 1" @compare="compareSelected" @close="transferMenu = undefined" @transfer="(direction, id) => transferSelection(transferMenu!.side, direction, id)" />
  <TransferQueue v-if="queuePanelOpen" data-testid="queue-panel" :queue="queue" :error="queueError" :busy="queueBusy" :app-busy="busy" @pause="pauseQueue" @cancel="cancelQueueJob" @clear="clearFinishedQueue" @retry="retryQueueJob" />

  <dialog ref="settingsDialog" data-testid="settings-dialog" class="modal settings-modal" :class="{ 'wizard-modal': wizard }" @close="onSettingsClose" @cancel="cancelSettings">
   <form v-if="wizard" class="modal-content" @submit.prevent="saveProject"><p v-if="error" role="alert" class="error">{{ error }}</p><header class="modal-header"><div><span class="eyebrow">НОВЫЙ ПРОЕКТ</span><h2>Создать проект</h2></div></header><p class="modal-intro">Укажите название и локальную папку. Серверы можно добавить позже.</p><label>Название проекта<input v-model="draft.name" data-testid="project-name" maxlength="120" required autofocus placeholder="Моё приложение"></label><label class="project-color-field">Цвет проекта<input v-model="draft.color" type="color" data-testid="project-color"></label><label class="project-glow-field">Размер свечения <output>{{ draft.glowSize ?? 100 }}%</output><input v-model.number="draft.glowSize" type="range" min="25" max="150" step="5" data-testid="project-glow-size"></label><label>Папка проекта<div class="directory-picker"><input v-model="draft.localRoot" @change="fillProjectName" data-testid="project-folder" :disabled="busy" required placeholder="C:\Projects\app"><button type="button" data-testid="project-folder-picker" class="quiet" :disabled="busy" @click="chooseLocalDirectory">Обзор…</button></div></label><section class="phpstorm-import"><label class="check-row"><input v-model="importEnabled" type="checkbox" :disabled="busy" data-testid="phpstorm-enable">Импортировать настройки одного проекта PhpStorm</label>
 <template v-if="importEnabled"><p class="hint">Укажите папку самого проекта с .idea. Другие проекты не просматриваются. Настройки сохранятся только после нажатия «Создать проект».</p><button type="button" class="quiet" data-testid="phpstorm-preview" :disabled="busy || !draft.localRoot.trim()" @click="previewPhpStormImport">Прочитать настройки PhpStorm</button>
 <div v-if="phpStormImport" class="import-preview" data-testid="phpstorm-result"><h3>Найденные серверы: {{ phpStormImport.project.connections.length }}</h3><p class="hint">Выберите серверы для импорта. Подключение к ним не выполняется.</p>
 <label v-if="phpStormImport.project.connections.length" class="check-row"><input type="checkbox" data-testid="import-select-all" :checked="allImportServersSelected" :indeterminate="importSelectionCount > 0 && !allImportServersSelected" :disabled="busy" @change="toggleAllImportServers">Выбрать всё</label>
 <div v-for="server in phpStormImport.project.connections" :key="server.id" class="import-server"><label class="check-row"><input v-model="importServers" :value="server.id" type="checkbox" :disabled="busy">{{ server.name }}</label><p>{{ server.connection.mode === 'ssh_alias' ? 'SSH-алиас: ' + server.connection.alias : server.connection.user + '@' + server.connection.host + ':' + server.connection.port }} · Корень: {{ server.rootPath }}</p><p v-if="phpStormImport.aliasConnectionIDs.includes(server.id)" class="hint">Выбор этого сервера подтверждает использование указанного SSH-алиаса из вашего доверенного OpenSSH config.</p><ul><li v-for="mapping in server.mappings" :key="mapping.id">{{ mapping.localPath }} → {{ mapping.deploymentPath }}</li></ul><p v-if="!server.mappings.length" class="hint">Маппингов нет — добавьте их в настройках перед передачей.</p></div>
 <ul class="hint" role="status"><li v-for="warning in phpStormImport.warnings" :key="warning">{{ warning }}</li></ul></div></template></section><footer class="modal-actions"><button type="button" class="quiet" :disabled="busy" @click="closeSettings()">Отмена</button><button class="primary" data-testid="project-save" :disabled="busy || (importEnabled && !phpStormImport)">Создать проект</button></footer></form>
   <form v-else class="modal-content settings-content" @submit.prevent="saveProject"><p v-if="error" role="alert" class="error">{{ error }}</p><header class="modal-header"><div><span class="eyebrow">НАСТРОЙКИ ПРОЕКТА</span><h2>{{ draft.name || 'Новый проект' }}</h2></div><button type="button" class="icon-button" aria-label="Закрыть настройки" :disabled="busy" @click="closeSettings()">×</button></header><div class="settings-layout"><nav class="settings-nav"><button type="button" class="nav-active">Общие</button><button type="button" @click="scrollToServers">Серверы и маппинги</button></nav><div class="settings-fields" id="server-settings"><section class="settings-section"><h3>Проект</h3><label>Название проекта<input v-model="draft.name" maxlength="120" required></label><label class="project-color-field">Цвет проекта<input v-model="draft.color" type="color" data-testid="project-color"></label><label class="project-glow-field">Размер свечения <output>{{ draft.glowSize ?? 100 }}%</output><input v-model.number="draft.glowSize" type="range" min="25" max="150" step="5" data-testid="project-glow-size"></label><label>Локальная папка проекта<div class="directory-picker"><input v-model="draft.localRoot" @change="fillProjectName" required><button type="button" class="quiet" :disabled="busy" @click="chooseLocalDirectory">Обзор…</button></div></label></section>
    <section class="settings-section"><div class="section-title"><div><h3>Серверы</h3><p>Подключение выполняется только вручную.</p></div><button type="button" data-testid="settings-add-server" class="quiet" :disabled="busy" @click="addConnection">＋ Добавить сервер</button></div><p v-if="!draft.connections.length" class="settings-empty">Серверов пока нет. Добавьте SSH- или SFTP-подключение, когда оно понадобится.</p>
     <div v-else class="server-editor"><aside class="server-list"><button v-for="(connection, index) in draft.connections" :key="connection.id" type="button" :class="{ chosen: editConnection === index }" @click="editConnection = index; editMapping = 0"><span>{{ connection.name || 'Без имени' }}</span><small>{{ connection.connection.mode === 'ssh_alias' ? (connection.connection.alias || 'SSH-алиас') : (connection.connection.host || 'Ручная настройка') }}</small></button></aside>
      <div class="server-fields"><div class="section-title"><h4>Подключение</h4><button type="button" class="link-button danger-link" :disabled="busy" @click="removeConnection">Удалить</button></div><label>Название сервера<input v-model="draftConnection.name" data-testid="server-name" required maxlength="120"></label><label class="check-row"><input type="radio" data-testid="default-server-select" name="default-server" :checked="draft.defaultConnectionID === draftConnection.id" :disabled="busy" @change="draft.defaultConnectionID = draftConnection.id">Использовать по умолчанию</label><label>Тип подключения<select v-model="draftConnection.connection.mode" :disabled="busy"><option value="ssh_alias">SSH-алиас</option><option value="manual">Вручную (SSH/SFTP)</option></select></label>
       <template v-if="draftConnection.connection.mode === 'ssh_alias'"><label>SSH-алиас<input v-model="draftConnection.connection.alias" data-testid="server-alias" required placeholder="work-dev"></label><label>Путь к SSH-конфигу <span class="optional">(необязательно)</span><input v-model="draftConnection.connection.configPath" placeholder="Основной SSH-конфиг"></label><div class="inline-actions"><button type="button" class="quiet" :disabled="busy" @click="discoverAliases">Импортировать алиасы…</button><select v-if="discovery?.aliases.length" v-model="importedAlias"><option value="">Выберите алиас</option><option v-for="alias in discovery.aliases" :key="alias.name" :value="alias.name">{{ alias.name }}</option></select><button v-if="discovery?.aliases.length" type="button" class="quiet" :disabled="busy" @click="importAlias">Использовать</button></div><p v-if="discovery?.warnings.length" class="hint">{{ discovery.warnings.join(' · ') }}</p></template>
       <template v-else><label>Хост<input v-model="draftConnection.connection.host" required></label><div class="two-fields"><label>Пользователь<input v-model="draftConnection.connection.user" required></label><label>Порт<input v-model.number="draftConnection.connection.port" type="number" min="1" max="65535" required></label></div><label>Приватный ключ <span class="optional">(необязательно, иначе SSH-agent)</span><input v-model="draftConnection.connection.identityFile"></label></template>
       <label>Способ аутентификации<select v-model="draftConnection.connection.authMode" data-testid="auth-mode" :disabled="busy"><option value="key_agent">Ключ или SSH-agent</option><option value="password">Пароль при подключении</option></select></label><p v-if="draftConnection.connection.authMode === 'password'" class="hint">Пароль будет запрошен во время подключения и не сохраняется в настройках.</p><button v-if="canForgetSavedPassword" type="button" data-testid="forget-password" class="quiet" :disabled="busy" @click="forgetSavedPassword">Забыть сохранённый пароль</button>
       <label>Файл known_hosts <span class="optional">(необязательно)</span><input v-model="draftConnection.connection.knownHostsFile"></label><div v-if="draftConnection.connection.mode === 'ssh_alias'" class="inline-actions"><label class="check-row"><input v-model="trustConfig" type="checkbox" :disabled="busy || queueRunning">Доверять внешнему SSH-конфигу</label><button type="button" class="quiet" :disabled="busy || queueRunning || !trustConfig || !draftConnection.connection.alias" @click="evaluateSSH">Проверить SSH</button></div><p v-if="effective" class="hint">{{ effective.hostname }} · {{ effective.user }} · порт {{ effective.port }}<span v-if="effective.proxyJump"> · через {{ effective.proxyJump }}</span></p><p v-if="effective?.hasProxyCommand" class="hint warning">В этом подключении используется ProxyCommand.</p>
       <label>Корень на сервере<input v-model="draftConnection.rootPath" data-testid="remote-root" placeholder="/var/www/html" required></label>
       <div class="settings-section mappings-section"><div class="section-title"><div><h4>Маппинги папок</h4><p>Локальная папка проекта связывается с путём относительно корня сервера.</p></div><button type="button" data-testid="mapping-add" class="quiet" :disabled="busy" @click="addMapping">＋ Добавить маппинг</button></div><p v-if="!draftConnection.mappings.length" class="hint">Маппингов пока нет. Добавьте их вручную, чтобы передавать файлы.</p><div v-for="(mapping, index) in draftConnection.mappings" :key="mapping.id" class="mapping-row"><label>Локальная папка<input v-model="mapping.localPath" data-testid="mapping-local-path" placeholder=". или src" required></label><span>→</span><label>Путь развёртывания от корня<input v-model="mapping.deploymentPath" data-testid="deployment-path" placeholder="." required></label><button type="button" data-testid="mapping-remove" class="icon-button" aria-label="Удалить маппинг" :disabled="busy" @click="removeMapping(index)">×</button></div></div>
       <details class="advanced-settings"><summary>Дополнительно · исключения передачи</summary><label>Один шаблон на строку<textarea v-model="excludesText" rows="5" placeholder=".git/**&#10;**/node_modules/**&#10;**/.env" :disabled="busy"></textarea></label><p class="hint">Пути отсчитываются от корня проекта. Исключения применяются при загрузке и скачивании. Файлы окружения защищены от передачи.</p><p class="hint">Ключи сервера проверяются строго; настройте ProxyJump в доверенном SSH-конфиге.</p></details>
      </div>
     </div>
    </section>
    <details class="advanced-settings diagnostics"><summary>Диагностика</summary><dl><dt>Платформа</dt><dd>{{ status?.platform }}</dd><dt>OpenSSH</dt><dd>{{ status?.sshPath || 'Не найден' }}</dd><dt>Состояние SSH</dt><dd>{{ status?.sshMessage }}</dd><dt>Папка настроек</dt><dd>{{ status?.dataDir }}</dd><dt>WebView2</dt><dd>{{ status?.webviewVersion || status?.webviewMessage }}</dd></dl></details>
    </div></div><footer class="modal-actions"><button v-if="busy" type="button" class="cancel-button" @click="cancel">Отменить операцию</button><button type="button" class="quiet" :disabled="busy" @click="closeSettings()">Отмена</button><button class="primary" data-testid="settings-save" :disabled="busy">Сохранить настройки</button></footer></form>
  </dialog>

  <dialog ref="previewDialog" class="modal preview-modal" @cancel="cancelPreview"><div v-if="preview" class="modal-content"><p v-if="error" role="alert" class="error">{{ error }}</p><header class="modal-header"><div><span class="eyebrow">ПЛАН ПЕРЕДАЧИ</span><h2>{{ preview.direction === 'upload' ? 'Загрузить на сервер' : 'Скачать в проект' }}</h2></div><button class="icon-button" :disabled="busy || queueBusy" aria-label="Закрыть просмотр" @click="closePreview">×</button></header><p class="modal-intro">{{ preview.server }} · {{ preview.fileCount }} файлов · {{ formatBytes(preview.totalBytes) }} · {{ previewConflictCount }} существующих файлов</p><div class="transfer-table"><table><thead><tr><th>Источник</th><th>Назначение</th><th>Действие</th></tr></thead><tbody><tr v-for="item in preview.items" :key="item.relativePath"><td>{{ item.source || item.relativePath }}</td><td>{{ item.destination }}</td><td>{{ transferAction(item) }}</td></tr></tbody></table></div><label class="check-row"><input v-model="overwrite" type="checkbox" :disabled="queueBusy || queueRunning">Разрешить замену существующих файлов</label><label v-if="previewEnvironmentCount" class="check-row"><input v-model="includeEnvironment" type="checkbox" :disabled="queueBusy || queueRunning">Явно разрешить передачу {{ previewEnvironmentCount }} файлов .env</label><p class="hint">Передача может завершиться частично. Уже переданные файлы и созданные папки сохраняются при ошибке или отмене.</p><footer class="modal-actions"><button class="quiet" :disabled="queueBusy" @click="closePreview">Отмена</button><button data-testid="queue-enqueue" class="primary" :disabled="queueBusy || queueRunning || !previewActionCount || (!!previewEnvironmentCount && !includeEnvironment)" @click="executeBatch">Добавить в очередь</button></footer></div></dialog>
  <dialog ref="authDialog" data-testid="auth-dialog" class="modal auth-modal" aria-labelledby="auth-title" @cancel="cancelAuthPrompt">
   <form v-if="authPrompt" class="modal-content auth-content" autocomplete="off" @submit.prevent="submitAuthPrompt()">
    <header class="modal-header"><div><span class="eyebrow">SSH</span><h2 id="auth-title">Авторизация SSH</h2></div></header>
    <p data-testid="auth-prompt" class="auth-prompt">{{ authPrompt.prompt }}</p>
    <label>Пароль<input ref="authAnswerInput" v-model="authAnswer" data-testid="auth-answer" type="password" autocomplete="off" :disabled="authSubmitting" autofocus></label>
    <label v-if="authPrompt.canRemember" class="check-row"><input v-model="authRemember" data-testid="auth-remember" type="checkbox" :disabled="authSubmitting">Сохранить пароль</label>
    <p v-if="authPromptError" role="alert" class="error">{{ authPromptError }}</p>
    <footer class="modal-actions"><button data-testid="auth-cancel" type="button" class="quiet" :disabled="authSubmitting" @click="cancelAuthPrompt()">Отмена</button><button data-testid="auth-submit" type="submit" class="primary" :disabled="authSubmitting">{{ authSubmitting ? 'Отправка…' : 'Продолжить' }}</button></footer>
   </form>
  </dialog>
 </main>
</template>
