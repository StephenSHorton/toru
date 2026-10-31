// effects-parity checks that the preview's effects (src/trim/fx/renderer.ts,
// WebGL2) match the export's (internal/fx StackGraph and, for v2 clips,
// internal/vid/effects.go, through ffmpeg).
// The cases come from internal/vid/effects_parity_test.go: the v2 cases, a
// few hand-picked v3 stacks, and cases generated from every effect
// definition in internal/fx/defs (default, each param's min and max, enum
// options, seeded random sets, half intensity, keyframes resolved at a
// time), so a new effect is checked without touching this script. For each
// it renders one frame with green, near-green, white and black boxes on it
// through ffmpeg with the graph the export builds, and through the renderer
// in headless Edge, and compares them with the def's parity limits.
//
//   bun scripts/effects-parity.ts      renders the export with the pinned
//                                      LGPL ffmpeg (downloaded once, see
//                                      internal/fx/fxtest); FX_FFMPEG=ffmpeg
//                                      uses the one on PATH instead
//   FX_PARITY_KEEP=1                   keeps the frames (raw RGBA)
//   FX_PARITY_ONLY=<substring>         runs only matching cases
import fs from "node:fs";
import path from "node:path";
import { compare, ffmpegFor, goBin, openPage, RENDER_PAGE, run, tempDir, type Limits } from "./fx-edge";

// v2 cases have no limits of their own; they keep v2's.
const V2_LIMITS: Limits = { mode: "pixels", mean: 1, p99: 6 };

const W = 320;
const tmp = tempDir("fx-parity");
const ffmpeg = ffmpegFor(tmp);

type Effect = { id: string; type: string; enabled: boolean; params: Record<string, unknown>; keys?: Record<string, { t: number; v: unknown; ease: string }[]>; amount?: number; group?: string };

// 1. The cases and their ffmpeg graphs, from the Go side.
run(goBin, ["test", "./internal/vid/", "-run", "^TestWriteEffectsParityCases$", "-count=1"], { TORU_FX_PARITY_DIR: tmp });
const all = JSON.parse(fs.readFileSync(path.join(tmp, "cases.json"), "utf8")) as {
  height: number;
  // v2 cases carry chroma/filters and a -vf chain; v3 cases an effect stack
  // (as saved: keyframe times in ms) and the -filter_complex graph
  // StackGraph builds ([0:v] → [fxout]).
  cases: { name: string; chroma?: unknown; filters?: unknown; filter: string; effects?: Effect[]; graph?: string; t?: number; limits?: Limits; border: number }[];
};
const H = all.height;
const only = process.env.FX_PARITY_ONLY;
const cases = all.cases.filter((c) => !only || c.name.includes(only));

// The first case is the editor's default green screen; both sides must agree
// on what that is.
const { DEFAULT_CHROMA } = await import("../src/trim/project");
if (JSON.stringify(all.cases[0].chroma) !== JSON.stringify(DEFAULT_CHROMA)) {
  throw new Error(`Go DefaultChroma ${JSON.stringify(all.cases[0].chroma)} != project.ts DEFAULT_CHROMA ${JSON.stringify(DEFAULT_CHROMA)}`);
}

// 2. The source frame: test pattern, a pure green box, a near-green box (so
// the soft key edge shows), and white and black boxes (which a green screen
// must leave opaque), as PNG and as raw RGBA.
const src = path.join(tmp, "src.png");
const WHITE_BOX = { x: 250, y: 120, w: 50, h: 40 };
run(ffmpeg, [
  "-v", "error", "-y", "-f", "lavfi", "-i", `testsrc2=s=${W}x${H}:r=1:d=1`,
  "-vf", "drawbox=x=40:y=30:w=110:h=80:c=0x00FF00:t=fill,drawbox=x=170:y=40:w=70:h=60:c=0x3CC850:t=fill," +
    `drawbox=x=${WHITE_BOX.x}:y=${WHITE_BOX.y}:w=${WHITE_BOX.w}:h=${WHITE_BOX.h}:c=0xFFFFFF:t=fill,drawbox=x=40:y=125:w=60:h=40:c=0x000000:t=fill`,
  "-frames:v", "1", src,
]);
const srcRGBA = run(ffmpeg, ["-v", "error", "-i", src, "-f", "rawvideo", "-pix_fmt", "rgba", "-"]);

