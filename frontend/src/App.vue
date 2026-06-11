<template>
  <div class="app">
    <header class="app-header">
      <h1 class="app-title">
        <button class="title-icon-btn" aria-label="Open menu" @click="openSideMenu">
          <img v-if="titleIcon" :src="'/icons/favicon.svg'" class="title-icon" alt="" />
          <svg v-else class="title-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
            <!-- frame border -->
            <rect x="2" y="2" width="20" height="20" rx="1.5"/>
            <!-- mat inset -->
            <rect x="4.5" y="4.5" width="15" height="15" rx="0.5"/>
            <!-- sun -->
            <circle cx="16" cy="8.5" r="1.5" fill="currentColor" stroke="none"/>
            <!-- mountain -->
            <polyline points="5.5,17 10,10.5 14.5,17"/>
          </svg>
        </button>
        {{ appTitle }}
      </h1>

      <ViewModeToggle :mode="viewMode" @change="setViewMode" />
    </header>

    <main>
      <nav v-if="folder" class="breadcrumb" aria-label="Folder breadcrumb">
        <button class="breadcrumb-item" @click="onSelectFolder('')">All photos</button>
        <template v-for="(seg, i) in folderSegments" :key="i">
          <span class="breadcrumb-sep" aria-hidden="true">›</span>
          <button
            class="breadcrumb-item"
            :class="{ 'breadcrumb-current': i === folderSegments.length - 1 }"
            :disabled="i === folderSegments.length - 1"
            @click="onSelectFolder(folderSegments.slice(0, i + 1).join('/'))"
          >{{ seg }}</button>
        </template>
      </nav>

      <GalleryGrid
        :images="images"
        :total="total"
        :loading="loading"
        :viewMode="viewMode"
        @open="openModal"
      />

      <div v-if="error" class="error-notice">
        Failed to load photos: {{ error }}
        <button class="retry-btn" @click="loadNextPage">Retry</button>
      </div>
    </main>

    <Teleport to="body">
      <LightboxModal
        ref="lightboxRef"
        v-if="modalOpen"
        :images="images"
        :initial-index="modalIndex"
        :has-more="hasMore"
        @close="closeModal"
        @need-more="loadNextPage"
        @deleted="onDeleted"
        @cropped="onCropped"
      />
      <SideMenu
        v-if="sideMenuOpen"
        :folder="folder"
        :video-enabled="videoEnabled"
        @close="closeSideMenu"
        @upload-files="onUploadFiles"
        @select-folder="onSelectFolder"
        @open-about="openAbout"
      />
      <AboutModal
        v-if="aboutOpen"
        :title="appTitle"
        :title-icon="titleIcon"
        :video-enabled="videoEnabled"
        :build-number="buildNumber"
        :image-count="imageCount"
        :image-total-bytes="imageTotalBytes"
        :disk-free-bytes="diskFreeBytes"
        @close="closeAbout"
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
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import GalleryGrid from './components/GalleryGrid.vue'
import LightboxModal from './components/LightboxModal.vue'
import ShareUploader from './components/ShareUploader.vue'
import UploadDialog from './components/UploadDialog.vue'
import PostUploadCropQueue from './components/PostUploadCropQueue.vue'
import ViewModeToggle from './components/ViewModeToggle.vue'
import SideMenu from './components/SideMenu.vue'
import AboutModal from './components/AboutModal.vue'
import { useGallery } from './composables/useGallery.js'

const { images, total, loading, error, hasMore, viewMode, folder, loadNextPage, setViewMode, setFolder, removeImage, replaceImage, forceReload } = useGallery()

