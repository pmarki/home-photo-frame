<template>
  <ul class="ft-list">
    <li v-for="node in nodes" :key="node.path" class="ft-item">
      <div
        class="ft-row"
        :class="{ 'ft-row-active': node.path === currentFolder }"
        :style="{ paddingLeft: (8 + depth * 14) + 'px' }"
        :title="rowTitle(node)"
      >
        <button
          v-if="node.children.length > 0"
          class="ft-chevron"
          :class="{ 'ft-chevron-open': isExpanded(node) }"
          :aria-label="isExpanded(node) ? 'Collapse' : 'Expand'"
          @click.stop="toggle(node)"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="9 6 15 12 9 18"/>
          </svg>
        </button>
        <span v-else class="ft-chevron-spacer" />
        <button class="ft-name" @click="$emit('select', node.path)">
          <svg class="ft-folder-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round">
            <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>
          </svg>
          <span class="ft-label">{{ node.name }}</span>
          <svg
            v-if="node.scope === 'private'"
            class="ft-scope-icon"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <circle cx="12" cy="8" r="3.5"/>
            <path d="M5 20c0-3.5 3-6 7-6s7 2.5 7 6"/>
          </svg>
          <svg
            v-else-if="node.scope === 'shared'"
            class="ft-scope-icon"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <circle cx="9" cy="8" r="3"/>
            <path d="M2.5 19c0-3 2.9-5.2 6.5-5.2s6.5 2.2 6.5 5.2"/>
            <circle cx="17" cy="7" r="2.5"/>
            <path d="M14.5 13.6c.8-.2 1.6-.3 2.5-.3 2.7 0 4.5 1.6 4.5 3.7"/>
          </svg>
          <svg
            v-else-if="node.scope === 'public'"
            class="ft-scope-icon"
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            stroke-width="1.6"
            stroke-linecap="round"
            stroke-linejoin="round"
          >
            <rect x="5" y="11" width="14" height="9" rx="1.5"/>
            <path d="M8 11V7a4 4 0 0 1 7.5-2"/>
          </svg>
        </button>
      </div>
      <FolderTree
        v-if="node.children.length > 0 && isExpanded(node)"
        :nodes="node.children"
        :current-folder="currentFolder"
        :depth="depth + 1"
        :auto-expand-depth="autoExpandDepth"
        @select="$emit('select', $event)"
      />
    </li>
  </ul>
</template>

<script>
// Module-level (runs once, NOT per-instance like <script setup>).
// All FolderTree instances share this same reactive object so a toggle at
// any depth is visible to siblings and parents, and the persisted localStorage
// blob always reflects the full set of overrides.
import { reactive, watch } from 'vue'

const STORAGE_KEY = 'folderTree.overrides'

function loadOverrides() {
  try {
    const raw = localStorage.getItem(STORAGE_KEY)
    if (!raw) return {}
    const obj = JSON.parse(raw)
    if (!obj || typeof obj !== 'object') return {}
    const out = {}
    for (const [k, v] of Object.entries(obj)) {
      if (typeof v === 'boolean') out[k] = v
    }
    return out
  } catch {
    return {}
  }
}

const overrides = reactive(loadOverrides())

watch(overrides, (state) => {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(state))
  } catch {
    // storage full or unavailable — silently degrade to in-memory only
  }
}, { deep: true, flush: 'post' })

</script>

<script setup>
defineOptions({ name: 'FolderTree' })

const props = defineProps({
  nodes: { type: Array, required: true },
  currentFolder: { type: String, default: '' },
  depth: { type: Number, default: 0 },
  autoExpandDepth: { type: Number, default: 4 },
})

defineEmits(['select'])

function isExpanded(node) {
  const v = overrides[node.path]
  if (typeof v === 'boolean') return v
  return props.depth < props.autoExpandDepth
}

function toggle(node) {
  overrides[node.path] = !isExpanded(node)
}

function rowTitle(node) {
  if (!node.scope) return ''
  if (node.scope === 'private') return 'Only you'
  if (node.scope === 'shared') {
    const names = (node.sharedWith ?? []).join(', ')
    return names ? `Shared with ${names}` : 'Shared'
  }
  if (node.scope === 'public') return 'Public — visible to everyone'
  return ''
}
</script>

<style scoped>
.ft-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.ft-item {
  margin: 0;
}

.ft-row {
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 2px 8px 2px 0;
  border-radius: 6px;
  transition: background 0.12s;
}
.ft-row:hover { background: rgba(255, 255, 255, 0.04); }
.ft-row-active {
  background: rgba(100, 120, 220, 0.18);
}
.ft-row-active .ft-label { color: #c0caff; }

.ft-chevron,
.ft-chevron-spacer {
  flex-shrink: 0;
  width: 20px;
  height: 24px;
  display: flex;
  align-items: center;
  justify-content: center;
  border: none;
  background: transparent;
  color: #777;
  cursor: pointer;
  border-radius: 4px;
  padding: 0;
}
.ft-chevron svg {
  width: 12px;
  height: 12px;
  transition: transform 0.15s;
}
.ft-chevron:hover { color: #ccc; background: rgba(255,255,255,0.05); }
.ft-chevron-open svg { transform: rotate(90deg); }

.ft-name {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 4px 6px;
  border: none;
  background: transparent;
  color: #d0d0d8;
  cursor: pointer;
  text-align: left;
  font: inherit;
  border-radius: 4px;
}
.ft-name:hover { color: #fff; }

.ft-folder-icon {
  width: 16px;
  height: 16px;
  flex-shrink: 0;
  color: #8a8a98;
  opacity: 0.85;
}

.ft-scope-icon {
  width: 14px;
  height: 14px;
  flex-shrink: 0;
  margin-left: auto;
  color: #8a8a98;
  opacity: 0.7;
}
.ft-row-active .ft-scope-icon { color: #c0caff; opacity: 0.85; }

.ft-label {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 0.9rem;
}
</style>
