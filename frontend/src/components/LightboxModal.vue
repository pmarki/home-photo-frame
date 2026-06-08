<template>
  <ImageCropper
    v-if="cropping"
    :src="currentImage.original"
    :filename="currentImage.filename"
    @crop="applyCrop"
    @cancel="cancelCrop"
  />

  <div class="lb-overlay" @click.self="onOverlayClick" role="dialog" aria-modal="true" :aria-label="currentImage.filename">

    <!-- Action error banner -->
    <div v-if="actionError" class="lb-action-error" role="alert">{{ actionError }}</div>

    <!-- Top bar -->
    <div class="lb-header">
      <span class="lb-filename">{{ currentImage.filename }}</span>
      <div class="lb-header-actions">
        <!-- Delete: first tap arms, second tap confirms -->
        <button
          :class="['lb-icon-btn', { 'lb-icon-btn--danger': deleteArmed }]"
          :title="deleteArmed ? 'Tap again to confirm delete' : 'Delete image'"
          @click="onDeleteClick"
          :disabled="deleting"
        >
          <svg v-if="!deleteArmed" viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="3 6 5 6 21 6"/>
            <path d="M19 6l-1 14a2 2 0 01-2 2H8a2 2 0 01-2-2L5 6"/>
            <path d="M10 11v6M14 11v6"/>
            <path d="M9 6V4a1 1 0 011-1h4a1 1 0 011 1v2"/>
          </svg>
          <svg v-else viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2">
            <polyline points="20 6 9 17 4 12"/>
          </svg>
        </button>
        <div class="lb-actions-sep" aria-hidden="true" />
        <!-- Share: uses Web Share API when available, falls back to download -->
        <button class="lb-icon-btn" :title="canShare ? 'Share original' : 'Download original'" @click="shareOrDownload" :disabled="sharing">
          <svg v-if="canShare" viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round">
            <circle cx="18" cy="5" r="3"/>
            <circle cx="6" cy="12" r="3"/>
            <circle cx="18" cy="19" r="3"/>
            <line x1="8.59" y1="13.51" x2="15.42" y2="17.49"/>
            <line x1="15.41" y1="6.51" x2="8.59" y2="10.49"/>
          </svg>
          <svg v-else viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2">
            <path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4"/>
            <polyline points="7 10 12 15 17 10"/>
            <line x1="12" y1="15" x2="12" y2="3"/>
          </svg>
        </button>
        <!-- Crop (hidden for videos but kept in DOM so header width stays constant) -->
        <button
          v-show="!isVideo(currentImage.filename)"
          class="lb-icon-btn"
          title="Crop image"
          @click="openCrop"
        >
          <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <polyline points="6 2 6 18 22 18"/>
            <polyline points="2 6 18 6 18 22"/>
          </svg>
        </button>
        <button class="lb-icon-btn" title="Close (Esc)" @click="onCloseClick">
          <svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2">
            <line x1="18" y1="6" x2="6" y2="18"/>
            <line x1="6" y1="6" x2="18" y2="18"/>
          </svg>
        </button>
      </div>
    </div>

    <!-- Image area -->
    <div class="lb-stage" ref="stageRef">
      <!-- Prev / Next buttons -->
      <button
        v-if="currentIndex > 0"
        class="lb-nav lb-prev"
        title="Previous (←)"
        @click="navigate(-1)"
        aria-label="Previous image"
      >
        <svg viewBox="0 0 24 24" width="26" height="26" fill="none" stroke="currentColor" stroke-width="2.5">
          <polyline points="15 18 9 12 15 6"/>
        </svg>
      </button>

      <transition :name="transitionName" mode="out-in">
        <div class="lb-image-wrap" :key="currentIndex">
          <div
            class="lb-zoom-wrap"
            :style="zoomedStyle"
            @dblclick="onDblClick"
            @pointerdown="onPanStart"
            @pointermove="onPanMove"
            @pointerup="onPanEnd"
            @pointercancel="onPanEnd"
          >
            <video
              v-if="isVideo(currentImage.filename)"
              :src="currentImage.original"
              class="lb-image"
              :class="{ loaded: imgLoaded }"
              autoplay muted loop playsinline controls
              @loadeddata="imgLoaded = true"
              @error="imgError = true"
            />
            <img
              v-else
              :src="currentImage.thumbMedium || currentImage.original"
              :alt="currentImage.filename"
              class="lb-image"
              :class="{ loaded: imgLoaded }"
              @load="imgLoaded = true"
              @error="imgError = true"
            />
          </div>
          <div v-if="!imgLoaded && !imgError" class="lb-spinner-wrap" aria-hidden="true">
            <div class="spinner" />
          </div>
          <div v-if="imgError" class="lb-error">Failed to load</div>
        </div>
      </transition>

      <button
        v-if="currentIndex < images.length - 1 || hasMore"
        class="lb-nav lb-next"
        title="Next (→)"
        @click="navigate(1)"
        aria-label="Next image"
        :disabled="currentIndex === images.length - 1 && hasMore"
      >
        <svg viewBox="0 0 24 24" width="26" height="26" fill="none" stroke="currentColor" stroke-width="2.5">
          <polyline points="9 18 15 12 9 6"/>
        </svg>
      </button>
    </div>

    <!-- Bottom bar -->
    <div class="lb-footer">
      <span class="lb-path" :class="{ copied: pathCopied }" @click="copyPath" title="Copy path">{{ pathCopied ? 'Copied!' : currentImage.path }}</span>
      <span class="lb-date">{{ formatDate(currentImage.modTime) }}</span>
    </div>
  </div>
