import { ref, computed, watch, onUnmounted } from 'vue'

const HIDE_DELAY_MS = 1500

export function useYearScrollbar({ images, scrollY, rowHeight, columnCount, totalRows, viewportHeight }) {
  const visible = ref(false)
  let hideTimer = null

  function getYear(img) {
    if (!img?.modTime) return null
    return new Date(img.modTime).getFullYear()
  }

  const yearEntries = computed(() => {
    const seen = new Map()
    const imgs = images.value
    for (let i = 0; i < imgs.length; i++) {
      const img = imgs[i]
      if (!img) continue
      const year = getYear(img)
      if (year != null && !seen.has(year)) seen.set(year, i)
    }
    return Array.from(seen.entries())
      .map(([year, firstIdx]) => ({ year, firstIdx }))
      .sort((a, b) => a.firstIdx - b.firstIdx)
  })

  const maxScrollY = computed(() =>
    Math.max(0, totalRows.value * rowHeight.value - viewportHeight.value)
  )

  const yearItems = computed(() => {
    if (yearEntries.value.length < 2) return []
    const max = maxScrollY.value
    const cols = Math.max(1, columnCount.value)
    const rh = rowHeight.value
    return yearEntries.value.map(({ year, firstIdx }) => {
      const sy = Math.floor(firstIdx / cols) * rh
      const pos = max > 0 ? Math.min(1, sy / max) : 0
      return { year, scrollY: sy, pos }
    })
  })

  const currentYear = computed(() => {
    const sy = scrollY.value
    const vp = viewportHeight.value
    const items = yearItems.value
    if (items.length === 0) return null
    let current = items[0].year
    for (const item of items) {
      if (item.scrollY <= sy + vp * 0.25) current = item.year
      else break
    }
    return current
  })

  // Greedy spacing filter: skip labels closer than MIN_GAP_PX to the previous
  // kept label, but always force-include the active year.
  const MIN_GAP_PX = 28
  const displayItems = computed(() => {
    const items = yearItems.value
    if (items.length === 0) return []
    const minGap = MIN_GAP_PX / Math.max(1, viewportHeight.value)
    const active = currentYear.value

    const kept = [items[0]]
    for (let i = 1; i < items.length; i++) {
      const item = items[i]
      if (item.pos - kept[kept.length - 1].pos >= minGap) {
        kept.push(item)
      }
    }

    // Ensure active year appears even if it was skipped by the greedy pass
    if (active != null && !kept.some(i => i.year === active)) {
      const activeItem = items.find(i => i.year === active)
      if (activeItem) {
        let minDist = Infinity, replaceIdx = 0
        for (let i = 0; i < kept.length; i++) {
          const d = Math.abs(kept[i].pos - activeItem.pos)
          if (d < minDist) { minDist = d; replaceIdx = i }
        }
        kept[replaceIdx] = activeItem
      }
    }

    return kept
  })

  watch(scrollY, () => {
    if (yearItems.value.length < 2) return
    visible.value = true
    clearTimeout(hideTimer)
    hideTimer = setTimeout(() => { visible.value = false }, HIDE_DELAY_MS)
  })

  const handlePos = computed(() => {
    const max = maxScrollY.value
    return max > 0 ? Math.min(1, scrollY.value / max) : 0
  })

  onUnmounted(() => clearTimeout(hideTimer))

  return { yearItems: displayItems, currentYear, visible, handlePos, maxScrollY }
}
