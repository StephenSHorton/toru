import { useEffect, useRef, useState, type ReactNode } from "react";
import { ChevronLeft, ChevronRight, Diamond, MoreHorizontal, Pipette, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NumberInput } from "@/components/ui/number-input";
import { Segmented } from "@/components/ui/segmented";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import type { ParamDef } from "@/trim/fx/def";
import { keyAt } from "@/trim/fx/ops";
import type { Keyframe, ParamValue } from "@/trim/project";

// controls.tsx holds the inspector's building blocks (docs/video-editor-v3.md,
// Inspector UX): a section with a title and an action, a number row (slider,
// numeric entry, double-click to reset, keyframe toggle), the keyframe toggle
// itself, a control generated from an effect param's definition, and a small
// pop-up menu.

export function Section({ title, action, children, className }: { title: ReactNode; action?: ReactNode; children?: ReactNode; className?: string }) {
  return (
    <section className={cn("flex flex-col gap-1.5 border-b border-border/60 px-3 py-2.5", className)}>
      <div className="flex min-h-6 items-center justify-between gap-2">
        <h3 className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{title}</h3>
        {action}
      </div>
      {children}
    </section>
  );
}

export function ResetButton({ onClick, title = "Reset", label = "Reset" }: { onClick: () => void; title?: string; label?: string }) {
  return (
    <Button size="sm" variant="ghost" className="h-6 px-1.5 text-[11px]" onClick={onClick} title={title}>
      <RotateCcw className="size-3" /> {label}
    </Button>
  );
}

// KeyState is what a value's keyframe toggle shows: its keys and the
// clip-local time of the playhead.
export type KeyState = { keys: Keyframe[] | undefined; t: number; onToggle: () => void; onJump: (t: number) => void };

// KeyframeToggle is ◆ (filled when a key sits at the playhead, outlined
// when the value has keys elsewhere, faint when it has none), with ◀ ▶ to
// jump to the previous / next key on hover.
export function KeyframeToggle({ k, label }: { k: KeyState; label: string }) {
  const keyed = !!k.keys?.length;
  const here = !!keyAt(k.keys, k.t);
  const prev = keyed ? [...k.keys!].reverse().find((x) => x.t < k.t - 1e-3) : undefined;
  const next = keyed ? k.keys!.find((x) => x.t > k.t + 1e-3) : undefined;
  return (
    <div className="group/kf flex shrink-0 items-center">
      <button
        type="button"
        className={cn("grid size-4 place-items-center text-muted-foreground hover:text-foreground disabled:opacity-0", !keyed && "invisible")}
        disabled={!prev}
        onClick={() => prev && k.onJump(prev.t)}
        title="Previous keyframe"
        aria-label={`${label}: previous keyframe`}
      >
        <ChevronLeft className="size-3" />
      </button>
      <button
        type="button"
        className={cn(
          "grid size-5 place-items-center",
          here ? "text-primary" : keyed ? "text-primary/70 hover:text-primary" : "text-muted-foreground/40 hover:text-foreground",
        )}
        onClick={k.onToggle}
        title={here ? "Remove the keyframe at the playhead" : keyed ? "Add a keyframe at the playhead" : "Animate: add a keyframe at the playhead"}
        aria-label={`${label}: ${here ? "remove keyframe" : "add keyframe"}`}
        aria-pressed={here}
      >
        <Diamond className={cn("size-3", here && "fill-current")} />
      </button>
      <button
        type="button"
        className={cn("grid size-4 place-items-center text-muted-foreground hover:text-foreground disabled:opacity-0", !keyed && "invisible")}
        disabled={!next}
        onClick={() => next && k.onJump(next.t)}
        title="Next keyframe"
        aria-label={`${label}: next keyframe`}
      >
        <ChevronRight className="size-3" />
      </button>
    </div>
  );
}

