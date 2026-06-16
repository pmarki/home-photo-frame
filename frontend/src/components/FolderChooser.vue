<template>
  <div
    class="fc-overlay"
    :class="{ 'fc-visible': visible }"
    role="dialog"
    aria-modal="true"
    :aria-label="title"
    @click.self="requestClose"
  >
    <div class="fc-card">
      <header class="fc-header">
        <h2 class="fc-title">{{ title }}</h2>
        <button class="fc-close" aria-label="Close" @click="requestClose">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="18" y1="6" x2="6" y2="18"/>
            <line x1="6" y1="6" x2="18" y2="18"/>
          </svg>
        </button>
      </header>

      <div ref="treeEl" class="fc-tree">
        <button
          class="fc-root"
          :class="{ 'fc-root-active': selected === '' }"
          @click="select('')"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 12l9-9 9 9"/>
            <path d="M5 10v10a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V10"/>
          </svg>
          <span>Photos root</span>
        </button>

        <div v-if="loading" class="fc-state">Loading…</div>
        <div v-else-if="error" class="fc-state fc-error">{{ error }}</div>
        <FolderTree
          v-else
          :nodes="tree"
          :current-folder="selected"
          @select="select"
        />
      </div>

      <div class="fc-new-folder">
        <input
          v-model="newName"
          class="fc-input"
          :placeholder="newPlaceholder"
          @keydown.enter.prevent="addNewFolder"
        />
        <button class="fc-add" :disabled="!canAdd" @click="addNewFolder">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="12" y1="5" x2="12" y2="19"/>
            <line x1="5" y1="12" x2="19" y2="12"/>
          </svg>
          New folder
        </button>
      </div>

      <footer class="fc-footer">
        <div class="fc-selected">
          <span class="fc-selected-label">Destination:</span>
          <code class="fc-selected-path">{{ selected || '(root)' }}</code>
        </div>
        <div class="fc-actions">
          <button class="fc-btn fc-btn-secondary" @click="requestClose">Cancel</button>
          <button class="fc-btn fc-btn-primary" @click="confirm">{{ confirmLabel }}</button>
        </div>
      </footer>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import FolderTree from './FolderTree.vue'
import { buildFolderTree, FOLDER_ORDER } from '../composables/useFolderTree.js'
import { lockBodyOverflow, unlockBodyOverflow } from '../composables/useBodyOverflowLock.js'

const props = defineProps({
  title: { type: String, default: 'Choose folder' },
  confirmLabel: { type: String, default: 'Choose' },
  initialFolder: { type: String, default: '' },
  newPlaceholder: { type: String, default: 'New folder name…' },
})

const emit = defineEmits(['close', 'confirm'])

const visible = ref(false)
const treeEl = ref(null)
const tree = ref([])
const loading = ref(true)
const error = ref(null)
const selected = ref(props.initialFolder || '')
const newName = ref('')
let closeTimer = null

const canAdd = computed(() => {
  const v = newName.value.trim()
  if (!v) return false
  if (v.includes('/') || v.includes('\\') || v === '.' || v === '..') return false
  return true
})

function requestClose() {
  if (!visible.value) return
  visible.value = false
  if (closeTimer) clearTimeout(closeTimer)
  closeTimer = setTimeout(() => emit('close'), 180)
}

function select(path) {
  selected.value = path
}

function confirm() {
  emit('confirm', selected.value)
}

function findNode(nodes, path) {
  for (const n of nodes) {
    if (n.path === path) return n
    const hit = findNode(n.children, path)
    if (hit) return hit
  }
  return null
}

function addNewFolder() {
  if (!canAdd.value) return
  const name = newName.value.trim()
  const parentPath = selected.value
  const newPath = parentPath ? parentPath + '/' + name : name

  let siblings
  if (parentPath === '') {
    siblings = tree.value
  } else {
    const parent = findNode(tree.value, parentPath)
    if (!parent) return
    siblings = parent.children
  }

  const existing = siblings.find(n => n.name.toLowerCase() === name.toLowerCase())
  if (existing) {
    selected.value = existing.path
  } else {
    const node = { name, path: newPath, children: [] }
    siblings.push(node)
    const direction = FOLDER_ORDER === 'desc' ? -1 : 1
    siblings.sort((a, b) => direction * a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }))
    selected.value = newPath
  }
  newName.value = ''
}

async function fetchFolders() {
  try {
    const res = await fetch('/api/folders?order=' + FOLDER_ORDER)
    if (!res.ok) throw new Error(`HTTP ${res.status}`)
    const data = await res.json()
    tree.value = buildFolderTree(data.folders ?? [], FOLDER_ORDER)
  } catch (e) {
    error.value = 'Failed to load folders'
  } finally {
    loading.value = false
  }
}

function onKeydown(e) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    requestClose()
  }
}

onMounted(async () => {
  lockBodyOverflow()
  document.addEventListener('keydown', onKeydown)
  fetchFolders()
  await nextTick()
  requestAnimationFrame(() => { visible.value = true })
})

