// live-check is the live board's test (docs/whiteboard.md, section 2;
// src/whiteboard/live.ts). It checks, in happy-dom:
//
//   1. Go's ops replay onto the window's copy exactly: the ops in
//      internal/board/testdata/live-ops.json (written by Go) end on Go's
//      board, applied in order, and through the window's receiver when they
//      arrive shuffled, twice over, or with one missing.
//   2. The real window (routes/Whiteboard.tsx) on a store in the page
//      (links.ts, memoryLink, which applies ops as Go does): an agent's
//      change appears live with its name on the block; the person's moves,
//      typing and deletes go out as one op each (and a key renders only the
//      typed block's parts, whiteboard#50); Ctrl+Z and Ctrl+Y step
//      through the person's and the agent's changes alike; board.act's
//      changes are tagged with the agent's name; and with events doubled,
//      delayed, reordered and dropped the window still ends on the store's
//      board, rev for rev.
//   3. The ops the window sent (and the agent's) are written to
//      internal/board/testdata/window-ops.json, which Go's TestWindowOps
//      applies through the real store: Go must accept each one and end on
//      the same board.
//
//   bun scripts/live-check.tsx           the checks, and that window-ops.json is current
//   bun scripts/live-check.tsx --write   also rewrite window-ops.json
//   bun scripts/live-check.tsx --verbose print every check
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { readFileSync, writeFileSync } from "node:fs";

GlobalRegistrator.register({ url: "http://127.0.0.1/" });
declare global {
  var __UI_SDK_STRICT__: boolean | undefined;
  var IS_REACT_ACT_ENVIRONMENT: boolean | undefined;
}
globalThis.__UI_SDK_STRICT__ = true;
globalThis.IS_REACT_ACT_ENVIRONMENT = false;
// Window commands try Go and fail quietly without it; keep the log clean.
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
const { applyOp } = await import("../src/whiteboard/document");
const { LiveBoard } = await import("../src/whiteboard/live");
const { memoryLink } = await import("../src/whiteboard/links");
const { withAgent } = await import("../src/whiteboard/window");
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

/** JSON with sorted keys and no undefined, for comparing boards. */
function canon(v: unknown): string {
  return JSON.stringify(v, (_k, x) =>
    x && typeof x === "object" && !Array.isArray(x) ? Object.fromEntries(Object.keys(x).sort().map((k) => [k, x[k]])) : x,
  );
}
const sameBoard = (a: BoardDoc | null, b: BoardDoc) => !!a && canon(a) === canon(b);
const wait = (ms: number) => new Promise((r) => setTimeout(r, ms));
const settle = () => wait(30);

const goFile = new URL("../../internal/board/testdata/live-ops.json", import.meta.url);
const windowFile = new URL("../../internal/board/testdata/window-ops.json", import.meta.url);
const golden = JSON.parse(readFileSync(goFile, "utf8")) as { start: BoardDoc; ops: BoardOp[]; end: BoardDoc };

// ── 1. Go's ops on the window's copy ──────────────────────────────────
{
  const end = golden.ops.reduce(applyOp, golden.start);
  ok("Go's ops, applied in order, end on Go's board", sameBoard(end, golden.end), { got: end, want: golden.end });
  ok("ops cover deletes, undo, redo, a rename and stacking", ["undo", "redo"].every((k) => golden.ops.some((o) => o.kind === k)) && golden.ops.some((o) => o.name) && golden.ops.some((o) => o.delete?.length));

  // A link that hands out the golden ops as Go's events; load gives a board at any rev.
  const replay = (at: number): BoardLink & { send: (op: BoardOp) => void; loads: number } => {
    let on: ((op: BoardOp) => void) | null = null;
    const link = {
      loads: 0,
      send: (op: BoardOp) => on?.(structuredClone(op)),
      load: async () => {
        link.loads++;
        return structuredClone(link.loads === 1 ? golden.ops.slice(0, at).reduce(applyOp, golden.start) : golden.end);
      },
      apply: async () => {
        throw new Error("not here");
      },
      undo: async () => null,
      redo: async () => null,
      subscribe: (_id: string, f: (op: BoardOp) => void) => {
        on = f;
        return () => (on = null);
      },
    };
    return link;
  };

  // Shuffled and doubled, from a board loaded part way through.
  for (const at of [0, 3, golden.ops.length]) {
    const link = replay(at);
    const live = new LiveBoard(golden.start.id, link, { gapMs: 10_000 });
    await live.start();
    const order = [...golden.ops, ...golden.ops].sort(() => Math.random() - 0.5);
    for (const op of order) link.send(op);
    ok(`shuffled, doubled ops from rev ${at} end on Go's board`, sameBoard(live.getState().doc, golden.end));
    ok(`... without a reload (${at})`, link.loads === 1, link.loads);
    live.stop();
  }

  // Ops that come before the board has loaded wait for it.
  {
    const link = replay(0);
    const live = new LiveBoard(golden.start.id, link, { gapMs: 10_000 });
    const started = live.start();
    for (const op of golden.ops.slice(0, 4)) link.send(op);
    await started;
    for (const op of golden.ops.slice(4)) link.send(op);
    ok("ops before the load are kept and applied once", sameBoard(live.getState().doc, golden.end));
    live.stop();
  }

  // One op never comes: the gap reloads the board.
  {
    const link = replay(0);
    const live = new LiveBoard(golden.start.id, link, { gapMs: 20 });
    await live.start();
    golden.ops.forEach((op, i) => i !== 4 && link.send(op));
    ok("a gap holds the ops after it", live.rev === 4, live.rev);
    await wait(60);
    ok("a gap that doesn't fill reloads", link.loads === 2 && sameBoard(live.getState().doc, golden.end), link.loads);
    live.stop();
  }

  // A shared board's copy replaced whole (joining, or catching up with
  // its owner) sends a reset: the window loads the board again, even at
  // rev 0, which Go leaves out.
  {
    const link = replay(0);
    const live = new LiveBoard(golden.start.id, link, { gapMs: 10_000 });
    await live.start();
    link.send({ kind: "reset", source: "" } as BoardOp);
    await wait(10);
    ok("a reset reloads the board, rev 0 too", link.loads === 2 && sameBoard(live.getState().doc, golden.end), link.loads);
    live.stop();
  }
}

