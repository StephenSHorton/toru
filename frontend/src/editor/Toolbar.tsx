// Toolbar — a COMPACT FLOATING bar (macOS Screenshot style) pinned bottom-center
// OVER the canvas. Frosted (.frost), shadcn Buttons, lucide icons, sharp corners
// only (no rounded-*). Tool buttons are driven by a registry-aligned list
// (TOOL_BUTTONS), mirroring the TOOLS registry in tools/index.ts. Color + stroke
// controls bind to the store. Copy flattens the stage to the clipboard and
// flashes a green check + "Copied". Done (when provided) copies that same
// flatten when the Copy-on-Done pref is on, archives to the library, then
// the parent dismisses. Empty-selection Esc shares that same finishAndArchive
// path. A Settings gear opens the tray-driven Settings/home window.
//
// The bar is HTML OUTSIDE the Konva Stage, so Copy (which flattens the Stage)
// never bakes it into the exported PNG. It sits above CropOverlay/TextEditingOverlay
// (z-20) and is pointer-events-auto so its buttons stay clickable; it positions
// itself absolutely (bottom-4 left-1/2 -translate-x-1/2) with no full-window
// wrapper, so clicks elsewhere still reach the canvas underneath.
//
// Copy-on-Done (default ON, overlay.json): Done and empty-selection Esc both
// copy the annotated flatten (same pipeline as Copy) then archive to the
// library. New Capture does not go through this path and does not auto-copy.

import { useCallback, useEffect, useRef, useState } from 'react';
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
import { copyToClipboard, finishAndArchive } from './exportActions';
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
   * When provided, renders a "Done" button. The toolbar copies (if the
   * Copy-on-Done pref is on) and archives to the library; the parent then
   * dismisses (overlay Finish / window Close). Empty-selection Esc uses
   * finishAndArchive directly so it honors the same preference.
   */
  onDone?: () => void | Promise<void>;
  /**
   * Dock in document flow (standalone editor window) instead of floating
   * absolute bottom-center. Used so the parent can size the window around the bar.
   */
  docked?: boolean;
  /** Ref to the bar root — parent measures width/height for window chrome math. */
  barRef?: React.RefObject<HTMLDivElement | null>;
  /**
   * Flash the green Copied state on mount. Used only when a caller already
   * copied (legacy); capture no longer auto-copies before the editor opens.
   */
  flashCopied?: boolean;
}

export function Toolbar({ stageRef, onNewCapture, onDone, docked, barRef, flashCopied }: ToolbarProps) {
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
  const [copiedGen, setCopiedGen] = useState(0);
  const [doneBusy, setDoneBusy] = useState(false);
  const copiedTimer = useRef<number | null>(null);

  const showCopied = useCallback(() => {
    setCopied(true);
    setCopiedGen((n) => n + 1);
    if (copiedTimer.current != null) window.clearTimeout(copiedTimer.current);
    copiedTimer.current = window.setTimeout(() => {
      setCopied(false);
      copiedTimer.current = null;
    }, COPIED_FLASH_MS);
  }, []);

  useEffect(() => {
    if (flashCopied) showCopied();
    return () => {
      if (copiedTimer.current != null) window.clearTimeout(copiedTimer.current);
    };
  }, [flashCopied, showCopied]);

  async function handleCopy() {
    const stage = stageRef.current;
    if (!stage) return;
    try {
      await copyToClipboard(stage);
      showCopied();
    } catch {
      // Leave the button as "Copy" on failure — no toast surface here.
    }
  }
  async function handleDone() {
    if (!onDone || doneBusy) return;
    const stage = stageRef.current;
    if (!stage) return;
    setDoneBusy(true);
    try {
      await finishAndArchive(stage, {
        onCopied: async () => {
          showCopied();
          // Brief beat so the Copied flash is visible before the parent dismisses.
          await new Promise((r) => window.setTimeout(r, 400));
        },
      });
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
        className={cn("min-w-[5.75rem]", copied && "hover:bg-emerald-400/10")}
        onClick={() => void handleCopy()}
      >
        {copied ? (
          <span
            key={copiedGen}
            className="toru-copied-flash inline-flex items-center gap-2 text-emerald-400"
          >
            <Check />
            Copied
          </span>
        ) : (
          <>
            <Copy />
            Copy
          </>
        )}
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
          title="Done — copy (if enabled), save to library, and close"
          disabled={doneBusy}
          onClick={() => void handleDone()}
        >
          <Check /> {doneBusy ? 'Saving…' : 'Done'}
        </Button>
      )}
    </div>
  );
}
