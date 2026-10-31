import { ClipboardPaste, Copy, MousePointerSquareDashed, Scissors, Trash2, VolumeX } from "lucide-react";
import type { AgentHandler } from "@/lib/agent";
import type { ActionCtx, EditorCommandSpec } from "@/trim/actions";
import {
  allClipIds,
  ALL_ATTRIBUTES,
  clipboard,
  copyClips,
  duplicateClips,
  liveIds,
  moveClips,
  pasteAttributes,
  pastedIds,
  pasteClips,
  removeClips,
  rippleDeleteClips,
  setClipsMuted,
  type AttributeSet,
} from "@/trim/multi";
import { selectClips, type TimelineSelection } from "@/trim/MultiTimeline";
import { clipEnd, findClip, type Project } from "@/trim/project";

// The editor's agent commands for several clips at once and the clip
// clipboard (trim/multi.ts): the same ops as the timeline's multi-selection,
// Ctrl+C / X / V and Paste attributes. agentCommands.ts registers them with
// its own helpers, so each is one undo step. Each also selects what it acted
// on, so the person sees it.

export interface MultiCommandCtx {
  project: () => Project;
  playhead: () => number;
  reg: (name: string, spec: EditorCommandSpec, handler: AgentHandler) => void;
  edit: (fn: (p: Project) => Project, why: string) => Project;
  obj: (props?: Record<string, unknown>, required?: string[]) => Record<string, unknown>;
  setSelection: (s: TimelineSelection) => void;
}

const idsSchema = { type: "array", items: { type: "string" }, description: "Clip ids, from editor.get." };

