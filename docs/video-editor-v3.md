# Video editor v3

Status: decided on Irori card #81 (coordinator ginger), 2026-10-30.
Architecture from #83 (minnow); feature survey and priorities from #82 (grove).
C1's frozen types landed in #106.

## Goal

Blake: the inspector should look clean and professional, and the editor
needs much more. He wants full adjustments, custom saved filters, speed
ramps and freeze frames, and an Effects library (glitch, shake and more)
where every parameter is adjustable and can be keyframed.

## Decisions (ginger, 2026-10-30)

- Adopt the #83 architecture as written below, including the shared JSON
  definition schema.
- Adopt #82's colour recommendation: every per-pixel colour step (adjust,
  curves, HSL, look and intensity) compiles into ONE generated 33³ 3D LUT.
  The export uses `lut3d`; the preview samples it with a WebGL2
  `sampler3D`. Spatial effects stay separate passes.
- The reverse cache gets its own 10 GB default, which can be cleared in
  Settings → Storage.
- Slow motion offers Off / Blend frames / Smooth. Smooth is labelled
  "slow export".
- Curve speeds run 0.1×–10×, with audio muted outside 0.25×–4×.
- App text never names or compares to other products.

## v3 scope (priorities from the #82 survey)


1. **Tabbed inspector**: Video / Color / Effects / Speed / Audio, with section and parameter resets, numeric entry, and an effect stack with enable/reorder. (P0)
2. **Keyframes** on transform, opacity and volume, with Linear/Hold/Ease in/out/in-out. TS samples the curves; export via ƒ expressions or `sendcmd`. (P0)
3. **Colour adjustments**: exposure, contrast, highlights, shadows, temperature, tint, saturation, vibrance, all compiled to one generated 3D LUT, exported with `lut3d` and previewed with a `sampler3D`. Parity is exact by construction. (P0)
4. **Looks + intensity**: about 15 curated `.cube` looks replacing the 6 matrices, with an intensity slider baked into the LUT. (P0)
5. **User-saved custom filters**: save, apply, rename, delete and export adjust + look + effects as JSON presets. (P0)
6. **.cube LUT import** via `lut3d`. (P0)
7. **Speed ramp presets**: Montage, Hero, Bullet, Jump Cut, Flash In, Flash Out. Exported as constant-speed sub-segments + concat. (P0)
8. **Freeze frame** of 0.1..10 s via `tpad`/`loop`. (P0)
9. **Zoom/punch-in effect**: scale, centre picked on the player, in/hold/out, easing. (P0)
10. **Shake**: amplitude, frequency, rotation, zoom, seeded. (P0)
11. **Flash** (white/colour, duration, intensity) and **RGB split / chromatic aberration** (`rgbashift` ⚙). (P0)
12. **Glitch**: rgbashift + block slices + noise bursts, seeded. There is no frei0r, so it's a composite. (P0)
13. **Sharpen** via `cas` ⚙, and **Gaussian blur** via `gblur` ⚙ replacing the box blur. (P0)
14. **Transitions**: crossfade, dip to black/white (P0), then the xfade wipes/slides/zoom/pixelize and a custom-expression glitch transition (P1).
15. **Vertical canvas**: 9:16/1:1/4:5 presets, Fit/Fill, and a **blurred background fill**. (P0)
16. **Text styling**: stroke, shadow, background box (P0); in/out animations and fonts (P1).
17. **Audio**: loudness normalise (-14 LUFS default) and noise reduction (`afftdn` / `arnndn`) (P0); EQ presets and ducking via `sidechaincompress` (P1).
18. **Export**: 60 fps (P0); quality slider, GIF/WebP via palettegen/paletteuse, and Shorts/TikTok presets (P1).
19. **Masks**: a shape mask for blurring or pixelating a region (names, killfeed, chat), with feather and invert via `maskedmerge` (P1). Also: vignette, grain, curves, HSL, whites/blacks (P1).
20. **Smooth slow-mo** (frame blend by default, opt-in optical flow via `minterpolate`), **reverse** via a reversed proxy, and **motion/radial blur** (P1).

## Flags: no LGPL route (or only an approximation)

- `eq`, `boxblur`, `smartblur`, `frei0r` (so no glitch0r, camerashake or rgbsplit0r): all replaced above by LGPL equivalents (lutrgb/colorlevels/LUT, gblur/avgblur, rgbashift, crop expressions).
- `perspective` (corner pin): ⛔ → rotate/shear, or skip in v3.
- Radial/zoom blur: ⛔ native → multi-scale `blend` approximation.
- Vignette *lighten*: `vignette` only darkens → `geq` or a generated-gradient `blend`.
- `rubberband` pitch shift: ⛔ → `asetrate`+`atempo` (changes formants, acceptable for voice FX).
- Speech-to-text: ⛔ no `whisper` filter → a separate whisper.cpp helper.
- Colour emoji in text and rounded text boxes: ⛔ in drawtext → pre-rendered PNG overlays.
- Object tracking for masks: ⛔ (P2 anyway).
- Encoders: no libx264/libx265 → hardware encoders or libopenh264/libsvtav1, as today.
- Optical flow: `minterpolate` is LGPL but slow and leaves artefacts on HUDs → opt-in only.
- Sidechain ducking and AI noise reduction have no WebAudio live equivalent → the preview renders through ffmpeg.



