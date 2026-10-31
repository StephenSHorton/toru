import type { Text, TextStyle } from "@/trim/edl";
import { ease } from "@/trim/fx/keyframes";
import { DEFAULT_FONT_CSS, DEFAULT_LINE_HEIGHT, fontFamily, fontInfo, loadFonts } from "@/trim/fonts";

// textRender.ts draws a text overlay (edl.ts Text + TextStyle) into a
// bitmap at the output frame's pixel size. The player shows that bitmap
// scaled (TextOverlays.tsx) and the export composites the same bitmap as a
// PNG (internal/vid/project_text.go), so styled text looks the same in both
// by construction: one rasteriser, one layout.
//
// Layout: the text goes through cleanText (the rules the v1 drawtext export
// used, so emoji are dropped in both), lines are laid out at the font's line
// height × lineSpacing, the block is centred on (x, y) of the frame, and
// each line is aligned left, centre or right inside it. Drawn back to
// front: the box, the shadow (of the outlined text, blurred), the outline
// (outside the glyphs: a stroke under the fill) and the fill.

// TEXT_DEFAULTS is how a text with no style draws: the classic look (white,
// a dark outline 6% of the size, centred).
export const TEXT_DEFAULTS: Required<Omit<TextStyle, "opacityKeys">> = {
  color: "#ffffff",
  fillOpacity: 1,
  outlineColor: "#000000",
  outlineOpacity: 0.8,
  outlineWidth: 0.06,
  shadowColor: "#000000",
  shadowOpacity: 0,
  shadowX: 0.05,
  shadowY: 0.05,
  shadowBlur: 0.08,
  boxColor: "#000000",
  boxOpacity: 0,
  boxPad: 0.3,
  boxRadius: 0,
  bold: false,
  italic: false,
  letterSpacing: 0,
  lineSpacing: 1,
  align: "center",
  opacity: 1,
};

// Limits for each number (the inspector's ranges; the renderer clamps).
export const TEXT_LIMITS: Record<
  "fillOpacity" | "outlineOpacity" | "outlineWidth" | "shadowOpacity" | "shadowX" | "shadowY" | "shadowBlur" | "boxOpacity" | "boxPad" | "boxRadius" | "letterSpacing" | "lineSpacing" | "opacity",
  [number, number]
> = {
  fillOpacity: [0, 1],
  outlineOpacity: [0, 1],
  outlineWidth: [0, 0.3],
  shadowOpacity: [0, 1],
  shadowX: [-0.5, 0.5],
  shadowY: [-0.5, 0.5],
  shadowBlur: [0, 0.5],
  boxOpacity: [0, 1],
  boxPad: [0, 1.5],
  boxRadius: [0, 1],
  letterSpacing: [-0.2, 1],
  lineSpacing: [0.6, 3],
  opacity: [0, 1],
};

export type ResolvedStyle = Required<Omit<TextStyle, "opacityKeys">> & Pick<TextStyle, "opacityKeys">;

const HEX = /^#[0-9a-f]{6}$/i;

// resolveStyle fills in the defaults and clamps every value.
export function resolveStyle(s: TextStyle | undefined): ResolvedStyle {
  const out = { ...TEXT_DEFAULTS, ...(s ?? {}) } as ResolvedStyle;
  for (const [k, [lo, hi]] of Object.entries(TEXT_LIMITS) as [keyof typeof TEXT_LIMITS, [number, number]][]) {
    const v = Number(out[k]);
    out[k] = Number.isFinite(v) ? Math.min(hi, Math.max(lo, v)) : TEXT_DEFAULTS[k];
  }
  for (const k of ["color", "outlineColor", "shadowColor", "boxColor"] as const) if (!HEX.test(String(out[k]))) out[k] = TEXT_DEFAULTS[k];
  if (out.align !== "left" && out.align !== "right") out.align = "center";
  out.bold = !!out.bold;
  out.italic = !!out.italic;
  return out;
}

// --- size and font -----------------------------------------------------------

export const TEXT_SIZES = { s: 0.04, m: 0.06, l: 0.09 } as const;
// Font size limits in output px (Go: MaxTextFontPx).
export const MIN_FONT_PX = 4;
export const MAX_FONT_PX = 1000;

// textFontSize is a text's font size in output pixels: fontPx when set,
// else its preset size (a fraction of the frame's height).
export function textFontSize(t: { size: keyof typeof TEXT_SIZES; fontPx?: number }, frameH: number): number {
  if (t.fontPx && t.fontPx > 0) return Math.min(Math.round(t.fontPx), MAX_FONT_PX);
  return Math.max(1, Math.round((TEXT_SIZES[t.size] ?? TEXT_SIZES.m) * frameH));
}

