// The whiteboard's canvas, as a UI SDK primitive: blocks placed freely on a
// canvas that pans and zooms, selected by click or lasso, moved, resized and
// joined by lines, by mouse, touch and keyboard. Framework-free; Board.tsx
// renders it in React.
//
// Built from UI SDK 0.1.0-beta.18's canvas experiments,
// examples/experiments/react/canvas.tsx and board.tsx (github.com/vercel-labs/ui,
// Apache-2.0, Copyright Vercel, Inc.), copied in as Toru's own code because the
// experiments aren't published and may change between betas. Modified: one
// primitive with both, blocks that resize (createResize) on the same boxes
// createMove moves, a port on every side of a block, lines named for the
// agent bridge, zoom keys on the surface itself, and the agent actions below.
//
// Behaviors: createViewport (pan, wheel and pinch zoom, zoom keys, fit),
// createSelection and createRovingFocus (click, Space, arrows), createLasso
// (Shift and drag on empty space), createMove (drag, Alt with arrows),
// createResize (handles, Ctrl with arrows) and createConnect (drag from a
// port, or C then arrows and Enter).
import {
  createChange,
  createConnect,
  createLasso,
  createMove,
  createResize,
  createRovingFocus,
  createSelection,
  createViewport,
  css,
  definePrimitive,
  live,
  selects,
  spatialMove,
  type ChangeDetails,
  type ConnectDraft,
  type ConnectReason,
  type LassoDraft,
  type MoveDraft,
  type MovePoint,
  type MoveReason,
  type ResizeDraft,
  type ResizeHandle,
  type ResizeReason,
  type Viewport,
} from "@ui-sdk/core"
import { edgePath, type EdgeSide } from "./edge-path"
import type { InkStroke } from "./ink"
import { layout, type LayoutKind, type LayoutOptions } from "./layout"

/**
 * A block on the board: its name, and its box in canvas units. `type` and
 * `props` are its block catalog entry and that entry's props (catalog.ts);
 * the canvas carries them along, and only arrange reads them (props.tags,
 * for cluster). A frame is type "frame". A freehand stroke
 * (ink.ts) is a block with `ink`: it selects, lassos, moves and deletes like
 * any block, but has no ports and doesn't resize.
 */
type BoardBlock = {
  label: string
  x: number
  y: number
  width: number
  height: number
  type?: string
  props?: Record<string, unknown>
  ink?: InkStroke
  /** Who made it and who last changed it: "person" or "agent:<name>" (Go's board.Source). The canvas shows `createdBy` and never reads either. */
  createdBy?: string
  updatedBy?: string
}

/** Every block, by id. Ids don't contain ":". */
type BoardBlocks = Record<string, BoardBlock>

/**
 * A line from one block's port to another's. A port is `<block>:<side>`, like
 * `idea:right`. `name` says what it joins, like "Idea → Plan", for the agent
 * bridge and screen readers; the board fills it in and keeps it current. (It
 * isn't a label a person writes on the line; that's for the block catalog.)
 */
type BoardLine = { id: string; from: string; to: string; name?: string }

type BoardBlocksChangeReason =
  | MoveReason
  | ResizeReason
  | "imperative"
  | "delete-key"
type BoardLinesChangeReason = ConnectReason | "imperative" | "delete-key"

type BoardOptions = {
  blocks?: BoardBlocks
  defaultBlocks?: BoardBlocks
  onBlocksChange?: (
    blocks: BoardBlocks,
    details: ChangeDetails<BoardBlocksChangeReason>
  ) => void
  lines?: BoardLine[]
  defaultLines?: BoardLine[]
  onLinesChange?: (
    lines: BoardLine[],
    details: ChangeDetails<BoardLinesChangeReason>
  ) => void
  selected?: string[]
  defaultSelected?: string[]
  onSelectedChange?: (selected: string[], details: ChangeDetails) => void
  /** The grid blocks land on and arrows move by, in canvas units. Defaults to 8. */
  grid?: number
}

type BoardState = {
  blocks: BoardBlocks
  lines: BoardLine[]
  selected: string[]
  focused: string | null
  moving: MoveDraft | null
  resizing: ResizeDraft | null
  lasso: LassoDraft | null
  viewport: Viewport
  connecting: ConnectDraft | null
  grid: number
}

type BoardRootState = {
  blocks: BoardBlocks
  lines: BoardLine[]
  selected: number
}
type BoardBlockState = { selected: boolean }

