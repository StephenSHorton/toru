import { useCallback, useEffect, useRef, useState } from "react";
import { Browser, Events as WailsEvents, Window } from "@wailsio/runtime";
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
  Link2,
  ExternalLink,
  SkipBack,
  SkipForward,
  ArrowLeftToLine,
  ArrowRightToLine,
  Crop,
  RotateCcw,
} from "lucide-react";
import { ExportService, ReplayService, UploadService, VideoService, TrimRequest } from "@/lib/api";
import { cn } from "@/lib/utils";
import { CropBox, type CropRect, type RenderedBox } from "@/trim/CropBox";

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
  // Instant-replay clips have a cached thumbnail: show it while the player
  // spins up its decoder (a 1440p/144fps clip can take a moment).
  const poster = src.startsWith("/__replayclip/") ? src.replace("/__replayclip/", "/__replaythumb/") : undefined;
  const [ready, setReady] = useState(false);
  useEffect(() => setReady(false), [src]);
  const [absPath, setAbsPath] = useState(params.get("path") ?? "");

  // All times in SECONDS. duration=0 means metadata hasn't loaded yet.
  const [duration, setDuration] = useState(0);
  const [inSec, setInSec] = useState(0);
  const [outSec, setOutSec] = useState(0);
  const [cur, setCur] = useState(0);

  const [precise, setPrecise] = useState(false);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");

  // Spatial crop (video pixels; null = full frame). cropMode shows the CropBox
  // overlay over the player; the crop itself stays applied after leaving crop
  // mode and is sent with the next Trim. A crop always re-encodes.
  const [cropMode, setCropMode] = useState(false);
  const [crop, setCrop] = useState<CropRect | null>(null);
  const [videoDims, setVideoDims] = useState({ w: 0, h: 0 });
  // Where the <video> frame is rendered inside the player pane (CSS px,
  // relative to the pane) so the overlay can sit exactly on top of it.
  const paneRef = useRef<HTMLDivElement>(null);
  const [videoBox, setVideoBox] = useState<RenderedBox>({ left: 0, top: 0, width: 0, height: 0 });
  useEffect(() => {
    if (!cropMode) return;
    const v = vidRef.current;
    const pane = paneRef.current;
    if (!v || !pane) return;
    const measure = () => {
      const r = v.getBoundingClientRect();
      const p = pane.getBoundingClientRect();
      // Content box only: the overlay must not cover the 1px border.
      setVideoBox({
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
  }, [cropMode, ready, src]);
  // Discord export runs in a modal (the two-pass re-encode can take a minute): a
  // "working" state with an indeterminate bar, then a success/error message.
  // null = modal closed.
  const [discord, setDiscord] = useState<
    { state: "working" | "done" | "error"; msg: string } | null
  >(null);

  // Share-as-link flow (catbox.moe). "consent" is the one-time explainer that
  // links are public and permanent; null = dialog closed.
  const [share, setShare] = useState<
    | { state: "consent" }
    | { state: "working"; stage: "prepare" | "compress" | "upload"; pct: number }
    | { state: "done"; url: string; copied: boolean }
    | { state: "error"; msg: string }
    | null
  >(null);
  const [existingLink, setExistingLink] = useState("");
  const [linkFlash, setLinkFlash] = useState(false);
  useEffect(() => {
    if (!absPath) return;
    void UploadService.LinkFor(absPath)
      .then((u) => setExistingLink(u ?? ""))
      .catch(() => setExistingLink(""));
  }, [absPath]);

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
    fitWindowToVideo(v.videoWidth, v.videoHeight, paneRef.current);
    setVideoDims({ w: v.videoWidth, h: v.videoHeight });
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

  // "Play from trim start": every play starts at the in point, so you can
  // keep re-watching the cut while adjusting it. Remembered per viewer.
  const [fromStart, setFromStart] = useState(() => {
    try {
      return window.localStorage.getItem("toru.trim.fromStart") === "1";
    } catch {
      return false;
    }
  });
  const toggleFromStart = (on: boolean) => {
    setFromStart(on);
    try {
      window.localStorage.setItem("toru.trim.fromStart", on ? "1" : "0");
    } catch {
      // per-viewer convenience only
    }
  };

  const toggle = () => {
    const v = vidRef.current;
    if (!v) return;
    if (v.paused) {
      if (fromStart || v.currentTime >= outSec - 0.01 || v.currentTime < inSec) {
        v.currentTime = inSec;
      }
      void v.play();
      setPlaying(true);
    } else {
      v.pause();
      setPlaying(false);
    }
  };

  // Jump the playhead to the in/out point; set the in/out point at the
  // playhead. Times come from the element (cur is only as fresh as the last
  // timeupdate) and snap to 10ms like the handles do.
  const jumpTo = (sec: number) => {
    const v = vidRef.current;
    if (!v || duration <= 0) return;
    v.currentTime = Math.max(0, Math.min(sec, duration));
    setCur(v.currentTime);
  };
  const playheadSec = () => {
    const v = vidRef.current;
    return v ? Math.round(v.currentTime * 100) / 100 : cur;
  };
  const setInHere = () => {
    if (duration <= 0) return;
    setInSec(Math.max(0, Math.min(playheadSec(), outSec - MIN_GAP)));
  };
  const setOutHere = () => {
    if (duration <= 0) return;
    setOutSec(Math.min(duration, Math.max(playheadSec(), inSec + MIN_GAP)));
  };

  // Keyboard: Space play/pause, Home/End jump to the clip ends, I/O set them at
  // the playhead. Ignored while typing in a field or while a dialog is open.
  // No dependency array: the closure must always see the current in/out.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const tag = (e.target as HTMLElement | null)?.tagName;
      if (tag === "INPUT" || tag === "TEXTAREA") return;
      if (share !== null || discord !== null) return;
      switch (e.key) {
        case " ":
          e.preventDefault(); // also stops a focused button from clicking
          toggle();
          break;
        case "Home":
          e.preventDefault();
          jumpTo(inSec);
          break;
        case "End":
          e.preventDefault();
          jumpTo(outSec);
          break;
        case "i":
        case "I":
          setInHere();
          break;
        case "o":
        case "O":
          setOutHere();
          break;
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  });

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
          // A crop always re-encodes on the Go side (yuv420 even dims, clamped).
          ...(crop ? { crop } : {}),
        }),
      );
      // Swap the player onto the trimmed artifact; metadata reload resets
      // duration and the in/out selection to the new clip. Instant-replay
      // clips are served from the replay folder, everything else via /__file.
      // The crop was baked into the new file, so it is cleared with the swap.
      const clipURL = await ReplayService.ClipURL(out).catch(() => "");
      setAbsPath(out);
      setSrc(clipURL || "/__file/" + encodeURIComponent(baseName(out)));
      setCrop(null);
      setCropMode(false);
      setPlaying(false);
      setCur(0);
      setNote(crop ? "Trimmed & cropped ✓" : precise ? "Trimmed (frame-accurate) ✓" : "Trimmed ✓");
    } catch (err) {
      setNote(`Trim failed: ${err}`);
    } finally {
      setBusy(false);
    }
  }, [absPath, busy, duration, inSec, outSec, precise, crop]);

  const SHARE_CONSENT_KEY = "toru.share.consented";

  const startShare = useCallback(async () => {
    if (!absPath) return;
    setShare({ state: "working", stage: "prepare", pct: 0 });
    const off = WailsEvents.On("upload:progress", (ev) => {
      const raw = Array.isArray(ev.data) ? ev.data[0] : ev.data;
      const p = raw as { stage?: string; pct?: number } | undefined;
      if (p?.stage === "compress" || p?.stage === "upload") {
        setShare({ state: "working", stage: p.stage, pct: p.pct ?? 0 });
      }
    });
    // The whole clip is shared as-is when it fits; a selection is cut out.
    const whole = inSec <= 0.05 && outSec >= duration - 0.05;
    try {
      const url = await UploadService.ShareLink(
        absPath,
        whole ? 0 : Math.round(inSec * 1000),
        whole ? 0 : Math.round(outSec * 1000),
      );
      let copied = true;
      try {
        await ExportService.CopyText(url);
      } catch {
        copied = false;
      }
      if (whole) setExistingLink(url);
      setShare({ state: "done", url, copied });
    } catch (err) {
      const msg = String(err).replace(/^Error:\s*/, "");
      setShare(msg.includes("cancelled") ? null : { state: "error", msg });
    } finally {
      off();
    }
  }, [absPath, inSec, outSec, duration]);

  const doShare = useCallback(() => {
    if (!absPath || busy) return;
    let consented = false;
    try {
      consented = window.localStorage.getItem(SHARE_CONSENT_KEY) === "1";
    } catch {
      // storage unavailable: ask every time
    }
    if (consented) void startShare();
    else setShare({ state: "consent" });
  }, [absPath, busy, startShare]);

  const pct = (sec: number) => (duration > 0 ? (sec / duration) * 100 : 0);
  const noFile = !absPath;
  const cropActive = crop !== null;
  // Copy / Save As / For Discord / Share act on the CURRENT file; only Trim
  // bakes the crop in. Say so on their tooltips while a crop is pending.
  const cropNote = cropActive ? " The crop is applied only by Trim & crop." : "";

  return (
    <div className="flex h-full flex-col overflow-hidden">
      <div ref={paneRef} className="relative flex min-h-0 flex-1 items-center justify-center p-4">
        {!ready ? (
          <div className="pointer-events-none absolute flex items-center gap-2 bg-black/60 px-3 py-1.5 text-xs text-white">
            <Loader2 className="size-3.5 animate-spin" /> Loading clip…
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
          onTimeUpdate={onTimeUpdate}
          onEnded={() => setPlaying(false)}
          onClick={toggle}
          title={playing ? "Click to pause" : "Click to play"}
        />
        {/* crop overlay — a sibling positioned over the measured video box so
            the video keeps its max-h-full fit (a wrapper would break it) */}
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

        <div className="mt-3 flex flex-wrap items-center gap-2">
          <Button
            size="icon"
            variant="secondary"
            onClick={toggle}
            title={playing ? "Pause (Space)" : fromStart ? "Play from the trim start (Space)" : "Play selection (Space)"}
            aria-label={playing ? "Pause" : "Play"}
          >
            {playing ? <Pause /> : <Play />}
          </Button>
          <div className="flex items-center">
            <Button
              size="sm"
              variant="ghost"
              disabled={duration <= 0}
              onClick={() => jumpTo(inSec)}
              title="Jump to the start of the selection (Home)"
              aria-label="Jump to the start of the selection"
            >
              <SkipBack />
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={duration <= 0}
              onClick={() => jumpTo(outSec)}
              title="Jump to the end of the selection (End)"
              aria-label="Jump to the end of the selection"
            >
              <SkipForward />
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={duration <= 0}
              onClick={setInHere}
              title="Start the selection at the playhead (I)"
              aria-label="Start the selection at the playhead"
            >
              <ArrowLeftToLine /> Start here
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={duration <= 0}
              onClick={setOutHere}
              title="End the selection at the playhead (O)"
              aria-label="End the selection at the playhead"
            >
              <ArrowRightToLine /> End here
            </Button>
          </div>
          <label
            className="flex cursor-pointer select-none items-center gap-1.5 whitespace-nowrap text-xs text-muted-foreground hover:text-foreground"
            title="Each time you press play, start from the beginning of your trim"
          >
            <input
              type="checkbox"
              checked={fromStart}
              onChange={(e) => toggleFromStart(e.target.checked)}
              className="accent-primary"
            />
            From trim start
          </label>
          <Button
            size="icon"
            variant="ghost"
            onClick={() => setMuted((m) => !m)}
            title={muted ? "Unmute preview" : "Mute preview (the file's audio is untouched)"}
            aria-label={muted ? "Unmute preview" : "Mute preview"}
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
          <span className={cn("whitespace-nowrap text-xs tabular-nums text-muted-foreground", duration <= 0 && "invisible")}>
            {fmtSec(inSec)} – {fmtSec(outSec)}
            <span className="mx-1.5 opacity-50">·</span>
            keep {fmtSec(Math.max(0, outSec - inSec))} of {fmtSec(duration)}
          </span>
          {note ? <span className="text-xs text-primary">{note}</span> : null}
          <div className="ml-auto flex flex-wrap items-center justify-end gap-1">
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
            <Tooltip
              content={
                cropActive
                  ? "Crop always re-encodes"
                  : "Re-encodes so the cut lands on the EXACT in/out frame you picked (slower). Off = instant cut that snaps to the nearest keyframe, which can leave a few extra frames at the start."
              }
            >
              <Button
                size="sm"
                variant={precise || cropActive ? "default" : "ghost"}
                disabled={cropActive}
                onClick={() => setPrecise((p) => !p)}
                aria-pressed={precise || cropActive}
              >
                <Timer /> Frame-accurate
              </Button>
            </Tooltip>
            <div className="mx-1 h-5 w-px bg-border" />
            <Button
              size="sm"
              variant="ghost"
              disabled={noFile || busy}
              onClick={() => void doCopy()}
              title={cropActive ? `Copy the current file.${cropNote}` : undefined}
            >
              <Copy /> Copy
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={noFile || busy}
              onClick={() => void doSaveAs()}
              title={cropActive ? `Save the current file.${cropNote}` : undefined}
            >
              <Save /> Save As…
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={noFile || busy}
              onClick={() => void doDiscord()}
              title={`Re-encode to fit Discord's 10MB upload cap and copy the result.${cropNote}`}
            >
              <Send /> For Discord
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={noFile || busy || duration <= 0}
              onClick={doShare}
              title={
                existingLink
                  ? `Already shared: ${existingLink}`
                  : `Upload your selection and copy a link anyone can open.${cropNote}`
              }
            >
              <Link2 /> {existingLink ? "Copy link" : "Share link"}
            </Button>
            <Button
              size="sm"
              disabled={noFile || busy || duration <= 0}
              onClick={() => void doTrim()}
              title={cropActive ? "Cut to the selection and crop the picture (re-encodes)" : "Cut the clip to the selection"}
            >
              <Scissors /> {busy ? "Working…" : cropActive ? "Trim & crop" : "Trim"}
            </Button>
          </div>
        </div>
        <div className="mt-2 text-[11px] text-muted-foreground">
          source: <span className="font-mono">{absPath || "(dev sample.mp4 — actions disabled)"}</span>
        </div>
      </div>

      {/* Share as link — one-time explainer, then progress, then the link. */}
      <Dialog
        open={share !== null}
        dismissable={share?.state !== "working"}
        onClose={() => setShare(null)}
      >
        {share?.state === "consent" ? (
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Link2 className="size-4 text-primary" />
              <h2 className="text-sm font-medium">Share as a link</h2>
            </div>
            <ul className="flex list-disc flex-col gap-1 pl-4 text-xs leading-relaxed text-muted-foreground">
              <li>
                Toru uploads the clip (or your trimmed selection) to <b>catbox.moe</b>, a free host
                with no account needed.
              </li>
              <li>
                <b>Anyone with the link can watch it.</b> Links never expire, and they can't be
                deleted from Toru afterwards.
              </li>
              <li>
                Clips over 200&nbsp;MB are compressed first (1080p60, or 720p for long clips). The
                link plays right in Discord.
              </li>
            </ul>
            <div className="flex justify-end gap-2">
              <Button size="sm" variant="ghost" onClick={() => setShare(null)}>
                Cancel
              </Button>
              <Button
                size="sm"
                onClick={() => {
                  try {
                    window.localStorage.setItem(SHARE_CONSENT_KEY, "1");
                  } catch {
                    // ask again next time
                  }
                  void startShare();
                }}
              >
                <Link2 /> Upload &amp; copy link
              </Button>
            </div>
          </div>
        ) : share?.state === "working" ? (
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <Loader2 className="size-4 animate-spin text-primary" />
              <h2 className="text-sm font-medium">
                {share.stage === "compress"
                  ? `Compressing… ${Math.round(share.pct)}%`
                  : share.stage === "upload"
                    ? `Uploading… ${Math.round(share.pct)}%`
                    : "Preparing…"}
              </h2>
            </div>
            <Progress value={share.pct} indeterminate={share.stage === "prepare"} />
            <div className="flex justify-end">
              <Button size="sm" variant="ghost" onClick={() => void UploadService.CancelShare()}>
                Cancel
              </Button>
            </div>
          </div>
        ) : share?.state === "done" ? (
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <CheckCircle2 className="size-4 text-primary" />
              <h2 className="text-sm font-medium">
                {share.copied ? "Link copied to your clipboard" : "Your link is ready"}
              </h2>
            </div>
            <input
              readOnly
              value={share.url}
              onFocus={(e) => e.currentTarget.select()}
              className="border border-border bg-background/40 px-2 py-1.5 font-mono text-xs"
            />
            <div className="flex justify-end gap-2">
              <Button size="sm" variant="ghost" onClick={() => void Browser.OpenURL(share.url)}>
                <ExternalLink /> Open
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => {
                  void ExportService.CopyText(share.url)
                    .then(() => {
                      setShare({ ...share, copied: true });
                      setLinkFlash(true);
                      window.setTimeout(() => setLinkFlash(false), 1500);
                    })
                    .catch((err) => setNote(`Copy failed: ${err}`));
                }}
              >
                {linkFlash ? <CheckCircle2 /> : <Copy />} {linkFlash ? "Copied" : "Copy link"}
              </Button>
              <Button size="sm" onClick={() => setShare(null)}>
                Done
              </Button>
            </div>
          </div>
        ) : share?.state === "error" ? (
          <div className="flex flex-col gap-3">
            <div className="flex items-center gap-2">
              <AlertCircle className="size-4 text-destructive" />
              <h2 className="text-sm font-medium">Couldn&rsquo;t share</h2>
            </div>
            <p className="text-xs leading-relaxed text-destructive">{share.msg}</p>
            <div className="flex justify-end">
              <Button size="sm" onClick={() => setShare(null)}>
                Close
              </Button>
            </div>
          </div>
        ) : null}
      </Dialog>

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
//
// CHROME_H assumes the control row fits on one line. It wraps on narrow
// windows (portrait clips) and at the 1280 max once the crop controls are in
// the row, so after the first resize the player pane is measured: if the video
// got less height than it was sized for, the deficit is added to the chrome
// and the window is sized once more. Measuring the deficit (rather than the
// panel) keeps this independent of the title-bar/DPI details the constant was
// tuned against. One correction pass only: the row can wrap transiently
// (the action note, label changes) and the window must not breathe.
function fitWindowToVideo(vw: number, vh: number, pane: HTMLElement | null) {
  if (!vw || !vh) return;
  const CHROME_H = 196; // timeline panel + paddings below the video (CSS px ≈ DIP)
  const SIDE_PAD = 32; // video container horizontal padding (p-4 → 16 * 2)
  const PANE_PAD = 32; // the pane's own p-4, top + bottom
  const aspect = vw / vh;
  const screenW = window.screen?.availWidth || 1600;
  const screenH = window.screen?.availHeight || 1000;
  const maxW = Math.min(1280, Math.round(screenW * 0.95));
  const maxH = Math.min(960, Math.round(screenH * 0.92));
  // Wide enough for the action row on one line, so a portrait or small clip
  // does not squeeze the buttons (they wrap as a fallback on small screens).
  const minW = Math.min(maxW, 820);

  const size = (chromeH: number) => {
    let dispW = Math.min(vw, maxW - SIDE_PAD);
    let dispH = dispW / aspect;
    const maxVideoH = maxH - chromeH;
    if (dispH > maxVideoH) {
      dispH = maxVideoH;
      dispW = dispH * aspect;
    }
    return {
      dispH,
      winW: Math.round(Math.max(minW, Math.min(maxW, dispW + SIDE_PAD))),
      winH: Math.round(Math.min(maxH, dispH + chromeH)),
    };
  };

  void (async () => {
    try {
      const first = size(CHROME_H);
      await Window.SetSize(first.winW, first.winH);
      await Window.Center();
      if (!pane) return;
      await settleLayout();
      // Height the video actually has vs. what it was sized for.
      const availH = pane.clientHeight - PANE_PAD;
      const deficit = Math.round(first.dispH - availH);
      if (deficit <= 1) return;
      const second = size(CHROME_H + deficit);
      if (second.winW === first.winW && second.winH === first.winH) return;
      await Window.SetSize(second.winW, second.winH);
      await Window.Center();
    } catch {
      /* runtime unavailable (dev sample) — layout still prevents scroll */
    }
  })();
}

// settleLayout resolves once the window has re-laid out after a SetSize: on
// the next resize event, or after a short wait when the size didn't change
// (the Go side pre-sizes the window, so often it doesn't).
function settleLayout(): Promise<void> {
  return new Promise((resolve) => {
    let done = false;
    let timer = 0;
    const finish = () => {
      if (done) return;
      done = true;
      window.removeEventListener("resize", finish);
      window.clearTimeout(timer);
      requestAnimationFrame(() => resolve());
    };
    timer = window.setTimeout(finish, 150);
    window.addEventListener("resize", finish);
  });
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
// The drag lives entirely on the handle via pointer capture (no window
// listeners that could outlive a lost pointerup and fight the next drag),
// ends cleanly on lostpointercapture, and always calls the latest onChange so
// clamping uses the other handle's current position. The grab area is wider
// than the visible bar.
function Handle({
  sec,
  duration,
  onChange,
}: {
  sec: number;
  duration: number;
  onChange: (s: number) => void;
}) {
  const drag = useRef<DOMRect | null>(null);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;

  const apply = (clientX: number) => {
    const rect = drag.current;
    if (!rect || duration <= 0) return;
    const frac = Math.max(0, Math.min(1, (clientX - rect.left) / rect.width));
    // Snap to 10ms so the readout's 2 decimals are exact, not 0.4999….
    onChangeRef.current(Math.round(frac * duration * 100) / 100);
  };

  const left = duration > 0 ? (sec / duration) * 100 : 0;
  return (
    <div
      onPointerDown={(e) => {
        if (duration <= 0) return;
        e.preventDefault(); // no text selection / native drag
        e.stopPropagation(); // don't trigger the track's click-to-seek
        e.currentTarget.setPointerCapture(e.pointerId);
        drag.current = e.currentTarget.parentElement!.getBoundingClientRect();
      }}
      onPointerMove={(e) => apply(e.clientX)}
      onPointerUp={(e) => {
        apply(e.clientX);
        drag.current = null;
        if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
      }}
      onLostPointerCapture={() => {
        drag.current = null;
      }}
      className="group/handle absolute top-0 z-10 flex h-full w-5 -translate-x-1/2 cursor-ew-resize touch-none justify-center"
      style={{ left: `${left}%` }}
    >
      <div className="h-full w-3 bg-primary transition-[width] group-hover/handle:w-3.5" />
    </div>
  );
}