// textFace is the CSS font a text draws with: its installed font (served
// from the font's own file), or Segoe UI Semibold when it has none or this
// PC lacks it. Bold and italic use the face's own variants when the family
// has them, else the browser's synthesised ones (the same in both, since
// both come from this canvas).
export function textFace(t: Pick<Text, "font">, s: ResolvedStyle) {
  const f = fontInfo(t.font);
  const family = f ? fontFamily(f) : DEFAULT_FONT_CSS;
  const weight = f ? (s.bold ? 700 : 400) : s.bold ? 700 : 600;
  return { family, weight, style: s.italic ? "italic" : "normal", lineHeight: f?.lineHeight ?? DEFAULT_LINE_HEIGHT };
}

// Emoji blocks and the invisible runes that build emoji sequences (Go:
// isEmojiRune). The default font has no glyphs for them.
const EMOJI = /[\u{1F000}-\u{1FAFF}\u{2600}-\u{27BF}\u{2B00}-\u{2BFF}\u{FE00}-\u{FE0F}\u{E0020}-\u{E007F}\u{200D}\u{20E3}]/gu;

// cleanText is the text as it's drawn: CRLF to LF, tabs to spaces, emoji
// dropped, trailing spaces and blank first/last lines trimmed.
export function cleanText(s: string): string {
  const lines = s
    .replace(/\r\n?/g, "\n")
    .replace(/\t/g, " ")
    .replace(EMOJI, "")
    .split("\n")
    .map((l) => l.replace(/ +$/, ""));
  return lines.join("\n").replace(/^\n+|\n+$/g, "");
}

export const hasDroppedChars = (s: string) => s.replace(EMOJI, "") !== s;

// --- opacity over time -------------------------------------------------------

// textOpacityAt is the whole text's opacity at t seconds after its start:
// its keyframes (the ease of the key a segment starts at: fx/keyframes.ts,
// the export's effectfx.Ease) or
// its static opacity.
export function textOpacityAt(s: ResolvedStyle, t: number): number {
  const ks = s.opacityKeys;
  if (!ks?.length) return s.opacity;
  if (!(t > ks[0].t)) return clamp(ks[0].v, 0, 1);
  for (let i = 0; i < ks.length - 1; i++) {
    const a = ks[i];
    const b = ks[i + 1];
    if (t < b.t) {
      if (a.ease === "hold") return clamp(a.v, 0, 1);
      const x = (t - a.t) / (b.t - a.t);
      return clamp(a.v + (b.v - a.v) * ease(a.ease, x), 0, 1);
    }
  }
  return clamp(ks[ks.length - 1].v, 0, 1);
}

// --- render ------------------------------------------------------------------

export type TextBitmap = {
  canvas: HTMLCanvasElement; // the drawn text, cropped to what it covers
  x: number; // where its top-left goes on the frame, output px
  y: number;
  // The text block (without outline, shadow or box) on the frame, output
  // px: the hit and selection box.
  block: { left: number; top: number; width: number; height: number };
};

const clamp = (n: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, n));
const clamp01 = (n: number) => (Number.isFinite(n) ? clamp(n, 0, 1) : 0.5);

