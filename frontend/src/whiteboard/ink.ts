// Freehand ink for the whiteboard: Toru's own UI SDK primitive, since UI SDK
// has no ink or stroke behavior (docs/whiteboard.md, section 1).
//
// The pen is the primitive. While it's on, its surface lies over the board and
// a press with a mouse or a pen follows the pointer with UI SDK's drag session
// (`bindDrag`), collecting points in canvas units. The stroke being drawn shows
// as an SVG path; when the pointer lifts, the finished stroke goes to
// `onStroke`, and the board keeps it as a block (board.ts, `BoardBlock.ink`),
// so strokes select, lasso, move and delete with everything else.
//
// A stroke's points are flat x, y, pressure triples, the shape the board
// document keeps in Go (internal/board, `Stroke`). On the board they're
// relative to the stroke's box; in the document they're absolute
// (`toDocumentStroke`, `fromDocumentStroke`).
//
// Colours and widths come from Toru's tokens (index.css, `--ink-*`). A stroke
// keeps its colour as the token's name, like `ink-blue`, so it follows the
// theme; any other CSS colour, as an agent might send, is drawn as written.
import { bindDrag, css, definePrimitive, live } from "@ui-sdk/core"

/** The shapes a stroke can be (shapes.ts); a freehand line has none. Go's `board.ShapeKinds`. */
type ShapeKind = "rect" | "ellipse" | "line" | "arrow" | "text" | "emoji"

/** What, besides its points, colour and width, makes a stroke a shape or a highlighter. */
type InkExtras = {
  /** A shape's kind. Its points are then exactly two: a box's corners or a line's ends. */
  kind?: ShapeKind
  /** A rect's or ellipse's inside colour, drawn see-through. */
  fill?: string
  /** A text label's words, or an emoji. */
  text?: string
  /** A freehand highlighter stroke: see-through, and drawn under the other ink. */
  highlight?: boolean
}

/** A stroke's ink: points relative to its block's top left corner. */
type InkStroke = InkExtras & {
  /** x, y, pressure for each point; pressure 0..1, 0.5 when the device has none. */
  points: number[]
  /** A pen token's name, like `ink-blue`, or a CSS colour. */
  color: string
  /** The line's width in canvas units. */
  width: number
}

/** A finished stroke as a board block: its box on the canvas and its ink. */
type InkBlock = {
  label: string
  x: number
  y: number
  width: number
  height: number
  ink: InkStroke
}

/** A stroke as the board document keeps it (Go's `board.Stroke`). */
type DocumentStroke = InkExtras & {
  id: string
  points: number[]
  color: string
  width: number
  frame?: string
}

type Point = { x: number; y: number }

/**
 * The pens: Toru's ink tokens (index.css), in the order the drawing bar shows
 * them. The theme's five first, then a few more. "Ink" is the theme's text
 * colour: white on a dark board, near black on a light one. Go's `inkPens`
 * (agent_commands_board_ink.go) lists the same.
 */
const inkColors = [
  { token: "ink-white", name: "Ink" },
  { token: "ink-blue", name: "Blue" },
  { token: "ink-red", name: "Red" },
  { token: "ink-yellow", name: "Yellow" },
  { token: "ink-green", name: "Green" },
  { token: "ink-orange", name: "Orange" },
  { token: "ink-purple", name: "Purple" },
  { token: "ink-pink", name: "Pink" },
  { token: "ink-grey", name: "Grey" },
] as const

/** Pen widths: Toru's `--ink-width-*` tokens, with the width used before CSS is read. */
const inkWidths = [
  { token: "ink-width-fine", name: "Fine", fallback: 2 },
  { token: "ink-width-medium", name: "Medium", fallback: 4 },
  { token: "ink-width-bold", name: "Bold", fallback: 8 },
] as const

