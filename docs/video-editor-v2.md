# Video editor v2

Status: decided on Irori card #1 ("Toru: add a video editor"), 2026-10-29.
v1 is in `docs/video-editor.md` and ships first; v2 is built alongside it.

## Goal

Turn the Edit video window into a layered editor:

- a **media bin** sidebar with your Library (clips, recordings,
  screenshots) and your own files (video, audio, images), dragged in from
  Explorer or picked with a dialog;
- a **multi-track timeline**: stack videos and images on top of each other
  (picture-in-picture, logos, memes), and add music or sound effects on
  audio tracks;
- per-clip **tools**: speed, volume and fades, green screen, color filters,
  position/size/opacity;
- **undo / redo** for everything (already in v1; v2 keeps it).

## What carries over from v1

Kept as they are: the window (`frontend/src/routes/EditVideo.tsx`),
Add clip's Library listing (`AddClipDialog.tsx`, becomes the bin's
Library tab), text overlays (`TextOverlays.tsx`, `edit_text.go`), crop
(`CropBox.tsx`), per-source-track audio levels (`AudioTracks.tsx`), export
service plumbing (`edit_service.go`: probe, Save As, progress, cancel,
Discord preset), undo history (`historyApply` / keyed drag merges in
`edl.ts`).

Replaced: the single-sequence `Edl` becomes a `Project` with tracks
(`frontend/src/trim/project.ts`), the one-`<video>` preview
(`usePreviewPlayback.ts`) becomes a canvas compositor, `EditTimeline.tsx`
becomes a multi-track timeline, `BuildEditArgs` gains a multi-track builder.
v1 code stays until the v2 window replaces it.

## v2 scope

1. **Media bin** (left sidebar, collapsible). Tabs: Library (clips,
   recordings, screenshots, from the existing services) and Imported (files
   you added this session). Import by dragging files from Explorer onto the
   window, or with an Import button (file dialog, multi-select). Accepted:
   video (mp4, mkv, mov, webm), audio (mp3, wav, m4a, aac, ogg, flac),
   images (png, jpg, jpeg, webp, gif as a still). Items show a thumbnail,
   name and length. Drag an item onto a track, or double-click to append it
   to the end of the bottom track. Imported files are used where they are; nothing is
   copied.
2. **Tracks.** Every track is free-form: clips sit anywhere in time and
   gaps are allowed (a gap with nothing under it exports as black / silence).
   Any number of **video** tracks (higher tracks draw on top) and **audio**
   tracks below them. A new project starts with one video track and one
   audio track. Add/remove track, mute, hide, lock. Delete leaves a gap;
   "Close gap" (right-click a gap, or `Shift+Delete` on a clip for a ripple
   delete) pulls later clips on that track left.
3. **Clips** on video tracks: video or image (image default length 5s,
   stretch by dragging its edge). On audio tracks: audio files, or the
   audio of a video. Split (`S`), delete, trim edges, move in time and
   between tracks of the same kind. Clips on one track never overlap: a drop
   onto an occupied spot lands at the nearest free spot.
4. **Transform.** Each video/image clip has position, scale and opacity.
   Drag it on the player to move it and drag its corners to resize
   (selected clip only); exact values in the inspector. Clips on the bottom
   video track default to fit-to-canvas; clips on higher tracks default to
   40% size, centred.
5. **Speed.** 0.25x to 4x per clip (video and audio), pitch kept.
6. **Audio.** Per clip: volume 0 to 200%, mute, fade in, fade out. Per
   track: mute. Separate-tracks clips keep v1's per-source-track levels.
7. **Green screen.** Per video/image clip: key color (eyedropper on the
   player, default pure green), strength (similarity) and edge softness
   (blend). Makes that color transparent so lower tracks show through.
8. **Filters.** Per video/image clip: brightness, contrast, saturation
   sliders, and one look preset: None, Black & white, Sepia, Vintage, Cool,
   Warm. Blur 0 to 20.
9. **Text** stays as v1 (always on top of every track).
10. **Canvas.** Output size and frame rate default to the first video
    added; changeable in the export dialog via the v1 presets.
11. **Projects.** The edit autosaves to a project file
    (`%AppData%/toru/projects/<id>.json`) as you work; Library gets a
    Projects tab to reopen one.
12. **Undo / redo** covers every change above (one step per drag).

Later (not v2): transitions, keyframed animation, masks, LUTs, more text
styles, emoji in text, GIF export, proxy media for 4K.

## Data model

TypeScript in `frontend/src/trim/project.ts` (pure, like `edl.ts`); Go
mirror in `internal/vid/project.go` (the export request). Times in
**seconds** in TS and **milliseconds** in Go, as in v1.

