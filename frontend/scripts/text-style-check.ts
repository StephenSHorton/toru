// text-style-check.ts checks the text style model (src/trim/textStyle.ts,
// textRender.ts resolveStyle/textOpacityAt): defaults and clamping, edits
// keeping only what differs from the plain look, opacity keyframes, presets,
// and moving pre-#125 saved styles out of localStorage.
// The drawing itself is checked against the export in a browser (PR notes).
// Run: bun scripts/text-style-check.ts
import { resolveStyle, textOpacityAt, TEXT_DEFAULTS } from "../src/trim/textRender";
import { BUILTIN_TEXT_PRESETS, migrateLocal, replaceTextStyle, setTextOpacity, setTextStyle, tidyStyle, toggleTextOpacityKey } from "../src/trim/textStyle";
import type { Project } from "../src/trim/project";

let failed = 0;
function check(name: string, ok: boolean, got?: unknown) {
  if (!ok) {
    failed++;
    console.error(`FAIL ${name}${got === undefined ? "" : `: ${JSON.stringify(got)}`}`);
  }
}

const near = (a: number, b: number) => Math.abs(a - b) < 1e-9;

// Defaults and clamping.
const d = resolveStyle(undefined);
check("defaults", JSON.stringify(d) === JSON.stringify({ ...TEXT_DEFAULTS }), d);
const c = resolveStyle({ outlineWidth: 9, opacity: -1, color: "red", align: "middle" as "left", lineSpacing: Number.NaN });
check("clamped", c.outlineWidth === 0.3 && c.opacity === 0 && c.color === "#ffffff" && c.align === "center" && c.lineSpacing === 1, c);

// tidyStyle keeps only what differs.
check("tidy drops defaults", tidyStyle({ color: "#ffffff", bold: false, outlineOpacity: 0.8 }) === undefined);
check("tidy keeps changes", JSON.stringify(tidyStyle({ color: "#ff0000", bold: false })) === '{"color":"#ff0000"}');

const p0: Project = {
  version: 3,
  id: "p",
  name: "t",
  canvas: { width: 1920, height: 1080, fps: 30 },
  media: [],
  tracks: [],
  texts: [{ id: "t1", text: "Hi", x: 0.5, y: 0.5, size: "m", start: 2, end: 6 }],
  crop: null,
};
const style = (p: Project) => p.texts[0].style;

let p = setTextStyle(p0, "t1", { bold: true, color: "#00ff00" });
check("set style", style(p)?.bold === true && style(p)?.color === "#00ff00", style(p));
check("unchanged is the same project", setTextStyle(p, "t1", { bold: true }) === p);
p = setTextStyle(p, "t1", { bold: false, color: "#ffffff" });
check("back to plain drops the style", style(p) === undefined, style(p));
check("unknown text", setTextStyle(p0, "nope", { bold: true }) === p0);

// Opacity: static, then keyed.
p = setTextOpacity(p0, "t1", 0.4, 1);
check("static opacity", style(p)?.opacity === 0.4 && !style(p)?.opacityKeys, style(p));
p = toggleTextOpacityKey(p, "t1", 0);
check("first key takes the shown value", style(p)?.opacityKeys?.length === 1 && style(p)!.opacityKeys![0].v === 0.4, style(p));
p = setTextOpacity(p, "t1", 1, 2);
check("set while keyed adds a key", style(p)?.opacityKeys?.length === 2, style(p));
check("interpolates", near(textOpacityAt(resolveStyle(style(p)), 1), 0.7), textOpacityAt(resolveStyle(style(p)), 1));
check("before the first key", near(textOpacityAt(resolveStyle(style(p)), -1), 0.4));
check("after the last key", near(textOpacityAt(resolveStyle(style(p)), 9), 1));
p = toggleTextOpacityKey(p, "t1", 2);
p = toggleTextOpacityKey(p, "t1", 0);
check("last key removed leaves its value", !style(p)?.opacityKeys && style(p)?.opacity === 0.4, style(p));

// Presets replace the style but keep the opacity keys.
p = toggleTextOpacityKey(setTextOpacity(p0, "t1", 0.5, 0), "t1", 0);
const neon = BUILTIN_TEXT_PRESETS.find((x) => x.id === "builtin:neon")!;
p = replaceTextStyle(p, "t1", neon.style);
check("preset applied", style(p)?.outlineColor === "#00e5ff" && style(p)?.opacityKeys?.length === 1, style(p));
p = replaceTextStyle(p, "t1", undefined);
check("reset keeps keys only", JSON.stringify(Object.keys(style(p) ?? {})) === '["opacityKeys"]', style(p));
check("builtin ids unique", new Set(BUILTIN_TEXT_PRESETS.map((x) => x.id)).size === BUILTIN_TEXT_PRESETS.length);
check("builtins are tidy", BUILTIN_TEXT_PRESETS.every((x) => JSON.stringify(tidyStyle(x.style) ?? {}) === JSON.stringify(x.style)));

// Migration of the styles saved in localStorage before #125.
{
  const mem = new Map<string, string>();
  const storage = { getItem: (k: string) => mem.get(k) ?? null, removeItem: (k: string) => void mem.delete(k) };
  const docs: Record<string, unknown>[] = [];
  const ok = async (doc: string) => void docs.push(JSON.parse(doc));
  check("migrate: nothing stored", (await migrateLocal(ok, storage)) === 0 && docs.length === 0);
  const ts = Date.UTC(2026, 9, 31, 20).toString(36);
  mem.set(
    "toru.editor.textPresets",
    JSON.stringify([
      { id: `text-${ts}-ab12`, name: "  Neon  ", style: { color: "#e6fffb", bold: false, opacityKeys: [{ t: 0, v: 1, ease: "linear" }] } },
      { id: "weird:id", name: "", style: {} },
      { id: 5, name: "bad" },
    ]),
  );
  // A failing save leaves localStorage for next time.
  let n = 0;
  const flaky = async () => {
    if (n++ === 1) throw new Error("disk full");
  };
  let threw = false;
  await migrateLocal(flaky, storage).catch(() => (threw = true));
  check("migrate: failure keeps localStorage", threw && mem.has("toru.editor.textPresets"));
  check("migrate: count", (await migrateLocal(ok, storage)) === 2, docs);
  check("migrate: cleared", !mem.has("toru.editor.textPresets"));
  const [a, b] = docs;
  check(
    "migrate: first",
    a.kind === "text" && a.version === 1 && a.id === `text-${ts}-ab12` && a.name === "Neon" && JSON.stringify(a.style) === '{"color":"#e6fffb"}' && a.createdAt === "2026-10-31T20:00:00.000Z",
    a,
  );
  check("migrate: bad id gets a new one", b.id === "" && b.name === "My style" && JSON.stringify(b.style) === "{}" && b.createdAt === undefined, b);
  mem.set("toru.editor.textPresets", "not json");
  check("migrate: junk is dropped", (await migrateLocal(ok, storage)) === 0 && !mem.has("toru.editor.textPresets"));
}

if (failed) {
  console.error(`${failed} check(s) failed`);
  process.exit(1);
}
console.log("text style: all checks passed");
