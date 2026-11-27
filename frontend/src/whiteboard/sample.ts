// The sample board the window opens on until boards come from Go (the live
// board card): one block of every catalog type. sample-board.json is in the
// board file's shape (blocks, frames, links), and Go's catalog test checks
// every block in it against the catalog.
import { facing, type BoardBlocks, type BoardLine } from "./board";
import { blockLabel } from "./catalog";
import sample from "./sample-board.json";

type FileBlock = { id: string; type: string; x: number; y: number; w: number; h: number; props: Record<string, unknown> };
type FileFrame = { id: string; name: string; x: number; y: number; w: number; h: number; color?: string };
type FileLink = { id: string; from: string; to: string };
type BoardFile = { blocks: FileBlock[]; frames: FileFrame[]; links: FileLink[] };

/**
 * A board file's blocks and frames as the canvas's blocks, frames first so
 * they sit behind; its links as lines between facing sides.
 */
function fromBoardFile(file: BoardFile): { blocks: BoardBlocks; lines: BoardLine[] } {
  const blocks: BoardBlocks = {};
  for (const f of file.frames) {
    const props: Record<string, unknown> = { name: f.name, ...(f.color ? { color: f.color } : {}) };
    blocks[f.id] = { label: blockLabel("frame", props), type: "frame", props, x: f.x, y: f.y, width: f.w, height: f.h };
  }
  for (const b of file.blocks) {
    blocks[b.id] = { label: blockLabel(b.type, b.props), type: b.type, props: b.props, x: b.x, y: b.y, width: b.w, height: b.h };
  }
  const lines = file.links
    .filter((l) => blocks[l.from] && blocks[l.to])
    .map((l) => {
      const [a, b] = facing(blocks[l.from], blocks[l.to]);
      return { id: l.id, from: `${l.from}:${a}`, to: `${l.to}:${b}` };
    });
  return { blocks, lines };
}

const sampleBoard = fromBoardFile(sample as BoardFile);

export { fromBoardFile, sampleBoard, type BoardFile };
