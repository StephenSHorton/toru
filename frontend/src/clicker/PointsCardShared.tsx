import { useCallback, useEffect, useRef, useState } from "react";
import { Events as WailsEvents } from "@wailsio/runtime";
import { ClickerSettings, ClickerStatus, PickEvent } from "@/lib/api";
import { cn } from "@/lib/utils";

// Small pieces shared by the Points, Safety, Zones and Process list cards.
// NumField and the input/select class strings are local stand-ins: they can
// switch to the shared number-input / select primitives in components/ui once
// those land.

/** CardProps is what the Auto Clicker page hands every settings card. */
export interface CardProps {
  settings: ClickerSettings;
  status: ClickerStatus;
  update: (fn: (s: ClickerSettings) => ClickerSettings) => void;
}

/** eventData unwraps a Wails event payload (sometimes sent as a 1-element array). */
export function eventData<T>(data: unknown): T {
  return (Array.isArray(data) ? data[0] : data) as T;
}

/** errText turns a rejected binding call into display text, dropping the "clicker: CODE: " prefix. */
export function errText(e: unknown): string {
  const raw = e instanceof Error ? e.message : typeof e === "string" ? e : String(e ?? "");
  const m = /^clicker: [A-Z_]+: (.*)$/s.exec(raw.trim());
  return m ? m[1] : raw || "Something went wrong";
}

/**
 * usePickActive tracks whether a point or zone pick is running, from
 * clicker:pick. "added"/"deleted" keep it active (Shift+right keeps picking);
 * "done"/"cancelled" end it. There is no bound call to read the pick state, so
 * a pick started before the card mounted shows up on its next event.
 */
export function usePickActive(kind: "point" | "zone"): [PickEvent | null, (on: boolean) => void] {
  const [active, setActive] = useState<PickEvent | null>(null);
  useEffect(() => {
    return WailsEvents.On("clicker:pick", (ev) => {
      const p = PickEvent.createFrom(eventData(ev.data) ?? {});
      if (p.kind !== kind) {
        // The pickers are mutually exclusive: the other one starting ends ours.
        if (p.phase === "active") setActive(null);
        return;
      }
      if (p.phase === "done" || p.phase === "cancelled") setActive(null);
      else if (p.phase === "active") setActive(p);
      else setActive((cur) => cur ?? p);
    });
  }, [kind]);
  // A local setter so a button can show the pick at once (or roll it back
  // when the call rejects) without waiting for the event.
  const set = useCallback(
    (on: boolean) => setActive(on ? new PickEvent({ kind, phase: "active", index: -1 }) : null),
    [kind],
  );
  return [active, set];
}

export const inputClass =
  "h-7 w-full min-w-0 border border-border bg-transparent px-1.5 text-sm tabular-nums focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50";
export const selectClass =
  "frost h-7 w-full border border-border bg-transparent px-1.5 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50";
export const optionClass = "bg-background text-foreground";

/**
 * NumField is an integer box that keeps a local draft while typing and
 * commits (clamped) on blur or Enter; Esc reverts. It is a text input, so the
 * mouse wheel never changes it by accident.
 */
export function NumField({
  value,
  min,
  max,
  onCommit,
  label,
  suffix,
  disabled,
  className,
}: {
  value: number;
  min: number;
  max: number;
  onCommit: (v: number) => void;
  label: string;
  suffix?: string;
  disabled?: boolean;
  className?: string;
}) {
  const [draft, setDraft] = useState<string | null>(null);
  // Set by Esc so the blur it triggers (which runs before React re-renders,
  // with the old draft) drops the draft instead of committing it.
  const reverting = useRef(false);
  const shown = draft ?? String(value);

  const commit = () => {
    if (reverting.current) {
      reverting.current = false;
      setDraft(null);
      return;
    }
    if (draft === null) return;
    setDraft(null);
    const n = Math.trunc(Number(draft.trim()));
    if (draft.trim() === "" || !Number.isFinite(n)) return; // revert
    const v = Math.min(max, Math.max(min, n));
    if (v !== value) onCommit(v);
  };

  return (
    <label className={cn("flex min-w-0 items-center gap-1", className)}>
      <input
        type="text"
        inputMode="numeric"
        aria-label={label}
        title={label}
        disabled={disabled}
        value={shown}
        onChange={(e) => setDraft(e.target.value.replace(/[^\d-]/g, ""))}
        onBlur={commit}
        onKeyDown={(e) => {
          if (e.key === "Enter") e.currentTarget.blur();
          else if (e.key === "Escape") {
            reverting.current = true;
            setDraft(null);
            e.currentTarget.blur();
          }
        }}
        className={inputClass}
      />
      {suffix && <span className="shrink-0 text-xs text-muted-foreground">{suffix}</span>}
    </label>
  );
}

/** CardHeader is a card title with a caption and an optional switch on the right. */
export function CardHeader({
  title,
  caption,
  children,
}: {
  title: string;
  caption: string;
  children?: React.ReactNode;
}) {
  return (
    <div className="flex items-start justify-between gap-3">
      <div className="flex flex-col">
        <span className="text-sm font-medium">{title}</span>
        <span className="text-xs text-muted-foreground">{caption}</span>
      </div>
      {children}
    </div>
  );
}

/** SwitchRow is a label + caption with a control on the right. */
export function SwitchRow({
  label,
  caption,
  children,
}: {
  label: string;
  caption?: string;
  children: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between gap-3">
      <div className="flex min-w-0 flex-col">
        <span className="text-sm">{label}</span>
        {caption && <span className="text-xs text-muted-foreground">{caption}</span>}
      </div>
      {children}
    </div>
  );
}
