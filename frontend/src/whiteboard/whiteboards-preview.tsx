import React from "react";
import ReactDOM from "react-dom/client";
import "../index.css";
import Settings, { CaptureCard } from "@/routes/Settings";
import { SendToWhiteboard, WhiteboardsContext, type WhiteboardSummary, type WhiteboardsApi } from "./WhiteboardsPage";
import type { CaptureItem } from "@/lib/api";

// whiteboards-preview.html (Vite only, never built): the main window's
// Whiteboards page and the Library's Send to whiteboard, with boards kept in
// memory instead of Go's WhiteboardsService, so they can be seen and
// screenshotted without Toru running. ?page=whiteboards is the hub on the
// Whiteboards page; ?page=send is a Library screenshot, with its chooser
// open when &open is added.

const params = new URLSearchParams(window.location.search);
const ago = (min: number) => new Date(Date.now() - min * 60_000).toISOString();

const boards: WhiteboardSummary[] = [
  { id: "brd-1", name: "Toru launch plan", blocks: 24, updatedAt: ago(12) },
  { id: "brd-2", name: "Bug triage: capture overlay", blocks: 9, updatedAt: ago(60 * 5) },
  { id: "brd-3", name: "Retro, week 47", blocks: 31, updatedAt: ago(60 * 26) },
  { id: "brd-4", name: "Untitled whiteboard", blocks: 0, updatedAt: ago(60 * 50) },
  { id: "brd-5", name: "brd-5", blocks: 0, updatedAt: ago(60 * 80), error: "not a Toru board file (no \"format\": \"toruboard\")" },
];

const listeners = new Set<() => void>();
const memory: WhiteboardsApi = {
  list: async () => boards.slice(),
  create: async (name) => {
    const b = { id: `brd-${boards.length + 1}`, name: name || "Untitled whiteboard", blocks: 0, updatedAt: ago(0) };
    boards.unshift(b);
    listeners.forEach((f) => f());
    return b;
  },
  open: async () => {},
  pickImport: async () => null,
  sendScreenshot: async (id) => boards.find((b) => b.id === id) ?? boards[0],
  onChange: (f) => {
    listeners.add(f);
    return () => listeners.delete(f);
  },
};

const shot = {
  id: "shot-1",
  kind: "image",
  label: "Screenshot 2026-11-27 11.42",
  path: "C:\\captures\\334fac5220a72cdbb351ea83b8e4a5d48d7bb81d438ba78bf94cd863669c346b.png",
  takenAt: ago(3),
} as unknown as CaptureItem;

function SendPreview() {
  const [open, setOpen] = React.useState(params.has("open"));
  return (
    <div className="p-6">
      <h1 className="text-xl font-semibold tracking-tight">Library</h1>
      <p className="text-sm text-muted-foreground">Screenshots save here when you hit Done</p>
      <div className="mt-4 grid w-[56rem] grid-cols-3 gap-3">
        <CaptureCard
          item={shot}
          selected={false}
          onToggleSelect={() => {}}
          onOpen={() => {}}
          onEdit={() => {}}
          onSend={() => {}}
          onSendToBoard={() => setOpen(true)}
          onDelete={() => {}}
        />
      </div>
      <SendToWhiteboard shot={open ? shot : null} onClose={() => setOpen(false)} />
    </div>
  );
}

const page = params.get("page");

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <WhiteboardsContext.Provider value={memory}>
      {page === "send" ? <SendPreview /> : <div className="h-screen"><Settings /></div>}
    </WhiteboardsContext.Provider>
  </React.StrictMode>,
);
