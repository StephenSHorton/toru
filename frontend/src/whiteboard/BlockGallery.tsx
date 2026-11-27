// The block gallery: a collapsible panel down the whiteboard's left side with
// a tile for every block catalog type (catalog.ts), each a small live render
// of the block (blocks.tsx) under its name. Drag a tile onto the board to put
// the block where it's dropped, or press it (click, Enter, Space) to put it in
// the middle of what's in view. Either way it's one board change, so one undo.
//
// It renders inside <Board> (it needs useBoard) and routes/Whiteboard.tsx only
// mounts it. The previews are inert and hidden from UI SDK's agent bridge, so
// board.snapshot lists the board's controls and not theirs; the tiles are UI
// SDK buttons ("Add Sticky note"), which agents can press with board.act.
//
// Collapsed, the panel is a thin rail; open or not is kept in localStorage.
// `sections` lets another source add its own tiles, like a person's own
// blocks; an empty section isn't shown.
import * as React from "react";
import { createPortal } from "react-dom";
import { Button } from "@ui-sdk/react/button";
import { PanelLeftCloseIcon, PanelLeftOpenIcon } from "lucide-react";
import { useBoard } from "./BoardView";
import type { BoardBlock } from "./board";
import { BlockBody } from "./blocks";
import { blockLabel, blockTypes, newProps, type BlockType } from "./catalog";
import "./block-gallery.css";

/** One tile: what it places (type, size, props) and what its preview shows. */
type GalleryItem = {
  type: string;
  label: string;
  description: string;
  w: number;
  h: number;
  /** The new block's props. */
  props: Record<string, unknown>;
  /** What the tile's preview shows: filled in, so it reads as that block. */
  preview: Record<string, unknown>;
  /** The size the preview renders at before it's scaled down, when not w × h; top is room above it, like a frame's name. */
  previewSize?: { w: number; h: number; top?: number };
};
type GallerySection = { id: string; title: string; items: GalleryItem[] };

// Filled-in props for each type's preview. A type missing here previews its
// new props, so a new catalog type still gets a tile.
const previewProps: Partial<Record<BlockType, Record<string, unknown>>> = {
  sticky: { text: "A quick idea", color: "yellow" },
  text: { text: "Heading", size: "xl" },
  card: { title: "Card", body: "A few lines about it.", color: "blue" },
  checklist: { title: "To do", items: [{ text: "First", done: true }, { text: "Second" }, { text: "Third" }] },
  poll: { question: "Which one?", options: [{ label: "This", votes: 3 }, { label: "That", votes: 1 }] },
  rating: { title: "Rating", value: 4, max: 5 },
  table: { title: "Table", columns: ["Task", "Owner"], rows: [["Plan", "You"], ["Build", "Agent"]] },
  "tag-list": { title: "Tags", items: ["ideas", "team", "agents"] },
  image: {},
  frame: { name: "Frame", color: "blue" },
};

// A frame is big and nearly empty: its preview is a smaller one, so its name
// (which sits above the frame) reads.
const previewSizes: Partial<Record<BlockType, { w: number; h: number; top?: number }>> = {
  frame: { w: 240, h: 130, top: 28 },
};

/** The catalog's tiles: blocks, then frames (the catalog's region types). */
function catalogSections(): GallerySection[] {
  const item = (t: (typeof blockTypes)[number]): GalleryItem => {
    const props = structuredClone((newProps as Record<string, unknown>)[t.type] ?? {}) as Record<string, unknown>;
    return {
      type: t.type,
      label: t.label,
      description: t.description,
      w: t.w,
      h: t.h,
      props,
      preview: previewProps[t.type as BlockType] ?? props,
      previewSize: previewSizes[t.type as BlockType],
    };
  };
  return [
    { id: "blocks", title: "Blocks", items: blockTypes.filter((t) => !t.region).map(item) },
    { id: "frames", title: "Frames", items: blockTypes.filter((t) => t.region).map(item) },
  ];
}

const openKey = "toru.whiteboard.gallery.open";
const grid = 8;
const snap = (v: number) => Math.round(v / grid) * grid;
// Pixels a press moves before it's a drag and not a click.
const dragFrom = 4;
// The preview area in a tile, in pixels (block-gallery.css).
const previewW = 108;
const previewH = 72;

function readOpen() {
  try {
    return localStorage.getItem(openKey) !== "0";
  } catch {
    return true;
  }
}

