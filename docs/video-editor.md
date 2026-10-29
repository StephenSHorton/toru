# Video editor v1

Status: draft, decisions open on Irori card #1 ("Toru: add a video editor").
Where a decision is still open, this spec builds to the **recommended** answer
and marks it _(pending decision)_.

## Goal

Add a small video editor for recordings and clips, in its own "Edit video"
window: cut out several parts of one video, set per-track audio levels, crop,
and export with a preset. Keep the original file untouched. The trim window
(`frontend/src/routes/Trim.tsx`) stays as it is for quick trims and gains an
"Edit" button. Ship it in one release.

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
track and drops the separate source tracks. The new export path below maps
tracks explicitly.

## v1 scope

In:

1. **Multi-cut timeline.** Split at the playhead (`S`), select a segment,
   delete it (`Delete`), drag segment edges. The output is the kept segments
   in order. Undo / redo (`Ctrl+Z` / `Ctrl+Shift+Z`). Zoom the timeline.
2. **Preview that skips cuts.** Playback jumps over deleted parts so what
   you see is what exports.
3. **Per-track audio.** If the file has separate source tracks, show one row
   per source with a volume slider (0 to 200%) and mute. Export re-mixes the
   sources at those levels. Files with one track get a single volume row.
   _(pending decision: preview)_
4. **Crop**, reusing `frontend/src/trim/CropBox.tsx`, applies to the whole
   export.
5. **Export presets:** Original quality (same size and frame rate), 1080p
   MP4, Discord-sized (fits 10MB, existing two-pass VP9). Progress bar and
   cancel.
6. **Output:** a new file beside the original named `<name>-edited.mp4`; the
   original is never changed. Clips stay in the clips folder; screen
   recordings are added to the Library. _(pending decision)_

