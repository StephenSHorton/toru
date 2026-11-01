import { useRef } from "react";
import { AlignCenter, AlignLeft, AlignRight, Bold, Italic, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import type { Text, TextStyle } from "@/trim/edl";
import { FontPicker } from "@/trim/FontPicker";
import { fmtSec, NumberRow, Section, type KeyState } from "@/trim/inspector/controls";
import type { Project } from "@/trim/project";
import { cleanText, resolveStyle, textOpacityAt, TEXT_DEFAULTS, TEXT_LIMITS, type ResolvedStyle } from "@/trim/textRender";
import { replaceTextStyle, setTextOpacity, setTextStyle, toggleTextOpacityKey } from "@/trim/textStyle";

// TextInspector is the right sidebar when a text is selected: its font and
// style (fill, outline, shadow, box, bold/italic, spacing, alignment) and
// its opacity, which can be keyframed (times from the text's start). The
// words, size and position are edited on the player. Every change is one
// undo step per gesture, like the clip inspector.

export function TextInspector({
  project,
  textId,
  apply,
  playhead,
  onSeek,
  className,
}: {
  project: Project;
  textId: string;
  apply: (fn: (p: Project) => Project, key?: string) => void;
  playhead: number;
  onSeek: (t: number) => void;
  className?: string;
}) {
  const gesture = useRef(0);
  const t = project.texts.find((x) => x.id === textId);
  if (!t) return null;
  const s = resolveStyle(t.style);
  const local = Math.max(0, Math.min(t.end - t.start, playhead - t.start));
  const edit = (fn: (p: Project) => Project, what: string) => apply(fn, `text:${t.id}:${what}:${gesture.current}`);
  const set = (patch: TextStyle, what: string) => edit((p) => setTextStyle(p, t.id, patch), what);
  const keys: KeyState = {
    keys: s.opacityKeys?.map((k) => ({ t: k.t, v: k.v, ease: k.ease })),
    t: local,
    onToggle: () => edit((p) => toggleTextOpacityKey(p, t.id, local), "opacity.key"),
    onJump: (kt) => onSeek(t.start + kt),
  };
  const styled = !!t.style && Object.keys(t.style).some((k) => k !== "opacityKeys");

  return (
    <aside
      className={cn("flex min-h-0 flex-col text-xs", className)}
      onPointerDownCapture={() => gesture.current++}
      onKeyDownCapture={() => gesture.current++}
      aria-label="Text settings"
    >
      <div className="border-b border-border/60 px-3 py-2">
        <div className="truncate text-sm font-medium">{cleanText(t.text).split("\n")[0] || "Text"}</div>
        <div className="text-[11px] text-muted-foreground tabular-nums">
          Text · {fmtSec(t.start)} – {fmtSec(t.end)} ({fmtSec(t.end - t.start)})
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden">
        <Section
          title="Font"
          action={
            styled ? (
              <Button size="sm" variant="ghost" className="h-6 gap-1 px-1.5 text-[11px]" onClick={() => edit((p) => replaceTextStyle(p, t.id, undefined), "reset")} title="Back to the plain style">
                <RotateCcw className="size-3" /> Reset style
              </Button>
            ) : null
          }
        >
          <FontPicker value={t.font ?? ""} onChange={(font) => edit((p) => ({ ...p, texts: p.texts.map((x) => (x.id === t.id ? { ...x, font: font || undefined } : x)) }), "font")} />
          <div className="flex items-center gap-1">
            <Toggle on={s.bold} label="Bold" onClick={() => set({ bold: !s.bold }, "bold")}>
              <Bold />
            </Toggle>
            <Toggle on={s.italic} label="Italic" onClick={() => set({ italic: !s.italic }, "italic")}>
              <Italic />
            </Toggle>
            <div className="mx-1 h-5 w-px bg-border" />
            {(
              [
                ["left", AlignLeft, "Align left"],
                ["center", AlignCenter, "Centre"],
                ["right", AlignRight, "Align right"],
              ] as const
            ).map(([a, Icon, label]) => (
              <Toggle key={a} on={s.align === a} label={label} onClick={() => set({ align: a }, "align")}>
                <Icon />
              </Toggle>
            ))}
          </div>
          <Num s={s} k="letterSpacing" label="Letters" scale={100} unit="%" step={0.005} hint="Space between letters, % of the size" onChange={(v) => set({ letterSpacing: v }, "letterSpacing")} />
          <Num s={s} k="lineSpacing" label="Lines" scale={100} unit="%" step={0.05} hint="Line height, % of the font's own" onChange={(v) => set({ lineSpacing: v }, "lineSpacing")} />
        </Section>

        <Section title="Fill">
          <ColorRow label="Colour" value={s.color} onChange={(c) => set({ color: c }, "color")} />
          <Num s={s} k="fillOpacity" label="Opacity" scale={100} unit="%" step={0.01} onChange={(v) => set({ fillOpacity: v }, "fillOpacity")} />
        </Section>

        <Toggleable title="Outline" on={s.outlineOpacity > 0} onToggle={(on) => set({ outlineOpacity: on ? 1 : 0 }, "outline.on")}>
          <ColorRow label="Colour" value={s.outlineColor} onChange={(c) => set({ outlineColor: c }, "outlineColor")} />
          <Num s={s} k="outlineWidth" label="Width" scale={100} unit="%" step={0.005} hint="% of the size" onChange={(v) => set({ outlineWidth: v }, "outlineWidth")} />
          <Num s={s} k="outlineOpacity" label="Opacity" scale={100} unit="%" step={0.01} onChange={(v) => set({ outlineOpacity: v }, "outlineOpacity")} />
        </Toggleable>

        <Toggleable title="Shadow" on={s.shadowOpacity > 0} onToggle={(on) => set({ shadowOpacity: on ? 0.8 : 0 }, "shadow.on")}>
          <ColorRow label="Colour" value={s.shadowColor} onChange={(c) => set({ shadowColor: c }, "shadowColor")} />
          <Num s={s} k="shadowX" label="Offset X" scale={100} unit="%" step={0.005} hint="% of the size" onChange={(v) => set({ shadowX: v }, "shadowX")} />
          <Num s={s} k="shadowY" label="Offset Y" scale={100} unit="%" step={0.005} hint="% of the size" onChange={(v) => set({ shadowY: v }, "shadowY")} />
          <Num s={s} k="shadowBlur" label="Blur" scale={100} unit="%" step={0.005} hint="% of the size" onChange={(v) => set({ shadowBlur: v }, "shadowBlur")} />
          <Num s={s} k="shadowOpacity" label="Opacity" scale={100} unit="%" step={0.01} onChange={(v) => set({ shadowOpacity: v }, "shadowOpacity")} />
        </Toggleable>

        <Toggleable title="Background" on={s.boxOpacity > 0} onToggle={(on) => set({ boxOpacity: on ? 0.6 : 0 }, "box.on")}>
          <ColorRow label="Colour" value={s.boxColor} onChange={(c) => set({ boxColor: c }, "boxColor")} />
          <Num s={s} k="boxPad" label="Padding" scale={100} unit="%" step={0.01} hint="% of the size" onChange={(v) => set({ boxPad: v }, "boxPad")} />
          <Num s={s} k="boxRadius" label="Corners" scale={100} unit="%" step={0.01} hint="Corner radius, % of the size" onChange={(v) => set({ boxRadius: v }, "boxRadius")} />
          <Num s={s} k="boxOpacity" label="Opacity" scale={100} unit="%" step={0.01} onChange={(v) => set({ boxOpacity: v }, "boxOpacity")} />
        </Toggleable>

        <Section title="Text">
          <NumberRow
            label="Opacity"
            value={textOpacityAt(s, local)}
            min={0}
            max={1}
            step={0.01}
            def={1}
            scale={100}
            unit="%"
            hint="The whole text; ◆ keyframes it at the playhead"
            keyframes={keys}
            onChange={(v) => edit((p) => setTextOpacity(p, t.id, v, local), "opacity")}
          />
        </Section>
      </div>
    </aside>
  );
}

function Num({
  s,
  k,
  label,
  step,
  scale,
  unit,
  hint,
  onChange,
}: {
  s: ResolvedStyle;
  k: keyof typeof TEXT_LIMITS;
  label: string;
  step: number;
  scale?: number;
  unit?: string;
  hint?: string;
  onChange: (v: number) => void;
}) {
  const [min, max] = TEXT_LIMITS[k];
  return <NumberRow label={label} value={s[k] as number} min={min} max={max} step={step} def={TEXT_DEFAULTS[k] as number} scale={scale} unit={unit} hint={hint} onChange={onChange} />;
}

function ColorRow({ label, value, onChange }: { label: string; value: string; onChange: (c: string) => void }) {
  return (
    <div className="flex items-center gap-1.5">
      <span className="w-[4.5rem] shrink-0 truncate">{label}</span>
      <label className="relative size-6 shrink-0 cursor-pointer border border-border" style={{ background: value }} title="Pick a colour">
        <input type="color" value={value} onChange={(e) => onChange(e.target.value)} className="absolute inset-0 cursor-pointer opacity-0" aria-label={label} />
      </label>
      <span className="font-mono text-[11px] text-muted-foreground">{value}</span>
    </div>
  );
}

function Toggle({ on, label, onClick, children }: { on: boolean; label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <Button size="icon" variant={on ? "secondary" : "ghost"} className="size-7 [&_svg]:size-3.5" onClick={onClick} title={label} aria-label={label} aria-pressed={on}>
      {children}
    </Button>
  );
}

function Toggleable({ title, on, onToggle, children }: { title: string; on: boolean; onToggle: (on: boolean) => void; children: React.ReactNode }) {
  return (
    <Section title={title} action={<Switch checked={on} onCheckedChange={onToggle} aria-label={`${title} on`} />}>
      {on ? children : null}
    </Section>
  );
}

export type { Text };
