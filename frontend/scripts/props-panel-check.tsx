// props-panel-check is the properties panel's test (whiteboard#29;
// src/whiteboard/PropsPanel.tsx). In happy-dom, on the real window
// (routes/Whiteboard.tsx) over a store in the page (links.ts, memoryLink),
// it checks:
//
//   1. The panel is hidden and inert with nothing selected, and shows when
//      a block is pressed; Escape hides it again.
//   2. A block's form comes from its catalog schema: a sticky's text,
//      colour and tags; a checklist's items with their done boxes. A change
//      is one op to the store, as the person, and Ctrl+Z takes it back.
//   3. Position, size, rotation, frame and lock: each one op; a locked
//      block doesn't move by keyboard or delete.
//   4. Several selected: the props they share, and one op that changes all.
//   5. A line picked from a block's list shows its label and style; each
//      change is one op on the link.
//   6. Ink: colour and width.
//   7. A schema the window has never seen (as an AI-built block type would
//      bring) still gets a field for every keyword.
//   8. A panel change made while typing in a block is still waiting, or
//      right after another panel change, builds on it and never puts the
//      old value back. F6 hops between the board and the panel.
//   10. AI-built block types (whiteboard#27): a block of a type the board
//      carries gets a form from that type's schema, and one whose type
//      arrives after the panel drew is drawn again with its form.
//   9. The ops the panel sent go to internal/board/testdata/panel-ops.json,
//      which Go's TestPanelOps applies through the real store.
//
//   bun scripts/props-panel-check.tsx            the checks, and that panel-ops.json is current
//   bun scripts/props-panel-check.tsx --write    also rewrite panel-ops.json
//   bun scripts/props-panel-check.tsx --verbose  print every check
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { readFileSync, writeFileSync } from "node:fs";

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
const realFetch = globalThis.fetch;
globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) =>
  String(input).includes("/wails/") ? new Response("no Go here", { status: 503 }) : realFetch(input, init)) as typeof fetch;

const React = await import("react");
const { createRoot } = await import("react-dom/client");
const { LiveBoard } = await import("../src/whiteboard/live");
const { memoryLink } = await import("../src/whiteboard/links");
const { PropsPanel, SchemaField } = await import("../src/whiteboard/PropsPanel");
const { setLibraryTypes } = await import("../src/whiteboard/custom-types");
// Two types agents built with board.block.define (whiteboard#27).
const custom = (await import("../../internal/board/testdata/custom-blocks.json")).default as { blocks: Record<string, any>[] };
const [ideaDef, bugDef] = custom.blocks.map((b, i) => ({ ...b, version: 1, origin: `blt-panel${i}` }));
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
const settle = () => wait(40);

const meta = { createdBy: "person", updatedBy: "person" };
const start: BoardDoc = {
  format: "toruboard",
  version: 1,
  id: "props",
  name: "Props",
  rev: 0,
  frames: [{ id: "frm-plan", name: "Plan", x: -20, y: 260, w: 480, h: 260, ...meta }],
  blocks: [
    { id: "s1", type: "sticky", x: 0, y: 0, w: 200, h: 200, props: { text: "First idea", tags: ["a"] }, ...meta },
    { id: "s2", type: "sticky", x: 320, y: 0, w: 200, h: 200, props: { text: "Second", color: "pink", tags: ["a", "b"] }, createdBy: "agent:Claude Code", updatedBy: "agent:Claude Code" },
    { id: "c1", type: "card", x: 640, y: 0, w: 240, h: 160, props: { title: "A card", color: "blue" }, ...meta },
    { id: "k1", type: "checklist", x: 0, y: 600, w: 240, h: 200, props: { title: "Todo", items: [{ text: "One" }, { text: "Two", done: true }] }, ...meta },
  ],
  links: [{ id: "lnk-12", from: "s1", to: "s2", fromPort: "right", toPort: "left", label: "leads to", ...meta }],
  ink: [{ id: "ink-1", points: [240, 340, 0.5, 330, 400, 0.7, 420, 360, 0.5], color: "ink-blue", width: 4, ...meta }],
  types: [bugDef as any],
};
start.blocks.push({ id: "u1", type: "bug-report", x: 1400, y: 600, w: 380, h: 480, props: { ...bugDef.defaults, title: "Save fails" }, createdBy: "agent:Claude Code", updatedBy: "agent:Claude Code" });

