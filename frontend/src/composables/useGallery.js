import { ref, computed } from 'vue'

const PAGE_LIMIT = 5000

const MODE_SORT = {
  gallery:  { sortBy: 'taken', sortOrder: 'desc' },
  timeline: { sortBy: 'mtime', sortOrder: 'desc' },
  browser:  { sortBy: 'name',  sortOrder: 'asc'  },
}

function normalizeMode(m) {
  return MODE_SORT[m] ? m : 'gallery'
}

function readFolderFromUrl() {
  const m = window.location.pathname.match(/^\/folder\/(.+?)\/?$/)
  return m ? decodeURI(m[1]) : ''
}

function readFiltersFromUrl() {
  const q = new URLSearchParams(window.location.search)
  return {
    owner: q.get('owner') || '',
    year: Number(q.get('year')) || 0,
    type: q.get('type') || '',
  }
}

function writeFiltersToUrl(filters) {
  const q = new URLSearchParams(window.location.search)
  if (filters.owner) q.set('owner', filters.owner); else q.delete('owner')
  if (filters.year)  q.set('year', String(filters.year)); else q.delete('year')
  if (filters.type)  q.set('type', filters.type); else q.delete('type')
  const qs = q.toString()
  const next = window.location.pathname + (qs ? '?' + qs : '') + window.location.hash
  window.history.replaceState(window.history.state, '', next)
}

export function useGallery() {
  const images = ref([])
  const total = ref(0)
  const currentPage = ref(0)
  const loading = ref(false)
  const error = ref(null)
  const viewMode = ref(normalizeMode(localStorage.getItem('viewMode')))
  const sortBy = ref(MODE_SORT[viewMode.value].sortBy)
  const sortOrder = ref(MODE_SORT[viewMode.value].sortOrder)
  const folder = ref(readFolderFromUrl())
  const filters = ref(readFiltersFromUrl())
  let generation = 0
  let currentController = null

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
    const controller = new AbortController()
    currentController = controller
    try {
      const nextPage = currentPage.value + 1
      const params = new URLSearchParams({
        sort: sortBy.value,
        order: sortOrder.value,
        page: nextPage,
        limit: PAGE_LIMIT,
      })
      if (folder.value) params.set('folder', folder.value)
      if (filters.value.owner) params.set('owner', filters.value.owner)
      if (filters.value.year)  params.set('year', String(filters.value.year))
      if (filters.value.type)  params.set('type', filters.value.type)
      const res = await fetch(`/api/images?${params}`, { signal: controller.signal })
      if (!res.ok) throw new Error(`HTTP ${res.status}`)
      const data = await res.json()
      if (gen !== generation) return
      images.value = [...images.value, ...(data.images ?? [])]
      total.value = data.total ?? 0
      currentPage.value = nextPage
    } catch (e) {
      if (e.name === 'AbortError') return
      if (gen === generation) error.value = e.message
      return
    } finally {
      // Only THIS fetch's controller may toggle shared state. If resetState
      // aborted us (controller swapped to null) or a successor fetch has
      // already taken over, leave its state alone.
      if (currentController === controller) {
        loading.value = false
        currentController = null
      }
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
    currentController?.abort()
    currentController = null
    // Clear loading so the immediate awaited loadNextPage() from setViewMode /
    // forceReload doesn't bail on its `if (loading.value) return` guard.
    loading.value = false
    images.value = []
    total.value = 0
    currentPage.value = 0
  }

  async function setViewMode(mode) {
    const target = normalizeMode(mode)
    if (viewMode.value === target) return
    viewMode.value = target
    localStorage.setItem('viewMode', target)
    const { sortBy: sb, sortOrder: so } = MODE_SORT[target]
    sortBy.value = sb
    sortOrder.value = so
    resetState()
    await loadNextPage()
  }

  async function forceReload() {
    resetState()
    await loadNextPage()
  }

  async function setFolder(f) {
    const target = f || ''
    if (folder.value === target) return
    folder.value = target
    resetState()
    await loadNextPage()
  }

  async function setFilters(partial) {
    const next = { ...filters.value, ...partial }
    if (next.owner === filters.value.owner &&
        next.year  === filters.value.year &&
        next.type  === filters.value.type) return
    filters.value = next
    writeFiltersToUrl(next)
    resetState()
    await loadNextPage()
  }

  return {
    images,
    total,
    loading,
    error,
    hasMore,
    viewMode,
    folder,
    filters,
    loadNextPage,
    setViewMode,
    setFolder,
    setFilters,
    removeImage,
    replaceImage,
    forceReload,
  }
}
