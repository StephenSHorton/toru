import { useCallback, useEffect, useRef, useState } from "react";
import { Window } from "@wailsio/runtime";
import { Button } from "@/components/ui/button";
import { Tooltip } from "@/components/ui/tooltip";
import { Dialog } from "@/components/ui/dialog";
import { Progress } from "@/components/ui/progress";
import {
  Play,
  Pause,
  Scissors,
  Copy,
  Save,
  Send,
  Timer,
  Volume2,
  VolumeX,
  Loader2,
  CheckCircle2,
  AlertCircle,
} from "lucide-react";
import { ExportService, VideoService, TrimRequest } from "@/lib/api";
import { cn } from "@/lib/utils";

// DEVELOPER 2 — video trim editor. A player, a timeline with TWO draggable
// in/out handles + a playhead, and Copy / Save As… / Trim actions. The region
// outside the handles is dimmed ("to be removed", QuickTime model); playback
// is clamped to the kept selection.
//
// The window receives TWO query params (see windows.go OpenTrim): ?vid= is the
// served /__file URL the <video> can actually play; ?path= is the absolute
// temp path that Copy/Save-As/Trim (Go-side file operations) need.
export default function Trim() {
  const vidRef = useRef<HTMLVideoElement>(null);
  const [playing, setPlaying] = useState(false);

  const params = new URLSearchParams(window.location.search);
  const [src, setSrc] = useState(params.get("vid") || "/sample.mp4");
  const [absPath, setAbsPath] = useState(params.get("path") ?? "");

  // All times in SECONDS. duration=0 means metadata hasn't loaded yet.
  const [duration, setDuration] = useState(0);
  const [inSec, setInSec] = useState(0);
  const [outSec, setOutSec] = useState(0);
  const [cur, setCur] = useState(0);

  const [precise, setPrecise] = useState(false);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  // Discord export runs in a modal (the two-pass re-encode can take a minute): a
  // "working" state with an indeterminate bar, then a success/error message.
  // null = modal closed.
  const [discord, setDiscord] = useState<
    { state: "working" | "done" | "error"; msg: string } | null
  >(null);

  // Playback volume/mute — preview-only controls (the FILE's audio is
  // untouched; trims and exports keep the recorded track). Volume persists.
  const [muted, setMuted] = useState(false);
  const [volume, setVolume] = useState(() => {
    const v = Number(window.localStorage.getItem("toru.trim.volume"));
    return Number.isFinite(v) && v > 0 && v <= 1 ? v : 1;
  });
  useEffect(() => {
    const v = vidRef.current;
    if (!v) return;
    v.muted = muted;
    v.volume = volume;
    window.localStorage.setItem("toru.trim.volume", String(volume));
  }, [muted, volume]);

  // Auto-clear the action feedback note.
  useEffect(() => {
    if (!note) return;
    const t = window.setTimeout(() => setNote(""), 2500);
    return () => window.clearTimeout(t);
  }, [note]);

  // MIN_GAP keeps the kept region selectable/playable (and the trim non-empty).
  const MIN_GAP = 0.1;

  const onLoadedMetadata = () => {
    const v = vidRef.current;
    if (!v) return;
    // Size the window to the clip's aspect ratio so the player isn't letterboxed
    // and (with the layout's min-h-0) never needs a vertical scrollbar.
    fitWindowToVideo(v.videoWidth, v.videoHeight);
    if (v.duration === Infinity) {
      // A WebM finalized without a duration header (e.g. a hard-killed mux)
      // reports Infinity; the standard fix is seeking far past the end, which
      // forces the real duration to materialize.
      v.currentTime = 1e7;
      v.onseeked = () => {
        v.onseeked = null;
        setDuration(v.duration);
        setInSec(0);
        setOutSec(v.duration);
        v.currentTime = 0;
      };
      return;
    }
    setDuration(v.duration);
    setInSec(0);
    setOutSec(v.duration);
  };

  // Clamp playback to the kept selection (QuickTime model): hitting the out
  // point pauses; pressing play from outside the selection restarts at in.
  const onTimeUpdate = () => {
    const v = vidRef.current;
    if (!v) return;
    setCur(v.currentTime);
    if (!v.paused && outSec > 0 && v.currentTime >= outSec) {
      v.pause();
      setPlaying(false);
    }
  };

  const toggle = () => {
    const v = vidRef.current;
    if (!v) return;
    if (v.paused) {
      if (v.currentTime >= outSec - 0.01 || v.currentTime < inSec) {
        v.currentTime = inSec;
      }
      void v.play();
      setPlaying(true);
    } else {
      v.pause();
      setPlaying(false);
    }
  };

  // Click-to-seek on the timeline track (handles stop propagation).
  const seekFromTrack = (e: React.PointerEvent) => {
    const v = vidRef.current;
    if (!v || duration <= 0) return;
    const rect = (e.currentTarget as HTMLElement).getBoundingClientRect();
    const frac = Math.max(0, Math.min(1, (e.clientX - rect.left) / rect.width));
    v.currentTime = frac * duration;
    setCur(v.currentTime);
  };

  const doCopy = useCallback(async () => {
    if (!absPath || busy) return;
    setBusy(true);
    try {
      await ExportService.CopyToClipboard(absPath, "video");
      setNote("Copied ✓");
    } catch (err) {
      setNote(`Copy failed: ${err}`);
    } finally {
      setBusy(false);
    }
  }, [absPath, busy]);

  const doSaveAs = useCallback(async () => {
    if (!absPath || busy) return;
    setBusy(true);
    try {
      const chosen = await ExportService.SaveAs(absPath, baseName(absPath));
      setNote(chosen ? "Saved ✓" : "");
    } catch (err) {
      setNote(`Save failed: ${err}`);
    } finally {
      setBusy(false);
    }
  }, [absPath, busy]);

  // Discord free tier caps uploads at 10MB. Export re-encodes to ~9MB (two-
  // pass, duration-based bitrate) and puts the RESULT on the clipboard as a
  // file, ready to paste into Discord. Sources already under the cap skip the
  // re-encode (Go returns the original path).
  const doDiscord = useCallback(async () => {
    if (!absPath || busy) return;
    setBusy(true);
    setDiscord({ state: "working", msg: "" });
    try {
      const out = await VideoService.ExportForDiscord(absPath);
      await ExportService.CopyToClipboard(out, "video");
      setDiscord({
        state: "done",
        msg:
          out === absPath
            ? "Already under 10 MB — copied to your clipboard. Paste it into Discord (Ctrl + V)."
            : "Discord-sized copy is on your clipboard. Paste it into Discord (Ctrl + V).",
      });
    } catch (err) {
      setDiscord({ state: "error", msg: String(err) });
    } finally {
      setBusy(false);
    }
  }, [absPath, busy]);

  const doTrim = useCallback(async () => {
    if (!absPath || busy || duration <= 0) return;
    setBusy(true);
    try {
      const out = await VideoService.Trim(
        new TrimRequest({
          videoPath: absPath,
          startMs: Math.round(inSec * 1000),
          endMs: Math.round(outSec * 1000),
          precise,
          outPath: "",
        }),
      );
      // Swap the player onto the trimmed artifact; metadata reload resets
      // duration and the in/out selection to the new clip.
      setAbsPath(out);
      setSrc("/__file/" + encodeURIComponent(baseName(out)));
      setPlaying(false);
      setCur(0);
      setNote(precise ? "Trimmed (frame-accurate) ✓" : "Trimmed ✓");
    } catch (err) {
      setNote(`Trim failed: ${err}`);
    } finally {
      setBusy(false);
    }
  }, [absPath, busy, duration, inSec, outSec, precise]);

  const pct = (sec: number) => (duration > 0 ? (sec / duration) * 100 : 0);
  const noFile = !absPath;

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div className="flex min-h-0 flex-1 items-center justify-center p-4">
        <video
          ref={vidRef}
          src={src}
          className="max-h-full max-w-full cursor-pointer border"
          onLoadedMetadata={onLoadedMetadata}
          onTimeUpdate={onTimeUpdate}
          onEnded={() => setPlaying(false)}
          onClick={toggle}
          title={playing ? "Click to pause" : "Click to play"}
        />
      </div>

      {/* frosted timeline */}
      <div className="frost m-3 p-3">
        <div
          className="relative h-14 w-full cursor-pointer border bg-card/40"
          onPointerDown={seekFromTrack}
        >
          {/* dimmed "to be removed" regions */}
          <div
            className="pointer-events-none absolute inset-y-0 left-0 bg-black/55"
            style={{ width: `${pct(inSec)}%` }}
          />
          <div
            className="pointer-events-none absolute inset-y-0 right-0 bg-black/55"
            style={{ width: `${100 - pct(outSec)}%` }}
          />
          {/* kept region outline */}
          <div
            className="pointer-events-none absolute inset-y-0 border-x-2 border-primary"
            style={{ left: `${pct(inSec)}%`, right: `${100 - pct(outSec)}%` }}
          />
          {/* playhead */}
          <div
            className="pointer-events-none absolute inset-y-0 w-px bg-foreground/90"
            style={{ left: `${pct(cur)}%` }}
          />
          {/* in/out handles */}
          <Handle
            sec={inSec}
            duration={duration}
            onChange={(s) => setInSec(Math.min(s, outSec - MIN_GAP))}
          />
          <Handle
            sec={outSec}
            duration={duration}
            onChange={(s) => setOutSec(Math.max(s, inSec + MIN_GAP))}
          />
        </div>

        <div className="mt-3 flex items-center gap-2">
          <Button
            size="icon"
            variant="secondary"
            onClick={toggle}
            title={playing ? "Pause" : "Play selection"}
          >
            {playing ? <Pause /> : <Play />}
          </Button>
          <Button
            size="icon"
            variant="ghost"
            onClick={() => setMuted((m) => !m)}
            title={muted ? "Unmute preview" : "Mute preview (the file's audio is untouched)"}
          >
            {muted || volume === 0 ? <VolumeX /> : <Volume2 />}
          </Button>
          <input
            type="range"
            min={0}
            max={1}
            step={0.05}
            value={muted ? 0 : volume}
            onChange={(e) => {
              const v = Number(e.target.value);
              setVolume(v);
              if (v > 0 && muted) setMuted(false);
            }}
            className="w-20 accent-primary"
            title="Preview volume"
          />
          <span className="text-xs tabular-nums text-muted-foreground">
            {fmtSec(inSec)} – {fmtSec(outSec)}
            <span className="mx-1.5 opacity-50">·</span>
            keep {fmtSec(Math.max(0, outSec - inSec))} of {fmtSec(duration)}
          </span>
          {note ? <span className="text-xs text-primary">{note}</span> : null}
          <div className="ml-auto flex items-center gap-1">
            <Tooltip content="Re-encodes so the cut lands on the EXACT in/out frame you picked (slower). Off = instant cut that snaps to the nearest keyframe, which can leave a few extra frames at the start.">
              <Button
                size="sm"
                variant={precise ? "default" : "ghost"}
                onClick={() => setPrecise((p) => !p)}
              >
                <Timer /> Frame-accurate
              </Button>
            </Tooltip>
            <div className="mx-1 h-5 w-px bg-border" />
            <Button size="sm" variant="ghost" disabled={noFile || busy} onClick={() => void doCopy()}>
              <Copy /> Copy
            </Button>
            <Button size="sm" variant="ghost" disabled={noFile || busy} onClick={() => void doSaveAs()}>
              <Save /> Save As…
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={noFile || busy}
              onClick={() => void doDiscord()}
              title="Re-encode to fit Discord's 10MB upload cap and copy the result"
            >
              <Send /> For Discord
            </Button>
            <Button size="sm" disabled={noFile || busy || duration <= 0} onClick={() => void doTrim()}>
              <Scissors /> {busy ? "Working…" : "Trim"}
            </Button>
          </div>
        </div>
        <div className="mt-2 text-[11px] text-muted-foreground">
          source: <span className="font-mono">{absPath || "(dev sample.mp4 — actions disabled)"}</span>
        </div>
      </div>

      {/* Discord export — modal with an indeterminate bar while the two-pass
          re-encode runs, then a success/error message. Non-dismissable while
          working so it can't be closed out from under the encode. */}
      <Dialog
        open={discord !== null}
        dismissable={discord?.state !== "working"}
        onClose={() => setDiscord(null)}
      >
        {discord ? (
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              {discord.state === "working" ? (
                <Loader2 className="size-4 animate-spin text-primary" />
              ) : discord.state === "done" ? (
                <CheckCircle2 className="size-4 text-primary" />
              ) : (
                <AlertCircle className="size-4 text-destructive" />
              )}
              <h2 className="text-sm font-medium">
                {discord.state === "working"
                  ? "Preparing for Discord…"
                  : discord.state === "done"
                    ? "Ready to share"
                    : "Export failed"}
              </h2>
            </div>

            {discord.state === "working" ? (
              <>
                <Progress indeterminate />
                <p className="text-xs leading-relaxed text-muted-foreground">
                  Re-encoding to fit Discord&rsquo;s 10&nbsp;MB upload limit. This can take a minute
                  for longer clips — you can leave this open.
                </p>
              </>
            ) : (
              <p
                className={cn(
                  "text-xs leading-relaxed",
                  discord.state === "error" ? "text-destructive" : "text-muted-foreground",
                )}
              >
                {discord.msg}
              </p>
            )}

            {discord.state !== "working" ? (
              <div className="flex justify-end">
                <Button size="sm" onClick={() => setDiscord(null)}>
                  Done
                </Button>
              </div>
            ) : null}
          </div>
        ) : null}
      </Dialog>
    </div>
  );
}

