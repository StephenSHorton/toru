import { useEffect, useRef, useState } from "react";
import { Check, Frame, Grid2x2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NumberInput } from "@/components/ui/number-input";
import { Segmented } from "@/components/ui/segmented";
import { Select } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import type { RenderedBox } from "@/trim/CropBox";
import { aspectLabel, CANVAS_FPS, CANVAS_PRESETS, MAX_CANVAS, MIN_CANVAS, normCanvas, presetOf, setCanvas, type Canvas, type Placement } from "@/trim/canvas";
import type { Project } from "@/trim/project";

// CanvasSettings is the viewer bar's project frame button: it shows the
// canvas (shape, size, rate) and opens a popover to pick a preset or a
// custom size and rate. When the shape changes it asks how to place the
// clips that fill the frame (fit, fill or keep); Apply is one undo step.

const PLACEMENTS: { value: Placement; label: string; title: string }[] = [
  { value: "fill", label: "Fill", title: "Scale each full-frame clip up to cover the new frame, cropping the edges" },
  { value: "fit", label: "Fit", title: "Fit each full-frame clip inside the new frame, with bars where the shape differs" },
  { value: "keep", label: "Keep", title: "Leave every clip's position and size as they are" },
];

export function CanvasSettings({
  project,
  apply,
  disabled,
}: {
  project: Project;
  apply: (fn: (p: Project) => Project, key?: string) => void;
  disabled?: boolean;
}) {
  const cur = project.canvas;
  const [open, setOpen] = useState(false);
  const [draft, setDraft] = useState<Canvas>(cur);
  const [place, setPlace] = useState<Placement>("fill");
  const root = useRef<HTMLDivElement>(null);

  // A fresh draft each time it opens.
  useEffect(() => {
    if (open) setDraft(cur);
  }, [open]); // eslint-disable-line react-hooks/exhaustive-deps -- only on open

  useEffect(() => {
    if (!open) return;
    const down = (e: PointerEvent) => {
      if (!root.current?.contains(e.target as Node)) setOpen(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key === "Escape") setOpen(false);
    };
    window.addEventListener("pointerdown", down, true);
    window.addEventListener("keydown", key);
    return () => {
      window.removeEventListener("pointerdown", down, true);
      window.removeEventListener("keydown", key);
    };
  }, [open]);

  const next = normCanvas(draft);
  const reshaped = Math.abs(next.width / next.height - cur.width / cur.height) > 1e-3;
  const changed = next.width !== cur.width || next.height !== cur.height || next.fps !== cur.fps;
  const preset = presetOf(next);

  const commit = () => {
    apply((p) => setCanvas(p, next, reshaped ? place : "keep"));
    setOpen(false);
  };

  return (
    <div ref={root} className="relative">
      <Button
        size="sm"
        variant={open ? "secondary" : "ghost"}
        className="h-7 px-2 text-[11px] font-normal text-muted-foreground"
        disabled={disabled}
        onClick={() => setOpen((o) => !o)}
        title="Project frame: shape, size and frame rate"
        aria-expanded={open}
        aria-haspopup="dialog"
      >
        <Frame /> {aspectLabel(cur)} · {cur.width}×{cur.height} · {Math.round(cur.fps * 100) / 100} fps
      </Button>
      {open ? (
        <div role="dialog" aria-label="Project frame" className="absolute bottom-full left-0 z-50 mb-2 w-[22rem] border border-border bg-popover p-3 text-popover-foreground shadow-xl">
          <div className="mb-2 text-xs font-medium">Project frame</div>
          <div className="grid grid-cols-4 gap-1.5">
            {CANVAS_PRESETS.map((p) => {
              const on = preset?.id === p.id;
              const k = 26 / Math.max(p.width, p.height);
              return (
                <button
                  key={p.id}
                  type="button"
                  className={cn("flex flex-col items-center gap-1 border px-1 py-2 text-[11px] hover:bg-accent", on ? "border-primary bg-primary/15" : "border-border")}
                  onClick={() => setDraft((d) => ({ ...d, width: p.width, height: p.height }))}
                  title={`${p.hint} (${p.width}×${p.height})`}
                  aria-pressed={on}
                >
                  <span className="flex h-7 items-center">
                    <span className={cn("block border", on ? "border-primary" : "border-foreground/60")} style={{ width: p.width * k, height: p.height * k }} />
                  </span>
                  <span className="font-medium">{p.label}</span>
                </button>
              );
            })}
          </div>
          <div className="mt-1 text-[11px] text-muted-foreground">{preset ? preset.hint : "Custom size"}</div>

          <div className="mt-3 flex items-center gap-1.5 text-[11px]">
            <span className="w-12 text-muted-foreground">Size</span>
            <NumberInput value={draft.width} onChange={(v) => setDraft((d) => ({ ...d, width: v }))} min={MIN_CANVAS} max={MAX_CANVAS} step={2} suffix="px" boxClassName="w-24" aria-label="Frame width" />
            <span className="text-muted-foreground">×</span>
            <NumberInput value={draft.height} onChange={(v) => setDraft((d) => ({ ...d, height: v }))} min={MIN_CANVAS} max={MAX_CANVAS} step={2} suffix="px" boxClassName="w-24" aria-label="Frame height" />
          </div>
          <div className="mt-2 flex items-center gap-1.5 text-[11px]">
            <span className="w-12 text-muted-foreground">Rate</span>
            <Select
              value={String(draft.fps)}
              options={[...new Set([...CANVAS_FPS, Math.round(cur.fps * 100) / 100])].sort((a, b) => a - b).map((f) => ({ value: String(f), label: `${f} fps` }))}
              onChange={(v) => setDraft((d) => ({ ...d, fps: Number(v) }))}
              boxClassName="w-28"
              aria-label="Frame rate"
            />
          </div>

          {reshaped ? (
            <div className="mt-3 border-t border-border pt-2">
              <div className="mb-1.5 text-[11px] text-muted-foreground">The shape changes. Place the clips that fill the frame:</div>
              <Segmented aria-label="Place clips" value={place} options={PLACEMENTS} onChange={setPlace} />
              <div className="mt-1 text-[11px] text-muted-foreground">{PLACEMENTS.find((p) => p.value === place)?.title}.</div>
            </div>
          ) : null}

          <div className="mt-3 flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={() => setOpen(false)}>
              Cancel
            </Button>
            <Button size="sm" onClick={commit} disabled={!changed}>
              <Check /> Apply
            </Button>
          </div>
        </div>
      ) : null}
    </div>
  );
}

