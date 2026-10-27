import { AlertTriangle, MousePointerClick, Pause, Play, Square } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NumberInput } from "@/components/ui/number-input";
import { Progress } from "@/components/ui/progress";
import { Segmented } from "@/components/ui/segmented";
import { Switch } from "@/components/ui/switch";
import { ClickerService, ClickerStats, ClickerStatus } from "@/lib/api";
import { cn } from "@/lib/utils";
import { ShortcutRows } from "@/shortcuts/Shortcuts";
import { Card, CardHead, Note, Row } from "./cardParts";
import {
  HotkeyMode,
  LimitKind,
  PauseCause,
  RunState,
  formatCount,
  formatElapsed,
  formatSeconds,
} from "./cadence";
import { isActive, type CardProps, type Clicker } from "./useClicker";

// Run — the live state of the clicker and its controls: Start (after the
// countdown, so the click doesn't land on this button), Stop, Pause, the Armed
// switch that gates the hotkeys, the hotkey mode and the two clicker hotkeys.

const HOTKEY_MODES = [
  { value: HotkeyMode.HotkeyToggle, label: "Toggle", title: "Press to start, press again to stop" },
  { value: HotkeyMode.HotkeyHold, label: "Hold", title: "Clicks only while the hotkey is held" },
] as const;

const CLICKER_ACTIONS = ["clickerToggle", "clickerArm"] as const;

function chipFor(s: ClickerStatus): { label: string; className: string; live?: boolean } {
  switch (s.state) {
    case RunState.StateCountdown:
      return {
        label: `Starting in ${Math.max(1, Math.ceil(s.countdownMs / 1000))}`,
        className: "border-amber-400/60 bg-amber-400/10 text-amber-300",
      };
    case RunState.StateRunning:
      return { label: "Clicking", className: "border-primary/60 bg-primary/15 text-primary", live: true };
    case RunState.StatePaused:
      return {
        label: s.pauseCause === PauseCause.PauseZone ? "Paused in a zone" : "Paused",
        className: "border-sky-300/50 bg-sky-300/10 text-sky-200",
      };
    default:
      return s.armed
        ? { label: "Idle", className: "border-border bg-muted text-muted-foreground" }
        : { label: "Disarmed", className: "border-destructive/60 bg-destructive/10 text-destructive" };
  }
}

