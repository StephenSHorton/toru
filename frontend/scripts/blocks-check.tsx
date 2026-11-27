// blocks-check is the block catalog's agent-ready test (docs/whiteboard.md,
// section 1). It renders the sample board, one block of every catalog type,
// in happy-dom with React and UI SDK's agent bridge, then for every type:
// the snapshot names the block and its controls, and `act` fills it, as an
// agent's change, into props the catalog's schema (and so Go) accepts.
// It also checks that the window renders exactly the catalog's types.
//
//   bun scripts/blocks-check.tsx          the checks
//   bun scripts/blocks-check.tsx --print  also print the agent snapshot
import { GlobalRegistrator } from "@happy-dom/global-registrator";

GlobalRegistrator.register({ url: "http://127.0.0.1/" });
declare global {
  var __UI_SDK_STRICT__: boolean | undefined;
  var IS_REACT_ACT_ENVIRONMENT: boolean | undefined;
}
globalThis.__UI_SDK_STRICT__ = true;
// Not React's act(): the bridge checks that each value took, which a
// controlled block only shows once React has rendered, as in the window.
globalThis.IS_REACT_ACT_ENVIRONMENT = false;

const React = await import("react");
const { createRoot } = await import("react-dom/client");
const { createAgent } = await import("@ui-sdk/react/agent");
const { Board } = await import("../src/whiteboard/BoardView");
const { BlockBody, blockRenderers, tokenize } = await import("../src/whiteboard/blocks");
const { blockLabel, blockTypes, checkProps, newProps } = await import("../src/whiteboard/catalog");
const { BoardImagesContext } = await import("../src/whiteboard/images");
const { sampleBoard } = await import("../src/whiteboard/sample");

type Blocks = import("../src/whiteboard/board").BoardBlocks;
type BoardImages = import("../src/whiteboard/images").BoardImages;

let pass = 0;
let fail = 0;
function ok(name: string, cond: boolean, detail?: unknown) {
  if (cond) pass++;
  else {
    fail++;
    console.log(`FAIL ${name}${detail === undefined ? "" : `: ${JSON.stringify(detail)}`}`);
  }
}

// ── the window renders exactly the catalog's types ────────────────────
{
  const catalog = blockTypes.map((t) => t.type).sort();
  const rendered = Object.keys(blockRenderers).sort();
  ok("every catalog type has a render, and every render a type", catalog.join() === rendered.join(), { catalog, rendered });
  ok("ten block types", catalog.length === 10, catalog);
  for (const t of blockTypes) {
    const props = newProps[t.type as keyof typeof newProps];
    ok(`a new ${t.type} block's props pass its schema`, props !== undefined && checkProps(t.type, props) === null, checkProps(t.type, props));
  }
  const sampleTypes = new Set(Object.values(sampleBoard.blocks).map((b) => b.type));
  ok("the sample board has every type", catalog.every((t) => sampleTypes.has(t)), [...sampleTypes]);
  ok("schema check refuses extra fields", checkProps("sticky", { text: "a", src: "x" }) !== null);
  ok("schema check refuses a bad colour", checkProps("card", { title: "a", color: "red" }) !== null);
  ok("tags split at commas", JSON.stringify(tokenize(["a, b,c"], true).map((x) => (typeof x === "string" ? x : x.label))) === '["a","b","c"]');
  ok("typing keeps the last piece as text", JSON.stringify(tokenize(["a, b"], false).map((x) => (typeof x === "string" ? `"${x}"` : x.label))) === '["a","\\" b\\""]');
}

// ── the rendered sample board ─────────────────────────────────────────
const picture = { asset: `${"a".repeat(64)}.png`, url: "/__board/image/a.png", width: 640, height: 400 };
const imported: string[] = [];
const images: BoardImages = {
  url: (asset) => `/__board/image/${asset}`,
  importFile: async (file) => {
    imported.push(`file:${file.name}`);
    return picture;
  },
  library: async () => [{ id: "shot1", label: "Shot of the editor", takenAt: "2026-11-27T10:00:00Z", url: "/__board/library/shot1" }],
  importLibrary: async (id) => {
    imported.push(`library:${id}`);
    return { ...picture, asset: `${"b".repeat(64)}.png` };
  },
};

// The bridge is made before the board renders, as the window makes it.
const agent = createAgent();
const changes: { id: string; source?: string }[] = [];
// The sample board, and a poll that takes several picks.
const startBlocks: Blocks = {
  ...sampleBoard.blocks,
  "poll-multi": {
    label: "Poll: Which days work?",
    type: "poll",
    props: { question: "Which days work?", multi: true, options: [{ label: "Mon" }, { label: "Tue", votes: 1 }] },
    x: 1040,
    y: 360,
    width: 300,
    height: 230,
  },
};
let current: Blocks = startBlocks;

