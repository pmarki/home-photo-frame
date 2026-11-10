import { SYNC_TAG, listJobs, publicJob, runQueue } from '../uploadQueue.js'

// Page side of the upload queue: starts the service worker on it (or runs it
// in the page where Background Sync is missing) and fans progress out to
// subscribers.

const listeners = new Set()
const IN_PAGE_RETRY_MS = 30_000
let inPageRun = null
let inPageRerun = false
let inPageRetryTimer = null

export function onUploadsChanged(fn) {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

function emit(jobs) {
  for (const fn of listeners) fn(jobs)
}

async function emitFromDB() {
  emit((await listJobs()).map(publicJob))
}

// refreshUploads re-reads the queue and tells every subscriber; call it after
// changing the queue from the page (queueing, retrying, clearing).
export function refreshUploads() {
  return emitFromDB().catch((err) => console.warn('upload: could not read the queue', err))
}

if (typeof navigator !== 'undefined' && 'serviceWorker' in navigator) {
  navigator.serviceWorker.addEventListener('message', (e) => {
    if (e.data?.type === 'uploads-progress') emit(e.data.jobs)
  })
}

// Uploads continue after the app is closed only when a service worker with
// Background Sync controls the page.
export function backgroundUploads() {
  return typeof navigator !== 'undefined' &&
    !!navigator.serviceWorker?.controller &&
    typeof SyncManager !== 'undefined'
}

export async function startUploads() {
  if (backgroundUploads()) {
    // The sync carries the upload on after the app closes. It doesn't fire
    // at once after a failure (Chrome backs off for minutes, and
    // re-registering doesn't reset that), so also have the worker start now;
    // the queue lock keeps the two from overlapping.
    try {
      const reg = await navigator.serviceWorker.ready
      await reg.sync.register(SYNC_TAG)
    } catch (err) {
      console.warn('upload: sync registration failed', err)
    }
    navigator.serviceWorker.controller?.postMessage({ type: 'uploads-kick' })
    return
  }
  runInPage()
}

function runInPage() {
  clearTimeout(inPageRetryTimer)
  // A run that is already finishing may have missed jobs added just now.
  if (inPageRun) { inPageRerun = true; return }
  inPageRun = runQueue({ onProgress: emitFromDB })
    .then((outcome) => {
      if (outcome === 'retry') inPageRetryTimer = setTimeout(runInPage, IN_PAGE_RETRY_MS)
    })
    .catch((err) => console.error('upload: queue run failed', err))
    .finally(() => {
      inPageRun = null
      emitFromDB()
      if (inPageRerun) { inPageRerun = false; runInPage() }
    })
}

// Ask for notification permission while handling the user's click, so the
// worker can report progress and results while the app is closed.
export function requestUploadNotifications() {
  if (!backgroundUploads() || typeof Notification === 'undefined') return
  if (Notification.permission === 'default') Notification.requestPermission().catch(() => {})
}
