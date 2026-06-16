<template>
  <Teleport to="body">
    <div
      class="ctx-backdrop"
      @click="onBackdropClick"
      @contextmenu.prevent="onBackdropClick"
    />
    <ul
      ref="menuEl"
      class="ctx-menu"
      role="menu"
      tabindex="-1"
      :style="positionStyle"
      @click.stop
      @contextmenu.prevent
    >
      <li>
        <button class="ctx-item" role="menuitem" @click="fireAction('open-new-tab')">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M14 3h7v7"/>
            <line x1="10" y1="14" x2="21" y2="3"/>
            <path d="M21 14v5a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5"/>
          </svg>
          <span>Open in new tab</span>
        </button>
      </li>
      <li>
        <button class="ctx-item" role="menuitem" @click="fireAction('save')">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/>
            <polyline points="7 10 12 15 17 10"/>
            <line x1="12" y1="15" x2="12" y2="3"/>
          </svg>
          <span>Save</span>
        </button>
      </li>
      <li>
        <button class="ctx-item" role="menuitem" @click="fireAction('share')">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <circle cx="18" cy="5" r="3"/>
            <circle cx="6" cy="12" r="3"/>
            <circle cx="18" cy="19" r="3"/>
            <line x1="8.6" y1="13.5" x2="15.4" y2="17.5"/>
            <line x1="15.4" y1="6.5" x2="8.6" y2="10.5"/>
          </svg>
          <span>Share</span>
        </button>
      </li>
      <li>
        <button class="ctx-item" role="menuitem" @click="fireAction('select')">
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
            <rect x="3" y="3" width="18" height="18" rx="2"/>
            <polyline points="8 12 11 15 16 9"/>
          </svg>
          <span>Select</span>
        </button>
      </li>
    </ul>
  </Teleport>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'

const props = defineProps({
  x: { type: Number, required: true },
  y: { type: Number, required: true },
})

const emit = defineEmits(['close', 'open-new-tab', 'save', 'share', 'select'])

const menuEl = ref(null)
const adjustedX = ref(props.x)
const adjustedY = ref(props.y)
const positionStyle = ref({ left: props.x + 'px', top: props.y + 'px', visibility: 'hidden' })

// Brief grace period so the synthesized click that follows a long-press
// touchend doesn't immediately fire a menu action or dismiss the menu.
const armed = ref(false)
let armTimer = null

function dismiss() { emit('close') }

function onBackdropClick() {
  if (!armed.value) return
  dismiss()
}

function fireAction(action) {
  if (!armed.value) return
  emit(action)
}

function onKeydown(e) {
  if (e.key === 'Escape') {
    e.stopPropagation()
    dismiss()
  }
}

function onScroll() { dismiss() }

onMounted(async () => {
  document.addEventListener('keydown', onKeydown, true)
  window.addEventListener('scroll', onScroll, true)
  window.addEventListener('resize', onScroll)
  armTimer = setTimeout(() => { armed.value = true }, 250)
  await nextTick()
  if (!menuEl.value) return
  const rect = menuEl.value.getBoundingClientRect()
  const margin = 8
  let x = Math.max(margin, props.x)
  let y = Math.max(margin, props.y)
  if (x + rect.width + margin > window.innerWidth) x = Math.max(margin, window.innerWidth - rect.width - margin)
  if (y + rect.height + margin > window.innerHeight) y = Math.max(margin, window.innerHeight - rect.height - margin)
  adjustedX.value = x
  adjustedY.value = y
  positionStyle.value = { left: x + 'px', top: y + 'px', visibility: 'visible' }
  menuEl.value.focus()
})

onUnmounted(() => {
  if (armTimer) clearTimeout(armTimer)
  document.removeEventListener('keydown', onKeydown, true)
  window.removeEventListener('scroll', onScroll, true)
  window.removeEventListener('resize', onScroll)
})
</script>

<style scoped>
.ctx-backdrop {
  position: fixed;
  inset: 0;
  z-index: 10499;
  background: transparent;
}

.ctx-menu {
  position: fixed;
  z-index: 10500;
  margin: 0;
  padding: 4px;
  list-style: none;
  min-width: 200px;
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 35%, black);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 10px;
  box-shadow: 0 10px 32px rgba(0, 0, 0, 0.55);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  outline: none;
  animation: ctx-in 0.12s ease;
}

@keyframes ctx-in {
  from { opacity: 0; transform: translateY(-4px); }
  to   { opacity: 1; transform: translateY(0); }
}

.ctx-menu li { margin: 0; }

.ctx-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 8px 12px;
  border: none;
  background: transparent;
  color: #e0e0e8;
  font: inherit;
  font-size: 0.9rem;
  border-radius: 6px;
  cursor: pointer;
  text-align: left;
}
.ctx-item:hover,
.ctx-item:focus-visible {
  background: rgba(255, 255, 255, 0.07);
  color: #fff;
  outline: none;
}
.ctx-item svg {
  width: 16px;
  height: 16px;
  flex-shrink: 0;
  color: #8a8a98;
}
.ctx-item:hover svg,
.ctx-item:focus-visible svg { color: #c0caff; }
</style>
