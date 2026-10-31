import type { EffectInstance, ParamValue } from "@/trim/project";
import { paramDefault, type EffectDef, type ParamDef } from "@/trim/fx/def";
import { valueAt } from "@/trim/fx/keyframes";

import adjustDef from "../../../../internal/fx/defs/adjust.json";
import blurDef from "../../../../internal/fx/defs/blur.json";
import chromaKeyDef from "../../../../internal/fx/defs/chromaKey.json";
import gaussianBlurDef from "../../../../internal/fx/defs/gaussianBlur.json";
import lookDef from "../../../../internal/fx/defs/look.json";

// registry.ts loads the effect definitions (internal/fx/defs/*.json, the
// same files Go embeds in internal/fx/registry.go) and resolves an effect
// stack's params, with the same clamping rules as Go's Def.Resolve.
//
// The defs are imported one by one (not with import.meta.glob) so the
// parity script, which bundles this with Bun, can load them too. A new
// effect adds its import here and its shader in types/index.ts.

export const defs: EffectDef[] = [adjustDef, blurDef, chromaKeyDef, gaussianBlurDef, lookDef] as EffectDef[];

const byType = new Map(defs.map((d) => [d.type, d]));

// defOf is the definition of an effect type (undefined for unknown types,
// which the renderer skips).
export function defOf(type: string): EffectDef | undefined {
  return byType.get(type);
}

// Resolved holds every param of a definition, clamped (see resolveParams).
export type Resolved = Record<string, ParamValue>;

// ResolvedEffect is one stack entry ready to draw: its params at a time,
// clamped, and its intensity (group amount, 1 when absent).
export type ResolvedEffect = { id: string; type: string; params: Resolved; amount: number; group?: string };

const HEX = /^#[0-9a-f]{6}$/i;

// clampParam fits v to the param, or returns undefined when v can't be read
// as the param's type (as Go's ParamDef.Clamp).
export function clampParam(p: ParamDef, v: unknown): ParamValue | undefined {
  switch (p.type) {
    case "number":
      return typeof v === "number" && !Number.isNaN(v) ? Math.max(p.min, Math.min(p.max, v)) : undefined;
    case "boolean":
      return typeof v === "boolean" ? v : undefined;
    case "color":
      return typeof v === "string" && HEX.test(v) ? v.toLowerCase() : undefined;
    case "enum":
      return typeof v === "string" && p.options.some((o) => o.id === v) ? v : undefined;
    case "point":
      if (!Array.isArray(v) || v.length !== 2 || v.some((n) => typeof n !== "number" || Number.isNaN(n))) return undefined;
      return [Math.max(0, Math.min(1, v[0])), Math.max(0, Math.min(1, v[1]))];
    case "file":
      return typeof v === "string" ? v : undefined;
  }
}

// resolveParams is every param of def: given values clamped, missing or
// unreadable ones at their default, unknown ones dropped.
export function resolveParams(def: EffectDef, params: Record<string, unknown>): Resolved {
  const out: Resolved = {};
  for (const p of def.params) out[p.id] = clampParam(p, params[p.id]) ?? paramDefault(p);
  return out;
}

// resolveStack is a clip's enabled effects at clip-local time t (seconds
// from the clip's start on the timeline), keyframes applied and params
// clamped. Unknown types are left out. skip drops types (the preview skips
// chromaKey while the colour picker reads the raw frame).
export function resolveStack(effects: EffectInstance[], t: number, opts: { skip?: string[] } = {}): ResolvedEffect[] {
  const out: ResolvedEffect[] = [];
  for (const e of effects) {
    const def = defOf(e.type);
    if (!def || !e.enabled || opts.skip?.includes(e.type)) continue;
    const base = resolveParams(def, e.params);
    const params: Resolved = {};
    for (const p of def.params) {
      const keys = e.keys?.[p.id];
      params[p.id] = keys?.length ? (clampParam(p, valueAt(keys, t, base[p.id])) ?? base[p.id]) : base[p.id];
    }
    const amount = typeof e.amount === "number" && !Number.isNaN(e.amount) ? Math.max(0, Math.min(1, e.amount)) : 1;
    out.push({ id: e.id, type: e.type, params, amount, group: e.group });
  }
  return out;
}
