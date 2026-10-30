import { useEffect, useLayoutEffect, useRef, useState, type MutableRefObject, type Ref } from "react";
import { VideoService } from "@/lib/api";
import { cn } from "@/lib/utils";
import { clipsAt, timelineTimeAt, type ActiveClip, type Clip, type Media, type Project } from "@/trim/project";
import { AudioMix, clipGain } from "@/trim/compositor/audio";
import { FxRenderer } from "@/trim/fx/renderer";
import { clipStack } from "@/trim/fx/stack";
import type { PlaybackClock } from "@/trim/compositor/usePlayback";

// Compositor is the v2 editor's preview (docs/video-editor-v2.md, Preview
// compositor): a WebGL2 canvas with the project's aspect that draws, every
// frame, the clips under the clock's time bottom→top, each through its
// effects (fx/renderer.ts) and placed by its transform and opacity; and that
// plays their audio through the AudioMix.
//
// Media: one hidden <video> per video clip being shown (an <audio> per audio
// track clip), from a pool keyed by clip id and reused while the clip lives.
// Each is kept at its clip's source time: while playing it plays at the
// clip's speed (pitch kept), small drift is pulled in by nudging the rate, and
// past MAX_DRIFT it re-seeks; while paused it seeks to the exact frame.
// Pausing doesn't seek: the clock moves to the frame the top video stopped on
// (a seek decodes from the last keyframe, a visible stall on long
// recordings). Resuming holds the clock until that video is really playing,
// so it doesn't start behind and re-seek. A video that has no new frame yet
// (seeking, loading) keeps showing its last one instead of going black.
// Clips about to start are loaded and seeked ahead (PREROLL) so cuts don't
// wait, and if a clip still has no frame when it's due the clock holds.
// Images are textures, loaded once per media.
//
// A clip recorded with separate tracks whose per-track levels were changed
// (clip.levels) plays those tracks instead of its mix: each is extracted by
// Go (VideoService.PreviewTrack) and played from its own <audio>, synced like
// the video and set to its level; until they are ready the mix plays.
//
// Text overlays, crop and the transform handles are DOM layers the window
// lays over the canvas (canvasRef gives the element to measure).

const MAX_DRIFT = 0.08; // s: past this a playing element re-seeks
// Small drift is pulled in by running a little fast or slow, in a few fixed
// steps so an element with sound isn't re-rated every frame: past each
// drift (s) the rate moves by that fraction.
const NUDGES: [number, number][] = [
  [0.03, 0.1],
  [0.012, 0.04],
  [1 / 240, 0.015],
];
const PREROLL = 1; // s: load clips starting this soon
const MAX_STALL = 1.5; // s: hold the clock at most this long for a frame
const POOL_IDLE = 8; // s: drop elements unused this long
const MAX_POOL = 16;

export type CompositorStats = {
  frames: number;
  cap?: number; // frames kept in log (default 600)
  // Per frame: clock time, frame interval (ms), whether the clock was held,
  // each shown video's drift (element time - wanted source time, s) and
  // each playing element's gain, by clip id.
  log: { t: number; dt: number; stalled: boolean; drift: Record<string, number>; gain: Record<string, number> }[];
};

// CompositorHandle is what the window gets back through `handle`.
export interface CompositorHandle {
  // pickColor reads the colour drawn at (nx, ny), 0..1 of the canvas, on the
  // next frame, as "#rrggbb". With rawClipId that clip is drawn without its
  // green screen for that frame, so the eyedropper sees the colour it keys.
  pickColor(nx: number, ny: number, rawClipId?: string): Promise<string>;
  // snapshot copies the next frame the preview draws onto a 2D canvas (the
  // GL buffer is only readable inside the frame that drew it).
  snapshot(): Promise<HTMLCanvasElement>;
}

export interface CompositorProps {
  project: Project;
  clock: PlaybackClock;
  className?: string;
  canvasRef?: Ref<HTMLCanvasElement>;
  handle?: MutableRefObject<CompositorHandle | null>;
  // For tests: per-frame timing and drift (last 600 frames).
  stats?: MutableRefObject<CompositorStats | null>;
}

