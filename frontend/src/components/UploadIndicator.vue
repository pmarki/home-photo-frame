<template>
  <button
    v-bind="$attrs"
    class="upload-indicator"
    :class="{ busy, failed: busy && summary.pending === 0 }"
    :title="label"
    :aria-label="label"
    @click="onClick"
  >
    <template v-if="busy">
      <svg class="ui-ring" viewBox="0 0 36 36" aria-hidden="true">
        <circle class="ui-ring-track" cx="18" cy="18" r="15" />
        <circle
          class="ui-ring-fill"
          cx="18" cy="18" r="15"
          :stroke-dasharray="CIRCUMFERENCE"
          :stroke-dashoffset="CIRCUMFERENCE * (1 - progress)"
        />
      </svg>
      <svg class="ui-arrow" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
        <template v-if="summary.pending > 0">
          <polyline points="17 9 12 4 7 9" />
          <line x1="12" y1="4" x2="12" y2="17" />
        </template>
        <template v-else>
          <line x1="12" y1="6" x2="12" y2="13" />
          <line x1="12" y1="17.5" x2="12" y2="17.6" />
        </template>
      </svg>
      <span v-if="badge" class="ui-badge">{{ badge }}</span>
    </template>
    <svg v-else class="ui-idle" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
      <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/>
      <polyline points="17 8 12 3 7 8"/>
      <line x1="12" y1="3" x2="12" y2="15"/>
    </svg>
  </button>
  <input
    ref="fileInput"
    type="file"
    multiple
    :accept="accept"
    style="display:none"
    @change="onFilesSelected"
  />
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { summarize } from '../uploadQueue.js'
import { onUploadsChanged, refreshUploads } from '../composables/uploadRunner.js'

// Header upload button. Idle, it opens the file picker (App uploads the files
// to the current folder). While uploads are queued or have failed it becomes a
// ring filling with overall progress (by bytes) plus the number of files left,
// and opens the upload dialog instead.
// Two root nodes (button + hidden input): attributes such as the header's
// class go on the button.
defineOptions({ inheritAttrs: false })
defineProps({
  accept: { type: String, default: 'image/*' },
})
const emit = defineEmits(['open', 'upload-files'])

const CIRCUMFERENCE = 2 * Math.PI * 15

const jobs = ref([])
const fileInput = ref(null)
const summary = computed(() => summarize(jobs.value))
const busy = computed(() => summary.value.pending > 0 || summary.value.failed > 0)

const progress = computed(() => {
  let total = 0
  let sent = 0
  for (const j of jobs.value) {
    total += j.size
    sent += j.state === 'done' || j.state === 'error' ? j.size : j.size * (j.progress / 100)
  }
  return total > 0 ? sent / total : 0
})

const badge = computed(() => summary.value.pending || summary.value.failed || '')

const label = computed(() => {
  const s = summary.value
  if (!busy.value) return 'Upload photos to this folder'
  if (s.pending > 0) {
    return `Uploading ${s.pending} file${s.pending !== 1 ? 's' : ''} (${Math.round(progress.value * 100)}%)`
  }
  return `${s.failed} upload${s.failed !== 1 ? 's' : ''} failed`
})

function onClick() {
  if (busy.value) emit('open')
  else fileInput.value?.click()
}

function onFilesSelected(e) {
  const files = Array.from(e.target.files)
  e.target.value = ''
  if (files.length > 0) emit('upload-files', files)
}

let unsubscribe = null
onMounted(() => {
  unsubscribe = onUploadsChanged((list) => { jobs.value = list })
  refreshUploads()
})
onUnmounted(() => unsubscribe?.())
</script>

<style scoped>
.upload-indicator {
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  padding: 0;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 8px;
  background: transparent;
  color: #888;
  cursor: pointer;
  transition: border-color 0.15s, color 0.15s;
}
.upload-indicator:hover { border-color: rgba(255, 255, 255, 0.25); color: #ccc; }
.ui-idle { width: 18px; height: 18px; }

/* Busy: the ring replaces the square outline. */
.upload-indicator.busy {
  border-color: transparent;
  border-radius: 50%;
  color: #c0caff;
}
.upload-indicator.busy:hover { background: rgba(255, 255, 255, 0.06); }

.ui-ring {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  transform: rotate(-90deg);
}
.ui-ring-track {
  fill: none;
  stroke: rgba(255, 255, 255, 0.1);
  stroke-width: 2.5;
}
.ui-ring-fill {
  fill: none;
  stroke: #7c9cfc;
  stroke-width: 2.5;
  stroke-linecap: round;
  transition: stroke-dashoffset 0.3s ease;
}
.ui-arrow { width: 15px; height: 15px; }

.ui-badge {
  position: absolute;
  top: -3px;
  right: -5px;
  min-width: 16px;
  height: 16px;
  padding: 0 4px;
  border-radius: 8px;
  background: #3b4a8f;
  color: #fff;
  font-size: 10px;
  font-weight: 600;
  line-height: 16px;
  text-align: center;
}

.upload-indicator.failed { color: #f87171; }
.upload-indicator.failed .ui-ring-fill { stroke: #f87171; }
.upload-indicator.failed .ui-badge { background: #b91c1c; }

@media (prefers-reduced-motion: reduce) {
  .ui-ring-fill { transition: none; }
}
</style>
