import { useCallback, useEffect, useMemo, useRef, useState } from "react";

// usePlayback is the v2 preview's clock (docs/video-editor-v2.md, Preview
// compositor): timeline time in seconds, run from performance.now() while
// playing. The Compositor reads clock.now() every frame and syncs the media
// to it; React state (time, playing) follows at most once per frame for the
// UI (playhead, buttons).
//
// The clock is the master. The Compositor can hold it (setStalled) while a
// video it needs right now hasn't got a frame yet, so a cut to a clip that is
// still loading waits instead of skipping ahead. Playing stops at `duration`.

export class PlaybackClock {
  private base = 0; // timeline time at baseNow
  private baseNow = 0; // performance.now() when base was set
  private running = false;
  private stalled = false;
  duration = 0;
  // Called on play/pause/seek and when playback ends.
  onChange: (() => void) | null = null;

  get playing() {
    return this.running;
  }

  // now is the current timeline time.
  now(): number {
    if (!this.running || this.stalled) return this.base;
    const t = this.base + (performance.now() - this.baseNow) / 1000;
    if (t >= this.duration) {
      this.base = this.duration;
      this.running = false;
      queueMicrotask(() => this.onChange?.());
      return this.duration;
    }
    return t;
  }

  play() {
    if (this.running) return;
    if (this.base >= this.duration - 1e-3) this.base = 0;
    this.baseNow = performance.now();
    this.running = true;
    this.onChange?.();
  }

  pause() {
    if (!this.running) return;
    this.base = this.now();
    this.running = false;
    this.onChange?.();
  }

  seek(t: number) {
    this.base = Math.max(0, Math.min(this.duration, Number.isFinite(t) ? t : 0));
    this.baseNow = performance.now();
    this.onChange?.();
  }

  // setStalled freezes time while true (the clock resumes where it stopped).
  setStalled(on: boolean) {
    if (on === this.stalled) return;
    if (on) this.base = this.now();
    this.stalled = on;
    this.baseNow = performance.now();
  }

  get isStalled() {
    return this.stalled;
  }
}

export function usePlayback(duration: number) {
  const clock = useMemo(() => new PlaybackClock(), []);
  clock.duration = duration;
  const [time, setTime] = useState(0);
  const [playing, setPlaying] = useState(false);

  // Follow the clock into React state, once per frame while playing.
  const raf = useRef(0);
  useEffect(() => {
    const sync = () => {
      setTime(clock.now());
      setPlaying(clock.playing);
    };
    const loop = () => {
      sync();
      raf.current = clock.playing ? requestAnimationFrame(loop) : 0;
    };
    clock.onChange = () => {
      sync();
      if (clock.playing && !raf.current) raf.current = requestAnimationFrame(loop);
    };
    return () => {
      clock.onChange = null;
      cancelAnimationFrame(raf.current);
      raf.current = 0;
    };
  }, [clock]);

  // A project that got shorter pulls the playhead back inside it.
  useEffect(() => {
    if (!clock.playing && clock.now() > duration) clock.seek(duration);
  }, [clock, duration]);

  const play = useCallback(() => clock.play(), [clock]);
  const pause = useCallback(() => clock.pause(), [clock]);
  const toggle = useCallback(() => (clock.playing ? clock.pause() : clock.play()), [clock]);
  const seek = useCallback((t: number) => clock.seek(t), [clock]);
  return { clock, time, playing, play, pause, toggle, seek };
}
