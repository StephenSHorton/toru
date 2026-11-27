// The board document as Go keeps it (internal/board: Board, Op), and the
// two ways between it and the canvas (board.ts) in the board window.
//
// Go owns the document. Every change is an op, and Go hands back each op
// "as applied": every item it touched in its final state, at its place in
// its list (`pos`), and every id it removed. `applyOp` makes that op on the
// window's copy, the same way Go's `put` does, so the copy matches Go's
// board rev for rev (internal/board/testdata/live-ops.json checks it).
//
// The canvas shape is smaller than the document's: frames are canvas blocks
// of type `frame`, strokes are blocks with `ink` (relative points), and
// lines join `block:side` ports. Fields the canvas doesn't have (a block's
// frame, group and rotation, a link's label and props) are kept from the
// document item when a canvas change is turned back into one (`toDocItem`).
import { facing, sides, type BoardBlock, type BoardBlocks, type BoardLine } from "./board"
import { blockLabel } from "./catalog"
import type { BlockDef } from "./custom-types"
import { fromDocumentStroke, toDocumentStroke, type InkExtras } from "./ink"

type Meta = { createdBy?: string; updatedBy?: string }
type DocBlock = Meta & {
  id: string
  type: string
  x: number
  y: number
  w: number
  h: number
  rotation?: number
  frame?: string
  group?: string
  props: Record<string, unknown>
  locked?: boolean
}
type DocLink = Meta & {
  id: string
  from: string
  to: string
  fromPort?: string
  toPort?: string
  label?: string
  props?: Record<string, unknown>
  locked?: boolean
}
type DocStroke = Meta & InkExtras & { id: string; points: number[]; color: string; width: number; frame?: string; locked?: boolean }
type DocFrame = Meta & { id: string; name: string; x: number; y: number; w: number; h: number; color?: string; locked?: boolean }

/** A board as Go keeps it (`board.Board`). */
type BoardDoc = {
  format?: string
  version?: number
  id: string
  name: string
  createdAt?: string
  updatedAt?: string
  rev: number
  frames: DocFrame[]
  blocks: DocBlock[]
  links: DocLink[]
  ink: DocStroke[]
  /** The custom block types the board's blocks use (custom-types.ts); left out when none. */
  types?: BlockDef[]
}

/** One change to a board (`board.Op`). Go sets rev, at and kind when it applies it. */
type BoardOp = {
  rev?: number
  at?: string
  kind?: "change" | "undo" | "redo" | "reset" | ""
  source: string
  name?: string
  delete?: string[]
  frames?: DocFrame[]
  blocks?: DocBlock[]
  ink?: DocStroke[]
  links?: DocLink[]
  pos?: Record<string, number>
  /** Custom block types put on the board (added, or a newer version of the same block). */
  types?: BlockDef[]
  /** Custom block types taken off the board (undo uses it). */
  dropTypes?: string[]
}

type ListKey = "frames" | "blocks" | "ink" | "links"
const listKeys: ListKey[] = ["frames", "blocks", "ink", "links"]
type DocItem = DocFrame | DocBlock | DocStroke | DocLink

/** Puts items in a list as Go's `put` does: replaced in place or added on top, then the ones in pos moved to their places, lowest first. */
function put<T extends { id: string }>(list: T[], items: T[] | undefined, pos: Record<string, number> = {}): T[] {
  if (!items?.length) return list
  let out = list.slice()
  const placed: { item: T; at: number }[] = []
  for (const item of items) {
    if (item.id in pos) {
      placed.push({ item, at: pos[item.id] })
      continue
    }
    const i = out.findIndex((x) => x.id === item.id)
    if (i >= 0) out[i] = item
    else out.push(item)
  }
  if (!placed.length) return out
  const moving = new Set(placed.map((p) => p.item.id))
  out = out.filter((x) => !moving.has(x.id))
  placed.sort((a, b) => a.at - b.at)
  for (const { item, at } of placed) out.splice(Math.min(Math.max(at, 0), out.length), 0, item)
  return out
}

