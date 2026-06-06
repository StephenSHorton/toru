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

import { useRef, useState } from 'react';
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
 * EmojiPicker — frosted popover that uses the OPERATING SYSTEM emoji panel as the
 * primary picker. Opening the popover focuses a hidden input, so pressing the
 * Windows emoji shortcut (⊞ Win + .) drops the chosen glyph straight in; we read
 * it, set it active, and arm the emoji tool so the next canvas click stamps it.
 *
 * Windows has no API to programmatically OPEN the panel (Win+. is a user gesture
 * that inserts into the focused field), so the user presses it themselves — and
 * the curated grid below stays as a fallback if the OS panel isn't available.
 *
 * Self-contained: reads/writes only the public store API via selectors.
 */
export function EmojiPicker() {
  const [open, setOpen] = useState(false);
  const inputRef = useRef<HTMLInputElement>(null);
  const activeEmoji = useEditorStore((s) => s.activeEmoji);
  const setEmoji = useEditorStore((s) => s.setEmoji);
  const setTool = useEditorStore((s) => s.setTool);
  const activeTool = useEditorStore((s) => s.activeTool);

  function arm(e: string) {
    setEmoji(e);
    setTool('emoji'); // arm the tool so the next canvas click drops this emoji
    setOpen(false);
  }

  function toggle() {
    setOpen((v) => {
      const next = !v;
      // Focus the hidden input so the OS emoji panel inserts into it.
      if (next) setTimeout(() => inputRef.current?.focus(), 0);
      return next;
    });
  }

  // The OS emoji panel inserts the glyph(s) into the focused input -> capture it.
  function onHiddenInput(e: React.FormEvent<HTMLInputElement>) {
    const val = e.currentTarget.value.trim();
    e.currentTarget.value = '';
    if (val) arm(val);
  }

  return (
    <div className="relative">
      <Button
        size="icon"
        variant={activeTool === 'emoji' ? 'default' : 'ghost'}
        title="Emoji sticker (E) — opens the system emoji panel"
        onClick={toggle}
      >
        <Smile />
      </Button>
      {open && (
        <div className="frost absolute bottom-full left-0 z-20 mb-1 w-max p-1.5">
          <div className="mb-1.5 max-w-[16rem] px-0.5 text-[11px] leading-snug text-muted-foreground">
            Press <span className="font-mono text-foreground">⊞&nbsp;Win&nbsp;+&nbsp;.</span> for the
            system emoji panel, or pick one:
          </div>
          {/* Hidden, focusable target for the OS emoji panel's insertion. */}
          <input
            ref={inputRef}
            onInput={onHiddenInput}
            aria-hidden
            tabIndex={-1}
            style={{ position: 'absolute', width: 1, height: 1, opacity: 0, padding: 0, border: 0 }}
          />
          <div className="grid grid-cols-8 gap-0.5">
            {EMOJI_SET.map((e) => (
              <button
                key={e}
                type="button"
                title={e}
                onClick={() => arm(e)}
                className="flex size-8 items-center justify-center text-xl leading-none hover:bg-accent"
                style={{ outline: activeEmoji === e ? '2px solid var(--color-ring)' : 'none' }}
              >
                {e}
              </button>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
