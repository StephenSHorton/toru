# Editor real-build check

`frontend/scripts/realbuild-check.ts` checks the video editor in a real Toru
build: it drives the editor through `toru ctl`, compares preview frames
(`editor.frame`) with the exported file at the same times, and screenshots
every inspector tab. Run it before a release that touches the editor.

What it covers:

| Phase  | What it builds | What it checks |
|--------|----------------|----------------|
| `time` | bullet ramp with frame blending, a reversed clip, a 1.5 s freeze frame | preview vs export frames, export length, silent freeze, tone at the ramp's 1x start |
| `fx`   | a saved v3 project: look, keyed gaussian blur, zoom punch-in, keyed opacity and x; exposure, keyed RGB split, flash, seeded shake | preview vs export frames, export length, effects survive `project.open` |
| `fx7`  | the same kind of stack through C7's commands: a red↔blue `.cube` imported (`preset.importLut`) as a lut effect, `editor.addEffect` + `editor.setKeyframe` (blur sigma, opacity), clip A's stack saved (`editor.savePreset`), written to `.torufx` (`preset.export`), imported back (`preset.import`) and applied to clip B, plus a built-in look | preview vs export frames, the LUT shows in the export, the `.torufx` round trip keeps the LUT |
| `ui`   | (the open edit) | every inspector tab selects; screenshots |

Per-effect pixel parity is the job of `bun scripts/effects-parity.ts`, which
needs no app. This check covers the wiring between the parts: the project the
app saves and loads, the preview compositor, the export graph and the
inspector.

The test source is 6 s of coloured seconds with a white box that moves 2.5 px
per frame at the compare size, plus a 440 Hz tone. A frame passes when the
mean RGB difference is ≤ 8, the 99th percentile ≤ 48 and the box is ≤ 4 px
away (under 2 frames). `--dry` checks that a 2-frame timing error fails.

## Dry run (no app)

```sh
cd frontend
bunx vite --host 127.0.0.1 --port 5191     # in another terminal
bun scripts/realbuild-check.ts --dry --url http://127.0.0.1:5191
```

This makes the source, builds and validates the fx project, calibrates the
frame diff, puts the `.cube` through ffmpeg's lut3d, and runs the `ui` phase in headless Edge on `?view=edit` (vite's
sample clip). It never touches the installed Toru.

## Real run

The real run closes the installed Toru, which stops the replay buffer, and
the reverse proxy and blend exports load the CPU and GPU. Ask Blake first,
and only run it when he isn't gaming.

1. Build a test exe from the branch to check. For the `ui` phase, first add
   `"--remote-debugging-port=9333"` to `AdditionalBrowserArgs` in `main.go`.
   **Never commit that line.**
   ```sh
   cd frontend && bun run build && cd ..
   go build -o "$SCRATCH/toru-check.exe" .
   git checkout main.go
   ```
2. Close the installed Toru: tray icon → Quit, or
   `Stop-Process -Name toru`. Then check `Get-Process toru | select Path`.
   Autostart can bring the installed Toru back, and SingleInstance then hands
   the test exe's launch to it.
3. Start the test exe, and check that it's the only `toru` process running.
4. Run the check:
   ```sh
   cd frontend
   bun scripts/realbuild-check.ts --exe "$SCRATCH/toru-check.exe" --out "$SCRATCH/realbuild"
   ```
   `--phases time,fx,ui` picks phases. `--keep` leaves the test media,
   projects and reverse proxies in place.
5. Quit the test exe and restart the installed Toru:
   `& "$env:LOCALAPPDATA\Programs\Toru\toru.exe"`.
6. Post `log.txt` and the `ui/*.png` screenshots from `--out`.

The script cleans up after itself unless `--keep` is given. It removes:
- the test media folder `Videos/Toru Replays/toru-check-<stamp>`
- the projects this run saved in `%AppData%/toru/projects` (its own
  `proj-toru-check-<stamp>` and autosaves of the test source)
- the filters and the LUT that `fx7` added (`preset.delete`, `preset.deleteLut`)
- reverse proxies created during the run in `%LOCALAPPDATA%/toru/cache/reverse`

Exports and frames stay in `--out`.

Notes:
- `toru ctl` saves an inline image (`editor.frame`) to a temp file and
  returns `{image: {mime, path}}`, not base64.
- The `fx` phase writes a project file and opens it with `project.open`, so it
  checks the same stack whatever the commands do. `fx7` builds its stack
  through C7's commands. In the project file, keyframe times are seconds from
  the clip's start on the timeline; `editor.setKeyframe` takes timeline
  seconds.
- The preview only serves Library files, which is why the source goes under
  `Videos/Toru Replays`.
