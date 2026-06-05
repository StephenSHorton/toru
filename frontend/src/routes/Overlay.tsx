import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Camera, Video, X } from "lucide-react";

// SHARED capture overlay (built first, together). This is the visual skeleton:
// a ~45% dim backdrop with a clear crop rectangle, 8 resize handles, a dimension
// badge, and a frosted control-bar pill. The real overlay is transparent +
// frameless + always-on-top + one-per-monitor (the Phase 0 spike) and emits a
// CaptureRequest on commit.
const HANDLES = [
  "nw", "n", "ne",
  "w", /* center */ "e",
  "sw", "s", "se",
];

export default function Overlay() {
  const [mode, setMode] = useState<"screenshot" | "video">("screenshot");
  // A mock crop rectangle (percent of viewport) for the skeleton.
  const crop = { left: "22%", top: "20%", width: "56%", height: "50%" };

  return (
    <div className="relative h-full w-full overflow-hidden">
      {/* dim backdrop */}
      <div className="absolute inset-0 bg-black/45" />

      {/* crop window: a clear hole punched in the dim (skeleton uses a bright ring) */}
      <div
        className="absolute ring-2 ring-primary"
        style={{ ...crop, boxShadow: "0 0 0 9999px rgba(0,0,0,0.0)" }}
      >
        {/* dimension badge */}
        <div className="frost absolute -top-7 left-0 px-2 py-0.5 text-[11px] tabular-nums">
          1280 × 800
        </div>
        {/* 8 resize handles */}
        {HANDLES.map((h) => (
          <span
            key={h}
            data-handle={h}
            className="absolute size-2.5 border border-background bg-primary"
            style={handleStyle(h)}
          />
        ))}
      </div>

      {/* frosted control-bar pill */}
      <div className="frost absolute bottom-8 left-1/2 flex -translate-x-1/2 items-center gap-1 p-1.5">
        <Button
          size="sm"
          variant={mode === "screenshot" ? "default" : "ghost"}
          onClick={() => setMode("screenshot")}
        >
          <Camera /> Screenshot
        </Button>
        <Button
          size="sm"
          variant={mode === "video" ? "default" : "ghost"}
          onClick={() => setMode("video")}
        >
          <Video /> Record
        </Button>
        <div className="mx-1 h-5 w-px bg-border" />
        <Button size="sm" variant="ghost" onClick={() => (window.location.search = "")}>
          <X /> Cancel
        </Button>
        <Button size="sm">{mode === "video" ? "Start Recording" : "Capture"}</Button>
      </div>
    </div>
  );
}

// handleStyle positions the 8 resize handles around the crop rectangle.
function handleStyle(h: string): React.CSSProperties {
  const s: React.CSSProperties = { transform: "translate(-50%, -50%)" };
  if (h.includes("n")) s.top = "0%";
  if (h.includes("s")) s.top = "100%";
  if (h === "e" || h === "w") s.top = "50%";
  if (h.includes("w")) s.left = "0%";
  if (h.includes("e")) s.left = "100%";
  if (h === "n" || h === "s") s.left = "50%";
  return s;
}
