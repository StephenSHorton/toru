// custom-blocks-check is the agent-ready test of custom blocks (whiteboard#27):
// block types agents design with board.block.define. It renders a board
// carrying internal/board/testdata/custom-blocks.json's two types (a feature
// idea and a bug report) in happy-dom with React and UI SDK's agent bridge,
// and checks that:
//   - each block draws its layout, named after its type and name prop;
//   - an agent fills every part through the bridge (vote, status, effort,
//     tags, severity, fixed, steps, fields) into props the type's schema takes;
//   - text from a definition stays text: no element, script or style from it;
//   - an unknown type and a broken definition show a plain placeholder card;
//   - the in-page store (what an exported board runs) carries types in ops,
//     undo and redo the way Go does.
//
//   bun scripts/custom-blocks-check.tsx          the checks
//   bun scripts/custom-blocks-check.tsx --print  also print the agent snapshot
import { GlobalRegistrator } from "@happy-dom/global-registrator";

GlobalRegistrator.register({ url: "http://127.0.0.1/" });
declare global {
  var __UI_SDK_STRICT__: boolean | undefined;
  var IS_REACT_ACT_ENVIRONMENT: boolean | undefined;
}
globalThis.__UI_SDK_STRICT__ = true;
globalThis.IS_REACT_ACT_ENVIRONMENT = false;

const React = await import("react");
const { createRoot } = await import("react-dom/client");
const { createAgent } = await import("@ui-sdk/react/agent");
const { Board } = await import("../src/whiteboard/BoardView");
const { BlockBody } = await import("../src/whiteboard/blocks");
const { blockLabel, blockType, checkProps } = await import("../src/whiteboard/catalog");
const { setBoardTypes, setLibraryTypes, setSaveHandler, libraryBlocks, customType, saveState } = await import("../src/whiteboard/custom-types");
const { BoardImagesContext } = await import("../src/whiteboard/images");
const { memoryLink, emptyBoard } = await import("../src/whiteboard/links");
const { LiveBoard } = await import("../src/whiteboard/live");
const samples = (await import("../../internal/board/testdata/custom-blocks.json")).default as { blocks: Record<string, unknown>[] };

type Blocks = import("../src/whiteboard/board").BoardBlocks;
type BlockDef = import("../src/whiteboard/custom-types").BlockDef;

let pass = 0;
let fail = 0;
function ok(name: string, cond: boolean, detail?: unknown) {
  if (cond) pass++;
  else {
    fail++;
    console.log(`FAIL ${name}${detail === undefined ? "" : `: ${JSON.stringify(detail)}`}`);
  }
}

const defs = samples.blocks.map((b, i) => ({ ...b, version: 1, origin: `blt-check${i}` }) as unknown as BlockDef);
const [ideaDef, bugDef] = defs;
// A type whose layout an older or broken Toru wrote: it must not crash the board.
// Its choice part reads an enum that isn't a list, which throws while drawing.
const brokenDef = {
  ...ideaDef,
  type: "broken-thing",
  label: "Broken thing",
  props: { type: "object", properties: { s: { type: "string", enum: "abc" } } },
  layout: { part: "choice", prop: "s" },
} as unknown as BlockDef;
// One with no layout at all isn't taken as a type.
const hollowDef = { ...ideaDef, type: "hollow", layout: null } as unknown as BlockDef;
// Text that looks like markup stays text.
const sneakyDef = {
  ...ideaDef,
  type: "sneaky",
  label: "<img src=x onerror=alert(1)>",
  layout: { part: "stack", children: [{ part: "heading", text: "<script>window.pwned = 1</script>{title}" }, { part: "text", text: "<b>bold?</b>" }] },
} as unknown as BlockDef;
setBoardTypes([ideaDef, bugDef, brokenDef, sneakyDef, hollowDef]);

