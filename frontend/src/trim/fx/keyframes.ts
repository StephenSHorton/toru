import type { Ease, Keyframe, ParamValue } from "@/trim/project";

// keyframes.ts is how a keyframed value moves over time
// (docs/video-editor-v3.md, Data model). internal/fx/keyframes.go is the same
// maths for the export; internal/fx/testdata/keyframes_golden.json holds
// cases both must reproduce (frontend/scripts/project-check.ts and
// keyframes_test.go read it).
//
// Keys are sorted by t (clip-local timeline seconds). Before the first key
// the value is the first key's, after the last the last key's; between two
// keys it moves from one to the next along the first key's ease. Numbers and
// points move smoothly, colours ("#rrggbb") per RGB channel; booleans and
// other strings (enum ids, paths) jump at the next key, like "hold".

// ease maps 0..1 progress through a segment to 0..1 of the change (cubic).
export function ease(kind: Ease, x: number): number {
  const u = Math.max(0, Math.min(1, x));
  switch (kind) {
    case "in":
      return u * u * u;
    case "out":
      return 1 - (1 - u) ** 3;
    case "inOut":
      return u < 0.5 ? 4 * u * u * u : 1 - (-2 * u + 2) ** 3 / 2;
    case "hold":
      return 0;
    default:
      return u;
  }
}

// valueAt is the value at time t: base when there are no keys.
export function valueAt(keys: Keyframe[] | undefined, t: number, base: ParamValue): ParamValue {
  if (!keys || keys.length === 0) return base;
  if (!(t > keys[0].t)) return keys[0].v;
  const last = keys[keys.length - 1];
  if (t >= last.t) return last.v;
  let i = 0;
  while (i < keys.length - 2 && t >= keys[i + 1].t) i++;
  const a = keys[i];
  const b = keys[i + 1];
  const span = b.t - a.t;
  const f = span > 0 ? ease(a.ease, (t - a.t) / span) : 1;
  return mix(a.v, b.v, f);
}

// mix moves from a towards b by f (0..1).
export function mix(a: ParamValue, b: ParamValue, f: number): ParamValue {
  if (typeof a === "number" && typeof b === "number") return a + (b - a) * f;
  if (Array.isArray(a) && Array.isArray(b)) return [a[0] + (b[0] - a[0]) * f, a[1] + (b[1] - a[1]) * f];
  if (typeof a === "string" && typeof b === "string") {
    const ca = rgb(a);
    const cb = rgb(b);
    if (ca && cb) return hex(ca.map((v, k) => Math.round(v + (cb[k] - v) * f)) as [number, number, number]);
  }
  return f >= 1 ? b : a;
}

function rgb(s: string): [number, number, number] | null {
  const m = /^#([0-9a-f]{6})$/i.exec(s);
  if (!m) return null;
  const n = parseInt(m[1], 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

function hex(c: [number, number, number]): string {
  return "#" + c.map((v) => Math.max(0, Math.min(255, v)).toString(16).padStart(2, "0")).join("");
}

// setKey puts a keyframe at t (replacing one within 1 ms), keeping order;
// the new key takes the ease of the key it replaces, else linear.
export function setKey(keys: Keyframe[] | undefined, t: number, v: ParamValue, e?: Ease): Keyframe[] {
  const list = (keys ?? []).slice();
  const i = list.findIndex((k) => Math.abs(k.t - t) < 1e-3);
  if (i >= 0) list[i] = { t: list[i].t, v, ease: e ?? list[i].ease };
  else list.push({ t, v, ease: e ?? "linear" });
  return list.sort((a, b) => a.t - b.t);
}

// removeKey drops the keyframe at t (within 1 ms); undefined when none are left.
export function removeKey(keys: Keyframe[] | undefined, t: number): Keyframe[] | undefined {
  const list = (keys ?? []).filter((k) => Math.abs(k.t - t) >= 1e-3);
  return list.length ? list : undefined;
}