// ── 2. The window on a store in the page ──────────────────────────────
const sticky = (text: string, color = "yellow") => ({ text, color });
const start: BoardDoc = {
  format: "toruboard",
  version: 1,
  id: "brd-window",
  name: "Window check",
  createdAt: "2026-11-27T12:00:00Z",
  updatedAt: "2026-11-27T12:00:00Z",
  rev: 0,
  frames: [{ id: "frm-plan", name: "Plan", x: -20, y: 260, w: 480, h: 260, createdBy: "person", updatedBy: "person" }],
  blocks: [
    { id: "s1", type: "sticky", x: 0, y: 0, w: 200, h: 200, props: sticky("First idea"), createdBy: "person", updatedBy: "person" },
    { id: "s2", type: "sticky", x: 320, y: 0, w: 200, h: 200, props: sticky("Second idea", "pink"), group: "g", createdBy: "person", updatedBy: "person" },
    { id: "s3", type: "sticky", x: 0, y: 300, w: 200, h: 200, props: sticky("In the plan", "green"), frame: "frm-plan", createdBy: "agent:Claude Code", updatedBy: "agent:Claude Code" },
  ],
  links: [{ id: "lnk-12", from: "s1", to: "s2", fromPort: "right", toPort: "left", label: "leads to", createdBy: "person", updatedBy: "person" }],
  ink: [{ id: "ink-1", points: [240, 340, 0.5, 330, 400, 0.7, 420, 360, 0.5], color: "ink-blue", width: 4, frame: "frm-plan", createdBy: "person", updatedBy: "person" }],
};

const mem = memoryLink(start);
// Every op the store applied, in order: what Go must replay.
const log: BoardOp[] = [];
const sent: BoardOp[] = [];
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
/** An agent's board command, as Go's board.add or board.update would apply it. */
const agentDoes = (op: Omit<BoardOp, "source">) => {
  const full = { source: "agent:Claude Code", ...op };
  log.push(structuredClone(full));
  return mem.applyFrom(full);
};

const agent = createAgent();
const live = new LiveBoard(start.id, store, { typingMs: 60, gapMs: 80 });
const host = document.createElement("div");
document.body.append(host);
createRoot(host).render(<Whiteboard live={live} />);
await settle();
await settle();

const el = (id: string) => host.querySelector<HTMLElement>(`[data-block="${id}"]`);
const by = (id: string) => el(id)?.querySelector('[data-slot="board-block-by"]')?.textContent ?? null;
const view = () => live.getState().view;
const memBlock = (id: string) => mem.board().blocks.find((b) => b.id === id);
const key = (target: EventTarget, k: string, mods: { ctrlKey?: boolean; shiftKey?: boolean; altKey?: boolean } = {}) =>
  target.dispatchEvent(new KeyboardEvent("keydown", { key: k, bubbles: true, cancelable: true, ...mods }));
const inSync = () => sameBoard(live.getState().doc, mem.board());

ok("the window shows the board from its store", !!el("s1") && !!el("s2") && !!el("s3") && !!el("frm-plan"), Object.keys(view().blocks));
ok("blocks say who made them: the person", by("s1") === "You", by("s1"));
ok("blocks say who made them: an agent, by name", by("s3") === "Claude Code", by("s3"));
ok("the line shows", view().lines.some((l) => l.id === "lnk-12" && l.from === "s1:right"), view().lines);

// An agent adds a block while the window is open.
agentDoes({ blocks: [{ id: "a1", type: "sticky", x: 600, y: 0, w: 200, h: 200, props: sticky("From the agent", "blue") }] });
await settle();
if (process.argv.includes("--debug")) console.log(live.rev, live.getState().error, Object.keys(view().blocks), host.innerHTML.length);
ok("an agent's block appears live", !!el("a1") && view().blocks.a1?.label.includes("From the agent"), Object.keys(view().blocks));
ok("... with the agent's name on it", by("a1") === "Claude Code", by("a1"));
ok("... and the window is at the store's rev", live.rev === 1 && inSync(), live.rev);

