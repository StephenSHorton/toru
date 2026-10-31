import { Volume2, VolumeX } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import type { TrackLevel } from "@/lib/api";
import { cn } from "@/lib/utils";
import { clipLength, updateClip, type ClipPatch } from "@/trim/project";
import { NumberRow, Section } from "@/trim/inspector/controls";
import type { TabProps } from "@/trim/inspector/Inspector";

// AudioTab: mute, volume (keyframable), fades, and the per-source-track
// levels of a clip recorded with separate tracks.

export function AudioTab({ clip: c, media, edit, keys, set, value }: TabProps) {
  const len = clipLength(c);
  const patch = (what: string, p: ClipPatch) => edit((q) => updateClip(q, c.id, p), what);

  // Separate source tracks (not the mix) for per-track levels.
  const srcTracks = media && media.tracks.length > 1 ? media.tracks.filter((t) => !t.isMix) : [];
  const levelOf = (index: number): TrackLevel =>
    c.levels?.find((l) => l.index === index) ?? ({ source: 0, index, volume: 100, muted: false } as TrackLevel);
  const setLevel = (index: number, change: Partial<Pick<TrackLevel, "volume" | "muted">>) => {
    const rows = srcTracks.map((t) => (t.index === index ? { ...levelOf(t.index), ...change } : levelOf(t.index)));
    const changed = rows.some((l) => l.volume !== 100 || l.muted);
    patch(`lv.${index}`, { levels: changed ? (rows as TrackLevel[]) : undefined });
  };

  return (
    <>
      <Section title="Volume">
        <div className="flex items-center justify-between gap-2">
          <span>Mute</span>
          <Switch checked={c.muted} onCheckedChange={(on) => patch("muted", { muted: on })} aria-label="Mute this clip" />
        </div>
        <NumberRow
          label="Volume"
          value={value({ anim: "volume" }) as number}
          min={0}
          max={2}
          step={0.01}
          def={1}
          scale={100}
          unit="%"
          disabled={c.muted}
          keyframes={keys({ anim: "volume" })}
          onChange={(v) => set({ anim: "volume" }, v)}
        />
        <NumberRow label="Fade in" value={c.fadeIn} min={0} max={Math.min(10, len)} step={0.05} def={0} unit="s" onChange={(v) => patch("fadeIn", { fadeIn: v })} />
        <NumberRow label="Fade out" value={c.fadeOut} min={0} max={Math.min(10, len)} step={0.05} def={0} unit="s" onChange={(v) => patch("fadeOut", { fadeOut: v })} />
      </Section>
      {srcTracks.length ? (
        <Section title="Recorded tracks">
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
                  title="Double-click to reset"
                />
                <span className="w-9 text-right tabular-nums text-muted-foreground">{l.volume}%</span>
              </div>
            );
          })}
        </Section>
      ) : null}
    </>
  );
}
