<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref, watch } from 'vue'
import { backend, type FileContent, type FileComparison, type FileSearchResult, type Project } from '../bridge'
import { recentBranches, rememberBranch } from '../git-recent'
import type { GitBranch } from '../bridge'
import { compareLines } from '../file-diff'
import { comparisonKey, putComparisonTab, type WorkspaceTab } from '../workspace-tabs'
import { formatBytes } from '../format-bytes'

const props = defineProps<{ project: Project; busy: boolean; remoteBusy: boolean }>()
const emit = defineEmits<{ activeFileChange: [file: FileContent | undefined]; openLocal: [relative: string]; refresh: [side: 'local' | 'remote', relative: string, connectionID: string] }>()
const tabs = ref<WorkspaceTab[]>([]), activeKey = ref('')
const activeTab = computed(() => tabs.value.find(t => t.key === activeKey.value))
const active = computed(() => activeTab.value?.kind === 'file' ? activeTab.value.file : undefined)
watch(active, file => emit('activeFileChange', file))
const lines = computed(() => (active.value?.text ?? '').replace(/\r\n/g, '\n').split('\n'))
const visibleLines = computed(() => lines.value.slice(0, 10000))
const comparison = computed(() => activeTab.value?.kind === 'comparison' ? activeTab.value.comparison : undefined)
const lineDiff = computed(() => comparison.value?.comparable ? compareLines(comparison.value.local.text, comparison.value.remote.text) : { rows: [], simplified: false, limited: false })
const diffRows = computed(() => lineDiff.value.rows.slice(0, 10000))
const searchDialog = ref<HTMLDialogElement>(), searchInput = ref<HTMLInputElement>()
const query = ref(''), search = ref<FileSearchResult>({ files: [], limited: false }), selected = ref(0), searching = ref(false), searchError = ref('')
let generation = 0, searchRevision = 0, timer: ReturnType<typeof setTimeout> | undefined
let searchTrigger: HTMLElement | undefined, compareTrigger: HTMLElement | undefined

