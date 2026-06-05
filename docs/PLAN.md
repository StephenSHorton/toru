# Implementation Plan: MacShot — a macOS-style screenshot & screen-recording tool for Windows 11

## 1. Executive Summary

We are building **a native Windows 11 desktop app that clones macOS's Cmd+Shift+5 screenshot/recording experience**: a global hotkey opens a full-screen dim+crop overlay where the user picks SCREENSHOT or VIDEO, adjusts the crop, and commits — screenshots open in a Konva annotation editor (shapes, pen, emoji, paste-image-as-layer, copy/save), recordings open in a minimal trim editor (timeline + two handles).

**Stack (one line):** Wails **v3** (`v3.0.0-alpha.98`) + Go + **React 19** + TypeScript + shadcn/ui + Tailwind.

**Overlay + capture approach (one line):** the overlay is **one transparent frameless always-on-top Wails-v3 WebviewWindow per monitor** (React/CSS dim+crop), and capture flows through **a single shared Go arg/dispatch seam (`internal/capture`)** — **pure-Go DXGI (`kbinani/screenshot`) for stills** and **a staged LGPL FFmpeg binary for video** (`ddagrab` GPU path, `gdigrab` software fallback) — so there is exactly ONE native capture component both developers share, and FFmpeg is kept off the screenshot hot path.

**Work-split headline:** after a 1-week joint Phase 0 that lands a **compiling, CI-gated `CaptureResult` contract + one screen-enumeration source of truth + a stub `Capture()` that returns checked-in `sample.png`/`sample.mp4`** on **Day 1**, **Developer 1 owns screenshot+annotation (React/Konva consuming a PNG path)** and **Developer 2 owns video+trim (React consuming an MP4 path)**, touching zero shared native code and each fully runnable in isolation against a static PNG / MP4 from day one. The overlay transparency spike and the real FFmpeg core proceed **in parallel behind those stubs** and are **not on the devs' critical path**.

---

## 2. Stack & Key Decisions

Each decision below is committed. "WHY" is one line; the runner-up names what we deliberately did not pick.

| Decision | **CHOSEN** | WHY | Runner-up (rejected) |
|---|---|---|---|
| **Wails version** | **v3, pinned `v3.0.0-alpha.98`** | v2 is single-window; we *require* multi-window (N overlays + 2 editor windows) + runtime `SetIgnoreMouseEvents` click-through, both v3-only. | v2 — mature but single-window model can't host independent overlay + editor windows. |
| **React major** | **React 19 (pinned in `package.json` Day 0)** | **`react-konva` v19 is React-19-only** and drives Dev1's whole editor; pinning Day 0 stops Dev1/Dev2 diverging on React major. shadcn/Radix are React-19-clean in 2026. | React 18 — would force `react-konva` ≤18 (EOL track) across the whole shared frontend. |
| **Still capture** | **Pure-Go DXGI via `kbinani/screenshot`** (in-process, no exec), behind the `internal/capture` seam | Instant stills are a **core** Cmd+Shift+5 expectation; a cold `ffmpeg` exec per shot is tens-to-hundreds of ms and `gdigrab` mishandles some DWM/layered content. DXGI duplication is the correct still primitive. | `ffmpeg gdigrab -frames:v 1` — kept as the documented **fallback** for adapters where DXGI duplication is unavailable (e.g., some RDP/VM sessions). |
| **Video capture** | **One staged LGPL FFmpeg binary** via `os/exec`: `ddagrab`→`gdigrab` | `ddagrab` is GPU 4K60 with monitor-relative region crop; FFmpeg also encodes the MP4 the trim editor edits. ONE binary, ONE video arg-builder. | WMF via cgo — no trim/thumbnail ecosystem, brittle COM. Pure-Go encoder — no real-time H.264. |
| **Capture seam** | **One `internal/capture` package** exposing `Capture(req)`; dispatch on `Mode` to the DXGI still path or the FFmpeg video path | ONE entrypoint, ONE contract, ONE place coordinate-rebasing lives → cleanest two-dev seam. Still/video use different primitives but share the request, result, and `Rect` semantics. | A single mechanism for both — would put FFmpeg's exec latency on the screenshot hot path. |
| **Export / output (clipboard + Save-As)** | **One SHARED `internal/export` package** owning ALL clipboard-write and Save-As logic for both stills and video, behind `Export.CopyToClipboard`/`Export.SaveAs` bindings | Copy-to-clipboard and Save-As are required for **both** media types; centralizing them off the Dev1/Dev2 seam stops clipboard logic forking between the two editors and keeps the multi-format image write and the `CF_HDROP` video write in one place. | Per-dev clipboard/save code in `internal/shot` and `internal/vid` — would diverge and duplicate the fiddly Win32 clipboard transaction. |
| **Overlay implementation** | **Transparent frameless AlwaysOnTop Wails-v3 WebviewWindow, ONE per monitor**, dim/crop drawn in React/CSS | Keeps the shared overlay in the product owner's exact stack (React/shadcn); per-monitor windows each inherit correct per-monitor DPI for free. | Single virtual-desktop-spanning window — no clean Wails span API + mixed-DPI breaks it. *Native Win32 `WS_EX_LAYERED` window is the documented fallback, not the default.* |
| **React editor canvas** | **react-konva (Konva, MIT) v19** | Only candidate that natively delivers image base layer + shapes + pen + emoji + **paste-image-as-movable/resizable/rotatable/layerable** (`Konva.Image` + `Transformer`) + one-call PNG export. | Fabric.js — viable but imperative (no first-party React reconciler). tldraw (paid+watermark) and Excalidraw (hand-drawn, weak paste-as-object) rejected outright. |
| **Video codec & licensing** | **H.264 via HW encoders (`h264_nvenc`/`qsv`/`amf`) with VP9/WebM as the royalty-free default-safe fallback; OpenH264 obtained by runtime download FROM CISCO, never bundled** | Resolves the **AVC patent** question, not just copyright: bundling *any* H.264 encoder (incl. HW or a self-built OpenH264) makes **us** the AVC distributor owing Via-LA royalties. Cisco's binary is patent-clean **only when the end user downloads it from Cisco at runtime** (Cisco pays the pool). See §10 for the committed decision. | Bundling `libx264` (GPL **and** AVC-royalty) or shipping a self-built OpenH264 binary (incurs AVC royalties) — both rejected. |
| **FFmpeg delivery** | **Installer-staged (NSIS drops `ffmpeg.exe` beside the app)** with a **first-run download + SHA256-verify** fallback — **NOT `//go:embed`** | A full FFmpeg with `ddagrab`+HW encoders is ~80–120 MB; `//go:embed` holds it in memory as `[]byte` and bloats the Go build. Staging keeps the binary out of the Go image. | `//go:embed third_party/ffmpeg/ffmpeg.exe` — **rejected**; `ffmpeg_embed.go` does not exist (replaced by `ffmpeg_resolve.go`). |
| **Global hotkey** | **In-house ~50-line `RegisterHotKey`/`WM_HOTKEY` wrapper** (default), with `golang.design/x/hotkey` as a vendored reference | `golang.design/x/hotkey` is **effectively unmaintained (last commit Feb 2023)**; the Win32 wrapper is thin enough to own outright, and owning it lets us control the **message loop / thread affinity** so it cooperates with Wails' message pump (a real integration risk — validated in the Phase 0 spike). | `golang.design/x/hotkey` as-is — usable but frozen and opaque about its message loop; `robotgo` — heavier cgo, more than we need. |
| **Clipboard (images)** | **READ** via JS `paste` event (reliable in WebView2) + Go fallback for the toolbar button; **WRITE** via a **multi-format `SetClipboardData` syscall publishing `'PNG'` (registered) + `CF_DIBV5` (32bpp premultiplied alpha) + `CF_DIB`** in one open-clipboard transaction, **now living in the shared `internal/export` package** (see §3.3 / §2 Export decision) | WebView2's `navigator.clipboard.write` throws `Document is not focused.`; and single-format DIB writes **paste black** on transparency (rounded corners/shadow) in CF_DIB-only targets — so multi-format is the **baseline**, not a contingency (see §3.3). Centralizing it in `internal/export` keeps the image clipboard-write next to the video `CF_HDROP` write. | `golang.design/x/clipboard` alone — writes a single format, loses alpha in common paste targets. |
| **Default hotkeys** | **Ctrl+Shift+2** = control-bar overlay (primary), **Ctrl+Shift+1** = instant region screenshot, **Ctrl+Shift+3** = instant region recording; **stop recording = Ctrl+Shift+0** (rebindable) + tray Stop square | All avoid OS-reserved **Win+Shift+S** (Snipping Tool), **Win+Shift+R**, **Win+G**, **PrtSc**, and **Ctrl+Shift+Esc** (Task Manager); shell intercepts the Win+ family before `RegisterHotKey` sees them. | — (all rebindable in Settings) |

