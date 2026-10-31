import type { AgentCommandSpec, AgentHandler } from "@/lib/agent";
import { clipEnd, DEFAULT_FREEZE, findClip, freezeFrame, mediaById, MAX_CURVE_SPEED, MIN_CURVE_SPEED, updateClip, type BlendFrames, type Clip, type Project, type SpeedCurve, type Track } from "@/trim/project";
import { isRampPreset, presetCurve, RAMP_PRESETS } from "@/trim/speed";

// The editor's time-effect agent commands (docs/video-editor-v3.md, Time
// effects): speed curves and their presets, freeze frames, reverse and slow
// motion blending. agentCommands.ts registers them with its own helpers, so
// each one is a single undo step like every other editor command.

export interface TimeCommandCtx {
  project: () => Project;
  playhead: () => number;
  reg: (name: string, spec: AgentCommandSpec, handler: AgentHandler) => void;
  edit: (fn: (p: Project) => Project, why: string) => Project;
  clipOr: (p: Project, clipId: string) => { track: Track; clip: Clip };
  obj: (props?: Record<string, unknown>, required?: string[]) => Record<string, unknown>;
  lockedNote: (locked: boolean) => string;
  selectClip: (id?: string) => void;
}

const presetIds = RAMP_PRESETS.map((p) => p.id);

// timeOf is what the commands return about a clip's timing.
const timeOf = (c: Clip) => ({
  clipId: c.id,
  start: c.start,
  end: clipEnd(c),
  in: c.in,
  out: c.out,
  speed: c.speed,
  speedCurve: c.speedCurve ?? null,
  hold: !!c.hold,
  reversed: !!c.reversed,
  blendFrames: c.blendFrames ?? "off",
});

