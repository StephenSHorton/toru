import { useState } from "react";
import { Flag } from "lucide-react";
import { NumberInput } from "@/components/ui/number-input";
import { Segmented } from "@/components/ui/segmented";
import { Select } from "@/components/ui/select";
import { Card, CardHead, Note } from "./cardParts";
import { LimitKind, formatSeconds } from "./cadence";
import type { CardProps } from "./useClicker";

// Limits — at most one automatic stop: after a number of clicks or an amount
// of time. Limit.Seconds is stored in seconds; the unit picker is display only.

const KINDS = [
  { value: LimitKind.LimitNone, label: "None" },
  { value: LimitKind.LimitClicks, label: "Clicks" },
  { value: LimitKind.LimitTime, label: "Time" },
] as const;

type TimeUnit = "s" | "m" | "h";
const TIME_UNIT_S: Record<TimeUnit, number> = { s: 1, m: 60, h: 3600 };
// MAX_LIMIT_S is Go's ceiling on Limit.Seconds (maxTimeLimitSec: 1,000 hours).
const MAX_LIMIT_S = 1000 * 3600;
const TIME_UNITS = [
  { value: "s" as TimeUnit, label: "seconds" },
  { value: "m" as TimeUnit, label: "minutes" },
  { value: "h" as TimeUnit, label: "hours" },
] as const;

// bestUnit picks the largest unit that shows the stored seconds exactly.
function bestUnit(sec: number): TimeUnit {
  if (sec >= 3600 && sec % 3600 === 0) return "h";
  if (sec >= 60 && sec % 60 === 0) return "m";
  return "s";
}

export default function LimitsCard({ settings, update }: CardProps) {
  const l = settings.profile.limit;
  const kind = l.kind === LimitKind.LimitClicks || l.kind === LimitKind.LimitTime ? l.kind : LimitKind.LimitNone;
  const [unit, setUnit] = useState<TimeUnit>(() => bestUnit(l.seconds));

  function setLimit(patch: Partial<typeof l>) {
    update((s) => ({ ...s, profile: { ...s.profile, limit: { ...s.profile.limit, ...patch } } }));
  }

  return (
    <Card label="Limits">
      <CardHead icon={Flag} title="Stop after" caption="End a run on its own after a set amount.">
        <Segmented aria-label="Limit" value={kind} options={KINDS} onChange={(k) => setLimit({ kind: k })} />
      </CardHead>

      {kind === LimitKind.LimitClicks ? (
        <div className="flex items-center gap-2">
          <NumberInput
            aria-label="Click limit"
            value={l.clicks}
            min={1}
            max={100_000_000}
            boxClassName="w-36"
            onChange={(clicks) => setLimit({ clicks })}
          />
          <span className="text-sm text-muted-foreground">clicks</span>
        </div>
      ) : kind === LimitKind.LimitTime ? (
        <div className="flex flex-col gap-1.5">
          <div className="flex items-center gap-2">
            <NumberInput
              aria-label="Time limit"
              value={l.seconds / TIME_UNIT_S[unit]}
              min={unit === "s" ? 1 : 0.1}
              max={MAX_LIMIT_S / TIME_UNIT_S[unit]}
              decimals={unit === "s" ? 0 : 2}
              boxClassName="w-28"
              onChange={(v) => setLimit({ seconds: Math.max(1, Math.round(v * TIME_UNIT_S[unit])) })}
            />
            <Select aria-label="Time unit" value={unit} options={TIME_UNITS} onChange={setUnit} boxClassName="w-28" />
          </div>
          <Note>Stops after {formatSeconds(l.seconds)} of clicking.</Note>
        </div>
      ) : (
        <Note>Runs until you stop it or a failsafe does.</Note>
      )}
    </Card>
  );
}
