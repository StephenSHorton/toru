import { useSyncExternalStore } from "react";
import type { LucideIcon } from "lucide-react";
import type { ContextMenuItem } from "@/components/ui/context-menu";
import type { AgentCommandSpec } from "@/lib/agent";
import { liveIds } from "@/trim/multi";
import { findClip, type Project } from "@/trim/project";

// actions.ts is the video editor's action registry: one list behind the
// Ctrl+K palette, the right-click menus (clip, gap, track header, viewer),
// the shortcuts sheet and the keyboard shortcuts.
//
// Most actions come from the agent commands: an editor command declares how
// it looks to the person with `ui` next to its description
// (agentCommands*.ts), and the editor's `reg` registers the command for
// agents and its actions here together. A new command with a `ui` shows up
// in the palette, its menus and the sheet on its own. An action with no
// agent command (zoom, crop mode, open the palette) is registered with
// registerAction by the part of the window that owns its state.
//
// Running an action from the UI calls the agent command's handler with
// ui.args(ctx), so it is the same code an agent runs (one undo step, the
// same checks), unless the action has its own run (a dialog instead of a
// path argument, say).

export type Scope = "clip" | "gap" | "track" | "viewer";

export interface ActionState {
  project: Project;
  playhead: number;
  playing: boolean;
  selection: { kind: "clip"; id: string; ids?: string[] } | { kind: "text"; id: string } | null;
  cropping?: boolean;
}

// ActionCtx is what an action sees: the editor's state plus its target, from
// a menu (the clip, track or gap right-clicked) or, from the palette and the
// keyboard, the selection and the playhead.
export interface ActionCtx extends ActionState {
  from: "menu" | "palette" | "key";
  clipId?: string;
  // The clips it acts on: the selection, or from a clip's menu that clip
  // (the whole selection when the clip is one of several selected).
  clipIds: string[];
  trackId?: string;
  textId?: string;
  t: number; // timeline seconds: the gap's time from a gap menu, else the playhead
}

export interface ActionUI {
  id: string; // stable, e.g. "clip.split"; recents are kept by it
  label: string | ((c: ActionCtx) => string);
  group: Group;
  icon?: LucideIcon;
  // Shown as key caps (palette, menus, sheet), e.g. "S", "Shift+Del",
  // "Ctrl+Z", "Space". keys are what the keyboard dispatcher matches
  // (default: [shortcut]); [] shows the shortcut without handling it here.
  shortcut?: string;
  keys?: readonly string[];
  whileTyping?: boolean; // its keys work in a text field too (Ctrl+K)
  noRecent?: boolean; // not listed under Recent (opening the palette itself)
  keywords?: string; // extra words the palette matches
  scopes?: Scope[]; // the menus it's in; every action is in the palette
  order?: number; // within its group (default: registration order)
  destructive?: boolean;
  // Left out where it makes no sense (Hide track on an audio track).
  hidden?: (c: ActionCtx) => boolean;
  // For one clip or several: "one" is left out while several clips are
  // selected, "many" unless they are (Delete vs Delete 3 clips).
  clips?: "one" | "many";
  // true, or why not: shown as the disabled item's tooltip.
  enabled?: (c: ActionCtx) => true | string;
  hint?: (c: ActionCtx) => string | undefined; // tooltip when enabled
  args?: (c: ActionCtx) => Record<string, unknown>;
  run?: (c: ActionCtx) => unknown;
  // In a menu, a submenu of choices instead of one item (e.g. a transition
  // per type). The palette runs the action itself (args/run): its default.
  choices?: (c: ActionCtx) => ActionChoice[];
}

export interface ActionChoice {
  label: string;
  icon?: LucideIcon;
  why?: string; // disabled, and why
  run: () => unknown;
}

// EditorCommandSpec is an editor agent command with how it looks to the
// person. ui isn't sent to agents.
export interface EditorCommandSpec extends AgentCommandSpec {
  ui?: ActionUI | ActionUI[];
}

export interface Action extends ActionUI {
  command?: string; // the agent command behind it
  exec: (c: ActionCtx) => unknown;
}

// Groups, in menu and sheet order; menus put a separator between groups.
export const GROUPS = ["Edit", "Time", "Audio", "Clip", "Track", "Insert", "Playback", "View", "History", "File", "Help"] as const;
export type Group = (typeof GROUPS)[number];

// ---- the registry -----------------------------------------------------------

const actions = new Map<string, Action>();
let seq = 0;
const orderOf = new Map<string, number>();
let snapshot: Action[] = [];
const listeners = new Set<() => void>();
let stateOf: (() => ActionState) | null = null;

function changed() {
  snapshot = [...actions.values()].sort(
    (a, b) => GROUPS.indexOf(a.group) - GROUPS.indexOf(b.group) || (a.order ?? orderOf.get(a.id)!) - (b.order ?? orderOf.get(b.id)!),
  );
  for (const l of listeners) l();
}

