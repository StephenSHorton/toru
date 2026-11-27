// The curated emoji set, shared by the screenshot editor's emoji tool
// (tools/emoji.tsx) and the whiteboard's drawing bar (whiteboard/DrawingBar.tsx).
// Its own module, with no imports, so the whiteboard takes the list without
// the editor's store or Konva.

/** Curated sticker set — expressive, screenshot-friendly, broad coverage. */
export const EMOJI_SET: readonly string[] = [
  '😀', '😂', '😍', '🤔', '😎', '😢', '😡', '🤯',
  '👍', '👎', '👏', '🙌', '🙏', '💪', '👀', '🤝',
  '❤️', '🔥', '⭐', '✨', '✅', '❌', '⚠️', '❓',
  '💯', '🎉', '🚀', '💡', '📌', '🔒', '🐛', '🎯',
] as const;