## Facts this design rests on (checked 2026-10-30)

minnow checked the exact FFmpeg Toru ships (`ffmpeg_fetch.go`: BtbN
autobuild-2026-09-30, n9.0.2 lgpl-shared, SHA-256 matches the pin).

- **Present:** colorchannelmixer, lutrgb, lut1d, lut3d, haldclut, curves,
  colorbalance, colortemperature, hue, huesaturation, vibrance,
  selectivecolor, exposure, vignette, gblur, avgblur, unsharp, cas, noise,
  chromakey, colorkey, hsvkey, lumakey, despill, geq, tpad, loop, reverse,
  areverse, setpts, atempo, minterpolate, tmix, framestep, rotate,
  hflip/vflip/transpose, zoompan, lenscorrection, chromashift, rgbashift,
  edgedetect, sobel, convolution, negate, pixelize, xfade, blend, colorize,
  monochrome, drawtext, fade.
- **Missing:** eq, boxblur, perspective, rubberband, frei0r.
- **Runtime commands** (so `sendcmd` can animate them): colorchannelmixer,
  lutrgb, curves, colorbalance, colortemperature, hue (also takes
  expressions in `t`), huesaturation, vibrance, exposure, gblur (sigma),
  chromakey, rotate, overlay x/y, crop, scale, chromashift, rgbashift.
- **No commands:** unsharp, noise, avgblur size. vignette has none either,
  but takes expressions.

The full list gets checked
into the repo as the LGPL allowlist (see "LGPL export").

## 1. Data model

### Clip effects: an ordered stack

```ts
// frontend/src/trim/project.ts (v3)
export type Ease = "linear" | "in" | "out" | "inOut" | "hold";
export type Keyframe = { t: number; v: ParamValue; ease: Ease };
// t: seconds from the clip's start ON THE TIMELINE (clip-local output time),
// so keyframes stay put on screen when the clip is trimmed at its end or
// its speed changes. ease shapes the segment from this keyframe to the next.

export type ParamValue = number | boolean | string; // string: "#rrggbb", enum ids, LUT path

export type EffectInstance = {
  id: string;                // newId("fx")
  type: string;              // registry id: "adjust", "chromaKey", "blur", "rgbSplit"...
  enabled: boolean;
  params: Record<string, ParamValue>;      // missing = the definition's default
  keys?: Record<string, Keyframe[]>;       // per param, sorted by t; absent = static
};

export type Clip = {
  // ...v2 fields unchanged: id, media, start, in, out, volume, muted,
  // fadeIn, fadeOut, levels, transform
  speed: number;                 // constant speed when there's no curve (as v2)
  speedCurve?: SpeedCurve;       // section 4; overrides speed
  hold?: boolean;                // freeze frame: shows source frame `in` for (out - in) s
  reversed?: boolean;            // section 4
  blendFrames?: "off" | "mix" | "smooth";
  effects: EffectInstance[];     // bottom → top: effects[0] runs first
  anim?: Record<AnimTarget, Keyframe[]>; // keyframes on transform / volume
};
export type AnimTarget = "x" | "y" | "scale" | "opacity" | "rotation" | "volume";
```

The transform gains `rotation` (degrees). Transform and volume are clip
properties, not effects, but they animate the same way (`anim`), with the
same Keyframe type, interpolation code and UI.

**Interpolation** is one pure function, shared by preview and export:
`valueAt(def, base, keys, t)`. Numbers interpolate with the segment's ease
curve: linear, cubic in/out/inOut, or hold (a step). Colours interpolate per
RGB channel. Booleans and enums always hold. It lives in
`frontend/src/trim/fx/keyframes.ts` with a Go twin, `internal/fx/keyframes.go`.
The parity test checks the two agree at 200 random (keys, t) pairs.

### Migration v2 → v3

`Project.version` becomes `3`. `openSaved` (EditVideo) and Go's `summarize`
accept 2 and 3. A v2 project is migrated in memory when it loads and saved
as v3 on the next autosave. An older Toru that meets a v3 file says
"This project was made with a newer Toru" instead of failing silently;
`summarize` already skips unknown versions.

Per clip, the migration is exact, so a migrated project renders the same
pixels:

1. `chroma` → `{type: "chromaKey", params: {color, similarity, blend}}`
2. `filters`. v2 draws `clamp(look · saturation)`, then
   `clamp(contrast/brightness)`, so (leaving out anything at its default):
   - with a look: `look{look, amount: 1, saturation}`, then
     `adjust{contrast, brightness}`
   - without one: `adjust{saturation, contrast, brightness}`

   Every effect clamps its output to 0..1, as v2 and the ffmpeg chains do.
   That's why saturation can't be its own `adjust` in front of a look: it
   would clamp between the two where v2 doesn't. So the look takes
   saturation as its own param, applied before its matrix. Contrast and
   brightness stay after the look: a look matrix and that step don't
   commute unless the look's rows sum to 1 (only bw does; sepia's rows sum
   to 1.35, 1.20 and 0.94). When the colour effects compile into one 3D LUT
   (Decisions), it composes them in stack order with these clamps. The
   `look` definition keeps v2's bw/sepia/vintage/cool/warm matrices among
   its options, for migrated clips. `project-check.ts` runs 8000 colours
   through 400 random filter sets both ways and requires an exact match.