/**
 * The board after an op Go applied: its deletes, then its items put at
 * their places, then its name. The op is Go's as-applied op, so it carries
 * every side effect (links to a deleted block, blocks out of a deleted
 * frame) itself.
 */
function applyOp(doc: BoardDoc, op: BoardOp): BoardDoc {
  const gone = new Set(op.delete ?? [])
  const next: BoardDoc = { ...doc }
  for (const key of listKeys) {
    const list = gone.size ? (doc[key] as DocItem[]).filter((x) => !gone.has(x.id)) : (doc[key] as DocItem[])
    ;(next as Record<ListKey, DocItem[]>)[key] = put(list, op[key] as DocItem[] | undefined, op.pos)
  }
  if (op.types?.length || op.dropTypes?.length) next.types = putTypes(doc.types, op.types, op.dropTypes)
  if (op.name !== undefined) next.name = op.name
  if (op.rev !== undefined) next.rev = op.rev
  if (op.at) next.updatedAt = op.at
  return next
}

/** A board's custom types after dropping and putting some, as Go's applier.types; undefined when none are left. */
function putTypes(list: BlockDef[] | undefined, put: BlockDef[] = [], drop: string[] = []): BlockDef[] | undefined {
  let out = (list ?? []).filter((d) => !drop.includes(d.type))
  for (const d of put) {
    const i = out.findIndex((x) => x.type === d.type)
    if (i >= 0) out = [...out.slice(0, i), d, ...out.slice(i + 1)]
    else out = [...out, d]
  }
  return out.length ? out : undefined
}

/** Which list an id is in, if any. */
function findItem(doc: BoardDoc, id: string): { key: ListKey; item: DocItem; index: number } | null {
  for (const key of listKeys) {
    const list = doc[key] as DocItem[]
    const index = list.findIndex((x) => x.id === id)
    if (index >= 0) return { key, item: list[index], index }
  }
  return null
}

const isSide = (s: string | undefined): s is (typeof sides)[number] => !!s && (sides as string[]).includes(s)

/** The canvas's blocks and lines for a document: frames first (behind), then blocks, then strokes. */
function toCanvas(doc: BoardDoc): { blocks: BoardBlocks; lines: BoardLine[] } {
  const blocks: BoardBlocks = {}
  for (const f of doc.frames) blocks[f.id] = frameBlock(f)
  for (const b of doc.blocks) blocks[b.id] = blockBlock(b)
  for (const s of doc.ink) blocks[s.id] = strokeBlock(s)
  const lines = doc.links.filter((l) => blocks[l.from] && blocks[l.to]).map((l) => linkLine(l, blocks))
  return { blocks, lines }
}

const meta = (m: Meta): Meta => ({
  ...(m.createdBy ? { createdBy: m.createdBy } : {}),
  ...(m.updatedBy ? { updatedBy: m.updatedBy } : {}),
})

// What the canvas shows of a lock and a turn, but never changes itself.
const locked = (x: { locked?: boolean }): Partial<BoardBlock> => (x.locked ? { locked: true } : {})
const turned = (b: DocBlock): Partial<BoardBlock> => (b.rotation ? { rotation: b.rotation } : {})

function frameBlock(f: DocFrame): BoardBlock {
  const props: Record<string, unknown> = { name: f.name, ...(f.color ? { color: f.color } : {}) }
  return { label: blockLabel("frame", props), type: "frame", props, x: f.x, y: f.y, width: f.w, height: f.h, ...meta(f), ...locked(f) }
}

function blockBlock(b: DocBlock): BoardBlock {
  return { label: blockLabel(b.type, b.props), type: b.type, props: b.props, x: b.x, y: b.y, width: b.w, height: b.h, ...meta(b), ...locked(b), ...turned(b) }
}

function strokeBlock(s: DocStroke): BoardBlock {
  return { ...fromDocumentStroke(s), ...meta(s), ...locked(s) }
}