```ts
export type MediaKind = "video" | "image" | "audio";
export type Media = {
  id: string;            // stable within the project
  path: string;          // absolute
  url: string;           // served URL for the preview (MediaService.Register)
  kind: MediaKind;
  duration: number;      // s; 0 for images
  width: number; height: number; // 0 for audio
  fps: number;           // 0 for image/audio
  hasAudio: boolean;
  tracks: AudioTrack[];  // v1 type: audio streams with titles / mix flag
};

export type Transform = { x: number; y: number; scale: number; opacity: number };
// x, y: clip centre in 0..1 of the canvas; scale: 1 = fit inside the canvas.

export type Chroma = { color: string; similarity: number; blend: number }; // "#00ff00", 0.01..1, 0..1
export type Look = "none" | "bw" | "sepia" | "vintage" | "cool" | "warm";
export type Filters = { brightness: number; contrast: number; saturation: number; look: Look; blur: number };
// brightness -1..1 (0), contrast 0..2 (1), saturation 0..3 (1), blur 0..20 (0)

export type Clip = {
  id: string;
  media: string;         // Media.id
  start: number;         // timeline position, s
  in: number; out: number; // source range, s (images: 0..length)
  speed: number;         // 0.25..4, 1
  volume: number;        // 0..2, 1
  muted: boolean;
  fadeIn: number; fadeOut: number; // s
  levels?: TrackLevel[]; // v1 per-source-track levels (index = audio stream)
  transform: Transform;  // video/image only
  chroma?: Chroma;       // video/image only; undefined = off
  filters: Filters;      // video/image only
};
// timeline length of a clip = (out - in) / speed

export type Track = {
  id: string;
  kind: "video" | "audio";
  muted: boolean; hidden: boolean; locked: boolean;
  clips: Clip[];         // sorted by start, no overlaps within a track
};

export type Project = {
  version: 2;
  id: string;
  name: string;
  canvas: { width: number; height: number; fps: number };
  media: Media[];
  tracks: Track[];       // video tracks bottom→top, then audio tracks
  texts: Text[];         // v1 Text, output time
  crop: Rect | null;     // v1 crop, canvas pixels
};
```

Pure operations (all return a new Project; history wraps them like v1):
`newProject(first: Media)`, `addMedia`, `addTrack(kind)`, `removeTrack`,
`setTrackFlags`, `insertClip(trackId, mediaId, at)` (nearest free spot),
`appendClip(mediaId)` (end of the bottom video track, or the first audio
track for audio), `moveClip(clipId, trackId, start)` (nearest free spot),
`trimClip(clipId, edge, t)`, `splitAt(t, trackId?)`, `removeClip`,
`rippleDelete(clipId)`, `closeGap(trackId, t)`,
`updateClip(clipId, patch)`, `projectDuration`, `clipsAt(t)` (visible clips
bottom→top with their source time), `fromEdl(v1Edl)`, `toExportRequest`.

Go export request:

```go
type ProjectExport struct {
	Project ProjectJSON `json:"project"` // the Project above, ms times
	Preset  string      `json:"preset"`  // v1 presets
	Height  int         `json:"height,omitempty"`
	Kbps    int         `json:"kbps,omitempty"`
	OutPath string      `json:"outPath"`
}
func (s *VideoService) ExportProject(req ProjectExport) (string, error) // "edit:progress"
```

## Design

### Media serving and import (Go)

`/__file/` only serves Toru's temp and Library folders. Imported files can
be anywhere, so a new `MediaService` keeps a registry of paths the user
picked or dropped and serves them at `/__media/<id>/<name>` with
`http.ServeContent` (Range support for seeking). `Register(paths) []Media`
probes each (ffprobe; images by `image.DecodeConfig` or ffprobe) and returns
Media. `PickMedia()` opens a multi-select dialog. The edit window turns on
`EnableFileDrop` and forwards `WindowFilesDropped` paths to the page as
`media:dropped`. `Thumbnail(id)` returns a served JPEG (ffmpeg one frame at
10%, cached in temp). Library clips use their existing URLs.

### Preview compositor (TS)

`frontend/src/trim/compositor/`: a WebGL2 canvas sized to the canvas
aspect. A clock (`usePlayback`) runs output time from
`performance.now()` while playing. Each frame, `clipsAt(t)` gives the
visible clips; a pool of hidden `<video>` elements (one per active video
clip, reused) is kept seeked to each clip's source time
(`in + (t - start) * speed`, `playbackRate = speed`, re-seek past 80ms
drift); images are `<img>` textures. Layers draw bottom→top with their
transform and opacity through the effects shader. Audio: a WebAudio graph,
one `MediaElementSource` per active video/audio element →
gain (volume × fades × track mute) → destination; separate-tracks clips
use v1's `useTrackPreview` approach. Text overlays render as the v1 DOM
layer over the canvas.

### Effects (TS shader + Go filters, one owner for parity)