**Seam note (the unifier):** `internal/capture.Capture(req)` is the *only* capture entrypoint. Still and video take different primitives (DXGI vs FFmpeg) but **share the request, the result, and — critically — the `Rect` coordinate contract** (§4.3). Because both live behind this seam, swapping the still primitive (DXGI ⇄ `gdigrab`) or the video region path (`ddagrab` ⇄ `gdigrab`) is a one-file localized change that never touches either editor or the contract. The **native Win32 `WS_EX_LAYERED | WS_EX_TRANSPARENT | WS_EX_TOPMOST` overlay is the pre-documented contingency** if the week-1 v3 transparency spike fails; it swaps only the overlay's rendering, leaving the contract and both editors untouched.

**Export/clipboard seam note:** clipboard-write and Save-As for **both** media types now live in the shared `internal/export` package (a sibling of `internal/capture`, off the Dev1/Dev2 seam, built in Phase 0). The multi-format **image** clipboard write (`'PNG'` + `CF_DIBV5` + `CF_DIB`) and the **video** `CF_HDROP` file-drop write are both authored there, so neither dev owns clipboard logic divergently — the screenshot editor calls `Export.CopyToClipboard(pngPath, "image")` and the trim editor calls `Export.CopyToClipboard(videoPath, "video")`; both call `Export.SaveAs(...)`.

---

## 3. UX Spec (macOS behavior → Windows clone)

### 3.0 Design Language (SHARED, applies to every surface)

A single design system, owned in `frontend/src/components/ui` during Phase 0, governs all three React routes (Overlay, Editor, Trim) so neither developer restyles independently. Three non-negotiable rules:

- **[D1] Sleek DARK MODE — the only theme for v1.** Ship a dark **shadcn** theme on a **neutral/zinc** base; v1 has no light theme and no theme toggle. Define the palette as **CSS custom-property tokens in `components/ui`** (the shadcn `--background`/`--foreground`/`--card`/`--popover`/`--primary`/`--muted`/`--accent`/`--border`/`--ring` token set, zinc-derived) so all three routes — Overlay, Editor, Trim — consume **one** theme from one place rather than each defining its own.
- **[D2] FROSTED GLASS / translucency on all app "chrome."** Every floating chrome surface reads as frosted glass: the capture-overlay control-bar pill, the floating post-capture thumbnail, the screenshot editor toolbar/panels, and the trim editor chrome. Implement via **BOTH** layers:
  - **(a) OS-level window backdrop** — Windows 11 **acrylic/mica** through **Wails v3 window options** for any window that is itself non-transparent chrome (the editor windows, the trim window, the thumbnail shell), so the native window frame/backdrop is translucent.
  - **(b) CSS `backdrop-blur`** on the panel elements inside the webview (control-bar pill, toolbars, popovers, panels) for in-webview frosting that works even where the window itself must stay transparent (the overlay).
  - **Note:** the dim+crop overlay itself keeps its **~45% black dim layer** unchanged — "frosted" applies to the **chrome/panels floating above** the dim, not to the dim layer.
- **[D3] SHARP EDGES everywhere — ZERO rounded corners.** Set the Tailwind theme `borderRadius` scale to **all-`0`** AND override shadcn's default **`--radius` token to `0`** so every button, popover, card, dialog, toolbar, **and the crop rectangle** render with crisp square corners. **shadcn components ship rounded by default** (their `--radius` token and `rounded-*` utilities) — the radius token **MUST be zeroed**, or square-corner intent silently regresses to rounded.
- **[D4] Ownership.** This is **SHARED design-system work owned in `components/ui` during Phase 0**; the dark-theme tokens, the frosted-chrome utilities, and the zeroed radius are landed once, up front, so neither developer restyles independently in their subtree.

### 3.1 Capture Overlay (SHARED — built first, together)

