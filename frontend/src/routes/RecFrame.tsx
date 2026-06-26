// RecFrame — the "glowing border" that outlines the recorded region while a
// recording is in flight. Opened by WindowsService.OpenRecordingFrame as a
// frameless, transparent, click-through window that covers the recorded region
// expanded by `m` px on every side.
//
// The recorded region is the window's inner rect (inset by `m`). The animated red
// outline is drawn just OUTSIDE that inner rect — inside the `m`-px margin band —
// so ffmpeg (which captures exactly the inner rect) never bakes the border into
// the video. The center is fully transparent, so the live content shows through
// untouched.
//
// CLICK-THROUGH is two-part and BOTH halves are load-bearing: (1) Go adds
// WS_EX_TRANSPARENT to the host window (makeWindowClickThrough) — NOT Wails'
// IgnoreMouseEvents, which would also force WS_EX_LAYERED and composite the window
// opaque (a solid white rectangle over the recording); (2) the root element below
// stays pointer-events:none so WebView2 itself swallows no clicks. Keep the root
// pointer-events:none — an interactive element here would silently break
// click-through over its bounds.
export default function RecFrame() {
  const margin = parseInt(
    new URLSearchParams(window.location.search).get("m") ?? "10",
    10,
  ) || 10;

  // NB: the opaque app <body> background is nulled to transparent in main.tsx
  // BEFORE first paint (a useEffect here would race the first composite, and that
  // opaque first frame would be CAPTURED by ffmpeg — see main.tsx).

  return (
    <div className="pointer-events-none h-screen w-screen overflow-hidden bg-transparent">
      <style>{`
        @keyframes toruRecGlow {
          0%, 100% { box-shadow: 0 0 0 0 rgba(239,68,68,0.85), 0 0 6px 1px rgba(239,68,68,0.55); }
          50%      { box-shadow: 0 0 0 0 rgba(239,68,68,0.85), 0 0 12px 3px rgba(239,68,68,0.95); }
        }
      `}</style>
      {/* Inner rect == the recorded region. The outline sits 2px OUTSIDE it (in the
          margin band), so it is never inside the captured pixels. */}
      <div
        className="absolute"
        style={{
          left: margin,
          top: margin,
          right: margin,
          bottom: margin,
          outline: "3px solid rgb(239 68 68)",
          outlineOffset: "2px",
          animation: "toruRecGlow 1.6s ease-in-out infinite",
        }}
      />
    </div>
  );
}
