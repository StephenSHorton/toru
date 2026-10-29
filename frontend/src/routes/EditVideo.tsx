import { useCallback, useEffect, useRef, useState } from "react";
import { Crop, Download, Loader2, Pause, Play, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { MediaService, VideoService } from "@/lib/api";
import { cn } from "@/lib/utils";
import { ProjectExport } from "../../bindings/github.com/StephenSHorton/toru/internal/vid/models.js";
import { Compositor, type CompositorHandle } from "@/trim/compositor/Compositor";
import { usePlayback } from "@/trim/compositor/usePlayback";
import { CropBox, type CropRect, type RenderedBox } from "@/trim/CropBox";
import { newId } from "@/trim/edl";
import { ExportDialog } from "@/trim/ExportDialog";
import { Inspector } from "@/trim/Inspector";
import { MediaBin, toMedia, useMediaBin } from "@/trim/MediaBin";
import { MultiTimeline, type TimelineSelection } from "@/trim/MultiTimeline";
import { PlayerHandles } from "@/trim/PlayerHandles";
import {
  addMedia,
  appendClip,
  DEFAULT_CANVAS,
  newProject,
  projectDuration,
  removeProjectText,
  setCrop,
  toExportRequest,
  upsertProjectText,
  useProjectHistory,
  type Media,
  type Project,
} from "@/trim/project";
import { TextOverlays } from "@/trim/TextOverlays";

// Video editor window, v2 (docs/video-editor-v2.md): the media bin on the
// left, the player in the middle (the WebGL compositor with text overlays,
// crop and the move/resize handles laid over it), the clip inspector on the
// right and the multi-track timeline below. Opened by
// WindowsService.OpenEditVideo: ?path= is the first file's absolute path
// (?vid= its served URL, used only when there's no path, as the dev sample);
// or by WindowsService.OpenEditProject: ?project= is a saved project's id.
//
// The whole edit is one Project (useProjectHistory: every change undoes,
// drags as one step). The playback clock (usePlayback) drives the compositor
// and the timeline's playhead. Export runs the v1 ExportDialog with
// VideoService.ExportProject.
//
// The project autosaves (VideoService.SaveProject, %AppData%/toru/projects)
// a moment after each change, and the Library's Projects tab reopens it.
// Media stay where they are: on reopening, each file is registered again,
// and the ones that moved or were deleted are listed as missing.

export default function EditVideo() {
  const params = new URLSearchParams(window.location.search);
  const absPath = params.get("path") ?? "";
  const devURL = params.get("vid") || "/sample.mp4";
  const projectID = params.get("project") ?? "";

  const { project, apply, undo, redo, reset, canUndo, canRedo } = useProjectHistory(emptyProject());
  const duration = projectDuration(project);
  const pb = usePlayback(duration);
  const bin = useMediaBin();
  const [loading, setLoading] = useState(true);
  const [loadErr, setLoadErr] = useState("");

  const [missing, setMissing] = useState<string[]>([]);
  // The last saved (or loaded) document, so an unchanged project isn't
  // written again.
  const saved = useRef("");
  const [saveNote, setSaveNote] = useState("");

  // Start the project (no undo step): a saved one, or a new one from the
  // file the window was opened with.
  useEffect(() => {
    let live = true;
    const start = projectID
      ? openSaved(projectID).then(({ project: p, missing: gone }) => {
          if (!live) return;
          saved.current = JSON.stringify(p);
          setMissing(gone);
          reset(p);
        })
      : firstMedia(absPath, devURL).then((m) => live && reset(newProject(m)));
    start.catch((err) => live && setLoadErr(String(err))).finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [projectID, absPath, devURL, reset]);

  // Autosave a moment after the last change.
  useEffect(() => {
    if (!project.media.length || !project.media.some((m) => m.path)) return;
    const doc = JSON.stringify(project);
    if (doc === saved.current) return;
    const timer = window.setTimeout(() => {
      VideoService.SaveProject(doc)
        .then(() => {
          saved.current = doc;
          setSaveNote(`Saved ${new Date().toLocaleTimeString(undefined, { hour: "numeric", minute: "2-digit" })}`);
        })
        .catch((err) => setSaveNote(`Couldn't save: ${err}`));
    }, 800);
    return () => window.clearTimeout(timer);
  }, [project]);

  const [selection, setSelection] = useState<TimelineSelection>(null);
  const [picking, setPicking] = useState(false);
  const [cropMode, setCropMode] = useState(false);
  const [exportOpen, setExportOpen] = useState(false);
  const cropGesture = useRef(0);

  // The canvas and where it sits in the player pane, for the DOM overlays.
  const paneRef = useRef<HTMLDivElement>(null);
  const [canvas, setCanvas] = useState<HTMLCanvasElement | null>(null);
  const compositor = useRef<CompositorHandle | null>(null);
  const canvasBox = useElementBox(canvas, paneRef);

  const cw = project.canvas.width;
  const ch = project.canvas.height;
  const crop = project.crop;
  // The output frame: the crop rect when there is one, else the canvas.
  const outW = crop?.w ?? cw;
  const outH = crop?.h ?? ch;
  const outBox: RenderedBox =
    crop && canvasBox.width > 0
      ? {
          left: canvasBox.left + (crop.x * canvasBox.width) / cw,
          top: canvasBox.top + (crop.y * canvasBox.height) / ch,
          width: (crop.w * canvasBox.width) / cw,
          height: (crop.h * canvasBox.height) / ch,
        }
      : canvasBox;

  const appendMedia = useCallback(
    (m: Media) => {
      apply((p) => appendClip(addMedia(p, m), m.id));
    },
    [apply],
  );

  const addText = useCallback(() => {
    if (duration <= 0) return;
    pb.pause();
    const start = Math.min(pb.clock.now(), Math.max(0, duration - 0.5));
    const id = newId("txt");
    apply((p) => upsertProjectText(p, { id, text: "", x: 0.5, y: 0.5, size: "m", start, end: Math.min(duration, start + 3) }));
    setSelection({ kind: "text", id });
  }, [apply, duration, pb]);

  const editText = useCallback(
    (id: string) => {
      const t = project.texts.find((x) => x.id === id);
      if (!t) return;
      pb.pause();
      const now = pb.clock.now();
      if (now < t.start || now >= t.end) pb.seek(t.start);
      setSelection({ kind: "text", id });
    },
    [project.texts, pb],
  );

  const runExport = useCallback(
    (choice: Parameters<typeof toExportRequest>[1]) =>
      VideoService.ExportProject(ProjectExport.createFrom(toExportRequest(project, choice))),
    [project],
  );
  const firstPath = project.media.find((m) => m.kind !== "audio")?.path ?? project.media[0]?.path ?? absPath;

  // Space plays/pauses; Esc leaves crop mode. The timeline has the edit keys.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null;
      if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) return;
      if (exportOpen) return;
      if (e.code === "Space") {
        e.preventDefault();
        pb.toggle();
      } else if (e.key === "Escape" && cropMode) {
        setCropMode(false);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [pb, cropMode, exportOpen]);

  const selectedClip = selection?.kind === "clip" ? selection.id : null;
  const ready = !loading && !loadErr && project.media.length > 0;

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="flex min-h-0 flex-1 gap-2 p-2 pb-0">
        <MediaBin bin={bin} onAppend={appendMedia} className="frost w-60 shrink-0" />

        <div className="flex min-w-0 flex-1 flex-col gap-2">
          <div ref={paneRef} className="relative min-h-0 flex-1">
            <Compositor project={project} clock={pb.clock} canvasRef={setCanvas} handle={compositor} className="h-full w-full" />
            {missing.length ? (
              <div className="absolute left-2 top-2 z-30 max-w-[70%] bg-black/75 px-3 py-1.5 text-xs text-amber-300" title={missing.join("\n")}>
                {missing.length === 1 ? "1 file is missing" : `${missing.length} files are missing`} (moved or deleted since the project was saved):{" "}
                {missing.map(baseName).join(", ")}
              </div>
            ) : null}
            {loadErr ? (
              <Center>
                <span className="bg-black/70 px-3 py-1.5 text-xs text-destructive">Couldn&rsquo;t open the video: {loadErr}</span>
              </Center>
            ) : loading ? (
              <Center>
                <span className="flex items-center gap-2 bg-black/60 px-3 py-1.5 text-xs text-white">
                  <Loader2 className="size-3.5 animate-spin" /> Loading video…
                </span>
              </Center>
            ) : null}
            {ready && !cropMode ? (
              <PlayerHandles
                project={project}
                time={pb.time}
                canvas={canvas}
                selection={selectedClip}
                onSelect={(id) => setSelection(id ? { kind: "clip", id } : null)}
                apply={apply}
                compositor={compositor}
                picking={picking}
                onPickingChange={setPicking}
              />
            ) : null}
            {ready && cropMode && canvasBox.width > 0 ? (
              <CropBox
                videoW={cw}
                videoH={ch}
                box={canvasBox}
                crop={crop}
                onChange={(r: CropRect | null) => apply((p) => setCrop(p, r), `crop:${cropGesture.current}`)}
                onBackdropClick={pb.toggle}
                backdropTitle={pb.playing ? "Click outside the crop to pause" : "Click outside the crop to play"}
              />
            ) : null}
            {ready && !cropMode && crop ? <CropMask box={canvasBox} cropBox={outBox} /> : null}
            {ready && !cropMode && !picking ? (
              <TextOverlays
                texts={project.texts}
                time={pb.time}
                frameW={outW}
                frameH={outH}
                box={outBox}
                selectedId={selection?.kind === "text" ? selection.id : null}
                onSelect={(id) => setSelection(id ? { kind: "text", id } : null)}
                onChange={(t) => apply((p) => upsertProjectText(p, t))}
                onRemove={(id) => {
                  apply((p) => removeProjectText(p, id));
                  setSelection(null);
                }}
              />
            ) : null}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <Button size="icon" variant="secondary" onClick={pb.toggle} disabled={!ready} title={pb.playing ? "Pause (Space)" : "Play (Space)"} aria-label={pb.playing ? "Pause" : "Play"}>
              {pb.playing ? <Pause /> : <Play />}
            </Button>
            <span className={cn("whitespace-nowrap text-xs tabular-nums text-muted-foreground", duration <= 0 && "invisible")}>
              {fmtTime(pb.time)} / {fmtTime(duration)}
            </span>
            <span className="text-[11px] text-muted-foreground">
              {cw}×{ch} · {Math.round(project.canvas.fps * 100) / 100} fps
              {saveNote ? <span className={cn("ml-2", saveNote.startsWith("Couldn") && "text-destructive")}>· {saveNote}</span> : null}
            </span>
            <div className="ml-auto flex items-center gap-1">
              <Button
                size="sm"
                variant={cropMode ? "default" : crop ? "secondary" : "ghost"}
                disabled={!ready}
                onClick={() => {
                  cropGesture.current++;
                  setCropMode((m) => !m);
                }}
                title={cropMode ? "Done cropping (the crop stays applied)" : crop ? `Crop: ${crop.w} × ${crop.h}, click to adjust` : "Crop the picture: drag a box over the video"}
                aria-pressed={cropMode}
              >
                <Crop /> {crop ? `${crop.w}×${crop.h}` : "Crop"}
              </Button>
              {crop ? (
                <Button size="sm" variant="ghost" onClick={() => apply((p) => setCrop(p, null))} title="Reset the crop to the full frame">
                  <RotateCcw /> Reset
                </Button>
              ) : null}
              <div className="mx-1 h-5 w-px bg-border" />
              <Button
                size="sm"
                disabled={!ready || duration <= 0 || !firstPath}
                onClick={() => {
                  pb.pause();
                  setExportOpen(true);
                }}
                title={firstPath ? "Export the edit to a new video file" : "Export needs a real file (not the dev sample)"}
              >
                <Download /> Export
              </Button>
            </div>
          </div>
        </div>

        <Inspector project={project} clipId={selectedClip} apply={apply} picking={picking} onPickingChange={setPicking} className="frost w-72 shrink-0" />
      </div>

      <div className="frost m-2 p-2">
        <MultiTimeline
          project={project}
          outTime={pb.time}
          onSeek={pb.seek}
          apply={apply}
          selection={selection}
          onSelect={setSelection}
          mediaFor={bin.byId}
          onUndo={undo}
          onRedo={redo}
          canUndo={canUndo}
          canRedo={canRedo}
          onAddText={addText}
          onEditText={editText}
          keyboard={!exportOpen}
        />
      </div>

      <ExportDialog open={exportOpen} onClose={() => setExportOpen(false)} run={runExport} firstSource={firstPath} frameHeight={outH} />
    </div>
  );
}

