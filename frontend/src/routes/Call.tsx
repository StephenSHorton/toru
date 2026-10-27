import { useEffect, useRef } from "react";
import { Events as WailsEvents, Window } from "@wailsio/runtime";
import { StreamService, StreamState } from "@/lib/api";

// Call — the hidden window that runs a voice call. It never renders anything:
// it holds the microphone, one audio peer connection to every other person in
// the call (mesh), and plays them. Chromium's audio processing does echo
// cancellation, auto gain and (when on) noise suppression. Signaling goes
// through StreamService.CallSend / "call:signal"; mute, deafen, devices and
// who's in the call all come from "stream:state". Speaking levels are
// measured on the audio thread (an AudioWorklet), so they stay live even
// though a hidden window's timers are throttled.

const ICE_SERVERS: RTCIceServer[] = [
  { urls: ["stun:stun.l.google.com:19302", "stun:stun1.l.google.com:19302"] },
  { urls: ["stun:stun.cloudflare.com:3478"] },
];

const VOICE_BITRATE = 96_000;
const SPEAKING_LEVEL = 0.015; // RMS
const SPEAKING_HOLD_MS = 350;
const CONNECT_TIMEOUT_MS = 12_000;

interface CallSignal {
  from: string;
  kind: string;
  sdp: string;
}

interface Peer {
  id: string;
  pc: RTCPeerConnection;
  initiator: boolean;
  audio: HTMLAudioElement;
  meter?: AudioWorkletNode;
  source?: MediaStreamAudioSourceNode;
  timer?: number;
}

// The level meter: posts the block RMS ~20 times a second from the audio thread.
const METER_WORKLET = `
class Meter extends AudioWorkletProcessor {
  constructor() { super(); this.acc = 0; this.n = 0; }
  process(inputs) {
    const ch = inputs[0] && inputs[0][0];
    if (ch) { let s = 0; for (let i = 0; i < ch.length; i++) s += ch[i] * ch[i]; this.acc += s; this.n += ch.length; }
    if (this.n >= 2400) { this.port.postMessage(Math.sqrt(this.acc / this.n)); this.acc = 0; this.n = 0; }
    return true;
  }
}
registerProcessor("toru-meter", Meter);
`;

function payload<T>(d: unknown): T | null {
  const v = Array.isArray(d) ? d[0] : d;
  return v && typeof v === "object" ? (v as T) : null;
}

function waitForIce(pc: RTCPeerConnection, ms: number): Promise<void> {
  if (pc.iceGatheringState === "complete") return Promise.resolve();
  return new Promise((resolve) => {
    const done = () => {
      pc.removeEventListener("icegatheringstatechange", check);
      window.clearTimeout(t);
      resolve();
    };
    const check = () => {
      if (pc.iceGatheringState === "complete") done();
    };
    const t = window.setTimeout(done, ms);
    pc.addEventListener("icegatheringstatechange", check);
  });
}

// chime plays a short two-note cue: up for someone joining, down for leaving.
function chime(ctx: AudioContext, up: boolean) {
  const notes = up ? [660, 880] : [660, 440];
  notes.forEach((f, i) => {
    const o = ctx.createOscillator();
    const g = ctx.createGain();
    o.type = "sine";
    o.frequency.value = f;
    const t0 = ctx.currentTime + i * 0.09;
    g.gain.setValueAtTime(0, t0);
    g.gain.linearRampToValueAtTime(0.08, t0 + 0.01);
    g.gain.exponentialRampToValueAtTime(0.0001, t0 + 0.16);
    o.connect(g).connect(ctx.destination);
    o.start(t0);
    o.stop(t0 + 0.18);
  });
}

class CallEngine {
  self = "";
  peers = new Map<string, Peer>();
  ctx = new AudioContext({ latencyHint: "interactive" });
  workletReady: Promise<void>;
  mic: MediaStream | null = null;
  micKey = "";
  micMeter?: AudioWorkletNode;
  micSource?: MediaStreamAudioSourceNode;
  muted = false;
  deafened = false;
  speaker = "";
  lastSpeak = new Map<string, number>();
  lastReport = "";
  closed = false;
  reportTimer: number;

  constructor() {
    const url = URL.createObjectURL(new Blob([METER_WORKLET], { type: "application/javascript" }));
    this.workletReady = this.ctx.audioWorklet.addModule(url);
    this.reportTimer = window.setInterval(() => this.report(), 150);
  }

