// Upload queue shared by the page and the service worker.
//
// Files are stored in IndexedDB together with their progress, so an upload
// survives the app being closed: the service worker picks the queue up from a
// Background Sync event and resumes at the next unacknowledged chunk. Without
// Background Sync (Firefox, Safari) the page runs the same engine itself.

export const SYNC_TAG = 'uploads'
export const CHUNK_SIZE = 1.5 * 1024 * 1024 // 1.5 MB per chunk

const DB_NAME = 'photo-frame-uploads'
// Job metadata is rewritten after every chunk; the file lives in its own
// store, written once, because putting a record that holds a Blob makes
// Chrome store another copy of the whole file.
const JOBS = 'jobs'
const BLOBS = 'blobs'
const LOCK_NAME = 'photo-frame-uploads'
const CHUNK_TIMEOUT_MS = 60_000
// Waits between attempts at one chunk before the run gives up and leaves the
// rest to the next run (Background Sync retries with its own backoff).
const CHUNK_RETRY_DELAYS_MS = [2_000, 6_000]

let dbPromise = null

function openDB() {
  if (!dbPromise) {
    dbPromise = new Promise((resolve, reject) => {
      const req = indexedDB.open(DB_NAME, 1)
      req.onupgradeneeded = () => {
        req.result.createObjectStore(JOBS, { keyPath: 'id' })
        req.result.createObjectStore(BLOBS, { keyPath: 'id' })
      }
      req.onsuccess = () => resolve(req.result)
      req.onerror = () => reject(req.error)
    })
    dbPromise.catch(() => { dbPromise = null })
  }
  return dbPromise
}

// withStores runs fn with the named object stores in one transaction and
// resolves with the result of the request fn returns, once committed.
async function withStores(names, mode, fn) {
  const db = await openDB()
  return new Promise((resolve, reject) => {
    const tx = db.transaction(names, mode)
    let result
    const req = fn(...names.map((n) => tx.objectStore(n)))
    if (req) req.onsuccess = () => { result = req.result }
    tx.oncomplete = () => resolve(result)
    tx.onerror = () => reject(tx.error)
    tx.onabort = () => reject(tx.error || new Error('transaction aborted'))
  })
}

export async function listJobs() {
  const jobs = await withStores([JOBS], 'readonly', (jobs) => jobs.getAll())
  return (jobs ?? []).sort((a, b) => a.createdAt - b.createdAt || a.order - b.order)
}

function putJob(job) {
  return withStores([JOBS], 'readwrite', (jobs) => { jobs.put(job) })
}

async function getBlob(id) {
  const rec = await withStores([BLOBS], 'readonly', (blobs) => blobs.get(id))
  return rec?.blob ?? null
}

// enqueue stores the files and resolves once they are committed to disk —
// from then on the app can be closed without losing the upload.
export async function enqueue(files, folder) {
  const createdAt = Date.now()
  if (globalThis.navigator?.storage?.persist) navigator.storage.persist().catch(() => {})
  const jobs = files.map((file, order) => ({
    id: crypto.randomUUID(),
    uploadId: crypto.randomUUID(),
    createdAt,
    order,
    name: file.name,
    size: file.size,
    folder: folder || '',
    totalChunks: Math.max(1, Math.ceil(file.size / CHUNK_SIZE)),
    nextChunk: 0,
    state: 'queued',
    error: null,
    result: null,
  }))
  await withStores([JOBS, BLOBS], 'readwrite', (jobStore, blobStore) => {
    jobs.forEach((job, i) => {
      jobStore.put(job)
      blobStore.put({ id: job.id, blob: files[i] })
    })
  })
  return jobs.map(publicJob)
}

// requeue puts failed jobs back in line, restarting them from the first chunk
// under a fresh upload ID (the server dropped the old one's chunks).
export async function requeue(ids) {
  const wanted = new Set(ids)
  const jobs = (await listJobs()).filter((j) => wanted.has(j.id) && j.state === 'error')
  await withStores([JOBS], 'readwrite', (store) => {
    for (const job of jobs) {
      store.put({ ...job, uploadId: crypto.randomUUID(), nextChunk: 0, state: 'queued', error: null })
    }
  })
}

export function removeJobs(ids) {
  return withStores([JOBS, BLOBS], 'readwrite', (jobs, blobs) => {
    for (const id of ids) {
      jobs.delete(id)
      blobs.delete(id)
    }
  })
}

export function isPending(job) {
  return job.state === 'queued' || job.state === 'uploading'
}

// publicJob is what the UI sees: the job plus a percentage.
export function publicJob(job) {
  const progress = job.state === 'done' ? 100 : Math.round((job.nextChunk / job.totalChunks) * 100)
  return { ...job, progress }
}

export function summarize(jobs) {
  const s = { total: jobs.length, done: 0, failed: 0, pending: 0 }
  for (const j of jobs) {
    if (j.state === 'done') s.done++
    else if (j.state === 'error') s.failed++
    else s.pending++
  }
  return s
}

