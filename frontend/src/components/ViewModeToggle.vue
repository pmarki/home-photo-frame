<template>
  <div class="view-mode-toggle">
    <button
      v-for="m in modes"
      :key="m.key"
      class="sort-icon-btn"
      :class="{ active: mode === m.key }"
      :title="m.label"
      :aria-label="m.label"
      :aria-pressed="mode === m.key"
      @click="$emit('change', m.key)"
    >
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <template v-if="m.key === 'gallery'">
          <rect x="3"  y="3"  width="8" height="8" rx="1"/>
          <rect x="13" y="3"  width="8" height="8" rx="1"/>
          <rect x="3"  y="13" width="8" height="8" rx="1"/>
          <rect x="13" y="13" width="8" height="8" rx="1"/>
        </template>
        <template v-else-if="m.key === 'timeline'">
          <line x1="4"  y1="6"  x2="16" y2="6"/>
          <line x1="8"  y1="12" x2="18" y2="12"/>
          <line x1="16" y1="18" x2="20" y2="18"/>
        </template>
        <template v-else>
          <path d="M3 6a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v10a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>
          <line x1="3"  y1="11" x2="21" y2="11"/>
          <line x1="7"  y1="15" x2="11" y2="15"/>
        </template>
      </svg>
    </button>
  </div>
</template>

<script setup>
defineProps({
  mode: { type: String, required: true },
})
defineEmits(['change'])

const modes = [
  { key: 'gallery',  label: 'Gallery view' },
  { key: 'timeline', label: 'Timeline view' },
  { key: 'browser',  label: 'File browser view' },
]
</script>

<style scoped>
.view-mode-toggle {
  display: flex;
  gap: 6px;
  flex: 1;
}
</style>
