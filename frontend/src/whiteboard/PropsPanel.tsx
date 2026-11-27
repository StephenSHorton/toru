// The whiteboard window's properties panel (whiteboard#29): down the right
// side, it shows and edits whatever is selected, and slides away when
// nothing is. It sits over the canvas, so the canvas never moves or shrinks.
//
// A block's props are a form made from its catalog type's JSON Schema
// (catalog.ts), keyword by keyword (enum, items, properties, min and max),
// never from the types the window knows, so a type added to the catalog
// later gets a form too. Under the props: position, size, rotation, frame,
// lock, and who made it and changed it last. A frame shows its name and
// colour, a line its label, arrow ends, dash and colour, ink its colour and
// width. With several selected it shows what they share, and a change goes
// to each of them.
//
// Every change is one LiveBoard.update: one op to Go's board store, the
// same store agents' board.update changes, so it's one undo. Text and
// numbers are sent when the field is left (or Enter), not per key; a field
// left because the selection changed is still sent, to what was selected.
import * as React from "react";
import { Button } from "@ui-sdk/react/button";
import { Checkbox, CheckboxIndicator } from "@ui-sdk/react/checkbox";
import { Input } from "@ui-sdk/react/input";
import { Radio, RadioGroup } from "@ui-sdk/react/radio";
import { Select, SelectContent, SelectItem, SelectItemIndicator, SelectTrigger, SelectValue } from "@ui-sdk/react/select";
import { Textarea } from "@ui-sdk/react/textarea";
import { CheckIcon, ChevronDownIcon, PlusIcon, XIcon } from "lucide-react";
import { who, type LineLook } from "./BoardView";
import { blockColors, blockLabel, blockType, checkProps, type Schema } from "./catalog";
import { findItem, type BoardDoc, type DocBlock, type DocFrame, type DocItem, type DocLink, type DocStroke, type ListKey } from "./document";
import { inkColor, inkColors, inkWidths, tokenWidth } from "./ink";
import type { DocPatch, LiveBoard } from "./live";
import "./props-panel.css";

/** One selected thing, as the document has it. */
type Target = { id: string; key: ListKey; item: DocItem };

type PropsPanelProps = {
  live: LiveBoard;
  /** The board as Go has it. */
  doc: BoardDoc | null;
  /** The canvas's selection: blocks, frames and strokes, by id. */
  selected: readonly string[];
  /** A line picked out on its own, if any. */
  link: string | null;
  /** Pick a line (from a block's list of lines). */
  onPickLink: (id: string) => void;
  /** Select these blocks (from a line's ends). */
  onSelect: (ids: string[]) => void;
  /** Whose a change is: "person", or the agent using the window. */
  source?: () => string;
};

/** What's selected, as document items; a picked line wins. */
function targetsOf(doc: BoardDoc | null, selected: readonly string[], link: string | null): Target[] {
  if (!doc) return [];
  const ids = link ? [link] : selected;
  const out: Target[] = [];
  for (const id of ids) {
    const found = findItem(doc, id);
    if (found) out.push({ id, key: found.key, item: found.item });
  }
  return out;
}

function PropsPanel({ live, doc, selected, link, onPickLink, onSelect, source }: PropsPanelProps) {
  const targets = targetsOf(doc, selected, link);
  const open = targets.length > 0;
  // While it slides away it keeps showing what it showed.
  const last = React.useRef<Target[]>([]);
  if (open) last.current = targets;
  const shown = open ? targets : last.current;
  const send = React.useCallback<Send>((build) => live.update(build, { source: source?.() ?? "person" }), [live, source]);
  return (
    <aside
      data-slot="props-panel"
      className="frost"
      aria-label="Properties"
      aria-keyshortcuts="F6"
      data-open={open ? "" : undefined}
      inert={!open}
      onWheel={(e) => e.stopPropagation()}
      onPointerDown={(e) => e.stopPropagation()}
    >
      {doc && shown.length > 0 && (
        <PanelBody
          // A new selection is a new form, so a half-typed field goes to what it was typed for.
          key={shown.map((t) => t.id).join(" ")}
          doc={doc}
          targets={shown}
          send={send}
          onPickLink={onPickLink}
          onSelect={onSelect}
        />
      )}
    </aside>
  );
}

/**
 * Sends one change. It's built from the board as it is when it goes, after
 * what's pending (typing in a block, an edit just before), never from the
 * board as the panel last drew it, so it can't put back what changed since.
 */
type Send = (build: (doc: BoardDoc) => DocPatch[]) => Promise<boolean>;

/** Each target as the document has it now; gone ones are left out. */
const now = (doc: BoardDoc, targets: Target[]): Target[] =>
  targets.flatMap((t) => {
    const found = findItem(doc, t.id);
    return found ? [{ id: t.id, key: found.key, item: found.item }] : [];
  });

type BodyProps = {
  doc: BoardDoc;
  targets: Target[];
  send: Send;
  onPickLink: (id: string) => void;
  onSelect: (ids: string[]) => void;
};

