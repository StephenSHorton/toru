import { useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, RefreshCw, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import { checkFont, DEFAULT_FONT_CSS, DEFAULT_FONT_LABEL, fontFamily, fontInfo, fontsFailed, loadFonts, useFonts, type FontInfo } from "@/trim/fonts";

// FontPicker chooses a text's font from the installed ones: a button showing
// the current font in its own face, opening a searchable dropdown (below the
// button, or above when there's more room there) where each name is drawn
// in its font. Files load only as their rows scroll into view
// (some are many MB). Picking a font the export can't draw (checkFont) shows
// why and keeps the old one. value "" is the default font.

export function FontPicker({ value, onChange }: { value: string; onChange: (font: string) => void }) {
  const fonts = useFonts();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState<string | null>(null);
  const search = useRef<HTMLInputElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const panel = useRef<HTMLDivElement>(null);
  const [place, setPlace] = useState<CSSProperties | null>(null);

  const current = fontInfo(value);
  const missing = !!value && !!fonts && !current;

  const shown = useMemo(() => {
    const q = query.trim().toLowerCase();
    const list = (fonts ?? []).filter((f) => f.name !== DEFAULT_FONT_LABEL); // listed first, as the default
    return q ? list.filter((f) => f.name.toLowerCase().includes(q) || f.family.toLowerCase().includes(q)) : list;
  }, [fonts, query]);

  // The dropdown mounts once it's placed (a frame after open), so focus then.
  const placed = !!place;
  useEffect(() => {
    if (open && placed) search.current?.focus();
  }, [open, placed]);

  // Place the dropdown against the button in the window.
  useLayoutEffect(() => {
    if (!open) {
      setPlace(null);
      return;
    }
    const placeIt = () => {
      const r = trigger.current?.getBoundingClientRect();
      if (!r) return;
      const below = innerHeight - r.bottom - 8;
      const above = r.top - 8;
      const down = below >= 260 || below >= above;
      setPlace({
        position: "fixed",
        left: r.left,
        width: r.width,
        maxHeight: Math.min(360, down ? below : above),
        ...(down ? { top: r.bottom + 2 } : { bottom: innerHeight - r.top + 2 }),
      });
    };
    placeIt();
    addEventListener("resize", placeIt);
    return () => removeEventListener("resize", placeIt);
  }, [open]);

  // A press outside the button and the dropdown closes it.
  useEffect(() => {
    if (!open) return;
    const onDown = (e: PointerEvent) => {
      const t = e.target as Node;
      if (!panel.current?.contains(t) && !trigger.current?.contains(t)) setOpen(false);
    };
    addEventListener("pointerdown", onDown, true);
    return () => removeEventListener("pointerdown", onDown, true);
  }, [open]);

  const pick = async (name: string) => {
    setError(null);
    if (name) {
      setBusy(name);
      const why = await checkFont(name);
      setBusy(null);
      if (why) {
        setError(why);
        return;
      }
    }
    setOpen(false);
    setQuery("");
    if (name !== value) onChange(name);
  };

  return (
    <div className="flex flex-col gap-1">
      <div className="flex items-center gap-2">
        <span className="w-8 shrink-0 text-[11px] text-muted-foreground">Font</span>
        <button
          ref={trigger}
          type="button"
          aria-label="Font"
          aria-expanded={open}
          onClick={() => {
            setOpen((o) => !o);
            setError(null);
          }}
          className="flex h-7 min-w-0 flex-1 items-center justify-between gap-2 border border-border bg-background px-2 text-left text-sm hover:bg-accent focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          <span className="truncate" style={{ fontFamily: current ? fontFamily(current) : DEFAULT_FONT_CSS, fontWeight: current ? 400 : 600 }}>
            {value || DEFAULT_FONT_LABEL}
          </span>
          <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" />
        </button>
      </div>
      {missing ? <p className="pl-10 text-[11px] text-muted-foreground">Not installed on this PC: shown and exported in {DEFAULT_FONT_LABEL}.</p> : null}
      {open && place
        ? createPortal(
            <div
              ref={panel}
              style={place}
              className="z-50 flex flex-col border border-border bg-popover text-popover-foreground shadow-lg"
              // Keep presses in the dropdown away from the player behind it.
              onPointerDown={(e) => e.stopPropagation()}
              onClick={(e) => e.stopPropagation()}
            >
              <div className="flex items-center gap-1.5 border-b border-border px-2">
                <Search className="size-3.5 shrink-0 text-muted-foreground" />
                <input
                  ref={search}
                  value={query}
                  placeholder="Search fonts"
                  aria-label="Search fonts"
                  onChange={(e) => setQuery(e.target.value)}
                  onKeyDown={(e) => {
                    e.stopPropagation(); // keep the editor's shortcuts out of the search box
                    if (e.key === "Escape") {
                      e.preventDefault();
                      setOpen(false);
                    } else if (e.key === "Enter" && shown.length) {
                      e.preventDefault();
                      void pick(shown[0].name);
                    }
                  }}
                  className="h-7 min-w-0 flex-1 bg-transparent text-sm outline-none"
                />
                <Button
                  variant="ghost"
                  size="icon"
                  className="size-6"
                  title="Reload the installed fonts"
                  aria-label="Reload the installed fonts"
                  onClick={() => void loadFonts(true).catch(() => {})}
                >
                  <RefreshCw className="size-3" />
                </Button>
              </div>
              <div role="listbox" aria-label="Installed fonts" className="min-h-0 flex-1 overflow-y-auto py-1">
                {!query.trim() || DEFAULT_FONT_LABEL.toLowerCase().includes(query.trim().toLowerCase()) ? (
              <FontRow label={DEFAULT_FONT_LABEL} selected={!value || value === DEFAULT_FONT_LABEL} onPick={() => void pick("")} />
            ) : null}
                {fonts === null ? <p className="px-2 py-1 text-[11px] text-muted-foreground">Loading fonts…</p> : null}
                {fonts && !shown.length ? (
                  <p className="px-2 py-1 text-[11px] text-muted-foreground">
                    {fontsFailed() ? "Couldn't read the installed fonts. Reload to try again." : "No installed font matches."}
                  </p>
                ) : null}
                {shown.map((f) => (
                  <FontRow key={f.name} font={f} label={f.name} selected={f.name === value} busy={busy === f.name} onPick={() => void pick(f.name)} />
                ))}
              </div>
              {error ? <p className="border-t border-border px-2 py-1 text-[11px] text-destructive">{error}</p> : null}
            </div>,
            document.body,
          )
        : null}
    </div>
  );
}

// FontRow is one font in the list, drawn in its own face once it scrolls
// into view (until then, in the UI font).
function FontRow({ font, label, selected, busy, onPick }: { font?: FontInfo; label: string; selected: boolean; busy?: boolean; onPick: () => void }) {
  const ref = useRef<HTMLButtonElement>(null);
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    const el = ref.current;
    if (!el || !font || visible) return;
    const io = new IntersectionObserver((es) => {
      if (es.some((e) => e.isIntersecting)) {
        setVisible(true);
        io.disconnect();
      }
    });
    io.observe(el);
    return () => io.disconnect();
  }, [font, visible]);
  const style = font ? (visible ? { fontFamily: fontFamily(font), fontWeight: 400 } : undefined) : { fontFamily: DEFAULT_FONT_CSS, fontWeight: 600 };
  return (
    <button
      ref={ref}
      type="button"
      role="option"
      aria-selected={selected}
      title={label}
      onClick={onPick}
      className={cn("flex w-full items-center gap-1.5 px-2 py-1 text-left text-[15px] leading-tight hover:bg-accent", selected && "bg-accent/60", busy && "opacity-60")}
    >
      <Check className={cn("size-3 shrink-0", !selected && "invisible")} />
      <span className="truncate" style={style}>
        {label}
      </span>
    </button>
  );
}
