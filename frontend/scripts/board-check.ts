// board-check exercises the whiteboard canvas (src/whiteboard/board.ts and
// edge-path.ts) in happy-dom: pan, zoom and fit; selecting, lassoing, moving,
// resizing and connecting blocks by mouse and keyboard; and that UI SDK's
// agent bridge names every block and line.
//
//   bun scripts/board-check.ts          the checks
//   bun scripts/board-check.ts --print  also print the agent snapshot
import { GlobalRegistrator } from "@happy-dom/global-registrator";

GlobalRegistrator.register({ url: "http://127.0.0.1/" });

const { mount } = await import("@ui-sdk/core/testing");
const { createAgent } = await import("@ui-sdk/core/agent");
const { board, nameLines, facing, dotGrid } = await import("../src/whiteboard/board");
const { edgePath } = await import("../src/whiteboard/edge-path");

type Blocks = import("../src/whiteboard/board").BoardBlocks;

let pass = 0;
let fail = 0;
function ok(name: string, cond: boolean, detail?: unknown) {
  if (cond) pass++;
  else {
    fail++;
    console.log(`FAIL ${name}${detail === undefined ? "" : `: ${JSON.stringify(detail)}`}`);
  }
}

// ── edge paths (pure) ─────────────────────────────────────────────────
{
  const s = edgePath({ x: 0, y: 0 }, { x: 100, y: 50 }, { shape: "straight" });
  ok("straight path", s.d === "M0,0 L100,50", s.d);
  ok("straight mid", s.mid.x === 50 && s.mid.y === 25, s.mid);
  const c = edgePath({ x: 0, y: 0, side: "right" }, { x: 200, y: 0, side: "left" });
  ok("curve leaves along its sides", c.d.startsWith("M0,0 C100,0 100,0 200,0"), c.d);
  ok("curve mid", c.mid.x === 100 && c.mid.y === 0, c.mid);
  const st = edgePath({ x: 0, y: 0, side: "right" }, { x: 100, y: 100, side: "left" }, { shape: "step" });
  ok("step path turns", st.d.includes("Q"), st.d);
}

// ── line names and facing sides (pure) ────────────────────────────────
{
  const blocks: Blocks = {
    a: { label: "Idea", x: 0, y: 0, width: 100, height: 50 },
    b: { label: "Plan", x: 300, y: 0, width: 100, height: 50 },
    c: { label: "Risk", x: 0, y: 300, width: 100, height: 50 },
  };
  const lines = [{ id: "a:right->b:left", from: "a:right", to: "b:left" }];
  const named = nameLines(blocks, lines);
  ok("line named by its blocks", named[0].name === "Idea → Plan", named);
  ok("named lines kept when unchanged", nameLines(blocks, named) === named);
  ok("facing right/left", facing(blocks.a, blocks.b).join() === "right,left");
  ok("facing bottom/top", facing(blocks.a, blocks.c).join() === "bottom,top");
  ok("facing left/right", facing(blocks.b, blocks.a).join() === "left,right");
}

// ── the mounted board ─────────────────────────────────────────────────
// happy-dom lays nothing out, so every element measures 0×0 unless a box is
// given here: the board's surface is 1000×600 at the page's top left.
const surfaceBox = { left: 0, top: 0, width: 1000, height: 600 };

const labels = ["Goal", "Users", "Problem", "Idea A", "Idea B", "Idea C", "Risks", "Metrics", "Plan", "Launch", "Retro", "Notes"];
const blocks: Blocks = Object.fromEntries(
  labels.map((label, i) => [`b${i}`, { label, x: (i % 4) * 200, y: Math.floor(i / 4) * 120, width: 160, height: 80 }]),
);

// The agent bridge is made before the board, as an app makes it before it renders.
const agent = createAgent();

const markup = `<root aria-label="Test board">
  <world>
    <svg>${["l1", "l2"].map((id) => `<line value="${id}"></line>`).join("")}<wire></wire></svg>
    ${Object.keys(blocks)
      .map(
        (id) => `<block value="${id}">${blocks[id].label}
          ${["top", "right", "bottom", "left"].map((s) => `<port value="${id}:${s}"></port>`).join("")}
          ${["n", "ne", "e", "se", "s", "sw", "w", "nw"].map((h) => `<handle value="${id}:${h}"></handle>`).join("")}
        </block>`,
      )
      .join("")}
  </world>
  <lasso></lasso>
  <zoom-out>-</zoom-out><zoom></zoom><zoom-in>+</zoom-in><fit>Fit</fit>
  <instructions>Instructions</instructions>
</root>`;