function linkLine(l: DocLink, blocks: BoardBlocks): BoardLine {
  const [a, b] = facing(blocks[l.from], blocks[l.to])
  return {
    id: l.id,
    from: `${l.from}:${isSide(l.fromPort) ? l.fromPort : a}`,
    to: `${l.to}:${isSide(l.toPort) ? l.toPort : b}`,
  }
}

/** What a canvas block is in the document. */
const blockKind = (b: BoardBlock): ListKey => (b.ink ? "ink" : b.type === "frame" ? "frames" : "blocks")

/**
 * A canvas block as its document item, keeping what the canvas doesn't
 * carry from the item it was (`was`). Meta is left as it was: Go sets it.
 */
function toDocItem(id: string, b: BoardBlock, was?: DocItem): { key: ListKey; item: DocItem } | null {
  const key = blockKind(b)
  const keep = was && findKindOf(was) === key ? was : undefined
  if (key === "ink") {
    const s = toDocumentStroke(id, b as Parameters<typeof toDocumentStroke>[1])
    const prev = keep as DocStroke | undefined
    const item: DocStroke = { ...prev, ...s, ...(prev?.frame ? { frame: prev.frame } : {}) }
    // What makes it a shape or a highlighter is the canvas's: one it took
    // away, like a fill, goes from the document too.
    for (const k of ["kind", "fill", "text", "highlight"] as const) if (!(k in s)) delete item[k]
    return { key, item }
  }
  if (key === "frames") {
    const props = b.props ?? {}
    const prev = keep as DocFrame | undefined
    const f: DocFrame = { ...prev, id, name: String(props.name ?? ""), x: b.x, y: b.y, w: b.width, h: b.height }
    if (typeof props.color === "string" && props.color) f.color = props.color
    else delete f.color
    return { key, item: f }
  }
  if (!b.type) return null
  const prev = keep as DocBlock | undefined
  return { key, item: { ...prev, id, type: b.type, props: b.props ?? {}, x: b.x, y: b.y, w: b.width, h: b.height } }
}

/** A canvas line as its document link, keeping its label and props. */
function toDocLink(line: BoardLine, was?: DocLink): DocLink {
  const end = (port: string) => {
    const i = port.lastIndexOf(":")
    return [port.slice(0, i), port.slice(i + 1)] as const
  }
  const [from, fromPort] = end(line.from)
  const [to, toPort] = end(line.to)
  return { ...was, id: line.id, from, to, fromPort, toPort }
}

/** The list an item belongs to, by its fields. */
function findKindOf(item: DocItem): ListKey {
  if ("points" in item) return "ink"
  if ("type" in item) return "blocks"
  if ("from" in item) return "links"
  return "frames"
}

/** Whether two document items are the same, apart from who made them and float noise. */
function sameItem(a: unknown, b: unknown): boolean {
  if (typeof a === "number" && typeof b === "number") return Math.abs(a - b) < 1e-6
  if (a === b) return true
  if (typeof a !== "object" || typeof b !== "object" || !a || !b) return false
  if (Array.isArray(a) !== Array.isArray(b)) return false
  const skip = (k: string) => k === "createdBy" || k === "updatedBy"
  const ka = Object.keys(a).filter((k) => !skip(k) && (a as Record<string, unknown>)[k] !== undefined)
  const kb = Object.keys(b).filter((k) => !skip(k) && (b as Record<string, unknown>)[k] !== undefined)
  if (ka.length !== kb.length) return false
  return ka.every((k) => sameItem((a as Record<string, unknown>)[k], (b as Record<string, unknown>)[k]))
}

export {
  putTypes,
  applyOp,
  blockKind,
  findItem,
  findKindOf,
  linkLine,
  listKeys,
  put,
  sameItem,
  toCanvas,
  toDocItem,
  toDocLink,
  type BoardDoc,
  type BoardOp,
  type DocBlock,
  type DocFrame,
  type DocItem,
  type DocLink,
  type DocStroke,
  type ListKey,
  type Meta,
}