onUnmounted(() => {
  unlockBodyOverflow()
  document.removeEventListener('keydown', onKeydown)
  if (closeTimer) clearTimeout(closeTimer)
})
</script>

<style scoped>
.fc-overlay {
  position: fixed;
  inset: 0;
  z-index: 10200;
  background: rgba(0, 0, 0, 0);
  display: flex;
  align-items: flex-end;
  justify-content: center;
  padding: 16px;
  padding-bottom: calc(16px + env(safe-area-inset-bottom));
  transition: background 0.18s ease;
}
.fc-overlay.fc-visible {
  background: rgba(0, 0, 0, 0.55);
}

@media (min-width: 600px) {
  .fc-overlay {
    align-items: center;
  }
}

.fc-card {
  width: 100%;
  max-width: 460px;
  height: 75vh;
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 35%, black);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 16px 16px 0 0;
  display: flex;
  flex-direction: column;
  overflow: hidden;
  opacity: 0;
  transform: translateY(20px);
  transition: opacity 0.18s ease, transform 0.18s ease;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.6);
}
.fc-visible .fc-card {
  opacity: 1;
  transform: translateY(0);
}

@media (min-width: 600px) {
  .fc-card { border-radius: 14px; }
  .fc-visible .fc-card { transform: scale(1); }
  .fc-card { transform: scale(0.97); }
}

.fc-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px 10px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  flex-shrink: 0;
}
.fc-title {
  font-size: 1.05rem;
  font-weight: 600;
  color: #fff;
  margin: 0;
}
.fc-close {
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
.fc-close:hover { color: #ccc; background: rgba(255,255,255,0.06); }
.fc-close svg { width: 18px; height: 18px; }

.fc-tree {
  flex: 1;
  min-height: 0;
  overflow-y: auto;
  padding: 6px 12px;
}

.fc-root {
  display: flex;
  align-items: center;
  gap: 8px;
  width: 100%;
  padding: 6px 8px;
  margin: 4px 0 8px 0;
  border: none;
  background: transparent;
  color: #d0d0d8;
  border-radius: 6px;
  cursor: pointer;
  font: inherit;
  font-size: 0.9rem;
  text-align: left;
}
.fc-root svg { width: 16px; height: 16px; color: #8a8a98; }
.fc-root:hover { background: rgba(255,255,255,0.04); }
.fc-root-active {
  background: rgba(100, 120, 220, 0.18);
  color: #c0caff;
}
.fc-root-active svg { color: #c0caff; }

.fc-state {
  padding: 12px 8px;
  font-size: 0.85rem;
  color: #888;
}
.fc-error { color: #f87171; }

.fc-new-folder {
  display: flex;
  gap: 8px;
  padding: 10px 14px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
  flex-shrink: 0;
}
.fc-input {
  flex: 1;
  min-width: 0;
  padding: 8px 10px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 6px;
  color: #e0e0e8;
  font: inherit;
  font-size: 0.9rem;
}
.fc-input:focus {
  outline: none;
  border-color: rgba(100, 120, 220, 0.5);
  background: rgba(255, 255, 255, 0.06);
}
.fc-add {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 0 12px;
  border: 1px solid rgba(255, 255, 255, 0.1);
  background: transparent;
  color: #ccc;
  border-radius: 6px;
  cursor: pointer;
  font: inherit;
  font-size: 0.85rem;
  white-space: nowrap;
  transition: all 0.15s;
}
.fc-add svg { width: 14px; height: 14px; }
.fc-add:hover:not(:disabled) {
  border-color: rgba(255, 255, 255, 0.25);
  color: #fff;
  background: rgba(255, 255, 255, 0.04);
}
.fc-add:disabled {
  opacity: 0.4;
  cursor: not-allowed;
}

.fc-footer {
  padding: 12px 16px calc(12px + env(safe-area-inset-bottom)) 16px;
  flex-shrink: 0;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.fc-selected {
  display: flex;
  align-items: center;
  gap: 6px;
  font-size: 0.8rem;
  color: #888;
  min-width: 0;
}
.fc-selected-label { flex-shrink: 0; }
.fc-selected-path {
  font-family: monospace;
  font-size: 0.85em;
  color: #c0caff;
  background: rgba(100, 120, 220, 0.1);
  padding: 2px 6px;
  border-radius: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.fc-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}

.fc-btn {
  padding: 8px 18px;
  border-radius: 8px;
  border: 1px solid transparent;
  font: inherit;
  font-size: 0.9rem;
  cursor: pointer;
  transition: all 0.15s;
}
.fc-btn-secondary {
  background: transparent;
  color: #aab;
  border-color: rgba(255, 255, 255, 0.12);
}
.fc-btn-secondary:hover {
  color: #fff;
  border-color: rgba(255, 255, 255, 0.25);
}
.fc-btn-primary {
  background: rgba(100, 120, 220, 0.25);
  color: #c0caff;
  border-color: rgba(100, 120, 220, 0.5);
}
.fc-btn-primary:hover {
  background: rgba(100, 120, 220, 0.4);
  color: #fff;
}
</style>
