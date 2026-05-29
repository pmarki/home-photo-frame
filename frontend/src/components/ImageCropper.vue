<template>
  <div class="cropper-shell">

    <!-- Header -->
    <div class="cropper-header">
      <span class="cropper-title">{{ filename || 'Crop image' }}</span>
      <span class="cropper-hint">Drag to select · drag box to move · drag handles to resize</span>
    </div>

    <!-- Stage -->
    <div
      class="cropper-stage"
      ref="stageRef"
      @mousedown.prevent="onStageDown"
      @touchstart.prevent="onStageTouchStart"
    >
      <img
        ref="imgRef"
        :src="src"
        class="cropper-img"
        draggable="false"
        @load="onImgLoad"
        @error="imgLoadError = true"
      />
      <div v-if="imgLoadError" class="cropper-load-error">Failed to load image</div>

      <!-- Crop box (box-shadow provides the dark overlay around it) -->
      <div
        v-if="sel"
        class="crop-box"
        :style="boxStyle"
        @mousedown.stop.prevent="e => startDrag('move', e)"
        @touchstart.stop.prevent="e => startTouchDrag('move', e)"
      >
        <!-- 8 resize handles -->
        <div
          v-for="h in HANDLES" :key="h"
          :class="`rh rh-${h}`"
          @mousedown.stop.prevent="e => startDrag(h, e)"
          @touchstart.stop.prevent="e => startTouchDrag(h, e)"
        />
        <!-- Pixel dimensions label -->
        <span class="crop-dims">{{ cropDims }}</span>
      </div>
    </div>

    <!-- Footer -->
    <div class="cropper-footer">
      <button class="btn-cancel" @click="$emit('cancel')">Cancel</button>
      <button class="btn-crop" :disabled="!sel || imgLoadError" @click="confirmCrop">Apply Crop</button>
    </div>

  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'

const HANDLES = ['nw', 'n', 'ne', 'e', 'se', 's', 'sw', 'w']

const props = defineProps({
  src:      { type: String, required: true },
  filename: { type: String, default: '' },
})

const emit = defineEmits(['crop', 'cancel'])

const stageRef = ref(null)
const imgRef   = ref(null)
const naturalW = ref(0)
const naturalH = ref(0)
// Image's rendered position & size within the stage (updated on load + resize)
const imgOfsX  = ref(0)
const imgOfsY  = ref(0)
const imgDispW = ref(1)
const imgDispH = ref(1)
// Current selection in stage coordinates { x, y, w, h }
const sel = ref(null)
const imgLoadError = ref(false)

// Active drag: { type, startX, startY, startSel }
// type is 'new' | 'move' | 'nw'|'n'|'ne'|'e'|'se'|'s'|'sw'|'w'
let drag = null

// ── Image load ────────────────────────────────────────────────────────

function onImgLoad() {
  naturalW.value = imgRef.value.naturalWidth
  naturalH.value = imgRef.value.naturalHeight
  syncImgRect()
}

function syncImgRect() {
  if (!imgRef.value || !stageRef.value) return
  const ir = imgRef.value.getBoundingClientRect()
  const sr = stageRef.value.getBoundingClientRect()
  imgOfsX.value  = ir.left - sr.left
  imgOfsY.value  = ir.top  - sr.top
  imgDispW.value = ir.width  || 1
  imgDispH.value = ir.height || 1
}

// ── Coordinate helpers ─────────────────────────────────────────────────

function clientToStage(clientX, clientY) {
  const r = stageRef.value.getBoundingClientRect()
  return { x: clientX - r.left, y: clientY - r.top }
}

// Clamp selection so it stays within the image's rendered bounds.
// Preserves the far edge when clamping the near edge (so resize handles feel natural).
function clampToImg(s) {
  const ox = imgOfsX.value, oy = imgOfsY.value
  const iw = imgDispW.value, ih = imgDispH.value
  let { x, y, w, h } = s

  if (x < ox)          { w -= ox - x;   x = ox }
  if (y < oy)          { h -= oy - y;   y = oy }
  if (x + w > ox + iw) { w = ox + iw - x }
  if (y + h > oy + ih) { h = oy + ih - y }
  if (w < 2) w = 2
  if (h < 2) h = 2
  return { x, y, w, h }
}

// ── Drag start ─────────────────────────────────────────────────────────

function startDrag(type, e) {
  const p = clientToStage(e.clientX, e.clientY)
  drag = { type, startX: p.x, startY: p.y, startSel: sel.value ? { ...sel.value } : null }
}