// The person moves a block by keyboard: one op, as the person.
el("s1")!.focus();
key(el("s1")!, "ArrowRight", { altKey: true });
await settle();
ok("a move goes to the store as one op", sent.length === 1 && sent[0].source === "person" && sent[0].blocks?.[0]?.id === "s1", sent);
ok("... keeping what the canvas doesn't carry", sent[0].blocks?.[0]?.props?.text === "First idea", sent[0]);
ok("... and it's saved", memBlock("s1")!.x > 0 && inSync(), memBlock("s1"));
const movedX = memBlock("s1")!.x;

// The person types into a sticky: letters wait for a pause and go as one op.
const textarea = () => el("s1")!.querySelector("textarea")!;
const setText = (ta: HTMLTextAreaElement, text: string) => {
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(ta, text);
  ta.dispatchEvent(new Event("input", { bubbles: true }));
};
textarea().focus();
// BoardView.tsx counts its renders into this (whiteboard#50).
const renders: Record<string, number> = {};
(globalThis as { __boardRenders?: Record<string, number> }).__boardRenders = renders;
for (const text of ["First idea!", "First idea!!", "First idea, typed"]) {
  setText(textarea(), text);
  await wait(10);
}
delete (globalThis as { __boardRenders?: Record<string, number> }).__boardRenders;
ok("typing shows at once", view().blocks.s1.props?.text === "First idea, typed", view().blocks.s1.props);
// A key renders the typed block and its own 4 ports and 8 resize handles, not
// every block's (whiteboard#46, #50). A linked block may render too: its
// description names the typed block by its words.
const allBlocks = host.querySelectorAll('[data-slot="board-block"]').length;
ok("a key renders the typed block's item alone", renders.item === 3, renders);
ok("... its ports and handles alone, not every block's", renders.part === 3 * 12, { renders, allBlocks });
ok("... and not every block", (renders.block ?? 0) <= 3 * 2 && allBlocks > 2, { renders, allBlocks });
ok("typing waits for a pause", sent.length === 1, sent.length);
await wait(120);
ok("typing goes as one op after the pause", sent.length === 2 && sent[1].blocks?.[0]?.props?.text === "First idea, typed", sent.length);
ok("... and the frame and group stay", memBlock("s2")!.group === "g" && mem.board().blocks.find((b) => b.id === "s3")!.frame === "frm-plan");

// Ctrl+Z / Ctrl+Y step through the person's changes and the agent's.
key(textarea(), "z", { ctrlKey: true });
await settle();
ok("Ctrl+Z undoes the typing as a whole", memBlock("s1")!.props.text === "First idea" && view().blocks.s1.props?.text === "First idea", memBlock("s1")!.props);
key(window, "y", { ctrlKey: true });
await settle();
ok("Ctrl+Y redoes it", memBlock("s1")!.props.text === "First idea, typed" && inSync(), memBlock("s1")!.props);
key(window, "z", { ctrlKey: true });
key(window, "z", { ctrlKey: true });
await settle();
ok("Ctrl+Z twice more takes back the move too", memBlock("s1")!.x === 0, memBlock("s1"));
key(window, "z", { ctrlKey: true });
await settle();
ok("Ctrl+Z undoes the agent's change like the person's", !memBlock("a1") && !el("a1") && inSync(), Object.keys(view().blocks));
key(window, "Z", { ctrlKey: true, shiftKey: true });
await settle();
ok("Ctrl+Shift+Z redoes the agent's change", !!memBlock("a1") && !!el("a1") && by("a1") === "Claude Code", Object.keys(view().blocks));
key(window, "y", { ctrlKey: true });
key(window, "y", { ctrlKey: true });
await settle();
ok("redo brings back the move and the typing", memBlock("s1")!.x === movedX && memBlock("s1")!.props.text === "First idea, typed" && inSync(), memBlock("s1"));
const undoneAll = log.filter((o) => o.kind === "undo").length;
ok("each undo and redo is one step in the store", undoneAll === 4 && log.filter((o) => o.kind === "redo").length === 4, log.map((o) => o.kind ?? "change"));

// The person deletes a block with a line to it: one op, line and all.
const before = sent.length;
el("s2")!.focus();
key(el("s2")!, "Delete");
await settle();
ok("a delete goes as one op", sent.length === before + 1 && sent.at(-1)!.delete?.includes("s2"), sent.slice(before));
ok("... and takes its line", !mem.board().links.length && !view().lines.length && inSync(), mem.board().links);
key(window, "z", { ctrlKey: true });
await settle();
ok("undo brings the block and its line back", !!memBlock("s2") && mem.board().links.length === 1 && view().lines.length === 1 && inSync());

