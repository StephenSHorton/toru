import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Square } from "lucide-react";
import { Window } from "@wailsio/runtime";
import { OverlayService, WindowsService } from "@/lib/api";

// DEVELOPER 2 — the floating "recording pill" (timer + Stop). Opened by
// WindowsService.OpenRecordingControls right after the overlay starts a
// recording; it is the ONLY stop affordance until the tray Stop square lands.
// Stop → OverlayService.StopRecording (graceful ffmpeg finalize, emits
// capture:done) → opens the trim editor on the artifact → closes itself.
export default function Recording() {
  const params = new URLSearchParams(window.location.search);
  const handle = params.get("handle") ?? "";
  // startError is set when this window was opened to report a FAILED start (Go's
  // OpenRecordingError) rather than an in-flight recording — there is no handle to
  // stop, just a message to show and dismiss.
  const startError = params.get("startError") ?? "";
  const [startedAt] = useState(() => Date.now());
  const [elapsed, setElapsed] = useState(0);
  const [stopping, setStopping] = useState(false);
  const [error, setError] = useState(startError);

  useEffect(() => {
    const t = setInterval(() => setElapsed(Date.now() - startedAt), 250);
    return () => clearInterval(t);
  }, [startedAt]);

  const stop = useCallback(async () => {
    if (stopping || !handle) return;
    setStopping(true);
    try {
      const res = await OverlayService.StopRecording(handle);
      if (res.videoPath) await WindowsService.OpenTrim(res.videoPath);
      await Window.Close();
    } catch (e) {
      // Leave the pill up with the failure visible; closing would silently
      // orphan the recording.
      setError(String(e));
      setStopping(false);
    }
  }, [stopping, handle]);

  if (error) {
    return (
      <div
        className="flex h-full items-center gap-2 px-3 text-xs text-red-400"
        style={{ "--wails-draggable": "drag" } as React.CSSProperties}
      >
        <span className="min-w-0 flex-1 truncate" title={error}>{error}</span>
        <Button size="sm" variant="ghost" onClick={() => void Window.Close()}>
          Dismiss
        </Button>
      </div>
    );
  }

  return (
    <div
      className="flex h-full items-center gap-3 px-4"
      style={{ "--wails-draggable": "drag" } as React.CSSProperties}
    >
      <span className="relative flex size-3">
        <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-red-500 opacity-60" />
        <span className="relative inline-flex size-3 rounded-full bg-red-500" />
      </span>
      <span className="flex-1 font-mono text-sm tabular-nums">{fmt(elapsed)}</span>
      <Button
        size="sm"
        variant="destructive"
        onClick={() => void stop()}
        disabled={stopping}
        title="Stop recording"
      >
        <Square className="size-3.5 fill-current" />
        {stopping ? "Stopping…" : "Stop"}
      </Button>
    </div>
  );
}

// fmt renders elapsed milliseconds as M:SS (H:MM:SS past the hour).
function fmt(ms: number): string {
  const s = Math.floor(ms / 1000);
  const sec = s % 60;
  const min = Math.floor(s / 60) % 60;
  const hr = Math.floor(s / 3600);
  const mm = String(min).padStart(2, "0");
  const ss = String(sec).padStart(2, "0");
  return hr > 0 ? `${hr}:${mm}:${ss}` : `${min}:${ss}`;
}
