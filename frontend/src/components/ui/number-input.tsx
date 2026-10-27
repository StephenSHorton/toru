import * as React from "react";
import { cn } from "@/lib/utils";

// NumberInput — a plain text field for numbers in Toru's sharp, dark style.
// While focused it keeps the typed text as a draft (so "1." or an empty field
// can sit there mid-edit) and commits each value that parses; on blur it clamps
// to min/max and snaps back to the committed value. Leading zeros are dropped as
// you type. With focus, the mouse wheel and the Up/Down arrows step by `step`:
// x5 with Shift, x25 with Ctrl, x100 with Ctrl+Shift.
export interface NumberInputProps
  extends Omit<React.InputHTMLAttributes<HTMLInputElement>, "value" | "onChange" | "min" | "max" | "step" | "type"> {
  value: number;
  onChange: (value: number) => void;
  min?: number;
  max?: number;
  step?: number;
  /** Digits after the decimal point; 0 (the default) means whole numbers only. */
  decimals?: number;
  /** Short unit shown inside the field on the right ("ms", "%", "px"). */
  suffix?: string;
  /** Class for the outer box (width etc.); className goes to the <input>. */
  boxClassName?: string;
}

function clamp(v: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, v));
}

function roundTo(v: number, decimals: number): number {
  const f = 10 ** decimals;
  return Math.round(v * f) / f;
}

function show(v: number, decimals: number): string {
  if (!Number.isFinite(v)) return "";
  return String(roundTo(v, decimals));
}

// stepMultiplier maps the held modifiers to the wheel/arrow step size.
function stepMultiplier(e: { shiftKey: boolean; ctrlKey: boolean }): number {
  if (e.ctrlKey && e.shiftKey) return 100;
  if (e.ctrlKey) return 25;
  if (e.shiftKey) return 5;
  return 1;
}

const NumberInput = React.forwardRef<HTMLInputElement, NumberInputProps>(
  (
    {
      value,
      onChange,
      min = -Infinity,
      max = Infinity,
      step = 1,
      decimals = 0,
      suffix,
      className,
      boxClassName,
      disabled,
      onFocus,
      onBlur,
      onKeyDown,
      ...props
    },
    ref,
  ) => {
    const [draft, setDraft] = React.useState<string | null>(null);
    const inner = React.useRef<HTMLInputElement | null>(null);
    React.useImperativeHandle(ref, () => inner.current as HTMLInputElement);

    // The wheel listener is registered once; it reads the latest props here.
    const live = React.useRef({ value, min, max, step, decimals, onChange, disabled });
    live.current = { value, min, max, step, decimals, onChange, disabled };

    const stepBy = React.useCallback((dir: number, mult: number) => {
      const p = live.current;
      if (p.disabled) return;
      const base = Number.isFinite(p.value) ? p.value : clamp(0, p.min, p.max);
      const next = clamp(roundTo(base + dir * p.step * mult, p.decimals), p.min, p.max);
      setDraft(show(next, p.decimals));
      if (next !== p.value) p.onChange(next);
    }, []);

    // React's onWheel is passive, so preventDefault (to stop the page scrolling
    // while stepping) needs a native listener.
    React.useEffect(() => {
      const el = inner.current;
      if (!el) return;
      const onWheel = (e: WheelEvent) => {
        if (document.activeElement !== el || e.deltaY === 0) return;
        e.preventDefault();
        stepBy(e.deltaY < 0 ? 1 : -1, stepMultiplier(e));
      };
      el.addEventListener("wheel", onWheel, { passive: false });
      return () => el.removeEventListener("wheel", onWheel);
    }, [stepBy]);

    function handleChange(text: string) {
      const pattern = decimals > 0 ? /[^0-9.,-]/g : /[^0-9-]/g;
      let t = text.replace(pattern, "").replace(",", ".");
      // Drop leading zeros ("007" -> "7") but keep "0" and "0.5".
      t = t.replace(/^(-?)0+(?=\d)/, "$1");
      if (min >= 0) t = t.replace(/-/g, "");
      setDraft(t);
      const n = Number(t);
      if (t === "" || t === "-" || t.endsWith(".") || !Number.isFinite(n)) return;
      const v = roundTo(n, decimals);
      // Above max commits max at once; below min waits for blur, so typing
      // "15" into a field with min 5 doesn't jump on the "1".
      if (v > max) onChange(max);
      else if (v >= min && v !== value) onChange(v);
    }

    function commit() {
      if (draft === null) return;
      const n = Number(draft.replace(",", "."));
      const v = Number.isFinite(n) && draft.trim() !== "" ? clamp(roundTo(n, decimals), min, max) : value;
      setDraft(null);
      if (v !== value) onChange(v);
    }

    return (
      <div
        className={cn(
          "flex h-8 items-center border border-border bg-background focus-within:ring-1 focus-within:ring-ring",
          disabled && "opacity-50",
          boxClassName,
        )}
      >
        <input
          ref={inner}
          type="text"
          inputMode={decimals > 0 ? "decimal" : "numeric"}
          autoComplete="off"
          spellCheck={false}
          disabled={disabled}
          value={draft ?? show(value, decimals)}
          onFocus={(e) => {
            setDraft(show(value, decimals));
            onFocus?.(e);
          }}
          onBlur={(e) => {
            commit();
            onBlur?.(e);
          }}
          onChange={(e) => handleChange(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "ArrowUp" || e.key === "ArrowDown") {
              e.preventDefault();
              stepBy(e.key === "ArrowUp" ? 1 : -1, stepMultiplier(e));
            } else if (e.key === "Enter") {
              e.currentTarget.blur(); // blur commits
            }
            onKeyDown?.(e);
          }}
          className={cn(
            "h-full w-full min-w-0 bg-transparent px-2 font-mono text-sm tabular-nums focus-visible:outline-none disabled:cursor-not-allowed",
            className,
          )}
          {...props}
        />
        {suffix ? <span className="shrink-0 pr-2 text-xs text-muted-foreground">{suffix}</span> : null}
      </div>
    );
  },
);
NumberInput.displayName = "NumberInput";

export { NumberInput };
