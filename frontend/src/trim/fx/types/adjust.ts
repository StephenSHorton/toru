import { adjustFn } from "@/trim/fx/lut";
import type { ShaderModule, Step } from "@/trim/fx/shader";
import { casSteps } from "./sharpen";

// adjust: the colour sliders go into the stack's LUT (lut.ts adjustFn);
// sharpen is the CAS pass after them (sharpen.ts, ffmpeg's cas).

const adjust: ShaderModule = {
  type: "adjust",
  steps(p) {
    const out: Step[] = [];
    const fn = adjustFn(p);
    if (fn) {
      const key = ["exposure", "contrast", "brightness", "highlights", "shadows", "whites", "blacks", "temperature", "tint", "saturation", "vibrance", "fade"]
        .map((k) => p[k])
        .join(":");
      out.push({ kind: "color", fn, key: `adjust:${key}` });
    }
    return [...out, ...casSteps(p.sharpen as number)];
  },
};
export default adjust;