/** The board document's limit on one stroke's points (Go's `MaxStrokeFloats / 3`). */
const maxPoints = 20000
/** How far, in screen pixels, the pointer moves before a new point is kept. */
const minGap = 1.5
/**
 * Stroke smoothing: as each point is kept, the one before it moves this
 * share of the way towards the middle of its neighbours, which evens out a
 * hand's wobble while the stroke is drawn, so the draft and the finished
 * stroke are the same line. A sharp turn (more than `cornerTurn`) is a
 * corner the hand meant, and stays where it is, as do the first and last
 * points. Pressure isn't touched.
 */
const smoothing = 0.5
/** The cosine of the turn past which a point is a corner and isn't smoothed: 60°. */
const cornerTurn = 0.5

/** Smooths point `j` of flat triples towards its neighbours (`smoothing`), unless it's a corner or an end. */
function smoothPoint(points: number[], j: number) {
  const n = points.length / 3
  if (j <= 0 || j >= n - 1) return
  const ax = points[j * 3 - 3], ay = points[j * 3 - 2]
  const bx = points[j * 3], by = points[j * 3 + 1]
  const cx = points[j * 3 + 3], cy = points[j * 3 + 4]
  const ux = bx - ax, uy = by - ay
  const vx = cx - bx, vy = cy - by
  const lu = Math.hypot(ux, uy)
  const lv = Math.hypot(vx, vy)
  if (!lu || !lv || (ux * vx + uy * vy) / (lu * lv) < cornerTurn) return
  points[j * 3] = bx + ((ax + cx) / 2 - bx) * smoothing
  points[j * 3 + 1] = by + ((ay + cy) / 2 - by) * smoothing
}

const tokenPattern = /^ink-[a-z0-9-]+$/

/** The CSS colour a stroke is drawn in: a pen token's variable, or the colour as written. */
function inkColor(color: string) {
  return tokenPattern.test(color) ? `var(--${color})` : color
}

/** A pen width token's value in canvas units, read from the page's CSS. */
function tokenWidth(token: string, fallback: number) {
  if (typeof document === "undefined") {
    return fallback
  }
  const value = parseFloat(
    getComputedStyle(document.documentElement).getPropertyValue(`--${token}`)
  )
  return value > 0 ? value : fallback
}

/** A point's pressure: a pen's own, or 0.5 for a mouse, as the document asks. */
function pressureOf(event: PointerEvent) {
  const p = event.pointerType === "pen" ? event.pressure : 0.5
  return p > 0 && p <= 1 ? p : 0.5
}

const num = (v: number) => String(Math.round(v * 100) / 100)

/**
 * A smooth SVG path through flat x, y, pressure triples: quadratic curves
 * through the midpoints between points, so the line has no corners. One point
 * draws a dot (with round caps).
 */
function inkPath(points: number[]) {
  const n = Math.floor(points.length / 3)
  if (!n) {
    return ""
  }
  const x = (i: number) => points[i * 3]
  const y = (i: number) => points[i * 3 + 1]
  let d = `M${num(x(0))},${num(y(0))}`
  if (n === 1) {
    return `${d} L${num(x(0))},${num(y(0))}`
  }
  if (n === 2) {
    return `${d} L${num(x(1))},${num(y(1))}`
  }
  for (let i = 1; i < n - 1; i++) {
    const mx = (x(i) + x(i + 1)) / 2
    const my = (y(i) + y(i + 1)) / 2
    d += ` Q${num(x(i))},${num(y(i))} ${num(mx)},${num(my)}`
  }
  return `${d} L${num(x(n - 1))},${num(y(n - 1))}`
}

/**
 * How much a point's pressure widens or thins the line: 1 at 0.5, the
 * pressure a mouse gives, so the pen's width is the width at an even press.
 */
function pressureScale(pressure: number) {
  const p = pressure >= 0 && pressure <= 1 ? pressure : 0.5
  return 0.3 + 1.4 * p
}

/** Whether a stroke's pressure changes along it, as a stylus's does. A mouse's never does. */
function hasPressure(points: number[]) {
  for (let i = 5; i < points.length; i += 3) {
    if (points[i] !== points[2]) {
      return true
    }
  }
  return false
}

/** The widest a stroke gets, as a share of its pen's width. */
function maxPressureScale(points: number[]) {
  if (!hasPressure(points)) {
    return 1
  }
  let most = 0
  for (let i = 2; i < points.length; i += 3) {
    most = Math.max(most, pressureScale(points[i]))
  }
  return most
}

