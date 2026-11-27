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

// The bridge is made before the board renders, as the window makes it. Its
// allow stands in for the person: a step on a control marked
// data-agent-confirm runs only while personSays is true.
let personSays = false;
const asked: string[] = [];
const agent = createAgent({
  allow: (step) => {
    if (!step.confirm) return true;
    asked.push(step.confirm);
    return personSays;
  },
});
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
    checklist: ["Write the catalog", "Agent-ready tests", "New item for Before launch", "Add to Before launch", "Remove item 3 from Before launch"],
    poll: ["Vote in Which block next?", "Edit options of Which block next?"],
    rating: ["Rate How useful is this board?"],
    table: ["Owners row 1 Task", "Owners row 3 Due", "Sort Owners by Owner", "Add row to Owners", "Edit columns of Owners"],
    "tag-list": ["Tags in Themes"],
    image: ["Choose picture file", "Pick from Library", "New picture file for Test card screenshot", "New picture from Library for Test card screenshot"],
    frame: ["Frame name: Ideas", "Frame colour: Ideas"],
    colour: ["Sticky colour: Send a screenshot straight to a board", "Card colour: Ship the block catalog"],
  };
  for (const [type, names] of Object.entries(want)) {
    for (const name of names) ok(`snapshot names the ${type} control "${name}"`, labels.includes(name), labels);
  }
  // The bridge finds a control by whole words of its name, so no control's
  // words may all be in another's.
  const words = (l: string) => new Set(l.toLowerCase().split(/[^\p{L}\p{N}-]+/u).filter(Boolean));
  const controls = snap.filter((c) => c.label && c.component !== "board");
  for (const a of controls) {
    for (const b of controls) {
      if (a === b) continue;
      const wb = words(b.label!);
      ok(`"${a.label}" isn't inside "${b.label}"`, ![...words(a.label!)].every((w) => wb.has(w)));
    }
  }
}