function startTouchDrag(type, e) {
  const t = e.touches[0]
  const p = clientToStage(t.clientX, t.clientY)
  drag = { type, startX: p.x, startY: p.y, startSel: sel.value ? { ...sel.value } : null }
}

function onStageDown(e) {
  const p = clientToStage(e.clientX, e.clientY)
  if (!insideImg(p)) return
  drag = { type: 'new', startX: p.x, startY: p.y, startSel: null }
  sel.value = null
}

function onStageTouchStart(e) {
  const t = e.touches[0]
  const p = clientToStage(t.clientX, t.clientY)
  if (!insideImg(p)) return
  drag = { type: 'new', startX: p.x, startY: p.y, startSel: null }
  sel.value = null
}

function insideImg({ x, y }) {
  const ox = imgOfsX.value, oy = imgOfsY.value
  return x >= ox && x <= ox + imgDispW.value && y >= oy && y <= oy + imgDispH.value
}

// ── Global drag handlers (registered on window so drag survives leaving stage) ──

function onGlobalMouseMove(e) {
  if (!drag) return
  applyDrag(clientToStage(e.clientX, e.clientY))
}

function onGlobalMouseUp() {
  finishDrag()
}

function onGlobalTouchMove(e) {
  if (!drag) return
  e.preventDefault()
  const t = e.touches[0]
  applyDrag(clientToStage(t.clientX, t.clientY))
}

function onGlobalTouchEnd() {
  finishDrag()
}

function applyDrag(p) {
  const dx = p.x - drag.startX
  const dy = p.y - drag.startY

  if (drag.type === 'new') {
    const x = Math.min(drag.startX, p.x)
    const y = Math.min(drag.startY, p.y)
    const w = Math.abs(dx)
    const h = Math.abs(dy)
    if (w > 2 || h > 2) sel.value = clampToImg({ x, y, w, h })

  } else if (drag.type === 'move') {
    sel.value = clampToImg({
      x: drag.startSel.x + dx,
      y: drag.startSel.y + dy,
      w: drag.startSel.w,
      h: drag.startSel.h,
    })

  } else {
    sel.value = clampToImg(applyHandle(drag.type, drag.startSel, dx, dy))
  }
}

function finishDrag() {
  // Discard accidental clicks (tiny selection)
  if (drag?.type === 'new' && sel.value && sel.value.w < 4 && sel.value.h < 4) {
    sel.value = null
  }
  drag = null
}

function applyHandle(type, s, dx, dy) {
  let { x, y, w, h } = s
  if (type.includes('n')) { const ny = y + dy; h = h - (ny - y); y = ny }
  if (type.includes('s')) { h += dy }
  if (type.includes('w')) { const nx = x + dx; w = w - (nx - x); x = nx }
  if (type.includes('e')) { w += dx }
  // Keep minimum size and anchor the opposite edge
  if (w < 4) { if (type.includes('w')) x = s.x + s.w - 4; w = 4 }
  if (h < 4) { if (type.includes('n')) y = s.y + s.h - 4; h = 4 }
  return { x, y, w, h }
}

// ── Computed ───────────────────────────────────────────────────────────

const boxStyle = computed(() => {
  if (!sel.value) return {}
  const { x, y, w, h } = sel.value
  return { left: x + 'px', top: y + 'px', width: w + 'px', height: h + 'px' }
})

const cropDims = computed(() => {
  if (!sel.value || !imgDispW.value) return ''
  const sx = naturalW.value / imgDispW.value
  const sy = naturalH.value / imgDispH.value
  return `${Math.round(sel.value.w * sx)} × ${Math.round(sel.value.h * sy)}`
})

// ── Confirm crop ───────────────────────────────────────────────────────

function confirmCrop() {
  if (!sel.value) return
  syncImgRect()   // ensure we use current layout
  const sx = naturalW.value / imgDispW.value
  const sy = naturalH.value / imgDispH.value
  emit('crop', {
    x:      Math.round((sel.value.x - imgOfsX.value) * sx),
    y:      Math.round((sel.value.y - imgOfsY.value) * sy),
    width:  Math.round(sel.value.w * sx),
    height: Math.round(sel.value.h * sy),
  })
}

// ── Lifecycle ──────────────────────────────────────────────────────────

onMounted(() => {
  window.addEventListener('mousemove', onGlobalMouseMove)
  window.addEventListener('mouseup',   onGlobalMouseUp)
  window.addEventListener('touchmove', onGlobalTouchMove, { passive: false })
  window.addEventListener('touchend',  onGlobalTouchEnd)
  document.body.style.overflow = 'hidden'
})