// emptyProject is the placeholder until the first file has loaded.
function emptyProject(): Project {
  return {
    version: 2,
    id: newId("proj"),
    name: "",
    canvas: { ...DEFAULT_CANVAS },
    media: [],
    tracks: [
      { id: newId("vt"), kind: "video", muted: false, hidden: false, locked: false, clips: [] },
      { id: newId("at"), kind: "audio", muted: false, hidden: false, locked: false, clips: [] },
    ],
    texts: [],
    crop: null,
  };
}

// openSaved loads a saved project and registers its media again (their ids
// are stable, so clips still point at them). Files that are gone stay in the
// project, listed as missing.
async function openSaved(id: string): Promise<{ project: Project; missing: string[] }> {
  const p = JSON.parse(await VideoService.LoadProject(id)) as Project;
  if (p?.version !== 2) throw new Error("that project can't be opened by this version of Toru");
  const paths = p.media.map((m) => m.path).filter(Boolean);
  const found = paths.length ? await MediaService.Register(paths) : [];
  const byId = new Map(found.map((m) => [m.id, toMedia(m)]));
  const media = p.media.map((m) => {
    const now = byId.get(m.id);
    return now ? { ...m, url: now.url, duration: now.duration || m.duration } : m;
  });
  return { project: { ...p, media }, missing: p.media.filter((m) => m.path && !byId.has(m.id)).map((m) => m.path) };
}