// An agent's change alone sends nothing back.
{
  const n = sent.length;
  agentDoes({ blocks: [{ ...memBlock("s3")!, props: sticky("In the plan, edited by the agent", "green") }] });
  await settle();
  ok("an agent's change shows and sends nothing back", sent.length === n && view().blocks.s3.props?.text === "In the plan, edited by the agent" && inSync(), sent.length - n);
}

// A stroke: moved as a block, saved as Go's stroke, still in its frame.
{
  const n = sent.length;
  const was = mem.board().ink[0].points;
  ok("the stroke shows as a block", !!el("ink-1") && !!view().blocks["ink-1"]?.ink, Object.keys(view().blocks));
  el("ink-1")!.focus();
  key(el("ink-1")!, "ArrowRight", { altKey: true });
  await settle();
  const now = mem.board().ink[0];
  ok("a stroke's move goes as one op of ink", sent.length === n + 1 && sent.at(-1)!.ink?.[0]?.id === "ink-1" && !sent.at(-1)!.blocks, sent.at(-1));
  const dx = now.points[0] - was[0];
  const dy = now.points[1] - was[1];
  const together = now.points.every((v, i) => Math.abs(v - (i % 3 === 0 ? was[i] + dx : i % 3 === 1 ? was[i] + dy : was[i])) < 1e-6);
  ok("... its points move with it, together", dx > 0 && together, { dx, dy, points: now.points });
  ok("... and it stays in its frame, in its colour", now.frame === "frm-plan" && now.color === "ink-blue" && now.width === 4 && inSync(), now);
}

// A frame: moved and renamed as Go's frame.
{
  const n = sent.length;
  el("frm-plan")!.focus();
  key(el("frm-plan")!, "ArrowDown", { altKey: true });
  await settle();
  const f = () => mem.board().frames[0];
  ok("a frame's move goes as one op of frames", sent.length === n + 1 && sent.at(-1)!.frames?.[0]?.id === "frm-plan" && f().y > 260 && f().name === "Plan", f());
  const input = el("frm-plan")!.querySelector<HTMLInputElement>("input")!;
  input.focus();
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(input, "Plan, renamed");
  input.dispatchEvent(new Event("input", { bubbles: true }));
  await wait(120);
  ok("renaming a frame saves its name", f().name === "Plan, renamed" && f().y > 260 && inSync(), f());
  ok("... and the blocks stay in it", mem.board().blocks.find((b) => b.id === "s3")!.frame === "frm-plan");
}

// A line drawn on the canvas (its connect, here through the bridge's
// connectBlocks): the canvas names it for its ports, Go gets an id it takes.
{
  const n = sent.length;
  const lines = view().lines.length;
  const board = agent.snapshot().find((c) => c.component === "board");
  const res = await withAgent("agent:Claude Code", live, () =>
    agent.act({ steps: [{ ref: board?.ref ?? "", action: "connectBlocks", args: ["s3", "a1"] }] }),
  );
  await settle();
  const op = sent.at(-1)!;
  ok("connecting runs", !!res?.ok, res);
  ok("a drawn line goes as one op", sent.length === n + 1 && op.links?.length === 1, sent.slice(n));
  ok("... under an id Go takes", /^[A-Za-z0-9_-]{1,64}$/.test(op.links?.[0]?.id ?? ""), op.links?.[0]?.id);
  ok("... with its ends and ports", op.links?.[0]?.from === "s3" && op.links?.[0]?.to === "a1" && !!op.links?.[0]?.fromPort && !!op.links?.[0]?.toPort, op.links?.[0]);
  ok("... and the window shows it once", view().lines.length === lines + 1 && mem.board().links.length === lines + 1 && inSync(), view().lines);
}

// An agent uses the live controls (board.act): its change carries its name.
{
  const res = await withAgent("agent:Claude Code", live, () =>
    agent.act({ steps: [{ ref: "Sticky note: Second idea", action: "setValue", args: ["Second idea, as the agent wrote it"] }] }),
  );
  await settle();
  ok("board.act's step runs", !!res?.ok, res);
  ok("... and is saved under the agent's name", memBlock("s2")!.props.text === "Second idea, as the agent wrote it" && memBlock("s2")!.updatedBy === "agent:Claude Code", memBlock("s2"));
  ok("... while who made the block stays the person", memBlock("s2")!.createdBy === "person" && by("s2") === "You");
  ok("... and was sent before board.act answered", sent.at(-1)!.source === "agent:Claude Code");
}

