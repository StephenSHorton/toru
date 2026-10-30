import { lookFn } from "@/trim/fx/lut";
import type { ShaderModule } from "@/trim/fx/shader";

// look: v2's look matrices with saturation and an intensity, all colour, so
// it lives in the stack's LUT (lut.ts lookFn).
const look: ShaderModule = {
  type: "look",
  steps(p) {
    const fn = lookFn(p);
    return fn ? [{ kind: "color", fn, key: `look:${p.look}:${p.saturation}:${p.amount}` }] : [];
  },
};
export default look;
