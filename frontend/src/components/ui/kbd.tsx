import * as React from "react";
import { cn } from "@/lib/utils";

// Kbd — a keyboard-key indicator (shadcn's <Kbd>, adapted to Toru's dark,
// SHARP-cornered design language: no rounded corners). Renders one key or
// modifier as a small key-cap. Wrap a combo in <KbdGroup> for consistent gaps.
function Kbd({ className, ...props }: React.ComponentProps<"kbd">) {
  return (
    <kbd
      data-slot="kbd"
      className={cn(
        "pointer-events-none inline-flex h-5 min-w-5 select-none items-center justify-center gap-1 border border-border bg-muted px-1.5 font-sans text-[0.7rem] font-medium text-muted-foreground [&_svg:not([class*='size-'])]:size-3",
        className,
      )}
      {...props}
    />
  );
}

function KbdGroup({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="kbd-group"
      className={cn("inline-flex items-center gap-1", className)}
      {...props}
    />
  );
}

export { Kbd, KbdGroup };