  meter(id: string, source: MediaStreamAudioSourceNode): AudioWorkletNode {
    const node = new AudioWorkletNode(this.ctx, "toru-meter");
    node.port.onmessage = (e) => {
      if ((e.data as number) > SPEAKING_LEVEL) this.lastSpeak.set(id, performance.now());
    };
    // Into a muted gain so the graph runs without playing the audio twice.
    const sink = this.ctx.createGain();
    sink.gain.value = 0;
    source.connect(node).connect(sink).connect(this.ctx.destination);
    return node;
  }

  report() {
    if (this.closed) return;
    const now = performance.now();
    const speaking: string[] = [];
    this.lastSpeak.forEach((t, id) => {
      if (now - t < SPEAKING_HOLD_MS && (id !== this.self || !this.muted)) speaking.push(id);
    });
    const peers: Record<string, string> = {};
    this.peers.forEach((p, id) => (peers[id] = p.pc.connectionState));
    speaking.sort();
    const key = JSON.stringify([speaking, peers]);
    if (key === this.lastReport) return;
    this.lastReport = key;
    void StreamService.CallReport({ speaking, peers }).catch(() => {});
  }

  // apply brings the engine in line with the Go side's state.
  async apply(st: StreamState) {
    if (this.closed) return;
    this.self = st.self;
    const call = st.call;
    if (!call.inCall) return;
    if (this.ctx.state === "suspended") void this.ctx.resume();

    await this.ensureMic(call.micDevice, call.noiseSuppression);
    this.muted = call.muted;
    this.mic?.getAudioTracks().forEach((t) => (t.enabled = !call.muted));

    if (call.deafened !== this.deafened) {
      this.deafened = call.deafened;
      this.peers.forEach((p) => (p.audio.muted = this.deafened));
    }
    if (call.speakerDevice !== this.speaker) {
      this.speaker = call.speakerDevice;
      this.peers.forEach((p) => this.setSink(p.audio));
    }

    const want = new Set((st.room?.members ?? []).filter((m) => m.inCall).map((m) => m.id));
    for (const id of [...this.peers.keys()]) {
      if (!want.has(id)) {
        this.drop(id);
        chime(this.ctx, false);
      }
    }
    for (const id of want) {
      if (!this.peers.has(id)) {
        if (this.self < id) void this.offer(id);
        else void StreamService.CallSend(id, "hello", "").catch(() => {});
      }
    }
  }

  setSink(a: HTMLAudioElement) {
    const el = a as HTMLAudioElement & { setSinkId?: (id: string) => Promise<void> };
    if (el.setSinkId) void el.setSinkId(this.speaker).catch(() => {});
  }