function PanelBody({ doc, targets, send, onPickLink, onSelect }: BodyProps) {
  const [error, setError] = React.useState<string | null>(null);
  const kinds = new Set(targets.map((t) => t.key));
  const kind = kinds.size === 1 ? targets[0].key : null;
  const one = targets.length === 1 ? targets[0] : null;
  const run: Send = async (build) => {
    setError(null);
    try {
      return await send(build);
    } catch (err) {
      // A change the schema refuses isn't sent; say why.
      setError(err instanceof Error ? err.message : String(err));
      return false;
    }
  };
  return (
    <>
      <header data-slot="props-panel-header">
        <h2>{title(targets)}</h2>
        {targets.length > 1 && <p>Changes go to all {targets.length}.</p>}
        <p>F6 moves between the board and here.</p>
      </header>
      {(kind === "blocks" || kind === "frames") && (
        <SchemaSection targets={targets} send={run} />
      )}
      {kind === "links" && one && <LinkSection doc={doc} link={one.item as DocLink} send={run} onSelect={onSelect} />}
      {kind === "ink" && <InkSection targets={targets} send={run} />}
      {(kind === "blocks" || kind === "frames") && <BoxSection targets={targets} send={run} />}
      {kind === "blocks" && doc.frames.length > 0 && <FrameSection doc={doc} targets={targets} send={run} />}
      <LockSection targets={targets} send={run} />
      {one && (one.key === "blocks" || one.key === "frames") && <LinesSection doc={doc} id={one.id} onPickLink={onPickLink} />}
      {error && (
        <p role="alert" data-slot="props-panel-error">
          {error}
        </p>
      )}
      <WhoSection targets={targets} />
    </>
  );
}

// ── names ──────────────────────────────────────────────────────────────

const kindNames: Record<ListKey, [string, string]> = {
  blocks: ["block", "blocks"],
  frames: ["frame", "frames"],
  ink: ["ink stroke", "ink strokes"],
  links: ["line", "lines"],
};

/** "Sticky note", "Frame", "Line", or "3 selected: 2 sticky notes, 1 card". */
function title(targets: Target[]) {
  const name = (t: Target) =>
    t.key === "blocks" ? (blockType((t.item as DocBlock).type)?.label ?? (t.item as DocBlock).type) : kindNames[t.key][0];
  if (targets.length === 1) {
    const n = name(targets[0]);
    return n.charAt(0).toUpperCase() + n.slice(1);
  }
  const counts = new Map<string, number>();
  for (const t of targets) counts.set(name(t), (counts.get(name(t)) ?? 0) + 1);
  const parts = [...counts].map(([n, c]) => `${c} ${c === 1 ? n.toLowerCase() : plural(n.toLowerCase())}`);
  return `${targets.length} selected: ${parts.join(", ")}`;
}

const plural = (n: string) => (n.endsWith("s") ? n : `${n}s`);

/** A field's name, from its key: "multi" → "Multi", "fromPort" → "From port". */
function fieldName(key: string) {
  if (key === "color") return "Colour";
  const words = key.replace(/([a-z])([A-Z])/g, "$1 $2").replace(/[-_]/g, " ").toLowerCase();
  return words.charAt(0).toUpperCase() + words.slice(1);
}

// ── values shared by several targets ───────────────────────────────────

/** Several targets that don't agree on a value. */
const MIXED = Symbol("mixed");
type Shared<T> = T | typeof MIXED;

const same = (a: unknown, b: unknown) => JSON.stringify(a) === JSON.stringify(b);

/** The one value every target has, or MIXED. */
function shared<T>(values: T[]): Shared<T> {
  return values.every((v) => same(v, values[0])) ? values[0] : MIXED;
}

// ── props, from the schema ─────────────────────────────────────────────

/** A target's props, as its catalog type has them; a frame's name and colour are props too. */
function propsOf(t: Target): Record<string, unknown> {
  if (t.key === "frames") {
    const f = t.item as DocFrame;
    return { name: f.name, ...(f.color ? { color: f.color } : {}) };
  }
  return (t.item as DocBlock).props ?? {};
}

const typeOf = (t: Target) => (t.key === "frames" ? "frame" : (t.item as DocBlock).type);

/** The fields every target's type has, with the same schema: one type's fields, or what several share. */
function sharedFields(targets: Target[]): [string, Schema][] {
  const schemas = [...new Set(targets.map(typeOf))].map((type) => blockType(type)?.props);
  if (schemas.some((s) => !s?.properties)) return [];
  const [first, ...rest] = schemas as Schema[];
  // The same field: the same keywords, whatever each type says about it.
  const bare = (s: Schema) => JSON.stringify(s, (k, v) => (k === "description" ? undefined : v));
  return Object.entries(first.properties!).filter(([k, s]) =>
    rest.every((r) => r.properties?.[k] && bare(r.properties[k]) === bare(s)),
  );
}

/** A target's patch for new props. */
function propsPatch(t: Target, props: Record<string, unknown>): DocPatch {
  if (t.key === "frames") return { id: t.id, name: String(props.name ?? ""), color: (props.color as string | undefined) || undefined };
  return { id: t.id, props };
}