// registerAction adds an action; the returned function removes it.
// Registering an id again replaces it (and keeps its place).
export function registerAction(a: Action): () => void {
  if (!orderOf.has(a.id)) orderOf.set(a.id, ++seq);
  actions.set(a.id, a);
  changed();
  return () => {
    if (actions.get(a.id) === a) {
      actions.delete(a.id);
      changed();
    }
  };
}

// commandActions are the actions of an agent command's ui: running one calls
// the handler with ui.args, unless it has its own run.
export function commandActions(name: string, spec: EditorCommandSpec, handler: (input: Record<string, unknown>) => unknown): Action[] {
  const list = spec.ui ? (Array.isArray(spec.ui) ? spec.ui : [spec.ui]) : [];
  return list.map((ui) => ({ ...ui, command: name, exec: ui.run ?? ((c) => handler(ui.args?.(c) ?? {})) }));
}

export function allActions(): Action[] {
  return snapshot;
}

export function useActions(): Action[] {
  return useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => snapshot,
  );
}

// provideActionState tells the registry where the editor's state is (the
// window's agent-command hook does).
export function provideActionState(get: (() => ActionState) | null) {
  stateOf = get;
}

// actionCtx is the context for target (a menu's), or for the selection and
// the playhead when there's none.
export function actionCtx(from: ActionCtx["from"], target?: { clipId?: string; trackId?: string; t?: number }): ActionCtx | null {
  const s = stateOf?.();
  if (!s) return null;
  const sel = s.selection;
  const selected = sel?.kind === "clip" ? liveIds(s.project, sel.ids?.length ? sel.ids : [sel.id]) : [];
  if (target) {
    const trackId = target.trackId ?? (target.clipId ? findClip(s.project, target.clipId)?.track.id : undefined);
    const clipIds = target.clipId ? (selected.length > 1 && selected.includes(target.clipId) ? selected : [target.clipId]) : [];
    return { ...s, from, ...target, clipIds, trackId, t: target.t ?? s.playhead };
  }
  const clipId = sel?.kind === "clip" ? sel.id : undefined;
  return {
    ...s,
    from,
    clipId,
    clipIds: selected,
    trackId: clipId ? findClip(s.project, clipId)?.track.id : undefined,
    textId: sel?.kind === "text" ? sel.id : undefined,
    t: s.playhead,
  };
}

// shown: whether a fits c (not hidden, right for one clip or several).
export const shown = (a: ActionUI, c: ActionCtx) =>
  !a.hidden?.(c) && (a.clips === "one" ? c.clipIds.length <= 1 : a.clips === "many" ? c.clipIds.length > 1 : true);

export const labelOf = (a: ActionUI, c: ActionCtx) => (typeof a.label === "function" ? a.label(c) : a.label);
export const whyNot = (a: ActionUI, c: ActionCtx): string | undefined => {
  const ok = a.enabled ? a.enabled(c) : true;
  return ok === true ? undefined : ok;
};

// ---- running and recents ----------------------------------------------------

const RECENT_KEY = "toru.editor.recentActions";
const RECENT_MAX = 8;

export function recentIds(): string[] {
  try {
    const v = JSON.parse(localStorage.getItem(RECENT_KEY) ?? "[]");
    return Array.isArray(v) ? v.filter((x) => typeof x === "string") : [];
  } catch {
    return [];
  }
}

function remember(id: string) {
  try {
    const next = [id, ...recentIds().filter((x) => x !== id)].slice(0, RECENT_MAX);
    localStorage.setItem(RECENT_KEY, JSON.stringify(next));
  } catch {
    // Storage off: no recents.
  }
}

// runAction runs a (enabled) action and remembers it for the palette. An
// error (the project changed underneath, a dialog failed) goes to the
// console: the disabled state already explains the usual reasons.
export function runAction(a: Action, c: ActionCtx) {
  if (whyNot(a, c) !== undefined) return;
  if (!a.noRecent) remember(a.id);
  try {
    const r = a.exec(c);
    if (r instanceof Promise) r.catch((err) => console.warn(`${a.id}:`, err));
  } catch (err) {
    console.warn(`${a.id}:`, err);
  }
}

// ---- menus ------------------------------------------------------------------

// menuFor is the right-click menu for scope at target: its actions in group
// order, a separator between groups. extra is appended as its own group.
export function menuFor(scope: Scope, target?: { clipId?: string; trackId?: string; t?: number }, extra: ContextMenuItem[] = []): ContextMenuItem[] {
  const c = actionCtx("menu", target);
  if (!c) return extra;
  const out: ContextMenuItem[] = [];
  let group: Group | null = null;
  for (const a of snapshot) {
    if (!a.scopes?.includes(scope) || !shown(a, c)) continue;
    if (group && a.group !== group) out.push({ kind: "separator" });
    group = a.group;
    out.push(menuItem(a, c));
  }
  if (extra.length) {
    if (out.length) out.push({ kind: "separator" });
    out.push(...extra);
  }
  return out;
}

