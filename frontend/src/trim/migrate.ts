import { newId } from "@/trim/edl";
import { DEFAULT_FILTERS, type Chroma, type Clip, type EffectInstance, type Filters, type Project } from "@/trim/project";

// migrate.ts turns a v2 project into v3 (docs/video-editor-v3.md, Migration
// v2 → v3): each clip's chroma and filters become an effect stack that draws
// the same pixels. v2 draws green screen, then clamp(look · saturation),
// then clamp(contrast/brightness), then blur, so the stack is:
//
//   chromaKey{color, similarity, blend}
//   with a look:  look{look, amount: 1, saturation}   then adjust{contrast, brightness}
//   without one:  adjust{saturation, contrast, brightness}
//   blur{amount}                                     (box blur, v2's units)
//
// leaving out anything at its default. Every effect clamps its output to
// 0..1, as v2 and the ffmpeg chains do, so saturation can't be its own
// adjust in front of a look (that would clamp between the two where v2
// doesn't); the look takes it as its own param, applied before its matrix.
// Contrast/brightness stays after the look: a look matrix and that step
// don't commute (only bw's rows sum to 1).
//
// These type and param names are the contract the effect definitions
// (internal/fx/defs) must keep: chromaKey; adjust runs saturation, then
// clamps, then contrast/brightness (saturation 0..3, contrast 0..2,
// brightness -1..1); look is clamp(look · saturation) blended by amount, with
// v2's bw/sepia/vintage/cool/warm matrices among its options; blur is a box
// blur, amount 0..20 at 1080 lines.

// migrateClipEffects is the effect stack for a v2 clip's chroma and filters.
export function migrateClipEffects(chroma: Chroma | undefined, filters: Filters | undefined): EffectInstance[] {
  const f = { ...DEFAULT_FILTERS, ...(filters ?? {}) };
  const out: EffectInstance[] = [];
  const add = (type: string, params: EffectInstance["params"]) => out.push({ id: newId("fx"), type, enabled: true, params });
  if (chroma) add("chromaKey", { color: chroma.color, similarity: chroma.similarity, blend: chroma.blend });
  const sat = f.saturation !== 1;
  const cb = f.contrast !== 1 || f.brightness !== 0;
  if (f.look !== "none") {
    add("look", sat ? { look: f.look, amount: 1, saturation: f.saturation } : { look: f.look, amount: 1 });
    if (cb) add("adjust", { contrast: f.contrast, brightness: f.brightness });
  } else if (sat || cb) {
    const p: EffectInstance["params"] = {};
    if (sat) p.saturation = f.saturation;
    if (cb) Object.assign(p, { contrast: f.contrast, brightness: f.brightness });
    add("adjust", p);
  }
  if (f.blur > 0) add("blur", { amount: f.blur });
  return out;
}

// migrateClip is a v2 clip as v3: effects instead of chroma and filters
// (filters stays at its defaults so v2-shaped code that still reads it sees
// "no filters"). A clip that already has effects is left alone.
export function migrateClip(c: Clip): Clip {
  if (c.effects) return c;
  const { chroma, ...rest } = c;
  return { ...rest, filters: { ...DEFAULT_FILTERS }, effects: migrateClipEffects(chroma, c.filters) };
}

// migrateProject is a project as v3 (the same object when it already is).
export function migrateProject(p: Project): Project {
  if (p.version === 3) return p;
  return { ...p, version: 3, tracks: p.tracks.map((t) => ({ ...t, clips: t.clips.map(migrateClip) })) };
}
