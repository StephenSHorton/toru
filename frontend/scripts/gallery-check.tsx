// gallery-check is the block gallery's test (src/whiteboard/BlockGallery.tsx).
// It checks, in happy-dom, on the real window (routes/Whiteboard.tsx) over a
// store in the page (links.ts, memoryLink, which applies ops as Go does):
//
//   1. The gallery has a tile for every block catalog type, frames included,
//      each with a preview, and the old Add block button and block-kind
//      dropdown are gone.
//   2. The previews are hidden from UI SDK's agent bridge: board.snapshot
//      lists the tiles ("Add Sticky note") and none of the previews' controls.
//   3. Pressing a tile (a click, or an agent's press) puts that block in the
//      middle of the view as one op, which one Ctrl+Z takes back; pressing it
//      again doesn't stack the new block on the last.
//   4. Dragging a tile onto the board puts the block where it's dropped,
//      centred on the pointer, as one op; a frame lands in the board's frames.
//      Dropping on the panel, or Escape mid-drag, puts nothing.
//   5. The panel collapses to a rail and opens again, and stays as it was
//      when the window opens again (localStorage).
//   6. "Your blocks" (whiteboard#31): no section while the library is empty;
//      a tile per custom type in the library, with its icon and label; a type
//      added while the window is open shows up without reopening it; dragging
//      one on puts a block with the type's defaults, and the same op copies
//      the type onto the board, so one Ctrl+Z takes both off.
//
//   bun scripts/gallery-check.tsx            the checks
//   bun scripts/gallery-check.tsx --verbose  print every check
import { GlobalRegistrator } from "@happy-dom/global-registrator";

GlobalRegistrator.register({ url: "http://127.0.0.1/" });
declare global {
  var __UI_SDK_STRICT__: boolean | undefined;
  var IS_REACT_ACT_ENVIRONMENT: boolean | undefined;
}
globalThis.__UI_SDK_STRICT__ = true;
globalThis.IS_REACT_ACT_ENVIRONMENT = false;
const warn = console.warn;
console.warn = (...args: unknown[]) => {
  if (!String(args[0]).startsWith("agent commands")) warn(...args);
};
// Go isn't here: answer the Wails runtime's calls (window commands) with a refusal.
const realFetch = globalThis.fetch;
globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) =>
  String(input).includes("/wails/") ? new Response("no Go here", { status: 503 }) : realFetch(input, init)) as typeof fetch;

const React = await import("react");
const { createRoot } = await import("react-dom/client");
const { createAgent } = await import("@ui-sdk/react/agent");
const { LiveBoard } = await import("../src/whiteboard/live");
const { memoryLink } = await import("../src/whiteboard/links");
const { dotGrid } = await import("../src/whiteboard/board");
const { blockTypes, checkProps } = await import("../src/whiteboard/catalog");
const { previewProps } = await import("../src/whiteboard/BlockGallery");
const { setLibraryTypes } = await import("../src/whiteboard/custom-types");
const samples = (await import("../../internal/board/testdata/custom-blocks.json")).default as { blocks: Record<string, unknown>[] };
type BlockDef = import("../src/whiteboard/custom-types").BlockDef;
const [ideaDef, bugDef] = samples.blocks.map((b, i) => ({ ...b, version: 1, origin: `blt-gallery${i}` }) as unknown as BlockDef);
const { default: Whiteboard } = await import("../src/routes/Whiteboard");

type BoardDoc = import("../src/whiteboard/document").BoardDoc;
type BoardOp = import("../src/whiteboard/document").BoardOp;
type BoardLink = import("../src/whiteboard/live").BoardLink;

let pass = 0;
let fail = 0;
const verbose = process.argv.includes("--verbose");
function ok(name: string, cond: boolean, detail?: unknown) {
  if (verbose) console.log(`${cond ? "ok  " : "FAIL"} ${name}`);
  if (cond) pass++;
  else {
    fail++;
    if (!verbose) console.log(`FAIL ${name}${detail === undefined ? "" : `: ${JSON.stringify(detail)}`}`);
  }
}
const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));
const settle = () => wait(30);

// The previews' props are real props: Go would take each.
for (const [type, props] of Object.entries(previewProps)) {
  ok(`the ${type} preview's props fit its schema`, checkProps(type, props) === null, checkProps(type, props));
}

const start: BoardDoc = {
  id: "gallery",
  name: "Gallery check",
  rev: 0,
  frames: [],
  blocks: [{ id: "s1", type: "sticky", x: 0, y: 0, w: 200, h: 200, props: { text: "Already here" }, createdBy: "person", updatedBy: "person" }],
  links: [],
  ink: [],
} as unknown as BoardDoc;