const mem = memoryLink(start);
const sent: BoardOp[] = [];
// Every op the store applied, undo and redo too: what Go must replay.
const log: BoardOp[] = [];
const store: BoardLink = {
  ...mem,
  apply: async (id, op) => {
    sent.push(structuredClone(op));
    log.push(structuredClone(op));
    return mem.apply(id, op);
  },
  undo: async (id) => {
    const r = await mem.undo(id);
    if (r) log.push({ kind: "undo", source: "person" });
    return r;
  },
  redo: async (id) => {
    const r = await mem.redo(id);
    if (r) log.push({ kind: "redo", source: "person" });
    return r;
  },
};
const live = new LiveBoard(start.id, store, { typingMs: 60, gapMs: 80 });
const host = document.createElement("div");
document.body.append(host);
createRoot(host).render(<Whiteboard live={live} />);
await settle();
await settle();

const $ = <T extends Element = HTMLElement>(sel: string, root: ParentNode = host) => root.querySelector<T>(sel);
const panel = () => $('[data-slot="props-panel"]')!;
const open = () => panel().hasAttribute("data-open");
const block = (id: string) => $(`[data-block="${id}"]`)!;
const doc = () => mem.board();
const item = (id: string) => [...doc().blocks, ...doc().frames, ...doc().ink, ...doc().links].find((x) => x.id === id) as Record<string, any>;
const byLabel = <T extends HTMLElement = HTMLElement>(label: string | RegExp) =>
  [...panel().querySelectorAll<T>("[aria-label]")].find((e) =>
    typeof label === "string" ? e.getAttribute("aria-label") === label : label.test(e.getAttribute("aria-label")!),
  ) ?? null;
const press = (target: Element, mods: { shiftKey?: boolean } = {}) => {
  target.dispatchEvent(new PointerEvent("pointerdown", { bubbles: true, cancelable: true, button: 0, pointerId: 1, ...mods }));
  target.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, cancelable: true, button: 0, pointerId: 1, ...mods }));
};
const click = (target: Element) => (target as HTMLElement).click();
const key = (target: EventTarget, k: string, mods: { ctrlKey?: boolean; shiftKey?: boolean; altKey?: boolean } = {}) =>
  target.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...mods }));
const type = async (el: HTMLInputElement | HTMLTextAreaElement, text: string) => {
  el.focus();
  const proto = el.tagName === "TEXTAREA" ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
  Object.getOwnPropertyDescriptor(proto, "value")!.set!.call(el, text);
  el.dispatchEvent(new Event("input", { bubbles: true }));
  await settle();
};
/** Lets go of the selection, as Escape on a block does. */
const none = async () => {
  const sel = $('[data-slot="board-block"][data-selected]');
  if (sel) {
    sel.focus();
    key(sel, "Escape");
  }
  await settle();
};
const leave = (el: HTMLElement) => {
  el.blur();
  el.dispatchEvent(new FocusEvent("focusout", { bubbles: true }));
};
const undo = async () => {
  key(window, "z", { ctrlKey: true });
  await settle();
};

// 1. Hidden with nothing selected.
ok("hidden with nothing selected", !open() && panel().hasAttribute("inert"));
press(block("s1"));
await settle();
ok("pressing a block shows the panel", open() && !panel().hasAttribute("inert"));
ok("... titled by its type", $("h2", panel())?.textContent === "Sticky note", $("h2", panel())?.textContent);
ok("... with who made it", panel().textContent!.includes("Made by You"));

