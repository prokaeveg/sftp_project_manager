<script setup lang="ts">
import { computed, nextTick, onMounted, onUnmounted, ref } from 'vue'
import type { ProjectConnection } from '../bridge'
const props = defineProps<{ x: number; y: number; connections: ProjectConnection[]; currentId?: string; canCompare?: boolean }>()
const emit = defineEmits<{ close: []; transfer: [direction: 'upload' | 'download', connectionID: string]; compare: [connectionID: string] }>()
const menu = ref<HTMLElement>(), submenu = ref<'upload' | 'download' | 'compare'>()
const left = ref(0), top = ref(0)
const current = computed(() => props.connections.find(c => c.id === props.currentId))
function choose(direction: 'upload' | 'download', id?: string) { if (id) emit('transfer', direction, id) }
function outside(event: Event) { if (!menu.value?.contains(event.target as Node)) emit('close') }
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
 await nextTick(); menu.value?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()
 document.addEventListener('pointerdown', outside)
 window.addEventListener('resize', close); document.addEventListener('scroll', outside, true)
})
function close() { emit('close') }
onUnmounted(() => { document.removeEventListener('pointerdown', outside); window.removeEventListener('resize', close); document.removeEventListener('scroll', outside, true) })
</script>
<template>
 <Teleport to="body">
  <div ref="menu" data-testid="transfer-menu" class="transfer-menu" role="menu" aria-label="Передача выделенных файлов" :style="{ left: `${left}px`, top: `${top}px`, maxHeight: `calc(100vh - ${top + 8}px)` }" @keydown="key" @contextmenu.prevent>
   <button role="menuitem" data-testid="menu-upload" :disabled="!current" @click="choose('upload', current?.id)">Загрузить на {{ current?.name ?? 'сервер' }}</button>
   <button role="menuitem" data-testid="menu-download" :disabled="!current" @click="choose('download', current?.id)">Скачать с {{ current?.name ?? 'сервера' }}</button>
   <template v-if="canCompare">
    <div class="menu-divider"></div>
    <button role="menuitem" data-testid="menu-compare-default" :disabled="!current" @click="current && emit('compare', current.id)">Compare with deployed version on {{ current?.name ?? 'server' }}</button>
    <template v-if="connections.length > 1">
     <button role="menuitem" data-testid="menu-compare-servers" aria-haspopup="menu" :aria-expanded="submenu === 'compare'" @click="submenu = submenu === 'compare' ? undefined : 'compare'" @mouseenter="submenu = 'compare'">Compare with… <span>▸</span></button>
     <div v-if="submenu === 'compare'" role="menu" class="server-submenu" aria-label="Сравнить с сервером"><button v-for="connection in connections" :key="connection.id" role="menuitem" :data-compare-server-id="connection.id" @click="emit('compare', connection.id)">{{ connection.name }}</button></div>
    </template>
   </template>
   <template v-if="connections.length > 1">
    <div class="menu-divider"></div>
    <template v-for="direction in (['upload', 'download'] as const)" :key="direction">
     <button role="menuitem" :data-testid="`menu-${direction}-servers`" aria-haspopup="menu" :aria-expanded="submenu === direction" @click="submenu = submenu === direction ? undefined : direction" @mouseenter="submenu = direction">{{ direction === 'upload' ? 'Загрузить на' : 'Скачать с' }} <span>▸</span></button>
     <div v-if="submenu === direction" role="menu" class="server-submenu" :aria-label="direction === 'upload' ? 'Загрузить на сервер' : 'Скачать с сервера'">
      <button v-for="connection in connections" :key="connection.id" role="menuitem" :data-server-id="connection.id" :data-direction="direction" @click="choose(direction, connection.id)">{{ connection.name }}<span v-if="connection.id === currentId">✓</span></button>
     </div>
    </template>
   </template>
   <p v-if="!connections.length" class="menu-hint">Добавьте сервер в настройках проекта</p>
  </div>
 </Teleport>
</template>