const mem = memoryLink(start);
const sent: BoardOp[] = [];
const store: BoardLink = {
  ...mem,
  apply: async (id, op) => {
    sent.push(structuredClone(op));
    return mem.apply(id, op);
  },
};

// The board fills a 1200 × 800 window; the gallery is its usual box on the left.
const rect = (left: number, top: number, width: number, height: number) =>
  ({ left, top, width, height, right: left + width, bottom: top + height, x: left, y: top, toJSON() {} }) as DOMRect;
const realRect = HTMLElement.prototype.getBoundingClientRect;
HTMLElement.prototype.getBoundingClientRect = function (this: HTMLElement) {
  if (this.getAttribute("aria-roledescription") === "whiteboard") return rect(0, 0, 1200, 800);
  if (this.dataset.slot === "block-gallery") return this.hasAttribute("data-open") ? rect(12, 60, 252, 728) : rect(12, 60, 44, 728);
  return realRect.call(this);
};

localStorage.removeItem("toru.whiteboard.gallery.open");
const agent = createAgent();
const live = new LiveBoard(start.id, store, { typingMs: 60, gapMs: 80 });
let host = document.createElement("div");
document.body.append(host);
let root = createRoot(host);
root.render(<Whiteboard live={live} />);
await settle();
await settle();

const gallery = () => host.querySelector<HTMLElement>('[data-slot="block-gallery"]');
const tile = (type: string) => host.querySelector<HTMLElement>(`[data-gallery-type="${type}"]`);
const memAll = () => [...mem.board().blocks, ...mem.board().frames] as { id: string; type?: string; x: number; y: number; w: number; h: number }[];
const key = (target: EventTarget, k: string, mods: { ctrlKey?: boolean } = {}) =>
  target.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...mods }));
const pointer = (target: EventTarget, type: string, x: number, y: number) =>
  target.dispatchEvent(new PointerEvent(type, { bubbles: true, cancelable: true, pointerId: 7, button: 0, buttons: type === "pointerup" ? 0 : 1, clientX: x, clientY: y }));
const toCanvas = (x: number, y: number) => {
  // The board's viewport, read off its world's transform.
  const style = host.querySelector<HTMLElement>('[data-slot="board-world"]')?.style.transform ?? "";
  const m = /translate\(([-\d.]+)px,\s*([-\d.]+)px\)\s*scale\(([-\d.]+)\)/.exec(style);
  const [vx, vy, zoom] = m ? [Number(m[1]), Number(m[2]), Number(m[3])] : [0, 0, 1];
  return { x: (x - vx) / zoom, y: (y - vy) / zoom, known: !!m, style };
};

// 1. Every catalog type, frames too; the old controls are gone.
ok("the gallery is on the board", !!gallery() && gallery()!.hasAttribute("data-open"));
for (const t of blockTypes) {
  const el = tile(t.type);
  ok(`a tile for ${t.label}`, !!el && el.getAttribute("aria-label") === `Add ${t.label}`, el?.outerHTML.slice(0, 120));
  ok(`... with a live preview of a ${t.label}`, !!el?.parentElement?.querySelector(`[data-slot="block-gallery-preview"] [data-type="${t.type}"] [data-slot="board-block-body"] > *`));
}
ok("frames have their own section", [...host.querySelectorAll('[data-slot="block-gallery-section"] h3')].map((h) => h.textContent).join() === "Blocks,Frames");
ok("no Add block button", ![...host.querySelectorAll("button")].some((b) => b.textContent?.includes("Add block")));
ok("no block-kind dropdown", !host.querySelector('[aria-label="Block kind"]'));

// 2. The agent bridge sees the tiles, not the previews.
{
  const snap = JSON.stringify(agent.snapshot());
  ok("agents see the tiles", snap.includes("Add Sticky note") && snap.includes("Add Frame"));
  ok("agents don't see the previews' controls", !["A quick idea", "Which one?", "To do", "Heading", "ideas"].some((s) => snap.includes(s)), snap.slice(0, 400));
}

