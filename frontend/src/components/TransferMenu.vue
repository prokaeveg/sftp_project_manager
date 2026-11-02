<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import ContextSubmenu from './ContextSubmenu.vue'
import type { ProjectConnection } from '../bridge'
const props = defineProps<{ x: number; y: number; connections: ProjectConnection[]; actionConnections: ProjectConnection[]; copyConnections: ProjectConnection[]; currentId?: string; canCopy?: boolean; canTransfer?: boolean; copyConnectionId?: string; canCompare?: boolean; canCompareGit?: boolean; recentGit?: string[]; canRefreshDirectory?: boolean; canExcludeDirectory?: boolean; searchExcluded?: boolean }>()
const emit = defineEmits<{ copyPath: [kind: 'local' | 'project' | 'server', connectionID?: string]; close: []; refreshDirectory: []; transfer: [direction: 'upload' | 'download', connectionID: string]; compare: [connectionID: string]; compareGit: [ref?: string]; excludeDirectory: [excluded: boolean] }>()
const menu = ref<HTMLElement>(), submenu = ref<'upload' | 'download' | 'compare' | 'copy' | 'git'>()
const left = ref(0), top = ref(0)
const current = computed(() => props.actionConnections.find(c => c.id === props.currentId) ?? props.actionConnections[0])
const copyServer = computed(() => props.copyConnections.find(c => c.id === props.copyConnectionId) ?? props.copyConnections.find(c => c.id === props.currentId) ?? props.copyConnections[0])
function choose(direction: 'upload' | 'download', id?: string) { if (id && props.actionConnections.some(c => c.id === id) && props.canTransfer !== false) emit('transfer', direction, id) }
function outside(event: Event) {
 // A closed settings dialog can deliver a queued scroll event after its
 // layout changes. It must not dismiss a newly opened tree context menu.
 if (event.type === 'scroll' && (event.target as Element)?.closest?.('dialog:not([open])')) return
 if (!menu.value?.contains(event.target as Node) && !(event.target as Element)?.closest?.('[data-context-submenu]')) emit('close')
}
function hoverMenu(event: MouseEvent) {
 if (!(event.target as Element).closest('.context-submenu')) submenu.value = undefined
}
function key(event: KeyboardEvent) {
 if (event.key === 'Escape') { event.preventDefault(); emit('close'); return }
 if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
 event.preventDefault()
 const buttons = Array.from(menu.value!.querySelectorAll<HTMLButtonElement>('button:not(:disabled)'))
 const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
 buttons[(index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length]?.focus()
}
onMounted(async () => {
 left.value = Math.max(8, Math.min(props.x, window.innerWidth - 290))
 top.value = Math.max(8, Math.min(props.y, window.innerHeight - 280))
 await nextTick(); menu.value?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus({ preventScroll: true })
 document.addEventListener('pointerdown', outside)
 window.addEventListener('resize', close); document.addEventListener('scroll', outside, true)
})
function close() { emit('close') }
onUnmounted(() => { document.removeEventListener('pointerdown', outside); window.removeEventListener('resize', close); document.removeEventListener('scroll', outside, true) })
</script>
<template>
 <Teleport to="body">
  <div ref="menu" data-testid="transfer-menu" class="transfer-menu" role="menu" aria-label="Действия с выделенными файлами" :style="{ left: `${left}px`, top: `${top}px`, maxHeight: `calc(100vh - ${top + 8}px)` }" @keydown="key" @mouseover="hoverMenu" @scroll="submenu = undefined" @contextmenu.prevent>
   <template v-if="canRefreshDirectory">
    <button role="menuitem" data-testid="menu-refresh-directory" @click="emit('refreshDirectory')">Обновить папку</button>
    <div class="menu-divider"></div>
   </template>
   <template v-if="canExcludeDirectory">
    <button role="menuitem" data-testid="menu-search-exclude" @click="emit('excludeDirectory', !searchExcluded)">{{ searchExcluded ? 'Unmark directory excluded' : 'Mark directory excluded' }}</button>
    <div class="menu-divider"></div>
   </template>
   <button role="menuitem" v-if="current" data-testid="menu-upload" :disabled="!current || canTransfer === false" @click="choose('upload', current?.id)">Загрузить на {{ current?.name ?? 'сервер' }}</button>
   <button role="menuitem" v-if="current" data-testid="menu-download" :disabled="!current || canTransfer === false" @click="choose('download', current?.id)">Скачать с {{ current?.name ?? 'сервера' }}</button>
   <template v-if="canCopy">
    <div class="menu-divider"></div>
    <button role="menuitem" data-testid="menu-copy-local" @click="emit('copyPath', 'local')">Копировать полный локальный путь</button>
    <button role="menuitem" data-testid="menu-copy-project" @click="emit('copyPath', 'project')">Копировать путь относительно проекта</button>
    <button role="menuitem" v-if="copyServer" data-testid="menu-copy-server" :disabled="!copyServer" @click="copyServer && emit('copyPath', 'server', copyServer.id)">Копировать путь на {{ copyServer?.name ?? 'сервере' }}</button>
    <template v-if="copyConnections.length">
     <ContextSubmenu label="Копировать путь на сервере…" test-id="menu-copy-servers" :open="submenu === 'copy'" @open="submenu = 'copy'" @close="submenu === 'copy' && (submenu = undefined)" @dismiss="close"><button v-for="connection in copyConnections" :key="connection.id" role="menuitem" :data-copy-server-id="connection.id" @click="emit('copyPath', 'server', connection.id)">{{ connection.name }}<span v-if="connection.id === copyServer?.id">✓</span></button></ContextSubmenu>
    </template>
   </template>
   <template v-if="canCompareGit">
    <div class="menu-divider"></div>
    <ContextSubmenu label="Сравнить с Git" test-id="menu-compare-git" :open="submenu === 'git'" @open="submenu = 'git'" @close="submenu === 'git' && (submenu = undefined)" @dismiss="close">
     <button v-for="ref in recentGit" :key="ref" role="menuitem" @click="emit('compareGit', ref)">{{ ref.replace(/^refs\/(heads|remotes)\//, '') }}</button>
     <button role="menuitem" data-testid="menu-select-git-branch" @click="emit('compareGit')">Выбрать ветку…</button>
    </ContextSubmenu>
   </template>
   <template v-if="canCompare && actionConnections.length">
    <div class="menu-divider"></div>
    <button role="menuitem" data-testid="menu-compare-default" :disabled="!current" @click="current && emit('compare', current.id)">Сравнить с сервером {{ current?.name ?? 'по умолчанию' }}</button>
    <template v-if="actionConnections.length">
     <ContextSubmenu label="Сравнить с" test-id="menu-compare-servers" :open="submenu === 'compare'" @open="submenu = 'compare'" @close="submenu === 'compare' && (submenu = undefined)" @dismiss="close"><button v-for="connection in actionConnections" :key="connection.id" role="menuitem" :data-compare-server-id="connection.id" @click="emit('compare', connection.id)">{{ connection.name }}<span v-if="connection.id === currentId" aria-label="Сервер по умолчанию">✓</span></button></ContextSubmenu>
    </template>
   </template>
   <template v-if="actionConnections.length">
    <div class="menu-divider"></div>
    <template v-for="direction in (['upload', 'download'] as const)" :key="direction">
     <ContextSubmenu :label="direction === 'upload' ? '\u0417\u0430\u0433\u0440\u0443\u0437\u0438\u0442\u044c \u043d\u0430' : '\u0421\u043a\u0430\u0447\u0430\u0442\u044c \u0441'" :test-id="`menu-${direction}-servers`" :open="submenu === direction" :disabled="canTransfer === false" @open="submenu = direction" @close="submenu === direction && (submenu = undefined)" @dismiss="close">
      <button v-for="connection in actionConnections" :key="connection.id" role="menuitem" :disabled="canTransfer === false" :data-server-id="connection.id" :data-direction="direction" @click="choose(direction, connection.id)">{{ connection.name }}<span v-if="connection.id === currentId">✓</span></button>
     </ContextSubmenu>
    </template>
   </template>
   <p v-if="connections.length && !actionConnections.length" class="menu-hint" data-testid="no-mapped-servers">Нет серверов с маппингом для выбранных путей.</p>
   <p v-if="!connections.length" class="menu-hint">Добавьте сервер в настройках проекта</p>
  </div>
 </Teleport>
</template>
