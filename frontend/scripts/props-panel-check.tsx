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
//
//   bun scripts/props-panel-check.tsx            the checks
//   bun scripts/props-panel-check.tsx --verbose  print every check
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
const realFetch = globalThis.fetch;
globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) =>
  String(input).includes("/wails/") ? new Response("no Go here", { status: 503 }) : realFetch(input, init)) as typeof fetch;

const React = await import("react");
const { createRoot } = await import("react-dom/client");
const { LiveBoard } = await import("../src/whiteboard/live");
const { memoryLink } = await import("../src/whiteboard/links");
const { SchemaField } = await import("../src/whiteboard/PropsPanel");
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
  format: "toru-board",
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
};

const mem = memoryLink(start);
const sent: BoardOp[] = [];
const store: BoardLink = {
  ...mem,
  apply: async (id, op) => {
    sent.push(structuredClone(op));
    return mem.apply(id, op);
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

// 6. Ink.
await none();
press(block("ink-1"));
await settle();
ok("a stroke shows its colour and width", $("h2", panel())?.textContent === "Ink stroke" && !!byLabel("Ink colour") && !!byLabel("Ink width"));
n = sent.length;
click(byLabel("Red")!);
await settle();
ok("its colour is one op", sent.length === n + 1 && item("ink-1").color === "ink-red", item("ink-1"));

// 1b. Escape lets go, and the panel hides.
block("ink-1").focus();
key(block("ink-1"), "Escape");
await settle();
ok("nothing selected: the panel hides and is inert", !open() && panel().hasAttribute("inert"));
ok("the window ends in step with the store", JSON.stringify(live.getState().doc) === JSON.stringify(mem.board()));

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

console.log(`props-panel-check: ${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
