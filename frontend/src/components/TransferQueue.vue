<script setup lang="ts">
import { computed } from 'vue'
import type { QueueJob, QueueState, QueueItem } from '../bridge'

const props = defineProps<{
 queue: QueueState;
 error: string;
 busy: boolean;
 appBusy: boolean
}>()

const emit = defineEmits<{
 pause: [paused: boolean];
 cancel: [id: string];
 clear: [];
 retry: [id: string]
}>()

const counts = computed(() => {
 const result = { running: 0, waiting: 0, completed: 0, failed: 0, cancelled: 0 }
 for (const job of props.queue.jobs) result[job.status]++
 return result
})

function label(status: QueueJob['status'] | QueueItem['status']) {
 return ({ waiting: 'В очереди', running: 'Передаётся', completed: 'Готово', skipped: 'Пропущено', failed: 'Ошибка', cancelled: 'Отменено' } as const)[status]
}

function bytes(value: number) {
 if (value < 1024) return `${value} Б`
 const units = ['КБ', 'МБ', 'ГБ', 'ТБ']
 let amount = value / 1024, unit = 0
 while (amount >= 1024 && unit < units.length - 1) { amount /= 1024; unit++ }
 return `${amount.toLocaleString('ru-RU', { maximumFractionDigits: 1 })} ${units[unit]}`
}

function percent(job: QueueJob) {
 if (job.totalBytes <= 0) return job.status === 'completed' ? 100 : 0
 return Math.max(0, Math.min(100, Math.floor(job.transferredBytes / job.totalBytes * 100)))
}

function finished(job: QueueJob) { return job.status === 'completed' || job.status === 'failed' || job.status === 'cancelled' }
function retryable(job: QueueJob) { return (job.status === 'failed' || job.status === 'cancelled') && job.items.some(item => item.status === 'failed' || item.status === 'cancelled') }
</script>

<template>
 <section class="queue-panel" aria-label="Очередь передач">
  <header class="queue-header">
   <div class="queue-title"><strong>Очередь передач</strong><span class="queue-summary">{{ counts.running }} выполняется · {{ counts.waiting }} ожидает<span v-if="counts.failed"> · {{ counts.failed }} ошибок</span></span></div>
    <button data-testid="queue-pause" type="button" class="small" :disabled="busy" @click="emit('pause', !queue.paused)">{{ queue.paused ? 'Продолжить' : 'Приостановить' }}</button>
   <button data-testid="queue-clear" type="button" class="quiet small" :disabled="busy || !queue.jobs.some(finished)" @click="emit('clear')">Очистить завершённые</button>
  </header>
  <p v-if="error" class="queue-error" role="alert">{{ error }}</p>
  <div v-if="!queue.jobs.length" class="queue-empty">Заданий пока нет. Приостановите очередь, чтобы подготовить несколько передач подряд.</div>
  <div v-else class="queue-jobs">
   <article v-for="job in queue.jobs" :key="job.id" class="queue-job" :data-queue-id="job.id">
    <div class="queue-job-top">
     <div class="queue-job-name"><strong>{{ job.projectName }}</strong><span>{{ job.direction === 'upload' ? 'Загрузка' : 'Скачивание' }} · {{ job.server }}</span></div>
     <span class="queue-status" :class="`status-${job.status}`">{{ label(job.status) }}</span>
     <button v-if="!finished(job)" data-testid="queue-cancel" type="button" class="quiet small danger-link" :disabled="busy" @click="emit('cancel', job.id)">{{ job.status === 'running' ? 'Отменить' : 'Убрать' }}</button>
     <button v-if="retryable(job)" data-testid="queue-retry" type="button" class="quiet small" :disabled="busy || appBusy || queue.jobs.some(candidate => candidate.status === 'running')" @click="emit('retry', job.id)">Повторить…</button>
    </div>
    <div class="queue-progress" role="progressbar" :aria-label="`Передача ${job.projectName}`" :aria-valuenow="percent(job)" aria-valuemin="0" aria-valuemax="100"><span :style="{ width: `${percent(job)}%` }" :class="{ 'progress-failed': job.status === 'failed', 'progress-cancelled': job.status === 'cancelled' }"></span></div>
    <div class="queue-meta"><span>{{ job.processedItems }} из {{ job.items.length }} элементов обработано</span><span>Скопировано {{ bytes(job.transferredBytes) }} из {{ bytes(job.totalBytes) }} · {{ percent(job) }}%</span></div>
    <p v-if="job.error" class="queue-error">{{ job.error }}</p>
    <details class="queue-details"><summary>Показать файлы ({{ job.items.length }})</summary><div class="queue-items"><div v-for="item in job.items" :key="item.relativePath" class="queue-item"><span class="queue-item-status" :class="`status-${item.status}`">{{ label(item.status) }}</span><details class="queue-item-paths"><summary :title="item.relativePath">{{ item.relativePath }}</summary><dl><dt>Источник</dt><dd :title="item.source">{{ item.source || item.relativePath }}</dd><dt>Назначение</dt><dd :title="item.destination">{{ item.destination }}</dd></dl></details><span v-if="item.kind === 'file'" class="queue-item-bytes">Скопировано {{ bytes(item.transferredBytes) }} из {{ bytes(item.size) }}</span><span v-else class="queue-item-bytes">Папка</span><span v-if="item.error" class="queue-item-error" :title="item.error">{{ item.error }}</span></div></div></details>
   </article>
  </div>
 </section>
</template>