// ── with the edit panels open, the names still hold ──────────────────
{
  await agent.act({
    steps: [
      { ref: "Edit options of Which block next?", action: "press" },
      { ref: "Edit options of Which days work?", action: "press" },
      { ref: "Edit columns of Owners", action: "press" },
    ],
  });
  await settle();
  const snap = snapshot();
  ok("with the panels open the snapshot still fits the bridge", !snap.some((c) => c.component === "more"), snap.length);
  const labels = snap.map((c) => c.label);
  for (const name of [
    "Option 1 text in Which block next?",
    "Remove option 3 from Which block next?",
    "New option for Which block next?",
    "Add option to Which block next?",
    "Option 2 text in Which days work?",
    "Column 1 name in Owners",
    "Remove column 3 from Owners",
    "New column for Owners",
    "Add column to Owners",
  ]) {
    ok(`an open panel shows "${name}"`, labels.includes(name), labels);
  }
  const words = (l: string) => new Set(l.toLowerCase().split(/[^\p{L}\p{N}-]+/u).filter(Boolean));
  const controls = snap.filter((c) => c.label && c.component !== "board");
  for (const a of controls) {
    for (const b of controls) {
      if (a === b) continue;
      const wb = words(b.label!);
      ok(`open: "${a.label}" isn't inside "${b.label}"`, ![...words(a.label!)].every((w) => wb.has(w)));
    }
  }
  const removes = snap.filter((c) => /^Remove /.test(c.label ?? ""));
  ok(
    "every remove control (3 items, 5 options, 3 columns) is marked for a person",
    removes.length === 11 && removes.every((c) => !!c.confirm),
    removes.map((c) => [c.label, c.confirm]),
  );
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

await run("poll vote", [{ ref: "Vote in Which block next?", action: "setValue", args: ["Timer"] }]);
ok("poll vote counted", props("poll").options[1].votes === 2, props("poll").options);
await run("poll change of mind", [{ ref: "Vote in Which block next?", action: "setValue", args: ["Kanban column"] }]);
ok("poll vote moved", props("poll").options[1].votes === 1 && props("poll").options[0].votes === 4, props("poll").options);
await run("multi-choice poll", [
  { ref: "Vote Mon in Which days work?", action: "toggle" },
  { ref: "Vote Tue in Which days work?", action: "toggle" },
]);
ok("multi-choice poll counts each pick", props("poll-multi").options[0].votes === 1 && props("poll-multi").options[1].votes === 2, props("poll-multi").options);

// ── poll options: add, rename, remove ──────────────────────────────────
await run("poll option added", [
  { ref: "New option for Which block next?", action: "setValue", args: ["Timer v2"] },
  { ref: "Add option to Which block next?", action: "press" },
]);
ok("poll has a fourth option", props("poll").options.length === 4 && props("poll").options[3].label === "Timer v2", props("poll").options);
await run("poll option renamed", [{ ref: "Option 2 text in Which block next?", action: "setValue", args: ["Countdown timer"] }]);
ok("poll option renamed, votes kept", props("poll").options[1].label === "Countdown timer" && props("poll").options[1].votes === 1, props("poll").options);
// Another option's name shows as typed (so typing "Timer 2" past "Timer"
// doesn't stick) but isn't kept, like an emptied one.
await run("typing another option's name", [{ ref: "Option 4 text in Which block next?", action: "setValue", args: ["Kanban column"] }]);
ok("another option's name isn't kept", props("poll").options[3].label === "Timer v2", props("poll").options);
await run("adding an option the poll has is refused", [
  { ref: "New option for Which block next?", action: "setValue", args: ["timer v2"] },
  { ref: "Add option to Which block next?", action: "press" },
]);
ok("no second Timer v2", props("poll").options.length === 4, props("poll").options);
await run("clear the refused new option", [{ ref: "New option for Which block next?", action: "setValue", args: [""] }]);
// An emptied name shows as typed but isn't kept, as with a card title.
await run("emptying a poll option", [{ ref: "Option 4 text in Which block next?", action: "setValue", args: [""] }]);
ok("the refused names left the poll alone", props("poll").options[3].label === "Timer v2", props("poll").options);
asked.length = 0;
await run("removing a poll option needs the person", [{ ref: "Remove option 4 from Which block next?", action: "press" }], false);
ok("the person was asked about the option", asked.length === 1 && asked[0].includes('"Timer v2"'), asked);
ok("a refused removal keeps the option", props("poll").options.length === 4, props("poll").options);
personSays = true;
await run("poll option removed with the person's yes", [{ ref: "Remove option 4 from Which block next?", action: "press" }]);
personSays = false;
ok("poll back to three options", props("poll").options.map((o: any) => o.label).join() === "Kanban column,Countdown timer,Code snippet", props("poll").options);
await run("a vote still lands after renaming", [{ ref: "Vote in Which block next?", action: "setValue", args: ["Countdown timer"] }]);
ok("vote moved to the renamed option", props("poll").options[1].votes === 2 && props("poll").options[0].votes === 3, props("poll").options);

// ── checklist items: remove, with the person's yes ─────────────────────
asked.length = 0;
await run("removing a checklist item needs the person", [{ ref: "Remove item 2 from Before launch", action: "press" }], false);
ok("the person was asked about the item", asked.length === 1 && asked[0].includes('"Render every block"'), asked);
ok("a refused removal keeps the item", props("checklist").items.length === 4, props("checklist").items);
personSays = true;
await run("checklist item removed with the person's yes", [{ ref: "Remove item 2 from Before launch", action: "press" }]);
personSays = false;
ok(
  "checklist lost only that item",
  props("checklist").items.map((i: any) => i.text).join() === "Write the catalog,Agent-ready tests,Write the docs",
  props("checklist").items,
);

// ── colours: sticky, card, frame ──────────────────────────────────────
await run("sticky colour", [{ ref: "Sticky colour: Quick polls", action: "setValue", args: ["blue"] }]);
ok("sticky is blue", props("idea-polls").color === "blue", props("idea-polls"));
ok("sticky shows blue", document.querySelector('[data-block="idea-polls"] [data-slot="block-sticky"]')?.getAttribute("data-color") === "blue");
await run("card colour by name", [{ ref: "Card colour: Ship it", action: "setValue", args: ["Orange"] }]);
ok("card is orange", props("card-ship").color === "orange", props("card-ship"));
await run("frame colour", [{ ref: "Frame colour: Ideas", action: "setValue", args: ["green"] }]);
ok("frame is green", props("frame-ideas").color === "green", props("frame-ideas"));
await run("a colour outside the catalog is refused", [{ ref: "Frame colour: Ideas", action: "setValue", args: ["red"] }], false);
ok("frame stays green", props("frame-ideas").color === "green", props("frame-ideas"));

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

// ── table columns: add, rename, remove ─────────────────────────────────
await run("table column added", [
  { ref: "New column for Owners", action: "setValue", args: ["Status"] },
  { ref: "Add column to Owners", action: "press" },
  { ref: "Owners row 1 Status", action: "setValue", args: ["Done"] },
]);
ok(
  "table has a Status column",
  props("table").columns.join() === "Task,Owner,Due,Status" && props("table").rows[0][3] === "Done",
  props("table"),
);
await run("table column renamed", [{ ref: "Column 3 name in Owners", action: "setValue", args: ["Deadline"] }]);
ok("column renamed, cells kept", props("table").columns[2] === "Deadline" && props("table").rows[0][2] === "Fri", props("table"));
await run("emptying a column name", [{ ref: "Column 3 name in Owners", action: "setValue", args: [""] }]);
await run("typing another column's name", [{ ref: "Column 3 name in Owners", action: "setValue", args: ["Task"] }]);
ok("an emptied column name isn't kept", props("table").columns[2] === "Deadline", props("table").columns);
asked.length = 0;
await run("removing a column needs the person", [{ ref: "Remove column 2 from Owners", action: "press" }], false);
ok("the person was asked about the column", asked.length === 1 && asked[0].includes('"Owner"'), asked);
personSays = true;
await run("column removed with the person's yes", [{ ref: "Remove column 2 from Owners", action: "press" }]);
personSays = false;
ok(
  "the column and its cells are gone",
  props("table").columns.join() === "Task,Deadline,Status" &&
    props("table").rows[0].join() === "Catalog,Fri,Done" &&
    props("table").rows[1].join() === "Live sync,Mon",
  props("table"),
);

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

// A filled image block takes a new picture, from a file...
const png2 = new File([new Uint8Array([0x89, 0x50, 0x4e, 0x47])], "editor timeline.png", { type: "image/png" });
await run("new picture from a file", [{ ref: "New picture file for Test card screenshot", action: "addFiles", args: [[png2]] }]);
ok(
  "the filled block shows the new file",
  props("image").asset === picture.asset && props("image").alt === "editor timeline" && props("image").caption === "Library screenshot",
  props("image"),
);
// ...or from the Library.
await run("open the Library picker on a filled block", [{ ref: "New picture from Library for editor timeline", action: "press" }]);
await run("new picture from the Library", [{ ref: "Use screenshot Shot of the editor", action: "press" }]);
ok("the filled block shows the Library shot", props("image").asset === `${"b".repeat(64)}.png` && props("image").alt === "Shot of the editor", props("image"));
ok("replacements imported through the service", imported.slice(2).join() === "file:editor timeline.png,library:shot1", imported);

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