const b = mount(
  board,
  {
    defaultBlocks: blocks,
    defaultLines: [
      { id: "l1", from: "b0:right", to: "b1:left" },
      { id: "l2", from: "b1:bottom", to: "b5:top" },
    ],
  },
  markup,
);

// Give the surface and every block a box, so the viewport, lasso and ports can measure.
const rect = (left: number, top: number, width: number, height: number) =>
  ({ left, top, width, height, right: left + width, bottom: top + height, x: left, y: top, toJSON() {} }) as DOMRect;
b.root.getBoundingClientRect = () => rect(surfaceBox.left, surfaceBox.top, surfaceBox.width, surfaceBox.height);
const placeOnScreen = () => {
  const { x, y, zoom } = b.state.viewport;
  for (const [id, box] of Object.entries(b.state.blocks)) {
    const el = b.part("block", id)!;
    el.getBoundingClientRect = () => rect(x + box.x * zoom, y + box.y * zoom, box.width * zoom, box.height * zoom);
    for (const side of ["top", "right", "bottom", "left"]) {
      const port = b.part("port", `${id}:${side}`)!;
      const px = side === "left" ? box.x : side === "right" ? box.x + box.width : box.x + box.width / 2;
      const py = side === "top" ? box.y : side === "bottom" ? box.y + box.height : box.y + box.height / 2;
      port.getBoundingClientRect = () => rect(x + px * zoom - 6, y + py * zoom - 6, 12, 12);
    }
  }
};
placeOnScreen();
await b.settle();

// Fit to content on mount: 12 blocks span 0..760 × 0..320, fit in 1000×600 with 48 padding.
{
  const v = b.state.viewport;
  ok("fits content on mount (zoom 1, no zooming in past 100%)", v.zoom === 1, v);
  ok("fit centers content", Math.abs(v.x - (500 - 380)) < 0.01 && Math.abs(v.y - (300 - 160)) < 0.01, v);
}

// ── agent bridge names every block and line ───────────────────────────
{
  const snap = agent.snapshot();
  const comp = snap.find((c) => c.component === "board");
  ok("snapshot has the board", !!comp, snap.map((c) => c.component));
  const snapBlocks = comp?.blocks as Blocks | undefined;
  const snapLines = comp?.lines as { id: string; name: string }[] | undefined;
  ok(
    "snapshot names every block (12)",
    !!snapBlocks && labels.every((label, i) => snapBlocks[`b${i}`]?.label === label),
    snapBlocks && Object.keys(snapBlocks).length,
  );
  ok(
    "snapshot names every line",
    snapLines?.length === 2 && snapLines[0].name === "Goal → Users" && snapLines[1].name === "Users → Idea C",
    snapLines,
  );
  ok("board label in snapshot", comp?.label === "Test board", comp?.label);
  for (const action of ["select", "moveBlock", "resizeBlock", "connectBlocks", "disconnect", "fit", "zoomTo"]) {
    ok(`agent action ${action}`, !!comp?.actions.includes(action), comp?.actions);
  }
  if (process.argv.includes("--print")) console.log(JSON.stringify(comp, null, 2));

  // An agent connects two blocks, and the new line is named.
  const res = await agent.act({ steps: [{ ref: "Test board", action: "connectBlocks", args: ["b2", "b6"] }] });
  ok("agent act connectBlocks ok", !res.error, res);
  const line = b.state.lines.find((l) => l.from.startsWith("b2:"));
  ok("agent line between facing sides", line?.from === "b2:bottom" && line?.to === "b6:top", line);
  ok("agent line named", line?.name === "Problem → Risks", line);
  ok("agent change has source agent", b.changes.some((c) => c.key === "lines" && c.source === "agent"), b.changes.at(-1));
  b.api.disconnect(line!.id);
  ok("disconnect removes the line", b.state.lines.length === 2);
}

