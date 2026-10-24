import { useCallback, useEffect, useRef, useState } from "react";
import { Clapperboard, FolderOpen, Headphones, Mic, Save, Volume2 } from "lucide-react";
import { Events as WailsEvents } from "@wailsio/runtime";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { ShortcutRow } from "@/shortcuts/Shortcuts";
import { useShortcuts } from "@/shortcuts/useShortcuts";
import { ReplayService, ReplaySettings, ReplayStatus } from "@/lib/api";
import type { ReplayMonitor } from "@/lib/api";
import { cn } from "@/lib/utils";

// Instant replay panel — ShadowPlay-style "save the last N minutes". Every
// control applies immediately through ReplayService.SetSettings; the Go side
// restarts the buffer only when a capture setting changed (which also empties
// what's buffered so far, so the status line drops back to 0).

const DURATIONS = [15, 30, 60, 120, 300, 600, 1200];
const FPS_OPTIONS = [30, 60, 120, 144];
const RESOLUTIONS: { value: number; label: string }[] = [
  { value: 0, label: "Native (recommended)" },
  { value: 2160, label: "2160p" },
  { value: 1440, label: "1440p" },
  { value: 1080, label: "1080p" },
  { value: 720, label: "720p" },
];
// All three run on the GPU's video encoder chip, so the choice doesn't change
// game performance — only how big the files are and where they play.
const CODECS: {
  value: string;
  label: string;
  tag?: string;
  summary: string;
  plays: string;
  size: string;
}[] = [
  {
    value: "h264",
    label: "H.264",
    tag: "Best for sharing",
    summary: "The safe choice. Clips play anywhere you drop them.",
    plays: "Plays inline in Discord, on phones, in every editor and site",
    size: "Biggest files at a given quality",
  },
  {
    value: "hevc",
    label: "HEVC (H.265)",
    summary: "Same quality as H.264 in about a third less space.",
    plays: "Fine in editors, iPhones and YouTube; Discord often can't play it inline",
    size: "Smaller files, or sharper at the same bitrate",
  },
  {
    value: "av1",
    label: "AV1",
    summary: "The newest and most efficient. Sharpest at low bitrates.",
    plays: "Plays in Discord, browsers and YouTube; older phones and editors may not",
    size: "Smallest files; needs an RTX 40/50, Radeon RX 7000+ or Intel Arc",
  },
];
const AUDIO_QUALITY: { value: number; label: string }[] = [
  { value: 96, label: "96 kbps (smallest)" },
  { value: 128, label: "128 kbps" },
  { value: 160, label: "160 kbps (recommended)" },
  { value: 192, label: "192 kbps" },
  { value: 256, label: "256 kbps" },
  { value: 320, label: "320 kbps (best)" },
];
type VolumeKey = "desktopVolume" | "discordVolume" | "micVolume";
const MIN_MBPS = 2;
const MAX_MBPS = 150;

function formatLength(sec: number): string {
  if (sec < 60) return `${sec}s`;
  if (sec % 60 === 0) return `${sec / 60} min`;
  return `${Math.floor(sec / 60)}:${String(sec % 60).padStart(2, "0")}`;
}

function formatClock(sec: number): string {
  return `${Math.floor(sec / 60)}:${String(sec % 60).padStart(2, "0")}`;
}

// Size of one saved clip: video bitrate + the 160 kbps AAC track.
function clipMB(mbps: number, sec: number): number {
  return ((mbps + 0.16) * sec) / 8;
}

function formatMB(mb: number): string {
  return mb >= 1000 ? `${(mb / 1000).toFixed(1)} GB` : `${Math.round(mb)} MB`;
}

function eventData<T>(data: unknown): T {
  return (Array.isArray(data) ? data[0] : data) as T;
}

const selectClass = "frost w-full border border-border bg-transparent px-2 py-1 text-sm";
const optionClass = "bg-background text-foreground";

