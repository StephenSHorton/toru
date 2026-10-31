import type { RGB } from "@/trim/fx/lut";
import type { Step } from "@/trim/fx/shader";
import type { Media } from "@/trim/project";

// colorspace.ts makes the preview decode video with the same YUV matrix as
// the export.
//
// The browser (WebView2 / Chromium) ignores a video's matrix tag and picks
// one by frame height: BT.709 from 720 lines up, BT.601 below (measured:
// 718 vs 720 lines, any width). ffmpeg, which the export runs on, follows
// the tag and treats an untagged video as BT.601 at any size. Toru's
// recordings are tagged bt470bg (BT.601) and are HD, so without this the
// preview decodes them as BT.709: reds pick up green (254,0,0 → 255,24,0)
// and greens darken (1,128,1 → 0,109,0).
//
// Two fixes, never both on one frame:
//   exactFrame      the frame's own YUV planes, re-tagged with the source's
//                   matrix and decoded again: exact. Costs a copy (~5 ms a
//                   1080p frame), so the compositor uses it for a paused
//                   frame (and what editor.frame / picks read), and the
//                   thumbnails for their frame.
//   correctionStep  the difference as an RGB matrix, run as the clip's first
//                   colour step (it folds into the effect LUT): free, used
//                   while playing. Within 2 levels except on saturated
//                   primaries, which the browser has already clipped.

export type Matrix = "601" | "709";

// browserMatrix is the matrix the browser decodes a frame of h lines with.
export const browserMatrix = (h: number): Matrix => (h >= 720 ? "709" : "601");

// matrixOfTag is the matrix ffmpeg decodes a video with, by its ffprobe
// color_space tag ("" = untagged); null for one this doesn't correct
// (BT.2020, SMPTE 240M, ...).
export function matrixOfTag(tag: string): Matrix | null {
  switch (tag) {
    case "":
    case "bt470bg":
    case "smpte170m":
      return "601";
    case "bt709":
      return "709";
    default:
      return null;
  }
}

// Tags by path, from VideoService.ProbeMedia: null when the probe failed.
const tags = new Map<string, string | null>();

function probe(path: string) {
  if (tags.has(path)) return;
  tags.set(path, null);
  // Imported lazily, so this module loads without the Wails runtime (checks).
  void import("@/lib/api")
    .then(({ VideoService }) => VideoService.ProbeMedia(path))
    .then((info) => tags.set(path, info.colorSpace ?? ""))
    .catch(() => tags.set(path, null));
}

// sourceMatrix is the matrix the export decodes media with: undefined while
// its probe runs, null when there's nothing to match (not video, a failed
// probe, an uncorrected tag). A media without a path (the dev sample) counts
// as untagged.
export function sourceMatrix(m: Media | undefined): Matrix | null | undefined {
  if (!m || m.kind !== "video") return null;
  if (!m.path) return matrixOfTag("");
  if (!tags.has(m.path)) {
    probe(m.path);
    return undefined;
  }
  const tag = tags.get(m.path);
  return tag == null ? null : matrixOfTag(tag);
}

export type MatrixFix = { from: Matrix; to: Matrix };

// matrixFix is what a frame of h lines from media needs: from = the
// browser's matrix, to = the source's; null when they agree or aren't known.
export function matrixFix(m: Media | undefined, h: number): MatrixFix | null {
  const to = sourceMatrix(m);
  if (!to || h <= 0) return null;
  const from = browserMatrix(h);
  return from === to ? null : { from, to };
}

// --- RGB correction --------------------------------------------------------------

const K: Record<Matrix, [number, number]> = { "601": [0.299, 0.114], "709": [0.2126, 0.0722] };

// encode: R'G'B' → Y'PbPr; decode: the inverse.
function encode(m: Matrix): number[][] {
  const [kr, kb] = K[m];
  const kg = 1 - kr - kb;
  return [
    [kr, kg, kb],
    [-kr / (2 * (1 - kb)), -kg / (2 * (1 - kb)), 0.5],
    [0.5, -kg / (2 * (1 - kr)), -kb / (2 * (1 - kr))],
  ];
}
function decode(m: Matrix): number[][] {
  const [kr, kb] = K[m];
  const kg = 1 - kr - kb;
  return [
    [1, 0, 2 * (1 - kr)],
    [1, (-2 * kb * (1 - kb)) / kg, (-2 * kr * (1 - kr)) / kg],
    [1, 2 * (1 - kb), 0],
  ];
}
const mul = (a: number[][], b: number[][]) => a.map((row) => b[0].map((_, j) => row.reduce((s, v, k) => s + v * b[k][j], 0)));

// correctionMatrix turns a frame decoded with fix.from into one decoded with
// fix.to (both limited range, so the offsets cancel). Row-major 3×3.
export function correctionMatrix(fix: MatrixFix): number[][] {
  return mul(decode(fix.to), encode(fix.from));
}

const clamp01 = (n: number) => (n < 0 ? 0 : n > 1 ? 1 : n);

// correctionStep is the correction as a colour step, for the renderer.
export function correctionStep(fix: MatrixFix): Step {
  const m = correctionMatrix(fix);
  return {
    kind: "color",
    key: `yuvfix:${fix.from}>${fix.to}`,
    fn: (c: RGB): RGB => [
      clamp01(m[0][0] * c[0] + m[0][1] * c[1] + m[0][2] * c[2]),
      clamp01(m[1][0] * c[0] + m[1][1] * c[1] + m[1][2] * c[2]),
      clamp01(m[2][0] * c[0] + m[2][1] * c[1] + m[2][2] * c[2]),
    ],
  };
}

// correctPixels applies the correction to RGBA bytes in place (a small
// canvas, when exactFrame isn't available).
export function correctPixels(px: Uint8ClampedArray, fix: MatrixFix) {
  const m = correctionMatrix(fix);
  for (let i = 0; i < px.length; i += 4) {
    const r = px[i];
    const g = px[i + 1];
    const b = px[i + 2];
    px[i] = m[0][0] * r + m[0][1] * g + m[0][2] * b;
    px[i + 1] = m[1][0] * r + m[1][1] * g + m[1][2] * b;
    px[i + 2] = m[2][0] * r + m[2][1] * g + m[2][2] * b;
  }
}

// --- Exact decode ----------------------------------------------------------------

// exactFrame is the video's current frame decoded with matrix `to`: its YUV
// planes copied out and wrapped in a new VideoFrame tagged with that matrix
// (primaries and transfer stay BT.709, so nothing else is converted). null
// when the browser can't hand over the planes; the caller closes the frame.
export async function exactFrame(v: HTMLVideoElement, to: Matrix): Promise<VideoFrame | null> {
  if (typeof VideoFrame === "undefined") return null;
  let vf: VideoFrame | undefined;
  try {
    vf = new VideoFrame(v);
    const format = vf.format;
    const rect = vf.visibleRect;
    if (!format || !rect || !(format === "NV12" || format.startsWith("I4"))) return null;
    const buf = new Uint8Array(vf.allocationSize());
    const layout = await vf.copyTo(buf);
    return new VideoFrame(buf, {
      format,
      codedWidth: rect.width,
      codedHeight: rect.height,
      layout,
      timestamp: vf.timestamp,
      colorSpace: { matrix: to === "601" ? "smpte170m" : "bt709", primaries: "bt709", transfer: "bt709", fullRange: false },
    });
  } catch {
    return null;
  } finally {
    vf?.close();
  }
}
