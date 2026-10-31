import { useRef } from "react";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import { setClipsMuted, updateClips } from "@/trim/multi";
import { NumberRow, Section } from "@/trim/inspector/controls";
import { clipLength, findClip, mediaById, type Clip, type Project } from "@/trim/project";

// MultiInspector is the inspector with several clips selected: what they
// share, changed on all of them at once (one undo step per gesture). A
// value that differs between them shows the first clip's, marked "mixed".
// Clips on locked tracks are left as they are.

export function MultiInspector({
  project: p,
  clipIds,
  apply,
  className,
}: {
  project: Project;
  clipIds: string[];
  apply: (fn: (p: Project) => Project, key?: string) => void;
  className?: string;
}) {
  // One undo step per press-and-drag: a new key each time a control is pressed.
  const gesture = useRef(0);
  const hits = clipIds.map((id) => findClip(p, id)).filter((h) => !!h);
  const clips = hits.map((h) => h.clip);
  if (!clips.length) return null;
  const visual = hits.filter((h) => h.track.kind === "video");
  const sound = hits.filter((h) => {
    const m = mediaById(p, h.clip.media);
    return !h.clip.hold && !!m && (m.kind === "audio" || (m.kind === "video" && m.hasAudio));
  });
  const locked = hits.filter((h) => h.track.locked).length;
  const ids = hits.map((h) => h.clip.id);
  const total = clips.reduce((s, c) => s + clipLength(c), 0);
  const edit = (what: string, fn: (q: Project) => Project) => apply(fn, `multi:${what}:${gesture.current}`);

  const shared = (of: Clip[], get: (c: Clip) => number) => {
    const first = of.length ? get(of[0]) : 0;
    return { value: first, mixed: of.some((c) => Math.abs(get(c) - first) > 1e-6) };
  };
  const opacity = shared(
    visual.map((h) => h.clip),
    (c) => c.transform.opacity,
  );
  const volume = shared(
    sound.map((h) => h.clip),
    (c) => c.volume,
  );
  const fadeIn = shared(clips, (c) => c.fadeIn);
  const fadeOut = shared(clips, (c) => c.fadeOut);
  const shortest = Math.min(...clips.map(clipLength));
  const allMuted = sound.length > 0 && sound.every((h) => h.clip.muted);
  const someMuted = sound.some((h) => h.clip.muted);
  const keyed = (c: Clip, k: "opacity" | "volume") => !!c.anim?.[k]?.length;

  return (
    <aside
      className={cn("flex min-h-0 flex-col text-xs", className)}
      aria-label="Settings for the selected clips"
      onPointerDownCapture={() => gesture.current++}
      onKeyDownCapture={() => gesture.current++}
    >
      <div className="border-b border-border/60 px-3 py-2">
        <div className="text-sm font-medium">{clips.length} clips selected</div>
        <div className="text-[11px] tabular-nums text-muted-foreground">
          {visual.length} video or image · {hits.length - visual.length} audio · {total.toFixed(2)}s in all
        </div>
        {locked ? <div className="mt-1.5 text-[11px] text-primary">{locked === 1 ? "1 clip is" : `${locked} clips are`} on a locked track and won't change.</div> : null}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {visual.length ? (
          <Section title="Video">
            <NumberRow
              label={opacity.mixed ? "Opacity (mixed)" : "Opacity"}
              value={opacity.value}
              min={0}
              max={1}
              step={0.01}
              def={1}
              scale={100}
              unit="%"
              hint={visual.some((h) => keyed(h.clip, "opacity")) ? "Some clips have opacity keyframes, which take over from this value" : undefined}
              onChange={(v) =>
                edit("opacity", (q) =>
                  updateClips(
                    q,
                    visual.map((h) => h.clip.id),
                    (c) => ({ transform: { ...c.transform, opacity: v } }),
                  ),
                )
              }
            />
          </Section>
        ) : null}
        {sound.length ? (
          <Section title="Volume">
            <div className="flex items-center justify-between gap-2">
              <span>{someMuted && !allMuted ? "Mute (some are)" : "Mute"}</span>
              <Switch
                checked={allMuted}
                onCheckedChange={(on) =>
                  edit(
                    "muted",
                    (q) =>
                      setClipsMuted(
                        q,
                        sound.map((h) => h.clip.id),
                        on,
                      ),
                  )
                }
                aria-label="Mute the selected clips"
              />
            </div>
            <NumberRow
              label={volume.mixed ? "Volume (mixed)" : "Volume"}
              value={volume.value}
              min={0}
              max={2}
              step={0.01}
              def={1}
              scale={100}
              unit="%"
              disabled={allMuted}
              hint={sound.some((h) => keyed(h.clip, "volume")) ? "Some clips have volume keyframes, which take over from this value" : undefined}
              onChange={(v) =>
                edit("volume", (q) =>
                  updateClips(
                    q,
                    sound.map((h) => h.clip.id),
                    { volume: v },
                  ),
                )
              }
            />
          </Section>
        ) : null}
        <Section title="Fades">
          <NumberRow
            label={fadeIn.mixed ? "In (mixed)" : "Fade in"}
            value={fadeIn.value}
            min={0}
            max={Math.min(10, shortest)}
            step={0.05}
            def={0}
            unit="s"
            onChange={(v) => edit("fadeIn", (q) => updateClips(q, ids, { fadeIn: v }))}
          />
          <NumberRow
            label={fadeOut.mixed ? "Out (mixed)" : "Fade out"}
            value={fadeOut.value}
            min={0}
            max={Math.min(10, shortest)}
            step={0.05}
            def={0}
            unit="s"
            onChange={(v) => edit("fadeOut", (q) => updateClips(q, ids, { fadeOut: v }))}
          />
        </Section>
        <p className="px-3 py-2.5 text-[11px] text-muted-foreground">
          Drag a selected clip to move them all. Delete, Ctrl+C, Ctrl+X and Ctrl+Alt+V (paste a copied clip's look and sound) work on the whole selection. Ctrl+click a clip to add or remove it.
        </p>
      </div>
    </aside>
  );
}
