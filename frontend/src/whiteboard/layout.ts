// Auto-layout for the board window: grid, tree and cluster by tag. The same
// arrangement as Go's board.layout (internal/board/layout.go), so a person in
// the window and an agent over MCP get the same board; board-check runs this
// on Go's golden file (internal/board/testdata/layout-golden.json).

type LayoutKind = "grid" | "tree" | "cluster"

const layoutKinds: { kind: LayoutKind; title: string; description: string }[] = [
  { kind: "grid", title: "Grid", description: "Rows and columns, in stacking order." },
  { kind: "tree", title: "Tree", description: "Follows the lines: roots first, each level one column further." },
  { kind: "cluster", title: "Cluster by tag", description: "One column per tag (a block's first tag), untagged last." },
]

type LayoutBlock = { id: string; x: number; y: number; w: number; h: number; tags?: readonly string[] }
type LayoutLink = { from: string; to: string }

type LayoutOptions = {
  /** Space between blocks. Default 40. */
  gap?: number
  /** grid: blocks per row. Default about a square. */
  columns?: number
  /** tree: which way levels go. Default right. */
  direction?: "right" | "down"
  /** Where the layout starts. Default: the blocks' top-left-most corner. */
  x?: number
  y?: number
}

type Point = { x: number; y: number }

/**
 * New top-left corners for blocks, in the order given (the stacking order).
 * links are the lines between blocks, by block id; tree follows them.
 */
function layout(
  blocks: readonly LayoutBlock[],
  links: readonly LayoutLink[],
  kind: LayoutKind,
  opt: LayoutOptions = {}
): Record<string, Point> {
  const out: Record<string, Point> = {}
  if (!blocks.length) {
    return out
  }
  const gap = opt.gap && opt.gap > 0 ? opt.gap : 40
  let ox = Math.min(...blocks.map((b) => b.x))
  let oy = Math.min(...blocks.map((b) => b.y))
  if (opt.x !== undefined && opt.y !== undefined) {
    ox = opt.x
    oy = opt.y
  }
  const byId = new Map(blocks.map((b) => [b.id, b]))

  if (kind === "grid") {
    const cols = opt.columns && opt.columns > 0 ? opt.columns : Math.ceil(Math.sqrt(blocks.length))
    const cw = Math.max(...blocks.map((b) => b.w))
    const ch = Math.max(...blocks.map((b) => b.h))
    blocks.forEach((b, i) => {
      out[b.id] = { x: ox + (i % cols) * (cw + gap), y: oy + Math.floor(i / cols) * (ch + gap) }
    })
    return out
  }

  if (kind === "cluster") {
    const groups = new Map<string, LayoutBlock[]>()
    for (const b of blocks) {
      const tag = b.tags?.[0] ?? ""
      const group = groups.get(tag)
      if (group) group.push(b)
      else groups.set(tag, [b])
    }
    // Go sorts tags with strings.ToLower and byte order; so does this.
    const names = [...groups.keys()].sort((a, b) => {
      if ((a === "") !== (b === "")) return a === "" ? 1 : -1
      const la = a.toLowerCase()
      const lb = b.toLowerCase()
      return la < lb ? -1 : la > lb ? 1 : 0
    })
    let colX = ox
    for (const name of names) {
      let y = oy
      let w = 0
      for (const b of groups.get(name)!) {
        out[b.id] = { x: colX, y }
        y += b.h + gap
        w = Math.max(w, b.w)
      }
      colX += w + gap * 2
    }
    return out
  }

  // tree
  const children = new Map<string, string[]>()
  const hasParent = new Set<string>()
  for (const l of links) {
    if (byId.has(l.from) && byId.has(l.to)) {
      children.set(l.from, [...(children.get(l.from) ?? []), l.to])
      hasParent.add(l.to)
    }
  }
  const level = new Map<string, number>()
  const order: string[] = []
  const visit = (roots: string[]) => {
    const queue = [...roots]
    for (const r of roots) level.set(r, 0)
    while (queue.length) {
      const id = queue.shift()!
      order.push(id)
      for (const c of children.get(id) ?? []) {
        if (!level.has(c)) {
          level.set(c, level.get(id)! + 1)
          queue.push(c)
        }
      }
    }
  }
  visit(blocks.filter((b) => !hasParent.has(b.id)).map((b) => b.id))
  // Blocks only in cycles: start from each one not reached yet.
  for (const b of blocks) {
    if (!level.has(b.id)) visit([b.id])
  }
  const levels: string[][] = []
  for (const id of order) {
    const l = level.get(id)!
    while (levels.length <= l) levels.push([])
    levels[l].push(id)
  }
  const down = opt.direction === "down"
  let along = 0
  for (const ids of levels) {
    let across = 0
    let size = 0
    for (const id of ids) {
      const b = byId.get(id)!
      if (down) {
        out[id] = { x: ox + across, y: oy + along }
        across += b.w + gap
        size = Math.max(size, b.h)
      } else {
        out[id] = { x: ox + along, y: oy + across }
        across += b.h + gap
        size = Math.max(size, b.w)
      }
    }
    along += size + gap * 2
  }
  return out
}

export { layout, layoutKinds, type LayoutBlock, type LayoutKind, type LayoutLink, type LayoutOptions, type Point }