</template>

<script setup>
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import ImageCropper from './ImageCropper.vue'

const props = defineProps({
  images:       { type: Array,   required: true },
  initialIndex: { type: Number,  default: 0 },
  hasMore:      { type: Boolean, default: false }
})

const emit = defineEmits(['close', 'need-more', 'deleted', 'cropped'])

const stageRef     = ref(null)
const currentIndex = ref(props.initialIndex)
const imgLoaded    = ref(false)
const imgError     = ref(false)
const transitionName = ref('slide-next')
const sharing      = ref(false)
const deleteArmed  = ref(false)
const deleting     = ref(false)
const cropping     = ref(false)
const actionError  = ref('')
const pathCopied   = ref(false)
const zoomed       = ref(false)
const zoomPanX     = ref(0)
const zoomPanY     = ref(0)
const isPanning    = ref(false)
let panStart       = null
let lastTapTime    = 0
let touchPanX      = 0
let touchPanY      = 0

const zoomedStyle = computed(() => {
  if (!zoomed.value) return { cursor: 'zoom-in', transition: 'transform 0.25s ease' }
  return {
    transform: `translate(${zoomPanX.value}px, ${zoomPanY.value}px) scale(2)`,
    transition: isPanning.value ? 'none' : 'transform 0.25s ease',
    cursor: isPanning.value ? 'grabbing' : 'grab',
  }
})

const isVideo = (filename) => /\.(mp4|webm|mov|m4v)$/i.test(filename ?? '')

const canShare = typeof navigator !== 'undefined' && typeof navigator.share === 'function'

function openCrop() {
  cropping.value = true
}

function cancelCrop() {
  cropping.value = false
}

async function copyPath() {
  const p = currentImage.value.path
  if (!p) return
  try {
    await navigator.clipboard.writeText(p)
    pathCopied.value = true
    setTimeout(() => { pathCopied.value = false }, 1500)
  } catch {
    // clipboard access denied — silently ignore
  }
}

function resetZoom() {
  zoomed.value = false
  zoomPanX.value = 0
  zoomPanY.value = 0
  isPanning.value = false
  panStart = null
}