// Events doubled, delayed and reordered while the person and an agent both work.
{
  mem.deliver = (op, send) => {
    setTimeout(() => send(op), Math.random() * 40);
    setTimeout(() => send(structuredClone(op)), Math.random() * 40);
  };
  for (let i = 0; i < 8; i++) {
    agentDoes({ blocks: [{ id: `b${i}`, type: "sticky", x: 900 + i * 40, y: 300, w: 120, h: 120, props: sticky(`Agent note ${i}`) }] });
    if (i % 3 === 0) {
      el("s1")!.focus();
      key(el("s1")!, "ArrowDown", { altKey: true });
    }
    await wait(5);
  }
  await wait(150);
  ok("doubled, reordered events: the window ends on the store's board", inSync(), { window: live.rev, store: mem.board().rev });
  ok("... every agent block once", Object.keys(view().blocks).filter((k) => /^b\d$/.test(k)).length === 8);

  // One event never arrives: the window reloads.
  let dropped = false;
  mem.deliver = (op, send) => {
    if (!dropped) {
      dropped = true;
      return;
    }
    queueMicrotask(() => send(op));
  };
  agentDoes({ blocks: [{ id: "lost", type: "sticky", x: 0, y: 600, w: 120, h: 120, props: sticky("Its event was lost") }] });
  agentDoes({ blocks: [{ id: "after", type: "sticky", x: 160, y: 600, w: 120, h: 120, props: sticky("After it") }] });
  await wait(200);
  ok("a lost event: the window reloads and has both blocks", inSync() && !!el("lost") && !!el("after"), Object.keys(view().blocks));
  mem.deliver = (op, send) => queueMicrotask(() => send(op));
}

ok("no change was lost or doubled: the window's board is the store's", inSync() && live.rev === mem.board().rev, { window: live.rev, store: mem.board().rev });
ok("no error shown", live.getState().error === null, live.getState().error);

// ── 3. The window's ops, for Go ───────────────────────────────────────
{
  // Times are Go's to set; leave them out so the file only changes with the ops.
  const strip = (v: unknown) =>
    JSON.parse(JSON.stringify(v, (k, x) => (k === "at" || k === "createdAt" || k === "updatedAt" ? undefined : x)));
  const file = {
    note: "Written by frontend/scripts/live-check.tsx --write: ops as the whiteboard window sent them (and the agent's, in between), and the board the window's store ended on. Go's TestWindowOps applies them. Don't edit by hand.",
    start: strip(start),
    ops: strip(log),
    end: strip(mem.board()),
  };
  const text = JSON.stringify(file, null, 2) + "\n";
  // The person's moves while the agent works are timed, so only which ops there are must match.
  const stable = (s: string) =>
    (JSON.parse(s).ops as BoardOp[])
      .map((o) => [o.kind ?? "change", o.source, ...(o.delete ?? []), ...(o.blocks ?? []).map((b) => b.id), ...(o.links ?? []).map((l) => l.id)].join(" "))
      .sort()
      .join("|");
  if (process.argv.includes("--write")) {
    writeFileSync(windowFile, text);
    console.log("wrote", windowFile.pathname);
  } else {
    let have = "";
    try {
      have = readFileSync(windowFile, "utf8");
    } catch {
      /* missing */
    }
    ok("window-ops.json is current (run with --write)", !!have && stable(have) === stable(text), have ? undefined : "missing");
  }
}

// ── 4. The board's name in the window ─────────────────────────────────
{
  const nameBox = () => host.querySelector<HTMLElement>('[data-slot="board-name"]');
  ok("the window shows the board's name", nameBox()?.textContent?.includes("Window check") === true, nameBox()?.textContent);
  ok("... and its title follows it", document.title === "Toru — Window check", document.title);
  const before = mem.board().rev;
  nameBox()!.querySelector<HTMLButtonElement>('button[aria-label="Rename board"]')!.click();
  await settle();
  const field = nameBox()!.querySelector<HTMLInputElement>('input[aria-label="Board name"]')!;
  ok("the pencil turns the name into a field", !!field && field.value === "Window check");
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")!.set!.call(field, "  Launch plan ");
  field.dispatchEvent(new Event("input", { bubbles: true }));
  await settle();
  key(nameBox()!.querySelector("input")!, "Enter");
  await settle();
  await settle();
  ok("Enter renames the board in its store, as one op", mem.board().name === "Launch plan" && mem.board().rev === before + 1, mem.board().name);
  ok("... the window shows the new name and title", nameBox()?.textContent?.includes("Launch plan") === true && document.title === "Toru — Launch plan", [nameBox()?.textContent, document.title]);
  key(window, "z", { ctrlKey: true });
  await settle();
  await settle();
  ok("Ctrl+Z takes the rename back", mem.board().name === "Window check" && nameBox()?.textContent?.includes("Window check") === true, mem.board().name);
  // An agent renames it: the window follows.
  agentDoes({ name: "Agent's name" });
  await settle();
  ok("an agent's rename shows live", nameBox()?.textContent?.includes("Agent's name") === true && inSync(), nameBox()?.textContent);
  ok("still no error shown", live.getState().error === null, live.getState().error);
}

