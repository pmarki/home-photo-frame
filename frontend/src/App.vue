<template>
  <div class="app">
    <div class="app-top">
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

      <button
        v-if="filterButtonVisible"
        class="sort-icon-btn header-filter-btn"
        :class="{ active: filtersOpen || hasActiveFilters }"
        :title="filtersOpen ? 'Hide filters' : 'Show filters'"
        aria-label="Toggle filters"
        :aria-expanded="filtersOpen"
        @click="filtersOpen = !filtersOpen"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <polygon points="3 4 21 4 14 12 14 20 10 18 10 12 3 4"/>
        </svg>
      </button>
      <ViewModeToggle class="header-view-mode" :mode="viewMode" @change="setViewMode" />
    </header>

    <FilterBar
      v-if="filtersOpen"
      :users="usersList"
      :current-user-id="userId"
      :video-enabled="videoEnabled"
      :available-years="availableYears"
      :value="filters"
      @change="onFilterChange"
      @clear="onFilterClear"
    />
    </div>

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
        @request-menu="onRequestMenu"
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
        @request-menu="onRequestMenu"
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
        :user-name="currentUserName"
        @close="closeAbout"
        @logout="onLogout"
      />
      <ShareUploader
        v-if="shareUploaderVisible"
        @done="onShareDone"
      />
      <UploadDialog
        v-if="uploadFiles"
        :files="uploadFiles"
        :destination="uploadDestination"
        @done="onUploadDone"
      />
      <PostUploadCropQueue
        v-if="cropQueue"
        :images="cropQueue"
        @done="onCropQueueDone"
      />
      <ImageContextMenu
        v-if="contextMenu"
        :x="contextMenu.x"
        :y="contextMenu.y"
        @close="contextMenu = null"
        @open-new-tab="onMenuOpenNewTab"
        @save="onMenuSave"
        @share="onMenuShare"
        @select="onMenuSelect"
      />
      <SelectionToolbar
        v-if="selection.selectMode.value"
        :count="selection.selectedPaths.value.size"
        @cancel="selection.exitSelectMode()"
        @share="onSelectionShare"
        @delete="onSelectionDelete"
        @copy="onSelectionCopy"
        @move="onSelectionMove"
      />
      <FolderChooser
        v-if="chooser"
        :title="chooser.title"
        :confirm-label="chooser.confirmLabel"
        :initial-folder="chooser.initialFolder"
        @close="chooser = null"
        @confirm="onChooserConfirm"
      />
      <UserSelectModal
        v-if="showUserModal"
        :users="usersList"
        @select="onUserSelect"
      />
      <div class="toast-container">
        <div v-for="toast in toasts" :key="toast.id" class="toast">{{ toast.message }}</div>
      </div>
    </Teleport>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import GalleryGrid from './components/GalleryGrid.vue'
import LightboxModal from './components/LightboxModal.vue'
import ShareUploader from './components/ShareUploader.vue'
import UploadDialog from './components/UploadDialog.vue'
import PostUploadCropQueue from './components/PostUploadCropQueue.vue'
import ViewModeToggle from './components/ViewModeToggle.vue'
import FilterBar from './components/FilterBar.vue'
import SideMenu from './components/SideMenu.vue'
import AboutModal from './components/AboutModal.vue'
import ImageContextMenu from './components/ImageContextMenu.vue'
import SelectionToolbar from './components/SelectionToolbar.vue'
import FolderChooser from './components/FolderChooser.vue'
import UserSelectModal from './components/UserSelectModal.vue'
import { useGallery } from './composables/useGallery.js'
import { useImageSelection } from './composables/useImageSelection.js'
import { useUser } from './composables/useUser.js'

function mixWithBlack(hex, pct) {
  let h = String(hex || '').trim().replace('#', '')
  if (h.length === 3) h = h.split('').map(c => c + c).join('')
  if (h.length !== 6) return hex
  const r = Math.round(parseInt(h.slice(0, 2), 16) * pct)
  const g = Math.round(parseInt(h.slice(2, 4), 16) * pct)
  const b = Math.round(parseInt(h.slice(4, 6), 16) * pct)
  return '#' + [r, g, b].map(v => v.toString(16).padStart(2, '0')).join('')
}

