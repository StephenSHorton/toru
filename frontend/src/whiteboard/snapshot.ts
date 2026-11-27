// A still copy of a whole board, for the board's PNG and PDF exports
// (docs/whiteboard.md section 3). The board window shows only what fits the
// screen; a snapshot lays every block, line and stroke out at zoom 1 on a
// page the board's size, however big, with none of the canvas's controls.
//
// - `snapshotBoard` clones the board's world layer into a page-sized box
//   (with what people typed copied into the clone), for printing.
// - `rasterize` draws a snapshot into PNG tiles at a scale (2×), through an
//   SVG <foreignObject> holding the snapshot and the page's stylesheets. It
//   waits on image loads only, never on animation frames, so it works in a
//   window nobody sees (Go's export WebView2, internal/webprint).
import type { BoardBlocks } from "./board"

/** Space around the blocks, in world units. Room for a block's "made by" tag and curved lines. */
const pad = 48

type Bounds = { x: number; y: number; width: number; height: number }

/** The box around every block, frame and stroke, with `pad` around it. An empty board is a small page. */
function boardBounds(blocks: BoardBlocks): Bounds {
  const all = Object.values(blocks)
  if (!all.length) return { x: -pad, y: -pad, width: 2 * pad + 320, height: 2 * pad + 200 }
  const x0 = Math.min(...all.map((b) => b.x))
  const y0 = Math.min(...all.map((b) => b.y))
  const x1 = Math.max(...all.map((b) => b.x + b.width))
  const y1 = Math.max(...all.map((b) => b.y + b.height))
  return { x: Math.floor(x0 - pad), y: Math.floor(y0 - pad), width: Math.ceil(x1 - x0 + 2 * pad), height: Math.ceil(y1 - y0 + 2 * pad) }
}

/** What a snapshot leaves out: the canvas's own controls, and the who-made-it tags. */
const chrome = [
  '[data-slot="board-port"]',
  '[data-slot="board-handle"]',
  '[data-slot="board-unlink"]',
  '[data-slot="board-wire"]',
  '[data-slot="board-block-by"]',
  '[data-slot="ink-draft"]',
  '[data-slot="block-image-replace"]',
  '[data-slot="block-add"]',
  '[data-slot="block-table-add"]',
].join(",")

type Snapshot = { el: HTMLElement; width: number; height: number; remove: () => void }

/**
 * A still copy of the board under `root` (a `[data-slot="board"]`), laid out
 * at zoom 1 in a box the size of `boardBounds(blocks)`, appended to `into`
 * (the body by default). Text people typed, ticks and chosen options are
 * copied into the clone, so it shows what the board shows.
 */
function snapshotBoard(root: HTMLElement, blocks: BoardBlocks, into: HTMLElement = document.body): Snapshot {
  const world = root.querySelector<HTMLElement>('[data-slot="board-world"]')
  if (!world) throw new Error("no board to export")
  const b = boardBounds(blocks)
  const box = document.createElement("div")
  // The root's own attributes (data-slot="board", its class), so the board's
  // styles apply to the copy as they do to the board.
  for (const a of Array.from(root.attributes)) {
    if (a.name !== "style" && a.name !== "id" && !a.name.startsWith("aria-") && a.name !== "tabindex" && a.name !== "role") box.setAttribute(a.name, a.value)
  }
  box.setAttribute("data-board-snapshot", "")
  box.style.cssText = `position:absolute;left:0;top:0;width:${b.width}px;height:${b.height}px;overflow:hidden;background-image:none;cursor:default;`
  const copy = world.cloneNode(true) as HTMLElement
  copyValues(world, copy)
  copy.style.transform = `translate(${-b.x}px, ${-b.y}px)`
  copy.style.transformOrigin = "0 0"
  copy.querySelectorAll(chrome).forEach((el) => el.remove())
  copy.querySelectorAll("[id]").forEach((el) => el.removeAttribute("id"))
  box.appendChild(copy)
  into.appendChild(box)
  return { el: box, width: b.width, height: b.height, remove: () => box.remove() }
}

/** Copies form state the DOM keeps in properties (typed text, ticks) into the clone's attributes. */
function copyValues(from: HTMLElement, to: HTMLElement) {
  const a = from.querySelectorAll<HTMLTextAreaElement | HTMLInputElement | HTMLSelectElement>("textarea, input, select")
  const b = to.querySelectorAll<HTMLTextAreaElement | HTMLInputElement | HTMLSelectElement>("textarea, input, select")
  a.forEach((el, i) => {
    const c = b[i]
    if (!c) return
    if (el instanceof HTMLTextAreaElement) c.textContent = el.value
    else if (el instanceof HTMLInputElement) {
      if (el.type === "checkbox" || el.type === "radio") el.checked ? c.setAttribute("checked", "") : c.removeAttribute("checked")
      else c.setAttribute("value", el.value)
    } else if (el instanceof HTMLSelectElement) {
      c.querySelectorAll("option").forEach((o, j) => (j === el.selectedIndex ? o.setAttribute("selected", "") : o.removeAttribute("selected")))
    }
  })
}

