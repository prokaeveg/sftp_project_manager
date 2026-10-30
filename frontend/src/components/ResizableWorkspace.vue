<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from 'vue'
import { fitPanels, readPanelSizes } from '../panel-layout'

const storageKey = 'sftp-workspace-panel-sizes'
const root = ref<HTMLElement>()
const width = ref(1000)
const preferred = ref(readPanelSizes(null))
const sizes = computed(() => fitPanels(width.value, preferred.value))
const columns = computed(() => `${sizes.value.left}px 6px minmax(230px, 1fr) 6px ${sizes.value.right}px`)
const active = ref<'left' | 'right'>()
let observer: ResizeObserver | undefined
let drag: { id: number; target: HTMLElement; x: number; size: number; cursor: string; selection: string } | undefined

function save() {
 try { localStorage.setItem(storageKey, JSON.stringify(preferred.value)) } catch { /* Resizing also works without storage. */ }
}
function setSize(side: 'left' | 'right', value: number) {
 const other = side === 'left' ? sizes.value.right : sizes.value.left
 const minimum = side === 'left' ? 175 : 195
 preferred.value = { ...sizes.value, [side]: Math.max(minimum, Math.min(value, width.value - 230 - other)) }
}
function start(side: 'left' | 'right', event: PointerEvent) {
 if (event.button !== 0 || drag) return
 const target = event.currentTarget as HTMLElement
 target.setPointerCapture(event.pointerId)
 drag = { id: event.pointerId, target, x: event.clientX, size: sizes.value[side], cursor: document.body.style.cursor, selection: document.body.style.userSelect }
 active.value = side
 document.body.style.cursor = 'col-resize'; document.body.style.userSelect = 'none'
 event.preventDefault()
}
function move(event: PointerEvent) {
 if (!drag || !active.value || drag.id !== event.pointerId) return
 setSize(active.value, drag.size + (event.clientX - drag.x) * (active.value === 'left' ? 1 : -1))
}
function finish() {
 if (!drag) return
 const previous = drag; drag = undefined; active.value = undefined
 document.body.style.cursor = previous.cursor; document.body.style.userSelect = previous.selection
 if (previous.target.hasPointerCapture(previous.id)) previous.target.releasePointerCapture(previous.id)
 save()
}
function keyResize(side: 'left' | 'right', event: KeyboardEvent) {
 if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
 event.preventDefault()
 setSize(side, sizes.value[side] + (event.key === 'ArrowRight' ? 10 : -10) * (side === 'left' ? 1 : -1)); save()
}
onMounted(() => {
 try { preferred.value = readPanelSizes(localStorage.getItem(storageKey)) } catch { /* Keep defaults. */ }
 observer = new ResizeObserver(entries => { width.value = Math.max(0, entries[0]!.contentRect.width - 12) })
 if (root.value) observer.observe(root.value)
})
onUnmounted(() => { finish(); observer?.disconnect() })
</script>

<template>
 <section ref="root" class="workspace resizable-workspace" :style="{ gridTemplateColumns: columns }">
  <slot name="left" />
  <div class="panel-splitter" :class="{ dragging: active === 'left' }" role="separator" tabindex="0" aria-label="Ширина панели проекта" aria-orientation="vertical" :aria-valuenow="Math.round(sizes.left)" :aria-valuemin="175" :aria-valuemax="Math.max(175, Math.floor(width - 230 - sizes.right))" @pointerdown="start('left', $event)" @pointermove="move" @pointerup="finish" @pointercancel="finish" @lostpointercapture="finish" @keydown="keyResize('left', $event)" />
  <slot />
  <div class="panel-splitter" :class="{ dragging: active === 'right' }" role="separator" tabindex="0" aria-label="Ширина панели сервера" aria-orientation="vertical" :aria-valuenow="Math.round(sizes.right)" :aria-valuemin="195" :aria-valuemax="Math.max(195, Math.floor(width - 230 - sizes.left))" @pointerdown="start('right', $event)" @pointermove="move" @pointerup="finish" @pointercancel="finish" @lostpointercapture="finish" @keydown="keyResize('right', $event)" />
  <slot name="right" />
 </section>
</template>