function Harness() {
  const [blocks, setBlocks] = React.useState<Blocks>(startBlocks);
  current = blocks;
  return (
    <BoardImagesContext.Provider value={images}>
      <Board
        aria-label="Test board"
        blocks={blocks}
        onBlocksChange={setBlocks}
        defaultLines={sampleBoard.lines}
        renderBlock={(id, all) => (
          <BlockBody
            id={id}
            type={all[id].type ?? ""}
            props={all[id].props ?? {}}
            onProps={(next, details) => {
              changes.push({ id, source: details.source });
              setBlocks((prev) => ({ ...prev, [id]: { ...prev[id], props: next, label: blockLabel(prev[id].type ?? "", next) } }));
            }}
          />
        )}
      />
    </BoardImagesContext.Provider>
  );
}

const host = document.createElement("div");
document.body.append(host);
const root = createRoot(host);
root.render(<Harness />);
const settle = () => new Promise((r) => setTimeout(r, 30));
await settle();

const snapshot = () => agent.snapshot().filter((c) => !c.offPage);
const props = (id: string) => current[id].props as Record<string, any>;

// ── snapshot names every block and its controls ──────────────────────
{
  const snap = snapshot();
  if (process.argv.includes("--print")) console.log(JSON.stringify(snap, null, 2));
  ok("snapshot fits the bridge (no 'more' entry)", !snap.some((c) => c.component === "more"), snap.length);
  for (const c of snap) ok(`${c.component} ${c.ref} has a name`, !!c.label, c);
  const boardEntry = snap.find((c) => c.component === "board");
  const named = boardEntry?.blocks as Blocks | undefined;
  for (const [id, block] of Object.entries(sampleBoard.blocks)) {
    ok(`board names ${block.type} block ${id}`, !!named && named[id]?.label === blockLabel(block.type!, block.props) && !!named[id]?.type, named?.[id]);
  }
  const labels = snap.map((c) => c.label);
  const want: Record<string, string[]> = {
    sticky: ["Sticky note: Send a screenshot straight to a board", "Sticky note: Quick polls to settle small decisions"],
    text: ["Text: Toru whiteboard: launch plan"],
    card: ["Card title: Ship the block catalog", "Card notes: Ship the block catalog"],
    checklist: ["Write the catalog", "Agent-ready tests", "New item for Before launch", "Add to Before launch"],
    poll: ["Which block next?"],
    rating: ["Rate How useful is this board?"],
    table: ["Owners row 1 Task", "Owners row 3 Due", "Sort Owners by Owner", "Add row to Owners"],
    "tag-list": ["Tags in Themes"],
    image: ["Choose picture file", "Pick from Library"],
    frame: ["Frame name: Ideas"],
  };
  for (const [type, names] of Object.entries(want)) {
    for (const name of names) ok(`snapshot names the ${type} control "${name}"`, labels.includes(name), labels);
  }
}

// ── act fills every type ──────────────────────────────────────────────
async function run(name: string, steps: { ref: string; action: string; args?: unknown[] }[], expectOk = true) {
  const res = await agent.act({ steps });
  await settle();
  ok(`act: ${name}`, !!res?.ok === expectOk, res?.error?.message ?? res);
}

changes.length = 0;
await run("sticky text", [{ ref: "Sticky note: Send a screenshot", action: "setValue", args: ["Capture to a board in one key"] }]);
ok("sticky text set", props("idea-capture").text === "Capture to a board in one key", props("idea-capture"));
ok("sticky renamed after its text", current["idea-capture"].label === "Sticky note: Capture to a board in one key", current["idea-capture"].label);

await run("text", [{ ref: "Text: Toru whiteboard", action: "setValue", args: ["Launch plan v2"] }]);
ok("text set", props("heading").text === "Launch plan v2" && props("heading").size === "xl", props("heading"));

await run("card notes", [{ ref: "Card notes: Ship the block catalog", action: "setValue", args: ["Friday"] }]);
await run("card title", [{ ref: "Card title: Ship the block catalog", action: "setValue", args: ["Ship it"] }]);
ok("card set", props("card-ship").title === "Ship it" && props("card-ship").body === "Friday", props("card-ship"));

await run("checklist tick and add", [
  { ref: "Agent-ready tests", action: "toggle" },
  { ref: "New item for Before launch", action: "setValue", args: ["Write the docs"] },
  { ref: "Add to Before launch", action: "press" },
]);
ok(
  "checklist ticked and grew",
  props("checklist").items[2].done === true && props("checklist").items[3]?.text === "Write the docs" && props("checklist").items.length === 4,
  props("checklist"),
);

await run("poll vote", [{ ref: "Which block next?", action: "setValue", args: ["Timer"] }]);
ok("poll vote counted", props("poll").options[1].votes === 2, props("poll").options);
await run("poll change of mind", [{ ref: "Which block next?", action: "setValue", args: ["Kanban column"] }]);
ok("poll vote moved", props("poll").options[1].votes === 1 && props("poll").options[0].votes === 4, props("poll").options);
await run("multi-choice poll", [
  { ref: "Vote Mon in Which days work?", action: "toggle" },
  { ref: "Vote Tue in Which days work?", action: "toggle" },
]);
ok("multi-choice poll counts each pick", props("poll-multi").options[0].votes === 1 && props("poll-multi").options[1].votes === 2, props("poll-multi").options);

