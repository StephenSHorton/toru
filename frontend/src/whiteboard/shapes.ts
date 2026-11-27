// Shapes on the whiteboard's ink layer: rectangles, ellipses, lines, arrows,
// text labels and emoji, the shape tools of the drawing bar (DrawingBar.tsx).
//
// A shape is an ink stroke with a `kind` and exactly two points (ink.ts,
// `InkExtras`; Go's `board.Stroke`): a box's opposite corners (rect,
// ellipse, text, emoji) or a line's two ends (line, arrow). So it saves,
// syncs, undoes and exports like any stroke, yet stays a shape: select it,
// move it, drag its corners or ends, change its colour, width and fill.
//
// They work like the screenshot editor's shape tools (editor/tools/shapes.tsx
// and arrowline.tsx): drag from one corner or end to the other, Shift keeps a
// square, a circle or a 45° line, and the arrow's head is as long as the
// editor's. The 45° and square rules are the editor's own (editor/geometry.ts).
// The editor draws with Konva; the board draws SVG (InkView.tsx), so only the
// geometry is shared.
import { constrainAngle45 } from "@/editor/geometry"
import { absolutePoints, arrowHead, pressureScale, strokeBlock, type InkBlock, type InkExtras, type InkStroke, type ShapeKind } from "./ink"

type Point = { x: number; y: number }

/** How a new shape looks: the drawing bar's colour, width and fill. */
type ShapeStyle = { color: string; width: number; fill?: string; text?: string }

/** Shapes drawn as a box from corner to corner. The rest run end to end. */
const boxKinds: ShapeKind[] = ["rect", "ellipse", "text", "emoji"]
const isBoxKind = (kind: ShapeKind) => boxKinds.includes(kind)

/** One line of a text label is this many font sizes tall (Go's textLineHeight). */
const lineHeight = 1.25
/** A character is about this many font sizes wide, when the page can't measure (Go's textCharWidth). */
const charWidth = 0.6

/**
 * Where a drag from `a` to `b` ends once Shift's rule is applied: a square
 * box (so a square or a circle), or a line at a multiple of 45°.
 */
function constrain(kind: ShapeKind, a: Point, b: Point, shift: boolean): Point {
  if (!shift) return b
  if (!isBoxKind(kind)) return constrainAngle45(a.x, a.y, b.x, b.y)
  const w = b.x - a.x
  const h = b.y - a.y
  const s = Math.max(Math.abs(w), Math.abs(h))
  return { x: a.x + Math.sign(w || 1) * s, y: a.y + Math.sign(h || 1) * s }
}

/** A shape from `a` to `b` (canvas units) as a board block. */
function shapeBlock(kind: ShapeKind, a: Point, b: Point, style: ShapeStyle): InkBlock {
  return strokeBlock([a.x, a.y, 0.5, b.x, b.y, 0.5], style.color, style.width, {
    kind,
    fill: style.fill,
    text: style.text,
  })
}

/** A shape block's two points in canvas units. */
function shapeEnds(block: { x: number; y: number; ink: InkStroke }): [Point, Point] {
  const p = absolutePoints(block)
  return [
    { x: p[0], y: p[1] },
    { x: p[3], y: p[4] },
  ]
}

/** A stroke's two points relative to its block. */
function localEnds(stroke: InkStroke): [Point, Point] {
  const p = stroke.points
  return [
    { x: p[0], y: p[1] },
    { x: p[3], y: p[4] },
  ]
}

/** The box between two points. */
function boxOf(a: Point, b: Point) {
  const x = Math.min(a.x, b.x)
  const y = Math.min(a.y, b.y)
  return { x, y, w: Math.abs(b.x - a.x), h: Math.abs(b.y - a.y) }
}

/** A text label's lines and font size, from the height of its box. */
function textLayout(text: string, height: number) {
  const lines = text.split("\n")
  return { lines, size: height / (lines.length * lineHeight) }
}

let measure: CanvasRenderingContext2D | null | undefined

/**
 * The box a text label takes at a font size: as wide as its longest line,
 * measured in the page's font when it can be, and `lineHeight` sizes a line.
 */
