import { createApp } from 'vue'
import App from './App.vue'

// The browser carries the `user` cookie on every /api/* request automatically,
// so we don't need to inject a header. We still surface 401 as an auth:required
// event so App.vue can re-show the user-select modal without each call site
// knowing.
const originalFetch = window.fetch.bind(window)
window.fetch = async (input, init = {}) => {
  const url = typeof input === 'string' ? input : input?.url ?? ''
  const res = await originalFetch(input, init)
  if (res.status === 401 && url.startsWith('/api/')) {
    document.cookie = 'user=; path=/; max-age=0; samesite=lax'
    window.dispatchEvent(new Event('auth:required'))
  }
  return res
}

createApp(App).mount('#app')

if ('serviceWorker' in navigator) {
  window.addEventListener('load', async () => {
    const reg = await navigator.serviceWorker.register('/sw.js', {
      scope: '/',
      updateViaCache: 'none',
    })

    let refreshing = false
    navigator.serviceWorker.addEventListener('controllerchange', () => {
      if (refreshing) return
      refreshing = true
      window.location.reload()
    })

    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') reg.update().catch(() => {})
    })
  })
}
