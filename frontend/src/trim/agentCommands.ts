import { useEffect, useRef, type MutableRefObject } from "react";
import { Window } from "@wailsio/runtime";
import {
  ArrowLeftRight,
  ArrowLeftToLine,
  AudioLines,
  Copy,
  Crop,
  Crosshair,
  Download,
  EyeOff,
  FileUp,
  Gauge,
  Lock,
  PanelRight,
  Play,
  Plus,
  Redo2,
  RotateCcw,
  Scissors,
  Trash2,
  Type,
  Undo2,
  VolumeX,
} from "lucide-react";
import { registerAgentCommand, type AgentHandler } from "@/lib/agent";
import { commandActions, provideActionState, registerAction, type Action, type ActionCtx, type EditorCommandSpec } from "@/trim/actions";
import { tabsFor, type TabId } from "@/trim/inspector/Inspector";
import { LIBRARIES, type LibraryId } from "@/trim/library/Library";
import { VideoService } from "@/lib/api";
import { ProjectExport } from "../../bindings/github.com/StephenSHorton/toru/internal/vid/models.js";
import { registerTimeCommands } from "@/trim/agentCommandsTime";
import type { CompositorHandle } from "@/trim/compositor/Compositor";
import { registerFxCommands } from "@/trim/agentCommandsFx";
import { CANVAS_PRESETS, MAX_CANVAS, MIN_CANVAS, normCanvas, placeClip, setCanvas, type ClipPlace, type Placement } from "@/trim/canvas";
import { registerMultiCommands } from "@/trim/agentCommandsMulti";
import { newId, type Text } from "@/trim/edl";
import { checkFont, DEFAULT_FONT_LABEL, fontInfo, loadFonts } from "@/trim/fonts";
import type { MediaBinState } from "@/trim/MediaBin";
import type { TimelineSelection } from "@/trim/MultiTimeline";
import { MAX_FONT_PX, MIN_FONT_PX, textFontSize } from "@/trim/TextOverlays";
import { addTransition, cutFor, DEFAULT_TRANSITION, isTransitionType, MAX_TRANSITION, MIN_TRANSITION, setClipTransition, TRANSITIONS, transitionLabel, type TransitionType } from "@/trim/transition";
import {
  addMedia,
  addTrack,
  appendClip,
  clipEnd,
  closeGap,
  DEFAULT_CHROMA,
  findClip,
  insertClip,
  mediaById,
  moveClip,
  projectDuration,
  removeClip,
  removeProjectText,
  removeTrack,
  rippleDelete,
  setCrop,
  setTrackFlags,
  splitAt,
  toExportRequest,
  type ExportSettings,
  trackById,
  trimClip,
  updateClip,
  upsertProjectText,
  type Clip,
  type ClipPatch,
  type Media,
  type Project,
  canExtractAudio,
  duplicateClip,
  extractAudio,
} from "@/trim/project";
import { soundExportRequest } from "@/trim/saveAudio";

// The video editor's agent commands (docs/agent-control.md, "Editor"): every
// tool of the Edit video window, for AI agents and scripts. Each edit goes
// through the window's undo history (apply), so the person sees it happen and
// can undo it; drags become single calls with exact values. Times are
// seconds on the timeline unless a name says otherwise.

export interface EditorAgentDeps {
  project: Project;
  apply: (fn: (p: Project) => Project, key?: string) => void;
  undo: () => void;
  redo: () => void;
  canUndo: boolean;
  canRedo: boolean;
  pb: { time: number; playing: boolean; play: () => void; pause: () => void; toggle?: () => void; seek: (t: number) => void; clock: { now(): number } };
  bin: MediaBinState;
  compositor: MutableRefObject<CompositorHandle | null>;
  selection: TimelineSelection;
  setSelection: (s: TimelineSelection) => void;
  firstPath: string;
  // The window's own ways to do what an agent passes arguments for (a dialog
  // instead of a path, the text editor instead of words); the menu and
  // palette items that need them are left out without them.
  ui?: EditorUiDeps;
}

export interface EditorUiDeps {
  saveAudio: (clipId: string) => void;
  addText: (t?: number) => void;
  openExport: () => void;
  canExport: true | string; // or why not
  openInspector: (clipId: string, tab?: TabId) => void;
  cropping: boolean;
  toggleCrop: () => void;
  openLibrary: (id: LibraryId) => void;
}

// The editor window's commands take an optional window name; Go routes the
// call (the agent gets names from editor.get).
const windowProp = {
  window: { type: "string", description: "Which Edit video window (editor.get's window). Default: the one used last." },
};

const obj = (props: Record<string, unknown> = {}, required: string[] = []) => ({
  type: "object",
  properties: { ...props, ...windowProp },
  ...(required.length ? { required } : {}),
});

const num = (description: string, extra: Record<string, unknown> = {}) => ({ type: "number", description, ...extra });
const str = (description: string, extra: Record<string, unknown> = {}) => ({ type: "string", description, ...extra });
const bool = (description: string) => ({ type: "boolean", description });

const transformSchema = {
  type: "object",
  description: "Where the clip sits on the canvas. x, y: its centre, 0..1 of the canvas. scale: 1 fits the whole picture inside the canvas. opacity 0..1.",
  properties: { x: num("0..1"), y: num("0..1"), scale: num("> 0"), opacity: num("0..1") },
};
const filtersSchema = {
  type: "object",
  description: "Colour and blur. brightness -1..1 (0), contrast 0..2 (1), saturation 0..3 (1), look, blur 0..20 (0).",
  properties: {
    brightness: num("-1..1"),
    contrast: num("0..2"),
    saturation: num("0..3"),
    look: str("A colour look.", { enum: ["none", "bw", "sepia", "vintage", "cool", "warm"] }),
    blur: num("0..20"),
  },
};
const chromaSchema = {
  description: "Green screen: true turns it on with pure green, false turns it off, or give { color: \"#rrggbb\", similarity 0.01..1, blend 0..1 }.",
  oneOf: [
    { type: "boolean" },
    { type: "object", properties: { color: str("#rrggbb"), similarity: num("0.01..1"), blend: num("0..1") } },
  ],
};

