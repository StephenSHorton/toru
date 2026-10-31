import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Check, Download, Import, Pencil, Plus, Save, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/segmented";
import { PresetService, type Lut } from "@/lib/api";
import { cn } from "@/lib/utils";
import { useClipFrame } from "@/trim/fx/frame";
import { forgetLut, loadLut, lutState, useLutsVersion } from "@/trim/fx/luts";
import { addEffect, newEffect, removeEffect, setValue } from "@/trim/fx/ops";
import { applyPreset, instancesOf, presetFromClip, refreshPresets, usePresets, type PresetDoc } from "@/trim/fx/presets";
import { defOf, resolveParams } from "@/trim/fx/registry";
import { clipEffects } from "@/trim/fx/stack";
import { renderThumb } from "@/trim/fx/thumb";
import { Section } from "@/trim/inspector/controls";
import type { TabProps } from "@/trim/inspector/Inspector";
import { Thumb } from "@/trim/inspector/Thumb";
import type { EffectInstance, Media } from "@/trim/project";

// FiltersBrowser is the Filters library (the rail on the left, layout B):
// one-click colour for the selected clip, shown as live thumbnails of its
// current frame (docs/video-editor-v3.md, Inspector UX "Filters"):
//   Looks       the look effect's built-in looks (None removes it)
//   LUTs        imported .cube files, applied as the clip's lut effect;
//               "Import LUT…" adds one
//   My filters  saved filters: click appends one as a group with an
//               intensity, shift-click replaces the clip's effects; save the
//               clip's effects as a new one, rename, delete, share as .torufx
// "All" shows the three sections together. An applied look or LUT is tuned
// in the inspector's Effects tab, like any effect.

type View = "all" | "looks" | "luts" | "mine";
const VIEW_KEY = "toru.edit.filtersView";

function readView(): View {
  try {
    const v = localStorage.getItem(VIEW_KEY);
    if (v === "all" || v === "looks" || v === "luts" || v === "mine") return v;
  } catch {
    // storage blocked: the default
  }
  return "all";
}

// FiltersBrowser applies to target, the selected visual clip (undefined: the
// tiles show, greyed, with why). preview is the media the thumbnails draw
// when there's no target.
export function FiltersBrowser({ target, preview, note }: { target?: TabProps; preview?: Media; note: string }) {
  const [view, setView] = useState<View>(readView);
  const pick = (v: View) => {
    setView(v);
    try {
      localStorage.setItem(VIEW_KEY, v);
    } catch {
      // not remembered
    }
  };
  const frame = useClipFrame(target?.media ?? preview, target?.srcT ?? 1);
  const props: SectionProps = { target, frame };
  return (
    <div className="flex flex-col gap-2 pt-2">
      <div className="px-3">
      <Segmented
        size="xs"
        value={view}
        onChange={pick}
        aria-label="Filters"
        options={[
          { value: "all", label: "All" },
          { value: "looks", label: "Looks" },
          { value: "luts", label: "LUTs" },
          { value: "mine", label: "My filters" },
        ]}
      />
      </div>
      {!target ? <div className="px-3 text-[11px] text-muted-foreground">{note}</div> : null}
      <fieldset disabled={!target || target.track.locked} className="min-w-0 disabled:opacity-50">
        {view === "all" || view === "looks" ? <Looks {...props} /> : null}
        {view === "all" || view === "luts" ? <Luts {...props} /> : null}
        {view === "all" || view === "mine" ? <MyFilters {...props} /> : null}
      </fieldset>
    </div>
  );
}

type SectionProps = { target?: TabProps; frame: HTMLCanvasElement | null };

