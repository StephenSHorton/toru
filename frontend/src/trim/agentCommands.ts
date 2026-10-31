import { useEffect, useRef, type MutableRefObject } from "react";
import { Window } from "@wailsio/runtime";
import { registerAgentCommand, type AgentCommandSpec, type AgentHandler } from "@/lib/agent";
import { VideoService } from "@/lib/api";
import { ProjectExport } from "../../bindings/github.com/StephenSHorton/toru/internal/vid/models.js";
import { registerTimeCommands } from "@/trim/agentCommandsTime";
import type { CompositorHandle } from "@/trim/compositor/Compositor";
import { registerFxCommands } from "@/trim/agentCommandsFx";
import { newId } from "@/trim/edl";
import type { MediaBinState } from "@/trim/MediaBin";
import type { TimelineSelection } from "@/trim/MultiTimeline";
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
  trackById,
  trimClip,
  updateClip,
  upsertProjectText,
  type Clip,
  type ClipPatch,
  type Media,
  type Project,
} from "@/trim/project";

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
  pb: { time: number; playing: boolean; play: () => void; pause: () => void; seek: (t: number) => void; clock: { now(): number } };
  bin: MediaBinState;
  compositor: MutableRefObject<CompositorHandle | null>;
  setSelection: (s: TimelineSelection) => void;
  firstPath: string;
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
    const reg = (name: string, spec: AgentCommandSpec, handler: AgentHandler) => offs.push(registerAgentCommand(name, spec, handler));

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
          texts: p.texts,
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
      { description: "Add a video track (drawn on top of the others) or an audio track. Returns its id.", input: obj({ kind: str("Track kind.", { enum: ["video", "audio"] }) }, ["kind"]) },
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
      { description: "Remove a track and every clip on it.", input: obj({ trackId: str("From editor.get.") }, ["trackId"]) },
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

    for (const [name, op, what] of [
      ["editor.removeClip", removeClip, "Delete a clip and leave a gap."],
      ["editor.rippleDelete", rippleDelete, "Delete a clip and pull the later clips on its track left (other tracks don't move)."],
    ] as const) {
      reg(name, { description: what, input: obj({ clipId: str("From editor.get.") }, ["clipId"]) }, (input: { clipId: string }) => {
        const hit = clipOr(d().project, input.clipId);
        edit((p) => op(p, hit.clip.id), "couldn't delete it" + lockedNote(hit.track.locked));
        d().setSelection(null);
        return { removed: hit.clip.id };
      });
    }

    reg(
      "editor.closeGap",
      { description: "Remove the empty stretch of a track at time t: the clips after it move left.", input: obj({ trackId: str("From editor.get."), t: num("A time inside the gap.") }, ["trackId", "t"]) },
      (input: { trackId: string; t: number }) => {
        const tr = trackOr(d().project, input.trackId);
        edit((p) => closeGap(p, tr.id, time(input.t, "t")), "there's no gap there with clips after it" + lockedNote(tr.locked));
        return { closed: true };
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
      "editor.setText",
      {
        description: "Add a text overlay (no id) or change one (id). White text with a dark outline, sizes s/m/l, centred at x, y (0..1 of the frame), shown from start to end seconds. Returns its id.",
        input: obj({
          id: str("A text's id to change it."),
          text: str("The words."),
          x: num("0..1"),
          y: num("0..1"),
          size: str("Size.", { enum: ["s", "m", "l"] }),
          start: num("Seconds."),
          end: num("Seconds."),
        }),
      },
      (input: { id?: string; text?: string; x?: number; y?: number; size?: "s" | "m" | "l"; start?: number; end?: number }) => {
        const p0 = d().project;
        const old = input.id ? p0.texts.find((t) => t.id === input.id) : undefined;
        if (input.id && !old) throw new Error(`no text "${input.id}"`);
        const dur = projectDuration(p0);
        const now = d().pb.clock.now();
        const start = time(input.start, "start", old?.start ?? Math.min(now, Math.max(0, dur - 0.5)));
        const t = {
          id: old?.id ?? newId("txt"),
          text: input.text ?? old?.text ?? "",
          x: input.x ?? old?.x ?? 0.5,
          y: input.y ?? old?.y ?? 0.85,
          size: input.size ?? old?.size ?? "m",
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

    // ---- history and playback ----------------------------------------------

    reg("editor.undo", { description: "Undo the last change (the person's or an agent's).", input: obj() }, () => {
      if (!d().canUndo) throw new Error("nothing to undo");
      d().undo();
      return { undone: true };
    });
    reg("editor.redo", { description: "Redo the last undone change.", input: obj() }, () => {
      if (!d().canRedo) throw new Error("nothing to redo");
      d().redo();
      return { redone: true };
    });
    reg("editor.seek", { description: "Move the playhead to t seconds.", input: obj({ t: num("Seconds on the timeline.") }, ["t"]) }, (input: { t: number }) => {
      const t = Math.min(time(input.t, "t"), projectDuration(d().project));
      d().pb.seek(t);
      return { playhead: t };
    });
    reg("editor.play", { description: "Play from the playhead.", input: obj() }, () => {
      d().pb.play();
      return { playing: true };
    });
    reg("editor.pause", { description: "Pause playback.", input: obj() }, () => {
      d().pb.pause();
      return { playing: false, playhead: d().pb.clock.now() };
    });

    // ---- export -------------------------------------------------------------

    reg(
      "editor.export",
      {
        description:
          "Export the edit to a video file and return its path once it's written. preset: original (the canvas), 1080p, 720p, discord (fits 10MB; may become .webm) or custom (height + kbps). path defaults to <first file>-edited.mp4 next to the first file; an existing file is never overwritten.",
        input: obj({
          preset: str("Output preset.", { enum: ["original", "1080p", "720p", "discord", "custom"] }),
          path: str("Absolute output path (.mp4, or .webm)."),
          height: num("custom only: output height (even)."),
          kbps: num("custom only: video bitrate."),
        }),
        timeoutSec: 30 * 60,
      },
      async (input: { preset?: string; path?: string; height?: number; kbps?: number }) => {
        const { project: p, firstPath, pb } = d();
        if (projectDuration(p) <= 0) throw new Error("the timeline is empty");
        const preset = (input.preset ?? "original") as "original" | "1080p" | "720p" | "discord" | "custom";
        if (!["original", "1080p", "720p", "discord", "custom"].includes(preset)) throw new Error(`unknown preset "${preset}"`);
        const out = input.path?.trim() || editedPath(firstPath);
        if (!out) throw new Error("path is required (the edit has no file to name it after)");
        if (await exists(out)) throw new Error(`${out} already exists: pick another path`);
        pb.pause();
        const written = await VideoService.ExportProject(
          ProjectExport.createFrom(toExportRequest(p, { preset, height: input.height, kbps: input.kbps, outPath: out })),
        );
        return { path: written };
      },
    );

    registerTimeCommands({ project: () => d().project, playhead: () => d().pb.clock.now(), reg, edit, clipOr, obj, lockedNote, selectClip: (id) => void selectClip(id) });
    registerFxCommands({ project: () => d().project, playhead: () => d().pb.clock.now(), reg, edit, clipOr, obj, lockedNote, selectClip: (id) => void selectClip(id) });

    return () => offs.forEach((off) => off());
  }, []);
}

// editedPath is <dir>\<name>-edited.mp4 for the first file ("" without one).
function editedPath(first: string): string {
  if (!first) return "";
  const i = Math.max(first.lastIndexOf("\\"), first.lastIndexOf("/"));
  const dir = first.slice(0, i + 1);
  const base = first.slice(i + 1).replace(/\.[^.]+$/, "");
  return `${dir}${base}-edited.mp4`;
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
