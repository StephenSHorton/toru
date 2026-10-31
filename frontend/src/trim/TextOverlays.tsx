import { useEffect, useRef, useState, type CSSProperties } from "react";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NumberInput } from "@/components/ui/number-input";
import { cn } from "@/lib/utils";
import type { RenderedBox } from "@/trim/CropBox";
import { FontPicker } from "@/trim/FontPicker";
import { baselineShift, DEFAULT_FONT_CSS, DEFAULT_LINE_HEIGHT, fontFamily, fontInfo, useFonts } from "@/trim/fonts";

// TextOverlays draws the editor's text overlays over the player, exactly as
// the export will (internal/vid/edit_text.go), and lets the user drag them
// and edit them. It is laid over the rendered OUTPUT frame: the parent passes
// the CSS box of the video, or of the crop rect when there is a crop, plus
// that frame's size in video pixels.
//
// Matching the export: the text's font (fonts.ts: the same file the export
// draws with, or Segoe UI Semibold when it has none or this PC lacks it),
// fontPx output pixels (older texts: 4/6/9% of the output height for s/m/l,
// rounded), lines centred at the font's own line height (what drawtext
// advances by), moved by baselineShift to drawtext's baseline, the block
// centred on (x, y), and an outline 6% of the font size drawn under the
// fill. Text goes
// through cleanText, the same rules the export applies, so what is dropped
// there (emoji) is not shown here either. Keep both sides in step.
//
// Positions are fractions of the output frame (0..1, the text's centre);
// times are output seconds. A drag or an edit reports once, when it ends,
// so each lands as one undo step.

export type OverlaySize = "s" | "m" | "l";

// Same shape as edl.ts's Text.
export type OverlayText = {
  id: string;
  text: string;
  x: number;
  y: number;
  size: OverlaySize;
  fontPx?: number; // output px; unset = from size
  font?: string; // installed font's name; unset/"" = the default
  start: number;
  end: number;
};

export const TEXT_SIZES: Record<OverlaySize, number> = { s: 0.04, m: 0.06, l: 0.09 };
// Font size limits in output px (Go: MaxTextFontPx).
export const MIN_FONT_PX = 4;
export const MAX_FONT_PX = 1000;
const BORDER_RATIO = 0.06;
// A seek lands on the frame at or before the time asked for, so a text added
// at the playhead starts a few ms after the frame on screen. This much slack
// (under a frame at 24fps) keeps it showing on that frame.
const START_SLACK = 0.04;

// presetFontPx is a preset size in output pixels (Go: TextFontSize).
export function presetFontPx(size: OverlaySize, frameH: number): number {
  return Math.max(1, Math.round(TEXT_SIZES[size] * frameH));
}

// textFontSize is a text's font size in output pixels (Go: textPx): fontPx
// when set, else its preset size.
export function textFontSize(t: { size: OverlaySize; fontPx?: number }, frameH: number): number {
  if (t.fontPx && t.fontPx > 0) return Math.min(Math.round(t.fontPx), MAX_FONT_PX);
  return presetFontPx(t.size, frameH);
}

// textFont is how the preview draws t's font: the installed face, or the
// default when it has none or it isn't installed (as the export falls back).
function textFont(t: OverlayText, fontPx: number) {
  const f = fontInfo(t.font);
  if (!f) return { family: DEFAULT_FONT_CSS, weight: 600, lineHeight: DEFAULT_LINE_HEIGHT, shift: 0 };
  return { family: fontFamily(f), weight: 400, lineHeight: f.lineHeight, shift: baselineShift(f, fontPx) };
}

// Emoji blocks and the invisible runes that build emoji sequences (Go:
// isEmojiRune). Segoe UI has no glyphs for them.
const EMOJI =
  /[\u{1F000}-\u{1FAFF}\u{2600}-\u{27BF}\u{2B00}-\u{2BFF}\u{FE00}-\u{FE0F}\u{E0020}-\u{E007F}\u{200D}\u{20E3}]/gu;

// cleanText is the text as the export draws it (Go: CleanOverlayText).
export function cleanText(s: string): string {
  const lines = s
    .replace(/\r\n?/g, "\n")
    .replace(/\t/g, " ")
    .replace(EMOJI, "")
    .split("\n")
    .map((l) => l.replace(/ +$/, ""));
  return lines.join("\n").replace(/^\n+|\n+$/g, "");
}