- **[O1] Trigger.** Global hotkey (default **Ctrl+Shift+2**) opens a frameless, transparent, always-on-top overlay covering the **entire virtual desktop** (one window per monitor), dimming everything to ~**45% black** via a CSS `rgba()` layer.
- **[O2] Two entry sub-modes, ONE overlay.** Ctrl+Shift+2 opens with the **control-bar pill** visible; Ctrl+Shift+1/3 open straight into **crosshair** sub-mode. Do not build two overlays — one overlay, two sub-modes.
- **[O3] Crosshair + magnifier loupe.** Cursor becomes a crosshair; a loupe follows the cursor showing a zoomed pixel grid + live **X,Y** readout (px).
- **[O4] Drag to select.** Click-drag paints a clear (un-dimmed) crop rectangle; a **dimension badge** shows live **W×H px** near the rectangle.
- **[O5] Modifier gestures (1:1 with macOS, overlay-local so no OS conflict).** **Shift** = lock to one axis while dragging; **Alt** = resize from center; **Space (held)** = move the whole selection without resizing.
- **[O6] 8 resize handles.** After release, the rect shows 4 corner + 4 edge handles; drag handles to resize, drag interior to move.
- **[O7] Control-bar pill** (centered near bottom, shadcn): `[Region Shot | Window Shot | Full-Screen Shot | Region Record | Full-Screen Record]` → **Options** dropdown → primary **Capture/Record** button.
- **[O8] Window sub-mode.** Hovering highlights the window under the cursor (auto-fills the selection from its bounds); click captures that window. *(Native plumbing: `EnumWindows` + `DwmGetWindowAttribute(DWMWA_EXTENDED_FRAME_BOUNDS)`, excluding cloaked/minimized. Scope as a distinct task; v1.1-eligible.)*
- **[O9] Esc** (or right-click) cancels and **broadcast-dismisses ALL per-monitor overlays at once** (one Go-side broadcast of `overlay:dismiss` to every window — owned by the `internal/overlay` lead, see §6).
- **[O10] Enter** or the Capture/Record button commits.
- **[O11] Hide-before-capture (concrete mechanism — NOT "wait one frame").** On commit the `OverlayManager`:
  1. Calls `SetWindowPos(SWP_HIDEWINDOW | SWP_NOMOVE | SWP_NOSIZE | SWP_NOZORDER)` on **all N** overlay HWNDs (synchronous; doesn't wait on the Wails async hide).
  2. Issues a **`DwmFlush()`** so the compositor has presented a frame **without** the dim layer (DwmFlush blocks until the next DWM composition pass).
  3. Adds a small **fixed safety delay (default 16 ms, configurable)** as belt-and-braces for slow compositors, then
  4. Fires `Capture(req)`.
  This is **shared overlay work owned by the `internal/overlay` lead** and is a named Phase 0 deliverable (see §6/§7), replacing the undefined "wait one frame."
- **[O12] Copy-on-commit modifier.** Holding a modifier (the macOS "add Control = copy") at commit copies to clipboard instead of saving.
- **[O13] Video selection is single-monitor (ENFORCED).** When the mode is **video**, the overlay **constrains the selection rectangle to the monitor where the drag started** — the rect cannot be dragged or resized across a monitor boundary; a tooltip explains why. This is enforced in `overlay/useCropRect` and guarantees the `ddagrab` GPU 4K60 path always applies (see §10 for the rationale and the non-enforced alternative we rejected).

### 3.2 Post-capture floating thumbnail (SHARED shell, parameterized by media type)

- **[T1]** A floating thumbnail appears **bottom-right ~5s** after every capture.
- **[T2] Ignore** → auto-save to the default folder (`Pictures\Screenshots`) — PNG for stills, MP4/WebM for video.
- **[T3] Click** → opens the appropriate editor window (Konva editor / trim editor).
- **[T4] Drag** → OS drag-and-drop of the saved file into other apps.
- **[T5] Hover** → a small **Save now** + **Edit** action row (Windows equivalent of macOS swipe).
- **[T6] Naming:** `Screenshot YYYY-MM-DD at H.MM.SS AM-PM.png` / `Screen Recording YYYY-MM-DD at H.MM.SS AM-PM.mp4` (`-` instead of `:` for NTFS).

### 3.3 Screenshot annotation editor (Developer 1)

Floating, resizable window. **Toolbar in macOS Markup order** (shadcn): `Select → Sketch → Draw → Shapes (rect / ellipse / line / arrow / speech-bubble / highlight / star) → Text → Shape Style (thickness / dash-solid / shadow) → Border Color → Fill Color → Text Style (font / size / color / align) → Rotate → Crop → ▸ Copy → Save`. Color = swatch popover; thickness lives under Shape Style.

- Multi-color/width **freehand pen** (`Konva.Line`, `tension` smoothing).
- **Emoji stickers** (`Konva.Text`) — movable/resizable/layerable.
- **Paste image** (Ctrl+V) → drops a `Konva.Image` layer with `Transformer` (move/resize/rotate) + z-order (bring-forward/send-back).
- All objects selectable/movable/resizable/**deletable (Del)**; **undo/redo (Ctrl+Z / Ctrl+Shift+Z)** via a serializable JSON scene stack independent of Konva.
- **Clipboard WRITE is multi-format from M5 day one (BASELINE, not a later escalation), and lives in the shared `internal/export` package — NOT in `internal/shot`.** The editor flattens the scene to a PNG file and calls **`Export.CopyToClipboard(pngPath, "image")`**, which in **one** `OpenClipboard`/`EmptyClipboard` transaction publishes **all three** image formats: the registered `'PNG'` format (lossless, alpha-correct for modern targets), `CF_DIBV5` (32bpp, premultiplied alpha, BI_BITFIELDS), and `CF_DIB` (24bpp, for legacy/CF_DIB-only targets — composited over white so it isn't black). Transparency-black on rounded corners/shadow is otherwise near-guaranteed in common targets; treating multi-format as the baseline prevents Dev1's M5 from slipping on a "smoke test surprise." Because this logic is shared in `internal/export`, the screenshot copy and the video copy live together and neither dev owns clipboard logic divergently.
- **Copy** = flattened composite via `Export.CopyToClipboard(pngPath, "image")` (the multi-format write above); **Save / Save-As** = PNG (with naming convention) via the shared **`Export.SaveAs(...)`** native dialog (location/format picker).
- *Shape-recognition Sketch (rough rect → clean rect) ships as v2; plain freehand first.*

### 3.4 Video trim editor (Developer 2 — intentionally minimal, no drawing)

- **[R1]** HTML5 `<video>` player (play / pause / scrubber / current-time).
- **[R2]** Filmstrip timeline (thumbnails from a side FFmpeg pass) with **TWO draggable handles** (left = in-point, right = out-point); region outside the handles **dimmed** ("to be removed", QuickTime model).
- **[R3]** Dragging a handle live-updates in/out time labels.
- **[R4]** **Trim/Done** commits the in/out range.
- **[R5]** **Save** overwrites; **Save-As** writes a new file via the shared **`Export.SaveAs(...)`** native dialog.
- **[R6]** One-click **"Optimize for Discord"** export (two-pass size-target ~9 MB; Discord free cap is 10 MB in 2026).
- **[R7] Recording controls:** tray Stop square **+** floating Stop pill **+** rebindable global stop hotkey (Ctrl+Shift+0); optional countdown (None/5s/10s) + show-cursor toggle from the shared Options dropdown; elapsed-time readout.
- **[R8] Copy-video-to-clipboard is IN SCOPE — as a file-drop reference (`CF_HDROP`), not as a video bitstream.** Windows has **no clipboard format for a video bitstream**, so the trim editor does **not** try to place pixels/frames on the clipboard. Instead the editor calls **`Export.CopyToClipboard(videoPath, "video")`**, which places the **saved file on the clipboard as a file-drop reference (`CF_HDROP`)** — exactly what File Explorer's own Copy does — so **paste into Explorer / Discord / Slack / Teams works** (each receives the file and uploads/embeds it). The technical caveat stands (no bitstream clipboard format exists); the conclusion is flipped to **in-scope** via the `CF_HDROP` file-reference path, owned in the shared `internal/export` package alongside the image clipboard write.

---

## 4. Architecture

### 4.1 Components

- **Single Wails v3 process** holding: `HotkeyManager`, `OverlayManager` (N per-monitor windows), `CaptureCore` (the shared dispatch seam: DXGI stills + FFmpeg video), `ExportService` (the shared `internal/export` seam: clipboard-write + Save-As for both media types), `ThumbnailShell`, `Tray`, and the editor-service bindings.
- **N transparent overlay WebviewWindows** (one per monitor) — same React `/overlay` route.
- **1 Screenshot Editor WebviewWindow** (Dev 1, React `/editor`) — opens on still commit.
- **1 Trim Editor WebviewWindow** (Dev 2, React `/trim`) — opens on video commit.
- **In-process DXGI grab** for stills (no child process); **child `ffmpeg.exe` process** — long-lived for a recording, transient for thumbnail/trim passes.

### 4.2 Text diagram

```
                       ┌───────────── ONE Go process (Wails v3) ─────────────┐
 [Global Hotkey] ──────▶ HotkeyManager (in-house RegisterHotKey/WM_HOTKEY,    │
 Ctrl+Shift+1/2/3       │                own thread + message loop)           │
                        │        │ OverlayManager.Open(req)                   │
                        │        ▼                                           │
                        │  [Overlay win mon0]…[Overlay win monN]  React dim  │
                        │        │  user drags rect + picks mode + commits   │
                        │        │  (video rect locked to ONE monitor)       │
                        │        │  JS→Go: Capture.Commit(CaptureRequest)     │
                        │        ▼                                           │
                        │  HIDE-BEFORE-CAPTURE: SetWindowPos(SWP_HIDEWINDOW)  │
                        │    on all N HWNDs → DwmFlush() → 16ms → fire        │
                        │        ▼                                           │
                        │   CaptureCore.Capture(req)  ── ONE seam ──────────  │
                        │        ├─ Mode=screenshot → DXGI grab (kbinani)     │
                        │        │     (fallback: ffmpeg gdigrab -frames:v 1) │
                        │        │        └▶ EmitEvent "capture:done"{ImagePath} → /editor (Dev1)
                        │        └─ Mode=video → StartRecording → ffmpeg      │
                        │              args.go REBASES virtual-desktop Rect → │
                        │              monitor-relative + output_idx for      │
                        │              ddagrab (nvenc, long-lived)            │
                        │              Stop()=write 'q' to stdin (clean moov) │
                        │              progress via -progress pipe → "record:progress"
                        │              on stop → EmitEvent "capture:done"{VideoPath} → /trim (Dev2)
                        │                                                      │
                        │   ExportService (internal/export) ── SHARED ───────  │
                        │        ├─ CopyToClipboard(path,"image") → multi-fmt  │
                        │        │     PNG + CF_DIBV5 + CF_DIB  (Dev1 editor)   │
                        │        ├─ CopyToClipboard(path,"video") → CF_HDROP   │
                        │        │     file-drop reference        (Dev2 editor) │
                        │        └─ SaveAs(src,name) → native dialog (BOTH)     │
                        └────────────────────────────────────────────────────┘

  ESC: ONE Go-side broadcast of "overlay:dismiss" to ALL N overlay windows.
```

### 4.3 The EXACT shared data contract (`internal/capture/contract.go`)

This struct set is the **entire Dev1 ↔ Dev2 interface**. It is **frozen on Day 1 only after it compiles and passes a CI `go build` + `go vet` gate** — a non-compiling contract blocks both devs, so "frozen" means "green in CI," not "written." Changes require both devs to agree.

> **`Rect` coordinate contract (read this — it is the single most important seam clarification).**
> `Rect` is **ALWAYS in virtual-desktop PHYSICAL pixels**. Origin = primary-monitor top-left; monitors to the left of / above the primary have **NEGATIVE** X/Y.
> - **`gdigrab`** consumes these coordinates **directly** — its `offset_x`/`offset_y` are virtual-desktop coordinates (negatives allowed). No conversion.
> - **`ddagrab`** does **NOT**. Its `offset_x`/`offset_y` are **monitor-relative**, and the monitor is chosen by **`output_idx`**. The **SAME `Rect` cannot feed both unchanged.**
> - Therefore **`args.go` is the sole owner of REBASING**: for the `ddagrab` path it looks up the target `ScreenInfo` by `MonitorID`, sets `output_idx = MonitorID`, and emits `offset_x = Rect.X − screen.X`, `offset_y = Rect.Y − screen.Y` (monitor-relative). This rebasing is the exact spot Dev2's video would otherwise silently capture the wrong region; it is covered by a golden unit test per monitor permutation (§8) that asserts the `ddagrab` arg string differs from the `gdigrab` arg string in precisely this way.

```go
package capture

// Rect is ALWAYS in virtual-desktop PHYSICAL pixels.
// Origin = primary-monitor top-left; monitors left/above the primary have NEGATIVE X/Y.
// gdigrab consumes X/Y directly (virtual-desktop coords). ddagrab does NOT:
// args.go rebases to monitor-relative (Rect.X - screen.X) and sets output_idx = MonitorID.
type Rect struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

type CaptureRequest struct {
	Mode          string  `json:"mode"`                // "screenshot" | "video"
	Sub           string  `json:"sub"`                 // "region" | "window" | "fullscreen"
	MonitorID     int     `json:"monitorId"`           // index == ddagrab output_idx
	Rect          Rect    `json:"rect"`                // physical px, virtual-desktop origin
	DPIScale      float64 `json:"dpiScale"`            // of the owning monitor
	IncludeCursor bool    `json:"includeCursor"`       // → draw_mouse / DXGI cursor compose
	CountdownSec  int     `json:"countdownSec"`        // 0 | 5 | 10 (video only)
	MicDevice     string  `json:"micDevice,omitempty"` // v1.1
	CopyOnCommit  bool    `json:"copyOnCommit"`        // copy instead of save
}

type CaptureResult struct {
	Mode      string `json:"mode"`
	ImagePath string `json:"imagePath,omitempty"` // screenshot
	VideoPath string `json:"videoPath,omitempty"` // video (on stop)
	HandleID  string `json:"handleId,omitempty"`  // long-lived recording handle
	Rect      Rect   `json:"rect"`
	MonitorID int    `json:"monitorId"`
	Cancelled bool   `json:"cancelled"`
}

type TrimRequest struct {
	VideoPath string `json:"videoPath"`
	StartMs   int    `json:"startMs"`
	EndMs     int    `json:"endMs"`
	Precise   bool   `json:"precise"` // true = re-encode (frame-accurate); false = -c copy
	OutPath   string `json:"outPath"`
}

type ScreenInfo struct {
	ID          int     `json:"id"`
	X           int     `json:"x"` // physical px, virtual-desktop origin (may be negative)
	Y           int     `json:"y"`
	W           int     `json:"w"`
	H           int     `json:"h"`
	ScaleFactor float64 `json:"scaleFactor"`
	IsPrimary   bool    `json:"isPrimary"`
}
```

> **Go struct-tag correctness (was a compile blocker — now fixed):** every field has its **own** line and **exactly one** `json:"…"` tag. Comma-grouped fields sharing one multi-key tag (`X, Y, W, H int \`json:"x" json:"y"…\``) is **invalid Go and will not compile** — it would have blocked both devs on Day 1. CI compiles `contract.go` before the freeze is declared.

**Wails binding & event signatures** (the only IPC crossing the seam):

```
// Bindings (typed JS→Go, generated via `wails3 generate bindings`):
Overlay.ListScreens()                         []ScreenInfo            // THE single screen-enum source of truth
Overlay.Commit(req CaptureRequest)            (CaptureResult, error)  // screenshot returns ImagePath; video returns HandleID
Overlay.Cancel()
Overlay.StartRecording(req CaptureRequest)    (handleID string, error)
Overlay.StopRecording(handleID string)        (CaptureResult, error)
// SHARED export bindings (internal/export — used by BOTH editors):
Export.CopyToClipboard(path string, mediaType string) error           // "image" → multi-format bitmap (PNG + CF_DIBV5 + CF_DIB); "video" → CF_HDROP file-drop reference
Export.SaveAs(srcPath string, suggestedName string) (string, error)   // native Save-As dialog → copies/moves file to chosen path; returns chosenPath; used by BOTH editors
// Dev1-owned bindings:
Screenshot.SavePNG(b64 string, path string)   (string, error)
Screenshot.ReadClipboardImage()               (string /*b64*/, error)  // toolbar Paste-button fallback
// Dev2-owned bindings:
Video.Trim(req TrimRequest)                    (string /*outPath*/, error)
Video.GenerateThumbnails(path string, n int)   ([]string, error)
Video.ExportForDiscord(path string)            (string, error)