// ── pan and zoom ──────────────────────────────────────────────────────
{
  const start = { ...b.state.viewport };
  b.root.dispatchEvent(new WheelEvent("wheel", { deltaX: 10, deltaY: 40, bubbles: true, cancelable: true }));
  ok("wheel pans", b.state.viewport.x === start.x - 10 && b.state.viewport.y === start.y - 40, b.state.viewport);

  const z0 = b.state.viewport.zoom;
  // happy-dom's WheelEvent drops ctrlKey, so it's set on the event here.
  const pinch = new WheelEvent("wheel", { deltaY: -50, clientX: 500, clientY: 300, bubbles: true, cancelable: true });
  Object.defineProperty(pinch, "ctrlKey", { value: true });
  b.root.dispatchEvent(pinch);
  ok("Ctrl+wheel (and trackpad pinch) zooms in", b.state.viewport.zoom > z0, b.state.viewport);

  // Keys on the surface itself, with no block focused.
  b.root.focus();
  const z1 = b.state.viewport.zoom;
  await b.keys("-");
  ok("minus key zooms out from the surface", b.state.viewport.zoom < z1, b.state.viewport);
  await b.keys("0");
  ok("0 goes back to 100%", b.state.viewport.zoom === 1, b.state.viewport);
  await b.keys("=");
  ok("plus (=) key zooms in", b.state.viewport.zoom === 1.25, b.state.viewport);
  await b.keys("1");
  ok("1 fits the content", b.state.viewport.zoom === 1 && Math.abs(b.state.viewport.x - 120) < 0.01, b.state.viewport);

  // Two-finger pinch on empty space.
  const vp = { ...b.state.viewport };
  const pe = (type: string, id: number, x: number, y: number) =>
    new PointerEvent(type, { pointerId: id, pointerType: "touch", clientX: x, clientY: y, button: 0, bubbles: true, cancelable: true });
  b.root.dispatchEvent(pe("pointerdown", 1, 400, 300));
  b.root.dispatchEvent(pe("pointerdown", 2, 600, 300));
  b.root.dispatchEvent(pe("pointermove", 2, 700, 300));
  b.root.dispatchEvent(pe("pointerup", 2, 700, 300));
  b.root.dispatchEvent(pe("pointerup", 1, 400, 300));
  ok("pinch zooms", Math.abs(b.state.viewport.zoom - vp.zoom * 1.5) < 1e-6, b.state.viewport);

  // A press on empty space focuses the board, so its keys work at once.
  (document.activeElement as HTMLElement | null)?.blur();
  await b.click(b.root);
  ok("a press on empty space focuses the board", document.activeElement === b.root, document.activeElement?.outerHTML.slice(0, 60));

  // A mouse drag on empty space pans.
  await b.api.viewport.setViewport({ x: 0, y: 0, zoom: 1 });
  await b.drag(b.root, { by: { x: 50, y: 30 } });
  ok("drag on empty space pans", b.state.viewport.x === 50 && b.state.viewport.y === 30, b.state.viewport);

  b.api.fit();
  placeOnScreen();
}

// A mouse press with Shift held, which mount's click can't do.
function shiftPress(el: HTMLElement) {
  const box = el.getBoundingClientRect();
  const init = { pointerId: 7, pointerType: "mouse", clientX: box.left + 4, clientY: box.top + 4, button: 0, shiftKey: true, bubbles: true, cancelable: true };
  el.dispatchEvent(new PointerEvent("pointerdown", { ...init, buttons: 1 }));
  el.dispatchEvent(new PointerEvent("pointerup", { ...init, buttons: 0 }));
  el.dispatchEvent(new MouseEvent("click", init));
}

// ── select by mouse and keyboard ──────────────────────────────────────
{
  await b.click(b.part("block", "b0")!);
  ok("click selects a block", b.state.selected.join() === "b0", b.state.selected);
  shiftPress(b.part("block", "b1")!);
  await b.settle();
  ok("Shift+click adds to the selection", b.state.selected.join() === "b0,b1", b.state.selected);
  await b.keys("Escape");
  ok("Escape clears the selection", b.state.selected.length === 0, b.state.selected);

  b.part("block", "b0")!.focus();
  await b.keys("Space");
  ok("Space selects the focused block", b.state.selected.join() === "b0", b.state.selected);
  await b.keys("ArrowRight");
  ok("arrow moves focus to the nearest block", document.activeElement === b.part("block", "b1"), document.activeElement?.textContent);
  await b.keys("Shift+ArrowRight");
  ok("Shift+arrow extends the selection", b.state.selected.join() === "b0,b1,b2", b.state.selected);
  await b.keys("Escape");
}

