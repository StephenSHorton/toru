import { useCallback, useEffect, useRef, useState } from "react";
import { MediaService } from "@/lib/api";
import { toMedia } from "@/trim/MediaBin";
import { placeVoiceTake, type Project } from "@/trim/project";
import { listMics, matchMic, replayMic, VoiceRecorder, type MicOption } from "@/trim/voice/recorder";

// useVoiceSession runs a mic voiceover take in the editor (Irori #129):
// open the mic (metering starts), count in 3-2-1, then record while the
// preview plays from the playhead, so the person talks over the video.
// Stop puts the take on a "Voice" audio track where recording started
// (shifted by the measured latency), as one undo step.
//
// The preview clock is the timeline: if it stalls on a clip still loading,
// the mic keeps going, so the take drifts by that stall (rare, short).

export type VoicePhase = "idle" | "opening" | "counting" | "recording" | "saving";

export interface VoiceState {
  phase: VoicePhase;
  count: number; // 3, 2, 1 while counting
  level: number; // 0..1 peak, while the mic is open
  elapsed: number; // s recorded
  error: string;
  device: string; // the mic in use (label)
}

export interface VoiceStartOptions {
  deviceId?: string; // a WebView device id (mics()); "" = the Replay settings mic
  countIn?: boolean; // default true
}

export interface VoiceTakeOut {
  clipId: string;
  trackId: string;
  start: number;
  seconds: number;
  path: string;
}

export interface VoiceControl {
  state: VoiceState;
  mics: () => Promise<{ mics: MicOption[]; preferred: string }>;
  start: (o?: VoiceStartOptions) => Promise<void>;
  stop: () => Promise<VoiceTakeOut>;
  cancel: () => Promise<void>;
}

const IDLE: VoiceState = { phase: "idle", count: 0, level: 0, elapsed: 0, error: "", device: "" };
export const COUNT_IN = 3;

type Clock = { now(): number; seek(t: number): void; play(): void; pause(): void; playing: boolean; duration: number };

export function useVoiceSession({
  project,
  apply,
  clock,
  onPlaced,
}: {
  project: Project;
  apply: (fn: (p: Project) => Project) => void;
  clock: Clock;
  onPlaced?: (clipId: string) => void;
}): VoiceControl {
  const [state, setState] = useState<VoiceState>(IDLE);
  const rec = useRef<VoiceRecorder | null>(null);
  const startAt = useRef(0); // timeline time recording started
  const cancelled = useRef(false);
  const projectRef = useRef(project);
  projectRef.current = project;
  const busy = useRef(false);
  const patch = useCallback((p: Partial<VoiceState>) => setState((s) => ({ ...s, ...p })), []);

  // Elapsed time while recording.
  useEffect(() => {
    if (state.phase !== "recording") return;
    const t0 = performance.now();
    const id = window.setInterval(() => patch({ elapsed: (performance.now() - t0) / 1000 }), 100);
    return () => window.clearInterval(id);
  }, [state.phase, patch]);

  // Closing the window releases the mic.
  useEffect(() => () => void rec.current?.close(), []);

  const mics = useCallback(async () => {
    const [list, want] = [await listMics(), await replayMic()];
    return { mics: list, preferred: matchMic(list, want)?.id ?? "" };
  }, []);

  const release = useCallback(async () => {
    const r = rec.current;
    rec.current = null;
    await r?.close();
  }, []);

  const start = useCallback(
    async (o: VoiceStartOptions = {}) => {
      if (busy.current) throw new Error("a voiceover take is already in progress");
      busy.current = true;
      cancelled.current = false;
      try {
        patch({ ...IDLE, phase: "opening" });
        let id = o.deviceId ?? "";
        if (!id) id = (await mics()).preferred;
        const r = await VoiceRecorder.open(id);
        rec.current = r;
        r.onLevel = (level) => patch({ level });
        // Labels appear once the mic is allowed: match the Replay mic again
        // now and switch to it if the first open fell back to the default.
        if (!o.deviceId && !id) {
          const again = (await mics()).preferred;
          if (again) {
            await r.close();
            const r2 = await VoiceRecorder.open(again);
            rec.current = r2;
            r2.onLevel = (level) => patch({ level });
          }
        }
        patch({ device: rec.current!.label });
        clock.pause();
        // At the very end the clock would rewind to 0 on play: record from
        // the end instead (the take extends the project).
        if (o.countIn !== false) {
          for (let n = COUNT_IN; n > 0; n--) {
            if (cancelled.current) throw new Error("cancelled");
            patch({ phase: "counting", count: n });
            await new Promise((res) => window.setTimeout(res, 1000));
          }
        }
        if (cancelled.current) throw new Error("cancelled");
        await rec.current!.start(projectRef.current.id);
        startAt.current = clock.now();
        if (startAt.current < clock.duration - 1e-3) clock.play();
        patch({ phase: "recording", count: 0, elapsed: 0 });
      } catch (err) {
        await release();
        busy.current = false;
        const msg = err instanceof Error ? err.message : String(err);
        setState({ ...IDLE, error: msg === "cancelled" ? "" : micError(msg) });
        throw err instanceof Error ? err : new Error(msg);
      }
    },
    [clock, mics, patch, release],
  );

  const stop = useCallback(async (): Promise<VoiceTakeOut> => {
    const r = rec.current;
    if (!r?.recording) throw new Error("not recording");
    patch({ phase: "saving" });
    clock.pause();
    try {
      const lat = r.latency();
      const take = await r.stop();
      await release();
      const [m] = await MediaService.Register([take.path]);
      if (!m) throw new Error("the take was saved but couldn't be opened: " + take.path);
      const media = toMedia(m);
      const at = startAt.current - lat;
      const placed = placeVoiceTake(projectRef.current, media, at);
      if (!placed) throw new Error("the take is too short to place");
      apply(() => placed.project);
      const track = placed.project.tracks.find((t) => t.clips.some((c) => c.id === placed.clipId))!;
      const clip = track.clips.find((c) => c.id === placed.clipId)!;
      onPlaced?.(placed.clipId);
      setState(IDLE);
      return { clipId: placed.clipId, trackId: track.id, start: clip.start, seconds: take.seconds, path: take.path };
    } catch (err) {
      await release();
      const msg = err instanceof Error ? err.message : String(err);
      setState({ ...IDLE, error: `Couldn't save the take: ${msg}` });
      throw err;
    } finally {
      busy.current = false;
    }
  }, [apply, clock, onPlaced, patch, release]);

  const cancel = useCallback(async () => {
    cancelled.current = true;
    if (rec.current?.recording) clock.pause();
    await release();
    busy.current = false;
    setState(IDLE);
  }, [clock, release]);

  return { state, mics, start, stop, cancel };
}

function micError(msg: string): string {
  if (/NotAllowed|Permission/i.test(msg)) return "Toru isn't allowed to use the mic (Windows Settings > Privacy > Microphone).";
  if (/NotFound|Requested device not found/i.test(msg)) return "No mic found. Plug one in or pick another.";
  if (/NotReadable|Could not start/i.test(msg)) return "The mic is busy or unavailable.";
  return `Couldn't open the mic: ${msg}`;
}