// Tile is a thumbnail button with a caption, a tick when on and optional
// hover actions.
function Tile({
  frame,
  stack,
  label,
  on,
  title,
  onClick,
  actions,
  version,
}: {
  frame: HTMLCanvasElement | null;
  stack: EffectInstance[];
  label: ReactNode;
  on?: boolean;
  title: string;
  onClick: (e: React.MouseEvent) => void;
  actions?: ReactNode;
  version?: number;
}) {
  return (
    <div className="group relative flex flex-col gap-1">
      <button
        type="button"
        onClick={onClick}
        className={cn("flex flex-col gap-1 text-left outline-none", on ? "text-foreground" : "text-muted-foreground hover:text-foreground")}
        aria-pressed={on}
        title={title}
      >
        <div className={cn("relative border", on ? "border-primary" : "border-border group-hover:border-foreground/40 group-focus-within:border-ring")}>
          <Thumb key={version} frame={frame} stack={stack} />
          {on ? (
            <span className="absolute right-0.5 top-0.5 grid size-4 place-items-center bg-primary text-primary-foreground">
              <Check className="size-3" />
            </span>
          ) : null}
        </div>
        <span className="truncate text-[11px]">{label}</span>
      </button>
      {actions ? <div className="absolute left-0.5 top-0.5 hidden gap-0.5 group-focus-within:flex group-hover:flex">{actions}</div> : null}
    </div>
  );
}

