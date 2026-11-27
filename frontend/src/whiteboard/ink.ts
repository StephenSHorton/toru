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

/** A stroke's ink: points relative to its block's top left corner. */
type InkStroke = {
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
type DocumentStroke = {
  id: string
  points: number[]
  color: string
  width: number
  frame?: string
}

type Point = { x: number; y: number }

/** The pens: Toru's ink tokens, in the order the toolbar shows them. */
const inkColors = [
  { token: "ink-white", name: "White" },
  { token: "ink-blue", name: "Blue" },
  { token: "ink-red", name: "Red" },
  { token: "ink-yellow", name: "Yellow" },
  { token: "ink-green", name: "Green" },
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
 * A finished stroke as a board block: its box is the line's bounds, widened by
 * half its width so the whole line sits inside, and its points move to be
 * relative to that box. `points` are absolute canvas x, y, pressure triples.
 */
function strokeBlock(points: number[], color: string, width: number): InkBlock {
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
  const pad = width / 2
  const x = left - pad
  const y = top - pad
  const relative = points.map((v, i) =>
    i % 3 === 0 ? v - x : i % 3 === 1 ? v - y : v
  )
  return {
    label: "Ink stroke",
    x,
    y,
    width: right - left + width,
    height: bottom - top + width,
    ink: { points: relative, color, width },
  }
}

/** A block's stroke as the board document keeps it: absolute points. */
function toDocumentStroke(id: string, block: Omit<InkBlock, "label">): DocumentStroke {
  const { x, y, ink } = block
  return {
    id,
    points: ink.points.map((v, i) => (i % 3 === 0 ? v + x : i % 3 === 1 ? v + y : v)),
    color: ink.color,
    width: ink.width,
  }
}

/** A document stroke as a board block. */
function fromDocumentStroke(stroke: DocumentStroke): InkBlock {
  return strokeBlock(stroke.points, stroke.color, stroke.width)
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
  /** Turns a point on screen (client pixels) into canvas units, like the board viewport's `toCanvas`. */
  toCanvas?: (point: Point) => Point
  /** A stroke was finished. Its id is new and free to use. */
  onStroke?: (id: string, block: InkBlock, details: { source: "person" }) => void
}

type InkState = {
  active: boolean
  color: string
  width: number
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
    toCanvas: "callback",
    onStroke: "callback",
  },
  state: (options: InkOptions): InkState => ({
    active: !!options.active,
    color: options.color ?? "ink-white",
    width: options.width ?? tokenWidth("ink-width-fine", 2),
    draft: null,
    drawn: 0,
  }),
  sync: (options) => ({
    active: !!options.active,
    ...(options.color === undefined ? {} : { color: options.color }),
    ...(options.width === undefined ? {} : { width: options.width }),
  }),
  controlled: ["active", "color", "width"],
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
      const { color, width } = store.get()
      const block = strokeBlock(points.slice(0, maxPoints * 3), color, width)
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
      live: live<InkState, any, {}>((state) => ({
        d: state.draft ? inkPath(state.draft) : "",
        stroke: inkColor(state.color),
        "stroke-width": String(state.width),
      })),
    }),
  }),
})

export {
  fromDocumentStroke,
  ink,
  inkColor,
  inkColors,
  inkPath,
  inkWidths,
  maxPoints,
  strokeBlock,
  strokeId,
  toDocumentStroke,
  tokenWidth,
  type DocumentStroke,
  type InkBlock,
  type InkOptions,
  type InkState,
  type InkStroke,
}
