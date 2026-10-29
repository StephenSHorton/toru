import { useEffect, useRef, type RefObject } from "react";
import { VideoService, type TrackLevel } from "@/lib/api";

// useTrackPreview lets you HEAR per-track levels while previewing (docs/
// video-editor.md, v1 scope 5). When the source on the <video> has levels
// set (see AudioTracks: only sources the user changed are listed), the video
// is muted and each of that source's listed tracks plays from its own
// <audio>, extracted by Go (VideoService.PreviewTrack), through a gain node
// at its volume (0..200%) or silenced when muted. A source with no levels
// plays the video's own audio, as recorded.
//
// The audio follows the video's clock: it plays and pauses with it, matches
// its rate, and re-seeks when it drifts more than 50ms. It doesn't care how
// the video is driven (usePreviewPlayback swaps src and seeks across cuts):
// it reads the video's current src to know which source is on, and treats
// the video's currentTime as that source's time.
//
// Extracted files are released when the window unloads or the hook unmounts.

const MAX_DRIFT = 0.05; // seconds

type Player = {
  audio: HTMLAudioElement;
  gain: GainNode;
  url: string;
  ready: boolean;
  seeking: boolean;
};

export function useTrackPreview(
  videoRef: RefObject<HTMLVideoElement | null>,
  sources: { path: string; url: string }[],
  levels: TrackLevel[],
) {
  const sourcesRef = useRef(sources);
  sourcesRef.current = sources;
  const levelsRef = useRef(levels);
  levelsRef.current = levels;

  // Keyed "path|index". A pending extraction is a promise so it starts once.
  const players = useRef(new Map<string, Player>());
  const pending = useRef(new Map<string, Promise<void>>());
  const ctxRef = useRef<AudioContext | null>(null);
  // The video's own muted state before this hook muted it (null = not muted
  // by us), restored when the source on screen has no levels again.
  const ownMuted = useRef<boolean | null>(null);

  useEffect(() => {
    let raf = 0;
    let gone = false;

    const ctx = () => (ctxRef.current ??= new AudioContext());

    const player = (path: string, index: number): Player | undefined => {
      const key = `${path}|${index}`;
      const have = players.current.get(key);
      if (have || pending.current.has(key)) return have;
      pending.current.set(
        key,
        VideoService.PreviewTrack(path, index)
          .then((url) => {
            if (gone) {
              void VideoService.ReleasePreviewTracks([url]);
              return;
            }
            const audio = new Audio();
            audio.preload = "auto";
            audio.src = url;
            const ac = ctx();
            const gain = ac.createGain();
            ac.createMediaElementSource(audio).connect(gain).connect(ac.destination);
            const p: Player = { audio, gain, url, ready: false, seeking: false };
            audio.addEventListener("canplay", () => (p.ready = true));
            audio.addEventListener("seeked", () => (p.seeking = false));
            players.current.set(key, p);
          })
          .catch((err) => console.warn("track preview:", err))
          .finally(() => pending.current.delete(key)),
      );
      return undefined;
    };

    const tick = () => {
      raf = requestAnimationFrame(tick);
      const v = videoRef.current;
      if (!v) return;
      const k = sourceIndex(v, sourcesRef.current);
      const want = k < 0 ? [] : levelsRef.current.filter((l) => l.source === k);
      const path = k < 0 ? "" : sourcesRef.current[k].path;

      const active = new Set<Player>();
      let allReady = want.length > 0;
      for (const l of want) {
        const p = player(path, l.index);
        if (!p || !p.ready) {
          allReady = false;
          continue;
        }
        active.add(p);
        const g = l.muted ? 0 : Math.max(0, Math.min(2, l.volume / 100));
        if (p.gain.gain.value !== g) p.gain.gain.value = g;
      }

      // Switch to the tracks only once all of them can play; until then the
      // video's own audio keeps sounding.
      const using = allReady;
      if (using && ownMuted.current === null) {
        ownMuted.current = v.muted;
        v.muted = true;
      } else if (!using && ownMuted.current !== null) {
        v.muted = ownMuted.current;
        ownMuted.current = null;
      }

      for (const p of players.current.values()) {
        const a = p.audio;
        if (!using || !active.has(p)) {
          if (!a.paused) a.pause();
          continue;
        }
        if (a.playbackRate !== v.playbackRate) a.playbackRate = v.playbackRate;
        if (v.paused || v.seeking || v.readyState < 2) {
          if (!a.paused) a.pause();
          if (v.paused && !p.seeking && Math.abs(a.currentTime - v.currentTime) > MAX_DRIFT) {
            p.seeking = true;
            a.currentTime = v.currentTime;
          }
          continue;
        }
        if (!p.seeking && Math.abs(a.currentTime - v.currentTime) > MAX_DRIFT) {
          p.seeking = true;
          a.currentTime = v.currentTime;
        }
        if (a.paused) {
          if (ctxRef.current?.state === "suspended") void ctxRef.current.resume();
          void a.play().catch(() => {});
        }
      }
    };
    raf = requestAnimationFrame(tick);

    const release = () => {
      const urls = [...players.current.values()].map((p) => p.url);
      for (const p of players.current.values()) {
        p.audio.pause();
        p.audio.removeAttribute("src");
        p.audio.load();
      }
      players.current.clear();
      if (urls.length) void VideoService.ReleasePreviewTracks(urls);
    };
    window.addEventListener("beforeunload", release);

    return () => {
      gone = true;
      cancelAnimationFrame(raf);
      window.removeEventListener("beforeunload", release);
      release();
      const v = videoRef.current;
      if (v && ownMuted.current !== null) v.muted = ownMuted.current;
      ownMuted.current = null;
      void ctxRef.current?.close();
      ctxRef.current = null;
    };
  }, [videoRef]);
}

// sourceIndex finds which source the video is showing by its src (-1 when
// none matches, e.g. still loading).
function sourceIndex(v: HTMLVideoElement, sources: { url: string }[]): number {
  const cur = v.currentSrc || v.src;
  if (!cur) return -1;
  return sources.findIndex((s) => s.url && absolute(s.url) === cur);
}

function absolute(u: string): string {
  try {
    return new URL(u, window.location.href).href;
  } catch {
    return u;
  }
}