const sides: EdgeSide[] = ["top", "right", "bottom", "left"]
const handles: ResizeHandle[] = ["n", "ne", "e", "se", "s", "sw", "w", "nw"]
const sideNames: Record<EdgeSide, string> = {
  top: "top",
  right: "right",
  bottom: "bottom",
  left: "left",
}
const handleNames: Record<string, string> = {
  n: "top",
  ne: "top right",
  e: "right",
  se: "bottom right",
  s: "bottom",
  sw: "bottom left",
  w: "left",
  nw: "top left",
}

/**
 * Controls inside a block: pressing one uses it, and selects the block
 * without starting a move.
 */
const blockControls =
  'input, textarea, select, button, a[href], label, [contenteditable], [role="checkbox"], [role="radio"], [role="slider"], [role="textbox"], [data-slot="rating"], [data-slot="file-input-dropzone"]'

/** The block a port or handle key belongs to: everything before its last ":". */
const blockOf = (key: string) => key.slice(0, key.lastIndexOf(":"))
/** What follows a port or handle key's last ":", its side or handle. */
const endOf = (key: string) => key.slice(key.lastIndexOf(":") + 1)

/** Names each line by its blocks, keeping the same array when nothing changed. */
function nameLines(blocks: BoardBlocks, lines: BoardLine[]) {
  let changed = false
  const named = lines.map((line) => {
    const name = `${blocks[blockOf(line.from)]?.label ?? blockOf(line.from)} → ${blocks[blockOf(line.to)]?.label ?? blockOf(line.to)}`
    if (line.name === name) {
      return line
    }
    changed = true
    return { ...line, name }
  })
  return changed ? named : lines
}

/** The box a block shows now: a resize's draft box, moved by a move's draft. */
function liveBox(state: BoardState, key: string) {
  const resizing = state.resizing?.key === key ? state.resizing.box : null
  const box = resizing ?? state.blocks[key]
  if (!box) {
    return null
  }
  const drag = state.moving?.keys.includes(key) ? state.moving : null
  return {
    x: box.x + (drag?.dx ?? 0),
    y: box.y + (drag?.dy ?? 0),
    width: box.width,
    height: box.height,
  }
}

/** Where a port sits on the canvas: the middle of its block's side. */
function portPoint(state: BoardState, port: string) {
  const box = liveBox(state, blockOf(port)) ?? { x: 0, y: 0, width: 0, height: 0 }
  const side = endOf(port) as EdgeSide
  return {
    x:
      side === "left"
        ? box.x
        : side === "right"
          ? box.x + box.width
          : box.x + box.width / 2,
    y:
      side === "top"
        ? box.y
        : side === "bottom"
          ? box.y + box.height
          : box.y + box.height / 2,
    side,
  }
}

/** The facing sides for a line from block a to block b, by where their middles are. */
function facing(a: BoardBlock, b: BoardBlock): [EdgeSide, EdgeSide] {
  const dx = b.x + b.width / 2 - (a.x + a.width / 2)
  const dy = b.y + b.height / 2 - (a.y + a.height / 2)
  if (Math.abs(dx) >= Math.abs(dy)) {
    return dx >= 0 ? ["right", "left"] : ["left", "right"]
  }
  return dy >= 0 ? ["bottom", "top"] : ["top", "bottom"]
}

/** The box around every block, or null for an empty board. */
function contentRect(blocks: BoardBlocks) {
  const all = Object.values(blocks)
  if (!all.length) {
    return null
  }
  return {
    left: Math.min(...all.map((b) => b.x)),
    top: Math.min(...all.map((b) => b.y)),
    right: Math.max(...all.map((b) => b.x + b.width)),
    bottom: Math.max(...all.map((b) => b.y + b.height)),
  }
}

/** Board units between the background's dots, which sit at multiples of it from (0, 0). */
const dotSpacing = 24

/**
 * The dot grid's background size and position for a viewport, so each dot
 * stays on the same board point as the canvas pans and zooms. board.css draws
 * one dot in the middle of each tile, so tiles start half a spacing early.
 */
function dotGrid({ x, y, zoom }: Viewport) {
  const size = dotSpacing * zoom
  return {
    "background-size": `${size}px ${size}px`,
    "background-position": `${x - size / 2}px ${y - size / 2}px`,
  }
}

/** The world's style from UI SDK's viewport, without its --canvas-zoom. */
function worldStyle(style: string) {
  return style
    .split(";")
    .filter((d) => !d.trim().startsWith("--canvas-zoom"))
    .join(";")
}