const modalOpen = ref(false)
const modalIndex = ref(0)
const shareUploaderVisible = ref(false)
const uploadFiles = ref(null)
const cropQueue = ref(null)  // [{filename}] — shown after upload when non-null
const sideMenuOpen = ref(false)
const aboutOpen = ref(false)
let savedScrollY = 0         // gallery scroll position saved when lightbox opens
let pendingFolderClose = false // suppresses history.back() in closeSideMenu when the sidebar is closing due to a folder pick (URL already updated)
const lightboxRef = ref(null)
const toasts = ref([])
let toastSeq = 0
const _storedCfg = (() => { try { return JSON.parse(localStorage.getItem('app-config') || 'null') } catch { return null } })()
const videoEnabled = ref(_storedCfg?.videoEnabled ?? false)
const titleIcon = ref(_storedCfg?.titleIcon ?? false)
const buildNumber = ref(_storedCfg?.buildNumber ?? '')
const imageCount = ref(_storedCfg?.imageCount ?? 0)
const imageTotalBytes = ref(_storedCfg?.imageTotalBytes ?? 0)
const diskFreeBytes = ref(_storedCfg?.diskFreeBytes ?? 0)

const isVideo = (filename) => /\.(mp4|webm|mov|m4v)$/i.test(filename ?? '')
const appTitle = ref(_storedCfg?.title || 'Photo Frame')
const folderSegments = computed(() => folder.value ? folder.value.split('/') : [])

function openModal(index) {
  savedScrollY = window.scrollY
  modalIndex.value = index
  modalOpen.value = true
  history.pushState({ modal: 'lightbox', index }, '')
}

async function closeModal() {
  modalOpen.value = false
  if (history.state?.modal === 'lightbox') history.back()
  await nextTick()
  window.scrollTo(0, savedScrollY)
}

function onPopState(e) {
  const modal = e.state?.modal ?? null
  if (modal !== null) return

  if (aboutOpen.value) {
    aboutOpen.value = false
  } else if (sideMenuOpen.value) {
    sideMenuOpen.value = false
  } else if (modalOpen.value) {
    const cropResult = lightboxRef.value?.tryExitCrop()
    if (cropResult) {
      history.pushState({ modal: 'lightbox' }, '')
      return
    }
    modalOpen.value = false
    nextTick().then(() => window.scrollTo(0, savedScrollY))
  } else if (cropQueue.value !== null) {
    cropQueue.value = null
    forceReload()
  } else if (uploadFiles.value !== null) {
    uploadFiles.value = null
    forceReload()
  } else if (shareUploaderVisible.value) {
    shareUploaderVisible.value = false
    const url = new URL(window.location.href)
    url.searchParams.delete('share-pending')
    history.replaceState({ modal: null }, '', url)
    forceReload()
  } else {
    const m = window.location.pathname.match(/^\/folder\/(.+?)\/?$/)
    const urlFolder = m ? decodeURI(m[1]) : ''
    if (folder.value !== urlFolder) {
      window.scrollTo(0, 0)
      setFolder(urlFolder)
    }
  }
}

function openSideMenu() {
  sideMenuOpen.value = true
  history.pushState({ modal: 'sidemenu' }, '')
}

function closeSideMenu() {
  sideMenuOpen.value = false
  if (pendingFolderClose) {
    pendingFolderClose = false
    return
  }
  if (history.state?.modal === 'sidemenu') history.back()
}

function openAbout() {
  aboutOpen.value = true
  history.pushState({ modal: 'about' }, '')
}

function closeAbout() {
  aboutOpen.value = false
  if (history.state?.modal === 'about') history.back()
}

function onSelectFolder(path) {
  const target = path || ''
  if (folder.value === target) return
  const newPath = target ? '/folder/' + encodeURI(target) : '/'
  history.replaceState({ modal: null }, '', newPath + window.location.search + window.location.hash)
  if (sideMenuOpen.value) pendingFolderClose = true
  window.scrollTo(0, 0)
  setFolder(target)
}

function showToast(message) {
  const id = ++toastSeq
  toasts.value.push({ id, message })
  setTimeout(() => { toasts.value = toasts.value.filter(t => t.id !== id) }, 4000)
}

function onUploadFiles(files) {
  if (!files || files.length === 0) return

  const existingPaths = new Set(images.value.map(img => img.path))
  const duplicates = files.filter(f => existingPaths.has(f.name))
  const unique = files.filter(f => !existingPaths.has(f.name))

  if (duplicates.length > 0) {
    const label = duplicates.length === 1
      ? `"${duplicates[0].name}" already exists`
      : `${duplicates.length} files already exist`
    showToast(label)
  }

  if (unique.length > 0) {
    uploadFiles.value = unique
    history.pushState({ modal: 'upload' }, '')
  }
}