function textBox(text: string, size: number, font = "system-ui, sans-serif") {
  const lines = text.split("\n")
  if (measure === undefined) {
    try {
      measure = typeof document === "undefined" ? null : document.createElement("canvas").getContext("2d")
    } catch {
      measure = null
    }
  }
  let widest = 0
  for (const line of lines) {
    let w = 0
    if (measure && typeof measure.measureText === "function") {
      measure.font = `${size}px ${font}`
      w = measure.measureText(line).width
    }
    if (!(w > 0)) w = Math.max(1, [...line].length) * size * charWidth
    widest = Math.max(widest, w)
  }
  return { w: Math.ceil(widest + size * 0.2), h: Math.ceil(lines.length * size * lineHeight) }
}

/** The three corners of an arrow's head at `to`, pointing away from `from`. */
function arrowHeadPoints(from: Point, to: Point, width: number): [Point, Point, Point] {
  const len = arrowHead(width)
  const angle = Math.atan2(to.y - from.y, to.x - from.x)
  const spread = Math.PI / 7
  return [
    to,
    { x: to.x - len * Math.cos(angle - spread), y: to.y - len * Math.sin(angle - spread) },
    { x: to.x - len * Math.cos(angle + spread), y: to.y - len * Math.sin(angle + spread) },
  ]
}

/** Where an arrow's shaft stops, so its square end hides under the head. */
function arrowShaftEnd(from: Point, to: Point, width: number): Point {
  const d = Math.hypot(to.x - from.x, to.y - from.y)
  if (d < 1e-6) return to
  const back = Math.min(d, arrowHead(width) * 0.6)
  return { x: to.x - ((to.x - from.x) / d) * back, y: to.y - ((to.y - from.y) / d) * back }
}

// ── the eraser's reach ───────────────────────────────────────────────────

function segmentDistance(p: Point, a: Point, b: Point) {
  const dx = b.x - a.x
  const dy = b.y - a.y
  const len2 = dx * dx + dy * dy
  const t = len2 ? Math.max(0, Math.min(1, ((p.x - a.x) * dx + (p.y - a.y) * dy) / len2)) : 0
  return Math.hypot(p.x - (a.x + t * dx), p.y - (a.y + t * dy))
}

/**
 * How far a point (canvas units) is from a stroke's ink: 0 inside a filled
 * shape, a text label or an emoji, else from the nearest edge of its line.
 */
function inkDistance(block: { x: number; y: number; ink: InkStroke }, p: Point): number {
  const { ink } = block
  const half = ink.width / 2
  if (!ink.kind) {
    const pts = absolutePoints(block)
    let best = Infinity
    const n = pts.length / 3
    if (n === 1) return Math.max(0, Math.hypot(p.x - pts[0], p.y - pts[1]) - half * pressureScale(pts[2]))
    for (let i = 0; i < n - 1; i++) {
      const a = { x: pts[i * 3], y: pts[i * 3 + 1] }
      const b = { x: pts[i * 3 + 3], y: pts[i * 3 + 4] }
      const r = half * Math.max(pressureScale(pts[i * 3 + 2]), pressureScale(pts[i * 3 + 5]))
      best = Math.min(best, segmentDistance(p, a, b) - r)
    }
    return Math.max(0, best)
  }
  const [a, b] = shapeEnds(block)
  const box = boxOf(a, b)
  const inside = p.x >= box.x && p.x <= box.x + box.w && p.y >= box.y && p.y <= box.y + box.h
  switch (ink.kind) {
    case "text":
    case "emoji":
      return inside ? 0 : Math.max(0, Math.hypot(Math.max(box.x - p.x, 0, p.x - box.x - box.w), Math.max(box.y - p.y, 0, p.y - box.y - box.h)))
    case "line":
    case "arrow":
      return Math.max(0, segmentDistance(p, a, b) - half)
    case "rect": {
      if (inside && ink.fill) return 0
      const c = [
        { x: box.x, y: box.y },
        { x: box.x + box.w, y: box.y },
        { x: box.x + box.w, y: box.y + box.h },
        { x: box.x, y: box.y + box.h },
      ]
      let best = Infinity
      for (let i = 0; i < 4; i++) best = Math.min(best, segmentDistance(p, c[i], c[(i + 1) % 4]))
      return Math.max(0, best - half)
    }
    case "ellipse": {
      const rx = box.w / 2
      const ry = box.h / 2
      const cx = box.x + rx
      const cy = box.y + ry
      if (ink.fill && rx > 0 && ry > 0 && ((p.x - cx) / rx) ** 2 + ((p.y - cy) / ry) ** 2 <= 1) return 0
      let best = Infinity
      let prev = { x: cx + rx, y: cy }
      for (let i = 1; i <= 64; i++) {
        const t = (i / 64) * Math.PI * 2
        const next = { x: cx + rx * Math.cos(t), y: cy + ry * Math.sin(t) }
        best = Math.min(best, segmentDistance(p, prev, next))
        prev = next
      }
      return Math.max(0, best - half)
    }
  }
}