function SchemaSection({ targets, send }: { targets: Target[]; send: Send }) {
  const fields = sharedFields(targets);
  const required = new Set(targets.flatMap((t) => blockType(typeOf(t))?.props.required ?? []));
  if (!fields.length) {
    return targets.length > 1 ? null : (
      <section data-slot="props-panel-section">
        <p data-slot="props-panel-note">This block's type isn't in the catalog, so its props can't be edited here.</p>
      </section>
    );
  }
  // Sets one field on every target, each from its own value, checked against its schema first.
  const set = (key: string, next: (was: unknown) => unknown) =>
    send((doc) =>
      now(doc, targets).map((t) => {
        const props = { ...propsOf(t) };
        const v = next(props[key]);
        if (v === undefined) delete props[key];
        else props[key] = v;
        const err = checkProps(typeOf(t), props);
        if (err) throw new Error(`${blockLabel(typeOf(t), propsOf(t))}: ${err.replace(/^props\./, "")}`);
        return propsPatch(t, props);
      }),
    );
  const label = targets.length === 1 ? blockLabel(typeOf(targets[0]), propsOf(targets[0])) : "selection";
  return (
    <section data-slot="props-panel-section" aria-label="Props">
      {fields.map(([key, schema]) => (
        <SchemaField
          key={key}
          name={fieldName(key)}
          label={`${fieldName(key)} of ${label}`}
          schema={schema}
          required={required.has(key)}
          value={shared(targets.map((t) => propsOf(t)[key]))}
          many={targets.length > 1}
          onChange={(next) => set(key, next)}
        />
      ))}
    </section>
  );
}

type FieldProps = {
  name: string;
  /** Its accessible name: the field and what it belongs to. */
  label: string;
  schema: Schema;
  required: boolean;
  value: Shared<unknown>;
  /** Several targets: a list of text adds and removes on each instead of replacing. */
  many?: boolean;
  /** An item of a list: a list of text in it is cells, like a table row, not words. */
  nested?: boolean;
  /** Changes the value, from what each target has. */
  onChange: (next: (was: unknown) => unknown) => Promise<boolean>;
};

/** One field of a schema, picked by its keywords. */
function SchemaField(props: FieldProps) {
  const { schema } = props;
  if (schema.type === "string" && schema.enum) {
    if (schema.enum.every((v) => blockColors.includes(v))) return <ColorField {...props} />;
    return <EnumField {...props} />;
  }
  if (schema.type === "string") return <TextField {...props} />;
  if (schema.type === "number" || schema.type === "integer") return <NumberField {...props} />;
  if (schema.type === "boolean") return <BoolField {...props} />;
  if (schema.type === "array" && schema.items?.type === "string" && !schema.items.enum) {
    return props.nested ? <CellsField {...props} /> : <WordsField {...props} />;
  }
  if (schema.type === "array" && schema.items) return <ListField {...props} />;
  if (schema.type === "object" && schema.properties) return <ObjectField {...props} />;
  return <JsonField {...props} />;
}

/** A field's frame: its name over its control, with a note when the targets differ. */
function Row({ name, mixed, children, htmlFor }: { name: string; mixed?: boolean; children: React.ReactNode; htmlFor?: string }) {
  return (
    <div data-slot="props-panel-row">
      <label data-slot="props-panel-label" htmlFor={htmlFor}>
        {name}
        {mixed && <span data-slot="props-panel-mixed"> (mixed)</span>}
      </label>
      {children}
    </div>
  );
}

/**
 * A field's text while it's edited: shown in place of the value, sent when
 * the field is left, and still sent if the field goes away mid-edit (the
 * selection changed). After it's sent it stays until the value comes back.
 */
function useDraft(value: string, commit: (text: string) => void) {
  const [draft, setDraft] = React.useState<{ text: string; sent: boolean } | null>(null);
  const latest = React.useRef({ draft, commit });
  latest.current = { draft, commit };
  React.useEffect(() => {
    setDraft((d) => (d?.sent ? null : d));
  }, [value]);
  React.useEffect(
    () => () => {
      const { draft: d, commit: c } = latest.current;
      if (d && !d.sent) c(d.text);
    },
    [],
  );
  return {
    text: draft ? draft.text : value,
    /** Typed and not yet sent: Ctrl+Z is the field's own until then (routes/Whiteboard.tsx). */
    dirty: !!draft && !draft.sent,
    edit: (text: string) => setDraft({ text, sent: false }),
    done: () => {
      if (!draft || draft.sent) return;
      if (draft.text === value) setDraft(null);
      else {
        setDraft({ ...draft, sent: true });
        commit(draft.text);
      }
    },
    cancel: () => setDraft(null),
  };
}

const useId = () => React.useId().replaceAll(":", "");

function TextField({ name, label, schema, required, value, nested, onChange }: FieldProps) {
  const id = useId();
  const mixed = value === MIXED;
  const draft = useDraft(mixed || typeof value !== "string" ? "" : value, (text) => {
    // An optional field left empty is taken away.
    void onChange(() => (text === "" && !required ? undefined : text));
  });
  // Long text gets a text area; a line, a title or a table cell a field.
  const long = !nested && (schema.maxLength === undefined || schema.maxLength > 1000);
  const common = {
    id,
    "aria-label": label,
    maxLength: schema.maxLength,
    placeholder: mixed ? "Mixed" : undefined,
    value: draft.text,
    "data-dirty": draft.dirty ? "" : undefined,
    onValueChange: (text: string) => draft.edit(text),
    onBlur: () => draft.done(),
  };
  return (
    <Row name={name} mixed={mixed} htmlFor={id}>
      {long ? (
        <Textarea
          {...common}
          rows={3}
          onKeyDown={(e: React.KeyboardEvent) => {
            // Ctrl+Enter keeps it; Escape takes the edit back.
            if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) draft.done();
            if (e.key === "Escape") draft.cancel();
          }}
        />
      ) : (
        <Input
          {...common}
          onKeyDown={(e: React.KeyboardEvent) => {
            if (e.key === "Enter") draft.done();
            if (e.key === "Escape") draft.cancel();
          }}
        />
      )}
    </Row>
  );
}

