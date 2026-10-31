import { useCallback, useState } from "react";
import type { Rect } from "@/lib/contract";

// The video editor's edit decision list (EDL). Pure data and pure functions:
// every op returns a NEW Edl (or the same object when it changes nothing, so
// history can skip no-ops), never mutates. See docs/video-editor.md.
//
// Times here are SECONDS. Segment times are SOURCE time (where in that file);
// text times are OUTPUT time (where in the exported video). The output is the
// segments played back to back in array order. toRequest converts to the Go
// EditRequest, which is in milliseconds.

export type Source = { path: string; url: string; duration: number; width: number; height: number };
export type Seg = { id: string; source: number; start: number; end: number }; // seconds, source time
// size is the older preset (4/6/9% of the output height), used when fontPx
// is unset; font is an installed font's name (fonts.ts), "" or unset for the
// default (Segoe UI Semibold).
export type Text = {
  id: string;
  text: string;
  x: number;
  y: number;
  size: "s" | "m" | "l";
  fontPx?: number;
  font?: string;
  start: number;
  end: number; // output time
  style?: TextStyle; // unset fields: the classic look (textRender.ts TEXT_DEFAULTS)
};

// TextStyle is how a text is drawn (trim/textRender.ts). Lengths are
// fractions of the font size, so a style looks the same at any size;
// colours are "#rrggbb" with a separate opacity 0..1.
export type TextStyle = {
  color?: string; // fill
  fillOpacity?: number;
  outlineColor?: string;
  outlineOpacity?: number; // 0 = no outline
  outlineWidth?: number; // fraction of the font size, outside the glyphs
  shadowColor?: string;
  shadowOpacity?: number; // 0 = no shadow
  shadowX?: number;
  shadowY?: number;
  shadowBlur?: number;
  boxColor?: string;
  boxOpacity?: number; // 0 = no box
  boxPad?: number;
  boxRadius?: number;
  bold?: boolean;
  italic?: boolean;
  letterSpacing?: number; // fraction of the font size between letters
  lineSpacing?: number; // multiplies the font's line height
  align?: "left" | "center" | "right";
  opacity?: number; // the whole text, 0..1
  // Keyframes on the whole text's opacity, t in seconds from the text's start.
  opacityKeys?: { t: number; v: number; ease: "linear" | "in" | "out" | "inOut" | "hold" }[];
};
export type Edl = { sources: Source[]; segs: Seg[]; texts: Text[] }; // segs in output order

// Local mirror of internal/vid EditRequest (card #2) until its bindings land;
// then import the generated models from "@/lib/api" instead.
export interface EditSegment {
  source: number;
  startMs: number;
  endMs: number;
}
export interface TrackLevel {
  source: number;
  index: number;
  volume: number; // percent, 0..200
  muted: boolean;
}
export interface TextOverlay {
  text: string;
  x: number;
  y: number;
  size: "s" | "m" | "l";
  fontPx?: number;
  font?: string;
  startMs: number;
  endMs: number;
}
export type Preset = "original" | "1080p" | "720p" | "discord" | "custom";
export interface EditRequest {
  sources: string[];
  segments: EditSegment[];
  crop?: Rect | null;
  tracks?: TrackLevel[];
  texts?: TextOverlay[];
  preset: Preset;
  height?: number;
  kbps?: number;
  outPath: string;
}

// Shortest segment split / setEdge will leave (s). Anything shorter is a
// mis-click, and ffmpeg's trim needs at least a frame.
export const MIN_SEG = 0.1;
// Times closer than this are the same time (float noise from drags).
const EPS = 1e-6;

const clamp = (n: number, lo: number, hi: number) => Math.max(lo, Math.min(hi, n));
const segLen = (s: Seg) => s.end - s.start;

let idSeq = 0;
export const newId = (prefix: string) =>
  typeof crypto !== "undefined" && "randomUUID" in crypto ? `${prefix}-${crypto.randomUUID()}` : `${prefix}-${Date.now()}-${++idSeq}`;

export function initEdl(first: Source): Edl {
  return { sources: [first], segs: [{ id: newId("seg"), source: 0, start: 0, end: first.duration }], texts: [] };
}

// addSource appends the source and one segment covering the whole of it.
export function addSource(edl: Edl, src: Source): Edl {
  const source = edl.sources.length;
  return {
    ...edl,
    sources: [...edl.sources, src],
    segs: [...edl.segs, { id: newId("seg"), source, start: 0, end: src.duration }],
  };
}

// split cuts the segment under outT in two. The left part keeps the segment's
// id. No-op when outT is outside the timeline or within MIN_SEG of an edge.
export function split(edl: Edl, outT: number): Edl {
  const hit = locate(edl, outT);
  if (!hit) return edl;
  const { seg, srcT } = hit;
  if (srcT - seg.start < MIN_SEG || seg.end - srcT < MIN_SEG) return edl;
  const i = edl.segs.indexOf(seg);
  const segs = edl.segs.slice();
  segs.splice(i, 1, { ...seg, end: srcT }, { id: newId("seg"), source: seg.source, start: srcT, end: seg.end });
  return { ...edl, segs };
}