/** Every rule of the page's stylesheets, as text. */
function pageCSS(doc: Document = document): string {
  let out = ""
  for (const sheet of Array.from(doc.styleSheets)) {
    try {
      for (const rule of Array.from(sheet.cssRules)) out += rule.cssText + "\n"
    } catch {
      /* a sheet from elsewhere: not ours */
    }
  }
  return out
}

/** Pictures in the snapshot as data: URLs, so the SVG image can draw them (an SVG image loads nothing). */
async function inlineImages(el: HTMLElement) {
  await Promise.all(
    Array.from(el.querySelectorAll("img")).map(async (img) => {
      const src = img.getAttribute("src") ?? ""
      if (!src || src.startsWith("data:")) return
      try {
        const blob = await (await fetch(src)).blob()
        const url = await new Promise<string>((ok, no) => {
          const r = new FileReader()
          r.onload = () => ok(String(r.result))
          r.onerror = () => no(r.error)
          r.readAsDataURL(blob)
        })
        img.setAttribute("src", url)
      } catch {
        img.removeAttribute("src")
      }
    }),
  )
}

type Tile = { x: number; y: number; width: number; height: number; png: string }

/** The largest tile side in pixels: well inside every Chromium canvas limit. */
const maxTile = 4096

/**
 * Draws a snapshot at `scale` (2 for a 2× PNG) and hands back PNG tiles of
 * at most `maxTile` pixels a side, left to right, top to bottom; `onTile`
 * gets each as base64 PNG with its place in pixels. A board of any size is
 * drawn whole: the tiles together are the picture.
 */
async function rasterize(snap: Snapshot, scale: number, onTile: (tile: Tile) => void | Promise<void>): Promise<{ width: number; height: number; tiles: number }> {
  const { el, width, height } = snap
  await inlineImages(el)
  const style = getComputedStyle(document.body)
  const wrap = document.createElement("div")
  wrap.setAttribute("xmlns", "http://www.w3.org/1999/xhtml")
  wrap.className = document.documentElement.className
  // The whiteboard theme (toru-ui-sdk.css): the SVG's root isn't <html>.
  const theme = document.documentElement.dataset.theme
  if (theme) wrap.dataset.theme = theme
  wrap.style.cssText = `width:${width}px;height:${height}px;font-family:${style.fontFamily};color:${style.color};font-size:${style.fontSize};line-height:${style.lineHeight};`
  const css = document.createElement("style")
  css.textContent = pageCSS()
  const copy = el.cloneNode(true) as HTMLElement
  copy.style.position = "relative"
  wrap.append(css, copy)
  const xhtml = new XMLSerializer().serializeToString(wrap)
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" width="${width}" height="${height}" viewBox="0 0 ${width} ${height}">` +
    `<foreignObject x="0" y="0" width="${width}" height="${height}">${xhtml}</foreignObject></svg>`
  const img = new Image()
  img.width = width
  img.height = height
  // A data: URL, not a blob: one: Chromium lets a canvas read back an SVG
  // image with a foreignObject only from data:.
  const url = "data:image/svg+xml;charset=utf-8," + encodeURIComponent(svg)
  {
    await new Promise<void>((ok, no) => {
      img.onload = () => ok()
      img.onerror = () => no(new Error("the board couldn't be drawn"))
      img.src = url
    })
    const W = Math.round(width * scale)
    const H = Math.round(height * scale)
    let n = 0
    for (let ty = 0; ty < H; ty += maxTile) {
      for (let tx = 0; tx < W; tx += maxTile) {
        const tw = Math.min(maxTile, W - tx)
        const th = Math.min(maxTile, H - ty)
        const canvas = document.createElement("canvas")
        canvas.width = tw
        canvas.height = th
        const ctx = canvas.getContext("2d")
        if (!ctx) throw new Error("no canvas to draw the board on")
        ctx.drawImage(img, tx / scale, ty / scale, tw / scale, th / scale, 0, 0, tw, th)
        const png = canvas.toDataURL("image/png").slice("data:image/png;base64,".length)
        await onTile({ x: tx, y: ty, width: tw, height: th, png })
        n++
      }
    }
    return { width: W, height: H, tiles: n }
  }
}

export { boardBounds, pageCSS, rasterize, snapshotBoard, type Bounds, type Snapshot, type Tile }
