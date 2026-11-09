<template>
  <!-- Teleported: the side menu drawer is transformed, which would otherwise
       turn this fixed overlay into one positioned inside the drawer. -->
  <Teleport to="body">
    <div
      class="rf-overlay"
      :class="{ 'rf-visible': visible }"
      role="dialog"
      aria-modal="true"
      aria-label="Rename folder"
      @click.self="requestClose"
    >
      <form class="rf-card" @submit.prevent="submit">
        <header class="rf-header">
          <h2 class="rf-title">Rename folder</h2>
          <button type="button" class="rf-close" aria-label="Close" @click="requestClose">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <line x1="18" y1="6" x2="6" y2="18"/>
              <line x1="6" y1="6" x2="18" y2="18"/>
            </svg>
          </button>
        </header>

        <div class="rf-body">
          <p v-if="parent" class="rf-parent">in <code>{{ parent }}</code></p>
          <input
            ref="inputEl"
            v-model="name"
            class="rf-input"
            aria-label="New folder name"
            autocomplete="off"
            spellcheck="false"
            :disabled="saving"
          />
          <p v-if="message" class="rf-error" role="alert">{{ message }}</p>
        </div>

        <footer class="rf-actions">
          <button type="button" class="rf-btn rf-btn-secondary" @click="requestClose">Cancel</button>
          <button type="submit" class="rf-btn rf-btn-primary" :disabled="!canSubmit">
            {{ saving ? 'Renaming…' : 'Rename' }}
          </button>
        </footer>
      </form>
    </div>
  </Teleport>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'
import { lockBodyOverflow, unlockBodyOverflow } from '../composables/useBodyOverflowLock.js'

const props = defineProps({
  path: { type: String, required: true },
})

const emit = defineEmits(['close', 'renamed'])

const slash = props.path.lastIndexOf('/')
const parent = slash < 0 ? '' : props.path.slice(0, slash)
const currentName = slash < 0 ? props.path : props.path.slice(slash + 1)

const visible = ref(false)
const inputEl = ref(null)
const name = ref(currentName)
const serverError = ref('')
const saving = ref(false)
let closeTimer = null

// Mirrors the server's validation in handleRenameFolder.
const validationError = computed(() => {
  const v = name.value.trim()
  if (!v || v === currentName) return ''
  if (v.includes('/') || v.includes('\\')) return 'Name cannot contain / or \\'
  if (v.startsWith('.')) return 'Name cannot start with a dot'
  return ''
})

const message = computed(() => validationError.value || serverError.value)

const canSubmit = computed(() => {
  const v = name.value.trim()
  return !saving.value && v !== '' && v !== currentName && !validationError.value
})

async function submit() {
  if (!canSubmit.value) return
  saving.value = true
  serverError.value = ''
  try {
    const res = await fetch('/api/rename-folder', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ path: props.path, name: name.value.trim() }),
    })
    if (!res.ok) {
      serverError.value = (await res.text()).trim() || 'Rename failed'
      return
    }
    emit('renamed', await res.json())
    requestClose()
  } catch {
    serverError.value = 'Rename failed — check your connection'
  } finally {
    saving.value = false
  }
}

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
  requestAnimationFrame(() => {
    visible.value = true
    inputEl.value?.focus()
    inputEl.value?.select()
  })
})

onUnmounted(() => {
  unlockBodyOverflow()
  document.removeEventListener('keydown', onKeydown)
  if (closeTimer) clearTimeout(closeTimer)
})
</script>

<style scoped>
.rf-overlay {
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
.rf-overlay.rf-visible {
  background: rgba(0, 0, 0, 0.55);
}

@media (min-width: 600px) {
  .rf-overlay { align-items: center; }
}

.rf-card {
  width: 100%;
  max-width: 400px;
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 35%, black);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 16px 16px 0 0;
  display: flex;
  flex-direction: column;
  opacity: 0;
  transform: translateY(20px);
  transition: opacity 0.18s ease, transform 0.18s ease;
}
.rf-visible .rf-card {
  opacity: 1;
  transform: translateY(0);
}

@media (min-width: 600px) {
  .rf-card { border-radius: 14px; transform: scale(0.97); }
  .rf-visible .rf-card { transform: scale(1); }
}

.rf-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 16px 10px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.06);
}
.rf-title {
  font-size: 1.05rem;
  font-weight: 600;
  color: #fff;
  margin: 0;
}
.rf-close {
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
.rf-close:hover { color: #ccc; background: rgba(255, 255, 255, 0.06); }
.rf-close svg { width: 18px; height: 18px; }

.rf-body {
  padding: 14px 16px 4px;
  display: flex;
  flex-direction: column;
  gap: 8px;
}
.rf-parent {
  margin: 0;
  font-size: 0.8rem;
  color: #889;
}
.rf-parent code {
  color: #aab;
  word-break: break-all;
}
.rf-input {
  width: 100%;
  box-sizing: border-box;
  padding: 9px 10px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 6px;
  color: #e0e0e8;
  font: inherit;
  font-size: 0.95rem;
}
.rf-input:focus {
  outline: none;
  border-color: rgba(100, 120, 220, 0.5);
  background: rgba(255, 255, 255, 0.06);
}
.rf-error {
  margin: 0;
  font-size: 0.8rem;
  color: #f87171;
}

.rf-actions {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
  padding: 12px 16px calc(12px + env(safe-area-inset-bottom));
}
.rf-btn {
  padding: 8px 18px;
  border-radius: 8px;
  border: 1px solid transparent;
  font: inherit;
  font-size: 0.9rem;
  cursor: pointer;
  transition: all 0.15s;
}
.rf-btn:disabled { opacity: 0.45; cursor: default; }
.rf-btn-secondary {
  background: transparent;
  color: #aab;
  border-color: rgba(255, 255, 255, 0.12);
}
.rf-btn-secondary:hover {
  color: #fff;
  border-color: rgba(255, 255, 255, 0.25);
}
.rf-btn-primary {
  background: rgba(100, 120, 220, 0.25);
  color: #c0caff;
  border-color: rgba(100, 120, 220, 0.5);
}
.rf-btn-primary:not(:disabled):hover {
  background: rgba(100, 120, 220, 0.4);
  color: #fff;
}
</style>
