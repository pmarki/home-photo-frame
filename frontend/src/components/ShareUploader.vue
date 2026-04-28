<template>
  <div class="su-overlay" role="dialog" aria-modal="true" aria-label="Uploading shared photos">
    <div class="su-sheet">
      <div class="su-header">
        <svg class="su-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5">
          <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/>
          <polyline points="17 8 12 3 7 8"/>
          <line x1="12" y1="3" x2="12" y2="15"/>
        </svg>
        <div>
          <h2 class="su-title">Receiving photos</h2>
          <p class="su-subtitle">{{ statusLine }}</p>
        </div>
      </div>

      <ul class="su-file-list">
        <li v-for="item in items" :key="item.name" class="su-file-item">
          <div class="su-file-info">
            <span class="su-file-name">{{ item.name }}</span>
            <span class="su-file-size">{{ formatSize(item.size) }}</span>
            <span class="su-file-status" :class="item.state">
              <svg v-if="item.state === 'done'" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.5"><polyline points="20 6 9 17 4 12"/></svg>
              <svg v-else-if="item.state === 'error'" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.5"><line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/></svg>
              <div v-else-if="item.state === 'uploading'" class="mini-spinner" />
              <span v-else class="su-queued-dot" />
            </span>
          </div>
          <div class="su-progress-track">
            <div
              class="su-progress-fill"
              :class="item.state"
              :style="{ width: item.progress + '%' }"
            />
          </div>
          <p v-if="item.error" class="su-error-msg">{{ item.error }}</p>
        </li>
      </ul>

      <div class="su-footer">
        <div v-if="allDone" class="su-footer-actions">
          <button class="su-btn su-btn-secondary" @click="finish(false)">Done</button>
          <button v-if="items.some(i => i.state === 'done')" class="su-btn su-btn-primary" @click="finish(true)">Crop</button>
        </div>
        <button v-else-if="!uploading" class="su-btn su-btn-secondary" @click="finish(false)">
          Cancel
        </button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted } from 'vue'

const emit = defineEmits(['done'])

const SHARE_CACHE = 'share-pending-v1'

const items = ref([])     // { name, size, state: 'queued'|'uploading'|'done'|'error', progress, error }
const uploading = ref(false)

const allDone = computed(
  () => items.value.length > 0 && items.value.every((i) => i.state === 'done' || i.state === 'error')
)

const statusLine = computed(() => {
  if (items.value.length === 0) return 'Reading shared files…'
  const done  = items.value.filter((i) => i.state === 'done').length
  const total = items.value.length
  if (allDone.value) {
    const failed = items.value.filter((i) => i.state === 'error').length
    return failed ? `${done} saved, ${failed} failed` : `${done} photo${done !== 1 ? 's' : ''} saved`
  }
  return `Uploading ${done + 1} of ${total}…`
})

// ── Read files from Cache API (written by the service worker) ─────────
async function readPendingFiles() {
  try {
    const cache = await caches.open(SHARE_CACHE)
    const manifestRes = await cache.match('/share-pending/manifest')
    if (!manifestRes) return []

    const fileInfos = await manifestRes.json()
    await cache.delete('/share-pending/manifest')

    const files = []
    for (const info of fileInfos) {
      const res = await cache.match(info.key)
      if (!res) continue
      const blob = await res.blob()
      files.push({
        file: new File([blob], info.name, { type: info.type }),
        name: info.name,
        size: info.size
      })
      await cache.delete(info.key)
    }
    return files
  } catch (err) {
    console.error('ShareUploader: read cache failed', err)
    return []
  }
}

// ── Upload one file with XHR progress events ──────────────────────────
function uploadFile(file, item) {
  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest()
    const form = new FormData()
    form.append('file', file)

    xhr.upload.addEventListener('progress', (e) => {
      if (e.lengthComputable) {
        item.progress = Math.round((e.loaded / e.total) * 100)
      }
    })

    xhr.addEventListener('load', () => {
      if (xhr.status >= 200 && xhr.status < 300) {
        item.progress = 100
        item.state = 'done'
        try {
          const d = JSON.parse(xhr.responseText)
          item.savedFilename = d.filename
          item.savedThumbSmall = d.thumbSmall
        } catch (_) {}
        resolve(true)
      } else {
        item.state = 'error'
        item.error = `Server error ${xhr.status}`
        resolve(false)
      }
    })

    xhr.addEventListener('error', () => {
      item.state = 'error'
      item.error = 'Network error'
      resolve(false)
    })

    xhr.open('POST', '/api/upload')
    xhr.send(form)
  })
}

