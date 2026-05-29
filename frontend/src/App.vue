<template>
  <div class="app">
    <header class="app-header">
      <h1 class="app-title">
        <svg class="title-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
          <!-- frame border -->
          <rect x="2" y="2" width="20" height="20" rx="1.5"/>
          <!-- mat inset -->
          <rect x="4.5" y="4.5" width="15" height="15" rx="0.5"/>
          <!-- sun -->
          <circle cx="16" cy="8.5" r="1.5" fill="currentColor" stroke="none"/>
          <!-- mountain -->
          <polyline points="5.5,17 10,10.5 14.5,17"/>
        </svg>
        {{ appTitle }}
      </h1>

      <div class="sort-controls" ref="sortRef">
        <button class="sort-icon-btn" @click="sortOpen = !sortOpen" :class="{ active: sortOpen }" title="Sort">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="4" y1="6" x2="20" y2="6"/>
            <line x1="4" y1="12" x2="14" y2="12"/>
            <line x1="4" y1="18" x2="9" y2="18"/>
          </svg>
        </button>
        <div v-if="sortOpen" class="sort-dropdown">
          <button
            v-for="opt in sortOptions"
            :key="opt.key"
            :class="['sort-option', { active: sortBy === opt.sortBy && sortOrder === opt.order }]"
            @click="setSort(opt.sortBy, opt.order); sortOpen = false"
          >
            {{ opt.label }}
          </button>
        </div>
      </div>

      <div class="header-count" v-if="total > 0">
        {{ images.length }}&thinsp;/&thinsp;{{ total }}
      </div>

      <button class="upload-icon-btn" title="Upload photos" @click="fileInput.click()">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/>
          <polyline points="17 8 12 3 7 8"/>
          <line x1="12" y1="3" x2="12" y2="15"/>
        </svg>
      </button>
      <input
        ref="fileInput"
        type="file"
        multiple
        :accept="videoEnabled ? 'image/*,video/*' : 'image/*'"
        style="display:none"
        @change="onFilesSelected"
      />
    </header>

    <main>
      <GalleryGrid
        :images="images"
        :loading="loading"
        :has-more="hasMore"
        @load-more="loadNextPage"
        @open="openModal"
      />

      <div v-if="error" class="error-notice">
        Failed to load photos: {{ error }}
        <button class="retry-btn" @click="loadNextPage">Retry</button>
      </div>
    </main>

    <Teleport to="body">
      <LightboxModal
        v-if="modalOpen"
        :images="images"
        :initial-index="modalIndex"
        :has-more="hasMore"
        @close="closeModal"
        @need-more="loadNextPage"
        @deleted="onDeleted"
        @cropped="onCropped"
      />
      <ShareUploader
        v-if="shareUploaderVisible"
        @done="onShareDone"
      />
      <UploadDialog
        v-if="uploadFiles"
        :files="uploadFiles"
        @done="onUploadDone"
      />
      <PostUploadCropQueue
        v-if="cropQueue"
        :images="cropQueue"
        @done="onCropQueueDone"
      />
      <div class="toast-container">
        <div v-for="toast in toasts" :key="toast.id" class="toast">{{ toast.message }}</div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted } from 'vue'
import GalleryGrid from './components/GalleryGrid.vue'
import LightboxModal from './components/LightboxModal.vue'
import ShareUploader from './components/ShareUploader.vue'
import UploadDialog from './components/UploadDialog.vue'
import PostUploadCropQueue from './components/PostUploadCropQueue.vue'
import { useGallery } from './composables/useGallery.js'

const { images, total, loading, error, hasMore, sortBy, sortOrder, loadNextPage, setSort, removeImage, replaceImage, forceReload } =
  useGallery()

const modalOpen = ref(false)
const modalIndex = ref(0)
const modalHadCrop = ref(false)
const shareUploaderVisible = ref(false)
const uploadFiles = ref(null)
const cropQueue = ref(null)  // [{filename}] — shown after upload when non-null
const fileInput = ref(null)
const sortOpen = ref(false)
const toasts = ref([])
let toastSeq = 0
const _storedCfg = (() => { try { return JSON.parse(localStorage.getItem('app-config') || 'null') } catch { return null } })()
const videoEnabled = ref(_storedCfg?.videoEnabled ?? false)

