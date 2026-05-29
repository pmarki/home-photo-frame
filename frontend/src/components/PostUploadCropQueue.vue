<template>
  <div v-if="cropError" class="cpq-error-toast" role="alert">{{ cropError }}</div>
  <ImageCropper
    :src="current.original"
    :filename="current.filename"
    @crop="onCropApplied"
    @cancel="advance"
  />
</template>

<script setup>
import { ref, computed } from 'vue'
import ImageCropper from './ImageCropper.vue'

const props = defineProps({
  images: { type: Array, required: true }, // [{ filename }]
})
const emit = defineEmits(['done'])

const queue = ref(props.images.map(img => ({ ...img })))
const idx = ref(0)
const cropError = ref('')

const current = computed(() => queue.value[idx.value])

async function onCropApplied(rect) {
  const filename = current.value.filename
  try {
    const res = await fetch(`/api/crop/${encodeURIComponent(filename)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(rect),
    })
    if (res.ok) {
      const data = await res.json()
      queue.value[idx.value] = { filename: data.filename, original: data.original }
    } else {
      throw new Error(`Server error ${res.status}`)
    }
  } catch (e) {
    console.error('crop failed:', e)
    cropError.value = `Crop failed for "${filename}" — skipping`
    setTimeout(() => { cropError.value = '' }, 3000)
  }
  advance()
}

function advance() {
  if (idx.value < queue.value.length - 1) {
    idx.value++
  } else {
    emit('done', queue.value)
  }
}
</script>

<style scoped>
.cpq-error-toast {
  position: fixed;
  top: env(safe-area-inset-top, 0);
  left: 50%;
  transform: translateX(-50%);
  z-index: 10100;
  background: rgba(220, 60, 60, 0.9);
  color: #fff;
  font-size: 0.85rem;
  padding: 8px 20px;
  border-radius: 0 0 8px 8px;
  pointer-events: none;
}
</style>
