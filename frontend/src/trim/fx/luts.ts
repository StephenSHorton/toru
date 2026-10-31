import { useEffect, useState } from "react";
import { parseCube, type Cube } from "@/trim/fx/cube";

// luts.ts caches the imported LUTs the preview draws (a lut effect names one
// by id, its file name in %AppData%\toru\luts). The shader module asks for a
// cube synchronously: a cube not loaded yet starts loading and the effect
// draws nothing until it's there (the player redraws every frame, so it
// shows up by itself; thumbnails re-render through useLutsVersion). A
// missing or broken file stays an error, which lutState reports for the UI.

type Entry = { cube?: Cube; error?: string; loading?: Promise<Cube | undefined> };

const cache = new Map<string, Entry>();
const listeners = new Set<() => void>();
let version = 0;

function changed() {
  version++;
  for (const l of listeners) l();
}

// loadLut loads (once) and returns a LUT, or undefined with the error kept.
export function loadLut(id: string): Promise<Cube | undefined> {
  const hit = cache.get(id);
  if (hit?.cube) return Promise.resolve(hit.cube);
  if (hit?.loading) return hit.loading;
  if (hit?.error) return Promise.resolve(undefined);
  const e: Entry = {};
  // Loaded lazily, so scripts that bundle the effect registry (parity
  // checks under Bun) don't pull in the Wails runtime.
  e.loading = import("@/lib/api")
    .then(({ PresetService }) => PresetService.ReadLut(id))
    .then((text) => {
      e.cube = parseCube(text);
      return e.cube;
    })
    .catch((err) => {
      e.error = err instanceof Error ? err.message : String(err);
      return undefined;
    })
    .finally(() => {
      e.loading = undefined;
      changed();
    });
  cache.set(id, e);
  return e.loading;
}

// lutCube is a loaded LUT, starting the load when it isn't.
export function lutCube(id: string): Cube | undefined {
  if (!id) return undefined;
  const hit = cache.get(id);
  if (hit?.cube) return hit.cube;
  if (!hit) void loadLut(id);
  return undefined;
}

// lutState says whether a LUT is ready, loading or broken (and why).
export function lutState(id: string): { ok: boolean; loading: boolean; error?: string } {
  const hit = cache.get(id);
  return { ok: !!hit?.cube, loading: !!hit?.loading || !hit, error: hit?.error };
}

// forgetLut drops a LUT from the cache (after it was deleted or replaced),
// so the next use reads it again.
export function forgetLut(id: string) {
  if (cache.delete(id)) changed();
}

// useLutsVersion re-renders a component whenever a LUT finishes loading.
export function useLutsVersion(): number {
  const [v, setV] = useState(version);
  useEffect(() => {
    const l = () => setV(version);
    listeners.add(l);
    return () => void listeners.delete(l);
  }, []);
  return v;
}
