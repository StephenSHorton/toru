import { cn } from "@/lib/utils";

// Progress — a thin sharp-cornered bar. With `indeterminate` it runs a sweeping
// animation (for work without a measurable percentage, like the two-pass Discord
// re-encode); otherwise it fills to `value` (0–100). Keyframe `toruIndeterminate`
// lives in index.css.
export interface ProgressProps {
  value?: number;
  indeterminate?: boolean;
  className?: string;
}

export function Progress({ value = 0, indeterminate = false, className }: ProgressProps) {
  const pct = Math.max(0, Math.min(100, value));
  return (
    <div className={cn("relative h-1.5 w-full overflow-hidden bg-muted", className)}>
      {indeterminate ? (
        <div className="absolute inset-y-0 left-0 w-1/3 bg-primary [animation:toruIndeterminate_1.2s_ease-in-out_infinite]" />
      ) : (
        <div
          className="h-full bg-primary transition-[width] duration-300 ease-out"
          style={{ width: `${pct}%` }}
        />
      )}
    </div>
  );
}