export function ReplayPanel() {
  const [settings, setSettings] = useState<ReplaySettings | null>(null);
  const [status, setStatus] = useState<ReplayStatus | null>(null);
  const [busy, setBusy] = useState(false);
  const [applyError, setApplyError] = useState("");
  const [monitors, setMonitors] = useState<ReplayMonitor[]>([]);
  const [mics, setMics] = useState<string[]>([]);
  const [discordRunning, setDiscordRunning] = useState(false);
  const [encoders, setEncoders] = useState<Record<string, boolean> | null>(null);
  const [saveDir, setSaveDir] = useState("");
  const [lastSave, setLastSave] = useState<{ ok: boolean; text: string } | null>(null);
  const [saving, setSaving] = useState(false);
  const [bitrateDraft, setBitrateDraft] = useState<number | null>(null);
  const bitrateTimer = useRef<number | undefined>(undefined);
  const [volumeDrafts, setVolumeDrafts] = useState<Partial<Record<VolumeKey, number>>>({});
  const volumeTimers = useRef<Partial<Record<VolumeKey, number>>>({});
  const [lastMic, setLastMic] = useState("");
  // Debounced slider commits run after later renders; read the newest settings.
  const settingsRef = useRef<ReplaySettings | null>(null);
  settingsRef.current = settings;
  const shortcuts = useShortcuts();
  const saveShortcut = shortcuts.shortcuts.find((sc) => sc.action === "replaySave");

  const refreshStatus = useCallback(() => {
    void ReplayService.GetStatus()
      .then((s) => setStatus(s))
      .catch(() => {});
  }, []);

  useEffect(() => {
    void ReplayService.GetSettings()
      .then(setSettings)
      .catch(() => {});
    void ReplayService.GetSaveDir()
      .then(setSaveDir)
      .catch(() => {});
    void ReplayService.ListMonitors()
      .then((m) => setMonitors(m ?? []))
      .catch(() => {});
    void ReplayService.ListMicrophones()
      .then((m) => setMics(m ?? []))
      .catch(() => {});
    void ReplayService.DiscordRunning()
      .then(setDiscordRunning)
      .catch(() => {});
    void ReplayService.EncoderSupport()
      .then((e) => setEncoders((e ?? {}) as Record<string, boolean>))
      .catch(() => {});
    refreshStatus();

    const offStatus = WailsEvents.On("replay:status", (ev) => {
      setStatus(ReplayStatus.createFrom(eventData(ev.data)));
    });
    const offSaved = WailsEvents.On("replay:saved", (ev) => {
      const path = eventData<string>(ev.data) ?? "";
      setLastSave({ ok: true, text: `Saved ${path.split(/[\\/]/).pop()}` });
    });
    const offError = WailsEvents.On("replay:error", (ev) => {
      setLastSave({ ok: false, text: eventData<string>(ev.data) ?? "Save failed" });
    });
    return () => {
      offStatus();
      offSaved();
      offError();
    };
  }, [refreshStatus]);

  // Status events only fire on state changes; poll so the "ready" counter fills.
  const enabled = settings?.enabled ?? false;
  useEffect(() => {
    if (!enabled) return;
    const id = window.setInterval(refreshStatus, 2000);
    return () => window.clearInterval(id);
  }, [enabled, refreshStatus]);

  const update = useCallback(
    async (patch: Partial<ReplaySettings>) => {
      const current = settingsRef.current;
      if (!current) return;
      const next = ReplaySettings.createFrom({ ...current, ...patch });
      settingsRef.current = next;
      setSettings(next);
      setBusy(true);
      setApplyError("");
      try {
        setStatus(await ReplayService.SetSettings(next));
      } catch (e) {
        setApplyError(String(e));
        refreshStatus();
      } finally {
        setBusy(false);
        void ReplayService.GetSettings()
          .then(setSettings)
          .catch(() => {});
      }
    },
    [refreshStatus],
  );

  // The bitrate slider fires on every pixel of drag; restart the buffer once
  // the user settles instead of on each step.
  const onBitrate = (mbps: number) => {
    setBitrateDraft(mbps);
    window.clearTimeout(bitrateTimer.current);
    bitrateTimer.current = window.setTimeout(() => {
      setBitrateDraft(null);
      void update({ bitrateMbps: mbps });
    }, 600);
  };

  const onVolume = (key: VolumeKey, value: number) => {
    setVolumeDrafts((d) => ({ ...d, [key]: value }));
    window.clearTimeout(volumeTimers.current[key]);
    volumeTimers.current[key] = window.setTimeout(() => {
      setVolumeDrafts((d) => {
        const rest = { ...d };
        delete rest[key];
        return rest;
      });
      void update({ [key]: value });
    }, 600);
  };

  const setMicOn = (on: boolean) => {
    if (on) {
      const device = lastMic || mics[0] || "";
      if (device) void update({ micDevice: device });
    } else {
      setLastMic(settings?.micDevice ?? "");
      void update({ micDevice: "" });
    }
  };

  const saveNow = async () => {
    setSaving(true);
    try {
      await ReplayService.SaveReplay();
    } catch {
      // replay:error already carries the message
    } finally {
      setSaving(false);
    }
  };

  const pickDir = async () => {
    try {
      const chosen = await ReplayService.PickSaveDir();
      if (chosen) setSaveDir(chosen);
    } catch {
      // keep the current folder
    }
  };

  const resetDir = async () => {
    try {
      await ReplayService.ResetSaveDir();
      setSaveDir(await ReplayService.GetSaveDir());
      setSettings(await ReplayService.GetSettings());
    } catch {
      // keep the current folder
    }
  };

  if (!settings) {
    return (
      <div className="frost flex flex-col gap-2 p-5">
        <PanelHeader />
        <p className="text-xs text-muted-foreground">Loading…</p>
      </div>
    );
  }

  const mbps = bitrateDraft ?? settings.bitrateMbps;
  const error = applyError || status?.error || "";

  return (
    <div className="frost flex flex-col gap-4 p-5">
      <div className="flex items-start justify-between gap-3">
        <div className="flex flex-col gap-0.5">
          <PanelHeader />
          <span className="text-xs text-muted-foreground">
            Keeps the last {formatLength(settings.durationSec)} of your screen ready in the
            background. Press the hotkey to save it.
          </span>
        </div>
        <Switch
          checked={settings.enabled}
          disabled={busy}
          onCheckedChange={(v) => void update({ enabled: v })}
          aria-label="Enable instant replay"
        />
      </div>

      {settings.enabled && (
        <StatusLine status={status} busy={busy} error={error} keep={settings.durationSec} />
      )}
      {!settings.enabled && applyError && <p className="text-xs text-destructive">{applyError}</p>}

      {saveShortcut && (
        <div className="flex flex-col gap-2 border-t border-border pt-4">
          <ShortcutRow sc={saveShortcut} onSave={shortcuts.save} onReset={shortcuts.reset} />
          <div className="flex flex-wrap items-center gap-2">
            <Button
              size="sm"
              variant="secondary"
              disabled={!settings.enabled || saving || !status?.recording}
              onClick={() => void saveNow()}
            >
              <Save /> {saving ? "Saving…" : `Save last ${formatLength(settings.durationSec)}`}
            </Button>
            {lastSave && (
              <span
                className={cn(
                  "truncate text-xs",
                  lastSave.ok ? "text-muted-foreground" : "text-destructive",
                )}
                title={lastSave.text}
              >
                {lastSave.text}
              </span>
            )}
          </div>
        </div>
      )}

      <Section title="Clip length" hint="How much the hotkey saves">
        <div className="flex flex-wrap gap-1.5">
          {DURATIONS.map((d) => (
            <Button
              key={d}
              size="sm"
              variant={settings.durationSec === d ? "default" : "outline"}
              disabled={busy}
              onClick={() => void update({ durationSec: d })}
            >
              {formatLength(d)}
            </Button>
          ))}
        </div>
      </Section>

      <Section title="Capture">
        <Field label="Screen">
          <select
            className={selectClass}
            value={settings.monitorId}
            disabled={busy}
            onChange={(e) => void update({ monitorId: Number(e.target.value) })}
          >
            <option value={-1} className={optionClass}>
              Primary display
            </option>
            {monitors.map((m) => (
              <option key={m.id} value={m.id} className={optionClass}>
                {m.label}
              </option>
            ))}
          </select>
        </Field>
        <ToggleRow
          label="Show mouse cursor"
          checked={settings.includeCursor}
          disabled={busy}
          onChange={(v) => void update({ includeCursor: v })}
        />
      </Section>

      <Section title="Quality">
        <div className="grid grid-cols-2 gap-3">
          <Field label="Resolution">
            <select
              className={selectClass}
              value={settings.height}
              disabled={busy}
              onChange={(e) => void update({ height: Number(e.target.value) })}
            >
              {RESOLUTIONS.map((r) => (
                <option key={r.value} value={r.value} className={optionClass}>
                  {r.label}
                </option>
              ))}
            </select>
          </Field>
          <Field label="Frame rate">
            <select
              className={selectClass}
              value={settings.fps}
              disabled={busy}
              onChange={(e) => void update({ fps: Number(e.target.value) })}
            >
              {FPS_OPTIONS.map((f) => (
                <option key={f} value={f} className={optionClass}>
                  {f} fps
                </option>
              ))}
            </select>
          </Field>
        </div>
        {settings.height !== 0 && (
          <p className="text-xs text-muted-foreground">
            Resizing happens on the CPU (about one core). Native stays entirely on the GPU — for
            smaller files, lower the bitrate instead.
          </p>
        )}

        <Field label={`Bitrate · ${mbps} Mbps`}>
          <input
            type="range"
            min={MIN_MBPS}
            max={MAX_MBPS}
            step={1}
            value={mbps}
            disabled={busy && bitrateDraft === null}
            onChange={(e) => onBitrate(Number(e.target.value))}
            className="w-full accent-primary"
            aria-label="Video bitrate in megabits per second"
          />
          <span className="text-xs text-muted-foreground">
            A {formatLength(settings.durationSec)} clip is about{" "}
            {formatMB(clipMB(mbps, settings.durationSec))}
          </span>
        </Field>

        <div className="flex flex-col gap-1.5">
          <span className="text-xs text-muted-foreground">
            Encoder — all three run on your graphics card's video chip, so none of them
            costs game performance. They differ in file size and where clips play.
          </span>
          <div role="radiogroup" aria-label="Encoder" className="grid gap-2 sm:grid-cols-3">
            {CODECS.map((c) => {
              const unsupported = encoders !== null && encoders[c.value] === false;
              const selected = settings.codec === c.value;
              return (
                <button
                  key={c.value}
                  type="button"
                  role="radio"
                  aria-checked={selected}
                  disabled={busy || unsupported}
                  onClick={() => void update({ codec: c.value })}
                  className={cn(
                    "flex flex-col gap-1.5 border p-3 text-left transition-colors",
                    "disabled:cursor-not-allowed disabled:opacity-50",
                    selected
                      ? "border-primary bg-primary/10"
                      : "border-border hover:border-primary/60",
                  )}
                >
                  <span className="flex flex-wrap items-center gap-1.5">
                    <span className="text-sm font-medium">{c.label}</span>
                    {c.tag && (
                      <span className="bg-primary/15 px-1.5 py-0.5 text-[10px] font-medium text-primary">
                        {c.tag}
                      </span>
                    )}
                  </span>
                  <span className="text-xs">{c.summary}</span>
                  <span className="text-xs text-muted-foreground">{c.plays}</span>
                  <span className="text-xs text-muted-foreground">{c.size}</span>
                  {unsupported && (
                    <span className="text-xs text-destructive">Your graphics card can't encode this</span>
                  )}
                </button>
              );
            })}
          </div>
          <span className="text-xs text-muted-foreground">
            Not sure? Keep H.264 if you post clips to Discord. Pick AV1 to save disk space.
          </span>
        </div>
      </Section>

      <Section
        title="Audio"
        hint="Choose what ends up in your clips. Each source has its own volume."
      >
        <AudioSourceRow
          icon={<Volume2 />}
          label="Game & desktop"
          hint="Your game, music, browser: everything you hear except Discord"
          checked={settings.desktopAudio}
          volume={volumeDrafts.desktopVolume ?? settings.desktopVolume}
          disabled={busy}
          onToggle={(v) => void update({ desktopAudio: v })}
          onVolume={(v) => onVolume("desktopVolume", v)}
        />
        <AudioSourceRow
          icon={<Headphones />}
          label="Discord voice chat"
          hint={
            discordRunning
              ? "Your friends in the call, plus Discord's sounds"
              : "Discord isn't running. It's picked up automatically when it starts"
          }
          checked={settings.discordAudio}
          volume={volumeDrafts.discordVolume ?? settings.discordVolume}
          disabled={busy}
          onToggle={(v) => void update({ discordAudio: v })}
          onVolume={(v) => onVolume("discordVolume", v)}
        />
        <AudioSourceRow
          icon={<Mic />}
          label="Your microphone"
          hint={
            mics.length === 0
              ? "No microphone found"
              : "Your own voice. Turn it off to keep yourself out of clips"
          }
          checked={settings.micDevice !== ""}
          volume={volumeDrafts.micVolume ?? settings.micVolume}
          disabled={busy || mics.length === 0}
          onToggle={setMicOn}
          onVolume={(v) => onVolume("micVolume", v)}
        >
          {settings.micDevice !== "" && (
            <select
              className={selectClass}
              value={settings.micDevice}
              disabled={busy}
              onChange={(e) => void update({ micDevice: e.target.value })}
              aria-label="Microphone device"
            >
              {!mics.includes(settings.micDevice) && (
                <option value={settings.micDevice} className={optionClass}>
                  {settings.micDevice} (not connected)
                </option>
              )}
              {mics.map((m) => (
                <option key={m} value={m} className={optionClass}>
                  {m}
                </option>
              ))}
            </select>
          )}
        </AudioSourceRow>

        <ToggleRow
          label="Separate audio tracks"
          hint="Also saves game, Discord and mic as their own tracks, so you can mute or level each one later in an editor like Premiere or DaVinci Resolve. Players still play the full mix."
          checked={settings.separateTracks}
          disabled={busy}
          onChange={(v) => void update({ separateTracks: v })}
        />
        <Field label="Audio quality">
          <select
            className={selectClass}
            value={settings.audioBitrateKbps}
            disabled={busy}
            onChange={(e) => void update({ audioBitrateKbps: Number(e.target.value) })}
          >
            {AUDIO_QUALITY.map((q) => (
              <option key={q.value} value={q.value} className={optionClass}>
                {q.label}
              </option>
            ))}
          </select>
        </Field>
        <span className="text-xs text-muted-foreground">
          Changing audio settings restarts the buffer, so anything recorded before the change is
          cleared.
        </span>
      </Section>

      <Section title="Saving">
        <div
          className="truncate border border-border bg-background/40 px-2 py-1.5 font-mono text-[11px] text-muted-foreground"
          title={saveDir || "—"}
        >
          {saveDir || "—"}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button size="sm" variant="secondary" onClick={() => void pickDir()}>
            <FolderOpen /> Change…
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={!settings.saveDir}
            onClick={() => void resetDir()}
          >
            Reset default
          </Button>
          <Button size="sm" variant="ghost" onClick={() => void ReplayService.OpenSaveDir()}>
            Open folder
          </Button>
        </div>
        <ToggleRow
          label="Sort clips into game folders"
          hint="Each clip goes into a folder named after the game you're playing when you press the hotkey (fullscreen games, and anything from Steam, Epic, Xbox, Riot, GOG, EA or Ubisoft). Everything else goes into Desktop."
          checked={settings.gameFolders}
          disabled={busy}
          onChange={(v) => void update({ gameFolders: v })}
        />
        <ToggleRow
          label="Show a message when a clip is saved"
          hint="A small note in the top-right corner. It never takes focus from your game. Exclusive-fullscreen games hide it, so keep the sound on too."
          checked={settings.toastOnSave}
          disabled={busy}
          onChange={(v) => void update({ toastOnSave: v })}
        />
        <ToggleRow
          label="Play a sound when a clip is saved"
          checked={settings.soundOnSave}
          disabled={busy}
          onChange={(v) => void update({ soundOnSave: v })}
        />
      </Section>

      <p className="border-t border-border pt-4 text-xs text-muted-foreground">
        Built for gaming: the screen is captured and encoded by your GPU's dedicated video engine,
        so frames never touch the CPU at native resolution. While on, it uses about{" "}
        {formatMB(clipMB(mbps, settings.durationSec + 6))} of temporary disk space, cleared when
        you turn it off.
      </p>
    </div>
  );
}