type Slot = {
  el: HTMLVideoElement | HTMLAudioElement;
  url: string;
  lastUsed: number;
  tex: WebGLTexture | null;
  uploaded: number; // element time of the frame in tex (-1 none)
  starting: boolean; // a play() is pending
  forcedMute: boolean; // muted because the browser blocked play() with sound
  w: number; // size of the frame in tex
  h: number;
};
type Still = { img: HTMLImageElement; tex: WebGLTexture | null; ready: boolean };

export function Compositor({ project, clock, className, canvasRef, handle, stats }: CompositorProps) {
  const boxRef = useRef<HTMLDivElement>(null);
  const canvas = useRef<HTMLCanvasElement | null>(null);
  const projectRef = useRef(project);
  projectRef.current = project;
  const [css, setCss] = useState({ w: 0, h: 0 });

  // Fit the canvas's aspect inside the box; the backing store is that size
  // in device pixels, at most the project's canvas.
  const aspect = project.canvas.width / Math.max(1, project.canvas.height);
  useLayoutEffect(() => {
    const box = boxRef.current;
    if (!box) return;
    const fit = () => {
      const bw = box.clientWidth;
      const bh = box.clientHeight;
      if (bw <= 0 || bh <= 0) return;
      const w = Math.min(bw, bh * aspect);
      setCss({ w: Math.floor(w), h: Math.floor(w / aspect) });
    };
    const ro = new ResizeObserver(fit);
    ro.observe(box);
    fit();
    return () => ro.disconnect();
  }, [aspect]);

  const setCanvas = (el: HTMLCanvasElement | null) => {
    canvas.current = el;
    if (typeof canvasRef === "function") canvasRef(el);
    else if (canvasRef) (canvasRef as MutableRefObject<HTMLCanvasElement | null>).current = el;
  };

  useEffect(() => {
    const cv = canvas.current;
    if (!cv) return;
    const gl = cv.getContext("webgl2", { alpha: false, antialias: false, premultipliedAlpha: false, preserveDrawingBuffer: false });
    if (!gl) return;
    const prog = layerProgram(gl);
    const loc = {
      rect: gl.getUniformLocation(prog, "u_rect"),
      tex: gl.getUniformLocation(prog, "u_tex"),
      opacity: gl.getUniformLocation(prog, "u_opacity"),
    };
    const vao = gl.createVertexArray();
    const effects = new FxRenderer(gl);
    const mix = new AudioMix();
    const pool = new Map<string, Slot>();
    const stills = new Map<string, Still>();
    let stallSince = 0;
    let lastFrame = performance.now();
    let raf = 0;
    // Colour picks waiting for the next frame.
    let picks: { nx: number; ny: number; raw?: string; done: (hex: string) => void }[] = [];
    let snaps: ((c: HTMLCanvasElement) => void)[] = [];
    if (handle) {
      handle.current = {
        pickColor: (nx, ny, raw) => new Promise((done) => picks.push({ nx, ny, raw, done })),
        snapshot: () => new Promise((done) => snaps.push(done)),
      };
    }
    let wasPlaying = false;

    const texture = () => {
      const t = gl.createTexture()!;
      gl.bindTexture(gl.TEXTURE_2D, t);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_S, gl.CLAMP_TO_EDGE);
      gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_WRAP_T, gl.CLAMP_TO_EDGE);
      return t;
    };

    const slotAt = (key: string, url: string, audioOnly: boolean, now: number): Slot => {
      let s = pool.get(key);
      if (!s || s.url !== url) {
        if (s) drop(key);
        const el = document.createElement(audioOnly ? "audio" : "video");
        el.preload = "auto";
        el.crossOrigin = "anonymous";
        if (el instanceof HTMLVideoElement) el.playsInline = true;
        el.preservesPitch = true;
        el.src = url;
        s = { el, url, lastUsed: now, tex: null, uploaded: -1, starting: false, forcedMute: false, w: 0, h: 0 };
        pool.set(key, s);
      }
      s.lastUsed = now;
      return s;
    };
    const slotFor = (clip: Clip, media: Media, audioOnly: boolean, now: number) => slotAt(clip.id, media.url, audioOnly, now);

    // Extracted source tracks, by "path|index": the served URL once ready,
    // null when extraction failed (the mix keeps playing).
    const trackUrls = new Map<string, string | null>();
    const trackUrl = (path: string, index: number): string | null | undefined => {
      const key = `${path}|${index}`;
      if (!trackUrls.has(key)) {
        trackUrls.set(key, null);
        pendingTracks.add(key);
        VideoService.PreviewTrack(path, index)
          .then((url) => {
            if (disposed) void VideoService.ReleasePreviewTracks([url]);
            else trackUrls.set(key, url);
          })
          .catch(() => trackUrls.set(key, null))
          .finally(() => pendingTracks.delete(key));
      }
      return pendingTracks.has(key) ? undefined : trackUrls.get(key);
    };
    const pendingTracks = new Set<string>();
    let disposed = false;

    const drop = (id: string) => {
      const s = pool.get(id);
      if (!s) return;
      s.el.pause();
      mix.forget(s.el);
      s.el.removeAttribute("src");
      s.el.load();
      if (s.tex) gl.deleteTexture(s.tex);
      pool.delete(id);
    };

    const stillFor = (m: Media): Still => {
      let s = stills.get(m.id);
      if (!s) {
        const img = new Image();
        img.crossOrigin = "anonymous";
        const still: Still = { img, tex: null, ready: false };
        img.onload = () => {
          still.tex = texture();
          gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
          gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, img);
          still.ready = true;
        };
        img.src = m.url;
        stills.set(m.id, (s = still));
      }
      return s;
    };

    // holdOnPause runs on the frame playback pauses: it stops the top video
    // where it is and moves the clock to that frame, so the sync below finds
    // nothing to seek and the picture stays put.
    const holdOnPause = (p: Project) => {
      const t = clock.now();
      const top = clipsAt(p, t)
        .reverse()
        .find((a) => a.track.kind === "video" && a.media.kind !== "image" && pool.has(a.clip.id));
      const el = top && pool.get(top.clip.id)?.el;
      if (!top || !el) return;
      el.pause();
      if (el.seeking || el.readyState < 2) return;
      const at = timelineTimeAt(top.clip, el.currentTime);
      // Only a normal playing drift; anything bigger is a real position.
      if (Math.abs(at - t) <= MAX_DRIFT * Math.max(1, top.clip.speed) * 2) clock.seek(at);
    };

    // sync keeps an element at its clip's source time.
    const sync = (s: Slot, a: ActiveClip, playing: boolean, gain: number) => {
      const el = s.el;
      const want = a.srcT;
      if (playing) {
        const drift = el.currentTime - want;
        if (!el.seeking && Math.abs(drift) > MAX_DRIFT * Math.max(1, a.clip.speed)) {
          el.currentTime = want;
        }
        const step = NUDGES.find(([d]) => Math.abs(drift) > d)?.[1] ?? 0;
        const rate = a.clip.speed * (1 - Math.sign(drift) * step);
        if (Math.abs(el.playbackRate - rate) > 1e-3) el.playbackRate = rate;
        if (el.paused && !el.ended && !s.starting) {
          // A play() the browser blocks (no user gesture yet, autoplay
          // policy) retries muted, so the picture keeps time; the sound
          // comes back once a gesture lets the AudioMix resume.
          s.starting = true;
          el.play()
            .catch((err) => {
              if (err?.name === "NotAllowedError" && !el.muted) {
                el.muted = true;
                s.forcedMute = true;
                return el.play().catch(() => {});
              }
            })
            .finally(() => (s.starting = false));
        }
      } else {
        if (!el.paused) el.pause();
        if (!el.seeking && Math.abs(el.currentTime - want) > 1e-3) el.currentTime = want;
      }
      if (s.forcedMute && mix.running) {
        el.muted = false;
        s.forcedMute = false;
      }
      mix.set(el, playing ? gain : 0);
    };

    const frame = () => {
      raf = requestAnimationFrame(frame);
      const now = performance.now();
      const dt = now - lastFrame;
      lastFrame = now;
      const p = projectRef.current;
      const playing = clock.playing;
      if (playing && !wasPlaying) mix.resume();
      if (!playing && wasPlaying) holdOnPause(p);
      wasPlaying = playing;
      const t = clock.now();
      const active = clipsAt(p, t);
      const used = new Set<string>();

      // Media for everything under the playhead.
      let starving = false;
      const gain: Record<string, number> = {};
      for (const a of active) {
        if (a.media.kind === "image") continue;
        const audioOnly = a.track.kind === "audio";
        const s = slotFor(a.clip, a.media, audioOnly, now);
        used.add(a.clip.id);
        const g = clipGain(a.clip, a.track, t);
        // Per-track levels: play the extracted tracks once all are ready.
        const levels = a.media.tracks.length > 1 ? (a.clip.levels ?? []) : [];
        const urls = levels.map((l) => trackUrl(a.media.path, l.index));
        const byTrack = levels.length > 0 && urls.every((u) => typeof u === "string");
        sync(s, a, playing, byTrack ? 0 : g);
        if (byTrack) {
          levels.forEach((l, i) => {
            const key = `${a.clip.id}|t${l.index}`;
            const ts = slotAt(key, urls[i] as string, true, now);
            used.add(key);
            sync(ts, a, playing, l.muted ? 0 : (g * l.volume) / 100);
            if (stats) gain[key] = mix.level(ts.el);
          });
        }
        if (stats) gain[a.clip.id] = mix.level(s.el);
        // Hold the clock while the picture can't keep up: no frame yet, or a
        // play() that hasn't started (resuming would otherwise run ahead and
        // force a re-seek).
        if (!audioOnly && playing && (s.el.readyState < 2 || s.starting)) starving = true;
      }
      // Load what's about to start, parked at its first frame.
      if (playing) {
        for (const track of p.tracks) {
          if (track.kind === "video" && track.hidden) continue;
          for (const c of track.clips) {
            if (c.start <= t || c.start > t + PREROLL || used.has(c.id)) continue;
            const m = p.media.find((x) => x.id === c.media);
            if (!m || m.kind === "image") continue;
            const s = slotFor(c, m, track.kind === "audio", now);
            used.add(c.id);
            if (!s.el.paused) s.el.pause();
            if (!s.el.seeking && Math.abs(s.el.currentTime - c.in) > 1e-3) s.el.currentTime = c.in;
            mix.set(s.el, 0);
          }
        }
      }
      // Everything else in the pool goes quiet; long-idle elements go.
      for (const [id, s] of pool) {
        if (used.has(id)) continue;
        if (!s.el.paused) s.el.pause();
        mix.set(s.el, 0);
        if (now - s.lastUsed > POOL_IDLE * 1000 || pool.size > MAX_POOL) drop(id);
      }
      // Hold the clock while a clip that's due has no frame yet.
      if (starving) {
        if (!stallSince) stallSince = now;
        clock.setStalled(now - stallSince < MAX_STALL * 1000);
      } else {
        stallSince = 0;
        clock.setStalled(false);
      }

      // Draw.
      const W = cv.width;
      const H = cv.height;
      gl.bindFramebuffer(gl.FRAMEBUFFER, null);
      gl.viewport(0, 0, W, H);
      gl.clearColor(0, 0, 0, 1);
      gl.clear(gl.COLOR_BUFFER_BIT);
      const drift: Record<string, number> = {};
      const rawClips = new Set(picks.map((k) => k.raw).filter(Boolean));
      for (const a of active) {
        if (a.track.kind !== "video") continue;
        let tex: WebGLTexture | null = null;
        let w = 0;
        let h = 0;
        if (a.media.kind === "image") {
          const st = stillFor(a.media);
          if (!st.ready) continue;
          tex = st.tex;
          w = st.img.naturalWidth;
          h = st.img.naturalHeight;
        } else {
          const s = pool.get(a.clip.id);
          const v = s?.el as HTMLVideoElement | undefined;
          if (!s || !v) continue;
          // Upload only a decoded frame; while seeking or loading, draw the
          // last one (none yet: skip the layer).
          if (v.readyState >= 2 && !v.seeking && v.videoWidth > 0) {
            if (!s.tex) s.tex = texture();
            if (playing || s.uploaded !== v.currentTime) {
              gl.bindTexture(gl.TEXTURE_2D, s.tex);
              gl.pixelStorei(gl.UNPACK_PREMULTIPLY_ALPHA_WEBGL, false);
              gl.texImage2D(gl.TEXTURE_2D, 0, gl.RGBA, gl.RGBA, gl.UNSIGNED_BYTE, v);
              s.uploaded = v.currentTime;
              s.w = v.videoWidth;
              s.h = v.videoHeight;
            }
          } else if (s.uploaded < 0) continue;
          tex = s.tex;
          w = s.w;
          h = s.h;
          drift[a.clip.id] = v.currentTime - a.srcT;
        }
        if (!tex || w <= 0 || h <= 0) continue;
        const out = effects.apply(tex, w, h, clipStack(a.clip, t - a.clip.start, rawClips.has(a.clip.id)), t - a.clip.start);
        drawLayer(gl, prog, vao, loc, out, w, h, a.clip, W, H);
      }
      // Read picks now, while this frame's pixels are still in the buffer.
      if (picks.length) {
        const px = new Uint8Array(4);
        for (const k of picks) {
          const x = Math.max(0, Math.min(W - 1, Math.floor(k.nx * W)));
          const y = Math.max(0, Math.min(H - 1, Math.floor((1 - k.ny) * H)));
          gl.readPixels(x, y, 1, 1, gl.RGBA, gl.UNSIGNED_BYTE, px);
          k.done("#" + [px[0], px[1], px[2]].map((v) => v.toString(16).padStart(2, "0")).join(""));
        }
        picks = [];
      }
      if (snaps.length) {
        const copy = document.createElement("canvas");
        copy.width = cv.width;
        copy.height = cv.height;
        copy.getContext("2d")?.drawImage(cv, 0, 0);
        for (const done of snaps) done(copy);
        snaps = [];
      }

      if (stats) {
        const st = (stats.current ??= { frames: 0, log: [] });
        st.frames++;
        st.log.push({ t, dt, stalled: clock.isStalled, drift, gain });
        if (st.log.length > (st.cap ?? 600)) st.log.shift();
      }
    };
    raf = requestAnimationFrame(frame);

    return () => {
      disposed = true;
      cancelAnimationFrame(raf);
      const extracted = [...trackUrls.values()].filter((u): u is string => !!u);
      if (extracted.length) void VideoService.ReleasePreviewTracks(extracted).catch(() => {});
      if (handle) handle.current = null;
      for (const id of [...pool.keys()]) drop(id);
      for (const s of stills.values()) if (s.tex) gl.deleteTexture(s.tex);
      effects.dispose();
      mix.dispose();
      gl.deleteProgram(prog);
      gl.deleteVertexArray(vao);
    };
  }, [clock, stats, handle]);

  // Backing store size: the CSS size in device pixels, capped at the
  // project's canvas.
  const dpr = typeof window !== "undefined" ? window.devicePixelRatio || 1 : 1;
  const bw = Math.max(1, Math.min(project.canvas.width, Math.round(css.w * dpr)));
  const bh = Math.max(1, Math.round(bw / aspect));

  return (
    <div ref={boxRef} className={cn("relative flex min-h-0 min-w-0 items-center justify-center overflow-hidden", className)}>
      <canvas ref={setCanvas} width={bw} height={bh} style={{ width: css.w, height: css.h }} className="block bg-black" />
    </div>
  );
}