/** A circle as one closed SVG subpath, drawn the same way round as `segmentOutline`'s. */
function circlePath(x: number, y: number, r: number) {
  return (
    `M${num(x + r)},${num(y)}` +
    ` A${num(r)},${num(r)} 0 1 0 ${num(x - r)},${num(y)}` +
    ` A${num(r)},${num(r)} 0 1 0 ${num(x + r)},${num(y)}Z`
  )
}

/**
 * One piece of a pressure stroke: the outline around two circles, the line's
 * width at each end, so the piece tapers from one to the other with round
 * ends. All pieces wind the same way, so with the default fill rule their
 * overlaps fill and the pieces join into one smooth line with round joins.
 */
function segmentOutline(x1: number, y1: number, r1: number, x2: number, y2: number, r2: number) {
  const dx = x2 - x1
  const dy = y2 - y1
  const d = Math.hypot(dx, dy)
  // One circle holds the other: the piece is the bigger circle.
  if (d <= Math.abs(r1 - r2) || d < 1e-6) {
    return r1 >= r2 ? circlePath(x1, y1, r1) : circlePath(x2, y2, r2)
  }
  const along = Math.atan2(dy, dx)
  // The angle, from the line's direction, where the outline leaves each circle.
  const a = Math.acos((r1 - r2) / d)
  const at = (x: number, y: number, r: number, angle: number) =>
    `${num(x + r * Math.cos(angle))},${num(y + r * Math.sin(angle))}`
  const far = 2 * a > Math.PI ? 1 : 0
  const back = 2 * a < Math.PI ? 1 : 0
  return (
    `M${at(x1, y1, r1, along + a)}` +
    ` L${at(x2, y2, r2, along + a)}` +
    ` A${num(r2)},${num(r2)} 0 ${far} 0 ${at(x2, y2, r2, along - a)}` +
    ` L${at(x1, y1, r1, along - a)}` +
    ` A${num(r1)},${num(r1)} 0 ${back} 0 ${at(x1, y1, r1, along + a)}Z`
  )
}

/**
 * Points along a stroke's smooth line (the curves `inkPath` draws), close
 * enough together that straight pieces between them look curved, each with
 * its pressure. Pressure is evened out over neighbouring points first, so a
 * stylus's small jitters don't show as lumps.
 */
function inkSamples(points: number[]) {
  const n = Math.floor(points.length / 3)
  const x = (i: number) => points[i * 3]
  const y = (i: number) => points[i * 3 + 1]
  const raw = (i: number) => points[i * 3 + 2]
  const p = (i: number) => {
    const a = raw(Math.max(0, i - 1))
    const c = raw(Math.min(n - 1, i + 1))
    return (a + 2 * raw(i) + c) / 4
  }
  const out: number[] = [x(0), y(0), p(0)]
  if (n === 1) {
    return out
  }
  // Each curve runs from one midpoint to the next, bending at the point between.
  const curve = (
    x0: number, y0: number, p0: number,
    cx: number, cy: number, cp: number,
    x1: number, y1: number, p1: number
  ) => {
    const length = Math.hypot(cx - x0, cy - y0) + Math.hypot(x1 - cx, y1 - cy)
    const steps = Math.max(1, Math.min(16, Math.ceil(length / 2)))
    for (let s = 1; s <= steps; s++) {
      const t = s / steps
      const u = 1 - t
      out.push(
        u * u * x0 + 2 * u * t * cx + t * t * x1,
        u * u * y0 + 2 * u * t * cy + t * t * y1,
        u * u * p0 + 2 * u * t * cp + t * t * p1
      )
    }
  }
  let sx = x(0)
  let sy = y(0)
  let sp = p(0)
  for (let i = 1; i < n - 1; i++) {
    const mx = (x(i) + x(i + 1)) / 2
    const my = (y(i) + y(i + 1)) / 2
    const mp = (p(i) + p(i + 1)) / 2
    curve(sx, sy, sp, x(i), y(i), p(i), mx, my, mp)
    sx = mx
    sy = my
    sp = mp
  }
  const ex = x(n - 1)
  const ey = y(n - 1)
  const ep = p(n - 1)
  curve(sx, sy, sp, (sx + ex) / 2, (sy + ey) / 2, (sp + ep) / 2, ex, ey, ep)
  return out
}

