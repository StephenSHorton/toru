import * as React from "react";
import { cn } from "@/lib/utils";

// Segmented — a row of mutually exclusive buttons (a radio group), sharp and
// dark like the Settings tabs. Controlled: `value` + `onChange`. Options can
// carry an icon, a hover title and their own disabled flag.
export interface SegmentedOption<T extends string> {
  value: T;
  label: React.ReactNode;
  icon?: React.ComponentType<{ className?: string }>;
  title?: string;
  disabled?: boolean;
}

export interface SegmentedProps<T extends string> {
  value: T;
  options: readonly SegmentedOption<T>[];
  onChange: (value: T) => void;
  disabled?: boolean;
  size?: "sm" | "xs";
  className?: string;
  "aria-label"?: string;
}

export function Segmented<T extends string>({
  value,
  options,
  onChange,
  disabled,
  size = "sm",
  className,
  ...props
}: SegmentedProps<T>) {
  // Arrow keys move the selection like a native radio group.
  function onKeyDown(e: React.KeyboardEvent) {
    if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
    const usable = options.filter((o) => !o.disabled);
    const i = usable.findIndex((o) => o.value === value);
    if (i < 0 || usable.length === 0) return;
    e.preventDefault();
    const next = usable[(i + (e.key === "ArrowRight" ? 1 : usable.length - 1)) % usable.length];
    onChange(next.value);
  }

  return (
    <div
      role="radiogroup"
      aria-label={props["aria-label"]}
      onKeyDown={onKeyDown}
      className={cn("inline-flex shrink-0 border border-border bg-background p-0.5", disabled && "opacity-50", className)}
    >
      {options.map((o) => {
        const active = o.value === value;
        const Icon = o.icon;
        return (
          <button
            key={o.value}
            type="button"
            role="radio"
            aria-checked={active}
            tabIndex={active ? 0 : -1}
            title={o.title}
            disabled={disabled || o.disabled}
            onClick={() => !active && onChange(o.value)}
            className={cn(
              "inline-flex items-center justify-center gap-1.5 whitespace-nowrap font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50 [&_svg]:size-3.5 [&_svg]:shrink-0",
              size === "sm" ? "h-7 px-2.5 text-xs" : "h-6 px-2 text-[11px]",
              active ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-accent hover:text-foreground",
            )}
          >
            {Icon ? <Icon /> : null}
            {o.label}
          </button>
        );
      })}
    </div>
  );
}
