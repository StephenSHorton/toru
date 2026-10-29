import { useCallback, useEffect, useRef, useState } from "react";
import { Crop, Loader2, Pause, Play, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { CropBox, type CropRect, type RenderedBox } from "@/trim/CropBox";

// Video editor window (docs/video-editor.md): cut, join, text, per-track
// audio, crop and export. Opened by WindowsService.OpenEditVideo with the same
// query params as the trim window: ?vid= is the served URL the <video> plays,
// ?path= the absolute path Go-side operations need.
//
// This is the shell: player + crop. The other v1 pieces drop into the
// labelled <Slot>s below as their cards land (timeline, text overlays, audio
// tracks, Add clip, Export).
export default function EditVideo() {
  const params = new URLSearchParams(window.location.search);
  const src = params.get("vid") || "/sample.mp4";
  const absPath = params.get("path") ?? "";
  // Instant-replay clips have a cached thumbnail to show while the decoder
  // spins up.
  const poster = src.startsWith("/__replayclip/") ? src.replace("/__replayclip/", "/__replaythumb/") : undefined;

  const vidRef = useRef<HTMLVideoElement>(null);
  const paneRef = useRef<HTMLDivElement>(null);
  const [ready, setReady] = useState(false);
  const [playing, setPlaying] = useState(false);
  const [duration, setDuration] = useState(0);
  const [cur, setCur] = useState(0);

  // Crop in video pixels (null = full frame); applies to the output frame.
  const [cropMode, setCropMode] = useState(false);
  const [crop, setCrop] = useState<CropRect | null>(null);
  const [videoDims, setVideoDims] = useState({ w: 0, h: 0 });
  const videoBox = useVideoBox(vidRef, paneRef, cropMode && ready);

  const toggle = useCallback(() => {
    const v = vidRef.current;
    if (!v) return;
    if (v.paused) void v.play().then(() => setPlaying(true)).catch(() => {});
    else {
      v.pause();
      setPlaying(false);
    }
  }, []);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
      if (e.code === "Space") {
        e.preventDefault();
        toggle();
      } else if (e.key === "Escape" && cropMode) {
        setCropMode(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [toggle, cropMode]);

  const onLoadedMetadata = () => {
    const v = vidRef.current;
    if (!v) return;
    setVideoDims({ w: v.videoWidth, h: v.videoHeight });
    setDuration(Number.isFinite(v.duration) ? v.duration : 0);
  };

  const cropActive = crop !== null;

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div ref={paneRef} className="relative flex min-h-0 flex-1 items-center justify-center p-4">
        {!ready ? (
          <div className="pointer-events-none absolute flex items-center gap-2 bg-black/60 px-3 py-1.5 text-xs text-white">
            <Loader2 className="size-3.5 animate-spin" /> Loading video…
          </div>
        ) : null}
        <video
          ref={vidRef}
          src={src}
          poster={poster}
          preload="auto"
          onCanPlay={() => setReady(true)}
          className="max-h-full max-w-full cursor-pointer border"
          onLoadedMetadata={onLoadedMetadata}
          onTimeUpdate={() => setCur(vidRef.current?.currentTime ?? 0)}
          onEnded={() => setPlaying(false)}
          onClick={toggle}
          title={playing ? "Click to pause" : "Click to play"}
        />
        {cropMode && ready ? (
          <CropBox
            videoW={videoDims.w}
            videoH={videoDims.h}
            box={videoBox}
            crop={crop}
            onChange={setCrop}
            onBackdropClick={toggle}
            backdropTitle={playing ? "Click outside the crop to pause" : "Click outside the crop to play"}
          />
        ) : null}
        {/* Text overlays card: draggable text boxes over the video box. */}
      </div>

      <div className="frost m-3 flex flex-col gap-3 p-3">
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="icon"
            variant="secondary"
            onClick={toggle}
            title={playing ? "Pause (Space)" : "Play (Space)"}
            aria-label={playing ? "Pause" : "Play"}
          >
            {playing ? <Pause /> : <Play />}
          </Button>
          <span className={cn("whitespace-nowrap text-xs tabular-nums text-muted-foreground", duration <= 0 && "invisible")}>
            {fmtTime(cur)} / {fmtTime(duration)}
          </span>
          <div className="ml-auto flex flex-wrap items-center justify-end gap-1">
            <Slot label="Add clip" />
            <Slot label="Text" />
            <Button
              size="sm"
              variant={cropMode ? "default" : cropActive ? "secondary" : "ghost"}
              disabled={duration <= 0}
              onClick={() => setCropMode((m) => !m)}
              title={
                cropMode
                  ? "Done cropping (the crop stays applied)"
                  : cropActive
                    ? `Crop: ${crop.w} × ${crop.h} — click to adjust`
                    : "Crop the picture: drag a box over the video"
              }
              aria-label={cropMode ? "Leave crop mode" : "Crop"}
              aria-pressed={cropMode}
            >
              <Crop /> {cropActive ? `${crop.w}×${crop.h}` : "Crop"}
            </Button>
            {cropActive ? (
              <Button
                size="sm"
                variant="ghost"
                onClick={() => setCrop(null)}
                title="Reset the crop to the full frame"
                aria-label="Reset crop"
              >
                <RotateCcw /> Reset
              </Button>
            ) : null}
            <div className="mx-1 h-5 w-px bg-border" />
            <Slot label="Export" />
          </div>
        </div>
        <Slot label="Timeline" className="h-20" />
        <Slot label="Audio tracks" className="h-12" />
        <div className="text-[11px] text-muted-foreground">
          source: <span className="font-mono">{absPath || "(dev sample.mp4)"}</span>
        </div>
      </div>
    </div>
  );
}

// Slot marks where another editor card's component goes. Replace each one
// with the real component when it lands.
function Slot({ label, className }: { label: string; className?: string }) {
  return (
    <div
      className={cn(
        "flex min-h-8 items-center justify-center border border-dashed border-border px-3 text-xs text-muted-foreground",
        className,
      )}
    >
      {label}
    </div>
  );
}

// useVideoBox measures where the <video> frame is rendered inside the pane
// (CSS px, relative to the pane, content box only) while active, so overlays
// such as the CropBox sit exactly on top of it.
function useVideoBox(
  vidRef: React.RefObject<HTMLVideoElement | null>,
  paneRef: React.RefObject<HTMLDivElement | null>,
  active: boolean,
): RenderedBox {
  const [box, setBox] = useState<RenderedBox>({ left: 0, top: 0, width: 0, height: 0 });
  useEffect(() => {
    if (!active) return;
    const v = vidRef.current;
    const pane = paneRef.current;
    if (!v || !pane) return;
    const measure = () => {
      const r = v.getBoundingClientRect();
      const p = pane.getBoundingClientRect();
      setBox({
        left: r.left - p.left + v.clientLeft,
        top: r.top - p.top + v.clientTop,
        width: v.clientWidth,
        height: v.clientHeight,
      });
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(v);
    ro.observe(pane);
    window.addEventListener("resize", measure);
    return () => {
      ro.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [vidRef, paneRef, active]);
  return box;
}

// fmtTime renders seconds as m:ss.t ("0:00.0", "12:34.5").
function fmtTime(s: number): string {
  if (!Number.isFinite(s) || s < 0) s = 0;
  const m = Math.floor(s / 60);
  const rest = s - m * 60;
  return `${m}:${rest < 10 ? "0" : ""}${rest.toFixed(1)}`;
}