export function registerTimeCommands(x: TimeCommandCtx) {
  const { reg, edit, clipOr, obj } = x;
  const videoOnly = (c: { track: Track }) => {
    if (c.track.kind !== "video") throw new Error("time effects are for clips on video tracks");
  };

  reg(
    "editor.setSpeedCurve",
    {
      description:
        `Give a clip a speed ramp. Either preset (${presetIds.join(", ")}: laid over the clip's whole source range) or points [{ s, v }]: s = source seconds from the clip's in point, v = speed ${MIN_CURVE_SPEED}..${MAX_CURVE_SPEED}; speed is linear between points and held past the ends. audio: follow (pitch kept, silent outside 0.25..4x; default) or mute (silent wherever it isn't 1x). The clip's length changes with the curve; a curve that would run into the next clip is refused. Pass clear: true to go back to a constant speed.`,
      input: obj(
        {
          clipId: { type: "string", description: "From editor.get." },
          preset: { type: "string", enum: presetIds, description: "A ramp preset." },
          points: {
            type: "array",
            description: "Curve points (instead of preset).",
            items: { type: "object", properties: { s: { type: "number" }, v: { type: "number" } }, required: ["s", "v"] },
          },
          audio: { type: "string", enum: ["follow", "mute"], description: "Sound during the ramp." },
          clear: { type: "boolean", description: "Remove the curve." },
        },
        ["clipId"],
      ),
    },
    (input: { clipId: string; preset?: string; points?: { s: number; v: number }[]; audio?: string; clear?: boolean }) => {
      const hit = clipOr(x.project(), input.clipId);
      const c = hit.clip;
      if (c.hold) throw new Error("a freeze frame has no speed to change");
      if (mediaById(x.project(), c.media)?.kind === "image") throw new Error("an image has no motion to ramp");
      let curve: SpeedCurve | undefined;
      if (!input.clear) {
        if (input.audio !== undefined && input.audio !== "follow" && input.audio !== "mute") throw new Error('audio must be "follow" or "mute"');
        const audio: SpeedCurve["audio"] = (input.audio as SpeedCurve["audio"] | undefined) ?? c.speedCurve?.audio ?? "follow";
        if (input.preset !== undefined) {
          if (!isRampPreset(input.preset)) throw new Error(`unknown preset "${input.preset}" (${presetIds.join(", ")})`);
          curve = presetCurve(input.preset, c.out - c.in, audio);
        } else if (Array.isArray(input.points)) {
          const pts = input.points.map((p) => ({ s: Number(p.s), v: Number(p.v) }));
          if (!pts.length || pts.some((p) => !Number.isFinite(p.s) || !Number.isFinite(p.v))) throw new Error("points must be [{ s, v }] numbers");
          curve = { points: pts, audio };
        } else if (c.speedCurve && input.audio !== undefined) {
          curve = { ...c.speedCurve, audio };
        } else throw new Error("pass preset, points, audio (to change a curve's sound) or clear: true");
      }
      const after = edit(
        (p) => updateClip(p, c.id, { speedCurve: curve }),
        (curve ? "that curve doesn't fit before the next clip, or it's already set" : "the clip has no speed curve") + x.lockedNote(hit.track.locked),
      );
      x.selectClip(c.id);
      return timeOf(findClip(after, c.id)!.clip);
    },
  );

  reg(
    "editor.freezeFrame",
    {
      description: `Freeze the frame at time t (default: the playhead) on a video track: the clip there is split and a still of that frame, seconds long (default ${DEFAULT_FREEZE}), is put in between; everything after it on that track moves right. The still is silent; effects and keyframes work on it.`,
      input: obj(
        {
          trackId: { type: "string", description: "The video track (editor.get)." },
          t: { type: "number", description: "Seconds on the timeline." },
          seconds: { type: "number", description: "How long the freeze lasts, 0.1..10." },
        },
        ["trackId"],
      ),
    },
    (input: { trackId: string; t?: number; seconds?: number }) => {
      const p0 = x.project();
      const track = p0.tracks.find((tr) => tr.id === input.trackId);
      if (!track) throw new Error(`no track "${input.trackId}" (editor.get lists them)`);
      if (track.kind !== "video") throw new Error("freeze frames go on video tracks");
      const t = input.t === undefined ? x.playhead() : Number(input.t);
      if (!Number.isFinite(t) || t < 0) throw new Error("t must be seconds >= 0");
      const len = input.seconds === undefined ? DEFAULT_FREEZE : Number(input.seconds);
      if (!(len >= 0.1 && len <= 10)) throw new Error("seconds must be 0.1..10");
      const after = edit((p) => freezeFrame(p, track.id, t, len), `no video clip to freeze at ${t.toFixed(2)}s` + x.lockedNote(track.locked));
      const had = new Set(p0.tracks.flatMap((tr) => tr.clips.map((c) => c.id)));
      const hold = after.tracks.flatMap((tr) => tr.clips).find((c) => c.hold && !had.has(c.id))!;
      x.selectClip(hold.id);
      return timeOf(hold);
    },
  );

  reg(
    "editor.setReverse",
    {
      description:
        "Play a clip backwards (or forwards again). The whole clip reverses; split it first to reverse part of one. A speed curve then runs over the reversed footage. The preview prepares a reversed copy in the background the first time.",
      input: obj({ clipId: { type: "string", description: "From editor.get." }, reversed: { type: "boolean", description: "Default true." } }, ["clipId"]),
    },
    (input: { clipId: string; reversed?: boolean }) => {
      const hit = clipOr(x.project(), input.clipId);
      videoOnly(hit);
      if (hit.clip.hold) throw new Error("a freeze frame can't be reversed");
      const on = input.reversed !== false;
      const after = edit((p) => updateClip(p, hit.clip.id, { reversed: on }), `the clip already plays ${on ? "backwards" : "forwards"}` + x.lockedNote(hit.track.locked));
      x.selectClip(hit.clip.id);
      return timeOf(findClip(after, hit.clip.id)!.clip);
    },
  );

  reg(
    "editor.setSlowMotion",
    {
      description:
        "How slow motion looks in the export: off (frames repeat), blend (neighbouring frames mixed; quick) or smooth (motion-interpolated in-between frames; a slow export, minutes for a long slow part). Only the parts slower than 1x change. The preview shows repeated frames either way.",
      input: obj({ clipId: { type: "string", description: "From editor.get." }, mode: { type: "string", enum: ["off", "blend", "smooth"] } }, ["clipId", "mode"]),
    },
    (input: { clipId: string; mode: string }) => {
      const hit = clipOr(x.project(), input.clipId);
      videoOnly(hit);
      const mode: BlendFrames | undefined = input.mode === "blend" ? "mix" : input.mode === "smooth" ? "smooth" : input.mode === "off" ? "off" : undefined;
      if (!mode) throw new Error('mode must be "off", "blend" or "smooth"');
      if (hit.clip.hold) throw new Error("a freeze frame has no motion to blend");
      const after = edit((p) => updateClip(p, hit.clip.id, { blendFrames: mode }), "that's already its slow motion" + x.lockedNote(hit.track.locked));
      x.selectClip(hit.clip.id);
      return timeOf(findClip(after, hit.clip.id)!.clip);
    },
  );
}
