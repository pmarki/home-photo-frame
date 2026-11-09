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

// Captured before App.vue strips the param on mount.
const launchedFromShare = new URL(window.location.href).searchParams.has('share-pending')

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
      // A share launch is a fresh load, so it often coincides with an SW
      // update. Reloading here would drop the shared files App.vue has already
      // pulled out of the share cache; the new SW takes effect next navigation.
      if (launchedFromShare) return
      refreshing = true
      window.location.reload()
    })

    document.addEventListener('visibilitychange', () => {
      if (document.visibilityState === 'visible') reg.update().catch(() => {})
    })
  })
}