3. `blur > 0` → `{type: "blur", params: {amount: blur}}`. The `blur`
   definition stays a box blur (avgblur, the same shader as v2) in the same
   1080-line units, so migrated clips are exact. A gaussian blur is a
   separate definition (`gaussianBlur`, gblur); its shader has to mimic
   gblur's IIR approximation, so its parity tolerance is looser.

A golden test renders 12 v2 clips through the v2 code (kept as reference
PNGs) and through the migrated v3 stack: they must match, with mean ≤ 1 and
p99 ≤ 6 as today.

## 2. Effect registry: one definition per effect type

### Layout

```
internal/fx/defs/<type>.json        THE definition (schema, UI, export capability)
internal/fx/<type>.go               Go filter builder (registers itself by type)
internal/fx/registry.go             embeds defs/*.json, validates, clamps params
frontend/src/trim/fx/registry.ts    loads the same JSON (import.meta.glob), types
frontend/src/trim/fx/types/<type>.ts  GLSL snippet + uniform mapping
```

The JSON sits under `internal/fx` because Go's `embed` can only reach the
package's own subtree. Vite imports it across the tree, with
`server.fs.allow` widened to the repo root.

### Definition (JSON)

```jsonc
{
  "type": "adjust",
  "version": 1,
  "name": "Adjust",                       // app text; never names another product
  "category": "adjust",                   // adjust | color | blur | stylize | distort | key | light
  "kind": "pixel",                        // pixel | neighbourhood | geometry (see shaders)
  "params": [
    { "id": "exposure",   "label": "Exposure",   "type": "number", "min": -3, "max": 3,  "default": 0, "step": 0.01, "unit": "EV",
      "animate": "cmd" },
    { "id": "contrast",   "label": "Contrast",   "type": "number", "min": 0,  "max": 2,  "default": 1, "display": "percent",
      "animate": "cmd" },
    { "id": "temperature","label": "Temperature","type": "number", "min": 2000, "max": 12000, "default": 6500, "unit": "K",
      "animate": "cmd" },
    { "id": "tint", "label": "Tint", "type": "number", "min": -1, "max": 1, "default": 0, "animate": "cmd" }
    // ... highlights, shadows, whites, blacks, saturation, vibrance, fade, sharpen
  ],
  "export": { "filters": ["exposure", "colortemperature", "colorbalance", "colorchannelmixer", "lutrgb", "curves", "unsharp"] },
  "parity": { "mode": "pixels", "mean": 1, "p99": 6 },  // or "stats" (noise/grain), or "none" + reason
  "amount": "native"                     // native | blend (generic mix via the blend filter) | none
}
```

- `animate` per param: `"cmd"` means export animates it with sendcmd;
  `"expr"` means the filter takes an expression in `t`; `false` means it
  can't be keyframed. The UI hides the keyframe toggle for `false` params
  (e.g. sharpen, built on unsharp, has no commands).
- `type` can be `number`, `boolean`, `color`, `enum` (with
  `options: [{id, label}]`), `point` (x/y pair) or `file` (LUT `.cube`,
  with `accept`).
- Everything else is generated from the definition:
  - **Inspector:** a slider, toggle, swatch plus eyedropper, segmented
    control or file picker per param.
  - **Agent commands:** `editor.effects` lists every definition as JSON
    Schema. `editor.addEffect` / `editor.setEffect` validate params against
    it.
  - **Clamping:** the same rules run on both sides from the JSON.

### Preview side (TS)

```ts
// frontend/src/trim/fx/types/adjust.ts
export default defineShader("adjust", {
  // pixel effects: a GLSL function body over vec4 c (straight alpha, 0..1)
  glsl: `vec4 fx_adjust(vec4 c, Adjust p) { ... return c; }`,
  uniforms: (p) => ({ ... }),          // resolved params at time t → uniform values
});
```

The renderer, `compositor/effects.ts` rewritten as `fx/renderer.ts`, builds
the clip's stack into as few passes as possible:

- **pixel** effects (adjust, look/LUT, colour, vignette, chroma key's colour
  test) are FUSED. Each run of consecutive pixel effects becomes one
  generated fragment shader that calls their functions in order. Programs
  are cached by the stack's type signature (`adjust|look|vignette`).
  Uniforms change every frame; shaders compile only when the stack's shape
  changes. Compiles go through `KHR_parallel_shader_compile`, and the old
  program draws until the new one is ready, so adding an effect never
  stutters.
- **neighbourhood** effects (blur, sharpen, chroma key's 3×3 average,
  pixelate, edge, RGB split) get their own pass(es) reading the previous
  target.
- **geometry** effects (zoom, shake, mirror, rotate, lens) change the UV
  mapping. Each is its own pass (cheap), so the order of a stack like
  "shake, then blur" means what it says.
- LUTs are `TEXTURE_3D` (WebGL2) with trilinear sampling, which matches
  lut3d's default interpolation.

### Export side (Go)

```go
// internal/fx/adjust.go
func init() { Register("adjust", buildAdjust) }

// buildAdjust returns the clip-chain fragment for one instance: its filters
// in order, the pixel format they need in, and per-param command targets
// for keyframes.
func buildAdjust(p Params, ctx Ctx) Chain { ... }

type Chain struct {
    Filters []Filter      // {Name: "exposure", Instance: "fx3_exp", Args: "exposure=0.4:black=0"}
    In      PixFmt        // rgba | gbrap | yuva420p | any
    Cmds    []CmdTarget   // param id → filter instance + option, for sendcmd
}
```