// Props Go would refuse don't reach the board: a card title over 200 characters.
await run("a card title too long for the catalog is refused", [{ ref: "Card title: Ship it", action: "setValue", args: ["x".repeat(201)] }], false);
ok("the card keeps its title", props("card-ship").title === "Ship it", props("card-ship").title.length);

await run("rating", [{ ref: "Rate How useful", action: "setValue", args: [2.5] }]);
ok("rating set", props("rating").value === 2.5, props("rating"));

await run("table cell and row", [
  { ref: "Owners row 2 Owner", action: "setValue", args: ["Ana"] },
  { ref: "Add row to Owners", action: "press" },
  { ref: "Owners row 4 Task", action: "setValue", args: ["Docs"] },
  { ref: "Sort Owners by Owner", action: "press" },
]);
ok(
  "table filled",
  props("table").rows[1][1] === "Ana" && props("table").rows.length === 4 && props("table").rows[3][0] === "Docs",
  props("table").rows,
);
ok("sorting leaves the stored rows alone", props("table").rows[0][0] === "Catalog", props("table").rows);

await run("tag list", [{ ref: "Tags in Themes", action: "setValue", args: [["speed, agents, ux"]] }]);
ok("tags set from text", JSON.stringify(props("tags").items) === '["speed","agents","ux"]', props("tags"));

await run("frame name", [{ ref: "Frame name: Ideas", action: "setValue", args: ["Ideas we like"] }]);
ok("frame renamed", props("frame-ideas").name === "Ideas we like", props("frame-ideas"));

// An image block takes a file...
const png = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "whiteboard shot.png", { type: "image/png" });
await run("image from a file", [{ ref: "Choose picture file", action: "addFiles", args: [[png]] }]);
ok("image from a file", props("image-empty").asset === picture.asset && props("image-empty").alt === "whiteboard shot", props("image-empty"));

// ...and a Library screenshot: open the picker, press one.
root.render(<Harness key="again" />);
await settle();
await run("open the Library picker", [{ ref: "Pick from Library", action: "press" }]);
await run("image from the Library", [{ ref: "Use screenshot Shot of the editor", action: "press" }]);
ok(
  "image from the Library",
  props("image-empty").asset === `${"b".repeat(64)}.png` && props("image-empty").alt === "Shot of the editor",
  props("image-empty"),
);
ok("images imported through the service", imported.join() === "file:whiteboard shot.png,library:shot1", imported);

// A button's press carries no change details; every control that has them says "agent".
ok(
  "every change is the agent's",
  changes.filter((c) => c.source === "agent").length >= 12 && changes.every((c) => c.source === undefined || c.source === "agent"),
  changes,
);
for (const [id, block] of Object.entries(current)) {
  const err = checkProps(block.type ?? "", block.props);
  ok(`after filling, ${id}'s props pass the ${block.type} schema`, err === null, err);
}

// ── pressing a control in a block uses it, not the block ──────────────
{
  const textarea = document.querySelector<HTMLTextAreaElement>('[data-block="idea-polls"] textarea')!;
  const before = { ...current["idea-polls"] };
  textarea.focus();
  {
    const opts = { bubbles: true, button: 0, buttons: 1, clientX: 100, clientY: 100, pointerId: 1, pointerType: "mouse" };
    textarea.dispatchEvent(new PointerEvent("pointerdown", opts));
    textarea.dispatchEvent(new PointerEvent("pointermove", { ...opts, clientX: 260, clientY: 220 }));
    textarea.dispatchEvent(new PointerEvent("pointerup", { ...opts, clientX: 260, clientY: 220, buttons: 0 }));
  }
  await settle();
  ok("a drag that starts in a text area doesn't move the block", current["idea-polls"].x === before.x && current["idea-polls"].y === before.y, current["idea-polls"]);
  ok("the text area keeps focus", document.activeElement === textarea, document.activeElement?.outerHTML.slice(0, 80));
  ok("the block it's in is selected", document.querySelector('[data-block="idea-polls"]')?.hasAttribute("data-selected") === true);

  // The sticky's rim, around its text area, drags it.
  const rim = document.querySelector<HTMLElement>('[data-block="idea-polls"] [data-slot="block-sticky"]')!;
  const opts = { bubbles: true, button: 0, buttons: 1, clientX: 100, clientY: 100, pointerId: 2, pointerType: "mouse" };
  rim.dispatchEvent(new PointerEvent("pointerdown", opts));
  window.dispatchEvent(new PointerEvent("pointermove", { ...opts, clientX: 180, clientY: 140 }));
  rim.dispatchEvent(new PointerEvent("pointermove", { ...opts, clientX: 260, clientY: 220 }));
  window.dispatchEvent(new PointerEvent("pointerup", { ...opts, clientX: 260, clientY: 220, buttons: 0 }));
  rim.dispatchEvent(new PointerEvent("pointerup", { ...opts, clientX: 260, clientY: 220, buttons: 0 }));
  await settle();
  ok("a drag from the sticky's rim moves it", current["idea-polls"].x !== before.x || current["idea-polls"].y !== before.y, current["idea-polls"]);
}

root.unmount();
console.log(`${pass} passed, ${fail} failed`);
process.exit(fail ? 1 : 0);
