import { useRef, type ReactNode } from "react";
import { Lock, Pipette, RotateCcw, Volume2, VolumeX } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/segmented";
import { Switch } from "@/components/ui/switch";
import type { TrackLevel } from "@/lib/api";
import { cn } from "@/lib/utils";
import {
  clipLength,
  DEFAULT_CHROMA,
  DEFAULT_FILTERS,
  findClip,
  FIT,
  MAX_SPEED,
  mediaById,
  MIN_SPEED,
  updateClip,
  type Chroma,
  type ClipPatch,
  type Filters,
  type Look,
  type Project,
  type Transform,
} from "@/trim/project";

// Inspector is the v2 editor's right sidebar (docs/video-editor-v2.md,
// Inspector): every setting of the selected clip. Transform (position, size,
// opacity), Speed, Audio (volume, mute, fades, and the per-source-track
// levels of a clip recorded with separate tracks), Green screen (on/off, key
// colour with an eyedropper on the player, similarity, blend) and Filters
// (brightness, contrast, saturation, look, blur).
//
// Every change goes through `apply` (useProjectHistory) as updateClip, which
// clamps it. A slider drag is one undo step: each press or key on a control
// starts a new gesture, and the changes of one gesture share an apply key.
// The eyedropper only flips `picking`; PlayerHandles takes the click on the
// player and sets the colour.

export interface InspectorProps {
  project: Project;
  clipId: string | null; // the selected clip
  apply: (fn: (p: Project) => Project, key?: string) => void;
  picking?: boolean; // the eyedropper is waiting for a click on the player
  onPickingChange?: (on: boolean) => void;
  className?: string;
}

const LOOKS: { value: Look; label: string }[] = [
  { value: "none", label: "None" },
  { value: "bw", label: "B&W" },
  { value: "sepia", label: "Sepia" },
  { value: "vintage", label: "Vintage" },
  { value: "cool", label: "Cool" },
  { value: "warm", label: "Warm" },
];
const SPEEDS = [0.25, 0.5, 1, 1.5, 2, 4];

