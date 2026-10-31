import type { ExportFormat, ExportSettings } from "@/trim/project";

// exportSettings holds the export screen's state, presets and the maths
// behind its summary: the output size, the length and the file size
// estimate. Pure: the screen (ExportDialog.tsx) and project-check use it.

export type SizeMode = "original" | "2160" | "1440" | "1080" | "720" | "480" | "custom";
export type Quality = "low" | "medium" | "high" | "best";

export interface ExportState {
  format: ExportFormat;
  discord: boolean; // fit the result to 10 MB (mp4 only)
  size: SizeMode;
  width: number; // custom only
  height: number;
  fit: "fit" | "fill";
  fps: number; // 0 = the project's
  rateMode: "quality" | "bitrate";
  quality: Quality;
  kbps: number;
  encoder: string; // "" = best
  audio: boolean;
  audioKbps: number;
  sampleRate: number;
  gifFps: number;
  gifWidth: number;
  gifLoop: number; // 0 forever, -1 once
  gifDither: "sierra2_4a" | "floyd_steinberg" | "bayer" | "none";
  gifColors: number;
}

export const DEFAULT_STATE: ExportState = {
  format: "mp4",
  discord: false,
  size: "original",
  width: 1920,
  height: 1080,
  fit: "fit",
  fps: 0,
  rateMode: "quality",
  quality: "high",
  kbps: 8000,
  encoder: "",
  audio: true,
  audioKbps: 160,
  sampleRate: 48000,
  gifFps: 15,
  gifWidth: 480,
  gifLoop: 0,
  gifDither: "sierra2_4a",
  gifColors: 256,
};

export interface Preset {
  id: string;
  name: string;
  builtin?: boolean;
  state: Partial<ExportState>;
}

// Built-in presets. They set only what they're about; the rest of the
// current settings stay.
export const BUILTIN_PRESETS: Preset[] = [
  { id: "original", name: "Original", builtin: true, state: { format: "mp4", discord: false, size: "original", fps: 0, rateMode: "quality", quality: "high", audio: true } },
  { id: "1080p", name: "1080p", builtin: true, state: { format: "mp4", discord: false, size: "1080", fit: "fit", rateMode: "quality", quality: "high", audio: true } },
  { id: "720p", name: "720p", builtin: true, state: { format: "mp4", discord: false, size: "720", fit: "fit", rateMode: "quality", quality: "high", audio: true } },
  { id: "discord", name: "Discord", builtin: true, state: { format: "mp4", discord: true, size: "original", rateMode: "quality", quality: "high", audio: true } },
  { id: "gif", name: "GIF", builtin: true, state: { format: "gif", discord: false, gifFps: 15, gifWidth: 480, gifLoop: 0, gifColors: 256, gifDither: "sierra2_4a" } },
  { id: "audio", name: "Audio only", builtin: true, state: { format: "m4a", discord: false, audio: true, audioKbps: 192 } },
];

export const FORMAT_LABELS: Record<ExportFormat, string> = {
  mp4: "MP4 (H.264)",
  hevc: "MP4 (HEVC, smaller)",
  webm: "WebM (VP9)",
  gif: "GIF (animated, no sound)",
  m4a: "Audio only (M4A)",
};
export const FORMAT_EXT: Record<ExportFormat, string> = { mp4: ".mp4", hevc: ".mp4", webm: ".webm", gif: ".gif", m4a: ".m4a" };

export const isVideo = (f: ExportFormat) => f === "mp4" || f === "hevc" || f === "webm";

const even = (n: number) => Math.max(2, Math.round(n / 2) * 2);

// outputSize is the frame the export writes for a frameW x frameH timeline
// (crop applied). Height presets never scale up; a custom size is exact.
export function outputSize(s: ExportState, frameW: number, frameH: number): { w: number; h: number } {
  if (s.format === "m4a") return { w: 0, h: 0 };
  if (s.format === "gif") {
    const w = even(Math.min(s.gifWidth, 3840));
    return { w, h: even((w * frameH) / Math.max(1, frameW)) };
  }
  if (s.discord || s.size === "original") return { w: even(frameW), h: even(frameH) };
  if (s.size === "custom") return { w: even(s.width), h: even(s.height) };
  const target = Number(s.size);
  if (frameH <= target) return { w: even(frameW), h: even(frameH) };
  return { w: even((frameW * target) / frameH), h: target };
}

