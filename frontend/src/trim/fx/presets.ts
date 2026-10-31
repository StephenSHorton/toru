import { useEffect, useState } from "react";
import { newId } from "@/trim/edl";
import { defOf } from "@/trim/fx/registry";
import { editEffects } from "@/trim/fx/ops";
import { clipEffects } from "@/trim/fx/stack";
import { clipLength, findClip, type Clip, type EffectInstance, type Keyframe, type ParamValue, type Project } from "@/trim/project";

// presets.ts is the editor side of saved filters (docs/video-editor-v3.md,
// User presets; Go: internal/presets). A preset is a named effect stack.
// Saving takes a clip's enabled effects; keyframe times are stored as
// fractions (0..1) of the clip's length so they fit a clip of any length.
// Applying appends the effects as a group (group = the preset's id), which
// the Effects tab shows as one card with an intensity; replace swaps the
// whole stack instead.

export type PresetEffect = {
  type: string;
  enabled: boolean;
  params: Record<string, ParamValue>;
  keys?: Record<string, Keyframe[]>; // t: fraction of the clip's length
};

export type PresetDoc = {
  version: 1;
  id: string;
  name: string;
  createdAt?: string;
  effects: PresetEffect[];
  thumb?: string;
  builtin?: boolean;
};

const PRESET_VERSION = 1;
const round6 = (n: number) => Math.round(n * 1e6) / 1e6;

// presetFromClip is a new preset of the clip's enabled, known effects (no
// ids, groups or intensities), or undefined when it has none.
export function presetFromClip(c: Clip, name: string, thumb?: string): Omit<PresetDoc, "id"> | undefined {
  const len = clipLength(c);
  const effects: PresetEffect[] = [];
  for (const e of clipEffects(c)) {
    if (!e.enabled || !defOf(e.type)) continue;
    const out: PresetEffect = { type: e.type, enabled: true, params: structuredClone(e.params) };
    if (e.keys) {
      const keys: Record<string, Keyframe[]> = {};
      for (const [param, ks] of Object.entries(e.keys)) {
        if (!ks?.length) continue;
        keys[param] = ks.map((k) => ({ ...k, t: len > 0 ? round6(Math.max(0, Math.min(1, k.t / len))) : 0 }));
      }
      if (Object.keys(keys).length) out.keys = keys;
    }
    effects.push(out);
  }
  if (!effects.length) return undefined;
  return { version: PRESET_VERSION, name: name.trim(), effects, ...(thumb ? { thumb } : {}) };
}

// instancesOf turns a preset into effect instances for a clip of length len
// seconds: new ids, keyframe fractions scaled back to clip-local seconds,
// unknown types dropped.
export function instancesOf(preset: Pick<PresetDoc, "id" | "effects">, len: number, group?: string): EffectInstance[] {
  const out: EffectInstance[] = [];
  for (const e of preset.effects) {
    if (!defOf(e.type)) continue;
    const inst: EffectInstance = { id: newId("fx"), type: e.type, enabled: e.enabled !== false, params: structuredClone(e.params ?? {}) };
    if (e.keys) {
      const keys: Record<string, Keyframe[]> = {};
      for (const [param, ks] of Object.entries(e.keys)) {
        if (ks?.length) keys[param] = ks.map((k) => ({ ...k, t: round6(Math.max(0, Math.min(1, k.t)) * Math.max(0, len)) })).sort((a, b) => a.t - b.t);
      }
      if (Object.keys(keys).length) inst.keys = keys;
    }
    if (group) inst.group = group;
    out.push(inst);
  }
  return out;
}

// applyPreset puts a preset on a clip: appended on top of its stack as one
// group, or with replace, as the whole stack (not grouped). A preset with a
// single effect isn't grouped either: its card already has its own controls.
export function applyPreset(p: Project, clipId: string, preset: Pick<PresetDoc, "id" | "effects">, opts: { replace?: boolean } = {}): Project {
  const c = findClip(p, clipId)?.clip;
  if (!c) return p;
  const len = clipLength(c);
  const grouped = !opts.replace && preset.effects.filter((e) => defOf(e.type)).length > 1;
  const add = instancesOf(preset, len, grouped ? preset.id : undefined);
  if (!add.length) return p;
  return editEffects(p, clipId, (fx) => (opts.replace ? add : [...fx, ...add]));
}

// --- The preset list, shared by every tile and card --------------------------

let list: PresetDoc[] | null = null;
let loading: Promise<void> | null = null;
const subs = new Set<(l: PresetDoc[]) => void>();

// refreshPresets reloads the list from Go and tells every user.
export function refreshPresets(): Promise<void> {
  loading = import("@/lib/api")
    .then(({ PresetService }) => PresetService.List())
    .then((l) => {
      list = l.map((x) => ({ ...x, createdAt: x.createdAt ? String(x.createdAt) : undefined }) as unknown as PresetDoc);
      for (const s of subs) s(list);
    })
    .catch(() => {
      list ??= [];
    })
    .finally(() => {
      loading = null;
    });
  return loading;
}

// presetsNow is the last loaded list (empty before the first load).
export function presetsNow(): PresetDoc[] {
  return list ?? [];
}

// usePresets is the preset list, loaded on first use and kept fresh (Go
// emits presets:changed after agent edits).
export function usePresets(): PresetDoc[] {
  const [l, setL] = useState<PresetDoc[]>(list ?? []);
  useEffect(() => {
    subs.add(setL);
    if (!list && !loading) void refreshPresets();
    else if (list) setL(list);
    let off: (() => void) | undefined;
    void import("@wailsio/runtime")
      .then(({ Events }) => {
        off = Events.On("presets:changed", () => void refreshPresets());
      })
      .catch(() => {});
    return () => {
      subs.delete(setL);
      off?.();
    };
  }, []);
  return l;
}
