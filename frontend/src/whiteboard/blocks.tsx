// What each block type in the catalog (catalog.ts) shows on the board, built
// from UI SDK components so people and agents fill them the same way: every
// control is named after its block, and every change comes back through
// onProps with UI SDK's change details (details.source is "agent" for an
// agent's change).
import * as React from "react";
import { Button } from "@ui-sdk/react/button";
import { Checkbox, CheckboxIndicator } from "@ui-sdk/react/checkbox";
import { FileInput, FileInputDropzone, FileInputTrigger } from "@ui-sdk/react/file-input";
import { Input } from "@ui-sdk/react/input";
import { Popover, PopoverContent, PopoverTitle, PopoverTrigger } from "@ui-sdk/react/popover";
import { Radio, RadioGroup, RadioIndicator } from "@ui-sdk/react/radio";
import { Rating, RatingItem } from "@ui-sdk/react/rating";
import { Textarea } from "@ui-sdk/react/textarea";
import { TokenInput, TokenInputEditor, type TokenInputValue } from "@ui-sdk/react/token-input";
import { ArrowDownIcon, ArrowUpIcon, CheckIcon, ImageIcon, PlusIcon, StarIcon } from "lucide-react";
import {
  blockLabel,
  checkProps,
  type BlockPropsByType,
  type BlockType,
  type CardProps,
  type ChecklistProps,
  type FrameProps,
  type ImageProps,
  type PollProps,
  type RatingProps,
  type StickyProps,
  type TableProps,
  type TagListProps,
  type TextProps,
} from "./catalog";
import { useBoardImages, type BoardImage, type LibraryShot } from "./images";
import "./blocks.css";

/** Who made a change, from UI SDK's change details. */
type PropsChange = { source?: string; reason?: string };

type BlockRenderProps<P> = {
  id: string;
  /** The block's name, from blockLabel. */
  label: string;
  props: P;
  /** False when the change was refused, as props the catalog's schema doesn't allow. */
  onProps: (next: P, details: PropsChange) => boolean;
};

const changeOf = (details: unknown): PropsChange => {
  const d = (details ?? {}) as PropsChange;
  return { source: d.source, reason: d.reason };
};

/** A text a schema needs non-empty: shown as typed, kept only once it has words. */
function useRequiredText(value: string, commit: (next: string, details: PropsChange) => boolean) {
  const [draft, setDraft] = React.useState(value);
  React.useEffect(() => setDraft(value), [value]);
  return {
    value: draft,
    onValueChange: (next: string, details: unknown) => {
      if (!next.trim() || commit(next, changeOf(details))) setDraft(next);
    },
    onBlur: () => setDraft(value),
  };
}

function Sticky({ label, props, onProps }: BlockRenderProps<StickyProps>) {
  return (
    <div data-slot="block-sticky" data-color={props.color ?? "yellow"}>
      <Textarea
        aria-label={label}
        maxLength={10000}
        placeholder="Write an idea"
        value={props.text}
        onValueChange={(text, details) => onProps({ ...props, text }, changeOf(details))}
      />
    </div>
  );
}

function Text({ label, props, onProps }: BlockRenderProps<TextProps>) {
  return (
    <div data-slot="block-text" data-size={props.size ?? "m"} data-align={props.align ?? "left"}>
      <Textarea
        aria-label={label}
        maxLength={20000}
        placeholder="Write something"
        value={props.text}
        onValueChange={(text, details) => onProps({ ...props, text }, changeOf(details))}
      />
    </div>
  );
}

function Card({ props, onProps }: BlockRenderProps<CardProps>) {
  const title = useRequiredText(props.title, (t, d) => onProps({ ...props, title: t }, d));
  return (
    <div data-slot="block-card" data-color={props.color ?? "grey"}>
      <div data-slot="block-card-title">
        <Input aria-label={`Card title: ${props.title}`} maxLength={200} {...title} />
      </div>
      <Textarea
        aria-label={`Card notes: ${props.title}`}
        maxLength={5000}
        placeholder="Notes"
        value={props.body ?? ""}
        onValueChange={(body, details) => onProps({ ...props, body }, changeOf(details))}
      />
    </div>
  );
}

