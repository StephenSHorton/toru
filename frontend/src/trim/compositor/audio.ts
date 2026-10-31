import { valueAt } from "@/trim/fx/keyframes";
import { clipEnd, type Clip, type Track } from "@/trim/project";
import { cutFade, trackTransitions } from "@/trim/transition";

// audio.ts is the v2 preview's audio mix (docs/video-editor-v2.md, Preview
// compositor): every playing <video> / <audio> element goes through its own
// MediaElementSource → GainNode → destination, and the Compositor sets each
// gain every frame from clipGain (volume × fades × clip / track mute). Speed
// is the element's playbackRate with pitch kept (preservesPitch), set by the
// Compositor.

// How fast a gain follows a new value (s, setTargetAtTime's time constant):
// short enough for fades, long enough that a step doesn't click.
const GAIN_SMOOTHING = 0.012;

// clipGain is a clip's level at timeline time t: its volume (with its
// keyframes, at clip-local time), ramped up over its fade in and down over
// its fade out (linear, like ffmpeg afade's default) and at a transition's
// cut, 0 when the clip or its track is muted.
export function clipGain(clip: Clip, track: Track, t: number): number {
  if (clip.muted || track.muted) return 0;
  const end = clipEnd(clip);
  let g = valueAt(clip.anim?.volume, t - clip.start, clip.volume) as number;
  if (clip.fadeIn > 0 && t < clip.start + clip.fadeIn) g *= Math.max(0, (t - clip.start) / clip.fadeIn);
  if (clip.fadeOut > 0 && t > end - clip.fadeOut) g *= Math.max(0, (end - t) / clip.fadeOut);
  // A transition's cut fades (transition.ts), on top of the clip's own.
  if (track.kind === "video" && (clip.transition || track.clips.some((c) => c.transition))) {
    g *= cutFade(trackTransitions(track), track.clips.indexOf(clip), t);
  }
  return Math.max(0, Math.min(2, g));
}

export class AudioMix {
  private ctx: AudioContext | null = null;
  private gains = new Map<HTMLMediaElement, GainNode>();

  // gainFor routes an element through its own gain node (once; an element
  // can only ever have one MediaElementSource).
  private gainFor(el: HTMLMediaElement): GainNode {
    let g = this.gains.get(el);
    if (!g) {
      const ctx = (this.ctx ??= new AudioContext({ latencyHint: "interactive" }));
      g = ctx.createGain();
      g.gain.value = 0;
      ctx.createMediaElementSource(el).connect(g).connect(ctx.destination);
      this.gains.set(el, g);
    }
    return g;
  }

  // set moves an element's level towards `gain` (0..2).
  set(el: HTMLMediaElement, gain: number) {
    const g = this.gainFor(el);
    const ctx = this.ctx!;
    if (Math.abs(g.gain.value - gain) < 1e-4) return;
    g.gain.setTargetAtTime(gain, ctx.currentTime, GAIN_SMOOTHING);
  }

  // level is an element's current gain (for tests and meters).
  level(el: HTMLMediaElement): number {
    return this.gains.get(el)?.gain.value ?? 0;
  }

  // resume starts the context (browsers start it suspended until a gesture).
  resume() {
    if (this.ctx?.state === "suspended") void this.ctx.resume();
  }

  // running says whether sound can play (the context isn't suspended).
  get running() {
    return this.ctx?.state === "running";
  }

  // forget drops an element that's being thrown away.
  forget(el: HTMLMediaElement) {
    this.gains.get(el)?.disconnect();
    this.gains.delete(el);
  }

  dispose() {
    for (const g of this.gains.values()) g.disconnect();
    this.gains.clear();
    void this.ctx?.close();
    this.ctx = null;
  }
}
