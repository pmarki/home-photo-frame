import { ref, computed } from 'vue'

const PAGE_LIMIT = 5000

export function useGallery() {
  const images = ref([])
  const total = ref(0)
  const currentPage = ref(0)
  const loading = ref(false)
  const error = ref(null)
  const sortBy = ref(localStorage.getItem('sortBy') || 'taken')
  const sortOrder = ref(localStorage.getItem('sortOrder') || 'desc')
  let generation = 0

  const hasMore = computed(
    () => currentPage.value === 0 || images.value.length < total.value
  )

  async function loadNextPage() {
    if (loading.value) return
    if (currentPage.value > 0 && !hasMore.value) return

    loading.value = true
    error.value = null
    const isFirstPage = currentPage.value === 0
    const gen = generation
    try {
      const nextPage = currentPage.value + 1
      const params = new URLSearchParams({
        sort: sortBy.value,
        order: sortOrder.value,
        page: nextPage,
        limit: PAGE_LIMIT,
      })
      const res = await fetch(`/api/images?${params}`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = await res.json()
      if (gen !== generation) return
      images.value = [...images.value, ...(data.images ?? [])]
      total.value = data.total ?? 0
      currentPage.value = nextPage
    } catch (e) {
      if (gen === generation) error.value = e.message
      return
    } finally {
      loading.value = false
    }

    if (isFirstPage && hasMore.value) loadRemainingPages(gen)
  }

  async function loadRemainingPages(gen) {
    while (gen === generation && hasMore.value) {
      if (loading.value) {
        await new Promise(r => setTimeout(r, 50))
        continue
      }
      const pageBefore = currentPage.value
      await loadNextPage()
      if (gen === generation && currentPage.value === pageBefore) break
    }
  }

  function removeImage(path) {
    const idx = images.value.findIndex(img => img?.path === path)
    if (idx !== -1) {
      images.value.splice(idx, 1)
      total.value = Math.max(0, total.value - 1)
    }
  }

  function replaceImage(oldPath, newImage) {
    const idx = images.value.findIndex(img => img?.path === oldPath)
    if (idx !== -1) images.value.splice(idx, 1, newImage)
  }

  function resetState() {
    generation++
    images.value = []
    total.value = 0
    currentPage.value = 0
  }

  async function setSort(by, order) {
    if (sortBy.value === by && sortOrder.value === order) return
    sortBy.value = by
    sortOrder.value = order
    localStorage.setItem('sortBy', by)
    localStorage.setItem('sortOrder', order)
    resetState()
    await loadNextPage()
  }

  async function forceReload() {
    resetState()
    await loadNextPage()
  }

  return {
    images,
    total,
    loading,
    error,
    hasMore,
    sortBy,
    sortOrder,
    loadNextPage,
    setSort,
    removeImage,
    replaceImage,
    forceReload,
  }
}
