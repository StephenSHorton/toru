import { useRef } from "react";
import { MAX_CURVE_SPEED, MIN_CURVE_SPEED, type SpeedCurve } from "@/trim/project";

// SpeedCurveEditor draws a clip's speed curve over its source time, with
// speed on a log scale (0.1× at the bottom, 1× in the middle, 10× at the
// top). Drag a point to move it (the first and last only up and down),
// double-click to add one, right-click a point to remove it.

const W = 248;
const H = 120;
const PAD = 6;
const LOG_MIN = Math.log10(MIN_CURVE_SPEED);
const LOG_MAX = Math.log10(MAX_CURVE_SPEED);

export function SpeedCurveEditor({ curve, len, onChange, disabled }: { curve: SpeedCurve; len: number; onChange: (c: SpeedCurve) => void; disabled?: boolean }) {
  const svg = useRef<SVGSVGElement>(null);
  const xOf = (s: number) => PAD + (len > 0 ? s / len : 0) * (W - 2 * PAD);
  const yOf = (v: number) => PAD + (1 - (Math.log10(v) - LOG_MIN) / (LOG_MAX - LOG_MIN)) * (H - 2 * PAD);
  const at = (e: { clientX: number; clientY: number }) => {
    const r = svg.current!.getBoundingClientRect();
    const fx = ((e.clientX - r.left) / r.width) * W;
    const fy = ((e.clientY - r.top) / r.height) * H;
    const s = Math.max(0, Math.min(len, ((fx - PAD) / (W - 2 * PAD)) * len));
    const lg = LOG_MIN + (1 - (fy - PAD) / (H - 2 * PAD)) * (LOG_MAX - LOG_MIN);
    // Snap to 1× near the middle line.
    const v = Math.abs(lg) < 0.04 ? 1 : 10 ** Math.max(LOG_MIN, Math.min(LOG_MAX, lg));
    return { s, v: Math.round(v * 100) / 100 };
  };
  const pts = curve.points;

  const startDrag = (i: number) => (e: React.PointerEvent) => {
    if (disabled || e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    (e.target as Element).setPointerCapture(e.pointerId);
    const end = i === 0 || i === pts.length - 1;
    const move = (ev: PointerEvent) => {
      const q = at(ev);
      const lo = i > 0 ? pts[i - 1].s + len * 0.01 : 0;
      const hi = i < pts.length - 1 ? pts[i + 1].s - len * 0.01 : len;
      const next = pts.slice();
      next[i] = { s: end ? pts[i].s : Math.max(lo, Math.min(hi, q.s)), v: q.v };
      onChange({ ...curve, points: next, preset: undefined });
    };
    const up = () => {
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };

  const add = (e: React.MouseEvent) => {
    if (disabled) return;
    const q = at(e);
    if (pts.some((p) => Math.abs(p.s - q.s) < len * 0.01)) return;
    onChange({ ...curve, points: [...pts, q].sort((a, b) => a.s - b.s), preset: undefined });
  };
  const remove = (i: number) => (e: React.MouseEvent) => {
    e.preventDefault();
    if (disabled || i === 0 || i === pts.length - 1) return;
    onChange({ ...curve, points: pts.filter((_, k) => k !== i), preset: undefined });
  };

  const path = pts.map((p, i) => `${i ? "L" : "M"}${xOf(p.s).toFixed(1)},${yOf(p.v).toFixed(1)}`).join(" ");
  return (
    <div className="flex gap-1">
      <div className="flex flex-col justify-between py-0.5 text-right text-[10px] tabular-nums text-muted-foreground">
        <span>10×</span>
        <span>1×</span>
        <span>0.1×</span>
      </div>
      <svg
        ref={svg}
        viewBox={`0 0 ${W} ${H}`}
        className="min-w-0 flex-1 touch-none select-none border border-border bg-background"
        onDoubleClick={add}
        role="img"
        aria-label="Speed curve. Double-click to add a point; drag points; right-click a point to remove it."
      >
        {[1, 2, 5, 0.5, 0.2].map((v) => (
          <line key={v} x1={0} x2={W} y1={yOf(v)} y2={yOf(v)} className="stroke-border" strokeWidth={0.5} strokeDasharray="2 3" />
        ))}
        <line x1={0} x2={W} y1={yOf(1)} y2={yOf(1)} className="stroke-muted-foreground/50" strokeWidth={0.75} />
        <path d={path} fill="none" className="stroke-primary" strokeWidth={1.75} />
        {pts.map((p, i) => (
          <g key={i}>
            <circle cx={xOf(p.s)} cy={yOf(p.v)} r={9} fill="transparent" className="cursor-grab" onPointerDown={startDrag(i)} onContextMenu={remove(i)} onDoubleClick={(e) => e.stopPropagation()}>
              <title>{`${p.v.toFixed(2)}× at ${p.s.toFixed(2)}s`}</title>
            </circle>
            <circle cx={xOf(p.s)} cy={yOf(p.v)} r={3.5} className="pointer-events-none fill-background stroke-primary" strokeWidth={1.5} />
          </g>
        ))}
      </svg>
    </div>
  );
}