export function Inspector({ project, clipId, apply, picking, onPickingChange, className }: InspectorProps) {
  const gesture = useRef(0);
  const hit = clipId ? findClip(project, clipId) : undefined;
  if (!hit) {
    return (
      <aside className={cn("flex flex-col gap-2 p-3 text-xs text-muted-foreground", className)}>
        Select a clip on the timeline or the player to edit it.
      </aside>
    );
  }
  const { clip: c, track } = hit;
  const media = mediaById(project, c.media);
  const visual = track.kind === "video";
  const audible = track.kind === "audio" || (media?.kind === "video" && media.hasAudio);
  const locked = track.locked;
  const len = clipLength(c);

  // set applies a patch; name + the current gesture make the undo key.
  const set = (name: string, patch: ClipPatch) => apply((p) => updateClip(p, c.id, patch), `inspect:${c.id}:${name}:${gesture.current}`);
  const bump = () => gesture.current++;
  const setT = (name: keyof Transform, v: number) => set(`t.${name}`, { transform: { ...c.transform, [name]: v } });
  const setF = <K extends keyof Filters>(name: K, v: Filters[K]) => set(`f.${name}`, { filters: { ...c.filters, [name]: v } });
  const setChroma = (name: keyof Chroma, v: Chroma[keyof Chroma]) =>
    set(`k.${name}`, { chroma: { ...(c.chroma ?? DEFAULT_CHROMA), [name]: v } as Chroma });

  // Separate source tracks (not the mix) for v1-style per-track levels.
  const srcTracks = media && media.tracks.length > 1 ? media.tracks.filter((t) => !t.isMix) : [];
  const levelOf = (index: number): TrackLevel =>
    c.levels?.find((l) => l.index === index) ?? ({ source: 0, index, volume: 100, muted: false } as TrackLevel);
  const setLevel = (index: number, patch: Partial<Pick<TrackLevel, "volume" | "muted">>) => {
    const rows = srcTracks.map((t) => (t.index === index ? { ...levelOf(t.index), ...patch } : levelOf(t.index)));
    const changed = rows.some((l) => l.volume !== 100 || l.muted);
    set(`lv.${index}`, { levels: changed ? (rows as TrackLevel[]) : undefined });
  };

  return (
    <aside
      className={cn("flex min-h-0 flex-col overflow-y-auto text-xs", className)}
      onPointerDownCapture={bump}
      onKeyDownCapture={bump}
      aria-label="Clip settings"
    >
      <div className="border-b border-border/60 px-3 py-2">
        <div className="truncate text-sm font-medium" title={media?.path}>
          {media ? baseName(media.path) : "(missing media)"}
        </div>
        <div className="text-[11px] text-muted-foreground tabular-nums">
          {media?.kind === "image" ? "Image" : media?.kind === "audio" ? "Audio" : "Video"} · {fmtSec(c.start)} – {fmtSec(c.start + len)} ({fmtSec(len)})
        </div>
        {locked ? (
          <div className="mt-1.5 flex items-center gap-1 text-[11px] text-primary">
            <Lock className="size-3" /> The track is locked. Unlock it to edit this clip.
          </div>
        ) : null}
      </div>

      <fieldset disabled={locked} className="contents">
        {visual ? (
          <Section
            title="Transform"
            action={
              <Button size="sm" variant="ghost" className="h-6 px-1.5 text-[11px]" onClick={() => set("t.reset", { transform: { ...FIT } })} title="Fit to the canvas, centred, fully opaque">
                <RotateCcw className="size-3" /> Fit
              </Button>
            }
          >
            <Slider label="X" value={c.transform.x * 100} min={-50} max={150} step={0.5} unit="%" onChange={(v) => setT("x", v / 100)} />
            <Slider label="Y" value={c.transform.y * 100} min={-50} max={150} step={0.5} unit="%" onChange={(v) => setT("y", v / 100)} />
            <Slider label="Size" value={c.transform.scale * 100} min={2} max={400} step={1} unit="%" onChange={(v) => setT("scale", v / 100)} />
            <Slider label="Opacity" value={c.transform.opacity * 100} min={0} max={100} step={1} unit="%" onChange={(v) => setT("opacity", v / 100)} />
          </Section>
        ) : null}

        {media?.kind !== "image" ? (
          <Section title="Speed">
            <Slider
              label="Speed"
              // A log scale so 0.25x..4x has 1x in the middle.
              value={Math.log2(c.speed)}
              min={Math.log2(MIN_SPEED)}
              max={Math.log2(MAX_SPEED)}
              step={0.01}
              display={`${round(c.speed, 2)}x`}
              onChange={(v) => set("speed", { speed: round(2 ** v, 2) })}
            />
            <div className="flex flex-wrap gap-1">
              {SPEEDS.map((s) => (
                <Button
                  key={s}
                  size="sm"
                  variant={Math.abs(c.speed - s) < 1e-3 ? "secondary" : "ghost"}
                  className="h-6 px-2 text-[11px] tabular-nums"
                  onClick={() => set("speed", { speed: s })}
                >
                  {s}x
                </Button>
              ))}
            </div>
            <div className="text-[11px] text-muted-foreground">Pitch is kept. A clip that would run into the next one speeds up just enough to fit.</div>
          </Section>
        ) : null}

        {audible ? (
          <Section title="Audio">
            <div className="flex items-center justify-between gap-2">
              <span>Mute</span>
              <Switch checked={c.muted} onCheckedChange={(on) => set("muted", { muted: on })} aria-label="Mute this clip" />
            </div>
            <Slider label="Volume" value={c.volume * 100} min={0} max={200} step={1} unit="%" disabled={c.muted} onChange={(v) => set("volume", { volume: v / 100 })} />
            <Slider label="Fade in" value={c.fadeIn} min={0} max={Math.min(10, len)} step={0.05} unit="s" decimals={2} onChange={(v) => set("fadeIn", { fadeIn: v })} />
            <Slider label="Fade out" value={c.fadeOut} min={0} max={Math.min(10, len)} step={0.05} unit="s" decimals={2} onChange={(v) => set("fadeOut", { fadeOut: v })} />
            {srcTracks.length ? (
              <div className="mt-1 flex flex-col gap-1 border-t border-border/40 pt-2">
                <div className="text-[11px] text-muted-foreground">Recorded tracks</div>
                {srcTracks.map((t, i) => {
                  const l = levelOf(t.index);
                  const name = t.title || `Track ${i + 1}`;
                  return (
                    <div key={t.index} className="flex items-center gap-1.5">
                      <Button
                        size="icon"
                        variant="ghost"
                        className="size-6 shrink-0 [&_svg]:size-3.5"
                        onClick={() => setLevel(t.index, { muted: !l.muted })}
                        title={l.muted ? `Unmute ${name}` : `Mute ${name}`}
                        aria-label={l.muted ? `Unmute ${name}` : `Mute ${name}`}
                        aria-pressed={l.muted}
                      >
                        {l.muted ? <VolumeX className="text-destructive" /> : <Volume2 />}
                      </Button>
                      <span className={cn("w-16 truncate", l.muted && "text-muted-foreground line-through")} title={name}>
                        {name}
                      </span>
                      <input
                        type="range"
                        min={0}
                        max={200}
                        step={5}
                        value={l.volume}
                        disabled={l.muted}
                        onChange={(e) => setLevel(t.index, { volume: Number(e.target.value) })}
                        onDoubleClick={() => setLevel(t.index, { volume: 100 })}
                        className="min-w-0 flex-1 accent-primary disabled:opacity-40"
                        aria-label={`${name} volume`}
                      />
                      <span className="w-9 text-right tabular-nums text-muted-foreground">{l.volume}%</span>
                    </div>
                  );
                })}
              </div>
            ) : null}
          </Section>
        ) : null}

        {visual ? (
          <Section
            title="Green screen"
            action={<Switch checked={!!c.chroma} onCheckedChange={(on) => set("chroma", { chroma: on ? { ...DEFAULT_CHROMA } : undefined })} aria-label="Green screen" />}
          >
            {c.chroma ? (
              <>
                <div className="flex items-center gap-1.5">
                  <span className="w-16 shrink-0">Key colour</span>
                  <label className="relative size-6 shrink-0 cursor-pointer border border-border" style={{ background: c.chroma.color }} title="Pick a colour">
                    <input
                      type="color"
                      value={c.chroma.color}
                      onChange={(e) => setChroma("color", e.target.value)}
                      className="absolute inset-0 cursor-pointer opacity-0"
                      aria-label="Key colour"
                    />
                  </label>
                  <span className="font-mono text-[11px] text-muted-foreground">{c.chroma.color}</span>
                  <Button
                    size="sm"
                    variant={picking ? "default" : "ghost"}
                    className="ml-auto h-6 px-1.5 text-[11px]"
                    onClick={() => onPickingChange?.(!picking)}
                    disabled={!onPickingChange}
                    title={picking ? "Click the player to pick the colour (Esc cancels)" : "Pick the key colour from the player"}
                    aria-pressed={!!picking}
                  >
                    <Pipette className="size-3" /> {picking ? "Click the video…" : "Pick"}
                  </Button>
                </div>
                <Slider label="Strength" value={c.chroma.similarity * 100} min={1} max={100} step={1} unit="%" onChange={(v) => setChroma("similarity", v / 100)} />
                <Slider label="Softness" value={c.chroma.blend * 100} min={0} max={100} step={1} unit="%" onChange={(v) => setChroma("blend", v / 100)} />
              </>
            ) : (
              <div className="text-[11px] text-muted-foreground">Turn on to make one colour transparent so the tracks below show through.</div>
            )}
          </Section>
        ) : null}

        {visual ? (
          <Section
            title="Filters"
            action={
              <Button size="sm" variant="ghost" className="h-6 px-1.5 text-[11px]" onClick={() => set("f.reset", { filters: { ...DEFAULT_FILTERS } })} title="Reset the filters">
                <RotateCcw className="size-3" /> Reset
              </Button>
            }
          >
            <Segmented size="xs" aria-label="Look" value={c.filters.look} options={LOOKS} onChange={(v) => setF("look", v)} className="flex-wrap" />
            <Slider label="Brightness" value={c.filters.brightness * 100} min={-100} max={100} step={1} onChange={(v) => setF("brightness", v / 100)} />
            <Slider label="Contrast" value={c.filters.contrast * 100} min={0} max={200} step={1} unit="%" onChange={(v) => setF("contrast", v / 100)} />
            <Slider label="Saturation" value={c.filters.saturation * 100} min={0} max={300} step={1} unit="%" onChange={(v) => setF("saturation", v / 100)} />
            <Slider label="Blur" value={c.filters.blur} min={0} max={20} step={0.5} decimals={1} onChange={(v) => setF("blur", v)} />
          </Section>
        ) : null}
      </fieldset>
    </aside>
  );
}

