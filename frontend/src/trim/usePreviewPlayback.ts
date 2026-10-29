import { useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { locate, outputDuration, toOutput, type Edl, type Seg } from "@/trim/edl";

// usePreviewPlayback plays an EDL in OUTPUT time on one <video>, so the
// preview is what exports: at the end of a segment it seeks to the next one,
// swapping the video's src when the next segment is from another source, and
// it stops at the end of the last segment.
//
// The hook owns the video's src and currentTime: the window renders a bare
// <video ref={videoRef} preload="auto" /> and drives it only through play /
// pause / seek. Segment ends are checked every animation frame (timeupdate
// fires ~4x a second, far too late to skip a cut cleanly).
//
// Edits while playing keep the current segment if it still exists (by id);
// otherwise playback re-locates to the same output time, clamped.

// How close to a segment's end counts as "reached it" (s). Half a 60fps frame.
const END_EPS = 1 / 120;
// Two segments of one source this close in source time play straight through
// with no seek (a split you haven't cut anything out of).
const CONTIGUOUS = 1 / 60;

export function usePreviewPlayback(videoRef: RefObject<HTMLVideoElement | null>, edl: Edl) {
  const [outTime, setOutTime] = useState(0);
  const [playing, setPlaying] = useState(false);

  const edlRef = useRef(edl);
  edlRef.current = edl;
  const outRef = useRef(0);
  const playingRef = useRef(false);
  const segIdRef = useRef<string | null>(null);
  // The url loaded in the video, and a pending load: its token (newer loads
  // win) and the source time to show once its metadata is in.
  const urlRef = useRef<string | null>(null);
  const pendingRef = useRef<{ token: number; srcT: number } | null>(null);
  const tokenRef = useRef(0);

  const setOut = useCallback((t: number) => {
    outRef.current = t;
    setOutTime(t);
  }, []);

  const stop = useCallback(() => {
    playingRef.current = false;
    setPlaying(false);
    videoRef.current?.pause();
  }, [videoRef]);

  const resume = useCallback(
    (v: HTMLVideoElement) => {
      if (playingRef.current && v.paused) v.play().catch(stop);
    },
    [stop],
  );

  // show puts the video on seg's source at source time srcT, loading the
  // source first when it isn't the one in the video.
  const show = useCallback(
    (seg: Seg, srcT: number) => {
      segIdRef.current = seg.id;
      const v = videoRef.current;
      const url = edlRef.current.sources[seg.source]?.url;
      if (!v || !url) return;
      if (urlRef.current !== url) {
        urlRef.current = url;
        const token = ++tokenRef.current;
        pendingRef.current = { token, srcT };
        const done = (ok: boolean) => {
          const p = pendingRef.current;
          if (!p || p.token !== token) return;
          pendingRef.current = null;
          if (!ok) return stop();
          v.currentTime = p.srcT;
          resume(v);
        };
        v.addEventListener("loadedmetadata", () => done(true), { once: true });
        v.addEventListener("error", () => done(false), { once: true });
        v.src = url;
        return;
      }
      if (pendingRef.current) {
        pendingRef.current.srcT = srcT;
        return;
      }
      v.currentTime = srcT;
      resume(v);
    },
    [videoRef, stop, resume],
  );

  const seek = useCallback(
    (outT: number) => {
      const e = edlRef.current;
      const t = Math.max(0, Math.min(outT, outputDuration(e)));
      const hit = locate(e, t);
      if (!hit) return;
      setOut(t);
      show(hit.seg, hit.srcT);
    },
    [setOut, show],
  );

  const play = useCallback(() => {
    if (outRef.current >= outputDuration(edlRef.current) - END_EPS) seek(0);
    playingRef.current = true;
    setPlaying(true);
    const v = videoRef.current;
    if (v && !pendingRef.current) v.play().catch(stop);
  }, [videoRef, seek, stop]);

  const pause = useCallback(() => stop(), [stop]);

  // Follow edits: keep the current segment when it survived (re-showing it if
  // an edge moved past the playhead), else re-locate by output time. The first
  // run loads the first source at 0.
  useEffect(() => {
    const v = videoRef.current;
    const seg = edl.segs.find((s) => s.id === segIdRef.current);
    if (!v || !seg || pendingRef.current || urlRef.current !== edl.sources[seg.source]?.url) {
      seek(outRef.current);
      return;
    }
    const t = v.currentTime;
    if (t < seg.start || t > seg.end) show(seg, Math.max(seg.start, Math.min(t, seg.end)));
    setOut(toOutput(edl, seg.id, Math.max(seg.start, Math.min(t, seg.end))));
  }, [edl, videoRef, seek, show, setOut]);

  // The playback clock: every frame, map the video's time to output time and
  // hop to the next segment at a segment's end. timeupdate ticks too, because
  // a minimized or hidden window gets few or no animation frames while the
  // video keeps playing.
  useEffect(() => {
    if (!playing) return;
    let raf = 0;
    const frame = () => {
      raf = requestAnimationFrame(frame);
      tick();
    };
    const tick = () => {
      const v = videoRef.current;
      const e = edlRef.current;
      if (!v || pendingRef.current) return;
      const i = e.segs.findIndex((s) => s.id === segIdRef.current);
      if (i < 0) return;
      const seg = e.segs[i];
      const t = v.currentTime;
      if (t < seg.end - END_EPS && !v.ended) {
        setOut(toOutput(e, seg.id, t));
        return;
      }
      const next = e.segs[i + 1];
      if (!next) {
        stop();
        setOut(outputDuration(e));
        return;
      }
      setOut(toOutput(e, next.id, next.start));
      const sameUrl = e.sources[next.source]?.url === urlRef.current;
      if (sameUrl && Math.abs(next.start - t) < CONTIGUOUS && !v.ended) segIdRef.current = next.id;
      else show(next, next.start);
    };
    const v = videoRef.current;
    v?.addEventListener("timeupdate", tick);
    raf = requestAnimationFrame(frame);
    return () => {
      cancelAnimationFrame(raf);
      v?.removeEventListener("timeupdate", tick);
    };
  }, [playing, videoRef, show, stop, setOut]);

  useEffect(() => () => videoRef.current?.pause(), [videoRef]);

  return { outTime, playing, play, pause, seek, duration: outputDuration(edl) };
}