// 3. What the export draws.
const exportFrame = (c: (typeof cases)[number]) =>
  c.graph
    ? run(ffmpeg, ["-v", "error", "-i", src, "-filter_complex", `${c.graph};[fxout]format=rgba[o]`, "-map", "[o]", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "-"])
    : run(ffmpeg, ["-v", "error", "-i", src, "-vf", (c.filter ? c.filter + "," : "") + "format=rgba", "-frames:v", "1", "-f", "rawvideo", "-pix_fmt", "rgba", "-"]);

// The editor's stack: keyframe times in seconds (the save writes ms).
const toEditor = (es: Effect[]) =>
  es.map((e) => (e.keys ? { ...e, keys: Object.fromEntries(Object.entries(e.keys).map(([p, ks]) => [p, ks.map((k) => ({ ...k, t: k.t / 1000 }))])) } : e));

// 4. What the preview draws, and 5. compare.
const page = await openPage(tmp, RENDER_PAGE, { "/src": srcRGBA });
let failed = 0;
let skipped = 0;
try {
  console.log(`effects parity, ${W}x${H}, ${cases.length} cases, export by ${ffmpeg}`);
  for (const [i, c] of cases.entries()) {
    const lim = c.limits ?? V2_LIMITS;
    if (lim.mode === "none") {
      skipped++;
      console.log(`skip ${c.name.padEnd(40)} ${lim.reason ?? ""}`);
      continue;
    }
    const fx = c.effects ? { effects: toEditor(c.effects) } : { chroma: c.chroma, filters: c.filters };
    const got = await page.evaluate(`render(${W}, ${H}, ${JSON.stringify(fx)}, ${c.t ?? 0})`);
    if (i === 0) console.log(`renderer: ${got.renderer}`);
    const preview = Buffer.from(got.pixels, "base64");
    const exp = exportFrame(c);
    const r = compare(exp, preview, W, H, c.border, lim);
    let ok = r.ok;
    const green = c.chroma || c.effects?.some((e) => e.type === "chromaKey" && JSON.stringify(e.params) === "{}");
    if (green) {
      // A default green screen leaves white opaque, in the export and the preview.
      const o = ((WHITE_BOX.y + WHITE_BOX.h / 2) * W + WHITE_BOX.x + WHITE_BOX.w / 2) * 4 + 3;
      if (exp[o] !== 255 || preview[o] !== 255) {
        console.log(`     white box alpha: export ${exp[o]}, preview ${preview[o]} (want 255)`);
        ok = false;
      }
    }
    if (!ok) failed++;
    const unit = lim.mode === "stats" ? ["Δmean", "Δstd "] : ["mean ", "p99  "];
    console.log(
      `${ok ? "ok  " : "FAIL"} ${c.name.padEnd(40)} ${unit[0]} ${r.mean.toFixed(2).padStart(5)}  ${unit[1]} ${r.p99.toFixed(1).padStart(5)}  max ${r.max.toFixed(1).padStart(5)}` +
        (ok ? "" : `  (limits ${lim.mean}/${lim.p99})`),
    );
    if (process.env.FX_PARITY_KEEP) {
      fs.writeFileSync(path.join(tmp, `${i}-export.rgba`), exp);
      fs.writeFileSync(path.join(tmp, `${i}-preview.rgba`), preview);
    }
  }
} finally {
  await page.close();
  if (!process.env.FX_PARITY_KEEP) fs.rmSync(tmp, { recursive: true, force: true });
  else console.log(`kept ${tmp}`);
}
console.log(failed ? `${failed} case(s) failed` : `all ${cases.length - skipped} cases match${skipped ? ` (${skipped} skipped: parity "none")` : ""}`);
process.exit(failed ? 1 : 0);
