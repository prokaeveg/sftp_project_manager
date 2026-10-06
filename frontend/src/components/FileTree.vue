<script setup lang="ts">
import type { TreeNode } from '../file-tree'
import type { FileEntry } from '../bridge'
defineProps<{ node: TreeNode; busy: boolean; side: 'local' | 'remote'; selected: Set<string> }>()
const emit = defineEmits<{
 toggle: [node: TreeNode];
 refresh: [node: TreeNode];
 transfer: [file: FileEntry];
 select: [file: FileEntry, checked: boolean]
}>()
</script>

<template>
 <li class="tree-node">
  <div class="tree-row" :class="{ excluded: node.entry.excluded }">
   <input class="tree-select" type="checkbox" :checked="selected.has(node.entry.relativePath)"
    :disabled="busy || node.entry.excluded || node.entry.isLink" :aria-label="`Выбрать ${node.entry.name}`"
    @change="emit('select', node.entry, ($event.target as HTMLInputElement).checked)">
   <button v-if="node.entry.isDir && !node.entry.isLink" class="tree-toggle" type="button"
    :disabled="busy || node.entry.excluded" :aria-expanded="node.expanded"
    :aria-label="`${node.expanded ? 'Свернуть' : 'Раскрыть'} ${node.entry.name}`" @click="emit('toggle', node)">
    {{ node.expanded ? '▾' : '▸' }} {{ node.entry.name }}/
   </button>
   <span v-else class="tree-name">{{ node.entry.name }} {{ node.entry.isLink ? '(ссылка, недоступна)' : `· ${node.entry.size} Б` }}</span>
   <span v-if="node.entry.excluded">(исключён)</span>
   <button v-if="node.entry.isDir && !node.entry.isLink && node.loaded" type="button"
    :disabled="busy || node.entry.excluded" :aria-label="`Обновить ${node.entry.name}`" @click="emit('refresh', node)">Обновить</button>
   <button v-if="!node.entry.isLink" type="button" :disabled="busy || node.entry.excluded"
    @click="emit('transfer', node.entry)">{{ side === 'local' ? 'Загрузить на сервер' : 'Скачать на компьютер' }}</button>
  </div>
  <template v-if="node.expanded">
   <p v-if="node.loading" role="status" class="tree-message">Загрузка…</p>
   <div v-if="node.error" class="tree-message error" role="alert">
    <p>{{ node.error }}</p><p v-if="node.loaded">Показано ранее загруженное содержимое.</p>
    <button type="button" :disabled="busy" @click="emit('refresh', node)">Повторить загрузку</button>
   </div>
   <p v-else-if="node.loaded && !node.children.length && !node.loading" class="tree-message">Каталог пуст.</p>
   <ul v-if="node.children.length" class="file-tree">
    <FileTree v-for="child in node.children" :key="child.entry.relativePath" :node="child" :busy="busy" :side="side" :selected="selected"
     @toggle="emit('toggle', $event)" @refresh="emit('refresh', $event)" @transfer="emit('transfer', $event)"
     @select="(file, checked) => emit('select', file, checked)" />
   </ul>
  </template>
 </li>
</template>
