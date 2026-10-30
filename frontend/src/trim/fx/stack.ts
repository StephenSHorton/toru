import { migrateClipEffects } from "@/trim/migrate";
import type { Clip, EffectInstance } from "@/trim/project";
import { resolveStack, type ResolvedEffect } from "@/trim/fx/registry";

// stack.ts is the effect stack a preview frame draws for a clip
// (renderer.ts). A clip that hasn't been migrated to v3 yet (the editor
// still loads v2 projects until both preview and export draw v3 effects)
// draws its v2 chroma and filters through the same migration that will
// convert it, so it looks the same before and after.

const migrated = new WeakMap<Clip, EffectInstance[]>();

// clipEffects is a clip's effect stack: its own, or its v2 settings as v3.
export function clipEffects(c: Clip): EffectInstance[] {
  if (c.effects) return c.effects;
  let fx = migrated.get(c);
  if (!fx) {
    fx = migrateClipEffects(c.chroma, c.filters);
    migrated.set(c, fx);
  }
  return fx;
}

// clipStack is what the renderer draws for c at clip-local time t (seconds
// from the clip's start on the timeline). raw leaves out the green screen,
// for the colour picker that reads the clip's own pixels.
export function clipStack(c: Clip, t: number, raw = false): ResolvedEffect[] {
  return resolveStack(clipEffects(c), t, raw ? { skip: ["chromaKey"] } : {});
}