function Checklist({ id, props, onProps }: BlockRenderProps<ChecklistProps>) {
  const name = props.title || "Checklist";
  const [draft, setDraft] = React.useState("");
  const add = (details: PropsChange) => {
    const text = draft.trim();
    if (!text) return;
    if (onProps({ ...props, items: [...props.items, { text }] }, details)) setDraft("");
  };
  const done = props.items.filter((item) => item.done).length;
  return (
    <div data-slot="block-checklist">
      <div data-slot="block-heading">
        <span>{name}</span>
        <span data-slot="block-count">
          {done}/{props.items.length}
        </span>
      </div>
      <ul data-slot="block-checklist-items">
        {props.items.map((item, i) => (
          <li key={`${id}-${i}`}>
            <label>
              <Checkbox
                checked={!!item.done}
                onCheckedChange={(checked, details) =>
                  onProps(
                    { ...props, items: props.items.map((it, j) => (j === i ? { ...it, done: checked === true } : it)) },
                    changeOf(details),
                  )
                }
              >
                <CheckboxIndicator>
                  <CheckIcon aria-hidden="true" />
                </CheckboxIndicator>
              </Checkbox>
              <span>{item.text}</span>
            </label>
          </li>
        ))}
      </ul>
      <div data-slot="block-add">
        <Input
          aria-label={`New item for ${name}`}
          maxLength={1000}
          placeholder="Add an item"
          value={draft}
          onValueChange={(v) => setDraft(v)}
          onKeyDown={(e) => {
            if (e.key === "Enter") add({ source: "person" });
          }}
        />
        <Button aria-label={`Add to ${name}`} onClick={() => add({})} disabled={!draft.trim()}>
          <PlusIcon aria-hidden="true" />
        </Button>
      </div>
    </div>
  );
}

function Poll({ id, props, onProps }: BlockRenderProps<PollProps>) {
  // Which options this window picked, so a change of mind moves its vote.
  const [mine, setMine] = React.useState<string[]>([]);
  const total = props.options.reduce((n, o) => n + (o.votes ?? 0), 0);
  const vote = (picked: string[], details: PropsChange) => {
    const options = props.options.map((o) => {
      const was = mine.includes(o.label);
      const now = picked.includes(o.label);
      return was === now ? o : { ...o, votes: Math.max(0, (o.votes ?? 0) + (now ? 1 : -1)) };
    });
    if (onProps({ ...props, options }, details)) setMine(picked);
  };
  const bar = (votes = 0) => (
    <span data-slot="block-poll-bar" aria-hidden="true" style={{ "--share": total ? votes / total : 0 } as React.CSSProperties} />
  );
  const count = (votes = 0) => <span data-slot="block-count">{votes}</span>;
  return (
    <div data-slot="block-poll">
      <div data-slot="block-heading">
        <span>{props.question}</span>
        <span data-slot="block-count">
          {total} {total === 1 ? "vote" : "votes"}
        </span>
      </div>
      {props.multi ? (
        <ul data-slot="block-poll-options" aria-label={props.question}>
          {props.options.map((o) => (
            <li key={`${id}-${o.label}`}>
              <label>
                <Checkbox
                  aria-label={`Vote ${o.label} in ${props.question}`}
                  checked={mine.includes(o.label)}
                  onCheckedChange={(checked, details) =>
                    vote(checked === true ? [...mine, o.label] : mine.filter((m) => m !== o.label), changeOf(details))
                  }
                >
                  <CheckboxIndicator>
                    <CheckIcon aria-hidden="true" />
                  </CheckboxIndicator>
                </Checkbox>
                <span>{o.label}</span>
                {count(o.votes)}
              </label>
              {bar(o.votes)}
            </li>
          ))}
        </ul>
      ) : (
        <RadioGroup
          aria-label={props.question}
          data-part="block-poll-options"
          value={mine[0] ?? null}
          onValueChange={(value, details) => vote(value == null ? [] : [String(value)], changeOf(details))}
        >
          {props.options.map((o) => (
            <div key={`${id}-${o.label}`}>
              <label>
                <Radio value={o.label}>
                  <RadioIndicator />
                </Radio>
                <span>{o.label}</span>
                {count(o.votes)}
              </label>
              {bar(o.votes)}
            </div>
          ))}
        </RadioGroup>
      )}
    </div>
  );
}

function RatingBlock({ props, onProps }: BlockRenderProps<RatingProps>) {
  const max = props.max ?? 5;
  return (
    <div data-slot="block-rating">
      <div data-slot="block-heading">
        <span>{props.title}</span>
        <span data-slot="block-count">{props.value == null ? "–" : `${props.value}/${max}`}</span>
      </div>
      <Rating
        aria-label={`Rate ${props.title}`}
        max={max}
        step={0.5}
        deselectable
        value={props.value ?? null}
        onValueChange={(value, details) => {
          const next = { ...props };
          if (value == null) delete next.value;
          else next.value = value;
          onProps(next, changeOf(details));
        }}
      >
        <RatingItem>
          <StarIcon aria-hidden="true" />
        </RatingItem>
      </Rating>
    </div>
  );
}