// Events (Go→JS broadcast, the async seam):
"capture:done"      { mode, imagePath|videoPath, rect, monitorId }   // routed to the correct editor BY MODE
"capture:cancelled"
"record:progress"   { handleId, elapsedMs, sizeBytes }
"overlay:dismiss"                                                     // ONE broadcast to ALL overlay windows on Esc
"capture:thumbnail" { mediaType, path }                              // drives the floating thumbnail shell
```

> **Note on the clipboard/Save-As bindings:** `Export.CopyToClipboard` and `Export.SaveAs` are **shared** (backed by `internal/export`), not Dev1- or Dev2-owned. The screenshot editor calls `Export.CopyToClipboard(pngPath, "image")`; the trim editor calls `Export.CopyToClipboard(videoPath, "video")`; both call `Export.SaveAs(...)` for their Save-As. The old `Screenshot.CopyPNGToClipboard` binding is **removed** — its multi-format write moved into `Export.CopyToClipboard(..., "image")`.

**Key invariant:** the `"capture:done"` event's `mode` is what routes to Dev1 vs Dev2. Neither editor imports the other; both subscribe only to this event + import only `internal/capture/contract.go` (and its generated `contract.ts` mirror), plus call the shared `Export.*` bindings.

---

## 5. Repository Structure

Single Go module + single Vite/React app. **Folder boundaries == team boundaries.**

```
macshot/
├─ go.mod  wails.json  main.go            # app wiring: tray, hotkeys, prewarm overlay, Mode router  [SHARED]
├─ build/windows/
│   ├─ macshot.manifest                   # Per-Monitor-V2 dpiAwareness  [SHARED, CRITICAL]
│   ├─ info.json  icon.ico  installer.nsi # NSIS also STAGES ffmpeg.exe beside the app exe
├─ internal/
│   ├─ capture/                           # ★ SHARED CORE — built first, together
│   │   ├─ contract.go                    #   the frozen seam (structs above) — CI-compiled before freeze
│   │   ├─ core.go                        #   Capture(req) dispatch on Mode; DXGI still path vs FFmpeg video path
│   │   ├─ still_dxgi.go                  #   DEFAULT stills: kbinani/screenshot (pure-Go DXGI), in-process
│   │   ├─ args.go                        #   buildStillFallbackArgs / buildVideoArgs / buildTrimArgs;
│   │   │                                 #   OWNS virtual-desktop→monitor-relative ddagrab rebasing (THE unifier)
│   │   ├─ recording.go                   #   Start/Stop, 'q'-to-stdin clean stop, -progress parse
│   │   ├─ encoders.go                    #   detect nvenc/qsv/amf; codec policy (H.264 vs VP9 — see §10)
│   │   └─ ffmpeg_resolve.go              #   locate/verify(SHA256)/stage ffmpeg.exe (NO go:embed); Cisco OpenH264 fetch
│   ├─ export/                            # ★ SHARED — clipboard-write + Save-As for BOTH media types (off the Dev1/Dev2 seam)
│   │   ├─ service.go                     #   Export.CopyToClipboard / Export.SaveAs bindings; mediaType dispatch + native Save-As dialog
│   │   └─ clipboard_windows.go           #   "image" → multi-format SetClipboardData ('PNG' + CF_DIBV5 + CF_DIB); "video" → CF_HDROP file-drop reference
│   ├─ overlay/                           # ★ SHARED — per-monitor window mgr (ONE NAMED OWNER, see §6)
│   │   ├─ manager.go                     #   ListScreens (source of truth), prewarm/Show, Esc broadcast,
│   │   │                                 #   hide-before-capture (SWP_HIDEWINDOW→DwmFlush→delay), monitor↔output_idx
│   │   └─ window_native_fallback.go      #   (contingency) WS_EX_LAYERED overlay, same contract
│   ├─ hotkey/registrar.go                # ★ SHARED — in-house RegisterHotKey/WM_HOTKEY + msg loop + rebind + conflict
│   ├─ dpi/awareness.go                   # ★ SHARED — SetProcessDpiAwarenessContext(-4) at init
│   ├─ thumbnail/shell.go                 # ★ SHARED — floating post-capture thumbnail (media-type param)
│   ├─ tray/tray.go                       # ★ SHARED — tray icon + recording Stop square
│   ├─ shot/                              # ◆ DEVELOPER 1 ONLY
│   │   └─ service.go                     #   SavePNG / ReadClipboardImage bindings (clipboard WRITE lives in internal/export)
│   └─ vid/                               # ◆ DEVELOPER 2 ONLY
│       ├─ service.go                     #   Trim / GenerateThumbnails / ExportForDiscord bindings
│       └─ trim.go                        #   -c copy vs re-encode, filmstrip, two-pass size target
├─ third_party/ffmpeg/
│   ├─ ffmpeg.exe                         # LGPL build — DEV CONVENIENCE ONLY (NOT embedded; staged by installer)
│   └─ LICENSE  README.lgpl
├─ testdata/
│   ├─ sample.png                         # checked-in — Dev1 runs /editor standalone day one
│   └─ sample.mp4                         # checked-in — Dev2 runs /trim standalone day one
└─ frontend/
    ├─ index.html  vite.config.ts  package.json  components.json   # React 19 PINNED in package.json; shadcn config
    └─ src/
        ├─ contract.ts                    # ★ SHARED — TS mirror of contract.go (generated)
        ├─ routes/
        │   ├─ Overlay.tsx                # ★ SHARED — dim, crosshair, loupe, badge, 8 handles, control bar
        │   ├─ Editor.tsx                 # ◆ DEV 1 — react-konva annotation editor
        │   └─ Trim.tsx                   # ◆ DEV 2 — <video> + filmstrip + 2 handles
        ├─ overlay/                       # ★ SHARED — useCropRect (incl. single-monitor video lock), useScreenInfo, ControlBar
        ├─ editor/                        # ◆ DEV 1 — konva tools, toolbar, useClipboardPaste, undo stack
        ├─ trim/                          # ◆ DEV 2 — Timeline, Filmstrip, useTrimHandles
        └─ components/ui/                 # ★ SHARED — shadcn primitives + DARK theme tokens (zinc), frosted-chrome utilities, ZEROED radius (React-19-clean)
