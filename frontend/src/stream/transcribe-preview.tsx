import React from "react";
import ReactDOM from "react-dom/client";
import "../index.css";
import { TranscribeBar } from "@/stream/Transcripts";
import type { StreamState } from "@/lib/api";

// transcribe-preview.html (Vite only, never built): the Game Room call
// card's transcription bar in each state of this PC's speech model, with
// Go's answers faked, so the notes can be seen and screenshotted without a
// call. Go side: internal/transcript (engine.go).

const realFetch = window.fetch.bind(window);
window.fetch = async (input, init) => {
  const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
  if (!url.includes("/wails/runtime")) return realFetch(input, init);
  return new Response("not in the preview", { status: 500 });
};

const gpuBytes = 735_571_214;
const base = {
  phase: "ready",
  done: 0,
  total: 68_876_053,
  error: "",
  model: "whisper base (multilingual)",
  threads: 4,
  running: true,
  language: "auto",
  device: "cpu",
  gpu: "",
  gpuReady: false,
  gpuFetching: false,
  gpuDone: 0,
  gpuTotal: gpuBytes,
  gpuError: "",
  gpuFailed: false,
};
const rtx = "NVIDIA GeForce RTX 5080";

const cases: { title: string; engine: Partial<typeof base> }[] = [
  { title: "PC without an NVIDIA GPU: nothing new, whisper runs on the CPU", engine: {} },
  { title: "NVIDIA GPU, GPU build not downloaded: its size shows before it starts", engine: { gpu: rtx } },
  { title: "NVIDIA GPU, GPU build downloading", engine: { gpu: rtx, gpuFetching: true, gpuDone: 312_000_000 } },
  { title: "NVIDIA GPU, GPU build in use", engine: { gpu: rtx, gpuReady: true, device: "gpu" } },
  {
    title: "NVIDIA GPU, GPU build failed this session: back on the CPU",
    engine: { gpu: rtx, gpuReady: true, gpuFailed: true, gpuError: "the GPU build started without the GPU" },
  },
];

function state(engine: Partial<typeof base>): StreamState {
  return {
    call: {
      inCall: true,
      transcribe: { on: true, by: "Stephen", since: Date.now() - 120_000, transcript: "", mine: true, engine: { ...base, ...engine } },
    },
  } as unknown as StreamState;
}

function Preview() {
  return (
    <div className="flex min-h-screen flex-col gap-6 bg-background p-8 text-foreground">
      {cases.map((c) => (
        <section key={c.title} className="flex max-w-3xl flex-col gap-2" data-case={c.title}>
          <h2 className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{c.title}</h2>
          <div className="border border-border bg-card p-4">
            <TranscribeBar state={state(c.engine)} onState={() => {}} />
          </div>
        </section>
      ))}
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <Preview />
  </React.StrictMode>,
);