const { images, total, loading, error, hasMore, viewMode, folder, filters, loadNextPage, setViewMode, setFolder, setFilters, removeImage, replaceImage, forceReload } = useGallery()
const { userId, setUser, clearUser } = useUser()
const showUserModal = ref(false)
const usersList = ref([])
let usersEnabled = false
const currentUserName = computed(() => {
  if (!userId.value) return ''
  const u = usersList.value.find(x => x.id === userId.value)
  return u?.name || userId.value
})

const filtersOpen = ref(false)
const hasActiveFilters = computed(() => !!filters.value.owner || !!filters.value.year || !!filters.value.type)
const filterButtonVisible = computed(() => usersList.value.length > 0 || videoEnabled.value || images.value.length > 0)
const availableYears = computed(() => {
  const seen = new Set()
  for (const img of images.value) {
    if (!img?.modTime) continue
    const y = new Date(img.modTime).getFullYear()
    if (Number.isFinite(y)) seen.add(y)
  }
  // Keep the currently-selected year visible even if no images of that year
  // are currently loaded (e.g. landing on ?year=2023 narrows the result set).
  if (filters.value.year) seen.add(filters.value.year)
  return Array.from(seen).sort((a, b) => b - a)
})

function onFilterChange(partial) {
  if (loading.value) return
  setFilters(partial)
}

function onFilterClear() {
  setFilters({ owner: '', year: 0, type: '' })
}

// Keep the bar open whenever a filter is active so the user can see what's
// applied without having to retoggle the funnel.
watch(hasActiveFilters, (active) => { if (active) filtersOpen.value = true })

const modalOpen = ref(false)
const modalIndex = ref(0)
const shareUploaderVisible = ref(false)
const uploadFiles = ref(null)
const cropQueue = ref(null)  // [{filename}] — shown after upload when non-null
const sideMenuOpen = ref(false)
const aboutOpen = ref(false)
const contextMenu = ref(null) // { image, x, y } | null
const chooser = ref(null) // { mode, title, confirmLabel, initialFolder, pendingFiles? } | null
const uploadDestination = ref('')
const selection = useImageSelection()
const canShare = typeof navigator !== 'undefined' && typeof navigator.share === 'function'
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

function onRequestMenu(payload) {
  contextMenu.value = payload
}

function onMenuOpenNewTab() {
  if (!contextMenu.value) return
  window.open(contextMenu.value.image.original, '_blank', 'noopener,noreferrer')
  contextMenu.value = null
}

function onMenuSave() {
  if (!contextMenu.value) return
  const img = contextMenu.value.image
  const a = document.createElement('a')
  a.href = img.original
  a.download = img.filename
  document.body.appendChild(a)
  a.click()
  a.remove()
  contextMenu.value = null
}

async function onMenuShare() {
  if (!contextMenu.value) return
  const img = contextMenu.value.image
  contextMenu.value = null
  const absoluteUrl = window.location.origin + img.original
  try {
    if (canShare) {
      const res = await fetch(img.original)
      const blob = await res.blob()
      const file = new File([blob], img.filename, { type: blob.type })
      if (navigator.canShare?.({ files: [file] })) {
        await navigator.share({ files: [file], title: img.filename })
        return
      }
      await navigator.share({ title: img.filename, url: absoluteUrl })
    } else {
      await navigator.clipboard.writeText(absoluteUrl)
      showToast('Link copied')
    }
  } catch (e) {
    if (e.name !== 'AbortError') showToast('Share failed')
  }
}

