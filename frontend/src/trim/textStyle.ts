import type { Text, TextStyle } from "@/trim/edl";
import type { Project } from "@/trim/project";
import { resolveStyle, textOpacityAt, TEXT_DEFAULTS } from "@/trim/textRender";

// textStyle.ts is the edits on a text's style (edl.ts TextStyle): pure
// project → project, for the inspector, the Text library's presets and the
// agent commands. A style keeps only what differs from TEXT_DEFAULTS, so an
// unstyled text stays unstyled in the saved project.

type Key = NonNullable<TextStyle["opacityKeys"]>[number];
const EPS = 1e-3;

function withText(p: Project, id: string, fn: (t: Text) => Text): Project {
  const i = p.texts.findIndex((t) => t.id === id);
  if (i < 0) return p;
  const next = fn(p.texts[i]);
  if (next === p.texts[i]) return p;
  const texts = p.texts.slice();
  texts[i] = next;
  return { ...p, texts };
}

// tidy drops the fields equal to the defaults, and the style when nothing's left.
export function tidyStyle(s: TextStyle | undefined): TextStyle | undefined {
  if (!s) return undefined;
  const out: TextStyle = {};
  for (const [k, v] of Object.entries(s) as [keyof TextStyle, unknown][]) {
    if (v === undefined) continue;
    if (k === "opacityKeys") {
      if (Array.isArray(v) && v.length) out.opacityKeys = v as Key[];
      continue;
    }
    if (v !== (TEXT_DEFAULTS as Record<string, unknown>)[k]) (out as Record<string, unknown>)[k] = v;
  }
  return Object.keys(out).length ? out : undefined;
}

// setTextStyle merges patch into a text's style.
export function setTextStyle(p: Project, id: string, patch: TextStyle): Project {
  return withText(p, id, (t) => {
    const style = tidyStyle({ ...(t.style ?? {}), ...patch });
    return JSON.stringify(style) === JSON.stringify(t.style) ? t : { ...t, style };
  });
}

// replaceTextStyle sets a text's whole style (a preset), keeping its
// opacity keyframes.
export function replaceTextStyle(p: Project, id: string, style: TextStyle | undefined): Project {
  return withText(p, id, (t) => {
    const next = tidyStyle({ ...(style ?? {}), opacityKeys: t.style?.opacityKeys });
    return JSON.stringify(next) === JSON.stringify(t.style) ? t : { ...t, style: next };
  });
}

// setTextOpacity sets the whole-text opacity at local time t (seconds from
// the text's start): the key there when keyed (added when there's none),
// else the static value.
export function setTextOpacity(p: Project, id: string, v: number, t: number): Project {
  v = Math.min(1, Math.max(0, v));
  return withText(p, id, (x) => {
    const ks = x.style?.opacityKeys;
    if (!ks?.length) return { ...x, style: tidyStyle({ ...(x.style ?? {}), opacity: v }) };
    const keys = ks.filter((k) => Math.abs(k.t - t) >= EPS);
    const old = ks.find((k) => Math.abs(k.t - t) < EPS);
    keys.push({ t: Math.max(0, t), v, ease: old?.ease ?? "linear" });
    keys.sort((a, b) => a.t - b.t);
    return { ...x, style: tidyStyle({ ...(x.style ?? {}), opacityKeys: keys }) };
  });
}

// toggleTextOpacityKey adds a key at t with the value shown there, or
// removes the key at t (the last one going leaves its value as the static
// opacity).
export function toggleTextOpacityKey(p: Project, id: string, t: number): Project {
  return withText(p, id, (x) => {
    const s = resolveStyle(x.style);
    const ks = x.style?.opacityKeys ?? [];
    const here = ks.find((k) => Math.abs(k.t - t) < EPS);
    if (here) {
      const left = ks.filter((k) => k !== here);
      return { ...x, style: tidyStyle({ ...(x.style ?? {}), opacityKeys: left, opacity: left.length ? x.style?.opacity : here.v }) };
    }
    const keys = [...ks, { t: Math.max(0, t), v: textOpacityAt(s, t), ease: "linear" as const }].sort((a, b) => a.t - b.t);
    return { ...x, style: tidyStyle({ ...(x.style ?? {}), opacityKeys: keys }) };
  });
}

