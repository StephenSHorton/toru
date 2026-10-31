import { useRef, useState, useSyncExternalStore } from "react";
import { ArrowLeftRight } from "lucide-react";
import { cn } from "@/lib/utils";
import type { Project, Track } from "@/trim/project";
import {
  DEFAULT_TRANSITION,
  isTransitionType,
  maxTransition,
  MIN_TRANSITION,
  setClipTransition,
  trackCuts,
  trackTransitions,
  TRANSITION_MIME,
  transitionDrag,
  transitionLabel,
} from "@/trim/transition";

// TransitionHandles draws one video track's transitions on the timeline
// (transition.ts): a box over each cut that has one, spanning its window,
// with edges to drag its length (both halves together; one undo step on
// release). Click selects it (the inspector edits it), Delete removes it.
// While a transition is dragged from the library, every cut that can take
// one shows a drop target. The clip edges at a cut stay trimmable: boxes sit
// in the lower half of the row.

export function TransitionHandles({
  track,
  x,
  pxPerSec,
  selectedId,
  onSelect,
  apply,
}: {
  track: Track;
  x: (t: number) => number;
  pxPerSec: number;
  selectedId: string | null; // the selected transition's incoming clip
  onSelect: (clipId: string) => void;
  apply: (fn: (p: Project) => Project, key?: string) => void;
}) {
  const dragging = useSyncExternalStore(transitionDrag.subscribe, transitionDrag.get);
  const [live, setLive] = useState<{ clipId: string; duration: number } | null>(null);
  const [over, setOver] = useState<string | null>(null);
  const drag = useRef<{ clipId: string; cut: number; max: number; duration: number } | null>(null);
  if (track.kind !== "video" || track.hidden) return null;
  const trans = trackTransitions(track);
  const cuts = trackCuts(track);

  const startEdge = (e: React.PointerEvent, clipId: string, cut: number, index: number) => {
    if (e.button !== 0 || track.locked) return;
    e.preventDefault();
    e.stopPropagation();
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { clipId, cut, max: maxTransition(track, index), duration: trans.get(index)?.duration ?? DEFAULT_TRANSITION };
    onSelect(clipId);
  };
  const moveEdge = (e: React.PointerEvent) => {
    const d = drag.current;
    if (!d || pxPerSec <= 0) return;
    const el = e.currentTarget.closest("[data-track-row]") as HTMLElement | null;
    const left = el ? el.getBoundingClientRect().left : 0;
    const t = (e.clientX - left) / pxPerSec;
    const duration = Math.round(Math.min(d.max, Math.max(MIN_TRANSITION, 2 * Math.abs(t - d.cut))) * 100) / 100;
    d.duration = duration;
    setLive({ clipId: d.clipId, duration });
  };
  const endEdge = () => {
    const d = drag.current;
    drag.current = null;
    setLive(null);
    if (!d) return;
    const c = track.clips.find((k) => k.id === d.clipId);
    if (c?.transition && Math.abs(c.transition.duration - d.duration) > 1e-3) {
      apply((p) => setClipTransition(p, d.clipId, { type: c.transition!.type, duration: d.duration }));
    }
  };

  const drop = (e: React.DragEvent, clipId: string, index: number) => {
    const type = e.dataTransfer.getData(TRANSITION_MIME);
    setOver(null);
    transitionDrag.set(false);
    if (!isTransitionType(type)) return;
    e.preventDefault();
    e.stopPropagation();
    const duration = Math.min(DEFAULT_TRANSITION, maxTransition(track, index));
    if (duration < MIN_TRANSITION) return;
    apply((p) => setClipTransition(p, clipId, { type, duration }));
    onSelect(clipId);
  };

  return (
    <>
      {cuts.map((cut) => {
        const tr = trans.get(cut.index);
        if (!tr) {
          if (!dragging || track.locked) return null;
          const room = maxTransition(track, cut.index) >= MIN_TRANSITION;
          return (
            <div
              key={`drop-${cut.clipId}`}
              aria-label="Drop a transition here"
              className={cn(
                "absolute inset-y-1 z-30 -ml-3 w-6 rounded-sm border-2 border-dashed",
                room ? "border-primary/70 bg-primary/10" : "border-muted-foreground/40",
                over === cut.clipId && room && "border-primary bg-primary/30",
              )}
              style={{ left: x(cut.t) }}
              onDragOver={(e) => {
                if (!e.dataTransfer.types.includes(TRANSITION_MIME) || !room) return;
                e.preventDefault();
                e.stopPropagation();
                e.dataTransfer.dropEffect = "copy";
                setOver(cut.clipId);
              }}
              onDragLeave={() => setOver((o) => (o === cut.clipId ? null : o))}
              onDrop={(e) => drop(e, cut.clipId, cut.index)}
              title={room ? "Drop to add the transition here" : "These clips are too short for a transition"}
            />
          );
        }
        const duration = live?.clipId === cut.clipId ? live.duration : tr.duration;
        const half = Math.floor(Math.round(duration * 1000) / 2) / 1000;
        const left = x(tr.cut - half);
        const width = Math.max(10, x(tr.cut - half + duration) - left);
        const selected = selectedId === cut.clipId;
        return (
          <div
            key={`tr-${cut.clipId}`}
            role="button"
            tabIndex={0}
            aria-label={`${transitionLabel(tr.type)} transition, ${duration.toFixed(2)} s`}
            title={`${transitionLabel(tr.type)} · ${duration.toFixed(2)} s`}
            className={cn(
              "group absolute bottom-1 z-30 flex h-[18px] items-center justify-center overflow-hidden rounded-sm border text-[10px] text-foreground shadow-sm outline-none",
              selected ? "border-primary bg-primary/80 text-primary-foreground" : "border-white/40 bg-background/80 hover:bg-background",
            )}
            style={{ left, width }}
            onPointerDown={(e) => {
              if (e.button !== 0) return;
              e.stopPropagation();
              onSelect(cut.clipId);
            }}
            onKeyDown={(e) => {
              if ((e.key === "Delete" || e.key === "Backspace") && !track.locked) {
                e.preventDefault();
                e.stopPropagation();
                apply((p) => setClipTransition(p, cut.clipId, undefined));
              }
            }}
            onDragOver={(e) => {
              if (!e.dataTransfer.types.includes(TRANSITION_MIME)) return;
              e.preventDefault();
              e.stopPropagation();
              e.dataTransfer.dropEffect = "copy";
            }}
            onDrop={(e) => {
              const type = e.dataTransfer.getData(TRANSITION_MIME);
              transitionDrag.set(false);
              if (!isTransitionType(type)) return;
              e.preventDefault();
              e.stopPropagation();
              apply((p) => setClipTransition(p, cut.clipId, { type, duration: tr.duration }));
              onSelect(cut.clipId);
            }}
          >
            <ArrowLeftRight className="size-3 shrink-0" />
            {width > 70 ? <span className="ml-1 truncate">{transitionLabel(tr.type)}</span> : null}
            {(["l", "r"] as const).map((side) => (
              <span
                key={side}
                aria-hidden
                className={cn("absolute inset-y-0 w-1.5 cursor-ew-resize bg-white/0 hover:bg-white/50", side === "l" ? "left-0" : "right-0", track.locked && "hidden")}
                onPointerDown={(e) => startEdge(e, cut.clipId, tr.cut, cut.index)}
                onPointerMove={moveEdge}
                onPointerUp={endEdge}
                onLostPointerCapture={endEdge}
              />
            ))}
          </div>
        );
      })}
    </>
  );
}
