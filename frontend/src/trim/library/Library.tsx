import { useCallback, useRef, useState, type ReactNode } from "react";
import { ArrowLeftRight, Captions, Clapperboard, Music, Palette, Sparkles, Type, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

// The editor's left column in layout B (docs/editor-layout-proposal.md): an
// icon rail of libraries (things you add) that opens one library panel at a
// time. Clicking the open entry again collapses the panel, giving the player
// the room. Properties of what's selected stay in the inspector on the right.
//
// The whole column is the Explorer drop target, so dropping files on the
// editor imports them whichever library is open, or none.

export type LibraryId = "media" | "text" | "effects" | "transitions" | "audio" | "captions" | "filters";

type Entry = { id: LibraryId; label: string; icon: typeof Type; hint: string; soon?: boolean };

// soon: planned (Captions); hidden from the rail until built.
export const LIBRARIES: Entry[] = [
  { id: "media", label: "Media", icon: Clapperboard, hint: "Your clips, recordings and imported files" },
  { id: "text", label: "Text", icon: Type, hint: "Add text over the video" },
  { id: "effects", label: "Effects", icon: Sparkles, hint: "Add effects to the selected clip" },
  { id: "transitions", label: "Transitions", icon: ArrowLeftRight, hint: "Transitions between clips" },
  { id: "audio", label: "Audio", icon: Music, hint: "Music and sound files" },
  { id: "captions", label: "Captions", icon: Captions, hint: "Captions", soon: true },
  { id: "filters", label: "Filters", icon: Palette, hint: "Looks, LUTs and your saved filters for the selected clip" },
];

const shown = LIBRARIES.filter((e) => !e.soon);

const OPEN_KEY = "toru.editor.library";
const WIDTH_KEY = "toru.editor.libraryWidth";
export const LIBRARY_MIN_W = 220;
export const LIBRARY_MAX_W = 520;
const DEFAULT_W = 280;

function read<T>(key: string, parse: (v: string) => T | undefined, fallback: T): T {
  try {
    const v = localStorage.getItem(key);
    if (v !== null) return parse(v) ?? fallback;
  } catch {
    // storage blocked: the default
  }
  return fallback;
}

function write(key: string, v: string) {
  try {
    localStorage.setItem(key, v);
  } catch {
    // not remembered; fine
  }
}

const clampW = (w: number) => Math.round(Math.max(LIBRARY_MIN_W, Math.min(LIBRARY_MAX_W, w)));

// useLibrary is which library is open (null: collapsed) and the panel's
// width, both remembered.
export function useLibrary() {
  // "" in storage: collapsed.
  const [open, setOpenState] = useState<LibraryId | null>(() =>
    read<LibraryId | "" | null>(OPEN_KEY, (v) => (v === "" || shown.some((e) => e.id === v) ? (v as LibraryId | "") : undefined), "media") || null,
  );
  const [width, setWidthState] = useState(() => read(WIDTH_KEY, (v) => (Number.isFinite(Number(v)) ? clampW(Number(v)) : undefined), DEFAULT_W));
  const setOpen = useCallback((id: LibraryId | null) => {
    setOpenState(id);
    write(OPEN_KEY, id ?? "");
  }, []);
  const toggle = useCallback((id: LibraryId) => setOpenState((cur) => {
    const next = cur === id ? null : id;
    write(OPEN_KEY, next ?? "");
    return next;
  }), []);
  const setWidth = useCallback((w: number, save: boolean) => {
    const c = clampW(w);
    setWidthState(c);
    if (save) write(WIDTH_KEY, String(c));
  }, []);
  return { open, setOpen, toggle, width, setWidth };
}

export type LibraryState = ReturnType<typeof useLibrary>;

export function LibraryColumn({ lib, render, className }: { lib: LibraryState; render: (id: LibraryId) => ReactNode; className?: string }) {
  const entry = shown.find((e) => e.id === lib.open);
  const drag = useRef<{ x: number; w: number } | null>(null);
  const railRef = useRef<HTMLDivElement>(null);

  // Arrow keys move between rail entries (a vertical tab list).
  const onRailKey = (e: React.KeyboardEvent) => {
    if (e.key !== "ArrowDown" && e.key !== "ArrowUp" && e.key !== "Home" && e.key !== "End") return;
    const btns = Array.from(railRef.current?.querySelectorAll<HTMLButtonElement>("[role=tab]") ?? []);
    const i = btns.indexOf(document.activeElement as HTMLButtonElement);
    const n = btns.length;
    const j = e.key === "Home" ? 0 : e.key === "End" ? n - 1 : (i + (e.key === "ArrowDown" ? 1 : -1) + n) % n;
    btns[j]?.focus();
    e.preventDefault();
  };

  return (
    <div
      // The runtime forwards Explorer drops only onto elements with this.
      data-file-drop-target
      className={cn(
        "flex min-h-0 shrink-0 gap-2",
        "[&.file-drop-target-active]:bg-primary/10 [&.file-drop-target-active]:outline [&.file-drop-target-active]:outline-2 [&.file-drop-target-active]:-outline-offset-2 [&.file-drop-target-active]:outline-primary",
        className,
      )}
    >
      <div ref={railRef} role="tablist" aria-orientation="vertical" aria-label="Libraries" className="frost flex w-16 shrink-0 flex-col gap-0.5 py-1.5" onKeyDown={onRailKey}>
        {shown.map((e) => {
          const Icon = e.icon;
          const on = e.id === lib.open;
          return (
            <button
              key={e.id}
              type="button"
              role="tab"
              id={`lib-tab-${e.id}`}
              aria-selected={on}
              aria-controls={on ? "lib-panel" : undefined}
              tabIndex={on || (!lib.open && e.id === shown[0].id) ? 0 : -1}
              onClick={() => lib.toggle(e.id)}
              title={on ? `${e.hint} (click to hide)` : e.hint}
              className={cn(
                "mx-1 flex flex-col items-center gap-1 border-l-2 px-0.5 py-2 text-[10.5px] outline-none transition-colors focus-visible:ring-1 focus-visible:ring-ring",
                on ? "border-primary bg-accent text-foreground" : "border-transparent text-muted-foreground hover:bg-accent/50 hover:text-foreground",
              )}
            >
              <Icon className="size-4" />
              {e.label}
            </button>
          );
        })}
      </div>

      {entry ? (
        <section
          id="lib-panel"
          role="tabpanel"
          aria-labelledby={`lib-tab-${entry.id}`}
          className="frost relative flex min-h-0 flex-col text-xs"
          style={{ width: lib.width }}
        >
          <div className="flex shrink-0 items-center gap-2 border-b border-border/60 py-1.5 pl-3 pr-1">
            <h2 className="text-sm font-medium">{entry.label}</h2>
            <Button size="icon" variant="ghost" className="ml-auto size-6 [&_svg]:size-3.5" onClick={() => lib.setOpen(null)} title="Hide the library" aria-label="Hide the library">
              <X />
            </Button>
          </div>
          <div className="min-h-0 flex-1 overflow-y-auto overflow-x-hidden">{render(entry.id)}</div>
          <div
            role="separator"
            aria-orientation="vertical"
            aria-label="Library width"
            aria-valuemin={LIBRARY_MIN_W}
            aria-valuemax={LIBRARY_MAX_W}
            aria-valuenow={lib.width}
            tabIndex={0}
            title="Drag to resize"
            className="absolute inset-y-0 -right-1.5 z-10 w-2 cursor-ew-resize outline-none hover:bg-primary/30 focus-visible:bg-primary/40"
            onPointerDown={(e) => {
              e.currentTarget.setPointerCapture(e.pointerId);
              drag.current = { x: e.clientX, w: lib.width };
            }}
            onPointerMove={(e) => drag.current && lib.setWidth(drag.current.w + e.clientX - drag.current.x, false)}
            onPointerUp={(e) => {
              if (!drag.current) return;
              lib.setWidth(drag.current.w + e.clientX - drag.current.x, true);
              drag.current = null;
            }}
            onPointerCancel={() => (drag.current = null)}
            onKeyDown={(e) => {
              if (e.key === "ArrowLeft" || e.key === "ArrowRight") {
                e.preventDefault();
                lib.setWidth(lib.width + (e.key === "ArrowRight" ? 20 : -20), true);
              }
            }}
          />
        </section>
      ) : null}
    </div>
  );
}