function NumberField({ name, label, schema, required, value, onChange }: FieldProps) {
  const id = useId();
  const mixed = value === MIXED;
  const [invalid, setInvalid] = React.useState(false);
  const draft = useDraft(mixed || typeof value !== "number" ? "" : String(value), (text) => {
    if (text.trim() === "") {
      if (!required) void onChange(() => undefined);
      return;
    }
    let n = Number(text);
    if (!Number.isFinite(n)) return setInvalid(true);
    if (schema.type === "integer") n = Math.round(n);
    if (schema.minimum !== undefined) n = Math.max(schema.minimum, n);
    if (schema.maximum !== undefined) n = Math.min(schema.maximum, n);
    setInvalid(false);
    void onChange(() => n);
  });
  return (
    <Row name={name} mixed={mixed} htmlFor={id}>
      <NumberInput id={id} label={label} draft={draft} min={schema.minimum} max={schema.maximum} mixed={mixed} invalid={invalid} />
    </Row>
  );
}

function NumberInput({
  id,
  label,
  draft,
  min,
  max,
  mixed,
  invalid,
}: {
  id: string;
  label: string;
  draft: ReturnType<typeof useDraft>;
  min?: number;
  max?: number;
  mixed?: boolean;
  invalid?: boolean;
}) {
  return (
    <Input
      id={id}
      type="number"
      inputMode="decimal"
      aria-label={label}
      aria-invalid={invalid || undefined}
      min={min}
      max={max}
      placeholder={mixed ? "Mixed" : undefined}
      value={draft.text}
      data-dirty={draft.dirty ? "" : undefined}
      onValueChange={(text: string) => draft.edit(text)}
      onBlur={() => draft.done()}
      onKeyDown={(e: React.KeyboardEvent) => {
        if (e.key === "Enter") draft.done();
        if (e.key === "Escape") draft.cancel();
      }}
    />
  );
}

function BoolField({ name, label, value, onChange }: FieldProps) {
  const mixed = value === MIXED;
  return (
    <div data-slot="props-panel-row" data-inline="">
      <Checkbox
        aria-label={label}
        checked={mixed ? "indeterminate" : value === true}
        onCheckedChange={(checked) => void onChange(() => checked === true)}
      >
        <CheckboxIndicator>
          <CheckIcon aria-hidden="true" />
        </CheckboxIndicator>
      </Checkbox>
      <span data-slot="props-panel-label">
        {name}
        {mixed && <span data-slot="props-panel-mixed"> (mixed)</span>}
      </span>
    </div>
  );
}

const NONE = "__none__";

function EnumField({ name, label, schema, required, value, onChange }: FieldProps) {
  const mixed = value === MIXED;
  const current = mixed ? "" : typeof value === "string" ? value : NONE;
  const options = [...(required ? [] : [NONE]), ...schema.enum!];
  return (
    <Row name={name} mixed={mixed}>
      <Pick
        label={label}
        value={current}
        options={options.map((v) => ({ value: v, label: v === NONE ? "Default" : fieldName(v) }))}
        onPick={(v) => void onChange(() => (v === NONE ? undefined : v))}
      />
    </Row>
  );
}

/** A choice of a few options, as UI SDK's Select. */
function Pick({
  label,
  value,
  options,
  onPick,
}: {
  label: string;
  value: string;
  options: { value: string; label: string }[];
  onPick: (value: string) => void;
}) {
  return (
    <Select value={value || null} onValueChange={(v) => v != null && onPick(String(v))}>
      <SelectTrigger aria-label={label}>
        <SelectValue placeholder="Mixed">{options.find((o) => o.value === value)?.label}</SelectValue>
        <ChevronDownIcon aria-hidden="true" />
      </SelectTrigger>
      <SelectContent className="frost">
        {options.map((o) => (
          <SelectItem key={o.value} value={o.value}>
            {o.label}
            <SelectItemIndicator>
              <CheckIcon aria-hidden="true" />
            </SelectItemIndicator>
          </SelectItem>
        ))}
      </SelectContent>
    </Select>
  );
}

function ColorField({ name, label, schema, value, onChange }: FieldProps) {
  const mixed = value === MIXED;
  return (
    <Row name={name} mixed={mixed}>
      <Swatches
        label={label}
        colors={schema.enum!}
        value={mixed || typeof value !== "string" ? "" : value}
        onColor={(c) => void onChange(() => c)}
      />
    </Row>
  );
}

function Swatches({ label, colors, value, onColor }: { label: string; colors: readonly string[]; value: string; onColor: (c: string) => void }) {
  return (
    <RadioGroup
      aria-label={label}
      data-part="block-swatches"
      data-slot="props-panel-swatches"
      value={value || null}
      onValueChange={(next) => next != null && onColor(String(next))}
    >
      {colors.map((c) => (
        <Radio key={c} value={c} aria-label={fieldName(c)} title={fieldName(c)} data-color={c} />
      ))}
    </RadioGroup>
  );
}

