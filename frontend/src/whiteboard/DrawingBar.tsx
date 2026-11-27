// The drawing bar: a frosted bar at the bottom middle of the whiteboard
// window with the drawing tools (select, hand, pen, highlighter, eraser, and
// the shapes: rectangle, ellipse, line, arrow, text and emoji), the pen
// colours, the width (presets and a fine slider), fill for rectangles and
// ellipses, the emoji to put down, and undo and redo.
//
// Each tool has a single-key shortcut, shown in its tooltip (drawing.ts,
// `toolKeys`), and Escape goes back to select. The tool, colour, widths, fill
// and emoji are saved (`useDrawingSettings`), so the next board opens the
// same. With the select tool, a colour, width or fill picked here also
// changes the selected strokes and shapes.
//
// Render it inside <Board> (it reads the board with `useBoard`), and pass the
// same settings to the board's `drawing` prop.
import { useCallback, useEffect, useState } from "react";
import { Button } from "@ui-sdk/react/button";
import { Popover, PopoverContent, PopoverTitle, PopoverTrigger } from "@ui-sdk/react/popover";
import { Slider, SliderControl, SliderIndicator, SliderThumb, SliderTrack } from "@ui-sdk/react/slider";
import { Toggle, ToggleGroup } from "@ui-sdk/react/toggle";
import {
  ArrowUpRightIcon,
  CircleIcon,
  EraserIcon,
  HandIcon,
  HighlighterIcon,
  MinusIcon,
  MousePointer2Icon,
  PaintBucketIcon,
  PenLineIcon,
  Redo2Icon,
  SmileIcon,
  SquareIcon,
  TypeIcon,
  Undo2Icon,
  type LucideIcon,
} from "lucide-react";
import { EMOJI_SET } from "@/editor/emoji-set";
import { useBoard } from "./BoardView";
import type { BoardBlocks } from "./board";
import { loadSettings, saveSettings, toolForKey, toolKeys, toolNames, widthKind, widthPresets, widthRange, type DrawingSettings, type Tool } from "./drawing";
import { inkColor, inkColors } from "./ink";
import { restyle } from "./shapes";
import { typing } from "./window";
import "./drawing-bar.css";

const icons: Record<Tool, LucideIcon> = {
  select: MousePointer2Icon,
  hand: HandIcon,
  pen: PenLineIcon,
  highlighter: HighlighterIcon,
  eraser: EraserIcon,
  rect: SquareIcon,
  ellipse: CircleIcon,
  line: MinusIcon,
  arrow: ArrowUpRightIcon,
  text: TypeIcon,
  emoji: SmileIcon,
};

/** The bar's tools, in groups. */
const groups: Tool[][] = [
  ["select", "hand"],
  ["pen", "highlighter", "eraser"],
  ["rect", "ellipse", "line", "arrow", "text", "emoji"],
];

const tip = (tool: Tool) => `${toolNames[tool]} (${toolKeys[tool]})`;

/** The drawing settings, loaded from and saved to the window's storage. */
function useDrawingSettings() {
  const [settings, setSettings] = useState<DrawingSettings>(() => loadSettings());
  useEffect(() => saveSettings(settings), [settings]);
  const change = useCallback((c: Partial<DrawingSettings>) => setSettings((was) => ({ ...was, ...c })), []);
  return [settings, change] as const;
}

type DrawingBarProps = {
  settings: DrawingSettings;
  onChange: (change: Partial<DrawingSettings>) => void;
  onUndo: () => void;
  onRedo: () => void;
};

