import { MultiInspector } from "@/trim/inspector/MultiInspector";
import { useEffect, useRef, useState } from "react";
import { AudioLines, Film, Gauge, Lock, SlidersHorizontal, Sparkles } from "lucide-react";
import { cn } from "@/lib/utils";
import { setValue, toggleKey, valueOf, type KeyRef } from "@/trim/fx/ops";
import { clipEffects } from "@/trim/fx/stack";
import { clipLength, findClip, mediaById, type Clip, type Media, type Project, type Track } from "@/trim/project";
import { baseName, fmtSec, type KeyState } from "@/trim/inspector/controls";
import { VideoTab } from "@/trim/inspector/VideoTab";
import { AdjustTab } from "@/trim/inspector/AdjustTab";
import { EffectsTab } from "@/trim/inspector/EffectsTab";
import { SpeedTab } from "@/trim/inspector/SpeedTab";
import { AudioTab } from "@/trim/inspector/AudioTab";

// Inspector is the v3 editor's right sidebar (docs/video-editor-v3.md,
// Inspector UX; layout B, docs/editor-layout-proposal.md): the selected
// clip's settings in five tabs, Video, Adjust, Effects, Speed and Audio,
// showing only the tabs that apply to the clip (audio clips: Speed and
// Audio; images: no Speed or Audio). Things you add (effect tiles, filters)
// live in the library on the left (trim/library), which edits the same
// selected clip through useClipTarget.
//
// Every change goes through `apply` (useProjectHistory) with the ops in
// fx/ops.ts. A slider drag is one undo step: each press or key on a
// control starts a new gesture, and the changes of one gesture share an
// apply key ("fx:<clip>:<what>:<gesture>"). Keyframes are at clip-local
// timeline time, playhead − clip.start.

export interface InspectorProps {
  project: Project;
  clipId: string | null; // the selected clip
  clipIds?: string[]; // every selected clip, when there are several
  apply: (fn: (p: Project) => Project, key?: string) => void;
  playhead: number; // timeline seconds
  onSeek: (t: number) => void;
  picking?: boolean; // the eyedropper is waiting for a click on the player
  onPickingChange?: (on: boolean) => void;
  // Shows a tab (the clip menu's "Speed…"); a new seq asks again.
  tabRequest?: { tab: TabId; seq: number };
  // Opens a library on the left (the Effects tab's "Add an effect").
  onOpenLibrary?: (id: "effects" | "filters") => void;
  className?: string;
}

export type TabId = "video" | "adjust" | "effects" | "speed" | "audio";

const TABS: { id: TabId; label: string; icon: typeof Film }[] = [
  { id: "video", label: "Video", icon: Film },
  { id: "adjust", label: "Adjust", icon: SlidersHorizontal },
  { id: "effects", label: "Effects", icon: Sparkles },
  { id: "speed", label: "Speed", icon: Gauge },
  { id: "audio", label: "Audio", icon: AudioLines },
];

// TabProps is what every tab gets.
export interface TabProps {
  project: Project;
  clip: Clip;
  track: Track;
  media: Media | undefined;
  t: number; // clip-local time of the playhead (clamped into the clip)
  srcT: number; // the source time shown at the playhead (for thumbnails)
  // edit applies a change; what names the control, for the undo key.
  edit: (fn: (p: Project) => Project, what: string) => void;
  keys: (ref: KeyRef) => KeyState;
  // set sets a keyframable value at the playhead.
  set: (ref: KeyRef, v: Parameters<typeof setValue>[3], what?: string) => void;
  value: (ref: KeyRef) => ReturnType<typeof valueOf>;
  picking: boolean;
  onPickingChange?: (on: boolean) => void;
  onOpenLibrary?: (id: "effects" | "filters") => void;
}

// tabsFor lists the tabs a clip has.
export function tabsFor(track: Track, media: Media | undefined): TabId[] {
  if (track.kind === "audio" || media?.kind === "audio") return ["speed", "audio"];
  if (media?.kind === "image") return ["video", "adjust", "effects"];
  return media?.hasAudio === false ? ["video", "adjust", "effects", "speed"] : ["video", "adjust", "effects", "speed", "audio"];
}

export interface ClipTargetArgs {
  project: Project;
  clipId: string | null;
  apply: (fn: (p: Project) => Project, key?: string) => void;
  playhead: number;
  onSeek: (t: number) => void;
  picking?: boolean;
  onPickingChange?: (on: boolean) => void;
  onOpenLibrary?: (id: "effects" | "filters") => void;
  // Keeps one panel's gestures apart from another's in the undo keys.
  scope?: string;
}

