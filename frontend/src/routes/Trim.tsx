import { useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Play, Pause, Scissors, Copy, Save } from "lucide-react";

// DEVELOPER 2 — video trim editor (skeleton). Intentionally minimal: a player,
// a filmstrip timeline, and TWO draggable handles (in/out). No drawing. The
// region outside the handles is dimmed ("to be removed", QuickTime model).
// Trim runs via VideoService.Trim; copy/save via the shared ExportService.
export default function Trim() {
  const vidRef = useRef<HTMLVideoElement>(null);
  const [playing, setPlaying] = useState(false);
  const [inPct, setInPct] = useState(10);
  const [outPct, setOutPct] = useState(82);
  const vidPath = new URLSearchParams(window.location.search).get("vid") ?? "";

  const toggle = () => {
    const v = vidRef.current;
    if (!v) return;
    if (v.paused) { v.play(); setPlaying(true); } else { v.pause(); setPlaying(false); }
  };

  return (
    <div className="flex h-full flex-col">
      <div className="flex flex-1 items-center justify-center p-4">
        <video
          ref={vidRef}
          src={vidPath || "/sample.mp4"}
          className="max-h-full max-w-full border"
          onEnded={() => setPlaying(false)}
        />
      </div>

      {/* frosted timeline */}
      <div className="frost m-3 p-3">
        <div className="relative h-14 w-full border bg-card/40">
          {/* dimmed "to be removed" regions */}
          <div className="absolute inset-y-0 left-0 bg-black/55" style={{ width: `${inPct}%` }} />
          <div className="absolute inset-y-0 right-0 bg-black/55" style={{ width: `${100 - outPct}%` }} />
          {/* kept region outline */}
          <div
            className="absolute inset-y-0 border-x-2 border-primary"
            style={{ left: `${inPct}%`, right: `${100 - outPct}%` }}
          />
          {/* in/out handles */}
          <Handle pct={inPct} onChange={(p) => setInPct(Math.min(p, outPct - 2))} />
          <Handle pct={outPct} onChange={(p) => setOutPct(Math.max(p, inPct + 2))} />
        </div>

        <div className="mt-3 flex items-center gap-2">
          <Button size="icon" variant="secondary" onClick={toggle} title={playing ? "Pause" : "Play"}>
            {playing ? <Pause /> : <Play />}
          </Button>
          <span className="text-xs tabular-nums text-muted-foreground">
            in {inPct}% · out {outPct}%
          </span>
          <div className="ml-auto flex gap-1">
            <Button size="sm" variant="ghost"><Copy /> Copy</Button>
            <Button size="sm" variant="ghost"><Save /> Save As…</Button>
            <Button size="sm"><Scissors /> Trim</Button>
          </div>
        </div>
        <div className="mt-2 text-[11px] text-muted-foreground">
          source: <span className="font-mono">{vidPath || "(dev sample.mp4)"}</span>
        </div>
      </div>
    </div>
  );
}

// Handle is a draggable in/out marker on the timeline.
function Handle({ pct, onChange }: { pct: number; onChange: (p: number) => void }) {
  const onDown = (e: React.MouseEvent) => {
    const track = (e.currentTarget as HTMLElement).parentElement!;
    const rect = track.getBoundingClientRect();
    const move = (ev: MouseEvent) => {
      const p = Math.round(((ev.clientX - rect.left) / rect.width) * 100);
      onChange(Math.max(0, Math.min(100, p)));
    };
    const up = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
    };
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  };
  return (
    <div
      onMouseDown={onDown}
      className="absolute top-0 z-10 h-full w-3 -translate-x-1/2 cursor-ew-resize bg-primary"
      style={{ left: `${pct}%` }}
    />
  );
}
