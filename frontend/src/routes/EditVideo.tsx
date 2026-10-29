import { useCallback, useEffect, useRef, useState } from "react";
import { Crop, Loader2, Pause, Play, Plus, RotateCcw, Type } from "lucide-react";
import { Button } from "@/components/ui/button";
import { VideoService, WindowsService, type TrackLevel } from "@/lib/api";
import { cn } from "@/lib/utils";
import { AddClipDialog } from "@/trim/AddClipDialog";
import { AudioTracks } from "@/trim/AudioTracks";
import { CropBox, type CropRect, type RenderedBox } from "@/trim/CropBox";
import { EditTimeline, type Selection } from "@/trim/EditTimeline";
import { addSource, initEdl, newText, removeText, upsertText, useEdlHistory, type Edl, type Source } from "@/trim/edl";
import { TextOverlays } from "@/trim/TextOverlays";
import { usePreviewPlayback } from "@/trim/usePreviewPlayback";
import { useTrackPreview } from "@/trim/useTrackPreview";

// Video editor window (docs/video-editor.md): cut, join, text, per-track
// audio, crop and export. Opened by WindowsService.OpenEditVideo with the same
// query params as the trim window: ?vid= is the served URL of the first
// source, ?path= its absolute path (Go-side operations need it).
//
// The edit lives in an EDL (useEdlHistory, so every edit undoes); the player
// is driven by usePreviewPlayback, which owns the <video>'s src and plays the
// EDL in output time, and EditTimeline edits it. Add clip appends a Library
// video as a new source. Text overlays sit over the output frame and edit the
// EDL's texts; per-track audio levels live beside the EDL (not in its undo
// history) and are heard live through useTrackPreview. Export drops into the
// labelled <Slot> below when its card lands; it sends toRequest(edl, { crop,
// tracks: levels, ... }).
const EMPTY_EDL: Edl = { sources: [], segs: [], texts: [] };

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
  const [loadErr, setLoadErr] = useState("");

  const { edl, apply, undo, redo, reset, canUndo, canRedo } = useEdlHistory(EMPTY_EDL);
  const { outTime, playing, play, pause, seek, duration } = usePreviewPlayback(vidRef, edl);
  const [selection, setSelection] = useState<Selection>(null);

  // Load the first source, then start the EDL from it (no undo step).
  useEffect(() => {
    let live = true;
    probeSource(absPath, src)
      .then((first) => live && reset(initEdl(first)))
      .catch((err) => live && setLoadErr(String(err)));
    return () => {
      live = false;
    };
  }, [absPath, src, reset]);

  // Add clip: probe the picked Library video and append it as a new source.
  const [addOpen, setAddOpen] = useState(false);
  const [adding, setAdding] = useState(false);
  const [note, setNote] = useState("");
  const onPickClip = useCallback(
    async (path: string) => {
      setAddOpen(false);
      setAdding(true);
      setNote("");
      try {
        const url = await WindowsService.ServedURL(path);
        const next = await probeSource(path, url);
        apply((e) => addSource(e, next));
      } catch (err) {
        setNote(`Couldn't add that clip: ${err}`);
      } finally {
        setAdding(false);
      }
    },
    [apply],
  );

  // Crop in pixels of the first source's frame (null = full frame); applies
  // to the output frame, which is the first source's size.
  const [cropMode, setCropMode] = useState(false);
  const [crop, setCrop] = useState<CropRect | null>(null);
  const frame = edl.sources[0];
  const videoBox = useVideoBox(vidRef, paneRef, ready);

  // Per-track audio levels (AudioTracks lists only sources the user changed),
  // heard while previewing.
  const [levels, setLevels] = useState<TrackLevel[]>([]);
  useTrackPreview(vidRef, edl.sources, levels);

  // Text overlays are drawn over the OUTPUT frame: the crop rect when there
  // is one, else the whole first-source frame.
  const outW = crop?.w ?? frame?.width ?? 0;
  const outH = crop?.h ?? frame?.height ?? 0;
  const outBox: RenderedBox =
    crop && frame
      ? {
          left: videoBox.left + (crop.x * videoBox.width) / frame.width,
          top: videoBox.top + (crop.y * videoBox.height) / frame.height,
          width: (crop.w * videoBox.width) / frame.width,
          height: (crop.h * videoBox.height) / frame.height,
        }
      : videoBox;
  const selectedText = selection?.kind === "text" ? selection.id : null;
  const addText = useCallback(() => {
    pause();
    const t = newText(edl, outTime, "");
    apply((e) => upsertText(e, t));
    setSelection({ kind: "text", id: t.id });
  }, [edl, outTime, apply, pause]);
  const editText = useCallback(
    (id: string) => {
      const t = edl.texts.find((x) => x.id === id);
      if (!t) return;
      pause();
      if (outTime < t.start || outTime >= t.end) seek(t.start);
      setSelection({ kind: "text", id });
    },
    [edl, outTime, pause, seek],
  );

  const toggle = useCallback(() => {
    if (playing) pause();
    else play();
  }, [playing, play, pause]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.target instanceof HTMLInputElement || e.target instanceof HTMLTextAreaElement) return;
      if (addOpen) return;
      if (e.code === "Space") {
        e.preventDefault();
        toggle();
      } else if (e.key === "Escape" && cropMode) {
        setCropMode(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [toggle, cropMode, addOpen]);

  const cropActive = crop !== null;

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div ref={paneRef} className="relative flex min-h-0 flex-1 items-center justify-center p-4">
        {loadErr ? (
          <div className="absolute bg-black/60 px-3 py-1.5 text-xs text-destructive">Couldn&rsquo;t load the video: {loadErr}</div>
        ) : !ready ? (
          <div className="pointer-events-none absolute flex items-center gap-2 bg-black/60 px-3 py-1.5 text-xs text-white">
            <Loader2 className="size-3.5 animate-spin" /> Loading video…
          </div>
        ) : null}
        {/* No src here: usePreviewPlayback sets it and swaps it between sources. */}
        <video
          ref={vidRef}
          poster={poster}
          preload="auto"
          onCanPlay={() => setReady(true)}
          onLoadedData={() => setReady(true)}
          className="max-h-full max-w-full cursor-pointer border"
          onClick={toggle}
          title={playing ? "Click to pause" : "Click to play"}
        />
        {cropMode && ready && frame ? (
          <CropBox
            videoW={frame.width}
            videoH={frame.height}
            box={videoBox}
            crop={crop}
            onChange={setCrop}
            onBackdropClick={toggle}
            backdropTitle={playing ? "Click outside the crop to pause" : "Click outside the crop to play"}
          />
        ) : null}
        {!cropMode && ready && outW > 0 ? (
          <TextOverlays
            texts={edl.texts}
            time={outTime}
            frameW={outW}
            frameH={outH}
            box={outBox}
            selectedId={selectedText}
            onSelect={(id) => setSelection(id ? { kind: "text", id } : null)}
            onChange={(t) => apply((e) => upsertText(e, t))}
            onRemove={(id) => {
              apply((e) => removeText(e, id));
              setSelection(null);
            }}
          />
        ) : null}
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
            {fmtTime(outTime)} / {fmtTime(duration)}
          </span>
          {note ? <span className="text-xs text-destructive">{note}</span> : null}
          <div className="ml-auto flex flex-wrap items-center justify-end gap-1">
            <Button
              size="sm"
              variant="ghost"
              disabled={!frame || adding}
              onClick={() => setAddOpen(true)}
              title="Add a clip or screen recording from the Library to the end"
            >
              {adding ? <Loader2 className="animate-spin" /> : <Plus />} Add clip
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={!frame || duration <= 0}
              onClick={addText}
              title="Add text at the playhead (drag it on the video, retime it on the timeline)"
            >
              <Type /> Text
            </Button>
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
        <EditTimeline
          edl={edl}
          outTime={outTime}
          onSeek={seek}
          apply={apply}
          selection={selection}
          onSelect={setSelection}
          onUndo={undo}
          onRedo={redo}
          canUndo={canUndo}
          canRedo={canRedo}
          keyboard={!addOpen}
          onEditText={editText}
        />
        <AudioTracks sources={edl.sources} levels={levels} onChange={setLevels} className="max-h-40 overflow-y-auto" />
        <div className="text-[11px] text-muted-foreground">
          source: <span className="font-mono">{absPath || "(dev sample.mp4)"}</span>
          {edl.sources.length > 1 ? ` + ${edl.sources.length - 1} more` : ""}
        </div>
      </div>

      <AddClipDialog
        open={addOpen}
        onClose={() => setAddOpen(false)}
        onPick={(p) => void onPickClip(p)}
        added={edl.sources.map((s) => s.path)}
      />
    </div>
  );
}

// probeSource reads a video's length and frame size: from ProbeMedia when
// there is a path (what the export will see), else from the browser's
// metadata for the served URL (the dev sample).
async function probeSource(path: string, url: string): Promise<Source> {
  if (path) {
    try {
      const m = await VideoService.ProbeMedia(path);
      if (m.durationMs > 0 && m.width > 0) {
        return { path, url, duration: m.durationMs / 1000, width: m.width, height: m.height };
      }
    } catch {
      // fall through to the browser
    }
  }
  return new Promise((resolve, reject) => {
    const v = document.createElement("video");
    v.preload = "metadata";
    v.muted = true;
    v.onloadedmetadata = () => {
      resolve({ path, url, duration: Number.isFinite(v.duration) ? v.duration : 0, width: v.videoWidth, height: v.videoHeight });
      v.removeAttribute("src");
      v.load();
    };
    v.onerror = () => reject(new Error("the file couldn't be read"));
    v.src = url;
  });
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
