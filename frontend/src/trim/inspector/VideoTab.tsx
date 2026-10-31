import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { placeClip, type ClipPlace } from "@/trim/canvas";
import { defOf, resolveParams } from "@/trim/fx/registry";
import { greenScreen, resetEffect, setGreenScreen } from "@/trim/fx/ops";
import { clipEffects } from "@/trim/fx/stack";
import type { AnimTarget, Project } from "@/trim/project";
import { NumberRow, ParamControl, ResetButton, Section } from "@/trim/inspector/controls";
import type { TabProps } from "@/trim/inspector/Inspector";

// VideoTab: the clip's place on the canvas (position, size, rotation,
// opacity, each keyframable) and the green screen quick toggle, which adds
// or removes the clip's chromaKey effect.

const TRANSFORM: { target: AnimTarget; label: string; min: number; max: number; step: number; scale: number; unit: string; def: number }[] = [
  { target: "x", label: "Position X", min: -0.5, max: 1.5, step: 0.005, scale: 100, unit: "%", def: 0.5 },
  { target: "y", label: "Position Y", min: -0.5, max: 1.5, step: 0.005, scale: 100, unit: "%", def: 0.5 },
  { target: "scale", label: "Size", min: 0.02, max: 4, step: 0.01, scale: 100, unit: "%", def: 1 },
  { target: "rotation", label: "Rotation", min: -180, max: 180, step: 0.5, scale: 1, unit: "°", def: 0 },
  { target: "opacity", label: "Opacity", min: 0, max: 1, step: 0.01, scale: 100, unit: "%", def: 1 },
];

const PLACES: { mode: ClipPlace; label: string; title: string }[] = [
  { mode: "fill", label: "Fill frame", title: "Cover the whole frame, centred, cropping what sticks out" },
  { mode: "fit", label: "Fit frame", title: "Show the whole picture inside the frame, centred" },
];

export function VideoTab(props: TabProps) {
  const { clip: c, edit, keys, set, value, picking, onPickingChange } = props;
  const fx = clipEffects(c);
  const key = greenScreen(fx);
  const keyDef = key && defOf(key.type);
  const keyParams = key && keyDef ? resolveParams(keyDef, key.params) : undefined;

  const place = (mode: ClipPlace) => edit((p: Project) => placeClip(p, c.id, mode), `place.${mode}`);

  return (
    <>
      <Section title="Transform" action={<ResetButton onClick={() => place("reset")} label="Reset" title="Back to the default: fitted, centred, upright, fully opaque (drops transform keyframes)" />}>
        <div className="flex gap-1" role="group" aria-label="Place on the frame">
          {PLACES.map((p) => (
            <Button key={p.mode} size="sm" variant="outline" className="h-7 flex-1 text-[11px]" onClick={() => place(p.mode)} title={p.title}>
              {p.label}
            </Button>
          ))}
        </div>
        {TRANSFORM.map((tr) => (
          <NumberRow
            key={tr.target}
            label={tr.label}
            value={value({ anim: tr.target }) as number}
            min={tr.min}
            max={tr.max}
            step={tr.step}
            def={tr.def}
            scale={tr.scale}
            unit={tr.unit}
            keyframes={keys({ anim: tr.target })}
            onChange={(v) => set({ anim: tr.target }, v)}
          />
        ))}
      </Section>

      <Section
        title="Green screen"
        action={<Switch checked={!!key} onCheckedChange={(on) => edit((p) => setGreenScreen(p, c.id, on), "green")} aria-label="Green screen" />}
      >
        {key && keyDef && keyParams ? (
          <>
            {keyDef.params.map((p) => (
              <ParamControl
                key={p.id}
                p={p}
                value={(value({ fx: key.id, param: p.id }) ?? keyParams[p.id]) as never}
                keyframes={keys({ fx: key.id, param: p.id })}
                onChange={(v) => set({ fx: key.id, param: p.id }, v)}
                eyedropper={p.type === "color" && onPickingChange ? { picking, onPickingChange } : undefined}
              />
            ))}
            <div className="flex justify-end">
              <ResetButton onClick={() => edit((p) => resetEffect(p, c.id, key.id), "green.reset")} title="Reset the green screen settings" />
            </div>
          </>
        ) : (
          <div className="text-[11px] text-muted-foreground">Turn on to make one colour transparent so the tracks below show through.</div>
        )}
      </Section>
    </>
  );
}