function Section({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-1.5 border-b border-border/60 px-3 py-2.5">
      <div className="flex min-h-6 items-center justify-between gap-2">
        <h3 className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{title}</h3>
        {action}
      </div>
      {children}
    </section>
  );
}

// Slider is a labelled range with its value shown beside it.
function Slider({
  label,
  value,
  min,
  max,
  step,
  unit = "",
  decimals = 0,
  display,
  disabled,
  onChange,
}: {
  label: string;
  value: number;
  min: number;
  max: number;
  step: number;
  unit?: string;
  decimals?: number;
  display?: string;
  disabled?: boolean;
  onChange: (v: number) => void;
}) {
  return (
    <label className={cn("flex items-center gap-2", disabled && "opacity-50")}>
      <span className="w-16 shrink-0">{label}</span>
      <input
        type="range"
        min={min}
        max={max}
        step={step}
        value={Math.max(min, Math.min(max, value))}
        disabled={disabled}
        onChange={(e) => onChange(Number(e.target.value))}
        className="min-w-0 flex-1 accent-primary"
        aria-label={label}
      />
      <span className="w-12 shrink-0 text-right tabular-nums text-muted-foreground">{display ?? `${round(value, decimals)}${unit}`}</span>
    </label>
  );
}

const round = (n: number, d: number) => Math.round(n * 10 ** d) / 10 ** d;

function fmtSec(s: number): string {
  if (s >= 60) {
    const m = Math.floor(s / 60);
    return `${m}m ${(s - m * 60).toFixed(2).padStart(5, "0")}s`;
  }
  return `${s.toFixed(2)}s`;
}

function baseName(p: string): string {
  return p.replace(/\\/g, "/").split("/").pop() ?? p;
}
