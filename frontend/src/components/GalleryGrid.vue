<template>
  <div class="gallery-wrapper">
    <!-- Empty state -->
    <div v-if="total === 0 && !loading" class="gallery-empty">
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.2" class="empty-icon">
        <path d="M23 19a2 2 0 01-2 2H3a2 2 0 01-2-2V8a2 2 0 012-2h4l2-3h6l2 3h4a2 2 0 012 2z"/>
        <circle cx="12" cy="13" r="4"/>
      </svg>
      <p>No photos found in the <code>photos/</code> folder</p>
    </div>

    <!-- Grid -->
    <div v-else ref="gridContainer" class="gallery-grid" role="list">
      <!-- Top spacer: occupies height of off-screen rows above viewport -->
      <div
        v-if="topSpacer > 0"
        class="vs-spacer"
        :style="{ height: topSpacer + 'px', gridColumn: '1 / -1' }"
        aria-hidden="true"
      />

      <template v-for="(img, i) in visibleItems" :key="startIdx + i">
        <button
          v-if="img"
          class="gallery-item"
          role="listitem"
          :aria-label="img.filename"
          :class="{ 'has-dims': img.width && img.height, 'img-loading': loadingSet.has(img.thumbSmall), 'img-error': errorSet.has(img.thumbSmall) }"
          :style="img.width && img.height ? { '--cover-scale': coverScale(img) } : {}"
          @click="$emit('open', startIdx + i)"
        >
          <img
            v-lazy-src="img.thumbSmall"
            :alt="img.filename"
            decoding="async"
            class="gallery-thumb"
            @load="onImgLoad"
            @error="onImgError"
          />
          <span class="img-loader" aria-hidden="true"><span /><span /><span /></span>
          <span class="img-broken" aria-hidden="true">
            <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round">
              <rect x="3" y="3" width="18" height="18" rx="2"/>
              <path d="M3 14l5-5 4 4 3-3 6 6"/>
              <circle cx="8.5" cy="8.5" r="1.5"/>
              <line x1="2" y1="2" x2="22" y2="22"/>
            </svg>
          </span>
          <div v-if="isVideo(img.filename)" class="video-badge" aria-hidden="true">▶</div>
          <div class="gallery-overlay" aria-hidden="true">
            <span class="gallery-name">{{ img.filename }}</span>
          </div>
          <div
            v-if="img.width && img.height && !loadingSet.has(img.thumbSmall) && !errorSet.has(img.thumbSmall)"
            class="orientation-badge"
            :class="imgOrientation(img)"
            aria-hidden="true"
          />
        </button>
        <GalleryPlaceholder v-else />
      </template>

      <!-- Bottom spacer: occupies height of off-screen rows below viewport -->
      <div
        v-if="bottomSpacer > 0"
        class="vs-spacer"
        :style="{ height: bottomSpacer + 'px', gridColumn: '1 / -1' }"
        aria-hidden="true"
      />
    </div>

    <!-- Loading spinner (only on initial load) -->
    <div v-if="loading && images.length === 0" class="gallery-loading" aria-label="Loading photos">
      <div class="spinner" />
    </div>
  </div>

  <YearScrollbar
    :yearItems="yearItems"
    :currentYear="currentYear"
    :visible="yearVisible"
    :handlePos="handlePos"
    :maxScrollY="maxScrollY"
  />
</template>

<script setup>
import { ref, toRef, reactive } from 'vue'
import GalleryPlaceholder from './GalleryPlaceholder.vue'
import YearScrollbar from './YearScrollbar.vue'
import { useVirtualScroll } from '../composables/useVirtualScroll.js'
import { useYearScrollbar } from '../composables/useYearScrollbar.js'

const props = defineProps({
  images:  { type: Array,   required: true },
  total:   { type: Number,  default: 0 },
  loading: { type: Boolean, default: false },
})

const emit = defineEmits(['open'])

const gridContainer = ref(null)
const imagesRef = toRef(props, 'images')

const { topSpacer, bottomSpacer, visibleItems, startIdx, columnCount, rowHeight, totalRows, scrollY, viewportHeight } = useVirtualScroll({
  images: imagesRef,
  total:  toRef(props, 'total'),
  containerRef: gridContainer,
})

const { yearItems, currentYear, visible: yearVisible, handlePos, maxScrollY } = useYearScrollbar({
  images: imagesRef,
  scrollY,
  rowHeight,
  columnCount,
  totalRows,
  viewportHeight,
})

