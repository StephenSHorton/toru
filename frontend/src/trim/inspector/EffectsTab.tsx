import { useMemo, useState } from "react";
import { ChevronDown, ChevronRight, GripVertical, Plus, RotateCcw, Search } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { cn } from "@/lib/utils";
import type { EffectCategory, EffectDef } from "@/trim/fx/def";
import { useClipFrame } from "@/trim/fx/frame";
import {
  addEffect,
  duplicateEffect,
  moveEffect,
  newEffect,
  pinnedAdjust,
  removeEffect,
  resetEffect,
  setEffectEnabled,
  setGroupAmount,
  ungroup,
} from "@/trim/fx/ops";
import { defOf, defs } from "@/trim/fx/registry";
import { clipEffects } from "@/trim/fx/stack";
import { newId } from "@/trim/edl";
import type { EffectInstance, ParamValue } from "@/trim/project";
import { Menu, NumberRow, ParamControl, Section } from "@/trim/inspector/controls";
import type { TabProps } from "@/trim/inspector/Inspector";
import { Thumb } from "@/trim/inspector/Thumb";

// EffectsTab: the effect browser (search, categories, a live thumbnail of
// the clip's frame through each effect; click adds it on top of the stack)
// and the clip's stack as cards, top = last applied. A card has a drag
// handle to reorder, an on/off switch, reset, a menu (move, duplicate, copy,
// paste, delete) and the effect's params, generated from its definition.
// Effects applied together from a saved filter show as one group card with
// an intensity. The pinned Adjust lives in its own tab.

const CATEGORIES: { id: EffectCategory; label: string }[] = [
  { id: "blur", label: "Blur" },
  { id: "stylize", label: "Stylize" },
  { id: "distort", label: "Distort" },
  { id: "light", label: "Light" },
  { id: "color", label: "Colour" },
  { id: "key", label: "Key" },
];

// THUMB_T is when a browser tile draws a time-based effect: inside its
// default window (zoom is fully in at 0.3–1.3 s, a flash is strongest just
// after 0). Others draw at 0.
const THUMB_T: Record<string, number> = { zoom: 0.8, flash: 0.03 };

// The effect a copy put aside, for Paste on any clip.
let copied: EffectInstance | null = null;

// showcase is the params a browser tile previews with (and a new effect
// starts with, so adding one shows at once): a param whose default
// does nothing (a number at its minimum, an enum at "none") shows at 40% of
// its range, or its first real option, instead.
function showcase(def: EffectDef): Record<string, ParamValue> {
  const out: Record<string, ParamValue> = {};
  for (const p of def.params) {
    if (p.type === "number" && p.default === p.min) out[p.id] = p.min + (p.max - p.min) * 0.4;
    if (p.type === "enum" && p.default === "none" && p.options.length > 1) out[p.id] = p.options.find((o) => o.id !== "none")!.id;
  }
  return out;
}

