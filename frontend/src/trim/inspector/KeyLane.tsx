import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/utils";
import { allKeys, moveKey, setKeyEase, type KeyRef } from "@/trim/fx/ops";
import type { Clip, Ease, Project } from "@/trim/project";

// KeyLane is the selected clip's keyframes as small diamonds on a thin lane
// along the bottom of the clip on the timeline (docs/video-editor-v3.md,
// Keyframes on the timeline). Click one to jump to it, drag to move it in
// time (Alt+drag copies it), right-click to set its ease.

const EASES: { id: Ease; label: string }[] = [
  { id: "linear", label: "Linear" },
  { id: "in", label: "Ease in" },
  { id: "out", label: "Ease out" },
  { id: "inOut", label: "Ease in-out" },
  { id: "hold", label: "Hold" },
];

let dragSeq = 0;

export function KeyLane({
  clip: c,
  left,
  width,
  pxPerSec,
  length,
  locked,
  apply,
  onSeek,
}: {
  clip: Clip;
  left: number;
  width: number;
  pxPerSec: number;
  length: number; // the clip's timeline length, s
  locked: boolean;
  apply: (fn: (p: Project) => Project, key?: string) => void;
  onSeek: (t: number) => void;
}) {
  const [menu, setMenu] = useState<{ x: number; y: number; ref: KeyRef; t: number; ease: Ease } | null>(null);
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!menu) return;
    const close = (e: PointerEvent) => !box.current?.contains(e.target as Node) && setMenu(null);
    window.addEventListener("pointerdown", close, true);
    return () => window.removeEventListener("pointerdown", close, true);
  }, [menu]);

  const rows = allKeys(c);
  if (!rows.length) return null;

  const startDrag = (e: React.PointerEvent, ref: KeyRef, t0: number) => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    const el = e.currentTarget as HTMLElement;
    el.setPointerCapture(e.pointerId);
    const x0 = e.clientX;
    const copy = e.altKey;
    const key = `keylane:${c.id}:${++dragSeq}`;
    let moved = false;
    let at = t0;
    let copied = false;
    const move = (ev: PointerEvent) => {
      if (!moved && Math.abs(ev.clientX - x0) < 3) return;
      moved = true;
      const to = Math.max(0, Math.min(length, Math.round((t0 + (ev.clientX - x0) / pxPerSec) * 1000) / 1000));
      // Each step moves the key from where the last step left it (a copy is
      // made once, then moved).
      const from = at;
      const dup = copy && at === t0 && !copied;
      if (from === to) return;
      apply((p) => moveKey(p, c.id, ref, from, to, dup), key);
      copied ||= dup;
      at = to;
    };
    const up = () => {
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
      if (!moved) onSeek(c.start + t0);
    };
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
  };

  return (
    <div
      ref={box}
      className="pointer-events-none absolute bottom-0.5 z-20 h-3 bg-black/55"
      style={{ left, width: Math.max(2, width) }}
      aria-label="Keyframes"
    >
      {rows.flatMap((r) =>
        r.keys.map((k) => (
          <button
            key={`${"anim" in r.ref ? r.ref.anim : `${r.ref.fx}.${r.ref.param}`}@${k.t}`}
            type="button"
            className={cn(
              "pointer-events-auto absolute top-1/2 size-2 -translate-x-1/2 -translate-y-1/2 rotate-45 border border-black/60 bg-primary hover:scale-125",
              k.ease === "hold" && "bg-amber-300",
              locked && "pointer-events-none",
            )}
            style={{ left: k.t * pxPerSec }}
            title={`${r.label} · ${k.t.toFixed(2)}s · ${EASES.find((x) => x.id === k.ease)?.label ?? k.ease}. Drag to move, Alt+drag to copy, right-click for the ease.`}
            onPointerDown={(e) => startDrag(e, r.ref, k.t)}
            onContextMenu={(e) => {
              e.preventDefault();
              e.stopPropagation();
              setMenu({ x: e.clientX, y: e.clientY, ref: r.ref, t: k.t, ease: k.ease });
            }}
          />
        )),
      )}
      {menu ? (
        <div className="pointer-events-auto fixed z-50 border border-border bg-popover py-1 text-xs text-popover-foreground shadow-lg" style={{ left: menu.x, top: menu.y }}>
          {EASES.map((x) => (
            <button
              key={x.id}
              type="button"
              className={cn("block w-full px-3 py-1.5 text-left hover:bg-accent", x.id === menu.ease && "text-primary")}
              onClick={() => {
                apply((p) => setKeyEase(p, c.id, menu.ref, menu.t, x.id));
                setMenu(null);
              }}
            >
              {x.label}
            </button>
          ))}
        </div>
      ) : null}
    </div>
  );
}