ok("blockType knows a custom type", blockType("feature-idea")?.label === "Feature idea" && blockType("feature-idea")?.w === 340);
ok("checkProps uses the custom schema", checkProps("bug-report", { title: "x", severity: "apocalyptic" }) !== null);
ok("checkProps passes good custom props", checkProps("bug-report", bugDef.defaults) === null, checkProps("bug-report", bugDef.defaults));
ok("a custom block is named by its name prop", blockLabel("feature-idea", { title: "Dark mode" }) === "Feature idea: Dark mode");

const startBlocks: Blocks = {
  idea: { label: blockLabel("feature-idea", { ...ideaDef.defaults, title: "Dark mode" }), type: "feature-idea", props: { ...ideaDef.defaults, title: "Dark mode" }, x: 0, y: 0, width: 320, height: 420 },
  bug: {
    label: blockLabel("bug-report", { ...bugDef.defaults, title: "Crash on paste" }),
    type: "bug-report",
    props: { ...bugDef.defaults, title: "Crash on paste", steps: [{ text: "Copy an image" }] },
    x: 360,
    y: 0,
    width: 360,
    height: 460,
  },
  unknown: { label: "from-the-future", type: "from-the-future", props: { anything: 1 }, x: 0, y: 480, width: 240, height: 120 },
  broken: { label: "Broken thing", type: "broken-thing", props: { title: "x" }, x: 260, y: 480, width: 240, height: 120 },
  hollow: { label: "hollow", type: "hollow", props: {}, x: 780, y: 480, width: 240, height: 120 },
  sneaky: { label: "sneaky", type: "sneaky", props: { title: "<i>hi</i>" }, x: 520, y: 480, width: 240, height: 160 },
};
let current: Blocks = startBlocks;
const agent = createAgent();
const images = { url: () => "", importFile: async () => Promise.reject(new Error("no")), library: async () => [], importLibrary: async () => Promise.reject(new Error("no")) };

function Harness() {
  const [blocks, setBlocks] = React.useState<Blocks>(startBlocks);
  current = blocks;
  return (
    <BoardImagesContext.Provider value={images as never}>
      <Board
        aria-label="Custom blocks board"
        blocks={blocks}
        onBlocksChange={setBlocks}
        renderBlock={(id, all) => (
          <BlockBody
            id={id}
            type={all[id].type ?? ""}
            props={all[id].props ?? {}}
            onProps={(next) => setBlocks((prev) => ({ ...prev, [id]: { ...prev[id], props: next, label: blockLabel(prev[id].type ?? "", next) } }))}
          />
        )}
      />
    </BoardImagesContext.Provider>
  );
}

const consoleError = console.error;
const errors: string[] = [];
console.error = (...a: unknown[]) => {
  const s = a.map(String).join(" ");
  // React reports the error a boundary caught; that's the broken type, on purpose.
  if (!/map is not a function|The above error occurred|error boundary|CustomBoundary/i.test(s)) errors.push(s);
};

const host = document.createElement("div");
document.body.append(host);
const root = createRoot(host);
root.render(<Harness />);
const settle = () => new Promise((r) => setTimeout(r, 40));
await settle();

const snapshot = () => agent.snapshot().filter((c) => !c.offPage);
const props = (id: string) => current[id].props as Record<string, any>;
async function run(name: string, steps: { ref: string; action: string; args?: unknown[] }[], expectOk = true) {
  const res = await agent.act({ steps });
  await settle();
  ok(`act: ${name}`, !!res?.ok === expectOk, res?.error?.message ?? res);
  return res;
}

// ── the blocks draw ───────────────────────────────────────────────────
{
  const snap = snapshot();
  if (process.argv.includes("--print")) console.log(JSON.stringify(snap.map((c) => [c.component, c.label]), null, 1));
  const labels = snap.map((c) => c.label);
  for (const name of [
    "Idea in Feature idea: Dark mode",
    "Why it matters in Feature idea: Dark mode",
    "Vote in Feature idea: Dark mode",
    "Status in Feature idea: Dark mode",
    "Effort in Feature idea: Dark mode",
    "Tags in Tags in Feature idea: Dark mode",
    "Fixed in Bug report: Crash on paste",
    "Bug in Bug report: Crash on paste",
    "Severity in Bug report: Crash on paste",
    "New item for Steps to reproduce in Bug report: Crash on paste",
    "Expected in Bug report: Crash on paste",
    "Actual in Bug report: Crash on paste",
  ]) {
    ok(`snapshot names "${name}"`, labels.includes(name), labels);
  }
  const idea = document.querySelector('[data-block="idea"]');
  ok("the idea shows its type's label", idea?.querySelector('[data-slot="custom-kind"]')?.textContent?.includes("Feature idea") === true);
  ok("the status badge takes its colour", idea?.querySelector('[data-slot="custom-badge"]')?.getAttribute("data-color") === "grey");
  ok("the bug's severity badge is yellow for medium", document.querySelector('[data-block="bug"] [data-slot="custom-badge"]')?.getAttribute("data-color") === "yellow");
}

