<script setup lang="ts">
import { onMounted, ref } from 'vue'
defineProps<{ projectName: string; busy: boolean; error: string }>()
const emit = defineEmits<{ choose: [choice: 'current' | 'new' | 'cancel'] }>()
const dialog = ref<HTMLDialogElement>()
onMounted(() => dialog.value?.showModal())
</script>

<template>
 <dialog ref="dialog" class="modal project-open-modal" data-testid="project-open-dialog" aria-labelledby="project-open-title" @cancel.prevent="!busy && emit('choose', 'cancel')">
  <div class="modal-content">
   <h2 id="project-open-title">Открыть проект</h2>
   <p>Где открыть проект «{{ projectName }}»?</p>
   <p v-if="error" role="alert" class="error">{{ error }}</p>
   <footer class="modal-actions project-open-actions">
    <button type="button" data-testid="project-open-cancel" :disabled="busy" @click="emit('choose', 'cancel')">Отмена</button>
    <button type="button" data-testid="project-open-new" :disabled="busy" @click="emit('choose', 'new')">В новом окне</button>
    <button type="button" class="primary" data-testid="project-open-current" :disabled="busy" autofocus @click="emit('choose', 'current')">В этом окне</button>
   </footer>
  </div>
 </dialog>
</template>