function PanelHeader() {
  return (
    <div className="flex items-center gap-2">
      <Clapperboard className="size-4" />
      <span className="text-sm font-medium">Instant replay</span>
    </div>
  );
}

function StatusLine({
  status,
  busy,
  error,
  keep,
}: {
  status: ReplayStatus | null;
  busy: boolean;
  error: string;
  keep: number;
}) {
  if (busy) {
    return <p className="text-xs text-muted-foreground">Starting…</p>;
  }
  if (error) {
    return <p className="text-xs text-destructive">{error}</p>;
  }
  if (!status?.recording) {
    return <p className="text-xs text-muted-foreground">Starting capture…</p>;
  }
  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground">
      <span className="size-2 shrink-0 animate-pulse bg-red-500" aria-hidden="true" />
      <span>
        Buffering · {formatClock(status.bufferedSec)} of {formatClock(keep)} ready ·{" "}
        {status.encoder}
        {status.zeroCopy ? " (GPU zero-copy)" : ""}
      </span>
    </div>
  );
}

function Section({
  title,
  hint,
  children,
}: {
  title: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex flex-col gap-3 border-t border-border pt-4">
      <div className="flex flex-col">
        <span className="text-sm font-medium">{title}</span>
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      </div>
      {children}
    </div>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="flex flex-col gap-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      {children}
    </label>
  );
}