```

**Divergence line:** everything under `internal/capture|export|overlay|hotkey|dpi|thumbnail|tray`, `main.go`, `routes/Overlay.tsx`, `overlay/`, `contract.ts`, `components/ui/` is **shared (week 1)**. Below that, `internal/shot` + `frontend/src/editor` + `routes/Editor.tsx` = **Dev1**; `internal/vid` + `third_party/ffmpeg` + `frontend/src/trim` + `routes/Trim.tsx` = **Dev2**. After week 1 the only common file is `contract.go` (frozen).

---

## 6. Two-Developer Work Split

**Named ownership of the shared core (assigned Day 0 — not "joint/whoever"):** **Developer 2 leads `internal/overlay` and is the single owner of the shared coordination glue** — `Overlay.ListScreens()` is the **one** screen-enumeration source of truth both halves trust; Esc broadcast-dismiss; the `MonitorID ↔ ddagrab output_idx` mapping; the `args.go` virtual-desktop→monitor-relative rebasing; the hide-before-capture sequence ([O11]); and the media-type-parameterized `thumbnail.Shell`. (Dev2 owns it because the video path is the one that *consumes* monitor-relative coordinates, so the rebasing and the source-of-truth must not fork.) Dev1 reviews and signs off on contract-affecting changes but does not author overlay/coordination code. This removes the "unowned glue" collision point.

**`internal/export` is a SHARED Phase-0 deliverable, NOT Dev1-only.** Clipboard-write (multi-format image + `CF_HDROP` video) and Save-As for both media types are landed in `internal/export` during Phase 0 alongside the contract and the overlay core, so neither editor owns clipboard/save logic divergently. Both editors consume the `Export.*` bindings; neither authors clipboard syscalls in their own subtree.

| | **SHARED (week 1, together)** | **Developer 1 — Screenshot + Editor** | **Developer 2 — Video + Trim (+ overlay/coordination LEAD)** |
|---|---|---|---|
| **Go packages** | `capture` (contract, core, still_dxgi, args, recording, encoders, resolve), `export` (clipboard-write + Save-As for both media types), `overlay`, `hotkey`, `dpi`, `thumbnail`, `tray`; `main.go` Mode router | `internal/shot` (SavePNG / ReadClipboardImage; clipboard WRITE is shared in `internal/export`) | `internal/vid` (service, trim) + **leads `internal/overlay` + screen-enum source of truth** + stages `third_party/ffmpeg/ffmpeg.exe` |
| **React** | `routes/Overlay.tsx`, `overlay/*`, `contract.ts`, `components/ui/*` + dark/frosted/zeroed-radius theme | `routes/Editor.tsx`, `editor/*` | `routes/Trim.tsx`, `trim/*` |
| **Deliverables** | Compiling CI-green contract; ONE `ListScreens` source of truth; per-monitor transparent overlay (dim/crosshair/loupe/badge/8-handles/control-bar/Esc-broadcast/gestures/**single-monitor video lock**/**concrete hide-before-capture**); DXGI stills + FFmpeg video `Capture()` producing PNG + MP4 from a hardcoded request; **shared `internal/export` (multi-format image clipboard write + `CF_HDROP` video clipboard write + `Export.SaveAs`)**; shared **dark-mode/frosted/zeroed-radius design system** in `components/ui`; in-house hotkeys; DPI manifest; thumbnail shell; tray | Konva canvas (image base, shapes, pen, emoji, paste-as-layer, z-order, undo/redo); Markup toolbar; clipboard READ (paste event + Go fallback) & **multi-format WRITE via `Export.CopyToClipboard(pngPath,"image")` as M5 baseline**; PNG save via `Export.SaveAs` | Recording lifecycle (Start/Stop clean-`q`, encoder detect/fallback, countdown, progress); tray/floating Stop + stop hotkey; trim editor (player + filmstrip + 2 handles + dim); `-c copy`/re-encode trim; Discord export; **video copy via `Export.CopyToClipboard(videoPath,"video")` (`CF_HDROP`)**; Save-As via `Export.SaveAs`; **overlay/coordination glue** |
| **Agreed interfaces (up front)** | `CaptureRequest` / `CaptureResult` / `TrimRequest` / `ScreenInfo` + the binding/event signatures in §4.3 (incl. shared `Export.CopyToClipboard` / `Export.SaveAs`) | Consumes `"capture:done"` where `mode=="screenshot"` → `ImagePath`; calls `Screenshot.*` + shared `Export.*` bindings | Consumes `"capture:done"` where `mode=="video"` → `VideoPath`; calls `Overlay.StartRecording/StopRecording` + `Video.*` + shared `Export.*` bindings |
| **Rule for shared code** | Changes to `internal/capture/contract.go` (and its `contract.ts` mirror) require **both devs' sign-off in the PR** and must pass the CI compile gate. Everything else each dev merges independently into their own subtree. All edits happen in **per-dev git worktrees branched from `origin/main`**, merged via PR. | — | — |
| **How to STUB the other half (day-one independence — gated ONLY on the compiling contract + stub Capture(), NOT on the overlay spike)** | — | Open `/editor` directly on the checked-in **`testdata/sample.png`**; ignore overlay + video entirely. The whole Konva editor + clipboard + save is testable with no recorder, no overlay, no Dev2 code. | Call `StartRecording` with a **hardcoded `Rect`** (no overlay); open `/trim` on the checked-in **`testdata/sample.mp4`**. The whole recording lifecycle + trim editor is testable with no screenshot, no overlay, no Dev1 code. |