`internal/vid/effects.go` then becomes a small loop. It walks the stack,
inserts `format=` only where the next fragment needs a different pixel
format, gives each filter a unique instance name (`@fx3_exp`) so commands
can address it, and builds the `sendcmd` file for keyframes (section 6).

### Adding an effect

1. `internal/fx/defs/rgbSplit.json`: params, category, export filters, parity mode.
2. `frontend/src/trim/fx/types/rgbSplit.ts`: shader.
3. `internal/fx/rgbSplit.go`: `rgbashift` builder.
4. Nothing else. Three tests catch a half-done effect:
   - `registry_test.go` fails if a def has no Go builder.
   - `frontend/scripts/fx-check.ts` fails if a def has no shader.
   - The parity script picks the new type up automatically and renders it
     at default, min, max and two seeded random parameter sets.
   - `lgpl_test.go` fails if any builder emits a filter that isn't in the
     allowlist.

## 3. User presets ("My filters")

A preset is a named effect stack.

```jsonc
// %AppData%/toru/presets/<id>.json
{ "version": 1, "id": "pre-…", "name": "Night raid", "createdAt": "…",
  "effects": [ { "type": "adjust", "enabled": true, "params": {…}, "keys": {…} }, … ],
  "thumb": "data:image/jpeg;base64,…" }   // 160×90 render of the frame it was saved from
```

- **Go** `internal/presets` is a `PresetService` with `List`, `Save(doc)`,
  `Rename(id, name)`, `Delete(id)`, `Import(path)` and `Export(id, path)`.
  It uses the same id pattern, temp-file-then-rename write and
  "unreadable files skipped" rule as `internal/vid/projects.go`. Files are
  `.json`. Export/import uses a `.torufx` file (the same JSON), so presets
  can be shared.
- **Save as filter**: from the stack's ⋯ menu, "Save these effects as a
  filter…". This takes the clip's enabled effects with new ids. Keyframes
  are kept, and times are relative, so they still fit a clip of another
  length.
- **Apply**: Filters tab → My filters → click. This appends the preset's
  effects to the stack as a group (`group: "<presetId>"` on each instance).
  The group shows as one card with an **Intensity** slider (0–100%), and
  "Ungroup" turns it into its separate cards. Shift-click replaces the stack.
- **Intensity**: the preview lerps between the group's input and output in
  one extra pass. The export does the same with `split` → the group's chain
  → `blend=all_mode=normal:all_opacity=<i>` (blend is present). Shipped
  looks and LUTs use the same mechanism.
- **Built-in looks**: v2's bw, sepia, vintage, cool and warm become
  read-only built-in presets (each a `look` instance). `.cube` LUT import
  goes to `%AppData%/toru/luts` and appears under Filters → LUTs.
- **Rename and delete** happen inline on the preset tile (pencil, trash).
  Delete asks first; the files are small, but it can't be undone.

## 4. Time effects

All the mapping maths lives in one module per side, `frontend/src/trim/speed.ts`
and `internal/vid/speed.go`, with a shared golden table checked by both test
suites.

### Speed ramps

```ts
export type SpeedCurve = {
  points: { s: number; v: number }[];  // s: SOURCE seconds from clip.in; v: speed 0.1..10; ≥ 2 points, sorted
  audio: "follow" | "mute";            // default follow
  preset?: string;                     // "montage" | "hero" | "bullet" | "flashIn" | "flashOut" | "jump" (UI label)
};
```

- **Speed is piecewise LINEAR in source time.** That gives a closed form
  both ways, with no numeric integration:
  - Between `(s0, v0)` and `(s1, v1)`, with `k = (v1 − v0)/(s1 − s0)`, the
    output time is `T(s) = T0 + ln((v0 + k(s − s0))/v0)/k`, or
    `(s − s0)/v0` when `k = 0`.
  - The inverse is `s(T) = s0 + v0·(e^{k(T − T0)} − 1)/k`.
  - The clip's timeline length is `T(out − in)`.
- **Preview**: `clipsAt` computes `srcT = clip.in + s(t − clip.start)`
  instead of the linear formula. The Compositor sets
  `playbackRate = v(srcT)` every frame (pitch kept). Drift correction is
  unchanged, and since rate changes are continuous, nudges stay small.
  Rates are capped at the browser's 0.0625–16. Below 0.25 the element is
  paused and stepped by seeks: very slow ramps look like slow motion, and
  the seek cost is fine at those rates.
- **Export, video**: `setpts` with the same function as a nested `if()` over
  the curve's segments, in the clip chain where `setpts=(PTS-STARTPTS)/speed`
  is today:
  `setpts='if(lt(S,s1),T0+log((v0+k0*(S-s0))/v0)/k0, if(lt(S,s2), …))/TB'`
  with `S=(PTS-STARTPTS)*TB`. A curve has few points (presets use 3–6), so
  the expression stays short. Then `fps=<canvas>` resamples as today:
  frames are dropped or duplicated, not blended (see blending).
