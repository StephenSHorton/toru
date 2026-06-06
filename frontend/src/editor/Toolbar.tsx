// Toolbar — frosted (.frost), shadcn Buttons, lucide icons. Tool buttons are
// driven by a registry-aligned list (TOOL_BUTTONS), mirroring the TOOLS registry
// in tools/index.ts. Color + stroke controls bind to the store. Paste/Copy/Save
// call the clipboard hook + export actions. Sharp corners only (no rounded-*).

import type Konva from 'konva';
import { Button } from '@/components/ui/button';
import {
  MousePointer2, Pen, Square, Circle, ArrowUpRight, Minus, Type, Crop,
  Undo2, Redo2, BringToFront, SendToBack, Trash2, Copy, Save,
} from 'lucide-react';
import type { LucideIcon } from 'lucide-react';
import { useEditorStore, BASE_IMAGE_ID } from './store';
import type { ToolId } from './types';
import { ColorPalette } from './ColorPalette';
import { StrokeWidthControl } from './StrokeWidthControl';
import { EmojiPicker } from './tools/emoji';
import { copyToClipboard, saveAs } from './exportActions';

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

export interface ToolbarProps {
  stageRef: React.RefObject<Konva.Stage | null>;
}

export function Toolbar({ stageRef }: ToolbarProps) {
  const activeTool = useEditorStore((s) => s.activeTool);
  const setTool = useEditorStore((s) => s.setTool);
  const selectedId = useEditorStore((s) => s.selectedId);
  const undo = useEditorStore((s) => s.undo);
  const redo = useEditorStore((s) => s.redo);
  const bringForward = useEditorStore((s) => s.bringForward);
  const sendBackward = useEditorStore((s) => s.sendBackward);
  const deleteSelected = useEditorStore((s) => s.deleteSelected);

  const hasSelection = !!selectedId && selectedId !== BASE_IMAGE_ID;

  async function handleCopy() {
    const stage = stageRef.current;
    if (stage) await copyToClipboard(stage);
  }
  async function handleSave() {
    const stage = stageRef.current;
    if (stage) await saveAs(stage);
  }

  return (
    <div className="frost z-10 flex items-center gap-1 px-2 py-1.5">
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

      <div className="ml-auto flex items-center gap-1">
        <Button size="sm" variant="ghost" title="Copy to clipboard" onClick={() => void handleCopy()}>
          <Copy /> Copy
        </Button>
        <Button size="sm" title="Save as…" onClick={() => void handleSave()}>
          <Save /> Save
        </Button>
      </div>
    </div>
  );
}
