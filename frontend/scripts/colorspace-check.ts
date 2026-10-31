// colorspace-check: the preview's colour-matrix correction (src/trim/colorspace.ts)
// against values measured in the browser and ffmpeg 9 (bars of known colours,
// BT.601 limited range, 1920×1080 and 640×480).
import { browserMatrix, correctionMatrix, matrixFix, matrixOfTag } from "../src/trim/colorspace";
import type { Media } from "../src/trim/project";

let failed = 0;
const check = (name: string, ok: boolean, got?: unknown) => {
  if (!ok) {
    failed++;
    console.error(`FAIL ${name}`, got ?? "");
  }
};

// The browser's decode of BT.601 bars at 1080 lines (as BT.709) and ffmpeg's.
const browser709 = [[255, 25, 0], [0, 109, 0], [211, 57, 204], [128, 128, 128], [225, 173, 137], [16, 47, 207], [243, 219, 31]];
const ffmpeg601 = [[252, 0, 0], [0, 127, 0], [198, 29, 198], [128, 128, 128], [219, 169, 138], [20, 39, 199], [238, 228, 38]];
// Saturated primaries are clipped by the browser before the correction (the
// exact path covers those); everything else lands within 2 levels.
const limit = [18, 10, 2, 0, 2, 2, 2];
const m = correctionMatrix({ from: "709", to: "601" });
browser709.forEach((c, i) => {
  const o = m.map((row) => Math.round(Math.max(0, Math.min(255, row[0] * c[0] + row[1] * c[1] + row[2] * c[2]))));
  const err = Math.max(...o.map((v, k) => Math.abs(v - ffmpeg601[i][k])));
  check(`709→601 bar ${i}`, err <= limit[i], { got: o, want: ffmpeg601[i], err });
});
// Grey stays grey, both ways; the two corrections undo each other.
for (const fix of [{ from: "709", to: "601" }, { from: "601", to: "709" }] as const) {
  const k = correctionMatrix(fix);
  check(`${fix.from}→${fix.to} rows sum to 1`, k.every((r) => Math.abs(r[0] + r[1] + r[2] - 1) < 1e-9), k);
}
const a = correctionMatrix({ from: "709", to: "601" });
const b = correctionMatrix({ from: "601", to: "709" });
const ab = a.map((r) => b[0].map((_, j) => r.reduce((s, v, k) => s + v * b[k][j], 0)));
check("corrections are inverses", ab.every((r, i) => r.every((v, j) => Math.abs(v - (i === j ? 1 : 0)) < 1e-9)), ab);

// Which frames need which correction (browser by height, ffmpeg by tag).
check("browser 720 = 709", browserMatrix(720) === "709" && browserMatrix(718) === "601");
check("tags", matrixOfTag("bt470bg", 1080) === "601" && matrixOfTag("smpte170m", 480) === "601" && matrixOfTag("bt709", 480) === "709" && matrixOfTag("bt2020nc", 1080) === null);
check("untagged by height", matrixOfTag("", 1080) === "709" && matrixOfTag("", 720) === "709" && matrixOfTag("", 718) === "601");
const dev = { id: "d", path: "", url: "", kind: "video", duration: 1, width: 1920, height: 1080, fps: 30, hasAudio: false, tracks: [] } as Media;
// Untagged video follows the browser's own rule: never corrected.
check("untagged HD → none", matrixFix(dev, 1080) === null, matrixFix(dev, 1080));
check("untagged SD → none", matrixFix({ ...dev, height: 480 }, 480) === null, matrixFix({ ...dev, height: 480 }, 480));
check("image → none", matrixFix({ ...dev, kind: "image" }, 1080) === null);

if (failed) {
  console.error(`${failed} check(s) failed`);
  process.exit(1);
}
console.log("colorspace: all checks passed");
