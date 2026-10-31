// actions-check.ts checks the editor's action registry (src/trim/actions.ts):
// palette matching and ordering, shortcut parsing, and menus built from the
// registered actions. Run: bun scripts/actions-check.ts
import {
  actionCtx,
  actionForKey,
  allActions,
  comboOf,
  commandActions,
  matchScore,
  menuFor,
  provideActionState,
  rankActions,
  registerAction,
  runAction,
  type Action,
} from "../src/trim/actions";
import type { Project } from "../src/trim/project";

let failed = 0;
function check(name: string, ok: boolean, got?: unknown) {
  if (!ok) {
    failed++;
    console.error(`FAIL ${name}${got === undefined ? "" : `: ${JSON.stringify(got)}`}`);
  }
}

// --- matching ------------------------------------------------------------------
check("prefix beats word prefix", matchScore("sp", "Speed…") > matchScore("sp", "Go to start split"));
check("word prefix beats substring", matchScore("aud", "Extract audio") > matchScore("aud", "Laudable"));
check("letters in order match", matchScore("xa", "Extract audio") > 0);
check("out of order doesn't", matchScore("ax", "Extract") === 0);
check("closer letters score higher", matchScore("ea", "Extract audio") < matchScore("ea", "Ear"));
check("case-insensitive", matchScore("SPLIT", "Split at playhead") > 0);
check("empty query matches", matchScore("", "anything") > 0);

// --- key combos ----------------------------------------------------------------
const ev = (key: string, m: { ctrl?: boolean; shift?: boolean; alt?: boolean } = {}) => ({ key, ctrlKey: !!m.ctrl, metaKey: false, shiftKey: !!m.shift, altKey: !!m.alt });
check("letter", comboOf(ev("s")) === "S", comboOf(ev("s")));
check("ctrl+shift letter", comboOf(ev("Z", { ctrl: true, shift: true })) === "Ctrl+Shift+Z", comboOf(ev("Z", { ctrl: true, shift: true })));
check("? has no Shift", comboOf(ev("?", { shift: true })) === "?", comboOf(ev("?", { shift: true })));
check("+ has no Shift", comboOf(ev("+", { shift: true })) === "+");
check("Delete is Del", comboOf(ev("Delete")) === "Del");
check("Shift+Delete", comboOf(ev("Delete", { shift: true })) === "Shift+Del");
check("Space", comboOf(ev(" ")) === "Space");
check("Ctrl+K", comboOf(ev("k", { ctrl: true })) === "Ctrl+K");

// --- registry, menus, palette ----------------------------------------------------
const project = { version: 3, id: "p", name: "", canvas: { width: 1920, height: 1080, fps: 30 }, media: [], tracks: [{ id: "v1", kind: "video", muted: false, hidden: false, locked: false, clips: [{ id: "c1" }] }], texts: [], crop: null } as unknown as Project;
const two = { ...project, tracks: [{ ...project.tracks[0], clips: [{ id: "c1" }, { id: "c2" }, { id: "c3" }] }] } as unknown as Project;
provideActionState(() => ({ project, playhead: 1, playing: false, selection: { kind: "clip", id: "c1" } }));

const ran: string[] = [];
const mk = (a: Partial<Action> & Pick<Action, "id" | "group">): Action => ({ label: a.id, exec: () => ran.push(a.id), ...a });
const offs = [
  registerAction(mk({ id: "t.delete", group: "Clip", label: "Delete", shortcut: "Del", keys: ["Del", "Backspace"], scopes: ["clip"], destructive: true })),
  registerAction(mk({ id: "t.split", group: "Edit", label: "Split at playhead", shortcut: "S", scopes: ["clip", "viewer"] })),
  registerAction(mk({ id: "t.speed", group: "Time", label: "Speed…", scopes: ["clip"], enabled: (c) => (c.clipId === "img" ? "An image has no speed" : true) })),
  registerAction(mk({ id: "t.hide", group: "Track", label: (c) => (c.trackId ? "Hide track" : "Hide"), scopes: ["track"], hidden: (c) => c.trackId === "a1" })),
  registerAction(mk({ id: "t.zoom", group: "View", label: "Zoom in", shortcut: "+", keys: ["+", "="] })),
];

check("sorted by group", allActions().map((a) => a.id).join() === "t.split,t.speed,t.delete,t.hide,t.zoom", allActions().map((a) => a.id));

const clipMenu = menuFor("clip", { clipId: "c1" });
check(
  "clip menu: groups with separators",
  clipMenu.map((i) => (i.kind === "separator" ? "|" : i.label)).join(",") === "Split at playhead,|,Speed…,|,Delete",
  clipMenu.map((i) => (i.kind === "separator" ? "|" : i.label)),
);
check("menu fills the clip's track", actionCtx("menu", { clipId: "c1" })?.trackId === "v1");
const img = menuFor("clip", { clipId: "img" }).find((i) => i.kind !== "separator" && i.label === "Speed…");
check("disabled item says why", !!img && img.kind !== "separator" && img.kind !== "sub" && img.disabled === true && img.hint === "An image has no speed", img);
check("hidden is left out", menuFor("track", { trackId: "a1" }).length === 0);
check("label from the target", (menuFor("track", { trackId: "v1" })[0] as { label: string }).label === "Hide track");
const extra = menuFor("viewer", undefined, [{ label: "X", onSelect: () => {} }]);
check("extra after a separator", extra.length === 3 && extra[1].kind === "separator", extra.length);

