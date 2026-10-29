# Video editor v1

Status: draft, decisions on Irori card #1 ("Toru: add a video editor").
Where a decision is still open, this spec builds to the **recommended** answer
and marks it _(pending decision)_.

## Goal

Add a small video editor for recordings and clips, in its own "Edit video"
window: cut parts out, join clips, add simple text, set per-track audio
levels, crop, and export with a preset. Source files are never changed. The
trim window (`frontend/src/routes/Trim.tsx`) stays as it is for quick trims
and gains an "Edit" button. Ship it in one release.

## What exists today

| Piece | Where | What it does |
| --- | --- | --- |
| Trim window | `frontend/src/routes/Trim.tsx` | One video, two handles (in/out), playhead, crop, Copy / Save As / Trim / Discord / Share link |
| Crop overlay | `frontend/src/trim/CropBox.tsx` | Crop rect in video pixels over the player |
| Trim backend | `internal/vid/service.go` `VideoService.Trim` | Builds args with `capture.BuildTrimArgs` and runs ffmpeg |
| Trim args | `internal/capture/args.go` `BuildTrimArgs` | One `[StartMs, EndMs]` range, optional crop, stream copy or re-encode |
| Trim contract | `internal/capture/contract.go` `TrimRequest` | Frozen contract (both developers sign off on changes) |
| Recording timeline | `frontend/src/routes/Timeline.tsx` | Long recordings as HLS; select a range, Create clip, opens the clip in the trim window |
| Library | `frontend/src/routes/Settings.tsx` `LibraryPage` | Tabs: all, shots, recordings, clips, sessions; clips open with `ReplayService.OpenClip` |
| Audio tracks | `internal/capture/replay.go` | With "separate tracks" on, clips carry track 0 = mix, then one titled track per source (game, Discord, mic) |

Known gap: `BuildTrimArgs` has no `-map`, so a trim keeps only the first audio
track and drops the separate source tracks. The new export path maps tracks
explicitly.

## v1 scope

In:

1. **Multi-cut timeline.** Split at the playhead (`S`), select a segment,
   delete it (`Delete`), drag segment edges, drag segments to reorder. The
   output is the kept segments in timeline order. Undo / redo (`Ctrl+Z` /
   `Ctrl+Shift+Z`). Zoom the timeline.
2. **Join clips.** "Add clip" picks a clip or screen recording from the
   Library and appends it to the end of the timeline as a new segment.
   Sources with a different size or frame rate are scaled and padded to the
   first source's size and frame rate.
3. **Preview that skips cuts.** Playback jumps over deleted parts and moves
   from source to source, so what you see is what exports.
4. **Text overlays, basic.** Add text, type it, drag it on the player, set
   its start and end on the timeline. One style: white Segoe UI Semibold with
   a dark outline, three sizes (S/M/L). Several overlays allowed.
5. **Per-track audio.** For each source with separate tracks, one row per
   source track with a volume slider (0 to 200%) and mute. Export re-mixes
   them at those levels. Sources with one track get a single volume row.
   _(pending decision: preview)_
6. **Crop**, reusing `frontend/src/trim/CropBox.tsx`, applies to the output
   frame.
7. **Export presets:** Original quality (first source's size and frame
   rate), 1080p MP4, Discord-sized (fits 10MB, existing two-pass VP9).
   Progress bar and cancel. _(pending decision)_
8. **Output:** a new file beside the first source named
   `<name>-edited.mp4`; sources are never changed. Clips stay in the clips
   folder; screen recordings are added to the Library. _(pending decision)_

