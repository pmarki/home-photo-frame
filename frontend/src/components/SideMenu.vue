<template>
  <div
    class="sm-overlay"
    :class="{ 'sm-visible': visible }"
    role="dialog"
    aria-modal="true"
    aria-label="Menu"
    @click.self="requestClose"
  >
    <aside class="sm-drawer">
      <header class="sm-header">
        <h2 class="sm-title">Menu</h2>
        <button class="sm-close" aria-label="Close menu" @click="requestClose">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="18" y1="6" x2="6" y2="18"/>
            <line x1="6" y1="6" x2="18" y2="18"/>
          </svg>
        </button>
      </header>

      <button class="sm-action sm-upload" @click="fileInput.click()">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/>
          <polyline points="17 8 12 3 7 8"/>
          <line x1="12" y1="3" x2="12" y2="15"/>
        </svg>
        <span>Upload photos</span>
      </button>
      <input
        ref="fileInput"
        type="file"
        multiple
        :accept="videoEnabled ? 'image/*,video/*' : 'image/*'"
        style="display:none"
        @change="onFilesSelected"
      />

      <nav ref="foldersEl" class="sm-folders">
        <button
          class="sm-all"
          :class="{ 'sm-all-active': !folder }"
          @click="select('')"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
            <rect x="3" y="3" width="7" height="7" rx="1"/>
            <rect x="14" y="3" width="7" height="7" rx="1"/>
            <rect x="3" y="14" width="7" height="7" rx="1"/>
            <rect x="14" y="14" width="7" height="7" rx="1"/>
          </svg>
          <span>All photos</span>
        </button>

        <div v-if="loadingFolders" class="sm-folders-state">Loading…</div>
        <div v-else-if="foldersError" class="sm-folders-state sm-folders-error">
          <span>{{ foldersError }}</span>
          <button class="sm-folders-retry" @click="fetchFolders">Retry</button>
        </div>
        <div v-else-if="tree.length === 0" class="sm-folders-state">No folders</div>
        <FolderTree
          v-else
          :nodes="tree"
          :current-folder="folder"
          @select="select"
        />
      </nav>

      <button class="sm-action sm-about" @click="$emit('open-about')">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="12" r="9"/>
          <line x1="12" y1="11" x2="12" y2="17"/>
          <circle cx="12" cy="7.5" r="0.6" fill="currentColor"/>
        </svg>
        <span>About</span>
      </button>
    </aside>
  </div>
</template>

<script setup>
import { ref, onMounted, onBeforeUnmount, onUnmounted, nextTick } from 'vue'
import FolderTree from './FolderTree.vue'
import { buildFolderTree, FOLDER_ORDER } from '../composables/useFolderTree.js'
import { lockBodyOverflow, unlockBodyOverflow } from '../composables/useBodyOverflowLock.js'

// Module-level: persists between mounts of SideMenu so the folder list
// remembers its scroll position across open/close cycles.
let savedFoldersScrollTop = 0

const props = defineProps({
  folder: { type: String, default: '' },
  videoEnabled: { type: Boolean, default: false },
})

const emit = defineEmits(['close', 'upload-files', 'select-folder', 'open-about'])

const visible = ref(false)
const fileInput = ref(null)
const foldersEl = ref(null)
const tree = ref([])
const loadingFolders = ref(true)
const foldersError = ref(null)
let closeTimer = null

function requestClose() {
  if (!visible.value) return
  visible.value = false
  if (closeTimer) clearTimeout(closeTimer)
  closeTimer = setTimeout(() => emit('close'), 200)
}

function select(path) {
  emit('select-folder', path)
  requestClose()
}

function onFilesSelected(e) {
  const files = Array.from(e.target.files)
  e.target.value = ''
  if (files.length === 0) return
  emit('upload-files', files)
  requestClose()
}

function onKeydown(e) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    requestClose()
  }
}

let fetchController = null

async function fetchFolders() {
  if (fetchController) fetchController.abort()
  fetchController = new AbortController()
  const controller = fetchController
  const timer = setTimeout(() => controller.abort(), 8000)

  loadingFolders.value = true
  foldersError.value = null
  try {
    const res = await fetch('/api/folders?order=' + FOLDER_ORDER, { signal: controller.signal })
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    tree.value = buildFolderTree(data.folders ?? [], FOLDER_ORDER)
  } catch (e) {
    if (e.name === 'AbortError') foldersError.value = 'Request timed out'
    else foldersError.value = 'Failed to load folders'
  } finally {
    clearTimeout(timer)
    if (fetchController === controller) fetchController = null
    loadingFolders.value = false
    await nextTick()
    if (foldersEl.value) foldersEl.value.scrollTop = savedFoldersScrollTop
  }
}

