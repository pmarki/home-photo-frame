<template>
  <div class="sel-toolbar" role="toolbar" aria-label="Selection actions">
    <span class="sel-count">{{ count }} selected</span>

    <button class="sel-btn sel-danger" aria-label="Delete" title="Delete" @click="$emit('delete')">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <polyline points="3 6 5 6 21 6"/>
        <path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/>
        <path d="M10 11v6"/>
        <path d="M14 11v6"/>
        <path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/>
      </svg>
    </button>

    <span class="sel-spacer" />

    <button class="sel-btn sel-btn-action" aria-label="Copy to" title="Copy to" @click="$emit('copy')">
      <svg class="sel-action-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <rect x="9" y="9" width="13" height="13" rx="2"/>
        <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>
      </svg>
      <span class="sel-action-label">Copy to</span>
    </button>
    <button class="sel-btn sel-btn-action" aria-label="Move to" title="Move to" @click="$emit('move')">
      <svg class="sel-action-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>
        <line x1="8" y1="13" x2="16" y2="13"/>
        <polyline points="13 10 16 13 13 16"/>
      </svg>
      <span class="sel-action-label">Move to</span>
    </button>
    <button class="sel-btn" aria-label="Share" title="Share" @click="$emit('share')">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
        <circle cx="18" cy="5" r="3"/>
        <circle cx="6" cy="12" r="3"/>
        <circle cx="18" cy="19" r="3"/>
        <line x1="8.6" y1="13.5" x2="15.4" y2="17.5"/>
        <line x1="15.4" y1="6.5" x2="8.6" y2="10.5"/>
      </svg>
    </button>

    <span class="sel-spacer" />

    <button class="sel-btn sel-cancel" aria-label="Cancel selection" @click="$emit('cancel')">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <line x1="18" y1="6" x2="6" y2="18"/>
        <line x1="6" y1="6" x2="18" y2="18"/>
      </svg>
    </button>
  </div>
</template>

<script setup>
defineProps({
  count: { type: Number, required: true },
})

defineEmits(['cancel', 'share', 'delete', 'copy', 'move'])
</script>

<style scoped>
.sel-toolbar {
  position: fixed;
  top: 0;
  left: 0;
  right: 0;
  z-index: 200;
  display: flex;
  align-items: center;
  gap: 12px;
  padding: 10px 12px calc(10px + env(safe-area-inset-bottom-fallback, 0px)) 12px;
  padding-top: calc(10px + env(safe-area-inset-top));
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 45%, black);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  border-bottom: 1px solid color-mix(in srgb, var(--bg-color, #0a0a0f) 80%, white);
  color: #fff;
}

.sel-count {
  flex: 1;
  margin-right: 8px;
  font-size: 0.95rem;
  font-weight: 500;
  white-space: nowrap;
}

.sel-spacer {
  width: 36px;
  flex-shrink: 0;
}

.sel-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: 1px solid transparent;
  background: transparent;
  color: #ccc;
  border-radius: 8px;
  cursor: pointer;
  padding: 0;
  transition: all 0.15s;
}
.sel-btn svg { width: 18px; height: 18px; }
.sel-btn:hover {
  border-color: rgba(255, 255, 255, 0.18);
  background: rgba(255, 255, 255, 0.05);
  color: #fff;
}
.sel-btn-action {
  width: auto;
  padding: 0 12px;
  font-size: 0.85rem;
  white-space: nowrap;
  gap: 6px;
}
.sel-action-icon { display: none; }

@media (max-width: 600px) {
  .sel-toolbar {
    gap: 6px;
    padding-left: 8px;
    padding-right: 8px;
  }
  .sel-count {
    margin-right: 4px;
    font-size: 0.85rem;
  }
  .sel-spacer { display: none; }
  .sel-btn { width: 32px; height: 32px; }
  .sel-btn svg { width: 16px; height: 16px; }
  .sel-btn-action {
    width: 32px;
    padding: 0;
  }
  .sel-action-icon { display: block; }
  .sel-action-label { display: none; }
}
.sel-cancel { color: #aab; }
.sel-danger:hover {
  border-color: rgba(248, 113, 113, 0.4);
  background: rgba(248, 113, 113, 0.08);
  color: #f87171;
}
</style>