Out of v1 (later): transitions, speed changes, music or extra audio files,
text styles beyond the one above, GIF export (issue #12), still frame
(issue #11), saved project files.

## Design

### Contract

No changes to `internal/capture/contract.go`. The editor's request type lives
in `internal/vid` (Developer 2 territory). `VideoService.Trim` and
`TrimRequest` stay as they are for the trim window.

### Backend (`internal/vid`)

```go
// edit.go: request types (the frontend gets them through bindings)
type Segment struct {
	Source  int `json:"source"`  // index into EditRequest.Sources
	StartMs int `json:"startMs"` // source time
	EndMs   int `json:"endMs"`
}

type TrackLevel struct {
	Source int  `json:"source"` // index into Sources
	Index  int  `json:"index"`  // audio stream index in that source (0 = first)
	Volume int  `json:"volume"` // percent, 0..200
	Muted  bool `json:"muted"`
}

type TextOverlay struct {
	Text    string  `json:"text"`
	X       float64 `json:"x"`       // centre of the text, 0..1 of output width (after crop)
	Y       float64 `json:"y"`       // centre, 0..1 of output height
	Size    string  `json:"size"`    // "s" | "m" | "l" = 4% | 6% | 9% of output height
	StartMs int     `json:"startMs"` // OUTPUT time
	EndMs   int     `json:"endMs"`
}

type EditRequest struct {
	Sources  []string      `json:"sources"`           // absolute paths; Sources[0] sets size/fps
	Segments []Segment     `json:"segments"`          // output order, at least one
	Crop     *capture.Rect `json:"crop,omitempty"`    // pixels of the Sources[0]-sized frame
	Tracks   []TrackLevel  `json:"tracks,omitempty"`  // a source with none listed keeps its mix track at 100%
	Texts    []TextOverlay `json:"texts,omitempty"`
	Preset   string        `json:"preset"`            // "original" | "1080p" | "discord"
	OutPath  string        `json:"outPath,omitempty"` // "" = <Sources[0] name>-edited.mp4
}

type MediaInfo struct {
	DurationMs int          `json:"durationMs"`
	Width      int          `json:"width"`
	Height     int          `json:"height"`
	FPS        float64      `json:"fps"`
	HasAudio   bool         `json:"hasAudio"`
	Tracks     []AudioTrack `json:"tracks"`
}

type AudioTrack struct {
	Index int    `json:"index"`
	Title string `json:"title"` // stream title/handler_name; "" if none
	IsMix bool   `json:"isMix"` // the default mixed track when separate tracks exist
}

// edit_args.go: pure, unit-tested, no ffmpeg calls
func BuildEditArgs(req EditRequest, info []MediaInfo, encoder string, textFilter string) []string

// edit_text.go: pure apart from writing the text files, unit-tested
func TextFilter(texts []TextOverlay, w, h int, fontFile, tmpDir string) (filter string, files []string, err error)

// service.go additions (bound to JS)
func (s *VideoService) ProbeMedia(path string) (MediaInfo, error)
func (s *VideoService) Export(req EditRequest) (string, error) // emits "edit:progress" {pct}
func (s *VideoService) CancelExport() error
```

Filter graph, per segment `i` from source `k`:
`[k:v]trim=start=..:end=..,setpts=PTS-STARTPTS,scale=W:H:force_original_aspect_ratio=decrease,pad=W:H:(ow-iw)/2:(oh-ih)/2,fps=F,format=yuv420p[v_i]`.
Audio per segment: the chosen source tracks, each `atrim` + `asetpts` +
`volume` + `aformat=fltp:48000:stereo`, summed with `amix normalize=0` (as
`replayAudioArgs` does), or the mix track alone; a source with no audio gets
`anullsrc` of the segment's length. Then `concat=n=N:v=1:a=1`, then crop,
then the text filter (`drawtext` with `textfile=` per overlay so nothing
needs escaping, font `C:\Windows\Fonts\seguisb.ttf`, `borderw`,
`enable='between(t,a,b)'`), then the preset's scale (`1080p` =
`scale=-2:1080`, only when the frame is taller). Encoder: the trim encoder
picker in `internal/capture` (export it; `args.go` is shared core, not the
contract). The Discord preset runs `ExportForDiscord` on the result.
Progress: `capture.RunFFmpegProgress` with the summed segment length.
Cancel: cancel the export's context.

### Frontend (`frontend/src/trim`)

```ts
// edl.ts: the edit decision list, pure (no React)
export type Source = { path: string; url: string; duration: number; width: number; height: number };
export type Seg = { id: string; source: number; start: number; end: number }; // seconds, source time
export type Text = { id: string; text: string; x: number; y: number; size: "s" | "m" | "l"; start: number; end: number }; // output time
export type Edl = { sources: Source[]; segs: Seg[]; texts: Text[] }; // segs in output order
export function initEdl(first: Source): Edl;
export function addSource(edl: Edl, src: Source): Edl;          // appends a whole-clip segment
export function split(edl: Edl, outT: number): Edl;            // at output time
export function remove(edl: Edl, segId: string): Edl;
export function move(edl: Edl, segId: string, toIndex: number): Edl;
export function setEdge(edl: Edl, segId: string, edge: "start" | "end", srcT: number): Edl;
export function outputDuration(edl: Edl): number;
export function locate(edl: Edl, outT: number): { seg: Seg; srcT: number } | null;
export function toOutput(edl: Edl, segId: string, srcT: number): number;
export function upsertText(edl: Edl, t: Text): Edl;
export function removeText(edl: Edl, id: string): Edl;
export function toRequest(edl: Edl, extra: { crop; tracks; preset }): EditRequest;
export function useEdlHistory(init: Edl): { edl; apply(fn); undo(); redo(); canUndo; canRedo };
```

- `usePreviewPlayback.ts`: plays the EDL in output time on one `<video>`.
  At a segment end it seeks to the next segment, swapping `src` when the
  source changes. Exposes `outTime`, `playing`, `play`, `pause`,
  `seek(outT)`.
- `EditTimeline.tsx`: segments (coloured by source), playhead, a text row
  above, split / delete / reorder / edge drag, zoom. Props in, callbacks
  out; owns no video.
- `TextOverlays.tsx`: the draggable text boxes over the player (same size
  and outline as the export) and the text edit popover.
- `AudioTracks.tsx`: rows from `ProbeMedia`, volume + mute, emits
  `TrackLevel[]`.
- `AddClipDialog.tsx`: lists Library clips and recordings, returns a path.
- `ExportDialog.tsx`: preset picker, progress, cancel, then Copy / Show in
  folder.
- `frontend/src/routes/EditVideo.tsx` (`?view=edit`): the window. Player,
  CropBox, TextOverlays, timeline, audio rows, Add clip, Export. Same
  `?vid=` / `?path=` query params as the trim window.

### Window and entry points

- New `WindowsService.OpenEditVideo(videoPath)` in `windows.go`, modelled on
  `OpenTrim`, title "Toru — Edit video"; route `edit` in `App.tsx`.
- `WindowsService.ServedURL(path)` returns the URL the player can load for
  any Library file (wraps `servedFileURL`), for clips added with Add clip.
- "Edit" buttons open it from: the trim window's toolbar, Library clip and
  recording cards (`Settings.tsx`), and the recording Timeline after Create
  clip (`Timeline.tsx`, beside opening the clip).
- The trim window keeps working exactly as today.

## Work split

Tracked as cards on the Irori Board under #1. File ownership:

| Card | Owns |
| --- | --- |
| Export args builder | `internal/vid/edit.go`, `internal/vid/edit_args.go` + tests, the encoder picker export in `internal/capture/args.go` |
| Export service | new methods in `internal/vid/service.go`, `internal/vid/edit_service*.go`, regenerated `frontend/bindings` |
| Text overlays | `internal/vid/edit_text.go` + tests, `frontend/src/trim/TextOverlays.tsx` |
| EDL model + preview playback | `frontend/src/trim/edl.ts`, `frontend/src/trim/usePreviewPlayback.ts` |
| Timeline UI | `frontend/src/trim/EditTimeline.tsx` |
| Window shell + Edit buttons | `windows.go` (OpenEditVideo, ServedURL), `frontend/src/App.tsx`, `frontend/src/routes/EditVideo.tsx`, Edit buttons in `Trim.tsx`, `Settings.tsx`, `Timeline.tsx` |
| Add clip from Library | `frontend/src/trim/AddClipDialog.tsx` |
| Audio tracks UI | `frontend/src/trim/AudioTracks.tsx` |
| Export dialog + final wiring | `frontend/src/trim/ExportDialog.tsx`, `frontend/src/routes/EditVideo.tsx` (after the shell merges) |

Go-side cards regenerate bindings with
`wails3 generate bindings -clean=true -ts`; whichever merges second rebases
on main and regenerates rather than hand-merging `frontend/bindings`.
`frontend/src/lib/api.ts` is shared: lease it before editing. Anyone touching
a file outside their row takes an Irori lease on it first. Until the Go types
land, frontend cards code against the shapes above.

## Done for v1

- Cut three parts out of a 2-minute clip, export Original: plays with no
  gaps, length equals the kept parts within one frame.
- Join a 1440p/144fps clip and a 1080p/60fps recording: exports at the first
  one's size and rate, the second letterboxed, audio in sync.
- Two text overlays at different times show where and when they were placed.
- A clip with separate tracks: mute the mic, export, the mic is gone and the
  game audio is unchanged.
- Crop + 1080p preset + cuts together export correctly.
- Source files are byte-identical after any export.
- `go vet`, `go test ./internal/...`, golangci-lint v2.12.2 and
  `bun run build` pass.