// ── an agent fills every part ─────────────────────────────────────────
await run("vote twice", [
  { ref: "Vote in Feature idea: Dark mode", action: "press" },
  { ref: "Vote in Feature idea: Dark mode", action: "press" },
]);
ok("two votes", props("idea").votes === 2, props("idea"));
await run("status", [{ ref: "Status in Feature idea: Dark mode", action: "setValue", args: ["building"] }]);
ok("status building", props("idea").status === "building", props("idea"));
ok("badge follows status", document.querySelector('[data-block="idea"] [data-slot="custom-badge"]')?.getAttribute("data-color") === "orange");
await run("a status outside the enum is refused", [{ ref: "Status in Feature idea: Dark mode", action: "setValue", args: ["cancelled"] }], false);
await run("effort", [{ ref: "Effort in Feature idea: Dark mode", action: "setValue", args: [7] }]);
ok("effort 7", props("idea").effort === 7, props("idea"));
await run("pitch", [{ ref: "Why it matters in Feature idea: Dark mode", action: "setValue", args: ["People work at night"] }]);
ok("pitch set", props("idea").pitch === "People work at night", props("idea"));
await run("title", [{ ref: "Idea in Feature idea: Dark mode", action: "setValue", args: ["Dark theme"] }]);
ok("title set and the block renamed", props("idea").title === "Dark theme" && current.idea.label === "Feature idea: Dark theme", current.idea);
await run("tags", [{ ref: "Tags in Tags in Feature idea: Dark theme", action: "setValue", args: [["ui, night"]] }]);
ok("tags set", JSON.stringify(props("idea").tags) === '["ui","night"]', props("idea").tags);

await run("bug: fixed, severity, step", [
  { ref: "Fixed in Bug report: Crash on paste", action: "toggle" },
  { ref: "Severity in Bug report: Crash on paste", action: "setValue", args: ["critical"] },
  { ref: "New item for Steps to reproduce in Bug report: Crash on paste", action: "setValue", args: ["Paste it twice"] },
  { ref: "Add to Steps to reproduce in Bug report: Crash on paste", action: "press" },
  { ref: "Copy an image", action: "toggle" },
  { ref: "Expected in Bug report: Crash on paste", action: "setValue", args: ["Two images"] },
]);
ok(
  "bug filled",
  props("bug").fixed === true && props("bug").severity === "critical" && props("bug").steps.length === 2 && props("bug").steps[0].done === true && props("bug").expected === "Two images",
  props("bug"),
);
ok("severity badge pink for critical", document.querySelector('[data-block="bug"] [data-slot="custom-badge"]')?.getAttribute("data-color") === "pink");
await run("an empty bug title isn't kept", [{ ref: "Bug in Bug report: Crash on paste", action: "setValue", args: [""] }]);
ok("the bug keeps its title", props("bug").title === "Crash on paste", props("bug").title);
for (const id of ["idea", "bug"]) {
  const err = checkProps(current[id].type!, current[id].props);
  ok(`after filling, ${id}'s props pass its type's schema`, err === null, err);
}

