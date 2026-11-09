// Sharing gallery originals through the Web Share API.

// MIME types Chrome accepts as shared files; for anything else canShare()
// returns false. Types are derived from the filename so a missing or odd
// Content-Type can't make Chrome reject a file it would accept. QuickTime
// (.mov) isn't on Chrome's list, so .mov files fall back to sharing a link.
const SHARE_TYPES = {
  jpg: 'image/jpeg',
  jpeg: 'image/jpeg',
  png: 'image/png',
  gif: 'image/gif',
  webp: 'image/webp',
  mp4: 'video/mp4',
  m4v: 'video/mp4',
  webm: 'video/webm',
}

// The last set of downloaded files, kept so a retry after an expired tap (see
// shareErrorMessage) shares instantly instead of downloading again.
let prepared = null // { key, files }

async function fetchShareFile(img) {
  const res = await fetch(img.original)
  if (!res.ok) throw new Error(`Couldn't load ${img.filename} (HTTP ${res.status})`)
  const blob = await res.blob()
  const ext = img.filename.split('.').pop().toLowerCase()
  return new File([blob], img.filename, { type: SHARE_TYPES[ext] || blob.type })
}

async function fetchShareFiles(imgs) {
  const key = imgs.map(i => i.original).join('\n')
  if (prepared?.key === key) return prepared.files
  const files = await Promise.all(imgs.map(fetchShareFile))
  prepared = { key, files }
  return files
}

// shareImages shares the originals of imgs as files. When the browser can't
// share them as files it shares their links instead and returns 'link', so
// callers can say so — a receiver expecting files (like this app's share
// target) gets nothing it can upload. Returns 'files' on a file share.
export async function shareImages(imgs, title) {
  const files = await fetchShareFiles(imgs)
  if (navigator.canShare?.({ files })) {
    await navigator.share({ files, title })
    prepared = null
    return 'files'
  }
  const links = imgs.map(i => window.location.origin + i.original)
  await navigator.share(links.length === 1 ? { title, url: links[0] } : { title, text: links.join('\n') })
  return 'link'
}

export const LINK_SHARED_MESSAGE = "This file type can't be shared as a file — shared a link instead"

// shareErrorMessage maps a navigator.share() failure to a user message, or
// null when the user simply dismissed the share sheet.
export function shareErrorMessage(e) {
  if (e?.name === 'AbortError') return null
  // Chrome only honours a tap for a few seconds; downloading a large original
  // first can outlast it. The files are kept, so the second tap is instant.
  if (e?.name === 'NotAllowedError') return 'File took too long to load — tap Share again'
  return e?.message || 'Share failed'
}