function tabTitle(tab: WorkspaceTab) {
 return tab.kind === 'comparison' ? `${tab.comparison.local.name} [${tab.comparison.remote.server}]` : title(tab.file)
}
function tabPath(tab: WorkspaceTab) { return tab.kind === 'comparison' ? `${tab.comparison.local.path} / ${tab.comparison.remote.path}` : tab.file.path }
async function showComparison(key: string, result: FileComparison) {
 putComparisonTab(tabs.value, key, result); activeKey.value = key
 await nextTick(); document.querySelector<HTMLButtonElement>('.file-tab[aria-selected="true"]')?.focus()
}
function title(file: FileContent) { return file.server ? `${file.name} [${file.server}]` : file.name }
async function openFile(side: 'local' | 'remote', relative: string, connectionID = '') {
 const version = generation, projectID = props.project.id
 const key = `${projectID}:${side}:${connectionID}:${relative}`
 const existing = tabs.value.find(t => t.key === key)
 if (existing) { activeKey.value = key; return }
 const file = side === 'local' ? await backend().ReadLocalFile(projectID, relative) : await backend().ReadRemoteFile(projectID, connectionID, relative)
 if (version !== generation) return
 tabs.value.push({ kind: 'file', key, file }); activeKey.value = key
}
async function refreshFile(side: 'local' | 'remote', relative: string, connectionID: string) {
 const version = generation, key = activeKey.value
 const file = side === 'local' ? await backend().ReadLocalFile(props.project.id, relative) : await backend().ReadRemoteFile(props.project.id, connectionID, relative)
 if (version !== generation) return
 const tab = tabs.value.find(t => t.key === key); if (tab?.kind === 'file') tab.file = file
}
function closeTab(key: string) {
 const index = tabs.value.findIndex(t => t.key === key)
 if (index < 0) return
 tabs.value.splice(index, 1)
 if (activeKey.value === key) activeKey.value = tabs.value[Math.min(index, tabs.value.length - 1)]?.key ?? ''
}
function tabKey(event: KeyboardEvent, index: number) {
 if (event.key === 'Delete') { event.preventDefault(); closeTab(tabs.value[index]!.key); return }
 if (event.key !== 'ArrowRight' && event.key !== 'ArrowLeft') return
 event.preventDefault()
 const next = (index + (event.key === 'ArrowRight' ? 1 : -1) + tabs.value.length) % tabs.value.length
 activeKey.value = tabs.value[next]!.key
 document.querySelectorAll<HTMLButtonElement>('.file-tab')[next]?.focus()
}
async function compareFile(relative: string, connectionID: string) {
 const version = generation
 compareTrigger = document.activeElement as HTMLElement
 const result = await backend().CompareFile(props.project.id, connectionID, relative)
 if (version !== generation) return
 await showComparison(comparisonKey(props.project.id, relative, 'ssh', connectionID), result)
}
const branchDialog = ref<HTMLDialogElement>(), branchInput = ref<HTMLInputElement>()
const branches = ref<GitBranch[]>([]), branchQuery = ref(''), branchError = ref(''), branchLoading = ref(false), branchBusy = ref(false), branchPath = ref('')
const filteredBranches = computed(() => branches.value.filter(b => b.name.toLowerCase().includes(branchQuery.value.toLowerCase())))
let branchRevision = 0
function closeBranches() { branchRevision++; branchDialog.value?.close(); compareTrigger?.isConnected && compareTrigger.focus() }
async function openGitCompare(relative: string, ref?: string) {
 const revision = ++branchRevision, version = generation
 compareTrigger = document.activeElement as HTMLElement
 branchPath.value = relative; branchQuery.value = ''; branchError.value = ''; branches.value = []; branchLoading.value = true; branchBusy.value = false
 branchDialog.value?.showModal(); await nextTick(); branchInput.value?.focus()
 try {
  const result = await backend().GetGitBranches(props.project.id)
  if (revision !== branchRevision || version !== generation) return
  const recent = recentBranches(props.project.localRoot)
  branches.value = result.sort((a, b) => { const ai = recent.indexOf(a.ref), bi = recent.indexOf(b.ref); return (ai < 0 ? 99 : ai) - (bi < 0 ? 99 : bi) })
  if (ref) {
   if (result.some(b => b.ref === ref)) await chooseBranch(ref)
   else branchError.value = 'Недавняя ветка больше не существует. Выберите другую.'
  }
 } catch (e) { if (revision === branchRevision) branchError.value = String(e) }
 finally { if (revision === branchRevision) branchLoading.value = false }
}
async function chooseBranch(ref: string) {
 if (branchBusy.value) return
 const version = generation, revision = branchRevision, root = props.project.localRoot
 branchBusy.value = true; branchError.value = ''
 try {
  const result = await backend().CompareGitFile(props.project.id, branchPath.value, ref)
  if (version !== generation || revision !== branchRevision) return
  rememberBranch(root, ref); branchDialog.value?.close()
  await showComparison(comparisonKey(props.project.id, branchPath.value, 'git', ref), result)
 } catch (e) { if (revision === branchRevision) branchError.value = String(e) }
 finally { if (revision === branchRevision) branchBusy.value = false }
}
async function openSearch() {
 if (!props.project.id || document.querySelector('dialog[open]')) return
 searchTrigger = document.activeElement as HTMLElement
 query.value = ''; search.value = { files: [], limited: false }; searchError.value = ''; selected.value = 0
 searchDialog.value?.showModal(); await nextTick(); searchInput.value?.focus(); void runSearch()
}
function closeSearch() {
 searchRevision++; if (timer) clearTimeout(timer); searching.value = false
 searchDialog.value?.close(); searchTrigger?.focus()
}
async function runSearch() {
 const version = ++searchRevision, projectID = props.project.id, value = query.value
 searching.value = true; searchError.value = ''
 search.value = { files: [], limited: false }
 try {
  const result = await backend().SearchProjectFiles(projectID, value)
  if (version !== searchRevision || !searchDialog.value?.open) return
  search.value = result; selected.value = 0
 } catch (e) { if (version === searchRevision) searchError.value = String(e) }
 finally { if (version === searchRevision) searching.value = false }
}
function chooseSearch() {
 const file = search.value.files[selected.value]
 if (!file || props.busy || searching.value) return
 closeSearch(); emit('openLocal', file.relativePath)
}
function searchKey(event: KeyboardEvent) {
 if (event.key === 'Enter') { event.preventDefault(); chooseSearch() }
 if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
  event.preventDefault(); selected.value = Math.max(0, Math.min(search.value.files.length - 1, selected.value + (event.key === 'ArrowDown' ? 1 : -1)))
  nextTick(() => document.querySelector(`[data-search-index="${selected.value}"]`)?.scrollIntoView({ block: 'nearest' }))
 }
}
function globalKey(event: KeyboardEvent) {
 if (event.ctrlKey && event.shiftKey && event.code === 'KeyN') { event.preventDefault(); void openSearch() }
}
watch(query, () => { searchRevision++; searching.value = true; if (timer) clearTimeout(timer); timer = setTimeout(runSearch, 180) })
watch(() => [props.project.id, props.project.localRoot], () => {
 generation++; tabs.value = []; activeKey.value = ''; closeSearch(); closeBranches()
})
onMounted(() => window.addEventListener('keydown', globalKey))
onUnmounted(() => { generation++; searchRevision++; if (timer) clearTimeout(timer); window.removeEventListener('keydown', globalKey) })
defineExpose({ openFile, refreshFile, compareFile, openSearch, openGitCompare })
</script>

