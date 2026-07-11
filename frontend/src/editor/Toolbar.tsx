// Toolbar — a COMPACT FLOATING bar (macOS Screenshot style) pinned bottom-center
// OVER the canvas. Frosted (.frost), shadcn Buttons, lucide icons, sharp corners
// only (no rounded-*). Tool buttons are driven by a registry-aligned list
// (TOOL_BUTTONS), mirroring the TOOLS registry in tools/index.ts. Color + stroke
// controls bind to the store. Copy flattens the stage to the clipboard; Done
// (when provided) archives the annotated PNG to the Toru library then dismisses.
// A Settings gear opens the tray-driven Settings/home window.
//
// The bar is HTML OUTSIDE the Konva Stage, so Copy (which flattens the Stage)
// never bakes it into the exported PNG. It sits above CropOverlay/TextEditingOverlay
// (z-20) and is pointer-events-auto so its buttons stay clickable; it positions
// itself absolutely (bottom-4 left-1/2 -translate-x-1/2) with no full-window
// wrapper, so clicks elsewhere still reach the canvas underneath.

import { useState } from 'react';
import type Konva from 'konva';
import { Button } from '@/components/ui/button';
import {
  MousePointer2, Pen, Square, Circle, ArrowUpRight, Minus, Type, Crop,
  Undo2, Redo2, BringToFront, SendToBack, Trash2, Copy, Check,
  Settings as SettingsIcon, Camera,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { useEditorStore, BASE_IMAGE_ID } from './store';
import type { ToolId } from './types';
import { ColorPalette } from './ColorPalette';
import { StrokeWidthControl } from './StrokeWidthControl';
import { EmojiPicker } from './tools/emoji';
import { copyToClipboard } from './exportActions';
import { WindowsService } from '@/lib/api';
import { cn } from '@/lib/utils';

// Aligns with the TOOLS registry (tools/index.ts). Order mirrors macOS Markup.
const TOOL_BUTTONS: { id: ToolId; icon: LucideIcon; label: string }[] = [
  { id: 'select', icon: MousePointer2, label: 'Select (V)' },
  { id: 'pen', icon: Pen, label: 'Pen (P)' },
  { id: 'rect', icon: Square, label: 'Rectangle (R)' },
  { id: 'ellipse', icon: Circle, label: 'Ellipse (O)' },
  { id: 'arrow', icon: ArrowUpRight, label: 'Arrow (A)' },
  { id: 'line', icon: Minus, label: 'Line (L)' },
  { id: 'text', icon: Type, label: 'Text (T)' },
  // 'emoji' is rendered by <EmojiPicker/> (a frosted popover) below, not as a
  // generic TOOL_BUTTONS entry, so the user picks a glyph before stamping.
  { id: 'crop', icon: Crop, label: 'Crop (C)' },
];

const Divider = () => <div className="mx-1 h-6 w-px bg-border" />;

/** How long the Copy button stays on "Copied" after a successful copy. */
const COPIED_FLASH_MS = 1500;

export interface ToolbarProps {
  stageRef: React.RefObject<Konva.Stage | null>;
  /** When provided, renders a "New" button to start a fresh capture. */
  onNewCapture?: () => void;
  /**
   * When provided, renders a "Done" button. The parent is responsible for
   * saving the annotated PNG to the library then dismissing.
   */
  onDone?: () => void | Promise<void>;
  /**
   * Dock in document flow (standalone editor window) instead of floating
   * absolute bottom-center. Used so the parent can size the window around the bar.
   */
  docked?: boolean;
  /** Ref to the bar root — parent measures width/height for window chrome math. */
  barRef?: React.RefObject<HTMLDivElement | null>;
}

export function Toolbar({ stageRef, onNewCapture, onDone, docked, barRef }: ToolbarProps) {
  const activeTool = useEditorStore((s) => s.activeTool);
  const setTool = useEditorStore((s) => s.setTool);
  const selectedId = useEditorStore((s) => s.selectedId);
  const undo = useEditorStore((s) => s.undo);
  const redo = useEditorStore((s) => s.redo);
  const bringForward = useEditorStore((s) => s.bringForward);
  const sendBackward = useEditorStore((s) => s.sendBackward);
  const deleteSelected = useEditorStore((s) => s.deleteSelected);

  const hasSelection = !!selectedId && selectedId !== BASE_IMAGE_ID;
  const [copied, setCopied] = useState(false);
  const [doneBusy, setDoneBusy] = useState(false);

  async function handleCopy() {
    const stage = stageRef.current;
    if (!stage) return;
    try {
      await copyToClipboard(stage);
      setCopied(true);
      window.setTimeout(() => setCopied(false), COPIED_FLASH_MS);
    } catch {
      // Leave the button as "Copy" on failure — no toast surface here.
    }
  }
  async function handleDone() {
    if (!onDone || doneBusy) return;
    setDoneBusy(true);
    try {
      await onDone();
    } finally {
      setDoneBusy(false);
    }
  }

  return (
    <div
      ref={barRef}
      className={cn(
        "frost pointer-events-auto z-20 flex items-center gap-1 px-2 py-1.5 shadow-lg",
        docked
          ? "relative mx-auto shrink-0"
          : "absolute bottom-4 left-1/2 -translate-x-1/2",
      )}
    >
      {TOOL_BUTTONS.map((t) => (
        <Button
          key={t.id}
          size="icon"
          variant={activeTool === t.id ? 'default' : 'ghost'}
          title={t.label}
          onClick={() => setTool(t.id)}
        >
          <t.icon />
        </Button>
      ))}

      <EmojiPicker />

      <Divider />

      <Button size="icon" variant="ghost" title="Undo (Ctrl+Z)" onClick={() => undo()}>
        <Undo2 />
      </Button>
      <Button size="icon" variant="ghost" title="Redo (Ctrl+Shift+Z)" onClick={() => redo()}>
        <Redo2 />
      </Button>
      <Button
        size="icon"
        variant="ghost"
        title="Bring forward (Ctrl+])"
        disabled={!hasSelection}
        onClick={() => selectedId && bringForward(selectedId)}
      >
        <BringToFront />
      </Button>
      <Button
        size="icon"
        variant="ghost"
        title="Send backward (Ctrl+[)"
        disabled={!hasSelection}
        onClick={() => selectedId && sendBackward(selectedId)}
      >
        <SendToBack />
      </Button>
      <Button
        size="icon"
        variant="ghost"
        title="Delete (Del)"
        disabled={!hasSelection}
        onClick={() => deleteSelected()}
      >
        <Trash2 />
      </Button>

      <Divider />

      <ColorPalette />
      <StrokeWidthControl />

      <Divider />

      <Button
        size="icon"
        variant="ghost"
        title="Settings"
        onClick={() => void WindowsService.OpenSettings()}
      >
        <SettingsIcon />
      </Button>
      <Button
        size="sm"
        variant="ghost"
        title={copied ? 'Copied' : 'Copy to clipboard'}
        onClick={() => void handleCopy()}
      >
        {copied ? <Check /> : <Copy />}
        {copied ? 'Copied' : 'Copy'}
      </Button>

      {(onNewCapture || onDone) && <Divider />}
      {onNewCapture && (
        <Button size="sm" variant="ghost" title="New capture" onClick={onNewCapture}>
          <Camera /> New
        </Button>
      )}
      {onDone && (
        <Button
          size="sm"
          title="Done — save to library and close"
          disabled={doneBusy}
          onClick={() => void handleDone()}
        >
          <Check /> {doneBusy ? 'Saving…' : 'Done'}
        </Button>
      )}
    </div>
  );
}
