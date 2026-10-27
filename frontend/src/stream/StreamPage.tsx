import { useCallback, useEffect, useRef, useState } from "react";
import { Events as WailsEvents } from "@wailsio/runtime";
import {
  AudioLines,
  Check,
  Copy,
  DoorOpen,
  Eye,
  MonitorUp,
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

  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Stream</h1>
        <p className="text-sm text-muted-foreground">
          Share your screen with friends at any resolution, frame rate and bitrate. It goes straight from
          your PC to theirs.
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
          onWatch={(id) => void StreamService.OpenWatch(id).catch((e: unknown) => setError(errText(e)))}
        />
        <LiveCard
          state={state}
          inRoom={inRoom}
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
  onWatch,
}: {
  state: StreamState;
  settings: StreamSettings;
  update: (p: Partial<StreamSettings>) => void;
  busy: boolean;
  onCreate: () => void;
  onJoin: (code: string) => void;
  onLeave: () => void;
  onWatch: (id: string) => void;
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

      {room ? <MemberList members={room.members} onWatch={onWatch} /> : null}
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

function MemberList({ members, onWatch }: { members: StreamMember[]; onWatch: (id: string) => void }) {
  if (members.length === 0) {
    return <Note>No one else is here yet. Send them the room code.</Note>;
  }
  return (
    <ul className="flex flex-col divide-y divide-border border border-border">
      {members.map((m) => (
        <li key={m.id} className="flex items-center gap-3 px-3 py-2">
          <span className={cn("size-2 shrink-0 rounded-full", m.live ? "bg-red-500" : "bg-emerald-400")} />
          <div className="flex min-w-0 flex-1 flex-col">
            <span className="truncate text-sm font-medium">{m.name}</span>
            <span className="truncate text-xs text-muted-foreground">{m.live ? describeLive(m.live) : "In the room"}</span>
          </div>
          {m.live ? (
            <Button size="sm" onClick={() => onWatch(m.id)}>
              <Eye />
              Watch
            </Button>
          ) : null}
        </li>
      ))}
    </ul>
  );
}

// ---------------------------------------------------------------------------
// Go live
// ---------------------------------------------------------------------------

function LiveCard({
  state,
  inRoom,
  busy,
  onGoLive,
  onStop,
  onPreview,
}: {
  state: StreamState;
  inRoom: boolean;
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
    <Card label="Go live">
      <CardHead
        icon={Radio}
        title="Go live"
        caption={inRoom ? "Everyone in the room can watch" : "Join a room first"}
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
              Stop
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
            <Button disabled={busy || !inRoom} onClick={onGoLive}>
              <MonitorUp />
              Go live
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

      <Row label="Bitrate" caption={bitrateHint(settings.bitrateKbps, outW, outH, settings.fps)}>
        <NumberInput
          value={settings.bitrateKbps / 1000}
          min={0.5}
          max={500}
          step={1}
          decimals={1}
          suffix="Mbps"
          onChange={(mbps) => update({ bitrateKbps: Math.round(mbps * 1000) })}
          boxClassName="w-28"
        />
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

// bitrateHint puts the bitrate in context: bits per pixel per frame, where
// ~0.05 is soft for games and ~0.1+ holds up in fast motion.
function bitrateHint(kbps: number, w: number, h: number, fps: number): string {
  if (!w || !h || !fps) return "";
  const bpp = (kbps * 1000) / (w * h * fps);
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
