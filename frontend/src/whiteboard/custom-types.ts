// Custom block types (whiteboard#27): block types agents designed with
// board.block.define, as Go's board.BlockDef. A board carries a copy of
// each one its blocks use (BoardDoc.types); the person's own library comes
// from Go's BlockLibraryService. This module keeps both for the window, so
// catalog.ts, blocks.tsx and the gallery can look a type up by name: the
// board's copy first (it is what the board's blocks were made with), then
// the library.
//
// A definition is data only (internal/board/blockparts.json): a props
// schema, defaults and a layout tree that custom-blocks.tsx draws with
// Toru's own UI SDK parts. Nothing in it is run as markup or code.
import partsJSON from "../../../internal/board/blockparts.json";

type LayoutNode = { part: string; children?: LayoutNode[] } & Record<string, unknown>;

/** A custom block type, as Go's board.BlockDef. */
type BlockDef = {
  type: string;
  label: string;
  description?: string;
  icon: string;
  w: number;
  h: number;
  props: Record<string, unknown>;
  defaults: Record<string, unknown>;
  layout: LayoutNode;
  nameProp?: string;
  version: number;
  origin: string;
};

const blockParts = partsJSON as unknown as {
  parts: Record<string, { description: string; children?: boolean }>;
  icons: string[];
};

let boardTypes = new Map<string, BlockDef>();
let libraryTypes = new Map<string, BlockDef>();
let libraryList: BlockDef[] = [];
let stamp = 0;
const listeners = new Set<() => void>();

function changed() {
  stamp++;
  for (const l of listeners) l();
}

const sameDefs = (a: Map<string, BlockDef>, b: BlockDef[]) =>
  a.size === b.length && b.every((d) => {
    const x = a.get(d.type);
    return !!x && x.origin === d.origin && x.version === d.version;
  });

/** The custom types the open board carries (BoardDoc.types). */
function setBoardTypes(types: BlockDef[] | undefined) {
  const list = Array.isArray(types) ? types.filter(isDef) : [];
  if (sameDefs(boardTypes, list)) return;
  boardTypes = new Map(list.map((d) => [d.type, d]));
  changed();
}

/** The person's own block library (BlockLibraryService.List). */
function setLibraryTypes(types: BlockDef[] | undefined) {
  const list = Array.isArray(types) ? types.filter(isDef) : [];
  if (sameDefs(libraryTypes, list) && list.every((d, i) => libraryList[i]?.type === d.type)) return;
  libraryList = list;
  libraryTypes = new Map(list.map((d) => [d.type, d]));
  changed();
}

/** Enough of a definition to draw: anything less is shown as a placeholder. */
function isDef(d: unknown): d is BlockDef {
  if (!d || typeof d !== "object") return false;
  const x = d as Partial<BlockDef>;
  return (
    typeof x.type === "string" &&
    typeof x.label === "string" &&
    !!x.layout &&
    typeof x.layout === "object" &&
    !!x.props &&
    typeof x.props === "object"
  );
}

/** A custom type by name: the board's copy, else the library's. */
function customType(type: string): BlockDef | undefined {
  return boardTypes.get(type) ?? libraryTypes.get(type);
}

/** The board's own copy of a type, if it carries one. */
const boardType = (type: string) => boardTypes.get(type);

/** The library's copy of a type, if the person has it. */
const libraryType = (type: string) => libraryTypes.get(type);

/** The person's own blocks, by label, for the gallery. */
const libraryBlocks = () => libraryList;

/**
 * Whether the board's copy of a type is something the person could save to
 * their library: the library lacks it, or has an older version of the
 * same block. "clash": the library has a different block of that name.
 */
function saveState(type: string): "saved" | "new" | "newer" | "clash" | "none" {
  const b = boardTypes.get(type);
  if (!b) return "none";
  const l = libraryTypes.get(type);
  if (!l) return "new";
  if (l.origin !== b.origin) return "clash";
  return b.version > l.version ? "newer" : "saved";
}

/** A name the library doesn't have yet, from type: type-2, type-3... */
function freeTypeName(type: string) {
  for (let i = 2; ; i++) {
    const base = type.slice(0, 40 - String(i).length - 1);
    const name = `${base}-${i}`;
    if (!libraryTypes.has(name)) return name;
  }
}

type SaveFn = (type: string, as: string) => Promise<unknown>;
let save: SaveFn | null = null;

/** How the window saves a board's custom type to the library; null where it can't (an exported page). */
function setSaveHandler(f: SaveFn | null) {
  save = f;
  changed();
}
const saveHandler = () => save;

/** For React's useSyncExternalStore: changes whenever either list does. */
const subscribeTypes = (l: () => void) => {
  listeners.add(l);
  return () => listeners.delete(l);
};
const typesStamp = () => stamp;

export {
  blockParts,
  boardType,
  customType,
  freeTypeName,
  isDef,
  libraryBlocks,
  libraryType,
  saveHandler,
  saveState,
  setSaveHandler,
  setBoardTypes,
  setLibraryTypes,
  subscribeTypes,
  typesStamp,
  type BlockDef,
  type LayoutNode,
};
