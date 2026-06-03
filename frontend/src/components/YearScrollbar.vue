<template>
  <Teleport to="body">
    <div
      ref="barRef"
      class="year-scrollbar"
      :class="{ active: visible || isDragging, 'is-touch': isTouchInput }"
      @pointerdown.prevent="onPointerDown"
      @pointermove="onPointerMove"
      @pointerup="onPointerUp"
      @pointercancel="onPointerUp"
    >
      <div class="year-labels">
        <div
          v-for="item in yearItems"
          :key="item.year"
          class="year-label"
          :class="{ active: item.year === currentYear }"
          :style="{ '--pos': item.pos }"
        >
          {{ item.year }}
        </div>
      </div>
      <div class="year-handle" :style="{ '--hpos': handlePos }" />
    </div>
  </Teleport>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'

const props = defineProps({
  yearItems:   { type: Array,   required: true },
  currentYear: { type: Number,  default: null },
  visible:     { type: Boolean, default: false },
  handlePos:   { type: Number,  default: 0 },
  maxScrollY:  { type: Number,  default: 0 },
})

const barRef = ref(null)
const isDragging = ref(false)
const headerHeight = ref(0)
const topOffset = computed(() => headerHeight.value + 'px')

const isTouchInput = ref(
  typeof window !== 'undefined' && window.matchMedia('(pointer: coarse)').matches
)

function onDocPointerDown(e) {
  if (e.pointerType === 'touch') isTouchInput.value = true
  else if (e.pointerType === 'mouse') isTouchInput.value = false
}

let headerObserver = null

onMounted(() => {
  const header = document.querySelector('.app-header')
  if (header) {
    headerHeight.value = header.offsetHeight
    headerObserver = new ResizeObserver(() => { headerHeight.value = header.offsetHeight })
    headerObserver.observe(header)
  }
  window.addEventListener('pointerdown', onDocPointerDown, { capture: true, passive: true })
})

onUnmounted(() => {
  headerObserver?.disconnect()
  window.removeEventListener('pointerdown', onDocPointerDown, { capture: true, passive: true })
})

function scrollToY(e) {
  const rect = barRef.value.getBoundingClientRect()
  const relY = Math.max(0, Math.min(rect.height, e.clientY - rect.top))
  window.scrollTo(0, (relY / rect.height) * props.maxScrollY)
}

function onPointerDown(e) {
  isDragging.value = true
  barRef.value.setPointerCapture(e.pointerId)
  scrollToY(e)
}

function onPointerMove(e) {
  if (!isDragging.value) return
  scrollToY(e)
}

function onPointerUp() {
  isDragging.value = false
}
</script>

<style scoped>
.year-scrollbar {
  position: fixed;
  right: 0;
  top: v-bind(topOffset);
  bottom: 0;
  width: 44px;
  pointer-events: none;
  z-index: 50;
}

.year-scrollbar.is-touch {
  pointer-events: auto;
  cursor: pointer;
  touch-action: none;
}

/* ── year labels (fade in/out) ───────────────────────── */
.year-labels {
  position: absolute;
  inset: 0;
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.3s ease;
}

.year-scrollbar.active .year-labels {
  opacity: 1;
}

.year-label {
  position: absolute;
  right: 18px;
  top: clamp(12px, calc(var(--pos) * 100%), calc(100% - 12px));
  transform: translateY(-50%);
  font-size: 13px;
  font-weight: 500;
  color: rgba(255, 255, 255, 0.55);
  white-space: nowrap;
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
  line-height: 1;
  background: rgba(0, 0, 0, 0.45);
  padding: 3px 7px;
  border-radius: 4px;
  transition: color 0.15s, background 0.15s;
}

.year-label.active {
  color: #fff;
  font-weight: 700;
  font-size: 14px;
  background: rgba(0, 0, 0, 0.7);
  padding: 4px 8px;
  border-radius: 5px;
}

/* ── draggable handle (touch only) ──────────────────── */
.year-handle {
  display: none;
  position: absolute;
  right: 0;
  width: 14px;
  height: 48px;
  border-radius: 4px 0 0 4px;
  background: linear-gradient(
    160deg,
    rgba(255, 255, 255, 0.22) 0%,
    rgba(160, 170, 200, 0.14) 55%,
    rgba(80,  90,  120, 0.22) 100%
  );
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-right: none;
  box-shadow:
    inset 1px  1px 0 rgba(255, 255, 255, 0.35),
    inset 0   -1px 0 rgba(0, 0, 0, 0.25),
    -3px 4px 12px rgba(0, 0, 0, 0.45);
  top: clamp(24px, calc(var(--hpos) * 100%), calc(100% - 24px));
  transform: translateY(-50%);
  pointer-events: none;
  transition: background 0.2s, box-shadow 0.2s;
}

.year-scrollbar.is-touch .year-handle {
  display: block;
}

.year-scrollbar.active .year-handle {
  background: linear-gradient(
    160deg,
    rgba(255, 255, 255, 0.55) 0%,
    rgba(200, 210, 235, 0.38) 55%,
    rgba(130, 145, 180, 0.45) 100%
  );
  box-shadow:
    inset 1px  1px 0 rgba(255, 255, 255, 0.65),
    inset 0   -1px 0 rgba(0, 0, 0, 0.3),
    -4px 5px 16px rgba(0, 0, 0, 0.6);
}
</style>