const board = /* @__PURE__ */ definePrimitive({
  name: "board",
  options: {
    blocks: "value",
    defaultBlocks: "value",
    onBlocksChange: "callback",
    lines: "value",
    defaultLines: "value",
    onLinesChange: "callback",
    selected: "list",
    defaultSelected: "list",
    onSelectedChange: "callback",
    grid: "number",
  },
  state: (options: BoardOptions): BoardState => {
    const blocks = options.blocks ?? options.defaultBlocks ?? {}
    return {
      blocks,
      lines: nameLines(blocks, options.lines ?? options.defaultLines ?? []),
      selected: options.selected ?? options.defaultSelected ?? [],
      focused: null,
      moving: null,
      resizing: null,
      lasso: null,
      viewport: { x: 0, y: 0, zoom: 1 },
      connecting: null,
      grid: options.grid ?? 8,
    }
  },
  sync: (options) => ({
    grid: options.grid ?? 8,
    ...(options.blocks === undefined ? {} : { blocks: options.blocks }),
    ...(options.lines === undefined ? {} : { lines: options.lines }),
    ...(options.selected === undefined ? {} : { selected: options.selected }),
  }),
  // The agent bridge's snapshot shows these whole: every block by id with its
  // label and box, every line with its name, and the selection.
  controlled: ["blocks", "lines", "selected"],
  live: ["moving", "resizing", "lasso", "viewport", "connecting"],
  agent: {
    actions: [
      "select",
      "moveBlock",
      "resizeBlock",
      "connectBlocks",
      "disconnect",
      "fit",
      "zoomTo",
      "arrange",
    ],
  },
  api: (context) => {
    const { store } = context
    const change = createChange(context)
    const block = (key: string) => store.get().blocks[key]
    const grid = () => store.get().grid
    /** Whether a block is a freehand stroke, which has no ports and doesn't resize. */
    const isInk = (key: string) => !!block(key)?.ink

    // Lines keep their names as blocks are renamed or lines come in unnamed.
    const name = () => {
      const { blocks, lines } = store.get()
      const named = nameLines(blocks, lines)
      if (named !== lines) {
        store.set({ lines: named })
      }
    }
    context.onDestroy(store.subscribe(name))

    // A move or resize hands back boxes that may have lost their labels or
    // sizes; keep what each block had and take the new geometry.
    const setBoxes = (
      next: Record<string, Partial<BoardBlock> & MovePoint>,
      details: { reason: BoardBlocksChangeReason; event?: Event }
    ) => {
      const blocks = store.get().blocks
      const merged: BoardBlocks = {}
      for (const [key, box] of Object.entries(next)) {
        const was = blocks[key]
        if (was) {
          merged[key] = {
            ...was,
            x: box.x,
            y: box.y,
            width: box.width ?? was.width,
            height: box.height ?? was.height,
          }
        }
      }
      return change("blocks", merged, details)
    }

    const viewport = createViewport<BoardState>(context, {
      key: "viewport",
      surface: () => context.element("root"),
      minZoom: () => 0.1,
      maxZoom: () => 4,
    })
    const roving = createRovingFocus<BoardState, { value: string }>(context, {
      slot: "block",
      key: "focused",
      move: spatialMove(),
      preferred: (item) => store.get().selected.includes(item.key),
    })
    const selection = createSelection(context, {
      key: "selected",
      set: (value, details) => change("selected", value, details),
      order: () => roving.items().map((item) => item.key),
      multiple: () => true,
    })
    const move = createMove<BoardState>(context, {
      key: "blocks",
      draft: "moving",
      set: (value, details) => setBoxes(value, details),
      group: (key) =>
        store.get().selected.includes(key) ? store.get().selected : [key],
      step: grid,
      largeStep: () => grid() * 8,
      scale: () => viewport.get().zoom,
      label: (key) => block(key)?.label,
    })
    const resize = createResize<BoardState>(context, {
      boxes: (state) => state.blocks,
      draft: "resizing",
      set: (value, details) => setBoxes(value, details),
      min: () => ({ width: 48, height: 32 }),
      step: grid,
      largeStep: () => grid() * 8,
      scale: () => viewport.get().zoom,
      label: (key) => block(key)?.label,
    })
    const lasso = createLasso<BoardState>(context, {
      slot: "block",
      draft: "lasso",
      selected: () => store.get().selected,
      set: (keys, details) => change("selected", keys, details),
      message: (count) =>
        `${count} ${count === 1 ? "block" : "blocks"} selected.`,
    })

    const between = (a: string, b: string) =>
      store
        .get()
        .lines.find(
          (line) =>
            (blockOf(line.from) === a && blockOf(line.to) === b) ||
            (blockOf(line.from) === b && blockOf(line.to) === a)
        )
    const addLine = (
      from: string,
      to: string,
      details: { reason: BoardLinesChangeReason; event?: Event }
    ) => {
      const { blocks, lines } = store.get()
      const line = { id: `${from}->${to}`, from, to }
      return change("lines", nameLines(blocks, [...lines, line]), details)
    }
    const removeLine = (
      id: string,
      details: { reason: BoardLinesChangeReason; event?: Event } = {
        reason: "imperative",
      }
    ) => {
      const lines = store.get().lines
      return (
        lines.some((line) => line.id === id) &&
        change(
          "lines",
          lines.filter((line) => line.id !== id),
          details
        )
      )
    }
    const connect = createConnect<BoardState>(context, {
      slot: "port",
      draft: "connecting",
      canConnect: (from, to) =>
        blockOf(from) !== blockOf(to) &&
        !isInk(blockOf(from)) &&
        !isInk(blockOf(to)),
      point: viewport.toCanvas,
      label: (port) =>
        `${block(blockOf(port))?.label ?? blockOf(port)} ${sideNames[endOf(port) as EdgeSide] ?? ""} side`,
      connect: (from, to, details) => {
        const existing = between(blockOf(from), blockOf(to))
        if (existing) {
          // From the keyboard, connecting a joined pair again disconnects it,
          // as in UI SDK's canvas experiment; a drag never removes a line.
          return (
            details.reason === "connect-key" &&
            removeLine(existing.id, details)
          )
        }
        return addLine(from, to, details)
      },
    })

    // Deletes blocks and strokes, the lines to them, and them from the selection.
    const deleteBlocks = (
      ids: string[],
      details: { reason: "imperative" | "delete-key"; event?: Event } = {
        reason: "imperative",
      }
    ) => {
      const { blocks, lines, selected } = store.get()
      const gone = new Set(ids.filter((id) => blocks[id]))
      if (!gone.size) {
        return false
      }
      const kept: BoardBlocks = {}
      for (const [id, box] of Object.entries(blocks)) {
        if (!gone.has(id)) {
          kept[id] = box
        }
      }
      const touches = (line: BoardLine) =>
        gone.has(blockOf(line.from)) || gone.has(blockOf(line.to))
      if (lines.some(touches)) {
        change(
          "lines",
          lines.filter((line) => !touches(line)),
          details
        )
      }
      if (selected.some((id) => gone.has(id))) {
        change(
          "selected",
          selected.filter((id) => !gone.has(id)),
          details
        )
      }
      return change("blocks", kept, details)
    }

    // Delete or Backspace deletes the selection, or the focused block when
    // nothing is selected.
    const deleteKeydown = (event: KeyboardEvent, focused?: string) => {
      if (
        (event.key !== "Delete" && event.key !== "Backspace") ||
        event.altKey ||
        event.ctrlKey ||
        event.metaKey
      ) {
        return false
      }
      const { selected } = store.get()
      const ids = selected.length ? selected : focused ? [focused] : []
      if (!ids.length) {
        return false
      }
      event.preventDefault()
      deleteBlocks(ids, { reason: "delete-key", event })
      return true
    }

    const fit = () => {
      const rect = contentRect(store.get().blocks)
      return !!rect && viewport.fit(rect, { padding: 48 })
    }

    // Zoom keys and 1 to fit, from a block or the surface itself.
    const viewKeydown = (event: KeyboardEvent, at?: { x: number; y: number }) => {
      if (viewport.keydown(event, at)) {
        return true
      }
      if (
        event.key === "1" &&
        !event.altKey &&
        !event.ctrlKey &&
        !event.metaKey
      ) {
        event.preventDefault()
        fit()
        return true
      }
      return false
    }

    const resizeKey: Record<string, readonly [number, number]> = {
      ArrowUp: [0, -1],
      ArrowDown: [0, 1],
      ArrowLeft: [-1, 0],
      ArrowRight: [1, 0],
    }

    const api = {
      viewport,
      roving,
      selection,
      move,
      resize,
      lasso,
      connect,
      handles,
      sides,
      liveBox,
      portPoint,
      fit,
      removeLine,
      deleteBlocks,

      /** Puts a block on the board, like a finished ink stroke. Its id mustn't contain ":". */
      addBlock(id: string, box: BoardBlock) {
        if (!id || id.includes(":") || block(id)) {
          return false
        }
        return change(
          "blocks",
          { ...store.get().blocks, [id]: box },
          { reason: "imperative" }
        )
      },

      /** Every block on the board now, by id. */
      blocks() {
        return store.get().blocks
      },

      /**
       * Puts many blocks and lines on the board in one change each, like a
       * template. False, and nothing added, if an id is taken or has ":".
       */
      addBlocks(add: BoardBlocks, addLines: BoardLine[] = []) {
        const { blocks, lines } = store.get()
        const ids = Object.keys(add)
        if (!ids.length || ids.some((id) => !id || id.includes(":") || blocks[id])) {
          return false
        }
        const all = { ...blocks, ...add }
        const known = new Set(lines.map((l) => l.id))
        const fresh = addLines.filter(
          (l) => !known.has(l.id) && all[blockOf(l.from)] && all[blockOf(l.to)]
        )
        change("blocks", all, { reason: "imperative" })
        if (fresh.length) {
          change("lines", nameLines(all, [...lines, ...fresh]), {
            reason: "imperative",
          })
        }
        return true
      },

      // Agent actions (listed in `agent.actions`), in plain arguments.

      /**
       * Arranges blocks as Go's board.layout does: "grid", "tree" (follows
       * the lines) or "cluster" (one column per first tag). ids picks the
       * blocks; without them, the selection when two or more are selected,
       * else every block. Frames and ink strokes stay where they are. Returns
       * how many blocks moved.
       */
      arrange(kind: LayoutKind, ids?: string[], options?: LayoutOptions) {
        if (kind !== "grid" && kind !== "tree" && kind !== "cluster") {
          return 0
        }
        const { blocks, lines, selected } = store.get()
        const pick = ids?.length
          ? ids
          : selected.length > 1
            ? selected
            : Object.keys(blocks)
        const placed = pick
          .filter((id, i) => pick.indexOf(id) === i)
          .filter((id) => blocks[id] && !blocks[id].ink && blocks[id].type !== "frame")
          .map((id) => {
            const b = blocks[id]
            const tags = b.props?.tags
            return {
              id,
              x: b.x,
              y: b.y,
              w: b.width,
              h: b.height,
              tags: Array.isArray(tags) ? tags.filter((t): t is string => typeof t === "string") : undefined,
            }
          })
        const pos = layout(
          placed,
          lines.map((l) => ({ from: blockOf(l.from), to: blockOf(l.to) })),
          kind,
          options
        )
        const next: BoardBlocks = { ...blocks }
        let moved = 0
        for (const [id, p] of Object.entries(pos)) {
          if (next[id].x !== p.x || next[id].y !== p.y) {
            next[id] = { ...next[id], x: p.x, y: p.y }
            moved++
          }
        }
        if (moved) {
          change("blocks", next, { reason: "imperative" })
          // Lines to moved blocks leave from the sides that now face.
          let turned = false
          const relined = lines.map((line) => {
            const a = blockOf(line.from)
            const b = blockOf(line.to)
            if ((!pos[a] && !pos[b]) || !next[a] || !next[b]) return line
            const [fs, ts] = facing(next[a], next[b])
            const from = `${a}:${fs}`
            const to = `${b}:${ts}`
            if (from === line.from && to === line.to) return line
            turned = true
            return { ...line, from, to }
          })
          if (turned) {
            change("lines", relined, { reason: "imperative" })
          }
        }
        return moved
      },

      /** Selects these blocks, by id. */
      select(ids: string[]) {
        return change(
          "selected",
          ids.filter((id) => block(id)),
          { reason: "imperative" }
        )
      },
      /** Moves a block's top left corner to x, y on the canvas. */
      moveBlock(id: string, x: number, y: number) {
        return !!block(id) && move.moveTo(id, { x, y })
      },
      /** Gives a block a new width and height, keeping its top left corner. Strokes don't resize. */
      resizeBlock(id: string, width: number, height: number) {
        const box = block(id)
        return (
          !!box &&
          !box.ink &&
          resize.resizeTo(id, { x: box.x, y: box.y, width, height })
        )
      },
      /** Draws a line from one block to another, between their facing sides. */
      connectBlocks(fromId: string, toId: string) {
        const a = block(fromId)
        const b = block(toId)
        if (!a || !b || a.ink || b.ink || fromId === toId || between(fromId, toId)) {
          return false
        }
        const [fromSide, toSide] = facing(a, b)
        return addLine(`${fromId}:${fromSide}`, `${toId}:${toSide}`, {
          reason: "imperative",
        })
      },
      /** Removes a line, by its id. */
      disconnect(lineId: string) {
        return removeLine(lineId)
      },
      /** Zooms to a scale, like 1 for 100%, about the middle of the board. */
      zoomTo(zoom: number) {
        return viewport.zoomTo(zoom)
      },

      // Event handlers, which the slots call.

      blockKeydown(event: KeyboardEvent, el: HTMLElement, key: string) {
        // Keys from inside a block, like a field in it, are that element's.
        if (event.target !== el) {
          return
        }
        if (connect.keydown(event) || move.keydown(event, key)) {
          return
        }
        if (deleteKeydown(event, key)) {
          return
        }
        const arrow = resizeKey[event.key]
        if (arrow && (event.ctrlKey || event.metaKey) && !event.altKey && !isInk(key)) {
          // Ctrl with an arrow moves the block's bottom right corner.
          event.preventDefault()
          const by = event.shiftKey ? grid() * 8 : grid()
          resize.resizeBy(key, "se", arrow[0] * by, arrow[1] * by, {
            reason: "resize-key",
            event,
          })
          return
        }
        const at = el.getBoundingClientRect()
        if (
          viewKeydown(event, {
            x: at.left + at.width / 2,
            y: at.top + at.height / 2,
          })
        ) {
          return
        }
        const { selected } = store.get()
        if (
          (event.key === "c" || event.key === "C") &&
          !isInk(key) &&
          !event.ctrlKey &&
          !event.metaKey &&
          !event.altKey
        ) {
          event.preventDefault()
          connect.start(`${key}:right`)
        } else if (
          (event.metaKey || event.ctrlKey) &&
          event.key.toLowerCase() === "a"
        ) {
          event.preventDefault()
          selection.toggleAll({ reason: "list-navigation", event })
        } else if (event.key === " ") {
          event.preventDefault()
          selection.toggle(key, { reason: "item-press", event })
        } else if (event.key === "Escape" && selected.length) {
          change("selected", [], { reason: "escape-key", event })
        } else {
          const moved = roving.keydown(event, el)
          if (moved?.shift) {
            const next = store.get().selected
            const add = [moved.from, moved.key].filter(
              (k): k is string => !!k && !next.includes(k)
            )
            if (add.length) {
              change("selected", [...next, ...add], {
                reason: "list-navigation",
                event,
              })
            }
          }
        }
      },
      blockPointerdown(event: PointerEvent, el: HTMLElement, key: string) {
        if (event.button !== 0) {
          return
        }
        event.stopPropagation()
        const target = event.target as Element | null
        const control =
          target && target !== el && el.contains(target)
            ? target.closest(blockControls)
            : null
        if (event.shiftKey || event.metaKey || event.ctrlKey) {
          selection.toggle(key, { reason: "item-press", event })
        } else if (!store.get().selected.includes(key)) {
          selection.choose(key, { reason: "item-press", event })
        }
        if (control && el.contains(control)) {
          return
        }
        move.pointerdown(event, key, el)
      },
      handlePointerdown(event: PointerEvent, el: HTMLElement, key: string) {
        // The block under the handle must not start a move too.
        event.stopPropagation()
        resize.pointerdown(event, blockOf(key), endOf(key) as ResizeHandle, el)
      },
      rootKeydown(event: KeyboardEvent, el: HTMLElement) {
        if (event.target === el && !deleteKeydown(event)) {
          viewKeydown(event)
        }
      },
      rootPointerdown(event: PointerEvent, el: HTMLElement) {
        const target = event.target as HTMLElement
        if (target !== el && !target.hasAttribute("data-board-empty")) {
          return
        }
        // The board takes keys once pressed, so + - 0 1 work without a block
        // focused (the viewport's press prevents the default that would focus it).
        if (document.activeElement !== el) {
          el.focus({ preventScroll: true })
        }
        // Touch always pans and pinches; Shift with a mouse or pen draws a lasso.
        if (event.shiftKey && event.pointerType !== "touch" && event.button === 0) {
          return lasso.pointerdown(event, el)
        }
        if (event.button === 0 && store.get().selected.length) {
          change("selected", [], { reason: "item-press", event })
        }
        viewport.pointerdown(event)
      },
    }
    return api
  },
  slots: (slot) => ({
    root: slot<{}, BoardRootState>({
      state: (state) => ({
        blocks: state.blocks,
        lines: state.lines,
        selected: state.selected.length,
      }),
      attrs: (_state, { id }) => ({
        role: "region",
        "aria-roledescription": "whiteboard",
        "aria-describedby": `${id}-instructions`,
        tabindex: "0",
        style: css({
          position: "relative",
          overflow: "hidden",
          "touch-action": "none",
          "user-select": "none",
        }),
      }),
      // The dot grid (board.css) moves and scales with the canvas: a dot at
      // every `dotSpacing` board units from (0, 0). Background size and
      // position aren't inherited, so changing them restyles the board alone;
      // a custom property here would restyle every element in it each frame.
      live: live<BoardState, any, {}>((state) => ({
        "data-connecting": state.connecting ? "" : undefined,
        "data-lassoing": state.lasso ? "" : undefined,
        style: css(dotGrid(state.viewport)),
      })),
      on: {
        wheel: (event, { api }) => api.viewport.wheel(event),
        pointerdown: (event, { el, api }) =>
          el && api.rootPointerdown(event, el),
        keydown: (event, { el, api }) => el && api.rootKeydown(event, el),
      },
      // Fit to the content once the board first has a size on screen.
      bind: (el, { api }) => {
        let fitted = false
        const fitOnce = () => {
          const { width, height } = el.getBoundingClientRect()
          if (!fitted && width > 0 && height > 0) {
            fitted = true
            api.fit()
          }
        }
        const frame = requestAnimationFrame(fitOnce)
        const watch =
          typeof ResizeObserver === "undefined"
            ? undefined
            : new ResizeObserver(fitOnce)
        watch?.observe(el)
        return () => {
          cancelAnimationFrame(frame)
          watch?.disconnect()
        }
      },
    }),
    world: slot({
      attrs: () => ({
        role: "listbox",
        "aria-label": "Blocks",
        "aria-multiselectable": "true",
        "data-board-empty": "",
        style: css({
          position: "absolute",
          left: 0,
          top: 0,
          width: "1px",
          height: "1px",
        }),
      }),
      // UI SDK's viewport style also sets --canvas-zoom, which nothing here
      // reads; as an inherited custom property, every zoom step would restyle
      // every block under the world. Only the transform is kept.
      live: live<BoardState, any, {}>((state, { api }) => ({
        style: worldStyle(api.viewport.style(state)),
      })),
    }),
    block: slot<{ value: string }, BoardBlockState>({
      props: { value: "string" },
      selects: selects({ focused: ({ key }) => [key, null] }),
      state: (state, { key }) => ({ selected: state.selected.includes(key) }),
      attrs: (state, part) => {
        const selected = state.selected.includes(part.key)
        const isStroke = !!state.blocks[part.key]?.ink
        const links = state.lines
          .filter(
            (line) =>
              blockOf(line.from) === part.key || blockOf(line.to) === part.key
          )
          .map((line) =>
            blockOf(line.from) === part.key
              ? `to ${state.blocks[blockOf(line.to)]?.label ?? blockOf(line.to)}`
              : `from ${state.blocks[blockOf(line.from)]?.label ?? blockOf(line.from)}`
          )
        return {
          role: "option",
          "aria-roledescription": "block",
          "aria-selected": String(selected),
          "aria-description": isStroke
            ? "Freehand stroke."
            : links.length
            ? `Connected ${links.join(", ")}.`
            : "Not connected.",
          tabindex: part.api.roving.tabIndex(state, part),
          "data-selected": selected ? "" : undefined,
          "data-ink": isStroke ? "" : undefined,
          style: part.api.resize.boxStyle(state, part.key),
        }
      },
      live: live<BoardState, any, { value: string }>(
        (state, { key, api }) => ({
          "data-dragging": api.move.dragging(state, key) ? "" : undefined,
          "data-resizing": api.resize.dragging(state, key),
          "data-lassoed": api.lasso.touches(state, key) ? "" : undefined,
          style: [api.move.style(state, key), api.resize.style(state, key)]
            .filter(Boolean)
            .join(";") || undefined,
        })
      ),
      on: {
        keydown: (event, { el, api, key }) =>
          el && api.blockKeydown(event, el, key),
        pointerdown: (event, { el, api, key }) =>
          el && api.blockPointerdown(event, el, key),
        focus: (event, { el, api, store, key }) => {
          const box = api.liveBox(store.get(), key)
          if (box && event.target === el) {
            api.viewport.reveal({
              left: box.x,
              top: box.y,
              right: box.x + box.width,
              bottom: box.y + box.height,
            })
          }
        },
      },
      bind: (el, part) => part.api.roving.bind(el, part),
    }),
    // A resize handle, keyed `<block>:<handle>`, like `idea:se`.
    handle: slot<{ value: string }>({
      props: { value: "string" },
      attrs: (_state, { key, api }) => ({
        "aria-hidden": "true",
        ...api.resize.handleAttrs(endOf(key) as ResizeHandle),
      }),
      on: {
        pointerdown: (event, { el, api, key }) =>
          el && api.handlePointerdown(event, el, key),
      },
    }),
    // A port lines start and end at, keyed `<block>:<side>`, like `idea:right`.
    port: slot<{ value: string }>({
      props: { value: "string" },
      attrs: (_state, { key }) => ({
        "aria-hidden": "true",
        "data-side": endOf(key),
      }),
      live: live<BoardState, any, { value: string }>(
        (state, { api, key }) => ({
          "data-connect": api.connect.role(state, key),
        })
      ),
      on: {
        pointerdown: (event, { el, api, key }) =>
          el && api.connect.pointerdown(event, key, el),
      },
    }),
    line: slot<{ value: string }>({
      props: { value: "string" },
      attrs: () => ({ fill: "none" }),
      live: live<BoardState, any, { value: string }>((state, { key, api }) => {
        const line = state.lines.find((l) => l.id === key)
        return line
          ? {
              d: edgePath(
                api.portPoint(state, line.from),
                api.portPoint(state, line.to)
              ).d,
            }
          : {}
      }),
    }),
    unlink: slot<{ value: string }>({
      props: { value: "string" },
      attrs: (state, { key }) => {
        const line = state.lines.find((l) => l.id === key)
        return {
          type: "button",
          tabindex: "-1",
          "aria-label": line ? `Disconnect ${line.name ?? key}` : undefined,
          style: css({ position: "absolute", translate: "-50% -50%" }),
        }
      },
      live: live<BoardState, any, { value: string }>((state, { key, api }) => {
        const line = state.lines.find((l) => l.id === key)
        if (!line) {
          return {}
        }
        const { mid } = edgePath(
          api.portPoint(state, line.from),
          api.portPoint(state, line.to)
        )
        return { style: css({ left: `${mid.x}px`, top: `${mid.y}px` }) }
      }),
      on: { pointerdown: (event) => event.stopPropagation() },
      press: ({ api, key }) => api.removeLine(key),
    }),
    // The loose line while one is being drawn.
    wire: slot({
      attrs: () => ({ fill: "none", "aria-hidden": "true" }),
      live: live<BoardState, any, {}>((state, { api }) => {
        const draft = state.connecting
        if (!draft) {
          return { d: "" }
        }
        const from = api.portPoint(state, draft.from)
        const opposite: Record<EdgeSide, EdgeSide> = {
          left: "right",
          right: "left",
          top: "bottom",
          bottom: "top",
        }
        const to = {
          x: draft.x,
          y: draft.y,
          side: draft.to
            ? (endOf(draft.to) as EdgeSide)
            : opposite[from.side],
        }
        return {
          d: edgePath(from, to).d,
          "data-snapped": draft.to ? "" : undefined,
        }
      }),
    }),
    lasso: slot({
      attrs: () => ({ "aria-hidden": "true" }),
      live: live<BoardState, any, {}>((state, { api }) => ({
        style: api.lasso.style(state),
      })),
    }),
    "zoom-in": slot({
      attrs: () => ({ type: "button", "aria-label": "Zoom in" }),
      press: ({ api }) => api.viewport.zoomBy(1.25),
    }),
    "zoom-out": slot({
      attrs: () => ({ type: "button", "aria-label": "Zoom out" }),
      press: ({ api }) => api.viewport.zoomBy(0.8),
    }),
    fit: slot({
      attrs: () => ({ type: "button", "aria-label": "Fit to content" }),
      press: ({ api }) => api.fit(),
    }),
    zoom: slot({
      attrs: () => ({ "aria-hidden": "true" }),
      // The store changes on every pointer move of a pan or drag. Setting
      // textContent each time would put a new text node in the board, which
      // makes the browser recheck UI SDK's :has() rules and restyle every
      // block (tens of ms a frame on a full board). So write only a new
      // percentage, into the one text node.
      bind: (el, { store }) => {
        const text = document.createTextNode("")
        el.replaceChildren(text)
        const write = () => {
          const shown = `${Math.round(store.get().viewport.zoom * 100)}%`
          if (text.data !== shown) {
            text.data = shown
          }
        }
        write()
        return store.subscribe(write)
      },
    }),
    instructions: slot({
      attrs: (_state, { id }) => ({ id: `${id}-instructions`, hidden: "" }),
    }),
  }),
})

/** The words the board's instructions part reads to screen readers. */
const boardInstructions =
  "Arrow keys move to the nearest block; Shift with an arrow adds it to the selection, and Space selects. " +
  "Alt with an arrow moves the selection; Ctrl with an arrow resizes the block. Delete removes the selection. " +
  "C connects from the focused block: arrows choose a port, Enter connects, and connecting a joined pair again disconnects it. " +
  "Plus and minus zoom, 0 goes back to 100%, 1 fits the board. Drag empty space to pan, Shift and drag to select an area."

export {
  blockControls,
  blockOf,
  board,
  boardInstructions,
  type BoardBlock,
  type BoardBlocks,
  type BoardBlocksChangeReason,
  type BoardBlockState,
  type BoardLine,
  type BoardLinesChangeReason,
  type BoardOptions,
  type BoardRootState,
  contentRect,
  endOf,
  facing,
  handleNames,
  nameLines,
  sides,
}