/**
 * A stroke with pressure as a filled SVG outline: the smooth line `inkPath`
 * draws, `width` wide where the pressure is 0.5 and wider or thinner as the
 * stylus pressed harder or lighter. Draw it with `fill`, not `stroke`.
 */
function inkOutline(points: number[], width: number) {
  const n = Math.floor(points.length / 3)
  if (!n) {
    return ""
  }
  const s = inkSamples(points)
  const r = (i: number) => (width / 2) * pressureScale(s[i * 3 + 2])
  const count = s.length / 3
  if (count === 1) {
    return circlePath(s[0], s[1], r(0))
  }
  const pieces: string[] = []
  for (let i = 0; i < count - 1; i++) {
    pieces.push(
      segmentOutline(s[i * 3], s[i * 3 + 1], r(i), s[i * 3 + 3], s[i * 3 + 4], r(i + 1))
    )
  }
  return pieces.join(" ")
}

/**
 * How a stroke is drawn in SVG. A stroke whose pressure changes along it, as
 * a stylus's does, is a filled outline that thickens and thins with the
 * pressure. One with even pressure, as every mouse stroke has, is today's
 * line of one width: the smooth path, stroked.
 */
function inkShape(points: number[], width: number) {
  return hasPressure(points)
    ? { filled: true, d: inkOutline(points, width) }
    : { filled: false, d: inkPath(points) }
}

/** What a stroke is called, for screen readers and the agent bridge. */
const strokeLabels: Record<ShapeKind, string> = {
  rect: "Rectangle",
  ellipse: "Ellipse",
  line: "Line",
  arrow: "Arrow",
  text: "Text label",
  emoji: "Emoji",
}

function strokeLabel(extras: InkExtras) {
  if (extras.kind === "text" && extras.text) return `Text label: ${extras.text.split("\n")[0].slice(0, 60)}`
  if (extras.kind === "emoji" && extras.text) return `Emoji ${extras.text}`
  if (extras.kind) return strokeLabels[extras.kind]
  return extras.highlight ? "Highlighter stroke" : "Ink stroke"
}

/** Only the extras that are set, so a plain stroke stays exactly as it was. */
function cleanExtras(extras: InkExtras): InkExtras {
  const out: InkExtras = {}
  if (extras.kind) out.kind = extras.kind
  if ((extras.kind === "rect" || extras.kind === "ellipse") && extras.fill) out.fill = extras.fill
  if ((extras.kind === "text" || extras.kind === "emoji") && extras.text) out.text = extras.text
  if (!extras.kind && extras.highlight) out.highlight = true
  return out
}

/** The head of an arrow this wide: how long it is, from its tip. Like the screenshot editor's arrows. */
function arrowHead(width: number) {
  return Math.max(10, width * 3)
}

/**
 * How far a stroke's line reaches past its points: half its widest width,
 * an arrow's head, and nothing for a text label or emoji, whose points are
 * its box.
 */
function strokePad(points: number[], width: number, extras: InkExtras) {
  if (extras.kind === "text" || extras.kind === "emoji") return 0
  if (extras.kind === "arrow") return Math.max(width / 2, arrowHead(width) * 0.6)
  if (extras.kind) return width / 2
  return (width / 2) * maxPressureScale(points)
}

/**
 * A finished stroke as a board block: its box is the line's bounds, widened by
 * half its widest width so the whole line sits inside, and its points move to
 * be relative to that box. `points` are absolute canvas x, y, pressure triples.
 * A shape (`extras.kind`) has two points: its box's corners or its line's ends.
 */
