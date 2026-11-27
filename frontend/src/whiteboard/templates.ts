// Brainstorm templates in the board window. The templates are Go's
// (internal/board/templates.go); templates.json is written from them by a Go
// test, so a board made from a template over MCP (board.create with a
// template) and one placed from the window's Template menu are the same.
import type { BoardBlocks, BoardLine } from "./board"
import { fromBoardFile, type BoardFile } from "./sample"
import templatesJSON from "./templates.json"

type BoardTemplate = BoardFile & { name: string; title: string; description: string }

const boardTemplates: readonly BoardTemplate[] = templatesJSON.templates as BoardTemplate[]

/**
 * A template as window blocks and lines (frames first, so they sit behind),
 * its top-left corner at x, y. Ids get prefix, so a template can go on a
 * board more than once. Null for an unknown name.
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
  const move = <T extends { id: string; x: number; y: number }>(b: T): T => ({
    ...b,
    id: prefix + b.id,
    x: b.x + dx,
    y: b.y + dy,
  })
  return fromBoardFile({
    frames: t.frames.map(move),
    blocks: t.blocks.map((b) => ({ ...move(b), props: structuredClone(b.props) })),
    links: t.links.map((l) => ({ id: prefix + l.id, from: prefix + l.from, to: prefix + l.to })),
  })
}

export { boardTemplates, templateBoard, type BoardTemplate }