export function EffectsTab(props: TabProps) {
  const { clip: c, media, srcT, edit } = props;
  const [query, setQuery] = useState("");
  const [cat, setCat] = useState<EffectCategory | "all">("all");
  const [, bumpClipboard] = useState(0);
  const frame = useClipFrame(media, srcT);

  const browsable = useMemo(() => defs.filter((d) => !d.hidden), []);
  const cats = CATEGORIES.filter((x) => browsable.some((d) => d.category === x.id));
  const q = query.trim().toLowerCase();
  const shown = browsable.filter((d) => (cat === "all" || d.category === cat) && (!q || d.name.toLowerCase().includes(q) || d.category.includes(q)));

  const fx = clipEffects(c);
  const pinned = pinnedAdjust(fx);
  // Cards top-down: the stack reversed, without the pinned Adjust, with each
  // saved-filter group folded into one card.
  const cards: { group?: string; items: EffectInstance[] }[] = [];
  for (const e of [...fx].reverse()) {
    if (e === pinned) continue;
    const last = cards[cards.length - 1];
    if (e.group && last?.group === e.group) last.items.push(e);
    else cards.push({ group: e.group, items: [e] });
  }

  const [drag, setDrag] = useState<{ id: string; over: number } | null>(null);
  const indexOf = (id: string) => fx.findIndex((e) => e.id === id);
  const paste = () => {
    if (!copied) return;
    const e = { ...structuredClone(copied), id: newId("fx"), group: undefined, amount: undefined };
    edit((p) => addEffect(p, c.id, e), "fx.paste");
  };

  return (
    <>
      <Section title="Add an effect">
        <div className="flex h-7 items-center gap-1.5 border border-border bg-background px-2 focus-within:ring-1 focus-within:ring-ring">
          <Search className="size-3.5 shrink-0 text-muted-foreground" />
          <input
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Search effects"
            className="min-w-0 flex-1 bg-transparent text-xs outline-none placeholder:text-muted-foreground"
            aria-label="Search effects"
          />
        </div>
        {cats.length > 1 ? (
          <div className="flex flex-wrap gap-1">
            {[{ id: "all" as const, label: "All" }, ...cats].map((x) => (
              <Button key={x.id} size="sm" variant={cat === x.id ? "secondary" : "ghost"} className="h-6 px-2 text-[11px]" onClick={() => setCat(x.id)} aria-pressed={cat === x.id}>
                {x.label}
              </Button>
            ))}
          </div>
        ) : null}
        <div className="grid grid-cols-3 gap-1.5">
          {shown.map((d) => (
            <button
              key={d.type}
              type="button"
              className="group flex flex-col gap-1 text-left text-muted-foreground outline-none hover:text-foreground"
              onClick={() => edit((p) => addEffect(p, c.id, newEffect(d.type, showcase(d))), `fx.add.${d.type}`)}
              title={`Add ${d.name} on top of this clip's effects`}
            >
              <div className="relative border border-border group-hover:border-foreground/40 group-focus-visible:border-ring">
                <Thumb frame={frame} stack={[{ id: `thumb-${d.type}`, type: d.type, enabled: true, params: showcase(d) }]} t={THUMB_T[d.type] ?? 0} />
                <span className="absolute bottom-0.5 right-0.5 hidden size-4 place-items-center bg-primary text-primary-foreground group-hover:grid">
                  <Plus className="size-3" />
                </span>
              </div>
              <span className="truncate text-[11px]">{d.name}</span>
            </button>
          ))}
          {!shown.length ? <div className="col-span-3 py-2 text-[11px] text-muted-foreground">No effects match “{query}”.</div> : null}
        </div>
      </Section>

      <Section
        title={`This clip (${cards.length})`}
        action={
          copied ? (
            <Button size="sm" variant="ghost" className="h-6 px-1.5 text-[11px]" onClick={paste} title={`Paste ${defOf(copied.type)?.name ?? "effect"}`}>
              Paste
            </Button>
          ) : null
        }
      >
        {!cards.length ? <div className="text-[11px] text-muted-foreground">No effects yet. Pick one above to add it.</div> : null}
        <div className="flex flex-col gap-1.5" onDragLeave={(e) => !e.currentTarget.contains(e.relatedTarget as Node) && setDrag((d) => (d ? { ...d, over: -1 } : d))}>
          {cards.map((card, ci) => {
            const top = card.items[0];
            const body =
              card.group !== undefined ? (
                <GroupCard key={top.id} {...props} items={card.items} group={card.group} />
              ) : (
                <EffectCard
                  key={top.id}
                  {...props}
                  e={top}
                  first={ci === 0}
                  last={ci === cards.length - 1}
                  onCopy={() => {
                    copied = structuredClone(top);
                    bumpClipboard((n) => n + 1);
                  }}
                  onPaste={copied ? paste : undefined}
                />
              );
            return (
              <div
                key={top.id}
                draggable={card.group === undefined}
                onDragStart={(ev) => {
                  if (!(ev.target as HTMLElement).closest?.("[data-grip]")) {
                    ev.preventDefault();
                    return;
                  }
                  ev.dataTransfer.effectAllowed = "move";
                  setDrag({ id: top.id, over: ci });
                }}
                onDragOver={(ev) => {
                  if (!drag) return;
                  ev.preventDefault();
                  if (drag.over !== ci) setDrag({ ...drag, over: ci });
                }}
                onDrop={(ev) => {
                  ev.preventDefault();
                  if (!drag) return;
                  // The dragged effect takes the dropped-on card's place in
                  // the stack (cards list it top-down: items[0] is highest).
                  const from = indexOf(drag.id);
                  const hi = indexOf(top.id);
                  const lo = indexOf(card.items[card.items.length - 1].id);
                  if (from >= 0 && hi >= 0) edit((p) => moveEffect(p, c.id, drag.id, from < hi ? hi : lo), "fx.move");
                  setDrag(null);
                }}
                onDragEnd={() => setDrag(null)}
                className={cn(drag && drag.over === ci && drag.id !== top.id && "outline outline-1 outline-primary", drag?.id === top.id && "opacity-50")}
              >
                {body}
              </div>
            );
          })}
        </div>
      </Section>
    </>
  );
}