// useClipTarget is the selected clip as TabProps (undefined when nothing, or
// a clip that's gone, is selected), plus the handlers that start a new undo
// gesture: spread them on the element around the controls, so each press or
// key is its own step and a drag is one.
export function useClipTarget(a: ClipTargetArgs): {
  props: TabProps | undefined;
  gestureHandlers: { onPointerDownCapture: () => void; onKeyDownCapture: () => void };
} {
  const gesture = useRef(0);
  const gestureHandlers = {
    onPointerDownCapture: () => void gesture.current++,
    onKeyDownCapture: () => void gesture.current++,
  };
  const hit = a.clipId ? findClip(a.project, a.clipId) : undefined;
  if (!hit) return { props: undefined, gestureHandlers };
  const { clip: c, track } = hit;
  const media = mediaById(a.project, c.media);
  const t = Math.max(0, Math.min(clipLength(c), a.playhead - c.start));
  const edit = (fn: (p: Project) => Project, what: string) => a.apply(fn, `fx:${a.scope ?? ""}${c.id}:${what}:${gesture.current}`);
  const refName = (ref: KeyRef) => ("anim" in ref ? `anim.${ref.anim}` : `${ref.fx}.${ref.param}`);
  return {
    gestureHandlers,
    props: {
      project: a.project,
      clip: c,
      track,
      media,
      t,
      srcT: c.hold ? c.in : Math.min(c.out, c.in + t * c.speed),
      edit,
      keys: (ref) => ({
        keys: "anim" in ref ? c.anim?.[ref.anim] : clipEffects(c).find((e) => e.id === ref.fx)?.keys?.[ref.param],
        t,
        onToggle: () => edit((p) => toggleKey(p, c.id, ref, t), `key.${refName(ref)}`),
        onJump: (kt) => a.onSeek(c.start + kt),
      }),
      set: (ref, v, what) => edit((p) => setValue(p, c.id, ref, v, t), what ?? refName(ref)),
      value: (ref) => valueOf(c, ref, t),
      picking: !!a.picking,
      onPickingChange: a.onPickingChange,
      onOpenLibrary: a.onOpenLibrary,
    },
  };
}

export function Inspector(props: InspectorProps) {
  if (props.clipIds && props.clipIds.length > 1) {
    return <MultiInspector project={props.project} clipIds={props.clipIds} apply={props.apply} className={props.className} />;
  }
  return <OneClipInspector {...props} />;
}

function OneClipInspector({ project, clipId, apply, playhead, onSeek, picking, onPickingChange, tabRequest, onOpenLibrary, className }: InspectorProps) {
  const [tab, setTab] = useState<TabId>(() => readTab());
  const asked = tabRequest?.seq ?? 0;
  useEffect(() => {
    if (tabRequest) setTab(tabRequest.tab);
    // Only a new request switches; the tab is the person's after that.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [asked]);
  const { props, gestureHandlers } = useClipTarget({ project, clipId, apply, playhead, onSeek, picking, onPickingChange, onOpenLibrary });
  if (!props) {
    return (
      <aside className={cn("flex flex-col gap-2 p-3 text-xs text-muted-foreground", className)}>
        Select a clip on the timeline or the player to edit it.
      </aside>
    );
  }
  const { clip: c, track, media } = props;
  const len = clipLength(c);
  const tabs = tabsFor(track, media);
  const shown = tabs.includes(tab) ? tab : tabs[0];
  const choose = (id: TabId) => {
    setTab(id);
    try {
      localStorage.setItem(TAB_KEY, id);
    } catch {
      // not remembered; fine
    }
  };

  return (
    <aside
      className={cn("flex min-h-0 flex-col text-xs", className)}
      {...gestureHandlers}
      aria-label="Clip settings"
    >
      <div className="border-b border-border/60 px-3 py-2">
        <div className="truncate text-sm font-medium" title={media?.path}>
          {media ? baseName(media.path) : "(missing media)"}
        </div>
        <div className="text-[11px] text-muted-foreground tabular-nums">
          {media?.kind === "image" ? "Image" : media?.kind === "audio" ? "Audio" : "Video"} · {fmtSec(c.start)} – {fmtSec(c.start + len)} ({fmtSec(len)})
        </div>
        {track.locked ? (
          <div className="mt-1.5 flex items-center gap-1 text-[11px] text-primary">
            <Lock className="size-3" /> The track is locked. Unlock it to edit this clip.
          </div>
        ) : null}
      </div>

      <div role="tablist" aria-label="Clip settings" className="grid shrink-0 border-b border-border/60" style={{ gridTemplateColumns: `repeat(${tabs.length}, minmax(0, 1fr))` }}>
        {TABS.filter((x) => tabs.includes(x.id)).map((x) => {
          const Icon = x.icon;
          const on = x.id === shown;
          return (
            <button
              key={x.id}
              type="button"
              role="tab"
              aria-selected={on}
              onClick={() => choose(x.id)}
              className={cn(
                "flex flex-col items-center gap-0.5 border-b-2 px-0.5 pb-1.5 pt-2 text-[10.5px] transition-colors",
                on ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              <Icon className="size-3.5" />
              {x.label}
            </button>
          );
        })}
      </div>

      <fieldset disabled={track.locked} className="min-h-0 min-w-0 flex-1 overflow-y-auto overflow-x-hidden" role="tabpanel">
        {shown === "video" ? <VideoTab {...props} /> : null}
        {shown === "adjust" ? <AdjustTab {...props} /> : null}
        {shown === "effects" ? <EffectsTab {...props} /> : null}
        {shown === "speed" ? <SpeedTab {...props} /> : null}
        {shown === "audio" ? <AudioTab {...props} /> : null}
      </fieldset>
    </aside>
  );
}

const TAB_KEY = "toru.editor.inspectorTab";

function readTab(): TabId {
  try {
    const v = localStorage.getItem(TAB_KEY);
    if (v && TABS.some((x) => x.id === v)) return v as TabId;
  } catch {
    // no storage
  }
  return "video";
}
