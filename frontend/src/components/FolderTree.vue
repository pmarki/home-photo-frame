<template>
  <ul class="ft-list">
    <li v-for="node in nodes" :key="node.path" class="ft-item">
      <div
        class="ft-row"
        :class="{ 'ft-row-active': node.path === currentFolder }"
        :style="{ paddingLeft: (8 + depth * 14) + 'px' }"
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

<script setup>
import { ref } from 'vue'

defineOptions({ name: 'FolderTree' })

// Module-level: shared across all FolderTree instances and persisted between
// SideMenu open/close cycles. Keyed by full folder path (globally unique).
const overrides = ref(new Map())

const props = defineProps({
  nodes: { type: Array, required: true },
  currentFolder: { type: String, default: '' },
  depth: { type: Number, default: 0 },
  autoExpandDepth: { type: Number, default: 4 },
})

defineEmits(['select'])

function isExpanded(node) {
  if (overrides.value.has(node.path)) return overrides.value.get(node.path)
  return props.depth < props.autoExpandDepth
}

function toggle(node) {
  const next = !isExpanded(node)
  const map = new Map(overrides.value)
  map.set(node.path, next)
  overrides.value = map
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

.ft-label {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  font-size: 0.9rem;
}
</style>