function EffectCard({
  clip: c,
  e,
  edit,
  keys,
  set,
  value,
  picking,
  onPickingChange,
  first,
  last,
  onCopy,
  onPaste,
}: TabProps & { e: EffectInstance; first: boolean; last: boolean; onCopy: () => void; onPaste?: () => void }) {
  const [open, setOpen] = useState(true);
  const def = defOf(e.type);
  const name = def?.name ?? e.type;
  const fx = clipEffects(c);
  const i = fx.findIndex((x) => x.id === e.id);
  const keyed = Object.values(e.keys ?? {}).some((k) => k?.length);
  return (
    <div className={cn("border border-border bg-background/40", !e.enabled && "opacity-60")}>
      <div className="flex items-center gap-1 px-1 py-1">
        <span data-grip className="cursor-grab text-muted-foreground hover:text-foreground active:cursor-grabbing" title="Drag to reorder">
          <GripVertical className="size-3.5" />
        </span>
        <button type="button" className="flex min-w-0 flex-1 items-center gap-1 text-left font-medium" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
          {open ? <ChevronDown className="size-3 shrink-0" /> : <ChevronRight className="size-3 shrink-0" />}
          <span className="truncate">{name}</span>
          {keyed ? <span className="text-[10px] font-normal text-primary">◆</span> : null}
        </button>
        <Switch checked={e.enabled} onCheckedChange={(on) => edit((p) => setEffectEnabled(p, c.id, e.id, on), `fx.on.${e.id}`)} aria-label={`${name} on`} />
        <Button size="icon" variant="ghost" className="size-6 [&_svg]:size-3" onClick={() => edit((p) => resetEffect(p, c.id, e.id), `fx.reset.${e.id}`)} title={`Reset ${name}`} aria-label={`Reset ${name}`}>
          <RotateCcw />
        </Button>
        <Menu
          label={`${name} options`}
          items={[
            // The list is top-down, the stack bottom-up: "up" runs later.
            { label: "Move up", disabled: first, onClick: () => edit((p) => moveEffect(p, c.id, e.id, i + 1), "fx.move") },
            { label: "Move down", disabled: last, onClick: () => edit((p) => moveEffect(p, c.id, e.id, i - 1), "fx.move") },
            { label: "Duplicate", onClick: () => edit((p) => duplicateEffect(p, c.id, e.id), "fx.dup") },
            { label: "Copy", onClick: onCopy },
            { label: "Paste", disabled: !onPaste, onClick: () => onPaste?.() },
            { label: "Delete", danger: true, onClick: () => edit((p) => removeEffect(p, c.id, e.id), "fx.del") },
          ]}
        />
      </div>
      {open && def ? (
        <div className="flex flex-col gap-1.5 border-t border-border/60 px-2 py-2">
          {def.params.map((p) => (
            <ParamControl
              key={p.id}
              p={p}
              value={value({ fx: e.id, param: p.id })!}
              keyframes={keys({ fx: e.id, param: p.id })}
              onChange={(v) => set({ fx: e.id, param: p.id }, v)}
              eyedropper={e.type === "chromaKey" && p.type === "color" && onPickingChange ? { picking, onPickingChange } : undefined}
            />
          ))}
        </div>
      ) : null}
      {!def ? <div className="px-2 pb-2 text-[11px] text-muted-foreground">This effect needs a newer Toru.</div> : null}
    </div>
  );
}

function GroupCard({ clip: c, items, group, edit }: TabProps & { items: EffectInstance[]; group: string }) {
  const amount = items[0].amount ?? 1;
  const on = items.some((e) => e.enabled);
  return (
    <div className={cn("border border-border bg-background/40", !on && "opacity-60")}>
      <div className="flex items-center gap-1 px-2 py-1">
        <span className="min-w-0 flex-1 truncate font-medium">{items.map((e) => defOf(e.type)?.name ?? e.type).join(" + ")}</span>
        <Switch
          checked={on}
          onCheckedChange={(v) =>
            edit((p) => items.reduce((q, e) => setEffectEnabled(q, c.id, e.id, v), p), `grp.on.${group}`)
          }
          aria-label="Filter on"
        />
        <Menu
          label="Filter options"
          items={[
            { label: "Ungroup", onClick: () => edit((p) => ungroup(p, c.id, group), `grp.ungroup.${group}`) },
            { label: "Delete", danger: true, onClick: () => edit((p) => items.reduce((q, e) => removeEffect(q, c.id, e.id), p), `grp.del.${group}`) },
          ]}
        />
      </div>
      <div className="border-t border-border/60 px-2 py-2">
        <NumberRow label="Intensity" value={amount} min={0} max={1} step={0.01} def={1} scale={100} unit="%" onChange={(v) => edit((p) => setGroupAmount(p, c.id, group, v), `grp.amount.${group}`)} />
      </div>
    </div>
  );
}