function toggleZoom(clientX, clientY) {
  if (zoomed.value) { resetZoom(); return }
  const rect = stageRef.value.getBoundingClientRect()
  zoomPanX.value = -(clientX - rect.left - rect.width / 2)
  zoomPanY.value = -(clientY - rect.top - rect.height / 2)
  zoomed.value = true
}

function onDblClick(e) {
  if (isVideo(currentImage.value.filename)) return
  toggleZoom(e.clientX, e.clientY)
}

function onPanStart(e) {
  if (!zoomed.value || e.pointerType === 'touch') return
  isPanning.value = true
  panStart = { x: e.clientX - zoomPanX.value, y: e.clientY - zoomPanY.value }
  e.currentTarget.setPointerCapture(e.pointerId)
}

function onPanMove(e) {
  if (!isPanning.value || e.pointerType === 'touch') return
  zoomPanX.value = e.clientX - panStart.x
  zoomPanY.value = e.clientY - panStart.y
}

function onPanEnd(e) {
  if (e.pointerType === 'touch') return
  isPanning.value = false
  panStart = null
}

function onCloseClick() {
  emit('close')
}

function onOverlayClick() {
  emit('close')
}

// Single action: share via Web Share API when possible, download otherwise
async function shareOrDownload() {
  if (sharing.value) return
  sharing.value = true
  const img = currentImage.value
  const url = img.original
  try {
    if (canShare) {
      const res = await fetch(url)
      const blob = await res.blob()
      const file = new File([blob], img.filename, { type: blob.type })
      if (navigator.canShare?.({ files: [file] })) {
        await navigator.share({ files: [file], title: img.filename })
      } else {
        await navigator.share({ title: img.filename, url: window.location.origin + url })
      }
    } else {
      // Fallback: trigger browser download of the original
      const a = document.createElement('a')
      a.href = url
      a.download = img.filename
      a.click()
    }
  } catch (e) {
    if (e.name !== 'AbortError') {
      actionError.value = e.message || 'Share failed'
      setTimeout(() => { actionError.value = '' }, 3000)
    }
  } finally {
    sharing.value = false
  }
}

const currentImage = computed(() => props.images[currentIndex.value] ?? { filename: '', path: '', modTime: null })

