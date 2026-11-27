// The drawing bar's settings (DrawingBar.tsx): which tool is on, and the
// colour, widths, fill and emoji the tools draw with. They're kept in the
// window's localStorage, so the next board opens with the same tool and pen.
import { EMOJI_SET } from "@/editor/emoji-set"
import { inkColors } from "./ink"
import type { ShapeKind } from "./ink"

/** The drawing bar's tools. The shapes are ink.ts's `ShapeKind`s. */
type Tool = "select" | "hand" | "pen" | "highlighter" | "eraser" | ShapeKind

/** What the tools draw with. Each kind of tool keeps its own width. */
type DrawingSettings = {
  tool: Tool
  /** A pen token's name, like `ink-blue`. */
  color: string
  /** Widths in canvas units: the pen's, the highlighter's, the shapes' (and a text label's size), the eraser's. */
  widths: { pen: number; highlighter: number; shape: number; eraser: number }
  /** Whether new rectangles and ellipses are filled with the colour. */
  fill: boolean
  /** The emoji the emoji tool puts down. */
  emoji: string
}

type WidthKind = keyof DrawingSettings["widths"]

const shapeKinds: ShapeKind[] = ["rect", "ellipse", "line", "arrow", "text", "emoji"]
const tools: Tool[] = ["select", "hand", "pen", "highlighter", "eraser", ...shapeKinds]

/** The tool's single-key shortcut, as its tooltip shows it. Like the screenshot editor's where they share a tool. */
const toolKeys: Record<Tool, string> = {
  select: "V",
  hand: "H",
  pen: "P",
  highlighter: "M",
  eraser: "E",
  rect: "R",
  ellipse: "O",
  line: "L",
  arrow: "A",
  text: "T",
  emoji: "J",
}

const toolNames: Record<Tool, string> = {
  select: "Select",
  hand: "Hand",
  pen: "Pen",
  highlighter: "Highlighter",
  eraser: "Eraser",
  rect: "Rectangle",
  ellipse: "Ellipse",
  line: "Line",
  arrow: "Arrow",
  text: "Text",
  emoji: "Emoji",
}

/** Which width a tool uses, or none (select and hand draw nothing). */
function widthKind(tool: Tool): WidthKind | null {
  if (tool === "select" || tool === "hand") return null
  if (tool === "pen" || tool === "highlighter" || tool === "eraser") return tool
  return "shape"
}

/** The width presets for each kind of tool, and the fine control's range. */
const widthPresets: Record<WidthKind, { name: string; value: number }[]> = {
  pen: [
    { name: "Fine", value: 2 },
    { name: "Medium", value: 4 },
    { name: "Bold", value: 8 },
  ],
  highlighter: [
    { name: "Thin", value: 12 },
    { name: "Medium", value: 20 },
    { name: "Wide", value: 32 },
  ],
  shape: [
    { name: "Fine", value: 2 },
    { name: "Medium", value: 4 },
    { name: "Bold", value: 8 },
  ],
  eraser: [
    { name: "Small", value: 8 },
    { name: "Medium", value: 20 },
    { name: "Large", value: 48 },
  ],
}
const widthRange: Record<WidthKind, { min: number; max: number; step: number }> = {
  pen: { min: 1, max: 40, step: 0.5 },
  highlighter: { min: 4, max: 80, step: 1 },
  shape: { min: 1, max: 40, step: 0.5 },
  eraser: { min: 4, max: 120, step: 1 },
}

/** A text label's font size for a shape width: the Fine, Medium and Bold widths give 18, 24 and 36. */
const textSize = (width: number) => 12 + width * 3
/** An emoji's size for a shape width: 36, 48 and 72. */
const emojiSize = (width: number) => 24 + width * 6

const defaults: DrawingSettings = {
  tool: "select",
  color: inkColors[0].token,
  widths: { pen: 2, highlighter: 20, shape: 4, eraser: 20 },
  fill: false,
  emoji: EMOJI_SET[0],
}

const storageKey = "toru.whiteboard.drawing"

const num = (v: unknown, kind: WidthKind) => {
  const r = widthRange[kind]
  return typeof v === "number" && v >= r.min && v <= r.max ? v : defaults.widths[kind]
}

/** The saved settings, or the defaults for whatever is missing or wrong. */
function loadSettings(storage: Pick<Storage, "getItem"> | undefined = globalThis.localStorage): DrawingSettings {
  let saved: Partial<DrawingSettings> = {}
  try {
    saved = JSON.parse(storage?.getItem(storageKey) ?? "{}") ?? {}
  } catch {
    saved = {}
  }
  const w = (saved.widths ?? {}) as Partial<DrawingSettings["widths"]>
  return {
    tool: tools.includes(saved.tool as Tool) ? (saved.tool as Tool) : defaults.tool,
    color: inkColors.some((c) => c.token === saved.color) ? (saved.color as string) : defaults.color,
    widths: { pen: num(w.pen, "pen"), highlighter: num(w.highlighter, "highlighter"), shape: num(w.shape, "shape"), eraser: num(w.eraser, "eraser") },
    fill: saved.fill === true,
    emoji: typeof saved.emoji === "string" && saved.emoji && saved.emoji.length <= 16 ? saved.emoji : defaults.emoji,
  }
}

function saveSettings(settings: DrawingSettings, storage: Pick<Storage, "setItem"> | undefined = globalThis.localStorage) {
  try {
    storage?.setItem(storageKey, JSON.stringify(settings))
  } catch {
    // Storage full or off: the settings last as long as the window.
  }
}

/** The tool a key press picks, if it's a tool's shortcut. */
function toolForKey(key: string): Tool | null {
  const k = key.toUpperCase()
  return tools.find((t) => toolKeys[t] === k) ?? null
}

export {
  defaults as defaultSettings,
  emojiSize,
  loadSettings,
  saveSettings,
  shapeKinds,
  storageKey,
  textSize,
  toolForKey,
  toolKeys,
  toolNames,
  tools,
  widthKind,
  widthPresets,
  widthRange,
  type DrawingSettings,
  type Tool,
  type WidthKind,
}
