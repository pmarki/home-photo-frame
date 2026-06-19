<template>
  <div
    class="us-overlay"
    :class="{ 'us-visible': visible }"
    role="dialog"
    aria-modal="true"
    aria-label="Choose user"
  >
    <div class="us-card">
      <h2 class="us-title">Browse as:</h2>
      <div class="us-list">
        <button
          v-for="u in users"
          :key="u.id"
          class="us-user"
          @click="$emit('select', u.id)"
        >{{ u.name }}</button>
      </div>
    </div>
  </div>
</template>

<script setup>
import { ref, onMounted, onUnmounted, nextTick } from 'vue'
import { lockBodyOverflow, unlockBodyOverflow } from '../composables/useBodyOverflowLock.js'

defineProps({
  users: { type: Array, required: true },
})

defineEmits(['select'])

const visible = ref(false)

onMounted(async () => {
  lockBodyOverflow()
  await nextTick()
  requestAnimationFrame(() => { visible.value = true })
})

onUnmounted(() => {
  unlockBodyOverflow()
})
</script>

<style scoped>
.us-overlay {
  position: fixed;
  inset: 0;
  z-index: 9700;
  background: rgba(0, 0, 0, 0);
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 16px;
  transition: background 0.18s ease;
}
.us-overlay.us-visible {
  background: rgba(0, 0, 0, 0.7);
}

.us-card {
  background: color-mix(in srgb, var(--bg-color, #0a0a0f) 35%, black);
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 14px;
  padding: 28px 24px;
  min-width: 280px;
  max-width: 92vw;
  text-align: center;
  opacity: 0;
  transform: translateY(8px) scale(0.98);
  transition: opacity 0.18s ease, transform 0.18s ease;
  box-shadow: 0 10px 40px rgba(0, 0, 0, 0.6);
}
.us-visible .us-card {
  opacity: 1;
  transform: translateY(0) scale(1);
}

.us-title {
  font-size: 1.2rem;
  font-weight: 600;
  color: #fff;
  margin: 0 0 20px;
}

.us-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.us-user {
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.12);
  color: #e0e0e8;
  border-radius: 10px;
  padding: 14px 18px;
  font-size: 1rem;
  font-weight: 500;
  cursor: pointer;
  transition: background 0.12s, border-color 0.12s, transform 0.06s;
}
.us-user:hover {
  background: rgba(255, 255, 255, 0.08);
  border-color: rgba(255, 255, 255, 0.22);
}
.us-user:active {
  transform: scale(0.98);
}
</style>