/**
 * A list of words, like tags. With several targets it shows the words they
 * all have; adding one adds it to each, removing one removes it from each.
 */
function WordsField({ name, label, schema, value, many, onChange }: FieldProps) {
  const id = useId();
  const [text, setText] = React.useState("");
  const words = value === MIXED ? [] : Array.isArray(value) ? (value as string[]) : [];
  const max = schema.items?.maxLength;
  const add = () => {
    const word = (max ? text.trim().slice(0, max) : text.trim());
    if (!word) return;
    setText("");
    void onChange((was) => {
      const list = Array.isArray(was) ? (was as string[]) : [];
      return list.includes(word) ? list : [...list, word];
    });
  };
  return (
    <Row name={name} mixed={value === MIXED && !many} htmlFor={id}>
      <ul data-slot="props-panel-words" aria-label={label}>
        {(many && value === MIXED ? [] : words).map((w) => (
          <li key={w}>
            <span>{w}</span>
            <Button
              aria-label={`Remove ${w} from ${label}`}
              title={`Remove ${w}`}
              onClick={() => void onChange((was) => (Array.isArray(was) ? (was as string[]).filter((x) => x !== w) : was))}
            >
              <XIcon aria-hidden="true" />
            </Button>
          </li>
        ))}
      </ul>
      <Input
        id={id}
        aria-label={`Add to ${label}`}
        placeholder={many ? "Add to each" : "Add, then Enter"}
        maxLength={max}
        value={text}
        data-dirty={text ? "" : undefined}
        onValueChange={(t: string) => setText(t)}
        onKeyDown={(e: React.KeyboardEvent) => {
          if (e.key === "Enter" || e.key === ",") {
            e.preventDefault();
            add();
          }
        }}
        onBlur={add}
      />
    </Row>
  );
}

/** A list of text inside a list, like a table row: one field per cell. */
function CellsField({ label, schema, value, onChange }: FieldProps) {
  const cells = Array.isArray(value) ? (value as unknown[]) : [];
  return (
    <div data-slot="props-panel-cells" role="group" aria-label={label}>
      {cells.map((cell, i) => (
        <TextField
          key={i}
          name=""
          label={`${label}, cell ${i + 1}`}
          schema={schema.items!}
          required
          nested
          value={cell}
          onChange={(next) =>
            onChange((was) => {
              const l = Array.isArray(was) ? [...was] : [];
              l[i] = next(l[i]);
              return l;
            })
          }
        />
      ))}
    </div>
  );
}

/** A schema's least value: what a new list item starts as. */
function emptyOf(s: Schema | undefined, like?: unknown): unknown {
  switch (s?.type) {
    case "string":
      return s.enum ? s.enum[0] : s.minLength ? "New" : "";
    case "number":
    case "integer":
      return s.minimum ?? 0;
    case "boolean":
      return false;
    case "array":
      // A row of a table: as long as the row before it.
      return Array.isArray(like) ? like.map((x) => emptyOf(s.items, x)) : [];
    case "object": {
      const o: Record<string, unknown> = {};
      for (const r of s.required ?? []) o[r] = emptyOf(s.properties?.[r]);
      return o;
    }
  }
  return null;
}

/** A list of things, like checklist items, poll options or table rows: each one's fields, a remove, and Add. */
function ListField({ name, label, schema, value, onChange }: FieldProps) {
  if (value === MIXED) {
    return (
      <Row name={name} mixed>
        <p data-slot="props-panel-note">Different in each: select one to edit its {name.toLowerCase()}.</p>
      </Row>
    );
  }
  const list = Array.isArray(value) ? value : [];
  const items = schema.items!;
  const canAdd = schema.maxItems === undefined || list.length < schema.maxItems;
  const canRemove = schema.minItems === undefined || list.length > schema.minItems;
  const at = (i: number, next: (was: unknown) => unknown) =>
    onChange((was) => {
      const l = Array.isArray(was) ? [...was] : [];
      l[i] = next(l[i]);
      return l;
    });
  return (
    <fieldset data-slot="props-panel-list">
      <legend data-slot="props-panel-label">{name}</legend>
      <ol>
        {list.map((item, i) => (
          <li key={i}>
            <div data-slot="props-panel-item">
              <SchemaField
                name={`${i + 1}`}
                label={`${label}, ${i + 1}`}
                schema={items}
                required
                nested
                value={item}
                onChange={(next) => at(i, next)}
              />
            </div>
            {canRemove && (
              <Button
                aria-label={`Remove ${i + 1} from ${label}`}
                title="Remove"
                data-slot="props-panel-remove"
                onClick={() => void onChange((was) => (Array.isArray(was) ? was.filter((_, j) => j !== i) : was))}
              >
                <XIcon aria-hidden="true" />
              </Button>
            )}
          </li>
        ))}
      </ol>
      {canAdd && (
        <Button
          aria-label={`Add to ${label}`}
          onClick={() =>
            void onChange((was) => {
              const l = Array.isArray(was) ? was : [];
              return [...l, emptyOf(items, l[l.length - 1])];
            })
          }
        >
          <PlusIcon aria-hidden="true" />
          Add
        </Button>
      )}
    </fieldset>
  );
}

