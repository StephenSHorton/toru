// canvas-check exercises the project frame (src/trim/canvas.ts): presets,
// fill/fit maths, which clips a frame change re-places, and per-clip
// placement.
//
//   bun scripts/canvas-check.ts
import { aspectLabel, fillScale, normCanvas, placeClip, presetOf, setCanvas } from "../src/trim/canvas";
import { FIT, type Clip, type Media, type Project, type Track } from "../src/trim/project";

let pass = 0;
let fail = 0;
function ok(name: string, cond: boolean, detail?: unknown) {
  if (cond) pass++;
  else {
    fail++;
    console.log(`FAIL ${name}${detail === undefined ? "" : `: ${JSON.stringify(detail)}`}`);
  }
}
const near = (a: number, b: number, tol = 1e-3) => Math.abs(a - b) <= tol;

const media = (id: string, w: number, h: number, kind: Media["kind"] = "video"): Media => ({ id, path: `C:/${id}.mp4`, url: "", kind, duration: 10, width: w, height: h, fps: 30, hasAudio: true, tracks: [] });
const clip = (id: string, m: string, p: Partial<Clip> = {}): Clip => ({
  id,
  media: m,
  start: 0,
  in: 0,
  out: 5,
  speed: 1,
  volume: 1,
  muted: false,
  fadeIn: 0,
  fadeOut: 0,
  transform: { ...FIT },
  filters: { brightness: 0, contrast: 1, saturation: 1, look: "none", blur: 0 },
  ...p,
});
const track = (id: string, kind: Track["kind"], clips: Clip[]): Track => ({ id, kind, muted: false, hidden: false, locked: false, clips });

function project(): Project {
  return {
    version: 3,
    id: "p",
    name: "t",
    canvas: { width: 1920, height: 1080, fps: 30 },
    media: [media("wide", 1920, 1080), media("cam", 1280, 720), media("logo", 512, 512, "image"), media("song", 0, 0, "audio")],
    tracks: [
      track("v1", "video", [clip("a", "wide"), clip("b", "wide", { start: 5, transform: { ...FIT, rotation: 5, opacity: 0.8 } })]),
      track("v2", "video", [
        clip("pip", "cam", { transform: { x: 0.8, y: 0.2, scale: 0.4, opacity: 1 } }),
        clip("full", "cam", { start: 6 }),
        clip("keyed", "cam", { start: 12, anim: { x: [{ t: 0, v: 0.2, ease: "linear" }, { t: 1, v: 0.8, ease: "linear" }] } }),
      ]),
      track("a1", "audio", [clip("s", "song")]),
    ],
    texts: [{ id: "t1", text: "hi", x: 0.5, y: 0.8, size: "m", start: 0, end: 2 }],
    crop: { x: 100, y: 0, w: 1000, h: 1080 },
  } as Project;
}
const byId = (p: Project, id: string) => p.tracks.flatMap((t) => t.clips).find((c) => c.id === id)!;

// Maths.
ok("fill 16:9 in 9:16", near(fillScale(1920, 1080, 1080, 1920), (1920 / 1080) ** 2));
ok("fill same shape is 1", near(fillScale(1280, 720, 1920, 1080), 1));
ok("fill square picture in 16:9", near(fillScale(512, 512, 1920, 1080), 1920 / 1080));
ok("fill 16:9 in 4:5", near(fillScale(1920, 1080, 1080, 1350), (1350 / 1080) * (1920 / 1080)));
{
  // Fill covers the frame exactly on one side (as the export sizes it: fit x scale).
  const [w, h, cw, ch] = [1920, 1080, 1080, 1920];
  const fit = Math.min(cw / w, ch / h);
  const s = fillScale(w, h, cw, ch);
  ok("filled picture covers the frame", w * fit * s >= cw - 0.5 && h * fit * s >= ch - 0.5 && (near(w * fit * s, cw, 0.5) || near(h * fit * s, ch, 0.5)), [w * fit * s, h * fit * s]);
}
ok("normCanvas evens and clamps", JSON.stringify(normCanvas({ width: 1081, height: 9, fps: 0 })) === JSON.stringify({ width: 1082, height: 16, fps: 30 }));
ok("presetOf", presetOf({ width: 1080, height: 1920, fps: 30 })?.id === "9:16" && !presetOf({ width: 1000, height: 1920, fps: 30 }));
ok("aspect labels", aspectLabel({ width: 1280, height: 720, fps: 30 }) === "16:9" && aspectLabel({ width: 2560, height: 1080, fps: 30 }) === "64:27", aspectLabel({ width: 2560, height: 1080, fps: 30 }));

