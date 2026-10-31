// fx-check catches a half-added effect on the preview side, the twin of Go's
// TestEveryDefHasABuilder: every internal/fx/defs/<type>.json must be
// imported by src/trim/fx/registry.ts (which lists them by hand so Bun can
// bundle it) and have a shader module in src/trim/fx/types/index.ts, the
// module must declare the def's type, and neither list may hold a type
// with no JSON. Each def must also be the file the registry loaded (same
// type as its file name, same content).
//
//   bun scripts/fx-check.ts
import fs from "node:fs";
import path from "node:path";
import { defs } from "../src/trim/fx/registry";
import { shaders } from "../src/trim/fx/types";

const DIR = path.resolve(import.meta.dir, "../../internal/fx/defs");
const problems: string[] = [];

const files = new Map(
  fs
    .readdirSync(DIR)
    .filter((f) => f.endsWith(".json"))
    .map((f) => [path.basename(f, ".json"), JSON.parse(fs.readFileSync(path.join(DIR, f), "utf8"))]),
);
const loaded = new Map(defs.map((d) => [d.type, d]));

for (const [name, json] of files) {
  if (json.type !== name) problems.push(`defs/${name}.json: type is "${json.type}", want "${name}" (the file name)`);
  const d = loaded.get(name);
  if (!d) problems.push(`defs/${name}.json: not imported by src/trim/fx/registry.ts (add it to defs)`);
  else if (JSON.stringify(d) !== JSON.stringify(json)) problems.push(`defs/${name}.json: registry.ts loads a different object for ${name}`);
  const s = shaders[name];
  if (!s) problems.push(`defs/${name}.json: no shader in src/trim/fx/types (add types/${name}.ts and list it in types/index.ts)`);
}
for (const type of loaded.keys()) if (!files.has(type)) problems.push(`registry.ts loads "${type}", which has no defs/${type}.json`);
if (loaded.size !== defs.length) problems.push(`registry.ts lists a type twice`);
for (const [key, s] of Object.entries(shaders)) {
  if (s.type !== key) problems.push(`types/index.ts: shader keyed "${key}" declares type "${s.type}"`);
  if (!files.has(key)) problems.push(`types/index.ts: shader "${key}" has no defs/${key}.json`);
}

for (const p of problems) console.log(`FAIL ${p}`);
console.log(problems.length ? `${problems.length} problem(s)` : `all ${files.size} effect definitions have a registry entry and a shader`);
process.exit(problems.length ? 1 : 0);
