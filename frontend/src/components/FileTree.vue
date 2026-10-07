<script setup lang="ts">
import type { TreeNode } from '../file-tree'
import type { FileEntry } from '../bridge'

const props = defineProps<{ node: TreeNode; busy: boolean; side: 'local' | 'remote'; selected: Set<string> }>()
const emit = defineEmits<{
 toggle: [node: TreeNode]
 refresh: [node: TreeNode]
 transfer: [file: FileEntry]
 select: [file: FileEntry, event: MouseEvent | KeyboardEvent]
 context: [file: FileEntry, event: MouseEvent]
}>()

function select(event: MouseEvent | KeyboardEvent) {
 if (props.busy || props.node.entry.excluded || props.node.entry.isLink) return
 emit('select', props.node.entry, event)
}

function onKeydown(event: KeyboardEvent) {
 if (event.target !== event.currentTarget) return
 if (event.key !== 'Enter' && event.key !== ' ') return
 event.preventDefault()
 select(event)
}

function onContextMenu(event: MouseEvent) {
 event.preventDefault()
 event.stopPropagation()
 emit('context', props.node.entry, event)
}
</script>

<template>
 <li class="tree-node">
  <div
   class="tree-row"
   :class="{ excluded: node.entry.excluded, selected: selected.has(node.entry.relativePath) }"
   role="treeitem"
   :aria-selected="selected.has(node.entry.relativePath)"
   :aria-expanded="node.entry.isDir && !node.entry.isLink ? node.expanded : undefined"
   :tabindex="node.entry.excluded || node.entry.isLink ? -1 : 0"
   @click="select($event)"
   @keydown="onKeydown"
   @contextmenu="onContextMenu"
  >
   <button
    v-if="node.entry.isDir && !node.entry.isLink"
    class="tree-toggle"
    type="button"
    :disabled="busy || node.entry.excluded"
    :aria-label="`${node.expanded ? 'Свернуть' : 'Раскрыть'} ${node.entry.name}`"
    :aria-expanded="node.expanded"
    @click.stop="emit('toggle', node)"
 ><span class="disclosure">{{ node.expanded ? '▾' : '▸' }}</span></button>
   <span v-else class="tree-disclosure-spacer"></span>
   <span class="tree-name"><span class="entry-icon">{{ node.entry.isLink ? '↗' : node.entry.isDir ? '▰' : '▫' }}</span>{{ node.entry.name }}{{ node.entry.isDir ? '/' : '' }}</span>
   <span v-if="node.entry.excluded" class="excluded-label">исключён</span>
   <span v-else-if="!node.entry.isDir && !node.entry.isLink" class="file-size">{{ node.entry.size }} Б</span>
   <button
    v-if="node.entry.isDir && !node.entry.isLink && node.loaded"
    class="row-action"
    type="button"
    :disabled="busy || node.entry.excluded"
    :aria-label="`Обновить ${node.entry.name}`"
    title="Обновить папку"
    @click.stop="emit('refresh', node)"
   >⟳</button>
  </div>
  <template v-if="node.expanded">
   <p v-if="node.loading" role="status" class="tree-message">Загрузка…</p>
   <div v-if="node.error" class="tree-message error" role="alert"><p>{{ node.error }}</p><p v-if="node.loaded">Показаны данные предыдущего чтения.</p><button type="button" :disabled="busy" @click="emit('refresh', node)">Повторить</button></div>
   <p v-else-if="node.loaded && !node.children.length && !node.loading" class="tree-message">Папка пуста</p>
   <ul v-if="node.children.length" class="file-tree"><FileTree v-for="child in node.children" :key="child.entry.relativePath" :node="child" :busy="busy" :side="side" :selected="selected" @toggle="emit('toggle', $event)" @refresh="emit('refresh', $event)" @select="(file, event) => emit('select', file, event)" @context="(file, event) => emit('context', file, event)" /></ul>
  </template>
 </li>
</template>