// ── text stays text; unknown and broken types are placeholders ───────
{
  const sneaky = document.querySelector('[data-block="sneaky"]')!;
  ok("no script element from a definition", document.querySelectorAll("script").length === 0);
  ok("no img element from a definition's label", sneaky.querySelectorAll("img").length === 0);
  ok("markup in a template shows as text", sneaky.querySelector('[data-slot="custom-heading"]')?.textContent === "<script>window.pwned = 1</script><i>hi</i>");
  ok("no b element from text", sneaky.querySelectorAll("b").length === 0);
  ok("nothing ran", (window as unknown as { pwned?: number }).pwned === undefined);
  const unknown = document.querySelector('[data-block="unknown"] [data-slot="block-placeholder"]');
  ok("an unknown type is a placeholder card", !!unknown && unknown.textContent!.includes("from-the-future"), document.querySelector('[data-block="unknown"]')?.innerHTML.slice(0, 200));
  const broken = document.querySelector('[data-block="broken"] [data-slot="block-placeholder"]');
  ok("a broken layout is a placeholder card, not a crash", !!broken, document.querySelector('[data-block="broken"]')?.innerHTML.slice(0, 200));
  ok("a type with no layout is a placeholder card", !!document.querySelector('[data-block="hollow"] [data-slot="block-placeholder"]'));
  ok("the rest of the board still draws", !!document.querySelector('[data-block="idea"] [data-slot="block-custom"]'));
}

// ── library and "Save to my blocks" ───────────────────────────────────
{
  setLibraryTypes([{ ...bugDef }]);
  ok("library lists the person's blocks", libraryBlocks().length === 1 && libraryBlocks()[0].type === "bug-report");
  ok("a board type the library lacks can be saved", saveState("feature-idea") === "new");
  ok("a board type the library has is saved", saveState("bug-report") === "saved");
  setLibraryTypes([{ ...bugDef }, { ...ideaDef, origin: "blt-mine" }]);
  ok("a different block of the same name clashes", saveState("feature-idea") === "clash");
  setLibraryTypes([{ ...bugDef }]);
  const saved: string[] = [];
  setSaveHandler(async (type, as) => {
    saved.push(`${type}:${as}`);
    setLibraryTypes([...libraryBlocks(), { ...ideaDef }]);
  });
  await settle();
  await run("save to my blocks", [{ ref: "Save Feature idea to my blocks", action: "press" }]);
  await settle();
  ok("the save went to the library", saved.join() === "feature-idea:", saved);
  ok("saved: the button goes", !snapshot().some((c) => c.label === "Save Feature idea to my blocks"));
  setSaveHandler(null);
  setBoardTypes([bugDef]);
  ok("the board's copy comes first, then the library", customType("feature-idea")?.origin === ideaDef.origin && customType("bug-report") !== undefined);
}

root.unmount();

// ── the in-page store carries types like Go ───────────────────────────
{
  setBoardTypes([]);
  setLibraryTypes([ideaDef]);
  const link = memoryLink(emptyBoard("brd-c", "Custom"));
  const live = new LiveBoard("brd-c", link, { gapMs: 50, typingMs: 10 });
  await live.start();
  // A person places a block of their own type: the op takes the definition with it.
  live.edit({ blocks: { n1: { label: "Feature idea: New idea", type: "feature-idea", props: { ...ideaDef.defaults }, x: 0, y: 0, width: 320, height: 380 } } });
  await live.flush();
  let doc = link.board();
  ok("placing a library block copies its type onto the board", doc.types?.length === 1 && doc.types[0].type === "feature-idea", doc.types);
  ok("the window's copy has it too", live.getState().doc?.types?.[0]?.type === "feature-idea");
  await live.undo();
  doc = link.board();
  ok("undo takes the type off with the block", !doc.types && doc.blocks.length === 0, doc);
  await live.redo();
  doc = link.board();
  ok("redo puts it back", doc.types?.length === 1 && doc.blocks.length === 1, doc);
  const other = { ...ideaDef, origin: "blt-else", version: 3 };
  let refused = false;
  try {
    link.applyFrom({ source: "person", types: [other] });
  } catch {
    refused = true;
  }
  ok("a different block of the same name is refused", refused);
  live.stop();
}

console.error = consoleError;
ok("no React errors", errors.length === 0, errors.slice(0, 3));
console.log(`${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
