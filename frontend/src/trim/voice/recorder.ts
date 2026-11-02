import { ReplayService, VoiceoverService } from "@/lib/api";
import { toPcm16 } from "@/trim/voice/pcm";

export { listMics, matchMic, type MicOption } from "@/trim/voice/pcm";

// recorder.ts records a mic voiceover take for the editor (Irori #129).
//
// The mic is opened in the WebView (getUserMedia, WASAPI shared mode, so it
// doesn't fight the replay buffer's ffmpeg capture of the same mic) with
// echo cancellation, noise suppression and auto gain OFF: a take is the raw
// mic, a plain clip the noise suppression card can process later. An
// AudioWorklet copies the samples from the moment start() is called and
// meters the level the whole time the mic is open (during the count-in too).
// The main thread turns the samples into 16-bit PCM and streams them to
// VoiceoverService (Go), which writes the WAV as it goes.
//
// Alignment: start() returns the AudioContext time of the first recorded
// frame. The person hears the preview late by the output latency and their
// voice reaches the worklet late by the input latency, so what they say over
// timeline time T lands at sample (T - start + latency). latency() is that
// sum; the caller shifts the clip earlier by it.

const WORKLET = `
class VoiceTap extends AudioWorkletProcessor {
  constructor() {
    super();
    this.rec = false; this.buf = new Float32Array(4096); this.n = 0;
    this.peak = 0; this.pn = 0;
    this.port.onmessage = (e) => {
      if (e.data === "start") { this.rec = true; this.n = 0; this.port.postMessage({ started: currentTime }); }
      else if (e.data === "stop") { this.flush(); this.rec = false; this.port.postMessage({ stopped: true }); }
    };
  }
  flush() {
    if (this.n > 0) { const out = this.buf.slice(0, this.n); this.port.postMessage({ pcm: out }, [out.buffer]); this.n = 0; }
  }
  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (!ch) return true;
    for (let i = 0; i < ch.length; i++) {
      const a = ch[i] < 0 ? -ch[i] : ch[i];
      if (a > this.peak) this.peak = a;
      if (this.rec) { this.buf[this.n++] = ch[i]; if (this.n === this.buf.length) this.flush(); }
    }
    this.pn += ch.length;
    if (this.pn >= 1200) { this.port.postMessage({ level: this.peak }); this.peak = 0; this.pn = 0; }
    return true;
  }
}
registerProcessor("toru-voice-tap", VoiceTap);
`;

// FLUSH_SAMPLES: send to Go about every half second.
const FLUSH_SECONDS = 0.5;

// replayMic is the mic chosen in Replay settings (a dshow name), "" if none.
export async function replayMic(): Promise<string> {
  try {
    return (await ReplayService.GetSettings())?.micDevice ?? "";
  } catch {
    return "";
  }
}

function toBase64(b: Uint8Array): string {
  let s = "";
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000));
  return btoa(s);
}

export type TakeResult = { path: string; seconds: number };

export class VoiceRecorder {
  readonly label: string;
  onLevel: ((peak: number) => void) | null = null;
  private take = "";
  private pending: Uint8Array[] = [];
  private pendingBytes = 0;
  private sent: Promise<void> = Promise.resolve();
  private failed: unknown = null;
  private frames = 0;
  private stopped: (() => void) | null = null;
  private closed = false;

  private constructor(
    private ctx: AudioContext,
    private stream: MediaStream,
    private node: AudioWorkletNode,
    private source: MediaStreamAudioSourceNode,
  ) {
    const track = stream.getAudioTracks()[0];
    this.label = track?.label || "Mic";
    node.port.onmessage = (e) => this.onMessage(e.data);
  }