check("key lookup", actionForKey(allActions(), "Backspace")?.id === "t.delete" && actionForKey(allActions(), "=")?.id === "t.zoom");
check("unknown key", actionForKey(allActions(), "Q") === undefined);

const pc = actionCtx("palette")!;
check("palette ctx uses the selection", pc.clipId === "c1" && pc.trackId === "v1" && pc.t === 1);
const byQuery = rankActions(allActions(), pc, "spl", []);
check("query: best match first", byQuery[0]?.action.id === "t.split", byQuery.map((r) => r.action.id));
const recentFirst = rankActions(allActions(), pc, "", ["t.zoom", "t.delete"]);
check("no query: recents first, in order", recentFirst.slice(0, 2).map((r) => r.action.id).join() === "t.zoom,t.delete", recentFirst.map((r) => r.action.id));
check("no query: every visible action", recentFirst.length === allActions().length);
check("recent flag", recentFirst[0].recent && !recentFirst[2].recent);

// runAction skips a disabled action and runs an enabled one.
runAction(allActions().find((a) => a.id === "t.speed")!, actionCtx("menu", { clipId: "img" })!);
runAction(allActions().find((a) => a.id === "t.split")!, pc);
check("run only when enabled", ran.join() === "t.split", ran);

// commandActions: the agent handler runs with ui.args unless ui.run is given.
const calls: unknown[] = [];
const [viaArgs, viaRun] = commandActions(
  "editor.x",
  {
    description: "",
    ui: [
      { id: "x.args", label: "A", group: "Edit", args: (c) => ({ clipId: c.clipId }) },
      { id: "x.run", label: "B", group: "Edit", run: () => calls.push("own") },
    ],
  },
  (input) => calls.push(input),
);
viaArgs.exec(pc);
viaRun.exec(pc);
check("command action: handler(args) or own run", JSON.stringify(calls) === JSON.stringify([{ clipId: "c1" }, "own"]), calls);
check("command action knows its command", viaArgs.command === "editor.x");

// choices: a submenu in menus, the action's own run in the palette.
const picked: string[] = [];
const offSub = registerAction(
  mk({ id: "t.trans", group: "Edit", label: "Add transition", scopes: ["gap"], choices: () => [{ label: "Dissolve", run: () => picked.push("dissolve") }, { label: "Wipe", why: "Not here", run: () => picked.push("wipe") }] }),
);
const sub = menuFor("gap", { trackId: "v1", t: 0 })[0];
check("choices make a submenu", sub?.kind === "sub" && sub.items.length === 2 && (sub.items[1] as { disabled?: boolean }).disabled === true, sub);
if (sub?.kind === "sub") (sub.items[0] as { onSelect: () => void }).onSelect();
check("choice runs", picked.join() === "dissolve", picked);
offSub();

// Unregistering removes it from menus and the palette.
offs.forEach((off) => off());

// One clip or several: "one" and "many" swap on the selection, and a key
// runs the one that fits.
const offMany = [
  registerAction(mk({ id: "t.del1", group: "Clip", label: "Delete", shortcut: "Del", scopes: ["clip"], clips: "one" })),
  registerAction(mk({ id: "t.delN", group: "Clip", label: (c) => `Delete ${c.clipIds.length} clips`, shortcut: "Del", scopes: ["clip"], clips: "many" })),
];
provideActionState(() => ({ project: two, playhead: 1, playing: false, selection: { kind: "clip", id: "c1", ids: ["c1", "c2"] } }));
const many = actionCtx("key")!;
check("selection ids in ctx", many.clipIds.join() === "c1,c2", many.clipIds);
check("key picks the many version", actionForKey(allActions(), "Del", many)?.id === "t.delN");
const groupMenu = menuFor("clip", { clipId: "c2" }).map((i) => (i.kind === "separator" ? "|" : i.label));
check("menu on a selected clip acts on the group", groupMenu.includes("Delete 2 clips") && !groupMenu.includes("Delete"), groupMenu);
const outside = actionCtx("menu", { clipId: "c3" })!;
check("menu on an unselected clip acts on it alone", outside.clipIds.join() === "c3" && actionForKey(allActions(), "Del", outside)?.id === "t.del1");
offMany.forEach((off) => off());
provideActionState(() => ({ project, playhead: 1, playing: false, selection: { kind: "clip", id: "c1" } }));

check("unregistered", allActions().length === 0);

if (failed) {
  console.error(`${failed} check(s) failed`);
  process.exit(1);
}
console.log("actions-check: all checks passed");
