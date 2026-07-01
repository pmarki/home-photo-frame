// Frontend default for folder ordering — fetch /api/folders with this value
// and pass it to buildFolderTree so the inferred intermediate nodes and any
// runtime-added nodes follow the same ordering the API returned.
export const FOLDER_ORDER = 'desc'

// Fold a string for accent-insensitive comparison. NFD splits combining accents
// off the base letter and the diacritic regex drops them, which handles the
// standard Polish accents (ń ó ą ę ś ć ź ż). ł is a precomposed letter with no
// canonical decomposition, so NFD leaves it as-is — we map it manually.
// Preserves char-for-char length so callers can index the original string
// using folded offsets.
export function foldStr(s) {
  return s
    .normalize('NFD')
    .replace(/\p{Diacritic}/gu, '')
    .toLowerCase()
    .replace(/ł/g, 'l')
}

// Recursively prune the tree, keeping any node whose own name matches the
// pre-folded term OR whose subtree contains a match. Never mutates input.
export function filterFolderTree(nodes, foldedTerm) {
  const out = []
  for (const n of nodes) {
    const childrenKept = filterFolderTree(n.children, foldedTerm)
    const selfMatch = foldStr(n.name).includes(foldedTerm)
    if (!selfMatch && childrenKept.length === 0) continue
    out.push({ ...n, children: childrenKept })
  }
  return out
}

// folders: Array of { path, scope?, sharedWith? } as returned by /api/folders.
// String entries are also accepted (treated as { path }) for backwards
// compatibility with callers that don't have scope info.
export function buildFolderTree(folders, order = FOLDER_ORDER) {
  const nodes = new Map()

  function normalize(entry) {
    if (typeof entry === 'string') return { path: entry }
    return entry
  }

  function ensure(fullPath, scope, sharedWith) {
    if (nodes.has(fullPath)) {
      const existing = nodes.get(fullPath)
      // First explicit metadata wins; later entries (e.g. siblings reusing the
      // same top-level path) don't overwrite a value already set.
      if (scope && !existing.scope) {
        existing.scope = scope
        existing.sharedWith = sharedWith
      }
      return existing
    }
    const slash = fullPath.lastIndexOf('/')
    const name = slash < 0 ? fullPath : fullPath.slice(slash + 1)
    const node = { name, path: fullPath, children: [], scope, sharedWith }
    nodes.set(fullPath, node)
    if (slash > 0) {
      // Intermediate node inherits its parent's scope/sharedWith, since
      // scope is determined by the top folder and is identical across the
      // whole subtree.
      const parent = ensure(fullPath.slice(0, slash), scope, sharedWith)
      parent.children.push(node)
    }
    return node
  }

  for (const raw of folders) {
    const entry = normalize(raw)
    if (!entry || !entry.path) continue
    ensure(entry.path, entry.scope, entry.sharedWith)
  }

  const direction = order === 'desc' ? -1 : 1
  const cmp = (a, b) => direction * a.name.localeCompare(b.name, undefined, { sensitivity: 'base' })
  for (const node of nodes.values()) node.children.sort(cmp)

  const roots = []
  for (const node of nodes.values()) {
    if (!node.path.includes('/')) roots.push(node)
  }
  return roots.sort(cmp)
}