// 2. The schema form.
const text = byLabel<HTMLTextAreaElement>(/^Text of /)!;
ok("a sticky's text is a field from its schema", !!text && text.value === "First idea", text?.value);
ok("... its colour, swatches from the schema's enum", !!byLabel(/^Colour of /) && panel().querySelectorAll('[data-part="block-swatches"] [role="radio"]').length === 7);
ok("... and its tags, as words", !!byLabel(/^Tags of /) && byLabel(/^Tags of /)!.textContent!.includes("a"));
let n = sent.length;
await type(text, "Edited in the panel");
await settle();
ok("typing waits until the field is left", sent.length === n);
leave(text);
await settle();
ok("leaving the field sends one op", sent.length === n + 1 && sent[n].source === "person", sent.slice(n));
ok("... that sets the block's text", item("s1").props.text === "Edited in the panel" && block("s1").querySelector("textarea")!.value === "Edited in the panel");
ok("... and keeps its other props", JSON.stringify(item("s1").props.tags) === '["a"]', item("s1").props);
await undo();
ok("Ctrl+Z takes the edit back", item("s1").props.text === "First idea", item("s1").props);

n = sent.length;
click(byLabel("Blue")!);
await settle();
ok("a swatch is one op", sent.length === n + 1 && item("s1").props.color === "blue", item("s1").props);
const addTag = byLabel<HTMLInputElement>(/^Add to Tags of /)!;
await type(addTag, "new");
key(addTag, "Enter");
await settle();
ok("adding a tag is one op", sent.length === n + 2 && JSON.stringify(item("s1").props.tags) === '["a","new"]', item("s1").props);
click(byLabel(/^Remove a from Tags of /)!);
await settle();
ok("removing a tag is one op", sent.length === n + 3 && JSON.stringify(item("s1").props.tags) === '["new"]', item("s1").props);

// A checklist: its items, from the schema's array of objects.
await none();
press(block("k1"));
await settle();
ok("a checklist shows its items", panel().querySelectorAll('[data-slot="props-panel-list"] > ol > li').length === 2);
n = sent.length;
const done1 = byLabel(/^Done, Items of .*, 1$/)!;
click(done1);
await settle();
ok("ticking an item's done box is one op", sent.length === n + 1 && item("k1").props.items[0].done === true, item("k1").props);
click(byLabel(/^Add to Items of /)!);
await settle();
ok("Add puts a new item on the list", item("k1").props.items.length === 3, item("k1").props);
click(byLabel(/^Remove 3 from Items of /)!);
await settle();
ok("... and remove takes it off", item("k1").props.items.length === 2, item("k1").props);

// 3. Position, size, rotation, frame, lock.
await none();
press(block("s1"));
await settle();
const num = async (label: string, value: string) => {
  const f = byLabel<HTMLInputElement>(label)!;
  await type(f, value);
  key(f, "Enter");
  await settle();
};
n = sent.length;
await num("X", "40");
ok("X is one op that moves the block", sent.length === n + 1 && item("s1").x === 40, item("s1"));
await num("Width", "260");
ok("Width resizes it", item("s1").w === 260, item("s1"));
await num("Rotation", "10");
ok("Rotation turns it, in the document", item("s1").rotation === 10, item("s1"));
ok("... and on the canvas", /rotate:\s*10deg/.test(block("s1").getAttribute("style") ?? ""), block("s1").getAttribute("style"));
ok("text, numbers and swatches went as one op each", sent.length === n + 3, sent.length - n);
const frameTrigger = byLabel("Frame it sits in")!;
ok("the frame it sits in is a choice", !!frameTrigger && frameTrigger.textContent!.includes("No frame"), frameTrigger?.textContent);

click(byLabel("Locked")!);
await settle();
ok("Locked is one op", item("s1").locked === true && block("s1").hasAttribute("data-locked"), item("s1"));
ok("... a locked block has no resize handles", !block("s1").querySelector('[data-slot="board-handle"]'));
const x = item("s1").x;
block("s1").focus();
key(block("s1"), "ArrowRight", { altKey: true });
await settle();
ok("... and doesn't move by keyboard", item("s1").x === x, item("s1").x);
key(block("s1"), "Delete");
await settle();
ok("... or delete", !!item("s1") && !!block("s1"));
press(block("s1"));
await settle();
click(byLabel("Locked")!);
await settle();
ok("unlocking is one op too", !item("s1").locked && !block("s1").hasAttribute("data-locked"), item("s1"));

