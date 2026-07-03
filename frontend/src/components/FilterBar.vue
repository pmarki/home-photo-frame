<template>
  <div ref="barEl" class="filter-bar" role="region" aria-label="Photo filters">
    <!-- Person -->
    <div v-if="showPerson" class="filter-chip-wrap">
      <button
        class="sort-icon-btn filter-chip"
        :class="{ active: !!value.owner }"
        :title="personLabel"
        :aria-label="personLabel"
        :aria-expanded="openChip === 'person'"
        @click.stop="toggle('person')"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="12" cy="8" r="4"/>
          <path d="M4 21a8 8 0 0 1 16 0"/>
        </svg>
        <span v-if="value.owner" class="chip-label">{{ ownerDisplay }}</span>
      </button>
      <ul v-if="openChip === 'person'" class="filter-popover" role="listbox" @click.stop>
        <li>
          <button class="filter-option" :class="{ selected: !value.owner }" @click="pick('owner', '')">
            Anyone
          </button>
        </li>
        <li v-for="u in users" :key="u.id">
          <button class="filter-option" :class="{ selected: value.owner === u.id }" @click="pick('owner', u.id)">
            {{ displayNameFor(u) }}
          </button>
        </li>
      </ul>
    </div>

    <!-- Year -->
    <div class="filter-chip-wrap">
      <button
        class="sort-icon-btn filter-chip"
        :class="{ active: !!value.year }"
        :title="yearLabel"
        :aria-label="yearLabel"
        :aria-expanded="openChip === 'year'"
        @click.stop="toggle('year')"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <rect x="3" y="5" width="18" height="16" rx="2"/>
          <line x1="3" y1="10" x2="21" y2="10"/>
          <line x1="8" y1="3" x2="8" y2="7"/>
          <line x1="16" y1="3" x2="16" y2="7"/>
        </svg>
        <span v-if="value.year" class="chip-label">{{ value.year }}</span>
      </button>
      <ul v-if="openChip === 'year'" class="filter-popover" role="listbox" @click.stop>
        <li>
          <button class="filter-option" :class="{ selected: !value.year }" @click="pick('year', 0)">
            Any year
          </button>
        </li>
        <li v-if="availableYears.length === 0" class="filter-empty">Loading…</li>
        <li v-for="y in availableYears" :key="y">
          <button class="filter-option" :class="{ selected: value.year === y }" @click="pick('year', y)">
            {{ y }}
          </button>
        </li>
      </ul>
    </div>

    <!-- Type -->
    <div v-if="videoEnabled" class="filter-chip-wrap">
      <button
        class="sort-icon-btn filter-chip"
        :class="{ active: !!value.type }"
        :title="typeLabel"
        :aria-label="typeLabel"
        :aria-expanded="openChip === 'type'"
        @click.stop="toggle('type')"
      >
        <svg v-if="value.type !== 'video'" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <rect x="3" y="5" width="18" height="14" rx="2"/>
          <circle cx="9" cy="11" r="2"/>
          <polyline points="3 17 9 13 14 17 21 11"/>
        </svg>
        <svg v-else viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <polygon points="5 4 19 12 5 20 5 4"/>
        </svg>
        <span v-if="value.type" class="chip-label">{{ value.type === 'video' ? 'Videos' : 'Photos' }}</span>
      </button>
      <ul v-if="openChip === 'type'" class="filter-popover" role="listbox" @click.stop>
        <li>
          <button class="filter-option" :class="{ selected: !value.type }" @click="pick('type', '')">Any</button>
        </li>
        <li>
          <button class="filter-option" :class="{ selected: value.type === 'image' }" @click="pick('type', 'image')">Photos</button>
        </li>
        <li>
          <button class="filter-option" :class="{ selected: value.type === 'video' }" @click="pick('type', 'video')">Videos</button>
        </li>
      </ul>
    </div>

    <!-- Viewed (recently / most) -->
    <div class="filter-chip-wrap">
      <button
        class="sort-icon-btn filter-chip"
        :class="{ active: !!value.viewed }"
        :title="viewedLabel"
        :aria-label="viewedLabel"
        :aria-expanded="openChip === 'viewed'"
        @click.stop="toggle('viewed')"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z"/>
          <circle cx="12" cy="12" r="3"/>
        </svg>
        <span v-if="value.viewed" class="chip-label">{{ viewedDisplay }}</span>
      </button>
      <ul v-if="openChip === 'viewed'" class="filter-popover" role="listbox" @click.stop>
        <li>
          <button class="filter-option" :class="{ selected: !value.viewed }" @click="pick('viewed', '')">Any</button>
        </li>
        <li>
          <button class="filter-option" :class="{ selected: value.viewed === 'recent' }" @click="pick('viewed', 'recent')">Recently viewed</button>
        </li>
        <li>
          <button class="filter-option" :class="{ selected: value.viewed === 'most' }" @click="pick('viewed', 'most')">Most viewed</button>
        </li>
      </ul>
    </div>

    <!-- Favorites (toggle only, no popover) -->
    <div class="filter-chip-wrap">
      <button
        class="sort-icon-btn filter-chip"
        :class="{ active: !!value.favorited }"
        :title="favoritedLabel"
        :aria-label="favoritedLabel"
        :aria-pressed="!!value.favorited"
        @click.stop="onFavoritedClick"
      >
        <svg
          viewBox="0 0 24 24"
          :fill="value.favorited ? 'currentColor' : 'none'"
          stroke="currentColor"
          stroke-width="1.8"
          stroke-linecap="round"
          stroke-linejoin="round"
        >
          <path d="M20.84 4.61a5.5 5.5 0 0 0-7.78 0L12 5.67l-1.06-1.06a5.5 5.5 0 1 0-7.78 7.78l1.06 1.06L12 21.23l7.78-7.78 1.06-1.06a5.5 5.5 0 0 0 0-7.78z"/>
        </svg>
        <span v-if="value.favorited" class="chip-label">Favorites</span>
      </button>
    </div>

    <!-- Search (last in the row, pushed to the right) -->
    <div class="filter-chip-wrap filter-chip-wrap-right">
      <button
        class="sort-icon-btn filter-chip"
        :class="{ active: !!value.search }"
        :title="searchLabel"
        :aria-label="searchLabel"
        :aria-expanded="openChip === 'search'"
        @click.stop="onSearchOpen"
      >
        <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round">
          <circle cx="11" cy="11" r="7"/>
          <line x1="20" y1="20" x2="16.5" y2="16.5"/>
        </svg>
        <span v-if="value.search" class="chip-label">{{ searchDisplay }}</span>
      </button>
      <div v-if="openChip === 'search'" class="filter-popover filter-popover-search" role="dialog" @click.stop>
        <input
          ref="searchInputEl"
          :value="searchInput"
          type="search"
          class="fb-search-input"
          placeholder="Search (e.g. luty 2010, zima)"
          aria-label="Search photos"
          autocomplete="off"
          @input="onSearchType($event.target.value)"
          @keydown.enter.prevent="onSearchCommit"
          @keydown.esc.prevent="onSearchEsc"
        />
        <button
          v-if="searchInput"
          class="fb-search-clear"
          type="button"
          aria-label="Clear search"
          @click="onSearchClear"
        >
          <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
            <line x1="6" y1="6" x2="18" y2="18"/>
            <line x1="18" y1="6" x2="6" y2="18"/>
          </svg>
        </button>
      </div>
    </div>

    <!-- Clear all (only when something is set) -->
    <button
      v-if="hasAny"
      class="sort-icon-btn filter-clear"
      title="Clear filters"
      aria-label="Clear filters"
      @click.stop="onClear"
    >
      <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
        <line x1="6" y1="6" x2="18" y2="18"/>
        <line x1="18" y1="6" x2="6" y2="18"/>
      </svg>
    </button>
  </div>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick, watch } from 'vue'

