import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { MediaService } from "@/lib/api";
import { toMedia } from "@/trim/MediaBin";
import type { Media } from "@/trim/project";
import { FLOOR_DB, loadPeaks, loudest, peakIn, type Peaks } from "@/trim/waveform";

// TrimWave is the clip window's waveform: the whole file's sound drawn across
// the trim timeline, under the dimmed regions and the handles. It shows the
// mix by default; a clip with separate tracks (game, voice chat, mic) can show
// one lane per track instead. The loudest moment gets a marker you can click
// to jump there.

// The strip shows the top RANGE_DB below each lane's own peak: game sound is
// loud most of the time, and on the full 60 dB scale it draws as a flat band
// where nothing stands out. This way the loudest moments do.
const RANGE_DB = 30;

export type WaveLane = { track?: number; label: string }; // track undefined = the mix

// useTrimMedia registers the clip with the media service (so its peaks are
// served at /__media/peaks/<id>) and returns it, or null while it loads or
// when it can't be read. In the vite dev server there is no Go: the sample
// clip is decoded in the page instead (waveform.ts).
export function useTrimMedia(path: string, src: string): Media | null {
  const [media, setMedia] = useState<Media | null>(null);
  useEffect(() => {
    setMedia(null);
    let live = true;
    const dev = (): Media | null =>
      import.meta.env.DEV && src
        ? { id: `dev:${src}`, path: src, url: src, kind: "video", duration: 0, width: 0, height: 0, fps: 0, hasAudio: true, tracks: [] }
        : null;
    if (!path) {
      setMedia(dev());
      return;
    }
    MediaService.Register([path])
      .then((list) => live && setMedia(list[0] ? toMedia(list[0]) : dev()))
      .catch(() => live && setMedia(dev()));
    return () => {
      live = false;
    };
  }, [path, src]);
  return media;
}

// trackLanes are the lanes for "one lane per track": every track but the mix,
// or [] when the clip has fewer than two (nothing to split).
export function trackLanes(m: Media | null): WaveLane[] {
  const sep = (m?.tracks ?? []).filter((t) => !t.isMix);
  if (sep.length < 2) return [];
  return sep.map((t, i) => ({ track: t.index, label: t.title || `Track ${i + 1}` }));
}

export const MIX_LANE: WaveLane[] = [{ label: "Mix" }];

export function TrimWave({
  media,
  lanes,
  duration,
  onSeek,
}: {
  media: Media | null;
  lanes: WaveLane[];
  duration: number;
  onSeek: (sec: number) => void;
}) {
  const [peaks, setPeaks] = useState<(Peaks | null)[]>([]);
  const [mix, setMix] = useState<Peaks | null>(null);
  const canvas = useRef<HTMLCanvasElement>(null);
  const laneKey = lanes.map((l) => l.track ?? "mix").join(",");

  useEffect(() => {
    setPeaks([]);
    if (!media) return;
    let live = true;
    void Promise.all(lanes.map((l) => loadPeaks(media, l.track).catch(() => null))).then((ps) => live && setPeaks(ps));
    return () => {
      live = false;
    };
    // laneKey stands for lanes
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [media, laneKey]);

  // The loudest moment is the mix's, whichever lanes are drawn.
  useEffect(() => {
    setMix(null);
    if (!media) return;
    let live = true;
    loadPeaks(media)
      .then((p) => live && setMix(p))
      .catch(() => live && setMix(null));
    return () => {
      live = false;
    };
  }, [media]);

  const draw = useRef<() => void>(() => {});
  draw.current = () => {
    const cv = canvas.current;
    if (!cv) return;
    const w = cv.clientWidth;
    const h = cv.clientHeight;
    const dpr = window.devicePixelRatio || 1;
    const pw = Math.round(w * dpr);
    const ph = Math.round(h * dpr);
    if (cv.width !== pw) cv.width = pw;
    if (cv.height !== ph) cv.height = ph;
    const g = cv.getContext("2d");
    if (!g) return;
    g.setTransform(1, 0, 0, 1, 0, 0);
    g.clearRect(0, 0, pw, ph);
    if (duration <= 0 || pw <= 0 || ph <= 0) return;
    g.fillStyle = getComputedStyle(cv).color;
    const n = Math.max(1, lanes.length);
    const lh = ph / n;
    const secPerPx = duration / pw;
    for (let k = 0; k < n; k++) {
      const p = peaks[k];
      if (!p) continue;
      const mid = lh * k + lh / 2;
      const half = lh / 2 - dpr;
      const top = p.levels[p.levels.length - 1][0] ?? 0; // the lane's loudest byte
      const span = (RANGE_DB / FLOOR_DB) * 255;
      const lo = top - span;
      g.beginPath();
      for (let i = 0; i < pw; i++) {
        const b = peakIn(p, i * secPerPx, (i + 1) * secPerPx);
        const a = Math.max(0.5, b > 0 ? Math.max(0, (b - lo) / span) * half : 0);
        g.rect(i, mid - a, 1, a * 2);
      }
      g.fill();
    }
  };

  useLayoutEffect(() => draw.current());

  useEffect(() => {
    const cv = canvas.current;
    if (!cv) return;
    const ro = new ResizeObserver(() => draw.current());
    ro.observe(cv);
    return () => ro.disconnect();
  }, [media?.hasAudio]); // the canvas is gone while a clip has no sound

  const top = mix ? loudest(mix) : -1;
  const showTop = top >= 0 && duration > 0 && top <= duration;
  const multi = lanes.length > 1;

  if (media && !media.hasAudio) return null;
  return (
    <>
      <canvas
        ref={canvas}
        className="pointer-events-none absolute inset-0 h-full w-full text-primary/45"
        aria-hidden="true"
      />
      {multi
        ? lanes.map((l, k) => (
            <span
              key={l.track ?? "mix"}
              className="pointer-events-none absolute left-1 truncate text-[10px] leading-none text-muted-foreground"
              style={{ top: `calc(${(k / lanes.length) * 100}% + 2px)`, maxWidth: "40%" }}
            >
              {l.label}
            </span>
          ))
        : null}
      {showTop ? (
        <button
          type="button"
          className="group/loud absolute -top-1 z-20 flex h-3 w-3 -translate-x-1/2 cursor-pointer items-start justify-center"
          style={{ left: `${(top / duration) * 100}%` }}
          title={`Loudest moment, ${fmtTime(top)}. Click to jump there.`}
          aria-label={`Jump to the loudest moment, ${fmtTime(top)}`}
          onPointerDown={(e) => e.stopPropagation()}
          onClick={(e) => {
            e.stopPropagation();
            onSeek(top);
          }}
        >
          <span className="block h-0 w-0 border-x-[5px] border-t-[7px] border-x-transparent border-t-amber-400 drop-shadow group-hover/loud:border-t-amber-300" />
        </button>
      ) : null}
    </>
  );
}

function fmtTime(s: number): string {
  const m = Math.floor(s / 60);
  const r = s - m * 60;
  return `${m}:${r.toFixed(1).padStart(4, "0")}`;
}