// useEditorAgentCommands registers the editor's commands while the window is
// open. deps is read fresh on every call (a ref), so handlers always see the
// current project and playhead.
export function useEditorAgentCommands(deps: EditorAgentDeps) {
  const ref = useRef(deps);
  ref.current = deps;

  useEffect(() => {
    const d = () => ref.current;
    const offs: (() => void)[] = [];
    // reg offers a command to agents and its ui (if any) to the palette, the
    // menus and the keyboard (trim/actions.ts).
    const reg = (name: string, spec: EditorCommandSpec, handler: AgentHandler) => {
      const { ui: _ui, ...agentSpec } = spec;
      offs.push(registerAgentCommand(name, agentSpec, handler));
      for (const a of commandActions(name, spec, handler)) offs.push(registerAction(a));
    };
    provideActionState(() => ({ project: d().project, playhead: d().pb.clock.now(), playing: d().pb.playing, selection: d().selection }));
    offs.push(() => provideActionState(null));

    // edit applies fn as one undo step and returns the new project. It fails
    // with why when fn changed nothing, so an agent never thinks a no-op
    // worked.
    const edit = (fn: (p: Project) => Project, why: string): Project => {
      const base = d().project;
      const next = fn(base);
      if (next === base) throw new Error(why);
      // Same result as next unless something changed the project meanwhile.
      d().apply((p) => (p === base ? next : fn(p)));
      return next;
    };
    const clipOr = (p: Project, clipId: string) => {
      const hit = findClip(p, String(clipId ?? ""));
      if (!hit) throw new Error(`no clip "${clipId}" (editor.get lists them)`);
      return hit;
    };
    const trackOr = (p: Project, trackId: string) => {
      const t = trackById(p, String(trackId ?? ""));
      if (!t) throw new Error(`no track "${trackId}" (editor.get lists them)`);
      return t;
    };
    const time = (v: unknown, name: string, fallback?: number): number => {
      if (v === undefined || v === null) {
        if (fallback !== undefined) return fallback;
        throw new Error(`${name} is required (seconds)`);
      }
      const n = Number(v);
      if (!Number.isFinite(n) || n < 0) throw new Error(`${name} must be seconds >= 0`);
      return n;
    };
    const lockedNote = (locked: boolean) => (locked ? " (the track is locked: unlock it with editor.setTrackFlags)" : "");
    const newClips = (before: Project, after: Project): Clip[] => {
      const had = new Set(before.tracks.flatMap((t) => t.clips.map((c) => c.id)));
      return after.tracks.flatMap((t) => t.clips.filter((c) => !had.has(c.id)));
    };
    const selectClip = (id?: string) => id && d().setSelection({ kind: "clip", id });

    // ---- how the commands look to the person (menus, palette, keys) --------
    // Each enabled() says why an item is greyed out; the target comes from
    // the menu, or from the selection and the playhead (actions.ts).
    const LOCKED = "The track is locked";
    const PICK_TRACK = "Right-click a track, or select a clip on it";
    const hitOf = (c: ActionCtx) => (c.clipId ? findClip(c.project, c.clipId) : undefined);
    const trackOf = (c: ActionCtx) => (c.trackId ? trackById(c.project, c.trackId) : undefined);
    const clipOk = (c: ActionCtx): true | string => {
      const hit = hitOf(c);
      if (!hit) return "Select a clip first";
      return hit.track.locked ? LOCKED : true;
    };
    const hasSound = (p: Project, clip: Clip) => {
      const m = mediaById(p, clip.media);
      return !!m && !clip.hold && (m.kind === "audio" || (m.kind === "video" && m.hasAudio));
    };
    const soundOk = (c: ActionCtx): true | string => {
      const ok = clipOk(c);
      if (ok !== true) return ok;
      return hasSound(c.project, hitOf(c)!.clip) ? true : "This clip has no sound";
    };
    // gapTrack: the track with a gap at c.t to close (the target's first).
    const gapTrack = (c: ActionCtx) => {
      const order = c.trackId ? [trackOf(c)].filter((t) => !!t) : c.project.tracks;
      return order.find((t) => closeGap(c.project, t.id, c.t) !== c.project);
    };
    const noUi = () => !d().ui;

    // ---- read -------------------------------------------------------------

    reg(
      "editor.get",
      {
        description:
          "The open edit: canvas, media, tracks (video bottom to top, then audio) with their clips (timeline start/end and source in/out in seconds, speed, volume, transform, effects with their ids, params and keyframes), texts, crop, playhead, and whether undo/redo are possible.",
        input: obj(),
      },
      async () => {
        const { project: p, pb, canUndo, canRedo } = d();
        return {
          window: await Window.Name().catch(() => ""),
          duration: projectDuration(p),
          playhead: pb.clock.now(),
          playing: pb.playing,
          canUndo,
          canRedo,
          canvas: p.canvas,
          media: p.media.map(({ id, path, kind, duration, width, height, fps, hasAudio, tracks }) => ({ id, path, kind, duration, width, height, fps, hasAudio, audioTracks: tracks })),
          tracks: p.tracks.map((t) => ({ ...t, clips: t.clips.map((c) => ({ ...c, end: clipEnd(c) })) })),
          // fontPx is filled in for older texts that only have a preset size.
          texts: p.texts.map((t) => ({ ...t, fontPx: textFontSize(t, p.crop?.h ?? p.canvas.height), font: t.font ?? "" })),
          crop: p.crop,
        };
      },
    );

    reg(
      "editor.frame",
      {
        description: "A PNG of the preview at time t (default: the playhead), as the player shows it (texts and crop aren't drawn). Seeks the player there.",
        input: obj({ t: num("Seconds on the timeline."), maxWidth: num("Scale down to this width (default 960).") }),
      },
      async (input: { t?: number; maxWidth?: number }) => {
        const { pb, compositor } = d();
        if (input.t !== undefined) {
          pb.pause();
          pb.seek(time(input.t, "t"));
        }
        const handle = compositor.current;
        if (!handle?.snapshot) throw new Error("the preview isn't ready yet");
        // Give the players a moment to land on the new time.
        await settle();
        const shot = await handle.snapshot();
        const maxW = Math.max(64, Math.min(3840, Number(input.maxWidth) || 960));
        const scale = Math.min(1, maxW / shot.width);
        const out = document.createElement("canvas");
        out.width = Math.max(1, Math.round(shot.width * scale));
        out.height = Math.max(1, Math.round(shot.height * scale));
        out.getContext("2d")!.drawImage(shot, 0, 0, out.width, out.height);
        const b64 = out.toDataURL("image/png").replace(/^data:image\/png;base64,/, "");
        return { t: pb.clock.now(), width: out.width, height: out.height, image: { mime: "image/png", base64: b64 } };
      },
    );

    reg(
      "editor.media",
      {
        description: "Media in the edit (ids for editor.insertClip / appendClip) and in the media bin: the Library (clips, recordings, screenshots) and files imported this session.",
        input: obj(),
      },
      () => {
        const { project: p, bin } = d();
        const brief = (m: Media) => ({ id: m.id, path: m.path, kind: m.kind, duration: m.duration, width: m.width, height: m.height });
        return {
          project: p.media.map(brief),
          library: (bin.library ?? []).map((it) => ({ ...brief(it.media), name: it.name, group: it.group })),
          imported: bin.imported.map((it) => ({ ...brief(it.media), name: it.name })),
        };
      },
    );

    // ---- media and tracks -------------------------------------------------

    reg(
      "editor.addMedia",
      {
        description:
          "Add files (video: mp4 mkv mov webm; audio: mp3 wav m4a aac ogg flac; images: png jpg webp gif) to the edit by absolute path. They're used where they are (nothing is copied). With append, each goes to the end of the bottom video track (audio: the first audio track). Returns their media ids.",
        input: obj({ paths: { type: "array", items: { type: "string" }, minItems: 1 }, append: bool("Also put each on the timeline at the end.") }, ["paths"]),
        // The person picks files instead of passing paths.
        ui: { id: "media.import", label: "Import media…", group: "Insert", icon: FileUp, keywords: "add files open video audio image", scopes: ["gap"], run: () => d().bin.pick() },
      },
      async (input: { paths: string[]; append?: boolean }) => {
        const paths = Array.isArray(input.paths) ? input.paths.map(String) : [];
        if (!paths.length) throw new Error("paths is required: absolute file paths");
        const items = await d().bin.importPaths(paths);
        if (!items.length) throw new Error("none of those files could be read (unsupported type, or missing)");
        const media = items.map((it) => it.media);
        const before = d().project;
        const after = edit((p) => {
          let q = p;
          for (const m of media) {
            q = addMedia(q, m);
            if (input.append) q = appendClip(q, m.id);
          }
          return q;
        }, "those files are already in the edit");
        const clips = newClips(before, after);
        selectClip(clips.at(-1)?.id);
        return {
          media: media.map((m) => ({ id: m.id, kind: m.kind, path: m.path, duration: m.duration })),
          skipped: paths.length - media.length,
          clips: clips.map((c) => c.id),
        };
      },
    );

    reg(
      "editor.addTrack",
      {
        description: "Add a video track (drawn on top of the others) or an audio track. Returns its id.",
        input: obj({ kind: str("Track kind.", { enum: ["video", "audio"] }) }, ["kind"]),
        ui: [
          { id: "track.addVideo", label: "Add video track", group: "Track", icon: Plus, order: 10, scopes: ["track"], args: () => ({ kind: "video" }) },
          { id: "track.addAudio", label: "Add audio track", group: "Track", icon: Plus, order: 11, scopes: ["track"], args: () => ({ kind: "audio" }) },
        ],
      },
      (input: { kind: "video" | "audio" }) => {
        if (input.kind !== "video" && input.kind !== "audio") throw new Error('kind must be "video" or "audio"');
        const before = d().project;
        const after = edit((p) => addTrack(p, input.kind), "couldn't add a track");
        const added = after.tracks.find((t) => !before.tracks.some((b) => b.id === t.id));
        return { trackId: added?.id };
      },
    );

    reg(
      "editor.removeTrack",
      {
        description: "Remove a track and every clip on it.",
        input: obj({ trackId: str("From editor.get.") }, ["trackId"]),
        ui: {
          id: "track.delete",
          label: "Delete track",
          group: "Track",
          icon: Trash2,
          order: 20,
          destructive: true,
          scopes: ["track"],
          enabled: (c) => {
            const t = trackOf(c);
            if (!t) return PICK_TRACK;
            if (t.locked) return LOCKED;
            return removeTrack(c.project, t.id) !== c.project ? true : "The last video track can't be deleted";
          },
          hint: () => "Its clips go too",
          args: (c) => ({ trackId: c.trackId }),
        },
      },
      (input: { trackId: string }) => {
        const t = trackOr(d().project, input.trackId);
        edit((p) => removeTrack(p, t.id), "couldn't remove that track" + lockedNote(t.locked) + " (a project keeps at least one track of each kind)");
        return { removed: t.id };
      },
    );

    reg(
      "editor.setTrackFlags",
      {
        description: "Mute (no audio), hide (no picture) or lock (no edits) a track.",
        input: obj({ trackId: str("From editor.get."), muted: bool("No audio."), hidden: bool("No picture."), locked: bool("No edits.") }, ["trackId"]),
        ui: [
          {
            id: "track.mute",
            label: (c) => (trackOf(c)?.muted ? "Unmute track" : "Mute track"),
            group: "Track",
            icon: VolumeX,
            order: 1,
            scopes: ["track"],
            enabled: (c) => (trackOf(c) ? true : PICK_TRACK),
            args: (c) => ({ trackId: c.trackId, muted: !trackOf(c)?.muted }),
          },
          {
            id: "track.hide",
            label: (c) => (trackOf(c)?.hidden ? "Show track" : "Hide track"),
            group: "Track",
            icon: EyeOff,
            order: 2,
            scopes: ["track"],
            hidden: (c) => trackOf(c)?.kind === "audio",
            enabled: (c) => (trackOf(c) ? true : PICK_TRACK),
            args: (c) => ({ trackId: c.trackId, hidden: !trackOf(c)?.hidden }),
          },
          {
            id: "track.lock",
            label: (c) => (trackOf(c)?.locked ? "Unlock track" : "Lock track"),
            group: "Track",
            icon: Lock,
            order: 3,
            scopes: ["track"],
            enabled: (c) => (trackOf(c) ? true : PICK_TRACK),
            hint: (c) => (trackOf(c)?.locked ? undefined : "No edits until it's unlocked"),
            args: (c) => ({ trackId: c.trackId, locked: !trackOf(c)?.locked }),
          },
        ],
      },
      (input: { trackId: string; muted?: boolean; hidden?: boolean; locked?: boolean }) => {
        const t = trackOr(d().project, input.trackId);
        const flags: { muted?: boolean; hidden?: boolean; locked?: boolean } = {};
        for (const k of ["muted", "hidden", "locked"] as const) if (typeof input[k] === "boolean") flags[k] = input[k];
        const after = edit((p) => setTrackFlags(p, t.id, flags), "the track already has those flags");
        const nt = trackById(after, t.id)!;
        return { trackId: t.id, muted: nt.muted, hidden: nt.hidden, locked: nt.locked };
      },
    );

    // ---- clips --------------------------------------------------------------

    reg(
      "editor.insertClip",
      {
        description:
          "Put a whole media on a track at time `at` (the nearest free spot if something is there). Video and images go on video tracks; audio, and the audio of a video, on audio tracks. Clips on higher video tracks start at 40% size, centred. Returns the clip id.",
        input: obj({ trackId: str("From editor.get."), mediaId: str("From editor.media or editor.addMedia."), at: num("Seconds on the timeline.") }, ["trackId", "mediaId", "at"]),
      },
      (input: { trackId: string; mediaId: string; at: number }) => {
        const p0 = d().project;
        const t = trackOr(p0, input.trackId);
        const m = mediaById(p0, input.mediaId) ?? d().bin.byId(String(input.mediaId));
        if (!m) throw new Error(`no media "${input.mediaId}" (editor.media lists it; editor.addMedia adds files)`);
        const at = time(input.at, "at");
        const after = edit((p) => insertClip(addMedia(p, m), t.id, m.id, at), `a ${m.kind} can't go on that ${t.kind} track` + lockedNote(t.locked));
        const c = newClips(p0, after)[0];
        selectClip(c?.id);
        return { clipId: c?.id, start: c?.start, end: c ? clipEnd(c) : undefined };
      },
    );

    reg(
      "editor.appendClip",
      {
        description: "Put a whole media at the end of the bottom video track (audio: the first audio track). Returns the clip id.",
        input: obj({ mediaId: str("From editor.media or editor.addMedia.") }, ["mediaId"]),
      },
      (input: { mediaId: string }) => {
        const p0 = d().project;
        const m = mediaById(p0, input.mediaId) ?? d().bin.byId(String(input.mediaId));
        if (!m) throw new Error(`no media "${input.mediaId}"`);
        const after = edit((p) => appendClip(addMedia(p, m), m.id), "couldn't append it (is the track locked?)");
        const c = newClips(p0, after)[0];
        selectClip(c?.id);
        return { clipId: c?.id, start: c?.start, end: c ? clipEnd(c) : undefined };
      },
    );

    reg(
      "editor.moveClip",
      {
        description: "Move a clip to `start` on its track or another track of the same kind (the nearest free spot).",
        input: obj({ clipId: str("From editor.get."), start: num("Seconds on the timeline."), trackId: str("Default: the clip's track.") }, ["clipId", "start"]),
      },
      (input: { clipId: string; start: number; trackId?: string }) => {
        const hit = clipOr(d().project, input.clipId);
        const to = input.trackId ? trackOr(d().project, input.trackId) : hit.track;
        const after = edit((p) => moveClip(p, hit.clip.id, to.id, time(input.start, "start")), "it's already there, or the track is locked or of the other kind");
        const c = findClip(after, hit.clip.id)!;
        selectClip(c.clip.id);
        return { clipId: c.clip.id, trackId: c.track.id, start: c.clip.start, end: clipEnd(c.clip) };
      },
    );

    reg(
      "editor.trimClip",
      {
        description: "Move a clip's start or end edge to timeline time t (an image can be stretched; a video only within its file).",
        input: obj({ clipId: str("From editor.get."), edge: str("Which edge.", { enum: ["start", "end"] }), t: num("Seconds on the timeline.") }, ["clipId", "edge", "t"]),
      },
      (input: { clipId: string; edge: "start" | "end"; t: number }) => {
        const hit = clipOr(d().project, input.clipId);
        if (input.edge !== "start" && input.edge !== "end") throw new Error('edge must be "start" or "end"');
        const after = edit((p) => trimClip(p, hit.clip.id, input.edge, time(input.t, "t")), "that trim changes nothing (past the file's end, too short, or locked)");
        const c = findClip(after, hit.clip.id)!.clip;
        return { clipId: c.id, start: c.start, end: clipEnd(c), in: c.in, out: c.out };
      },
    );

    reg(
      "editor.split",
      {
        description: "Cut the clips under time t (default: the playhead) in two, on one track or every track. Returns the new right-hand clips' ids.",
        input: obj({ t: num("Seconds on the timeline."), trackId: str("Only this track.") }),
        // From a clip's menu, that clip's track; else the selected clip's
        // track, or every track.
        ui: {
          id: "clip.split",
          label: "Split at playhead",
          group: "Edit",
          icon: Scissors,
          shortcut: "S",
          keywords: "cut razor",
          scopes: ["clip", "viewer"],
          enabled: (c) => {
            const hit = c.from === "menu" ? hitOf(c) : undefined;
            if (hit) {
              if (hit.track.locked) return LOCKED;
              const under = c.playhead >= hit.clip.start - 1e-6 && c.playhead < clipEnd(hit.clip) - 1e-6;
              return under && findClip(splitAt(c.project, c.playhead, hit.track.id), hit.clip.id)?.clip !== hit.clip ? true : "Move the playhead inside the clip";
            }
            return splitAt(c.project, c.playhead, c.trackId) !== c.project ? true : "Move the playhead over a clip";
          },
          args: (c) => ({ t: c.playhead, trackId: c.trackId }),
        },
      },
      (input: { t?: number; trackId?: string }) => {
        const p0 = d().project;
        const t = time(input.t, "t", d().pb.clock.now());
        if (input.trackId) trackOr(p0, input.trackId);
        const after = edit((p) => splitAt(p, t, input.trackId), `no clip to split at ${t.toFixed(2)}s (too close to an edge, or none there)`);
        const right = newClips(p0, after);
        selectClip(right[0]?.id);
        return { t, newClips: right.map((c) => c.id) };
      },
    );

    // The Delete keys delete a selected text or transition too.
    for (const [name, op, what, ui] of [
      ["editor.removeClip", removeClip, "Delete a clip and leave a gap.", { id: "clip.delete", label: "Delete", shortcut: "Del", keys: ["Del", "Backspace"], hint: "Leaves a gap" }],
      [
        "editor.rippleDelete",
        rippleDelete,
        "Delete a clip and pull the later clips on its track left (other tracks don't move).",
        { id: "clip.rippleDelete", label: "Ripple delete", shortcut: "Shift+Del", keys: ["Shift+Del", "Shift+Backspace"], hint: "Pulls the rest of the track left" },
      ],
    ] as const) {
      const handler = (input: { clipId: string }) => {
        const hit = clipOr(d().project, input.clipId);
        edit((p) => op(p, hit.clip.id), "couldn't delete it" + lockedNote(hit.track.locked));
        d().setSelection(null);
        return { removed: hit.clip.id };
      };
      reg(
        name,
        {
          description: what,
          input: obj({ clipId: str("From editor.get.") }, ["clipId"]),
          ui: {
            ...ui,
            clips: "one",
            group: "Clip",
            icon: Trash2,
            order: 10,
            destructive: true,
            keywords: "remove",
            scopes: ["clip"],
            enabled: (c) => (!c.clipId && (c.textId || c.transitionId) ? true : clipOk(c)),
            hint: (c) => (!c.clipId && c.textId ? "Deletes the selected text" : !c.clipId && c.transitionId ? "Removes the selected transition" : ui.hint),
            run: (c) => {
              if (c.clipId) return handler({ clipId: c.clipId });
              if (c.transitionId) {
                const tid = c.transitionId;
                d().apply((p) => setClipTransition(p, tid, undefined));
                d().setSelection(null);
                return;
              }
              if (!c.textId) return;
              const id = c.textId;
              d().apply((p) => removeProjectText(p, id));
              d().setSelection(null);
            },
          },
        },
        handler,
      );
    }

    reg(
      "editor.closeGap",
      {
        description: "Remove the empty stretch of a track at time t: the clips after it move left.",
        input: obj({ trackId: str("From editor.get."), t: num("A time inside the gap.") }, ["trackId", "t"]),
        ui: {
          id: "gap.close",
          label: "Close gap",
          group: "Edit",
          icon: ArrowLeftToLine,
          keywords: "remove space empty",
          scopes: ["gap"],
          enabled: (c) => (gapTrack(c) ? true : trackOf(c)?.locked ? LOCKED : c.from === "menu" ? "No clips after this gap" : "Move the playhead into a gap"),
          hint: () => "The clips after it move left",
          args: (c) => ({ trackId: gapTrack(c)?.id, t: c.t }),
        },
      },
      (input: { trackId: string; t: number }) => {
        const tr = trackOr(d().project, input.trackId);
        edit((p) => closeGap(p, tr.id, time(input.t, "t")), "there's no gap there with clips after it" + lockedNote(tr.locked));
        return { closed: true };
      },
    );

    reg(
      "editor.duplicateClip",
      {
        description: "Copy a clip (effects, keyframes and all) onto the same track, at the first free spot after it. Returns the copy.",
        input: obj({ clipId: str("From editor.get.") }, ["clipId"]),
        ui: { id: "clip.duplicate", clips: "one", label: "Duplicate", group: "Edit", icon: Copy, keywords: "copy clone", scopes: ["clip"], enabled: clipOk, args: (c) => ({ clipId: c.clipId }) },
      },
      (input: { clipId: string }) => {
        const hit = clipOr(d().project, input.clipId);
        const before = d().project;
        const after = edit((p) => duplicateClip(p, hit.clip.id), "couldn't duplicate it" + lockedNote(hit.track.locked));
        const copy = newClips(before, after)[0];
        selectClip(copy?.id);
        return { clip: copy };
      },
    );

    reg(
      "editor.extractAudio",
      {
        description:
          "Extract audio: move a video clip's sound onto its own audio track (the first audio track free at that time, or a new one) as a separate clip with the same range, speed, volume, fades and volume keyframes. The video clip is muted. One undo step. Returns the new audio clip and its track.",
        input: obj({ clipId: str("A video clip with sound (editor.get).") }, ["clipId"]),
        ui: {
          id: "clip.extractAudio", clips: "one",
          label: "Extract audio",
          group: "Audio",
          icon: AudioLines,
          keywords: "separate detach sound",
          scopes: ["clip"],
          enabled: (c) => {
            const hit = hitOf(c);
            if (!hit) return "Select a clip first";
            if (canExtractAudio(c.project, hit.clip.id)) return true;
            const m = mediaById(c.project, hit.clip.media);
            if (hit.track.locked) return LOCKED;
            if (hit.track.kind === "audio" || m?.kind === "audio") return "This clip is already audio";
            if (hit.clip.hold) return "A freeze frame has no sound";
            if (!m?.hasAudio) return "This clip has no sound";
            return "The clip is muted: unmute it first";
          },
          hint: () => "Move the sound to its own audio track",
          args: (c) => ({ clipId: c.clipId }),
        },
      },
      (input: { clipId: string }) => {
        const p0 = d().project;
        const hit = clipOr(p0, input.clipId);
        if (!canExtractAudio(p0, hit.clip.id)) {
          const m = mediaById(p0, hit.clip.media);
          const why =
            hit.track.kind === "audio" || m?.kind !== "video"
              ? "it isn't a video clip"
              : hit.clip.hold
                ? "a freeze frame has no sound"
                : !m.hasAudio
                  ? "its file has no sound"
                  : hit.clip.muted
                    ? "the clip is muted (unmute it with editor.updateClip first)"
                    : "the track is locked (unlock it with editor.setTrackFlags)";
          throw new Error(`can't extract the audio: ${why}`);
        }
        const after = edit((p) => extractAudio(p, hit.clip.id), "couldn't extract the audio");
        const audio = newClips(p0, after)[0];
        const track = after.tracks.find((t) => t.clips.some((c) => c.id === audio?.id));
        return { audio, trackId: track?.id, video: hit.clip.id };
      },
    );

    reg(
      "editor.saveAudio",
      {
        description:
          "Save a clip's sound as an audio file, exactly as the clip plays it (range, speed, reverse, volume, fades, every audio stream). path must be absolute and end in .m4a (AAC) or .wav (16-bit PCM); an existing file is never overwritten. Returns the path once it's written.",
        input: obj({ clipId: str("A clip with sound (editor.get)."), path: str("Absolute output path ending in .m4a or .wav.") }, ["clipId", "path"]),
        timeoutSec: 10 * 60,
        // The person picks the file in a Save As dialog.
        ui: {
          id: "clip.saveAudio", clips: "one",
          label: "Save audio as file…",
          group: "Audio",
          icon: Download,
          keywords: "export sound m4a wav",
          scopes: ["clip"],
          hidden: noUi,
          enabled: (c) => (hitOf(c) ? (hasSound(c.project, hitOf(c)!.clip) ? true : "This clip has no sound") : "Select a clip first"),
          hint: () => "M4A or WAV",
          run: (c) => d().ui?.saveAudio(c.clipId!),
        },
      },
      async (input: { clipId: string; path: string }) => {
        const p = d().project;
        const hit = clipOr(p, input.clipId);
        const out = String(input.path ?? "").trim();
        if (!/\.(m4a|wav)$/i.test(out)) throw new Error("path must end in .m4a or .wav");
        if (await exists(out)) throw new Error(`${out} already exists: pick another path`);
        const written = await VideoService.ExportProject(ProjectExport.createFrom(soundExportRequest(p, hit.clip.id, out)));
        return { path: written };
      },
    );

    reg(
      "editor.updateClip",
      {
        description:
          "Change a clip's settings (only what you pass): transform (position/size/opacity), speed 0.25..4 (pitch kept), volume 0..2, muted, fadeIn/fadeOut seconds, green screen (chroma), filters, and per-source-track audio levels.",
        input: obj(
          {
            clipId: str("From editor.get."),
            transform: transformSchema,
            speed: num("0.25..4"),
            volume: num("0..2 (1 = as recorded)"),
            muted: bool("Silence the clip."),
            fadeIn: num("Seconds."),
            fadeOut: num("Seconds."),
            chroma: chromaSchema,
            filters: filtersSchema,
            levels: {
              type: "array",
              description: "Per-source-track levels for clips recorded with separate tracks: { index (audio stream), volume 0..200, muted }.",
              items: { type: "object", properties: { index: { type: "integer" }, volume: { type: "number" }, muted: { type: "boolean" } }, required: ["index"] },
            },
          },
          ["clipId"],
        ),
        ui: {
          id: "clip.mute", clips: "one",
          label: (c) => (hitOf(c)?.clip.muted ? "Unmute" : "Mute") + (c.from === "menu" ? "" : " clip"),
          group: "Audio",
          icon: VolumeX,
          keywords: "silence sound",
          scopes: ["clip"],
          enabled: soundOk,
          args: (c) => ({ clipId: c.clipId, muted: !hitOf(c)?.clip.muted }),
        },
      },
      (input: Record<string, unknown> & { clipId: string }) => {
        const hit = clipOr(d().project, input.clipId);
        const c = hit.clip;
        const patch: ClipPatch = {};
        if (input.transform !== undefined) patch.transform = { ...c.transform, ...(input.transform as object) };
        if (input.filters !== undefined) patch.filters = { ...c.filters, ...(input.filters as object) };
        for (const k of ["speed", "volume", "fadeIn", "fadeOut"] as const) if (input[k] !== undefined) patch[k] = Number(input[k]);
        if (input.muted !== undefined) patch.muted = !!input.muted;
        if (input.chroma !== undefined) {
          const ch = input.chroma;
          patch.chroma = ch === false || ch === null ? undefined : ch === true ? { ...DEFAULT_CHROMA } : { ...DEFAULT_CHROMA, ...(c.chroma ?? {}), ...(ch as object) };
        }
        if (input.levels !== undefined) {
          const ls = Array.isArray(input.levels) ? (input.levels as { index: number; volume?: number; muted?: boolean }[]) : [];
          patch.levels = ls.map((l) => ({ source: 0, index: Number(l.index), volume: l.volume === undefined ? 100 : Number(l.volume), muted: !!l.muted }));
        }
        if (!Object.keys(patch).length) throw new Error("nothing to change: pass at least one setting");
        const after = edit((p) => updateClip(p, c.id, patch), "those are already its settings" + lockedNote(hit.track.locked));
        selectClip(c.id);
        const n = findClip(after, c.id)!.clip;
        return { clipId: n.id, start: n.start, end: clipEnd(n), speed: n.speed, volume: n.volume, muted: n.muted, fadeIn: n.fadeIn, fadeOut: n.fadeOut, transform: n.transform, chroma: n.chroma ?? null, filters: n.filters };
      },
    );

    // ---- texts and crop -----------------------------------------------------

    reg(
      "editor.listFonts",
      {
        description: "The installed fonts a text can use (editor.setText font): each one's name and family. Pass refresh true after installing a font.",
        input: obj({ query: str("Only fonts whose name or family contains this (any case)."), refresh: { type: "boolean", description: "Re-read the installed fonts." } }),
      },
      async (input: { query?: string; refresh?: boolean }) => {
        const all = await loadFonts(!!input.refresh);
        const q = (input.query ?? "").trim().toLowerCase();
        const list = q ? all.filter((f) => f.name.toLowerCase().includes(q) || f.family.toLowerCase().includes(q)) : all;
        return { default: DEFAULT_FONT_LABEL, count: list.length, fonts: list.map((f) => ({ name: f.name, family: f.family })) };
      },
    );

    reg(
      "editor.setText",
      {
        description:
          "Add a text overlay (no id) or change one (id). White text with a dark outline, centred at x, y (0..1 of the frame), shown from start to end seconds. size is pixels of the output frame (after crop) or a preset s/m/l (4/6/9% of its height); font is an installed font's name (editor.listFonts), \"\" for the default. Returns the text.",
        input: obj({
          id: str("A text's id to change it."),
          text: str("The words."),
          x: num("0..1"),
          y: num("0..1"),
          size: {
            description: `Font size: output pixels (${MIN_FONT_PX}..${MAX_FONT_PX}) or a preset.`,
            oneOf: [{ type: "number" }, { type: "string", enum: ["s", "m", "l"] }],
          },
          font: str(`An installed font's name from editor.listFonts; "" for the default (${DEFAULT_FONT_LABEL}).`),
          start: num("Seconds."),
          end: num("Seconds."),
        }),
        // The person types the words in the text editor.
        ui: {
          id: "text.add",
          label: (c) => (c.from === "menu" && c.t !== c.playhead ? "Add text here" : "Add text"),
          group: "Insert",
          icon: Type,
          keywords: "title caption words overlay",
          scopes: ["gap", "viewer"],
          hidden: noUi,
          enabled: (c) => (projectDuration(c.project) > 0 ? true : "Add a clip first"),
          run: (c) => d().ui?.addText(c.t),
        },
      },
      async (input: { id?: string; text?: string; x?: number; y?: number; size?: number | string; font?: string; start?: number; end?: number }) => {
        const p0 = d().project;
        const old = input.id ? p0.texts.find((t) => t.id === input.id) : undefined;
        if (input.id && !old) throw new Error(`no text "${input.id}"`);
        const dur = projectDuration(p0);
        const now = d().pb.clock.now();
        const start = time(input.start, "start", old?.start ?? Math.min(now, Math.max(0, dur - 0.5)));
        let size = old?.size ?? "m";
        let fontPx = old?.fontPx;
        if (typeof input.size === "string") {
          if (input.size !== "s" && input.size !== "m" && input.size !== "l") throw new Error('size must be pixels or "s", "m" or "l"');
          size = input.size;
          fontPx = undefined;
        } else if (input.size !== undefined) {
          const n = Math.round(Number(input.size));
          if (!Number.isFinite(n) || n < MIN_FONT_PX || n > MAX_FONT_PX) throw new Error(`size must be ${MIN_FONT_PX}..${MAX_FONT_PX} pixels`);
          fontPx = n;
        }
        let font = old?.font;
        if (input.font !== undefined) {
          font = String(input.font).trim() || undefined;
          if (font) {
            await loadFonts();
            if (!fontInfo(font)) throw new Error(`no installed font "${font}" (see editor.listFonts)`);
            const why = await checkFont(font);
            if (why) throw new Error(why);
          }
        }
        const t: Text = {
          id: old?.id ?? newId("txt"),
          text: input.text ?? old?.text ?? "",
          x: input.x ?? old?.x ?? 0.5,
          y: input.y ?? old?.y ?? 0.85,
          size,
          ...(fontPx ? { fontPx } : {}),
          ...(font ? { font } : {}),
          start,
          end: time(input.end, "end", old?.end ?? Math.min(Math.max(dur, start + 0.5), start + 3)),
        };
        if (!t.text.trim()) throw new Error("text is required");
        if (t.end <= t.start) throw new Error("end must be after start");
        // Not selected: selecting a text opens its editor, which would take
        // the person's keyboard.
        edit((p) => upsertProjectText(p, t), "that text is unchanged");
        return t;
      },
    );

    // ---- transitions (transition.ts) -------------------------------------

    // addAndSelect is the UI's add: a transition at the clip's cut, then
    // selected (the inspector shows it). An agent's add doesn't select.
    const addAndSelect = (clipId: string, type: TransitionType) => {
      let at = "";
      edit((p) => {
        const r = addTransition(p, clipId, type);
        if (typeof r === "string") throw new Error(r);
        at = r.clipId;
        return r.project;
      }, "that transition is already there");
      d().setSelection({ kind: "transition", id: at });
    };

    reg(
      "editor.addTransition",
      {
        description:
          "Add a transition at a clip's cut, or change the one there: between the clip and the one right before it on its video track (or, when it has none, the one right after). Centred on the cut, so the timeline's length doesn't change; each clip holds its edge frame for the half past its own end. Returns the incoming clip's id (where the transition is stored), its type and length.",
        input: obj(
          {
            clipId: str("A clip on either side of the cut (editor.get)."),
            type: str("The transition.", { enum: TRANSITIONS.map((t) => t.type) }),
            duration: num(`Seconds (${MIN_TRANSITION}..${MAX_TRANSITION}; default ${DEFAULT_TRANSITION}, or the current one's), shortened to what fits.`),
          },
          ["clipId", "type"],
        ),
        ui: {
          id: "clip.addTransition",
          label: "Add transition",
          group: "Edit",
          icon: ArrowLeftRight,
          keywords: "dissolve fade dip wipe slide zoom blur cut",
          scopes: ["clip"],
          clips: "one",
          enabled: (c) => {
            if (!c.clipId) return "Select a clip next to another on its track";
            const at = cutFor(c.project, c.clipId);
            return typeof at === "string" ? at : true;
          },
          // The palette (and a plain menu click) adds a cross dissolve and
          // selects it, so the inspector can change its type and length.
          run: (c) => addAndSelect(c.clipId!, "fade"),
          choices: (c) =>
            TRANSITIONS.map((t) => ({ label: t.label, run: () => addAndSelect(c.clipId!, t.type) })),
        },
      },
      (input: { clipId: string; type: string; duration?: number }) => {
        if (!isTransitionType(input.type)) throw new Error(`unknown transition "${input.type}" (one of ${TRANSITIONS.map((t) => t.type).join(", ")})`);
        if (input.duration !== undefined && !(Number(input.duration) >= MIN_TRANSITION && Number(input.duration) <= MAX_TRANSITION)) {
          throw new Error(`duration must be ${MIN_TRANSITION}..${MAX_TRANSITION} seconds`);
        }
        clipOr(d().project, input.clipId);
        const r = addTransition(d().project, String(input.clipId), input.type, input.duration === undefined ? undefined : Number(input.duration));
        if (typeof r === "string") throw new Error(r);
        const after = edit(() => r.project, "that transition is already there");
        const c = findClip(after, r.clipId)!.clip;
        return { clipId: r.clipId, type: c.transition?.type, label: transitionLabel(c.transition?.type ?? ""), duration: c.transition?.duration };
      },
    );

    reg(
      "editor.removeTransition",
      {
        description: "Remove the transition stored on a clip (its incoming one; editor.get shows it as the clip's transition).",
        input: obj({ clipId: str("The clip whose transition goes (the one after the cut).") }, ["clipId"]),
      },
      (input: { clipId: string }) => {
        clipOr(d().project, input.clipId);
        edit((p) => setClipTransition(p, String(input.clipId), undefined), `clip "${input.clipId}" has no transition`);
        return { removed: input.clipId };
      },
    );

    reg(
      "editor.removeText",
      { description: "Remove a text overlay.", input: obj({ id: str("From editor.get.") }, ["id"]) },
      (input: { id: string }) => {
        edit((p) => removeProjectText(p, String(input.id)), `no text "${input.id}"`);
        return { removed: input.id };
      },
    );

    reg(
      "editor.setCrop",
      {
        description: "Crop the output to a rectangle in canvas pixels ({x, y, w, h}), or pass rect null to remove the crop.",
        input: obj({
          rect: {
            description: "Canvas pixels, or null for no crop.",
            oneOf: [{ type: "null" }, { type: "object", properties: { x: { type: "integer" }, y: { type: "integer" }, w: { type: "integer" }, h: { type: "integer" } }, required: ["x", "y", "w", "h"] }],
          },
        }, ["rect"]),
        ui: {
          id: "crop.reset",
          label: "Reset crop",
          group: "View",
          icon: RotateCcw,
          order: 2,
          keywords: "full frame uncrop",
          scopes: ["viewer"],
          hidden: (c) => !c.project.crop,
          args: () => ({ rect: null }),
        },
      },
      (input: { rect: { x: number; y: number; w: number; h: number } | null }) => {
        const { canvas } = d().project;
        let r = input.rect;
        if (r) {
          r = { x: Math.round(r.x), y: Math.round(r.y), w: Math.round(r.w), h: Math.round(r.h) };
          if (r.w < 2 || r.h < 2 || r.x < 0 || r.y < 0 || r.x + r.w > canvas.width || r.y + r.h > canvas.height) {
            throw new Error(`the crop must fit inside the ${canvas.width}x${canvas.height} canvas`);
          }
        }
        edit((p) => setCrop(p, r), r ? "that's already the crop" : "there's no crop");
        return { crop: r };
      },
    );

    reg(
      "editor.setCanvas",
      {
        description:
          "Change the project frame: a preset (16:9, 9:16, 1:1, 4:5) or width/height, and/or fps. When the shape changes, place says what happens to the clips that fill the frame (every clip on the bottom video track, and centred full-size clips above it): fill covers the new frame (cropping), fit fits inside it, keep leaves transforms alone. Default fill. A crop is removed when the size changes. One undo step.",
        input: obj({
          preset: { type: "string", enum: CANVAS_PRESETS.map((p) => p.id), description: "A preset frame (sets width and height)." },
          width: { type: "integer", description: `Frame width in px (even, ${MIN_CANVAS}..${MAX_CANVAS}).` },
          height: { type: "integer", description: `Frame height in px (even, ${MIN_CANVAS}..${MAX_CANVAS}).` },
          fps: num("Frame rate."),
          place: { type: "string", enum: ["fill", "fit", "keep"], description: "How to place full-frame clips when the shape changes (default fill)." },
        }),
      },
      (input: { preset?: string; width?: number; height?: number; fps?: number; place?: Placement }) => {
        const cur = d().project.canvas;
        const pr = input.preset !== undefined ? CANVAS_PRESETS.find((p) => p.id === input.preset) : undefined;
        if (input.preset !== undefined && !pr) throw new Error(`no preset "${input.preset}" (${CANVAS_PRESETS.map((p) => p.id).join(", ")})`);
        const place = input.place ?? "fill";
        if (!["fill", "fit", "keep"].includes(place)) throw new Error(`place must be fill, fit or keep`);
        const next = normCanvas({
          width: pr?.width ?? (input.width !== undefined ? Number(input.width) : cur.width),
          height: pr?.height ?? (input.height !== undefined ? Number(input.height) : cur.height),
          fps: input.fps !== undefined ? Number(input.fps) : cur.fps,
        });
        const out = edit((p) => setCanvas(p, next, place), "that's already the frame");
        return { canvas: out.canvas, crop: out.crop ?? null };
      },
    );

    reg(
      "editor.placeClip",
      {
        description: "Place a video or image clip on the frame: fill (cover it, centred, cropping), fit (whole picture inside, centred) or reset (the default transform, dropping transform keyframes). One undo step.",
        input: obj({ clipId: str("From editor.get."), mode: { type: "string", enum: ["fill", "fit", "reset"] } }, ["clipId", "mode"]),
      },
      (input: { clipId: string; mode: ClipPlace }) => {
        if (!["fill", "fit", "reset"].includes(input.mode)) throw new Error("mode must be fill, fit or reset");
        const out = edit((p) => placeClip(p, String(input.clipId), input.mode), `clip "${input.clipId}" is already placed that way (or isn't a picture)`);
        const c = out.tracks.flatMap((t) => t.clips).find((x) => x.id === input.clipId);
        return { clipId: input.clipId, transform: c?.transform };
      },
    );

    // ---- history and playback ----------------------------------------------

    reg(
      "editor.undo",
      {
        description: "Undo the last change (the person's or an agent's).",
        input: obj(),
        ui: { id: "history.undo", label: "Undo", group: "History", icon: Undo2, shortcut: "Ctrl+Z", enabled: () => d().canUndo || "Nothing to undo" },
      },
      () => {
        if (!d().canUndo) throw new Error("nothing to undo");
        d().undo();
        return { undone: true };
      },
    );
    reg(
      "editor.redo",
      {
        description: "Redo the last undone change.",
        input: obj(),
        ui: { id: "history.redo", label: "Redo", group: "History", icon: Redo2, shortcut: "Ctrl+Shift+Z", keys: ["Ctrl+Shift+Z", "Ctrl+Y"], enabled: () => d().canRedo || "Nothing to redo" },
      },
      () => {
        if (!d().canRedo) throw new Error("nothing to redo");
        d().redo();
        return { redone: true };
      },
    );
    reg(
      "editor.seek",
      {
        description: "Move the playhead to t seconds.",
        input: obj({ t: num("Seconds on the timeline.") }, ["t"]),
        ui: [
          { id: "playback.seekHere", label: "Move playhead here", group: "Playback", icon: Crosshair, scopes: ["gap"], hidden: (c) => c.from !== "menu", args: (c) => ({ t: c.t }) },
          { id: "playback.start", label: "Go to start", group: "Playback", shortcut: "Home", keywords: "beginning rewind", args: () => ({ t: 0 }) },
          { id: "playback.end", label: "Go to end", group: "Playback", shortcut: "End", args: (c) => ({ t: projectDuration(c.project) }) },
        ],
      },
      (input: { t: number }) => {
        const t = Math.min(time(input.t, "t"), projectDuration(d().project));
        d().pb.seek(t);
        return { playhead: t };
      },
    );
    reg(
      "editor.play",
      {
        description: "Play from the playhead.",
        input: obj(),
        // One item for both: Space plays or pauses.
        ui: {
          id: "playback.toggle",
          label: (c) => (c.playing ? "Pause" : "Play"),
          group: "Playback",
          icon: Play,
          shortcut: "Space",
          keywords: "pause stop",
          scopes: ["viewer"],
          enabled: (c) => (projectDuration(c.project) > 0 ? true : "Add a clip first"),
          run: () => (d().pb.toggle ? d().pb.toggle!() : d().pb.playing ? d().pb.pause() : d().pb.play()),
        },
      },
      () => {
        d().pb.play();
        return { playing: true };
      },
    );
    reg("editor.pause", { description: "Pause playback.", input: obj() }, () => {
      d().pb.pause();
      return { playing: false, playhead: d().pb.clock.now() };
    });

    // ---- export -------------------------------------------------------------

    reg(
      "editor.export",
      {
        description:
          "Export the edit to a file and return its path once it's written. Simple: preset original (the canvas), 1080p, 720p, discord (fits 10MB; may become .webm) or custom (height + kbps). Full control: options (format mp4 / hevc / webm / gif / m4a / wav, size, fit, fps, quality or kbps, encoder, audio, range, gif settings; see editor.exportFormats for what this PC can make). path defaults to <first file>-edited.<ext> next to the first file; an existing file is never overwritten.",
        // The person picks the settings on the export screen.
        ui: {
          id: "file.export",
          label: "Export…",
          group: "File",
          icon: Download,
          keywords: "render save video mp4 gif",
          scopes: ["viewer"],
          hidden: noUi,
          enabled: () => d().ui?.canExport ?? "Export isn't available",
          run: () => d().ui?.openExport(),
        },
        input: obj({
          preset: str("Output preset (with options: only discord changes anything, fitting the result to 10MB).", { enum: ["original", "1080p", "720p", "discord", "custom"] }),
          path: str("Absolute output path; its extension must match the format (.mp4 for mp4/hevc, .webm, .gif, .m4a, .wav)."),
          height: num("custom preset only: output height (even)."),
          kbps: num("custom preset only: video bitrate."),
          options: {
            type: "object",
            description: "Export settings. Zero/absent fields keep the defaults.",
            properties: {
              format: str("mp4 (H.264), hevc (H.265 in .mp4), webm (VP9), gif, m4a (audio only, AAC) or wav (audio only, PCM).", { enum: ["mp4", "hevc", "webm", "gif", "m4a", "wav"] }),
              width: num("Output width (even; needs height). 0 = keep."),
              height: num("Output height (even). Height alone keeps the aspect."),
              fit: str("How another aspect fits width x height: fit (black bars) or fill (cropped).", { enum: ["fit", "fill"] }),
              fps: num("Frame rate (0 = the canvas rate)."),
              quality: str("Quality level (ignored with kbps).", { enum: ["low", "medium", "high", "best"] }),
              kbps: num("Video bitrate in kbps instead of a quality level."),
              encoder: str("Video encoder (editor.exportFormats lists the usable ones); default the best."),
              noAudio: bool("Leave the sound out."),
              audioKbps: num("Audio bitrate (default 160)."),
              sampleRate: num("44100 or 48000 (default)."),
              rangeIn: num("Start of the exported part, ms on the timeline."),
              rangeOut: num("End of the exported part, ms (0 = the end)."),
              gif: {
                type: "object",
                properties: {
                  fps: num("1..50 (default 15)."),
                  width: num("Width (default 480); height follows."),
                  loop: num("0 forever (default), -1 play once, n = repeat n more times."),
                  dither: str("Dithering.", { enum: ["sierra2_4a", "floyd_steinberg", "bayer", "none"] }),
                  colors: num("2..256 (default 256)."),
                },
              },
            },
            required: ["format"],
          },
        }),
        timeoutSec: 30 * 60,
      },
      async (input: { preset?: string; path?: string; height?: number; kbps?: number; options?: ExportSettings }) => {
        const { project: p, firstPath, pb } = d();
        if (projectDuration(p) <= 0) throw new Error("the timeline is empty");
        const preset = (input.preset ?? "original") as "original" | "1080p" | "720p" | "discord" | "custom";
        if (!["original", "1080p", "720p", "discord", "custom"].includes(preset)) throw new Error(`unknown preset "${preset}"`);
        const options = input.options;
        const ext = options ? EXPORT_EXT[options.format] : ".mp4";
        if (options && !ext) throw new Error(`unknown format "${options.format}" (mp4, hevc, webm, gif, m4a or wav)`);
        const out = input.path?.trim() || editedPath(firstPath, ext);
        if (!out) throw new Error("path is required (the edit has no file to name it after)");
        if (await exists(out)) throw new Error(`${out} already exists: pick another path`);
        pb.pause();
        const written = await VideoService.ExportProject(
          ProjectExport.createFrom(toExportRequest(p, { preset, height: input.height, kbps: input.kbps, outPath: out, options })),
        );
        return { path: written };
      },
    );

    reg(
      "editor.exportFormats",
      {
        description: "What this PC can export: formats and, per video format, the usable encoders (best first) for editor.export's options.encoder.",
      },
      async () => VideoService.ExportCapabilities(),
    );

    registerTimeCommands({ project: () => d().project, playhead: () => d().pb.clock.now(), reg, edit, clipOr, obj, lockedNote, selectClip: (id) => void selectClip(id) });
    registerFxCommands({ project: () => d().project, playhead: () => d().pb.clock.now(), reg, edit, clipOr, obj, lockedNote, selectClip: (id) => void selectClip(id) });
    registerMultiCommands({ project: () => d().project, playhead: () => d().pb.clock.now(), reg, edit, obj, setSelection: (s) => d().setSelection(s) });

    // ---- window-only actions (no agent command: an agent sets values directly)
    const act = (a: Omit<Action, "exec"> & { exec: Action["exec"] }) => offs.push(registerAction(a));
    const pickClip = (c: ActionCtx) => (hitOf(c) ? true : "Select a clip first");
    // tabOk: the target clip has that inspector tab (no Speed for an image,
    // no Adjust for audio...).
    const tabOk = (tab: TabId, why: string) => (c: ActionCtx) => {
      const hit = hitOf(c);
      if (!hit) return "Select a clip first";
      return tabsFor(hit.track, mediaById(c.project, hit.clip.media)).includes(tab) ? true : why;
    };
    act({
      id: "clip.speed",
      clips: "one",
      label: "Speed…",
      group: "Time",
      icon: Gauge,
      keywords: "slow motion fast ramp",
      scopes: ["clip"],
      order: -1,
      hidden: noUi,
      enabled: tabOk("speed", "An image has no speed"),
      exec: (c) => d().ui?.openInspector(c.clipId!, "speed"),
    });
    act({ id: "clip.inspect", label: "Open in inspector", group: "Clip", icon: PanelRight, keywords: "settings properties", scopes: ["clip"], order: 1, hidden: noUi, enabled: pickClip, exec: (c) => d().ui?.openInspector(c.clipId!) });
    // Each inspector tab, from the palette.
    for (const [tab, label, words, why] of [
      ["video", "Position and size", "transform scale opacity move", "Audio has no picture"],
      ["adjust", "Adjust colour", "brightness contrast saturation color", "Audio has no picture"],
      ["effects", "Effects", "blur glow green screen chroma key", "Audio has no picture"],
      ["audio", "Volume and fades", "audio sound level", "This clip has no sound"],
    ] as const) {
      act({ id: `inspector.${tab}`, label: `${label}…`, group: "Clip", icon: PanelRight, keywords: `inspector ${words}`, order: 50, hidden: noUi, enabled: tabOk(tab, why), exec: (c) => d().ui?.openInspector(c.clipId!, tab) });
    }
    // Each library on the rail.
    for (const lib of LIBRARIES.filter((l) => !l.soon)) {
      act({ id: `library.${lib.id}`, label: `${lib.label} library`, group: "View", icon: lib.icon, order: 20, keywords: `open panel rail ${lib.hint}`, hidden: noUi, exec: () => d().ui?.openLibrary(lib.id) });
    }
    act({
      id: "view.crop",
      label: () => (d().ui?.cropping ? "Done cropping" : "Crop…"),
      group: "View",
      icon: Crop,
      order: 1,
      keywords: "cut frame trim picture",
      scopes: ["viewer"],
      hidden: noUi,
      enabled: (c) => (projectDuration(c.project) > 0 ? true : "Add a clip first"),
      exec: () => d().ui?.toggleCrop(),
    });

    return () => offs.forEach((off) => off());
  }, []);
}

const EXPORT_EXT: Record<string, string> = { mp4: ".mp4", hevc: ".mp4", webm: ".webm", gif: ".gif", m4a: ".m4a", wav: ".wav" };

// editedPath is <dir>\<name>-edited<ext> for the first file ("" without one).
function editedPath(first: string, ext = ".mp4"): string {
  if (!first) return "";
  const i = Math.max(first.lastIndexOf("\\"), first.lastIndexOf("/"));
  const dir = first.slice(0, i + 1);
  const base = first.slice(i + 1).replace(/\.[^.]+$/, "");
  return `${dir}${base}-edited${ext}`;
}

// exists asks Go whether a path is taken (MediaService.Register probes only
// media, so a HEAD-free check: ProbeMedia fails on a missing file).
async function exists(path: string): Promise<boolean> {
  try {
    await VideoService.ProbeMedia(path);
    return true;
  } catch (err) {
    return !/no such file|cannot find|not exist/i.test(String(err));
  }
}

// settle waits a few frames so seeked video players draw the new time.
function settle(): Promise<void> {
  return new Promise((done) => {
    let n = 0;
    const tick = () => (++n >= 6 ? window.setTimeout(done, 120) : requestAnimationFrame(tick));
    requestAnimationFrame(tick);
  });
}