const props = defineProps({
  users: { type: Array, default: () => [] },
  currentUserId: { type: String, default: '' },
  videoEnabled: { type: Boolean, default: false },
  availableYears: { type: Array, default: () => [] },
  value: { type: Object, required: true },
})

const emit = defineEmits(['change', 'clear'])

const openChip = ref(null)
const barEl = ref(null)

const showPerson = computed(() => props.users && props.users.length > 0)
const hasAny = computed(() => !!props.value.owner || !!props.value.year || !!props.value.type || !!props.value.viewed || !!props.value.favorited || !!props.value.search)

const searchInputEl = ref(null)
const searchInput = ref(props.value.search || '')
let searchDebounce = null

// Sync local input if the parent's search changes (e.g. URL, clear-all).
watch(() => props.value.search, v => { searchInput.value = v || '' })

const searchLabel = computed(() =>
  props.value.search ? `Search: ${props.value.search}` : 'Search'
)
const searchDisplay = computed(() => {
  const s = props.value.search || ''
  return s.length > 20 ? s.slice(0, 20) + '…' : s
})

function onSearchOpen() {
  const opening = openChip.value !== 'search'
  toggle('search')
  if (opening) {
    searchInput.value = props.value.search || ''
    nextTick(() => searchInputEl.value?.focus())
  }
}