const isVideo = (filename) => /\.(mp4|webm|mov|m4v)$/i.test(filename ?? '')
const sortRef = ref(null)
const appTitle = ref(_storedCfg?.title || 'Photo Frame')

function onClickOutside(e) {
  if (sortRef.value && !sortRef.value.contains(e.target)) sortOpen.value = false
}

const sortOptions = [
  { key: 'taken-desc',  label: 'Date taken ↓',     sortBy: 'taken', order: 'desc' },
  { key: 'taken-asc',   label: 'Date taken ↑',     sortBy: 'taken', order: 'asc'  },
  { key: 'mtime-desc',  label: 'Date modified ↓',  sortBy: 'mtime', order: 'desc' },
  { key: 'mtime-asc',   label: 'Date modified ↑',  sortBy: 'mtime', order: 'asc'  },
  { key: 'name-asc',    label: 'Name ↑',           sortBy: 'name',  order: 'asc'  },
  { key: 'name-desc',   label: 'Name ↓',           sortBy: 'name',  order: 'desc' },
]

function openModal(index) {
  modalIndex.value = index
  modalHadCrop.value = false
  modalOpen.value = true
}

function closeModal() {
  modalOpen.value = false
  if (modalHadCrop.value) {
    modalHadCrop.value = false
    forceReload()
  }
}

function showToast(message) {
  const id = ++toastSeq
  toasts.value.push({ id, message })
  setTimeout(() => { toasts.value = toasts.value.filter(t => t.id !== id) }, 4000)
}

function onFilesSelected(e) {
  const files = Array.from(e.target.files)
  e.target.value = ''
  if (files.length === 0) return

  const existingNames = new Set(images.value.map(img => img.filename))
  const duplicates = files.filter(f => existingNames.has(f.name))
  const unique = files.filter(f => !existingNames.has(f.name))

  if (duplicates.length > 0) {
    const label = duplicates.length === 1
      ? `"${duplicates[0].name}" already exists`
      : `${duplicates.length} files already exist`
    showToast(label)
  }

  if (unique.length > 0) uploadFiles.value = unique
}

function onUploadDone(uploadedImages) {
  uploadFiles.value = null
  const croppable = uploadedImages?.filter(i => !isVideo(i.filename)) ?? []
  if (croppable.length > 0) {
    cropQueue.value = croppable
  } else {
    forceReload()
  }
}

function onDeleted(filename) {
  removeImage(filename)
  if (images.value.length === 0) modalOpen.value = false
}

function onCropped(oldFilename, newImage) {
  replaceImage(oldFilename, newImage)  // immediate update in the still-open lightbox
  modalHadCrop.value = true
}

function onShareDone(uploadedImages) {
  shareUploaderVisible.value = false
  const url = new URL(window.location.href)
  url.searchParams.delete('share-pending')
  history.replaceState({}, '', url)
  const croppable = uploadedImages?.filter(i => !isVideo(i.filename)) ?? []
  if (croppable.length > 0) {
    cropQueue.value = croppable
  } else {
    forceReload()
  }
}

function onCropQueueDone() {
  cropQueue.value = null
  forceReload()
}

onMounted(() => {
  fetch('/api/config')
    .then(r => r.ok ? r.json() : null)
    .then(cfg => {
      if (!cfg) return
      try { localStorage.setItem('app-config', JSON.stringify(cfg)) } catch {}
      if (cfg.title) { appTitle.value = cfg.title; document.title = cfg.title }
      if (cfg.videoEnabled) videoEnabled.value = true
      if (cfg.bgColor) document.documentElement.style.setProperty('--bg-color', cfg.bgColor)
    })
    .catch(() => {})

  document.addEventListener('click', onClickOutside, true)
  const url = new URL(window.location.href)
  if (url.searchParams.has('share-pending')) {
    shareUploaderVisible.value = true
  }
  loadNextPage()
})

onUnmounted(() => {
  document.removeEventListener('click', onClickOutside, true)
})
</script>

