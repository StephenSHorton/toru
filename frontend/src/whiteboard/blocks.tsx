// What each block type in the catalog (catalog.ts) shows on the board, built
// from UI SDK components so people and agents fill them the same way: every
// control is named after its block, and every change comes back through
// onProps with UI SDK's change details (details.source is "agent" for an
// agent's change).
import * as React from "react";
import { Button } from "@ui-sdk/react/button";
import { Checkbox, CheckboxIndicator } from "@ui-sdk/react/checkbox";
import { Collapsible, CollapsiblePanel, CollapsibleTrigger } from "@ui-sdk/react/collapsible";
import { FileInput, FileInputDropzone, FileInputTrigger } from "@ui-sdk/react/file-input";
import { Input } from "@ui-sdk/react/input";
import { Popover, PopoverContent, PopoverTitle, PopoverTrigger } from "@ui-sdk/react/popover";
import { Radio, RadioGroup, RadioIndicator } from "@ui-sdk/react/radio";
import { Rating, RatingItem } from "@ui-sdk/react/rating";
import { Textarea } from "@ui-sdk/react/textarea";
import { TokenInput, TokenInputEditor, type TokenInputValue } from "@ui-sdk/react/token-input";
import {
  ArrowDownIcon,
  ArrowUpIcon,
  CheckIcon,
  ImageIcon,
  PencilIcon,
  PlusIcon,
  StarIcon,
  XIcon,
} from "lucide-react";
import {
  blockColors,
  blockLabel,
  checkProps,
  excerpt,
  type BlockColor,
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

/**
 * A text a schema needs non-empty: shown as typed, kept only once it has
 * words. `held` names other texts that show as typed but aren't kept, like a
 * poll option that, half typed, matches another option.
 */
function useRequiredText(
  value: string,
  commit: (next: string, details: PropsChange) => boolean,
  held?: (next: string) => boolean,
) {
  const [draft, setDraft] = React.useState(value);
  React.useEffect(() => setDraft(value), [value]);
  return {
    value: draft,
    onValueChange: (next: string, details: unknown) => {
      if (!next.trim() || held?.(next) || commit(next, changeOf(details))) setDraft(next);
    },
    onBlur: () => setDraft(value),
  };
}

/** One input for a name a schema needs non-empty, like a poll option or a column. */
function NameInput({
  label,
  value,
  maxLength,
  taken,
  commit,
}: {
  label: string;
  value: string;
  maxLength: number;
  /** Names already used by its neighbours: typed, shown, not kept. */
  taken: string[];
  commit: (next: string, details: PropsChange) => boolean;
}) {
  const text = useRequiredText(value, commit, (next) => taken.includes(nameKey(next)));
  return <Input aria-label={label} maxLength={maxLength} {...text} />;
}

/** A name typed into a field and added with Enter or its button, like a new poll option. */
function AddField({
  inputLabel,
  buttonLabel,
  placeholder,
  maxLength,
  disabled,
  add,
}: {
  inputLabel: string;
  buttonLabel: string;
  placeholder: string;
  maxLength: number;
  disabled?: boolean;
  /** True when the name was added. */
  add: (text: string, details: PropsChange) => boolean;
}) {
  const [draft, setDraft] = React.useState("");
  const submit = (details: PropsChange) => {
    const text = draft.trim();
    if (text && add(text, details)) setDraft("");
  };
  return (
    <div data-slot="block-add">
      <Input
        aria-label={inputLabel}
        maxLength={maxLength}
        placeholder={placeholder}
        disabled={disabled}
        value={draft}
        onValueChange={(v) => setDraft(v)}
        onKeyDown={(e) => {
          if (e.key === "Enter") submit({ source: "person" });
        }}
      />
      <Button aria-label={buttonLabel} onClick={() => submit({})} disabled={disabled || !draft.trim()}>
        <PlusIcon aria-hidden="true" />
      </Button>
    </div>
  );
}

/** Two names that differ only in case or spacing at the ends name the same thing. */
const nameKey = (name: string) => name.trim().toLowerCase();

const colorName = (c: string) => c.charAt(0).toUpperCase() + c.slice(1);

/** The catalog's colours as a row of swatches; it shows while the block is selected or hovered. */
function ColorSwatches({
  label,
  value,
  onColor,
}: {
  label: string;
  value: string;
  onColor: (color: BlockColor, details: PropsChange) => boolean;
}) {
  return (
    <RadioGroup
      aria-label={label}
      data-part="block-swatches"
      value={value}
      onValueChange={(next, details) => {
        if (next != null) onColor(String(next) as BlockColor, changeOf(details));
      }}
    >
      {blockColors.map((c) => (
        <Radio key={c} value={c} aria-label={colorName(c)} title={colorName(c)} data-color={c} />
      ))}
    </RadioGroup>
  );
}

function Sticky({ label, props, onProps }: BlockRenderProps<StickyProps>) {
  return (
    <div data-slot="block-sticky" data-color={props.color ?? "yellow"}>
      <ColorSwatches
        label={`Sticky colour: ${excerpt(props.text)}`}
        value={props.color ?? "yellow"}
        onColor={(color, d) => onProps({ ...props, color }, d)}
      />
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
      <ColorSwatches
        label={`Card colour: ${props.title}`}
        value={props.color ?? "grey"}
        onColor={(color, d) => onProps({ ...props, color }, d)}
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
            <Button
              aria-label={`Remove item ${i + 1} from ${name}`}
              data-part="block-remove"
              data-agent-confirm={`Removes "${item.text}" from ${name}`}
              onClick={() => onProps({ ...props, items: props.items.filter((_, j) => j !== i) }, {})}
            >
              <XIcon aria-hidden="true" />
            </Button>
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
  const others = (i: number) => props.options.filter((_, j) => j !== i).map((o) => nameKey(o.label));
  const renameOption = (i: number, label: string, details: PropsChange) => {
    const was = props.options[i].label;
    if (others(i).includes(nameKey(label))) return false;
    const ok = onProps({ ...props, options: props.options.map((o, j) => (j === i ? { ...o, label } : o)) }, details);
    if (ok) setMine((m) => m.map((l) => (l === was ? label : l)));
    return ok;
  };
  const addOption = (label: string, details: PropsChange) =>
    !others(-1).includes(nameKey(label)) && onProps({ ...props, options: [...props.options, { label }] }, details);
  const removeOption = (i: number) => {
    const was = props.options[i].label;
    if (onProps({ ...props, options: props.options.filter((_, j) => j !== i) }, {})) setMine((m) => m.filter((l) => l !== was));
  };
  const bar = (votes = 0) => (
    <span data-slot="block-poll-bar" aria-hidden="true" style={{ "--share": total ? votes / total : 0 } as React.CSSProperties} />
  );
  const count = (votes = 0) => <span data-slot="block-count">{votes}</span>;
  return (
    <div data-slot="block-poll">
      <Collapsible data-part="block-edit">
        <div data-slot="block-heading">
          <span>{props.question}</span>
          <span data-slot="block-count">
            {total} {total === 1 ? "vote" : "votes"}
          </span>
          <CollapsibleTrigger aria-label={`Edit options of ${props.question}`}>
            <PencilIcon aria-hidden="true" />
            Options
          </CollapsibleTrigger>
        </div>
        <CollapsiblePanel>
          <ul data-slot="block-edit-list">
            {props.options.map((o, i) => (
              <li key={`${id}-edit-${i}`}>
                <NameInput
                  label={`Option ${i + 1} text in ${props.question}`}
                  value={o.label}
                  maxLength={200}
                  taken={others(i)}
                  commit={(label, d) => renameOption(i, label, d)}
                />
                <Button
                  aria-label={`Remove option ${i + 1} from ${props.question}`}
                  data-part="block-remove"
                  data-agent-confirm={`Removes the option "${o.label}" and its ${o.votes ?? 0} ${o.votes === 1 ? "vote" : "votes"} from the poll "${props.question}"`}
                  disabled={props.options.length <= 2}
                  onClick={() => removeOption(i)}
                >
                  <XIcon aria-hidden="true" />
                </Button>
              </li>
            ))}
          </ul>
          <AddField
            inputLabel={`New option for ${props.question}`}
            buttonLabel={`Add option to ${props.question}`}
            placeholder="Add an option"
            maxLength={200}
            disabled={props.options.length >= 20}
            add={addOption}
          />
        </CollapsiblePanel>
      </Collapsible>
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
          aria-label={`Vote in ${props.question}`}
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
  // Each column names its cells and its sort button, so no two share a name.
  const otherColumns = (c: number) => props.columns.filter((_, j) => j !== c).map(nameKey);
  const renameColumn = (c: number, col: string, details: PropsChange) =>
    !otherColumns(c).includes(nameKey(col)) &&
    onProps({ ...props, columns: props.columns.map((x, j) => (j === c ? col : x)) }, details);
  const addColumn = (col: string, details: PropsChange) =>
    !otherColumns(-1).includes(nameKey(col)) && onProps({ ...props, columns: [...props.columns, col] }, details);
  const removeColumn = (c: number) => {
    const next: TableProps = { ...props, columns: props.columns.filter((_, j) => j !== c) };
    if (props.rows) next.rows = rows.map((row) => row.filter((_, j) => j !== c));
    if (onProps(next, {})) setSort(null);
  };
  return (
    <div data-slot="block-table">
      <Collapsible data-part="block-edit">
        <div data-slot="block-heading">
          <span>{name}</span>
          <CollapsibleTrigger aria-label={`Edit columns of ${name}`}>
            <PencilIcon aria-hidden="true" />
            Columns
          </CollapsibleTrigger>
        </div>
        <CollapsiblePanel>
          <ul data-slot="block-edit-list">
            {props.columns.map((col, c) => (
              <li key={`${id}-col-${c}`}>
                <NameInput
                  label={`Column ${c + 1} name in ${name}`}
                  value={col}
                  maxLength={80}
                  taken={otherColumns(c)}
                  commit={(next, d) => renameColumn(c, next, d)}
                />
                <Button
                  aria-label={`Remove column ${c + 1} from ${name}`}
                  data-part="block-remove"
                  data-agent-confirm={`Removes the column "${col}" and every cell in it from ${name}`}
                  disabled={props.columns.length <= 1}
                  onClick={() => removeColumn(c)}
                >
                  <XIcon aria-hidden="true" />
                </Button>
              </li>
            ))}
          </ul>
          <AddField
            inputLabel={`New column for ${name}`}
            buttonLabel={`Add column to ${name}`}
            placeholder="Add a column"
            maxLength={80}
            disabled={props.columns.length >= 12}
            add={addColumn}
          />
        </CollapsiblePanel>
      </Collapsible>
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

/** Where a picture comes from: a chosen or dropped file, or a Library screenshot. */
function PicturePicker({
  fileLabel,
  libraryLabel,
  drop,
  onPicture,
  onError,
}: {
  /** The file button's text, which names it for agents. */
  fileLabel: string;
  libraryLabel: string;
  /** Show a drop zone around the file button (the empty block). */
  drop: boolean;
  onPicture: (load: () => Promise<BoardImage>, alt: string, details: PropsChange) => void;
  onError: (message: string) => void;
}) {
  const images = useBoardImages();
  const [shots, setShots] = React.useState<LibraryShot[] | null>(null);
  // On a filled block the buttons sit over the picture, so they show a short
  // word and carry their full name for agents and screen readers.
  const trigger = drop ? (
    <FileInputTrigger>{fileLabel}</FileInputTrigger>
  ) : (
    <FileInputTrigger aria-label={fileLabel}>Replace</FileInputTrigger>
  );
  return (
    <>
      <FileInput
        accept={["image/png", "image/jpeg", "image/gif", "image/webp"]}
        maxSize={32 << 20}
        value={[]}
        onValueChange={(files, details) => {
          const file = files[0];
          if (file) onPicture(() => images.importFile(file), file.name.replace(/\.[^.]+$/, ""), changeOf(details));
        }}
        onReject={() => onError("That file isn't a PNG, JPEG, GIF or WebP picture under 32 MB.")}
      >
        {drop ? (
          <FileInputDropzone data-part="block-image-drop">
            <ImageIcon aria-hidden="true" />
            <span>Drop a picture here</span>
            {trigger}
          </FileInputDropzone>
        ) : (
          trigger
        )}
      </FileInput>
      <Popover
        onOpenChange={(open) => {
          if (!open) return;
          setShots(null);
          images.library().then(setShots, (err) => {
            setShots([]);
            onError(err instanceof Error ? err.message : "The Library isn't available here.");
          });
        }}
      >
        {drop ? (
          <PopoverTrigger>{libraryLabel}</PopoverTrigger>
        ) : (
          <PopoverTrigger aria-label={libraryLabel}>Library</PopoverTrigger>
        )}
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
                    onClick={() => onPicture(() => images.importLibrary(s.id), s.label, {})}
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
    </>
  );
}

function ImageBlock({ props, onProps }: BlockRenderProps<ImageProps>) {
  const images = useBoardImages();
  const [error, setError] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  const place = async (load: () => Promise<BoardImage>, alt: string, details: PropsChange) => {
    setBusy(true);
    setError("");
    try {
      const img = await load();
      // A new picture in a filled block takes the new picture's name as its
      // description, since the old one described the old picture.
      const next = { ...props, asset: img.asset, alt: props.asset ? alt || props.alt : props.alt || alt };
      if (!next.alt) delete next.alt;
      onProps(next, details);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  };
  if (props.asset) {
    const what = props.alt?.trim() || props.caption?.trim() || "image";
    return (
      <figure data-slot="block-image" data-fit={props.fit ?? "contain"} aria-busy={busy || undefined}>
        <img src={images.url(props.asset)} alt={props.alt ?? ""} draggable={false} />
        {props.caption && <figcaption>{props.caption}</figcaption>}
        <div data-slot="block-image-replace">
          <PicturePicker
            fileLabel={`New picture file for ${excerpt(what)}`}
            libraryLabel={`New picture from Library for ${excerpt(what)}`}
            drop={false}
            onPicture={(load, alt, d) => void place(load, alt, d)}
            onError={setError}
          />
        </div>
        {error && <p data-slot="block-error">{error}</p>}
      </figure>
    );
  }
  return (
    <div data-slot="block-image-empty" aria-busy={busy || undefined}>
      <PicturePicker
        fileLabel="Choose picture file"
        libraryLabel="Pick from Library"
        drop
        onPicture={(load, alt, d) => void place(load, alt, d)}
        onError={setError}
      />
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
        <ColorSwatches
          label={`Frame colour: ${props.name}`}
          value={props.color ?? "grey"}
          onColor={(color, d) => onProps({ ...props, color }, d)}
        />
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
