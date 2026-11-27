// templates-check: the board window's brainstorm templates and auto-layout
// (src/whiteboard/templates.ts, layout.ts and the board's addBlocks and
// arrange) in happy-dom.
//
// - The window's layout gives exactly Go's board.layout positions, on Go's
//   golden file (internal/board/testdata/layout-golden.json).
// - Every template in templates.json (written from Go) becomes window blocks
//   with frames first, every line joining two of its blocks.
// - addBlocks places a template on a mounted board in one change, and
//   arrange lays it out as grid, tree and cluster, leaving frames put.
//
//   bun scripts/templates-check.ts
import { GlobalRegistrator } from "@happy-dom/global-registrator";
import { readFileSync } from "node:fs";

GlobalRegistrator.register({ url: "http://127.0.0.1/" });

const { mount } = await import("@ui-sdk/core/testing");
const { board } = await import("../src/whiteboard/board");
const { layout } = await import("../src/whiteboard/layout");
const { boardTemplates, templateBoard } = await import("../src/whiteboard/templates");

let pass = 0;
let fail = 0;
function ok(name: string, cond: boolean, detail?: unknown) {
  if (cond) pass++;
  else {
    fail++;
    console.log(`FAIL ${name}${detail === undefined ? "" : `: ${JSON.stringify(detail)}`}`);
  }
}

// ── the same layout as Go ─────────────────────────────────────────────
{
  type GoBlock = { id: string; x: number; y: number; w: number; h: number; props: { tags?: string[] } };
  type Case = {
    kind: "grid" | "tree" | "cluster";
    direction?: "right" | "down";
    columns?: number;
    gap?: number;
    only?: string[];
    positions: Record<string, { x: number; y: number }>;
  };
  const golden = JSON.parse(
    readFileSync(new URL("../../internal/board/testdata/layout-golden.json", import.meta.url), "utf8"),
  ) as { blocks: GoBlock[]; links: { from: string; to: string }[]; cases: Case[] };
  const all = golden.blocks.map((b) => ({ id: b.id, x: b.x, y: b.y, w: b.w, h: b.h, tags: b.props.tags }));
  ok("golden has cases", golden.cases.length >= 6, golden.cases.length);
  for (const c of golden.cases) {
    const blocks = c.only ? c.only.map((id) => all.find((b) => b.id === id)!) : all;
    const got = layout(blocks, golden.links, c.kind, { gap: c.gap, columns: c.columns, direction: c.direction });
    const name = `${c.kind}${c.direction ? ` ${c.direction}` : ""}${c.columns ? ` ${c.columns} columns` : ""}${c.only ? " (some blocks)" : ""}`;
    ok(`layout ${name} matches Go`, JSON.stringify(sortKeys(got)) === JSON.stringify(sortKeys(c.positions)), { got, want: c.positions });
  }
}

function sortKeys<T>(o: Record<string, T>) {
  return Object.fromEntries(Object.keys(o).sort().map((k) => [k, o[k]]));
}

// ── templates become window boards ───────────────────────────────────
{
  ok("at least five templates", boardTemplates.length >= 5, boardTemplates.length);
  for (const t of boardTemplates) {
    const made = templateBoard(t.name, { prefix: "p-", x: 100, y: 50 });
    ok(`${t.name}: made`, !!made);
    if (!made) continue;
    const ids = Object.keys(made.blocks);
    ok(`${t.name}: every frame and block`, ids.length === t.frames.length + t.blocks.length, ids.length);
    ok(`${t.name}: ids prefixed`, ids.every((id) => id.startsWith("p-")));
    ok(
      `${t.name}: frames first`,
      ids.slice(0, t.frames.length).every((id) => made.blocks[id].type === "frame"),
    );
    const boxes = Object.values(made.blocks);
    ok(
      `${t.name}: top-left at x, y`,
      Math.min(...boxes.map((b) => b.x)) === 100 && Math.min(...boxes.map((b) => b.y)) === 50,
    );
    ok(`${t.name}: every block has words`, boxes.every((b) => b.label.trim().length > 0));
    ok(
      `${t.name}: lines join its blocks`,
      made.lines.every((l) => made.blocks[l.from.split(":")[0]] && made.blocks[l.to.split(":")[0]]),
    );
  }
  ok("unknown template", templateBoard("nope") === null);
}

// ── Template and Layout on a mounted board ────────────────────────────
{
  const start = templateBoard("mind-map")!;
  const b = mount(board, { defaultBlocks: start.blocks, defaultLines: start.lines }, `<root aria-label="Test"><world></world></root>`);
  await b.settle();
  const before = Object.keys(b.state.blocks).length;

  const retro = templateBoard("retro", { prefix: "t1-", x: 2000, y: 0 })!;
  ok("addBlocks places a template", b.api.addBlocks(retro.blocks, retro.lines));
  ok("all its blocks are on the board", Object.keys(b.state.blocks).length === before + Object.keys(retro.blocks).length);
  ok("the same ids twice are refused", !b.api.addBlocks(retro.blocks));

  const frames = Object.entries(b.state.blocks).filter(([, x]) => x.type === "frame");
  const framesAt = JSON.stringify(frames.map(([id, x]) => [id, x.x, x.y]));

  // Tree: the mind map's topic, then its branches, then their ideas, left to right.
  b.api.select([]);
  const movedTree = b.api.arrange("tree");
  const s = b.state.blocks;
  ok("tree moves blocks", movedTree > 0, movedTree);
  ok("tree: topic left of branches left of ideas", s.topic.x < s["branch-1"].x && s["branch-1"].x < s["branch-1-1"].x, [
    s.topic.x,
    s["branch-1"].x,
    s["branch-1-1"].x,
  ]);
  ok(
    "tree: lines turned to the sides that face",
    b.state.lines.filter((l) => l.from.startsWith("topic:")).every((l) => l.from === "topic:right" && l.to.endsWith(":left")),
    b.state.lines.slice(0, 4),
  );

  // Cluster: one column per tag; blocks with the same first tag share an x.
  b.api.arrange("cluster");
  const c = b.state.blocks;
  ok("cluster: a branch and its ideas share a column", c["branch-2"].x === c["branch-2-1"].x && c["branch-2"].x === c["branch-2-2"].x);
  ok("cluster: retro columns apart", c["t1-went-well-1"].x !== c["t1-went-badly-1"].x);

  // Grid on just the selection.
  b.api.select(["branch-1", "branch-2", "branch-3", "branch-4"]);
  b.api.arrange("grid");
  const g = b.state.blocks;
  ok("grid on the selection: two by two", g["branch-1"].y === g["branch-2"].y && g["branch-3"].y === g["branch-4"].y && g["branch-1"].x === g["branch-3"].x, [
    g["branch-1"],
    g["branch-2"],
    g["branch-3"],
  ]);

  const framesNow = JSON.stringify(Object.entries(b.state.blocks).filter(([, x]) => x.type === "frame").map(([id, x]) => [id, x.x, x.y]));
  ok("frames stay where they are", framesNow === framesAt);
  ok("an unknown layout moves nothing", b.api.arrange("spiral" as never) === 0);
  b.unmount();
}

console.log(`templates-check: ${pass} passed, ${fail} failed`);
if (fail) process.exit(1);
