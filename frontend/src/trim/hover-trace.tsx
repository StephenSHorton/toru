// The page hover-trace.html shows: the editor's own timeline
// (MultiTimeline) on a sample project of 4 tracks and 24 clips, and its
// library's effect, filter and transition browsers, the effects both aimed
// at the first clip and with no clip (its tiles disabled), as in the editor
// window (routes/EditVideo.tsx), with no Go behind them.
// scripts/hover-trace.ts moves the mouse over it and traces what each hover
// restyles (whiteboard#56).
import "@/index.css";
import { StrictMode, useState } from "react";
import { createRoot } from "react-dom/client";
import { MultiTimeline, type TimelineSelection } from "@/trim/MultiTimeline";
import { EffectBrowser } from "@/trim/inspector/EffectsTab";
import { FiltersBrowser } from "@/trim/inspector/FiltersTab";
import { TransitionsBrowser } from "@/trim/library/Transitions";
import type { TabProps } from "@/trim/inspector/Inspector";
import { DEFAULT_FILTERS, FIT, type Clip, type Media, type Project, type Track } from "@/trim/project";

const media = (id: string, kind: Media["kind"]): Media => ({ id, path: `C:\\v\\${id}.mp4`, url: "", kind, duration: 60, width: 1920, height: 1080, fps: 30, hasAudio: kind !== "image", tracks: [] });
const clip = (id: string, mediaId: string, start: number, len: number): Clip => ({
  id,
  media: mediaId,
  start,
  in: 0,
  out: len,
  speed: 1,
  volume: 1,
  muted: false,
  fadeIn: 0,
  fadeOut: 0,
  transform: { ...FIT },
  filters: { ...DEFAULT_FILTERS },
});
const track = (id: string, kind: Track["kind"], mediaId: string, n: number): Track => ({
  id,
  kind,
  muted: false,
  hidden: false,
  locked: false,
  clips: Array.from({ length: n }, (_, i) => clip(`${id}-c${i}`, mediaId, i * 5 + (kind === "audio" ? 1 : 0), 4)),
});

const sample: Project = {
  version: 3,
  id: "proj-trace",
  name: "Hover trace",
  canvas: { width: 1920, height: 1080, fps: 30 },
  media: [media("m-a", "video"), media("m-b", "video"), media("m-s", "audio")],
  tracks: [track("v1", "video", "m-a", 6), track("v2", "video", "m-b", 6), track("a1", "audio", "m-s", 6), track("a2", "audio", "m-s", 6)],
  texts: [],
  crop: null,
};

function Page() {
  const [project, setProject] = useState(sample);
  const [selection, setSelection] = useState<TimelineSelection>(null);
  const [time, setTime] = useState(3);
  const t = project.tracks[0];
  const target = {
    project,
    clip: t.clips[0],
    track: t,
    media: project.media[0],
    t: 0,
    srcT: 1,
    edit: (fn: (p: Project) => Project) => setProject(fn),
    picking: false,
  } as unknown as TabProps;
  return (
    <div className="flex h-screen flex-col gap-2 p-2">
      <div className="flex gap-2" style={{ height: 520 }}>
        <div id="effects" className="frost w-[360px] overflow-auto p-2">
          <EffectBrowser target={target} note="" />
        </div>
        <div id="effects-off" className="frost w-[360px] overflow-auto p-2">
          <EffectBrowser note="Select a clip to add an effect." />
        </div>
        <div id="filters" className="frost w-[360px] overflow-auto p-2">
          <FiltersBrowser target={target} note="" />
        </div>
        <div id="transitions" className="frost w-[360px] overflow-auto">
          <TransitionsBrowser onPick={() => undefined} note="Click a transition to use it on the selected clip's cut." />
        </div>
      </div>
      <div id="timeline" className="frost p-2">
        <MultiTimeline project={project} outTime={time} onSeek={setTime} apply={(fn) => setProject(fn)} selection={selection} onSelect={setSelection} />
      </div>
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <Page />
  </StrictMode>,
);
