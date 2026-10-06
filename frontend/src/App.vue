<script setup lang="ts">
import { computed, nextTick, onMounted, reactive, ref, watch } from 'vue'
import { backend, type BatchPreview, type BatchResult, type FileEntry, type Project, type ProjectConnection, type SSHDiscovery, type SSHEffective, type Status } from './bridge'
import FileTree from './components/FileTree.vue'
import { loadDirectory, selectEntry, toggleDirectory, toMappingPath, treeRoot, type TreeNode } from './file-tree'

function emptyProject(): Project { return { id: '', name: '', localRoot: '', connections: [] } }
function emptyConnection(): ProjectConnection {
 return { id: crypto.randomUUID(), name: 'Новый сервер', connection: { mode: 'ssh_alias', alias: '', configPath: '', host: '', user: '', port: 22, identityFile: '', knownHostsFile: '' }, mappings: [{ id: crypto.randomUUID(), localPath: '.', remotePath: '/srv/app' }], excludes: ['.git/**', '**/node_modules/**', '**/.env', '**/.env.*'] }
}
const status = ref<Status>(), project = reactive<Project>(emptyProject()), draft = reactive<Project>(emptyProject())
const error = ref(''), notice = ref(''), busy = ref(false), choosingDirectory = ref(false)
const preview = ref<BatchPreview>(), batchResult = ref<{ plan: BatchPreview; result: BatchResult }>()
const overwrite = ref(false), includeEnvironment = ref(false)
const wizard = ref(false), settingsOpen = ref(false), editConnection = ref(0), editMapping = ref(0)
const settingsDialog = ref<HTMLDialogElement>(), previewDialog = ref<HTMLDialogElement>(), settingsTrigger = ref<HTMLElement>()
const trees = reactive({ local: treeRoot(), remote: treeRoot() })
const selections = reactive({ local: new Set<string>(), remote: new Set<string>() })
const discovery = ref<SSHDiscovery>(), importedAlias = ref(''), effective = ref<SSHEffective>(), trustConfig = ref(false)
const resultLabels = { completed: 'Готово', skipped: 'Пропущено', failed: 'Ошибка', cancelled: 'Отменено' }
const projects = computed(() => status.value?.settings.projects ?? [])
const savedProject = computed(() => projects.value.find(p => p.id === status.value?.settings.activeProjectID))
const savedConnection = computed(() => savedProject.value?.connections.find(c => c.id === status.value?.settings.activeConnectionID))
const activeMapping = computed(() => savedConnection.value?.mappings.find(m => m.id === status.value?.settings.activeMappingID) ?? savedConnection.value?.mappings[0])
const draftConnection = computed(() => draft.connections[editConnection.value]!)
const resultCounts = computed(() => { const count = { completed: 0, skipped: 0, failed: 0, cancelled: 0 }; for (const item of batchResult.value?.result.items ?? []) count[item.status]++; return count })
const resultDestinations = computed(() => new Map(batchResult.value?.plan.items.map(item => [item.relativePath, item.destination]) ?? []))
const previewActionCount = computed(() => preview.value?.items.filter(item => !item.skippedReason).length ?? 0)
const previewConflictCount = computed(() => preview.value?.items.filter(item => item.kind === 'file' && item.exists && !item.skippedReason).length ?? 0)
const previewEnvironmentCount = computed(() => preview.value?.items.filter(item => { const name = item.relativePath.split('/').at(-1)?.toLowerCase() ?? ''; return item.kind === 'file' && !item.skippedReason && (name === '.env' || name.startsWith('.env.')) }).length ?? 0)
const excludesText = computed({ get: () => (draftConnection.value?.excludes ?? []).join('\n'), set: (value: string) => { if (draftConnection.value) draftConnection.value.excludes = value.replace(/\r\n/g, '\n').split('\n') } })