export const hasDroppedChars = (s: string) => s.replace(EMOJI, "") !== s;

// textStyle places and styles one overlay over a frameH-tall frame drawn
// `scale` CSS px per output px, inside a box positioned at the frame's origin.
export function textStyle(t: OverlayText, box: RenderedBox, frameH: number): CSSProperties {
  const scale = box.height / frameH;
  const fontPx = textFontSize(t, frameH);
  const border = Math.max(1, Math.round(fontPx * BORDER_RATIO));
  const font = textFont(t, fontPx);
  return {
    position: "absolute",
    left: box.left + clamp01(t.x) * box.width,
    top: box.top + clamp01(t.y) * box.height,
    transform: `translate(-50%, calc(-50% + ${font.shift * scale}px))`,
    fontFamily: font.family,
    fontWeight: font.weight,
    fontSynthesis: "none",
    fontSize: fontPx * scale,
    lineHeight: font.lineHeight,
    whiteSpace: "pre",
    textAlign: "center",
    color: "white",
    // The stroke is centred on the outline and paint-order puts it under the
    // fill, so only the outer half shows: border px outside, like drawtext.
    WebkitTextStroke: `${2 * border * scale}px rgba(0, 0, 0, 0.8)`,
    paintOrder: "stroke fill",
  };
}

const clamp01 = (n: number) => (Number.isFinite(n) ? Math.min(1, Math.max(0, n)) : 0.5);

export interface TextOverlaysProps {
  texts: OverlayText[];
  // Current output time, seconds: texts show while start <= time < end.
  time: number;
  // The output frame (after crop) in video pixels.
  frameW: number;
  frameH: number;
  // Where that frame is drawn, CSS px relative to the positioned parent.
  box: RenderedBox;
  selectedId: string | null;
  onSelect: (id: string | null) => void;
  onChange: (t: OverlayText) => void;
  onRemove: (id: string) => void;
}

export function TextOverlays({ texts, time, frameW, frameH, box, selectedId, onSelect, onChange, onRemove }: TextOverlaysProps) {
  // The text being dragged and where it is now (fractions), so a drag
  // re-renders only here and reports once on release.
  const drag = useRef<{ id: string; x0: number; y0: number; start: { x: number; y: number }; moved: boolean } | null>(null);
  const [dragPos, setDragPos] = useState<{ id: string; x: number; y: number } | null>(null);
  // The selected text while it's being typed or resized in the editor:
  // shown live, reported once when the edit lands.
  const [draft, setDraft] = useState<OverlayText | null>(null);
  useFonts(); // re-render once the font list (and each font file) loads

  if (frameW <= 0 || frameH <= 0 || box.width <= 0 || box.height <= 0) return null;

  const selected = texts.find((t) => t.id === selectedId) ?? null;

  const begin = (t: OverlayText) => (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    e.stopPropagation();
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { id: t.id, x0: e.clientX, y0: e.clientY, start: { x: t.x, y: t.y }, moved: false };
    if (t.id !== selectedId) onSelect(t.id);
  };
  const move = (e: React.PointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d) return;
    const dx = e.clientX - d.x0;
    const dy = e.clientY - d.y0;
    if (!d.moved && Math.hypot(dx, dy) < 3) return; // a click, not a drag
    d.moved = true;
    setDragPos({ id: d.id, x: clamp01(d.start.x + dx / box.width), y: clamp01(d.start.y + dy / box.height) });
  };
  const end = (t: OverlayText) => (e: React.PointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    drag.current = null;
    if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
    if (d?.moved && dragPos && dragPos.id === t.id) onChange({ ...t, x: dragPos.x, y: dragPos.y });
    setDragPos(null);
  };

  return (
    <>
      {texts.map((t) => {
        const isSel = t.id === selectedId;
        const active = time >= t.start - START_SLACK && time < t.end;
        if (!active && !isSel) return null;
        const base = draft?.id === t.id ? draft : t;
        const shown = dragPos?.id === t.id ? { ...base, x: dragPos.x, y: dragPos.y } : base;
        const body = cleanText(shown.text);
        return (
          <div
            key={t.id}
            role="button"
            aria-label={`Text: ${body || "empty"}`}
            title="Drag to move"
            onPointerDown={begin(t)}
            onPointerMove={move}
            onPointerUp={end(t)}
            onLostPointerCapture={() => {
              drag.current = null;
              setDragPos(null);
            }}
            onClick={(e) => e.stopPropagation()}
            style={textStyle(shown, box, frameH)}
            className={cn(
              "z-20 cursor-move touch-none select-none outline-offset-4",
              isSel ? "outline-2 outline-dashed outline-primary" : "hover:outline-1 hover:outline-dashed hover:outline-white/60",
              !active && "opacity-40",
            )}
          >
            {/* An empty text still needs a box to grab. */}
            {body || <span className="text-white/50 [-webkit-text-stroke:0]">Text</span>}
          </div>
        );
      })}
      {selected && !dragPos ? (
        <TextEditor
          key={selected.id}
          text={selected}
          anchor={textAnchor(draft?.id === selected.id ? draft : selected, box, frameH)}
          box={box}
          frameH={frameH}
          onDraft={setDraft}
          onChange={(t) => {
            setDraft(null);
            onChange(t);
          }}
          onRemove={() => onRemove(selected.id)}
          onClose={() => onSelect(null)}
        />
      ) : null}
    </>
  );
}

