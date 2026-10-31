import { useState, type CSSProperties } from "react";
import { TRANSITION_MIME, TRANSITIONS, transitionDrag, type TransitionType } from "@/trim/transition";

// The Transitions library (layout B's rail): one tile per transition. Drag a
// tile onto a cut on the timeline (the cuts that can take one light up), or
// click it to use it on the selected transition or the selected clip's cut.

// A tile's picture: the outgoing clip (A) part way into the incoming one (B).
const A = "linear-gradient(135deg, rgb(71 85 105), rgb(30 41 59))";
const B = "linear-gradient(135deg, rgb(56 189 248), rgb(37 99 235))";
function look(type: TransitionType): { b: CSSProperties; a?: CSSProperties; over?: string } {
  switch (type) {
    case "fade":
      return { b: { opacity: 0.5 } };
    case "fadeblack":
      return { b: { opacity: 0 }, over: "rgb(0 0 0 / 0.8)" };
    case "fadewhite":
      return { b: { opacity: 0 }, over: "rgb(255 255 255 / 0.8)" };
    case "wipeleft":
      return { b: { clipPath: "inset(0 0 0 50%)" } };
    case "wiperight":
      return { b: { clipPath: "inset(0 50% 0 0)" } };
    case "wipeup":
      return { b: { clipPath: "inset(50% 0 0 0)" } };
    case "wipedown":
      return { b: { clipPath: "inset(0 0 50% 0)" } };
    case "slideleft":
      return { b: { transform: "translateX(50%)" }, a: { transform: "translateX(-50%)" } };
    case "slideright":
      return { b: { transform: "translateX(-50%)" }, a: { transform: "translateX(50%)" } };
    case "slideup":
      return { b: { transform: "translateY(50%)" }, a: { transform: "translateY(-50%)" } };
    case "slidedown":
      return { b: { transform: "translateY(-50%)" }, a: { transform: "translateY(50%)" } };
    case "zoomin":
      return { b: { opacity: 0.35 }, a: { transform: "scale(1.6)" } };
    case "hblur":
      return { b: { opacity: 0.5, filter: "blur(3px)" }, a: { filter: "blur(3px)" } };
  }
}

// onPick uses a transition on the current selection and returns why not
// (shown under the note) when it can't.
export function TransitionsBrowser({ onPick, note }: { onPick: (type: TransitionType) => string | void; note: string }) {
  const groups = [...new Set(TRANSITIONS.map((t) => t.group))];
  const [why, setWhy] = useState<string | null>(null);
  return (
    <div className="flex flex-col gap-3 p-3">
      <p className="text-[11px] text-muted-foreground">{note}</p>
      {why ? <p className="text-[11px] text-destructive">{why}</p> : null}
      {groups.map((g) => (
        <section key={g} className="flex flex-col gap-1.5">
          <h3 className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{g}</h3>
          <div className="grid grid-cols-3 gap-1.5">
            {TRANSITIONS.filter((t) => t.group === g).map((t) => {
              const l = look(t.type);
              return (
                <button
                  key={t.type}
                  type="button"
                  draggable
                  onDragStart={(e) => {
                    e.dataTransfer.setData(TRANSITION_MIME, t.type);
                    e.dataTransfer.setData("text/plain", t.label);
                    e.dataTransfer.effectAllowed = "copy";
                    transitionDrag.set(true);
                  }}
                  onDragEnd={() => transitionDrag.set(false)}
                  onClick={() => setWhy(onPick(t.type) || null)}
                  title={`${t.label}: click to use it on the selected clip's cut, or drag it onto a cut`}
                  className="group flex min-w-0 cursor-grab flex-col gap-1 text-left text-muted-foreground outline-none hover:text-foreground focus-visible:text-foreground"
                >
                  <span className="relative block aspect-video w-full overflow-hidden border border-border group-hover:border-primary group-focus-visible:border-primary">
                    <span className="absolute inset-0" style={{ background: A, ...l.a }} />
                    <span className="absolute inset-0" style={{ background: B, ...l.b }} />
                    {l.over ? <span className="absolute inset-0" style={{ background: l.over }} /> : null}
                  </span>
                  <span className="truncate text-[11px]">{t.label}</span>
                </button>
              );
            })}
          </div>
        </section>
      ))}
    </div>
  );
}
