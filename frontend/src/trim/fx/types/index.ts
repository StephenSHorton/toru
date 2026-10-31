import type { ShaderModule } from "@/trim/fx/shader";
import adjust from "./adjust";
import blur from "./blur";
import chromaKey from "./chromaKey";
import flash from "./flash";
import gaussianBlur from "./gaussianBlur";
import look from "./look";
import rgbSplit from "./rgbSplit";
import shake from "./shake";
import sharpen from "./sharpen";
import zoom from "./zoom";

// The shader module of every effect type, by type (one per
// internal/fx/defs/<type>.json; registry.ts imports the defs).
export const shaders: Record<string, ShaderModule> = Object.fromEntries([adjust, blur, chromaKey, flash, gaussianBlur, look, rgbSplit, shake, sharpen, zoom].map((m) => [m.type, m]));
