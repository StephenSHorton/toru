// Brainstorm templates in the board window. The templates are Go's
// (internal/board/templates.go); templates.json is written from them by a Go
// test, so a board made from a template over MCP (board.create with a
// template) and one placed from the window's Template menu are the same.
import templatesJSON from "./templates.json"
import { facing, type BoardBlocks, type BoardLine } from "./board"

type GoFrame = { id: string; name: string; x: number; y: number; w: number; h: number; color?: string }
type GoBlock = {
  id: string
  type: string
  x: number
  y: number
  w: number
  h: number
  frame?: string
  props: Record<string, unknown>
}
type GoLink = { id: string; from: string; to: string }

type BoardTemplate = {
  name: string
  title: string
  description: string
  frames: GoFrame[]
  blocks: GoBlock[]
  links: GoLink[]
}

const boardTemplates: readonly BoardTemplate[] = templatesJSON.templates as BoardTemplate[]

/** The words a block shows: its text, title or question. */
function blockText(type: string, props: Record<string, unknown>): string {
  const pick = type === "poll" ? props.question : type === "checklist" ? (props.title ?? "Checklist") : props.text
  return typeof pick === "string" ? pick : type
}

/**
 * A template as window blocks and lines, its top-left corner at x, y. Ids
 * get prefix, so a template can go on a board more than once. Frames come
 * first, so they sit behind the blocks in them.
 */
function templateBoard(
  name: string,
  { prefix = "", x = 0, y = 0 }: { prefix?: string; x?: number; y?: number } = {}
): { blocks: BoardBlocks; lines: BoardLine[] } | null {
  const t = boardTemplates.find((t) => t.name === name)
  if (!t) {
    return null
  }
  const all = [...t.frames, ...t.blocks]
  const dx = x - Math.min(...all.map((b) => b.x))
  const dy = y - Math.min(...all.map((b) => b.y))
  const blocks: BoardBlocks = {}
  for (const f of t.frames) {
    blocks[prefix + f.id] = {
      label: f.name,
      x: f.x + dx,
      y: f.y + dy,
      width: f.w,
      height: f.h,
      type: "frame",
      props: { name: f.name, ...(f.color ? { color: f.color } : {}) },
    }
  }
  for (const b of t.blocks) {
    blocks[prefix + b.id] = {
      label: blockText(b.type, b.props),
      x: b.x + dx,
      y: b.y + dy,
      width: b.w,
      height: b.h,
      type: b.type,
      props: { ...b.props },
    }
  }
  const lines = t.links.map((l) => {
    const from = prefix + l.from
    const to = prefix + l.to
    const [fs, ts] = facing(blocks[from], blocks[to])
    return { id: prefix + l.id, from: `${from}:${fs}`, to: `${to}:${ts}` }
  })
  return { blocks, lines }
}

export { blockText, boardTemplates, templateBoard, type BoardTemplate }