function onSearchType(v) {
  searchInput.value = v
  if (searchDebounce) clearTimeout(searchDebounce)
  searchDebounce = setTimeout(() => {
    searchDebounce = null
    if ((props.value.search || '') !== searchInput.value) {
      emit('change', { search: searchInput.value })
    }
  }, 250)
}

function onSearchCommit() {
  if (searchDebounce) { clearTimeout(searchDebounce); searchDebounce = null }
  if ((props.value.search || '') !== searchInput.value) {
    emit('change', { search: searchInput.value })
  }
  openChip.value = null
}

function onSearchEsc() {
  if (searchDebounce) { clearTimeout(searchDebounce); searchDebounce = null }
  searchInput.value = props.value.search || ''
  openChip.value = null
}

function onSearchClear() {
  if (searchDebounce) { clearTimeout(searchDebounce); searchDebounce = null }
  searchInput.value = ''
  emit('change', { search: '' })
  nextTick(() => searchInputEl.value?.focus())
}

function displayNameFor(u) {
  if (!u) return ''
  return u.id === props.currentUserId ? u.name : `Shared with ${u.name}`
}

const ownerDisplay = computed(() => {
  const u = props.users.find(u => u.id === props.value.owner)
  return u ? displayNameFor(u) : props.value.owner
})

const personLabel = computed(() => props.value.owner ? `Person: ${ownerDisplay.value}` : 'Filter by person')
const yearLabel = computed(() => props.value.year ? `Year: ${props.value.year}` : 'Filter by year')
const typeLabel = computed(() => {
  if (props.value.type === 'video') return 'Videos only'
  if (props.value.type === 'image') return 'Photos only'
  return 'Filter by type'
})
const viewedDisplay = computed(() => {
  if (props.value.viewed === 'recent') return 'Recent'
  if (props.value.viewed === 'most')   return 'Most'
  return ''
})
const viewedLabel = computed(() => {
  if (props.value.viewed === 'recent') return 'Recently viewed'
  if (props.value.viewed === 'most')   return 'Most viewed'
  return 'Filter by view history'
})
const favoritedLabel = computed(() =>
  props.value.favorited ? 'Showing favorites only' : 'Show favorites only'
)

function onFavoritedClick() {
  openChip.value = null
  emit('change', { favorited: !props.value.favorited })
}

function toggle(chip) {
  openChip.value = openChip.value === chip ? null : chip
}

function pick(key, val) {
  emit('change', { [key]: val })
  openChip.value = null
}

function onClear() {
  emit('clear')
  openChip.value = null
}

function onDocClick(e) {
  if (!openChip.value) return
  if (barEl.value && barEl.value.contains(e.target)) return
  e.stopPropagation()
  e.preventDefault()
  openChip.value = null
}
function onKey(e) {
  if (e.key === 'Escape') openChip.value = null
}

onMounted(() => {
  document.addEventListener('click', onDocClick, true)
  document.addEventListener('keydown', onKey)
})
onUnmounted(() => {
  document.removeEventListener('click', onDocClick, true)
  document.removeEventListener('keydown', onKey)
})
</script>

<style scoped>
.filter-bar {
  position: relative;
  z-index: 50;
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
  /* No backdrop-filter here: it would create a containing block for the
     popover's position: fixed on mobile, snapping its bottom-anchor to the
     filter bar instead of the viewport. */
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 55%, black);
}

