// Color palette — 8 fixed swatches. Active swatch ring = the design-system
// outline (var(--color-ring)), 2px solid. Clicking sets the active color (and
// recolors the selected node, per the store's setColor semantics).

import { useEditorStore } from './store';

const SWATCHES = [
  '#ff3b30', '#ff9500', '#ffcc00', '#34c759',
  '#0a84ff', '#bf5af2', '#ffffff', '#000000',
];

export function ColorPalette() {
  const activeColor = useEditorStore((s) => s.activeColor);
  const setColor = useEditorStore((s) => s.setColor);

  return (
    <div className="flex items-center gap-1">
      {SWATCHES.map((c) => (
        <button
          key={c}
          type="button"
          title={c}
          onClick={() => setColor(c)}
          className="size-6 border"
          style={{
            background: c,
            outline: activeColor === c ? '2px solid var(--color-ring)' : 'none',
            outlineOffset: activeColor === c ? '1px' : undefined,
          }}
        />
      ))}
    </div>
  );
}
