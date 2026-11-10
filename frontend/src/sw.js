import { precacheAndRoute } from 'workbox-precaching'
import { clientsClaim } from 'workbox-core'
import { registerRoute, NavigationRoute } from 'workbox-routing'
import { CacheFirst, NetworkFirst, StaleWhileRevalidate } from 'workbox-strategies'
import { ExpirationPlugin } from 'workbox-expiration'
import { CacheableResponsePlugin } from 'workbox-cacheable-response'
import { SYNC_TAG, listJobs, publicJob, runQueue, summarize } from './uploadQueue.js'

// Activate a new SW as soon as it installs. vite-plugin-pwa only injects this
// for generated workers; with injectManifest it's on us. Without it a new
// version waits until every window of the app is closed — on Android an
// installed PWA rarely is, so the old SW (and its share-target handling) kept
// running indefinitely. clientsClaim() then takes over open tabs, and main.js
// reloads them on controllerchange so page and SW versions match.
self.addEventListener('install', () => self.skipWaiting())
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
  // The manifest entry always records what arrived — files, the form field
  // names, or the error — so the page can explain an empty share instead of
  // showing a generic "not received".
  // contentType tells an empty share's causes apart: multipart means the OS
  // attached nothing; url-encoded or none means the installed app's share
  // target predates file support and the app must be reinstalled.
  const report = { files: [], fields: [], error: null, contentType: request.headers.get('content-type') || '' }
  try {
    const cache = await caches.open(SHARE_CACHE)
    // Purge any stale entries left over from a previous share that was
    // interrupted before the page could clean them up (prevents quota buildup)
    const stale = await cache.keys()
    await Promise.all(stale.map((k) => cache.delete(k)))

    try {
      const formData = await request.formData()
      for (const [name, value] of formData.entries()) {
        report.fields.push(value instanceof File ? `${name} (file: ${value.type || 'unknown type'})` : name)
      }
      const incoming = formData.getAll('images').filter((f) => f instanceof File && f.size > 0)

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
        report.files.push({ key, name: file.name, type: file.type, size: file.size })
      }
    } catch (err) {
      console.error('[SW] share-target error:', err)
      report.error = String(err?.message || err)
    }

    // Manifest entry — App.vue reads this first
    await cache.put(
      '/share-pending/manifest',
      new Response(JSON.stringify(report), {
        headers: { 'Content-Type': 'application/json' }
      })
    )
  } catch (err) {
    console.error('[SW] share-target cache error:', err)
  }

  // Always redirect to the app; ?share-pending=1 triggers the upload UI
  return Response.redirect('/?share-pending=1', 303)
}

// ── Background uploads ────────────────────────────────────────────────
// The page queues uploads in IndexedDB (uploadQueue.js) and registers a sync;
// the worker uploads from here, so closing the app doesn't stop them.

// Chrome cancels a sync event after a few minutes; stop starting chunks well
// before that and continue in a fresh sync event.
const UPLOAD_RUN_BUDGET_MS = 150_000
const UPLOAD_NOTIFICATION_TAG = 'uploads'
const UPLOAD_ICON = '/icons/icon-192.png'
let lastProgressBody = ''

self.addEventListener('sync', (event) => {
  if (event.tag !== SYNC_TAG) return
  event.waitUntil((async () => {
    const outcome = await runUploads()
    if (outcome === 'more') await self.registration.sync.register(SYNC_TAG)
    if (outcome === 'retry') {
      if (event.lastChance) {
        await showUploadNotification('Uploads paused', 'Open the app to continue', { silent: true })
      }
      // Rejecting makes Chrome retry the sync later, with backoff.
      throw new Error('upload interrupted')
    }
  })())
})

// Sent by the page whenever it starts uploads, so they begin (or resume after
// a failure) without waiting for the sync event.
self.addEventListener('message', (event) => {
  if (event.data?.type !== 'uploads-kick') return
  event.waitUntil(runUploads().then((outcome) => {
    if (outcome !== 'idle') return self.registration.sync?.register(SYNC_TAG).catch(() => {})
  }))
})