- **Export, audio** (`follow`): the clip's source audio is cut into
  0.1 s source chunks (`atrim` + `asetpts`). Each chunk gets
  `atempo=<mean speed over the chunk>`, chained so each step stays within
  0.5–2. The chunks are joined with `concat`. A 10 s ramp makes 100 chunks:
  fine for ffmpeg, and the graph is generated. Outside 0.25–4× the audio is
  muted for that chunk, because atempo artefacts get ugly there. `mute`
  silences the clip's audio wherever the curve isn't 1×. The preview uses
  the same rule, so the two match.
- **Presets** are point sets scaled to the clip's length:
  - Montage: 1 → 3 → 0.3 → 3 → 1
  - Hero: 1 → 0.2 (hold) → 1
  - Bullet: 1 → 0.1 → 1
  - Flash in / out: 4 → 1, 1 → 4
  - Jump: 1 → 6 → 1

  The curve editor (section 5) edits the points.

### Freeze frame

- "Freeze frame" in the clip menu, or `F`, splits the clip at the playhead
  and inserts a **hold clip**. It ripples everything after it on that track
  by the hold length (default 2 s).
- A hold clip is a normal clip with `hold: true`, `out − in` = the hold
  length and speed 1. It shows source frame `in` for its whole length. Its
  audio is always silent, and its effects and keyframes work as usual (a
  zoom-in on a frozen frame is the classic use).
- **Preview**: the element stays paused at `in` (the paused path; after
  #80 it doesn't seek again).
- **Export**: `-ss in -i path`, then
  `trim=end_frame=1,tpad=stop_mode=clone:stop_duration=<len>,setpts=…`.
  tpad is present. No audio input.

### Reverse

- The `reverse` filter buffers the whole input in RAM. At 3440×1440 and
  144 fps, yuv420 is about 1 GB per second of footage, so it can't be used
  on a clip directly.
- **Reverse proxy** (Go, `internal/vid/reverse.go`): `ReverseProxy(path, in, out)`
  renders the range in 2 s chunks, each `-ss k -t 2 -i src -vf reverse -af areverse`,
  encoded all-intra on NVENC (`-g 1`, high bitrate) so frame-accurate
  seeking is cheap. The chunks are concatenated in reverse order. Memory
  stays bounded at about 2 GB for one chunk.
  - Cache: `%LOCALAPPDATA%/toru/cache/reverse/<sha1(path|size|mtime|in|out)>.mp4`,
    LRU-trimmed to 10 GB, cleared from Settings → Storage.
  - Progress events go to the editor, which shows the clip striped with
    "Preparing reverse… 40%". Preview and export both read the proxy, with
    in/out mirrored inside it.
- `reversed: true` applies to the whole clip; split first to reverse part
  of one. Reverse and a speed curve combine: the curve applies to the
  reversed source time.

### Frame blending

`blendFrames`:

- `off` (default): frames are dropped or repeated.
- `mix`: slow motion blends neighbouring frames, with `tmix=frames=2|3`
  (by slowdown) after the speed `setpts` and before `fps`. It's cheap, and
  export-only at first; the preview badge says "Blended on export".
  Preview v2 could crossfade the current and next decoded frames, but that
  needs two elements per clip, which is deferred.
- `smooth`: motion-interpolated slow motion,
  `minterpolate=fps=<canvas>:mi_mode=mci:mc_mode=aobmc:vsbmc=1`. It's
  present, but slow: roughly 2–8 fps at 1440p on the 9800X3D, so a 10 s
  slow-mo takes minutes. Export-only, with an estimate in the export dialog
  ("Smooth slow motion adds about 3 min"). It applies only where the clip is
  slower than 1×: the ramp's slow segments are split out with `select`,
  interpolated, then concatenated.

### Transitions (C8, card #119)

A transition sits at a cut between two clips that touch on one video
track. It is stored on the incoming clip (`clip.transition = {type,
duration}`; seconds in the editor, ms in the export request) and plays
centred on the cut: a duration d runs from cut − ⌊d/2⌋ to cut + (d − ⌊d/2⌋),
in whole ms, so the timeline's length doesn't change.

- **No source handles.** Each clip holds its edge frame for the half that
  runs past its own end: the outgoing clip its last frame, the incoming
  clip its first (export: `tpad` clone; preview: rate 0 at the edge, with
  effects and keyframes frozen there too). One rule for every kind of clip
  (speed curves, reverse, freeze frames, images). Using real handles for
  plain 1x clips is a follow-up.
- **Types** are ffmpeg `xfade`'s: fade (Cross dissolve), fadeblack,
  fadewhite, wipeleft/right/up/down, slideleft/right/up/down, zoomin and
  hblur (Blur). The export joins the clips' canvas-size `yuva420p` layers
  with xfade (a run of linked clips chains them; offset = window start −
  run start) and overlays the run once. The preview
  (`compositor/transition-gl.ts`) draws both layers into buffers and runs a
  port of xfade's formulas over them, un-premultiplying first, because xfade
  mixes straight colour and alpha.
- **Sound** fades out over the half before the cut and in over the half
  after (linear, as afade), on top of the clips' own fades.
- **Doesn't fit → ignored**, never an error. A transition needs the clip
  right before it on the same visible video track, ending where it starts
  (within 1 ms), both drawing a picture, and room for each half next to the
  clip's other transition. `transitionAt` in Go and `trackTransitions` in TS
  apply the same rule; edits that move clips apart leave the field in place.
  Split keeps it on the left part only.
- **What can't match exactly** (`bun run test:transitions` has the limits
  and their reasons): xfade's dip fills are Y=0 and Y=255 on limited-range
  planes, below black and above white, and the preview copies that exactly.
  xfade works per plane on 4:2:0, so the half-size chroma planes take their
  own whole-pixel offsets in slides and zoom. The preview's blur takes 32
  samples across xfade's box window.