  // open asks for the mic (deviceId "" = the default one) and starts
  // metering. Nothing is recorded until start().
  static async open(deviceId: string): Promise<VoiceRecorder> {
    const constraints: MediaTrackConstraints = { echoCancellation: false, noiseSuppression: false, autoGainControl: false, channelCount: 1 };
    let stream: MediaStream;
    try {
      stream = await navigator.mediaDevices.getUserMedia({ audio: { ...constraints, deviceId: deviceId ? { exact: deviceId } : undefined } });
    } catch (err) {
      if (!deviceId) throw err;
      stream = await navigator.mediaDevices.getUserMedia({ audio: constraints }); // that mic is gone: the default one
    }
    const ctx = new AudioContext({ latencyHint: "interactive" });
    try {
      await ctx.audioWorklet.addModule(URL.createObjectURL(new Blob([WORKLET], { type: "application/javascript" })));
      const source = ctx.createMediaStreamSource(stream);
      const node = new AudioWorkletNode(ctx, "toru-voice-tap", { numberOfInputs: 1, numberOfOutputs: 0 });
      source.connect(node);
      if (ctx.state === "suspended") await ctx.resume();
      return new VoiceRecorder(ctx, stream, node, source);
    } catch (err) {
      stream.getTracks().forEach((t) => t.stop());
      void ctx.close();
      throw err;
    }
  }

  get sampleRate() {
    return this.ctx.sampleRate;
  }

  get recording() {
    return !!this.take;
  }

  // latency is how late, in seconds, a word said over the preview reaches
  // the take: the output latency (hearing the video) plus the input latency
  // (the mic), each measured when the browser reports it.
  latency(): number {
    const track = this.stream.getAudioTracks()[0];
    const s = track?.getSettings() as MediaTrackSettings & { latency?: number };
    const input = typeof s?.latency === "number" && s.latency >= 0 && s.latency < 1 ? s.latency : 0.01;
    const out = this.ctx.outputLatency;
    const output = (Number.isFinite(out) && out > 0 && out < 1 ? out : 0.02) + (this.ctx.baseLatency || 0);
    return input + output;
  }

  // start opens a take for the project and records from now.
  async start(projectId: string): Promise<void> {
    if (this.take || this.closed) throw new Error("already recording");
    this.take = await VoiceoverService.Begin(projectId, Math.round(this.ctx.sampleRate));
    this.frames = 0;
    this.failed = null;
    this.node.port.postMessage("start");
  }

  // stop ends the take and returns its WAV path and length.
  async stop(): Promise<TakeResult> {
    if (!this.take) throw new Error("not recording");
    await new Promise<void>((resolve) => {
      this.stopped = resolve;
      this.node.port.postMessage("stop");
      window.setTimeout(resolve, 1000); // the worklet is gone: keep what arrived
    });
    this.stopped = null;
    this.flush();
    const id = this.take;
    this.take = "";
    await this.sent;
    if (this.failed) {
      await VoiceoverService.Cancel(id).catch(() => {});
      throw this.failed;
    }
    const path = await VoiceoverService.Finish(id);
    return { path, seconds: this.frames / this.ctx.sampleRate };
  }

  // cancel drops the take being recorded (its file too).
  async cancel(): Promise<void> {
    if (!this.take) return;
    this.node.port.postMessage("stop");
    const id = this.take;
    this.take = "";
    this.pending = [];
    this.pendingBytes = 0;
    await this.sent.catch(() => {});
    await VoiceoverService.Cancel(id).catch(() => {});
  }

  // close releases the mic. A take still recording is cancelled.
  async close(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    await this.cancel();
    this.source.disconnect();
    this.node.port.onmessage = null;
    this.stream.getTracks().forEach((t) => t.stop());
    await this.ctx.close().catch(() => {});
  }

  private onMessage(d: { level?: number; pcm?: Float32Array; stopped?: boolean }) {
    if (typeof d.level === "number") this.onLevel?.(d.level);
    if (d.pcm && this.take) {
      this.frames += d.pcm.length;
      const b = toPcm16(d.pcm);
      this.pending.push(b);
      this.pendingBytes += b.length;
      if (this.pendingBytes >= this.ctx.sampleRate * 2 * FLUSH_SECONDS) this.flush();
    }
    if (d.stopped) this.stopped?.();
  }

  // flush sends what's pending to Go, in order after the earlier sends.
  private flush() {
    if (!this.pendingBytes || !this.take) return;
    const all = new Uint8Array(this.pendingBytes);
    let o = 0;
    for (const b of this.pending) {
      all.set(b, o);
      o += b.length;
    }
    this.pending = [];
    this.pendingBytes = 0;
    const id = this.take;
    const chunk = toBase64(all);
    this.sent = this.sent.then(() =>
      this.failed
        ? undefined
        : VoiceoverService.Append(id, chunk).catch((err) => {
            this.failed = err;
          }),
    );
  }
}
