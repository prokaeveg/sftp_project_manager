<script setup lang="ts">
import type { TreeNode } from '../file-tree'
import type { FileEntry } from '../bridge'
defineProps<{ node: TreeNode; busy: boolean; side: 'local' | 'remote'; selected: Set<string> }>()
const emit = defineEmits<{ toggle: [node: TreeNode]; refresh: [node: TreeNode]; transfer: [file: FileEntry]; select: [file: FileEntry, checked: boolean] }>()
</script>

<template>
 <li class="tree-node">
  <div class="tree-row" :class="{ excluded: node.entry.excluded, selected: selected.has(node.entry.relativePath) }">
   <input class="tree-select" type="checkbox" :checked="selected.has(node.entry.relativePath)" :disabled="busy || node.entry.excluded || node.entry.isLink" :aria-label="`Выбрать ${node.entry.name}`" @change="emit('select', node.entry, ($event.target as HTMLInputElement).checked)">
   <button v-if="node.entry.isDir && !node.entry.isLink" class="tree-toggle" type="button" :disabled="busy || node.entry.excluded" :aria-expanded="node.expanded" @click="emit('toggle', node)"><span class="disclosure">{{ node.expanded ? '▾' : '▸' }}</span><span class="entry-icon">▰</span>{{ node.entry.name }}/</button>
   <span v-else class="tree-name"><span class="entry-icon">{{ node.entry.isLink ? '↗' : '▤' }}</span>{{ node.entry.name }}</span>
   <span v-if="node.entry.excluded" class="excluded-label">исключён</span>
   <span v-else-if="!node.entry.isDir && !node.entry.isLink" class="file-size">{{ node.entry.size }} Б</span>
   <button v-if="node.entry.isDir && !node.entry.isLink && node.loaded" class="row-action" type="button" :disabled="busy || node.entry.excluded" :aria-label="`Обновить ${node.entry.name}`" title="Обновить папку" @click="emit('refresh', node)">⟳</button>
  </div>
  <template v-if="node.expanded">
   <p v-if="node.loading" role="status" class="tree-message">Загрузка…</p>
   <div v-if="node.error" class="tree-message error" role="alert"><p>{{ node.error }}</p><p v-if="node.loaded">Показаны данные предыдущего чтения.</p><button type="button" :disabled="busy" @click="emit('refresh', node)">Повторить</button></div>
   <p v-else-if="node.loaded && !node.children.length && !node.loading" class="tree-message">Папка пуста</p>
   <ul v-if="node.children.length" class="file-tree"><FileTree v-for="child in node.children" :key="child.entry.relativePath" :node="child" :busy="busy" :side="side" :selected="selected" @toggle="emit('toggle', $event)" @refresh="emit('refresh', $event)" @select="(file, checked) => emit('select', file, checked)" /></ul>
  </template>
 </li>
</template>
