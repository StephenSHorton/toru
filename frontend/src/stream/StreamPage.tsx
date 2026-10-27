import { useCallback, useEffect, useRef, useState } from "react";
import { Events as WailsEvents } from "@wailsio/runtime";
import {
  AudioLines,
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  DoorOpen,
  Eye,
  HeadphoneOff,
  Headphones,
  Loader2,
  Mic,
  MicOff,
  MonitorUp,
  Network,
  Phone,
  PhoneOff,
  Radio,
  RotateCcw,
  ShieldCheck,
  Square,
  Trash2,
  Users,
  Video,
  Volume1,
  Volume2,
  VolumeX,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { NumberInput } from "@/components/ui/number-input";
import { Segmented } from "@/components/ui/segmented";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { Card, CardHead, Note, Row } from "@/clicker/cardParts";
import { ExportService, LanService, LanState, StreamService, StreamSettings, StreamState } from "@/lib/api";
import type { LanPeer, StreamLiveInfo, StreamMember, StreamMonitor } from "@/lib/api";
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

const EMPTY_LAN = new LanState({ state: "off" });

/** useLanState mirrors LanService's state the same way. */
function useLanState(): [LanState, (s: LanState) => void] {
  const [state, setState] = useState<LanState>(EMPTY_LAN);
  useEffect(() => {
    let gotEvent = false;
    const off = WailsEvents.On("lan:state", (ev) => {
      const d = payload<LanState>(ev.data);
      if (!d) return;
      gotEvent = true;
      setState(d);
    });
    void LanService.GetState()
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

/** copyText copies text with the shared toast; true when it worked. */
async function copyText(text: string, label: string): Promise<boolean> {
  try {
    await ExportService.CopyTextAs(text, label);
    return true;
  } catch {
    return false; // the toast reports a failed copy
  }
}

export function describeLive(info: StreamLiveInfo): string {
  const mbps = info.bitrateKbps / 1000;
  return `${info.width}×${info.height} · ${info.fps} fps · ${mbps >= 10 ? Math.round(mbps) : mbps.toFixed(1)} Mbps · ${
    info.codec === "av1" ? "AV1" : "H.264"
  }`;
}

export default function StreamPage() {
  const [state, setState] = useStreamState();
  const [lan, setLan] = useLanState();
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

  const runLan = async (fn: () => Promise<LanState>) => {
    setError("");
    try {
      setLan(await fn());
    } catch (e) {
      setError(errText(e));
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
        <h1 className="text-xl font-semibold tracking-tight">Game Room</h1>
        <p className="text-sm text-muted-foreground">
          Voice chat, screen sharing at any resolution, frame rate and bitrate, and a shared LAN for your games. Everything goes
          straight from PC to PC.
        </p>
      </div>

      {error ? (
        <div className="border border-destructive/60 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
      ) : null}

      <div className="grid gap-4 lg:grid-cols-2">
        <RoomCard
          state={state}
          lan={lan}
          settings={settings}
          update={update}
          busy={busy}
          onCreate={() => void run(() => StreamService.CreateRoom())}
          onJoin={(code) => void run(() => StreamService.JoinRoom(code))}
          onLeave={() => void run(() => StreamService.LeaveRoom())}
        />
        {inRoom ? (
          <LanCard lan={lan} run={(fn) => void runLan(fn)} />
        ) : (
          // Shown before a room too, so LAN is findable: it works per room.
          <Card label="LAN" wide>
            <CardHead
              icon={Network}
              title="LAN"
              caption="Create or join a room first: everyone in the room with LAN on shares one network, so LAN and direct-play games find each other"
            />
          </Card>
        )}
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
            onVolume={(id, pct) => void StreamService.SetPeerVolume(id, pct).then(setState).catch((e: unknown) => setError(errText(e)))}
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
  lan,
  settings,
  update,
  busy,
  onCreate,
  onJoin,
  onLeave,
}: {
  state: StreamState;
  lan: LanState;
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

      {room ? <MemberList members={room.members} lan={lan} /> : null}
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

function MemberList({ members, lan }: { members: StreamMember[]; lan: LanState }) {
  if (members.length === 0) {
    return <Note>No one else is here yet. Send them the room code.</Note>;
  }
  // LAN chips show while our LAN is up (or coming up).
  const showLan = lan.wanted && (lan.state === "on" || lan.state === "starting");
  return (
    <ul className="flex flex-col divide-y divide-border border border-border">
      {members.map((m) => (
        <li key={m.id} className="flex flex-wrap items-center gap-x-3 gap-y-1 px-3 py-2">
          <span className={cn("size-2 shrink-0 rounded-full", m.inCall ? "bg-sky-400" : "bg-emerald-400")} />
          <span className="min-w-0 flex-1 truncate text-sm font-medium">{m.name}</span>
          {showLan ? <LanChips member={m} peer={lan.peers?.[m.id]} /> : null}
          <span className="shrink-0 text-xs text-muted-foreground">
            {m.live ? "Sharing in the call" : m.inCall ? "In the call" : "Online"}
          </span>
        </li>
      ))}
    </ul>
  );
}

const chipBase = "inline-flex items-center gap-1.5 border px-2 py-0.5 text-xs";

// LanChips: a member's overlay address (click to copy) and how our tunnel to
// them is doing: Direct, Relayed, Connecting… or LAN off on their side.
function LanChips({ member, peer }: { member: StreamMember; peer: LanPeer | undefined }) {
  const [copied, setCopied] = useState(false);
  const info = member.lan;
  if (!info || !info.on) {
    return <span className={cn(chipBase, "border-dashed border-border text-muted-foreground")}>LAN off</span>;
  }
  const connected = peer?.state === "connected";
  const failed = peer?.state === "failed";
  const relayed = connected && peer?.path === "relay";
  const label = connected ? (relayed ? "Relayed" : "Direct") : failed ? "Can't connect" : "Connecting…";
  return (
    <span className="inline-flex shrink-0 items-center gap-1.5">
      <button
        type="button"
        title={`Copy ${member.name}'s LAN address`}
        onClick={() =>
          void copyText(info.ip, "LAN address").then((ok) => {
            if (!ok) return;
            setCopied(true);
            window.setTimeout(() => setCopied(false), 1500);
          })
        }
        className={cn(chipBase, "border-border font-mono text-foreground hover:bg-accent")}
      >
        {info.ip}
        {copied ? <Check className="size-3" /> : <Copy className="size-3 text-muted-foreground" />}
      </button>
      <span
        title={
          relayed
            ? "Through the relay: your PCs couldn't reach each other directly"
            : connected
              ? "Straight from PC to PC"
              : undefined
        }
        className={cn(
          chipBase,
          connected
            ? relayed
              ? "border-sky-500/50 bg-sky-500/10 text-sky-300"
              : "border-emerald-500/50 bg-emerald-500/10 text-emerald-300"
            : failed
              ? "border-destructive/60 bg-destructive/10 text-destructive"
              : "border-amber-400/60 bg-amber-400/10 text-amber-300",
        )}
      >
        {!connected && !failed ? <span className="size-1.5 animate-pulse rounded-full bg-amber-300" /> : null}
        {label}
        {connected && peer && peer.rttMs > 0 ? <span className="tabular-nums opacity-80">· {peer.rttMs} ms</span> : null}
      </span>
    </span>
  );
}

// ---------------------------------------------------------------------------
// LAN
// ---------------------------------------------------------------------------

// LanCard: one shared virtual network for everyone in the room, so LAN games
// find each other. The service behind it needs a one-time administrator
// setup; after that the switch needs no prompts. Go side: LanService.
function LanCard({ lan, run }: { lan: LanState; run: (fn: () => Promise<LanState>) => void }) {
  const [advanced, setAdvanced] = useState(false);
  const [copied, setCopied] = useState(false);
  const busy = lan.busy !== "";
  const installed = lan.state !== "setup" && lan.state !== "unsupported";
  // While LAN waits for an update the switch stays, if it is on, so it can
  // be turned off.
  const switchable = installed && (lan.state !== "update" || lan.wanted);

  const copy = async () => {
    if (!(await copyText(lan.ip, "LAN address"))) return;
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1500);
  };

  return (
    <Card label="LAN" wide>
      <CardHead
        icon={Network}
        title="LAN"
        caption="Play LAN games with everyone in this room, as if you were all in the same house"
      >
        {lan.state === "on" ? <LanStatusChip tone="on" label="On" /> : null}
        {lan.state === "starting" ? <LanStatusChip tone="wait" label="Starting…" /> : null}
        {switchable ? (
          <Switch
            checked={lan.wanted}
            disabled={busy}
            onCheckedChange={(on) => run(() => (on ? LanService.Enable() : LanService.Disable()))}
            aria-label="LAN"
          />
        ) : null}
      </CardHead>

      {lan.state === "unsupported" ? <Note>LAN needs Windows.</Note> : null}

      {lan.state === "setup" ? (
        <div className="flex flex-wrap items-center gap-3">
          <Note>LAN needs a one-time administrator setup. Windows asks for permission once; after that the switch just works.</Note>
          <Button size="sm" disabled={busy} onClick={() => run(() => LanService.Setup())}>
            {busy ? <Loader2 className="animate-spin" /> : <ShieldCheck />}
            {busy ? "Setting up…" : "Set up"}
          </Button>
        </div>
      ) : null}

      {lan.state === "update" ? (
        <div className="flex flex-wrap items-center gap-3">
          <Note warn>This Toru is newer than its LAN service.</Note>
          <Button size="sm" disabled={busy} onClick={() => run(() => LanService.Setup())}>
            {busy ? <Loader2 className="animate-spin" /> : <ShieldCheck />}
            {busy ? "Updating…" : "Update LAN (needs admin)"}
          </Button>
        </div>
      ) : null}

      {lan.state === "on" && lan.ip ? (
        <div className="flex flex-wrap items-center gap-2">
          <span className="text-sm text-muted-foreground">On —</span>
          <span className="select-all border border-border bg-background px-3 py-1.5 font-mono text-base tracking-wider">{lan.ip}</span>
          <Button size="sm" variant="outline" onClick={() => void copy()}>
            {copied ? <Check /> : <Copy />}
            {copied ? "Copied" : "Copy"}
          </Button>
          <span className="text-xs text-muted-foreground">Friends join your games at this address</span>
        </div>
      ) : null}

      {lan.state === "off" && installed ? <Note>Off</Note> : null}

      {lan.state === "error" ? (
        <Note warn>
          {lan.error === "range-in-use"
            ? `Address range in use${lan.detail ? `: ${lan.detail} already uses 100.96.0.0/16` : ""}. Turn that off, then try again.`
            : lan.error === "wrong-session"
              ? "LAN is already on for another Windows user on this PC."
              : lan.error === "timeout"
                ? "LAN didn't come up. Turn it off and on again; if it keeps happening, run Set up again from Advanced."
                : `LAN couldn't start${lan.detail ? `: ${lan.detail}` : "."}`}
        </Note>
      ) : null}

      {installed ? (
        <div className="flex flex-col gap-1">
          {lan.relay === "none" ? <Note>Direct only (no relay set up)</Note> : null}
          {lan.relay === "error" ? <Note warn>The relay isn't answering right now, so connections are direct only.</Note> : null}
          <Note>Turn LAN on before you start the game.</Note>
          <Note>While LAN is on, LAN games look for servers on Toru LAN, not your home network.</Note>
        </div>
      ) : null}

      {installed ? (
        <div className="flex flex-col gap-3">
          <button
            type="button"
            onClick={() => setAdvanced((a) => !a)}
            className="inline-flex w-fit items-center gap-1 text-xs text-muted-foreground hover:text-foreground"
            aria-expanded={advanced}
          >
            {advanced ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
            Advanced
          </button>
          {advanced ? (
            <>
              <Row label="Allow file sharing and remote desktop" caption="Lets people in the room reach Windows file sharing, Remote Desktop and WinRM on this PC">
                <Switch checked={lan.allowServices} disabled={busy} onCheckedChange={(v) => run(() => LanService.SetPolicy(v, lan.mdns))} />
              </Row>
              <Row label="Share mDNS and SSDP" caption="Device and media server discovery. Also shows this PC's name to the room">
                <Switch checked={lan.mdns} disabled={busy} onCheckedChange={(v) => run(() => LanService.SetPolicy(lan.allowServices, v))} />
              </Row>
              <Row label="Remove LAN" caption="Takes the service and the Toru LAN adapter off this PC (asks for permission)">
                <Button size="sm" variant="ghost" disabled={busy} onClick={() => run(() => LanService.Uninstall())}>
                  {lan.busy === "uninstall" ? <Loader2 className="animate-spin" /> : <Trash2 />}
                  {lan.busy === "uninstall" ? "Removing…" : "Remove LAN"}
                </Button>
              </Row>
              {lan.serviceVersion ? <Note>LAN service {lan.serviceVersion}</Note> : null}
            </>
          ) : null}
        </div>
      ) : null}
    </Card>
  );
}

function LanStatusChip({ tone, label }: { tone: "on" | "wait"; label: string }) {
  return (
    <span
      className={cn(
        chipBase,
        tone === "on" ? "border-emerald-500/50 bg-emerald-500/10 text-emerald-300" : "border-amber-400/60 bg-amber-400/10 text-amber-300",
      )}
    >
      <span className={cn("size-1.5 rounded-full", tone === "on" ? "bg-emerald-400" : "animate-pulse bg-amber-300")} />
      {label}
    </span>
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
  volume: number; // how loud they play for us, 0-200%
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
  onVolume,
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
  onVolume: (id: string, pct: number) => void;
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
    volume: call.volumes?.[m.id] ?? 100,
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
      volume: 100,
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
            <CallTile key={t.id} tile={t} canWatch={call.inCall} onWatch={onWatch} onVolume={onVolume} />
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

function CallTile({
  tile,
  canWatch,
  onWatch,
  onVolume,
}: {
  tile: Tile;
  canWatch: boolean;
  onWatch: (id: string) => void;
  onVolume: (id: string, pct: number) => void;
}) {
  const connecting = !tile.self && canWatch && tile.link !== "" && tile.link !== "connected";
  // Volume is per person, for us only; it applies while we're in the call.
  const hasVolume = !tile.self && canWatch;
  const [volumeOpen, setVolumeOpen] = useState(false);
  const VolumeIcon = tile.volume === 0 ? VolumeX : tile.volume < 100 ? Volume1 : Volume2;
  return (
    <div
      onContextMenu={(e) => {
        if (!hasVolume) return;
        e.preventDefault();
        setVolumeOpen(true);
      }}
      className={cn(
        "relative flex flex-col items-center gap-2 border bg-background/40 px-2 py-3 transition-colors",
        tile.speaking ? "border-emerald-400 shadow-[0_0_0_1px_rgb(52_211_153)]" : "border-border",
      )}
    >
      {hasVolume ? (
        <button
          type="button"
          onClick={() => setVolumeOpen((o) => !o)}
          title={`${tile.name}'s volume (right-click the tile too)`}
          aria-label={`${tile.name}'s volume`}
          className={cn(
            "absolute right-1 top-1 inline-flex items-center gap-0.5 p-1 text-[10px] tabular-nums hover:bg-accent hover:text-foreground",
            tile.volume === 0 ? "text-amber-300" : tile.volume !== 100 ? "text-foreground" : "text-muted-foreground",
          )}
        >
          <VolumeIcon className="size-3.5" />
          {tile.volume !== 100 ? `${tile.volume}%` : null}
        </button>
      ) : null}
      {hasVolume && volumeOpen ? (
        <VolumePopover
          name={tile.name}
          value={tile.volume}
          onChange={(pct) => onVolume(tile.id, pct)}
          onClose={() => setVolumeOpen(false)}
        />
      ) : null}
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
      {hasVolume && tile.volume === 0 ? <span className="text-[11px] text-amber-300">Muted for you</span> : null}
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

// VolumePopover: one person's volume for us, 0-200% like Discord. Changes
// apply live (the call window follows call.volumes); Go keeps it per person.
function VolumePopover({
  name,
  value,
  onChange,
  onClose,
}: {
  name: string;
  value: number;
  onChange: (pct: number) => void;
  onClose: () => void;
}) {
  const [draft, setDraft] = useState(value);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    const down = (e: PointerEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) onClose();
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    // Next tick, so the click that opened it doesn't close it.
    const t = window.setTimeout(() => window.addEventListener("pointerdown", down), 0);
    window.addEventListener("keydown", key);
    return () => {
      window.clearTimeout(t);
      window.removeEventListener("pointerdown", down);
      window.removeEventListener("keydown", key);
    };
  }, [onClose]);
  const set = (pct: number) => {
    setDraft(pct);
    onChange(pct);
  };
  return (
    <div
      ref={ref}
      role="dialog"
      aria-label={`${name}'s volume`}
      onContextMenu={(e) => e.preventDefault()}
      className="frost absolute right-1 top-8 z-20 flex w-52 flex-col gap-2 border border-border bg-background p-3 text-left shadow-lg"
    >
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-xs font-medium">{name}</span>
        <span className={cn("shrink-0 text-xs tabular-nums", draft === 0 ? "text-amber-300" : "text-muted-foreground")}>
          {draft === 0 ? "Muted for you" : `${draft}%`}
        </span>
      </div>
      <input
        type="range"
        min={0}
        max={200}
        step={5}
        value={draft}
        onChange={(e) => set(Number(e.target.value))}
        className="w-full accent-primary"
        aria-label={`${name}'s volume`}
        autoFocus
      />
      <div className="flex items-center justify-between gap-2">
        <Button size="sm" variant="ghost" className="h-7 px-2" onClick={() => set(draft === 0 ? 100 : 0)}>
          {draft === 0 ? <Volume2 /> : <VolumeX />}
          {draft === 0 ? "Unmute" : "Mute for me"}
        </Button>
        <Button size="sm" variant="ghost" className="h-7 px-2" disabled={draft === 100} onClick={() => set(100)} title="Back to 100%">
          <RotateCcw />
          Reset
        </Button>
      </div>
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