// ── lasso ─────────────────────────────────────────────────────────────
{
  // Blocks b0 and b1 sit at screen x 120..280 and 320..480, y 140..220.
  const { x, y } = b.state.viewport;
  const down = new PointerEvent("pointerdown", { pointerId: 9, pointerType: "mouse", clientX: x - 10, clientY: y - 10, button: 0, buttons: 1, shiftKey: true, bubbles: true, cancelable: true });
  b.root.dispatchEvent(down);
  const at = (cx: number, cy: number, type: string) =>
    new PointerEvent(type, { pointerId: 9, pointerType: "mouse", clientX: cx, clientY: cy, button: 0, buttons: type === "pointerup" ? 0 : 1, shiftKey: true, bubbles: true, cancelable: true });
  for (const t of [0.25, 0.5, 1]) b.root.dispatchEvent(at(x - 10 + 390 * t, y - 10 + 60 * t, "pointermove"));
  window.dispatchEvent(at(x + 380, y + 50, "pointerup"));
  b.root.dispatchEvent(at(x + 380, y + 50, "pointerup"));
  await b.settle();
  ok("Shift+drag on empty space lassos blocks", b.state.selected.slice().sort().join() === "b0,b1", b.state.selected);
}

// ── move by mouse and keyboard ────────────────────────────────────────
{
  await b.keys(); // nothing
  const before = { ...b.state.blocks.b0 };
  const before1 = { ...b.state.blocks.b1 };
  await b.drag(b.part("block", "b0")!, { by: { x: 40, y: 16 } });
  ok("drag moves the selected blocks together", b.state.blocks.b0.x === before.x + 40 && b.state.blocks.b1.x === before1.x + 40 && b.state.blocks.b0.y === before.y + 16, [b.state.blocks.b0, b.state.blocks.b1]);
  ok("move keeps size and label", b.state.blocks.b0.width === 160 && b.state.blocks.b0.label === "Goal", b.state.blocks.b0);
  placeOnScreen();

  await b.click(b.part("block", "b3")!);
  b.part("block", "b3")!.focus();
  const was = { ...b.state.blocks.b3 };
  await b.keys("Alt+ArrowDown");
  ok("Alt+arrow moves a block by the grid", b.state.blocks.b3.y === was.y + 8 && b.state.blocks.b3.x === was.x, b.state.blocks.b3);
  await b.keys("Alt+Shift+ArrowRight");
  ok("Alt+Shift+arrow moves it further", b.state.blocks.b3.x === was.x + 64, b.state.blocks.b3);
  placeOnScreen();
}

// ── resize by mouse and keyboard ──────────────────────────────────────
{
  const was = { ...b.state.blocks.b3 };
  await b.keys("Control+ArrowRight");
  ok("Ctrl+arrow widens the block", b.state.blocks.b3.width === was.width + 8 && b.state.blocks.b3.x === was.x, b.state.blocks.b3);
  await b.keys("Control+Shift+ArrowDown");
  ok("Ctrl+Shift+arrow grows it further", b.state.blocks.b3.height === was.height + 64, b.state.blocks.b3);
  placeOnScreen();

  const box = { ...b.state.blocks.b4 };
  const se = b.part("handle", "b4:se")!;
  await b.drag(se, { by: { x: 40, y: 24 } });
  ok("drag a corner handle resizes", b.state.blocks.b4.width === box.width + 40 && b.state.blocks.b4.height === box.height + 24 && b.state.blocks.b4.x === box.x, b.state.blocks.b4);
  const nw = b.part("handle", "b4:nw")!;
  await b.drag(nw, { by: { x: 16, y: 8 } });
  ok("drag the top left handle moves that corner", b.state.blocks.b4.x === box.x + 16 && b.state.blocks.b4.y === box.y + 8 && b.state.blocks.b4.width === box.width + 24, b.state.blocks.b4);
  ok("resize keeps the label", b.state.blocks.b4.label === "Idea B");
  const small = b.state.blocks.b4;
  await b.drag(b.part("handle", "b4:se")!, { by: { x: -1000, y: -1000 } });
  ok("resize stops at the smallest size", b.state.blocks.b4.width === 48 && b.state.blocks.b4.height === 32, [small, b.state.blocks.b4]);
  placeOnScreen();
}

