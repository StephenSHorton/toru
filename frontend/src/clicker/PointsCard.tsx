import { useCallback, useEffect, useRef, useState } from "react";
import { Crosshair, GripVertical, MousePointerClick, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { ClickerService, ClickPoint, ClickerSettings } from "@/lib/api";
import { cn } from "@/lib/utils";
import {
  CardHeader,
  CardProps,
  NumField,
  SwitchRow,
  errText,
  usePickActive,
} from "./PointsCardShared";

// Click points: an ordered list of screen spots a run visits in turn. Points
// are added on screen by the picker (Go owns adding/deleting during a pick and
// sends the new list as clicker:settings); this card edits, reorders and
// removes them.

const INT32_MIN = -2147483648;
const INT32_MAX = 2147483647;

// The grid every row and the header share: index+handle, X, Y, clicks, r, delete.
const rowGrid = "grid grid-cols-[3rem_1fr_1fr_1fr_1fr_2rem] items-center gap-2";

function withPoints(s: ClickerSettings, fn: (pts: ClickPoint[]) => ClickPoint[]): ClickerSettings {
  const cp = s.profile.clickPoints;
  return {
    ...s,
    profile: { ...s.profile, clickPoints: { ...cp, points: fn(cp.points ?? []) } },
  };
}

function moveItem<T>(list: T[], from: number, to: number): T[] {
  const next = list.slice();
  const [item] = next.splice(from, 1);
  next.splice(to, 0, item);
  return next;
}

export default function PointsCard({ settings, status, update }: CardProps) {
  const cp = settings.profile.clickPoints;
  const points = cp.points ?? [];
  const [pick, setPick] = usePickActive("point");
  const picking = pick !== null;
  const [err, setErr] = useState("");
  const [confirmClear, setConfirmClear] = useState(false);

  // Drag reorder: while a handle is held the rows render in `order` (indexes
  // into points); the new order is saved once, on release.
  const [order, setOrderState] = useState<number[] | null>(null);
  const orderRef = useRef<number[] | null>(null);
  const setOrder = (fn: number[] | null | ((cur: number[] | null) => number[] | null)) => {
    const next = typeof fn === "function" ? fn(orderRef.current) : fn;
    orderRef.current = next;
    setOrderState(next);
  };
  const drag = useRef<{ from: number; pointerY: number; raf: number } | null>(null);
  const listRef = useRef<HTMLDivElement | null>(null);

  const startPick = useCallback(async () => {
    setErr("");
    setPick(true);
    try {
      await ClickerService.StartPointPick();
    } catch (e) {
      setPick(false);
      setErr(errText(e));
    }
  }, [setPick]);

  const cancelPick = useCallback(() => {
    setPick(false);
    void ClickerService.CancelPick().catch(() => {});
  }, [setPick]);

  // Esc in the hub cancels too, matching the picker's own Esc on the desktop.
  useEffect(() => {
    if (!picking) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") cancelPick();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [picking, cancelPick]);

  // Leaving the page ends a pick this card started.
  const pickingRef = useRef(false);
  pickingRef.current = picking;
  useEffect(
    () => () => {
      if (pickingRef.current) void ClickerService.CancelPick().catch(() => {});
    },
    [],
  );

  const setEnabled = (on: boolean) => {
    if (!on && picking) cancelPick();
    update((s) => ({
      ...s,
      profile: { ...s.profile, clickPoints: { ...s.profile.clickPoints, enabled: on } },
    }));
  };

  const setFlag = (key: "stopAfterPass" | "glide", on: boolean) =>
    update((s) => ({
      ...s,
      profile: { ...s.profile, clickPoints: { ...s.profile.clickPoints, [key]: on } },
    }));

  const editPoint = (id: string, patch: Partial<ClickPoint>) =>
    update((s) => withPoints(s, (pts) => pts.map((p) => (p.id === id ? { ...p, ...patch } : p))));

  const removePoint = (id: string) =>
    update((s) => withPoints(s, (pts) => pts.filter((p) => p.id !== id)));

  const clearAll = () => {
    setConfirmClear(false);
    update((s) => withPoints(s, () => []));
  };

  // --- drag reorder (pointer capture, not HTML5 DnD) ---
  // The capture goes on the list container, not the handle: rows re-render in
  // the new order mid-drag, and moving the dragged row's own node (a downward
  // drag) would drop a capture held by its handle.

  const onHandleDown = (e: React.PointerEvent<HTMLButtonElement>, index: number) => {
    if (e.button !== 0 || !listRef.current) return;
    e.preventDefault();
    listRef.current.setPointerCapture(e.pointerId);
    drag.current = { from: index, pointerY: e.clientY, raf: 0 };
    setOrder(points.map((_, i) => i));
  };

  const onDragMove = (e: React.PointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d) return;
    d.pointerY = e.clientY;
    if (d.raf) return;
    d.raf = requestAnimationFrame(() => {
      d.raf = 0;
      const list = listRef.current;
      if (!list || !drag.current) return;
      // Nudge the list when the pointer nears its top or bottom edge.
      const box = list.getBoundingClientRect();
      if (d.pointerY < box.top + 32) list.scrollTop -= 8;
      else if (d.pointerY > box.bottom - 32) list.scrollTop += 8;

      const rows = Array.from(list.querySelectorAll<HTMLElement>("[data-point-row]"));
      // rows is in DOM order, which is the current `order`.
      // Insertion slot: the first row (other than the dragged one) whose
      // vertical midpoint is below the pointer.
      setOrder((cur) => {
        if (!cur) return cur;
        const others = cur.filter((i) => i !== d.from);
        let slot = others.length;
        let seen = 0;
        for (const row of rows) {
          const idx = Number(row.dataset.pointRow);
          if (idx === d.from) continue;
          const r = row.getBoundingClientRect();
          if (d.pointerY < r.top + r.height / 2) {
            slot = seen;
            break;
          }
          seen++;
        }
        const next = others.slice();
        next.splice(slot, 0, d.from);
        return next.every((v, i) => v === cur[i]) ? cur : next;
      });
    });
  };

  const endDrag = (commit: boolean) => {
    const d = drag.current;
    drag.current = null;
    if (d?.raf) cancelAnimationFrame(d.raf);
    const final = orderRef.current;
    setOrder(null);
    if (!commit || !d || !final) return;
    const to = final.indexOf(d.from);
    if (to < 0 || to === d.from) return;
    const id = points[d.from]?.id;
    update((s) =>
      withPoints(s, (pts) => {
        // Re-find by id: the list may have changed while dragging.
        const from = pts.findIndex((p) => p.id === id);
        return from < 0 ? pts : moveItem(pts, from, Math.min(to, pts.length - 1));
      }),
    );
  };

  const rows = order ?? points.map((_, i) => i);
  const running = status.state === "running" || status.state === "paused";
  const dragging = drag.current?.from ?? -1;

  return (
    <section
      className="frost flex flex-col gap-4 border border-border p-4 lg:col-span-2"
      aria-label="Click points"
    >
      <CardHeader
        title="Click points"
        caption="Visit these spots in order and click each one. Every spot uses the speed and settings above."
      >
        <Switch checked={cp.enabled} onCheckedChange={setEnabled} aria-label="Use click points" />
      </CardHeader>

      <fieldset
        disabled={!cp.enabled}
        className={cn("flex min-w-0 flex-col gap-3", !cp.enabled && "opacity-50")}
      >
        <div className="flex flex-wrap items-center gap-2">
          {picking ? (
            <Button size="sm" variant="secondary" onClick={cancelPick}>
              <X /> Cancel picking
            </Button>
          ) : (
            <Button size="sm" variant={points.length ? "secondary" : "default"} onClick={() => void startPick()}>
              <Crosshair /> Pick on screen
            </Button>
          )}
          {picking && (
            <span className="text-xs text-muted-foreground">
              Right-click to add. Shift + right-click adds more, Ctrl + right-click deletes, Esc
              cancels.
            </span>
          )}
        </div>
        {err && <p className="text-xs text-destructive">{err}</p>}

        {points.length === 0 ? (
          <div className="flex flex-col items-center gap-2 border border-dashed border-border px-4 py-8 text-center">
            <MousePointerClick className="size-6 text-muted-foreground" />
            <p className="text-sm font-medium">No click points yet</p>
            <p className="text-xs text-muted-foreground">
              Press Pick on screen, then right-click anywhere to add a point.
            </p>
          </div>
        ) : (
          <div className="flex flex-col">
            <div className={cn(rowGrid, "px-1 pb-1 text-[11px] text-muted-foreground")}>
              <span>#</span>
              <span>X</span>
              <span>Y</span>
              <span>Clicks</span>
              <span>Radius</span>
              <span />
            </div>
            <div
              ref={listRef}
              className="flex max-h-80 flex-col overflow-y-auto"
              onPointerMove={onDragMove}
              onPointerUp={() => drag.current && endDrag(true)}
              onPointerCancel={() => drag.current && endDrag(false)}
              onLostPointerCapture={() => drag.current && endDrag(true)}
            >
              {rows.map((pi, pos) => {
                const p = points[pi];
                if (!p) return null;
                const active = running && cp.enabled && status.activePoint === pi;
                const progress = active
                  ? Math.min(1, Math.max(0, status.pointTick / Math.max(1, p.clicks)))
                  : 0;
                return (
                  <div
                    key={p.id || pi}
                    data-point-row={pi}
                    className={cn(
                      rowGrid,
                      "relative border-b border-border/60 px-1 py-1.5",
                      active && "bg-emerald-500/10",
                      dragging === pi && "bg-accent/60",
                    )}
                  >
                    <div className="flex items-center gap-0.5">
                      <button
                        type="button"
                        aria-label={`Drag point ${pos + 1} to reorder`}
                        title="Drag to reorder"
                        className="cursor-grab touch-none text-muted-foreground hover:text-foreground active:cursor-grabbing"
                        onPointerDown={(e) => onHandleDown(e, pi)}
                      >
                        <GripVertical className="size-4" />
                      </button>
                      <span className="text-xs tabular-nums text-muted-foreground">{pos + 1}</span>
                      {/* TODO(clicker): warn on points inside a corner/edge failsafe.
                          That needs the monitor rects, which the page has no bound
                          API for (ReplayService.ListMonitors has no origin); see
                          safety.PointInFailsafe on the Go side. */}
                    </div>
                    <NumField
                      label={`Point ${pos + 1} X`}
                      value={p.x}
                      min={INT32_MIN}
                      max={INT32_MAX}
                      onCommit={(x) => editPoint(p.id, { x })}
                    />
                    <NumField
                      label={`Point ${pos + 1} Y`}
                      value={p.y}
                      min={INT32_MIN}
                      max={INT32_MAX}
                      onCommit={(y) => editPoint(p.id, { y })}
                    />
                    <NumField
                      label={`Point ${pos + 1} clicks`}
                      value={p.clicks}
                      min={1}
                      max={999_999}
                      onCommit={(clicks) => editPoint(p.id, { clicks })}
                    />
                    <NumField
                      label={`Point ${pos + 1} radius`}
                      value={p.radius}
                      min={0}
                      max={9_999}
                      suffix="px"
                      onCommit={(radius) => editPoint(p.id, { radius })}
                    />
                    <Button
                      size="icon"
                      variant="ghost"
                      className="h-7 w-7"
                      aria-label={`Delete point ${pos + 1}`}
                      title="Delete"
                      onClick={() => removePoint(p.id)}
                    >
                      <Trash2 />
                    </Button>
                    {active && (
                      <span
                        aria-hidden="true"
                        className="absolute bottom-0 left-0 h-0.5 bg-emerald-400 transition-[width] duration-100"
                        style={{ width: `${progress * 100}%` }}
                      />
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        )}

        <SwitchRow
          label="Stop after one pass"
          caption="Stop after the last point instead of starting over from the first."
        >
          <Switch
            checked={cp.stopAfterPass}
            onCheckedChange={(on) => setFlag("stopAfterPass", on)}
            aria-label="Stop after one pass"
          />
        </SwitchRow>
        <SwitchRow
          label="Glide between points"
          caption="Move the cursor smoothly to the next point when the interval leaves time for it."
        >
          <Switch
            checked={cp.glide}
            onCheckedChange={(on) => setFlag("glide", on)}
            aria-label="Glide between points"
          />
        </SwitchRow>

        {points.length > 0 && (
          <div className="flex flex-wrap items-center gap-2">
            {confirmClear ? (
              <>
                <span className="text-xs text-muted-foreground">
                  Remove all {points.length} points?
                </span>
                <Button size="sm" variant="destructive" onClick={clearAll}>
                  Clear all
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setConfirmClear(false)}>
                  Keep them
                </Button>
              </>
            ) : (
              <Button size="sm" variant="ghost" onClick={() => setConfirmClear(true)}>
                <Trash2 /> Clear all
              </Button>
            )}
          </div>
        )}
      </fieldset>
    </section>
  );
}