onMounted(async () => {
  lockBodyOverflow()
  document.addEventListener('keydown', onKeydown)
  fetchFolders()
  await nextTick()
  requestAnimationFrame(() => { visible.value = true })
})

onBeforeUnmount(() => {
  if (foldersEl.value) savedFoldersScrollTop = foldersEl.value.scrollTop
})

onUnmounted(() => {
  unlockBodyOverflow()
  document.removeEventListener('keydown', onKeydown)
  if (closeTimer) clearTimeout(closeTimer)
  if (fetchController) fetchController.abort()
})
</script>

<style scoped>
.sm-overlay {
  position: fixed;
  inset: 0;
  z-index: 9500;
  background: rgba(0, 0, 0, 0);
  transition: background 0.2s ease;
}
.sm-overlay.sm-visible {
  background: rgba(0, 0, 0, 0.55);
}

.sm-drawer {
  position: absolute;
  top: 0;
  left: 0;
  bottom: 0;
  width: 300px;
  max-width: 100vw;
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 35%, black);
  border-right: 1px solid color-mix(in srgb, var(--bg-color, #0a0a0f) 80%, white);
  display: flex;
  flex-direction: column;
  padding: 12px 12px calc(12px + env(safe-area-inset-bottom)) 12px;
  padding-top: calc(12px + env(safe-area-inset-top));
  transform: translateX(-100%);
  transition: transform 0.2s ease;
  box-shadow: 4px 0 24px rgba(0, 0, 0, 0.4);
}
.sm-visible .sm-drawer {
  transform: translateX(0);
}

@media (max-width: 600px) {
  .sm-drawer { width: 280px; }
}
@media (max-width: 480px) {
  .sm-drawer { width: 100vw; border-right: none; }
}

.sm-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 4px 4px 12px 8px;
  flex-shrink: 0;
}

.sm-title {
  font-size: 1rem;
  font-weight: 600;
  color: #fff;
  margin: 0;
}

.sm-close {
  width: 32px;
  height: 32px;
  border: none;
  background: transparent;
  color: #888;
  cursor: pointer;
  border-radius: 6px;
  display: flex;
  align-items: center;
  justify-content: center;
}
.sm-close:hover { color: #ccc; background: rgba(255,255,255,0.06); }
.sm-close svg { width: 18px; height: 18px; }

.sm-action {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 10px 12px;
  border: 1px solid rgba(255, 255, 255, 0.08);
  background: rgba(255, 255, 255, 0.02);
  color: #e0e0e8;
  border-radius: 8px;
  cursor: pointer;
  font: inherit;
  font-size: 0.9rem;
  text-align: left;
  transition: all 0.15s;
  flex-shrink: 0;
}
.sm-action svg { width: 18px; height: 18px; color: #888; }
.sm-action:hover {
  border-color: rgba(255, 255, 255, 0.18);
  background: rgba(255, 255, 255, 0.05);
}
.sm-action:hover svg { color: #ccc; }

.sm-upload { margin-bottom: 12px; }
.sm-about { margin-top: 12px; }

.sm-folders {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 4px 0;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}

.sm-all {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 6px 8px;
  margin: 4px 0 6px 0;
  border: none;
  background: transparent;
  color: #d0d0d8;
  border-radius: 6px;
  cursor: pointer;
  font: inherit;
  font-size: 0.9rem;
  text-align: left;
}
.sm-all svg { width: 16px; height: 16px; color: #8a8a98; }
.sm-all:hover { background: rgba(255,255,255,0.04); }
.sm-all-active {
  background: rgba(100, 120, 220, 0.18);
  color: #c0caff;
}
.sm-all-active svg { color: #c0caff; }

.sm-folders-state {
  padding: 12px 8px;
  font-size: 0.85rem;
  color: #888;
  display: flex;
  align-items: center;
  gap: 10px;
  flex-wrap: wrap;
}
.sm-folders-error { color: #f87171; }
.sm-folders-retry {
  padding: 4px 10px;
  border: 1px solid rgba(255, 255, 255, 0.18);
  background: rgba(255, 255, 255, 0.04);
  color: #e0e0e8;
  font: inherit;
  font-size: 0.8rem;
  border-radius: 6px;
  cursor: pointer;
}
.sm-folders-retry:hover {
  background: rgba(255, 255, 255, 0.08);
  border-color: rgba(255, 255, 255, 0.28);
}
</style>