// ── connect by mouse and keyboard ─────────────────────────────────────
{
  const count = b.state.lines.length;
  // Drag from b8's right port onto b9's left port.
  const from = b.part("port", "b8:right")!;
  const to = b.part("port", "b9:left")!.getBoundingClientRect();
  const a = from.getBoundingClientRect();
  await b.drag(from, { by: { x: to.left + 6 - (a.left + 6), y: to.top + 6 - (a.top + 6) } });
  const made = b.state.lines.find((l) => l.from === "b8:right");
  ok("drag from a port to another connects", b.state.lines.length === count + 1 && made?.to === "b9:left", b.state.lines);
  ok("dragged line is named", made?.name === "Plan → Launch", made);

  // Keyboard: C on b10, then arrows to a port, Enter.
  b.part("block", "b10")!.focus();
  await b.keys("c");
  ok("C starts a connection", !!b.state.connecting, b.state.connecting);
  await b.keys("ArrowRight", "Enter");
  const k = b.state.lines.find((l) => l.from === "b10:right");
  ok("arrows and Enter connect from the keyboard", !!k && k.to.startsWith("b11:"), b.state.lines);
  // Connecting the joined pair again from the keyboard disconnects it.
  await b.keys("c", "ArrowRight", "Enter");
  ok("connecting a joined pair again disconnects it", !b.state.lines.some((l) => l.from === "b10:right"), b.state.lines);
  await b.keys("c", "Escape");
  ok("Escape cancels a connection", !b.state.connecting);
  await b.keys("Control+c");
  ok("Ctrl+C doesn't start a connection", !b.state.connecting);
}

// ── click selects, double-click edits (a field in a block) ────────────
{
  const el = b.part("block", "b11")!;
  const field = document.createElement("textarea");
  field.value = "Some notes";
  el.append(field);
  const tick = document.createElement("button");
  tick.setAttribute("role", "checkbox");
  el.append(tick);
  const at = { ...b.state.blocks.b11 };
  b.root.focus();
  await b.click(b.root);
  await b.click(field);
  ok("a click on a block's text field selects the block", b.state.selected.join() === "b11", b.state.selected);
  ok("...and doesn't edit it", b.state.editing === null && !el.hasAttribute("data-editing"), b.state.editing);
  await b.drag(field, { by: { x: 16, y: 8 } });
  ok("a drag from a text field moves the block", b.state.blocks.b11.x === at.x + 16 && b.state.blocks.b11.y === at.y + 8, b.state.blocks.b11);
  placeOnScreen();

  field.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true, button: 0, clientX: 10, clientY: 10 }));
  await b.settle();
  ok("double-click edits the block's text", b.state.editing === "b11" && el.hasAttribute("data-editing") && document.activeElement === field, b.state.editing);
  const still = { ...b.state.blocks.b11 };
  await b.drag(field, { by: { x: 40, y: 40 } });
  ok("while editing, a drag in the text doesn't move the block", b.state.blocks.b11.x === still.x && b.state.blocks.b11.y === still.y, b.state.blocks.b11);
  field.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  await b.settle();
  ok("Escape stops editing, keeps the block selected and focused", b.state.editing === null && b.state.selected.join() === "b11" && document.activeElement === el, [b.state.editing, b.state.selected]);
  await b.keys("Enter");
  ok("Enter on the block edits its text", b.state.editing === "b11" && document.activeElement === field, b.state.editing);
  ok("...with the caret at the end", field.selectionStart === field.value.length, field.selectionStart);
  await b.click(b.root);
  ok("a click outside stops editing and keeps the selection", b.state.editing === null && b.state.selected.join() === "b11", [b.state.editing, b.state.selected]);
  await b.click(b.root);
  ok("the next click outside clears the selection", b.state.selected.length === 0, b.state.selected);

  // A quick control takes one press and never starts a move or an edit.
  const was = { ...b.state.blocks.b11 };
  await b.drag(tick, { by: { x: 30, y: 0 } });
  ok("a drag from a quick control doesn't move the block", b.state.blocks.b11.x === was.x, b.state.blocks.b11);
  tick.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, cancelable: true, button: 0 }));
  await b.settle();
  ok("a double-click on a quick control doesn't edit", b.state.editing === null, b.state.editing);

  // A block with no text field doesn't edit.
  b.part("block", "b10")!.focus();
  await b.keys("Enter");
  ok("Enter on a block with no text does nothing", b.state.editing === null, b.state.editing);
  field.remove();
  tick.remove();
}

b.unmount();