function Table({ id, props, onProps }: BlockRenderProps<TableProps>) {
  const name = props.title || "Table";
  const rows = props.rows ?? [];
  const [sort, setSort] = React.useState<{ col: number; desc: boolean } | null>(null);
  const order = rows.map((_, i) => i);
  if (sort) {
    order.sort((a, b) => {
      const c = (rows[a][sort.col] ?? "").localeCompare(rows[b][sort.col] ?? "", undefined, { numeric: true });
      return sort.desc ? -c : c;
    });
  }
  const setCell = (r: number, c: number, value: string, details: PropsChange) => {
    const next = rows.map((row, i) => {
      if (i !== r) return row;
      const cells = props.columns.map((_, j) => row[j] ?? "");
      cells[c] = value;
      return cells;
    });
    onProps({ ...props, rows: next }, details);
  };
  return (
    <div data-slot="block-table">
      <div data-slot="block-heading">
        <span>{name}</span>
      </div>
      <table>
        <thead>
          <tr>
            {props.columns.map((col, c) => (
              <th key={`${id}-h${c}`} scope="col" aria-sort={sort?.col === c ? (sort.desc ? "descending" : "ascending") : undefined}>
                <Button
                  aria-label={`Sort ${name} by ${col}`}
                  onClick={() => setSort(sort?.col === c ? (sort.desc ? null : { col: c, desc: true }) : { col: c, desc: false })}
                >
                  {col}
                  {sort?.col === c &&
                    (sort.desc ? <ArrowDownIcon aria-hidden="true" /> : <ArrowUpIcon aria-hidden="true" />)}
                </Button>
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {order.map((r) => (
            <tr key={`${id}-r${r}`}>
              {props.columns.map((col, c) => (
                <td key={`${id}-r${r}c${c}`}>
                  <Input
                    aria-label={`${name} row ${r + 1} ${col}`}
                    maxLength={1000}
                    value={rows[r][c] ?? ""}
                    onValueChange={(v, details) => setCell(r, c, v, changeOf(details))}
                  />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <div data-slot="block-table-add">
        <Button
          aria-label={`Add row to ${name}`}
          disabled={rows.length >= 50}
          onClick={() => onProps({ ...props, rows: [...rows, props.columns.map(() => "")] }, {})}
        >
          Add row
        </Button>
      </div>
    </div>
  );
}

/** Splits typed text into tags at commas and new lines; the last piece stays text unless `all`. */
function tokenize(value: TokenInputValue, all: boolean): TokenInputValue {
  const out: TokenInputValue = [];
  value.forEach((part, i) => {
    if (typeof part !== "string") {
      out.push(part);
      return;
    }
    const pieces = part.split(/[,\n]/);
    const last = all || i < value.length - 1 ? undefined : pieces.pop();
    for (const p of pieces) {
      const t = p.trim().slice(0, 40);
      if (t) out.push({ type: "tag", value: t, label: t });
    }
    if (last) out.push(last);
  });
  return out;
}

const tagsOf = (value: TokenInputValue) => {
  const seen = new Set<string>();
  for (const part of value) {
    if (typeof part !== "string" && part.label.trim()) seen.add(part.label.trim().slice(0, 40));
  }
  return [...seen].slice(0, 50);
};

function TagList({ props, onProps }: BlockRenderProps<TagListProps>) {
  const toValue = (items: string[]): TokenInputValue => items.map((t) => ({ type: "tag", value: t, label: t }));
  const [value, setValue] = React.useState<TokenInputValue>(() => toValue(props.items));
  const items = props.items.join("\n");
  React.useEffect(() => {
    setValue((v) => (tagsOf(v).join("\n") === items ? v : toValue(props.items)));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [items]);
  const apply = (next: TokenInputValue, all: boolean, details: PropsChange) => {
    const v = tokenize(next, all);
    setValue(v);
    const tags = tagsOf(v);
    if (tags.join("\n") !== items) onProps({ ...props, items: tags }, details);
  };
  return (
    <div data-slot="block-tags">
      <div data-slot="block-heading">
        <span>{props.title}</span>
      </div>
      <TokenInput
        value={value}
        onValueChange={(next, details) => {
          const d = changeOf(details);
          apply(next, d.source === "agent", d);
        }}
        onSubmit={(next) => apply(next, true, { source: "person" })}
      >
        <TokenInputEditor aria-label={`Tags in ${props.title}`} placeholder="Type a tag, then a comma" />
      </TokenInput>
    </div>
  );
}

function ImageBlock({ props, onProps }: BlockRenderProps<ImageProps>) {
  const images = useBoardImages();
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const [shots, setShots] = React.useState<LibraryShot[] | null>(null);
  const place = async (load: () => Promise<BoardImage>, alt: string, details: PropsChange) => {
    setBusy(true);
    setError("");
    try {
      const img = await load();
      onProps({ ...props, asset: img.asset, alt: props.alt || alt }, details);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };
  if (props.asset) {
    return (
      <figure data-slot="block-image" data-fit={props.fit ?? "contain"}>
        <img src={images.url(props.asset)} alt={props.alt ?? ""} draggable={false} />
        {props.caption && <figcaption>{props.caption}</figcaption>}
      </figure>
    );
  }
  return (
    <div data-slot="block-image-empty" aria-busy={busy || undefined}>
      <FileInput
        accept={["image/png", "image/jpeg", "image/gif", "image/webp"]}
        maxSize={32 << 20}
        value={[]}
        onValueChange={(files, details) => {
          const file = files[0];
          if (file) void place(() => images.importFile(file), file.name.replace(/\.[^.]+$/, ""), changeOf(details));
        }}
        onReject={() => setError("That file isn't a PNG, JPEG, GIF or WebP picture under 32 MB.")}
      >
        <FileInputDropzone data-part="block-image-drop">
          <ImageIcon aria-hidden="true" />
          <span>Drop a picture here</span>
          <FileInputTrigger>Choose picture file</FileInputTrigger>
        </FileInputDropzone>
      </FileInput>
      <Popover
        onOpenChange={(open) => {
          if (!open) return;
          setShots(null);
          images.library().then(setShots, (err) => {
            setShots([]);
            setError(err instanceof Error ? err.message : "The Library isn't available here.");
          });
        }}
      >
        <PopoverTrigger>Pick from Library</PopoverTrigger>
        <PopoverContent className="frost" data-part="block-library">
          <PopoverTitle>Library screenshots</PopoverTitle>
          {shots === null ? (
            <p>Loading…</p>
          ) : shots.length === 0 ? (
            <p>No screenshots in the Library.</p>
          ) : (
            <ul>
              {shots.slice(0, 12).map((s) => (
                <li key={s.id}>
                  <Button
                    aria-label={`Use screenshot ${s.label || s.id}`}
                    onClick={() => void place(() => images.importLibrary(s.id), s.label, {})}
                  >
                    <img src={s.url} alt="" />
                    <span>{s.label}</span>
                  </Button>
                </li>
              ))}
            </ul>
          )}
        </PopoverContent>
      </Popover>
      {error && <p data-slot="block-error">{error}</p>}
    </div>
  );
}

function Frame({ props, onProps }: BlockRenderProps<FrameProps>) {
  const name = useRequiredText(props.name, (n, d) => onProps({ ...props, name: n }, d));
  return (
    <div data-slot="block-frame" data-color={props.color ?? "grey"}>
      <div data-slot="block-frame-name">
        <Input aria-label={`Frame name: ${props.name}`} maxLength={120} {...name} />
      </div>
    </div>
  );
}

type Renderers = { [T in BlockType]: (p: BlockRenderProps<BlockPropsByType[T]>) => React.ReactNode };

const blockRenderers: Renderers = {
  sticky: Sticky,
  text: Text,
  card: Card,
  checklist: Checklist,
  poll: Poll,
  rating: RatingBlock,
  table: Table,
  "tag-list": TagList,
  image: ImageBlock,
  frame: Frame,
};

/** One block's content, by its type; a type the window doesn't know shows its name. */
function BlockBody({
  id,
  type,
  props,
  onProps,
}: {
  id: string;
  type: string;
  props: Record<string, unknown>;
  onProps: (next: Record<string, unknown>, details: PropsChange) => void;
}) {
  const Render = blockRenderers[type as BlockType] as
    | ((p: BlockRenderProps<Record<string, unknown>>) => React.ReactNode)
    | undefined;
  if (!Render) return <span data-slot="board-block-label">{blockLabel(type, props)}</span>;
  // Props Go would refuse never leave the block: the control keeps its value,
  // and the agent bridge reports the step didn't take.
  const keep = (next: Record<string, unknown>, details: PropsChange) => {
    if (checkProps(type, next) !== null) return false;
    onProps(next, details);
    return true;
  };
  return <Render id={id} label={blockLabel(type, props)} props={props} onProps={keep} />;
}

export { BlockBody, blockRenderers, tokenize, type PropsChange };