function baseName(p: string): string {
  return p.replace(/\\/g, "/").split("/").pop() ?? p;
}

// firstMedia registers the file the window opened with (MediaService probes
// and serves it), or reads the dev sample's metadata in the browser.
async function firstMedia(path: string, devURL: string): Promise<Media> {
  if (path) {
    const [m] = await MediaService.Register([path]);
    if (!m) throw new Error("the file couldn't be read");
    return toMedia(m);
  }
  return new Promise((resolve, reject) => {
    const v = document.createElement("video");
    v.preload = "metadata";
    v.muted = true;
    v.onloadedmetadata = () =>
      resolve({
        id: "dev-sample",
        path: "",
        url: devURL,
        kind: "video",
        duration: Number.isFinite(v.duration) ? v.duration : 0,
        width: v.videoWidth,
        height: v.videoHeight,
        fps: 30,
        hasAudio: true,
        tracks: [],
      });
    v.onerror = () => reject(new Error("the file couldn't be read"));
    v.src = devURL;
  });
}

function Center({ children }: { children: React.ReactNode }) {
  return <div className="pointer-events-none absolute inset-0 z-30 flex items-center justify-center">{children}</div>;
}

// CropMask hides what an applied crop cuts away while previewing, so the
// player shows what the export will: dark bands around the kept rect (CSS px,
// relative to the pane). Clicks pass through.
function CropMask({ box, cropBox }: { box: RenderedBox; cropBox: RenderedBox }) {
  const l = cropBox.left - box.left;
  const t = cropBox.top - box.top;
  const r = l + cropBox.width;
  const b = t + cropBox.height;
  const band = "absolute bg-black/85";
  return (
    <div className="pointer-events-none absolute z-[15]" style={{ left: box.left, top: box.top, width: box.width, height: box.height }} aria-hidden>
      <div className={band} style={{ left: 0, top: 0, right: 0, height: t }} />
      <div className={band} style={{ left: 0, top: b, right: 0, bottom: 0 }} />
      <div className={band} style={{ left: 0, top: t, width: l, height: b - t }} />
      <div className={band} style={{ left: r, top: t, right: 0, height: b - t }} />
      <div className="absolute border border-white/25" style={{ left: l, top: t, width: r - l, height: b - t }} />
    </div>
  );
}