// 4. Several selected.
await none();
// Space adds the focused block to the selection.
for (const id of ["s1", "s2", "c1"]) {
  block(id).focus();
  key(block(id), " ");
  await settle();
}
await settle();
ok("several selected: a title that counts them", $("h2", panel())?.textContent === "3 selected: 2 sticky notes, 1 card", $("h2", panel())?.textContent);
ok("... the props they share: colour and tags, not text", !!byLabel("Colour of selection") && !!byLabel("Tags of selection") && !byLabel(/^Text of /));
n = sent.length;
click(byLabel("Green")!);
await settle();
ok("a shared change is one op", sent.length === n + 1 && sent[n].blocks?.length === 3, sent.slice(n));
ok("... that changes them all", ["s1", "s2", "c1"].every((id) => item(id).props.color === "green"), ["s1", "s2", "c1"].map((id) => item(id).props.color));
const addEach = byLabel<HTMLInputElement>("Add to Tags of selection")!;
await type(addEach, "shared");
key(addEach, "Enter");
await settle();
ok("a tag added to several goes to each, keeping their own", JSON.stringify(item("s2").props.tags) === '["a","b","shared"]' && JSON.stringify(item("c1").props.tags) === '["shared"]', [item("s2").props.tags, item("c1").props.tags]);
await undo();
await undo();
ok("Ctrl+Z twice takes back both, for all three", item("s2").props.color === "pink" && item("c1").props.color === "blue" && !item("c1").props.tags, [item("s2").props, item("c1").props]);

// 5. A line, picked from a block's list of lines.
await none();
press(block("s1"));
await settle();
const lineButton = [...panel().querySelectorAll("button")].find((b) => b.textContent!.startsWith("To Sticky note: Second"))!;
ok("a block lists its lines", !!lineButton);
click(lineButton);
await settle();
ok("picking a line shows the line's props", $("h2", panel())?.textContent === "Line" && !!byLabel("Line label"), $("h2", panel())?.textContent);
ok("... and marks it on the canvas", !!$('[data-slot="board-line"][data-selected]'));
ok("... and the canvas lets go of its blocks", !$('[data-slot="board-block"][data-selected]'));
n = sent.length;
const label = byLabel<HTMLTextAreaElement>("Line label")!;
await type(label, "depends on");
key(label, "Enter");
await settle();
ok("the label is one op on the link", sent.length === n + 1 && sent[n].links?.[0]?.id === "lnk-12" && item("lnk-12").label === "depends on", item("lnk-12"));
ok("... and shows on the line", $('[data-slot="board-line-label"]')?.textContent === "depends on");
click(byLabel("Purple")!);
await settle();
ok("its colour is one op", item("lnk-12").props?.color === "purple" && $('[data-slot="board-line"]')?.getAttribute("data-color") === "purple", item("lnk-12"));
ok("the link keeps its ends", item("lnk-12").from === "s1" && item("lnk-12").fromPort === "right");
await undo();
ok("Ctrl+Z takes the colour back", !item("lnk-12").props?.color, item("lnk-12"));
click(byLabel("Locked")!);
await settle();
ok("a locked line has no disconnect button", item("lnk-12").locked === true && !$('[data-slot="board-unlink"]'), item("lnk-12"));
await undo();
ok("... and gets it back when unlocked", !item("lnk-12").locked && !!$('[data-slot="board-unlink"]'));
key(panel(), "Escape");

// A press on a line picks it.
await none();
press(block("s1"));
await settle();
const hit = $('[data-slot="board-line-hit"]');
ok("each line has a press target", !!hit);
hit && press(hit);
await settle();
ok("pressing a line picks it", $("h2", panel())?.textContent === "Line", $("h2", panel())?.textContent);
// A line's end selects that block, on the canvas and in the panel.
const toEnd = [...panel().querySelectorAll("button")].find((b) => b.textContent!.startsWith("To "));
toEnd && click(toEnd);
await settle();
ok(
  "a line's end selects its block",
  block("s2").hasAttribute("data-selected") && !block("s1").hasAttribute("data-selected") && $("h2", panel())?.textContent === "Sticky note",
  $("h2", panel())?.textContent,
);
ok("... and lets go of the line", !$('[data-slot="board-line"][data-selected]'));