function rgba(hex: string, a: number): string {
  const n = parseInt(hex.slice(1), 16);
  return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${a})`;
}

type Ctx = CanvasRenderingContext2D;

function setFont(ctx: Ctx, face: ReturnType<typeof textFace>, px: number, s: ResolvedStyle) {
  ctx.font = `${face.style} ${face.weight} ${px}px ${face.family}`;
  ctx.fontKerning = "normal";
  (ctx as Ctx & { letterSpacing?: string }).letterSpacing = `${s.letterSpacing * px}px`;
  ctx.textBaseline = "alphabetic";
  ctx.textAlign = "left";
}

// renderText draws t on a frameW×frameH output frame; null when it draws
// nothing (blank text, or fully transparent). The bitmap leaves out the
// whole-text opacity, which the player and the export apply over time.
export function renderText(t: Text, frameW: number, frameH: number, placeholder?: string): TextBitmap | null {
  const body = cleanText(t.text) || placeholder || "";
  if (!body.trim() || frameW <= 0 || frameH <= 0) return null;
  const s = resolveStyle(t.style);
  const px = textFontSize(t, frameH);
  const face = textFace(t, s);
  const lines = body.split("\n");

  const probe = document.createElement("canvas").getContext("2d")!;
  setFont(probe, face, px, s);
  const m = probe.measureText("Hg");
  const ascent = m.fontBoundingBoxAscent ?? px * 0.8;
  const descent = m.fontBoundingBoxDescent ?? px * 0.2;
  const lineH = face.lineHeight * s.lineSpacing * px;
  // Letter spacing adds after every letter, the last one too: take it off,
  // so a line is centred on its letters.
  const ls = s.letterSpacing * px;
  const widths = lines.map((l) => (l ? probe.measureText(l).width - ls : 0));
  const blockW = Math.max(1, ...widths);
  const blockH = lines.length * lineH;
  const cx = clamp01(t.x) * frameW;
  const cy = clamp01(t.y) * frameH;
  const left = cx - blockW / 2;
  const top = cy - blockH / 2;

  // How far drawing reaches past the block: outline, shadow (offset and
  // blur), the box, and italic overhang.
  const border = s.outlineOpacity > 0 ? Math.max(1, Math.round(px * s.outlineWidth)) : 0;
  const pad = s.boxOpacity > 0 ? s.boxPad * px : 0;
  const blur = s.shadowOpacity > 0 ? s.shadowBlur * px : 0;
  const sx = s.shadowOpacity > 0 ? s.shadowX * px : 0;
  const sy = s.shadowOpacity > 0 ? s.shadowY * px : 0;
  const reach = border + px * 0.3 + (s.italic ? px * 0.25 : 0);
  const ext = {
    l: Math.max(reach + blur * 2 - sx, reach, pad),
    r: Math.max(reach + blur * 2 + sx, reach, pad),
    t: Math.max(reach + blur * 2 - sy, reach, pad),
    b: Math.max(reach + blur * 2 + sy, reach, pad),
  };
  const x0 = Math.max(0, Math.floor(left - ext.l));
  const y0 = Math.max(0, Math.floor(top - ext.t));
  const x1 = Math.min(frameW, Math.ceil(left + blockW + ext.r));
  const y1 = Math.min(frameH, Math.ceil(top + blockH + ext.b));
  const block = { left, top, width: blockW, height: blockH };
  if (x1 - x0 < 1 || y1 - y0 < 1) return null;

  const out = document.createElement("canvas");
  out.width = x1 - x0;
  out.height = y1 - y0;
  const ctx = out.getContext("2d")!;
  ctx.translate(-x0, -y0);

  if (s.boxOpacity > 0) {
    ctx.fillStyle = rgba(s.boxColor, s.boxOpacity);
    const r = Math.min(s.boxRadius * px, (blockH + 2 * pad) / 2, (blockW + 2 * pad) / 2);
    ctx.beginPath();
    ctx.roundRect(left - pad, top - pad, blockW + 2 * pad, blockH + 2 * pad, r);
    ctx.fill();
  }

  // The outlined text on its own layer, so the shadow is cast once by the
  // whole shape (not darker where outline and fill overlap).
  const layer = document.createElement("canvas");
  layer.width = out.width;
  layer.height = out.height;
  const lc = layer.getContext("2d")!;
  lc.translate(-x0, -y0);
  setFont(lc, face, px, s);
  lc.lineJoin = "round";
  lc.miterLimit = 2;
  lines.forEach((line, i) => {
    if (!line) return;
    const w = widths[i];
    const lx = s.align === "left" ? left : s.align === "right" ? left + blockW - w : cx - w / 2;
    const base = top + i * lineH + (lineH - (ascent + descent)) / 2 + ascent;
    if (border > 0) {
      // Centred on the outline, under the fill: border px show outside.
      lc.strokeStyle = rgba(s.outlineColor, s.outlineOpacity);
      lc.lineWidth = border * 2;
      lc.strokeText(line, lx, base);
    }
    if (s.fillOpacity > 0) {
      // The fill cuts the stroke away where they overlap, so a see-through
      // fill shows what's behind the text, not the outline's inner half.
      lc.globalCompositeOperation = "destination-out";
      lc.fillStyle = "#000";
      lc.fillText(line, lx, base);
      lc.globalCompositeOperation = "source-over";
      lc.fillStyle = rgba(s.color, s.fillOpacity);
      lc.fillText(line, lx, base);
    }
  });

  ctx.setTransform(1, 0, 0, 1, 0, 0);
  if (s.shadowOpacity > 0) {
    ctx.save();
    ctx.shadowColor = rgba(s.shadowColor, s.shadowOpacity);
    ctx.shadowBlur = blur;
    ctx.shadowOffsetX = sx;
    ctx.shadowOffsetY = sy;
    ctx.drawImage(layer, 0, 0);
    ctx.restore();
  } else {
    ctx.drawImage(layer, 0, 0);
  }
  return { canvas: out, x: x0, y: y0, block };
}

// textFontsReady waits for t's font file to load, so a bitmap drawn now is
// in its font (the first draw can otherwise come out in the fallback).
export async function textFontsReady(texts: Pick<Text, "font" | "style">[]): Promise<void> {
  await loadFonts().catch(() => {});
  await Promise.all(
    texts.map((t) => {
      const face = textFace(t, resolveStyle(t.style));
      return document.fonts.load(`${face.style} ${face.weight} 40px ${face.family}`).catch(() => []);
    }),
  );
}

// textPng is t's bitmap as the export takes it: a PNG data URL, where it
// goes on the frame, and the whole-text opacity (static or keyframed).
export type TextImageJSON = { png: string; x: number; y: number; w: number; h: number };
export function textPng(t: Text, frameW: number, frameH: number): TextImageJSON | null {
  const bmp = renderText(t, frameW, frameH);
  if (!bmp) return null;
  return { png: bmp.canvas.toDataURL("image/png"), x: bmp.x, y: bmp.y, w: bmp.canvas.width, h: bmp.canvas.height };
}