/** An object's fields, each from its schema. */
function ObjectField({ name, label, schema, value, onChange }: FieldProps) {
  const obj = value !== MIXED && value && typeof value === "object" && !Array.isArray(value) ? (value as Record<string, unknown>) : {};
  const required = new Set(schema.required ?? []);
  return (
    <div data-slot="props-panel-object" role="group" aria-label={label}>
      {Object.entries(schema.properties!).map(([k, s]) => (
        <SchemaField
          key={k}
          name={fieldName(k)}
          label={`${fieldName(k)}, ${label}`}
          schema={s}
          required={required.has(k)}
          value={value === MIXED ? MIXED : obj[k]}
          onChange={(next) =>
            onChange((was) => {
              const o = { ...(was && typeof was === "object" ? (was as Record<string, unknown>) : {}) };
              const v = next(o[k]);
              if (v === undefined) delete o[k];
              else o[k] = v;
              return o;
            })
          }
        />
      ))}
      {name && null}
    </div>
  );
}

/** A field whose schema has no form here: its JSON, sent when left if it parses. */
function JsonField({ name, label, required, value, onChange }: FieldProps) {
  const id = useId();
  const [bad, setBad] = React.useState(false);
  const draft = useDraft(value === MIXED || value === undefined ? "" : JSON.stringify(value, null, 2), (text) => {
    if (!text.trim()) {
      if (!required) void onChange(() => undefined);
      return;
    }
    try {
      const v = JSON.parse(text);
      setBad(false);
      void onChange(() => v);
    } catch {
      setBad(true);
    }
  });
  return (
    <Row name={`${name} (JSON)`} mixed={value === MIXED} htmlFor={id}>
      <Textarea
        id={id}
        aria-label={label}
        aria-invalid={bad || undefined}
        rows={3}
        value={draft.text}
        data-dirty={draft.dirty ? "" : undefined}
        onValueChange={(t: string) => draft.edit(t)}
        onBlur={() => draft.done()}
      />
    </Row>
  );
}

// ── the rest of an item ────────────────────────────────────────────────

function BoxSection({ targets, send }: { targets: Target[]; send: Send }) {
  const boxes = targets.map((t) => t.item as DocBlock | DocFrame);
  const turnable = targets.every((t) => t.key === "blocks");
  const field = (key: "x" | "y" | "w" | "h" | "rotation", name: string, min?: number, max?: number) => (
    <BoxField
      key={key}
      name={name}
      label={`${name}${targets.length > 1 ? " of the selection" : ""}`}
      value={shared(boxes.map((b) => (key === "rotation" ? ((b as DocBlock).rotation ?? 0) : b[key])))}
      min={min}
      max={max}
      onSet={(n) => send(() => targets.map((t) => ({ id: t.id, [key]: key === "rotation" && n === 0 ? undefined : n })))}
    />
  );
  return (
    <section data-slot="props-panel-section" aria-label="Position and size">
      <div data-slot="props-panel-grid">
        {field("x", "X")}
        {field("y", "Y")}
        {field("w", "Width", 8)}
        {field("h", "Height", 8)}
        {turnable && field("rotation", "Rotation", -360, 360)}
      </div>
    </section>
  );
}

function BoxField({
  name,
  label,
  value,
  min,
  max,
  onSet,
}: {
  name: string;
  label: string;
  value: Shared<number>;
  min?: number;
  max?: number;
  onSet: (n: number) => Promise<boolean>;
}) {
  const id = useId();
  const mixed = value === MIXED;
  const draft = useDraft(mixed ? "" : String(Math.round(value * 100) / 100), (text) => {
    let n = Number(text);
    if (text.trim() === "" || !Number.isFinite(n)) return;
    if (min !== undefined) n = Math.max(min, n);
    if (max !== undefined) n = Math.min(max, n);
    void onSet(n);
  });
  return (
    <Row name={name} mixed={mixed} htmlFor={id}>
      <NumberInput id={id} label={label} draft={draft} min={min} max={max} mixed={mixed} />
    </Row>
  );
}

function FrameSection({ doc, targets, send }: { doc: BoardDoc; targets: Target[]; send: Send }) {
  const value = shared(targets.map((t) => (t.item as DocBlock).frame ?? ""));
  return (
    <section data-slot="props-panel-section">
      <Row name="Frame" mixed={value === MIXED}>
        <Pick
          label="Frame it sits in"
          value={value === MIXED ? "" : value || NONE}
          options={[{ value: NONE, label: "No frame" }, ...doc.frames.map((f) => ({ value: f.id, label: f.name || f.id }))]}
          onPick={(v) => void send(() => targets.map((t) => ({ id: t.id, frame: v === NONE ? undefined : v })))}
        />
      </Row>
    </section>
  );
}

function LockSection({ targets, send }: { targets: Target[]; send: Send }) {
  const value = shared(targets.map((t) => !!(t.item as { locked?: boolean }).locked));
  return (
    <section data-slot="props-panel-section">
      <div data-slot="props-panel-row" data-inline="">
        <Checkbox
          aria-label="Locked"
          aria-description="A locked item can't be moved, resized or deleted on the canvas until it's unlocked."
          checked={value === MIXED ? "indeterminate" : value}
          onCheckedChange={(checked) => void send(() => targets.map((t) => ({ id: t.id, locked: checked === true ? true : undefined })))}
        >
          <CheckboxIndicator>
            <CheckIcon aria-hidden="true" />
          </CheckboxIndicator>
        </Checkbox>
        <span data-slot="props-panel-label">
          Locked
          {value === MIXED && <span data-slot="props-panel-mixed"> (mixed)</span>}
        </span>
      </div>
    </section>
  );
}

