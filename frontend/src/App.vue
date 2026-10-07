<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, reactive, ref, watch } from 'vue'
import { backend, type AuthPrompt, type BatchPreview, type FileEntry, type NavigationStatus, type Project, type ProjectConnection, type QueueState, type SSHDiscovery, type SSHEffective, type Status } from './bridge'
import FileTree from './components/FileTree.vue'
import TransferQueue from './components/TransferQueue.vue'
import TransferMenu from './components/TransferMenu.vue'
import { fromDeploymentPath, loadDirectory, selectTreeEntry, toggleDirectory, toMappingPath, treeRoot, type TreeNode } from './file-tree'

function emptyProject(): Project { return { id: '', name: '', localRoot: '', connections: [] } }
function emptyConnection(): ProjectConnection {
 return { id: crypto.randomUUID(), name: 'Новый сервер', rootPath: '/', connection: { mode: 'ssh_alias', alias: '', configPath: '', host: '', user: '', port: 22, identityFile: '', knownHostsFile: '' }, mappings: [], excludes: ['.git/**', '**/node_modules/**', '**/.env', '**/.env.*'] }
}
const status = ref<Status>(), project = reactive<Project>(emptyProject()), draft = reactive<Project>(emptyProject())
const error = ref(''), notice = ref(''), busy = ref(false), choosingDirectory = ref(false)
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
const authDialog = ref<HTMLDialogElement>(), authAnswerInput = ref<HTMLInputElement>()
const authPrompt = ref<AuthPrompt | null>(null)
const authAnswer = ref(''), authRemember = ref(false), authPromptError = ref(''), authSubmitting = ref(false)
const trees = reactive({ local: treeRoot(), remote: treeRoot() })
const selections = reactive({ local: new Set<string>(), remote: new Set<string>() })
const selectionAnchors: Record<'local' | 'remote', string | null | undefined> = { local: undefined, remote: undefined }
const transferMenu = ref<{ side: 'local' | 'remote'; x: number; y: number }>()

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
function replaceProject(target: Project, value: Project) { Object.assign(target, copy(value)); target.defaultConnectionID = value.defaultConnectionID }
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
 replaceProject(draft, emptyProject()); wizard.value = true; settingsOpen.value = true; error.value = ''
 await nextTick(); settingsDialog.value?.showModal()
}
function closeSettings(discard = true) {
 if (discard) refreshDraftFromProject()
 settingsDialog.value?.close(); settingsOpen.value = false; wizard.value = false
 nextTick(() => settingsTrigger.value?.focus())
}
function onSettingsClose() {
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
function removeMapping(index: number) { if (draftConnection.value.mappings.length > 1) { draftConnection.value.mappings.splice(index, 1); editMapping.value = 0 } }
function scrollToServers() { document.getElementById('server-settings')?.scrollIntoView({ behavior: 'smooth' }) }
async function chooseLocalDirectory() {
 if (busy.value) return
 choosingDirectory.value = true
 try { await run(async () => { const selected = await backend().SelectLocalDirectory(draft.localRoot); if (selected) draft.localRoot = selected }) }
 finally { choosingDirectory.value = false }
}
async function saveProject() {
 await run(async () => {
  const payload = copy(draft)
  for (const connection of payload.connections) connection.excludes = (connection.excludes ?? []).filter(Boolean)
  if (!payload.defaultConnectionID && payload.connections.length) payload.defaultConnectionID = payload.connections[0].id
  status.value = await backend().SaveProject(payload)
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
  const api = window.go?.main.App
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
 const prompt = authPrompt.value, api = window.go?.main.App
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
async function selectProject(id: string) {
 const selected = projects.value.find(p => p.id === id); if (!selected) return
 await run(async () => {
  navigationRevision++; setNavigationDisconnected(id, '')
  status.value = await backend().SelectProject(id, '', '')
  replaceProject(project, projects.value.find(p => p.id === id) ?? selected)
  refreshDraftFromProject(); resetTrees(); nameRemoteRoot(); await loadLocalRoot()
  await syncNavigationStatus()
 })
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
 selectionAnchors[side] = selectTreeEntry(selections[side], trees[side], file, event, selectionAnchors[side])
 preview.value = undefined
}
function openTransferMenu(side: 'local' | 'remote', event: MouseEvent, file?: FileEntry) {
 if (busy.value || queueRunning.value || file?.excluded || file?.isLink) return
 if (file && !selections[side].has(file.relativePath)) {
  selections[side].clear(); selections[side].add(file.relativePath); selectionAnchors[side] = file.relativePath
 }
 if (!selections[side].size) return
 preview.value = undefined
 transferMenu.value = { side, x: event.clientX, y: event.clientY }
}
function projectSelectionPaths(side: 'local' | 'remote'): string[] {
 const paths = [...selections[side]]
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
async function transferSelection(side: 'local' | 'remote', direction: 'upload' | 'download', connectionID = quickConnection.value?.id) {
 transferMenu.value = undefined
 if (busy.value || queueRunning.value || !selections[side].size) return
 let paths: string[]
 try { paths = projectSelectionPaths(side) } catch (e) { error.value = explainError(e); return }
 if (!paths.length) return
 const connection = savedProject.value?.connections.find(c => c.id === connectionID)
 if (!connection) { error.value = 'Добавьте сервер в настройки проекта.'; return }
 if (!connection.mappings.length) { error.value = 'Добавьте маппинг к серверу, чтобы передавать файлы.'; return }
 const candidates = connection.mappings.filter(mapping => paths.every(path => { try { toMappingPath(path, mapping.localPath); return true } catch { return false } }))
  .sort((left, right) => right.localPath.split(/[\\/]/).filter(Boolean).length - left.localPath.split(/[\\/]/).filter(Boolean).length)
 const mapping = candidates[0]
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
onUnmounted(() => { queuePollingEnabled = false; authPollingEnabled = false; authPollRevision++; authAnswer.value = ''; authRemember.value = false; if (queuePollTimer) clearTimeout(queuePollTimer); if (authPollTimer) clearTimeout(authPollTimer) })
</script>

<template>
 <main class="app-shell">
  <header class="topbar">
   <div class="brand-mark">⇄</div><strong class="brand">SFTP проекты</strong>
   <label class="header-project">Проект <select data-testid="project-select" :value="project.id" :disabled="busy || !projects.length" @change="selectProject(($event.target as HTMLSelectElement).value)"><option v-for="item in projects" :key="item.id" :value="item.id">{{ item.name }}</option></select></label>
   <button data-testid="create-project" class="quiet" :disabled="busy" @click="startNewProject">＋ Новый проект</button>
   <span class="top-spacer"></span><button data-testid="queue-toggle" type="button" class="quiet" :aria-expanded="queuePanelOpen" @click="queuePanelOpen = !queuePanelOpen">⇅ Очередь<span v-if="queueCount" class="queue-count" :title="`${queueCount} выполняется или ожидает`">{{ queueCount }}</span><span v-if="queueProblems" class="queue-count queue-count-error">{{ queueProblems }}</span></button><button ref="settingsTrigger" data-testid="settings-open" class="quiet" :disabled="busy || !project.id" @click="openSettings">⚙ Настройки</button>
   <button v-if="busy && !settingsOpen && !wizard" class="cancel-button" @click="cancel">Отменить операцию</button>
  </header>
  <div v-if="error && !settingsOpen && !wizard && !preview" class="toast error" role="alert">{{ error }}<button aria-label="Закрыть ошибку" @click="error = ''">×</button></div>
  <div v-if="notice" class="toast" role="status">{{ notice }}<button aria-label="Закрыть сообщение" @click="notice = ''">×</button></div>
  <section v-if="status && project.id" class="workspace">
   <section class="pane local-pane" data-testid="local-pane">
    <header class="pane-header"><div><h1>Проект</h1><div class="root-path" :title="project.localRoot">{{ project.localRoot }}</div></div><button class="icon-button" title="Обновить дерево проекта" aria-label="Обновить дерево проекта" :disabled="busy" @click="refreshLocal">⟳</button></header>
    <div class="tree-toolbar"><span>{{ selections.local.size }} выбрано</span><span class="toolbar-spacer"></span><button class="link-button" :disabled="busy || !selections.local.size" @click="clearSelection('local')">Снять выбор</button><button class="primary small" :disabled="busy || queueRunning || !selections.local.size || !savedConnection" @click="transferSelection('local', 'upload')">Загрузить на {{ quickConnection?.name ?? 'сервер' }}</button><button v-if="(savedProject?.connections.length ?? 0) > 1" class="quiet small transfer-server-button" :disabled="busy || queueRunning || !selections.local.size" @click="openTransferMenu('local', $event)">Загрузить на… ▸</button></div>
    <div class="tree-scroll"><p v-if="trees.local.loading && !trees.local.loaded" class="empty-state">Загрузка файлов проекта…</p><p v-else-if="trees.local.error && !trees.local.children.length" class="empty-state" role="alert">{{ trees.local.error }} <button @click="refreshLocal">Повторить</button></p><ul class="file-tree tree-root"><FileTree :node="trees.local" :busy="busy" side="local" :selected="selections.local" @toggle="treeAction('local', $event)" @refresh="treeAction('local', $event, true)" @select="(file, event) => selectFile('local', file, event)" @context="(file, event) => openTransferMenu('local', event, file)" /></ul></div>
   </section>
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
    <div v-if="savedConnection" class="tree-toolbar"><span>{{ savedConnection.name }} · {{ selections.remote.size }} выбрано</span><span class="toolbar-spacer"></span><button class="link-button" :disabled="busy || !selections.remote.size" @click="clearSelection('remote')">Снять выбор</button><button class="primary small" :disabled="busy || queueRunning || !selections.remote.size || !trees.remote.loaded" @click="transferSelection('remote', 'download')">Скачать с {{ quickConnection?.name }}</button><button v-if="(savedProject?.connections.length ?? 0) > 1" class="quiet small transfer-server-button" :disabled="busy || queueRunning || !selections.remote.size" @click="openTransferMenu('remote', $event)">Скачать с… ▸</button></div>
    <div class="tree-scroll">
     <div v-if="!savedConnection" class="empty-server"><div class="empty-icon">⌁</div><h2>Сервер не настроен</h2><p>Добавьте SSH- или SFTP-сервер, чтобы просматривать и передавать файлы проекта.</p><button data-testid="add-server" class="primary" :disabled="busy" @click="openSettings">＋ Добавить сервер</button></div>
     <template v-else><div v-if="!trees.remote.loaded && !trees.remote.loading" class="empty-server disconnected-state"><div class="empty-icon">⌁</div><h2>Сервер не подключён</h2><p>Подключитесь, чтобы открыть <strong>{{ savedConnection.rootPath }}</strong>.</p></div><p v-if="trees.remote.error" class="empty-state" role="alert">{{ trees.remote.error }} <button :disabled="busy || queueRunning" @click="connectRemote">Повторить</button></p><ul class="file-tree tree-root"><FileTree :node="trees.remote" :busy="busy || queueRunning" side="remote" :selected="selections.remote" @toggle="treeAction('remote', $event)" @refresh="treeAction('remote', $event, true)" @select="(file, event) => selectFile('remote', file, event)" @context="(file, event) => openTransferMenu('remote', event, file)" /></ul></template>
    </div>
   </section>
  </section>
  <TransferMenu v-if="transferMenu" :x="transferMenu.x" :y="transferMenu.y" :connections="savedProject?.connections ?? []" :current-id="quickConnection?.id" @close="transferMenu = undefined" @transfer="(direction, id) => transferSelection(transferMenu!.side, direction, id)" />
  <TransferQueue v-if="queuePanelOpen" data-testid="queue-panel" :queue="queue" :error="queueError" :busy="queueBusy" :app-busy="busy" @pause="pauseQueue" @cancel="cancelQueueJob" @clear="clearFinishedQueue" @retry="retryQueueJob" />

  <dialog ref="settingsDialog" data-testid="settings-dialog" class="modal settings-modal" :class="{ 'wizard-modal': wizard }" @close="onSettingsClose" @cancel="cancelSettings">
   <form v-if="wizard" class="modal-content" @submit.prevent="saveProject"><p v-if="error" role="alert" class="error">{{ error }}</p><header class="modal-header"><div><span class="eyebrow">ПЕРВЫЙ ЗАПУСК</span><h2>Создать проект</h2></div></header><p class="modal-intro">Укажите название и локальную папку. Серверы можно добавить позже.</p><label>Название проекта<input v-model="draft.name" data-testid="project-name" maxlength="120" required autofocus placeholder="Моё приложение"></label><label>Папка проекта<div class="directory-picker"><input v-model="draft.localRoot" data-testid="project-folder" required placeholder="C:\Projects\app"><button type="button" data-testid="project-folder-picker" class="quiet" :disabled="busy" @click="chooseLocalDirectory">Обзор…</button></div></label><footer class="modal-actions"><button v-if="busy" type="button" class="cancel-button" @click="cancel">Отменить операцию</button><button class="primary" data-testid="project-save" :disabled="busy">Создать проект</button></footer></form>
   <form v-else class="modal-content settings-content" @submit.prevent="saveProject"><p v-if="error" role="alert" class="error">{{ error }}</p><header class="modal-header"><div><span class="eyebrow">НАСТРОЙКИ ПРОЕКТА</span><h2>{{ draft.name || 'Новый проект' }}</h2></div><button type="button" class="icon-button" aria-label="Закрыть настройки" :disabled="busy" @click="closeSettings()">×</button></header><div class="settings-layout"><nav class="settings-nav"><button type="button" class="nav-active">Общие</button><button type="button" @click="scrollToServers">Серверы и маппинги</button></nav><div class="settings-fields" id="server-settings"><section class="settings-section"><h3>Проект</h3><label>Название проекта<input v-model="draft.name" maxlength="120" required></label><label>Локальная папка проекта<div class="directory-picker"><input v-model="draft.localRoot" required><button type="button" class="quiet" :disabled="busy" @click="chooseLocalDirectory">Обзор…</button></div></label></section>
    <section class="settings-section"><div class="section-title"><div><h3>Серверы</h3><p>Подключение выполняется только вручную.</p></div><button type="button" data-testid="settings-add-server" class="quiet" :disabled="busy" @click="addConnection">＋ Добавить сервер</button></div><p v-if="!draft.connections.length" class="settings-empty">Серверов пока нет. Добавьте SSH- или SFTP-подключение, когда оно понадобится.</p>
     <div v-else class="server-editor"><aside class="server-list"><button v-for="(connection, index) in draft.connections" :key="connection.id" type="button" :class="{ chosen: editConnection === index }" @click="editConnection = index; editMapping = 0"><span>{{ connection.name || 'Без имени' }}</span><small>{{ connection.connection.mode === 'ssh_alias' ? (connection.connection.alias || 'SSH-алиас') : (connection.connection.host || 'Ручная настройка') }}</small></button></aside>
      <div class="server-fields"><div class="section-title"><h4>Подключение</h4><button type="button" class="link-button danger-link" :disabled="busy" @click="removeConnection">Удалить</button></div><label>Название сервера<input v-model="draftConnection.name" data-testid="server-name" required maxlength="120"></label><label class="check-row"><input type="radio" data-testid="default-server-select" name="default-server" :checked="draft.defaultConnectionID === draftConnection.id" :disabled="busy" @change="draft.defaultConnectionID = draftConnection.id">Использовать по умолчанию</label><label>Тип подключения<select v-model="draftConnection.connection.mode" :disabled="busy"><option value="ssh_alias">SSH-алиас</option><option value="manual">Вручную (SSH/SFTP)</option></select></label>
       <template v-if="draftConnection.connection.mode === 'ssh_alias'"><label>SSH-алиас<input v-model="draftConnection.connection.alias" data-testid="server-alias" required placeholder="work-dev"></label><label>Путь к SSH-конфигу <span class="optional">(необязательно)</span><input v-model="draftConnection.connection.configPath" placeholder="Основной SSH-конфиг"></label><div class="inline-actions"><button type="button" class="quiet" :disabled="busy" @click="discoverAliases">Импортировать алиасы…</button><select v-if="discovery?.aliases.length" v-model="importedAlias"><option value="">Выберите алиас</option><option v-for="alias in discovery.aliases" :key="alias.name" :value="alias.name">{{ alias.name }}</option></select><button v-if="discovery?.aliases.length" type="button" class="quiet" :disabled="busy" @click="importAlias">Использовать</button></div><p v-if="discovery?.warnings.length" class="hint">{{ discovery.warnings.join(' · ') }}</p></template>
       <template v-else><label>Хост<input v-model="draftConnection.connection.host" required></label><div class="two-fields"><label>Пользователь<input v-model="draftConnection.connection.user" required></label><label>Порт<input v-model.number="draftConnection.connection.port" type="number" min="1" max="65535" required></label></div><label>Приватный ключ <span class="optional">(необязательно, иначе SSH-agent)</span><input v-model="draftConnection.connection.identityFile"></label></template>
       <label>Способ аутентификации<select v-model="draftConnection.connection.authMode" data-testid="auth-mode" :disabled="busy"><option value="key_agent">Ключ или SSH-agent</option><option value="password">Пароль при подключении</option></select></label><p v-if="draftConnection.connection.authMode === 'password'" class="hint">Пароль будет запрошен во время подключения и не сохраняется в настройках.</p><button v-if="canForgetSavedPassword" type="button" data-testid="forget-password" class="quiet" :disabled="busy" @click="forgetSavedPassword">Забыть сохранённый пароль</button>
       <label>Файл known_hosts <span class="optional">(необязательно)</span><input v-model="draftConnection.connection.knownHostsFile"></label><div v-if="draftConnection.connection.mode === 'ssh_alias'" class="inline-actions"><label class="check-row"><input v-model="trustConfig" type="checkbox" :disabled="busy || queueRunning">Доверять внешнему SSH-конфигу</label><button type="button" class="quiet" :disabled="busy || queueRunning || !trustConfig || !draftConnection.connection.alias" @click="evaluateSSH">Проверить SSH</button></div><p v-if="effective" class="hint">{{ effective.hostname }} · {{ effective.user }} · порт {{ effective.port }}<span v-if="effective.proxyJump"> · через {{ effective.proxyJump }}</span></p><p v-if="effective?.hasProxyCommand" class="hint warning">В этом подключении используется ProxyCommand.</p>
       <label>Корень на сервере<input v-model="draftConnection.rootPath" data-testid="remote-root" placeholder="/var/www/html" required></label>
       <div class="settings-section mappings-section"><div class="section-title"><div><h4>Маппинги папок</h4><p>Локальная папка проекта связывается с путём относительно корня сервера.</p></div><button type="button" data-testid="mapping-add" class="quiet" :disabled="busy" @click="addMapping">＋ Добавить маппинг</button></div><p v-if="!draftConnection.mappings.length" class="hint">Маппингов пока нет. Добавьте их вручную, чтобы передавать файлы.</p><div v-for="(mapping, index) in draftConnection.mappings" :key="mapping.id" class="mapping-row"><label>Локальная папка<input v-model="mapping.localPath" data-testid="mapping-local-path" placeholder=". или src" required></label><span>→</span><label>Путь развёртывания от корня<input v-model="mapping.deploymentPath" data-testid="deployment-path" placeholder="." required></label><button v-if="draftConnection.mappings.length > 1" type="button" class="icon-button" aria-label="Удалить маппинг" :disabled="busy" @click="removeMapping(index)">×</button></div></div>
       <details class="advanced-settings"><summary>Дополнительно · исключения передачи</summary><label>Один шаблон на строку<textarea v-model="excludesText" rows="5" placeholder=".git/**&#10;**/node_modules/**&#10;**/.env" :disabled="busy"></textarea></label><p class="hint">Пути отсчитываются от корня проекта. Исключения применяются при загрузке и скачивании. Файлы окружения защищены от передачи.</p><p class="hint">Ключи сервера проверяются строго; настройте ProxyJump в доверенном SSH-конфиге.</p></details>
      </div>
     </div>
    </section>
    <details class="advanced-settings diagnostics"><summary>Диагностика</summary><dl><dt>Платформа</dt><dd>{{ status?.platform }}</dd><dt>OpenSSH</dt><dd>{{ status?.sshPath || 'Не найден' }}</dd><dt>Состояние SSH</dt><dd>{{ status?.sshMessage }}</dd><dt>Папка настроек</dt><dd>{{ status?.dataDir }}</dd><dt>WebView2</dt><dd>{{ status?.webviewVersion || status?.webviewMessage }}</dd></dl></details>
    </div></div><footer class="modal-actions"><button v-if="busy" type="button" class="cancel-button" @click="cancel">Отменить операцию</button><button type="button" class="quiet" :disabled="busy" @click="closeSettings()">Отмена</button><button class="primary" data-testid="settings-save" :disabled="busy">Сохранить настройки</button></footer></form>
  </dialog>

  <dialog ref="previewDialog" class="modal preview-modal" @cancel="cancelPreview"><div v-if="preview" class="modal-content"><p v-if="error" role="alert" class="error">{{ error }}</p><header class="modal-header"><div><span class="eyebrow">ПЛАН ПЕРЕДАЧИ</span><h2>{{ preview.direction === 'upload' ? 'Загрузить на сервер' : 'Скачать в проект' }}</h2></div><button class="icon-button" :disabled="busy || queueBusy" aria-label="Закрыть просмотр" @click="closePreview">×</button></header><p class="modal-intro">{{ preview.server }} · {{ preview.fileCount }} файлов · {{ preview.totalBytes }} байт · {{ previewConflictCount }} существующих файлов</p><div class="transfer-table"><table><thead><tr><th>Источник</th><th>Назначение</th><th>Действие</th></tr></thead><tbody><tr v-for="item in preview.items" :key="item.relativePath"><td>{{ item.source || item.relativePath }}</td><td>{{ item.destination }}</td><td>{{ transferAction(item) }}</td></tr></tbody></table></div><label class="check-row"><input v-model="overwrite" type="checkbox" :disabled="queueBusy || queueRunning">Разрешить замену существующих файлов</label><label v-if="previewEnvironmentCount" class="check-row"><input v-model="includeEnvironment" type="checkbox" :disabled="queueBusy || queueRunning">Явно разрешить передачу {{ previewEnvironmentCount }} файлов .env</label><p class="hint">Передача может завершиться частично. Уже переданные файлы и созданные папки сохраняются при ошибке или отмене.</p><footer class="modal-actions"><button class="quiet" :disabled="queueBusy" @click="closePreview">Отмена</button><button data-testid="queue-enqueue" class="primary" :disabled="queueBusy || queueRunning || !previewActionCount || (!!previewEnvironmentCount && !includeEnvironment)" @click="executeBatch">Добавить в очередь</button></footer></div></dialog>
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
