import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Play, Pause, Scissors, Copy, Save, Send, Timer } from "lucide-react";
import { ExportService, VideoService, TrimRequest } from "@/lib/api";

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
    setNote("Exporting for Discord… (re-encode can take a bit)");
    try {
      const out = await VideoService.ExportForDiscord(absPath);
      await ExportService.CopyToClipboard(out, "video");
      setNote(out === absPath ? "Already under 10MB — copied ✓" : "Discord-sized copy on clipboard ✓");
    } catch (err) {
      setNote(`Discord export failed: ${err}`);
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
    <div className="flex h-full flex-col">
      <div className="flex flex-1 items-center justify-center p-4">
        <video
          ref={vidRef}
          src={src}
          className="max-h-full max-w-full border"
          onLoadedMetadata={onLoadedMetadata}
          onTimeUpdate={onTimeUpdate}
          onEnded={() => setPlaying(false)}
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
          <span className="text-xs tabular-nums text-muted-foreground">
            {fmtSec(inSec)} – {fmtSec(outSec)}
            <span className="mx-1.5 opacity-50">·</span>
            keep {fmtSec(Math.max(0, outSec - inSec))} of {fmtSec(duration)}
          </span>
          {note ? <span className="text-xs text-primary">{note}</span> : null}
          <div className="ml-auto flex items-center gap-1">
            <Button
              size="sm"
              variant={precise ? "default" : "ghost"}
              onClick={() => setPrecise((p) => !p)}
              title="Frame-accurate trim re-encodes (slower); off = instant cut snapping to the nearest keyframe"
            >
              <Timer /> Frame-accurate
            </Button>
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
    </div>
  );
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