// PulseDot is the same "live" marker Send Files uses.
function PulseDot() {
  return (
    <span className="relative flex size-2.5 shrink-0" aria-hidden="true">
      <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-sky-400 opacity-60" />
      <span className="relative inline-flex size-2.5 rounded-full bg-sky-400" />
    </span>
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

export interface RunCardProps extends CardProps {
  stats: ClickerStats | null;
  apply: Clicker["apply"];
  run: Clicker["run"];
}

export default function RunCard({ settings, status, update, stats, apply, run }: RunCardProps) {
  const active = isActive(status);
  const running = status.state === RunState.StateRunning;
  const paused = status.state === RunState.StatePaused;
  const chip = chipFor(status);
  const limit = settings.profile.limit;
  const points = settings.profile.clickPoints;

  let progress: number | null = null;
  let clicksText = formatCount(status.clicks);
  let timeText = formatElapsed(status.elapsedMs);
  if (limit.kind === LimitKind.LimitClicks && limit.clicks > 0) {
    clicksText += ` / ${formatCount(limit.clicks)}`;
    if (active) progress = (status.clicks / limit.clicks) * 100;
  } else if (limit.kind === LimitKind.LimitTime && limit.seconds > 0) {
    timeText += ` / ${formatElapsed(limit.seconds * 1000)}`;
    if (active) progress = (status.elapsedMs / (limit.seconds * 1000)) * 100;
  }
  const showPoint = active && points.enabled && status.activePoint >= 0 && points.points.length > 0;

  const hotkeyHint = status.hotkey
    ? settings.hotkeyMode === HotkeyMode.HotkeyHold
      ? `Hold ${status.hotkey} anywhere to click.`
      : `Press ${status.hotkey} anywhere to start or stop.`
    : "No start/stop hotkey set.";

  return (
    <Card label="Run" wide>
      <CardHead icon={MousePointerClick} title="Run" caption={hotkeyHint}>
        <span
          className={cn("inline-flex items-center gap-1.5 border px-2 py-0.5 text-xs font-medium", chip.className)}
          role="status"
        >
          {chip.live ? <PulseDot /> : null}
          {chip.label}
        </span>
      </CardHead>

      <div className="flex flex-wrap items-end justify-between gap-4">
        <div className="flex flex-wrap gap-6">
          <Stat label="Clicks" value={clicksText} />
          <Stat label="Time" value={timeText} />
          {showPoint ? <Stat label="Point" value={`Pt ${status.activePoint + 1}/${points.points.length}`} /> : null}
        </div>
        <div className="flex items-center gap-2">
          {active ? (
            <>
              {running || paused ? (
                <Button
                  variant="secondary"
                  onClick={() => void run(() => ClickerService.SetPaused(running))}
                  aria-label={running ? "Pause" : "Resume"}
                >
                  {running ? <Pause /> : <Play />}
                  {running ? "Pause" : "Resume"}
                </Button>
              ) : null}
              <Button variant="destructive" onClick={() => void run(() => ClickerService.Stop())}>
                <Square /> Stop
              </Button>
            </>
          ) : (
            <Button onClick={() => void run(() => ClickerService.Start())}>
              <Play /> Start
            </Button>
          )}
        </div>
      </div>

      {progress !== null ? <Progress value={progress} /> : null}

      {!active && status.stopReason ? (
        <Note>
          Stopped: {status.stopReason}
          {status.clicks > 0 ? ` · ${formatCount(status.clicks)} clicks` : ""}
        </Note>
      ) : null}
      {status.warning ? (
        <p className="flex items-start gap-1.5 text-xs text-amber-400">
          <AlertTriangle className="mt-px size-3.5 shrink-0" />
          {status.warning}
        </p>
      ) : null}

      <div className="grid gap-4 border-t border-border pt-4 lg:grid-cols-2">
        <div className="flex flex-col gap-3">
          <Row label="Armed" caption="Off: the clicker hotkey does nothing and passes through to other apps.">
            <Switch
              aria-label="Armed"
              checked={settings.armed}
              onCheckedChange={(armed) => void apply(() => ClickerService.SetArmed(armed))}
            />
          </Row>
          <Row label="Hotkey mode" caption={settings.hotkeyMode === HotkeyMode.HotkeyHold ? "Clicks while held." : "Press to start, again to stop."}>
            <Segmented
              aria-label="Hotkey mode"
              value={settings.hotkeyMode === HotkeyMode.HotkeyHold ? HotkeyMode.HotkeyHold : HotkeyMode.HotkeyToggle}
              options={HOTKEY_MODES}
              onChange={(hotkeyMode) => update((s) => ({ ...s, hotkeyMode }))}
            />
          </Row>
          <Row label="Start delay" caption="Countdown after Start, so the first click doesn't land here.">
            <NumberInput
              aria-label="Start delay"
              value={settings.startDelayMs / 1000}
              min={0}
              max={10}
              decimals={1}
              suffix="s"
              boxClassName="w-20"
              onChange={(v) => update((s) => ({ ...s, startDelayMs: Math.round(v * 1000) }))}
            />
          </Row>
        </div>
        <ShortcutRows actions={CLICKER_ACTIONS} />
      </div>

      {stats ? (
        <p className="text-xs text-muted-foreground">
          All time: {formatCount(stats.clicks)} clicks over {formatSeconds(stats.seconds)} in {formatCount(stats.runs)}{" "}
          {stats.runs === 1 ? "run" : "runs"}.
        </p>
      ) : null}
    </Card>
  );
}
