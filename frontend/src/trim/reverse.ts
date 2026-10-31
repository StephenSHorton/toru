import { Events as WailsEvents } from "@wailsio/runtime";
import { create } from "zustand";
import { MediaService, VideoService } from "@/lib/api";
import type { Clip, Media } from "@/trim/project";

// reverse.ts is the preview's side of reversed clips (docs/video-editor-v3.md,
// Reverse): it asks Go for a clip range's reverse proxy (internal/vid/
// reverse.go) once the range has stopped changing, follows its
// "reverse:progress" events and serves the finished proxy like any media.
// Until it's ready the Compositor steps the source backwards by seeks.

export type ReverseState = {
  pct: number; // 0..1 while rendering
  url?: string; // served proxy, once ready
  error?: string;
};

type Store = { byRange: Record<string, ReverseState> };
export const useReverse = create<Store>(() => ({ byRange: {} }));

// A range must stay the same this long (ms) before its proxy is made, so
// dragging a reversed clip's edge doesn't render every step.
const SETTLE = 700;

const rangeId = (path: string, inMs: number, outMs: number) => `${path}|${inMs}|${outMs}`;
const firstAsked = new Map<string, number>();
const started = new Set<string>();
const keys = new Map<string, string>(); // Go's progress key → range id

let listening = false;
function listen() {
  if (listening) return;
  listening = true;
  WailsEvents.On("reverse:progress", (ev: { data: unknown }) => {
    const raw = (Array.isArray(ev.data) ? ev.data[0] : ev.data) as { key?: string; pct?: number } | undefined;
    const id = raw?.key ? keys.get(raw.key) : undefined;
    if (id) set(id, { pct: Number(raw?.pct ?? 0) });
  });
  WailsEvents.On("reverse:cleared", () => forgetReverse());
}

function set(id: string, patch: Partial<ReverseState>) {
  useReverse.setState((s) => ({ byRange: { ...s.byRange, [id]: { ...(s.byRange[id] ?? { pct: 0 }), ...patch } } }));
}

const ms = (s: number) => Math.round(s * 1000);

// reverseRange is the proxy range of a reversed clip (as the export rounds
// it), or undefined for one that isn't reversed video.
export function reverseRange(clip: Clip, media: Media): { path: string; inMs: number; outMs: number; id: string } | undefined {
  if (!clip.reversed || clip.hold || media.kind !== "video") return undefined;
  const inMs = ms(clip.in);
  const outMs = ms(clip.out);
  return { path: media.path, inMs, outMs, id: rangeId(media.path, inMs, outMs) };
}

// reverseUrl is the clip's reverse proxy URL once it's ready. Called every
// frame; it starts the render once the range has settled.
export function reverseUrl(clip: Clip, media: Media, now = performance.now()): string | undefined {
  const r = reverseRange(clip, media);
  if (!r) return undefined;
  const st = useReverse.getState().byRange[r.id];
  if (st?.url) return st.url;
  if (started.has(r.id)) return undefined;
  const first = firstAsked.get(r.id) ?? now;
  firstAsked.set(r.id, first);
  if (now - first < SETTLE) return undefined;
  started.add(r.id);
  listen();
  set(r.id, { pct: 0 });
  void (async () => {
    try {
      keys.set(await VideoService.ReverseKey(r.path, r.inMs, r.outMs), r.id);
      const file = await VideoService.ReverseProxy(r.path, r.inMs, r.outMs);
      const [m] = await MediaService.Register([file]);
      if (!m?.url) throw new Error("the reversed copy couldn't be opened");
      set(r.id, { pct: 1, url: m.url });
    } catch (err) {
      set(r.id, { error: String(err) });
      started.delete(r.id); // try again on a later ask
      firstAsked.delete(r.id);
    }
  })();
  return undefined;
}

// useReverseState is a reversed clip's proxy state for the UI ("Preparing
// reverse… 40%"), or undefined when it isn't reversed.
export function useReverseState(clip: Clip | undefined, media: Media | undefined): ReverseState | undefined {
  const id = clip && media ? reverseRange(clip, media)?.id : undefined;
  return useReverse((s) => (id ? (s.byRange[id] ?? { pct: 0 }) : undefined));
}

// forgetReverse drops every ready proxy URL (after Settings cleared the
// cache), so they render again when next shown.
export function forgetReverse() {
  started.clear();
  firstAsked.clear();
  keys.clear();
  useReverse.setState({ byRange: {} });
}
