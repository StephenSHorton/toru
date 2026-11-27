// ink-check exercises the whiteboard's freehand ink (src/whiteboard/ink.ts) in
// happy-dom: the smooth path and a stroke's box; the pen drawing a
// stroke by mouse through UI SDK's drag session; strokes on the board
// selecting, moving and deleting like blocks, but never connecting or
// resizing; and the stroke as the board document keeps it in Go.
//
// It also writes the stroke the pen drew, as the op the window will send Go,
// to internal/board/testdata/ink-op-from-window.json, which a Go test
// (internal/board/ink_window_test.go) applies and saves. With --check it
// fails instead if that file differs from what the pen draws now.
//
//   bun scripts/ink-check.ts           the checks, and write the Go fixture
//   bun scripts/ink-check.ts --check   the checks, and compare the fixture
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { readFileSync, writeFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

GlobalRegistrator.register({ url: "http://127.0.0.1/" });

const { mount } = await import("@ui-sdk/core/testing");
const { createAgent } = await import("@ui-sdk/core/agent");
const { board } = await import("../src/whiteboard/board");
const ink = await import("../src/whiteboard/ink");

type Blocks = import("../src/whiteboard/board").BoardBlocks;
type InkBlock = import("../src/whiteboard/ink").InkBlock;

let pass = 0;
let fail = 0;
function ok(name: string, cond: boolean, detail?: unknown) {
  if (cond) pass++;
  else {
    fail++;
    console.log(`FAIL ${name}${detail === undefined ? "" : `: ${JSON.stringify(detail)}`}`);
  }
}

const rect = (left: number, top: number, width: number, height: number) =>
  ({ left, top, width, height, right: left + width, bottom: top + height, x: left, y: top, toJSON() {} }) as DOMRect;

// ── the path and a stroke's box (pure) ───────────────────
{
  const d = ink.inkPath([0, 0, 0.5, 10, 0, 0.5, 20, 10, 0.5, 30, 10, 0.5]);
  ok("path starts at the first point", d.startsWith("M0,0 "), d);
  ok("path curves through midpoints (no corners)", d.includes("Q10,0 15,5") && d.includes("Q20,10 25,10"), d);
  ok("path ends at the last point", d.endsWith("L30,10"), d);
  ok("one point draws a dot", ink.inkPath([5, 6, 0.5]) === "M5,6 L5,6");
  ok("no points, no path", ink.inkPath([]) === "");

  const s = ink.strokeBlock([100, 50, 0.7, 140, 80, 0.7], "ink-blue", 4);
  ok("box is the line's bounds widened by half the width", s.x === 98 && s.y === 48 && s.width === 44 && s.height === 34, s);
  ok("points are relative to the box", s.ink.points.join() === "2,2,0.7,42,32,0.7", s.ink.points);
  ok("stroke keeps its pen", s.ink.color === "ink-blue" && s.ink.width === 4 && s.label === "Ink stroke", s);

  const doc = ink.toDocumentStroke("ink-1", s);
  ok("document stroke has absolute points", doc.points.join() === "100,50,0.7,140,80,0.7", doc);
  const back = ink.fromDocumentStroke(doc);
  ok("document stroke reads back to the same block", JSON.stringify(back) === JSON.stringify(s), back);

  // Pressure: a stylus stroke is a filled outline that thickens and thins.
  ok("pressure 0.5 draws the pen's own width", ink.pressureScale(0.5) === 1);
  ok("harder is wider, lighter is thinner", ink.pressureScale(1) > 1.5 && ink.pressureScale(0.05) < 0.5);
  const even = [0, 0, 0.5, 10, 0, 0.5, 20, 10, 0.5, 30, 10, 0.5];
  const evenShape = ink.inkShape(even, 4);
  ok("even pressure (a mouse) draws today's stroked line", !evenShape.filled && evenShape.d === ink.inkPath(even), evenShape);
  ok("even pressure keeps the box padding", ink.maxPressureScale(even) === 1);
  const pressed = [0, 0, 0.1, 50, 0, 0.5, 100, 0, 1, 150, 0, 1];
  const pressedShape = ink.inkShape(pressed, 4);
  ok("changing pressure (a stylus) draws a filled outline", pressedShape.filled && pressedShape.d.includes("A") && pressedShape.d.endsWith("Z"), pressedShape.d.slice(0, 80));
  // The outline's round ends are arcs of the line's half width at that point.
  const radii = [...pressedShape.d.matchAll(/A([0-9.]+),/g)].map((m) => Number(m[1]));
  ok("outline is thin where light, wide where pressed", Math.min(...radii) < 1.2 && Math.max(...radii) > 3.2, [Math.min(...radii), Math.max(...radii)]);
  ok("outline's first piece is thinner than its last", radii[0] < radii.at(-1)!, [radii[0], radii.at(-1)]);
  ok("outline is one smooth path of many pieces", (pressedShape.d.match(/M/g) ?? []).length > 3);
  const box = ink.strokeBlock(pressed, "ink-blue", 4);
  ok("a pressure stroke's box fits its widest point", Math.abs(box.y - -2 * ink.pressureScale(1)) < 1e-9 && Math.abs(box.height - 4 * ink.pressureScale(1)) < 1e-9, box);
  ok("a pressure stroke's dot is a circle", ink.inkOutline([5, 5, 1], 4).startsWith("M"), ink.inkOutline([5, 5, 1], 4));

  ok("pen token drawn as its variable", ink.inkColor("ink-red") === "var(--ink-red)");
  ok("other colours drawn as written", ink.inkColor("#ff0000") === "#ff0000");
  ok("five pen colours, all tokens", ink.inkColors.length === 5 && ink.inkColors.every((c) => /^ink-[a-z]+$/.test(c.token)));
  ok("stroke ids fit the document and the board", /^[A-Za-z0-9_-]{1,64}$/.test(ink.strokeId()) && !ink.strokeId().includes(":"));
}

// ── pen widths come from Toru's tokens ────────────────────────────────
{
  const css = readFileSync(fileURLToPath(new URL("../src/index.css", import.meta.url)), "utf8");
  for (const c of ink.inkColors) {
    ok(`index.css defines --${c.token}`, new RegExp(`--${c.token}:`).test(css));
  }
  for (const w of ink.inkWidths) {
    const m = css.match(new RegExp(`--${w.token}:\\s*([0-9.]+);`));
    ok(`index.css defines --${w.token} as ${w.fallback}`, !!m && Number(m[1]) === w.fallback, m?.[1]);
  }
  // The width is read from the page's CSS, not hard-coded.
  document.documentElement.style.setProperty("--ink-width-medium", "5");
  ok("pen width read from its token", ink.tokenWidth("ink-width-medium", 4) === 5);
  document.documentElement.style.removeProperty("--ink-width-medium");
  ok("falls back when the token is missing", ink.tokenWidth("ink-width-nope", 3) === 3);
}

// ── the pen draws through UI SDK's drag session ───────────────────────
let drawn: { id: string; block: InkBlock; source: string } | null = null;
{
  // The board under the pen is at 100,100 on screen and zoomed to 200%.
  const toCanvas = ({ x, y }: { x: number; y: number }) => ({ x: (x - 100) / 2, y: (y - 100) / 2 });
  const strokes: { id: string; block: InkBlock; source: string }[] = [];
  const mountPen = (active: boolean) =>
    mount(
      ink.ink,
      {
        active,
        color: "ink-green",
        width: 4,
        toCanvas,
        onStroke: (id: string, block: InkBlock, details: { source: string }) => strokes.push({ id, block, source: details.source }),
      },
      `<surface></surface><draft></draft>`,
    );
  const off = mountPen(false);
  ok("surface hidden while the pen is off", off.part("surface")!.hasAttribute("hidden"));
  await off.drag(off.part("surface")!, { by: { x: 60, y: 40 } });
  ok("pen off: a drag draws nothing", strokes.length === 0, strokes);
  off.unmount();

  const pen = mountPen(true);
  const surface = pen.part("surface")!;
  surface.getBoundingClientRect = () => rect(100, 100, 800, 600);
  await pen.settle();
  ok("surface shown while the pen is on", !surface.hasAttribute("hidden") && surface.hasAttribute("data-active"));

  // Press, then move in four steps, a frame apart, like a hand drawing.
  const pe = (type: string, x: number, y: number, buttons = 1) =>
    new PointerEvent(type, { pointerId: 7, pointerType: "mouse", clientX: x, clientY: y, button: 0, buttons, bubbles: true, cancelable: true });
  surface.dispatchEvent(pe("pointerdown", 500, 300));
  await pen.settle();
  ok("a press starts a draft", Array.isArray(pen.state.draft) && pen.state.draft.length === 3, pen.state.draft);
  for (const [x, y] of [[520, 310], [540, 330], [560, 360], [580, 400]]) {
    surface.dispatchEvent(pe("pointermove", x, y));
    await pen.settle();
  }
  const draft = pen.part("draft")!;
  ok("the draft path draws while drawing", (draft.getAttribute("d") ?? "").includes("Q"), draft.getAttribute("d"));
  ok("the draft is in the pen's colour and width", draft.getAttribute("stroke") === "var(--ink-green)" && draft.getAttribute("stroke-width") === "4");
  surface.dispatchEvent(pe("pointerup", 580, 400, 0));
  await pen.settle();
  ok("lifting finishes one stroke", strokes.length === 1, strokes.length);
  ok("the draft clears", pen.state.draft === null && (draft.getAttribute("d") ?? "") === "");
  const s = strokes[0]?.block;
  ok("stroke points are in canvas units", !!s && s.x === 200 - 2 && s.y === 100 - 2, s);
  ok("stroke spans the drag", !!s && s.width === 40 + 4 && s.height === 50 + 4, s);
  ok("a mouse stroke has pressure 0.5", !!s && s.ink.points.filter((_, i) => i % 3 === 2).every((p) => p === 0.5), s?.ink.points);
  ok("stroke made by a person", strokes[0]?.source === "person");
  ok("the finished stroke keeps every point drawn (5)", !!s && s.ink.points.length === 15, s?.ink.points);
  drawn = strokes[0] ?? null;

  // A V: the finished stroke is the same line as the draft, corner and all.
  surface.dispatchEvent(pe("pointerdown", 200, 200));
  let lastDraft = "";
  for (const [x, y] of [[210, 220], [220, 240], [230, 260], [240, 240], [250, 220], [260, 200]]) {
    surface.dispatchEvent(pe("pointermove", x, y));
    await pen.settle();
    lastDraft = draft.getAttribute("d") ?? "";
  }
  surface.dispatchEvent(pe("pointerup", 260, 200, 0));
  await pen.settle();
  const v = strokes.at(-1)!.block;
  const vDoc = ink.toDocumentStroke("v", v);
  ok("a V's finished line is its draft line", ink.inkPath(vDoc.points) === lastDraft, [ink.inkPath(vDoc.points), lastDraft]);
  ok("a V keeps its corner point", vDoc.points.join().includes("65,80"), vDoc.points);
  strokes.pop();

  // A tap leaves a dot.
  surface.dispatchEvent(pe("pointerdown", 300, 300));
  surface.dispatchEvent(pe("pointerup", 300, 300, 0));
  await pen.settle();
  ok("a tap leaves a dot", strokes.length === 2 && strokes[1].block.ink.points.length === 3, strokes[1]?.block);

  // A stylus: its draft and its stroke follow the pressure.
  const pp = (type: string, x: number, y: number, pressure: number, buttons = 1) =>
    new PointerEvent(type, { pointerId: 8, pointerType: "pen", clientX: x, clientY: y, button: 0, buttons, pressure, bubbles: true, cancelable: true });
  surface.dispatchEvent(pp("pointerdown", 300, 300, 0.2));
  for (const [x, p] of [[320, 0.4], [340, 0.6], [360, 0.8], [380, 0.9]]) {
    surface.dispatchEvent(pp("pointermove", x, 300, p));
    await pen.settle();
  }
  ok("a stylus draft is a filled outline", draft.hasAttribute("data-filled") && draft.getAttribute("fill") === "var(--ink-green)" && draft.getAttribute("stroke") === "none", draft.outerHTML.slice(0, 160));
  surface.dispatchEvent(pp("pointerup", 380, 300, 0.9, 0));
  await pen.settle();
  const styl = strokes.pop()!.block;
  ok("a stylus stroke keeps its pressures", styl.ink.points.filter((_, i) => i % 3 === 2).join() === "0.2,0.4,0.6,0.8,0.9", styl.ink.points);
  ok("a stylus stroke draws filled", ink.inkShape(styl.ink.points, styl.ink.width).filled);
  ok("the draft goes back to a line after", !draft.hasAttribute("data-filled"));

  // Escape mid-stroke drops it.
  surface.dispatchEvent(pe("pointerdown", 300, 300));
  surface.dispatchEvent(pe("pointermove", 340, 320));
  await pen.settle();
  document.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  surface.dispatchEvent(pe("pointerup", 340, 320, 0));
  await pen.settle();
  ok("Escape drops the stroke being drawn", strokes.length === 2 && pen.state.draft === null, strokes.length);

  // Touch is left to the board, to pan and pinch.
  const touch = new PointerEvent("pointerdown", { pointerId: 9, pointerType: "touch", clientX: 300, clientY: 300, button: 0, bubbles: true, cancelable: true });
  surface.dispatchEvent(touch);
  ok("touch doesn't draw", pen.state.draft === null && !touch.defaultPrevented);

  pen.unmount();
}

// ── strokes on the board: select, move, delete like blocks ────────────
{
  const agent = createAgent();
  const stroke = drawn!.block;
  const blocks: Blocks = {
    a: { label: "Idea", x: 0, y: 0, width: 160, height: 80 },
    b: { label: "Plan", x: 400, y: 0, width: 160, height: 80 },
    [drawn!.id]: stroke,
  };
  const ids = Object.keys(blocks);
  const markup = `<root aria-label="Ink board">
    <world>
      <svg><line value="l1"></line><wire></wire></svg>
      ${ids
        .map((id) =>
          blocks[id].ink
            ? `<block value="${id}">ink</block>`
            : `<block value="${id}">${blocks[id].label}${["top", "right", "bottom", "left"].map((s) => `<port value="${id}:${s}"></port>`).join("")}</block>`,
        )
        .join("")}
    </world>
    <lasso></lasso><instructions>Instructions</instructions>
  </root>`;
  const b = mount(board, { defaultBlocks: blocks, defaultLines: [{ id: "l1", from: "a:right", to: "b:left" }] }, markup);
  b.root.getBoundingClientRect = () => rect(0, 0, 1000, 600);
  const place = () => {
    const { x, y, zoom } = b.state.viewport;
    for (const [id, box] of Object.entries(b.state.blocks)) {
      const el = b.part("block", id);
      if (el) el.getBoundingClientRect = () => rect(x + box.x * zoom, y + box.y * zoom, box.width * zoom, box.height * zoom);
    }
  };
  place();
  await b.settle();
  place();
  const id = drawn!.id;
  const el = b.part("block", id)!;
  ok("a stroke is a block marked as ink", el.hasAttribute("data-ink") && el.getAttribute("aria-description") === "Freehand stroke.", el.outerHTML.slice(0, 200));

  const comp = agent.snapshot().find((c) => c.component === "board");
  ok("agent snapshot lists the stroke with its ink", (comp?.blocks as Blocks | undefined)?.[id]?.ink?.color === "ink-green", comp?.blocks);

  await b.click(el);
  ok("click selects a stroke", b.state.selected.join() === id, b.state.selected);

  const before = { ...b.state.blocks[id] };
  await b.drag(el, { by: { x: 40, y: 24 } });
  const after = b.state.blocks[id];
  ok("drag moves a stroke", after.x !== before.x && after.y !== before.y, [before, after]);
  ok("a moved stroke keeps its ink", JSON.stringify(after.ink) === JSON.stringify(stroke.ink), after.ink);

  el.focus();
  await b.keys("Alt+ArrowRight");
  ok("Alt+arrow moves a stroke", b.state.blocks[id].x === after.x + 8, b.state.blocks[id]);
  await b.keys("Control+ArrowRight");
  ok("Ctrl+arrow doesn't resize a stroke", b.state.blocks[id].width === stroke.width, b.state.blocks[id]);
  ok("resizeBlock refuses a stroke", b.api.resizeBlock(id, 300, 300) === false);
  await b.keys("c");
  ok("C doesn't start a line from a stroke", !b.state.connecting);
  ok("connectBlocks refuses a stroke", b.api.connectBlocks("a", id) === false && b.state.lines.length === 1);

  // Lasso takes strokes along with blocks.
  b.api.select([]);
  await b.settle();
  ok("select([]) clears", b.state.selected.length === 0);
  b.api.select(["a", id]);
  await b.settle();
  ok("a stroke selects with blocks", b.state.selected.length === 2);

  // Delete removes the selection: the block, its line and the stroke.
  el.focus();
  await b.keys("Delete");
  ok("Delete removes the selected stroke", !b.state.blocks[id], Object.keys(b.state.blocks));
  ok("Delete removes the selected block too", !b.state.blocks.a && !!b.state.blocks.b, Object.keys(b.state.blocks));
  ok("and the lines to it", b.state.lines.length === 0, b.state.lines);
  ok("and empties the selection", b.state.selected.length === 0, b.state.selected);

  // Backspace on the board itself, with a stroke selected.
  ok("addBlock puts a stroke back", b.api.addBlock(id, stroke) === true && !!b.state.blocks[id]);
  ok("addBlock refuses an id it has", b.api.addBlock(id, stroke) === false);
  ok("addBlock refuses an id with ':'", b.api.addBlock("x:y", stroke) === false);
  b.api.select([id]);
  await b.settle();
  b.root.focus();
  await b.keys("Backspace");
  ok("Backspace on the board deletes the selection", !b.state.blocks[id], Object.keys(b.state.blocks));
  b.root.focus();
  await b.keys("Delete");
  ok("Delete with nothing selected deletes nothing", !!b.state.blocks.b);
  b.unmount();
}

// ── the stroke as the board document keeps it ─────────────────────────
{
  const stroke = ink.toDocumentStroke("ink-window-sample", drawn!.block);
  const op = { source: "person", ink: [stroke] };
  const json = `${JSON.stringify(op, null, 2)}\n`;
  const path = fileURLToPath(new URL("../../internal/board/testdata/ink-op-from-window.json", import.meta.url));
  ok("document stroke is x, y, pressure triples", stroke.points.length % 3 === 0 && stroke.points.length >= 6, stroke.points);
  ok("document stroke points are absolute canvas units", stroke.points[0] === 200 && stroke.points[1] === 100, stroke.points.slice(0, 3));
  if (process.argv.includes("--check")) {
    let had = "";
    try {
      had = readFileSync(path, "utf8").replace(/\r\n/g, "\n");
    } catch {}
    ok("Go fixture matches what the pen draws (run without --check to rewrite it)", had === json);
  } else {
    writeFileSync(path, json);
    console.log(`wrote ${path}`);
  }
}

console.log(`ink-check: ${pass} passed, ${fail} failed`);
if (fail) process.exit(1);