---

## 7. Phased Roadmap

### Phase 0 — Skeleton + Contract + Stubs (the true unblocker) + parallel spikes (Week 1)

**Re-cut so the minimal unblocker lands Day 1 and is NOT gated on the overlay spike or the full FFmpeg core.** The critical path for *the two devs starting* is just: compiling contract + one screen-enum + a `Capture()` stub returning checked-in samples. The overlay spike and the real FFmpeg core run **in parallel behind those stubs**.

1. **Day 0 — repo + CI + React 19 pin.** `wails3 init`; React **19** + TS + shadcn + Tailwind frontend with **React 19 pinned in `package.json`** (verify shadcn/Radix are React-19-clean at init); land the **shared design system in `components/ui`** (dark zinc theme tokens, frosted-chrome utilities + Wails-v3 acrylic/mica window options, Tailwind `borderRadius` all-`0` + `--radius` token zeroed — see §3.0); Per-Monitor-V2 manifest; GitHub Actions on `windows-latest` (build + lint) including a **`go build ./internal/capture` gate so the contract must compile**; branch protection on `main`; check in `testdata/sample.png` + `testdata/sample.mp4`.
2. **Day 1 — freeze `contract.go` + `contract.ts` (only once CI-green) + ship the stub `Capture()` + the ONE `ListScreens` source + scaffold the shared `internal/export` package.** `Capture()` initially **returns the checked-in sample paths**; `Overlay.ListScreens()` returns real `ScreenInfo`; **`internal/export` exposes `Export.CopyToClipboard` / `Export.SaveAs`** (multi-format image write + `CF_HDROP` video write + native Save-As dialog) so both editors can call them from day one. **This is the unblock — both devs start against stubs at end of Day 1**, independent of everything below.
3. **Day 1–5 (PARALLEL track, off the devs' critical path) — THE OVERLAY SPIKE.** On a **real 2-monitor mixed-DPI rig**, prove a transparent + frameless + AlwaysOnTop **per-monitor** Wails v3 window with React dim + `SetIgnoreMouseEvents` click-through, the **in-house `RegisterHotKey`/`WM_HOTKEY` message-loop integration with Wails' message pump**, and the **concrete hide-before-capture sequence** (SWP_HIDEWINDOW→DwmFlush→delay) all work (pinned alpha ≥ alpha.97, which fixed fullscreen click-through). **If the transparency spike fails → switch to the native Win32 `WS_EX_LAYERED` overlay** (`window_native_fallback.go`); contract + both editors + stubs are unaffected.
4. **Day 1–5 (PARALLEL track) — real capture core.** Wire `still_dxgi.go` (default DXGI stills) + `args.go`/`recording.go` so one hardcoded `CaptureRequest` yields **both** a PNG (DXGI; `gdigrab` fallback) **and** an MP4 (`ddagrab`/`gdigrab` encode, with the **monitor-relative rebasing**) from the same seam. Implement clean-`q` stop + encoder detection + the §10 codec policy. Swap the stub `Capture()` for the real one when green — devs continue uninterrupted because the contract didn't move.
5. **Day 3–5 — shared overlay React** (overlay lead): dim, crosshair, loupe, W×H badge, 8 handles, Space/Shift/Alt gestures, **single-monitor video lock**, control-bar pill, mode toggle → emits `Commit()`/`Cancel()`; wire `"capture:done"` Mode routing + thumbnail shell + tray.

**Phase 0 exit demo:** hotkey → overlay → drag rect → pick *screenshot* → a real PNG (DXGI) opens a blank editor window; pick *video* (rect locked to one monitor) → record → a real MP4 opens a blank trim window. **Contract is frozen and CI-green; the capture core is real; the shared `internal/export` clipboard/Save-As service and the dark/frosted/sharp-edge design system are landed; the devs have been productive against stubs since Day 1.**

### Phase 1 — Parallel tracks (Weeks 2–5)

**Track A (Dev1, screenshot):** M1 Konva canvas loads PNG + select/move/delete → M2 shapes + multi-color pen + emoji → M3 paste-image-as-layer + z-order + Transformer → M4 Markup toolbar + color popovers → M5 **multi-format clipboard WRITE via shared `Export.CopyToClipboard(...,"image")` (baseline)** + READ + save via `Export.SaveAs` + undo/redo.

**Track B (Dev2, video):** M1 recording lifecycle (Start/Stop/progress/countdown) → M2 tray+floating Stop + stop hotkey → M3 trim editor player + filmstrip → M4 two-handle in/out + `-c copy` trim → M5 re-encode toggle + Discord export + **video copy via shared `Export.CopyToClipboard(...,"video")` (`CF_HDROP`)** + Save-As via `Export.SaveAs`. *(Overlay/coordination glue and the shared `internal/export` package are Phase 0 work, not Phase 1.)*

### Phase 2 — Integration & polish (Week 6)

Window sub-mode capture ([O8]), settings/rebind UI, auto-update, installer (incl. **NSIS FFmpeg staging** + **runtime Cisco OpenH264 fetch** path), real-hardware multi-monitor/mixed-DPI QA pass, FFmpeg LGPL attribution + source mirror, codec/licensing sign-off (§10).

### Deferred to v1.1

System/mic **audio** (FFmpeg has no native WASAPI loopback → needs Go cgo WASAPI capture as a 2nd input); **shape-recognition Sketch**; cross-platform.

---

## 8. Build, CI & Tooling

- **Toolchain:** Go (per §9), `wails3` CLI, Node + pnpm, NSIS (for the installer), WebView2 runtime.
- **GitHub Actions (`windows-latest`):**
  - **`go build ./internal/capture && go vet ./internal/capture` as a Day-0 gate** — the frozen contract MUST compile before it's declared frozen (catches the struct-tag class of bug).
  - `pnpm install && pnpm lint && pnpm build` (frontend: ESLint + `tsc --noEmit`); verify the **React 19** lockfile is honored.
  - `go vet ./...` + `golangci-lint run`.
  - `wails3 task build` (full app build) — gated on PRs.
  - **`choco install nsis` before `wails build -nsis`** (windows-latest runners don't ship `makensis`; Wails fails-quiet otherwise).
  - Upload the built `.exe` + installer as artifacts; on tag, attach to a GitHub Release with **SHA256SUMS**.
- **FFmpeg delivery (NOT `//go:embed`):** the **NSIS installer stages `ffmpeg.exe` beside the app exe**; on first run, `ffmpeg_resolve.go` locates it, **verifies SHA256**, and (only if missing, e.g. a portable/zip distribution) **downloads it + verifies SHA256**. Use an **LGPL build** (HW encoders + container/muxers, **no `libx264`**). The Go binary never embeds the ~80–120 MB FFmpeg.
- **OpenH264 (Cisco) handling:** if the user opts into H.264 output and no HW encoder is present, `ffmpeg_resolve.go` **downloads Cisco's prebuilt OpenH264 module from Cisco at runtime** (the only patent-clean path — see §10) and loads it; it is **never bundled or self-built**.
- **Installer + auto-update:** NSIS via `wails build -nsis`. Auto-update via a GitHub-Releases-backed updater (check latest tag, download installer, verify SHA256, run silent install) — mirror the pattern the team shipped in wc3-forge's in-app updater.
- **Testing strategy:**
  - *Unit (Go):* `args.go` arg-builder is pure (request → `[]string`); **golden-test every Mode/Sub/monitor permutation, asserting the `ddagrab` arg string is correctly REBASED (monitor-relative + `output_idx`) and differs from the `gdigrab` (virtual-desktop) arg string in exactly that way** — this guards the single most failure-prone seam. Encoder-detection and codec policy mocked. `internal/export` clipboard-format selection (`"image"`→multi-format bitmap vs `"video"`→`CF_HDROP`) is unit-tested with the actual `SetClipboardData` call mocked.
  - *Capture/overlay (hard to unit-test):* an **E2E harness** — a Go test spawns the real `ffmpeg`, captures a 2×2 known-color test pattern at a known rect, and asserts the PNG's corner pixels; a separate DXGI test asserts the in-process still grab returns the same pattern; a recording test records 1s, stops via `'q'`, and asserts the MP4/WebM is playable (`ffprobe` moov/keyframes present). Overlay coordinate math (incl. the rebasing) validated against a synthetic multi-monitor `ScreenInfo` fixture. **Hide-before-capture** verified by asserting the captured frame contains no dim-layer pixels at a known overlay location. Manual QA checklist for the live overlay on real 2-monitor mixed-DPI hardware (the spike rig becomes the QA rig).
  - *React:* Vitest + Testing Library (React 19) for the Konva scene-state reducer (undo/redo), the multi-monitor-lock crop math, and the trim handle math; Editor/Trim run standalone against `testdata/sample.png` / `testdata/sample.mp4`.

---

## 9. Prerequisite Software (install locally)

- **Go 1.23+** (toolchain matching the pinned Wails v3 alpha's `go.mod`).
- **Wails v3 CLI:** `go install github.com/wailsapp/wails/v3/cmd/wails3@v3.0.0-alpha.98` (pin exact).
- **Node 20+ and pnpm** (`corepack enable && corepack prepare pnpm@latest --activate`).
- **React 19 (hard prereq — pinned in `package.json` Day 0).** `react-konva` v19 is **React-19-only**; the entire shared frontend (overlay, both editors, shadcn/ui) is on React 19. Verify shadcn/Radix versions are React-19-clean at init. Do not let either dev's subtree pull a React-18 `react-konva`.
- **WebView2 Runtime** (Evergreen) — ships with Win11 but verify; the installer should ensure it.
- **NSIS** (`choco install nsis`) — for building the installer locally (and it stages `ffmpeg.exe`).
- **Microsoft Visual Studio Build Tools / C build toolchain** — needed for cgo-touching deps and the in-house Win32 hotkey/clipboard syscalls; the optional WASAPI v1.1 path is cgo. Install "Desktop development with C++". *(Note: `kbinani/screenshot` and the multi-format clipboard use `golang.org/x/sys/windows` syscalls, largely cgo-free, but VS Build Tools are still the safe baseline.)*
- **FFmpeg (LGPL build) for dev** — drop `ffmpeg.exe` into `third_party/ffmpeg/` for local runs; devs also want a system FFmpeg + `ffprobe` on PATH for the E2E tests. (Shipping is via installer staging, not this file.)
- **`golangci-lint`** for the Go lint gate.
- A **real 2-monitor, mixed-DPI** machine (e.g., a 4K + 1080p pair) for the spike/QA — **non-negotiable** for validating the overlay, the rebasing, and hide-before-capture.

---

## 10. Risks & Open Questions

| Risk | Severity | Mitigation / Decision |
|---|---|---|
| **H.264 AVC PATENT exposure (not just copyright).** Dropping `libx264` clears the GPL/copyright issue but **not** the AVC patent (Via-LA pool). Bundling **any** H.264 encoder — HW (`nvenc`/`qsv`/`amf`) **or** a self-built OpenH264 binary — makes **us** the AVC distributor owing royalties. Cisco's OpenH264 is patent-clean **only when the end user downloads the prebuilt module from Cisco at runtime** (Cisco pays the pool). | **HIGH (legal)** | **Committed decision:** (1) **Default output = VP9 in WebM (royalty-free)** so the out-of-box path carries **zero** AVC obligation. (2) **H.264/MP4 is opt-in.** When chosen, use HW encoders **only on machines that already have them** and document that this still carries AVC distribution exposure if we ship the encoder — so for the **software** H.264 path we **download Cisco's OpenH264 module from Cisco at runtime and never bundle/self-build it**. (3) Add LGPLv2.1 attribution + FFmpeg source mirror. (4) Get explicit legal sign-off on the HW-encoder distribution question before any wide MP4-by-default release. This replaces the prior "no libx264 = clean (flag for legal at scale)" hand-wave. |
| **FFmpeg delivery via `//go:embed`** — a full FFmpeg (~80–120 MB) embedded as `[]byte` bloats/slows the Go build and holds the binary in memory. | **MED-HIGH** | **`//go:embed` removed.** Ship via **NSIS installer staging** (drops `ffmpeg.exe` beside the app) with a **first-run download + SHA256-verify** fallback for portable distributions. `ffmpeg_embed.go` does not exist; the file is `ffmpeg_resolve.go` (locate/verify/stage). This also hosts the Cisco OpenH264 runtime fetch. |
| **`Rect` → `ddagrab` coordinate mismatch (THE seam).** `gdigrab` offsets are virtual-desktop; `ddagrab` offsets are monitor-relative + `output_idx`. The same `Rect` cannot feed both unchanged — Dev2's video would silently capture the wrong region. | **HIGH** | Contract states `Rect` is **virtual-desktop physical px**; **`args.go` is the sole owner of rebasing** (`offset = Rect − screen.{X,Y}`, `output_idx = MonitorID`) for the `ddagrab` path (§4.3). **Golden unit test per monitor permutation** asserts the `ddagrab` vs `gdigrab` arg strings differ exactly here. |
| **Invalid Go struct tags** — comma-grouped multi-key `json` tags don't compile; the "frozen Day 1" contract would block both devs. | **HIGH (was a compile blocker)** | Fixed: **one field per line, one `json` tag each** (§4.3). **CI `go build ./internal/capture` gate runs Day 0**; "frozen" = CI-green, not merely written. |
| **Still-capture latency/correctness** — a cold `ffmpeg gdigrab -frames:v 1` exec per screenshot is tens-to-hundreds of ms and mishandles some DWM/layered content; instant stills are a **core** Cmd+Shift+5 expectation. | **MED** | **Default still path = pure-Go DXGI (`kbinani/screenshot`), in-process, no exec** (`still_dxgi.go`), behind the `internal/capture` seam. `gdigrab` retained only as the documented fallback for adapters lacking DXGI duplication (some RDP/VM). FFmpeg is off the screenshot hot path entirely. |
| **Hide-before-capture "wait one frame" is undefined** across async window-hide + DWM recomposite + separate-process grab → intermittently captures the dim layer. | **MED** | Replaced with a **concrete sequence** ([O11]): `SetWindowPos(SWP_HIDEWINDOW)` on all N HWNDs → **`DwmFlush()`** (blocks until next composition) → small fixed **16 ms** safety delay → fire `Capture()`. **Owned by the `internal/overlay` lead**, a named Phase 0 deliverable, with an E2E "no dim pixels at known location" assertion. |
| **Multi-monitor video spanning is a silent UX trap** — `ddagrab` can't span; `gdigrab` software fallback at 4K60 drops frames, so the "4K60" headline doesn't hold on spanning. | **MED** | **Decision: video selection is ENFORCED single-monitor in the overlay** ([O13], `useCropRect`) — the rect can't cross a monitor boundary. Guarantees the `ddagrab` GPU path always applies; Dev2 builds `StartRecording` against this guarantee, not against a degraded spanning path. (Rejected alternative: allow spanning with an explicit "degraded/software, may drop frames" warning.) |
| **Coordination "glue" unowned** — Esc broadcast, cross-monitor crop ownership, `MonitorID↔output_idx`, hide-then-capture, thumbnail shell were "joint/whoever." | **MED** | **Single named owner: Developer 2 leads `internal/overlay`** and owns the **one** `ListScreens` source of truth + all the glue (§6). Removes the cross-monitor collision point. |
| **Clipboard logic forking between editors** — if Dev1's image copy and Dev2's video copy were authored separately, the fiddly Win32 clipboard transaction would diverge (e.g., image goes multi-format but video re-implements its own broken write). | **MED** | **Single named owner: the shared `internal/export` package** (Phase-0 deliverable, off the Dev1/Dev2 seam) owns ALL clipboard-write (`"image"`→multi-format bitmap; `"video"`→`CF_HDROP`) and Save-As. Both editors call `Export.CopyToClipboard` / `Export.SaveAs`; neither authors clipboard syscalls in its own subtree (§6). |
| **Wails v3 transparent multi-monitor overlay** is alpha; positioning has a bug history (#2739/#3947/#4691). | **MED (front-loaded, OFF the devs' critical path)** | **Week-1 spike (parallel, behind stubs)** on real mixed-DPI hardware. Documented native **Win32 `WS_EX_LAYERED` fallback** that swaps only overlay rendering — contract + editors + stubs unaffected. Pin alpha ≥ alpha.97 (fullscreen click-through fix verified). |
| **DPI scaling across monitors** — GDI/gdigrab returns wrong physical px unless process is Per-Monitor-V2 aware. | **MED** | Ship the **Per-Monitor-V2 manifest** + `SetProcessDpiAwarenessContext(-4)` at startup. `Rect` stays virtual-desktop physical px in the contract. Config, not code. |
| **Clipboard image fidelity** — single-format DIB writes paste **black** on transparency (rounded corners/shadow) in CF_DIB-only targets; this is near-guaranteed, not a corner case. | **MED** | **Multi-format write is the BASELINE** (§3.3), authored once in `internal/export`: one `SetClipboardData` transaction publishing **`'PNG'` (registered) + `CF_DIBV5` (32bpp premultiplied alpha) + `CF_DIB` (24bpp composited over white)**. Not deferred to "if smoke-testing shows black." Smoke-tested across Paint/Word/Slack/Discord/browser. |
| **Global-hotkey library is unmaintained** — `golang.design/x/hotkey` last commit Feb 2023; `RegisterHotKey` also needs a message loop on the owning thread, and its interaction with Wails' message pump is unaddressed. | **MED** | **Default = ~50-line in-house `RegisterHotKey`/`WM_HOTKEY` wrapper** we own, so we control the **message loop / thread affinity** and verify cooperation with Wails' pump **in the Phase 0 spike**. `golang.design/x/hotkey` kept only as a vendored reference. |
| **Keyframe-accurate trim** — `-c copy` snaps to the nearest preceding I-frame (off by up to one GOP). | **MED** | Record with **`-g 60`** so copy-trim lands ≤1s off; expose a **"frame-accurate (re-encode)"** toggle for true precision (slower). |
| **Clean FFmpeg stop** — hard-kill corrupts the MP4 moov atom. | **MED** | `Stop()` **writes `'q'` to stdin and waits**; record to a temp file, rename on success; `-movflags +faststart`. Baked into the contract from day 1. |
| **Hotkey collisions** — Win+Shift+S/R, PrtSc, Ctrl+Shift+Esc are OS-reserved/intercepted. | **LOW** | Default to **Ctrl+Shift+1/2/3** + stop=Ctrl+Shift+0; catch `RegisterHotKey` conflict errors and surface a **rebind UI**. |
| **AlwaysOnTop vs exclusive-fullscreen games** (#4272). | **LOW** | Fine for desktop/window capture (the product scope). The native fallback's `WS_EX_TOPMOST` closes the gap if needed. |
| **v3-alpha churn still touches editor windows** even with the native-overlay fallback. | **LOW–MED** | Pin the exact alpha; expect occasional window-API breakage on the editor side; CI build gate catches it early. |

**Open questions to settle in Phase 0:** default save folder (Pictures\Screenshots vs configurable Desktop)? Default video container — **VP9/WebM (royalty-free) confirmed as the out-of-box default**, with H.264/MP4 opt-in (codec policy in `encoders.go`)? Whether window sub-mode ([O8]) is v1 or v1.1?

---

## 11. Suggested Project Names

| Name | Vibe |
|---|---|
| **MacShot** | On-the-nose "macOS-style shots on Windows"; great working title. |
| **Snipster** | Playful, friendly riff on "snip" — approachable consumer tool. |
| **CleanShot Win** | Evokes the beloved macOS CleanShot X; signals premium polish. |
| **Framegrab** | Technical, precise; nods to both stills and video frames. |
| **Dim & Crop** | Literally describes the signature overlay interaction. |
| **Capsule** | Tidy, modern; "capture + handle it" in one little capsule. |
| **Snapfire** | Fast, energetic — instant hotkey-to-shot feel. |
| **Lumen Snip** | Light/clean aesthetic; sounds designed, not utilitarian. |
| **OverShot** | The overlay is the hero; double-meaning with "shot." |
| **Reframe** | Smart, calm; covers crop, annotate, and trim in one word. |

---

**The seam in one sentence (hand this to both devs):** the overlay emits exactly one `CaptureRequest` (video rect locked to one monitor), Go's single `CaptureCore.Capture()` turns it into a PNG (in-process DXGI) or MP4/WebM path (FFmpeg, with `args.go` rebasing the virtual-desktop `Rect` to monitor-relative `ddagrab` coords), and the `"capture:done"` event routes by `mode` to either the Konva editor (Dev1) or the trim editor (Dev2) — neither of whom imports the other or anything beyond `internal/capture/contract.go`, while both share `internal/export` for clipboard-write (`CopyToClipboard`) and Save-As (`SaveAs`).

**Sources consulted to verify 2026 status:** [Wails releases](https://github.com/wailsapp/wails/releases) · [Wails v3 What's New](https://v3.wails.io/whats-new/) · [FFmpeg ddagrab filter docs (8.0)](https://ayosec.github.io/ffmpeg-filters-docs/8.0/Sources/Video/ddagrab.html) · [Cisco OpenH264 / AVC royalty FAQ](https://www.openh264.org/faq.html) · [kbinani/screenshot (pure-Go DXGI)](https://github.com/kbinani/screenshot)