## 5. Inspector UX

The inspector is a fixed-width column (as v2) with **six tabs**. The tab bar
only shows tabs that apply to the selected clip: audio clips get Speed and
Audio; images get everything except Speed and Audio.

```
┌ Clip: Call of Duty HQ 20.32.39 ─────────────────────┐
│ [Video][Adjust][Filters][Effects][Speed][Audio]     │
├─────────────────────────────────────────────────────┤
│ VIDEO                                               │
│  Position  X [====|====] 50%   Y [====|====] 50%  ◆ │  ◆ = keyframe toggle (filled when a key is at the playhead)
│  Size      [=====|===] 100%                       ◆ │  ◀ ◆ ▶ appears on hover: previous / toggle / next key
│  Rotation  [====|====] 0°                         ◆ │
│  Opacity   [=========|] 100%                      ◆ │
│  ── Green screen ─────────────────────── [on] ──    │  quick toggle; adds/removes the chromaKey effect
│  Key colour ■ 🖉   Strength [==|====]  Softness ... │
└─────────────────────────────────────────────────────┘
```

- **Adjust**: one pinned `adjust` instance, always first in the stack,
  shown as plain sections:
  - Light: Exposure, Contrast, Highlights, Shadows, Whites, Blacks
  - Colour: Temperature, Tint, Saturation, Vibrance
  - Detail: Sharpen, Fade

  Every slider double-clicks back to its default, and each section has
  "Reset". It's the same instance agents edit through `editor.setEffect`.

```
│ FILTERS                                             │
│  [ All | Looks | LUTs | My filters ]                │
│  ┌────┐┌────┐┌────┐┌────┐                           │
│  │thumb││thumb││thumb││ +  │  thumbs = the clip's current frame through each
│  └────┘└────┘└────┘└────┘                           │  look (rendered by the same renderer, cached per frame)
│  Night raid    Warm   B&W   Import LUT…             │
│  Intensity [=======|==] 80%       (applied filter)  │
│  [Save current effects as a filter…]                │
```

```
│ EFFECTS                                             │
│  🔍 search      [Blur][Stylize][Distort][Light][Key]│  browser: grid of live thumbnails, hover = preview
│  ┌────┐┌────┐┌────┐┌────┐                           │  on the player, click = add to the top of the stack
│  │    ││    ││    ││    │ RGB split  Shake  Zoom…   │
│  └────┘└────┘└────┘└────┘                           │
│  ── This clip (3) ─────────────────────────────     │  the stack, top = last applied
│  ┌ ⠿ RGB split          [on] ↺  ⋯ ┐                 │  ⠿ drag to reorder · on/off · reset · menu
│  │  Amount [===|=====] 6 px    ◆   │                 │  (duplicate, copy, paste, save as filter, delete)
│  │  Angle  [=====|===] 0°      ◆   │                 │
│  └─────────────────────────────────┘                 │
│  ┌ ⠿ Shake (collapsed)  [on] ↺  ⋯ ┐                 │
│  ┌ ⠿ Night raid  80%    [on] ↺  ⋯ ┐  group card     │
```

```
│ SPEED                                               │
│  ( Constant | Curve )                                │
│  Constant: Speed [===|=====] 1.00×  Duration 0:12.4 │
│  Curve:  presets [Montage][Hero][Bullet][Flash in]… │
│   10×┤        •                                     │  draggable points over the clip's source time;
│    1×┤──•────/ \────•──                             │  double-click adds, right-click removes;
│  0.1×┤          •                                   │  y is log-scaled
│        Audio during ramps: (Follow | Mute)          │
│  Reverse [off]   (Preparing… 40% while the proxy renders) │
│  Freeze frame at playhead [2.0 s] [Add]             │
│  Slow motion: (Off | Blend frames | Smooth — slow export) │
```

- **Audio**: as v2 (volume ◆, fades, per-source-track levels), plus
  volume keyframes.
- **Keyframes on the timeline**: a selected clip shows its keyframes as
  small diamonds on a thin lane under the clip. Dragging one moves it in
  time, and Alt+drag copies it. The ease is set from the diamond's
  right-click menu: Linear, Ease in, Ease out, Ease in-out, Hold. Turning
  keyframing on for a param records its current value at the playhead.
  After that, editing the param at a playhead without a key adds a key.
- **Undo**: every control goes through the editor's undo history, as v2.
  Slider drags merge into one step (history keys, `"fx:<id>:<param>"`).
- **App text** never names or compares other products.

## 6. Performance and the LGPL export

### Preview budget (RTX 5080, WebView2 / ANGLE D3D11)