// 6. Ink.
await none();
press(block("ink-1"));
await settle();
ok("a stroke shows its colour and width", $("h2", panel())?.textContent === "Ink stroke" && !!byLabel("Ink colour") && !!byLabel("Ink width"));
n = sent.length;
click(byLabel("Red")!);
await settle();
ok("its colour is one op", sent.length === n + 1 && item("ink-1").color === "ink-red", item("ink-1"));

// 8. Changes that land close together.
await none();
press(block("s2"));
await settle();
// Typing in the block itself waits for a pause; a swatch pressed meanwhile keeps it.
const inPlace = block("s2").querySelector("textarea")!;
inPlace.focus();
Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(inPlace, "Typed in place");
inPlace.dispatchEvent(new Event("input", { bubbles: true }));
click(byLabel("Orange")!);
await wait(150);
ok("a panel change while typing in the block waits keeps the typing", item("s2").props.text === "Typed in place" && item("s2").props.color === "orange", item("s2").props);
// Two panel changes before the first is answered: both stay.
click(byLabel("Yellow")!);
const tagNow = byLabel<HTMLInputElement>(/^Add to Tags of /)!;
tagNow.focus();
Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(tagNow, "quick");
tagNow.dispatchEvent(new Event("input", { bubbles: true }));
await wait(0);
key(tagNow, "Enter");
await wait(150);
ok("two quick panel changes both stay", item("s2").props.color === "yellow" && (item("s2").props.tags as string[]).includes("quick"), item("s2").props);

// F6 hops between the board and the panel.
block("s2").focus();
key(block("s2"), "F6");
await settle();
ok("F6 from the board goes into the panel", panel().contains(document.activeElement), document.activeElement?.outerHTML.slice(0, 80));
key(document.activeElement!, "F6");
await settle();
ok("F6 from the panel goes back to the selected block", document.activeElement === block("s2"), document.activeElement?.outerHTML.slice(0, 80));

// Lines can be pressed over a frame: their targets sit above frames, below other blocks.
const hits = $('[data-slot="board-line-hit"]')!.closest("svg")!;
const before = (a: Element, b: Element) => !!(a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING);
ok("line targets are above frames", before(block("frm-plan"), hits));
ok("... and below other blocks", before(hits, block("s1")));

// 1b. Escape lets go, and the panel hides.
block("ink-1").focus();
key(block("ink-1"), "Escape");
await settle();
ok("nothing selected: the panel hides and is inert", !open() && panel().hasAttribute("inert"));
ok("the window ends in step with the store", JSON.stringify(live.getState().doc) === JSON.stringify(mem.board()));

// 10. A block of a type an agent built, which the board carries.
{
  press(block("u1"));
  await settle();
  ok("an AI-built block shows its type's label", $("h2", panel())?.textContent === "Bug report", $("h2", panel())?.textContent);
  const title = byLabel<HTMLInputElement>(/^Title of /);
  ok("... its text from its own schema", title?.value === "Save fails", title?.value);
  ok("... its enum a choice", !!byLabel(/^Severity of /));
  ok("... its list of objects, a list", !!panel().querySelector('[data-slot="props-panel-list"]'));
  const n = sent.length;
  await type(title!, "Save fails on Mac");
  leave(title!);
  await settle();
  ok("an edit to it is one op", sent.length === n + 1 && item("u1").props.title === "Save fails on Mac", item("u1").props);
  ok("... checked against its type's schema", byLabel<HTMLInputElement>(/^Title of /)?.value === "Save fails on Mac");
  await undo();
  ok("... and Ctrl+Z takes it back", item("u1").props.title === "Save fails", item("u1").props.title);
  await none();
}