// 3. Pressing a tile.
{
  const n = sent.length;
  tile("sticky")!.click();
  await settle();
  const op = sent[n];
  const added = op?.blocks?.[0];
  ok("pressing a tile sends one op", sent.length === n + 1 && op.source === "person" && op.blocks?.length === 1 && !op.frames, sent.slice(n));
  ok("... of that type, with its new props", added?.type === "sticky" && (added.props as { text?: string }).text === "" && added.w === 200 && added.h === 200, added);
  const mid = toCanvas((264 + 1200) / 2, 400);
  // Snap to grid is on by default: the corner lands on the dots, so the middle is within half a dot step.
  ok("... in the middle of the view right of the panel", !!added && Math.abs(added.x + added.w / 2 - mid.x) <= dotGrid / 2 && Math.abs(added.y + added.h / 2 - mid.y) <= dotGrid / 2, { added, mid });
  ok("... its corner on the dots (Snap to grid)", !!added && added.x % dotGrid === 0 && added.y % dotGrid === 0, added);
  ok("... and the board has it", memAll().some((b) => b.id === added?.id));
  ok("... selected", host.querySelector(`[data-block="${added?.id}"]`)?.hasAttribute("data-selected") === true);

  tile("sticky")!.click();
  await settle();
  const again = sent[n + 1]?.blocks?.[0];
  ok("pressing it again doesn't stack the two", !!again && (again.x !== added?.x || again.y !== added?.y), { added, again });

  key(window, "z", { ctrlKey: true });
  await settle();
  ok("one Ctrl+Z takes the last one back", !memAll().some((b) => b.id === again?.id) && memAll().some((b) => b.id === added?.id));
  key(window, "z", { ctrlKey: true });
  await settle();
  ok("... and one more the first", !memAll().some((b) => b.id === added?.id) && memAll().length === 1, memAll().map((b) => b.id));
}

// An agent presses a tile through board.act.
{
  const n = sent.length;
  await agent.act({ steps: [{ ref: "Add Poll", action: "press" }] });
  await settle();
  ok("an agent can press a tile", sent.length === n + 1 && sent[n].blocks?.[0]?.type === "poll", sent.slice(n));
  key(window, "z", { ctrlKey: true });
  await settle();
}

// 4. Dragging a tile.
{
  const n = sent.length;
  const t = tile("card")!;
  pointer(t, "pointerdown", 80, 140);
  pointer(t, "pointermove", 200, 200);
  await settle();
  ok("a drag shows where the block lands", !!document.querySelector('[data-slot="block-gallery-ghost"]'));
  pointer(t, "pointermove", 700, 300);
  await settle();
  ok("... marked over the board", document.querySelector('[data-slot="block-gallery-ghost"]')?.hasAttribute("data-over-board") === true);
  pointer(t, "pointerup", 700, 300);
  t.click(); // the click a browser sends after the press
  await settle();
  const added = sent[n]?.blocks?.[0];
  const at = toCanvas(700, 300);
  ok("dropping on the board sends one op", sent.length === n + 1 && added?.type === "card", sent.slice(n));
  ok("... with the block's middle where it was dropped", !!added && Math.abs(added.x + added.w / 2 - at.x) <= dotGrid / 2 && Math.abs(added.y + added.h / 2 - at.y) <= dotGrid / 2, { added, at });
  ok("... and the drag is over", !document.querySelector('[data-slot="block-gallery-ghost"]'));
  key(window, "z", { ctrlKey: true });
  await settle();
  ok("one Ctrl+Z takes it back", memAll().length === 1);
}
{
  const n = sent.length;
  const t = tile("frame")!;
  pointer(t, "pointerdown", 180, 600);
  pointer(t, "pointermove", 600, 500);
  pointer(t, "pointerup", 600, 500);
  t.click();
  await settle();
  const f = sent[n]?.frames?.[0];
  ok("a dragged frame lands in the board's frames", sent.length === n + 1 && !!f && mem.board().frames.some((x) => x.id === f.id) && !sent[n].blocks?.length, sent.slice(n));
  ok("... named Frame", (f as { name?: string } | undefined)?.name === "Frame", f);
  key(window, "z", { ctrlKey: true });
  await settle();
}
{
  const n = sent.length;
  const t = tile("text")!;
  pointer(t, "pointerdown", 80, 140);
  pointer(t, "pointermove", 150, 300);
  pointer(t, "pointerup", 150, 300);
  t.click();
  await settle();
  ok("dropping on the panel puts nothing", sent.length === n, sent.slice(n));

  pointer(t, "pointerdown", 80, 140);
  pointer(t, "pointermove", 700, 300);
  await settle();
  key(window, "Escape");
  await settle();
  ok("Escape drops the drag", !document.querySelector('[data-slot="block-gallery-ghost"]'));
  pointer(t, "pointerup", 700, 300);
  t.click();
  await settle();
  ok("... and puts nothing", sent.length === n, sent.slice(n));
}

// The wheel over the panel scrolls its list and leaves the board's zoom alone.
{
  const world = () => host.querySelector<HTMLElement>('[data-slot="board-world"]')?.style.transform;
  const was = world();
  host.querySelector('[data-slot="block-gallery-list"]')!.dispatchEvent(new WheelEvent("wheel", { bubbles: true, cancelable: true, deltaY: 120, clientX: 100, clientY: 300 }));
  await settle();
  ok("the wheel over the panel doesn't zoom the board", !!was && world() === was, [was, world()]);
}