async function applyCrop(rect) {
  const imgPath = currentImage.value.path || currentImage.value.filename
  const encodedPath = imgPath.split('/').map(encodeURIComponent).join('/')
  try {
    const res = await fetch(`/api/crop/${encodedPath}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(rect),
    })
    if (!res.ok) throw new Error(await res.text())
    const newImage = await res.json()
    emit('cropped', imgPath, newImage)
  } catch (e) {
    console.error('crop failed:', e)
    actionError.value = e.message || 'Crop failed'
    setTimeout(() => { actionError.value = '' }, 3000)
  } finally {
    cropping.value = false
  }
}

async function onDeleteClick() {
  if (deleting.value) return
  if (!deleteArmed.value) {
    deleteArmed.value = true
    return
  }
  deleting.value = true
  const imgPath = currentImage.value.path || currentImage.value.filename
  const encodedPath = imgPath.split('/').map(encodeURIComponent).join('/')
  try {
    const res = await fetch(`/api/delete/${encodedPath}`, { method: 'DELETE' })
    if (!res.ok) throw new Error(`Server error ${res.status}`)
    // Clamp index before notifying parent so the lightbox shows the right image immediately
    const newLen = props.images.length - 1
    if (currentIndex.value >= newLen && newLen > 0) {
      currentIndex.value = newLen - 1
    }
    emit('deleted', imgPath)
  } catch (e) {
    console.error('delete failed:', e)
    actionError.value = e.message || 'Delete failed'
    setTimeout(() => { actionError.value = '' }, 3000)
  } finally {
    deleting.value = false
    deleteArmed.value = false
  }
}

function navigate(dir) {
  const next = currentIndex.value + dir
  if (next < 0 || next >= props.images.length) return
  transitionName.value = dir > 0 ? 'slide-next' : 'slide-prev'
  imgLoaded.value = false
  imgError.value  = false
  deleteArmed.value = false
  cropping.value = false
  resetZoom()
  currentIndex.value = next

  if (next >= props.images.length - 3 && props.hasMore) {
    emit('need-more')
  }
}

// ── Keyboard navigation ───────────────────────────────────────────────
function onKeydown(e) {
  if (e.key === 'ArrowLeft')       navigate(-1)
  else if (e.key === 'ArrowRight') navigate(1)
  else if (e.key === 'Escape') {
    if (cropping.value) cancelCrop()
    else emit('close')
  }
}

// ── Touch/swipe navigation ────────────────────────────────────────────
let touchStartX = 0
let touchStartY = 0

function onTouchStart(e) {
  if (e.touches.length !== 1) return
  touchStartX = touchPanX = e.touches[0].clientX
  touchStartY = touchPanY = e.touches[0].clientY
}

function onTouchMove(e) {
  if (!zoomed.value || e.touches.length !== 1) return
  e.preventDefault()
  const t = e.touches[0]
  zoomPanX.value += t.clientX - touchPanX
  zoomPanY.value += t.clientY - touchPanY
  touchPanX = t.clientX
  touchPanY = t.clientY
}

function onTouchEnd(e) {
  if (cropping.value) return
  const now = Date.now()
  const t = e.changedTouches[0]
  const dx = touchStartX - t.clientX
  const dy = touchStartY - t.clientY
  // Double-tap → toggle zoom (takes priority over swipe)
  if (Math.abs(dx) < 20 && Math.abs(dy) < 20 && now - lastTapTime < 300) {
    toggleZoom(t.clientX, t.clientY)
    lastTapTime = 0
    return
  }
  lastTapTime = now
  // Swipe navigation disabled while zoomed
  if (zoomed.value) return
  if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5) {
    navigate(dx > 0 ? 1 : -1)
  }
}

// ── Date formatting ───────────────────────────────────────────────────
const dateFormatter = new Intl.DateTimeFormat(undefined, {
  year: 'numeric', month: 'short', day: 'numeric',
  hour: '2-digit', minute: '2-digit'
})

function formatDate(iso) {
  if (!iso) return ''
  return dateFormatter.format(new Date(iso))
}

// Reset image state when the displayed image changes (navigation or crop replacement)
watch(() => currentImage.value.filename, () => {
  imgLoaded.value = false
  imgError.value  = false
})

defineExpose({
  tryExitCrop() {
    if (!cropping.value) return false
    cropping.value = false
    return true
  }
})

onMounted(() => {
  document.addEventListener('keydown', onKeydown)
  document.addEventListener('touchstart', onTouchStart, { passive: true })
  document.addEventListener('touchmove', onTouchMove, { passive: false })
  document.addEventListener('touchend', onTouchEnd, { passive: true })
  document.body.style.overflow = 'hidden'
})

onUnmounted(() => {
  document.removeEventListener('keydown', onKeydown)
  document.removeEventListener('touchstart', onTouchStart)
  document.removeEventListener('touchmove', onTouchMove)
  document.removeEventListener('touchend', onTouchEnd)
  document.body.style.overflow = ''
})
</script>

<style scoped>
/* ─── Overlay ──────────────────────────────────────────────────────── */
.lb-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0, 0, 0, 0.96);
  z-index: 9999;
  display: flex;
  flex-direction: column;
  /* Safe area insets for notched phones */
  padding-top: env(safe-area-inset-top);
  padding-bottom: env(safe-area-inset-bottom);
}

/* ─── Action error banner ──────────────────────────────────────────── */
.lb-action-error {
  flex-shrink: 0;
  padding: 7px 14px;
  background: rgba(220, 60, 60, 0.15);
  border-bottom: 1px solid rgba(220, 60, 60, 0.3);
  color: #f87171;
  font-size: 0.82rem;
  text-align: center;
}

/* ─── Header ───────────────────────────────────────────────────────── */
.lb-header {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 14px;
  background: rgba(0,0,0,0.5);
  gap: 12px;
}

.lb-filename {
  font-size: 0.85rem;
  color: #bbb;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}

.lb-header-actions {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-shrink: 0;
}

.lb-actions-sep {
  width: 1px;
  height: 20px;
  background: rgba(255, 255, 255, 0.12);
  margin: 0 2px;
}

.lb-icon-btn {
  background: rgba(255, 255, 255, 0.08);
  border: none;
  border-radius: 8px;
  color: #ccc;
  padding: 7px;
  cursor: pointer;
  display: flex;
  align-items: center;
  transition: background 0.15s, color 0.15s;
}
.lb-icon-btn:hover { background: rgba(255,255,255,0.18); color: #fff; }
.lb-icon-btn:disabled { opacity: 0.4; cursor: default; }
.lb-icon-btn--danger { background: rgba(220, 60, 60, 0.25); color: #f87171; }
.lb-icon-btn--danger:hover { background: rgba(220, 60, 60, 0.4); color: #fca5a5; }

/* ─── Stage (image + nav arrows) ──────────────────────────────────── */
.lb-stage {
  flex: 1;
  position: relative;
  display: flex;
  align-items: center;
  justify-content: center;
  overflow: hidden;
  min-height: 0;
}

/* ─── Image wrapper ────────────────────────────────────────────────── */
.lb-image-wrap {
  width: 100%;
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
  position: absolute;
  inset: 0;
}

.lb-zoom-wrap {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  transform-origin: center center;
  touch-action: none;
}

.lb-image {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
  display: block;
  opacity: 0;
  transition: opacity 0.25s;
  user-select: none;
  -webkit-user-drag: none;
}
.lb-image.loaded { opacity: 1; }

.lb-spinner-wrap {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
}

.spinner {
  width: 40px;
  height: 40px;
  border: 3px solid rgba(255,255,255,0.08);
  border-top-color: rgba(150,170,255,0.85);
  border-radius: 50%;
  animation: spin 0.75s linear infinite;
}
@keyframes spin { to { transform: rotate(360deg); } }

.lb-error {
  color: #f87171;
  font-size: 0.9rem;
}

/* ─── Navigation arrows ────────────────────────────────────────────── */
.lb-nav {
  position: absolute;
  top: 50%;
  transform: translateY(-50%);
  z-index: 10;
  background: rgba(255,255,255,0.1);
  border: 1px solid rgba(255,255,255,0.08);
  border-radius: 50%;
  width: 52px;
  height: 52px;
  display: flex;
  align-items: center;
  justify-content: center;
  cursor: pointer;
  color: #ddd;
  transition: background 0.15s, color 0.15s;
}
.lb-nav:hover { background: rgba(255,255,255,0.22); color: #fff; }
.lb-nav:disabled { opacity: 0.3; cursor: default; }
.lb-prev { left: 14px; }
.lb-next { right: 14px; }

/* ─── Footer ───────────────────────────────────────────────────────── */
.lb-footer {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 14px;
  background: rgba(0,0,0,0.5);
}

.lb-path {
  font-size: 0.78rem;
  color: #666;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
  cursor: pointer;
  transition: color 0.15s;
}
.lb-path:hover { color: #999; }
.lb-path.copied { color: #6ee7b7; }
.lb-date { font-size: 0.78rem; color: #555; flex-shrink: 0; }

/* ─── Slide transitions ────────────────────────────────────────────── */
.slide-next-enter-active,
.slide-next-leave-active,
.slide-prev-enter-active,
.slide-prev-leave-active {
  transition: transform 0.22s ease, opacity 0.22s ease;
  position: absolute;
  inset: 0;
}

.slide-next-enter-from  { transform: translateX(8%);  opacity: 0; }
.slide-next-leave-to    { transform: translateX(-8%); opacity: 0; }
.slide-prev-enter-from  { transform: translateX(-8%); opacity: 0; }
.slide-prev-leave-to    { transform: translateX(8%);  opacity: 0; }
</style>