// remove deletes a segment. The last segment can't go (an export needs one).
// Texts after it move back with the content; texts over it are clipped.
export function remove(edl: Edl, segId: string): Edl {
  const i = edl.segs.findIndex((s) => s.id === segId);
  if (i < 0 || edl.segs.length <= 1) return edl;
  const at = outStart(edl, i);
  const len = segLen(edl.segs[i]);
  const segs = edl.segs.filter((s) => s.id !== segId);
  const texts = edl.texts.map((t) => ({ ...t, start: cutTime(t.start, at, len), end: cutTime(t.end, at, len) }));
  return clampTexts({ ...edl, segs, texts });
}

// move puts a segment at toIndex (clamped) in output order. Texts stay at
// their output times.
export function move(edl: Edl, segId: string, toIndex: number): Edl {
  const i = edl.segs.findIndex((s) => s.id === segId);
  if (i < 0) return edl;
  const to = clamp(Math.round(toIndex), 0, edl.segs.length - 1);
  if (to === i) return edl;
  const segs = edl.segs.slice();
  const [seg] = segs.splice(i, 1);
  segs.splice(to, 0, seg);
  return { ...edl, segs };
}

// setEdge moves a segment's start or end to source time srcT, kept inside the
// source and at least MIN_SEG long. Segments may overlap in source time (the
// same moment can appear twice). Texts after the segment move with it.
export function setEdge(edl: Edl, segId: string, edge: "start" | "end", srcT: number): Edl {
  const i = edl.segs.findIndex((s) => s.id === segId);
  if (i < 0) return edl;
  const seg = edl.segs[i];
  const dur = edl.sources[seg.source]?.duration ?? seg.end;
  const next =
    edge === "start"
      ? { ...seg, start: clamp(srcT, 0, Math.max(0, seg.end - MIN_SEG)) }
      : { ...seg, end: clamp(srcT, Math.min(dur, seg.start + MIN_SEG), dur) };
  if (Math.abs(next.start - seg.start) < EPS && Math.abs(next.end - seg.end) < EPS) return edl;
  const after = outStart(edl, i) + segLen(seg);
  const delta = segLen(next) - segLen(seg);
  const segs = edl.segs.slice();
  segs[i] = next;
  const texts = edl.texts.map((t) => ({
    ...t,
    start: t.start >= after - EPS ? t.start + delta : t.start,
    end: t.end >= after - EPS ? t.end + delta : t.end,
  }));
  return clampTexts({ ...edl, segs, texts });
}

export function outputDuration(edl: Edl): number {
  return edl.segs.reduce((sum, s) => sum + segLen(s), 0);
}

// locate maps an output time to the segment playing then and the source time
// in it. A time exactly on a boundary belongs to the segment that starts
// there; the very end of the timeline maps to the end of the last segment.
// null when outT is outside [0, outputDuration] or there are no segments.
export function locate(edl: Edl, outT: number): { seg: Seg; srcT: number } | null {
  if (!edl.segs.length || outT < -EPS) return null;
  let acc = 0;
  for (const seg of edl.segs) {
    const len = segLen(seg);
    if (outT < acc + len - EPS) return { seg, srcT: seg.start + Math.max(0, outT - acc) };
    acc += len;
  }
  if (outT > acc + EPS) return null;
  const last = edl.segs[edl.segs.length - 1];
  return { seg: last, srcT: last.end };
}

// toOutput maps a source time inside a segment to output time (clamped to the
// segment). -1 when the segment isn't in the EDL.
export function toOutput(edl: Edl, segId: string, srcT: number): number {
  const i = edl.segs.findIndex((s) => s.id === segId);
  if (i < 0) return -1;
  const seg = edl.segs[i];
  return outStart(edl, i) + clamp(srcT - seg.start, 0, segLen(seg));
}

export function upsertText(edl: Edl, t: Text): Edl {
  const i = edl.texts.findIndex((x) => x.id === t.id);
  const texts = edl.texts.slice();
  if (i < 0) texts.push(t);
  else texts[i] = t;
  return clampTexts({ ...edl, texts });
}

export function removeText(edl: Edl, id: string): Edl {
  if (!edl.texts.some((t) => t.id === id)) return edl;
  return { ...edl, texts: edl.texts.filter((t) => t.id !== id) };
}

// newText makes a text overlay for upsertText: centred, medium, 3s from outT.
export function newText(edl: Edl, outT: number, text = "Text"): Text {
  const dur = outputDuration(edl);
  const start = clamp(outT, 0, Math.max(0, dur - MIN_SEG));
  return { id: newId("txt"), text, x: 0.5, y: 0.5, size: "m", start, end: Math.min(dur, start + 3) };
}