async function onSelectionDelete() {
  const paths = Array.from(selection.selectedPaths.value)
  if (paths.length === 0) return
  const label = paths.length === 1 ? '1 photo' : paths.length + ' photos'
  if (!window.confirm('Delete ' + label + '? This cannot be undone.')) return
  try {
    const res = await fetch('/api/delete', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ paths }),
    })
    if (!res.ok) throw new Error('Server error ' + res.status)
    const data = await res.json()
    for (const p of data.deleted ?? []) removeImage(p)
    selection.exitSelectMode()
    const okCount = (data.deleted ?? []).length
    const failCount = (data.failed ?? []).length
    if (failCount === 0) showToast('Deleted ' + okCount)
    else showToast('Deleted ' + okCount + ', ' + failCount + ' failed')
  } catch (e) {
    showToast('Delete failed')
  }
}

async function onSelectionShare() {
  const paths = Array.from(selection.selectedPaths.value)
  if (paths.length === 0) return
  const imgs = paths
    .map(p => images.value.find(i => i.path === p))
    .filter(Boolean)
  if (imgs.length === 0) {
    showToast('Share failed')
    return
  }
  const urlList = imgs.map(i => window.location.origin + i.original).join('\n')
  try {
    if (canShare) {
      const files = await Promise.all(imgs.map(async (img) => {
        const res = await fetch(img.original)
        const blob = await res.blob()
        return new File([blob], img.filename, { type: blob.type })
      }))
      if (navigator.canShare?.({ files })) {
        await navigator.share({ files, title: imgs.length + ' photos' })
        return
      }
      await navigator.share({ title: imgs.length + ' photos', text: urlList })
    } else {
      await navigator.clipboard.writeText(urlList)
      showToast(imgs.length === 1 ? 'Link copied' : imgs.length + ' links copied')
    }
  } catch (e) {
    if (e.name !== 'AbortError') showToast('Share failed')
  }
}

function onMenuSelect() {
  if (!contextMenu.value) return
  const img = contextMenu.value.image
  contextMenu.value = null
  if (modalOpen.value) {
    modalOpen.value = false
    if (history.state?.modal === 'lightbox') history.back()
  }
  selection.enterSelectMode(img.path)
}

function onGlobalKeydown(e) {
  if (e.key !== 'Escape') return
  if (contextMenu.value) return
  if (modalOpen.value || sideMenuOpen.value || aboutOpen.value) return
  if (selection.selectMode.value) {
    e.stopPropagation()
    selection.exitSelectMode()
  }
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
  chooser.value = {
    mode: 'upload',
    title: 'Upload to…',
    confirmLabel: 'Upload',
    initialFolder: folder.value,
    pendingFiles: files,
  }
}

function onSelectionCopy() {
  if (selection.selectedPaths.value.size === 0) return
  chooser.value = {
    mode: 'copy',
    title: 'Copy to…',
    confirmLabel: 'Copy',
    initialFolder: '',
  }
}

function onSelectionMove() {
  if (selection.selectedPaths.value.size === 0) return
  chooser.value = {
    mode: 'move',
    title: 'Move to…',
    confirmLabel: 'Move',
    initialFolder: '',
  }
}

async function onChooserConfirm(destination) {
  const c = chooser.value
  if (!c) return
  chooser.value = null
  if (c.mode === 'upload') {
    const files = c.pendingFiles
    const existingPaths = new Set(images.value.map(img => img.path))
    const target = (name) => destination ? destination + '/' + name : name
    const duplicates = files.filter(f => existingPaths.has(target(f.name)))
    const unique = files.filter(f => !existingPaths.has(target(f.name)))
    if (duplicates.length > 0) {
      const label = duplicates.length === 1
        ? `"${duplicates[0].name}" already exists`
        : `${duplicates.length} files already exist`
      showToast(label)
    }
    if (unique.length > 0) {
      uploadDestination.value = destination
      uploadFiles.value = unique
      history.pushState({ modal: 'upload' }, '')
    }
  } else if (c.mode === 'copy' || c.mode === 'move') {
    await performCopyMove(c.mode, destination)
  }
}