// layerProgram draws one textured quad (a clip layer) with an opacity.
function layerProgram(gl: WebGL2RenderingContext): WebGLProgram {
  const vs = `#version 300 es
uniform vec4 u_rect; // x0, y0 (top-left), x1, y1 in clip space
out vec2 v_uv;
void main() {
  vec2 c = vec2(gl_VertexID & 1, (gl_VertexID >> 1) & 1); // 0,0 1,0 0,1 1,1
  v_uv = c;
  gl_Position = vec4(mix(u_rect.x, u_rect.z, c.x), mix(u_rect.y, u_rect.w, c.y), 0.0, 1.0);
}`;
  const fs = `#version 300 es
precision mediump float;
uniform sampler2D u_tex;
uniform float u_opacity;
in vec2 v_uv;
out vec4 o;
void main() {
  vec4 c = texture(u_tex, v_uv);
  o = vec4(c.rgb, c.a * u_opacity);
}`;
  const p = gl.createProgram()!;
  for (const [type, src] of [
    [gl.VERTEX_SHADER, vs],
    [gl.FRAGMENT_SHADER, fs],
  ] as const) {
    const s = gl.createShader(type)!;
    gl.shaderSource(s, src);
    gl.compileShader(s);
    if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(`compositor shader: ${gl.getShaderInfoLog(s)}`);
    gl.attachShader(p, s);
  }
  gl.linkProgram(p);
  if (!gl.getProgramParameter(p, gl.LINK_STATUS)) throw new Error(`compositor program: ${gl.getProgramInfoLog(p)}`);
  return p;
}