- **GPU**: up to 4 ms per frame for a clip's whole stack at 144 Hz.
  Effects run at **preview resolution**, not source resolution. The first
  pass resamples the source frame to the layer's on-screen size; today v2
  runs effects at source size (3440×1440 = 5 MP). Params that depend on
  resolution (blur, RGB split, pixelate, shake) are defined in 1080-line
  units, so the look doesn't change with resolution, and the parity test
  runs at a fixed height (180), as today.
  - At about 1.6 MP per layer, a 10-pass stack is about 16 MP per frame,
    or 2.3 Gpx/s at 144 fps. That's a small fraction of the 5080's fill
    rate.
  - Pixel effects fuse into one pass, so a typical stack (adjust, look,
    vignette, RGB split, blur) is 4 passes.
- **Upload**: the real cost today is `texImage2D` of every 5 MP frame,
  about 20 MB. Switch to `requestVideoFrameCallback`, uploading only when
  the element has a new frame, so a 60 fps source on a 144 Hz display
  uploads 60 times a second, not 144.
- **Compile**: shapes are cached and compiled in parallel, so adding an
  effect never blocks a frame.
- **Budget checks**: the Compositor's `stats` hook (it already logs per
  frame) gains `gpuMs` via `EXT_disjoint_timer_query_webgl2` when
  available. A perf script (C4) plays a 3440×1440 clip with the
  "everything" stack and fails above 6 ms p95.

### Export budget (9800X3D, CPU filters)

- Colour filters on 1440p frames run at hundreds of fps, so they're
  negligible.
- gblur at large sigma, minterpolate and the reverse proxy are the slow
  ones. The export dialog shows an estimate per heavy option.
- Decode stays hardware where possible, and filters run with
  `-filter_complex_threads <cores>`.

### LGPL-only export

- `internal/fx/lgpl_filters.txt` is generated from the PINNED build's
  `ffmpeg -filters` output. Regenerating it is part of bumping
  `ffmpegBuild`/`ffmpegSHA256`/`ffmpegSize`, as one change.
- `lgpl_test.go` runs every registered builder at default, min and max,
  plus each keyframe mode. It parses the filter names out of the chains
  and fails on any name not in the allowlist. That catches `eq`, `boxblur`
  and `perspective` the moment someone writes them.
- Each definition's `export.filters` must list what its builder emits, and
  the test checks the two agree, so a definition stays honest about its
  route.
- `TestEffectsRunInFFmpeg` (it exists today) also runs against the pinned
  build when `TORU_FFMPEG` points at it (C4 adds a helper that downloads
  and verifies it, like `fetchFFmpeg`). That checks options and syntax,
  not just names.

### Keyframes in the export

| Param `animate` | Export | Matches the preview because |
| --- | --- | --- |
| `cmd` | A `sendcmd=f=<clip>.cmds` filter at the head of the clip's effect chain, after the speed setpts, so its timestamps are clip-local output time. It gets one line per output frame per animated param inside the keyed range, with values from `valueAt` (Go). The file is written next to the export's temp files, since a 60 s clip with 3 animated params at 60 fps is 10,800 lines, too long for a command line. | The preview samples `valueAt` at the same frame times. |
| `expr` | The filter's own expression in `t` (hue h/s/b, rotate angle, vignette angle, overlay x/y), a piecewise `if()` of eased segments, built from the same keys. | Same function, written as an expression. |
| `false` | Not animatable. The UI hides ◆, and the agent command refuses keys. | — |

Transform keyframes are the `anim` targets:

- x/y → overlay `x`/`y` expressions in `t`, offset by the clip's start.
- scale → `scale` with `eval=frame` and size expressions. The overlay
  position is computed from the same eased scale, so the clip stays
  centred.
- rotation → `rotate` with an angle expression (present).
- opacity → `colorchannelmixer` `aa` via sendcmd.
- volume → `volume` with `eval=frame` and an expression.

## 7. Work split (cards under #81)

**Day-0 contract:** the TS types in section 1 and the JSON definition
schema in section 2 are frozen by C1's first commit. Everyone else builds
against them in parallel.

