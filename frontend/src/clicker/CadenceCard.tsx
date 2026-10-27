import { useState } from "react";
import { Gauge } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { NumberInput } from "@/components/ui/number-input";
import { Segmented } from "@/components/ui/segmented";
import { Select } from "@/components/ui/select";
import { Card, CardHead, Note } from "./cardParts";
import {
  CadenceMode,
  InputKind,
  PressMode,
  RATE_UNITS,
  RateUnit,
  UNIT_MS,
  UNIT_WORD,
  formatInterval,
  formatRate,
  intervalOf,
  joinMs,
  maxCps,
  minIntervalMs,
  rateForInterval,
  splitMs,
  type Hms,
} from "./cadence";
import type { CardProps } from "./useClicker";

// Cadence — how fast a run clicks, as a rate (N per second/minute/hour/day) or
// an interval (h/m/s/ms). The form being edited is stored with Mode; Go derives
// the other one on save, and the card previews it read-only meanwhile.

const MODES = [
  { value: CadenceMode.CadenceRate, label: "Rate" },
  { value: CadenceMode.CadenceInterval, label: "Interval" },
] as const;

const UNIT_OPTIONS = RATE_UNITS.map((u) => ({ value: u, label: `per ${UNIT_WORD[u]}` }));

export default function CadenceCard({ settings, update }: CardProps) {
  const p = settings.profile;
  const c = p.cadence;
  const [confirmHigh, setConfirmHigh] = useState(false);
  const keyHold = p.input === InputKind.InputKeyboard && p.press === PressMode.PressHold;
  const ceiling = maxCps(settings.highRate);
  const floorMs = minIntervalMs(settings.highRate);

  function setCadence(patch: Partial<typeof c>) {
    update((s) => ({ ...s, profile: { ...s.profile, cadence: { ...s.profile.cadence, ...patch } } }));
  }

  function switchMode(mode: CadenceMode) {
    if (mode === c.mode) return;
    if (mode === CadenceMode.CadenceInterval) {
      setCadence({ mode, intervalMs: Math.max(floorMs, Math.round(intervalOf(c) * 1000) / 1000) });
    } else {
      setCadence({ mode, ...rateForInterval(c.intervalMs, settings.highRate) });
    }
  }

  function setPart(k: keyof Hms, v: number) {
    const parts = { ...splitMs(c.intervalMs), [k]: v };
    setCadence({ intervalMs: Math.max(floorMs, joinMs(parts)) });
  }

  function setHighRate(on: boolean) {
    update((s) => ({ ...s, highRate: on }));
  }

  const parts = splitMs(c.intervalMs);
  const rateMax = (ceiling * UNIT_MS[c.unit]) / 1000;
  const preview = rateForInterval(c.intervalMs, settings.highRate);

  return (
    <Card label="Cadence">
      <CardHead icon={Gauge} title="Speed" caption="How often a run clicks or presses.">
        <Segmented
          aria-label="Speed as"
          value={c.mode === CadenceMode.CadenceInterval ? CadenceMode.CadenceInterval : CadenceMode.CadenceRate}
          options={MODES}
          onChange={switchMode}
          disabled={keyHold}
        />
      </CardHead>

      {keyHold ? (
        <Note>Speed isn't used while Hold keeps a key down: Windows' own key repeat sets the pace.</Note>
      ) : c.mode === CadenceMode.CadenceInterval ? (
        <div className="flex flex-col gap-2">
          <div className="grid grid-cols-4 gap-2">
            {(
              [
                ["h", "h", 999],
                ["m", "m", 9_999],
                ["s", "s", 99_999],
                ["ms", "ms", 999_999],
              ] as const
            ).map(([k, suffix, max]) => (
              <NumberInput
                key={k}
                aria-label={`Interval ${suffix}`}
                value={parts[k]}
                min={0}
                max={max}
                suffix={suffix}
                onChange={(v) => setPart(k, v)}
              />
            ))}
          </div>
          <Note>
            = {formatRate(preview.rate, preview.unit)} (every {formatInterval(c.intervalMs)})
          </Note>
        </div>
      ) : (
        <div className="flex flex-col gap-2">
          <div className="flex items-center gap-2">
            <NumberInput
              aria-label="Clicks"
              value={c.rate}
              min={0.01}
              max={rateMax}
              decimals={2}
              boxClassName="w-28"
              onChange={(rate) => setCadence({ rate })}
            />
            <span className="text-sm text-muted-foreground">clicks</span>
            <Select
              aria-label="Rate unit"
              value={c.unit || RateUnit.UnitSecond}
              options={UNIT_OPTIONS}
              onChange={(unit) => setCadence({ unit })}
              boxClassName="w-32"
            />
          </div>
          <Note>= one click every {formatInterval(intervalOf(c))}</Note>
        </div>
      )}

      <label className="flex items-start gap-2 border-t border-border pt-3 text-sm">
        <input
          type="checkbox"
          className="mt-0.5 size-4 shrink-0 accent-[var(--primary)]"
          checked={settings.highRate}
          onChange={(e) => (e.target.checked ? setConfirmHigh(true) : setHighRate(false))}
        />
        <span className="flex flex-col">
          <span className="font-medium">Allow up to 1,000 clicks a second</span>
          <span className="text-xs text-muted-foreground">
            The limit is {ceiling.toLocaleString()} a second ({formatInterval(floorMs)} at the shortest).
          </span>
        </span>
      </label>

      <Dialog open={confirmHigh} onClose={() => setConfirmHigh(false)}>
        <div className="flex flex-col gap-3">
          <span className="text-sm font-medium">Allow 1,000 clicks a second?</span>
          <p className="text-xs text-muted-foreground">
            Above 500 a second many apps and games drop clicks or stall, and a runaway clicker is harder to stop.
            Keep the failsafes on.
          </p>
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={() => setConfirmHigh(false)}>
              Cancel
            </Button>
            <Button
              size="sm"
              onClick={() => {
                setConfirmHigh(false);
                setHighRate(true);
              }}
            >
              Allow
            </Button>
          </div>
        </div>
      </Dialog>
    </Card>
  );
}
