import { ref, computed, onMounted, onUnmounted, watch } from 'vue'

const OVERSCAN = 3

// Mirrors the CSS breakpoints in GalleryGrid to avoid a wrong-column-count first frame
function estimateGrid() {
  if (typeof window === 'undefined') return { cols: 1, itemPx: 160, gapPx: 3 }
  const vw = window.innerWidth
  const gapPx = vw >= 1280 ? 6 : vw >= 768 ? 5 : vw >= 480 ? 4 : 3
  const minCol = vw >= 1800 ? 300 : vw >= 1280 ? 260 : vw >= 768 ? 220 : vw >= 480 ? 190 : 160
  const w = Math.min(vw, 1800) - 24 // subtract .app padding
  const cols = Math.max(1, Math.floor((w + gapPx) / (minCol + gapPx)))
  const itemPx = (w - (cols - 1) * gapPx) / cols
  return { cols, itemPx, gapPx }
}

export function useVirtualScroll({ images, total, containerRef }) {
  const { cols, itemPx, gapPx } = estimateGrid()
  const columnCount = ref(cols)
  const itemSize = ref(itemPx)
  const gap = ref(gapPx)
  const scrollY = ref(typeof window !== 'undefined' ? window.scrollY : 0)
  const viewportHeight = ref(typeof window !== 'undefined' ? window.innerHeight : 800)

  let rafId = null
  let resizeObserver = null

  function readGridMetrics() {
    const el = containerRef.value
    if (!el) return
    const style = getComputedStyle(el)
    const cols = style.gridTemplateColumns.split(' ').filter(Boolean)
    if (cols.length > 0) {
      columnCount.value = cols.length
      itemSize.value = parseFloat(cols[0])
    }
    const g = parseFloat(style.gap || style.rowGap || '0')
    if (!isNaN(g)) gap.value = g
  }

  function onScroll() {
    if (rafId) return
    rafId = requestAnimationFrame(() => {
      rafId = null
      scrollY.value = window.scrollY
    })
  }

  function onResize() {
    viewportHeight.value = window.innerHeight
  }

  onMounted(() => {
    resizeObserver = new ResizeObserver(readGridMetrics)
    if (containerRef.value) resizeObserver.observe(containerRef.value)
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onResize, { passive: true })

    // Read after next paint so CSS grid has resolved auto-fill columns
    requestAnimationFrame(readGridMetrics)
  })

  onUnmounted(() => {
    resizeObserver?.disconnect()
    window.removeEventListener('scroll', onScroll)
    window.removeEventListener('resize', onResize)
    if (rafId) cancelAnimationFrame(rafId)
  })

  // Re-read when containerRef becomes available (deferred mount)
  watch(containerRef, (el) => {
    if (el) {
      resizeObserver?.observe(el)
      requestAnimationFrame(readGridMetrics)
    }
  })

  const rowHeight = computed(() => itemSize.value + gap.value)

  const totalRows = computed(() => {
    if (columnCount.value < 1 || total.value < 1) return 0
    return Math.ceil(total.value / columnCount.value)
  })

  const firstRow = computed(() =>
    Math.max(0, Math.floor(scrollY.value / rowHeight.value) - OVERSCAN)
  )

  const lastRow = computed(() =>
    Math.min(
      totalRows.value - 1,
      Math.ceil((scrollY.value + viewportHeight.value) / rowHeight.value) + OVERSCAN
    )
  )

  const startIdx = computed(() => firstRow.value * columnCount.value)

  const endIdx = computed(() =>
    Math.min(total.value, (lastRow.value + 1) * columnCount.value)
  )

  const topSpacer = computed(() => firstRow.value * rowHeight.value)

  const bottomSpacer = computed(() => {
    const hiddenRowsBelow = totalRows.value - lastRow.value - 1
    return Math.max(0, hiddenRowsBelow * rowHeight.value)
  })

  const visibleItems = computed(() => {
    const start = startIdx.value
    const end = endIdx.value
    const result = []
    for (let i = start; i < end; i++) {
      result.push(images.value[i] ?? null)
    }
    return result
  })

  return {
    topSpacer,
    bottomSpacer,
    visibleItems,
    startIdx,
    columnCount,
    rowHeight,
    totalRows,
    scrollY,
    viewportHeight,
  }
}