function DrawingBar({ settings, onChange, onUndo, onRedo }: DrawingBarProps) {
  const board = useBoard();
  const { tool, color, fill } = settings;
  const kind = widthKind(tool) ?? "pen";
  const width = settings.widths[kind];

  // With the select tool, a style picked here also restyles the selected ink.
  const restyleSelected = (change: { color?: string; width?: number; fill?: string | undefined }) => {
    if (tool !== "select") return;
    const blocks = board.blocks();
    const next: BoardBlocks = {};
    for (const id of board.selected()) {
      const b = blocks[id];
      if (!b?.ink) continue;
      const c = { ...change };
      // Fill is a rectangle's or ellipse's. A new colour recolours a fill
      // that's there, and leaves a hollow shape hollow.
      if ("fill" in c && b.ink.kind !== "rect" && b.ink.kind !== "ellipse") delete c.fill;
      if (c.color && !("fill" in c) && b.ink.fill) c.fill = c.color;
      if (!Object.keys(c).length) continue;
      next[id] = { ...b, ...restyle({ ...b, ink: b.ink }, c) };
    }
    if (Object.keys(next).length) board.replaceBlocks(next);
  };

  // Tool shortcuts: one key, no modifiers, not while typing.
  useEffect(() => {
    const keydown = (event: KeyboardEvent) => {
      if (event.ctrlKey || event.metaKey || event.altKey || event.repeat) return;
      const target = event.target as HTMLElement | null;
      // Not while typing, nor in a menu, dialog or list of options (the
      // board itself is a listbox of blocks: its keys are the bar's).
      if (typing() || target?.closest?.('input, textarea, select, [contenteditable=""], [contenteditable="true"], [role="dialog"], [role="menu"], [role="listbox"]:not([data-slot="board-world"])')) return;
      if (event.key === "Escape") {
        if (tool !== "select") onChange({ tool: "select" });
        return;
      }
      const next = toolForKey(event.key);
      if (next) {
        event.preventDefault();
        onChange({ tool: next });
      }
    };
    // In the capture phase: a focused block's typeahead stops the key from
    // reaching the window.
    window.addEventListener("keydown", keydown, true);
    return () => window.removeEventListener("keydown", keydown, true);
  }, [tool, onChange]);

  const setWidth = (value: number) => {
    onChange({ widths: { ...settings.widths, [kind]: value } });
    if (kind === "pen" || kind === "shape") restyleSelected({ width: value });
  };

  const range = widthRange[kind];
  const presets = widthPresets[kind];
  const widthLabel = kind === "eraser" ? "Eraser size" : tool === "text" ? "Text size" : tool === "emoji" ? "Emoji size" : "Width";

  return (
    <div
      data-slot="drawing-bar"
      role="toolbar"
      aria-label="Drawing tools"
      className="frost"
      onWheel={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
      onDoubleClick={(e) => e.stopPropagation()}
    >
      <ToggleGroup
        aria-label="Tool"
        deselectable={false}
        value={[tool]}
        onValueChange={(value) => value[0] && onChange({ tool: value[0] as Tool })}
        data-part="drawing-tools"
      >
        {groups.map((group, i) => (
          <div key={i} data-slot="drawing-group">
            {group.map((t) => {
              const Icon = icons[t];
              return (
                <Toggle key={t} value={t} aria-label={toolNames[t]} aria-keyshortcuts={toolKeys[t]} title={tip(t)} data-tool={t}>
                  <Icon aria-hidden="true" />
                </Toggle>
              );
            })}
          </div>
        ))}
      </ToggleGroup>

      <span data-slot="drawing-separator" />

      <ToggleGroup
        aria-label="Colour"
        deselectable={false}
        value={[color]}
        onValueChange={(value) => {
          const c = value[0];
          if (!c) return;
          onChange({ color: c });
          restyleSelected({ color: c });
        }}
        data-part="drawing-colours"
      >
        {inkColors.map((c) => (
          <Toggle key={c.token} value={c.token} aria-label={c.name} title={c.name} data-colour={c.token}>
            <span data-slot="drawing-swatch" style={{ background: inkColor(c.token) }} />
          </Toggle>
        ))}
      </ToggleGroup>

      <span data-slot="drawing-separator" />

      <Popover>
        <PopoverTrigger data-part="drawing-width-trigger" aria-label={`${widthLabel}: ${width}`} title={`${widthLabel}: ${width}`}>
          <span
            data-slot="drawing-width-preview"
            data-eraser={kind === "eraser" ? "" : undefined}
            style={{
              height: `${Math.min(18, Math.max(2, width / (kind === "highlighter" || kind === "eraser" ? 3 : 1)))}px`,
              background: kind === "eraser" ? undefined : inkColor(color),
              opacity: kind === "highlighter" ? 0.5 : undefined,
            }}
          />
        </PopoverTrigger>
        <PopoverContent className="frost" data-part="drawing-width">
          <PopoverTitle>{widthLabel}</PopoverTitle>
          <ToggleGroup
            aria-label={`${widthLabel} presets`}
            deselectable={false}
            value={presets.some((p) => p.value === width) ? [String(width)] : []}
            onValueChange={(value) => value[0] && setWidth(Number(value[0]))}
            data-part="drawing-width-presets"
          >
            {presets.map((p) => (
              <Toggle key={p.name} value={String(p.value)} title={`${p.name} (${p.value})`} data-width={p.value}>
                {p.name}
              </Toggle>
            ))}
          </ToggleGroup>
          <div data-slot="drawing-width-fine">
            <Slider value={width} min={range.min} max={range.max} step={range.step} onValueChange={(v) => setWidth(Array.isArray(v) ? v[0] : v)}>
              <SliderControl>
                <SliderTrack>
                  <SliderIndicator />
                  <SliderThumb aria-label={widthLabel} />
                </SliderTrack>
              </SliderControl>
            </Slider>
            <output data-slot="drawing-width-value">{width}</output>
          </div>
        </PopoverContent>
      </Popover>

      {(tool === "rect" || tool === "ellipse" || tool === "select") && (
        <Toggle
          data-part="drawing-fill"
          aria-label="Fill rectangles and ellipses"
          title="Fill rectangles and ellipses with the colour"
          pressed={fill}
          onPressedChange={(on: boolean) => {
            onChange({ fill: on });
            restyleSelected({ fill: on ? color : undefined });
          }}
        >
          <PaintBucketIcon aria-hidden="true" />
        </Toggle>
      )}

      {tool === "emoji" && (
        <Popover>
          <PopoverTrigger data-part="drawing-emoji-trigger" aria-label={`Emoji to put down: ${settings.emoji}`} title="Pick the emoji to put down">
            {settings.emoji}
          </PopoverTrigger>
          <PopoverContent className="frost" data-part="drawing-emoji">
            <PopoverTitle>Emoji</PopoverTitle>
            <div data-slot="drawing-emoji-grid">
              {EMOJI_SET.map((e) => (
                <Button key={e} aria-label={e} aria-pressed={e === settings.emoji} data-emoji={e} onClick={() => onChange({ emoji: e })}>
                  {e}
                </Button>
              ))}
            </div>
          </PopoverContent>
        </Popover>
      )}

      <span data-slot="drawing-separator" />

      <Button data-part="drawing-undo" aria-label="Undo" aria-keyshortcuts="Control+Z" title="Undo (Ctrl+Z)" onClick={onUndo}>
        <Undo2Icon aria-hidden="true" />
      </Button>
      <Button data-part="drawing-redo" aria-label="Redo" aria-keyshortcuts="Control+Y" title="Redo (Ctrl+Y)" onClick={onRedo}>
        <Redo2Icon aria-hidden="true" />
      </Button>
    </div>
  );
}

export { DrawingBar, useDrawingSettings, type DrawingBarProps };
