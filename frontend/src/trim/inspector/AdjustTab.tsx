import { paramDefault } from "@/trim/fx/def";
import { defOf, resolveParams } from "@/trim/fx/registry";
import { ensureAdjust, pinnedAdjust, resetEffect, setValue, toggleKey } from "@/trim/fx/ops";
import { valueAt } from "@/trim/fx/keyframes";
import { clipEffects } from "@/trim/fx/stack";
import type { Project } from "@/trim/project";
import { ParamControl, ResetButton, Section } from "@/trim/inspector/controls";
import type { TabProps } from "@/trim/inspector/Inspector";

// AdjustTab shows the clip's pinned Adjust effect (its first adjust; one is
// added first in the stack on the first change) as plain sections: Light,
// Colour, Detail. Each slider double-clicks back to its default and each
// section has its own Reset. It's the same instance agents edit.

const SECTIONS: { title: string; params: string[] }[] = [
  { title: "Light", params: ["exposure", "brightness", "contrast", "highlights", "shadows", "whites", "blacks"] },
  { title: "Colour", params: ["temperature", "tint", "saturation", "vibrance"] },
  { title: "Detail", params: ["sharpen", "fade"] },
];

export function AdjustTab({ clip: c, t, edit, keys }: TabProps) {
  const def = defOf("adjust");
  if (!def) return null;
  const adj = pinnedAdjust(clipEffects(c));
  const base = resolveParams(def, adj?.params ?? {});
  const listed = new Set(SECTIONS.flatMap((s) => s.params));
  // Params a newer definition adds that no section lists go under Detail.
  const sections = SECTIONS.map((s) => (s.title === "Detail" ? { ...s, params: [...s.params, ...def.params.map((p) => p.id).filter((id) => !listed.has(id))] } : s));

  // withAdjust runs fn on the adjust's id, adding the adjust first if needed.
  const withAdjust = (fn: (p: Project, id: string) => Project) => (p: Project) => {
    const [q, id] = ensureAdjust(p, c.id);
    return id ? fn(q, id) : p;
  };

  return (
    <>
      {sections.map((s) => {
        const params = s.params.map((id) => def.params.find((p) => p.id === id)).filter((p) => !!p);
        const changed = !!adj && params.some((p) => adj.keys?.[p.id]?.length || JSON.stringify(base[p.id]) !== JSON.stringify(paramDefault(p)));
        return (
          <Section
            key={s.title}
            title={s.title}
            action={changed && adj ? <ResetButton onClick={() => edit((p) => resetEffect(p, c.id, adj.id, params.map((x) => x.id)), `adjust.reset.${s.title}`)} title={`Reset ${s.title.toLowerCase()}`} /> : null}
          >
            {params.map((p) => {
              const ks = adj ? keys({ fx: adj.id, param: p.id }) : undefined;
              return (
                <ParamControl
                  key={p.id}
                  p={p}
                  value={valueAt(adj?.keys?.[p.id], t, base[p.id])}
                  keyframes={{
                    keys: ks?.keys,
                    t,
                    onJump: ks?.onJump ?? (() => {}),
                    onToggle: () => edit(withAdjust((q, id) => toggleKey(q, c.id, { fx: id, param: p.id }, t)), `key.adjust.${p.id}`),
                  }}
                  onChange={(v) => edit(withAdjust((q, id) => setValue(q, c.id, { fx: id, param: p.id }, v, t)), `adjust.${p.id}`)}
                />
              );
            })}
          </Section>
        );
      })}
    </>
  );
}
