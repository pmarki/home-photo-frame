import { ref, computed } from 'vue'

const PAGE_LIMIT = 50

export function useGallery() {
  const images = ref([])
  const total = ref(0)
  const currentPage = ref(0)
  const loading = ref(false)
  const error = ref(null)
  const sortBy = ref(localStorage.getItem('sortBy') || 'date')
  const sortOrder = ref(localStorage.getItem('sortOrder') || 'desc')

  // hasMore is true before the first load (page 0) or when there are more pages.
  const hasMore = computed(
    () => currentPage.value === 0 || images.value.length < total.value
  )

  async function loadNextPage() {
    if (loading.value) return
    if (currentPage.value > 0 && !hasMore.value) return

    loading.value = true
    error.value = null
    try {
      const nextPage = currentPage.value + 1
      const params = new URLSearchParams({
        sort: sortBy.value,
        order: sortOrder.value,
        page: nextPage,
        limit: PAGE_LIMIT
      })
      const res = await fetch(`/api/images?${params}`)
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = await res.json()
      images.value = [...images.value, ...(data.images ?? [])]
      total.value = data.total ?? 0
      currentPage.value = nextPage
    } catch (e) {
      error.value = e.message
    } finally {
      loading.value = false
    }
  }

  function removeImage(filename) {
    const idx = images.value.findIndex(img => img.filename === filename)
    if (idx !== -1) {
      images.value.splice(idx, 1)
      total.value = Math.max(0, total.value - 1)
    }
  }

  async function setSort(by, order) {
    if (sortBy.value === by && sortOrder.value === order) return
    sortBy.value = by
    sortOrder.value = order
    localStorage.setItem('sortBy', by)
    localStorage.setItem('sortOrder', order)
    // Reset and reload from scratch
    images.value = []
    total.value = 0
    currentPage.value = 0
    await loadNextPage()
  }

  function replaceImage(oldFilename, newImage) {
    const idx = images.value.findIndex(img => img.filename === oldFilename)
    if (idx !== -1) images.value.splice(idx, 1, newImage)
  }

  async function forceReload() {
    images.value = []
    total.value = 0
    currentPage.value = 0
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
