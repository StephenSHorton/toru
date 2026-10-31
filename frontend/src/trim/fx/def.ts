import type { ParamValue } from "@/trim/project";

// def.ts is the shape of an effect definition (docs/video-editor-v3.md,
// Effect registry): one JSON file per effect type in internal/fx/defs/,
// embedded by Go (internal/fx/def.go, the same fields) and loaded here by the
// registry. The inspector controls, agent command schemas, clamping and the
// parity cases all come from it; the shader (frontend/src/trim/fx/types/)
// and the Go filter builder (internal/fx/<type>.go) hold the behaviour.

// How the preview renderer runs an effect: pixel effects (one colour in, one
// out) fuse into one shader pass; neighbourhood effects read nearby pixels
// and get their own pass(es); geometry effects remap where pixels come from.
export type EffectKind = "pixel" | "neighbourhood" | "geometry";

export type EffectCategory = "adjust" | "color" | "blur" | "stylize" | "distort" | "key" | "light";

// How a param is keyframed in the export: "cmd" sends values to the filter
// with sendcmd, "expr" writes them into the filter's expression in t, false
// means it can't be keyframed (the UI hides the keyframe toggle).
export type Animate = "cmd" | "expr" | false;

type ParamBase = {
  id: string; // key in EffectInstance.params; lowerCamel
  label: string; // app text
  animate: Animate;
  hint?: string; // tooltip
};
export type ParamDef =
  | (ParamBase & {
      type: "number";
      min: number;
      max: number;
      default: number;
      step?: number;
      unit?: string; // shown after the value: "px", "°", "K", "EV", "s"
      display?: "percent"; // show value×100 with "%"
    })
  | (ParamBase & { type: "boolean"; default: boolean })
  | (ParamBase & { type: "color"; default: string }) // "#rrggbb"
  | (ParamBase & { type: "enum"; default: string; options: { id: string; label: string }[] })
  | (ParamBase & { type: "point"; default: [number, number] }) // x, y in 0..1 of the frame
  | (ParamBase & { type: "file"; default: string; accept: string[] }); // e.g. [".cube"]

export type EffectDef = {
  type: string; // registry id, lowerCamel, = the file name
  version: number; // bumped when params change meaning; instances migrate
  name: string; // app text; never names another product
  category: EffectCategory;
  kind: EffectKind;
  params: ParamDef[];
  // The ffmpeg filters the Go builder may emit (all must be in
  // internal/fx/lgpl_filters.txt).
  export: { filters: string[] };
  // How the parity check compares preview and export: pixels (mean and p99
  // in 0..255 levels), stats (random effects like grain: mean/variance), or
  // none (with the reason). border: how far the effect reads past a pixel
  // (px of a 1080-line frame), skipped at the frame's edge; t: the
  // clip-local time (s) generated cases draw at (internal/fx/def.go Parity).
  parity: ({ mode: "pixels"; mean: number; p99: number } | { mode: "stats" } | { mode: "none"; reason: string }) & { border?: number; t?: number };
  // How a group's intensity applies: the effect scales itself (native), a
  // generic mix with the input (blend), or not at all (none).
  amount: "native" | "blend" | "none";
  hidden?: boolean; // not in the browser (e.g. the pinned Adjust instance's def is shown in its own tab)
};

// paramDefault is a param's default as a ParamValue.
export function paramDefault(p: ParamDef): ParamValue {
  return p.type === "point" ? [p.default[0], p.default[1]] : p.default;
}