function TileAction({ icon: Icon, label, onClick, danger }: { icon: typeof Pencil; label: string; onClick: () => void; danger?: boolean }) {
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

// AddTile is a dashed tile for an action (import, save).
function AddTile({ icon: Icon, label, onClick, disabled }: { icon: typeof Plus; label: string; onClick: () => void; disabled?: boolean }) {
  return (
    <button
      type="button"
      onClick={onClick}
      disabled={disabled}
      className="flex flex-col gap-1 text-left text-muted-foreground outline-none hover:text-foreground disabled:pointer-events-none disabled:opacity-40"
      title={label}
    >
      <div className="grid aspect-video w-full place-items-center border border-dashed border-border hover:border-foreground/40 focus-visible:border-ring">
        <Icon className="size-4" />
      </div>
      <span className="truncate text-[11px]">{label}</span>
    </button>
  );
}

function ErrorLine({ text }: { text: string }) {
  return text ? <div className="text-[11px] text-destructive">{text}</div> : null;
}

const errText = (err: unknown) => (err instanceof Error ? err.message : String(err)).replace(/^Error:\s*/, "");

// --- Looks -------------------------------------------------------------------

function Looks({ target, frame }: SectionProps) {
  const c = target?.clip;
  const def = defOf("look");
  if (!def) return null;
  const lookParam = def.params.find((p) => p.id === "look");
  const options = lookParam?.type === "enum" ? lookParam.options : [];
  const look = c && clipEffects(c).find((e) => e.type === "look");
  const current = look ? (resolveParams(def, look.params).look as string) : c ? "none" : "";

  const pick = (id: string) =>
    c &&
    target?.edit((p) => {
      if (id === "none") return look ? removeEffect(p, c.id, look.id) : p;
      if (look) return setValue(p, c.id, { fx: look.id, param: "look" }, id, 0);
      return addEffect(p, c.id, newEffect("look", { look: id }));
    }, "look");

  return (
    <>
      <Section title="Looks">
        <div className="grid grid-cols-3 gap-1.5">
          {options.map((o) => (
            <Tile
              key={o.id}
              frame={frame}
              stack={o.id === "none" ? [] : [{ id: `thumb-${o.id}`, type: "look", enabled: true, params: { look: o.id } }]}
              label={o.label}
              on={o.id === current}
              title={o.id === "none" ? "No look" : `Apply ${o.label}`}
              onClick={() => pick(o.id)}
            />
          ))}
        </div>
      </Section>
    </>
  );
}

// --- LUTs --------------------------------------------------------------------

let lutList: Lut[] | null = null;

function useLuts(): [Lut[], () => Promise<void>] {
  const [list, setList] = useState<Lut[]>(lutList ?? []);
  const reload = async () => {
    try {
      lutList = await PresetService.ListLuts();
    } catch {
      lutList ??= [];
    }
    setList(lutList);
  };
  useEffect(() => {
    void reload();
  }, []);
  return [list, reload];
}

function Luts({ target, frame }: SectionProps) {
  const c = target?.clip;
  const [luts, reload] = useLuts();
  const version = useLutsVersion();
  const [error, setError] = useState("");
  const [confirmDel, setConfirmDel] = useState<string | null>(null);
  const def = defOf("lut");
  if (!def) return null;
  const applied = c && clipEffects(c).find((e) => e.type === "lut");
  const current = applied ? (resolveParams(def, applied.params).lut as string) : "";

  const pick = async (id: string) => {
    if (!c) return;
    setError("");
    if (id) {
      // Load it first, so the preview has it the moment it's applied.
      if (!(await loadLut(id))) {
        setError(lutState(id).error ?? "couldn't read that LUT");
        return;
      }
    }
    target?.edit((p) => {
      if (!id) return applied ? removeEffect(p, c.id, applied.id) : p;
      if (applied) return setValue(p, c.id, { fx: applied.id, param: "lut" }, id, 0);
      return addEffect(p, c.id, newEffect("lut", { lut: id }));
    }, "lut");
  };
  const importLut = async () => {
    setError("");
    try {
      const [l, ok] = await PresetService.PickLut();
      if (!ok) return;
      forgetLut(l.id);
      await reload();
      await pick(l.id);
    } catch (err) {
      setError(errText(err));
    }
  };
  const del = async (id: string) => {
    setConfirmDel(null);
    try {
      await PresetService.DeleteLut(id);
      forgetLut(id);
      await reload();
    } catch (err) {
      setError(errText(err));
    }
  };
  const missing = current && !luts.some((l) => l.id === current);

  return (
    <>
      <Section title="LUTs">
        <div className="grid grid-cols-3 gap-1.5">
          <Tile frame={frame} stack={[]} label="None" on={!!c && !current} title="No LUT" onClick={() => void pick("")} />
          {luts.map((l) =>
            confirmDel === l.id ? (
              <div key={l.id} className="flex flex-col justify-center gap-1 border border-destructive/60 p-1 text-[11px]">
                <span className="truncate">Delete {l.name}?</span>
                <div className="flex gap-1">
                  <Button size="sm" variant="destructive" className="h-5 flex-1 px-1 text-[11px]" onClick={() => void del(l.id)}>
                    Delete
                  </Button>
                  <Button size="sm" variant="ghost" className="h-5 flex-1 px-1 text-[11px]" onClick={() => setConfirmDel(null)}>
                    Keep
                  </Button>
                </div>
              </div>
            ) : (
              <Tile
                key={l.id}
                frame={frame}
                version={version}
                stack={[{ id: `thumb-lut-${l.id}`, type: "lut", enabled: true, params: { lut: l.id } }]}
                label={l.name}
                on={l.id === current}
                title={`Apply ${l.name} (${l.size}³)`}
                onClick={() => void pick(l.id)}
                actions={<TileAction icon={Trash2} label={`Delete ${l.name}`} danger onClick={() => setConfirmDel(l.id)} />}
              />
            ),
          )}
          <AddTile icon={Import} label="Import LUT…" onClick={() => void importLut()} />
        </div>
        {missing ? <div className="text-[11px] text-muted-foreground">This clip's LUT ({current}) isn't imported on this PC, so it's drawn without it.</div> : null}
        <ErrorLine text={error} />
      </Section>
    </>
  );
}

// --- My filters --------------------------------------------------------------

// thumbOf is a 160×90 JPEG of the frame through the clip's effects, for the
// saved file (the tiles themselves render live).
function thumbOf(frame: HTMLCanvasElement | null, stack: EffectInstance[], t: number): string | undefined {
  if (!frame) return undefined;
  try {
    const bmp = renderThumb(frame, stack, 160, 90, t);
    const cv = document.createElement("canvas");
    cv.width = bmp.width;
    cv.height = bmp.height;
    cv.getContext("2d")!.drawImage(bmp, 0, 0);
    bmp.close();
    return cv.toDataURL("image/jpeg", 0.8);
  } catch {
    return undefined;
  }
}

function MyFilters({ target, frame }: SectionProps) {
  const c = target?.clip;
  const all = usePresets();
  const version = useLutsVersion();
  const mine = useMemo(() => all.filter((p) => !p.builtin), [all]);
  const [error, setError] = useState("");
  const [naming, setNaming] = useState<null | { id?: string; name: string }>(null);
  const [confirmDel, setConfirmDel] = useState<string | null>(null);
  const canSave = !!c && clipEffects(c).some((e) => e.enabled && defOf(e.type));
  // Stable ids, so a tile's thumbnail only re-renders when its preset changes.
  const thumbStacks = useMemo(() => new Map(mine.map((pre) => [pre.id, instancesOf(pre, 1).map((e, i) => ({ ...e, id: `thumb-${pre.id}-${i}` }))])), [mine]);

  const apply = (pre: PresetDoc, replace: boolean) => {
    if (!c) return;
    setError("");
    for (const e of pre.effects) if (e.type === "lut" && typeof e.params?.lut === "string") void loadLut(e.params.lut as string);
    target?.edit((p) => applyPreset(p, c.id, pre, { replace }), `preset.${pre.id}`);
  };
  const save = async () => {
    if (!naming) return;
    const name = naming.name.trim();
    if (!name) return setError("give the filter a name");
    setError("");
    try {
      if (naming.id) {
        await PresetService.Rename(naming.id, name);
      } else {
        if (!c) return;
        const doc = presetFromClip(c, name, thumbOf(frame, clipEffects(c), target?.t ?? 0));
        if (!doc) return setError("this clip has no effects to save");
        await PresetService.Save(JSON.stringify(doc));
      }
      setNaming(null);
      await refreshPresets();
    } catch (err) {
      setError(errText(err));
    }
  };
  const run = async (fn: () => Promise<unknown>) => {
    setError("");
    try {
      await fn();
      await refreshPresets();
    } catch (err) {
      setError(errText(err));
    }
  };

  return (
    <Section
      title="My filters"
      action={
        <Button size="sm" variant="ghost" className="h-6 gap-1 px-1.5 text-[11px]" onClick={() => void run(() => PresetService.PickImport())} title="Add a filter someone shared (.torufx)">
          <Import className="size-3" /> Import
        </Button>
      }
    >
      {naming ? (
        <form
          className="flex items-center gap-1"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <input
            autoFocus
            value={naming.name}
            maxLength={80}
            onChange={(e) => setNaming({ ...naming, name: e.target.value })}
            onKeyDown={(e) => e.key === "Escape" && setNaming(null)}
            placeholder="Filter name"
            aria-label="Filter name"
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
      <div className="grid grid-cols-3 gap-1.5">
        {mine.map((pre) =>
          confirmDel === pre.id ? (
            <div key={pre.id} className="flex flex-col justify-center gap-1 border border-destructive/60 p-1 text-[11px]">
              <span className="truncate">Delete {pre.name}?</span>
              <div className="flex gap-1">
                <Button
                  size="sm"
                  variant="destructive"
                  className="h-5 flex-1 px-1 text-[11px]"
                  onClick={() => {
                    setConfirmDel(null);
                    void run(() => PresetService.Delete(pre.id));
                  }}
                >
                  Delete
                </Button>
                <Button size="sm" variant="ghost" className="h-5 flex-1 px-1 text-[11px]" onClick={() => setConfirmDel(null)}>
                  Keep
                </Button>
              </div>
            </div>
          ) : (
            <Tile
              key={pre.id}
              frame={frame}
              version={version}
              stack={thumbStacks.get(pre.id) ?? []}
              label={pre.name}
              title={`Add ${pre.name} (Shift: replace this clip's effects)`}
              onClick={(e) => apply(pre, e.shiftKey)}
              actions={
                <>
                  <TileAction icon={Pencil} label={`Rename ${pre.name}`} onClick={() => setNaming({ id: pre.id, name: pre.name })} />
                  <TileAction icon={Download} label={`Share ${pre.name} as a file`} onClick={() => void run(() => PresetService.PickExport(pre.id))} />
                  <TileAction icon={Trash2} label={`Delete ${pre.name}`} danger onClick={() => setConfirmDel(pre.id)} />
                </>
              }
            />
          ),
        )}
        <AddTile icon={Save} label="Save as filter…" disabled={!canSave || !!naming} onClick={() => setNaming({ name: "" })} />
      </div>
      {!mine.length && !naming ? <div className="text-[11px] text-muted-foreground">Save a clip's effects as a filter to reuse them on other clips.</div> : null}
      <ErrorLine text={error} />
    </Section>
  );
}
