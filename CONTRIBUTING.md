# Contributing to Toru — the two-developer split

Toru is built by two developers in parallel behind one small, frozen contract.
This document is the rule of the road so you rarely touch the same files.

## Who owns what

| Area | Owner | Files |
| --- | --- | --- |
| **Shared core** (built first, together) | both | `internal/capture` `internal/overlay` `internal/export` `internal/dpi` `internal/hotkey` `internal/thumbnail` `internal/tray`, `main.go`, `frontend/src/routes/Overlay.tsx`, `frontend/src/components/ui`, `frontend/src/lib/contract.ts` |
| **Screenshot + annotation editor** | **Developer 1** | `internal/shot`, `frontend/src/routes/Editor.tsx`, `frontend/src/editor/*` |
| **Video record + trim editor** | **Developer 2** *(also leads `internal/overlay` + screen enumeration)* | `internal/vid`, `frontend/src/routes/Trim.tsx`, `frontend/src/trim/*`, staging `third_party/ffmpeg` |

After the shared week-1 work, the **only** file both developers share is
`internal/capture/contract.go`.

## The contract rule

`internal/capture/contract.go` (and its TS mirror `frontend/src/lib/contract.ts`)
is the entire Dev1 ↔ Dev2 interface: `CaptureRequest`, `CaptureResult`,
`TrimRequest`, `ScreenInfo`, plus the `capture:done` event that routes **by
`mode`**.

- Changing `contract.go` requires **both developers to sign off** on the PR.
- It must always keep `go build ./internal/capture` green (the contract gate; run it locally).
- After changing Go-side bindings, regenerate: `wails3 generate bindings -ts -clean`,
  and commit `frontend/bindings`.

## Work in isolation from day one (stub the other half)

You do **not** need the other developer's code, the real overlay, or the real
capture pipeline to build and test your half:

- **Developer 1:** open the editor on the bundled sample —
  `wails3 dev`, then in the dev hub click *Open screenshot editor*. It loads
  `frontend/public/sample.png`. Build the whole Konva editor against that.
- **Developer 2:** open the trim editor on the bundled sample — click *Open trim
  editor*. It loads `frontend/public/sample.mp4`. Build the whole recording +
  trim flow against that; `internal/capture.StubCapturer` returns a sample MP4.

`Capture()` currently returns checked-in samples; swap `StubCapturer` for the
real `Capturer` in `main.go` once the DXGI/FFmpeg paths land — the contract
does not move, so neither editor is disturbed.

## Git workflow

This repo may be edited by more than one person (and AI sessions) at once. **Do
not commit directly to `main`.** Branch, push, open a PR:

```sh
git switch -c dev1/editor-shapes        # or dev2/recording-lifecycle
# ... work, commit ...
git push -u origin HEAD                  # open a PR
```

For parallel local work, prefer a git worktree per task so working trees don't
collide.

## Checks (run locally - no CI)

There is no CI on PRs or pushes (to save GitHub Actions minutes). Before you
open or merge a PR, or push to `main`, run these locally on Windows; all must pass:

```sh
go build ./internal/capture     # contract gate
go build ./cmd/...
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /dev/null ./cmd/toru-relay   # bash
go vet ./internal/... ./cmd/...
go test ./internal/... ./cmd/...
golangci-lint run ./internal/... ./cmd/...   # v2.12.2
cd frontend && bun install --frozen-lockfile && bun run build && cd ..   # tsc + vite
go build -o build/bin/toru.exe .
```

## Agent control: every feature registers its actions

Toru can be driven by AI agents and scripts (`docs/agent-control.md`). A feature
isn't done until its actions are in the agent registry: Go services register
`agentctl.Command`s on the registry built in `main.go`, and windows register
theirs with `registerAgentCommand` from `frontend/src/lib/agent.ts`. Window
commands change state through the window's undo history; commands that delete
or overwrite things set `Destructive`/`destructive` (they then need
`"confirm": true`).

## Commit messages

Conventional-commit style (`feat:`, `fix:`, `chore:`, `docs:`). Keep the shared
core changes in their own commits, separate from your half's work.