// fitWindowToVideo resizes the trim window so the player matches the clip's
// aspect ratio — no wasted letterbox bars, and (with the layout's min-h-0 +
// overflow-hidden) never tall enough to need a vertical scrollbar. Bounds keep
// the window on-screen; the timeline panel's fixed chrome is added below the
// video. A runtime-unavailable reject (dev /sample.mp4) is harmless — the
// layout already prevents scroll on its own.
function fitWindowToVideo(vw: number, vh: number) {
  if (!vw || !vh) return;
  const CHROME_H = 196; // timeline panel + paddings below the video (CSS px ≈ DIP)
  const SIDE_PAD = 32; // video container horizontal padding (p-4 → 16 * 2)
  const aspect = vw / vh;
  const screenW = window.screen?.availWidth || 1600;
  const screenH = window.screen?.availHeight || 1000;
  const maxW = Math.min(1280, Math.round(screenW * 0.95));
  const maxH = Math.min(960, Math.round(screenH * 0.92));
  const minW = 520;

  let dispW = Math.min(vw, maxW - SIDE_PAD);
  let dispH = dispW / aspect;
  const maxVideoH = maxH - CHROME_H;
  if (dispH > maxVideoH) {
    dispH = maxVideoH;
    dispW = dispH * aspect;
  }
  const winW = Math.round(Math.max(minW, Math.min(maxW, dispW + SIDE_PAD)));
  const winH = Math.round(Math.min(maxH, dispH + CHROME_H));

  void (async () => {
    try {
      await Window.SetSize(winW, winH);
      await Window.Center();
    } catch {
      /* runtime unavailable (dev sample) — layout still prevents scroll */
    }
  })();
}

