import { useCallback, useEffect, useRef, useState } from "react";
import { ChevronRight, Circle, Clapperboard, Film, FolderOpen, Headphones, Info, Mic, Save, Square, Volume2 } from "lucide-react";
import { Tooltip } from "@/components/ui/tooltip";
import { Events as WailsEvents } from "@wailsio/runtime";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { ShortcutRow } from "@/shortcuts/Shortcuts";
import { useShortcuts } from "@/shortcuts/useShortcuts";
import { ReplayService, ReplaySettings, ReplayStatus, Shortcut } from "@/lib/api";
import type { RecorderState, ReplayMonitor } from "@/lib/api";
import { cn } from "@/lib/utils";

// Instant replay panel — ShadowPlay-style "save the last N minutes". Every
// control applies immediately through ReplayService.SetSettings; the Go side
// restarts the buffer only when a capture setting changed (which also empties
// what's buffered so far, so the status line drops back to 0).

const DURATIONS = [15, 30, 60, 120, 300, 600, 1200];

// Recording modes: ShadowPlay-style instant replay, plus Steam Game
// Recording's background and manual modes.
const MODES: { value: string; label: string; icon: React.ReactNode; summary: string; detail: string }[] = [
  {
    value: "replay",
    label: "Instant replay",
    icon: <Clapperboard className="size-4" />,
    summary: "Keeps the last few minutes. Press the hotkey to save a clip.",
    detail: "Lightest on disk: nothing is kept unless you save it.",
  },
  {
    value: "background",
    label: "Background recording",
    icon: <Film className="size-4" />,
    summary: "Records your whole play session. Scrub back and cut clips later.",
    detail: "Old footage is deleted automatically after the time you choose.",
  },
  {
    value: "manual",
    label: "Manual recording",
    icon: <Circle className="size-4" />,
    summary: "Press a hotkey to start and stop recording.",
    detail: "Recordings are kept until you delete them.",
  },
];
const KEEP_OPTIONS = [30, 60, 120, 240, 480, 720, 1440];
const QUALITY: { value: string; label: string; mbps?: number }[] = [
  { value: "standard", label: "Standard", mbps: 12 },
  { value: "high", label: "High", mbps: 25 },
  { value: "custom", label: "Custom" },
];

function formatHours(h: number): string {
  if (h < 1) return `${Math.max(1, Math.round(h * 60))} min`;
  return h < 10 ? `${h.toFixed(1)} hours` : `${Math.round(h)} hours`;
}
const DISK_OPTIONS = [10, 25, 50, 100, 200, 500];

function formatMinutes(m: number): string {
  return m < 60 ? `${m} min` : `${m / 60} h`;
}