self.addEventListener('notificationclick', (event) => {
  if (event.notification.tag !== UPLOAD_NOTIFICATION_TAG) return
  event.notification.close()
  event.waitUntil((async () => {
    const wins = await self.clients.matchAll({ type: 'window', includeUncontrolled: true })
    if (wins.length > 0) return wins[0].focus()
    return self.clients.openWindow('/')
  })())
})

async function runUploads() {
  let worked = false
  const outcome = await runQueue({
    deadline: Date.now() + UPLOAD_RUN_BUDGET_MS,
    onProgress: async () => {
      worked = true
      await broadcastUploads(false)
    },
  })
  if (worked) await broadcastUploads(outcome === 'idle')
  return outcome
}

// broadcastUploads sends the queue to open windows and, while none is
// visible, keeps a notification up to date. finished marks the end of a run
// that emptied the queue.
async function broadcastUploads(finished) {
  const jobs = (await listJobs()).map(publicJob)
  const wins = await self.clients.matchAll({ type: 'window' })
  for (const win of wins) win.postMessage({ type: 'uploads-progress', jobs })

  const s = summarize(jobs)
  if (wins.some((w) => w.visibilityState === 'visible')) {
    // The page shows progress itself.
    if (finished) {
      lastProgressBody = ''
      await closeUploadNotifications()
    }
    return
  }
  if (s.pending > 0) {
    // Progress is reported per chunk; only touch the notification per file.
    const body = `${s.done + s.failed + 1} of ${s.total}`
    if (body === lastProgressBody) return
    lastProgressBody = body
    await showUploadNotification('Uploading photos', body, { silent: true })
  } else if (finished) {
    lastProgressBody = ''
    const saved = `${s.done} saved`
    await showUploadNotification('Upload finished', s.failed ? `${saved}, ${s.failed} failed` : saved, { renotify: true })
  }
}

async function showUploadNotification(title, body, options) {
  if (typeof Notification === 'undefined' || Notification.permission !== 'granted') return
  try {
    await self.registration.showNotification(title, {
      body,
      tag: UPLOAD_NOTIFICATION_TAG,
      icon: UPLOAD_ICON,
      ...options,
    })
  } catch (err) {
    console.warn('[SW] upload notification failed:', err)
  }
}

async function closeUploadNotifications() {
  const shown = await self.registration.getNotifications({ tag: UPLOAD_NOTIFICATION_TAG })
  for (const n of shown) n.close()
}

// Earlier versions cached whole videos in the originals cache; drop them on
// activation so the space comes back without waiting for expiry.
self.addEventListener('activate', (event) => {
  event.waitUntil(
    caches.open('originals-v1').then(async (cache) => {
      const keys = await cache.keys()
      await Promise.all(keys.filter((req) => isVideoURL(new URL(req.url))).map((req) => cache.delete(req)))
    })
  )
})

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

// Folder list: same pattern — network-first with a short timeout so a stale
// connection (e.g. after the tab returns from background) fails fast instead
// of hanging the sidebar's loading state forever.
registerRoute(
  ({ url }) => url.pathname === '/api/folders',
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

// Originals: cache-first. URLs are content-addressed (hash changes when the
// file's mtime changes), so the cached response is always valid — no need
// for SWR's background revalidation. Cache-first also means previously-viewed
// images aren't blocked behind a stale network connection after tab wake.
//
// Videos are excluded: whole videos would blow through the storage quota,
// and <video> streams them with Range requests, which a cache-first route
// can't answer (the 206 responses aren't cacheable). Unmatched requests fall
// through to the network, so the browser and server handle ranges natively.
const VIDEO_EXT = /\.(mp4|webm|mov|m4v)$/i

function isVideoURL(url) {
  return VIDEO_EXT.test(url.pathname)
}

registerRoute(
  ({ url, request }) =>
    url.pathname.startsWith('/api/original/') &&
    !isVideoURL(url) &&
    !request.headers.has('range'),
  new CacheFirst({
    cacheName: 'originals-v1',
    plugins: [
      new ExpirationPlugin({ maxEntries: 100, maxAgeSeconds: 7 * 24 * 60 * 60 }),
      new CacheableResponsePlugin({ statuses: [0, 200] })
    ]
  })
)