onUnmounted(() => {
  window.removeEventListener('mousemove', onGlobalMouseMove)
  window.removeEventListener('mouseup',   onGlobalMouseUp)
  window.removeEventListener('touchmove', onGlobalTouchMove)
  window.removeEventListener('touchend',  onGlobalTouchEnd)
  document.body.style.overflow = ''
})
</script>

<style scoped>
/* ─── Shell ─────────────────────────────────────────────────────────── */
.cropper-shell {
  position: fixed;
  inset: 0;
  z-index: 10000;
  background: #0a0a0e;
  display: flex;
  flex-direction: column;
  padding-top: env(safe-area-inset-top);
  padding-bottom: env(safe-area-inset-bottom);
}

/* ─── Header ────────────────────────────────────────────────────────── */
.cropper-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 16px;
  background: rgba(0,0,0,0.55);
}

.cropper-title {
  font-size: 0.85rem;
  color: #bbb;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.cropper-hint {
  font-size: 0.75rem;
  color: #555;
  flex-shrink: 0;
}

/* ─── Stage ─────────────────────────────────────────────────────────── */
.cropper-stage {
  flex: 1;
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  cursor: crosshair;
  min-height: 0;
  user-select: none;
}

.cropper-img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
  display: block;
  pointer-events: none;
  user-select: none;
  -webkit-user-drag: none;
}

/* ─── Load error ────────────────────────────────────────────────────── */
.cropper-load-error {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  color: #f87171;
  font-size: 0.9rem;
  background: rgba(0, 0, 0, 0.6);
  pointer-events: none;
}

/* ─── Crop box ──────────────────────────────────────────────────────── */
.crop-box {
  position: absolute;
  box-sizing: border-box;
  border: 1.5px solid rgba(255, 255, 255, 0.9);
  /* Giant shadow acts as the dark overlay outside the selection */
  box-shadow: 0 0 0 9999px rgba(0, 0, 0, 0.52), inset 0 0 0 1px rgba(0, 0, 0, 0.25);
  cursor: move;
  z-index: 2;
}

/* ─── Resize handles ────────────────────────────────────────────────── */
.rh {
  position: absolute;
  width: 11px;
  height: 11px;
  background: #fff;
  border: 1px solid rgba(0, 0, 0, 0.35);
  border-radius: 1px;
  z-index: 3;
}
/* Corners */
.rh-nw { top: -6px;  left: -6px;  cursor: nwse-resize; }
.rh-ne { top: -6px;  right: -6px; cursor: nesw-resize; }
.rh-se { bottom: -6px; right: -6px; cursor: nwse-resize; }
.rh-sw { bottom: -6px; left: -6px;  cursor: nesw-resize; }
/* Edges */
.rh-n { top: -6px;    left: calc(50% - 5px); cursor: ns-resize; }
.rh-s { bottom: -6px; left: calc(50% - 5px); cursor: ns-resize; }
.rh-w { top: calc(50% - 5px); left: -6px;    cursor: ew-resize; }
.rh-e { top: calc(50% - 5px); right: -6px;   cursor: ew-resize; }

/* ─── Dimension label ───────────────────────────────────────────────── */
.crop-dims {
  position: absolute;
  bottom: 5px;
  left: 50%;
  transform: translateX(-50%);
  font-size: 0.72rem;
  color: #fff;
  background: rgba(0, 0, 0, 0.6);
  padding: 2px 7px;
  border-radius: 3px;
  pointer-events: none;
  white-space: nowrap;
}

/* ─── Footer ────────────────────────────────────────────────────────── */
.cropper-footer {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 10px;
  padding: 12px 16px;
  background: rgba(0, 0, 0, 0.6);
}

.btn-cancel {
  background: rgba(255, 255, 255, 0.08);
  border: none;
  color: #aaa;
  padding: 8px 20px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 0.9rem;
}
.btn-cancel:hover { background: rgba(255, 255, 255, 0.15); color: #fff; }

.btn-crop {
  background: rgba(90, 130, 255, 0.85);
  border: none;
  color: #fff;
  padding: 8px 22px;
  border-radius: 6px;
  cursor: pointer;
  font-size: 0.9rem;
  font-weight: 500;
  transition: background 0.15s;
}
.btn-crop:hover:not(:disabled) { background: rgba(90, 130, 255, 1); }
.btn-crop:disabled { opacity: 0.35; cursor: default; }
</style>