// GuidesButton toggles the viewer's safe-area guides.
export function GuidesButton({ on, onChange }: { on: boolean; onChange: (on: boolean) => void }) {
  return (
    <Button size="sm" variant={on ? "secondary" : "ghost"} onClick={() => onChange(!on)} title="Show safe-area guides over the picture" aria-pressed={on}>
      <Grid2x2 /> Guides
    </Button>
  );
}

// SafeAreaGuides draws over the output frame (box, in the player pane):
// the 90% action-safe and 80% title-safe areas, centre lines, and on a tall
// vertical frame the bands phone apps usually cover with their own buttons
// and captions (top, bottom and the right edge).
export function SafeAreaGuides({ box, frameW, frameH }: { box: RenderedBox; frameW: number; frameH: number }) {
  if (box.width <= 0 || box.height <= 0) return null;
  const tall = frameH / frameW > 1.6;
  const inset = (f: number) => ({ left: `${f * 50}%`, top: `${f * 50}%`, right: `${f * 50}%`, bottom: `${f * 50}%` });
  return (
    <div className="pointer-events-none absolute z-20" style={{ left: box.left, top: box.top, width: box.width, height: box.height }} aria-hidden="true" data-safe-guides>
      <div className="absolute border border-dashed border-white/60" style={inset(0.1)} title="Action safe" />
      <div className="absolute border border-dotted border-yellow-300/70" style={inset(0.2)} title="Title safe" />
      <div className="absolute inset-y-0 left-1/2 w-px bg-white/25" />
      <div className="absolute inset-x-0 top-1/2 h-px bg-white/25" />
      {tall ? (
        <>
          <div className="absolute inset-x-0 top-0 bg-red-500/25" style={{ height: "10%" }} title="Often covered by app controls" />
          <div className="absolute inset-x-0 bottom-0 bg-red-500/25" style={{ height: "20%" }} title="Often covered by captions and controls" />
          <div className="absolute right-0 bg-red-500/25" style={{ top: "10%", bottom: "20%", width: "14%" }} title="Often covered by side buttons" />
        </>
      ) : null}
    </div>
  );
}