// useElementBox is where an element is drawn inside the pane (CSS px,
// relative to the pane), kept up to date as either resizes.
function useElementBox(el: HTMLElement | null, paneRef: React.RefObject<HTMLDivElement | null>): RenderedBox {
  const [box, setBox] = useState<RenderedBox>({ left: 0, top: 0, width: 0, height: 0 });
  useEffect(() => {
    const pane = paneRef.current;
    if (!el || !pane) return;
    const measure = () => {
      const r = el.getBoundingClientRect();
      const p = pane.getBoundingClientRect();
      setBox((b) =>
        b.left === r.left - p.left && b.top === r.top - p.top && b.width === r.width && b.height === r.height
          ? b
          : { left: r.left - p.left, top: r.top - p.top, width: r.width, height: r.height },
      );
    };
    measure();
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    ro.observe(pane);
    window.addEventListener("resize", measure);
    return () => {
      ro.disconnect();
      window.removeEventListener("resize", measure);
    };
  }, [el, paneRef]);
  return box;
}

// fmtTime renders seconds as m:ss.t ("0:00.0", "12:34.5").
function fmtTime(s: number): string {
  if (!Number.isFinite(s) || s < 0) s = 0;
  const m = Math.floor(s / 60);
  const rest = s - m * 60;
  return `${m}:${rest < 10 ? "0" : ""}${rest.toFixed(1)}`;
}
