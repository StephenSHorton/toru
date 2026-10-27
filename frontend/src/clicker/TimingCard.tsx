import { Timer } from "lucide-react";
import { NumberInput } from "@/components/ui/number-input";
import { Segmented } from "@/components/ui/segmented";
import { Switch } from "@/components/ui/switch";
import { Card, CardHead, Note, Row } from "./cardParts";
import { DOUBLE_CLICK_MAX_CPS, InputKind, PressMode, cpsOf, formatInterval, intervalOf } from "./cadence";
import type { CardProps } from "./useClicker";

// Timing — the shape of each click: how long the button stays down (Click), or
// holding it (Hold), plus double click and Gaussian jitter. The rules that tie
// these together (double click off at high rates or in Hold) live in Go's
// Normalize; the card only explains why a control is off.

const PRESSES = [
  { value: PressMode.PressClick, label: "Click" },
  { value: PressMode.PressHold, label: "Hold" },
] as const;

export default function TimingCard({ settings, update }: CardProps) {
  const p = settings.profile;
  const keyboard = p.input === InputKind.InputKeyboard;
  const hold = p.press === PressMode.PressHold;
  const cps = cpsOf(p.cadence);
  const interval = intervalOf(p.cadence);

  function setProfile(patch: Partial<typeof p>) {
    update((s) => ({ ...s, profile: { ...s.profile, ...patch } }));
  }

  const doubleReason = hold
    ? "Not available in Hold mode."
    : cps >= DOUBLE_CLICK_MAX_CPS
      ? `Needs a speed under ${DOUBLE_CLICK_MAX_CPS} clicks a second.`
      : "";

  return (
    <Card label="Timing">
      <CardHead
        icon={Timer}
        title="Press"
        caption={hold ? "Hold the button or key instead of clicking." : "Press and release once per click."}
      >
        <Segmented
          aria-label="Press mode"
          value={hold ? PressMode.PressHold : PressMode.PressClick}
          options={PRESSES}
          onChange={(press) => setProfile({ press })}
        />
      </CardHead>

      {!hold ? (
        <Row
          label="Held down for"
          caption={
            interval > 0
              ? `Of each click's ${formatInterval(interval)}: about ${formatInterval((interval * p.dutyPercent) / 100)}.`
              : "Share of each click the button stays down."
          }
        >
          <NumberInput
            aria-label="Held down percent"
            value={p.dutyPercent}
            min={0}
            max={100}
            decimals={1}
            suffix="%"
            boxClassName="w-24"
            onChange={(dutyPercent) => setProfile({ dutyPercent })}
          />
        </Row>
      ) : keyboard ? (
        <Note>
          Holds the key down and repeats it at Windows' key-repeat rate, like a held key. Speed is not used.
        </Note>
      ) : (
        <Row label="Release for" caption="Holds for one interval, releases, waits this long, and holds again.">
          <NumberInput
            aria-label="Release gap"
            value={p.holdGapMs}
            min={0}
            max={600_000}
            suffix="ms"
            boxClassName="w-28"
            onChange={(holdGapMs) => setProfile({ holdGapMs })}
          />
        </Row>
      )}

      <div className="flex flex-col gap-1.5 border-t border-border pt-3">
        <Row label={keyboard ? "Double press" : "Double click"} caption={doubleReason || "Each click becomes two."}>
          <Switch
            aria-label="Double click"
            checked={p.doubleClick && !doubleReason}
            disabled={!!doubleReason}
            title={doubleReason || undefined}
            onCheckedChange={(doubleClick) => setProfile({ doubleClick })}
          />
        </Row>
        {p.doubleClick && !doubleReason ? (
          <Row label="Gap between the two" caption="Capped by Windows' double-click time.">
            <NumberInput
              aria-label="Double click gap"
              value={p.doubleGapMs}
              min={1}
              max={1000}
              suffix="ms"
              boxClassName="w-24"
              onChange={(doubleGapMs) => setProfile({ doubleGapMs })}
            />
          </Row>
        ) : null}
      </div>

      <div className="flex flex-col gap-1.5 border-t border-border pt-3">
        <Row label="Randomize timing" caption="Varies each interval so the rhythm isn't machine-exact.">
          <Switch
            aria-label="Randomize timing"
            checked={p.jitter.enabled}
            disabled={hold && keyboard}
            onCheckedChange={(enabled) => setProfile({ jitter: { ...p.jitter, enabled } })}
          />
        </Row>
        {p.jitter.enabled && !(hold && keyboard) ? (
          <Row label="Spread" caption="Standard deviation, as a share of the interval.">
            <NumberInput
              aria-label="Jitter percent"
              value={p.jitter.percent}
              min={0}
              max={100}
              decimals={1}
              suffix="%"
              boxClassName="w-24"
              onChange={(percent) => setProfile({ jitter: { ...p.jitter, percent } })}
            />
          </Row>
        ) : null}
      </div>
    </Card>
  );
}