// NumberRow is one number: a slider, a field you can type into, and its
// keyframe toggle. Double-click the label or slider to reset it. scale maps
// the stored value to what's shown (100 for percent).
export function NumberRow({
  label,
  value,
  min,
  max,
  step,
  def,
  scale = 1,
  unit,
  decimals,
  hint,
  disabled,
  keyframes,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  def?: number; // double-click resets to this
  scale?: number;
  unit?: string;
  decimals?: number;
  hint?: string;
  disabled?: boolean;
  keyframes?: KeyState;
  onChange: (v: number) => void;
}) {
  const dec = decimals ?? decimalsOf(step * scale);
  const reset = def === undefined ? undefined : () => onChange(def);
  return (
    <div className={cn("flex items-center gap-1.5", disabled && "opacity-50")} title={hint}>
      <span className="w-[4.5rem] shrink-0 truncate" onDoubleClick={reset}>
        {label}
      </span>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={Math.max(min, Math.min(max, value))}
        disabled={disabled}
        onChange={(e) => onChange(Number(e.target.value))}
        onDoubleClick={reset}
        className="min-w-0 flex-1 accent-primary"
        aria-label={label}
        title={def === undefined ? hint : `${hint ? `${hint}. ` : ""}Double-click to reset`}
      />
      <NumberInput
        value={round(value * scale, dec)}
        onChange={(v) => onChange(v / scale)}
        min={min * scale}
        max={max * scale}
        step={step * scale}
        decimals={dec}
        suffix={unit}
        disabled={disabled}
        boxClassName={cn("h-6 shrink-0", unit ? "w-[4.25rem]" : "w-14")}
        className="text-right text-[11px] tabular-nums"
        aria-label={`${label} value`}
      />
      {keyframes ? <KeyframeToggle k={keyframes} label={label} /> : null}
    </div>
  );
}

// ParamControl is the control an effect param's definition asks for: a
// number row, a switch, a colour swatch (with the player eyedropper for a
// key colour), a segmented choice, a point (two rows) or a file name.
export function ParamControl({
  p,
  value,
  keyframes,
  disabled,
  onChange,
  eyedropper,
}: {
  p: ParamDef;
  value: ParamValue;
  keyframes?: KeyState; // absent for params that can't be keyframed
  disabled?: boolean;
  onChange: (v: ParamValue) => void;
  eyedropper?: { picking: boolean; onPickingChange: (on: boolean) => void };
}) {
  const kf = p.animate === false ? undefined : keyframes;
  switch (p.type) {
    case "number": {
      const pct = p.display === "percent";
      return (
        <NumberRow
          label={p.label}
          value={value as number}
          min={p.min}
          max={p.max}
          step={p.step ?? (p.max - p.min) / 100}
          def={p.default}
          scale={pct ? 100 : 1}
          unit={pct ? "%" : p.unit}
          hint={p.hint}
          disabled={disabled}
          keyframes={kf}
          onChange={onChange}
        />
      );
    }
    case "boolean":
      return (
        <div className="flex items-center gap-1.5" title={p.hint}>
          <span className="flex-1" onDoubleClick={() => onChange(p.default)}>
            {p.label}
          </span>
          <Switch checked={value as boolean} onCheckedChange={onChange} disabled={disabled} aria-label={p.label} />
          {kf ? <KeyframeToggle k={kf} label={p.label} /> : null}
        </div>
      );
    case "color":
      return (
        <div className="flex items-center gap-1.5" title={p.hint}>
          <span className="w-[4.5rem] shrink-0 truncate" onDoubleClick={() => onChange(p.default)}>
            {p.label}
          </span>
          <label className="relative size-6 shrink-0 cursor-pointer border border-border" style={{ background: value as string }} title="Pick a colour">
            <input type="color" value={value as string} disabled={disabled} onChange={(e) => onChange(e.target.value)} className="absolute inset-0 cursor-pointer opacity-0" aria-label={p.label} />
          </label>
          <span className="font-mono text-[11px] text-muted-foreground">{value as string}</span>
          {eyedropper ? (
            <Button
              size="sm"
              variant={eyedropper.picking ? "default" : "ghost"}
              className="ml-auto h-6 px-1.5 text-[11px]"
              onClick={() => eyedropper.onPickingChange(!eyedropper.picking)}
              disabled={disabled}
              title={eyedropper.picking ? "Click the player to pick the colour (Esc cancels)" : "Pick the colour from the player"}
              aria-pressed={eyedropper.picking}
            >
              <Pipette className="size-3" /> {eyedropper.picking ? "Click the video…" : "Pick"}
            </Button>
          ) : (
            <span className="flex-1" />
          )}
          {kf ? <KeyframeToggle k={kf} label={p.label} /> : null}
        </div>
      );
    case "enum":
      return (
        <div className="flex flex-col gap-1" title={p.hint}>
          <span onDoubleClick={() => onChange(p.default)}>{p.label}</span>
          <Segmented
            size="xs"
            aria-label={p.label}
            value={value as string}
            options={p.options.map((o) => ({ value: o.id, label: o.label }))}
            onChange={onChange}
            disabled={disabled}
            className="flex-wrap"
          />
        </div>
      );
    case "point": {
      const [x, y] = value as [number, number];
      return (
        <>
          <NumberRow label={`${p.label} X`} value={x} min={0} max={1} step={0.005} def={p.default[0]} scale={100} unit="%" disabled={disabled} onChange={(v) => onChange([v, y])} />
          <NumberRow label={`${p.label} Y`} value={y} min={0} max={1} step={0.005} def={p.default[1]} scale={100} unit="%" disabled={disabled} keyframes={kf} onChange={(v) => onChange([x, v])} />
        </>
      );
    }
    case "file":
      return (
        <div className="flex items-center gap-1.5" title={p.hint}>
          <span className="w-[4.5rem] shrink-0 truncate">{p.label}</span>
          <span className="truncate text-muted-foreground" title={value as string}>
            {(value as string) ? baseName(value as string) : "None"}
          </span>
        </div>
      );
  }
}

