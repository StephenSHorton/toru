import { useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { Command, Keyboard, Search } from "lucide-react";
import { Dialog } from "@/components/ui/dialog";
import { Kbd, KbdGroup } from "@/components/ui/kbd";
import { cn } from "@/lib/utils";
import {
  actionCtx,
  actionForKey,
  allActions,
  comboOf,
  GROUPS,
  labelOf,
  rankActions,
  registerAction,
  runAction,
  typingIn,
  useActions,
  type Action,
  type ActionCtx,
  type Ranked,
} from "@/trim/actions";

// EditorCommands is the editor's keyboard layer over the action registry
// (actions.ts): it dispatches every action's shortcut, and owns the Ctrl+K
// command palette and the shortcuts sheet (?). Mount it once in the editor
// window; enabled false (a dialog owns the keys) turns the shortcuts off.

export function EditorCommands({ enabled = true }: { enabled?: boolean }) {
  const [palette, setPalette] = useState(false);
  const [sheet, setSheet] = useState(false);

  useEffect(() => {
    const offs = [
      registerAction({
        id: "help.palette",
        label: "Search actions",
        group: "Help",
        icon: Command,
        shortcut: "Ctrl+K",
        whileTyping: true,
        noRecent: true,
        keywords: "command palette find",
        exec: () => setPalette((o) => !o),
      }),
      registerAction({ id: "help.shortcuts", label: "Keyboard shortcuts", group: "Help", icon: Keyboard, shortcut: "?", keywords: "keys help cheat sheet", exec: () => setSheet(true) }),
    ];
    return () => offs.forEach((off) => off());
  }, []);

  // The shortcuts. Off while the palette or the sheet is open (they take
  // their own keys) or the window says so.
  const live = enabled && !palette && !sheet;
  useEffect(() => {
    if (!live) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.repeat && e.key !== "+" && e.key !== "-" && e.key !== "=") return;
      const c = actionCtx("key");
      if (!c) return;
      const a = actionForKey(allActions(), comboOf(e), c);
      if (!a || (typingIn(e.target) && !a.whileTyping)) return;
      e.preventDefault();
      runAction(a, c);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [live]);

  return (
    <>
      {palette ? <CommandPalette onClose={() => setPalette(false)} /> : null}
      <ShortcutsSheet open={sheet} onClose={() => setSheet(false)} />
    </>
  );
}

// ---- the palette ------------------------------------------------------------

function CommandPalette({ onClose }: { onClose: () => void }) {
  const actions = useActions();
  const [query, setQuery] = useState("");
  const [active, setActive] = useState(0);
  const listRef = useRef<HTMLDivElement>(null);
  // The state when it opened: the selection and playhead the actions act on.
  const [ctx] = useState(() => actionCtx("palette"));
  const rows = useMemo(() => (ctx ? rankActions(actions, ctx, query) : []), [actions, ctx, query]);

  useEffect(() => setActive(0), [query]);
  useEffect(() => {
    listRef.current?.querySelector(`[data-index="${active}"]`)?.scrollIntoView({ block: "nearest" });
  }, [active]);

  const run = (r: Ranked | undefined) => {
    if (!r || r.why || !ctx) return;
    onClose();
    // After the palette is gone, so focus and keys are back on the page, and
    // with the state as it is now (the playhead moves while playing).
    window.setTimeout(() => {
      const now = actionCtx("palette");
      if (now) runAction(r.action, now);
    }, 0);
  };

  const onKeyDown = (e: React.KeyboardEvent) => {
    // The page's shortcuts listen on window.
    e.stopPropagation();
    const n = rows.length;
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setActive((i) => (n ? (i + 1) % n : 0));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setActive((i) => (n ? (i - 1 + n) % n : 0));
    } else if (e.key === "PageDown" || e.key === "PageUp") {
      e.preventDefault();
      setActive((i) => Math.max(0, Math.min(n - 1, i + (e.key === "PageDown" ? 8 : -8))));
    } else if (e.key === "Enter") {
      e.preventDefault();
      run(rows[active]);
    } else if (e.key === "Escape" || (e.key.toLowerCase() === "k" && (e.ctrlKey || e.metaKey))) {
      e.preventDefault();
      onClose();
    } else if (e.key === "Tab") {
      e.preventDefault();
    }
  };

  // Headings: with no query, Recent and then each group; with one, a flat
  // list best match first.
  const searching = query.trim() !== "";
  let lastHead = "";
  const current = rows[active];

  return createPortal(
    <div className="fixed inset-0 z-50 flex justify-center px-4 pt-[12vh]" onKeyDown={onKeyDown}>
      <div className="absolute inset-0 bg-black/50" onPointerDown={onClose} />
      <div role="dialog" aria-label="Search actions" className="relative z-10 flex h-fit max-h-[70vh] w-[34rem] max-w-full flex-col border border-border bg-popover text-popover-foreground shadow-2xl">
        <div className="flex items-center gap-2 border-b border-border px-3">
          <Search className="size-4 shrink-0 text-muted-foreground" />
          <input
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search actions…"
            className="h-11 min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
            role="combobox"
            aria-expanded
            aria-controls="palette-list"
            aria-activedescendant={current ? `palette-${current.action.id}` : undefined}
          />
          <Kbd>Esc</Kbd>
        </div>
        <div ref={listRef} id="palette-list" role="listbox" aria-label="Actions" className="min-h-0 flex-1 overflow-y-auto py-1">
          {rows.length === 0 ? <div className="px-3 py-6 text-center text-xs text-muted-foreground">No action matches &ldquo;{query}&rdquo;</div> : null}
          {rows.map((r, i) => {
            const head = searching ? "" : r.recent && rows.slice(0, i + 1).every((x) => x.recent) ? "Recent" : r.action.group;
            const showHead = head !== lastHead;
            lastHead = head;
            return (
              <div key={r.action.id}>
                {showHead && head ? <div className="px-3 pb-1 pt-2 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{head}</div> : null}
                <PaletteRow r={r} index={i} active={i === active} searching={searching} onHover={() => setActive(i)} onRun={() => run(r)} />
              </div>
            );
          })}
        </div>
        <div className="flex items-center gap-3 border-t border-border px-3 py-1.5 text-[11px] text-muted-foreground">
          <span className="flex items-center gap-1">
            <Kbd>↑</Kbd>
            <Kbd>↓</Kbd> move
          </span>
          <span className="flex items-center gap-1">
            <Kbd>Enter</Kbd> run
          </span>
          <span className="ml-auto truncate">{current?.why ?? current?.action.hint?.(ctx as ActionCtx) ?? ""}</span>
        </div>
      </div>
    </div>,
    document.body,
  );
}

function PaletteRow({ r, index, active, searching, onHover, onRun }: { r: Ranked; index: number; active: boolean; searching: boolean; onHover: () => void; onRun: () => void }) {
  const Icon = r.action.icon;
  return (
    <div
      id={`palette-${r.action.id}`}
      data-index={index}
      role="option"
      aria-selected={active}
      aria-disabled={!!r.why}
      title={r.why}
      onPointerMove={onHover}
      onClick={onRun}
      className={cn(
        "mx-1 flex h-8 cursor-default items-center gap-2 px-2 text-sm",
        active && "bg-primary/20",
        r.why ? "text-muted-foreground/60" : r.action.destructive ? "text-destructive" : "text-foreground",
      )}
    >
      <span className="flex size-4 shrink-0 items-center justify-center [&_svg]:size-4">{Icon ? <Icon className="opacity-80" /> : null}</span>
      <span className="truncate">{r.label}</span>
      {searching ? <span className="shrink-0 text-[11px] text-muted-foreground">{r.action.group}</span> : null}
      {r.why && active ? <span className="min-w-0 truncate text-[11px] text-muted-foreground">· {r.why}</span> : null}
      <span className="ml-auto shrink-0">{r.action.shortcut ? <Keys combo={r.action.shortcut} /> : null}</span>
    </div>
  );
}

// Keys shows "Ctrl+Shift+Z" as key caps.
export function Keys({ combo }: { combo: string }) {
  const parts = combo === "+" ? ["+"] : combo.split("+").filter(Boolean);
  return (
    <KbdGroup>
      {parts.map((k, i) => (
        <Kbd key={i}>{k}</Kbd>
      ))}
    </KbdGroup>
  );
}

// ---- the shortcuts sheet ------------------------------------------------------

// Mouse and keys that aren't actions.
const GESTURES: [string, string][] = [
  ["Right-click", "Menus on clips, gaps, tracks, the player"],
  ["Ctrl+Wheel", "Zoom the timeline at the pointer"],
  ["Alt", "Hold while dragging to not snap"],
  ["Esc", "Close a menu, or finish cropping"],
];

function ShortcutsSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  const actions = useActions();
  const ctx = open ? actionCtx("palette") : null;
  if (!open) return null;
  const keyed = actions.filter((a) => a.shortcut);
  const byGroup = GROUPS.map((g) => [g, keyed.filter((a) => a.group === g)] as const).filter(([, l]) => l.length);
  return (
    <Dialog open={open} onClose={onClose} className="w-[40rem]">
      <div
        onKeyDown={(e) => {
          e.stopPropagation();
          if (e.key === "Escape") onClose();
        }}
      >
        <h2 className="mb-3 text-sm font-semibold">Keyboard shortcuts</h2>
        <div className="grid max-h-[65vh] grid-cols-1 gap-x-8 gap-y-3 overflow-y-auto sm:grid-cols-2">
          {byGroup.map(([g, list]) => (
            <section key={g}>
              <h3 className="mb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">{g}</h3>
              {list.map((a: Action) => (
                <div key={a.id} className="flex h-7 items-center gap-2 text-xs">
                  <span className="truncate">{ctx ? labelOf(a, ctx) : String(a.label)}</span>
                  <span className="ml-auto shrink-0">
                    <Keys combo={a.shortcut!} />
                  </span>
                </div>
              ))}
            </section>
          ))}
          <section>
            <h3 className="mb-1 text-[10px] font-medium uppercase tracking-wide text-muted-foreground">Mouse</h3>
            {GESTURES.map(([k, what]) => (
              <div key={k} className="flex h-7 items-center gap-2 text-xs">
                <span className="truncate">{what}</span>
                <span className="ml-auto shrink-0">
                  <Keys combo={k} />
                </span>
              </div>
            ))}
          </section>
        </div>
        <p className="mt-3 text-[11px] text-muted-foreground">
          Every action, with or without a key, is in <Keys combo="Ctrl+K" />.
        </p>
      </div>
    </Dialog>
  );
}
