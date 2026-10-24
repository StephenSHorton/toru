import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import Hls from "hls.js";
import { Events as WailsEvents, Window } from "@wailsio/runtime";
import {
  Bookmark,
  Camera,
  ChevronLeft,
  ChevronRight,
  Circle,
  FolderOpen,
  Loader2,
  Lock,
  LockOpen,
  Pause,
  Play,
  Scissors,
  SkipBack,
  SkipForward,
  Trash2,
  X,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { ReplayService } from "@/lib/api";
import type { RecordingMarker, RecordingSession } from "@/lib/api";
import { cn } from "@/lib/utils";

// Timeline viewer for a background/manual recording (Steam's "Recordings"
// view): the whole session plays as HLS straight from its 2s segments, with
// markers on a zoomable timeline. Drag on the timeline (or press I / O) to
// select a range, then Create clip to save it and open it in the editor.

const ZOOMS = [1, 4, 16, 64];

function fmt(sec: number): string {
  const s = Math.max(0, Math.floor(sec));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const r = s % 60;
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${String(r).padStart(2, "0")}` : `${m}:${String(r).padStart(2, "0")}`;
}

function fmtUntil(ms: number): string {
  const min = Math.max(0, Math.round((ms - Date.now()) / 60000));
  if (min >= 60) return `${Math.floor(min / 60)}h ${min % 60}m`;
  return `${min}m`;
}

function markerIcon(kind: string) {
  switch (kind) {
    case "clip":
      return <Scissors className="size-3" />;
    case "screenshot":
      return <Camera className="size-3" />;
    case "start":
      return <Circle className="size-3" />;
    default:
      return <Bookmark className="size-3" />;
  }
}

function markerLabel(m: RecordingMarker): string {
  switch (m.kind) {
    case "clip":
      return m.label ? `Clip saved: ${m.label}` : "Clip saved";
    case "screenshot":
      return "Screenshot";
    case "start":
      return m.label ? `Recording started (${m.label})` : "Recording started";
    default:
      return "Marker";
  }
}

export default function Timeline() {
  const id = Number(new URLSearchParams(window.location.search).get("id") ?? "0");
  const vidRef = useRef<HTMLVideoElement>(null);
  const trackRef = useRef<HTMLDivElement>(null);
  const [session, setSession] = useState<RecordingSession | null>(null);
  const [loadError, setLoadError] = useState("");
  const [playing, setPlaying] = useState(false);
  const [cur, setCur] = useState(0);
  const [ready, setReady] = useState(false);
  const [sel, setSel] = useState<{ a: number; b: number } | null>(null);
  const [zoom, setZoom] = useState(1);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const [confirmDelete, setConfirmDelete] = useState(false);

  const refresh = useCallback(() => {
    void ReplayService.GetSession(id)
      .then((s) => {
        setSession(s);
        void Window.SetTitle(`Toru — ${s.game || (s.mode === "manual" ? "Recording" : "Desktop")} · ${new Date(s.startMs).toLocaleString()}`).catch(() => {});
      })
      .catch((e) => setLoadError(String(e).replace(/^Error:\s*/, "")));
  }, [id]);

  useEffect(() => {
    refresh();
    const off = WailsEvents.On("recording:changed", () => refresh());
    return () => off();
  }, [refresh]);

  // Live recordings grow: re-read the session (duration, markers) regularly.
  useEffect(() => {
    if (!session?.live) return;
    const t = window.setInterval(refresh, 5000);
    return () => window.clearInterval(t);
  }, [session?.live, refresh]);

  // Attach the HLS playlist once.
  const playlist = session?.playlistUrl ?? "";
  useEffect(() => {
    const v = vidRef.current;
    if (!v || !playlist) return;
    if (Hls.isSupported()) {
      const hls = new Hls({ startPosition: 0, maxBufferLength: 20 });
      hls.loadSource(playlist);
      hls.attachMedia(v);
      return () => hls.destroy();
    }
    v.src = playlist;
  }, [playlist]);

  useEffect(() => {
    if (!note) return;
    const t = window.setTimeout(() => setNote(""), 3000);
    return () => window.clearTimeout(t);
  }, [note]);

  const duration = Math.max(session?.durationSec ?? 0, cur);

  // Visible window of the timeline for the current zoom, centred on the playhead.
  const view = useMemo(() => {
    const span = duration / zoom;
    const start = Math.min(Math.max(0, cur - span / 2), Math.max(0, duration - span));
    return { start, end: start + span, span: Math.max(span, 0.001) };
  }, [duration, zoom, cur]);
  const pct = (t: number) => ((t - view.start) / view.span) * 100;

  const seek = (t: number) => {
    const v = vidRef.current;
    if (!v) return;
    v.currentTime = Math.max(0, Math.min(t, duration));
    setCur(v.currentTime);
  };

  const toggle = () => {
    const v = vidRef.current;
    if (!v) return;
    if (v.paused) {
      void v.play();
      setPlaying(true);
    } else {
      v.pause();
      setPlaying(false);
    }
  };

  const timeAt = (clientX: number) => {
    const r = trackRef.current?.getBoundingClientRect();
    if (!r) return 0;
    const f = Math.max(0, Math.min(1, (clientX - r.left) / r.width));
    return view.start + f * view.span;
  };

  // Click = seek; drag = select a range. Pointer capture keeps the drag on
  // the track (no window listeners to leak if a pointerup is lost).
  const dragRef = useRef<{ x0: number; t0: number; dragging: boolean } | null>(null);
  const trackHandlers = {
    onPointerDown: (e: React.PointerEvent<HTMLDivElement>) => {
      e.preventDefault();
      e.currentTarget.setPointerCapture(e.pointerId);
      dragRef.current = { x0: e.clientX, t0: timeAt(e.clientX), dragging: false };
    },
    onPointerMove: (e: React.PointerEvent<HTMLDivElement>) => {
      const d = dragRef.current;
      if (!d) return;
      if (!d.dragging && Math.abs(e.clientX - d.x0) > 4) d.dragging = true;
      if (d.dragging) {
        const t1 = timeAt(e.clientX);
        setSel({ a: Math.min(d.t0, t1), b: Math.max(d.t0, t1) });
      }
    },
    onPointerUp: (e: React.PointerEvent<HTMLDivElement>) => {
      const d = dragRef.current;
      dragRef.current = null;
      if (d && !d.dragging) seek(timeAt(e.clientX));
    },
    onLostPointerCapture: () => {
      dragRef.current = null;
    },
  };

  const markers = session?.markers ?? [];
  const jumpMarker = useCallback(
    (dir: 1 | -1) => {
      const sorted = [...markers].sort((a, b) => a.offsetSec - b.offsetSec);
      const next =
        dir > 0
          ? sorted.find((m) => m.offsetSec > cur + 0.5)
          : [...sorted].reverse().find((m) => m.offsetSec < cur - 0.5);
      if (next) seek(Math.max(0, next.offsetSec - 2));
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [markers, cur],
  );

  const setIn = () => setSel((s) => ({ a: cur, b: s && s.b > cur ? s.b : Math.min(duration, cur + 30) }));
  const setOut = () => setSel((s) => ({ a: s && s.a < cur ? s.a : Math.max(0, cur - 30), b: cur }));

  const addMarker = async () => {
    try {
      await ReplayService.AddSessionMarker(id, cur);
      setNote("Marker added");
      refresh();
    } catch (e) {
      setNote(String(e));
    }
  };

  const createClip = async () => {
    if (!sel || sel.b - sel.a < 0.5) return;
    setBusy(true);
    try {
      const path = await ReplayService.CreateClip(id, sel.a, sel.b);
      setNote("Clip saved, opening the editor…");
      await ReplayService.OpenClip(path);
    } catch (e) {
      setNote(`Couldn't create the clip: ${String(e).replace(/^Error:\s*/, "")}`);
    } finally {
      setBusy(false);
    }
  };

  const toggleKeep = async () => {
    if (!session) return;
    try {
      await ReplayService.KeepSession(id, !session.kept);
      setNote(session.kept ? "Will be auto-deleted like other background footage" : "Kept: won't be auto-deleted");
      refresh();
    } catch (e) {
      setNote(String(e));
    }
  };

  const doDelete = async () => {
    try {
      await ReplayService.DeleteSession(id);
      await Window.Close();
    } catch (e) {
      setConfirmDelete(false);
      setNote(String(e).replace(/^Error:\s*/, ""));
    }
  };

  // Keyboard: Space play/pause, ←/→ 5s, J/L 10s, I/O in/out, M marker,
  // [ / ] previous/next marker, Esc clear selection.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.target as HTMLElement)?.tagName === "INPUT") return;
      switch (e.key) {
        case " ":
          e.preventDefault();
          toggle();
          break;
        case "ArrowLeft":
          seek(cur - 5);
          break;
        case "ArrowRight":
          seek(cur + 5);
          break;
        case "j":
        case "J":
          seek(cur - 10);
          break;
        case "l":
        case "L":
          seek(cur + 10);
          break;
        case "i":
        case "I":
          setIn();
          break;
        case "o":
        case "O":
          setOut();
          break;
        case "m":
        case "M":
          void addMarker();
          break;
        case "[":
          jumpMarker(-1);
          break;
        case "]":
          jumpMarker(1);
          break;
        case "Escape":
          setSel(null);
          break;
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

  if (loadError) {
    return (
      <div className="flex h-full items-center justify-center p-6 text-sm text-muted-foreground">
        {loadError}
      </div>
    );
  }

  // Tick marks: aim for ~8 labels across the visible window.
  const tickStep = [5, 10, 30, 60, 120, 300, 600, 1800, 3600].find((s) => view.span / s <= 10) ?? 3600;
  const ticks: number[] = [];
  for (let t = Math.ceil(view.start / tickStep) * tickStep; t <= view.end; t += tickStep) ticks.push(t);

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="relative flex min-h-0 flex-1 items-center justify-center bg-black/40 p-3">
        {!ready ? (
          <div className="pointer-events-none absolute flex items-center gap-2 bg-black/60 px-3 py-1.5 text-xs text-white">
            <Loader2 className="size-3.5 animate-spin" /> Loading recording…
          </div>
        ) : null}
        <video
          ref={vidRef}
          poster={session?.thumbUrl}
          className="max-h-full max-w-full cursor-pointer"
          onClick={toggle}
          onCanPlay={() => setReady(true)}
          onTimeUpdate={(e) => setCur(e.currentTarget.currentTime)}
          onPlay={() => setPlaying(true)}
          onPause={() => setPlaying(false)}
        />
      </div>

      <div className="flex flex-col gap-2 border-t border-border p-3">
        {/* Header: what this is + its retention state */}
        <div className="flex flex-wrap items-center gap-2 text-xs">
          <span className="text-sm font-medium">
            {session?.game || (session?.mode === "manual" ? "Manual recording" : "Desktop")}
          </span>
          {session ? (
            <span className="text-muted-foreground">
              {new Date(session.startMs).toLocaleString()} · {fmt(session.durationSec)}
            </span>
          ) : null}
          {session?.live ? (
            <span className="flex items-center gap-1 bg-destructive/15 px-1.5 py-0.5 text-destructive">
              <Circle className="size-2 fill-current" /> Recording
            </span>
          ) : null}
          {session?.mode === "manual" ? (
            <span className="bg-primary/15 px-1.5 py-0.5 text-primary">Manual · kept until you delete it</span>
          ) : session?.kept ? (
            <span className="bg-primary/15 px-1.5 py-0.5 text-primary">Kept</span>
          ) : session?.deleteAtMs ? (
            <span className="bg-muted px-1.5 py-0.5 text-muted-foreground">
              Background · oldest footage auto-deletes in {fmtUntil(session.deleteAtMs)}
            </span>
          ) : null}
          {note ? <span className="ml-auto text-primary">{note}</span> : null}
        </div>

        {/* Timeline */}
        <div className="select-none">
          <div className="relative h-5">
            {markers
              .filter((m) => m.offsetSec >= view.start && m.offsetSec <= view.end)
              .map((m, i) => (
                <button
                  key={`${m.t}-${i}`}
                  type="button"
                  title={`${markerLabel(m)} · ${fmt(m.offsetSec)}`}
                  onClick={() => seek(Math.max(0, m.offsetSec - 2))}
                  className={cn(
                    "absolute top-0 flex size-5 -translate-x-1/2 items-center justify-center border border-black/40",
                    m.kind === "clip"
                      ? "bg-emerald-500 text-black"
                      : m.kind === "screenshot"
                        ? "bg-amber-400 text-black"
                        : m.kind === "start"
                          ? "bg-muted text-foreground"
                          : "bg-primary text-primary-foreground",
                  )}
                  style={{ left: `${pct(m.offsetSec)}%` }}
                >
                  {markerIcon(m.kind)}
                </button>
              ))}
          </div>
          <div
            ref={trackRef}
            {...trackHandlers}
            className="relative h-10 cursor-pointer touch-none overflow-hidden border border-border bg-muted/40"
            title="Click to seek · drag to select a range"
          >
            {sel ? (
              <div
                className="absolute inset-y-0 border-x-2 border-primary bg-primary/25"
                style={{ left: `${pct(sel.a)}%`, width: `${pct(sel.b) - pct(sel.a)}%` }}
              />
            ) : null}
            {markers.map((m, i) => (
              <div
                key={`l${m.t}-${i}`}
                className="absolute inset-y-0 w-px bg-white/30"
                style={{ left: `${pct(m.offsetSec)}%` }}
              />
            ))}
            <div className="absolute inset-y-0 w-0.5 bg-white" style={{ left: `${pct(cur)}%` }} />
          </div>
          <div className="relative h-4 text-[10px] text-muted-foreground">
            {ticks.map((t) => (
              <span key={t} className="absolute -translate-x-1/2 tabular-nums" style={{ left: `${pct(t)}%` }}>
                {fmt(t)}
              </span>
            ))}
          </div>
        </div>

        {/* Controls */}
        <div className="flex flex-wrap items-center gap-1">
          <Button size="sm" variant="ghost" onClick={toggle} title="Play / pause (Space)">
            {playing ? <Pause /> : <Play />}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => seek(cur - 10)} title="Back 10s (J)">
            <SkipBack />
          </Button>
          <Button size="sm" variant="ghost" onClick={() => seek(cur + 10)} title="Forward 10s (L)">
            <SkipForward />
          </Button>
          <span className="mx-1 whitespace-nowrap text-xs tabular-nums text-muted-foreground">
            {fmt(cur)} / {fmt(duration)}
          </span>
          <div className="mx-1 h-5 w-px bg-border" />
          <Button size="sm" variant="ghost" onClick={() => jumpMarker(-1)} title="Previous marker ([)">
            <ChevronLeft /> Marker
          </Button>
          <Button size="sm" variant="ghost" onClick={() => jumpMarker(1)} title="Next marker (])">
            Marker <ChevronRight />
          </Button>
          <Button size="sm" variant="ghost" onClick={() => void addMarker()} title="Add a marker here (M)">
            <Bookmark /> Add
          </Button>
          <div className="mx-1 h-5 w-px bg-border" />
          <Button
            size="sm"
            variant="ghost"
            disabled={zoom === ZOOMS[0]}
            onClick={() => setZoom(ZOOMS[Math.max(0, ZOOMS.indexOf(zoom) - 1)])}
            title="Zoom out"
          >
            <ZoomOut />
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={zoom === ZOOMS[ZOOMS.length - 1]}
            onClick={() => setZoom(ZOOMS[Math.min(ZOOMS.length - 1, ZOOMS.indexOf(zoom) + 1)])}
            title="Zoom in"
          >
            <ZoomIn />
          </Button>

          <div className="ml-auto flex flex-wrap items-center gap-1">
            {sel ? (
              <>
                <span className="text-xs tabular-nums text-muted-foreground">
                  {fmt(sel.a)} – {fmt(sel.b)} ({fmt(sel.b - sel.a)})
                </span>
                <Button size="sm" variant="ghost" onClick={() => setSel(null)} title="Clear selection (Esc)">
                  <X />
                </Button>
              </>
            ) : (
              <span className="text-xs text-muted-foreground">Drag on the timeline or press I / O to select</span>
            )}
            <Button size="sm" disabled={!sel || busy || sel.b - sel.a < 0.5} onClick={() => void createClip()}>
              <Scissors /> {busy ? "Saving…" : "Create clip"}
            </Button>
            {session?.mode !== "manual" ? (
              <Button size="sm" variant="ghost" onClick={() => void toggleKeep()} title="Keep this recording so it isn't auto-deleted">
                {session?.kept ? <LockOpen /> : <Lock />} {session?.kept ? "Unkeep" : "Keep"}
              </Button>
            ) : null}
            <Button size="sm" variant="ghost" onClick={() => void ReplayService.OpenRecordingsDir()} title="Open the recordings folder">
              <FolderOpen />
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={session?.live}
              onClick={() => setConfirmDelete(true)}
              title={session?.live ? "Stop the recording first" : "Delete this recording"}
            >
              <Trash2 />
            </Button>
          </div>
        </div>
      </div>

      <Dialog open={confirmDelete} onClose={() => setConfirmDelete(false)}>
        <div className="flex flex-col gap-3">
          <h2 className="text-sm font-medium">Delete this recording?</h2>
          <p className="text-xs text-muted-foreground">
            {fmt(session?.durationSec ?? 0)} of footage will be deleted from disk. Clips you already
            saved from it are kept. This can't be undone.
          </p>
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(false)}>
              Cancel
            </Button>
            <Button size="sm" variant="destructive" onClick={() => void doDelete()}>
              <Trash2 /> Delete
            </Button>
          </div>
        </div>
      </Dialog>
    </div>
  );
}
