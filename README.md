# Toru (撮る)

A macOS-style screenshot & screen-recording tool for **Windows 11**. Press a
hotkey → a dim/crop overlay appears → pick **screenshot** or **video**, adjust
the crop, and commit. Screenshots open in an annotation editor (shapes, pen,
emoji, paste-image-as-a-layer, copy/save); recordings open in a minimal trim
editor. Dark, frosted-glass, sharp-edged.

> 撮る (*toru*) is the Japanese verb "to take a photo / shoot video" — it names
> both halves of the app.

**Status: Phase-0 skeleton.** Capture is stubbed (returns checked-in sample
media) so both editors are fully runnable today. The real overlay, capture, and
clipboard paths are TODOs marked in the code.

## Stack

| Layer | Choice |
| --- | --- |
| Shell | **Wails v3** (`v3.0.0-alpha.98`) + Go |
| Frontend | **React 19** + TypeScript + **shadcn/ui** + Tailwind v4 |
| Editor canvas | react-konva (Konva) |
| Stills | pure-Go DXGI (`kbinani/screenshot`) *(planned)* |
| Video | bundled FFmpeg (`ddagrab` → `gdigrab`) *(planned)* |
| Package manager | **bun** |

Full design doc: [`docs/PLAN.md`](docs/PLAN.md). How the two developers split the
work: [`CONTRIBUTING.md`](CONTRIBUTING.md).

## Prerequisites

- [Go](https://go.dev/dl/) (see `go.mod` for the version)
- [Wails v3 CLI](https://v3.wails.io/): `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha.98`
- [Bun](https://bun.sh) + Node
- WebView2 Runtime (ships with Windows 11)
- [FFmpeg](https://ffmpeg.org) on PATH (video dev/tests) — or set `TORU_FFMPEG`
- For installer builds (later): NSIS (`choco install nsis`)

## Quickstart

```sh
# from the repo root
cd frontend && bun install && cd ..
wails3 dev            # hot-reloads Go + frontend; opens the dev hub window
```

The **dev hub** window has buttons to open each surface (overlay, screenshot
editor, trim editor). Each opens its own route so you can work on your half in
isolation against the bundled `sample.png` / `sample.mp4`.

Build a binary:

```sh
cd frontend && bun run build && cd ..
go build -o build/bin/toru.exe .      # or: wails3 build
```

## Project layout

```
internal/
  capture/   ★ SHARED seam — contract.go (frozen), Capture(), ffmpeg args (golden-tested)
  overlay/   ★ SHARED — dim/crop overlay + the single ListScreens() source of truth
  export/    ★ SHARED — copy-to-clipboard (image bitmap / video CF_HDROP) + save-as
  dpi/ hotkey/ thumbnail/ tray/   ★ SHARED plumbing
  shot/      ◆ DEVELOPER 1 — screenshot editor helpers
  vid/       ◆ DEVELOPER 2 — video record + trim
frontend/src/
  routes/    Overlay.tsx (shared) · Editor.tsx (Dev 1) · Trim.tsx (Dev 2) · Hub.tsx
  components/ui/  shadcn primitives + the dark/frosted/zero-radius theme
  lib/contract.ts TS mirror of contract.go
main.go      app wiring (services, windows, hotkeys, tray)
testdata/    sample.png + sample.mp4 (the stubs both editors develop against)
```

## Releasing & auto-update

Cut a release by pushing a semver tag — that's the whole flow:

```sh
git tag v0.1.0
git push origin v0.1.0
```

`.github/workflows/release.yml` (on `v*.*.*`) then, on `windows-latest`:
stamps the version into the binary (`-X main.version=`) and the Win32 resource,
builds the app + a **per-user NSIS installer** (`wails3 task windows:build` +
`makensis`), and publishes a GitHub Release with three assets:

| Asset | |
| --- | --- |
| `toru-<version>-windows-amd64-installer.exe` | the installer |
| `toru-<version>-windows-amd64.zip` | portable (just `toru.exe`) |
| `SHA256SUMS` | checksums for both |

**In-app updater** (`internal/update` + the Hub's *Check for Updates*): on
launch (and on demand) the app queries the latest GitHub Release, compares it to
its own `version`, and — if newer — offers a one-click **Install & Restart** that
downloads the installer, verifies it against `SHA256SUMS`, runs it silently, and
quits so NSIS can replace the running exe. Dev builds (`version == "dev"`) never
prompt. You must cut a real `v*` release before the updater has anything to find.

## The seam, in one sentence

The overlay emits one `CaptureRequest`; Go's single `Capture()` turns it into a
PNG (DXGI) or MP4 (FFmpeg) path; the `capture:done` event routes **by mode** to
either the Konva editor (Dev 1) or the trim editor (Dev 2) — neither imports the
other, only `internal/capture/contract.go`.