function onUploadDone(uploadedImages) {
  const croppable = (uploadedImages ?? []).filter(i => !isVideo(i.filename))
  uploadFiles.value = null
  if (croppable.length > 0) {
    history.replaceState({ modal: 'cropqueue' }, '')
    cropQueue.value = croppable
  } else {
    if (history.state?.modal === 'upload') history.back()
    forceReload()
  }
}

function onDeleted(path) {
  removeImage(path)
  if (images.value.length === 0) {
    modalOpen.value = false
    if (history.state?.modal === 'lightbox') history.back()
  }
}

function onCropped(oldPath, newImage) {
  replaceImage(oldPath, newImage)
}

function onShareDone(uploadedImages) {
  shareUploaderVisible.value = false
  const url = new URL(window.location.href)
  url.searchParams.delete('share-pending')
  history.replaceState({ modal: null }, '', url)
  const croppable = (uploadedImages ?? []).filter(i => !isVideo(i.filename))
  if (croppable.length > 0) {
    cropQueue.value = croppable
    history.pushState({ modal: 'cropqueue' }, '')
  } else {
    forceReload()
  }
}

function onCropQueueDone() {
  cropQueue.value = null
  if (history.state?.modal === 'cropqueue') history.back()
  forceReload()
}

onMounted(() => {
  history.scrollRestoration = 'manual'
  history.replaceState({ modal: null }, '')

  fetch('/api/config')
    .then(r => r.ok ? r.json() : null)
    .then(cfg => {
      if (!cfg) return
      try { localStorage.setItem('app-config', JSON.stringify(cfg)) } catch {}
      if (cfg.title) { appTitle.value = cfg.title; document.title = cfg.title }
      if (cfg.videoEnabled) videoEnabled.value = true
      if (cfg.bgColor) document.documentElement.style.setProperty('--bg-color', cfg.bgColor)
      titleIcon.value = cfg.titleIcon ?? false
      buildNumber.value = cfg.buildNumber ?? ''
      imageCount.value = cfg.imageCount ?? 0
      imageTotalBytes.value = cfg.imageTotalBytes ?? 0
      diskFreeBytes.value = cfg.diskFreeBytes ?? 0
    })
    .catch(() => {})

  const url = new URL(window.location.href)
  if (url.searchParams.has('share-pending')) {
    shareUploaderVisible.value = true
    history.pushState({ modal: 'share-uploader' }, '')
  }
  window.addEventListener('popstate', onPopState)
  loadNextPage()
})

onUnmounted(() => {
  window.removeEventListener('popstate', onPopState)
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

.title-icon-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border-radius: 8px;
  border: 1px solid transparent;
  background: transparent;
  color: inherit;
  cursor: pointer;
  padding: 0;
  transition: all 0.15s;
}
.title-icon-btn:hover {
  border-color: rgba(255, 255, 255, 0.18);
  background: rgba(255, 255, 255, 0.04);
}

.title-icon {
  width: 22px;
  height: 22px;
  opacity: 0.8;
  display: block;
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

/* ─── Breadcrumb ───────────────────────────────────────────────────── */
.breadcrumb {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  padding: 10px 4px 8px;
  font-size: 0.85rem;
  color: #888;
}

.breadcrumb-item {
  background: transparent;
  border: none;
  color: #aab;
  cursor: pointer;
  padding: 2px 6px;
  border-radius: 5px;
  font: inherit;
  transition: background 0.12s, color 0.12s;
}
.breadcrumb-item:hover:not(:disabled) {
  background: rgba(255, 255, 255, 0.06);
  color: #fff;
}
.breadcrumb-item:disabled {
  cursor: default;
}

.breadcrumb-current {
  color: #fff;
  font-weight: 500;
}

.breadcrumb-sep {
  color: #555;
  font-size: 0.95em;
  user-select: none;
}

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
