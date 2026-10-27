import { useEffect, useState } from "react";
import { BellRing } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { NotificationOptions, ReplayService, SettingsService } from "@/lib/api";
import { errText } from "@/transfer/useTransfer";

// Settings → Notifications: Toru's notification cards (internal/toast) and
// which features post them. Cards stack in a corner over every window and
// never take focus; the sounds are the s&box editor's.

const CORNERS = [
  { value: "top-right", label: "Top right" },
  { value: "bottom-right", label: "Bottom right" },
  { value: "top-left", label: "Top left" },
  { value: "bottom-left", label: "Bottom left" },
] as const;
type Corner = (typeof CORNERS)[number]["value"];

export function NotificationsPanel({ onOpenRecording }: { onOpenRecording: () => void }) {
  const [opts, setOpts] = useState<NotificationOptions | null>(null);
  // Volume in percent while the slider is dragged; saved on release.
  const [volume, setVolume] = useState<number | null>(null);
  const [recordingNotices, setRecordingNotices] = useState<boolean | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    void SettingsService.GetNotifications()
      .then(setOpts)
      .catch(() => setOpts(new NotificationOptions({ enabled: true, sound: true, volume: 0.6, corner: "top-right" })));
    void ReplayService.GetSettings()
      .then((s) => setRecordingNotices(s.recordingNotices))
      .catch(() => {});
  }, []);

  const apply = async (patch: Partial<NotificationOptions>) => {
    if (!opts) return;
    const prev = opts;
    setOpts(new NotificationOptions({ ...opts, ...patch }));
    setBusy(true);
    setError("");
    try {
      setOpts(await SettingsService.SetNotifications(new NotificationOptions({ ...prev, ...patch })));
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  const commitVolume = async () => {
    if (volume === null) return;
    await apply({ volume: volume / 100 });
    setVolume(null);
    void SettingsService.PreviewNotificationSound();
  };

  // The replay settings are re-read before the write so a change made
  // elsewhere meanwhile (the tray's Game recording item) isn't undone.
  const toggleRecordingNotices = async (next: boolean) => {
    setRecordingNotices(next);
    try {
      const s = await ReplayService.GetSettings();
      s.recordingNotices = next;
      // Settings are saved even when recording can't start; that error
      // belongs to the Game recording tab.
      await ReplayService.SetSettings(s).catch(() => {});
    } catch {
      setRecordingNotices(!next);
    }
  };

  if (!opts) return null;
  const shownVolume = volume ?? Math.round(opts.volume * 100);

  return (
    <div className="frost flex flex-col gap-4 p-5">
      <div className="flex items-center justify-between gap-3">
        <div className="flex flex-col">
          <span className="text-sm font-medium">Show notifications</span>
          <span className="text-xs text-muted-foreground">
            Small cards over every window that never take focus from your game
          </span>
        </div>
        <Switch
          checked={opts.enabled}
          disabled={busy}
          onCheckedChange={(v) => void apply({ enabled: v })}
          aria-label="Show notifications"
        />
      </div>

      <div className="flex items-center justify-between gap-3">
        <div className="flex flex-col">
          <span className="text-sm font-medium">Play sounds</span>
          <span className="text-xs text-muted-foreground">
            Exclusive-fullscreen games hide the cards, so the sound is what gets through. The clip save sound has
            its own switch under Game recording → Saving clips and plays at this volume either way
          </span>
        </div>
        <Switch
          checked={opts.sound}
          disabled={busy}
          onCheckedChange={(v) => void apply({ sound: v })}
          aria-label="Play notification sounds"
        />
      </div>

      <div className="flex items-center gap-3">
        <span className="w-14 shrink-0 text-xs text-muted-foreground">Volume</span>
        <input
          type="range"
          min={0}
          max={100}
          step={5}
          value={shownVolume}
          onChange={(e) => setVolume(Number(e.target.value))}
          onPointerUp={() => void commitVolume()}
          onKeyUp={() => void commitVolume()}
          className="w-full accent-primary disabled:opacity-50"
          aria-label="Notification volume"
        />
        <span className="w-11 shrink-0 text-right text-xs tabular-nums">
          {shownVolume === 0 ? "Muted" : `${shownVolume}%`}
        </span>
      </div>

      <div className="flex items-center justify-between gap-3">
        <div className="flex flex-col">
          <span className="text-sm font-medium">Position</span>
          <span className="text-xs text-muted-foreground">
            Which corner cards stack in, on the screen you're using
          </span>
        </div>
        <Select<Corner>
          value={(CORNERS.some((c) => c.value === opts.corner) ? opts.corner : "top-right") as Corner}
          options={CORNERS}
          disabled={busy}
          onChange={(v) => void apply({ corner: v })}
          boxClassName="w-36"
          aria-label="Notification position"
        />
      </div>

      <div>
        <Button
          size="sm"
          variant="secondary"
          disabled={!opts.enabled}
          onClick={() => void SettingsService.TestNotification()}
        >
          <BellRing /> Test notification
        </Button>
      </div>

      <div className="flex flex-col gap-3 border-t border-border pt-4">
        <div className="flex items-center justify-between gap-3">
          <div className="flex flex-col">
            <span className="text-sm font-medium">Game recording</span>
            <span className="text-xs text-muted-foreground">
              When instant replay, background recording or Hotkey recording starts (including at Windows startup),
              when a Game session is saved, and when you add a marker
            </span>
          </div>
          <Switch
            checked={recordingNotices ?? true}
            disabled={recordingNotices === null || !opts.enabled}
            onCheckedChange={(v) => void toggleRecordingNotices(v)}
            aria-label="Notify about game recording"
          />
        </div>
        <span className="text-xs text-muted-foreground">
          Clip saved messages and their sound are under{" "}
          <button type="button" className="text-foreground underline underline-offset-2" onClick={onOpenRecording}>
            Game recording → Saving clips
          </button>
          . The auto clicker's stop notice is under Auto Clicker → Safety.
        </span>
      </div>

      {error ? <span className="text-xs text-destructive">{error}</span> : null}
    </div>
  );
}
