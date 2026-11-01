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

const SAVED_KEY = "toru.editor.textPresets"; // before #125: saved styles lived in localStorage
let saved: TextPreset[] | null = null;
let loading: Promise<void> | null = null;
let watching = false;
const listeners = new Set<() => void>();
const EMPTY: TextPreset[] = [];

// savedTextPresets is the styles saved on this PC (Go: internal/presets,
// kind "text", in %AppData%\toru\presets), as last loaded: the same array
// until the list changes (useSyncExternalStore needs that). Empty before
// the first load; ensureTextPresets waits for it.
export function savedTextPresets(): TextPreset[] {
  if (!saved && !loading) void refreshTextPresets();
  return saved ?? EMPTY;
}

type Doc = { id: string; name: string; style?: Record<string, unknown> | null };
const fromDoc = (d: Doc): TextPreset => ({ id: d.id, name: d.name, style: (d.style ?? {}) as TextStyle });

// refreshTextPresets reloads the saved styles from Go, migrating any left in
// localStorage first, and tells every subscriber.
export function refreshTextPresets(): Promise<void> {
  if (loading) return loading;
  loading = (async () => {
    const { PresetService } = await import("@/lib/api");
    // A style Go refuses mustn't hide the rest: list anyway, and the
    // migration tries again next load.
    await migrateLocal(PresetService.Save).catch(() => {});
    const list = ((await PresetService.ListTextStyles()) ?? []) as unknown as Doc[];
    saved = list.map(fromDoc);
    listeners.forEach((l) => l());
    watch();
  })()
    .catch(() => {
      // No Go (a test or the Vite dev page): keep what we have, or show none
      // rather than retrying on every render. localStorage stays put, so a
      // failed migration runs again next time.
      saved ??= [];
    })
    .finally(() => {
      loading = null;
    });
  return loading;
}

// ensureTextPresets is the saved styles once they've loaded (agent commands
// may run before the Text library ever opened).
export async function ensureTextPresets(): Promise<TextPreset[]> {
  if (!saved) await refreshTextPresets();
  return saved ?? EMPTY;
}

// watch reloads when another window or an agent changes the presets.
function watch() {
  if (watching) return;
  watching = true;
  void import("@wailsio/runtime")
    .then(({ Events }) => Events.On("presets:changed", () => void refreshTextPresets()))
    .catch(() => {
      watching = false;
    });
}

// migrateLocal moves the styles saved in localStorage before #125 into the
// preset service, once: each keeps its id (so a retry overwrites rather
// than copies) and its save time (from the id), and the localStorage entry
// goes only after every one is saved.
export async function migrateLocal(save: (doc: string) => Promise<unknown>, storage: Pick<Storage, "getItem" | "removeItem"> | undefined = globalThis.localStorage): Promise<number> {
  let raw: unknown;
  try {
    raw = JSON.parse(storage?.getItem(SAVED_KEY) ?? "null");
  } catch {
    raw = []; // unreadable: nothing to keep, so it goes
  }
  if (raw == null) return 0;
  const list = Array.isArray(raw) ? raw.filter((x) => x && typeof x.id === "string" && typeof x.name === "string" && x.style && typeof x.style === "object") : [];
  for (const x of list as TextPreset[]) {
    const id = /^[A-Za-z0-9-]{1,80}$/.test(x.id) && !x.id.startsWith("builtin-") ? x.id : "";
    const ms = parseInt(x.id.split("-")[1] ?? "", 36);
    const createdAt = Number.isFinite(ms) && ms > 1.5e12 && ms < 4e12 ? new Date(ms).toISOString() : undefined;
    const { opacityKeys: _keys, ...style } = x.style;
    await save(JSON.stringify({ version: 1, kind: "text", id, name: x.name.trim().slice(0, 60) || "My style", style: tidyStyle(style) ?? {}, ...(createdAt ? { createdAt } : {}) }));
  }
  storage?.removeItem(SAVED_KEY);
  return list.length;
}

export function subscribeTextPresets(l: () => void) {
  listeners.add(l);
  return () => listeners.delete(l);
}

async function service() {
  return (await import("@/lib/api")).PresetService;
}

// changed reloads after an edit; a load already under way may predate the
// edit, so it waits for that one and loads again.
async function changed() {
  if (loading) await loading;
  await refreshTextPresets();
}

// saveTextPreset keeps style (without its keyframes) under name.
export async function saveTextPreset(name: string, style: TextStyle | undefined): Promise<TextPreset> {
  const { opacityKeys: _keys, ...rest } = style ?? {};
  const svc = await service();
  const doc = await svc.Save(JSON.stringify({ version: 1, kind: "text", name: name.trim().slice(0, 60) || "My style", style: tidyStyle(rest) ?? {} }));
  await changed();
  return fromDoc(doc as unknown as Doc);
}

export async function deleteTextPreset(id: string) {
  await (await service()).Delete(id);
  await changed();
}

export async function renameTextPreset(id: string, name: string) {
  const n = name.trim().slice(0, 60);
  if (!n) return;
  await (await service()).Rename(id, n);
  await changed();
}

// importTextPreset asks for a shared .torufx text style; false when the
// dialog was cancelled.
export async function importTextPreset(): Promise<boolean> {
  const [, ok] = await (await service()).PickImportTextStyle();
  if (ok) await changed();
  return !!ok;
}

// exportTextPreset asks where to save a style as .torufx; the path written,
// or "" when cancelled.
export async function exportTextPreset(id: string): Promise<string> {
  return (await (await service()).PickExport(id)) ?? "";
}

export function textPresetById(id: string): TextPreset | undefined {
  return [...BUILTIN_TEXT_PRESETS, ...savedTextPresets()].find((p) => p.id === id);
}