// ── Sequential upload loop ─────────────────────────────────────────────
async function runUploads(files) {
  uploading.value = true
  items.value = files.map(({ name, size }) => ({
    name, size, state: 'queued', progress: 0, error: null, savedFilename: null, savedThumbSmall: null,
  }))

  for (let i = 0; i < files.length; i++) {
    items.value[i].state = 'uploading'
    await uploadFile(files[i].file, items.value[i])
  }
  uploading.value = false
}

function finish(withCrop) {
  const uploaded = withCrop
    ? items.value.filter(i => i.state === 'done' && i.savedFilename)
        .map(i => ({ filename: i.savedFilename, thumbSmall: i.savedThumbSmall }))
    : []
  emit('done', uploaded)
}

function formatSize(bytes) {
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
}

onMounted(async () => {
  const files = await readPendingFiles()
  if (files.length === 0) {
    // Nothing in cache — maybe a stale ?share-pending param; dismiss
    emit('done')
    return
  }
  await runUploads(files)
})
</script>

<style scoped>
.su-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.7);
  z-index: 9000;
  display: flex;
  align-items: flex-end;
  justify-content: center;
  padding-bottom: env(safe-area-inset-bottom);
}

@media (min-width: 600px) {
  .su-overlay { align-items: center; }
}

.su-sheet {
  background: #18181f;
  border-radius: 20px 20px 0 0;
  width: 100%;
  max-width: 520px;
  padding: 24px 20px 20px;
  box-shadow: 0 -8px 40px rgba(0, 0, 0, 0.5);
  display: flex;
  flex-direction: column;
  gap: 20px;
}

@media (min-width: 600px) {
  .su-sheet { border-radius: 16px; }
}

/* ─── Header ───────────────────────────────────────────────────────── */
.su-header {
  display: flex;
  align-items: center;
  gap: 14px;
}

.su-icon {
  width: 40px;
  height: 40px;
  flex-shrink: 0;
  color: #7c9cfc;
}

.su-title {
  font-size: 1.05rem;
  font-weight: 600;
  color: #f0f0f8;
  margin-bottom: 2px;
}

.su-subtitle {
  font-size: 0.82rem;
  color: #888;
}

/* ─── File list ────────────────────────────────────────────────────── */
.su-file-list {
  list-style: none;
  display: flex;
  flex-direction: column;
  gap: 14px;
  max-height: 50vh;
  overflow-y: auto;
}

.su-file-item {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.su-file-info {
  display: flex;
  align-items: center;
  gap: 8px;
}

.su-file-name {
  flex: 1;
  font-size: 0.85rem;
  color: #ccc;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.su-file-size {
  font-size: 0.75rem;
  color: #555;
  flex-shrink: 0;
}

.su-file-status {
  width: 20px;
  height: 20px;
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.su-file-status.done   { color: #4ade80; }
.su-file-status.error  { color: #f87171; }
.su-file-status.uploading { color: #7c9cfc; }

.su-queued-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: #444;
  display: block;
}

.mini-spinner {
  width: 14px;
  height: 14px;
  border: 2px solid rgba(124, 156, 252, 0.2);
  border-top-color: #7c9cfc;
  border-radius: 50%;
  animation: spin 0.7s linear infinite;
}

@keyframes spin { to { transform: rotate(360deg); } }

/* ─── Progress bar ─────────────────────────────────────────────────── */
.su-progress-track {
  height: 4px;
  background: rgba(255, 255, 255, 0.06);
  border-radius: 2px;
  overflow: hidden;
}

.su-progress-fill {
  height: 100%;
  border-radius: 2px;
  transition: width 0.15s ease;
  background: #7c9cfc;
}
.su-progress-fill.done  { background: #4ade80; }
.su-progress-fill.error { background: #f87171; }
.su-progress-fill.queued { background: transparent; }

/* ─── Error message ────────────────────────────────────────────────── */
.su-error-msg {
  font-size: 0.75rem;
  color: #f87171;
  margin-top: 2px;
}

/* ─── Footer ───────────────────────────────────────────────────────── */
.su-footer {
  display: flex;
  justify-content: flex-end;
}

.su-footer-actions {
  display: flex;
  gap: 8px;
}

.su-btn {
  padding: 10px 24px;
  border-radius: 10px;
  border: none;
  font-size: 0.9rem;
  font-weight: 500;
  cursor: pointer;
  transition: opacity 0.15s;
}
.su-btn:hover { opacity: 0.85; }

.su-btn-primary   { background: #7c9cfc; color: #0a0a1a; }
.su-btn-secondary { background: rgba(255,255,255,0.06); color: #888; }
</style>