// --- presets -----------------------------------------------------------------

export type TextPreset = { id: string; name: string; style: TextStyle; builtin?: boolean };

// BUILTIN_TEXT_PRESETS are the Text library's built-in looks.
export const BUILTIN_TEXT_PRESETS: TextPreset[] = [
  { id: "builtin:classic", name: "Classic", style: {}, builtin: true },
  { id: "builtin:clean", name: "Clean", style: { outlineOpacity: 0, shadowOpacity: 0.6, shadowX: 0.03, shadowY: 0.04, shadowBlur: 0.1 }, builtin: true },
  { id: "builtin:bold-outline", name: "Bold outline", style: { bold: true, outlineOpacity: 1, outlineWidth: 0.12 }, builtin: true },
  { id: "builtin:caption", name: "Caption box", style: { outlineOpacity: 0, boxOpacity: 0.65, boxPad: 0.35, boxRadius: 0.25 }, builtin: true },
  { id: "builtin:highlight", name: "Highlight", style: { color: "#111111", outlineOpacity: 0, boxColor: "#ffd60a", boxOpacity: 1, boxPad: 0.25, boxRadius: 0.1, bold: true }, builtin: true },
  { id: "builtin:neon", name: "Neon", style: { color: "#e6fffb", outlineColor: "#00e5ff", outlineOpacity: 1, outlineWidth: 0.05, shadowColor: "#00e5ff", shadowOpacity: 1, shadowX: 0, shadowY: 0, shadowBlur: 0.35 }, builtin: true },
  { id: "builtin:retro", name: "Retro", style: { color: "#ffcc00", outlineColor: "#ff3d7f", outlineOpacity: 1, outlineWidth: 0.08, shadowColor: "#2a0a4a", shadowOpacity: 1, shadowX: 0.08, shadowY: 0.08, shadowBlur: 0, italic: true, bold: true }, builtin: true },
  { id: "builtin:subtle", name: "Subtle", style: { fillOpacity: 0.9, outlineOpacity: 0, shadowOpacity: 0.45, shadowX: 0, shadowY: 0.02, shadowBlur: 0.15, letterSpacing: 0.04 }, builtin: true },
];

const SAVED_KEY = "toru.editor.textPresets";
let saved: TextPreset[] | null = null;
const listeners = new Set<() => void>();

// savedTextPresets is the presets this PC saved (localStorage; they're
// small and only the editor reads them).
export function savedTextPresets(): TextPreset[] {
  if (saved) return saved;
  try {
    const raw = JSON.parse(localStorage.getItem(SAVED_KEY) ?? "[]");
    saved = Array.isArray(raw) ? raw.filter((x) => x && typeof x.id === "string" && typeof x.name === "string" && x.style && typeof x.style === "object") : [];
  } catch {
    saved = [];
  }
  return saved!;
}

function store(list: TextPreset[]) {
  saved = list;
  try {
    localStorage.setItem(SAVED_KEY, JSON.stringify(list));
  } catch {
    // not kept past this session
  }
  listeners.forEach((l) => l());
}

export function subscribeTextPresets(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

// saveTextPreset keeps style (without its keyframes) under name.
export function saveTextPreset(name: string, style: TextStyle | undefined): TextPreset {
  const { opacityKeys: _keys, ...rest } = style ?? {};
  const pre: TextPreset = { id: `text-${Date.now().toString(36)}-${Math.random().toString(36).slice(2, 6)}`, name: name.trim().slice(0, 60) || "My style", style: tidyStyle(rest) ?? {} };
  store([...savedTextPresets(), pre]);
  return pre;
}

export function deleteTextPreset(id: string) {
  store(savedTextPresets().filter((p) => p.id !== id));
}

export function renameTextPreset(id: string, name: string) {
  store(savedTextPresets().map((p) => (p.id === id ? { ...p, name: name.trim().slice(0, 60) || p.name } : p)));
}

export function textPresetById(id: string): TextPreset | undefined {
  return [...BUILTIN_TEXT_PRESETS, ...savedTextPresets()].find((p) => p.id === id);
}
