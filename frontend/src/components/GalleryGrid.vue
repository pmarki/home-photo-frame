<template>
  <div class="gallery-wrapper">
    <!-- Empty state -->
    <div v-if="!loading && images.length === 0" class="gallery-empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2" class="empty-icon">
        <path d="M23 19a2 2 0 01-2 2H3a2 2 0 01-2-2V8a2 2 0 012-2h4l2-3h6l2 3h4a2 2 0 012 2z"/>
        <circle cx="12" cy="13" r="4"/>
      </svg>
      <p>No photos found in the <code>photos/</code> folder</p>
    </div>

    <!-- Grid -->
    <div v-else class="gallery-grid" role="list">
      <button
        v-for="(img, idx) in images"
        :key="img.filename"
        class="gallery-item"
        role="listitem"
        :aria-label="img.filename"
        :style="{ '--cover-scale': coverScales[img.filename] }"
        @click="$emit('open', idx)"
      >
        <img
          :src="img.thumbSmall"
          :alt="img.filename"
          loading="lazy"
          decoding="async"
          class="gallery-thumb"
          @error="onImgError"
        />
        <div v-if="isVideo(img.filename)" class="video-badge" aria-hidden="true">▶</div>
        <div class="gallery-overlay" aria-hidden="true">
          <span class="gallery-name">{{ img.filename }}</span>
        </div>
        <div
          v-if="img.width && img.height"
          class="orientation-badge"
          :class="imgOrientation(img)"
          aria-hidden="true"
        />
      </button>
    </div>

    <!-- Infinite-scroll sentinel (observed by IntersectionObserver) -->
    <div ref="sentinel" class="sentinel" aria-hidden="true" />

    <!-- Loading spinner -->
    <div v-if="loading" class="gallery-loading" aria-label="Loading photos">
      <div class="spinner" />
    </div>

    <!-- All-loaded notice -->
    <div v-if="!hasMore && images.length > 0" class="gallery-end">
      All {{ images.length }} photos loaded
    </div>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, watch } from 'vue'

const props = defineProps({
  images:  { type: Array,   required: true },
  loading: { type: Boolean, default: false },
  hasMore: { type: Boolean, default: false }
})

const emit = defineEmits(['load-more', 'open'])

const isVideo = (filename) => filename?.toLowerCase().endsWith('.mp4')

const sentinel = ref(null)
let observer = null

function setupObserver() {
  if (observer) observer.disconnect()
  observer = new IntersectionObserver(
    ([entry]) => {
      if (entry.isIntersecting && props.hasMore && !props.loading) {
        emit('load-more')
      }
    },
    { rootMargin: '600px' } // start fetching 600 px before the user reaches the bottom
  )
  if (sentinel.value) observer.observe(sentinel.value)
}

// Precomputed cover-scale per filename; recomputed only when images array changes.
const coverScales = computed(() => {
  const map = Object.create(null)
  for (const img of props.images) {
    map[img.filename] = (!img.width || !img.height) ? 1 :
      Math.max(img.width, img.height) / Math.min(img.width, img.height)
  }
  return map
})

// Fall back image: hide broken thumbnails gracefully
function onImgError(e) {
  e.target.style.opacity = '0.15'
}

function imgOrientation(img) {
  if (img.width > img.height) return 'landscape'
  if (img.height > img.width) return 'portrait'
  return 'square'
}

onMounted(setupObserver)
onUnmounted(() => observer?.disconnect())

// Reconnect observer whenever hasMore changes (e.g. after sort resets)
watch(() => props.hasMore, (val) => {
  if (val) setupObserver()
  else observer?.disconnect()
})
</script>

<style scoped>
.gallery-wrapper {
  padding: 16px 0 32px;
}

/* ─── Empty state ──────────────────────────────────────────────────── */
.gallery-empty {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 16px;
  padding: 80px 20px;
  color: #444;
}
.empty-icon { width: 64px; height: 64px; }
.gallery-empty p { font-size: 0.95rem; }
.gallery-empty code {
  background: rgba(255,255,255,0.07);
  padding: 1px 6px;
  border-radius: 4px;
  font-size: 0.9em;
}

/* ─── Grid ─────────────────────────────────────────────────────────── */
.gallery-grid {
  display: grid;
  /* Responsive columns: at least 160 px wide, fill available space */
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: 3px;
  contain: layout;     /* Limit browser reflow scope */
}

@media (min-width: 480px)  { .gallery-grid { grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); gap: 4px; } }
@media (min-width: 768px)  { .gallery-grid { grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 5px; } }
@media (min-width: 1280px) { .gallery-grid { grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 6px; } }
@media (min-width: 1800px) { .gallery-grid { grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); } }

/* ─── Individual item ──────────────────────────────────────────────── */
.gallery-item {
  position: relative;
  aspect-ratio: 1;
  overflow: hidden;
  border-radius: 2px;
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 55%, black);
  cursor: pointer;
  border: none;
  padding: 0;
  display: block;
}

.gallery-thumb {
  width: 100%;
  height: 100%;
  object-fit: contain;
  display: block;
  /* Scale up to fill the square (simulates cover). Hover reverts to 1 to reveal the full image. */
  transform: scale(var(--cover-scale, 1));
  transition: transform 0.3s ease;
}

/* ─── Orientation badge ─────────────────────────────────────────────── */
.video-badge {
  position: absolute;
  top: 8px;
  left: 8px;
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  font-size: 0.65rem;
  display: flex;
  align-items: center;
  justify-content: center;
  pointer-events: none;
  padding-left: 2px; /* optical centre for ▶ */
}

.orientation-badge {
  position: absolute;
  bottom: 5px;
  right: 5px;
  border: 1.5px solid rgba(255, 255, 255, 0.65);
  background: rgba(0, 0, 0, 0.45);
  border-radius: 1px;
  pointer-events: none;
}
.orientation-badge.landscape { width: 16px; height: 11px; }
.orientation-badge.portrait  { width: 11px; height: 16px; }
.orientation-badge.square    { width: 13px; height: 13px; }

.gallery-item:hover .gallery-thumb,
.gallery-item:focus-visible .gallery-thumb {
  transform: scale(1);
}

.gallery-item:focus-visible {
  outline: 2px solid rgba(100, 120, 220, 0.8);
  outline-offset: 2px;
}

/* ─── Hover overlay ────────────────────────────────────────────────── */
.gallery-overlay {
  position: absolute;
  inset: 0;
  background: linear-gradient(to top, rgba(0,0,0,0.75) 0%, transparent 55%);
  opacity: 0;
  transition: opacity 0.2s;
  display: flex;
  align-items: flex-end;
  padding: 8px 10px;
  pointer-events: none;
}

.gallery-item:hover .gallery-overlay,
.gallery-item:focus-visible .gallery-overlay {
  opacity: 1;
}

.gallery-name {
  font-size: 0.7rem;
  color: rgba(255,255,255,0.9);
  word-break: break-all;
  line-height: 1.3;
}

/* ─── Loading / sentinel / end ─────────────────────────────────────── */
.sentinel { height: 1px; margin-top: 4px; }

.gallery-loading {
  display: flex;
  justify-content: center;
  padding: 40px;
}

.spinner {
  width: 36px;
  height: 36px;
  border: 3px solid rgba(255, 255, 255, 0.08);
  border-top-color: rgba(150, 170, 255, 0.8);
  border-radius: 50%;
  animation: spin 0.75s linear infinite;
}

@keyframes spin { to { transform: rotate(360deg); } }

.gallery-end {
  text-align: center;
  padding: 28px;
  color: #333;
  font-size: 0.82rem;
}
</style>
