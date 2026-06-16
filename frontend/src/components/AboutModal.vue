<template>
  <div
    class="ab-overlay"
    :class="{ 'ab-visible': visible }"
    role="dialog"
    aria-modal="true"
    aria-label="About"
    @click.self="requestClose"
  >
    <div class="ab-card">
      <button class="ab-close" aria-label="Close" @click="requestClose">
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
          <line x1="18" y1="6" x2="6" y2="18"/>
          <line x1="6" y1="6" x2="18" y2="18"/>
        </svg>
      </button>
      <img v-if="titleIcon" src="/icons/favicon.svg" class="ab-icon" alt="" />
      <svg v-else class="ab-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round">
        <rect x="2" y="2" width="20" height="20" rx="1.5"/>
        <rect x="4.5" y="4.5" width="15" height="15" rx="0.5"/>
        <circle cx="16" cy="8.5" r="1.5" fill="currentColor" stroke="none"/>
        <polyline points="5.5,17 10,10.5 14.5,17"/>
      </svg>
      <h2 class="ab-title">{{ title }}</h2>
      <dl class="ab-meta">
        <div class="ab-meta-row">
          <dt>Build</dt>
          <dd>{{ buildNumber || '—' }}</dd>
        </div>
        <div class="ab-meta-row">
          <dt>Video upload</dt>
          <dd>{{ videoEnabled ? 'Enabled' : 'Disabled' }}</dd>
        </div>
        <div class="ab-meta-row">
          <dt>Photos</dt>
          <dd>{{ imageCount.toLocaleString() }}</dd>
        </div>
        <div class="ab-meta-row">
          <dt>Storage used</dt>
          <dd>{{ formatBytes(imageTotalBytes) }}</dd>
        </div>
        <div class="ab-meta-row">
          <dt>Free space</dt>
          <dd>{{ formatBytes(diskFreeBytes) }}</dd>
        </div>
      </dl>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { lockBodyOverflow, unlockBodyOverflow } from '../composables/useBodyOverflowLock.js'

defineProps({
  title: { type: String, required: true },
  titleIcon: { type: Boolean, default: false },
  videoEnabled: { type: Boolean, default: false },
  buildNumber: { type: String, default: '' },
  imageCount: { type: Number, default: 0 },
  imageTotalBytes: { type: Number, default: 0 },
  diskFreeBytes: { type: Number, default: 0 },
})

function formatBytes(bytes) {
  if (!bytes || bytes < 0) return '—'
  if (bytes < 1024) return bytes + ' B'
  if (bytes < 1024 * 1024) return (bytes / 1024).toFixed(1) + ' KB'
  if (bytes < 1024 * 1024 * 1024) return (bytes / (1024 * 1024)).toFixed(1) + ' MB'
  if (bytes < 1024 * 1024 * 1024 * 1024) return (bytes / (1024 * 1024 * 1024)).toFixed(2) + ' GB'
  return (bytes / (1024 * 1024 * 1024 * 1024)).toFixed(2) + ' TB'
}

const emit = defineEmits(['close'])

const visible = ref(false)
let closeTimer = null

function requestClose() {
  if (!visible.value) return
  visible.value = false
  if (closeTimer) clearTimeout(closeTimer)
  closeTimer = setTimeout(() => emit('close'), 180)
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
.ab-overlay {
  position: fixed;
  inset: 0;
  z-index: 9600;
  background: rgba(0, 0, 0, 0);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  transition: background 0.18s ease;
}
.ab-overlay.ab-visible {
  background: rgba(0, 0, 0, 0.65);
}

.ab-card {
  position: relative;
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 35%, black);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 14px;
  padding: 32px 28px 24px;
  min-width: 260px;
  max-width: 92vw;
  text-align: center;
  opacity: 0;
  transform: translateY(8px) scale(0.98);
  transition: opacity 0.18s ease, transform 0.18s ease;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.6);
}
.ab-visible .ab-card {
  opacity: 1;
  transform: translateY(0) scale(1);
}

.ab-icon {
  display: block;
  width: 56px;
  height: 56px;
  margin: 0 auto 14px;
  color: #c0caff;
  opacity: 0.95;
}

.ab-title {
  font-size: 1.3rem;
  font-weight: 600;
  color: #fff;
  margin: 0 0 18px;
}

.ab-meta {
  margin: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 0.85rem;
}

.ab-meta-row {
  display: flex;
  justify-content: space-between;
  align-items: baseline;
  gap: 16px;
  padding: 4px 2px;
  border-top: 1px solid rgba(255, 255, 255, 0.06);
}
.ab-meta-row:last-child { border-bottom: 1px solid rgba(255, 255, 255, 0.06); }

.ab-meta dt {
  color: #888;
  font-weight: 400;
}
.ab-meta dd {
  color: #e0e0e8;
  font-weight: 500;
  margin: 0;
}

.ab-close {
  position: absolute;
  top: 8px;
  right: 8px;
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
.ab-close:hover { color: #ccc; background: rgba(255,255,255,0.06); }
.ab-close svg { width: 16px; height: 16px; }
</style>