function InkSection({ targets, send }: { targets: Target[]; send: Send }) {
  const strokes = targets.map((t) => t.item as DocStroke);
  const color = shared(strokes.map((s) => s.color));
  const width = shared(strokes.map((s) => s.width));
  const widths = inkWidths.map((w) => ({ ...w, value: tokenWidth(w.token, w.fallback) }));
  return (
    <section data-slot="props-panel-section" aria-label="Ink">
      <Row name="Colour" mixed={color === MIXED}>
        <RadioGroup
          aria-label="Ink colour"
          data-slot="props-panel-ink"
          value={color === MIXED ? null : color}
          onValueChange={(c) => c != null && void send(() => targets.map((t) => ({ id: t.id, color: String(c) })))}
        >
          {inkColors.map((c) => (
            <Radio key={c.token} value={c.token} aria-label={c.name} title={c.name} style={{ background: inkColor(c.token) }} />
          ))}
        </RadioGroup>
      </Row>
      <Row name="Width" mixed={width === MIXED}>
        <Pick
          label="Ink width"
          value={width === MIXED ? "" : String(widths.find((w) => w.value === width)?.value ?? width)}
          options={[
            ...widths.map((w) => ({ value: String(w.value), label: `${w.name} (${w.value})` })),
            ...(width !== MIXED && !widths.some((w) => w.value === width) ? [{ value: String(width), label: String(width) }] : []),
          ]}
          onPick={(v) => void send(() => targets.map((t) => ({ id: t.id, width: Number(v) })))}
        />
      </Row>
    </section>
  );
}

/** A line's style: its link's props, as board.connect's style describes them. */
type LinkStyle = { start?: string; end?: string; dash?: string; color?: string };

function LinkSection({ doc, link, send, onSelect }: { doc: BoardDoc; link: DocLink; send: Send; onSelect: (ids: string[]) => void }) {
  const style = (link.props ?? {}) as LinkStyle;
  const setStyle = (key: keyof LinkStyle, v: string | undefined) =>
    send((doc) => {
      const was = findItem(doc, link.id)?.item as DocLink | undefined;
      if (!was) return [];
      const props: Record<string, unknown> = { ...was.props };
      if (v === undefined) delete props[key];
      else props[key] = v;
      return [{ id: link.id, props: Object.keys(props).length ? props : undefined }];
    });
  const end = (id: string) => {
    const found = findItem(doc, id);
    if (!found) return id;
    if (found.key === "frames") return `Frame: ${(found.item as DocFrame).name}`;
    if (found.key === "blocks") return blockLabel((found.item as DocBlock).type, (found.item as DocBlock).props);
    return id;
  };
  const labelId = useId();
  const draft = useDraft(link.label ?? "", (text) => void send(() => [{ id: link.id, label: text.trim() ? text : undefined }]));
  return (
    <section data-slot="props-panel-section" aria-label="Line">
      <div data-slot="props-panel-ends">
        <Button onClick={() => onSelect([link.from])} title="Select this end">
          From {end(link.from)}
        </Button>
        <Button onClick={() => onSelect([link.to])} title="Select this end">
          To {end(link.to)}
        </Button>
      </div>
      <Row name="Label" htmlFor={labelId}>
        <Textarea
          id={labelId}
          aria-label="Line label"
          maxLength={2000}
          rows={2}
          placeholder="Write on the line"
          value={draft.text}
          data-dirty={draft.dirty ? "" : undefined}
          onValueChange={(t: string) => draft.edit(t)}
          onBlur={() => draft.done()}
          onKeyDown={(e: React.KeyboardEvent) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              draft.done();
            }
            if (e.key === "Escape") draft.cancel();
          }}
        />
      </Row>
      <div data-slot="props-panel-grid">
        <Row name="Start">
          <Pick
            label="Line start"
            value={style.start === "arrow" ? "arrow" : "none"}
            options={[
              { value: "none", label: "Plain" },
              { value: "arrow", label: "Arrow" },
            ]}
            onPick={(v) => void setStyle("start", v === "arrow" ? "arrow" : undefined)}
          />
        </Row>
        <Row name="End">
          <Pick
            label="Line end"
            value={style.end === "arrow" ? "arrow" : "none"}
            options={[
              { value: "none", label: "Plain" },
              { value: "arrow", label: "Arrow" },
            ]}
            onPick={(v) => void setStyle("end", v === "arrow" ? "arrow" : undefined)}
          />
        </Row>
      </div>
      <Row name="Dash">
        <Pick
          label="Line dash"
          value={style.dash === "dashed" || style.dash === "dotted" ? style.dash : "solid"}
          options={[
            { value: "solid", label: "Solid" },
            { value: "dashed", label: "Dashed" },
            { value: "dotted", label: "Dotted" },
          ]}
          onPick={(v) => void setStyle("dash", v === "solid" ? undefined : v)}
        />
      </Row>
      <Row name="Colour">
        <div data-slot="props-panel-inline">
          <Swatches label="Line colour" colors={blockColors} value={style.color ?? ""} onColor={(c) => void setStyle("color", c)} />
          {style.color && (
            <Button aria-label="Line colour: default" title="Default colour" onClick={() => void setStyle("color", undefined)}>
              <XIcon aria-hidden="true" />
            </Button>
          )}
        </div>
      </Row>
    </section>
  );
}

