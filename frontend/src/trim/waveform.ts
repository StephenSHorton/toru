import { clipGain } from "@/trim/compositor/audio";
import type { Clip, Media, Track } from "@/trim/project";
import { clipAudible, localSrc, toSource } from "@/trim/speed";

// waveform.ts loads a media file's waveform peaks (internal/media/peaks.go)
// and reads them for a timeline clip: which source time each pixel shows
// (trim, speed curve, reverse) and how loud it is there (volume keys, fades).
//
// A peak is one byte per 1/rate s of source: the loudest sample there on a
// dB scale, 0 = FLOOR_DB below full scale or quieter, 255 = full scale.
// levels[0] holds the bytes as served; each next level halves it (the max
// of each pair), so a column covering minutes of a long recording reads a
// handful of bytes, not thousands.

export const FLOOR_DB = 60;

export interface Peaks {
  rate: number; // bins per source second in levels[0]
  levels: Uint8Array[];
}

export function buildPeaks(base: Uint8Array, rate: number): Peaks {
  const levels = [base];
  for (let cur = base; cur.length > 1; ) {
    const next = new Uint8Array(Math.ceil(cur.length / 2));
    for (let i = 0; i < next.length; i++) {
      const a = cur[2 * i];
      const b = 2 * i + 1 < cur.length ? cur[2 * i + 1] : 0;
      next[i] = a > b ? a : b;
    }
    levels.push(next);
    cur = next;
  }
  return { rate, levels };
}

// peakIn is the loudest byte between source times s0 and s1 (s0 <= s1):
// exactly the bins they cover (at least the one under s0), read up the
// pyramid like a segment tree, so any span costs O(log n). 0 past the end.
export function peakIn(p: Peaks, s0: number, s1: number): number {
  const n = p.levels[0].length;
  let lo = Math.max(0, Math.floor(s0 * p.rate + 1e-9));
  if (lo >= n) return 0;
  let hi = Math.min(n - 1, Math.max(lo, Math.ceil(s1 * p.rate - 1e-9) - 1));
  let m = 0;
  for (let k = 0; lo <= hi && k < p.levels.length; k++) {
    const lv = p.levels[k];
    if (lo & 1) m = Math.max(m, lv[lo++]);
    if (!(hi & 1)) m = Math.max(m, lv[hi--]);
    lo >>= 1;
    hi >>= 1;
  }
  return m;
}

// height is a peak byte at a gain (0..2) as a fraction of the full height:
// the gain moves it in dB, the way a volume change sounds.
export function peakHeight(byte: number, gain: number): number {
  if (byte <= 0 || gain <= 0) return 0;
  const db = (byte / 255) * FLOOR_DB - FLOOR_DB + 20 * Math.log10(gain);
  return Math.max(0, Math.min(1, (db + FLOOR_DB) / FLOOR_DB));
}

// columnHeights fills out[i] with the waveform height (0..1) of pixel column
// i of a clip, columns starting x0 px into the clip at pxPerSec. Silence
// where the clip makes no sound (freeze frame, a ramp's muted stretch, past
// the end of the source); a muted clip or track is drawn at its volume (the
// timeline dims it) rather than flat, so its sound stays visible.
export function columnHeights(out: Float32Array, p: Peaks, clip: Clip, track: Track, x0: number, pxPerSec: number): void {
  const unmuted = { ...track, muted: false };
  const c = clip.muted ? { ...clip, muted: false } : clip;
  for (let i = 0; i < out.length; i++) {
    const T0 = (x0 + i) / pxPerSec;
    const T1 = (x0 + i + 1) / pxPerSec;
    const u0 = localSrc(c, T0);
    const u1 = localSrc(c, T1);
    if (c.hold || !clipAudible(c, (u0 + u1) / 2)) {
      out[i] = 0;
      continue;
    }
    const a = toSource(c, u0);
    const b = toSource(c, u1);
    const byte = peakIn(p, Math.min(a, b), Math.max(a, b));
    out[i] = peakHeight(byte, clipGain(c, unmuted, c.start + (T0 + T1) / 2));
  }
}

// ---- loading ----------------------------------------------------------------

const cache = new Map<string, Promise<Peaks | null>>();

// loadPeaks fetches a file's peaks once per media (null: no sound). A failed
// load is forgotten, so the next timeline render tries again.
export function loadPeaks(m: Media): Promise<Peaks | null> {
  if (!m.hasAudio || m.kind === "image") return Promise.resolve(null);
  const key = `${m.id}|${m.path}`;
  let pr = cache.get(key);
  if (!pr) {
    pr = fetchPeaks(m).catch((err) => {
      cache.delete(key);
      throw err;
    });
    cache.set(key, pr);
  }
  return pr;
}

async function fetchPeaks(m: Media): Promise<Peaks | null> {
  const res = await fetch(`/__media/peaks/${encodeURIComponent(m.id)}`).catch(() => null);
  if (res?.status === 204) return null;
  if (res?.ok && (res.headers.get("Content-Type") ?? "").includes("octet-stream")) {
    const rate = Number(res.headers.get("X-Peaks-Rate")) || 100;
    return buildPeaks(new Uint8Array(await res.arrayBuffer()), rate);
  }
  // The vite dev server has no Go: decode the (small) sample in the page.
  if (import.meta.env.DEV && m.url) return decodeInPage(m.url);
  throw new Error(`peaks for ${m.path}: ${res ? res.status : "no response"}`);
}

async function decodeInPage(url: string): Promise<Peaks | null> {
  const buf = await (await fetch(url)).arrayBuffer();
  const ctx = new OfflineAudioContext(1, 1, 44100);
  const audio = await ctx.decodeAudioData(buf).catch(() => null);
  if (!audio) return null;
  const rate = 100;
  const per = Math.max(1, Math.round(audio.sampleRate / rate));
  const ch = audio.getChannelData(0);
  const out = new Uint8Array(Math.ceil(ch.length / per));
  for (let i = 0; i < out.length; i++) {
    let m = 0;
    for (let j = i * per, e = Math.min(ch.length, j + per); j < e; j++) {
      const v = Math.abs(ch[j]);
      if (v > m) m = v;
    }
    const db = m > 0 ? 20 * Math.log10(m) : -Infinity;
    out[i] = Math.max(0, Math.min(255, Math.round(((db + FLOOR_DB) / FLOOR_DB) * 255)));
  }
  return buildPeaks(out, rate);
}
