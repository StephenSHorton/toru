import { Check } from "lucide-react";
import { cn } from "@/lib/utils";
import { useClipFrame } from "@/trim/fx/frame";
import { addEffect, newEffect, removeEffect, setValue } from "@/trim/fx/ops";
import { defOf, resolveParams } from "@/trim/fx/registry";
import { clipEffects } from "@/trim/fx/stack";
import { ParamControl, Section } from "@/trim/inspector/controls";
import type { TabProps } from "@/trim/inspector/Inspector";
import { Thumb } from "@/trim/inspector/Thumb";

// FiltersTab: one-click looks shown as live thumbnails of the clip's
// current frame. Picking a look sets the clip's look effect (adding one on
// top of the stack if it has none); None removes it. Intensity is the look's
// own amount.

export function FiltersTab({ clip: c, media, srcT, edit, keys, set, value }: TabProps) {
  const def = defOf("look");
  const frame = useClipFrame(media, srcT);
  if (!def) return null;
  const lookParam = def.params.find((p) => p.id === "look");
  const options = lookParam?.type === "enum" ? lookParam.options : [];
  const look = clipEffects(c).find((e) => e.type === "look");
  const current = look ? (resolveParams(def, look.params).look as string) : "none";

  const pick = (id: string) =>
    edit((p) => {
      if (id === "none") return look ? removeEffect(p, c.id, look.id) : p;
      if (look) return setValue(p, c.id, { fx: look.id, param: "look" }, id, 0);
      return addEffect(p, c.id, newEffect("look", { look: id }));
    }, "look");

  return (
    <>
      <Section title="Looks">
        <div className="grid grid-cols-3 gap-1.5">
          {options.map((o) => {
            const on = o.id === current;
            return (
              <button
                key={o.id}
                type="button"
                onClick={() => pick(o.id)}
                className={cn("group relative flex flex-col gap-1 text-left outline-none", on ? "text-foreground" : "text-muted-foreground hover:text-foreground")}
                aria-pressed={on}
                title={o.id === "none" ? "No look" : `Apply ${o.label}`}
              >
                <div className={cn("relative border", on ? "border-primary" : "border-border group-hover:border-foreground/40 group-focus-visible:border-ring")}>
                  <Thumb frame={frame} stack={o.id === "none" ? [] : [{ id: `thumb-${o.id}`, type: "look", enabled: true, params: { look: o.id } }]} />
                  {on ? (
                    <span className="absolute right-0.5 top-0.5 grid size-4 place-items-center bg-primary text-primary-foreground">
                      <Check className="size-3" />
                    </span>
                  ) : null}
                </div>
                <span className="truncate text-[11px]">{o.label}</span>
              </button>
            );
          })}
        </div>
      </Section>
      {look ? (
        <Section title="Applied look">
          {def.params
            .filter((p) => p.id !== "look")
            .map((p) => (
              <ParamControl key={p.id} p={p} value={value({ fx: look.id, param: p.id })!} keyframes={keys({ fx: look.id, param: p.id })} onChange={(v) => set({ fx: look.id, param: p.id }, v)} />
            ))}
        </Section>
      ) : null}
    </>
  );
}