// textAnchor is the selected text's centre and half height in CSS px, for
// placing the popover clear of it.
function textAnchor(t: OverlayText, box: RenderedBox, frameH: number) {
  const lines = Math.max(1, cleanText(t.text).split("\n").length);
  const px = textFontSize(t, frameH);
  const half = (lines * textFont(t, px).lineHeight * px * (box.height / frameH)) / 2;
  return { x: box.left + clamp01(t.x) * box.width, y: box.top + clamp01(t.y) * box.height, half };
}

const PRESETS = [
  { value: "s", label: "S", title: "Small" },
  { value: "m", label: "M", title: "Medium" },
  { value: "l", label: "L", title: "Large" },
] as const;

const POPOVER_W = 300;

function TextEditor({
  text,
  anchor,
  box,
  frameH,
  onDraft,
  onChange,
  onRemove,
  onClose,
}: {
  text: OverlayText;
  anchor: { x: number; y: number; half: number };
  box: RenderedBox;
  frameH: number;
  onDraft: (t: OverlayText | null) => void;
  onChange: (t: OverlayText) => void;
  onRemove: () => void;
  onClose: () => void;
}) {
  // A draft while typing; it lands on blur, Ctrl+Enter or Escape, so typing
  // a sentence is one undo step, not one per key.
  const [draft, setDraft] = useState(text.text);
  const textRef = useRef(text);
  textRef.current = text;
  const draftRef = useRef(draft);
  draftRef.current = draft;
  const area = useRef<HTMLTextAreaElement>(null);

  const commit = () => {
    if (draftRef.current !== textRef.current.text) onChange({ ...textRef.current, text: draftRef.current });
    else onDraft(null);
  };

  // New (empty) texts open ready to type.
  useEffect(() => {
    if (text.text === "") area.current?.focus();
    // Commit a pending draft if the popover goes away (selection change).
    return commit;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Below the text, or above it in the lower half; kept inside the frame box.
  const below = anchor.y < box.top + box.height / 2;
  const left = Math.min(Math.max(anchor.x - POPOVER_W / 2, box.left), box.left + box.width - POPOVER_W);
  const gap = anchor.half + 10;
  const pos: CSSProperties = below ? { top: anchor.y + gap } : { bottom: `calc(100% - ${anchor.y - gap}px)` };

  return (
    <div
      className="absolute z-30 flex flex-col gap-2 border border-border bg-popover p-2 text-popover-foreground shadow-lg"
      style={{ left: Math.max(left, 0), width: POPOVER_W, ...pos }}
      onPointerDown={(e) => e.stopPropagation()}
      onClick={(e) => e.stopPropagation()}
    >
      <textarea
        ref={area}
        value={draft}
        rows={2}
        placeholder="Type your text"
        onChange={(e) => {
          // Shown live over the video (and the popover placed for it), so
          // the commit on blur doesn't move the popover under the pointer.
          setDraft(e.target.value);
          onDraft({ ...textRef.current, text: e.target.value });
        }}
        onBlur={commit}
        onKeyDown={(e) => {
          e.stopPropagation(); // keep the editor's shortcuts (S, Delete, Space) out of the textbox
          if (e.key === "Escape" || (e.key === "Enter" && e.ctrlKey)) {
            e.preventDefault();
            commit();
            onClose();
          }
        }}
        className="w-full resize-none border border-border bg-background px-2 py-1 text-sm outline-none focus-visible:ring-1 focus-visible:ring-ring"
      />
      {hasDroppedChars(draft) ? <p className="text-[11px] text-muted-foreground">Emoji aren't drawn in the video.</p> : null}
      <FontPicker
        value={text.font ?? ""}
        onChange={(font) => onChange({ ...textRef.current, text: draftRef.current, font: font || undefined })}
      />
      <SizeRow text={text} frameH={frameH} onDraft={(px) => onDraft(px === null ? null : { ...textRef.current, text: draftRef.current, fontPx: px })} onCommit={(px) => onChange({ ...textRef.current, text: draftRef.current, fontPx: px })} />
      <div className="flex items-center justify-end gap-2">
        <div className="flex items-center gap-1">
          <Button variant="ghost" size="sm" className="h-7 px-2 text-destructive" title="Delete text" onClick={onRemove}>
            <Trash2 />
          </Button>
          <Button
            size="sm"
            className="h-7"
            onClick={() => {
              commit();
              onClose();
            }}
          >
            Done
          </Button>
        </div>
      </div>
    </div>
  );
}

// SizeRow is the font size in output px: a slider (reported on release, so
// a drag is one undo step), a number field (on Enter or blur) and quick
// S/M/L presets (4/6/9% of the frame's height).
function SizeRow({ text, frameH, onDraft, onCommit }: { text: OverlayText; frameH: number; onDraft: (px: number | null) => void; onCommit: (px: number) => void }) {
  const px = textFontSize(text, frameH);
  const [live, setLive] = useState<number | null>(null);
  const liveRef = useRef<number | null>(null);
  const shown = live ?? px;
  const sliderMax = Math.max(px, Math.round(frameH * 0.3), 120);
  const set = (v: number) => {
    const n = Math.min(MAX_FONT_PX, Math.max(MIN_FONT_PX, Math.round(v)));
    liveRef.current = n;
    setLive(n);
    onDraft(n);
  };
  const commit = () => {
    const n = liveRef.current;
    liveRef.current = null;
    setLive(null);
    if (n !== null && n !== text.fontPx) onCommit(n);
    else onDraft(null); // unchanged: drop the live copy
  };
  return (
    <div className="flex flex-col gap-1.5">
      <div className="flex items-center gap-2">
        <span className="w-8 shrink-0 text-[11px] text-muted-foreground">Size</span>
        <input
          type="range"
          aria-label="Text size"
          min={MIN_FONT_PX}
          max={sliderMax}
          step={1}
          value={Math.min(shown, sliderMax)}
          onChange={(e) => set(Number(e.target.value))}
          onPointerUp={commit}
          onKeyUp={commit}
          onBlur={() => liveRef.current !== null && commit()}
          className="h-4 min-w-0 flex-1 accent-primary"
        />
        <NumberInput
          aria-label="Text size in pixels"
          value={shown}
          min={MIN_FONT_PX}
          max={MAX_FONT_PX}
          suffix="px"
          boxClassName="w-20"
          className="h-7"
          onChange={set}
          onBlur={commit}
          onKeyDown={(e) => {
            e.stopPropagation();
            if (e.key === "Enter") commit();
          }}
        />
      </div>
      <div className="flex items-center gap-1 pl-10">
        {PRESETS.map((p) => {
          const v = presetFontPx(p.value, frameH);
          return (
            <Button
              key={p.value}
              size="sm"
              variant={shown === v ? "secondary" : "ghost"}
              className="h-6 min-w-7 px-1.5 text-[11px]"
              title={`${p.title} (${v}px)`}
              onClick={() => v !== text.fontPx && onCommit(v)}
            >
              {p.label}
            </Button>
          );
        })}
      </div>
    </div>
  );
}
