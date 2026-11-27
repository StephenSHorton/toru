// board-perf.html (Vite only, never built): the whiteboard window on a board
// kept in memory, for scripts/board-perf.ts. The script puts a .toruboard's
// document in window.__perfBoard before the page loads (else the sample board
// opens), then drives the board with synthetic input and reads the frame
// times window.__perf records.
import ReactDOM from "react-dom/client";
import "../index.css";
import Whiteboard from "@/routes/Whiteboard";
import type { BoardDoc } from "./document";
import { memoryLink } from "./links";
import { LiveBoard } from "./live";
import { sampleDoc } from "./window";

declare global {
  interface Window {
    __perfBoard?: BoardDoc;
    __perf?: { start(): void; stop(): number[] };
  }
}

// The time between frames, from requestAnimationFrame, while recording.
let frames: number[] = [];
let recording = false;
let last = 0;
const tick = (t: number) => {
  if (recording && last) frames.push(t - last);
  last = t;
  requestAnimationFrame(tick);
};
requestAnimationFrame(tick);
window.__perf = {
  start() {
    frames = [];
    last = 0;
    recording = true;
  },
  stop() {
    recording = false;
    return frames;
  },
};

const doc = window.__perfBoard ?? sampleDoc();
const live = new LiveBoard(doc.id, memoryLink(doc));
ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(<Whiteboard live={live} />);