function copy<T>(value: T): T { return JSON.parse(JSON.stringify(value)) }
function replaceProject(target: Project, value: Project) { Object.assign(target, copy(value)); target.defaultConnectionID = value.defaultConnectionID }
function resetTrees() { trees.local = treeRoot(); trees.remote = treeRoot(); selections.local.clear(); selections.remote.clear(); preview.value = undefined }
function nameRemoteRoot() { const path = activeMapping.value?.remotePath.replace(/\/+$/, ''); trees.remote.entry.name = path?.split('/').at(-1) || savedConnection.value?.name || 'Сервер' }
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
function addMapping() { draftConnection.value.mappings.push({ id: crypto.randomUUID(), localPath: '', remotePath: '' }); editMapping.value = draftConnection.value.mappings.length - 1 }
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
  replaceProject(project, projects.value.find(p => p.id === status.value?.settings.activeProjectID) ?? payload)
  resetTrees(); nameRemoteRoot(); closeSettings(false); await loadLocalRoot()
  notice.value = 'Проект сохранён.'
 })
}
async function selectProject(id: string) {
 const selected = projects.value.find(p => p.id === id); if (!selected) return
 await run(async () => {
  status.value = await backend().SelectProject(id, '', '')
  replaceProject(project, projects.value.find(p => p.id === id) ?? selected)
  refreshDraftFromProject(); resetTrees(); nameRemoteRoot(); await loadLocalRoot()
 })
}
async function selectConnection(id: string) {
 const connection = savedProject.value?.connections.find(c => c.id === id), mapping = connection?.mappings[0]
 if (savedProject.value && connection && mapping) await selectProfile(savedProject.value.id, connection.id, mapping.id)
}
async function selectMapping(id: string) {
 const connection = savedConnection.value
 if (connection && savedProject.value && connection.mappings.some(m => m.id === id)) await selectProfile(savedProject.value.id, connection.id, id)
}
async function selectProfile(projectID: string, connectionID: string, mappingID: string) {
 await run(async () => { status.value = await backend().SelectProject(projectID, connectionID, mappingID); trees.remote = treeRoot(); nameRemoteRoot(); selections.remote.clear(); selections.local.clear(); closePreview(); await loadLocalRoot() })
}
async function refreshLocal() {
 if (!project.id) return
 await run(async () => { selections.local.clear(); closePreview(); await loadDirectory(trees.local, path => backend().ListProjectLocal(path)) })
}
async function connectRemote() {
 if (!savedConnection.value) return
 trees.remote.entry.name = activeMapping.value?.remotePath.split('/').filter(Boolean).at(-1) || savedConnection.value.name
 await run(async () => { selections.remote.clear(); closePreview(); await loadDirectory(trees.remote, path => backend().ListRemote(path)) })
}
async function treeAction(side: 'local' | 'remote', node: TreeNode, refresh = false) {
 await run(async () => {
  const loader = (path: string) => side === 'local' ? backend().ListProjectLocal(path) : backend().ListRemote(path)
  if (refresh) { selections[side].clear(); preview.value = undefined; await loadDirectory(node, loader) }
  else await toggleDirectory(node, loader)
 })
}
function selectFile(side: 'local' | 'remote', file: FileEntry, checked: boolean) { selectEntry(selections[side], file, checked); preview.value = undefined }
function clearSelection(side: 'local' | 'remote') { selections[side].clear(); preview.value = undefined }
function transferAction(item: BatchPreview['items'][number]) { return item.skippedReason ? `Пропустить: ${item.skippedReason}` : item.kind === 'directory' ? (item.exists ? 'Папка существует' : 'Создать папку') : item.exists ? (overwrite.value ? 'Заменить' : 'Пропустить существующий') : 'Передать' }
async function prepareBatch(direction: 'upload' | 'download', paths: string[]) {
 if (busy.value || !paths.length) return
 let transferPaths = paths
 if (direction === 'upload') {
  const mappingPath = activeMapping.value?.localPath
  if (!mappingPath) { error.value = 'Выберите сервер и маппинг для загрузки.'; return }
  try { transferPaths = paths.map(path => toMappingPath(path, mappingPath)) } catch (e) { error.value = String(e); return }
 }
 await run(async () => {
  preview.value = undefined; overwrite.value = false; includeEnvironment.value = false
  preview.value = await backend().PreviewBatch(direction, transferPaths)
  await nextTick(); previewDialog.value?.showModal()
 })
}
async function prepareSingle(side: 'local' | 'remote', file: FileEntry) { await prepareBatch(side === 'local' ? 'upload' : 'download', [file.relativePath]) }
function closePreview() { previewDialog.value?.close(); preview.value = undefined }
function cancelPreview(event: Event) { if (busy.value) { event.preventDefault(); return }; preview.value = undefined }
async function executeBatch() {
 const plan = preview.value; if (!plan) return
 await run(async () => {
  previewDialog.value?.close(); preview.value = undefined
  let result: BatchResult
  try { result = await backend().ExecuteBatch(plan.token, overwrite.value ? 'overwrite' : 'skip', includeEnvironment.value) }
  catch (e) { throw e }
  batchResult.value = { plan, result }
  selections[plan.direction === 'upload' ? 'local' : 'remote'].clear()
  notice.value = 'Передача завершена. Результаты доступны в нижней панели.'
 })
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
 if (!trustConfig.value || draftConnection.value.connection.mode !== 'ssh_alias' || !draftConnection.value.connection.alias || busy.value) return
 effective.value = undefined
 await run(async () => { effective.value = await backend().EvaluateSSH({ ...draftConnection.value.connection }, trustConfig.value) })
}
async function cancel() { try { await backend().CancelOperation() } catch (e) { error.value = String(e) } }

