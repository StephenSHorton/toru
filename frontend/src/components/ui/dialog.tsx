import * as React from "react";
import { cn } from "@/lib/utils";

// Dialog — a minimal modal (backdrop + centred frosted panel) in Toru's design
// language (no Radix). `dismissable` gates backdrop-click + Escape so a long
// operation (e.g. the Discord re-encode) can't be closed out from under itself;
// flip it true once the work finishes and the panel shows a Done button.
export interface DialogProps {
  open: boolean;
  onClose?: () => void;
  dismissable?: boolean;
  className?: string;
  children: React.ReactNode;
}

export function Dialog({ open, onClose, dismissable = true, className, children }: DialogProps) {
  React.useEffect(() => {
    if (!open || !dismissable) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose?.();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, dismissable, onClose]);

  if (!open) return null;
  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center p-6">
      <div
        className="absolute inset-0 bg-black/60 backdrop-blur-sm"
        onClick={dismissable ? onClose : undefined}
      />
      <div className={cn("frost relative z-10 w-[24rem] max-w-[90vw] p-5", className)}>{children}</div>
    </div>
  );
}
