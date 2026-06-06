// Stroke-width control — a .frost popover of preset chips (2/4/6/10px). Sharp
// corners (no rounded-* anywhere). Sets the active stroke width (and updates the
// selected node, per the store's setStrokeWidth semantics).

import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Minus } from 'lucide-react';
import { useEditorStore } from './store';

const WIDTHS = [2, 4, 6, 10];

export function StrokeWidthControl() {
  const [open, setOpen] = useState(false);
  const activeStrokeWidth = useEditorStore((s) => s.activeStrokeWidth);
  const setStrokeWidth = useEditorStore((s) => s.setStrokeWidth);

  return (
    <div className="relative">
      <Button
        size="sm"
        variant="ghost"
        title="Stroke width"
        onClick={() => setOpen((v) => !v)}
        className="gap-1"
      >
        <Minus />
        <span className="tabular-nums">{activeStrokeWidth}px</span>
      </Button>
      {open && (
        <div className="frost absolute bottom-full left-0 z-20 mb-1 flex flex-col gap-1 p-1.5">
          {WIDTHS.map((w) => (
            <button
              key={w}
              type="button"
              onClick={() => {
                setStrokeWidth(w);
                setOpen(false);
              }}
              className="flex items-center gap-2 px-2 py-1 text-xs hover:bg-accent"
              style={{ outline: activeStrokeWidth === w ? '2px solid var(--color-ring)' : 'none' }}
            >
              <span
                className="inline-block bg-foreground"
                style={{ width: 28, height: w }}
              />
              <span className="tabular-nums text-muted-foreground">{w}px</span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
