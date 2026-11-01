import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { Check, Download, Import, Pencil, Plus, Save, Trash2, Type } from "lucide-react";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";
import type { Text } from "@/trim/edl";
import { useFontsVersion } from "@/trim/fonts";
import { Section } from "@/trim/inspector/controls";
import { renderText } from "@/trim/textRender";
import {
  BUILTIN_TEXT_PRESETS,
  deleteTextPreset,
  exportTextPreset,
  importTextPreset,
  renameTextPreset,
  saveTextPreset,
  savedTextPresets,
  subscribeTextPresets,
  tidyStyle,
  type TextPreset,
} from "@/trim/textStyle";

// TextLibrary is the Text entry of the left rail: Add text, and text styles
// as live previews (drawn by the same renderer as the player and the
// export). Clicking a style restyles the selected text, or adds a new text
// in that style when none is selected. "Save style" keeps the selected
// text's look for later (Go keeps it in %AppData%; Import and the tile's
// share button move styles between PCs as .torufx files).

const TILE_W = 240;
const TILE_H = 120;

function PresetPreview({ preset, font, version }: { preset: TextPreset; font?: string; version: number }) {
  const ref = useRef<HTMLCanvasElement>(null);
  useEffect(() => {
    const cv = ref.current;
    if (!cv) return;
    const sample: Text = { id: "preview", text: "Title", x: 0.5, y: 0.5, size: "m", fontPx: 44, font, start: 0, end: 1, style: preset.style };
    const bmp = renderText(sample, TILE_W, TILE_H);
    const ctx = cv.getContext("2d")!;
    ctx.clearRect(0, 0, TILE_W, TILE_H);
    if (bmp) ctx.drawImage(bmp.canvas, bmp.x, bmp.y);
  }, [preset, font, version]);
  return <canvas ref={ref} width={TILE_W} height={TILE_H} className="aspect-[2/1] w-full bg-[linear-gradient(135deg,#3b4a5a,#6b5a7a)]" />;
}

