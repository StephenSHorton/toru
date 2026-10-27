import { useCallback, useEffect, useRef, useState } from "react";
import { Events as WailsEvents } from "@wailsio/runtime";
import {
  AudioLines,
  Check,
  Copy,
  DoorOpen,
  Eye,
  HeadphoneOff,
  Headphones,
  Mic,
  MicOff,
  MonitorUp,
  Phone,
  PhoneOff,
  Radio,
  Square,
  Users,
  Video,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { NumberInput } from "@/components/ui/number-input";
import { Segmented } from "@/components/ui/segmented";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Card, CardHead, Note, Row } from "@/clicker/cardParts";
import { ExportService, StreamService, StreamSettings, StreamState } from "@/lib/api";
import type { StreamLiveInfo, StreamMember, StreamMonitor } from "@/lib/api";
import { errText } from "@/transfer/useTransfer";
import { cn } from "@/lib/utils";

// Stream — Toru's own screen sharing. A room is a reusable code friends join;
// anyone in it can go live and the others watch in a Toru window. The picture
// goes straight from the streamer's PC to each viewer at whatever size, frame
// rate and bitrate is set here. Go side: internal/stream.

const EMPTY_STATE = new StreamState();

function payload<T>(d: unknown): T | null {
  const v = Array.isArray(d) ? d[0] : d;
  return v && typeof v === "object" ? (v as T) : null;
}

/** useStreamState mirrors the Go side's State: subscribe first, then fetch once. */
function useStreamState(): [StreamState, (s: StreamState) => void] {
  const [state, setState] = useState<StreamState>(EMPTY_STATE);
  useEffect(() => {
    let gotEvent = false;
    const off = WailsEvents.On("stream:state", (ev) => {
      const d = payload<StreamState>(ev.data);
      if (!d) return;
      gotEvent = true;
      setState(d);
    });
    void StreamService.GetState()
      .then((s) => {
        if (!gotEvent) setState(s);
      })
      .catch(() => {});
    return () => {
      off();
    };
  }, []);
  return [state, setState];
}

export function describeLive(info: StreamLiveInfo): string {
  const mbps = info.bitrateKbps / 1000;
  return `${info.width}×${info.height} · ${info.fps} fps · ${mbps >= 10 ? Math.round(mbps) : mbps.toFixed(1)} Mbps · ${
    info.codec === "av1" ? "AV1" : "H.264"
  }`;
}

