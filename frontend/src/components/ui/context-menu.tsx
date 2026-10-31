import * as React from "react";
import { createPortal } from "react-dom";
import { ChevronRight, type LucideIcon } from "lucide-react";
import { Kbd } from "@/components/ui/kbd";
import { cn } from "@/lib/utils";

// ContextMenu — a right-click menu in Toru's sharp, dark style (no Radix,
// like dialog.tsx). The caller opens it at the pointer with a list of
// items: actions (with an optional shortcut hint), separators and one level
// of submenus. It renders in a portal on <body> so a frosted parent (a
// backdrop-filter makes `fixed` relative to it) can't clip or shift it, and
// it stays inside the window.
//
// Keys: ↑/↓ (and Home/End) move, → or Enter opens a submenu, ← closes it,
// Enter/Space runs an item, Escape closes. Keys never reach the page's own
// shortcuts while the menu is open. A press outside, a resize or the
// window losing focus closes it.

export type ContextMenuItem =
  | {
      kind?: "item";
      label: string;
      icon?: LucideIcon;
      shortcut?: string; // shown as a key cap, e.g. "S" or "Shift+Del"
      disabled?: boolean;
      destructive?: boolean;
      hint?: string; // tooltip, e.g. why it's disabled
      onSelect: () => void;
    }
  | { kind: "separator" }
  | { kind: "sub"; label: string; icon?: LucideIcon; disabled?: boolean; items: ContextMenuItem[] };

export interface ContextMenuProps {
  x: number;
  y: number; // viewport px, where the pointer was
  items: ContextMenuItem[];
  onClose: () => void;
  label?: string; // accessible name
}

const GAP = 4; // px kept from the window's edges

export function ContextMenu({ x, y, items, onClose, label }: ContextMenuProps) {
  const root = React.useRef<HTMLDivElement>(null);

  React.useEffect(() => {
    const outside = (e: PointerEvent) => {
      if (!(e.target instanceof Node) || !root.current?.contains(e.target)) onClose();
    };
    const close = () => onClose();
    window.addEventListener("pointerdown", outside, true);
    window.addEventListener("resize", close);
    window.addEventListener("blur", close);
    return () => {
      window.removeEventListener("pointerdown", outside, true);
      window.removeEventListener("resize", close);
      window.removeEventListener("blur", close);
    };
  }, [onClose]);

  return createPortal(
    <div ref={root} onContextMenu={(e) => e.preventDefault()}>
      <MenuPanel x={x} y={y} items={items} onClose={onClose} label={label} autoFocus />
    </div>,
    document.body,
  );
}