// 7. A schema the window has never seen.
const made = document.createElement("div");
document.body.append(made);
const madeRoot = createRoot(made);
let got: unknown = null;
madeRoot.render(
  <SchemaField
    name="Settings"
    label="Settings"
    required
    value={{ title: "T", level: 2, on: true, mode: "b", words: ["x"], rows: [{ name: "r" }], extra: { a: 1 } }}
    schema={{
      type: "object",
      properties: {
        title: { type: "string", maxLength: 80 },
        level: { type: "integer", minimum: 1, maximum: 5 },
        on: { type: "boolean" },
        mode: { type: "string", enum: ["a", "b"] },
        words: { type: "array", items: { type: "string" } },
        rows: { type: "array", items: { type: "object", properties: { name: { type: "string" } } } },
        extra: {},
      },
    }}
    onChange={async (next) => {
      got = next(null);
      return true;
    }}
  />,
);
await settle();
const has = (sel: string) => !!made.querySelector(sel);
ok("an unknown type's text is a field", (made.querySelector<HTMLInputElement>('input[aria-label="Title, Settings"]')?.value ?? "") === "T");
ok("... its number a number field", made.querySelector<HTMLInputElement>('input[aria-label="Level, Settings"]')?.type === "number");
ok("... its boolean a checkbox", has('[aria-label="On, Settings"][role="checkbox"]'));
ok("... its enum a choice", has('[aria-label="Mode, Settings"]'));
ok("... its list of text, words", has('[aria-label="Words, Settings"]'));
ok("... its list of objects, a list", has('[data-slot="props-panel-list"]'));
ok("... and what has no form, JSON", has('textarea[aria-label="Extra, Settings"]'));
const lvl = made.querySelector<HTMLInputElement>('input[aria-label="Level, Settings"]')!;
await type(lvl, "9");
key(lvl, "Enter");
await settle();
ok("a number is kept to its schema's maximum", (got as { level?: number })?.level === 5, got);
madeRoot.unmount();

// 10b. A type that arrives after the panel drew (the library loads late).
{
  const lateDoc = structuredClone(mem.board());
  lateDoc.blocks.push({ id: "f1", type: ideaDef.type, x: 0, y: 2000, w: 340, h: 300, props: { ...ideaDef.defaults }, createdBy: "person", updatedBy: "person" } as any);
  const at = document.createElement("div");
  document.body.append(at);
  const root = createRoot(at);
  root.render(<PropsPanel live={live} doc={lateDoc} selected={["f1"]} link={null} onPickLink={() => {}} onSelect={() => {}} />);
  await settle();
  const h2 = () => at.querySelector("h2")?.textContent;
  ok("before its type arrives, the block shows by its type name", h2()?.toLowerCase() === ideaDef.type, h2());
  setLibraryTypes([ideaDef as any]);
  await settle();
  ok("when it arrives, the panel draws again with its label", h2() === ideaDef.label, h2());
  ok("... and a form from its schema", at.querySelectorAll('[data-slot="props-panel-section"] [aria-label]').length > 2);
  setLibraryTypes([]);
  root.unmount();
}

// 9. The panel's ops, for Go.
{
  const panelFile = new URL("../../internal/board/testdata/panel-ops.json", import.meta.url);
  const strip = (v: unknown) =>
    JSON.parse(JSON.stringify(v, (k, x) => (k === "at" || k === "createdAt" || k === "updatedAt" ? undefined : x)));
  const file = {
    note: "Written by frontend/scripts/props-panel-check.tsx --write: ops as the whiteboard window's properties panel sent them, and the board the window's store ended on. Go's TestPanelOps applies them. Don't edit by hand.",
    start: strip(start),
    ops: strip(log),
    end: strip(mem.board()),
  };
  const text = JSON.stringify(file, null, 2) + "\n";
  if (process.argv.includes("--write")) {
    writeFileSync(panelFile, text);
    console.log("wrote", panelFile.pathname);
  } else {
    let have = "";
    try {
      have = readFileSync(panelFile, "utf8");
    } catch {
      /* missing */
    }
    ok("panel-ops.json is current (run with --write)", have === text, have ? undefined : "missing");
  }
}

console.log(`props-panel-check: ${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
