import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NumberInput } from "@/components/ui/number-input";
import { Select } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import { Section } from "@/trim/inspector/controls";
import type { Project } from "@/trim/project";
import { maxTransition, MIN_TRANSITION, setClipTransition, trackTransitions, TRANSITIONS, transitionLabel, type TransitionType } from "@/trim/transition";

// The inspector for a selected transition (selected by its incoming clip's
// id): its type and length, and Remove. Each change is one undo step.
export function TransitionInspector({
  project,
  clipId,
  apply,
  onClose,
  className,
}: {
  project: Project;
  clipId: string;
  apply: (fn: (p: Project) => Project, key?: string) => void;
  onClose: () => void;
  className?: string;
}) {
  const track = project.tracks.find((t) => t.clips.some((c) => c.id === clipId));
  const index = track ? track.clips.findIndex((c) => c.id === clipId) : -1;
  const tr = track ? trackTransitions(track).get(index) : undefined;
  const stored = track?.clips[index]?.transition;
  if (!track || !stored) {
    return (
      <aside className={cn("flex flex-col p-3 text-[11px] text-muted-foreground", className)}>
        This transition is gone. Select a clip or a transition on the timeline.
      </aside>
    );
  }
  const max = Math.max(MIN_TRANSITION, maxTransition(track, index));
  const set = (patch: Partial<{ type: TransitionType; duration: number }>, key?: string) =>
    apply((p) => setClipTransition(p, clipId, { type: stored.type, duration: stored.duration, ...patch }), key);
  return (
    <aside className={cn("flex min-h-0 flex-col overflow-y-auto", className)}>
      <Section
        title="Transition"
        action={
          <Button
            size="sm"
            variant="ghost"
            className="h-6 px-1.5 text-[11px] text-destructive"
            disabled={track.locked}
            onClick={() => {
              apply((p) => setClipTransition(p, clipId, undefined));
              onClose();
            }}
          >
            <Trash2 className="size-3" /> Remove
          </Button>
        }
      >
        <label className="flex items-center gap-2 text-[11px] text-muted-foreground">
          <span className="w-16 shrink-0">Type</span>
          <Select
            aria-label="Transition type"
            value={stored.type}
            options={TRANSITIONS.map((t) => ({ value: t.type, label: t.label }))}
            onChange={(type) => set({ type })}
            disabled={track.locked}
            boxClassName="h-7 min-w-0 flex-1"
            className="text-xs"
          />
        </label>
        <label className="flex items-center gap-2 text-[11px] text-muted-foreground">
          <span className="w-16 shrink-0">Length</span>
          <input
            type="range"
            aria-label="Transition length"
            min={MIN_TRANSITION}
            max={max}
            step={0.05}
            value={Math.min(stored.duration, max)}
            disabled={track.locked}
            onChange={(e) => set({ duration: Number(e.target.value) }, `transition-len:${clipId}`)}
            className="h-4 min-w-0 flex-1 accent-primary"
          />
          <NumberInput
            aria-label="Transition length in seconds"
            value={stored.duration}
            min={MIN_TRANSITION}
            max={max}
            step={0.05}
            decimals={2}
            suffix="s"
            boxClassName="w-20"
            className="h-7"
            disabled={track.locked}
            onChange={(duration) => set({ duration }, `transition-len:${clipId}`)}
          />
        </label>
        {!tr ? (
          <p className="text-[11px] text-destructive">
            This {transitionLabel(stored.type).toLowerCase()} isn't playing: the clips have moved apart or are too short for it.
          </p>
        ) : (
          <p className="text-[11px] text-muted-foreground">
            Centred on the cut: {(tr.cut - tr.start).toFixed(2)} s before it and {(tr.end - tr.cut).toFixed(2)} s after. Each clip holds its edge frame
            for the half it runs past its own end.
          </p>
        )}
      </Section>
    </aside>
  );
}
