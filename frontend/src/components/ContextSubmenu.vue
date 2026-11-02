<script setup lang="ts">
import { nextTick, onUnmounted, ref, watch } from 'vue'
import { submenuPosition } from '../submenu-position'

const props = defineProps<{ open: boolean; label: string; testId: string; disabled?: boolean }>()
const emit = defineEmits<{ open: []; close: []; dismiss: [] }>()
const trigger = ref<HTMLButtonElement>(), panel = ref<HTMLElement>()
const position = ref({ left: 0, top: 0 })
let timer: ReturnType<typeof setTimeout> | undefined
function cancelClose() { if (timer) clearTimeout(timer); timer = undefined }
function enter() { cancelClose(); if (!props.disabled) emit('open') }
function leave() { cancelClose(); timer = setTimeout(() => emit('close'), 120) }
async function focusPanel() {
 enter(); await nextTick(); panel.value?.querySelector<HTMLButtonElement>('button:not(:disabled)')?.focus()
}
function triggerKey(event: KeyboardEvent) {
 if (event.key === 'ArrowRight') { event.preventDefault(); event.stopPropagation(); void focusPanel() }
}
function panelKey(event: KeyboardEvent) {
 event.stopPropagation()
 if (event.key === 'ArrowLeft') { event.preventDefault(); emit('close'); trigger.value?.focus(); return }
 if (event.key === 'Escape') { event.preventDefault(); emit('dismiss'); return }
 if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp') return
 event.preventDefault()
 const buttons = Array.from(panel.value!.querySelectorAll<HTMLButtonElement>('button:not(:disabled)'))
 const index = buttons.indexOf(document.activeElement as HTMLButtonElement)
 buttons[(index + (event.key === 'ArrowDown' ? 1 : -1) + buttons.length) % buttons.length]?.focus()
}
watch(() => props.open, async open => {
 cancelClose()
 if (!open) return
 await nextTick()
 if (trigger.value && panel.value) position.value = submenuPosition(trigger.value.getBoundingClientRect(), panel.value.offsetWidth, panel.value.offsetHeight, window.innerWidth, window.innerHeight)
})
onUnmounted(cancelClose)
</script>

<template>
 <div class="context-submenu" @mouseenter="enter" @mouseleave="leave">
  <button ref="trigger" role="menuitem" :data-testid="testId" :disabled="disabled" aria-haspopup="menu" :aria-expanded="open" @click="enter" @keydown="triggerKey">{{ label }} <span aria-hidden="true">▸</span></button>
  <Teleport to="body">
   <div v-if="open" ref="panel" class="transfer-menu context-submenu-panel" data-context-submenu role="menu" :aria-label="label" :style="{ left: `${position.left}px`, top: `${position.top}px` }" @mouseenter="cancelClose" @mouseleave="leave" @keydown="panelKey" @contextmenu.prevent><slot /></div>
  </Teleport>
 </div>
</template>
