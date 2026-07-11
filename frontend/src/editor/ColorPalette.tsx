// Color picker control — a single active-color swatch that opens a frosted
// popover with preset swatches + a native OS color picker. Collapses the old
// 8-swatch row so the toolbar stays compact (macOS Screenshot style).

import { useEffect, useRef, useState } from 'react';
import { Pipette } from 'lucide-react';
import { useEditorStore } from './store';
import { cn } from '@/lib/utils';

const SWATCHES = [
  '#ff3b30', '#ff9500', '#ffcc00', '#34c759',
  '#0a84ff', '#5ac8fa', '#bf5af2', '#ff2d55',
  '#ffffff', '#8e8e93', '#3a3a3c', '#000000',
];

export function ColorPalette() {
  const activeColor = useEditorStore((s) => s.activeColor);
  const setColor = useEditorStore((s) => s.setColor);
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const nativeRef = useRef<HTMLInputElement>(null);

  // Click-outside closes the menu.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: MouseEvent) => {
      if (!rootRef.current?.contains(e.target as Node)) setOpen(false);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') setOpen(false);
    };
    window.addEventListener('mousedown', onDown);
    window.addEventListener('keydown', onKey);
    return () => {
      window.removeEventListener('mousedown', onDown);
      window.removeEventListener('keydown', onKey);
    };
  }, [open]);

  return (
    <div ref={rootRef} className="relative">
      <button
        type="button"
        title={`Color ${activeColor}`}
        aria-label="Color"
        aria-expanded={open}
        onClick={() => setOpen((v) => !v)}
        className={cn(
          'inline-flex size-8 items-center justify-center border border-border',
          'transition-transform duration-100 active:scale-[0.97]',
          'hover:bg-accent',
        )}
      >
        <span
          className="size-5 border border-border/80"
          style={{ background: activeColor }}
        />
      </button>

      {open && (
        <div
          className="frost absolute bottom-full left-1/2 z-40 mb-2 w-48 -translate-x-1/2 p-2 shadow-lg"
          // Keep mousedown inside the panel from dismissing via click-outside mid-drag.
          onMouseDown={(e) => e.stopPropagation()}
        >
          <div className="mb-2 grid grid-cols-6 gap-1.5">
            {SWATCHES.map((c) => (
              <button
                key={c}
                type="button"
                title={c}
                onClick={() => {
                  setColor(c);
                  setOpen(false);
                }}
                className="size-6 border border-border/60 transition-transform active:scale-95"
                style={{
                  background: c,
                  outline: activeColor.toLowerCase() === c.toLowerCase()
                    ? '2px solid var(--color-ring)'
                    : 'none',
                  outlineOffset: '1px',
                }}
              />
            ))}
          </div>

          <button
            type="button"
            className="flex w-full items-center gap-2 border border-border px-2 py-1.5 text-xs hover:bg-accent"
            onClick={() => nativeRef.current?.click()}
          >
            <Pipette className="size-3.5 shrink-0" />
            Custom color…
            <span
              className="ml-auto size-4 border border-border"
              style={{ background: activeColor }}
            />
          </button>
          {/* Visually hidden native picker — full OS color dialog on Windows. */}
          <input
            ref={nativeRef}
            type="color"
            value={normalizeHex(activeColor)}
            onChange={(e) => setColor(e.target.value)}
            className="sr-only"
            tabIndex={-1}
            aria-hidden
          />
        </div>
      )}
    </div>
  );
}

/** Coerce any css color to a #rrggbb value the <input type=color> accepts. */
function normalizeHex(c: string): string {
  if (/^#[0-9a-fA-F]{6}$/.test(c)) return c;
  if (/^#[0-9a-fA-F]{3}$/.test(c)) {
    const r = c[1], g = c[2], b = c[3];
    return `#${r}${r}${g}${g}${b}${b}`;
  }
  return '#ff3b30';
}
