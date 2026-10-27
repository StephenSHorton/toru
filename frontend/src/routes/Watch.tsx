import { useCallback, useEffect, useRef, useState } from "react";
import { Events as WailsEvents, Window } from "@wailsio/runtime";
import { Activity, Loader2, Maximize, Minimize, RefreshCw, Volume2, VolumeX } from "lucide-react";
import { Button } from "@/components/ui/button";
import { StreamService } from "@/lib/api";
import type { StreamHostEvent, StreamLiveInfo, StreamOffer } from "@/lib/api";
import { cn } from "@/lib/utils";

// Watch window — one live stream from a room member. The streamer's Toru
// sends a WebRTC offer through the room (StreamService relays it here on
// stream:offer); this window answers, and the picture then arrives straight
// from their PC. Playback is tuned for latency: the jitter buffer target and
// playout delay are zero, so frames show as soon as they decode.

const ICE_SERVERS: RTCIceServer[] = [
  { urls: ["stun:stun.l.google.com:19302", "stun:stun1.l.google.com:19302"] },
  { urls: ["stun:stun.cloudflare.com:3478"] },
];

const OFFER_TIMEOUT_MS = 15_000;
const VOLUME_KEY = "toru.watch.volume";

type Phase = "connecting" | "playing" | "ended" | "error";

interface Stats {
  width: number;
  height: number;
  fps: number;
  mbps: number;
  lossPct: number;
  rttMs: number;
  jitterMs: number;
  decodeMs: number;
  codec: string;
}

function payload<T>(d: unknown): T | null {
  const v = Array.isArray(d) ? d[0] : d;
  return v && typeof v === "object" ? (v as T) : null;
}

