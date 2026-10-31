import { useEffect, useRef, useState, type CSSProperties } from "react";
import { Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NumberInput } from "@/components/ui/number-input";
import { cn } from "@/lib/utils";
import type { RenderedBox } from "@/trim/CropBox";
import { FontPicker } from "@/trim/FontPicker";
import type { Text } from "@/trim/edl";
import { useFontsVersion } from "@/trim/fonts";
import { cleanText, hasDroppedChars, renderText, resolveStyle, textFontSize, textOpacityAt, TEXT_SIZES, MAX_FONT_PX, MIN_FONT_PX, type TextBitmap } from "@/trim/textRender";

// TextOverlays draws the editor's text overlays over the player and lets
// the user drag them and edit them. It is laid over the rendered OUTPUT
// frame: the parent passes the CSS box of the video, or of the crop rect when
// there is a crop, plus that frame's size in video pixels.
//
// Each text is drawn by textRender.ts at the output frame's pixel size and
// shown scaled: the same bitmap the export composites, so what's on the
// player is what's exported (font, outline, shadow, box, spacing). The
// whole-text opacity (keyframable) is applied on top, as the export does.
//
// Positions are fractions of the output frame (0..1, the text's centre);
// times are output seconds. A drag or an edit reports once, when it ends,
// so each lands as one undo step.

export type OverlaySize = "s" | "m" | "l";

// Same shape as edl.ts's Text.
export type OverlayText = Text;

export { MAX_FONT_PX, MIN_FONT_PX, TEXT_SIZES, textFontSize } from "@/trim/textRender";

// A seek lands on the frame at or before the time asked for, so a text added
// at the playhead starts a few ms after the frame on screen. This much slack
// (under a frame at 24fps) keeps it showing on that frame.
const START_SLACK = 0.04;

// presetFontPx is a preset size in output pixels.
export function presetFontPx(size: OverlaySize, frameH: number): number {
  return Math.max(1, Math.round(TEXT_SIZES[size] * frameH));
}

const clamp01 = (n: number) => (Number.isFinite(n) ? Math.min(1, Math.max(0, n)) : 0.5);

// TextBitmapView shows one text's bitmap over the frame box, cached until
// the text, the frame size or the fonts change.
function TextBitmapView({ bmp, box, scale, opacity }: { bmp: TextBitmap; box: RenderedBox; scale: number; opacity: number }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const cv = ref.current;
    if (!cv) return;
    cv.width = bmp.canvas.width;
    cv.height = bmp.canvas.height;
    const ctx = cv.getContext("2d")!;
    ctx.clearRect(0, 0, cv.width, cv.height);
    ctx.drawImage(bmp.canvas, 0, 0);
  }, [bmp]);
  return (
    <canvas
      ref={ref}
      aria-hidden
      className="pointer-events-none absolute z-20"
      style={{ left: box.left + bmp.x * scale, top: box.top + bmp.y * scale, width: bmp.canvas.width * scale, height: bmp.canvas.height * scale, opacity }}
    />
  );
}

// useBitmaps renders each text once per change (text, style, font, frame
// size, a font file loading).
function useBitmaps(texts: OverlayText[], frameW: number, frameH: number, fontsVersion: unknown) {
  const cache = useRef(new Map<string, { key: string; bmp: TextBitmap | null }>());
  const out = new Map<string, TextBitmap | null>();
  for (const t of texts) {
    const key = JSON.stringify([t.text, t.style, t.font, t.fontPx, t.size, t.x, t.y, frameW, frameH, fontsVersion]);
    let hit = cache.current.get(t.id);
    if (!hit || hit.key !== key) {
      hit = { key, bmp: renderText(t, frameW, frameH, "Text") };
      cache.current.set(t.id, hit);
    }
    out.set(t.id, hit.bmp);
  }
  return out;
}

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
  const fontsVersion = useFontsVersion(); // re-render once the font list (and each font file) loads
  const shownTexts = texts.map((t) => {
    const base = draft?.id === t.id ? draft : t;
    return dragPos?.id === t.id ? { ...base, x: dragPos.x, y: dragPos.y } : base;
  });
  const bitmaps = useBitmaps(shownTexts, frameW, frameH, fontsVersion);

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
      {texts.map((t, i) => {
        const isSel = t.id === selectedId;
        const active = time >= t.start - START_SLACK && time < t.end;
        if (!active && !isSel) return null;
        const shown = shownTexts[i];
        const body = cleanText(shown.text);
        const bmp = bitmaps.get(t.id);
        const scale = box.height / frameH;
        const op = textOpacityAt(resolveStyle(shown.style), Math.max(0, time - t.start));
        const blk = bmp?.block ?? { left: clamp01(shown.x) * frameW - 20, top: clamp01(shown.y) * frameH - 10, width: 40, height: 20 };
        return (
          <div key={t.id}>
            {bmp ? <TextBitmapView bmp={bmp} box={box} scale={scale} opacity={active ? (body ? op : 0.5) : 0.4} /> : null}
            <div
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
              style={{ position: "absolute", left: box.left + blk.left * scale, top: box.top + blk.top * scale, width: blk.width * scale, height: blk.height * scale }}
              className={cn(
                "z-20 cursor-move touch-none select-none outline-offset-4",
                isSel ? "outline-2 outline-dashed outline-primary" : "hover:outline-1 hover:outline-dashed hover:outline-white/60",
              )}
            />
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
  const half = (lines * 1.33 * resolveStyle(t.style).lineSpacing * px * (box.height / frameH)) / 2;
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