// ── reshaping ────────────────────────────────────────────────────────────

/** A shape's handles: a line's two ends, or a box's four corners. */
type HandleName = "start" | "end" | "nw" | "ne" | "se" | "sw"

/** Where each of a shape's handles sits, in canvas units. */
function shapeHandles(block: { x: number; y: number; ink: InkStroke }): { name: HandleName; at: Point }[] {
  const [a, b] = shapeEnds(block)
  if (!block.ink.kind || !isBoxKind(block.ink.kind)) {
    return [
      { name: "start", at: a },
      { name: "end", at: b },
    ]
  }
  const box = boxOf(a, b)
  return [
    { name: "nw", at: { x: box.x, y: box.y } },
    { name: "ne", at: { x: box.x + box.w, y: box.y } },
    { name: "se", at: { x: box.x + box.w, y: box.y + box.h } },
    { name: "sw", at: { x: box.x, y: box.y + box.h } },
  ]
}

const opposite: Record<string, HandleName> = { nw: "se", ne: "sw", se: "nw", sw: "ne" }

/**
 * A shape with one handle dragged to `to` (canvas units). A line's end moves
 * and the other stays; a box's corner moves and the opposite corner stays.
 * Shift keeps the line at 45° or the box square. An emoji stays square.
 */
function reshape(block: { x: number; y: number; ink: InkStroke }, handle: HandleName, to: Point, shift: boolean): InkBlock {
  const { ink } = block
  const kind = ink.kind as ShapeKind
  const [a, b] = shapeEnds(block)
  const style: ShapeStyle = { color: ink.color, width: ink.width, fill: ink.fill, text: ink.text }
  if (handle === "start") return shapeBlock(kind, constrain(kind, b, to, shift), b, style)
  if (handle === "end") return shapeBlock(kind, a, constrain(kind, a, to, shift), style)
  const fixed = shapeHandles(block).find((h) => h.name === opposite[handle])!.at
  return shapeBlock(kind, fixed, constrain(kind, fixed, to, shift || kind === "emoji"), style)
}

/** A stroke restyled: a new colour, width or fill, its points as they are. */
function restyle(block: { x: number; y: number; ink: InkStroke }, change: Partial<Pick<InkExtras, "fill">> & { color?: string; width?: number }): InkBlock {
  const { ink } = block
  const extras: InkExtras = { kind: ink.kind, fill: ink.fill, text: ink.text, highlight: ink.highlight }
  if ("fill" in change) extras.fill = change.fill
  return strokeBlock(absolutePoints(block), change.color ?? ink.color, change.width ?? ink.width, extras)
}

export {
  arrowHeadPoints,
  arrowShaftEnd,
  boxOf,
  constrain,
  inkDistance,
  isBoxKind,
  lineHeight,
  localEnds,
  reshape,
  restyle,
  shapeBlock,
  shapeEnds,
  shapeHandles,
  textBox,
  textLayout,
  type HandleName,
  type Point,
  type ShapeStyle,
}