// ── 5. The toolbar: no dead controls ──────────────────────────────────
{
  const snapBox = () =>
    [...host.querySelectorAll<HTMLElement>('[role="checkbox"]')].find((c) => c.closest("label")?.textContent?.includes("Snap to grid"));
  ok("the zoom slider is gone", !host.querySelector('[role="slider"]') && !host.querySelector('[aria-label="Zoom"]'));
  ok("Snap to grid starts ticked", snapBox()?.getAttribute("aria-checked") === "true", snapBox()?.outerHTML.slice(0, 120));
  const before = { ...memBlock("s1")! };
  el("s1")!.focus();
  key(el("s1")!, "ArrowRight", { altKey: true });
  await settle();
  ok("... and Alt+arrow moves a block one dot (24)", memBlock("s1")!.x === before.x + 24, [before.x, memBlock("s1")!.x]);
  snapBox()!.click();
  await settle();
  ok("unticking it is saved", snapBox()?.getAttribute("aria-checked") === "false" && localStorage.getItem("toru.whiteboard.snapToGrid") === "0", localStorage.getItem("toru.whiteboard.snapToGrid"));
  const was = memBlock("s1")!.x;
  el("s1")!.focus();
  key(el("s1")!, "ArrowRight", { altKey: true });
  await settle();
  ok("... and moves go where they're put (1 unit)", memBlock("s1")!.x === was + 1, [was, memBlock("s1")!.x]);
  snapBox()!.click();
  await settle();
  ok("ticking it again is saved", localStorage.getItem("toru.whiteboard.snapToGrid") === "1");
  const names = JSON.stringify(agent.snapshot().map((c) => [c.component, c.label]));
  ok("agents don't see Share: who can reach a board is the person's (whiteboard#13)", !/share/i.test(names), names);
  ok("agents don't see a zoom slider", !agent.snapshot().some((c) => c.component === "slider"), names);
  // Open Share: the room sharing popover (whiteboard/share.tsx; no Go here, so it waits on its status).
  [...host.querySelectorAll<HTMLButtonElement>("button")].find((x) => x.textContent?.trim() === "Share")!.click();
  await settle();
  ok("Share opens", !!document.querySelector("[data-share-popover]"));
  ok("... and makes no promise about who can view the board", !document.body.textContent?.includes("Anyone with the link"));
  key(document.activeElement ?? document.body, "Escape");
  await settle();
}

