import { precacheAndRoute } from 'workbox-precaching'
import { clientsClaim } from 'workbox-core'
import { registerRoute, NavigationRoute } from 'workbox-routing'
import { CacheFirst, NetworkFirst, StaleWhileRevalidate } from 'workbox-strategies'
import { ExpirationPlugin } from 'workbox-expiration'
import { CacheableResponsePlugin } from 'workbox-cacheable-response'

// With registerType:'autoUpdate' vite-plugin-pwa injects skipWaiting() for us;
// clientsClaim() makes the newly-activated SW take over open tabs immediately
// so users don't need a manual reload to get the new SW logic.
clientsClaim()

// Injected by vite-plugin-pwa at build time
precacheAndRoute(self.__WB_MANIFEST)

// ── Share Target interception ─────────────────────────────────────────
// Android POSTs to /share-target when the user shares images to this PWA.
// We intercept here so the user sees our upload UI instead of a blank page.
self.addEventListener('fetch', (event) => {
  const url = new URL(event.request.url)
  if (url.pathname === '/share-target' && event.request.method === 'POST') {
    event.respondWith(handleShareTarget(event.request))
  }
})

const SHARE_CACHE = 'share-pending-v1'

async function handleShareTarget(request) {
  try {
    const formData = await request.formData()
    const incoming = formData.getAll('images').filter((f) => f instanceof File)

    if (incoming.length > 0) {
      const cache = await caches.open(SHARE_CACHE)

      // Purge any stale entries left over from a previous share that was
      // interrupted before the page could clean them up (prevents quota buildup)
      const stale = await cache.keys()
      await Promise.all(stale.map((k) => cache.delete(k)))

      const fileInfos = []
      for (const file of incoming) {
        const key = `/share-pending/${self.crypto.randomUUID()}`
        // Pass File directly as the response body — avoids loading the entire
        // file into SW memory via arrayBuffer(), which OOMs on large videos
        await cache.put(
          key,
          new Response(file, {
            headers: {
              'Content-Type': file.type || 'application/octet-stream',
              'X-Filename': encodeURIComponent(file.name),
              'X-Size': String(file.size)
            }
          })
        )
        fileInfos.push({ key, name: file.name, type: file.type, size: file.size })
      }

      // Manifest entry — the Vue component reads this first
      await cache.put(
        '/share-pending/manifest',
        new Response(JSON.stringify(fileInfos), {
          headers: { 'Content-Type': 'application/json' }
        })
      )
    }
  } catch (err) {
    console.error('[SW] share-target error:', err)
  }

  // Always redirect to the app; ?share-pending=1 triggers the upload UI
  return Response.redirect('/?share-pending=1', 303)
}

// ── Runtime caching ───────────────────────────────────────────────────

// Thumbnails: cache-first, 30-day TTL, max 2000 entries
registerRoute(
  ({ url }) => url.pathname.startsWith('/api/thumb/'),
  new CacheFirst({
    cacheName: 'thumbnails-v1',
    plugins: [
      new ExpirationPlugin({ maxEntries: 2000, maxAgeSeconds: 30 * 24 * 60 * 60 }),
      new CacheableResponsePlugin({ statuses: [0, 200] })
    ]
  })
)

// Image list: network-first so sorting / new uploads are always fresh.
// fetchOptions.cache:'no-store' bypasses the browser HTTP cache so the SW
// always reaches the actual server (not a stale HTTP-cached response).
registerRoute(
  ({ url }) => url.pathname.startsWith('/api/images'),
  new NetworkFirst({
    cacheName: 'api-v1',
    networkTimeoutSeconds: 5,
    fetchOptions: { cache: 'no-store' },
    plugins: [new CacheableResponsePlugin({ statuses: [0, 200] })]
  })
)

// App config: stale-while-revalidate so offline/slow loads get cached values instantly
registerRoute(
  ({ url }) => url.pathname === '/api/config',
  new StaleWhileRevalidate({
    cacheName: 'api-config-v1',
    plugins: [new CacheableResponsePlugin({ statuses: [0, 200] })]
  })
)

// Originals: stale-while-revalidate (large files that rarely change)
registerRoute(
  ({ url }) => url.pathname.startsWith('/api/original/'),
  new StaleWhileRevalidate({
    cacheName: 'originals-v1',
    plugins: [
      new ExpirationPlugin({ maxEntries: 100, maxAgeSeconds: 7 * 24 * 60 * 60 }),
      new CacheableResponsePlugin({ statuses: [0, 200] })
    ]
  })
)
