<script setup lang="ts">
import { onMounted, onUnmounted, ref, watch } from 'vue'
const props = defineProps<{ path: string }>()
const visible = ref(false), anchor = ref<HTMLElement>(), expanded = ref<HTMLElement>()
const appearance = ref<Record<string, string>>({})
function show() {
 const text = anchor.value?.firstElementChild as HTMLElement | undefined
 if (!text || text.scrollWidth <= text.clientWidth + 1) return
 const rect = text.getBoundingClientRect(), style = getComputedStyle(text)
 const row = text.closest('.tree-row')
 const background = row ? getComputedStyle(row).backgroundColor : ''
 appearance.value = {
  left: `${rect.left - 6}px`, top: `${rect.top}px`, minHeight: `${rect.height}px`,
  maxWidth: `${Math.max(0, window.innerWidth - rect.left - 8)}px`,
  font: style.font, lineHeight: style.lineHeight, letterSpacing: style.letterSpacing,
  color: style.color, backgroundColor: background && background !== 'rgba(0, 0, 0, 0)' ? background : 'var(--bg)',
 }
 visible.value = true
}
function hide() { visible.value = false }
function leave(event: MouseEvent) {
 const target = event.relatedTarget as Node | null
 if (target && (anchor.value?.contains(target) || expanded.value?.contains(target))) return
 hide()
}
watch(() => props.path, hide)
onMounted(() => { window.addEventListener('resize', hide); document.addEventListener('scroll', hide, true) })
onUnmounted(() => { window.removeEventListener('resize', hide); document.removeEventListener('scroll', hide, true) })
</script>
<template>
 <span ref="anchor" class="project-root-path" data-testid="project-root-path" tabindex="0" :aria-label="path" @mouseenter="show" @mouseleave="leave" @focus="show" @blur="hide" @keydown.esc.stop="hide"><span class="project-root-path-text">{{ path }}</span></span>
 <Teleport to="body"><span v-if="visible" ref="expanded" aria-hidden="true" class="project-path-expanded" data-testid="project-path-expanded" :style="appearance" @mouseleave="leave">{{ path }}</span></Teleport>
</template>
