import { useState } from "react";
import { Snowflake } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NumberInput } from "@/components/ui/number-input";
import { Segmented } from "@/components/ui/segmented";
import { setSpeedCurve } from "@/trim/fx/ops";
import { presetCurve, RAMP_PRESETS } from "@/trim/speed";
import { clipLength, DEFAULT_FREEZE, freezeFrame, MAX_SPEED, MIN_SPEED, updateClip, type SpeedCurve } from "@/trim/project";
import { fmtSec, round, Section } from "@/trim/inspector/controls";
import type { TabProps } from "@/trim/inspector/Inspector";
import { SpeedCurveEditor } from "@/trim/inspector/SpeedCurveEditor";

// SpeedTab: a constant speed (log slider, typed value, quick picks) or a
// speed curve (ramp presets and the curve editor, with what the audio does
// during a ramp), and a freeze frame at the playhead.

const SPEEDS = [0.25, 0.5, 1, 1.5, 2, 4];

export function SpeedTab({ clip: c, track, media, t, edit }: TabProps) {
  const [freezeLen, setFreezeLen] = useState(DEFAULT_FREEZE);
  const srcLen = c.out - c.in;
  const mode = c.speedCurve ? "curve" : "constant";
  const setCurve = (curve: SpeedCurve | undefined, what = "curve") => edit((p) => setSpeedCurve(p, c.id, curve), what);
  const visual = track.kind === "video" && media?.kind !== "audio";

  if (c.hold) {
    return (
      <Section title="Freeze frame">
        <div className="text-[11px] text-muted-foreground">This clip holds one frame for {fmtSec(clipLength(c))}. Trim it on the timeline to change how long.</div>
      </Section>
    );
  }

  return (
    <>
      <Section title="Speed">
        <Segmented
          size="xs"
          aria-label="Speed mode"
          value={mode}
          options={[
            { value: "constant", label: "Constant" },
            { value: "curve", label: "Curve" },
          ]}
          onChange={(m) => setCurve(m === "curve" ? presetCurve("montage", srcLen) : undefined, "speed.mode")}
        />
        {mode === "constant" ? (
          <>
            <div className="flex items-center gap-1.5">
              <span className="w-[4.5rem] shrink-0" onDoubleClick={() => edit((p) => updateClip(p, c.id, { speed: 1 }), "speed")}>
                Speed
              </span>
              <input
                type="range"
                // A log scale so 0.25x..4x has 1x in the middle.
                min={Math.log2(MIN_SPEED)}
                max={Math.log2(MAX_SPEED)}
                step={0.01}
                value={Math.log2(c.speed)}
                onChange={(e) => edit((p) => updateClip(p, c.id, { speed: round(2 ** Number(e.target.value), 2) }), "speed")}
                onDoubleClick={() => edit((p) => updateClip(p, c.id, { speed: 1 }), "speed")}
                className="min-w-0 flex-1 accent-primary"
                aria-label="Speed"
                title="Double-click to reset"
              />
              <NumberInput
                value={c.speed}
                onChange={(v) => edit((p) => updateClip(p, c.id, { speed: v }), "speed")}
                min={MIN_SPEED}
                max={MAX_SPEED}
                step={0.05}
                decimals={2}
                suffix="×"
                boxClassName="h-6 w-[4.25rem] shrink-0"
                className="text-right text-[11px] tabular-nums"
                aria-label="Speed value"
              />
            </div>
            <div className="flex flex-wrap gap-1">
              {SPEEDS.map((s) => (
                <Button
                  key={s}
                  size="sm"
                  variant={Math.abs(c.speed - s) < 1e-3 ? "secondary" : "ghost"}
                  className="h-6 px-2 text-[11px] tabular-nums"
                  onClick={() => edit((p) => updateClip(p, c.id, { speed: s }), "speed")}
                >
                  {s}×
                </Button>
              ))}
            </div>
            <div className="text-[11px] text-muted-foreground tabular-nums">
              Duration {fmtSec(clipLength(c))}. Pitch is kept. A clip that would run into the next one speeds up just enough to fit.
            </div>
          </>
        ) : c.speedCurve ? (
          <>
            <div className="flex flex-wrap gap-1">
              {RAMP_PRESETS.map((x) => (
                <Button
                  key={x.id}
                  size="sm"
                  variant={c.speedCurve?.preset === x.id ? "secondary" : "ghost"}
                  className="h-6 px-2 text-[11px]"
                  onClick={() => setCurve(presetCurve(x.id, srcLen, c.speedCurve?.audio), "curve.preset")}
                >
                  {x.label}
                </Button>
              ))}
            </div>
            <SpeedCurveEditor curve={c.speedCurve} len={srcLen} onChange={(cv) => setCurve(cv)} />
            <div className="text-[11px] text-muted-foreground">Double-click to add a point, drag to move one, right-click to remove it.</div>
            <div className="flex items-center justify-between gap-2">
              <span>Audio during ramps</span>
              <Segmented
                size="xs"
                aria-label="Audio during ramps"
                value={c.speedCurve.audio}
                options={[
                  { value: "follow", label: "Follow", title: "Plays at the curve's speed (muted below 0.25× and above 4×)" },
                  { value: "mute", label: "Mute", title: "Silent wherever the speed isn't 1×" },
                ]}
                onChange={(a) => setCurve({ ...c.speedCurve!, audio: a }, "curve.audio")}
              />
            </div>
          </>
        ) : null}
      </Section>

      {visual ? (
        <Section title="Freeze frame at the playhead">
          <div className="flex items-center gap-1.5">
            <span className="flex-1">Length</span>
            <NumberInput
              value={freezeLen}
              onChange={setFreezeLen}
              min={0.1}
              max={10}
              step={0.1}
              decimals={1}
              suffix="s"
              boxClassName="h-6 w-16 shrink-0"
              className="text-right text-[11px] tabular-nums"
              aria-label="Freeze length"
            />
            <Button
              size="sm"
              variant="secondary"
              className="h-6 px-2 text-[11px]"
              onClick={() => edit((p) => freezeFrame(p, track.id, c.start + t, freezeLen), "freeze")}
              title="Split the clip at the playhead and insert a still of that frame (F)"
            >
              <Snowflake className="size-3" /> Add
            </Button>
          </div>
        </Section>
      ) : null}
    </>
  );
}
