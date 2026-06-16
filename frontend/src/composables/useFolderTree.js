// Frontend default for folder ordering — fetch /api/folders with this value
// and pass it to buildFolderTree so the inferred intermediate nodes and any
// runtime-added nodes follow the same ordering the API returned.
export const FOLDER_ORDER = 'desc'

export function buildFolderTree(paths, order = FOLDER_ORDER) {
  const nodes = new Map()

  function ensure(fullPath) {
    if (nodes.has(fullPath)) return nodes.get(fullPath)
    const slash = fullPath.lastIndexOf('/')
    const name = slash < 0 ? fullPath : fullPath.slice(slash + 1)
    const node = { name, path: fullPath, children: [] }
    nodes.set(fullPath, node)
    if (slash > 0) {
      const parent = ensure(fullPath.slice(0, slash))
      parent.children.push(node)
    }
    return node
  }

  for (const p of paths) {
    if (!p) continue
    ensure(p)
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