// A drag that ends with no click doesn't eat the next press.
{
  const n = sent.length;
  const t = tile("rating")!;
  pointer(t, "pointerdown", 80, 140);
  pointer(t, "pointermove", 700, 300);
  pointer(t, "pointerup", 700, 300);
  await settle(); // no click follows
  t.click();
  await settle();
  ok("a drag with no click after it doesn't swallow the next press", sent.length === n + 2, sent.slice(n));
  key(window, "z", { ctrlKey: true });
  key(window, "z", { ctrlKey: true });
  await settle();
}

// 5. Collapse, and its saved state.
{
  host.querySelector<HTMLElement>('[aria-label="Hide blocks"]')!.click();
  await settle();
  ok("Hide blocks collapses the panel to a rail", !gallery()!.hasAttribute("data-open") && !tile("sticky") && !!host.querySelector('[aria-label="Show blocks"]'));
  ok("... and saves it", localStorage.getItem("toru.whiteboard.gallery.open") === "0");
  root.unmount();
  host.remove();
  host = document.createElement("div");
  document.body.append(host);
  root = createRoot(host);
  root.render(<Whiteboard live={live} />);
  await settle();
  await settle();
  ok("the window opens with it collapsed", !!gallery() && !gallery()!.hasAttribute("data-open"));
  host.querySelector<HTMLElement>('[aria-label="Show blocks"]')!.click();
  await settle();
  ok("Show blocks opens it", gallery()!.hasAttribute("data-open") && !!tile("sticky") && localStorage.getItem("toru.whiteboard.gallery.open") === "1");
}

// 6. Your blocks.
{
  const titles = () => [...host.querySelectorAll('[data-slot="block-gallery-section"] h3')].map((h) => h.textContent).join();
  ok("no Your blocks section while the library is empty", titles() === "Blocks,Frames", titles());

  // What block-library.ts does with BlockLibraryService.List's answer.
  setLibraryTypes([ideaDef]);
  await settle();
  ok("the library's blocks get a section", titles() === "Blocks,Frames,Your blocks", titles());
  const idea = tile(ideaDef.type);
  ok("... a tile for the feature idea, by its label", idea?.getAttribute("aria-label") === `Add ${ideaDef.label}` && idea?.textContent === ideaDef.label, idea?.outerHTML.slice(0, 160));
  ok("... showing its icon", !!idea?.parentElement?.querySelector('[data-slot="block-gallery-preview"][data-icon] svg[data-slot="block-gallery-icon"]'));

  // An agent defines another (board.block.define, then blocks:changed and a fresh List).
  setLibraryTypes([ideaDef, bugDef]);
  await settle();
  ok("a block defined while the window is open shows up", !!tile(bugDef.type) && tile(bugDef.type)!.textContent === bugDef.label);
  ok("... in Your blocks", tile(bugDef.type)?.closest('[data-slot="block-gallery-section"]')?.querySelector("h3")?.textContent === "Your blocks");
  ok("agents see the custom tiles", JSON.stringify(agent.snapshot()).includes(`Add ${bugDef.label}`));

  const n = sent.length;
  ok("the board doesn't carry the type yet", !mem.board().types?.some((d) => d.type === ideaDef.type));
  const t = tile(ideaDef.type)!;
  pointer(t, "pointerdown", 80, 500);
  pointer(t, "pointermove", 700, 400);
  pointer(t, "pointerup", 700, 400);
  t.click();
  await settle();
  const op = sent[n];
  const added = op?.blocks?.[0];
  ok("dragging a custom block on sends one op", sent.length === n + 1 && added?.type === ideaDef.type, sent.slice(n));
  ok("... with the type's defaults as props", JSON.stringify(added?.props) === JSON.stringify(ideaDef.defaults), added?.props);
  ok("... at the type's size", added?.w === ideaDef.w && added?.h === ideaDef.h, added);
  ok("... carrying the type", op?.types?.length === 1 && op.types[0].type === ideaDef.type, op?.types);
  ok("the board's file carries the type", mem.board().types?.some((d) => d.type === ideaDef.type) === true && memAll().some((b) => b.id === added?.id));
  ok("... and draws the block", !!host.querySelector(`[data-block="${added?.id}"]`));
  key(window, "z", { ctrlKey: true });
  await settle();
  ok("one Ctrl+Z takes the block off", !memAll().some((b) => b.id === added?.id));
  ok("... and the type", !mem.board().types?.some((d) => d.type === ideaDef.type), mem.board().types);

  setLibraryTypes([]);
  await settle();
  ok("the section goes when the library empties", titles() === "Blocks,Frames" && !tile(ideaDef.type), titles());
}

root.unmount();
console.log(`gallery-check: ${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