function strokeBlock(points: number[], color: string, width: number, extras: InkExtras = {}): InkBlock {
  const more = cleanExtras(extras)
  let left = Infinity
  let top = Infinity
  let right = -Infinity
  let bottom = -Infinity
  for (let i = 0; i < points.length; i += 3) {
    left = Math.min(left, points[i])
    right = Math.max(right, points[i])
    top = Math.min(top, points[i + 1])
    bottom = Math.max(bottom, points[i + 1])
  }
  const pad = strokePad(points, width, more)
  const x = left - pad
  const y = top - pad
  const relative = points.map((v, i) =>
    i % 3 === 0 ? v - x : i % 3 === 1 ? v - y : v
  )
  return {
    label: strokeLabel(more),
    x,
    y,
    width: right - left + 2 * pad,
    height: bottom - top + 2 * pad,
    ink: { points: relative, color, width, ...more },
  }
}

/** A stroke's points in canvas units, from its block. */
function absolutePoints(block: { x: number; y: number; ink: InkStroke }) {
  const { x, y, ink } = block
  return ink.points.map((v, i) => (i % 3 === 0 ? v + x : i % 3 === 1 ? v + y : v))
}

/** A block's stroke as the board document keeps it: absolute points. */
function toDocumentStroke(id: string, block: Omit<InkBlock, "label">): DocumentStroke {
  const { ink } = block
  return {
    id,
    points: absolutePoints(block),
    color: ink.color,
    width: ink.width,
    ...cleanExtras(ink),
  }
}

/** A document stroke as a board block. */
function fromDocumentStroke(stroke: DocumentStroke): InkBlock {
  return strokeBlock(stroke.points, stroke.color, stroke.width, stroke)
}

/** A new stroke's id: the document's id pattern, never a ":" (board.ts). */
function strokeId() {
  return `ink-${Date.now().toString(36)}${Math.random().toString(36).slice(2, 8)}`
}

type InkOptions = {
  /** Whether the pen is on: its surface takes presses and draws. */
  active?: boolean
  /** The pen's colour: a token's name like `ink-blue`, or a CSS colour. Defaults to `ink-white`. */
  color?: string
  /** The pen's width in canvas units. Defaults to the fine pen token. */
  width?: number
  /** A highlighter: its strokes are see-through, under the other ink, and even (no pressure). */
  highlight?: boolean
  /** Turns a point on screen (client pixels) into canvas units, like the board viewport's `toCanvas`. */
  toCanvas?: (point: Point) => Point
  /** A stroke was finished. Its id is new and free to use. */
  onStroke?: (id: string, block: InkBlock, details: { source: "person" }) => void
}

type InkState = {
  active: boolean
  color: string
  width: number
  highlight: boolean
  /** The stroke being drawn, absolute canvas triples, or null. */
  draft: number[] | null
  /** Counts points added to the draft, so its path redraws. */
  drawn: number
}