Out of v1 (later): joining several clips, text overlays, transitions, speed
changes, music or extra audio files, GIF export (issue #12), still frame
(issue #11), saved project files.

## Design

### Contract

No changes to `internal/capture/contract.go`. The editor's request type lives
in `internal/vid` (Developer 2 territory). `VideoService.Trim` and
`TrimRequest` stay as they are for anything that still uses them.

### Backend (`internal/vid`)

```go
// edit.go
type Segment struct {
	StartMs int `json:"startMs"`
	EndMs   int `json:"endMs"`
}

type TrackLevel struct {
	Index  int  `json:"index"`  // audio stream index in the source (0 = first)
	Volume int  `json:"volume"` // percent, 0..200
	Muted  bool `json:"muted"`
}

type EditRequest struct {
	VideoPath string         `json:"videoPath"`
	Segments  []Segment      `json:"segments"`         // kept parts, in output order, non-overlapping
	Crop      *capture.Rect  `json:"crop,omitempty"`   // video pixels, like TrimRequest.Crop
	Tracks    []TrackLevel   `json:"tracks,omitempty"` // empty = keep audio as recorded
	Preset    string         `json:"preset"`           // "original" | "1080p" | "discord"
	OutPath   string         `json:"outPath,omitempty"` // "" = <name>-edited.<ext>
}

type AudioTrack struct {
	Index int    `json:"index"`
	Title string `json:"title"` // from the stream's title/handler_name; "" if none
	IsMix bool   `json:"isMix"` // the default mixed track when separate tracks exist
}

func BuildEditArgs(req EditRequest, info SourceInfo) []string // pure, unit-tested
func (s *VideoService) ProbeAudioTracks(path string) ([]AudioTrack, error)
func (s *VideoService) Export(req EditRequest) (string, error) // emits "edit:progress" {pct}
func (s *VideoService) CancelExport() error
```

Args: one `trim`/`atrim` + `setpts`/`asetpts` pair per segment into `concat`,
then crop and the preset's scale. Audio: when `Tracks` is set, mix the source
tracks (not the mix track) with `volume` and `amix normalize=0`, as
`replayAudioArgs` does; otherwise keep the mix track. A single segment with
no crop, no track changes and preset "original" may stream copy like
`BuildTrimArgs`. Encoder choice reuses the trim encoder picker in
`internal/capture` (export it if needed; `args.go` is shared core, not the
contract). Discord preset reuses `ExportForDiscord` on the edited output.
Progress uses `capture.RunFFmpegProgress` with the summed segment length.

### Frontend (`frontend/src/trim`)

```ts
// edl.ts: the edit decision list, pure (no React)
export type Seg = { id: string; start: number; end: number }; // seconds, source time
export type Edl = { duration: number; segs: Seg[] };          // kept segments, sorted
export function initEdl(duration: number): Edl;
export function split(edl: Edl, at: number): Edl;
export function remove(edl: Edl, segId: string): Edl;
export function setEdge(edl: Edl, segId: string, edge: "start" | "end", t: number): Edl;
export function outputDuration(edl: Edl): number;
export function sourceToOutput(edl: Edl, t: number): number | null; // null = inside a cut
export function nextKept(edl: Edl, t: number): number | null;       // where playback resumes
// plus an undo/redo history wrapper: useEdlHistory()
```

- `usePreviewPlayback.ts`: drives the `<video>`; when the playhead enters a
  cut it seeks to `nextKept`; stops at the end of the last segment.
- `EditTimeline.tsx`: segments, cuts, playhead, split/delete, edge drag,
  zoom. Takes `edl`, `cur`, callbacks; owns no video.
- `AudioTracks.tsx`: rows from `ProbeAudioTracks`, volume + mute, emits
  `TrackLevel[]`.
- `ExportDialog.tsx`: preset picker, progress, cancel, then Copy / Show in
  folder.
- `frontend/src/routes/EditVideo.tsx` (`?view=edit`): the window. Player,
  CropBox, timeline, audio rows, Export button. Same `?vid=` / `?path=` query
  params as the trim window.

### Window and entry points

- New `WindowsService.OpenEditVideo(videoPath)` in `windows.go`, modelled on
  `OpenTrim`, title "Toru — Edit video"; route `edit` in `App.tsx`.
- "Edit" buttons open it from: the trim window's toolbar, Library clip and
  recording cards (`Settings.tsx`), and the recording Timeline after Create
  clip (`Timeline.tsx`, as a second action beside opening the clip).
- The trim window keeps working exactly as today.

## Work split

Tracked as cards on the Irori Board under #1. File ownership:

| Card | Owns |
| --- | --- |
| Backend export engine | `internal/vid/edit*.go`, new methods in `internal/vid/service.go`, the encoder helper export in `internal/capture/args.go`, regenerated `frontend/bindings` |
| EDL model + preview playback | `frontend/src/trim/edl.ts`, `frontend/src/trim/usePreviewPlayback.ts` |
| Timeline UI | `frontend/src/trim/EditTimeline.tsx` |
| Audio tracks UI | `frontend/src/trim/AudioTracks.tsx` |
| Edit video window shell | `windows.go` (OpenEditVideo only), `frontend/src/App.tsx`, `frontend/src/routes/EditVideo.tsx` |
| Export dialog + wiring | `frontend/src/trim/ExportDialog.tsx`, `frontend/src/routes/EditVideo.tsx` (after the shell merges) |
| Edit buttons | `frontend/src/routes/Trim.tsx`, `frontend/src/routes/Settings.tsx` (Library cards), `frontend/src/routes/Timeline.tsx` |

Go-side cards (backend, window shell) regenerate bindings with
`wails3 generate bindings -clean=true -ts`; whichever merges second rebases on
main and regenerates rather than hand-merging `frontend/bindings`.
`frontend/src/lib/api.ts` is shared: lease it before editing. Anyone touching
a file outside their row takes an Irori lease on it first.

## Done for v1

- Cut three parts out of a 2-minute clip, export Original: plays with no
  gaps, length equals the kept parts within one frame.
- A clip with separate tracks: mute the mic, export, the mic is gone and the
  game audio is unchanged.
- Crop + 1080p preset + cuts together export correctly.
- The original file is byte-identical after any export.
- `go vet`, `go test ./internal/...`, golangci-lint v2.12.2 and
  `bun run build` pass.