async function performCopyMove(action, destination) {
  const paths = Array.from(selection.selectedPaths.value)
  if (paths.length === 0) return
  try {
    const res = await fetch('/api/' + action, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ paths, destination }),
    })
    if (!res.ok) throw new Error('Server error ' + res.status)
    const data = await res.json()
    const okList = action === 'copy' ? (data.copied ?? []) : (data.moved ?? [])
    const failCount = (data.failed ?? []).length
    if (action === 'move') {
      for (const item of okList) removeImage(item.from)
    }
    selection.exitSelectMode()
    forceReload()
    const verb = action === 'copy' ? 'Copied' : 'Moved'
    if (failCount === 0) showToast(verb + ' ' + okList.length)
    else showToast(verb + ' ' + okList.length + ', ' + failCount + ' failed')
  } catch (e) {
    showToast(action === 'copy' ? 'Copy failed' : 'Move failed')
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

async function loadUsersList() {
  try {
    const res = await fetch('/api/users')
    if (!res.ok) return
    const data = await res.json()
    usersList.value = data.users ?? []
  } catch {}
}

async function showUserPicker() {
  await loadUsersList()
  if (usersList.value.length > 0) showUserModal.value = true
}

function onUserSelect(id) {
  setUser(id)
  showUserModal.value = false
  // The previously selected folder may not exist for the new user. Reset to
  // root before loading so we don't 403 on a stale folder=… query.
  if (folder.value !== '') {
    history.replaceState({ modal: null }, '', '/')
    setFolder('') // resets gallery state and triggers loadNextPage with new X-User
  } else {
    forceReload()
  }
}

function onLogout() {
  clearUser()
  showUserPicker()
}

function onAuthRequired() {
  showUserPicker()
}

onMounted(async () => {
  history.scrollRestoration = 'manual'
  history.replaceState({ modal: null }, '')

  try {
    const r = await fetch('/api/config')
    if (r.ok) {
      const cfg = await r.json()
      try { localStorage.setItem('app-config', JSON.stringify(cfg)) } catch {}
      if (cfg.title) { appTitle.value = cfg.title; document.title = cfg.title }
      if (cfg.videoEnabled) videoEnabled.value = true
      if (cfg.bgColor) {
        document.documentElement.style.setProperty('--bg-color', cfg.bgColor)
        const tc = document.querySelector('meta[name="theme-color"]')
        if (tc) tc.setAttribute('content', mixWithBlack(cfg.bgColor, 0.45))
      }
      titleIcon.value = cfg.titleIcon ?? false
      buildNumber.value = cfg.buildNumber ?? ''
      imageCount.value = cfg.imageCount ?? 0
      imageTotalBytes.value = cfg.imageTotalBytes ?? 0
      diskFreeBytes.value = cfg.diskFreeBytes ?? 0
      usersEnabled = cfg.usersEnabled === true
    }
  } catch {}

  const url = new URL(window.location.href)
  if (url.searchParams.has('share-pending')) {
    shareUploaderVisible.value = true
    history.pushState({ modal: 'share-uploader' }, '')
  }
  window.addEventListener('popstate', onPopState)
  window.addEventListener('keydown', onGlobalKeydown)
  window.addEventListener('auth:required', onAuthRequired)

  if (usersEnabled) {
    if (!userId.value) {
      await showUserPicker()
      return // gallery loads after user selection
    }
    loadUsersList() // preload so the About modal can show the display name
  }
  loadNextPage()
})

onUnmounted(() => {
  window.removeEventListener('popstate', onPopState)
  window.removeEventListener('keydown', onGlobalKeydown)
  window.removeEventListener('auth:required', onAuthRequired)
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

/* ─── Sticky top (header + optional filter bar) ────────────────────── */
.app-top {
  position: sticky;
  top: 0;
  z-index: 100;
}

/* ─── Header ───────────────────────────────────────────────────────── */
.app-header {
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

.header-view-mode {
  margin-left: 6px;
}

.header-filter-btn {
  margin-left: auto;
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
  z-index: 10800;
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
