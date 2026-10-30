import type { ShaderModule } from "@/trim/fx/shader";
import adjust from "./adjust";
import blur from "./blur";
import chromaKey from "./chromaKey";
import look from "./look";

// The shader module of every effect type, by type (one per
// internal/fx/defs/<type>.json; registry.ts imports the defs).
export const shaders: Record<string, ShaderModule> = Object.fromEntries([adjust, blur, chromaKey, look].map((m) => [m.type, m]));