<template>
 <section class="pane editor-pane" data-testid="file-workspace">
  <div class="editor-tabs">
   <div class="tabs-list" role="tablist" aria-label="Открытые файлы">
   <div v-for="(tab, index) in tabs" :key="tab.key" class="tab-wrapper" :class="{ active: activeKey === tab.key }">
    <button class="file-tab" role="tab" :aria-selected="activeKey === tab.key" :tabindex="activeKey === tab.key ? 0 : -1" :title="tabPath(tab)" @click="activeKey = tab.key" @keydown="tabKey($event, index)">{{ tabTitle(tab) }}</button>
    <button class="tab-close" :aria-label="`Закрыть ${tabTitle(tab)}`" @click="closeTab(tab.key)">×</button>
   </div>
   </div><button class="icon-button" data-testid="find-file" title="Найти файл (Ctrl+Shift+N)" aria-label="Найти файл" @click="openSearch">⌕</button>
  </div>
  <section v-if="comparison" class="comparison-tab" data-testid="comparison-tab" role="region" aria-label="Сравнение файлов">
    <header class="compare-heading"><div><strong>{{ comparison.local.name }} — {{ comparison.remote.server }}</strong><p>{{ comparison.equal ? 'Файлы идентичны' : comparison.comparable ? 'Файлы отличаются' : 'Текстовое сравнение недоступно: бинарный файл, кодировка или лимит размера' }}</p></div><button class="icon-button" aria-label="Закрыть сравнение" @click="closeTab(activeKey)">×</button></header>
    <div class="compare-paths"><span :title="comparison.local.path">Локально · {{ comparison.local.path }}</span><span :title="comparison.remote.path">{{ comparison.remote.server }} · {{ comparison.remote.path }}</span></div>
    <p v-if="lineDiff.simplified || lineDiff.limited || lineDiff.rows.length > 10000" class="viewer-warning">Большие различия показаны блоком; отображается до 10 000 строк.</p>
    <p v-if="comparison.comparable && !comparison.equal && !lineDiff.limited && !lineDiff.rows.some(row => row.changed)" class="viewer-warning">Текст строк совпадает; отличаются кодировка, BOM или окончания строк.</p>
    <div v-if="comparison.comparable" class="diff-scroll" tabindex="0" aria-label="Различия файлов" data-testid="file-diff">
     <div v-for="(row, index) in diffRows" :key="index" class="diff-row"><div class="diff-cell" :class="{ 'diff-removed': row.changed && row.left !== undefined }"><span class="line-number">{{ row.leftNumber }}</span><code>{{ row.left ?? ' ' }}</code></div><div class="diff-cell" :class="{ 'diff-added': row.changed && row.right !== undefined }"><span class="line-number">{{ row.rightNumber }}</span><code>{{ row.right ?? ' ' }}</code></div></div>
    </div>
    <footer class="compare-footer"><span class="diff-legend">Красный — локальная версия · Зелёный — {{ comparison.remote.server.startsWith('Git ·') ? 'версия в Git' : 'серверная версия' }}</span><button class="quiet" @click="closeTab(activeKey)">Закрыть</button></footer>

  </section>
  <template v-else-if="active">
   <header class="editor-file-info"><span :title="active.path">{{ active.server ? `${active.server} · ` : '' }}{{ active.path }}</span><button class="icon-button" data-testid="refresh-file" :disabled="busy || (!!active.connectionID && remoteBusy)" title="Прочитать файл заново" aria-label="Обновить файл" @click="emit('refresh', active.connectionID ? 'remote' : 'local', active.relativePath, active.connectionID)">⟳</button></header>
   <p v-if="active.binary" class="empty-state">Бинарный файл или неизвестная кодировка. Текстовый предпросмотр недоступен.</p>
   <template v-else>
    <p v-if="active.truncated || lines.length > 10000" class="viewer-warning">Показана только часть файла (лимит 2 МиБ / 10 000 строк).</p>
    <div class="code-viewer" role="region" aria-label="Содержимое файла" tabindex="0" data-testid="file-content"><div v-for="(line, index) in visibleLines" :key="index" class="code-line"><span class="line-number" aria-hidden="true">{{ index + 1 }}</span><code>{{ line || ' ' }}</code></div></div>
   </template>
   <footer class="editor-status">Только чтение <span>{{ active.encoding || 'Бинарный' }} · {{ formatBytes(active.size) }}</span></footer>
  </template>
  <div v-else class="editor-empty"><span class="empty-icon">≡</span><h2>Предпросмотр файла</h2><p>Откройте файл двойным кликом в дереве.</p><button class="quiet" @click="openSearch">Найти файл <kbd>Ctrl+Shift+N</kbd></button></div>
 </section>
 <Teleport to="body">
  <dialog ref="branchDialog" class="modal file-search-modal" data-testid="git-branch-dialog" aria-label="Выбор ветки Git" @cancel.prevent="closeBranches">
   <header class="search-heading"><strong>Сравнить с веткой Git</strong><button class="icon-button" aria-label="Закрыть выбор ветки" @click="closeBranches">×</button></header>
   <p class="search-message">{{ branchPath }} · Локальные ветки и ветки всех remote</p>
   <input ref="branchInput" v-model="branchQuery" aria-label="Поиск веток" placeholder="Поиск по веткам…" @keydown.enter.prevent="!branchLoading && filteredBranches[0] && chooseBranch(filteredBranches[0].ref)">
   <p v-if="branchError" class="error search-message" role="alert">{{ branchError }}</p>
   <p v-if="branchLoading || branchBusy" class="search-message" role="status">{{ branchBusy ? 'Сравнение…' : 'Чтение веток…' }}</p>
   <p v-else-if="!filteredBranches.length" class="search-message">Ветки не найдены</p>
   <div class="search-results"><button v-for="branch in filteredBranches" :key="branch.ref" class="search-result" :disabled="branchBusy || branchLoading" @click="chooseBranch(branch.ref)"><strong>{{ branch.name }}</strong><span>{{ branch.ref.startsWith('refs/heads/') ? 'Локальная ветка' : 'Remote' }}</span></button></div>
   <footer class="search-footer"><span>Без переключения ветки · Esc закрыть</span><button class="quiet" @click="closeBranches">Закрыть</button></footer>
  </dialog>
  <dialog ref="searchDialog" class="modal file-search-modal" data-testid="file-search-dialog" aria-label="Найти файл" @cancel.prevent="closeSearch">
   <header class="search-heading"><label for="file-search-input">Найти файл в проекте</label><button class="icon-button" aria-label="Закрыть поиск" @click="closeSearch">×</button></header>
   <input id="file-search-input" ref="searchInput" v-model="query" data-testid="file-search-input" placeholder="Имя файла…" maxlength="120" autocomplete="off" aria-controls="file-search-results" :aria-activedescendant="search.files.length ? `file-result-${selected}` : undefined" role="combobox" aria-expanded="true" @keydown="searchKey">
   <p v-if="searchError" role="alert" class="error search-message">{{ searchError }}</p><p v-else-if="searching" role="status" class="search-message">Поиск…</p><p v-else-if="!search.files.length" class="search-message">Файлы не найдены</p>
   <div id="file-search-results" class="search-results" role="listbox" aria-label="Найденные файлы"><button v-for="(file, index) in search.files" :id="`file-result-${index}`" :key="file.relativePath" :data-search-index="index" class="search-result" :class="{ chosen: index === selected }" role="option" :aria-selected="index === selected" :disabled="busy || searching" @click="selected = index" @dblclick="selected = index; chooseSearch()" @keydown.enter.prevent="selected = index; chooseSearch()"><strong>{{ file.name }}</strong><span>{{ file.relativePath }}</span></button></div>
   <footer class="search-footer"><span>{{ search.limited ? 'Результат ограничен: уточните имя файла' : `${search.files.length} файлов` }} · ↑↓ выбрать · Enter открыть · Esc закрыть</span><button class="primary small" :disabled="busy || searching || !search.files.length" @click="chooseSearch">Открыть</button></footer>
  </dialog>
 </Teleport>
</template>