/** A block's lines, each a button that picks it, so lines are reachable from the keyboard. */
function LinesSection({ doc, id, onPickLink }: { doc: BoardDoc; id: string; onPickLink: (id: string) => void }) {
  const lines = doc.links.filter((l) => l.from === id || l.to === id);
  if (!lines.length) return null;
  const name = (other: string) => {
    const found = findItem(doc, other);
    if (found?.key === "blocks") return blockLabel((found.item as DocBlock).type, (found.item as DocBlock).props);
    if (found?.key === "frames") return `Frame: ${(found.item as DocFrame).name}`;
    return other;
  };
  return (
    <section data-slot="props-panel-section" aria-label="Lines">
      <h3 data-slot="props-panel-label">Lines</h3>
      <ul data-slot="props-panel-lines">
        {lines.map((l) => (
          <li key={l.id}>
            <Button onClick={() => onPickLink(l.id)} title="Edit this line">
              {l.from === id ? `To ${name(l.to)}` : `From ${name(l.from)}`}
              {l.label ? ` (${l.label})` : ""}
            </Button>
          </li>
        ))}
      </ul>
    </section>
  );
}

function WhoSection({ targets }: { targets: Target[] }) {
  const names = (key: "createdBy" | "updatedBy") => [
    ...new Set(targets.map((t) => (t.item as { createdBy?: string; updatedBy?: string })[key]).filter((s): s is string => !!s).map(who)),
  ];
  const made = names("createdBy");
  const changed = names("updatedBy");
  if (!made.length && !changed.length) return null;
  return (
    <footer data-slot="props-panel-who">
      {made.length > 0 && (
        <p>
          Made by <strong>{made.join(", ")}</strong>
        </p>
      )}
      {changed.length > 0 && (
        <p>
          Last changed by <strong>{changed.join(", ")}</strong>
        </p>
      )}
    </footer>
  );
}

/**
 * The window's selection for the canvas and the panel together: the
 * canvas's blocks (controlled, so the panel sees them) or one picked line.
 * Spread `board` on <Board>, `panel` on <PropsPanel>, and `onPointerDownCapture`
 * on what holds them, so a press elsewhere on the board lets go of a line.
 */
function usePropsSelection(doc: BoardDoc | null) {
  const [selected, setSelected] = React.useState<string[]>([]);
  const [link, setLink] = React.useState<string | null>(null);
  const pickLink = React.useCallback((id: string) => {
    setSelected([]);
    setLink(id);
  }, []);
  const links = doc?.links;
  const looks = React.useMemo(() => new Map((links ?? []).map((l) => [l.id, lineLook(l)])), [links]);
  return {
    board: {
      selected,
      onSelectedChange: (next: string[]) => {
        setSelected(next);
        if (next.length) setLink(null);
      },
      selectedLine: link,
      onLinePress: pickLink,
      lineLook: (id: string) => looks.get(id),
    },
    panel: {
      selected,
      link,
      onPickLink: pickLink,
      onSelect: (ids: string[]) => {
        setLink(null);
        setSelected(ids);
      },
    },
    // F6 hops from the board to the panel and back, so the keyboard reaches it
    // without tabbing past every block.
    onKeyDownCapture: (e: React.KeyboardEvent) => {
      if (e.key !== "F6" || e.altKey || e.ctrlKey || e.metaKey) return;
      const root = e.currentTarget as HTMLElement;
      const panel = root.querySelector<HTMLElement>('[data-slot="props-panel"]');
      if (!panel?.hasAttribute("data-open")) return;
      e.preventDefault();
      e.stopPropagation();
      if (panel.contains(e.target as Node)) {
        const back =
          root.querySelector<HTMLElement>('[data-slot="board-block"][data-selected]') ?? root.querySelector<HTMLElement>('[data-slot="board"]');
        back?.focus();
      } else {
        panel.querySelector<HTMLElement>(focusable)?.focus();
      }
    },
    onPointerDownCapture: (e: React.PointerEvent) => {
      const t = e.target as Element;
      if (link && t.closest?.('[data-slot="board"]') && !t.closest('[data-slot="board-line-hit"]')) setLink(null);
    },
  };
}

/** What can take focus in the panel, in order. */
const focusable = 'input:not([disabled]), textarea:not([disabled]), button:not([disabled]):not([tabindex="-1"]), [role="radio"][tabindex="0"], [tabindex="0"]';

/** How a link's line looks on the canvas: its label and style. */
function lineLook(l: DocLink): LineLook | undefined {
  const s = (l.props ?? {}) as LinkStyle;
  if (!l.label && !l.props && !l.locked) return undefined;
  return {
    label: l.label,
    start: s.start === "arrow" ? "arrow" : undefined,
    end: s.end === "arrow" ? "arrow" : undefined,
    dash: s.dash === "dashed" || s.dash === "dotted" ? s.dash : undefined,
    color: typeof s.color === "string" && blockColors.includes(s.color) ? s.color : undefined,
    locked: l.locked || undefined,
  };
}

export { lineLook, PropsPanel, SchemaField, sharedFields, targetsOf, usePropsSelection, type PropsPanelProps };
