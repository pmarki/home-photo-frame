import { ref, watch, onUnmounted } from 'vue'

// Scrolling faster than this many viewport heights per second counts as
// "fast" and activates the detector.
const FAST_VIEWPORTS_PER_SEC = 1.5
// A single sample moving more than this many viewports is a programmatic jump
// (scrollTo on folder change, scroll restore after the lightbox), not a fling.
const JUMP_VIEWPORTS = 2
// Once active, stay active until scrolling has been idle this long, so the
// indicator doesn't flicker while a fling decelerates below the threshold.
const IDLE_MS = 900

export function initialFastScrollState() {
  return { lastY: null, lastT: null, active: false }
}

// nextFastScrollState folds one scroll sample { y, t } (px, ms) into the
// detector state. Pure, so it can be exercised without a browser.
export function nextFastScrollState(prev, { y, t }, viewportHeight) {
  const next = { lastY: y, lastT: t, active: prev.active }
  if (prev.lastY == null || viewportHeight <= 0) return next
  const dy = Math.abs(y - prev.lastY)
  if (dy > JUMP_VIEWPORTS * viewportHeight) return next
  const dt = Math.max(1, t - prev.lastT)
  const pxPerSec = (dy / dt) * 1000
  if (pxPerSec > FAST_VIEWPORTS_PER_SEC * viewportHeight) next.active = true
  return next
}

// useFastScroll reports whether the user is currently scrolling fast.
export function useFastScroll({ scrollY, viewportHeight }) {
  const active = ref(false)
  let state = initialFastScrollState()
  let idleTimer = null

  watch(scrollY, (y) => {
    state = nextFastScrollState(state, { y, t: performance.now() }, viewportHeight.value)
    if (!state.active) return
    active.value = true
    clearTimeout(idleTimer)
    idleTimer = setTimeout(() => {
      state = { ...state, active: false }
      active.value = false
    }, IDLE_MS)
  })

  onUnmounted(() => clearTimeout(idleTimer))

  return { active }
}