.filter-chip-wrap {
  position: relative;
}

.filter-chip {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  width: auto;
  min-width: 34px;
  padding: 0 10px;
}
.filter-chip .chip-label {
  font-size: 0.85rem;
  line-height: 1;
  white-space: nowrap;
}

.filter-popover {
  position: absolute;
  top: calc(100% + 6px);
  left: 0;
  z-index: 200;
  margin: 0;
  padding: 4px;
  list-style: none;
  min-width: 160px;
  max-height: 60vh;
  overflow-y: auto;
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 35%, black);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 10px;
  box-shadow: 0 10px 32px rgba(0, 0, 0, 0.55);
  backdrop-filter: blur(12px);
  -webkit-backdrop-filter: blur(12px);
  animation: fb-pop-in 0.12s ease;
}

@keyframes fb-pop-in {
  from { opacity: 0; transform: translateY(-4px); }
  to   { opacity: 1; transform: translateY(0); }
}

.filter-popover li { margin: 0; }
.filter-empty {
  padding: 8px 12px;
  color: #888;
  font-size: 0.85rem;
}

.filter-option {
  display: flex;
  align-items: center;
  width: 100%;
  padding: 8px 12px;
  border: none;
  background: transparent;
  color: #e0e0e8;
  font: inherit;
  font-size: 16px;
  border-radius: 6px;
  cursor: pointer;
  text-align: left;
  white-space: nowrap;
}
.filter-option:hover,
.filter-option:focus-visible {
  background: rgba(255, 255, 255, 0.07);
  color: #fff;
  outline: none;
}
.filter-option.selected {
  background: rgba(100, 120, 220, 0.18);
  color: #c0caff;
}

.filter-clear {
  /* Only kicks in when the search chip is hidden (no auto-margin winner) */
  margin-left: auto;
}

.filter-chip-wrap-right {
  /* Push the search chip (and the Clear button next to it, if present) to
     the right edge of the row. Using margin-left: auto in a flex row soaks
     up the remaining space before this item. */
  margin-left: auto;
}
/* When search is present at the right edge, Clear no longer needs the
   auto push — sit directly next to search. */
.filter-chip-wrap-right + .filter-clear {
  margin-left: 0;
}

.filter-popover-search {
  display: flex;
  align-items: center;
  gap: 4px;
  padding: 8px;
  min-width: 260px;
  /* Anchor to the right edge of the chip so a search chip sitting at the
     far right of the row doesn't overflow the viewport. */
  left: auto;
  right: 0;
}
.fb-search-input {
  flex: 1;
  min-width: 0;
  padding: 8px 10px;
  background: rgba(0, 0, 0, 0.35);
  border: 1px solid rgba(255, 255, 255, 0.12);
  border-radius: 8px;
  color: #e6e6ee;
  font: inherit;
  font-size: 16px;
  outline: none;
}
.fb-search-input::placeholder { color: #888; }
.fb-search-input:focus {
  border-color: rgba(120, 140, 220, 0.6);
  background: rgba(0, 0, 0, 0.45);
}
.fb-search-input::-webkit-search-cancel-button { display: none; }
.fb-search-clear {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  background: transparent;
  color: #aaa;
  cursor: pointer;
  border-radius: 6px;
}
.fb-search-clear svg { width: 14px; height: 14px; }
.fb-search-clear:hover { color: #ccc; background: rgba(255, 255, 255, 0.06); }

/* Mobile / small tablet: popovers become a bottom sheet so they're reachable with one thumb */
@media (max-width: 768px) {
  .filter-popover {
    position: fixed;
    top: auto;
    left: 0;
    right: 0;
    bottom: 0;
    margin: 0;
    border-radius: 14px 14px 0 0;
    border-bottom: none;
    max-height: 70vh;
    padding: 8px 8px 24px;
    animation: fb-sheet-in 0.18s ease;
  }
  @keyframes fb-sheet-in {
    from { transform: translateY(100%); }
    to   { transform: translateY(0); }
  }
  .filter-option { padding: 12px 16px; }
}
</style>
