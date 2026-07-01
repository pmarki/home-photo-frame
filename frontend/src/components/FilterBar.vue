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
import { ref, computed, onMounted, onUnmounted } from 'vue'

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
const hasAny = computed(() => !!props.value.owner || !!props.value.year || !!props.value.type)

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
  margin-left: auto;
}

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
