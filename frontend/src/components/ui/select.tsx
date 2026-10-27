import * as React from "react";
import { ChevronDown } from "lucide-react";
import { cn } from "@/lib/utils";

// Select — a native <select> dressed in Toru's sharp, dark style. Native keeps
// keyboard, screen reader and wheel behaviour for free; the chevron is drawn on
// top because `appearance-none` removes the system one.
export interface SelectOption<T extends string> {
  value: T;
  label: string;
  disabled?: boolean;
}

export interface SelectProps<T extends string>
  extends Omit<React.SelectHTMLAttributes<HTMLSelectElement>, "value" | "onChange"> {
  value: T;
  options: readonly SelectOption<T>[];
  onChange: (value: T) => void;
  boxClassName?: string;
}

export function Select<T extends string>({
  value,
  options,
  onChange,
  className,
  boxClassName,
  disabled,
  ...props
}: SelectProps<T>) {
  return (
    <div className={cn("relative inline-flex h-8", disabled && "opacity-50", boxClassName)}>
      <select
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value as T)}
        className={cn(
          "h-full w-full cursor-pointer appearance-none border border-border bg-background pl-2 pr-7 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:cursor-not-allowed",
          className,
        )}
        {...props}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value} disabled={o.disabled} className="bg-popover text-popover-foreground">
            {o.label}
          </option>
        ))}
      </select>
      <ChevronDown className="pointer-events-none absolute right-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
    </div>
  );
}
