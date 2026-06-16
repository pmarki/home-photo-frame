import { ref } from 'vue'

const selectMode = ref(false)
const selectedPaths = ref(new Set())

function enterSelectMode(initialPath) {
  selectMode.value = true
  if (initialPath) {
    const next = new Set(selectedPaths.value)
    next.add(initialPath)
    selectedPaths.value = next
  }
}

function togglePath(path) {
  if (!path) return
  const next = new Set(selectedPaths.value)
  if (next.has(path)) next.delete(path)
  else next.add(path)
  selectedPaths.value = next
}

function exitSelectMode() {
  selectMode.value = false
  selectedPaths.value = new Set()
}

function isSelected(path) {
  return selectedPaths.value.has(path)
}

export function useImageSelection() {
  return {
    selectMode,
    selectedPaths,
    enterSelectMode,
    togglePath,
    exitSelectMode,
    isSelected,
  }
}
