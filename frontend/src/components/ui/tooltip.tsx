import * as React from "react";
import { cn } from "@/lib/utils";

// Tooltip — a hand-rolled hover/focus popover in Toru's dark, SHARP-cornered
// design (no Radix, matching Kbd/Switch). Wrap any focusable element; the label
// shows on hover OR keyboard focus (so it's not mouse-only). Defaults to sitting
// ABOVE the trigger, which suits bottom-anchored toolbars (the trim controls).
export interface TooltipProps {
  content: React.ReactNode;
  children: React.ReactNode;
  side?: "top" | "bottom";
  className?: string;
}

export function Tooltip({ content, children, side = "top", className }: TooltipProps) {
  const [open, setOpen] = React.useState(false);
  // Centred on the trigger by default; nudged sideways so it never runs off
  // the window edge (e.g. an info icon near the left of a panel).
  const tipRef = React.useRef<HTMLSpanElement>(null);
  const [shift, setShift] = React.useState(0);
  React.useLayoutEffect(() => {
    if (!open) {
      setShift(0);
      return;
    }
    const el = tipRef.current;
    if (!el) return;
    const r = el.getBoundingClientRect();
    const margin = 8;
    if (r.left < margin) setShift(margin - r.left);
    else if (r.right > window.innerWidth - margin) setShift(window.innerWidth - margin - r.right);
  }, [open]);
  return (
    <span
      className="relative inline-flex"
      onMouseEnter={() => setOpen(true)}
      onMouseLeave={() => setOpen(false)}
      onFocus={() => setOpen(true)}
      onBlur={() => setOpen(false)}
    >
      {children}
      {open ? (
        <span
          ref={tipRef}
          role="tooltip"
          style={shift ? { marginLeft: shift } : undefined}
          className={cn(
            "pointer-events-none absolute left-1/2 z-50 w-max max-w-[16rem] -translate-x-1/2 border border-border bg-popover px-2 py-1.5 text-xs leading-snug text-popover-foreground shadow-lg",
            side === "top" ? "bottom-full mb-1.5" : "top-full mt-1.5",
            className,
          )}
        >
          {content}
        </span>
      ) : null}
    </span>
  );
}