// ── Snap to grid: a board on the dots' grid (dotGrid, 24) ─────────────
{
  ok("the dots are 24 apart", dotGrid === 24, dotGrid);
  const g = mount(
    board,
    {
      grid: dotGrid,
      defaultBlocks: {
        a: { label: "Aligned", x: 0, y: 0, width: 192, height: 96 },
        odd: { label: "Placed by an agent", x: 13, y: 250, width: 150, height: 70 },
      },
    },
    `<root aria-label="Snap board"><world>
      <block value="a">Aligned${["n", "ne", "e", "se", "s", "sw", "w", "nw"].map((h) => `<handle value="a:${h}"></handle>`).join("")}</block>
      <block value="odd">Odd</block>
    </world><lasso></lasso><instructions>i</instructions></root>`,
  );
  g.root.getBoundingClientRect = () => rect(0, 0, 1000, 600);
  await g.api.viewport.setViewport({ x: 0, y: 0, zoom: 1 });
  for (const [id, box] of Object.entries(g.state.blocks)) g.part("block", id)!.getBoundingClientRect = () => rect(box.x, box.y, box.width, box.height);
  await g.settle();

  // Mid-drag, the block already shows on the dots: 37 right and 10 down shows as 48 right (2 dots), 0 down.
  const el = g.part("block", "a")!;
  const pe = (type: string, x: number, y: number) =>
    new PointerEvent(type, { pointerId: 3, pointerType: "mouse", clientX: x, clientY: y, button: 0, buttons: type === "pointerup" ? 0 : 1, bubbles: true, cancelable: true });
  el.dispatchEvent(pe("pointerdown", 20, 20));
  for (const t of [0.3, 0.6, 1]) window.dispatchEvent(pe("pointermove", 20 + 37 * t, 20 + 10 * t));
  el.dispatchEvent(pe("pointermove", 57, 30));
  await g.settle();
  const style = el.getAttribute("style") ?? "";
  ok("while dragging, the block shows on the dots", /translate:\s*48px 0px/.test(style) && g.api.liveBox(g.state, "a")?.x === 48, style);
  window.dispatchEvent(pe("pointerup", 57, 30));
  el.dispatchEvent(pe("pointerup", 57, 30));
  await g.settle();
  ok("the drop lands where it showed", g.state.blocks.a.x === 48 && g.state.blocks.a.y === 0, g.state.blocks.a);

  // A resize lands its edges on the dots, and can't go below the smallest box on the grid.
  for (const [id, box] of Object.entries(g.state.blocks)) g.part("block", id)!.getBoundingClientRect = () => rect(box.x, box.y, box.width, box.height);
  await g.drag(g.part("handle", "a:se")!, { by: { x: 30, y: 17 } });
  ok("a resize lands on the dots", g.state.blocks.a.width === 216 && g.state.blocks.a.height === 120, g.state.blocks.a);
  await g.drag(g.part("handle", "a:se")!, { by: { x: -1000, y: -1000 } });
  ok("the smallest box is on the grid too", g.state.blocks.a.width === 48 && g.state.blocks.a.height === 48, g.state.blocks.a);

  // An agent's exact x/y and size stay exact.
  g.api.moveBlock("odd", 101, 203);
  ok("an agent's moveBlock keeps its exact x/y", g.state.blocks.odd.x === 101 && g.state.blocks.odd.y === 203, g.state.blocks.odd);
  g.api.resizeBlock("odd", 155, 77);
  ok("an agent's resizeBlock keeps its exact size", g.state.blocks.odd.width === 155 && g.state.blocks.odd.height === 77, g.state.blocks.odd);
  g.api.addBlock("new", { label: "Added in code", x: 7, y: 9, width: 50, height: 50 });
  ok("a block added in code keeps its exact x/y", g.state.blocks.new.x === 7 && g.state.blocks.new.y === 9, g.state.blocks.new);
  g.unmount();
}

// ── Snap to grid off: grid 1 places blocks where they're dropped ──────
{
  const f = mount(
    board,
    { grid: 1, defaultBlocks: { a: { label: "Free", x: 0, y: 0, width: 100, height: 60 } } },
    `<root aria-label="Free board"><world><block value="a">Free</block></world><lasso></lasso><instructions>i</instructions></root>`,
  );
  f.root.getBoundingClientRect = () => rect(0, 0, 1000, 600);
  await f.api.viewport.setViewport({ x: 0, y: 0, zoom: 1 });
  f.part("block", "a")!.getBoundingClientRect = () => rect(0, 0, 100, 60);
  await f.drag(f.part("block", "a")!, { by: { x: 37, y: 10 } });
  ok("with snapping off, a drag lands where it's dropped", f.state.blocks.a.x === 37 && f.state.blocks.a.y === 10, f.state.blocks.a);
  f.unmount();
}

console.log(`board-check: ${pass} passed, ${fail} failed`);
if (fail) process.exit(1);