function AudioSourceRow({
  icon,
  label,
  hint,
  checked,
  volume,
  disabled,
  onToggle,
  onVolume,
  children,
}: {
  icon: React.ReactNode;
  label: string;
  hint: string;
  checked: boolean;
  volume: number;
  disabled?: boolean;
  onToggle: (v: boolean) => void;
  onVolume: (v: number) => void;
  children?: React.ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex flex-col gap-2 border p-3 transition-colors",
        checked ? "border-border" : "border-border/50",
      )}
    >
      <div className="flex items-center justify-between gap-3">
        <div className="flex items-start gap-2.5">
          <span
            className={cn(
              "mt-0.5 [&>svg]:size-4",
              checked ? "text-primary" : "text-muted-foreground",
            )}
          >
            {icon}
          </span>
          <div className="flex flex-col">
            <span className="text-sm">{label}</span>
            <span className="text-xs text-muted-foreground">{hint}</span>
          </div>
        </div>
        <Switch checked={checked} disabled={disabled} onCheckedChange={onToggle} aria-label={label} />
      </div>
      {checked && (
        <>
          {children}
          <div className="flex items-center gap-3">
            <span className="w-14 shrink-0 text-xs text-muted-foreground">Volume</span>
            <input
              type="range"
              min={0}
              max={200}
              step={5}
              value={volume}
              onChange={(e) => onVolume(Number(e.target.value))}
              className="w-full accent-primary"
              aria-label={`${label} volume`}
            />
            <span className="w-11 shrink-0 text-right text-xs tabular-nums">
              {volume === 0 ? "Muted" : `${volume}%`}
            </span>
          </div>
        </>
      )}
    </div>
  );
}

function ToggleRow({
  label,
  hint,
  checked,
  disabled,
  onChange,
}: {
  label: string;
  hint?: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="flex flex-col">
        <span className="text-sm">{label}</span>
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      </div>
      <Switch checked={checked} disabled={disabled} onCheckedChange={onChange} aria-label={label} />
    </div>
  );
}
