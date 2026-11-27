// A board exported as one HTML file (docs/whiteboard.md section 3): the board
// window's own canvas and blocks, built into a single file with nothing to
// fetch (vite.board-export.config.ts), and the board and its pictures in the
// page's #toru-board script, which Go's board export fills
// (internal/boardexport). Opened in any browser, offline, it stays live:
// polls vote, checklists tick, notes take typing, blocks move. Changes stay in
// the page (memoryLink); nothing reaches Toru.
//
// With #print in its address, Go's export WebView2 (internal/webprint) uses
// it to print the board to PDF or draw it as a PNG: the page swaps the canvas
// for a still copy of the whole board at zoom 1 (whiteboard/snapshot.ts),
// says "ready:{width,height}", and toruExport.png(scale) sends the picture
// back as "tile:" messages and a "png:" message.
import React, { useCallback, useEffect, useState, useSyncExternalStore } from "react";
import ReactDOM from "react-dom/client";
import { Button } from "@ui-sdk/react/button";
import "../index.css";
import "@/whiteboard/toru-ui-sdk.css";
import { Board, useBoard } from "@/whiteboard/BoardView";
import type { BoardBlocks } from "@/whiteboard/board";
import { BlockBody, type PropsChange } from "@/whiteboard/blocks";
import { blockLabel } from "@/whiteboard/catalog";
import type { BoardDoc } from "@/whiteboard/document";
import { BoardImagesContext, type BoardImages } from "@/whiteboard/images";
import { emptyBoard, memoryLink } from "@/whiteboard/links";
import { LiveBoard } from "@/whiteboard/live";
import { rasterize, snapshotBoard, type Snapshot } from "@/whiteboard/snapshot";
import sample from "@/whiteboard/sample-board.json";
import "./board-export.css";

type Payload = { board: BoardDoc; images?: Record<string, string>; exportedAt?: string };

declare global {
  interface Window {
    chrome?: { webview?: { postMessage: (m: string) => void } };
    toruExport?: { png: (scale: number) => Promise<void> };
  }
}

/** The board this file carries; the sample board when it's opened unfilled (Vite's dev server). */
function payload(): Payload {
  const text = document.getElementById("toru-board")?.textContent ?? "null";
  const p = JSON.parse(text) as Payload | null;
  if (p?.board) return p;
  const { frames, blocks, links } = structuredClone(sample) as Partial<BoardDoc>;
  return { board: { ...emptyBoard("sample", "Sample board"), frames: frames ?? [], blocks: blocks ?? [], links: links ?? [] } };
}

/** Tells Go's export WebView2, when there is one. */
const tell = (m: string) => window.chrome?.webview?.postMessage(m);

const noChange = () => Promise.reject(new Error("Pictures can't be changed in an exported board."));

function imagesOf(p: Payload): BoardImages {
  const images = p.images ?? {};
  return {
    url: (asset) => images[asset] ?? "",
    importFile: noChange,
    library: async () => [],
    importLibrary: noChange,
  };
}

function ExportedBoard({ data, print }: { data: Payload; print: boolean }) {
  const [live] = useState(() => new LiveBoard(data.board.id, memoryLink(data.board)));
  const { view, doc } = useSyncExternalStore(live.subscribe, live.getState);
  useEffect(() => {
    void live.start();
    return () => live.stop();
  }, [live]);

  const setProps = useCallback(
    (blockId: string, props: Record<string, unknown>, _details: PropsChange) => {
      const blocks = live.getState().view.blocks;
      const block = blocks[blockId];
      if (!block?.type) return;
      live.edit({ blocks: { ...blocks, [blockId]: { ...block, props, label: blockLabel(block.type, props) } } });
    },
    [live],
  );
  const renderBlock = useCallback(
    (blockId: string, all: BoardBlocks) => {
      const block = all[blockId];
      if (!block.type) return block.label;
      return <BlockBody id={blockId} type={block.type} props={block.props ?? {}} onProps={(next, details) => setProps(blockId, next, details)} />;
    },
    [setProps],
  );

  return (
    <div className="relative h-full overflow-hidden">
      <Board
        aria-label={`Whiteboard ${data.board.name}`}
        className="h-full w-full"
        blocks={view.blocks}
        onBlocksChange={(blocks) => live.edit({ blocks })}
        lines={view.lines}
        onLinesChange={(lines) => live.edit({ lines })}
        renderBlock={renderBlock}
      >
        {!print && <Header name={data.board.name} exportedAt={data.exportedAt} />}
      </Board>
      {print && doc && <Printer blocks={view.blocks} />}
    </div>
  );
}

function Header({ name, exportedAt }: { name: string; exportedAt?: string }) {
  const board = useBoard();
  const when = exportedAt ? new Date(exportedAt) : null;
  return (
    <div data-slot="export-header" className="frost absolute top-3 left-1/2 flex -translate-x-1/2 items-center gap-3 px-3 py-2" onWheel={(e) => e.stopPropagation()}>
      <strong>{name}</strong>
      <span data-slot="export-note">
        Exported from Toru{when && !Number.isNaN(when.getTime()) ? ` on ${when.toLocaleDateString()}` : ""}. Changes stay in this page.
      </span>
      <Button onClick={() => board.fit()}>Fit</Button>
    </div>
  );
}

// Print mode: once the board has rendered, swap it for a still copy of the
// whole board, and tell Go its size.
function Printer({ blocks }: { blocks: BoardBlocks }) {
  useEffect(() => {
    let snap: Snapshot | null = null;
    // Let the blocks' first effects run (UI SDK parts settle after mount).
    const t = setTimeout(async () => {
      try {
        const root = document.querySelector<HTMLElement>('[data-slot="board"]');
        if (!root) throw new Error("no board to print");
        await Promise.all(Array.from(root.querySelectorAll("img")).map((img) => img.decode().catch(() => {})));
        snap = snapshotBoard(root, blocks, document.getElementById("print")!);
        document.documentElement.dataset.printing = "";
        document.documentElement.style.setProperty("--print-width", `${snap.width}px`);
        document.documentElement.style.setProperty("--print-height", `${snap.height}px`);
        const s = snap;
        window.toruExport = {
          png: async (scale) => {
            try {
              const done = await rasterize(s, scale, (tile) => tell(`tile:${JSON.stringify(tile)}`));
              tell(`png:${JSON.stringify(done)}`);
            } catch (e) {
              tell(`error:${e instanceof Error ? e.message : String(e)}`);
            }
          },
        };
        tell(`ready:${JSON.stringify({ width: snap.width, height: snap.height })}`);
      } catch (e) {
        tell(`error:${e instanceof Error ? e.message : String(e)}`);
      }
    }, 50);
    return () => {
      clearTimeout(t);
      snap?.remove();
    };
    // Once, for the board as it loaded.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return null;
}

const data = payload();
const print = window.location.hash === "#print";
document.title = data.board.name;
if (print) {
  const box = document.createElement("div");
  box.id = "print";
  document.body.appendChild(box);
}
window.addEventListener("error", (e) => tell(`error:${e.message}`));

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>
    <BoardImagesContext.Provider value={imagesOf(data)}>
      <ExportedBoard data={data} print={print} />
    </BoardImagesContext.Provider>
  </React.StrictMode>,
);