export default function StreamPage() {
  const [state, setState] = useStreamState();
  const [settings, setSettingsState] = useState<StreamSettings | null>(null);
  const [monitors, setMonitors] = useState<StreamMonitor[]>([]);
  const [support, setSupport] = useState<Record<string, boolean>>({});
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    void StreamService.GetSettings().then(setSettingsState).catch(() => {});
    void StreamService.ListMonitors().then(setMonitors).catch(() => {});
    void StreamService.EncoderSupport()
      .then((m) => setSupport(m as Record<string, boolean>))
      .catch(() => {});
  }, []);

  // Typing in a number field commits each value; while live every save
  // restarts the encoder, so saves are debounced.
  const saveTimer = useRef<number | undefined>(undefined);
  const update = useCallback((patch: Partial<StreamSettings>) => {
    setSettingsState((prev) => {
      if (!prev) return prev;
      const next = new StreamSettings({ ...prev, ...patch });
      window.clearTimeout(saveTimer.current);
      saveTimer.current = window.setTimeout(() => {
        StreamService.SetSettings(next)
          .then((saved) => {
            setSettingsState(saved);
            setError("");
          })
          .catch((e: unknown) => setError(errText(e)));
      }, 500);
      return next;
    });
  }, []);
  useEffect(() => () => window.clearTimeout(saveTimer.current), []);

  const run = async (fn: () => Promise<StreamState>) => {
    setBusy(true);
    setError("");
    try {
      setState(await fn());
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  if (!settings) return null;
  const inRoom = !!state.room;
  const monitor = monitors.find((m) => m.id === settings.monitorId) ?? monitors.find((m) => m.primary) ?? monitors[0];

  const inCall = state.call.inCall;
  const watch = (id: string) => void StreamService.OpenWatch(id).catch((e: unknown) => setError(errText(e)));

  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Call</h1>
        <p className="text-sm text-muted-foreground">
          Talk with friends and share your screen at any resolution, frame rate and bitrate. Everything goes
          straight from PC to PC.
        </p>
      </div>

      {error ? (
        <div className="border border-destructive/60 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-2">
        <RoomCard
          state={state}
          settings={settings}
          update={update}
          busy={busy}
          onCreate={() => void run(() => StreamService.CreateRoom())}
          onJoin={(code) => void run(() => StreamService.JoinRoom(code))}
          onLeave={() => void run(() => StreamService.LeaveRoom())}
        />
        {inRoom ? (
          <CallCard
            state={state}
            name={settings.name}
            busy={busy}
            onJoin={() => void run(() => StreamService.JoinCall())}
            onLeave={() => void run(() => StreamService.LeaveCall())}
            onMute={(on) => void run(() => StreamService.SetMuted(on))}
            onDeafen={(on) => void run(() => StreamService.SetDeafened(on))}
            onShare={() => void run(() => StreamService.GoLive())}
            onStopShare={() => void run(() => StreamService.StopLive())}
            onWatch={watch}
          />
        ) : null}
        <VoiceCard state={state} onChange={(noise, mic, speaker) => void run(() => StreamService.SetVoice(noise, mic, speaker))} />
        <LiveCard
          state={state}
          inCall={inCall}
          busy={busy}
          onGoLive={() => void run(() => StreamService.GoLive())}
          onStop={() => void run(() => StreamService.StopLive())}
          onPreview={(on) => void run(() => StreamService.SetPreview(on))}
        />
        <VideoCard settings={settings} update={update} monitors={monitors} monitor={monitor} support={support} />
        <AudioCard settings={settings} update={update} />
      </div>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Room
// ---------------------------------------------------------------------------

function RoomCard({
  state,
  settings,
  update,
  busy,
  onCreate,
  onJoin,
  onLeave,
}: {
  state: StreamState;
  settings: StreamSettings;
  update: (p: Partial<StreamSettings>) => void;
  busy: boolean;
  onCreate: () => void;
  onJoin: (code: string) => void;
  onLeave: () => void;
}) {
  const [code, setCode] = useState("");
  const [copied, setCopied] = useState(false);
  const room = state.room;

  const copy = async () => {
    if (!room) return;
    try {
      await ExportService.CopyTextAs(room.code, "Room code");
      setCopied(true);
      window.setTimeout(() => setCopied(false), 1500);
    } catch {
      // the toast reports a failed copy
    }
  };

  return (
    <Card label="Room" wide>
      <CardHead
        icon={Users}
        title="Room"
        caption={room ? "Share the code with friends. It stays the same, so they only need it once." : "Make a room, or join a friend's with their code."}
      >
        {room ? <RelayChip connected={room.connected} relays={room.relays} /> : null}
      </CardHead>

      {room ? (
        <div className="flex flex-wrap items-center gap-2">
          <span className="select-all border border-border bg-background px-3 py-1.5 font-mono text-base tracking-wider">
            {room.code}
          </span>
          <Button size="sm" variant="outline" onClick={() => void copy()}>
            {copied ? <Check /> : <Copy />}
            {copied ? "Copied" : "Copy"}
          </Button>
          <div className="flex-1" />
          <Button size="sm" variant="ghost" disabled={busy} onClick={onLeave}>
            <DoorOpen />
            Leave room
          </Button>
        </div>
      ) : (
        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" disabled={busy} onClick={onCreate}>
            Create a room
          </Button>
          <span className="px-1 text-xs text-muted-foreground">or</span>
          <form
            className="flex flex-1 items-center gap-2"
            onSubmit={(e) => {
              e.preventDefault();
              if (code.trim()) onJoin(code);
            }}
          >
            <input
              value={code}
              onChange={(e) => setCode(e.target.value)}
              placeholder="Room code"
              spellCheck={false}
              className="h-8 min-w-0 flex-1 border border-border bg-background px-2 font-mono text-sm uppercase tracking-wider focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
            <Button size="sm" variant="outline" type="submit" disabled={busy || !code.trim()}>
              Join
            </Button>
          </form>
        </div>
      )}

      <div className="grid gap-3 sm:grid-cols-2">
        <Row label="Your name" caption="What the room sees">
          <NameInput value={settings.name} onCommit={(name) => update({ name })} />
        </Row>
        <Row label="Live alerts" caption="Notify me when someone goes live">
          <Switch checked={settings.notifyLive} onCheckedChange={(notifyLive) => update({ notifyLive })} />
        </Row>
      </div>

      {room ? <MemberList members={room.members} /> : null}
    </Card>
  );
}

function RelayChip({ connected, relays }: { connected: number; relays: number }) {
  const ok = connected > 0;
  return (
    <span
      title={`${connected} of ${relays} relays connected`}
      className={cn(
        "inline-flex items-center gap-1.5 border px-2 py-0.5 text-xs",
        ok ? "border-emerald-500/50 bg-emerald-500/10 text-emerald-300" : "border-amber-400/60 bg-amber-400/10 text-amber-300",
      )}
    >
      <span className={cn("size-1.5 rounded-full", ok ? "bg-emerald-400" : "animate-pulse bg-amber-300")} />
      {ok ? "Connected" : "Connecting…"}
    </span>
  );
}

function NameInput({ value, onCommit }: { value: string; onCommit: (v: string) => void }) {
  const [draft, setDraft] = useState(value);
  useEffect(() => setDraft(value), [value]);
  const commit = () => {
    const v = draft.trim();
    if (v && v !== value) onCommit(v);
    else setDraft(value);
  };
  return (
    <input
      value={draft}
      maxLength={32}
      onChange={(e) => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") (e.target as HTMLInputElement).blur();
      }}
      className="h-8 w-40 border border-border bg-background px-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
    />
  );
}

function MemberList({ members }: { members: StreamMember[] }) {
  if (members.length === 0) {
    return <Note>No one else is here yet. Send them the room code.</Note>;
  }
  return (
    <ul className="flex flex-col divide-y divide-border border border-border">
      {members.map((m) => (
        <li key={m.id} className="flex items-center gap-3 px-3 py-2">
          <span className={cn("size-2 shrink-0 rounded-full", m.inCall ? "bg-sky-400" : "bg-emerald-400")} />
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{m.name}</span>
          <span className="shrink-0 text-xs text-muted-foreground">
            {m.live ? "Sharing in the call" : m.inCall ? "In the call" : "Online"}
          </span>
        </li>
      ))}
    </ul>
  );
}

// ---------------------------------------------------------------------------
// Call
// ---------------------------------------------------------------------------

interface Tile {
  id: string;
  name: string;
  self: boolean;
  speaking: boolean;
  muted: boolean;
  deafened: boolean;
  live: StreamLiveInfo | null;
  link: string; // our connection to them ("" for us)
}

function CallCard({
  state,
  name,
  busy,
  onJoin,
  onLeave,
  onMute,
  onDeafen,
  onShare,
  onStopShare,
  onWatch,
}: {
  state: StreamState;
  name: string;
  busy: boolean;
  onJoin: () => void;
  onLeave: () => void;
  onMute: (on: boolean) => void;
  onDeafen: (on: boolean) => void;
  onShare: () => void;
  onStopShare: () => void;
  onWatch: (id: string) => void;
}) {
  const call = state.call;
  const others = (state.room?.members ?? []).filter((m) => m.inCall);
  const tiles: Tile[] = others.map((m) => ({
    id: m.id,
    name: m.name,
    self: false,
    speaking: m.speaking,
    muted: m.muted,
    deafened: m.deafened,
    live: m.live,
    link: call.peers?.[m.id] ?? "",
  }));
  if (call.inCall) {
    tiles.unshift({
      id: state.self,
      name,
      self: true,
      speaking: call.speaking,
      muted: call.muted,
      deafened: call.deafened,
      live: state.live.live ? state.live.info : null,
      link: "",
    });
  }

  return (
    <Card label="Call" wide>
      <CardHead
        icon={Headphones}
        title="Voice call"
        caption={
          call.inCall
            ? "You're in the call"
            : others.length > 0
              ? `${others.length} ${others.length === 1 ? "person is" : "people are"} talking`
              : "No one's in the call yet"
        }
      />

      {tiles.length > 0 ? (
        <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 xl:grid-cols-4">
          {tiles.map((t) => (
            <CallTile key={t.id} tile={t} canWatch={call.inCall} onWatch={onWatch} />
          ))}
        </div>
      ) : null}

      {call.inCall ? (
        <div className="flex flex-wrap items-center gap-2">
          <Button
            variant={call.muted ? "destructive" : "outline"}
            onClick={() => onMute(!call.muted)}
            title={call.muted ? "Unmute" : "Mute"}
          >
            {call.muted ? <MicOff /> : <Mic />}
            {call.muted ? "Unmute" : "Mute"}
          </Button>
          <Button
            variant={call.deafened ? "destructive" : "outline"}
            onClick={() => onDeafen(!call.deafened)}
            title={call.deafened ? "Undeafen" : "Deafen: hear no one, and mute your mic"}
          >
            {call.deafened ? <HeadphoneOff /> : <Headphones />}
            {call.deafened ? "Undeafen" : "Deafen"}
          </Button>
          {state.live.live ? (
            <Button variant="outline" disabled={busy} onClick={onStopShare}>
              <Square />
              Stop sharing
            </Button>
          ) : (
            <Button variant="outline" disabled={busy} onClick={onShare}>
              <MonitorUp />
              Share screen
            </Button>
          )}
          <div className="flex-1" />
          <Button variant="destructive" disabled={busy} onClick={onLeave}>
            <PhoneOff />
            Leave
          </Button>
        </div>
      ) : (
        <div>
          <Button disabled={busy} onClick={onJoin}>
            <Phone />
            Join call
          </Button>
        </div>
      )}
    </Card>
  );
}

function CallTile({ tile, canWatch, onWatch }: { tile: Tile; canWatch: boolean; onWatch: (id: string) => void }) {
  const connecting = !tile.self && canWatch && tile.link !== "" && tile.link !== "connected";
  return (
    <div
      className={cn(
        "flex flex-col items-center gap-2 border bg-background/40 px-2 py-3 transition-colors",
        tile.speaking ? "border-emerald-400 shadow-[0_0_0_1px_rgb(52_211_153)]" : "border-border",
      )}
    >
      <div
        className={cn(
          "flex size-12 items-center justify-center rounded-full bg-muted text-lg font-semibold",
          tile.speaking && "ring-2 ring-emerald-400 ring-offset-2 ring-offset-background",
        )}
      >
        {tile.name.slice(0, 1).toUpperCase()}
      </div>
      <div className="flex max-w-full items-center gap-1 text-sm">
        <span className="truncate">{tile.self ? `${tile.name} (you)` : tile.name}</span>
        {tile.deafened ? (
          <HeadphoneOff className="size-3.5 shrink-0 text-destructive" />
        ) : tile.muted ? (
          <MicOff className="size-3.5 shrink-0 text-destructive" />
        ) : null}
      </div>
      {tile.live ? (
        <div className="flex flex-col items-center gap-1">
          <span className="inline-flex items-center gap-1 bg-red-600 px-1.5 text-[10px] font-semibold uppercase tracking-wide text-white">
            Live
          </span>
          {!tile.self && canWatch ? (
            <Button size="sm" onClick={() => onWatch(tile.id)}>
              <Eye />
              Watch
            </Button>
          ) : null}
        </div>
      ) : connecting ? (
        <span className="text-[11px] text-muted-foreground">{tile.link === "failed" ? "Can't connect" : "Connecting…"}</span>
      ) : null}
    </div>
  );
}

const NOISE_CAPTIONS: Record<string, string> = {
  off: "Your mic as it is",
  standard: "Filters out fans, hum and steady background noise",
  ai: "Also removes keyboard clicks and other sudden sounds",
};

// VoiceCard: noise suppression and the mic and speaker devices. The hidden
// call window applies changes at once.
function VoiceCard({
  state,
  onChange,
}: {
  state: StreamState;
  onChange: (noiseMode: string, mic: string, speaker: string) => void;
}) {
  const call = state.call;
  const [devices, setDevices] = useState<MediaDeviceInfo[]>([]);
  useEffect(() => {
    const load = () =>
      void navigator.mediaDevices
        .enumerateDevices()
        .then(setDevices)
        .catch(() => {});
    load();
    navigator.mediaDevices.addEventListener("devicechange", load);
    return () => navigator.mediaDevices.removeEventListener("devicechange", load);
  }, [call.inCall]);

  const list = (kind: MediaDeviceKind, fallback: string) => [
    { value: "", label: "System default" },
    ...devices
      .filter((d) => d.kind === kind && d.deviceId && d.deviceId !== "default" && d.deviceId !== "communications")
      .map((d, i) => ({ value: d.deviceId, label: d.label || `${fallback} ${i + 1}` })),
  ];
  const mics = list("audioinput", "Microphone");
  const speakers = list("audiooutput", "Speakers");

  return (
    <Card label="Voice">
      <CardHead icon={Mic} title="Voice" caption="Applies right away, even mid-call" />
      <Row label="Noise suppression" caption={NOISE_CAPTIONS[call.noiseMode] ?? NOISE_CAPTIONS.standard}>
        <Segmented
          size="xs"
          value={call.noiseMode}
          options={[
            { value: "off", label: "Off" },
            { value: "standard", label: "Standard", title: "The browser's built-in noise suppression" },
            { value: "ai", label: "AI", title: "DeepFilterNet3 speech enhancement, runs on this PC" },
          ]}
          onChange={(noise) => onChange(noise, call.micDevice, call.speakerDevice)}
          aria-label="Noise suppression"
        />
      </Row>
      <Row label="Microphone">
        <Select
          value={mics.some((m) => m.value === call.micDevice) ? call.micDevice : ""}
          options={mics}
          onChange={(mic) => onChange(call.noiseMode, mic, call.speakerDevice)}
          boxClassName="w-56"
        />
      </Row>
      <Row label="Speakers">
        <Select
          value={speakers.some((m) => m.value === call.speakerDevice) ? call.speakerDevice : ""}
          options={speakers}
          onChange={(speaker) => onChange(call.noiseMode, call.micDevice, speaker)}
          boxClassName="w-56"
        />
      </Row>
    </Card>
  );
}

// ---------------------------------------------------------------------------
// Go live
// ---------------------------------------------------------------------------

function LiveCard({
  state,
  inCall,
  busy,
  onGoLive,
  onStop,
  onPreview,
}: {
  state: StreamState;
  inCall: boolean;
  busy: boolean;
  onGoLive: () => void;
  onStop: () => void;
  onPreview: (on: boolean) => void;
}) {
  const live = state.live;
  const viewers = live.viewers ?? [];
  const watching = viewers.filter((v) => v.connected && v.peer !== state.self).length;
  const previewRow = (
    <Row label="Show my stream" caption="Watch yourself the way viewers see you. Off saves your GPU the extra decode">
      <Switch checked={state.preview} onCheckedChange={onPreview} disabled={busy} />
    </Row>
  );
  return (
    <Card label="Share screen">
      <CardHead
        icon={Radio}
        title="Share screen"
        caption={inCall ? "Everyone in the call can watch" : "Join the call to share your screen"}
      >
        {live.live ? (
          <span className="inline-flex items-center gap-1.5 border border-red-500/60 bg-red-500/10 px-2 py-0.5 text-xs text-red-300">
            <span className="size-1.5 animate-pulse rounded-full bg-red-500" />
            Live
          </span>
        ) : null}
      </CardHead>

      {live.live ? (
        <>
          <div className="grid grid-cols-3 gap-3">
            <Stat label="Watching" value={String(watching)} />
            <Stat label="Sending" value={`${(live.sentKbps / 1000).toFixed(1)} Mbps`} />
            <Stat label="Frame rate" value={`${live.sentFps} fps`} />
          </div>
          <Note>
            {describeLive(live.info)} · {live.encoder}
            {live.zeroCopy ? " · GPU only" : ""}
          </Note>
          {live.error ? <Note warn>{live.error}</Note> : null}
          {live.audioWarning ? <Note warn>{live.audioWarning}</Note> : null}
          {viewers.length > 0 ? (
            <ul className="flex flex-wrap gap-1.5">
              {viewers
                .filter((v) => v.peer !== state.self)
                .map((v) => (
                  <li
                    key={v.peer + v.name}
                    className={cn(
                      "border px-2 py-0.5 text-xs",
                      v.connected ? "border-border text-foreground" : "border-dashed border-border text-muted-foreground",
                    )}
                  >
                    {v.name}
                    {v.connected ? "" : " · connecting"}
                  </li>
                ))}
            </ul>
          ) : null}
          {previewRow}
          <div>
            <Button variant="destructive" disabled={busy} onClick={onStop}>
              <Square />
              Stop sharing
            </Button>
          </div>
        </>
      ) : (
        <>
          <Note>
            Each viewer gets their own copy, so your upload needs about the bitrate times the number of viewers.
          </Note>
          {live.error ? <Note warn>{live.error}</Note> : null}
          {previewRow}
          <div>
            <Button disabled={busy || !inCall} onClick={onGoLive}>
              <MonitorUp />
              Share screen
            </Button>
          </div>
        </>
      )}
    </Card>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex flex-col">
      <span className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</span>
      <span className="font-mono text-lg tabular-nums">{value}</span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Video + audio
// ---------------------------------------------------------------------------

const RESOLUTIONS = [
  { w: 7680, h: 4320 },
  { w: 5120, h: 2160 },
  { w: 3840, h: 2160 },
  { w: 3440, h: 1440 },
  { w: 2560, h: 1440 },
  { w: 2560, h: 1080 },
  { w: 1920, h: 1080 },
  { w: 1600, h: 900 },
  { w: 1280, h: 720 },
  { w: 854, h: 480 },
] as const;

const FPS_PRESETS = [30, 60, 120, 144, 165, 240] as const;

function VideoCard({
  settings,
  update,
  monitors,
  monitor,
  support,
}: {
  settings: StreamSettings;
  update: (p: Partial<StreamSettings>) => void;
  monitors: StreamMonitor[];
  monitor: StreamMonitor | undefined;
  support: Record<string, boolean>;
}) {
  const native = settings.width === 0 || settings.height === 0;
  const preset = native ? "native" : RESOLUTIONS.some((r) => r.w === settings.width && r.h === settings.height) ? `${settings.width}x${settings.height}` : "custom";
  const [custom, setCustom] = useState(preset === "custom");
  const resValue = custom ? "custom" : preset;
  const outW = native ? (monitor?.w ?? 0) : settings.width;
  const outH = native ? (monitor?.h ?? 0) : settings.height;
  const fpsPreset = (FPS_PRESETS as readonly number[]).includes(settings.fps) ? String(settings.fps) : "custom";
  const autoKbps = autoBitrateKbps(outW, outH, settings.fps, settings.codec);
  const kbps = settings.autoBitrate ? autoKbps : settings.bitrateKbps;

  const resOptions = [
    { value: "native", label: monitor ? `Same as monitor (${monitor.w}×${monitor.h})` : "Same as monitor" },
    ...RESOLUTIONS.map((r) => ({ value: `${r.w}x${r.h}`, label: `${r.w}×${r.h}` })),
    { value: "custom", label: "Custom…" },
  ];

  return (
    <Card label="Video">
      <CardHead icon={Video} title="Video" caption="Changes apply right away, even while you're live" />

      <Row label="Monitor">
        <Select
          value={String(monitor?.id ?? -1)}
          options={monitors.map((m) => ({ value: String(m.id), label: m.label }))}
          onChange={(v) => update({ monitorId: Number(v) })}
          boxClassName="w-56"
        />
      </Row>

      <Row label="Resolution" caption={native ? "No scaling, the fastest path" : "Scaled on the way out"}>
        <Select
          value={resValue}
          options={resOptions}
          onChange={(v) => {
            if (v === "custom") {
              setCustom(true);
              update({ width: outW || 1920, height: outH || 1080 });
              return;
            }
            setCustom(false);
            if (v === "native") update({ width: 0, height: 0 });
            else {
              const [w, h] = v.split("x").map(Number);
              update({ width: w, height: h });
            }
          }}
          boxClassName="w-56"
        />
      </Row>
      {resValue === "custom" ? (
        <div className="flex items-center justify-end gap-2">
          <NumberInput value={settings.width} min={128} max={7680} step={2} suffix="w" onChange={(width) => update({ width })} boxClassName="w-24" />
          <span className="text-muted-foreground">×</span>
          <NumberInput value={settings.height} min={128} max={4320} step={2} suffix="h" onChange={(height) => update({ height })} boxClassName="w-24" />
        </div>
      ) : null}

      <Row label="Frame rate">
        <div className="flex items-center gap-2">
          <Segmented
            size="xs"
            value={fpsPreset}
            options={FPS_PRESETS.map((f) => ({ value: String(f), label: String(f) }))}
            onChange={(v) => update({ fps: Number(v) })}
            aria-label="Frame rate"
          />
          <NumberInput value={settings.fps} min={1} max={500} suffix="fps" onChange={(fps) => update({ fps })} boxClassName="w-20" />
        </div>
      </Row>

      <Row
        label="Bitrate"
        caption={
          settings.autoBitrate
            ? `${formatMbps(kbps)} Mbps, follows resolution, frame rate and codec`
            : bitrateHint(kbps, outW, outH, settings.fps, settings.codec)
        }
      >
        <div className="flex items-center gap-2">
          <Segmented
            size="xs"
            value={settings.autoBitrate ? "auto" : "manual"}
            options={[
              { value: "auto", label: "Auto" },
              { value: "manual", label: "Manual" },
            ]}
            // Manual starts from what Auto was giving, so the switch alone
            // changes nothing.
            onChange={(v) => update(v === "auto" ? { autoBitrate: true } : { autoBitrate: false, bitrateKbps: autoKbps })}
            aria-label="Bitrate mode"
          />
          <NumberInput
            value={kbps / 1000}
            min={0.5}
            max={500}
            step={1}
            decimals={1}
            suffix="Mbps"
            onChange={(mbps) => update({ autoBitrate: false, bitrateKbps: Math.round(mbps * 1000) })}
            boxClassName="w-28"
          />
        </div>
      </Row>

      <Row label="Codec" caption={settings.codec === "av1" ? "Sharper per bit; viewers need a recent GPU" : "Plays on every PC"}>
        <Segmented
          size="xs"
          value={settings.codec}
          options={[
            { value: "h264", label: "H.264", disabled: support.h264 === false },
            { value: "av1", label: "AV1", disabled: support.av1 === false, title: support.av1 === false ? "Your GPU can't encode AV1" : undefined },
          ]}
          onChange={(codec) => update({ codec })}
          aria-label="Codec"
        />
      </Row>

      <Row label="Full frame every" caption="Lower lets new viewers start sooner; higher looks better">
        <NumberInput value={settings.keyframeSec} min={1} max={10} suffix="s" onChange={(keyframeSec) => update({ keyframeSec })} boxClassName="w-20" />
      </Row>

      <Row label="Show mouse pointer">
        <Switch checked={settings.cursor} onCheckedChange={(cursor) => update({ cursor })} />
      </Row>
    </Card>
  );
}

// autoBitrateKbps mirrors stream.AutoBitrateKbps (internal/stream/settings.go),
// which is what the encoder actually uses; TestAutoBitrateKbps pins the
// numbers. Pixels × fps × bits per pixel, clamped; an unknown size counts as
// 1080p.
function autoBitrateKbps(w: number, h: number, fps: number, codec: string): number {
  if (w <= 0 || h <= 0) [w, h] = [1920, 1080];
  const bpp = codec === "av1" ? 0.06 : 0.1;
  const kbps = Math.round((w * h * Math.max(fps, 1) * bpp) / 1000);
  return Math.min(Math.max(kbps, 1_500), 150_000);
}

function formatMbps(kbps: number): string {
  const mbps = kbps / 1000;
  return mbps >= 10 ? String(Math.round(mbps)) : mbps.toFixed(1);
}

// bitrateHint puts the bitrate in context: bits per pixel per frame, where
// ~0.05 is soft for games and ~0.1+ holds up in fast motion with H.264. AV1
// needs about 40% fewer bits for the same picture.
function bitrateHint(kbps: number, w: number, h: number, fps: number, codec: string): string {
  if (!w || !h || !fps) return "";
  const bpp = ((kbps * 1000) / (w * h * fps)) * (codec === "av1" ? 1 / 0.6 : 1);
  if (bpp < 0.03) return "Low for this size and frame rate: fast motion will blur";
  if (bpp < 0.07) return "Good for most things";
  return "Plenty, even for fast games";
}

const AUDIO_RATES = [96, 128, 160, 256, 320, 510] as const;

function AudioCard({ settings, update }: { settings: StreamSettings; update: (p: Partial<StreamSettings>) => void }) {
  return (
    <Card label="Audio">
      <CardHead icon={AudioLines} title="Audio" caption="Your PC's sound, never Toru's own" />
      <Row label="Stream desktop audio">
        <Switch checked={settings.audio} onCheckedChange={(audio) => update({ audio })} />
      </Row>
      <Row label="Audio quality">
        <Select
          value={String(settings.audioKbps)}
          options={AUDIO_RATES.map((r) => ({ value: String(r), label: `${r} kbps` }))}
          onChange={(v) => update({ audioKbps: Number(v) })}
          disabled={!settings.audio}
          boxClassName="w-32"
        />
      </Row>
    </Card>
  );
}