// Menu is a ⋯ button with a small pop-up list of actions.
export function Menu({ items, label }: { items: { label: string; onClick: () => void; disabled?: boolean; danger?: boolean }[]; label: string }) {
  const [open, setOpen] = useState(false);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const close = (e: PointerEvent) => {
      if (!box.current?.contains(e.target as Node)) setOpen(false);
    };
    const esc = (e: KeyboardEvent) => e.key === "Escape" && setOpen(false);
    window.addEventListener("pointerdown", close, true);
    window.addEventListener("keydown", esc);
    return () => {
      window.removeEventListener("pointerdown", close, true);
      window.removeEventListener("keydown", esc);
    };
  }, [open]);
  return (
    <div ref={box} className="relative">
      <Button size="icon" variant="ghost" className="size-6 [&_svg]:size-3.5" onClick={() => setOpen((o) => !o)} title={label} aria-label={label} aria-expanded={open}>
        <MoreHorizontal />
      </Button>
      {open ? (
        <div role="menu" className="absolute right-0 top-7 z-30 flex min-w-40 flex-col border border-border bg-popover py-1 text-xs shadow-lg">
          {items.map((it) => (
            <button
              key={it.label}
              type="button"
              role="menuitem"
              disabled={it.disabled}
              className={cn("px-3 py-1.5 text-left hover:bg-accent disabled:pointer-events-none disabled:opacity-40", it.danger && "text-destructive")}
              onClick={() => {
                setOpen(false);
                it.onClick();
              }}
            >
              {it.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}

export const round = (n: number, d: number) => Math.round(n * 10 ** d) / 10 ** d;

function decimalsOf(step: number): number {
  if (!(step > 0)) return 2;
  if (step >= 1) return 0;
  return Math.min(3, Math.ceil(-Math.log10(step) - 1e-9));
}

export function baseName(p: string): string {
  return p.replace(/\\/g, "/").split("/").pop() ?? p;
}

export function fmtSec(s: number): string {
  if (s >= 60) {
    const m = Math.floor(s / 60);
    return `${m}m ${(s - m * 60).toFixed(2).padStart(5, "0")}s`;
  }
  return `${s.toFixed(2)}s`;
}