export function registerMultiCommands(x: MultiCommandCtx) {
  const { reg, edit, obj } = x;
  // clips checks the ids: every one must name a clip.
  const clips = (v: unknown): string[] => {
    if (!Array.isArray(v) || !v.length) throw new Error("clipIds must be a list of clip ids (editor.get lists them)");
    const ids = v.map(String);
    const missing = ids.filter((id) => !findClip(x.project(), id));
    if (missing.length) throw new Error(`no clip ${missing.map((m) => `"${m}"`).join(", ")} (editor.get lists them)`);
    return liveIds(x.project(), ids);
  };
  // How they look to the person (actions.ts): they act on ctx.clipIds, the
  // selection (or the right-clicked clip's).
  const LOCKED = "A clip's track is locked";
  const n = (c: ActionCtx) => c.clipIds.length;
  const hits = (c: ActionCtx) => c.clipIds.map((id) => findClip(c.project, id)).filter((h) => !!h);
  const some = (c: ActionCtx) => (n(c) ? true : "Select a clip first");
  const noneLocked = (c: ActionCtx) => (!n(c) ? "Select a clip first" : hits(c).some((h) => h.track.locked) ? LOCKED : true);
  const notAllLocked = (c: ActionCtx) => (!n(c) ? "Select a clip first" : hits(c).every((h) => h.track.locked) ? LOCKED : true);
  const count = (c: ActionCtx, verb: string) => (n(c) > 1 ? `${verb} ${n(c)} clips` : verb);
  const place = (p: Project, ids: string[]) => ids.map((id) => {
    const c = findClip(p, id)!;
    return { clipId: id, trackId: c.track.id, start: c.clip.start, end: clipEnd(c.clip) };
  });

  reg(
    "editor.selectClips",
    {
      description: "Select clips on the timeline, as Ctrl+click does (an empty list clears the selection). The first is the one the inspector shows.",
      input: obj({ clipIds: { ...idsSchema, description: "Clip ids from editor.get; [] clears." } }, ["clipIds"]),
      ui: [
        {
          id: "select.all",
          label: "Select all clips",
          group: "Edit",
          icon: MousePointerSquareDashed,
          shortcut: "Ctrl+A",
          order: 40,
          enabled: (c) => (allClipIds(c.project).length ? true : "There are no clips"),
          // The selected clip stays the inspector's.
          run: (c) => x.setSelection(selectClips(allClipIds(c.project), c.clipId)),
        },
        {
          id: "select.none",
          label: "Clear selection",
          group: "Edit",
          shortcut: "Esc",
          order: 41,
          enabled: (c) => (c.selection ? true : "Nothing is selected"),
          run: () => x.setSelection(null),
        },
      ],
    },
    (input: { clipIds: string[] }) => {
      const ids = Array.isArray(input.clipIds) && input.clipIds.length ? clips(input.clipIds) : [];
      x.setSelection(selectClips(ids, ids[0]));
      return { selected: ids };
    },
  );

  reg(
    "editor.moveClips",
    {
      description: "Move several clips by the same amount of time, on their own tracks. If a clip would hit another clip or go before 0, they all move only as far as they can.",
      input: obj({ clipIds: idsSchema, by: { type: "number", description: "Seconds, + later, - earlier." } }, ["clipIds", "by"]),
    },
    (input: { clipIds: string[]; by: number }) => {
      const ids = clips(input.clipIds);
      const after = edit((p) => moveClips(p, ids, Number(input.by)), "they can't move that way (a clip in the way, the start of the timeline, or a locked track)");
      x.setSelection(selectClips(ids, ids[0]));
      return { clips: place(after, ids) };
    },
  );

  reg(
    "editor.removeClips",
    {
      description: "Delete several clips. ripple: also pull the later clips on each of their tracks left (Shift+Delete). Clips on locked tracks stay.",
      input: obj({ clipIds: idsSchema, ripple: { type: "boolean", description: "Close the gaps they leave. Default false." } }, ["clipIds"]),
      // Several selected clips (one clip: editor.removeClip / rippleDelete).
      ui: [
        {
          id: "clips.delete",
          label: (c) => `Delete ${n(c)} clips`,
          group: "Clip",
          icon: Trash2,
          shortcut: "Del",
          keys: ["Del", "Backspace"],
          order: 10,
          clips: "many",
          destructive: true,
          scopes: ["clip"],
          enabled: notAllLocked,
          hint: () => "Leaves gaps",
          args: (c) => ({ clipIds: c.clipIds }),
        },
        {
          id: "clips.rippleDelete",
          label: "Ripple delete",
          group: "Clip",
          icon: Trash2,
          shortcut: "Shift+Del",
          keys: ["Shift+Del", "Shift+Backspace"],
          order: 11,
          clips: "many",
          destructive: true,
          scopes: ["clip"],
          enabled: notAllLocked,
          hint: () => "Pulls the rest of each track left",
          args: (c) => ({ clipIds: c.clipIds, ripple: true }),
        },
      ],
    },
    (input: { clipIds: string[]; ripple?: boolean }) => {
      const ids = clips(input.clipIds);
      const after = edit((p) => (input.ripple ? rippleDeleteClips(p, ids) : removeClips(p, ids)), "nothing to delete: their tracks are locked");
      x.setSelection(null);
      return { removed: ids.filter((id) => !findClip(after, id)) };
    },
  );

  reg(
    "editor.duplicateClips",
    {
      description: "Copy several clips (effects, keyframes and all) to just after them, keeping their spacing and tracks. Returns the copies.",
      input: obj({ clipIds: idsSchema }, ["clipIds"]),
      ui: { id: "clips.duplicate", label: (c) => count(c, "Duplicate"), group: "Edit", icon: Copy, clips: "many", scopes: ["clip"], enabled: noneLocked, hint: () => "Copies them after the selection", args: (c) => ({ clipIds: c.clipIds }) },
    },
    (input: { clipIds: string[] }) => {
      const ids = clips(input.clipIds);
      const before = x.project();
      const after = edit((p) => duplicateClips(p, ids), "couldn't duplicate them (a locked track)");
      const added = pastedIds(before, after);
      x.setSelection(selectClips(added));
      return { clips: place(after, added) };
    },
  );

  reg(
    "editor.setClipsMuted",
    {
      description: "Mute or unmute several clips.",
      input: obj({ clipIds: idsSchema, muted: { type: "boolean" } }, ["clipIds", "muted"]),
      ui: {
        id: "clips.mute",
        label: (c) => count(c, hits(c).every((h) => h.clip.muted) ? "Unmute" : "Mute"),
        group: "Audio",
        icon: VolumeX,
        clips: "many",
        scopes: ["clip"],
        enabled: notAllLocked,
        args: (c) => ({ clipIds: c.clipIds, muted: !hits(c).every((h) => h.clip.muted) }),
      },
    },
    (input: { clipIds: string[]; muted: boolean }) => {
      const ids = clips(input.clipIds);
      edit((p) => setClipsMuted(p, ids, !!input.muted), input.muted ? "they're already muted (or on locked tracks)" : "they're already unmuted (or on locked tracks)");
      return { muted: !!input.muted, clipIds: ids };
    },
  );

  reg(
    "editor.copyClips",
    {
      description: "Copy clips to the editor's clip clipboard (Ctrl+C): for editor.pasteClips and editor.pasteAttributes, in this window.",
      input: obj({ clipIds: idsSchema }, ["clipIds"]),
      ui: { id: "clips.copy", label: (c) => count(c, "Copy"), group: "Edit", icon: Copy, shortcut: "Ctrl+C", order: 20, scopes: ["clip"], enabled: some, args: (c) => ({ clipIds: c.clipIds }) },
    },
    (input: { clipIds: string[] }) => {
      const ids = clips(input.clipIds);
      clipboard.set(copyClips(x.project(), ids));
      return { copied: ids.length };
    },
  );

  reg(
    "editor.cutClips",
    {
      description: "Copy clips to the clip clipboard and delete them, leaving gaps (Ctrl+X).",
      input: obj({ clipIds: idsSchema }, ["clipIds"]),
      ui: { id: "clips.cut", label: (c) => count(c, "Cut"), group: "Edit", icon: Scissors, shortcut: "Ctrl+X", order: 21, enabled: noneLocked, args: (c) => ({ clipIds: c.clipIds }) },
    },
    (input: { clipIds: string[] }) => {
      const ids = clips(input.clipIds);
      const cb = copyClips(x.project(), ids);
      edit((p) => removeClips(p, ids), "nothing to cut: their tracks are locked");
      clipboard.set(cb);
      x.setSelection(null);
      return { cut: ids.length };
    },
  );

  reg(
    "editor.pasteClips",
    {
      description:
        "Paste the copied clips (Ctrl+V) at t, or the playhead: at the first time from there where they all fit, keeping their spacing. The lowest copied track lands on trackId (or the first unlocked track of its kind); tracks are added if needed. Returns the new clips.",
      input: obj({ t: { type: "number", description: "Seconds. Default: the playhead." }, trackId: { type: "string", description: "From editor.get. Optional." } }),
      // From a gap's menu: there, on that track; else at the playhead, on
      // the selected clip's track.
      ui: {
        id: "clips.paste",
        label: (c) => (c.from === "menu" ? "Paste here" : "Paste"),
        group: "Edit",
        icon: ClipboardPaste,
        shortcut: "Ctrl+V",
        order: 22,
        scopes: ["gap"],
        enabled: () => (clipboard.get() ? true : "Copy a clip first"),
        args: (c) => ({ t: c.t, trackId: c.trackId }),
      },
    },
    (input: { t?: number; trackId?: string }) => {
      const cb = clipboard.get();
      if (!cb) throw new Error("nothing copied: use editor.copyClips first");
      const t = input.t === undefined || input.t === null ? x.playhead() : Number(input.t);
      if (!Number.isFinite(t) || t < 0) throw new Error("t must be seconds >= 0");
      const before = x.project();
      const after = edit((p) => pasteClips(p, cb, t, input.trackId), "couldn't paste there (a locked track)");
      const added = pastedIds(before, after);
      x.setSelection(selectClips(added));
      return { clips: place(after, added) };
    },
  );

  reg(
    "editor.pasteAttributes",
    {
      description:
        "Give clips another clip's look and sound (Ctrl+Alt+V): effects, green screen and filters; position, scale, opacity, rotation and their keyframes; volume, mute, fades and volume keyframes. Not its speed, direction or length. Copies from fromClipId, or the first copied clip. parts limits what's copied.",
      input: obj(
        {
          clipIds: idsSchema,
          fromClipId: { type: "string", description: "The clip to copy from. Default: the first clip on the clip clipboard." },
          parts: { type: "array", items: { type: "string", enum: [...ALL_ATTRIBUTES] }, description: "effects, transform, audio. Default: all three." },
        },
        ["clipIds"],
      ),
      ui: {
        id: "clips.pasteAttributes",
        label: "Paste attributes",
        group: "Edit",
        icon: ClipboardPaste,
        shortcut: "Ctrl+Alt+V",
        order: 23,
        keywords: "look style effects copy settings",
        scopes: ["clip"],
        enabled: (c) => (!clipboard.get() ? "Copy a clip first" : notAllLocked(c)),
        hint: () => "The copied clip's effects, position, opacity and sound",
        args: (c) => ({ clipIds: c.clipIds }),
      },
    },
    (input: { clipIds: string[]; fromClipId?: string; parts?: string[] }) => {
      const ids = clips(input.clipIds);
      const src = input.fromClipId ? findClip(x.project(), String(input.fromClipId))?.clip : clipboard.get()?.items[0]?.clip;
      if (!src) throw new Error(input.fromClipId ? `no clip "${input.fromClipId}"` : "nothing copied: give fromClipId or use editor.copyClips first");
      const parts = (input.parts?.length ? input.parts : ALL_ATTRIBUTES).filter((s): s is AttributeSet => (ALL_ATTRIBUTES as readonly string[]).includes(s));
      if (!parts.length) throw new Error("parts must be effects, transform and/or audio");
      edit((p) => pasteAttributes(p, src, ids.filter((id) => id !== src.id), parts), "nothing changed (they already match, or are on locked tracks)");
      x.setSelection(selectClips(ids, ids[0]));
      return { clipIds: ids, parts };
    },
  );
}