export function menuItem(a: Action, c: ActionCtx): ContextMenuItem {
  const why = whyNot(a, c);
  if (a.choices && why === undefined) {
    const sub = a.choices(c).map(
      (ch): ContextMenuItem => ({
        label: ch.label,
        icon: ch.icon,
        disabled: !!ch.why,
        hint: ch.why,
        onSelect: () => {
          remember(a.id);
          try {
            const r = ch.run();
            if (r instanceof Promise) r.catch((err) => console.warn(`${a.id}:`, err));
          } catch (err) {
            console.warn(`${a.id}:`, err);
          }
        },
      }),
    );
    return { kind: "sub", label: labelOf(a, c), icon: a.icon, items: sub };
  }
  return {
    label: labelOf(a, c),
    icon: a.icon,
    shortcut: a.shortcut,
    destructive: a.destructive,
    disabled: why !== undefined,
    hint: why ?? a.hint?.(c),
    onSelect: () => runAction(a, c),
  };
}

// ---- the palette's matching -------------------------------------------------

// matchScore scores query against text: higher is better, 0 is no match.
// A whole-text prefix beats a word prefix beats letters in order (closer
// together is better), so "spl" finds "Split at playhead" first and "xa"
// still finds "Extract audio".
export function matchScore(query: string, text: string, fuzzy = true): number {
  const q = query.trim().toLowerCase();
  if (!q) return 1;
  const s = text.toLowerCase();
  if (s.startsWith(q)) return 1000 - s.length;
  const word = s.indexOf(" " + q);
  if (word >= 0) return 800 - word - s.length / 100;
  const at = s.indexOf(q);
  if (at >= 0) return 600 - at - s.length / 100;
  if (!fuzzy) return 0;
  // Letters in order; each skipped letter costs.
  let i = 0;
  let gaps = 0;
  let last = -1;
  for (let j = 0; j < s.length && i < q.length; j++) {
    if (s[j] === q[i]) {
      if (last >= 0) gaps += j - last - 1;
      last = j;
      i++;
    }
  }
  if (i < q.length) return 0;
  return Math.max(1, 400 - gaps * 10 - s.length / 100);
}

export interface Ranked {
  action: Action;
  label: string;
  why?: string;
  recent: boolean;
}

// rankActions is the palette's list for query: with no query, recent actions
// first then everything in group order; with one, the matches best first
// (label, group and keywords count; a recent one gets a small lift).
export function rankActions(list: Action[], c: ActionCtx, query: string, recents: string[] = recentIds()): Ranked[] {
  const visible = list.filter((a) => shown(a, c));
  const rec = new Map(recents.map((id, i) => [id, i]));
  const item = (a: Action): Ranked => ({ action: a, label: labelOf(a, c), why: whyNot(a, c), recent: rec.has(a.id) });
  if (!query.trim()) {
    const recent = visible.filter((a) => rec.has(a.id)).sort((a, b) => rec.get(a.id)! - rec.get(b.id)!);
    return [...recent, ...visible.filter((a) => !rec.has(a.id))].map(item);
  }
  const scored: { r: Ranked; score: number }[] = [];
  for (const a of visible) {
    const r = item(a);
    const score = Math.max(matchScore(query, r.label), matchScore(query, a.keywords ?? "", false) * 0.9, matchScore(query, a.group, false) * 0.5);
    if (score > 0) scored.push({ r, score: score + (rec.has(a.id) ? 50 - rec.get(a.id)! : 0) + (r.why ? 0 : 5) });
  }
  return scored.sort((x, y) => y.score - x.score).map((x) => x.r);
}

// ---- keyboard shortcuts -------------------------------------------------------

// comboOf is a key event as a shortcut string, e.g. "Ctrl+Shift+Z", "Del",
// "Space", "?". Shift isn't named for keys that aren't letters or named
// keys ("?" is Shift+/ on most layouts, "+" Shift+=).
export function comboOf(e: Pick<KeyboardEvent, "key" | "ctrlKey" | "metaKey" | "shiftKey" | "altKey">): string {
  const names: Record<string, string> = { " ": "Space", Delete: "Del", Backspace: "Backspace", Escape: "Esc", ArrowLeft: "Left", ArrowRight: "Right", ArrowUp: "Up", ArrowDown: "Down" };
  let key = names[e.key] ?? e.key;
  const letter = /^[a-z]$/i.test(key);
  const named = key.length > 1;
  if (letter) key = key.toUpperCase();
  const mods: string[] = [];
  if (e.ctrlKey || e.metaKey) mods.push("Ctrl");
  if (e.altKey) mods.push("Alt");
  if (e.shiftKey && (letter || named)) mods.push("Shift");
  return [...mods, key].join("+");
}

// actionForKey is the action whose keys match combo, if any; with c, the
// first that fits c (Del is Delete for one clip, Delete N clips for several).
export function actionForKey(list: Action[], combo: string, c?: ActionCtx): Action | undefined {
  return list.find((a) => (a.keys ?? (a.shortcut ? [a.shortcut] : [])).includes(combo) && (!c || shown(a, c)));
}

// typingIn: keys go to text fields, not to shortcuts.
export function typingIn(t: EventTarget | null): boolean {
  const el = t as HTMLElement | null;
  return !!el && (el.tagName === "INPUT" || el.tagName === "TEXTAREA" || el.tagName === "SELECT" || el.isContentEditable);
}