function formatGB(bytes: number): string {
  return bytes >= 1e9 ? `${(bytes / 1e9).toFixed(1)} GB` : `${Math.round(bytes / 1e6)} MB`;
}
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
  const recordShortcut = shortcuts.shortcuts.find((sc) => sc.action === "recordToggle");
  const markerShortcut = shortcuts.shortcuts.find((sc) => sc.action === "timelineMarker");
  const [recorder, setRecorder] = useState<RecorderState | null>(null);
  const [recordingsDir, setRecordingsDir] = useState("");
  const refreshRecorder = useCallback(() => {
    void ReplayService.GetRecorderState()
      .then((r) => {
        setRecorder(r);
        setRecordingsDir(r.dir);
      })
      .catch(() => {});
  }, []);
  useEffect(() => {
    refreshRecorder();
    const off = WailsEvents.On("recording:changed", () => refreshRecorder());
    return () => off();
  }, [refreshRecorder]);

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
    const id = window.setInterval(() => {
      refreshStatus();
      refreshRecorder();
    }, 2000);
    return () => window.clearInterval(id);
  }, [enabled, refreshStatus, refreshRecorder]);

  const toggleManual = async () => {
    try {
      await ReplayService.ToggleRecording();
    } catch (e) {
      setApplyError(String(e).replace(/^Error:\s*/, ""));
    }
    refreshRecorder();
    refreshStatus();
  };

  const pickRecordingsDir = async () => {
    try {
      const chosen = await ReplayService.PickRecordingsDir();
      if (chosen) setRecordingsDir(chosen);
      setSettings(await ReplayService.GetSettings());
    } catch {
      // keep the current folder
    }
  };

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

  const [recBitrateDraft, setRecBitrateDraft] = useState<number | null>(null);
  const recBitrateTimer = useRef<number | undefined>(undefined);
  const onRecBitrate = (m: number) => {
    setRecBitrateDraft(m);
    window.clearTimeout(recBitrateTimer.current);
    recBitrateTimer.current = window.setTimeout(() => {
      setRecBitrateDraft(null);
      void update({ recordingBitrateMbps: m });
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
  const mode = MODES.find((m) => m.value === settings.mode) ?? MODES[0];
  const codec = CODECS.find((c) => c.value === settings.codec) ?? CODECS[0];
  const session = settings.mode !== "replay";
  const saveKey = saveShortcut ? comboText(saveShortcut) : "";
  const monitorLabel =
    settings.monitorId < 0
      ? "Primary display"
      : (monitors.find((m) => m.id === settings.monitorId)?.label.split(" · ")[0] ?? "Display");
  // Background/manual recording has its own quality, separate from instant replay.
  const customRecMbps = recBitrateDraft ?? settings.recordingBitrateMbps;
  const qualitySpec = (q: string) =>
    q === "standard"
      ? { fps: Math.min(settings.fps, 60), mbps: 12 }
      : q === "high"
        ? { fps: Math.min(settings.fps, 60), mbps: 25 }
        : { fps: settings.recordingFps, mbps: customRecMbps };
  const recSpec = qualitySpec(settings.recordingQuality);
  const recMbps = recSpec.mbps;
  const gbPerHour = (m: number) => (m * 3600) / 8 / 1000;
  const fmtGBh = (m: number) => {
    const g = gbPerHour(m);
    return `${g.toFixed(g < 10 ? 1 : 0)} GB/hour`;
  };
  const audioParts = [
    settings.desktopAudio && "Game",
    settings.discordAudio && "Discord",
    settings.micDevice && "Mic",
  ].filter(Boolean);

  return (
    <div className="flex flex-col gap-3">
      {/* Header: on/off, live status, mode */}
      <div className="frost flex flex-col gap-3 p-4">
        <div className="flex items-center justify-between gap-3">
          <PanelHeader />
          <Switch
            checked={settings.enabled}
            disabled={busy}
            onCheckedChange={(v) => void update({ enabled: v })}
            aria-label="Enable game recording"
          />
        </div>

        <div role="radiogroup" aria-label="Recording mode" className="grid grid-cols-3 border border-border">
          {MODES.map((m, i) => {
            const selected = settings.mode === m.value;
            return (
              <button
                key={m.value}
                type="button"
                role="radio"
                aria-checked={selected}
                disabled={busy}
                onClick={() => void update({ mode: m.value })}
                className={cn(
                  "flex items-center justify-center gap-1.5 px-2 py-2 text-xs font-medium transition-colors disabled:opacity-60",
                  i > 0 && "border-l border-border",
                  selected ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-accent hover:text-foreground",
                )}
              >
                {m.icon}
                <span className="truncate">{m.label}</span>
              </button>
            );
          })}
        </div>
        <p className="flex items-center gap-1.5 text-xs text-muted-foreground">
          {mode.summary}
          <InfoTip>{mode.detail}</InfoTip>
        </p>

        {settings.enabled ? (
          settings.mode === "replay" ? (
            <StatusLine status={status} busy={busy} error={error} keep={settings.durationSec} />
          ) : (
            <RecorderLine
              recorder={recorder}
              status={status}
              busy={busy}
              error={error}
              gamesOnly={settings.gamesOnly}
              recordKey={recordShortcut ? comboText(recordShortcut) : ""}
            />
          )
        ) : applyError ? (
          <p className="text-xs text-destructive">{applyError}</p>
        ) : (
          <p className="text-xs text-muted-foreground">Off. Turn it on to start.</p>
        )}
      </div>

      <Collapsible
        id="hotkeys"
        title="Hotkeys & clips"
        summary={
          (saveKey ? `${saveKey} saves the last ${formatLength(settings.durationSec)}` : `Saves the last ${formatLength(settings.durationSec)}`) +
          (settings.mode === "manual" && recordShortcut ? ` · ${comboText(recordShortcut)} records` : "") +
          (session && markerShortcut ? ` · ${comboText(markerShortcut)} marks` : "")
        }
        defaultOpen
      >
        {saveShortcut && (
          <div className="flex flex-col gap-1">
            <ShortcutRow
              sc={session ? Shortcut.createFrom({ ...saveShortcut, label: "Save a clip" }) : saveShortcut}
              onSave={shortcuts.save}
              onReset={shortcuts.reset}
            />
            {session ? (
              <span className="text-xs text-muted-foreground">
                Saves the last {formatLength(settings.durationSec)} of the recording to your clips. The
                recording keeps going.
              </span>
            ) : null}
          </div>
        )}
        <Field
          label={
            <>
              Clip length
              <InfoTip>
                How much the save hotkey keeps.
                {session ? " It works while recording too, and drops a marker on the timeline." : ""}
              </InfoTip>
            </>
          }
        >
          <Chips
            options={DURATIONS}
            value={settings.durationSec}
            format={formatLength}
            disabled={busy}
            onPick={(d) => void update({ durationSec: d })}
          />
        </Field>
        <div className="flex flex-wrap items-center gap-2">
          <Button
            size="sm"
            variant="secondary"
            disabled={!settings.enabled || saving || !status?.recording}
            onClick={() => void saveNow()}
          >
            <Save /> {saving ? "Saving…" : `Save last ${formatLength(settings.durationSec)} now`}
          </Button>
          {session && !status?.recording && settings.enabled ? (
            <span className="text-xs text-muted-foreground">
              {settings.mode === "background" ? "Available while a game is being recorded" : "Available while recording"}
            </span>
          ) : null}
          {lastSave && (
            <span
              className={cn("truncate text-xs", lastSave.ok ? "text-muted-foreground" : "text-destructive")}
              title={lastSave.text}
            >
              {lastSave.text}
            </span>
          )}
        </div>
        {settings.mode === "manual" && recordShortcut && (
          <div className="flex flex-col gap-2 border-t border-border pt-3">
            <ShortcutRow sc={recordShortcut} onSave={shortcuts.save} onReset={shortcuts.reset} />
            <div>
              <Button
                size="sm"
                variant={recorder?.manual ? "destructive" : "secondary"}
                disabled={!settings.enabled || busy}
                onClick={() => void toggleManual()}
              >
                {recorder?.manual ? <Square /> : <Circle />} {recorder?.manual ? "Stop recording" : "Start recording"}
              </Button>
            </div>
          </div>
        )}
        {session && markerShortcut && (
          <div className="border-t border-border pt-3">
            <ShortcutRow sc={markerShortcut} onSave={shortcuts.save} onReset={shortcuts.reset} />
          </div>
        )}
      </Collapsible>

      {session && (
        <Collapsible
          id="storage"
          title={settings.mode === "background" ? "Background recording" : "Recording storage"}
          summary={
            `${QUALITY.find((q) => q.value === settings.recordingQuality)?.label ?? "Custom"} (${recSpec.fps} fps, ${recSpec.mbps} Mbps) · ` +
            (settings.mode === "background"
              ? settings.retention === "time"
                ? `keeps ${formatMinutes(settings.backgroundMinutes)} · `
                : "keeps whole sessions · "
              : "") +
            (recorder ? `${formatGB(recorder.diskBytes)} of ${settings.maxDiskGB} GB used` : `${settings.maxDiskGB} GB limit`)
          }
          defaultOpen
        >
          <Field
            label={
              <>
                Recording quality
                <InfoTip>
                  Whole sessions need a much lower bitrate than instant replay to fit on disk. Standard
                  looks good for watching back and cutting clips. Custom lets you pick the frame rate and
                  bitrate yourself. Instant replay keeps its own quality (Video section).
                </InfoTip>
              </>
            }
          >
            <div role="radiogroup" aria-label="Recording quality" className="grid grid-cols-3 border border-border">
              {QUALITY.map((q, i) => {
                const selected = settings.recordingQuality === q.value;
                const spec = qualitySpec(q.value);
                return (
                  <button
                    key={q.value}
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    disabled={busy}
                    onClick={() => void update({ recordingQuality: q.value })}
                    className={cn(
                      "flex flex-col items-center px-2 py-1.5 text-xs transition-colors disabled:opacity-60",
                      i > 0 && "border-l border-border",
                      selected ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-accent hover:text-foreground",
                    )}
                  >
                    <span className="font-medium">{q.label}</span>
                    <span className={cn("text-[10px]", selected ? "opacity-90" : "opacity-70")}>
                      {spec.fps} fps · {spec.mbps} Mbps
                    </span>
                    <span className={cn("text-[10px]", selected ? "opacity-90" : "opacity-70")}>
                      ~{fmtGBh(spec.mbps)}
                    </span>
                  </button>
                );
              })}
            </div>
          </Field>
          {settings.recordingQuality === "custom" && (
            <div className="grid grid-cols-[8rem_1fr] items-end gap-3">
              <Field
                label={
                  <>
                    Frame rate
                    <InfoTip>
                      Higher frame rates look smoother but don't change the file size: the bitrate is a
                      fixed amount of data per second, spread over more frames. Raise the bitrate too if
                      fast motion looks blocky.
                    </InfoTip>
                  </>
                }
              >
                <select
                  className={selectClass}
                  value={settings.recordingFps}
                  disabled={busy}
                  onChange={(e) => void update({ recordingFps: Number(e.target.value) })}
                >
                  {FPS_OPTIONS.map((f) => (
                    <option key={f} value={f} className={optionClass}>
                      {f} fps
                    </option>
                  ))}
                </select>
              </Field>
              <Field label={`Bitrate · ${customRecMbps} Mbps · ~${fmtGBh(customRecMbps)}`}>
                <input
                  type="range"
                  min={MIN_MBPS}
                  max={MAX_MBPS}
                  step={1}
                  value={customRecMbps}
                  disabled={busy && recBitrateDraft === null}
                  onChange={(e) => onRecBitrate(Number(e.target.value))}
                  className="w-full accent-primary"
                  aria-label="Recording bitrate in megabits per second"
                />
              </Field>
            </div>
          )}
          {settings.mode === "background" && (
            <>
              <Field
                label={
                  <>
                    What to keep
                    <InfoTip>
                      Whole sessions: every play session is kept in full until the storage limit is
                      reached, then the oldest session is deleted. Last few hours: only recent footage
                      is kept and anything older is trimmed as you play.
                    </InfoTip>
                  </>
                }
              >
                <div role="radiogroup" aria-label="What to keep" className="grid grid-cols-2 border border-border">
                  {[
                    { value: "sessions", label: "Whole sessions", sub: "Delete the oldest when full" },
                    { value: "time", label: "Last few hours", sub: "Trim older footage" },
                  ].map((o, i) => {
                    const selected = settings.retention === o.value;
                    return (
                      <button
                        key={o.value}
                        type="button"
                        role="radio"
                        aria-checked={selected}
                        disabled={busy}
                        onClick={() => void update({ retention: o.value })}
                        className={cn(
                          "flex flex-col items-center px-2 py-1.5 text-xs transition-colors disabled:opacity-60",
                          i > 0 && "border-l border-border",
                          selected ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-accent hover:text-foreground",
                        )}
                      >
                        <span className="font-medium">{o.label}</span>
                        <span className={cn("text-[10px]", selected ? "opacity-90" : "opacity-70")}>{o.sub}</span>
                      </button>
                    );
                  })}
                </div>
              </Field>
              {settings.retention === "time" && (
                <Field label="Keep the last">
                  <Chips
                    options={KEEP_OPTIONS}
                    value={settings.backgroundMinutes}
                    format={formatMinutes}
                    disabled={busy}
                    onPick={(m) => void update({ backgroundMinutes: m })}
                  />
                </Field>
              )}
              <ToggleRow
                label="Only while playing a game"
                info="Starts when a game is in focus (fullscreen, or anything installed from Steam, Epic, Xbox, Riot, …) and stops when it closes. Off: records your screen all the time."
                checked={settings.gamesOnly}
                disabled={busy}
                onChange={(v) => void update({ gamesOnly: v })}
              />
            </>
          )}
          <Field
            label={
              <>
                Storage limit
                <InfoTip>
                  {settings.mode === "background"
                    ? settings.retention === "time"
                      ? "When full, the oldest background footage is trimmed first. Manual recordings are never deleted automatically. To keep something for good, save it as a clip."
                      : "When full, the oldest whole session is deleted to make room. Manual recordings are never deleted automatically. To keep something for good, save it as a clip."
                    : "Manual recordings are never deleted automatically. Delete old ones from the Library's Recordings tab."}
                </InfoTip>
              </>
            }
          >
            <Chips
              options={DISK_OPTIONS}
              value={settings.maxDiskGB}
              format={(g) => `${g} GB`}
              disabled={busy}
              onPick={(g) => void update({ maxDiskGB: g })}
            />
          </Field>
          <div className="flex flex-col gap-1">
            {recorder ? (
              <div className="h-1 w-full overflow-hidden bg-muted">
                <div
                  className={cn("h-full", recorder.diskBytes > recorder.maxBytes * 0.9 ? "bg-destructive" : "bg-primary")}
                  style={{ width: `${Math.min(100, (recorder.diskBytes / Math.max(1, recorder.maxBytes)) * 100)}%` }}
                />
              </div>
            ) : null}
            <span className="text-xs text-muted-foreground">
              {recorder ? `${formatGB(recorder.diskBytes)} used · ` : ""}
              {settings.maxDiskGB} GB holds about{" "}
              <span className="text-foreground">{formatHours((settings.maxDiskGB * 8000) / Math.max(1, recMbps) / 3600)}</span>{" "}
              at {recMbps} Mbps · Toru always leaves 10 GB free on the drive
            </span>
          </div>
          <FolderRow
            label="Recordings folder"
            path={recordingsDir}
            onChange={() => void pickRecordingsDir()}
            onReset={settings.recordingsDir ? () => void ReplayService.ResetRecordingsDir().then(refreshRecorder) : undefined}
            onOpen={() => void ReplayService.OpenRecordingsDir()}
          />
          <ToggleRow
            label="Notify when recording starts and stops"
            info="A small corner message that never takes focus from your game."
            checked={settings.recordingNotices}
            disabled={busy}
            onChange={(v) => void update({ recordingNotices: v })}
          />
        </Collapsible>
      )}

      <Collapsible
        id="video"
        title="Video"
        summary={
          `${monitorLabel} · ${RESOLUTIONS.find((r) => r.value === settings.height)?.label.replace(" (recommended)", "") ?? "Native"}` +
          (session ? "" : ` · ${settings.fps} fps · ${mbps} Mbps`) +
          ` · ${codec.label}`
        }
      >
        {session ? (
          <p className="text-xs text-muted-foreground">
            Frame rate and bitrate for recording are under Recording quality above.
          </p>
        ) : null}
        <div className="grid grid-cols-2 gap-3">
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
          {!session ? (
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
          ) : null}
          <Field
            label={
              <>
                Resolution
                <InfoTip>
                  Native is captured and encoded entirely on your graphics card. Lower resolutions are
                  resized on the CPU (about one core). For smaller files, lower the bitrate instead.
                </InfoTip>
              </>
            }
          >
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
          <div className="flex items-end pb-1">
            <ToggleRow
              label="Show mouse cursor"
              checked={settings.includeCursor}
              disabled={busy}
              onChange={(v) => void update({ includeCursor: v })}
            />
          </div>
        </div>

        {!session ? (
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
              A {formatLength(settings.durationSec)} clip ≈ {formatMB(clipMB(mbps, settings.durationSec))}
            </span>
          </Field>
        ) : null}

        <Field
          label={
            <>
              Encoder
              <InfoTip>
                All three run on your graphics card's video chip, so none of them costs game
                performance. They differ in file size and where clips play.
              </InfoTip>
            </>
          }
        >
          <div role="radiogroup" aria-label="Encoder" className="grid grid-cols-3 border border-border">
            {CODECS.map((c, i) => {
              const noGPU = encoders !== null && encoders[c.value] === false;
              const noSession = c.value === "av1" && session;
              const selected = settings.codec === c.value;
              const why = noGPU
                ? "Your graphics card can't encode this."
                : noSession
                  ? "Not available for background or manual recording."
                  : `${c.plays}. ${c.size}.`;
              return (
                <Tooltip key={c.value} content={why} side="bottom" className="max-w-[18rem]">
                  <button
                    type="button"
                    role="radio"
                    aria-checked={selected}
                    disabled={busy || noGPU || noSession}
                    onClick={() => void update({ codec: c.value })}
                    className={cn(
                      "w-full px-2 py-2 text-xs font-medium transition-colors disabled:cursor-not-allowed disabled:opacity-40",
                      i > 0 && "border-l border-border",
                      selected ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-accent hover:text-foreground",
                    )}
                  >
                    {c.label}
                  </button>
                </Tooltip>
              );
            })}
          </div>
          <span className="text-xs text-muted-foreground">
            {codec.summary}
            {codec.tag ? ` ${codec.tag}.` : ""}
          </span>
        </Field>
      </Collapsible>

      <Collapsible
        id="audio"
        title="Audio"
        summary={`${audioParts.length ? audioParts.join(", ") : "No audio"} · ${settings.audioBitrateKbps} kbps${settings.separateTracks ? " · separate tracks" : ""}`}
      >
        <AudioSourceRow
          icon={<Volume2 />}
          label="Game & desktop"
          hint="Everything you hear except Discord"
          checked={settings.desktopAudio}
          volume={volumeDrafts.desktopVolume ?? settings.desktopVolume}
          disabled={busy}
          onToggle={(v) => void update({ desktopAudio: v })}
          onVolume={(v) => onVolume("desktopVolume", v)}
        />
        <AudioSourceRow
          icon={<Headphones />}
          label="Discord voice chat"
          hint={discordRunning ? "Your call and Discord's sounds" : "Picked up when Discord starts"}
          checked={settings.discordAudio}
          volume={volumeDrafts.discordVolume ?? settings.discordVolume}
          disabled={busy}
          onToggle={(v) => void update({ discordAudio: v })}
          onVolume={(v) => onVolume("discordVolume", v)}
        />
        <AudioSourceRow
          icon={<Mic />}
          label="Your microphone"
          hint={mics.length === 0 ? "No microphone found" : "Turn off to keep your voice out"}
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
        <div className="grid grid-cols-2 items-end gap-3">
          <ToggleRow
            label="Separate tracks"
            info="Also saves game, Discord and mic as their own tracks, so you can mute or level each one later in a video editor. Players still play the full mix."
            checked={settings.separateTracks}
            disabled={busy}
            onChange={(v) => void update({ separateTracks: v })}
          />
          <Field label="Quality">
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
        </div>
        <p className="text-xs text-muted-foreground">
          Audio changes restart recording, so what was buffered before is cleared.
        </p>
      </Collapsible>

      <Collapsible
        id="saving"
        title="Saving clips"
        summary={[
          saveDir.split(/[\\/]/).slice(-2).join("\\"),
          settings.gameFolders && "game folders",
          settings.soundOnSave && "sound",
          settings.toastOnSave && "message",
        ]
          .filter(Boolean)
          .join(" · ")}
      >
        <FolderRow
          label="Clips folder"
          path={saveDir}
          onChange={() => void pickDir()}
          onReset={settings.saveDir ? () => void resetDir() : undefined}
          onOpen={() => void ReplayService.OpenSaveDir()}
        />
        <ToggleRow
          label="Sort into game folders"
          info="Each clip goes into a folder named after the game you were playing (fullscreen games, and anything installed from Steam, Epic, Xbox, Riot, GOG, EA or Ubisoft). Everything else goes into Desktop."
          checked={settings.gameFolders}
          disabled={busy}
          onChange={(v) => void update({ gameFolders: v })}
        />
        <ToggleRow
          label="Play a sound when saved"
          checked={settings.soundOnSave}
          disabled={busy}
          onChange={(v) => void update({ soundOnSave: v })}
        />
        <ToggleRow
          label="Show a message when saved"
          info="A small note in the top-right corner that never takes focus from your game. Exclusive-fullscreen games hide it, so keep the sound on too."
          checked={settings.toastOnSave}
          disabled={busy}
          onChange={(v) => void update({ toastOnSave: v })}
        />
      </Collapsible>
    </div>
  );
}

// Collapsible is a settings section that shows a one-line summary of its
// current values while closed. Open/closed is remembered per viewer.
function Collapsible({
  id,
  title,
  summary,
  defaultOpen = false,
  children,
}: {
  id: string;
  title: string;
  summary?: string;
  defaultOpen?: boolean;
  children: React.ReactNode;
}) {
  const key = `toru.settings.open.${id}`;
  const [open, setOpen] = useState(() => {
    try {
      const v = window.localStorage.getItem(key);
      return v === null ? defaultOpen : v === "1";
    } catch {
      return defaultOpen;
    }
  });
  const toggle = () => {
    setOpen((o) => {
      try {
        window.localStorage.setItem(key, o ? "0" : "1");
      } catch {
        // per-viewer convenience only
      }
      return !o;
    });
  };
  return (
    <div className="frost flex flex-col">
      <button
        type="button"
        onClick={toggle}
        aria-expanded={open}
        className="flex items-center gap-2 px-4 py-3 text-left hover:bg-accent/40"
      >
        <ChevronRight className={cn("size-4 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")} />
        <span className="shrink-0 text-sm font-medium">{title}</span>
        {summary && !open ? (
          <span className="ml-auto min-w-0 truncate text-xs text-muted-foreground" title={summary}>
            {summary}
          </span>
        ) : null}
      </button>
      {open ? <div className="flex flex-col gap-4 border-t border-border px-4 pb-4 pt-3">{children}</div> : null}
    </div>
  );
}

// InfoTip is a small ⓘ that explains a setting on hover/focus, keeping the
// layout to one short line per option.
function InfoTip({ children }: { children: React.ReactNode }) {
  return (
    <Tooltip content={children} side="bottom" className="max-w-[18rem] text-left font-normal">
      <button
        type="button"
        tabIndex={0}
        aria-label="More info"
        className="inline-flex text-muted-foreground/70 hover:text-foreground"
        onClick={(e) => e.preventDefault()}
      >
        <Info className="size-3.5" />
      </button>
    </Tooltip>
  );
}

// Chips is a row of exclusive choice buttons.
function Chips<T extends number>({
  options,
  value,
  format,
  disabled,
  onPick,
}: {
  options: T[];
  value: T;
  format: (v: T) => string;
  disabled?: boolean;
  onPick: (v: T) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1">
      {options.map((o) => (
        <Button
          key={o}
          size="sm"
          variant={value === o ? "default" : "outline"}
          disabled={disabled}
          onClick={() => onPick(o)}
          className="h-7 px-2.5 text-xs"
        >
          {format(o)}
        </Button>
      ))}
    </div>
  );
}

// FolderRow shows a folder path with change / reset / open actions.
function FolderRow({
  label,
  path,
  onChange,
  onReset,
  onOpen,
}: {
  label: string;
  path: string;
  onChange: () => void;
  onReset?: () => void;
  onOpen: () => void;
}) {
  return (
    <div className="flex flex-col gap-1">
      <span className="text-xs text-muted-foreground">{label}</span>
      <div className="flex items-center gap-1">
        <div
          className="min-w-0 flex-1 truncate border border-border bg-background/40 px-2 py-1.5 font-mono text-[11px] text-muted-foreground"
          title={path || "—"}
        >
          {path || "—"}
        </div>
        <Button size="sm" variant="secondary" className="h-7" onClick={onChange}>
          Change…
        </Button>
        {onReset ? (
          <Button size="sm" variant="ghost" className="h-7" onClick={onReset} title="Back to the default folder">
            Reset
          </Button>
        ) : null}
        <Button size="sm" variant="ghost" className="h-7 px-2" onClick={onOpen} title="Open in Explorer">
          <FolderOpen />
        </Button>
      </div>
    </div>
  );
}

function PanelHeader() {
  return (
    <div className="flex items-center gap-2">
      <Clapperboard className="size-4" />
      <span className="text-sm font-medium">Game recording</span>
    </div>
  );
}

function comboText(sc: { win: boolean; ctrl: boolean; alt: boolean; shift: boolean; key: string }): string {
  return [sc.win && "Win", sc.ctrl && "Ctrl", sc.alt && "Alt", sc.shift && "Shift", sc.key].filter(Boolean).join("+");
}

function RecorderLine({
  recorder,
  status,
  busy,
  error,
  gamesOnly,
  recordKey,
}: {
  recorder: RecorderState | null;
  status: ReplayStatus | null;
  busy: boolean;
  error: string;
  gamesOnly: boolean;
  recordKey: string;
}) {
  if (busy) return <p className="text-xs text-muted-foreground">Applying…</p>;
  if (error) return <p className="text-xs text-destructive">{error}</p>;
  if (!recorder) return null;
  if (recorder.recording) {
    return (
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <span className="size-2 shrink-0 animate-pulse bg-red-500" aria-hidden="true" />
        <span>
          Recording {recorder.game ? recorder.game : "your screen"}
          {status?.encoder ? ` · ${status.encoder}${status.zeroCopy ? " (GPU zero-copy)" : ""}` : ""}
          {recorder.mode === "manual" && recordKey ? ` · press ${recordKey} to stop` : ""}
        </span>
      </div>
    );
  }
  if (recorder.mode === "manual") {
    return (
      <p className="text-xs text-muted-foreground">
        Ready. Press {recordKey || "the record hotkey"} or Start recording below.
      </p>
    );
  }
  return (
    <p className="text-xs text-muted-foreground">
      {gamesOnly ? "Waiting for a game. Recording starts automatically when one is in focus." : "Starting…"}
    </p>
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

function Field({ label, children }: { label: React.ReactNode; children: React.ReactNode }) {
  return (
    <div className="flex flex-col gap-1.5">
      <span className="flex items-center gap-1.5 text-xs text-muted-foreground">{label}</span>
      {children}
    </div>
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
        "flex flex-col gap-2 border px-3 py-2.5 transition-colors",
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
  info,
  checked,
  disabled,
  onChange,
}: {
  label: string;
  hint?: string;
  info?: React.ReactNode;
  checked: boolean;
  disabled?: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex w-full items-center justify-between gap-3">
      <div className="flex min-w-0 flex-col">
        <span className="flex items-center gap-1.5 text-sm">
          {label}
          {info ? <InfoTip>{info}</InfoTip> : null}
        </span>
        {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
      </div>
      <Switch checked={checked} disabled={disabled} onCheckedChange={onChange} aria-label={label} />
    </div>
  );
}