`frontend/src/trim/compositor/effects.ts`: GLSL for chroma key (YUV
distance with similarity/blend, like ffmpeg `chromakey`), brightness /
contrast / saturation and looks as color matrices, and blur. Toru ships an
**LGPL** ffmpeg build, which has no `eq` or `boxblur` (both GPL), so every
export filter must be LGPL: color uses `colorchannelmixer` + `lutrgb`,
blur uses `avgblur`, and the rest of the graph sticks to `overlay`,
`scale`, `format`, `colorchannelmixer`, `adelay`, `amix`, `atempo`,
`afade`, `volume`. `internal/vid/effects.go` holds `ClipJSON` (the Go
mirror of Clip, ms times; other files reference it rather than redefine
it), `ClipVideoFilter(c ClipJSON, srcH int) string` (the matching chain,
alpha kept) and `ClipAudioFilter(c ClipJSON, segLen float64) string`
(`atempo`, chained for <0.5 / >2, then `volume`, then `afade`). A test renders the
same frame both ways and compares within a tolerance, as the text card did.

### Export (Go)

`internal/vid/project_args.go`: `BuildProjectArgs(p, info, encoder,
textFilter) []string`. Inputs: one per clip (`-ss in -t len -i path`;
images `-loop 1 -t len -i`). Canvas: `color=black:s=WxH:r=F:d=total`.
Per video clip: `setpts=(PTS-STARTPTS)/speed` + `ClipVideoFilter` + scale to
its transform size + `format=yuva420p` (alpha for chroma/opacity:
`colorchannelmixer=aa=opacity`), then `overlay=x:y:enable='between(t,s,e)'`
with `setpts=PTS+start/TB` onto the running canvas, bottom track first.
Audio: each audible clip `atrim/asetpts` + `ClipAudioFilter` +
`adelay=start` → `amix=normalize=0` over all. Then crop, text, preset
scale and codecs as v1. Reuses `EditFrameSize` / `TextFilter` / preset
code.

### Timeline (TS)

`frontend/src/trim/MultiTimeline.tsx`: track headers (name, mute, hide,
lock, delete) on the left; rows of clips (video tracks, top track first,
audio below); ruler, playhead, zoom (v1 behaviour). Drag clips within and
between tracks of the same kind, edge-trim, split, delete, drop from the
media bin (HTML5 drag with `application/x-toru-media` = media id). Snaps to
the playhead and clip edges. Keys as v1.

### Inspector (TS)

`frontend/src/trim/Inspector.tsx`: right sidebar for the selected clip:
Transform (x, y, scale, opacity), Speed, Audio (volume, mute, fades,
v1 track levels), Green screen (on/off, eyedropper, similarity, blend),
Filters (sliders, look presets, blur). Every change goes through `apply`
with a key per slider drag. `PlayerHandles.tsx`: move/resize handles over
the canvas for the selected clip.

## Work split (cards under #1)

| Card | Owns | Needs |
| --- | --- | --- |
| A. Project model + history | `frontend/src/trim/project.ts`, `frontend/scripts/project-check.ts` | — |
| B. Media bin + import | `internal/media/**`, `main.go` (register service + middleware, lease), `windows.go` (file drop on the edit window, lease), `frontend/src/trim/MediaBin.tsx` | — |
| C. Multi-track timeline | `frontend/src/trim/MultiTimeline.tsx` | A's types (spec) |
| D. Compositor preview + audio | `frontend/src/trim/compositor/{usePlayback,Compositor,audio}.ts(x)` | A's types; E's shader API |
| E. Effects (preview + export parity) | `frontend/src/trim/compositor/effects.ts`, `internal/vid/effects.go` + tests | — |
| F. Multi-track export | `internal/vid/project.go`, `internal/vid/project_args.go` + tests, `ExportProject` in `edit_service.go` (lease), bindings | E's `ClipVideoFilter`/`ClipAudioFilter` signatures |
| G. Inspector + player handles | `frontend/src/trim/Inspector.tsx`, `frontend/src/trim/PlayerHandles.tsx` | A's types |
| H. Window integration + projects + real-build check | `frontend/src/routes/EditVideo.tsx`, project autosave (`internal/vid/projects.go`), Library Projects tab, screenshots on #1 | all |

A to G can start at once against the types and signatures in this spec.
H integrates. Lease shared files (`main.go`, `windows.go`,
`edit_service.go`, `frontend/src/lib/api.ts`, `frontend/bindings`) only
while editing them; whichever Go card merges second regenerates bindings.

## Done for v2

- Drag an MP3 and a PNG from Explorer onto the window: both appear in the
  bin and can be placed on tracks.
- A gameplay clip on the bottom track, a webcam clip on a higher track at 30% size in
  a corner with its green background keyed out, a logo PNG on a third track,
  music on an audio track faded in and out: the preview and the export
  match.
- A clip at 0.5x and one at 2x play at those speeds with pitch kept.
- Black & white plus brightness on one clip only.
- Every change undoes and redoes; closing and reopening the project keeps
  the edit.
- `go vet`, `go test ./internal/...`, golangci-lint v2.12.2 and
  `bun run build` pass; screenshots from a built toru.exe on #1.
