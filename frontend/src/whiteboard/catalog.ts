// The block catalog: every block type a board can hold, with its props as
// JSON Schema. It is internal/board/catalog.json, the same file Go embeds for
// board.catalog and for checking the props agents send, so the window and Go
// always agree. blocks.tsx renders each type; scripts/blocks-check.ts fails
// when a type has no render or a render has no type.
import catalogJSON from "../../../internal/board/catalog.json";

type Schema = {
  type?: string;
  description?: string;
  properties?: Record<string, Schema>;
  required?: string[];
  additionalProperties?: boolean;
  items?: Schema;
  enum?: string[];
  minLength?: number;
  maxLength?: number;
  minItems?: number;
  maxItems?: number;
  minimum?: number;
  maximum?: number;
};

type BlockTypeInfo = {
  type: string;
  label: string;
  description: string;
  /** A type that isn't a block in the board file: frame is the board's frames list. */
  region?: boolean;
  w: number;
  h: number;
  props: Schema;
};

const blockColors = catalogJSON.colors as readonly string[];
type BlockColor = "yellow" | "pink" | "blue" | "green" | "purple" | "orange" | "grey";

type StickyProps = { text: string; color?: BlockColor; tags?: string[] };
type TextProps = { text: string; size?: "s" | "m" | "l" | "xl"; align?: "left" | "center" | "right"; tags?: string[] };
type CardProps = { title: string; body?: string; color?: BlockColor; tags?: string[] };
type ChecklistItem = { text: string; done?: boolean };
type ChecklistProps = { title?: string; items: ChecklistItem[]; tags?: string[] };
type PollOption = { label: string; votes?: number };
type PollProps = { question: string; options: PollOption[]; multi?: boolean; tags?: string[] };
type RatingProps = { title: string; value?: number; max?: number; tags?: string[] };
type TableProps = { title?: string; columns: string[]; rows?: string[][]; tags?: string[] };
type TagListProps = { title: string; items: string[]; tags?: string[] };
type ImageProps = { asset?: string; alt?: string; caption?: string; fit?: "contain" | "cover"; tags?: string[] };
type FrameProps = { name: string; color?: BlockColor };

/** Each type's props, as its schema in catalog.json describes them. */
type BlockPropsByType = {
  sticky: StickyProps;
  text: TextProps;
  card: CardProps;
  checklist: ChecklistProps;
  poll: PollProps;
  rating: RatingProps;
  table: TableProps;
  "tag-list": TagListProps;
  image: ImageProps;
  frame: FrameProps;
};
type BlockType = keyof BlockPropsByType;

const blockTypes = catalogJSON.types as unknown as BlockTypeInfo[];
const byType = new Map(blockTypes.map((t) => [t.type, t]));

const blockType = (type: string) => byType.get(type);

/** A new block's props: the least its schema needs. */
const newProps: { [T in BlockType]: BlockPropsByType[T] } = {
  sticky: { text: "" },
  text: { text: "" },
  card: { title: "New card" },
  checklist: { title: "Checklist", items: [] },
  poll: { question: "New poll", options: [{ label: "Yes" }, { label: "No" }] },
  rating: { title: "New rating" },
  table: { title: "Table", columns: ["Name", "Notes"], rows: [["", ""]] },
  "tag-list": { title: "Tags", items: [] },
  image: {},
  frame: { name: "Frame" },
};

/** The first line of some text, cut to a few words, for a name. */
function excerpt(text: string, max = 40) {
  const line = text.trim().split("\n")[0]?.trim() ?? "";
  return line.length > max ? `${line.slice(0, max - 1).trimEnd()}…` : line;
}

/**
 * A block's name: its type and what it says, like "Sticky note: Faster
 * capture". The board names lines and the agent bridge's blocks by it.
 */
function blockLabel(type: string, props: Record<string, unknown> = {}) {
  const kind = blockType(type)?.label ?? type;
  const str = (key: string) => (typeof props[key] === "string" ? (props[key] as string) : "");
  const what =
    type === "sticky" || type === "text"
      ? excerpt(str("text"))
      : type === "poll"
        ? excerpt(str("question"))
        : type === "frame"
          ? excerpt(str("name"))
          : type === "image"
            ? excerpt(str("alt") || str("caption"))
            : excerpt(str("title"));
  return what ? `${kind}: ${what}` : kind;
}

/**
 * Why a value breaks a schema, or null: the same keywords and messages as
 * Go's checkSchema (internal/board/catalog.go), which refuses what this does.
 */
function checkSchema(s: Schema, v: unknown, at: string): string | null {
  switch (s.type) {
    case "object": {
      if (typeof v !== "object" || v === null || Array.isArray(v)) return `${at} must be an object`;
      const obj = v as Record<string, unknown>;
      for (const r of s.required ?? []) if (!(r in obj)) return `${at} needs "${r}"`;
      for (const k of Object.keys(obj).sort()) {
        const sub = s.properties?.[k];
        if (!sub) {
          if (s.additionalProperties === false) return `${at} has no field "${k}"`;
          continue;
        }
        const err = checkSchema(sub, obj[k], `${at}.${k}`);
        if (err) return err;
      }
      return null;
    }
    case "array": {
      if (!Array.isArray(v)) return `${at} must be a list`;
      if (s.minItems !== undefined && v.length < s.minItems) return `${at} needs at least ${s.minItems} items`;
      if (s.maxItems !== undefined && v.length > s.maxItems) return `${at} has more than ${s.maxItems} items`;
      if (s.items) {
        for (let i = 0; i < v.length; i++) {
          const err = checkSchema(s.items, v[i], `${at}[${i}]`);
          if (err) return err;
        }
      }
      return null;
    }
    case "string": {
      if (typeof v !== "string") return `${at} must be text`;
      const n = [...v].length;
      if (s.minLength !== undefined && n < s.minLength) return `${at} is too short`;
      if (s.maxLength !== undefined && n > s.maxLength) return `${at} is too long (${s.maxLength} characters at most)`;
      if (s.enum && !s.enum.includes(v)) return `${at} must be one of ${s.enum.join(", ")}`;
      return null;
    }
    case "boolean":
      return typeof v === "boolean" ? null : `${at} must be true or false`;
    case "integer":
    case "number": {
      if (typeof v !== "number" || !Number.isFinite(v)) return `${at} must be a number`;
      if (s.type === "integer" && !Number.isInteger(v)) return `${at} must be a whole number`;
      if (s.minimum !== undefined && v < s.minimum) return `${at} must be at least ${s.minimum}`;
      if (s.maximum !== undefined && v > s.maximum) return `${at} must be at most ${s.maximum}`;
      return null;
    }
  }
  return null;
}

/** Why a block's props break its type's schema, or null when Go would take them. */
function checkProps(type: string, props: unknown): string | null {
  const info = blockType(type);
  if (!info) return `no block type "${type}"`;
  return checkSchema(info.props, props, "props");
}

export {
  blockColors,
  checkProps,
  blockLabel,
  blockType,
  blockTypes,
  excerpt,
  newProps,
  type BlockColor,
  type BlockPropsByType,
  type BlockType,
  type BlockTypeInfo,
  type CardProps,
  type ChecklistItem,
  type ChecklistProps,
  type FrameProps,
  type ImageProps,
  type PollOption,
  type PollProps,
  type RatingProps,
  type Schema,
  type StickyProps,
  type TableProps,
  type TagListProps,
  type TextProps,
};
