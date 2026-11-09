<template>
  <Teleport to="body">
    <div
      v-if="label"
      class="scroll-date"
      :class="{ visible }"
      :style="{ top: topPx }"
      aria-hidden="true"
    >
      {{ label }}
    </div>
  </Teleport>
</template>

<script setup>
import { ref, computed, onMounted, onUnmounted } from 'vue'

defineProps({
  label:   { type: String,  default: null },
  visible: { type: Boolean, default: false },
})

// Sit just below the sticky top area (header plus the optional filter bar),
// tracking its height as the filter bar opens and closes.
const topHeight = ref(0)
const topPx = computed(() => (topHeight.value + 8) + 'px')
let observer = null

onMounted(() => {
  const top = document.querySelector('.app-top')
  if (!top) return
  topHeight.value = top.offsetHeight
  observer = new ResizeObserver(() => { topHeight.value = top.offsetHeight })
  observer.observe(top)
})

onUnmounted(() => observer?.disconnect())
</script>

<style scoped>
.scroll-date {
  position: fixed;
  left: 50%;
  z-index: 60;
  transform: translate(-50%, -4px);
  padding: 4px 11px;
  border-radius: 999px;
  background: rgba(0, 0, 0, 0.45);
  backdrop-filter: blur(8px);
  -webkit-backdrop-filter: blur(8px);
  color: rgba(255, 255, 255, 0.85);
  font-size: 12px;
  font-weight: 500;
  letter-spacing: 0.01em;
  white-space: nowrap;
  pointer-events: none;
  opacity: 0;
  transition: opacity 0.15s ease, transform 0.15s ease;
}
.scroll-date.visible {
  opacity: 1;
  transform: translate(-50%, 0);
}

@media (prefers-reduced-motion: reduce) {
  .scroll-date { transition: none; }
}
</style>
