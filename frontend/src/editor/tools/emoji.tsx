// EMOJI tool — places a Konva.Text emoji sticker node at the click point, then
// selects it so it is immediately movable/resizable/rotatable via the Transformer
// (the select tool's Transformer binds to selectedId). One click == one node ==
// one history step (addNode commits). The glyph comes from the store's
// `activeEmoji`; size from `activeFontSize` (scaled up — stickers read better big).
//
// This file also exports <EmojiPicker/>: a small frosted (.frost) popover of a
// curated emoji set, modeled on StrokeWidthControl. Clicking a swatch sets the
// active emoji (store.setEmoji) and switches to the emoji tool so the next Stage
// click drops it. Sharp corners only (no rounded-*), shadcn Button + lucide icon.
//
// Design-language compliance: dark-mode .frost chrome, zero rounded corners,
// shadcn Button, lucide icons (per BUILD SPEC). Tool interface implemented
// verbatim from ./types; EmojiNode produced verbatim from ../types.

import { useState } from 'react';
import { Button } from '@/components/ui/button';
import { Smile } from 'lucide-react';
import type { Tool } from './types';
import type { EmojiNode } from '../types';
import { useEditorStore } from '../store';
import { newId } from '../geometry';

/** Curated sticker set — expressive, screenshot-friendly, broad coverage. */
export const EMOJI_SET: readonly string[] = [
  '😀', '😂', '😍', '🤔', '😎', '😢', '😡', '🤯',
  '👍', '👎', '👏', '🙌', '🙏', '💪', '👀', '🤝',
  '❤️', '🔥', '⭐', '✨', '✅', '❌', '⚠️', '❓',
  '💯', '🎉', '🚀', '💡', '📌', '🔒', '🐛', '🎯',
] as const;

/** Emoji stickers render larger than text by default — they read better as glyphs. */
const EMOJI_SCALE = 2;

/**
 * EMOJI tool. On pointer-down, drop the active emoji at the click point (centered
 * on the cursor) and select it. Off-canvas clicks (getPointer() == null) no-op.
 * onPointerMove/Up are inert — placement is a single discrete click.
 */
export const emojiTool: Tool = {
  id: 'emoji',
  cursor: 'crosshair',

  onPointerDown(_e, ctx) {
    const p = ctx.getPointer();
    if (!p) return; // clicked outside the fit-scaled image

    const { activeEmoji, activeFontSize } = ctx.store;
    const fontSize = Math.max(8, activeFontSize * EMOJI_SCALE);

    // EmojiView renders a Konva.Text with no offset, so {x,y} is the glyph's
    // top-left. Bias by ~half the glyph box so the sticker lands centered on the
    // cursor — feels like "stamping" the emoji where you clicked.
    const half = fontSize / 2;

    const node: EmojiNode = {
      id: newId('emoji'),
      type: 'emoji',
      x: p.x - half,
      y: p.y - half,
      rotation: 0,
      opacity: 1,
      draggable: true,
      emoji: activeEmoji,
      fontSize,
    };

    ctx.store.addNode(node);
    ctx.store.select(node.id);
  },

  onPointerMove() {
    // no-op: emoji placement is a single discrete click.
  },

  onPointerUp() {
    // no-op.
  },
};

/**
 * EmojiPicker — frosted popover of the curated emoji set. Picking an emoji sets
 * the active emoji and activates the emoji tool, so the next Stage click stamps
 * it. Mirrors StrokeWidthControl's .frost popover + active-outline convention.
 *
 * Wired into the Toolbar by Integrate (see wiringInstructions). Self-contained:
 * reads/writes only the public store API via selectors.
 */
export function EmojiPicker() {
  const [open, setOpen] = useState(false);
  const activeEmoji = useEditorStore((s) => s.activeEmoji);
  const setEmoji = useEditorStore((s) => s.setEmoji);
  const setTool = useEditorStore((s) => s.setTool);
  const activeTool = useEditorStore((s) => s.activeTool);

  function pick(e: string) {
    setEmoji(e);
    setTool('emoji'); // arm the tool so the next canvas click drops this emoji
    setOpen(false);
  }

  return (
    <div className="relative">
      <Button
        size="icon"
        variant={activeTool === 'emoji' ? 'default' : 'ghost'}
        title="Emoji sticker (E)"
        onClick={() => setOpen((v) => !v)}
      >
        <Smile />
      </Button>
      {open && (
        <div className="frost absolute left-0 top-full z-20 mt-1 grid w-max grid-cols-8 gap-0.5 p-1.5">
          {EMOJI_SET.map((e) => (
            <button
              key={e}
              type="button"
              title={e}
              onClick={() => pick(e)}
              className="flex size-8 items-center justify-center text-xl leading-none hover:bg-accent"
              style={{ outline: activeEmoji === e ? '2px solid var(--color-ring)' : 'none' }}
            >
              {e}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