// drawLayer places a w×h frame on the W×H canvas by the clip's transform:
// scale 1 fits it inside the canvas, centred on (x, y) in 0..1 of the canvas.
function drawLayer(
  gl: WebGL2RenderingContext,
  prog: WebGLProgram,
  vao: WebGLVertexArrayObject | null,
  loc: { rect: WebGLUniformLocation | null; tex: WebGLUniformLocation | null; opacity: WebGLUniformLocation | null },
  tex: WebGLTexture,
  w: number,
  h: number,
  clip: Clip,
  W: number,
  H: number,
) {
  const tr = clip.transform;
  if (tr.opacity <= 0 || tr.scale <= 0) return;
  const fit = Math.min(W / w, H / h) * tr.scale;
  const dw = w * fit;
  const dh = h * fit;
  const cx = tr.x * W;
  const cy = tr.y * H;
  // Pixel rect (top-left origin) → clip space (y up). Texture row 0 is the
  // frame's top, so the quad's top edge gets v = 0.
  const x0 = ((cx - dw / 2) / W) * 2 - 1;
  const x1 = ((cx + dw / 2) / W) * 2 - 1;
  const y0 = 1 - ((cy - dh / 2) / H) * 2;
  const y1 = 1 - ((cy + dh / 2) / H) * 2;
  gl.useProgram(prog);
  gl.bindVertexArray(vao);
  gl.activeTexture(gl.TEXTURE0);
  gl.bindTexture(gl.TEXTURE_2D, tex);
  // The effects output is NEAREST-sampled; scaling a layer wants LINEAR.
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR);
  gl.texParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR);
  gl.uniform1i(loc.tex, 0);
  gl.uniform4f(loc.rect, x0, y0, x1, y1);
  gl.uniform1f(loc.opacity, Math.min(1, tr.opacity));
  gl.enable(gl.BLEND);
  gl.blendFuncSeparate(gl.SRC_ALPHA, gl.ONE_MINUS_SRC_ALPHA, gl.ONE, gl.ONE_MINUS_SRC_ALPHA);
  gl.drawArrays(gl.TRIANGLE_STRIP, 0, 4);
  gl.bindVertexArray(null);
}