function newId(blocks: Record<string, unknown>) {
  let id = "";
  do id = `blk-${crypto.randomUUID().replaceAll("-", "").slice(0, 12)}`;
  while (blocks[id]);
  return id;
}

type Drag = { item: GalleryItem; pointer: number; x0: number; y0: number; x: number; y: number; moving: boolean };

/** The gallery panel. Render it inside <Board>. */
function BlockGallery({ sections = catalogSections() }: { sections?: GallerySection[] }) {
  const board = useBoard();
  const [open, setOpen] = React.useState(readOpen);
  const [drag, setDrag] = React.useState<Drag | null>(null);
  const dragRef = React.useRef<Drag | null>(null);
  // A drag ends in a click on the tile; this swallows it.
  const dragged = React.useRef(false);
  const panel = React.useRef<HTMLElement>(null);
  const listId = React.useId();

  const setOpenSaved = (next: boolean) => {
    setOpen(next);
    try {
      localStorage.setItem(openKey, next ? "1" : "0");
    } catch {
      // no storage: it stays as it is until the window closes
    }
  };

  const root = () => panel.current?.closest<HTMLElement>('[aria-roledescription="whiteboard"]') ?? null;

  /** Puts the item's block on the board with its middle at a canvas point. */
  const place = (item: GalleryItem, cx: number, cy: number, nudge: boolean) => {
    const blocks = board.blocks();
    let x = snap(cx - item.w / 2);
    let y = snap(cy - item.h / 2);
    // Pressing a tile again doesn't stack the new block exactly on the last.
    if (nudge) {
      while (Object.values(blocks).some((b) => b.x === x && b.y === y)) {
        x += grid * 3;
        y += grid * 3;
      }
    }
    const props = structuredClone(item.props);
    const id = newId(blocks);
    const box: BoardBlock = { label: blockLabel(item.type, props), type: item.type, props, x, y, width: item.w, height: item.h };
    // Selected once the board has it back from the window's live copy.
    if (board.addBlock(id, box)) requestAnimationFrame(() => board.select([id]));
  };

  /** Pressed (click, Enter, Space, an agent's press): the middle of what's in view, right of the panel. */
  const placeInView = (item: GalleryItem) => {
    const el = root();
    if (!el) return;
    const r = el.getBoundingClientRect();
    const left = Math.min(Math.max(panel.current?.getBoundingClientRect().right ?? r.left, r.left), r.right);
    const p = board.viewport.toCanvas((left + r.right) / 2, (r.top + r.bottom) / 2);
    place(item, p.x, p.y, true);
  };

  /** Whether a point on screen is on the board and not on the panel. */
  const onBoard = (x: number, y: number) => {
    const r = root()?.getBoundingClientRect();
    const p = panel.current?.getBoundingClientRect();
    if (!r || x < r.left || x > r.right || y < r.top || y > r.bottom) return false;
    return !p || x < p.left || x > p.right || y < p.top || y > p.bottom;
  };

  const update = (next: Drag | null) => {
    dragRef.current = next;
    setDrag(next);
  };

  const pointerDown = (item: GalleryItem) => (event: React.PointerEvent<HTMLElement>) => {
    if (event.button !== 0) return;
    dragged.current = false;
    (event.currentTarget as HTMLElement).setPointerCapture?.(event.pointerId);
    update({ item, pointer: event.pointerId, x0: event.clientX, y0: event.clientY, x: event.clientX, y: event.clientY, moving: false });
  };
  const pointerMove = (event: React.PointerEvent<HTMLElement>) => {
    const d = dragRef.current;
    if (!d || d.pointer !== event.pointerId) return;
    const moving = d.moving || Math.hypot(event.clientX - d.x0, event.clientY - d.y0) >= dragFrom;
    update({ ...d, x: event.clientX, y: event.clientY, moving });
  };
  const pointerUp = (event: React.PointerEvent<HTMLElement>) => {
    const d = dragRef.current;
    if (!d || d.pointer !== event.pointerId) return;
    update(null);
    if (!d.moving) return; // a click: onClick places it
    dragged.current = true;
    if (onBoard(event.clientX, event.clientY)) {
      const p = board.viewport.toCanvas(event.clientX, event.clientY);
      place(d.item, p.x, p.y, false);
    }
  };
  const pointerCancel = () => {
    if (dragRef.current?.moving) dragged.current = true;
    update(null);
  };

  // Escape drops a drag without placing anything.
  React.useEffect(() => {
    if (!drag?.moving) return;
    const keydown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      event.preventDefault();
      event.stopPropagation();
      dragged.current = true;
      update(null);
    };
    window.addEventListener("keydown", keydown, true);
    return () => window.removeEventListener("keydown", keydown, true);
  }, [drag?.moving]);

  const zoom = board.viewport.get().zoom;
  const shown = sections.filter((s) => s.items.length);

  return (
    <aside
      ref={panel}
      data-slot="block-gallery"
      data-open={open ? "" : undefined}
      aria-label="Block gallery"
      className="frost"
      // The board under the panel doesn't pan, zoom or take its keys.
      onWheel={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.stopPropagation()}
    >
      {open ? (
        <>
          <header data-slot="block-gallery-header">
            <h2>Add to board</h2>
            <Button aria-label="Hide blocks" title="Hide blocks" aria-expanded="true" aria-controls={listId} onClick={() => setOpenSaved(false)}>
              <PanelLeftCloseIcon aria-hidden="true" />
            </Button>
          </header>
          <div id={listId} data-slot="block-gallery-list">
            {shown.map((section) => (
              <section key={section.id} data-slot="block-gallery-section" aria-labelledby={`${listId}-${section.id}`}>
                <h3 id={`${listId}-${section.id}`}>{section.title}</h3>
                <div data-slot="block-gallery-grid">
                  {section.items.map((item) => (
                    // The preview sits behind the tile's button, not in it:
                    // blocks hold buttons of their own.
                    <div key={item.type} data-slot="block-gallery-tile" data-dragging={drag?.moving && drag.item === item ? "" : undefined}>
                      <TilePreview item={item} />
                    <Button
                      data-part="block-gallery-tile"
                      data-gallery-type={item.type}
                      aria-label={`Add ${item.label}`}
                      title={`${item.description} Drag it onto the board, or press to add it in the middle.`}
                      onPointerDown={pointerDown(item)}
                      onPointerMove={pointerMove}
                      onPointerUp={pointerUp}
                      onPointerCancel={pointerCancel}
                      onClick={() => {
                        if (dragged.current) {
                          dragged.current = false;
                          return;
                        }
                        placeInView(item);
                      }}
                    >
                      <span data-slot="block-gallery-name">{item.label}</span>
                    </Button>
                    </div>
                  ))}
                </div>
              </section>
            ))}
          </div>
        </>
      ) : (
        <Button
          data-part="block-gallery-rail"
          aria-label="Show blocks"
          title="Show blocks"
          aria-expanded="false"
          onClick={() => setOpenSaved(true)}
        >
          <PanelLeftOpenIcon aria-hidden="true" />
          <span data-slot="block-gallery-rail-label">Blocks</span>
        </Button>
      )}
      {drag?.moving &&
        // On the page, not in the panel: the panel's frosted glass would be
        // the ghost's frame for position: fixed.
        createPortal(
        <div
          data-slot="block-gallery-ghost"
          data-over-board={onBoard(drag.x, drag.y) ? "" : undefined}
          aria-hidden="true"
          style={{
            left: drag.x - (drag.item.w * zoom) / 2,
            top: drag.y - (drag.item.h * zoom) / 2,
            width: drag.item.w * zoom,
            height: drag.item.h * zoom,
          }}
        >
          <span>{drag.item.label}</span>
        </div>,
        document.body,
      )}
    </aside>
  );
}

// The block itself at its new size, scaled into the tile. Inert: a picture
// of the block, never its controls, and the agent bridge doesn't see it.
const TilePreview = React.memo(function TilePreview({ item }: { item: GalleryItem }) {
  const { w, h } = item.previewSize ?? item;
  const top = item.previewSize?.top ?? 0;
  const scale = Math.min(previewW / w, previewH / (h + top));
  return (
    <span data-slot="block-gallery-preview" aria-hidden="true" data-agent-hidden="" inert>
      <span
        data-slot="board-block"
        data-type={item.type}
        style={{ width: w, height: h, transform: `translate(-50%, calc(-50% + ${(top * scale) / 2}px)) scale(${scale})` }}
      >
        <span data-slot="board-block-body">
          <BlockBody id={`gallery-${item.type}`} type={item.type} props={item.preview} onProps={() => {}} />
        </span>
      </span>
    </span>
  );
});

export { BlockGallery, catalogSections, previewProps, type GalleryItem, type GallerySection };