// fmtSec renders seconds with 2 decimals: "0.00s", "12.34s"; minutes past 60s.
function fmtSec(s: number): string {
  if (!Number.isFinite(s)) return "0.00s";
  if (s >= 60) {
    const m = Math.floor(s / 60);
    return `${m}m ${(s - m * 60).toFixed(2)}s`;
  }
  return `${s.toFixed(2)}s`;
}

// baseName extracts the file name from a Windows or POSIX path.
function baseName(p: string): string {
  return p.split(/[\\/]/).pop() ?? p;
}

// Handle is a draggable in/out marker on the timeline, positioned in seconds.
function Handle({
  sec,
  duration,
  onChange,
}: {
  sec: number;
  duration: number;
  onChange: (s: number) => void;
}) {
  const onDown = (e: React.PointerEvent) => {
    if (duration <= 0) return;
    e.stopPropagation(); // don't trigger the track's click-to-seek
    (e.target as Element).setPointerCapture?.(e.pointerId);
    const track = (e.currentTarget as HTMLElement).parentElement!;
    const rect = track.getBoundingClientRect();
    const move = (ev: PointerEvent) => {
      const frac = Math.max(0, Math.min(1, (ev.clientX - rect.left) / rect.width));
      // Snap to 10ms so the readout's 2 decimals are exact, not 0.4999….
      onChange(Math.round(frac * duration * 100) / 100);
    };
    const up = (ev: PointerEvent) => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      (e.target as Element).releasePointerCapture?.(ev.pointerId);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };
  const left = duration > 0 ? (sec / duration) * 100 : 0;
  return (
    <div
      onPointerDown={onDown}
      className="absolute top-0 z-10 h-full w-3 -translate-x-1/2 cursor-ew-resize bg-primary"
      style={{ left: `${left}%` }}
    />
  );
}