export function TextLibrary({
  selected,
  canAdd,
  onAdd,
  onApply,
}: {
  selected: Text | undefined; // the selected text, if any
  canAdd: boolean;
  onAdd: (preset?: TextPreset) => void;
  onApply: (preset: TextPreset) => void;
}) {
  const saved = useSyncExternalStore(subscribeTextPresets, savedTextPresets);
  const version = useFontsVersion();
  const [naming, setNaming] = useState<null | { id?: string; name: string }>(null);
  const [error, setError] = useState("");
  const run = async (fn: () => Promise<unknown>) => {
    setError("");
    try {
      await fn();
    } catch (err) {
      setError((err instanceof Error ? err.message : String(err)).replace(/^Error:\s*/, ""));
    }
  };
  const current = JSON.stringify(tidyStyle({ ...(selected?.style ?? {}), opacityKeys: undefined }) ?? {});

  const tile = (p: TextPreset) => {
    const on = !!selected && JSON.stringify(p.style) === current;
    return (
      <div key={p.id} className="group relative">
        <button
          type="button"
          disabled={!selected && !canAdd}
          onClick={() => (selected ? onApply(p) : onAdd(p))}
          title={selected ? `Style the selected text: ${p.name}` : `Add text: ${p.name}`}
          className={cn("flex w-full flex-col gap-1 text-left outline-none disabled:opacity-50", on ? "text-foreground" : "text-muted-foreground enabled:hover:text-foreground")}
        >
          <div className={cn("relative border", on ? "border-primary" : "border-border group-hover:border-foreground/40 group-focus-within:border-ring")}>
            <PresetPreview preset={p} font={selected?.font} version={version} />
            {on ? (
              <span className="absolute right-0.5 top-0.5 grid size-4 place-items-center bg-primary text-primary-foreground">
                <Check className="size-3" />
              </span>
            ) : null}
          </div>
          <span className="truncate text-[11px]">{p.name}</span>
        </button>
        {!p.builtin ? (
          <div className="absolute left-0.5 top-0.5 hidden gap-0.5 group-focus-within:flex group-hover:flex">
            <TileAction icon={Pencil} label={`Rename ${p.name}`} onClick={() => setNaming({ id: p.id, name: p.name })} />
            <TileAction icon={Download} label={`Share ${p.name} as a file`} onClick={() => void run(() => exportTextPreset(p.id))} />
            <TileAction icon={Trash2} label={`Delete ${p.name}`} danger onClick={() => void run(() => deleteTextPreset(p.id))} />
          </div>
        ) : null}
      </div>
    );
  };

  const save = () => {
    if (!naming) return;
    const n = naming;
    setNaming(null);
    void run(async () => {
      if (n.id) await renameTextPreset(n.id, n.name);
      else if (selected) await saveTextPreset(n.name, selected.style);
    });
  };

  return (
    <div className="flex flex-col">
      <div className="flex flex-col gap-2 p-3">
        <Button size="sm" variant="secondary" className="justify-start" disabled={!canAdd} onClick={() => onAdd()} title="Add text at the playhead">
          <Type /> Add text
        </Button>
        <p className="text-[11px] text-muted-foreground">
          {selected ? "Click a style to apply it to the selected text." : "Click a style to add text in it at the playhead. Select a text to restyle it."}
        </p>
      </div>
      <Section title="Styles">
        <div className="grid grid-cols-2 gap-1.5">{BUILTIN_TEXT_PRESETS.map(tile)}</div>
      </Section>
      <Section
        title="My styles"
        action={
          <div className="flex items-center gap-0.5">
            <Button size="sm" variant="ghost" className="h-6 gap-1 px-1.5 text-[11px]" onClick={() => void run(importTextPreset)} title="Add a text style someone shared (.torufx)">
              <Import className="size-3" /> Import
            </Button>
            <Button size="sm" variant="ghost" className="h-6 gap-1 px-1.5 text-[11px]" disabled={!selected || !!naming} onClick={() => setNaming({ name: "" })} title={selected ? "Save the selected text's style" : "Select a text to save its style"}>
              <Save className="size-3" /> Save style
            </Button>
          </div>
        }
      >
        {naming ? (
          <form
            className="flex items-center gap-1"
            onSubmit={(e) => {
              e.preventDefault();
              save();
            }}
          >
            <input
              autoFocus
              value={naming.name}
              maxLength={60}
              onChange={(e) => setNaming({ ...naming, name: e.target.value })}
              onKeyDown={(e) => {
                e.stopPropagation();
                if (e.key === "Escape") setNaming(null);
              }}
              placeholder="Style name"
              aria-label="Style name"
              className="h-6 min-w-0 flex-1 border border-border bg-background px-1.5 text-xs outline-none focus-visible:ring-1 focus-visible:ring-ring"
            />
            <Button type="submit" size="sm" className="h-6 px-2 text-[11px]">
              {naming.id ? "Rename" : "Save"}
            </Button>
            <Button type="button" size="sm" variant="ghost" className="h-6 px-2 text-[11px]" onClick={() => setNaming(null)}>
              Cancel
            </Button>
          </form>
        ) : null}
        {error ? <p className="mb-1.5 text-[11px] text-destructive">{error}</p> : null}
        {saved.length ? <div className="grid grid-cols-2 gap-1.5">{saved.map(tile)}</div> : <p className="text-[11px] text-muted-foreground">Style a text, select it and press Save style to reuse its look.</p>}
      </Section>
    </div>
  );
}

function TileAction({ icon: Icon, label, onClick, danger }: { icon: typeof Plus; label: string; onClick: () => void; danger?: boolean }) {
  return (
    <button
      type="button"
      className={cn("grid size-5 place-items-center bg-background/90 text-muted-foreground outline-none hover:text-foreground focus-visible:ring-1 focus-visible:ring-ring", danger && "hover:text-destructive")}
      title={label}
      aria-label={label}
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
    >
      <Icon className="size-3" />
    </button>
  );
}