// 16:9 â†’ 9:16 with fill.
{
  const p = project();
  const q = setCanvas(p, { width: 1080, height: 1920, fps: 30 }, "fill");
  const s = (1920 / 1080) ** 2;
  ok("canvas changed", q.canvas.width === 1080 && q.canvas.height === 1920);
  ok("crop dropped", q.crop === null);
  ok("bottom clip filled", near(byId(q, "a").transform.scale, s) && byId(q, "a").transform.x === 0.5, byId(q, "a").transform);
  ok("bottom clip keeps rotation/opacity", byId(q, "b").transform.rotation === 5 && byId(q, "b").transform.opacity === 0.8 && near(byId(q, "b").transform.scale, s));
  ok("centred full overlay filled", near(byId(q, "full").transform.scale, s), byId(q, "full").transform);
  ok("picture-in-picture kept", JSON.stringify(byId(q, "pip").transform) === JSON.stringify(byId(p, "pip").transform));
  ok("keyframed overlay kept", byId(q, "keyed").anim?.x?.length === 2 && byId(q, "keyed").transform.scale === 1);
  ok("audio untouched", byId(q, "s") === byId(p, "s"));
  ok("texts untouched", q.texts === p.texts);
  // Back to 16:9 with fill: a filled overlay counts as full frame again.
  const r = setCanvas(q, { width: 1920, height: 1080, fps: 30 }, "fill");
  ok("round trip back to fill 1", near(byId(r, "a").transform.scale, 1) && near(byId(r, "full").transform.scale, 1), [byId(r, "a").transform, byId(r, "full").transform]);
}
// fit and keep.
{
  const p = project();
  const f = setCanvas(p, { width: 1080, height: 1080, fps: 30 }, "fit");
  ok("fit: scale 1 centred", byId(f, "a").transform.scale === 1 && byId(f, "full").transform.scale === 1);
  const k = setCanvas(p, { width: 1080, height: 1920, fps: 30 }, "keep");
  ok("keep: transforms unchanged", p.tracks.every((t) => t.clips.every((c) => JSON.stringify(byId(k, c.id).transform) === JSON.stringify(c.transform))));
  ok("keep: crop still dropped", k.crop === null);
  const r = setCanvas(p, { width: 1920, height: 1080, fps: 60 }, "fill");
  ok("rate only: no placement, crop kept", r.canvas.fps === 60 && byId(r, "a") === byId(p, "a") && r.crop === p.crop);
  ok("no change returns the same project", setCanvas(p, { width: 1920, height: 1080, fps: 30 }, "fill") === p);
}
// Per clip.
{
  const p = project();
  const filled = placeClip(p, "pip", "fill");
  ok("placeClip fill centres", byId(filled, "pip").transform.x === 0.5 && near(byId(filled, "pip").transform.scale, 1), byId(filled, "pip").transform);
  const sq = placeClip(p, "keyed", "fit");
  ok("placeClip fit drops position keys", !byId(sq, "keyed").anim?.x?.length && byId(sq, "keyed").transform.x === 0.5);
  const lg = placeClip({ ...p, canvas: { width: 1080, height: 1920, fps: 30 } }, "a", "fill");
  ok("placeClip fill on vertical", near(byId(lg, "a").transform.scale, (1920 / 1080) ** 2));
  const rs = placeClip(placeClip(p, "b", "fill"), "b", "reset");
  ok("reset is the default transform", JSON.stringify(byId(rs, "b").transform) === JSON.stringify(FIT));
  ok("audio clip ignored", placeClip(p, "s", "fill") === p);
  ok("same-shape fill is 1", near(byId(placeClip(p, "a", "fill"), "a").transform.scale, 1));
}

console.log(`${pass} passed, ${fail} failed`);
if (fail) process.exit(1);