| Card | Owns (paths) | Depends on | Delivers |
| --- | --- | --- | --- |
| **C1 Model + migration + keyframes** | `frontend/src/trim/project.ts`, `frontend/src/trim/migrate.ts` (new), `frontend/src/trim/fx/keyframes.ts` (new), `frontend/scripts/project-check.ts`, `internal/vid/project.go` (types/validate only), `internal/vid/projects.go` (`summarize` v2+v3), `internal/fx/keyframes.go` (new) | — | v3 types, v2→v3 migration (exact), `valueAt` TS+Go with shared golden table, `editor.get` returns v3 |
| **C2 Registry + preview renderer** | `internal/fx/defs/**` (new), `internal/fx/registry.go`, `frontend/src/trim/fx/registry.ts`, `frontend/src/trim/fx/types/**`, `frontend/src/trim/fx/renderer.ts` (replaces `compositor/effects.ts`), `frontend/vite.config.ts` (fs.allow) | C1 types | JSON schema + loader both sides; fused/multi-pass renderer; v2's effects (chromaKey, adjust, look, blur) as the first 4 defs; Compositor switched to the renderer (one-line call-site change, coordinated with C5) |
| **C3 Go builders + export** | `internal/fx/*.go` builders, `internal/fx/lgpl_filters.txt`, `internal/fx/lgpl_test.go`, `internal/vid/effects.go`, `internal/vid/project_args.go` | C1 types, C2 defs | builders for the first 4 defs plus 6 from #82's P0 list; format negotiation; sendcmd/expr keyframes; anim targets in the overlay chain; LGPL allowlist test |
| **C4 Parity + perf harness** | `frontend/scripts/effects-parity.ts`, `frontend/scripts/fx-check.ts` (new), `frontend/scripts/fx-perf.ts` (new), `internal/vid/effects_parity_test.go`, `internal/fx/pinned_ffmpeg_test.go` (new) | C2+C3 interfaces (can start on v2's 4 effects) | parity cases generated from the registry (default/min/max/random, keyed-at-t), migration golden images, pinned-ffmpeg test runner, GPU perf budget check |
| **C5 Time effects** | `frontend/src/trim/speed.ts` (new), `internal/vid/speed.go` (new), `internal/vid/reverse.go` (new), the time-mapping part of `frontend/src/trim/compositor/Compositor.tsx` (lease `Compositor.tsx` while editing; C2 only touches its renderer call) | C1 | speed curves (closed-form maths both sides + golden table), freeze/hold clips, reverse proxy with cache and progress, tmix/minterpolate export, the clip-length changes in project.ts ops (via C1's owner or a lease) |
| **C6 Inspector + browser UI** | `frontend/src/trim/Inspector.tsx` → `frontend/src/trim/inspector/**` (new folder: tabs, ParamControl, EffectCard, EffectBrowser, SpeedCurveEditor, KeyframeToggle), `frontend/src/trim/MultiTimeline.tsx` (keyframe lane only) | C1 types, C2 registry API (`defs`, `renderThumb`) | the six tabs, generated controls, browser with live thumbnails, stack reorder/enable/reset, keyframe toggles + timeline diamonds, speed curve editor; screenshots on a real build |
| **C7 Presets + agent commands** | `internal/presets/**` (new), `agent_commands_presets.go` (new), `frontend/src/trim/agentCommands.ts`, bindings regen | C1 types, C2 registry | PresetService (list/save/rename/delete/import/export, `.torufx`), built-in looks as presets, LUT import folder, `editor.effects`/`addEffect`/`setEffect`/`removeEffect`/`moveEffect`/`setKeyframe`/`removeKeyframe`/`applyPreset` + `preset.*` |

**Order**: C1 goes first and lands its types in hours, not days. Then C2,
C3, C4, C5 and C7 run in parallel. C6 starts once C2 exposes `defs` and
`renderThumb`, and can mock them before that.

**Conflicts to watch**: `project.ts` (C1 owns; C5 needs clip-length changes
→ C1 makes them, or C5 leases the file); `Compositor.tsx` (C5 owns time
mapping, C2 changes one call site); `agentCommands.ts` (C7 only).

## Notes for the builders (known traps)

- **Speed ramps (C3/C5):** a segment with `k = 0` must use the
  `(s − s0)/v0` form; `ln(…)/k` divides by zero. Use
  `S = (PTS-STARTPTS)*TB` consistently in the setpts expression.
- **Animated scale (C3):** a `scale` with `eval=frame` feeding `overlay`
  changes the overlay input's frame size every frame. Check on the pinned
  build that overlay accepts that. If it doesn't, scale into a fixed-size
  transparent box (`pad` with `eval=frame`) before the overlay.
- **Audio ramps (C5):** atempo uses windows tens of ms long, so 0.1 s
  chunks joined with plain `concat` can click at the boundaries. Start with
  0.25 s chunks, and add a short `acrossfade` at the joins if clicks remain.
- **Hold clips (C1):** they reuse `in..out` as a made-up source range, as
  images do. Trim and length ops must treat `hold` like an image, not
  bounded by the media's duration.
- **Version gate (C1):** `SaveProject` rejects `version ≠ 2` today, not just
  `summarize`. `ClipJSON`/`MediaJSON` live in `effects.go` (C3's file). C1
  moves them into `project.go` first, on day 0, and adds the v3 fields
  there, so C3 builds on them without a conflict.
- **sendcmd path (C3):** escape the Windows path in `sendcmd=f=…`
  (forward slashes, `\:`) the way `TextFilter` escapes its font path.

## Deliberate deviation (adopted)

The card asks for "one TS definition plus its Go twin". This design puts the
**schema** in a shared JSON file (`internal/fx/defs/<type>.json`), which Go
embeds and Vite imports, plus a TS shader file and a Go builder file. That's
the only way the params (names, ranges, defaults, units, keyframe support,
export filters) are written exactly once. Two hand-kept copies of the schema
drift; JSON can't. The behaviour (shader, filter chain) is still one file
per language, as the card intended.


## Later cards (after C1–C7)

| Card | Scope |
| --- | --- |
| C8 Transitions | Crossfade, dip to black/white (P0), xfade wipes/slides/zoom/pixelize, and a glitch transition (P1) |
| C9 Canvas + text styling | 9:16/1:1/4:5 presets, Fit/Fill, blurred background fill; text stroke/shadow/box, in/out animations |
| C10 Audio tools | loudnorm -14 LUFS, noise reduction (afftdn/arnndn), EQ presets, ducking (sidechaincompress) |
| C11 Masks + export | Shape masks to blur/pixelate a region (feather, invert), 60 fps export, quality slider, GIF/WebP, Shorts presets |
