import { cubeFn } from "@/trim/fx/cube";
import { lutCube } from "@/trim/fx/luts";
import type { ShaderModule } from "@/trim/fx/shader";

// lut: an imported .cube LUT with an intensity, all colour, so it is sampled
// into the stack's generated LUT (cube.ts cubeFn). Until the file has loaded
// (luts.ts) it draws nothing.
const lut: ShaderModule = {
  type: "lut",
  steps(p) {
    const id = p.lut as string;
    const cube = lutCube(id);
    const fn = cube ? cubeFn(cube, p.amount as number) : null;
    return fn ? [{ kind: "color", fn, key: `lut:${id}:${cube!.size}:${p.amount}` }] : [];
  },
};
export default lut;