// ── 6. The drawing bar and its tools ──────────────────────────────────
{
  const bar = () => host.querySelector<HTMLElement>('[data-slot="drawing-bar"]')!;
  const toolButton = (t: string) => bar().querySelector<HTMLElement>(`[data-tool="${t}"]`)!;
  const pressed = (t: string) => toolButton(t)?.hasAttribute("data-pressed") || toolButton(t)?.getAttribute("aria-pressed") === "true";
  const surface = () => host.querySelector<HTMLElement>('[data-slot="tool-surface"]')!;
  const inkNow = () => mem.board().ink;
  const pe = (type: string, x: number, y: number, more: PointerEventInit = {}) =>
    new PointerEvent(type, { pointerId: 3, pointerType: "mouse", clientX: x, clientY: y, button: 0, buttons: type === "pointerup" ? 0 : 1, bubbles: true, cancelable: true, ...more });
  const drag = async (target: Element, from: [number, number], to: [number, number], more: PointerEventInit = {}) => {
    target.dispatchEvent(pe("pointerdown", ...from, more));
    for (let i = 1; i <= 4; i++) window.dispatchEvent(pe("pointermove", from[0] + ((to[0] - from[0]) * i) / 4, from[1] + ((to[1] - from[1]) * i) / 4, more));
    window.dispatchEvent(pe("pointerup", ...to, more));
    await settle();
    await settle();
  };

  // A canvas point on screen, from the world's transform (the board pans to show what's focused).
  const screenOf = (cx: number, cy: number) => {
    const w = host.querySelector<HTMLElement>('[data-slot="board-world"]')!.getAttribute("style") ?? "";
    const m = /translate\(([-\d.]+)px,\s*([-\d.]+)px\)\s*scale\(([\d.]+)\)/.exec(w);
    const [tx, ty, z] = m ? [Number(m[1]), Number(m[2]), Number(m[3])] : [0, 0, 1];
    return [tx + cx * z, ty + cy * z] as [number, number];
  };

  ok("the drawing bar is in the window, a toolbar", bar()?.getAttribute("role") === "toolbar", bar()?.outerHTML.slice(0, 80));
  const tools = ["select", "hand", "pen", "highlighter", "eraser", "rect", "ellipse", "line", "arrow", "text", "emoji"];
  ok("it has every tool", tools.every((t) => !!toolButton(t)), tools.filter((t) => !toolButton(t)));
  ok("each tool's tooltip names its shortcut", tools.every((t) => /\([A-Z]\)$/.test(toolButton(t).title)), tools.map((t) => toolButton(t).title));
  ok("nine colours", bar().querySelectorAll("[data-colour]").length === 9);
  ok("undo and redo buttons", !!bar().querySelector('[data-part="drawing-undo"]') && !!bar().querySelector('[data-part="drawing-redo"]'));
  ok("select is on at first", pressed("select"));

  // Shortcuts pick tools, not while typing; the choice is saved.
  key(document.body, "r");
  await settle();
  ok("R picks the rectangle", pressed("rect") && surface().hasAttribute("data-active"), toolButton("rect").outerHTML.slice(0, 120));
  ok("the tool is saved", JSON.parse(localStorage.getItem("toru.whiteboard.drawing") ?? "{}").tool === "rect", localStorage.getItem("toru.whiteboard.drawing"));
  ok("the fill toggle shows for a rectangle", !!bar().querySelector('[data-part="drawing-fill"]'));

  // A rectangle: one drag, one op, one stroke of kind rect.
  let n = sent.length;
  const inkBefore = inkNow().length;
  const worldStyle = () => host.querySelector<HTMLElement>('[data-slot="board-world"]')!.getAttribute("style");
  const viewBefore = worldStyle();
  await drag(surface(), [300, 300], [420, 380]);
  ok("drawing a shape doesn't pan the board", worldStyle() === viewBefore, [viewBefore, worldStyle()]);
  const rect = inkNow().at(-1)!;
  ok("dragging draws a rectangle as one op", sent.length === n + 1 && inkNow().length === inkBefore + 1 && rect.kind === "rect" && rect.points.length === 6, { sent: sent.length - n, rect });
  ok("... in the bar's colour and width", rect.color === "ink-white" && rect.width === 4, rect);
  const rectBlock = () => view().blocks[rect.id];
  ok("... and it shows on the board as a shape", !!el(rect.id) && el(rect.id)!.getAttribute("data-shape") === "rect" && !!el(rect.id)!.querySelector("rect[data-slot=board-shape-line]"));

  // Every shape tool draws its shape.
  for (const [k, t] of [["o", "ellipse"], ["l", "line"], ["a", "arrow"]] as const) {
    key(document.body, k);
    await settle();
    n = sent.length;
    await drag(surface(), [300, 420], [380, 470], { shiftKey: t === "line" });
    ok(`${t} draws`, pressed(t) && sent.length === n + 1 && inkNow().at(-1)!.kind === t, inkNow().at(-1));
  }
  {
    const l = inkNow().find((s) => s.kind === "line")!;
    const angle = (Math.atan2(l.points[4] - l.points[1], l.points[3] - l.points[0]) * 180) / Math.PI;
    ok("Shift keeps a line at 45°", Math.abs(angle / 45 - Math.round(angle / 45)) < 1e-6, angle);
  }

  // Text: click, type, Enter.
  key(document.body, "t");
  await settle();
  surface().dispatchEvent(pe("pointerdown", 500, 500));
  await settle();
  const editor = host.querySelector<HTMLTextAreaElement>('[data-slot="shape-text-editor"]');
  ok("the text tool opens a label to type", !!editor);
  if (editor) {
    Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")!.set!.call(editor, "Hello board");
    editor.dispatchEvent(new Event("input", { bubbles: true }));
    await settle();
    n = sent.length;
    key(editor, "Enter");
    await settle();
    await settle();
    const label = inkNow().at(-1)!;
    ok("Enter puts the label on the board as one op", sent.length === n + 1 && label.kind === "text" && label.text === "Hello board", label);
    ok("... and it shows as text", el(label.id)?.querySelector("text")?.textContent === "Hello board");
  }

  // Emoji: click puts the chosen emoji down.
  key(document.body, "j");
  await settle();
  n = sent.length;
  surface().dispatchEvent(pe("pointerdown", 560, 300));
  await settle();
  await settle();
  ok("the emoji tool puts an emoji down", sent.length === n + 1 && inkNow().at(-1)!.kind === "emoji" && !!inkNow().at(-1)!.text, inkNow().at(-1));

  // Select the rectangle: handles; drag a corner: one op, still a rect.
  key(document.body, "Escape");
  await settle();
  ok("Escape goes back to select", pressed("select") && !surface().hasAttribute("data-active"));
  // Select it as a click on its line does (the board's own selection).
  el(rect.id)!.focus();
  key(el(rect.id)!, " ");
  await settle();
  const handles = host.querySelectorAll<HTMLElement>('[data-slot="shape-handle"]');
  ok("a selected rectangle shows its four corner handles", handles.length === 4, handles.length);
  if (handles.length === 4) {
    const se = host.querySelector<HTMLElement>('[data-handle="se"]')!;
    const before = rectBlock();
    n = sent.length;
    const corner = screenOf(rect.points[3], rect.points[4]);
    await drag(se, corner, [corner[0] + 60, corner[1] + 40]);
    const after = inkNow().find((s) => s.id === rect.id)!;
    ok("dragging a corner reshapes it as one op", sent.length === n + 1 && after.kind === "rect" && after.points[3] - after.points[0] > rect.points[3] - rect.points[0], { after, rect });
    ok("... keeping the opposite corner", after.points[0] === rect.points[0] && after.points[1] === rect.points[1], after.points);
    void before;

    // With select, the bar's colour restyles the selection.
    n = sent.length;
    bar().querySelector<HTMLElement>('[data-colour="ink-red"]')!.click();
    await settle();
    await settle();
    ok("a colour picked with a shape selected recolours it", sent.length === n + 1 && inkNow().find((s) => s.id === rect.id)!.color === "ink-red", inkNow().find((s) => s.id === rect.id));
    ok("... and leaves a hollow shape hollow", !inkNow().find((s) => s.id === rect.id)!.fill);

    // The fill toggle fills the selected rectangle, then takes it away: one op each.
    const fillToggle = () => bar().querySelector<HTMLElement>('[data-part="drawing-fill"]')!;
    n = sent.length;
    fillToggle().click();
    await settle();
    await settle();
    ok("Fill fills the selected rectangle as one op", sent.length === n + 1 && inkNow().find((s) => s.id === rect.id)!.fill === "ink-red", inkNow().find((s) => s.id === rect.id));
    fillToggle().click();
    await settle();
    await settle();
    ok("... and Fill again takes it away", sent.length === n + 2 && !("fill" in inkNow().find((s) => s.id === rect.id)!), inkNow().find((s) => s.id === rect.id));

    // Backspace while typing a label deletes nothing on the board.
    const count0 = Object.keys(view().blocks).length;
    el(rect.id)!.focus();
    key(el(rect.id)!, "t");
    await settle();
    surface().dispatchEvent(pe("pointerdown", 700, 700));
    await settle();
    const ed = host.querySelector<HTMLTextAreaElement>('[data-slot="shape-text-editor"]')!;
    key(ed, "Backspace");
    await settle();
    ok("Backspace in a label being typed deletes nothing", Object.keys(view().blocks).length === count0 && !!el(rect.id));
    key(ed, "Escape");
    await settle();
    key(document.body, "v");
    await settle();
    el(rect.id)!.focus();
    key(el(rect.id)!, " ");
    await settle();
  }

  // The eraser rubs out what it touches, as one op; undo brings it back.
  key(document.body, "e");
  await settle();
  const target = inkNow().find((s) => s.kind === "emoji")!;
  const box = view().blocks[target.id];
  n = sent.length;
  const count = inkNow().length;
  ok("the eraser's ring follows it", surface().getAttribute("data-tool") === "eraser");
  {
    const [x, y] = screenOf(box.x + box.width / 2, box.y + box.height / 2);
    await drag(surface(), [x - 5, y], [x + 5, y]);
  }
  ok("erasing is one op", sent.length === n + 1 && inkNow().length < count && !inkNow().some((s) => s.id === target.id), { ops: sent.length - n, before: count, after: inkNow().length });
  n = sent.length;
  bar().querySelector<HTMLElement>('[data-part="drawing-undo"]')!.click();
  await settle();
  await settle();
  ok("the undo button brings it back", inkNow().some((s) => s.id === target.id), inkNow().map((s) => s.id));
  bar().querySelector<HTMLElement>('[data-part="drawing-redo"]')!.click();
  await settle();
  await settle();
  ok("the redo button rubs it out again", !inkNow().some((s) => s.id === target.id));

  // The highlighter draws see-through, under the ink, through the pen.
  key(document.body, "m");
  await settle();
  const penSurface = host.querySelector<HTMLElement>('[data-slot="ink-surface"]')!;
  ok("M picks the highlighter, through the pen's surface", pressed("highlighter") && penSurface.hasAttribute("data-active"));
  n = sent.length;
  penSurface.dispatchEvent(pe("pointerdown", 200, 600));
  for (const x of [220, 240, 260, 280]) penSurface.dispatchEvent(pe("pointermove", x, 600));
  penSurface.dispatchEvent(pe("pointerup", 280, 600));
  await settle();
  await settle();
  const hl = inkNow().at(-1)!;
  ok("a highlighter stroke is saved as one", sent.length === n + 1 && hl.highlight === true && hl.width === 20 && !hl.kind, hl);
  const order = [...host.querySelectorAll<HTMLElement>("[data-block]")].map((e) => e.dataset.block);
  ok("... and drawn under the other ink", order.indexOf(hl.id) < order.indexOf(rect.id), order);

  // The hand pans and changes nothing.
  key(document.body, "h");
  await settle();
  n = sent.length;
  const was = worldStyle();
  await drag(surface(), [400, 400], [460, 430]);
  const moved = (st: string | null) => /translate\(([-\d.]+)px,\s*([-\d.]+)px\)/.exec(st ?? "")!.slice(1).map(Number);
  const [x0, y0] = moved(was);
  const [x1, y1] = moved(worldStyle());
  ok("the hand pans without changing the board", pressed("hand") && sent.length === n);
  ok("... by just as far as it dragged", x1 - x0 === 60 && y1 - y0 === 30, [x1 - x0, y1 - y0]);

  // Shortcuts work with a block focused (the board is a listbox of blocks).
  el("s1")!.focus();
  key(el("s1")!, "r");
  await settle();
  ok("a shortcut works with a block focused", pressed("rect"));

  // The width: presets and the fine control, per tool.
  key(document.body, "p");
  await settle();
  ok("the width control names the pen's width", bar().querySelector('[data-part="drawing-width-trigger"]')!.getAttribute("title") === "Width: 2");
  key(document.body, "v");
  await settle();
  ok("still no error shown after drawing", live.getState().error === null && inSync(), live.getState().error);
}

console.log(`live-check: ${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