function newSession(): string {
  const b = new Uint8Array(8);
  crypto.getRandomValues(b);
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

// Low-latency receiver hints. jitterBufferTarget is standard; playoutDelayHint
// is Chromium's older name for the same idea.
function tuneReceiver(r: RTCRtpReceiver) {
  const rr = r as RTCRtpReceiver & { jitterBufferTarget?: number | null; playoutDelayHint?: number };
  try {
    rr.jitterBufferTarget = 0;
  } catch {
    // older runtime
  }
  try {
    rr.playoutDelayHint = 0;
  } catch {
    // not Chromium
  }
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

function readVolume(): number {
  try {
    const v = Number(window.localStorage.getItem(VOLUME_KEY));
    return Number.isFinite(v) && v >= 0 && v <= 1 && window.localStorage.getItem(VOLUME_KEY) !== null ? v : 1;
  } catch {
    return 1;
  }
}

export default function Watch() {
  const params = new URLSearchParams(window.location.search);
  const host = params.get("host") ?? "";
  const name = params.get("name") ?? "Stream";

  const videoRef = useRef<HTMLVideoElement>(null);
  const pcRef = useRef<RTCPeerConnection | null>(null);
  const sessionRef = useRef(newSession());
  const timeoutRef = useRef<number | undefined>(undefined);
  const closedByToru = useRef(false);

  const [phase, setPhase] = useState<Phase>("connecting");
  const [message, setMessage] = useState("");
  const [info, setInfo] = useState<StreamLiveInfo | null>(null);
  const [stats, setStats] = useState<Stats | null>(null);
  const [showStats, setShowStats] = useState(false);
  const [volume, setVolume] = useState(readVolume);
  const [muted, setMuted] = useState(false);
  const [fullscreen, setFullscreen] = useState(false);
  const [chrome, setChrome] = useState(true);

  const closePc = useCallback(() => {
    const pc = pcRef.current;
    pcRef.current = null;
    if (pc) {
      pc.ontrack = null;
      pc.onconnectionstatechange = null;
      pc.close();
    }
  }, []);

  // start (re)requests the stream under a fresh session.
  const start = useCallback(() => {
    closePc();
    const old = sessionRef.current;
    void StreamService.WatchStop(host, old).catch(() => {});
    sessionRef.current = newSession();
    setPhase("connecting");
    setMessage(`Connecting to ${name}…`);
    window.clearTimeout(timeoutRef.current);
    timeoutRef.current = window.setTimeout(() => {
      setPhase("error");
      setMessage(`${name} didn't answer. They may have stopped streaming, or the room relays are slow right now.`);
    }, OFFER_TIMEOUT_MS);
    StreamService.WatchStart(host, sessionRef.current).catch((e: unknown) => {
      window.clearTimeout(timeoutRef.current);
      setPhase("error");
      setMessage(String((e as Error)?.message ?? e));
    });
  }, [closePc, host, name]);

  const negotiate = useCallback(
    async (o: StreamOffer) => {
      window.clearTimeout(timeoutRef.current);
      closePc();
      setInfo(o.info);
      setMessage(`Connecting to ${name}…`);
      const pc = new RTCPeerConnection({ iceServers: ICE_SERVERS, bundlePolicy: "max-bundle" });
      pcRef.current = pc;
      const ms = new MediaStream();
      pc.ontrack = (e) => {
        tuneReceiver(e.receiver);
        ms.addTrack(e.track);
        const v = videoRef.current;
        if (v && v.srcObject !== ms) {
          v.srcObject = ms;
          void v.play().catch(() => {});
        }
      };
      pc.onconnectionstatechange = () => {
        if (pcRef.current !== pc) return;
        switch (pc.connectionState) {
          case "connected":
            setPhase("playing");
            setMessage("");
            break;
          case "disconnected":
            setMessage("Connection hiccup, waiting…");
            break;
          case "failed":
            setPhase("error");
            setMessage(`Lost the connection to ${name}.`);
            break;
        }
      };
      try {
        await pc.setRemoteDescription({ type: "offer", sdp: o.sdp });
        await pc.setLocalDescription(await pc.createAnswer());
        await waitForIce(pc, 3000);
        if (pcRef.current !== pc || !pc.localDescription) return;
        await StreamService.WatchAnswer(host, o.session, pc.localDescription.sdp);
        // If ICE never completes, say so rather than spinning forever.
        timeoutRef.current = window.setTimeout(() => {
          if (pcRef.current === pc && pc.connectionState !== "connected") {
            setPhase("error");
            setMessage(`Couldn't connect to ${name}. A strict router or firewall on one side may be blocking it.`);
          }
        }, OFFER_TIMEOUT_MS);
      } catch (e) {
        setPhase("error");
        setMessage(String((e as Error)?.message ?? e));
      }
    },
    [closePc, host, name],
  );

  useEffect(() => {
    const offOffer = WailsEvents.On("stream:offer", (ev) => {
      const o = payload<StreamOffer>(ev.data);
      if (o && o.host === host && o.session === sessionRef.current) void negotiate(o);
    });
    const offHost = WailsEvents.On("stream:host", (ev) => {
      const h = payload<StreamHostEvent>(ev.data);
      if (!h || h.host !== host || h.session !== sessionRef.current) return;
      if (h.kind === "restart") {
        start();
      } else if (h.kind === "close") {
        // "Show my stream" was turned off (or the stream stopped): close
        // without counting it as the user closing the preview.
        closedByToru.current = true;
        void StreamService.WatchStop(host, sessionRef.current).catch(() => {});
        closePc();
        void Window.Close().catch(() => {});
      } else {
        window.clearTimeout(timeoutRef.current);
        closePc();
        setPhase("ended");
        setMessage(`${name} stopped streaming.`);
      }
    });
    start();
    const bye = () => {
      if (!closedByToru.current) {
        closedByToru.current = true;
        void StreamService.WatchClosed(host, sessionRef.current).catch(() => {});
      }
      closePc();
    };
    window.addEventListener("beforeunload", bye);
    return () => {
      offOffer();
      offHost();
      window.removeEventListener("beforeunload", bye);
      window.clearTimeout(timeoutRef.current);
      bye();
    };
  }, [closePc, host, name, negotiate, start]);

  // Stats overlay: sampled once a second from getStats.
  useEffect(() => {
    if (!showStats || phase !== "playing") return;
    let last: { t: number; bytes: number; frames: number; lost: number; recv: number } | null = null;
    const id = window.setInterval(async () => {
      const pc = pcRef.current;
      if (!pc) return;
      const report = await pc.getStats();
      let inbound: Record<string, number | string> | null = null;
      let rtt = 0;
      let codec = "";
      report.forEach((s) => {
        if (s.type === "inbound-rtp" && s.kind === "video") inbound = s;
        if (s.type === "candidate-pair" && s.nominated && typeof s.currentRoundTripTime === "number") rtt = s.currentRoundTripTime;
      });
      if (!inbound) return;
      const ib = inbound as Record<string, number | string>;
      const codecStat = ib.codecId ? report.get(String(ib.codecId)) : null;
      if (codecStat?.mimeType) codec = String(codecStat.mimeType).replace("video/", "");
      const now = performance.now();
      const bytes = Number(ib.bytesReceived ?? 0);
      const frames = Number(ib.framesDecoded ?? 0);
      const lost = Number(ib.packetsLost ?? 0);
      const recv = Number(ib.packetsReceived ?? 0);
      const jbDelay = Number(ib.jitterBufferDelay ?? 0);
      const jbCount = Number(ib.jitterBufferEmittedCount ?? 0);
      const decodeTime = Number(ib.totalDecodeTime ?? 0);
      if (last) {
        const dt = (now - last.t) / 1000;
        const dLost = lost - last.lost;
        const dRecv = recv - last.recv;
        setStats({
          width: Number(ib.frameWidth ?? 0),
          height: Number(ib.frameHeight ?? 0),
          fps: Math.round((frames - last.frames) / dt),
          mbps: ((bytes - last.bytes) * 8) / dt / 1e6,
          lossPct: dRecv + dLost > 0 ? (100 * Math.max(0, dLost)) / (dRecv + dLost) : 0,
          rttMs: rtt * 1000,
          jitterMs: jbCount > 0 ? (1000 * jbDelay) / jbCount : 0,
          decodeMs: frames > 0 ? (1000 * decodeTime) / frames : 0,
          codec,
        });
      }
      last = { t: now, bytes, frames, lost, recv };
    }, 1000);
    return () => window.clearInterval(id);
  }, [showStats, phase]);

  useEffect(() => {
    const v = videoRef.current;
    if (!v) return;
    v.volume = volume;
    v.muted = muted;
    try {
      window.localStorage.setItem(VOLUME_KEY, String(volume));
    } catch {
      // per-viewer convenience only
    }
  }, [volume, muted]);

  // Controls fade out after a moment without the mouse moving.
  useEffect(() => {
    let t: number | undefined;
    const wake = () => {
      setChrome(true);
      window.clearTimeout(t);
      t = window.setTimeout(() => setChrome(false), 2500);
    };
    wake();
    window.addEventListener("mousemove", wake);
    return () => {
      window.removeEventListener("mousemove", wake);
      window.clearTimeout(t);
    };
  }, []);

  const toggleFullscreen = useCallback(async () => {
    try {
      if (document.fullscreenElement) {
        await document.exitFullscreen();
      } else {
        await document.documentElement.requestFullscreen();
      }
    } catch {
      // fall back to the window API
      try {
        if (fullscreen) await Window.UnFullscreen();
        else await Window.Fullscreen();
        setFullscreen(!fullscreen);
      } catch {
        // nothing else to try
      }
    }
  }, [fullscreen]);

  useEffect(() => {
    const onFs = () => setFullscreen(!!document.fullscreenElement);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "f" || e.key === "F" || (e.key === "Enter" && e.altKey)) void toggleFullscreen();
      if (e.key === "m" || e.key === "M") setMuted((m) => !m);
      if (e.key === "s" || e.key === "S") setShowStats((s) => !s);
    };
    document.addEventListener("fullscreenchange", onFs);
    window.addEventListener("keydown", onKey);
    return () => {
      document.removeEventListener("fullscreenchange", onFs);
      window.removeEventListener("keydown", onKey);
    };
  }, [toggleFullscreen]);

  return (
    <div
      className={cn("relative h-screen w-screen overflow-hidden bg-black text-white", !chrome && phase === "playing" && "cursor-none")}
      onDoubleClick={() => void toggleFullscreen()}
    >
      <video
        ref={videoRef}
        autoPlay
        playsInline
        disablePictureInPicture
        className="absolute inset-0 h-full w-full object-contain"
      />

      {phase !== "playing" ? (
        <div className="absolute inset-0 flex flex-col items-center justify-center gap-3 p-6 text-center">
          {phase === "connecting" ? <Loader2 className="size-6 animate-spin text-white/70" /> : null}
          <p className="max-w-md text-sm text-white/80">{message}</p>
          {phase === "error" || phase === "ended" ? (
            <div className="flex gap-2">
              <Button size="sm" variant="secondary" onClick={start}>
                <RefreshCw />
                Try again
              </Button>
              <Button size="sm" variant="ghost" onClick={() => void Window.Close()}>
                Close
              </Button>
            </div>
          ) : null}
        </div>
      ) : message ? (
        <div className="absolute left-1/2 top-4 -translate-x-1/2 bg-black/70 px-3 py-1 text-xs">{message}</div>
      ) : null}

      {showStats && stats ? (
        <div className="absolute left-3 top-3 bg-black/70 px-3 py-2 font-mono text-[11px] leading-5 tabular-nums">
          <div>
            {stats.width}×{stats.height} · {stats.fps} fps · {stats.codec}
          </div>
          <div>{stats.mbps.toFixed(1)} Mbps · loss {stats.lossPct.toFixed(1)}%</div>
          <div>
            ping {stats.rttMs.toFixed(0)} ms · buffer {stats.jitterMs.toFixed(0)} ms · decode {stats.decodeMs.toFixed(1)} ms
          </div>
        </div>
      ) : null}

      <div
        className={cn(
          "absolute inset-x-0 bottom-0 flex items-center gap-3 bg-gradient-to-t from-black/80 to-transparent px-4 pb-3 pt-8 transition-opacity duration-300",
          chrome || phase !== "playing" ? "opacity-100" : "pointer-events-none opacity-0",
        )}
        onDoubleClick={(e) => e.stopPropagation()}
      >
        <span className="inline-flex items-center gap-1.5 text-sm font-medium">
          {phase === "playing" ? <span className="size-2 animate-pulse rounded-full bg-red-500" /> : null}
          {name}
        </span>
        {info ? (
          <span className="hidden text-xs text-white/60 sm:inline">
            {info.width}×{info.height} · {info.fps} fps
          </span>
        ) : null}
        <div className="flex-1" />
        {info?.audio ? (
          <div className="flex items-center gap-2">
            <button type="button" className="p-1 text-white/80 hover:text-white" onClick={() => setMuted((m) => !m)} title="Mute (M)">
              {muted || volume === 0 ? <VolumeX className="size-4" /> : <Volume2 className="size-4" />}
            </button>
            <input
              type="range"
              min={0}
              max={1}
              step={0.01}
              value={muted ? 0 : volume}
              onChange={(e) => {
                setVolume(Number(e.target.value));
                setMuted(false);
              }}
              className="w-24 accent-white"
              aria-label="Volume"
            />
          </div>
        ) : null}
        <button
          type="button"
          className={cn("p-1 hover:text-white", showStats ? "text-white" : "text-white/70")}
          onClick={() => setShowStats((s) => !s)}
          title="Stats (S)"
        >
          <Activity className="size-4" />
        </button>
        <button type="button" className="p-1 text-white/80 hover:text-white" onClick={() => void toggleFullscreen()} title="Fullscreen (F)">
          {fullscreen ? <Minimize className="size-4" /> : <Maximize className="size-4" />}
        </button>
      </div>
    </div>
  );
}