// toRequest builds the Go EditRequest (milliseconds). Empty or blank texts and
// texts with no time left on screen are dropped.
export function toRequest(
  edl: Edl,
  extra: { crop?: Rect | null; tracks?: TrackLevel[]; preset: Preset; height?: number; kbps?: number; outPath?: string },
): EditRequest {
  const ms = (s: number) => Math.round(s * 1000);
  const segments = edl.segs.map((s) => {
    const startMs = ms(s.start);
    return { source: s.source, startMs, endMs: Math.max(startMs + 1, ms(s.end)) };
  });
  const texts = edl.texts
    .filter((t) => t.text.trim() && ms(t.end) > ms(t.start))
    .map((t) => ({
      text: t.text,
      x: t.x,
      y: t.y,
      size: t.size,
      ...(t.fontPx ? { fontPx: t.fontPx } : {}),
      ...(t.font ? { font: t.font } : {}),
      startMs: ms(t.start),
      endMs: ms(t.end),
    }));
  const req: EditRequest = {
    sources: edl.sources.map((s) => s.path),
    segments,
    preset: extra.preset,
    outPath: extra.outPath ?? "",
  };
  if (extra.crop) req.crop = extra.crop;
  if (extra.tracks?.length) req.tracks = extra.tracks;
  if (texts.length) req.texts = texts;
  if (extra.preset === "custom") {
    if (extra.height) req.height = extra.height;
    if (extra.kbps) req.kbps = extra.kbps;
  }
  return req;
}

// Output time at which segs[i] starts.
function outStart(edl: Edl, i: number): number {
  let acc = 0;
  for (let k = 0; k < i; k++) acc += segLen(edl.segs[k]);
  return acc;
}

// Where output time t lands after removing [at, at+len).
function cutTime(t: number, at: number, len: number): number {
  if (t <= at) return t;
  if (t >= at + len) return t - len;
  return at;
}

// Keep every text inside [0, outputDuration] with end >= start. A text that
// ends up with no length is kept (undo can bring it back) but toRequest drops it.
function clampTexts(edl: Edl): Edl {
  const dur = outputDuration(edl);
  const texts = edl.texts.map((t) => {
    const start = clamp(t.start, 0, dur);
    const end = clamp(t.end, start, dur);
    return start === t.start && end === t.end ? t : { ...t, start, end };
  });
  return { ...edl, texts };
}

// --- Undo / redo ---------------------------------------------------------

// History is the pure core of useEdlHistory (and v2's useProjectHistory in
// project.ts), exported for tests. T is the edited document.
export type History<T = Edl> = { past: T[]; present: T; future: T[]; key: string | null };

const MAX_HISTORY = 200;

export function historyInit<T>(init: T): History<T> {
  return { past: [], present: init, future: [], key: null };
}

// historyApply records fn(present) as one undo step. Consecutive applies with
// the same non-empty key merge into one step, so a drag that calls apply on
// every pointer move undoes in one go: pass e.g. `edge:${segId}:${dragId}`.
export function historyApply<T>(h: History<T>, fn: (e: T) => T, key?: string): History<T> {
  const next = fn(h.present);
  if (next === h.present) return h;
  if (key && key === h.key) return { ...h, present: next, future: [] };
  return { past: [...h.past, h.present].slice(-MAX_HISTORY), present: next, future: [], key: key ?? null };
}

export function historyUndo<T>(h: History<T>): History<T> {
  if (!h.past.length) return h;
  return { past: h.past.slice(0, -1), present: h.past[h.past.length - 1], future: [h.present, ...h.future], key: null };
}

export function historyRedo<T>(h: History<T>): History<T> {
  if (!h.future.length) return h;
  return { past: [...h.past, h.present], present: h.future[0], future: h.future.slice(1), key: null };
}

// useHistory holds a document with undo/redo. reset replaces it and clears
// history (e.g. once the first source has loaded).
export function useHistory<T>(init: T) {
  const [h, setH] = useState(() => historyInit(init));
  const apply = useCallback((fn: (e: T) => T, key?: string) => setH((cur) => historyApply(cur, fn, key)), []);
  const undo = useCallback(() => setH(historyUndo), []);
  const redo = useCallback(() => setH(historyRedo), []);
  const reset = useCallback((doc: T) => setH(historyInit(doc)), []);
  // patchAll changes every state in the history the same way, without an
  // undo step (a rename from outside shouldn't come back on undo).
  const patchAll = useCallback(
    (fn: (e: T) => T) => setH((cur) => ({ ...cur, past: cur.past.map(fn), present: fn(cur.present), future: cur.future.map(fn) })),
    [],
  );
  return { doc: h.present, apply, undo, redo, reset, patchAll, canUndo: h.past.length > 0, canRedo: h.future.length > 0 };
}

export function useEdlHistory(init: Edl) {
  const { doc, ...rest } = useHistory(init);
  return { edl: doc, ...rest };
}
