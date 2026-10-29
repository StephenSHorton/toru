// edl-check exercises the video editor's EDL ops (src/trim/edl.ts) on edge
// cases: splits at and near boundaries, removing around texts, reordering,
// edge drags past limits, output<->source time mapping, history merging and
// the EditRequest it builds.
//
//   bun scripts/edl-check.ts
import {
  addSource,
  historyApply,
  historyInit,
  historyRedo,
  historyUndo,
  initEdl,
  locate,
  MIN_SEG,
  move,
  newText,
  outputDuration,
  remove,
  removeText,
  setEdge,
  split,
  toOutput,
  toRequest,
  upsertText,
  type Edl,
} from "../src/trim/edl";

let failed = 0;
let passed = 0;
function check(name: string, ok: boolean, detail?: unknown) {
  if (ok) passed++;
  else {
    failed++;
    console.error(`FAIL ${name}`, detail ?? "");
  }
}
const near = (a: number, b: number, eps = 1e-9) => Math.abs(a - b) < eps;
const spans = (e: Edl) => e.segs.map((s) => [s.source, s.start, s.end]);

const A = { path: "C:/a.mp4", url: "/__file/a", duration: 120, width: 2560, height: 1440 };
const B = { path: "C:/b.mp4", url: "/__file/b", duration: 30, width: 1920, height: 1080 };

// init / addSource
let e = initEdl(A);
check("init one whole segment", e.segs.length === 1 && near(outputDuration(e), 120));
e = addSource(e, B);
check("addSource appends whole clip", JSON.stringify(spans(e)) === JSON.stringify([[0, 0, 120], [1, 0, 30]]));
check("addSource duration", near(outputDuration(e), 150));

// split
const s1 = split(e, 60);
check("split middle", JSON.stringify(spans(s1)) === JSON.stringify([[0, 0, 60], [0, 60, 120], [1, 0, 30]]), spans(s1));
check("split keeps left id", s1.segs[0].id === e.segs[0].id && s1.segs[1].id !== e.segs[0].id);
check("split on boundary is no-op", split(e, 120) === e);
check("split within MIN_SEG of edge is no-op", split(e, MIN_SEG / 2) === e && split(e, 120 - MIN_SEG / 2) === e);
check("split before 0 is no-op", split(e, -1) === e);
check("split past end is no-op", split(e, 151) === e);
check("split in second source", JSON.stringify(spans(split(e, 135))) === JSON.stringify([[0, 0, 120], [1, 0, 15], [1, 15, 30]]));

// locate / toOutput
check("locate 0", locate(e, 0)?.seg.id === e.segs[0].id && near(locate(e, 0)!.srcT, 0));
check("locate boundary goes to next seg", locate(e, 120)?.seg.id === e.segs[1].id && near(locate(e, 120)!.srcT, 0));
check("locate end maps to last seg end", locate(e, 150)?.seg.id === e.segs[1].id && near(locate(e, 150)!.srcT, 30));
check("locate past end is null", locate(e, 150.5) === null);
check("locate negative is null", locate(e, -0.5) === null);
check("locate empty is null", locate({ ...e, segs: [] }, 0) === null);
check("toOutput second seg", near(toOutput(e, e.segs[1].id, 10), 130));
check("toOutput clamps", near(toOutput(e, e.segs[1].id, 99), 150) && near(toOutput(e, e.segs[0].id, -5), 0));
check("toOutput unknown seg", toOutput(e, "nope", 1) === -1);
for (const t of [0, 0.5, 59.9, 60, 119.99, 120, 137.25, 150]) {
  const hit = locate(e, t)!;
  check(`round trip ${t}`, near(toOutput(e, hit.seg.id, hit.srcT), t), [t, hit]);
}

// remove: cut 30..60 out of A, texts shift with content
let r = split(split(e, 30), 60); // [0-30][30-60][60-120][B]
const tBefore = { ...newText(r, 10), id: "t1", start: 10, end: 20 };
const tAfter = { ...newText(r, 70), id: "t2", start: 70, end: 80 };
const tOver = { ...newText(r, 25), id: "t3", start: 25, end: 40 };
r = upsertText(upsertText(upsertText(r, tBefore), tAfter), tOver);
const cut = remove(r, r.segs[1].id);
check("remove seg", JSON.stringify(spans(cut)) === JSON.stringify([[0, 0, 30], [0, 60, 120], [1, 0, 30]]), spans(cut));
check("remove duration", near(outputDuration(cut), 120));
const tx = Object.fromEntries(cut.texts.map((t) => [t.id, [t.start, t.end]]));
check("text before cut unchanged", JSON.stringify(tx.t1) === "[10,20]", tx.t1);
check("text after cut shifts back", JSON.stringify(tx.t2) === "[40,50]", tx.t2);
check("text over cut is clipped", JSON.stringify(tx.t3) === "[25,30]", tx.t3);
check("remove unknown is no-op", remove(r, "nope") === r);
const one = initEdl(A);
check("remove last segment is no-op", remove(one, one.segs[0].id) === one);