// runQueue uploads queued jobs until the queue is empty or time runs out.
// budgetMs stops it starting new chunks; stopAfterMs aborts a chunk still in
// flight, so a run never outlives the event that hosts it (Chrome cuts a sync
// event off after 3 minutes and counts that as a failure).
//
// Only one runner works at a time across the page and the service worker. A
// caller that finds another run active returns 'busy' at once rather than
// waiting — waiting would burn its own event's time — and the active run
// picks up whatever was queued meanwhile.
//
// Resolves to 'idle' (nothing left), 'more' (out of time), 'retry' (server or
// network unreachable) or 'busy'.
export async function runQueue({ budgetMs = Infinity, stopAfterMs = Infinity, onProgress } = {}) {
  const locks = globalThis.navigator?.locks
  for (;;) {
    const work = () => {
      const start = Date.now()
      return processQueue({ deadline: start + budgetMs, stopAt: start + stopAfterMs }, onProgress)
    }
    const outcome = locks
      ? await locks.request(LOCK_NAME, { ifAvailable: true }, (lock) => (lock ? work() : 'busy'))
      : await work()
    // A job queued just as this run found the queue empty was turned away
    // as 'busy'; it's committed by now, so look once more.
    if (outcome === 'idle' && (await listJobs()).some(isPending)) continue
    return outcome
  }
}

async function processQueue(limits, onProgress) {
  for (;;) {
    const job = (await listJobs()).find(isPending)
    if (!job) return 'idle'
    if (Date.now() > limits.deadline) return 'more'
    const outcome = await uploadJob(job, limits, onProgress)
    if (outcome !== 'next') return outcome
  }
}

async function uploadJob(job, limits, onProgress) {
  const blob = await getBlob(job.id)
  if (!blob) {
    job.state = 'error'
    job.error = 'The file is no longer available — upload it again'
    await putJob(job)
    await onProgress?.()
    return 'next'
  }
  if (job.state !== 'uploading') {
    job.state = 'uploading'
    await putJob(job)
    await onProgress?.()
  }
  for (let i = job.nextChunk; i < job.totalChunks; i++) {
    if (Date.now() > limits.deadline) return 'more'
    const res = await sendChunkWithRetry(job, blob, i, limits.stopAt)
    if (res.stopped) return 'more'
    if (res.transient) return 'retry'
    if (!res.ok) {
      job.state = 'error'
      job.error = res.status === 409 ? 'File already exists' : res.body.trim() || `Server error ${res.status}`
      await putJob(job)
      await onProgress?.()
      return 'next'
    }
    job.nextChunk = i + 1
    if (job.nextChunk < job.totalChunks) {
      await putJob(job)
    } else {
      job.state = 'done'
      try { job.result = JSON.parse(res.body) } catch (e) { console.warn('upload: could not parse server response', e) }
      // Drop the stored copy of the file; only the result is needed now.
      await withStores([JOBS, BLOBS], 'readwrite', (jobs, blobs) => {
        jobs.put(job)
        blobs.delete(job.id)
      })
    }
    await onProgress?.()
  }
  return 'next'
}

async function sendChunkWithRetry(job, blob, index, stopAt) {
  let res
  for (let attempt = 0; ; attempt++) {
    res = await sendChunk(job, blob, index, stopAt)
    if (res.stopped || !res.transient || attempt >= CHUNK_RETRY_DELAYS_MS.length) return res
    const delay = CHUNK_RETRY_DELAYS_MS[attempt]
    if (Date.now() + delay >= stopAt) return { ...res, stopped: true }
    await new Promise((r) => setTimeout(r, delay))
  }
}

async function sendChunk(job, blob, index, stopAt) {
  const start = index * CHUNK_SIZE
  const form = new FormData()
  form.append('file', blob.slice(start, Math.min(start + CHUNK_SIZE, job.size)), job.name)
  form.append('uploadId', job.uploadId)
  form.append('chunkIndex', String(index))
  form.append('totalChunks', String(job.totalChunks))
  form.append('filename', job.name)
  if (job.folder) form.append('folder', job.folder)
  const timeoutMs = Math.min(CHUNK_TIMEOUT_MS, stopAt - Date.now())
  try {
    const res = await fetch('/api/upload', {
      method: 'POST',
      body: form,
      credentials: 'same-origin',
      signal: AbortSignal.timeout(Math.max(0, timeoutMs)),
    })
    const body = await res.text()
    // 5xx, 408 and 429 are worth retrying later; other 4xx are final.
    const transient = res.status >= 500 || res.status === 408 || res.status === 429
    return { ok: res.ok, status: res.status, body, transient }
  } catch {
    // Aborted at the run's hard stop: not a failure, the next run resends it.
    return { ok: false, status: 0, body: '', transient: true, stopped: Date.now() >= stopAt }
  }
}