function MenuPanel({
  x,
  y,
  items,
  onClose,
  onBack,
  label,
  autoFocus,
  anchor,
}: {
  x: number;
  y: number;
  items: ContextMenuItem[];
  onClose: () => void;
  onBack?: () => void; // a submenu: ← closes just it
  label?: string;
  autoFocus?: boolean;
  anchor?: DOMRect; // a submenu's parent item: flips left when there's no room right
}) {
  const ref = React.useRef<HTMLDivElement>(null);
  const [pos, setPos] = React.useState<{ left: number; top: number } | null>(null);
  const [openSub, setOpenSub] = React.useState<{ index: number; rect: DOMRect; focus: boolean } | null>(null);
  const hoverTimer = React.useRef<number | undefined>(undefined);

  React.useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const w = el.offsetWidth;
    const h = el.offsetHeight;
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    let left = x;
    if (left + w > vw - GAP) left = anchor ? anchor.left - w : vw - GAP - w;
    let top = y;
    if (top + h > vh - GAP) top = anchor ? vh - GAP - h : y - h;
    setPos({ left: Math.max(GAP, left), top: Math.max(GAP, top) });
  }, [x, y, anchor]);

  React.useEffect(() => {
    if (autoFocus && pos) focusItem(ref.current, 0);
  }, [autoFocus, pos]);

  React.useEffect(() => () => window.clearTimeout(hoverTimer.current), []);

  const onKeyDown = (e: React.KeyboardEvent) => {
    // The page's shortcuts (S split, Delete…) listen on window.
    e.stopPropagation();
    const el = ref.current;
    if (!el) return;
    switch (e.key) {
      case "ArrowDown":
        e.preventDefault();
        moveFocus(el, 1);
        break;
      case "ArrowUp":
        e.preventDefault();
        moveFocus(el, -1);
        break;
      case "Home":
        e.preventDefault();
        focusItem(el, 0);
        break;
      case "End":
        e.preventDefault();
        focusItem(el, -1);
        break;
      case "ArrowLeft":
        if (onBack) {
          e.preventDefault();
          onBack();
        }
        break;
      case "Escape":
        e.preventDefault();
        onClose();
        break;
      case "Tab":
        e.preventDefault();
        break;
    }
  };

  const openAt = (index: number, target: HTMLElement, focus: boolean) => setOpenSub({ index, rect: target.getBoundingClientRect(), focus });

  return (
    <>
      <div
        ref={ref}
        data-menu-panel
        role="menu"
        aria-label={label}
        tabIndex={-1}
        className="fixed z-[60] min-w-48 border border-border bg-popover py-1 text-xs text-popover-foreground shadow-lg outline-none"
        style={{ left: pos?.left ?? x, top: pos?.top ?? y, visibility: pos ? "visible" : "hidden" }}
        onKeyDown={onKeyDown}
        onPointerDown={(e) => e.stopPropagation()}
      >
        {items.map((item, i) => {
          if (item.kind === "separator") return <div key={`sep-${i}`} role="separator" className="my-1 h-px bg-border/70" />;
          const Icon = item.icon;
          const isSub = item.kind === "sub";
          const open = isSub && openSub?.index === i;
          return (
            <button
              key={`${item.label}-${i}`}
              type="button"
              role="menuitem"
              data-menu-item
              aria-haspopup={isSub ? "menu" : undefined}
              aria-expanded={isSub ? open : undefined}
              disabled={item.disabled}
              title={!isSub ? item.hint : undefined}
              className={cn(
                "flex w-full items-center gap-2 px-2.5 py-1.5 text-left outline-none",
                "hover:bg-accent focus-visible:bg-accent disabled:pointer-events-none disabled:opacity-45",
                open && "bg-accent",
                !isSub && item.destructive && "text-destructive",
              )}
              onPointerEnter={(e) => {
                window.clearTimeout(hoverTimer.current);
                const target = e.currentTarget;
                if (isSub) hoverTimer.current = window.setTimeout(() => openAt(i, target, false), 120);
                else if (openSub) hoverTimer.current = window.setTimeout(() => setOpenSub(null), 200);
              }}
              onKeyDown={(e) => {
                if (isSub && (e.key === "ArrowRight" || e.key === "Enter" || e.key === " ")) {
                  e.preventDefault();
                  e.stopPropagation();
                  openAt(i, e.currentTarget, true);
                }
              }}
              onClick={(e) => {
                if (isSub) {
                  openAt(i, e.currentTarget, true);
                  return;
                }
                onClose();
                item.onSelect();
              }}
            >
              <span className="flex size-3.5 shrink-0 items-center justify-center">{Icon ? <Icon className="size-3.5 opacity-80" /> : null}</span>
              <span className="flex-1 truncate">{item.label}</span>
              {!isSub && item.shortcut ? <Kbd className="h-4 min-w-4 px-1 text-[10px]">{item.shortcut}</Kbd> : null}
              {isSub ? <ChevronRight className="size-3.5 opacity-70" /> : null}
            </button>
          );
        })}
      </div>
      {openSub && items[openSub.index]?.kind === "sub" ? (
        <MenuPanel
          key={openSub.index}
          x={openSub.rect.right}
          y={openSub.rect.top - 5}
          anchor={openSub.rect}
          items={(items[openSub.index] as Extract<ContextMenuItem, { kind: "sub" }>).items}
          onClose={onClose}
          onBack={() => {
            const idx = openSub.index;
            setOpenSub(null);
            ref.current?.querySelectorAll<HTMLElement>("[data-menu-item]")[itemsBefore(items, idx)]?.focus();
          }}
          label={(items[openSub.index] as { label: string }).label}
          autoFocus={openSub.focus}
        />
      ) : null}
    </>
  );
}

// itemsBefore is the index among the rendered buttons of items[i].
function itemsBefore(items: ContextMenuItem[], i: number): number {
  return items.slice(0, i).filter((x) => x.kind !== "separator").length;
}

function enabledItems(panel: HTMLElement | null): HTMLElement[] {
  if (!panel) return [];
  return Array.from(panel.querySelectorAll<HTMLButtonElement>(":scope > [data-menu-item]")).filter((b) => !b.disabled);
}

function focusItem(panel: HTMLElement | null, index: number) {
  const list = enabledItems(panel);
  if (!list.length) {
    panel?.focus();
    return;
  }
  list[index < 0 ? list.length - 1 : Math.min(index, list.length - 1)].focus();
}

function moveFocus(panel: HTMLElement, dir: 1 | -1) {
  const list = enabledItems(panel);
  if (!list.length) return;
  const at = list.indexOf(document.activeElement as HTMLElement);
  const next = at < 0 ? (dir > 0 ? 0 : list.length - 1) : (at + dir + list.length) % list.length;
  list[next].focus();
}
