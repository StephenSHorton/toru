// text-style-check.ts checks the text style model (src/trim/textStyle.ts,
// textRender.ts resolveStyle/textOpacityAt): defaults and clamping, edits
// keeping only what differs from the plain look, opacity keyframes, presets.
// The drawing itself is checked against the export in a browser (PR notes).
// Run: bun scripts/text-style-check.ts
import { resolveStyle, textOpacityAt, TEXT_DEFAULTS } from "../src/trim/textRender";
import { BUILTIN_TEXT_PRESETS, replaceTextStyle, setTextOpacity, setTextStyle, tidyStyle, toggleTextOpacityKey } from "../src/trim/textStyle";
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

if (failed) {
  console.error(`${failed} check(s) failed`);
  process.exit(1);
}
console.log("text style: all checks passed");