<style>
/* ─── Global reset ─────────────────────────────────────────────────── */
*, *::before, *::after { box-sizing: border-box; margin: 0; padding: 0; }

html { height: 100%; }

body {
  background: var(--bg-color, #0a0a0f);
  color: #e0e0e8;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  min-height: 100%;
  -webkit-font-smoothing: antialiased;
}

/* ─── App layout ───────────────────────────────────────────────────── */
.app {
  max-width: 1800px;
  margin: 0 auto;
  padding: 0 12px;
}

/* ─── Header ───────────────────────────────────────────────────────── */
.app-header {
  position: sticky;
  top: 0;
  z-index: 100;
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 45%, black);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border-bottom: 1px solid color-mix(in srgb, var(--bg-color, #0a0a0f) 80%, white);
  padding: 10px 12px;
  margin: 0 -12px;
  display: flex;
  align-items: center;
  gap: 20px;
  flex-wrap: wrap;
}

.app-title {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 1.15rem;
  font-weight: 600;
  color: #fff;
  white-space: nowrap;
  flex-shrink: 0;
}

.title-icon {
  width: 22px;
  height: 22px;
  opacity: 0.8;
}

.sort-controls {
  position: relative;
  flex: 1;
}

.sort-icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: 8px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  background: transparent;
  color: #888;
  cursor: pointer;
  transition: all 0.15s;
}
.sort-icon-btn svg { width: 18px; height: 18px; }
.sort-icon-btn:hover { border-color: rgba(255,255,255,0.25); color: #ccc; }
.sort-icon-btn.active { border-color: rgba(100,120,220,0.6); color: #c0caff; background: rgba(100,120,220,0.15); }

.sort-dropdown {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: 200;
  background: #16161f;
  border: 1px solid rgba(255,255,255,0.1);
  border-radius: 10px;
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 2px;
  min-width: 130px;
  box-shadow: 0 8px 24px rgba(0,0,0,0.5);
}

.sort-option {
  padding: 7px 14px;
  border-radius: 6px;
  border: none;
  background: transparent;
  color: #aaa;
  font-size: 0.85rem;
  cursor: pointer;
  text-align: left;
  transition: all 0.1s;
  white-space: nowrap;
}
.sort-option:hover { background: rgba(255,255,255,0.06); color: #ddd; }
.sort-option.active { background: rgba(100,120,220,0.2); color: #c0caff; }

.header-count {
  font-size: 0.78rem;
  color: #444;
  white-space: nowrap;
  flex-shrink: 0;
}

.upload-icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 34px;
  height: 34px;
  border-radius: 8px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  background: transparent;
  color: #888;
  cursor: pointer;
  flex-shrink: 0;
  transition: all 0.15s;
}
.upload-icon-btn svg { width: 18px; height: 18px; }
.upload-icon-btn:hover { border-color: rgba(255,255,255,0.25); color: #ccc; }

/* ─── Error notice ─────────────────────────────────────────────────── */
.error-notice {
  text-align: center;
  padding: 32px;
  color: #f87171;
  font-size: 0.9rem;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 12px;
}

.retry-btn {
  padding: 6px 20px;
  border-radius: 6px;
  border: 1px solid rgba(248, 113, 113, 0.4);
  background: transparent;
  color: #f87171;
  cursor: pointer;
  font-size: 0.85rem;
}
.retry-btn:hover { background: rgba(248, 113, 113, 0.1); }

/* ─── Toasts ───────────────────────────────────────────────────────── */
.toast-container {
  position: fixed;
  bottom: calc(24px + env(safe-area-inset-bottom));
  left: 50%;
  transform: translateX(-50%);
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  z-index: 9999;
  pointer-events: none;
}

.toast {
  background: #2a2a38;
  color: #f0e08a;
  border: 1px solid rgba(240, 224, 138, 0.25);
  border-radius: 10px;
  padding: 10px 18px;
  font-size: 0.85rem;
  white-space: nowrap;
  box-shadow: 0 4px 20px rgba(0, 0, 0, 0.5);
  animation: toast-in 0.2s ease;
}

@keyframes toast-in {
  from { opacity: 0; transform: translateY(8px); }
  to   { opacity: 1; transform: translateY(0); }
}
</style>