const ink = /* @__PURE__ */ definePrimitive({
  name: "ink",
  options: {
    active: "boolean",
    color: "string",
    width: "number",
    highlight: "boolean",
    toCanvas: "callback",
    onStroke: "callback",
  },
  state: (options: InkOptions): InkState => ({
    active: !!options.active,
    color: options.color ?? "ink-white",
    width: options.width ?? tokenWidth("ink-width-fine", 2),
    highlight: !!options.highlight,
    draft: null,
    drawn: 0,
  }),
  sync: (options) => ({
    active: !!options.active,
    highlight: !!options.highlight,
    ...(options.color === undefined ? {} : { color: options.color }),
    ...(options.width === undefined ? {} : { width: options.width }),
  }),
  controlled: ["active", "color", "width", "highlight"],
  live: ["draft", "drawn"],
  api: (context) => {
    const { store } = context
    const toCanvas = (p: Point) => context.options().toCanvas?.(p) ?? p
    let stop: (() => void) | undefined
    context.onDestroy(() => stop?.())

    // The finished stroke keeps every point the pen kept (at least minGap
    // apart on screen), so it looks exactly like the draft: dropping points
    // would let the smoothing round off corners.
    const finish = (points: number[], source: "person") => {
      const { color, width, highlight } = store.get()
      const kept = points.slice(0, maxPoints * 3)
      // A highlighter's line is even: it keeps no pressure.
      if (highlight) for (let i = 2; i < kept.length; i += 3) kept[i] = 0.5
      const block = strokeBlock(kept, color, width, { highlight })
      context.options().onStroke?.(strokeId(), block, { source })
      return block
    }

    const api = {
      /** Stops the stroke being drawn without keeping it. */
      cancel() {
        stop?.()
        stop = undefined
        store.set({ draft: null })
      },

      surfacePointerdown(event: PointerEvent, el: HTMLElement) {
        // Touch pans and pinches, as on the rest of the board; a mouse's other
        // buttons pan too. Both fall through to the board.
        if (!store.get().active || event.button !== 0 || event.pointerType === "touch") {
          return
        }
        event.stopPropagation()
        event.preventDefault()
        stop?.()
        const points: number[] = []
        let last: Point | null = null
        const add = (e: PointerEvent, always = false) => {
          const at = { x: e.clientX, y: e.clientY }
          if (!always && last && Math.hypot(at.x - last.x, at.y - last.y) < minGap) {
            return
          }
          if (points.length >= maxPoints * 3) {
            return
          }
          last = at
          const p = toCanvas(at)
          points.push(p.x, p.y, pressureOf(e))
          // The point before now has both neighbours: smooth it.
          smoothPoint(points, points.length / 3 - 2)
        }
        add(event, true)
        store.set({ draft: points, drawn: points.length })
        stop = bindDrag(el, event, {
          cancelOnEscape: true,
          onMove: ({ event: move }) => {
            // Moves arrive once a frame; the browser keeps the ones in between.
            const between = move.getCoalescedEvents?.() ?? []
            for (const e of between.length ? between : [move]) {
              add(e)
            }
            store.set({ drawn: points.length })
          },
          onEnd: (up, canceled) => {
            stop = undefined
            if (!canceled) {
              if (up.clientX !== last?.x || up.clientY !== last?.y) {
                add(up, true)
              }
              finish(points, "person")
            }
            store.set({ draft: null, drawn: 0 })
          },
        })
      },
    }
    return api
  },
  slots: (slot) => ({
    // Lies over the board while the pen is on; presses on it draw.
    surface: slot({
      attrs: (state) => ({
        "aria-hidden": "true",
        // A press the pen leaves alone, like touch, pans the board under it.
        "data-board-empty": "",
        "data-active": state.active ? "" : undefined,
        hidden: state.active ? undefined : "",
        style: css({
          position: "absolute",
          inset: 0,
          "touch-action": "none",
        }),
      }),
      on: {
        pointerdown: (event, { el, api }) => el && api.surfacePointerdown(event, el),
      },
    }),
    // The stroke being drawn, in canvas units: put it in the board's world.
    draft: slot({
      // Round caps and joins come from ink.css.
      attrs: () => ({ fill: "none", "aria-hidden": "true" }),
      // A stylus's draft is filled, like its finished stroke (`inkShape`).
      live: live<InkState, any, {}>((state) => {
        const shape = state.draft ? inkShape(state.draft, state.width) : { filled: false, d: "" }
        return {
          d: shape.d,
          "data-filled": shape.filled ? "" : undefined,
          "data-highlight": state.highlight ? "" : undefined,
          fill: shape.filled ? inkColor(state.color) : "none",
          stroke: shape.filled ? "none" : inkColor(state.color),
          "stroke-width": String(state.width),
        }
      }),
    }),
  }),
})

export {
  absolutePoints,
  arrowHead,
  cleanExtras,
  fromDocumentStroke,
  ink,
  inkColor,
  inkColors,
  inkOutline,
  inkPath,
  inkShape,
  inkWidths,
  maxPressureScale,
  maxPoints,
  pressureScale,
  smoothPoint,
  smoothing,
  strokeBlock,
  strokeLabel,
  strokeId,
  toDocumentStroke,
  tokenWidth,
  type DocumentStroke,
  type InkBlock,
  type InkExtras,
  type ShapeKind,
  type InkOptions,
  type InkState,
  type InkStroke,
}