  // ensureMic (re)opens the microphone when the device or noise suppression
  // changes, swapping the new track into every connection in place.
  async ensureMic(device: string, ns: boolean) {
    const key = `${device}|${ns}`;
    if (this.mic && key === this.micKey) return;
    this.micKey = key;
    let next: MediaStream;
    try {
      next = await navigator.mediaDevices.getUserMedia({
        audio: {
          deviceId: device ? { exact: device } : undefined,
          echoCancellation: true,
          autoGainControl: true,
          noiseSuppression: ns,
          channelCount: 1,
        },
      });
    } catch {
      // The chosen device is gone: fall back to the default one.
      next = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, autoGainControl: true, noiseSuppression: ns, channelCount: 1 },
      });
    }
    const track = next.getAudioTracks()[0];
    track.enabled = !this.muted;
    const old = this.mic;
    this.mic = next;
    this.peers.forEach((p) => {
      const sender = p.pc.getSenders().find((s) => s.track?.kind === "audio" || s.track === null);
      if (sender) void sender.replaceTrack(track);
    });
    old?.getTracks().forEach((t) => t.stop());
    this.micSource?.disconnect();
    this.micMeter?.disconnect();
    await this.workletReady;
    this.micSource = this.ctx.createMediaStreamSource(next);
    this.micMeter = this.meter(this.self, this.micSource);
  }

  newPeer(id: string, initiator: boolean): Peer {
    this.drop(id);
    const pc = new RTCPeerConnection({ iceServers: ICE_SERVERS, bundlePolicy: "max-bundle" });
    const audio = new Audio();
    audio.autoplay = true;
    audio.muted = this.deafened;
    this.setSink(audio);
    document.body.appendChild(audio);
    const peer: Peer = { id, pc, initiator, audio };
    const track = this.mic?.getAudioTracks()[0];
    if (track && this.mic) pc.addTrack(track, this.mic);
    else pc.addTransceiver("audio", { direction: "sendrecv" });
    pc.ontrack = (e) => {
      const ms = e.streams[0] ?? new MediaStream([e.track]);
      e.receiver.jitterBufferTarget = 0;
      audio.srcObject = ms;
      void audio.play().catch(() => {});
      void this.workletReady.then(() => {
        if (this.peers.get(id) !== peer) return;
        peer.source?.disconnect();
        peer.meter?.disconnect();
        peer.source = this.ctx.createMediaStreamSource(ms);
        peer.meter = this.meter(id, peer.source);
      });
    };
    pc.onconnectionstatechange = () => {
      if (this.peers.get(id) !== peer) return;
      if (pc.connectionState === "connected") {
        window.clearTimeout(peer.timer);
        chime(this.ctx, true);
        const sender = pc.getSenders()[0];
        if (sender) {
          const params = sender.getParameters();
          if (params.encodings?.length) {
            params.encodings[0].maxBitrate = VOICE_BITRATE;
            void sender.setParameters(params).catch(() => {});
          }
        }
      }
      if (pc.connectionState === "failed") this.retry(id);
      this.report();
    };
    peer.timer = window.setTimeout(() => {
      if (this.peers.get(id) === peer && pc.connectionState !== "connected") this.retry(id);
    }, CONNECT_TIMEOUT_MS);
    this.peers.set(id, peer);
    return peer;
  }

  retry(id: string) {
    const p = this.peers.get(id);
    if (!p) return;
    if (p.initiator) void this.offer(id);
    else void StreamService.CallSend(id, "hello", "").catch(() => {});
  }

  async offer(id: string) {
    const peer = this.newPeer(id, true);
    const pc = peer.pc;
    await pc.setLocalDescription(await pc.createOffer());
    await waitForIce(pc, 2500);
    if (this.peers.get(id) !== peer || !pc.localDescription) return;
    await StreamService.CallSend(id, "offer", pc.localDescription.sdp).catch(() => {});
  }

  async onSignal(sig: CallSignal) {
    if (this.closed) return;
    switch (sig.kind) {
      case "hello": {
        // They're waiting for us to start; (re)offer unless already connected.
        const p = this.peers.get(sig.from);
        if (this.self < sig.from && (!p || p.pc.connectionState !== "connected")) void this.offer(sig.from);
        break;
      }
      case "offer": {
        const peer = this.newPeer(sig.from, false);
        const pc = peer.pc;
        await pc.setRemoteDescription({ type: "offer", sdp: sig.sdp });
        await pc.setLocalDescription(await pc.createAnswer());
        await waitForIce(pc, 2500);
        if (this.peers.get(sig.from) !== peer || !pc.localDescription) return;
        await StreamService.CallSend(sig.from, "answer", pc.localDescription.sdp).catch(() => {});
        break;
      }
      case "answer": {
        const p = this.peers.get(sig.from);
        if (p && p.pc.signalingState === "have-local-offer") {
          await p.pc.setRemoteDescription({ type: "answer", sdp: sig.sdp }).catch(() => {});
        }
        break;
      }
    }
  }

  drop(id: string) {
    const p = this.peers.get(id);
    if (!p) return;
    this.peers.delete(id);
    this.lastSpeak.delete(id);
    window.clearTimeout(p.timer);
    p.pc.ontrack = null;
    p.pc.onconnectionstatechange = null;
    p.pc.close();
    p.source?.disconnect();
    p.meter?.disconnect();
    p.audio.srcObject = null;
    p.audio.remove();
  }

  close() {
    if (this.closed) return;
    this.closed = true;
    window.clearInterval(this.reportTimer);
    for (const id of [...this.peers.keys()]) this.drop(id);
    this.mic?.getTracks().forEach((t) => t.stop());
    void this.ctx.close();
  }
}

export default function Call() {
  const engine = useRef<CallEngine | null>(null);

  useEffect(() => {
    const e = new CallEngine();
    engine.current = e;
    // Serialize applies: each one may reopen the mic.
    let chain = Promise.resolve();
    const apply = (st: StreamState) => {
      chain = chain.then(() => e.apply(st)).catch(() => {});
    };
    const offState = WailsEvents.On("stream:state", (ev) => {
      const st = payload<StreamState>(ev.data);
      if (st) apply(st);
    });
    const offSignal = WailsEvents.On("call:signal", (ev) => {
      const sig = payload<CallSignal>(ev.data);
      if (sig) chain = chain.then(() => e.onSignal(sig)).catch(() => {});
    });
    const offEnd = WailsEvents.On("call:end", () => {
      e.close();
      void Window.Close().catch(() => {});
    });
    void StreamService.GetState().then((st) => {
      chime(e.ctx, true);
      apply(st);
    });
    return () => {
      offState();
      offSignal();
      offEnd();
      e.close();
    };
  }, []);

  return null;
}