const loadingSet = reactive(new Set())
const errorSet   = reactive(new Set())

// Delay setting img.src until the item has been in view for 200 ms.
// If the item scrolls out before the timer fires (fast scroll), unmounted
// clears the timer and the thumbnail request never happens.
const vLazySrc = {
  mounted(el, { value }) {
    el._lazyValue = value
    el._lazySrcTimer = setTimeout(() => { el.src = value }, 200)
    loadingSet.add(value)
  },
  updated(el, { value, oldValue }) {
    if (value !== oldValue) {
      clearTimeout(el._lazySrcTimer)
      el.style.opacity = '0'
      el._lazyValue = value
      el._lazySrcTimer = setTimeout(() => { el.src = value }, 200)
      loadingSet.delete(oldValue)
      loadingSet.add(value)
    }
  },
  unmounted(el) {
    clearTimeout(el._lazySrcTimer)
    el.removeAttribute('src')
    loadingSet.delete(el._lazyValue)
  },
}

const isVideo = (filename) => /\.(mp4|webm|mov|m4v)$/i.test(filename ?? '')

function coverScale(img) {
  if (!img.width || !img.height) return 1
  return Math.max(img.width, img.height) / Math.min(img.width, img.height)
}

function onImgLoad(e) {
  e.target.style.opacity = '1'
  loadingSet.delete(e.target._lazyValue)
}

function onImgError(e) {
  e.target.style.opacity = '0'
  loadingSet.delete(e.target._lazyValue)
  errorSet.add(e.target._lazyValue)
}

function imgOrientation(img) {
  if (img.width > img.height) return 'landscape'
  if (img.height > img.width) return 'portrait'
  return 'square'
}
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
  grid-template-columns: repeat(auto-fill, minmax(160px, 1fr));
  gap: 3px;
  contain: layout;
}

@media (min-width: 480px)  { .gallery-grid { grid-template-columns: repeat(auto-fill, minmax(190px, 1fr)); gap: 4px; } }
@media (min-width: 768px)  { .gallery-grid { grid-template-columns: repeat(auto-fill, minmax(220px, 1fr)); gap: 5px; } }
@media (min-width: 1280px) { .gallery-grid { grid-template-columns: repeat(auto-fill, minmax(260px, 1fr)); gap: 6px; } }
@media (min-width: 1800px) { .gallery-grid { grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); } }

.vs-spacer { display: block; }

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
  object-fit: cover;    /* fills cell when dimensions are unknown */
  display: block;
  opacity: 0;
  transition: opacity 0.15s, transform 0.3s ease;
}

/* When dimensions are known: use contain + scale so hover can reveal the full image */
.has-dims .gallery-thumb {
  object-fit: contain;
  transform: scale(var(--cover-scale, 1));
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
  padding-left: 2px;
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

.gallery-item.has-dims:hover .gallery-thumb,
.gallery-item.has-dims:focus-visible .gallery-thumb {
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

/* ─── Loading ───────────────────────────────────────────────────────── */
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

/* ─── Per-image dots loader ────────────────────────────────────────── */
.img-loader {
  position: absolute;
  inset: 0;
  display: none;
  align-items: center;
  justify-content: center;
  gap: 5px;
  pointer-events: none;
}

.img-loading .img-loader {
  display: flex;
}

.img-loader span {
  width: 5px;
  height: 5px;
  border-radius: 50%;
  background: rgba(255, 255, 255, 0.3);
  animation: img-dot 1.1s ease-in-out infinite;
}

.img-loader span:nth-child(1) { animation-delay: -0.36s; }
.img-loader span:nth-child(2) { animation-delay: -0.18s; }
.img-loader span:nth-child(3) { animation-delay: 0s; }

@keyframes img-dot {
  0%, 60%, 100% { transform: scale(0.65); opacity: 0.25; }
  30%           { transform: scale(1);    opacity: 0.75; }
}

/* ─── Broken image icon ─────────────────────────────────────────────── */
.img-broken {
  position: absolute;
  inset: 0;
  display: none;
  align-items: center;
  justify-content: center;
  pointer-events: none;
}

.img-error .img-broken {
  display: flex;
}

.img-broken svg {
  width: 32px;
  height: 32px;
  color: rgba(255, 255, 255, 0.2);
}
</style>