// toOptions is the Go ExportOptions for the state, the range in seconds
// (null = the whole timeline) and the timeline's frame size.
export function toOptions(s: ExportState, range: { in: number; out: number } | null, frameW: number, frameH: number): ExportSettings {
  const o: ExportSettings = { format: s.format };
  if (range) {
    o.rangeIn = Math.round(range.in * 1000);
    o.rangeOut = Math.round(range.out * 1000);
  }
  if (s.format === "gif") {
    o.gif = { fps: s.gifFps, width: even(s.gifWidth), loop: s.gifLoop, dither: s.gifDither, colors: s.gifColors };
    return o;
  }
  if (s.format !== "m4a") {
    if (!s.discord && s.size !== "original") {
      const { w, h } = outputSize(s, frameW, frameH);
      if (s.size === "custom") {
        o.width = w;
        o.height = h;
        o.fit = s.fit;
      } else if (h !== even(frameH)) o.height = h;
    }
    if (s.fps > 0) o.fps = s.fps;
    if (s.rateMode === "bitrate" && !s.discord) o.kbps = s.kbps;
    else o.quality = s.quality;
    if (s.encoder) o.encoder = s.encoder;
    if (!s.audio) o.noAudio = true;
  }
  if (s.format === "m4a" || s.audio) {
    o.audioKbps = s.audioKbps;
    o.sampleRate = s.format === "webm" ? 48000 : s.sampleRate;
  }
  return o;
}

// Bits per pixel per frame for each quality step: rough averages for screen
// and game footage at the encoders' quality modes.
const BPP: Record<Quality, number> = { low: 0.04, medium: 0.07, high: 0.11, best: 0.2 };
const CODEC_FACTOR: Partial<Record<ExportFormat, number>> = { mp4: 1, hevc: 0.6, webm: 0.65 };

// estimateBytes is the file size: exact-ish for a bitrate, an estimate
// otherwise (exact: false).
export function estimateBytes(s: ExportState, seconds: number, frameW: number, frameH: number, projectFps: number): { bytes: number; exact: boolean } {
  const audio = s.format === "m4a" || (isVideo(s.format) && s.audio) ? (s.audioKbps * 1000 * seconds) / 8 : 0;
  if (s.format === "m4a") return { bytes: audio, exact: true };
  const { w, h } = outputSize(s, frameW, frameH);
  if (s.format === "gif") {
    // Dithered GIF frames compress to roughly a third of a byte per pixel.
    const frames = seconds * s.gifFps;
    return { bytes: w * h * frames * 0.35 * Math.min(1, 0.5 + s.gifColors / 512), exact: false };
  }
  if (s.discord) return { bytes: Math.min(9.5e6, estimateBytes({ ...s, discord: false }, seconds, frameW, frameH, projectFps).bytes), exact: false };
  if (s.rateMode === "bitrate") return { bytes: (s.kbps * 1000 * seconds) / 8 + audio, exact: true };
  const fps = s.fps || projectFps || 30;
  const video = (w * h * fps * BPP[s.quality] * (CODEC_FACTOR[s.format] ?? 1) * seconds) / 8;
  return { bytes: video + audio, exact: false };
}

export function formatBytes(n: number): string {
  if (n < 1024 * 1024) return `${Math.max(1, Math.round(n / 1024))} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(n < 10 * 1024 * 1024 ? 1 : 0)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(2)} GB`;
}

// ---- remembered settings ---------------------------------------------------

const STATE_KEY = "toru.edit.export.v2";
const FOLDER_KEY = "toru.edit.export.folder";
const PRESETS_KEY = "toru.edit.export.presets";

function read<T>(key: string): T | null {
  try {
    return JSON.parse(window.localStorage.getItem(key) ?? "null") as T | null;
  } catch {
    return null;
  }
}
function write(key: string, v: unknown) {
  try {
    window.localStorage.setItem(key, JSON.stringify(v));
  } catch {
    // not remembered; fine
  }
}

export function loadState(): ExportState {
  const v = read<Partial<ExportState>>(STATE_KEY);
  return v && typeof v === "object" ? { ...DEFAULT_STATE, ...v } : { ...DEFAULT_STATE };
}
export const saveState = (s: ExportState) => write(STATE_KEY, s);
export const loadFolder = () => read<string>(FOLDER_KEY) ?? "";
export const saveFolder = (dir: string) => write(FOLDER_KEY, dir);

export function loadPresets(): Preset[] {
  const v = read<Preset[]>(PRESETS_KEY);
  return Array.isArray(v) ? v.filter((p) => p && typeof p.id === "string" && typeof p.name === "string" && p.state) : [];
}
export const savePresets = (ps: Preset[]) => write(PRESETS_KEY, ps);

// dirOf / baseOf split a Windows or POSIX path.
export function dirOf(p: string): string {
  const i = Math.max(p.lastIndexOf("\\"), p.lastIndexOf("/"));
  return i > 0 ? p.slice(0, i) : "";
}
export function baseOf(p: string): string {
  const i = Math.max(p.lastIndexOf("\\"), p.lastIndexOf("/"));
  return p.slice(i + 1).replace(/\.[^.]+$/, "");
}

export function formatTime(s: number): string {
  const m = Math.floor(s / 60);
  const sec = s - m * 60;
  return `${m}:${sec.toFixed(1).padStart(4, "0")}`;
}