watch(() => [draftConnection.value?.id, draftConnection.value?.connection.mode, draftConnection.value?.connection.configPath, draftConnection.value?.connection.alias], () => {
 discovery.value = undefined; importedAlias.value = ''; effective.value = undefined; trustConfig.value = false
})

onMounted(async () => {
 await run(async () => {
  status.value = await backend().GetStatus()
  const active = savedProject.value
  if (!active) { await beginProject(); return }
  replaceProject(project, active); refreshDraftFromProject(); nameRemoteRoot(); await loadLocalRoot()
 })
})
</script>

<template>
 <main class="app-shell">
  <header class="topbar">
   <div class="brand-mark">⇄</div><strong class="brand">SFTP проекты</strong>
   <label class="header-project">Проект <select data-testid="project-select" :value="project.id" :disabled="busy || !projects.length" @change="selectProject(($event.target as HTMLSelectElement).value)"><option v-for="item in projects" :key="item.id" :value="item.id">{{ item.name }}</option></select></label>
   <button data-testid="create-project" class="quiet" :disabled="busy" @click="startNewProject">＋ Новый проект</button>
   <span class="top-spacer"></span><button ref="settingsTrigger" data-testid="settings-open" class="quiet" :disabled="busy || !project.id" @click="openSettings">⚙ Настройки</button>
   <button v-if="busy && !settingsOpen && !wizard" class="cancel-button" @click="cancel">Отменить операцию</button>
  </header>
  <div v-if="error && !settingsOpen && !wizard && !preview" class="toast error" role="alert">{{ error }}<button aria-label="Закрыть ошибку" @click="error = ''">×</button></div>
  <div v-if="notice" class="toast" role="status">{{ notice }}<button aria-label="Закрыть сообщение" @click="notice = ''">×</button></div>
  <section v-if="status && project.id" class="workspace">
   <section class="pane local-pane" data-testid="local-pane">
    <header class="pane-header"><div><h1>Проект</h1><div class="root-path" :title="project.localRoot">{{ project.localRoot }}</div></div><button class="icon-button" title="Обновить дерево проекта" aria-label="Обновить дерево проекта" :disabled="busy" @click="refreshLocal">⟳</button></header>
    <div class="tree-toolbar"><span>{{ selections.local.size }} выбрано</span><span class="toolbar-spacer"></span><button class="link-button" :disabled="busy || !selections.local.size" @click="clearSelection('local')">Снять выбор</button><button class="primary small" :disabled="busy || !selections.local.size || !savedConnection" @click="prepareBatch('upload', [...selections.local])">Загрузить</button></div>
    <div class="tree-scroll"><p v-if="trees.local.loading && !trees.local.loaded" class="empty-state">Загрузка файлов проекта…</p><p v-else-if="trees.local.error && !trees.local.children.length" class="empty-state" role="alert">{{ trees.local.error }} <button @click="refreshLocal">Повторить</button></p><ul class="file-tree tree-root"><FileTree :node="trees.local" :busy="busy" side="local" :selected="selections.local" @toggle="treeAction('local', $event)" @refresh="treeAction('local', $event, true)" @select="(file, checked) => selectFile('local', file, checked)" /></ul></div>
   </section>
   <section class="pane remote-pane" data-testid="remote-pane">
    <header class="pane-header remote-header"><div><h1>Сервер</h1><div class="root-path" :title="activeMapping?.remotePath || 'Сервер не выбран'">{{ activeMapping?.remotePath || 'Сервер не выбран' }}</div></div>
     <label class="header-select">Сервер <select data-testid="server-select" :value="status.settings.activeConnectionID ?? ''" :disabled="busy || !savedProject?.connections.length" @change="selectConnection(($event.target as HTMLSelectElement).value)"><option v-for="connection in savedProject?.connections" :key="connection.id" :value="connection.id">{{ connection.name }}</option></select></label>
     <label class="header-select mapping-select">Маппинг <select :value="status.settings.activeMappingID ?? ''" :disabled="busy || !savedConnection?.mappings.length" @change="selectMapping(($event.target as HTMLSelectElement).value)"><option v-for="mapping in savedConnection?.mappings" :key="mapping.id" :value="mapping.id">{{ mapping.localPath }} → {{ mapping.remotePath }}</option></select></label>
     <button class="quiet compact" :disabled="busy || !project.id" @click="openSettings" aria-label="Настройки серверов">⚙</button><button v-if="savedConnection" data-testid="connect-server" class="primary small" :disabled="busy" @click="connectRemote">Подключиться / обновить</button>
    </header>
    <div v-if="savedConnection" class="tree-toolbar"><span>{{ savedConnection.name }} · {{ selections.remote.size }} выбрано</span><span class="toolbar-spacer"></span><button class="link-button" :disabled="busy || !selections.remote.size" @click="clearSelection('remote')">Снять выбор</button><button class="primary small" :disabled="busy || !selections.remote.size || !trees.remote.loaded" @click="prepareBatch('download', [...selections.remote])">Скачать</button></div>
    <div class="tree-scroll">
     <div v-if="!savedConnection" class="empty-server"><div class="empty-icon">⌁</div><h2>Сервер не настроен</h2><p>Добавьте SSH- или SFTP-сервер, чтобы просматривать и передавать файлы проекта.</p><button data-testid="add-server" class="primary" :disabled="busy" @click="openSettings">＋ Добавить сервер</button></div>
     <template v-else><div v-if="!trees.remote.loaded && !trees.remote.loading" class="empty-server disconnected-state"><div class="empty-icon">⌁</div><h2>Сервер не подключён</h2><p>Подключитесь, чтобы открыть <strong>{{ activeMapping?.remotePath }}</strong>.</p></div><p v-if="trees.remote.error" class="empty-state" role="alert">{{ trees.remote.error }} <button @click="connectRemote">Повторить</button></p><ul class="file-tree tree-root"><FileTree :node="trees.remote" :busy="busy" side="remote" :selected="selections.remote" @toggle="treeAction('remote', $event)" @refresh="treeAction('remote', $event, true)" @select="(file, checked) => selectFile('remote', file, checked)" /></ul></template>
    </div>
   </section>
  </section>
  <details v-if="batchResult" class="results-panel" open><summary>Результаты передачи <span class="result-summary">{{ resultCounts.completed }} готово · {{ resultCounts.skipped }} пропущено · {{ resultCounts.failed }} ошибок · {{ resultCounts.cancelled }} отменено</span><button class="link-button" @click.prevent="batchResult = undefined">Закрыть</button></summary><div class="result-table"><table><thead><tr><th>Путь</th><th>Назначение</th><th>Тип</th><th>Результат</th><th>Подробности</th></tr></thead><tbody><tr v-for="item in batchResult.result.items" :key="item.relativePath"><td>{{ item.relativePath }}</td><td>{{ resultDestinations.get(item.relativePath) }}</td><td>{{ item.kind === 'directory' ? 'Папка' : 'Файл' }}</td><td>{{ resultLabels[item.status] }}</td><td>{{ item.error }}</td></tr></tbody></table></div></details>

  <dialog ref="settingsDialog" data-testid="settings-dialog" class="modal settings-modal" :class="{ 'wizard-modal': wizard }" @close="onSettingsClose" @cancel="cancelSettings">
   <form v-if="wizard" class="modal-content" @submit.prevent="saveProject"><p v-if="error" role="alert" class="error">{{ error }}</p><header class="modal-header"><div><span class="eyebrow">ПЕРВЫЙ ЗАПУСК</span><h2>Создать проект</h2></div></header><p class="modal-intro">Укажите название и локальную папку. Серверы можно добавить позже.</p><label>Название проекта<input v-model="draft.name" data-testid="project-name" maxlength="120" required autofocus placeholder="Моё приложение"></label><label>Папка проекта<div class="directory-picker"><input v-model="draft.localRoot" data-testid="project-folder" required placeholder="C:\Projects\app"><button type="button" data-testid="project-folder-picker" class="quiet" :disabled="busy" @click="chooseLocalDirectory">Обзор…</button></div></label><footer class="modal-actions"><button v-if="busy" type="button" class="cancel-button" @click="cancel">Отменить операцию</button><button class="primary" data-testid="project-save" :disabled="busy">Создать проект</button></footer></form>
   <form v-else class="modal-content settings-content" @submit.prevent="saveProject"><p v-if="error" role="alert" class="error">{{ error }}</p><header class="modal-header"><div><span class="eyebrow">НАСТРОЙКИ ПРОЕКТА</span><h2>{{ draft.name || 'Новый проект' }}</h2></div><button type="button" class="icon-button" aria-label="Закрыть настройки" :disabled="busy" @click="closeSettings()">×</button></header><div class="settings-layout"><nav class="settings-nav"><button type="button" class="nav-active">Общие</button><button type="button" @click="scrollToServers">Серверы и маппинги</button></nav><div class="settings-fields" id="server-settings"><section class="settings-section"><h3>Проект</h3><label>Название проекта<input v-model="draft.name" maxlength="120" required></label><label>Локальная папка проекта<div class="directory-picker"><input v-model="draft.localRoot" required><button type="button" class="quiet" :disabled="busy" @click="chooseLocalDirectory">Обзор…</button></div></label></section>
    <section class="settings-section"><div class="section-title"><div><h3>Серверы</h3><p>Подключение выполняется только вручную.</p></div><button type="button" data-testid="settings-add-server" class="quiet" :disabled="busy" @click="addConnection">＋ Добавить сервер</button></div><p v-if="!draft.connections.length" class="settings-empty">Серверов пока нет. Добавьте SSH- или SFTP-подключение, когда оно понадобится.</p>
     <div v-else class="server-editor"><aside class="server-list"><button v-for="(connection, index) in draft.connections" :key="connection.id" type="button" :class="{ chosen: editConnection === index }" @click="editConnection = index; editMapping = 0"><span>{{ connection.name || 'Без имени' }}</span><small>{{ connection.connection.mode === 'ssh_alias' ? (connection.connection.alias || 'SSH-алиас') : (connection.connection.host || 'Ручная настройка') }}</small></button></aside>
      <div class="server-fields"><div class="section-title"><h4>Подключение</h4><button type="button" class="link-button danger-link" :disabled="busy" @click="removeConnection">Удалить</button></div><label>Название сервера<input v-model="draftConnection.name" data-testid="server-name" required maxlength="120"></label><label class="check-row"><input type="radio" data-testid="default-server-select" name="default-server" :checked="draft.defaultConnectionID === draftConnection.id" :disabled="busy" @change="draft.defaultConnectionID = draftConnection.id">Использовать по умолчанию</label><label>Тип подключения<select v-model="draftConnection.connection.mode" :disabled="busy"><option value="ssh_alias">SSH-алиас</option><option value="manual">Вручную (SSH/SFTP)</option></select></label>
       <template v-if="draftConnection.connection.mode === 'ssh_alias'"><label>SSH-алиас<input v-model="draftConnection.connection.alias" data-testid="server-alias" required placeholder="work-dev"></label><label>Путь к SSH-конфигу <span class="optional">(необязательно)</span><input v-model="draftConnection.connection.configPath" placeholder="Основной SSH-конфиг"></label><div class="inline-actions"><button type="button" class="quiet" :disabled="busy" @click="discoverAliases">Импортировать алиасы…</button><select v-if="discovery?.aliases.length" v-model="importedAlias"><option value="">Выберите алиас</option><option v-for="alias in discovery.aliases" :key="alias.name" :value="alias.name">{{ alias.name }}</option></select><button v-if="discovery?.aliases.length" type="button" class="quiet" :disabled="busy" @click="importAlias">Использовать</button></div><p v-if="discovery?.warnings.length" class="hint">{{ discovery.warnings.join(' · ') }}</p></template>
       <template v-else><label>Хост<input v-model="draftConnection.connection.host" required></label><div class="two-fields"><label>Пользователь<input v-model="draftConnection.connection.user" required></label><label>Порт<input v-model.number="draftConnection.connection.port" type="number" min="1" max="65535" required></label></div><label>Приватный ключ <span class="optional">(необязательно, иначе SSH-agent)</span><input v-model="draftConnection.connection.identityFile"></label></template>
       <label>Файл known_hosts <span class="optional">(необязательно)</span><input v-model="draftConnection.connection.knownHostsFile"></label><div v-if="draftConnection.connection.mode === 'ssh_alias'" class="inline-actions"><label class="check-row"><input v-model="trustConfig" type="checkbox" :disabled="busy">Доверять внешнему SSH-конфигу</label><button type="button" class="quiet" :disabled="busy || !trustConfig || !draftConnection.connection.alias" @click="evaluateSSH">Проверить SSH</button></div><p v-if="effective" class="hint">{{ effective.hostname }} · {{ effective.user }} · порт {{ effective.port }}<span v-if="effective.proxyJump"> · через {{ effective.proxyJump }}</span></p><p v-if="effective?.hasProxyCommand" class="hint warning">В этом подключении используется ProxyCommand.</p>
       <div class="settings-section mappings-section"><div class="section-title"><div><h4>Маппинги папок</h4><p>Локальные пути отсчитываются от папки проекта.</p></div><button type="button" class="quiet" :disabled="busy" @click="addMapping">＋ Добавить маппинг</button></div><div v-for="(mapping, index) in draftConnection.mappings" :key="mapping.id" class="mapping-row"><label>Локальная папка<input v-model="mapping.localPath" placeholder=". или src" required></label><span>→</span><label>Папка на сервере<input v-model="mapping.remotePath" data-testid="remote-root" placeholder="/srv/app" required></label><button v-if="draftConnection.mappings.length > 1" type="button" class="icon-button" aria-label="Удалить маппинг" :disabled="busy" @click="removeMapping(index)">×</button></div></div>
       <details class="advanced-settings"><summary>Дополнительно · исключения передачи</summary><label>Один шаблон на строку<textarea v-model="excludesText" rows="5" placeholder=".git/**&#10;**/node_modules/**&#10;**/.env" :disabled="busy"></textarea></label><p class="hint">Пути отсчитываются от корня проекта. Исключения применяются при загрузке и скачивании. Файлы окружения защищены от передачи.</p><p class="hint">Пароли SSH и MFA не поддерживаются. Ключи сервера проверяются строго; настройте ProxyJump в доверенном SSH-конфиге.</p></details>
      </div>
     </div>
    </section>
    <details class="advanced-settings diagnostics"><summary>Диагностика</summary><dl><dt>Платформа</dt><dd>{{ status?.platform }}</dd><dt>OpenSSH</dt><dd>{{ status?.sshPath || 'Не найден' }}</dd><dt>Состояние SSH</dt><dd>{{ status?.sshMessage }}</dd><dt>Папка настроек</dt><dd>{{ status?.dataDir }}</dd><dt>WebView2</dt><dd>{{ status?.webviewVersion || status?.webviewMessage }}</dd></dl></details>
    </div></div><footer class="modal-actions"><button v-if="busy" type="button" class="cancel-button" @click="cancel">Отменить операцию</button><button type="button" class="quiet" :disabled="busy" @click="closeSettings()">Отмена</button><button class="primary" data-testid="settings-save" :disabled="busy">Сохранить настройки</button></footer></form>
  </dialog>

  <dialog ref="previewDialog" class="modal preview-modal" @cancel="cancelPreview" @close="cancelPreview"><div v-if="preview" class="modal-content"><p v-if="error" role="alert" class="error">{{ error }}</p><header class="modal-header"><div><span class="eyebrow">ПЛАН ПЕРЕДАЧИ</span><h2>{{ preview.direction === 'upload' ? 'Загрузить на сервер' : 'Скачать в проект' }}</h2></div><button class="icon-button" :disabled="busy" aria-label="Закрыть просмотр" @click="closePreview">×</button></header><p class="modal-intro">{{ preview.server }} · {{ preview.fileCount }} файлов · {{ preview.totalBytes }} байт · {{ previewConflictCount }} существующих файлов</p><div class="transfer-table"><table><thead><tr><th>Источник</th><th>Назначение</th><th>Действие</th></tr></thead><tbody><tr v-for="item in preview.items" :key="item.relativePath"><td>{{ item.source || item.relativePath }}</td><td>{{ item.destination }}</td><td>{{ transferAction(item) }}</td></tr></tbody></table></div><label class="check-row"><input v-model="overwrite" type="checkbox" :disabled="busy">Разрешить замену существующих файлов</label><label v-if="previewEnvironmentCount" class="check-row"><input v-model="includeEnvironment" type="checkbox" :disabled="busy">Явно разрешить передачу {{ previewEnvironmentCount }} файлов .env</label><p class="hint">Передача может завершиться частично. Уже переданные файлы и созданные папки сохраняются при ошибке или отмене.</p><footer class="modal-actions"><button v-if="busy" type="button" class="cancel-button" @click="cancel">Отменить операцию</button><button class="quiet" :disabled="busy" @click="closePreview">Отмена</button><button class="primary" :disabled="busy || !previewActionCount || (!!previewEnvironmentCount && !includeEnvironment)" @click="executeBatch">Начать передачу</button></footer></div></dialog>
 </main>
</template>