// move
const m = move(s1, s1.segs[2].id, 0);
check("move to front", JSON.stringify(spans(m)) === JSON.stringify([[1, 0, 30], [0, 0, 60], [0, 60, 120]]), spans(m));
check("move clamps index", JSON.stringify(spans(move(s1, s1.segs[0].id, 99))) === JSON.stringify([[0, 60, 120], [1, 0, 30], [0, 0, 60]]));
check("move to same index is no-op", move(s1, s1.segs[1].id, 1) === s1);
check("move unknown is no-op", move(s1, "nope", 0) === s1);

// setEdge
const g = s1.segs[1]; // A 60..120
check("setEdge start", JSON.stringify(spans(setEdge(s1, g.id, "start", 70))[1]) === "[0,70,120]");
check("setEdge end past source clamps", setEdge(s1, s1.segs[2].id, "end", 99).segs[2].end === 30);
check("setEdge start below 0 clamps", setEdge(s1, s1.segs[0].id, "start", -3) === s1);
check("setEdge start past end keeps MIN_SEG", near(setEdge(s1, g.id, "start", 500).segs[1].start, 120 - MIN_SEG));
check("setEdge end before start keeps MIN_SEG", near(setEdge(s1, g.id, "end", 0).segs[1].end, 60 + MIN_SEG));
check("setEdge can overlap another seg", setEdge(s1, g.id, "start", 10).segs[1].start === 10);
check("setEdge unchanged is no-op", setEdge(s1, g.id, "end", 120) === s1);
const withText = upsertText(s1, { ...newText(s1, 125), id: "tx", start: 125, end: 128 });
const shrunk = setEdge(withText, g.id, "end", 100); // seg loses 20s, text after it moves back
check("setEdge shifts later texts", near(shrunk.texts[0].start, 105) && near(shrunk.texts[0].end, 108), shrunk.texts[0]);

// texts
const t0 = newText(e, 149);
check("newText fits timeline", t0.start <= 150 && t0.end <= 150 && t0.end > t0.start, t0);
const up = upsertText(e, { ...t0, start: 140, end: 999 });
check("upsertText clamps end", up.texts[0].end === 150);
const up2 = upsertText(up, { ...up.texts[0], text: "hi" });
check("upsertText replaces by id", up2.texts.length === 1 && up2.texts[0].text === "hi");
check("removeText", removeText(up2, t0.id).texts.length === 0 && removeText(up2, "nope") === up2);

// toRequest
const req = toRequest(upsertText(cut, { ...newText(cut, 0), id: "blank", text: "  " }), {
  crop: { x: 0, y: 0, w: 1280, h: 720 },
  tracks: [{ source: 0, index: 2, volume: 0, muted: true }],
  preset: "1080p",
  height: 900,
  outPath: "C:/out.mp4",
});
check("toRequest sources", JSON.stringify(req.sources) === JSON.stringify(["C:/a.mp4", "C:/b.mp4"]));
check(
  "toRequest segments in ms",
  JSON.stringify(req.segments) ===
    JSON.stringify([
      { source: 0, startMs: 0, endMs: 30000 },
      { source: 0, startMs: 60000, endMs: 120000 },
      { source: 1, startMs: 0, endMs: 30000 },
    ]),
  req.segments,
);
check("toRequest drops blank text", req.texts?.length === 3 && !req.texts.some((t) => !t.text.trim()));
check("toRequest text ms", req.texts?.some((t) => t.startMs === 40000 && t.endMs === 50000) === true, req.texts);
check("toRequest height only for custom", req.height === undefined);
check("toRequest keeps crop/tracks/outPath", !!req.crop && req.tracks?.length === 1 && req.outPath === "C:/out.mp4");
const bare = toRequest(initEdl(A), { preset: "custom", height: 720, kbps: 6000 });
check("toRequest bare omits optionals", !("crop" in bare) && !("tracks" in bare) && !("texts" in bare));
check("toRequest custom keeps height/kbps", bare.height === 720 && bare.kbps === 6000);

// history
let h = historyInit(e);
h = historyApply(h, (x) => split(x, 60));
h = historyApply(h, (x) => split(x, 90));
check("history two steps", h.past.length === 2 && h.present.segs.length === 4);
const noop = historyApply(h, (x) => split(x, 0));
check("history skips no-ops", noop === h);
h = historyUndo(h);
check("undo", h.present.segs.length === 3 && h.future.length === 1);
h = historyRedo(h);
check("redo", h.present.segs.length === 4 && h.future.length === 0);
h = historyUndo(h);
h = historyApply(h, (x) => move(x, x.segs[2].id, 0));
check("apply after undo clears redo", h.future.length === 0);
const before = h.past.length;
const id = h.present.segs[0].id;
for (const t of [5, 10, 15, 20]) h = historyApply(h, (x) => setEdge(x, id, "end", t), "drag:1");
check("same key merges into one step", h.past.length === before + 1 && h.present.segs[0].end === 20);
h = historyUndo(h);
check("undo a merged drag in one go", h.present.segs[0].end === 30);
check("undo at start is no-op", historyUndo(historyInit(e)).present === e);
check("redo at end is no-op", historyRedo(historyInit(e)).present === e);

console.log(`${passed} passed, ${failed} failed`);
if (failed) process.exit(1);
