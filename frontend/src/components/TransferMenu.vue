<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import type { ProjectConnection } from '../bridge'
const props = defineProps<{ x: number; y: number; connections: ProjectConnection[]; currentId?: string; canCopy?: boolean; canTransfer?: boolean; copyConnectionId?: string; canCompare?: boolean; canRefreshDirectory?: boolean; canExcludeDirectory?: boolean; searchExcluded?: boolean }>()
const emit = defineEmits<{ copyPath: [kind: 'local' | 'project' | 'server', connectionID?: string]; close: []; refreshDirectory: []; transfer: [direction: 'upload' | 'download', connectionID: string]; compare: [connectionID: string]; excludeDirectory: [excluded: boolean] }>()
const menu = ref<HTMLElement>(), submenu = ref<'upload' | 'download' | 'compare' | 'copy'>()
const left = ref(0), top = ref(0)
const current = computed(() => props.connections.find(c => c.id === props.currentId))
const copyServer = computed(() => props.connections.find(c => c.id === props.copyConnectionId) ?? current.value)
function choose(direction: 'upload' | 'download', id?: string) { if (id && props.canTransfer !== false) emit('transfer', direction, id) }
function outside(event: Event) {
 // A closed settings dialog can deliver a queued scroll event after its
 // layout changes. It must not dismiss a newly opened tree context menu.
 if (event.type === 'scroll' && (event.target as Element)?.closest?.('dialog:not([open])')) return
 if (!menu.value?.contains(event.target as Node)) emit('close')
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
  <div ref="menu" data-testid="transfer-menu" class="transfer-menu" role="menu" aria-label="Действия с выделенными файлами" :style="{ left: `${left}px`, top: `${top}px`, maxHeight: `calc(100vh - ${top + 8}px)` }" @keydown="key" @contextmenu.prevent>
   <template v-if="canRefreshDirectory">
    <button role="menuitem" data-testid="menu-refresh-directory" @click="emit('refreshDirectory')">Обновить папку</button>
    <div class="menu-divider"></div>
   </template>
   <template v-if="canExcludeDirectory">
    <button role="menuitem" data-testid="menu-search-exclude" @click="emit('excludeDirectory', !searchExcluded)">{{ searchExcluded ? 'Unmark directory excluded' : 'Mark directory excluded' }}</button>
    <div class="menu-divider"></div>
   </template>
   <button role="menuitem" data-testid="menu-upload" :disabled="!current || canTransfer === false" @click="choose('upload', current?.id)">Загрузить на {{ current?.name ?? 'сервер' }}</button>
   <button role="menuitem" data-testid="menu-download" :disabled="!current || canTransfer === false" @click="choose('download', current?.id)">Скачать с {{ current?.name ?? 'сервера' }}</button>
   <template v-if="canCopy">
    <div class="menu-divider"></div>
    <button role="menuitem" data-testid="menu-copy-local" @click="emit('copyPath', 'local')">Копировать полный локальный путь</button>
    <button role="menuitem" data-testid="menu-copy-project" @click="emit('copyPath', 'project')">Копировать путь относительно проекта</button>
    <button role="menuitem" data-testid="menu-copy-server" :disabled="!copyServer" @click="copyServer && emit('copyPath', 'server', copyServer.id)">Копировать путь на {{ copyServer?.name ?? 'сервере' }}</button>
    <template v-if="connections.length > 1">
     <button role="menuitem" data-testid="menu-copy-servers" aria-haspopup="menu" :aria-expanded="submenu === 'copy'" @mouseenter="submenu = 'copy'" @click="submenu = submenu === 'copy' ? undefined : 'copy'">Копировать путь на сервере… <span>▸</span></button>
     <div v-if="submenu === 'copy'" class="server-submenu" role="menu" aria-label="Сервер для копирования пути"><button v-for="connection in connections" :key="connection.id" role="menuitem" :data-copy-server-id="connection.id" @click="emit('copyPath', 'server', connection.id)">{{ connection.name }}<span v-if="connection.id === copyServer?.id">✓</span></button></div>
    </template>
   </template>
   <template v-if="canCompare">
    <div class="menu-divider"></div>
    <button role="menuitem" data-testid="menu-compare-default" :disabled="!current" @click="current && emit('compare', current.id)">Сравнить с сервером {{ current?.name ?? 'по умолчанию' }}</button>
    <template v-if="connections.length > 1">
     <button role="menuitem" data-testid="menu-compare-servers" aria-haspopup="menu" :aria-expanded="submenu === 'compare'" @click="submenu = submenu === 'compare' ? undefined : 'compare'" @mouseenter="submenu = 'compare'">Сравнить с <span aria-hidden="true">▸</span></button>
     <div v-if="submenu === 'compare'" role="menu" class="server-submenu" aria-label="Сравнить с сервером"><button v-for="connection in connections" :key="connection.id" role="menuitem" :data-compare-server-id="connection.id" @click="emit('compare', connection.id)">{{ connection.name }}<span v-if="connection.id === currentId" aria-label="Сервер по умолчанию">✓</span></button></div>
    </template>
   </template>
   <template v-if="connections.length > 1">
    <div class="menu-divider"></div>
    <template v-for="direction in (['upload', 'download'] as const)" :key="direction">
     <button role="menuitem" :disabled="canTransfer === false" :data-testid="`menu-${direction}-servers`" aria-haspopup="menu" :aria-expanded="submenu === direction" @click="submenu = submenu === direction ? undefined : direction" @mouseenter="submenu = direction">{{ direction === 'upload' ? 'Загрузить на' : 'Скачать с' }} <span>▸</span></button>
     <div v-if="submenu === direction" role="menu" class="server-submenu" :aria-label="direction === 'upload' ? 'Загрузить на сервер' : 'Скачать с сервера'">
      <button v-for="connection in connections" :key="connection.id" role="menuitem" :disabled="canTransfer === false" :data-server-id="connection.id" :data-direction="direction" @click="choose(direction, connection.id)">{{ connection.name }}<span v-if="connection.id === currentId">✓</span></button>
     </div>
    </template>
   </template>
   <p v-if="!connections.length" class="menu-hint">Добавьте сервер в настройках проекта</p>
  </div>
 </Teleport>
</template>
