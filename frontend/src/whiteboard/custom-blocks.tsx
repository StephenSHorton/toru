// Draws custom blocks (whiteboard#27): block types agents designed with
// board.block.define. A definition's layout is a tree of the parts in
// internal/board/blockparts.json, and each part is drawn here with Toru's
// own UI SDK components, bound to one of the block's props. Nothing in a
// definition is ever run: text is shown as text (React escapes it), there
// is no markup, no style, no address and no script, so a board from anyone
// stays as safe as the built-in blocks.
//
// Every part is defensive: a prop of the wrong shape shows as empty, an
// unknown part is skipped, and a block whose type the window doesn't have
// (or whose layout throws) shows as a plain placeholder card.
import * as React from "react";
import { Button } from "@ui-sdk/react/button";
import { Checkbox, CheckboxIndicator } from "@ui-sdk/react/checkbox";
import { Input } from "@ui-sdk/react/input";
import { Meter, MeterIndicator, MeterTrack } from "@ui-sdk/react/meter";
import { Radio, RadioGroup, RadioIndicator } from "@ui-sdk/react/radio";
import { Slider, SliderControl, SliderIndicator, SliderThumb, SliderTrack } from "@ui-sdk/react/slider";
import { Textarea } from "@ui-sdk/react/textarea";
import {
  BookmarkIcon,
  BoxIcon,
  BugIcon,
  CalendarIcon,
  ChartBarIcon,
  CheckIcon,
  CircleHelpIcon,
  ClockIcon,
  FileTextIcon,
  FlagIcon,
  FlameIcon,
  GitBranchIcon,
  HeartIcon,
  ImageIcon,
  InfoIcon,
  LayersIcon,
  LightbulbIcon,
  ListIcon,
  MapPinIcon,
  MegaphoneIcon,
  MessageSquareIcon,
  MinusIcon,
  PackageIcon,
  PlusIcon,
  PuzzleIcon,
  RocketIcon,
  ShieldIcon,
  SmileIcon,
  SparklesIcon,
  SquareCheckIcon,
  StarIcon,
  StickyNoteIcon,
  TableIcon,
  TagIcon,
  TargetIcon,
  ThumbsUpIcon,
  TriangleAlertIcon,
  TrophyIcon,
  UserIcon,
  UsersIcon,
  VoteIcon,
  WrenchIcon,
  XIcon,
  ZapIcon,
  type LucideIcon,
} from "lucide-react";
import type { BlockColor, Schema } from "./catalog";
import type { BlockDef, LayoutNode } from "./custom-types";
import { useBoardImages } from "./images";
import "./custom-blocks.css";

/** The icons blockparts.json allows, by name. */
const icons: Record<string, LucideIcon> = {
  box: BoxIcon,
  bookmark: BookmarkIcon,
  bug: BugIcon,
  calendar: CalendarIcon,
  "chart-bar": ChartBarIcon,
  "circle-help": CircleHelpIcon,
  clock: ClockIcon,
  "file-text": FileTextIcon,
  flag: FlagIcon,
  flame: FlameIcon,
  "git-branch": GitBranchIcon,
  heart: HeartIcon,
  image: ImageIcon,
  info: InfoIcon,
  layers: LayersIcon,
  lightbulb: LightbulbIcon,
  list: ListIcon,
  "map-pin": MapPinIcon,
  megaphone: MegaphoneIcon,
  "message-square": MessageSquareIcon,
  package: PackageIcon,
  puzzle: PuzzleIcon,
  rocket: RocketIcon,
  shield: ShieldIcon,
  smile: SmileIcon,
  sparkles: SparklesIcon,
  "square-check": SquareCheckIcon,
  star: StarIcon,
  "sticky-note": StickyNoteIcon,
  table: TableIcon,
  tag: TagIcon,
  target: TargetIcon,
  "thumbs-up": ThumbsUpIcon,
  "triangle-alert": TriangleAlertIcon,
  trophy: TrophyIcon,
  user: UserIcon,
  users: UsersIcon,
  vote: VoteIcon,
  wrench: WrenchIcon,
  zap: ZapIcon,
};

/** An allowed icon by name; a box for any other. */
function Icon({ icon, ...rest }: { icon?: unknown } & Omit<React.SVGProps<SVGSVGElement>, "name">) {
  const I = (typeof icon === "string" && icons[icon]) || BoxIcon;
  return <I aria-hidden="true" {...(rest as object)} />;
}

type Change = { source?: string; reason?: string };
type Props = Record<string, unknown>;

/** The built-in renderers a part reuses (blocks.tsx passes them, to keep the modules apart). */
type BuiltinRender = (p: {
  id: string;
  label: string;
  props: Record<string, unknown>;
  onProps: (next: Record<string, unknown>, details: Change) => boolean;
}) => React.ReactNode;

type Ctx = {
  id: string;
  def: BlockDef;
  /** The block's name, like "Feature idea: Dark mode", which every control's name starts from. */
  name: string;
  props: Props;
  schema: Record<string, Schema>;
  set: (key: string, value: unknown, details: Change) => boolean;
  builtins: Record<string, BuiltinRender | undefined>;
};

const changeOf = (details: unknown): Change => {
  const d = (details ?? {}) as Change;
  return { source: d.source, reason: d.reason };
};

const str = (v: unknown) => (typeof v === "string" ? v : "");
const num = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? v : undefined);
const list = (v: unknown) => (Array.isArray(v) ? v : []);
const isColor = (v: unknown): v is BlockColor =>
  typeof v === "string" && ["yellow", "pink", "blue", "green", "purple", "orange", "grey"].includes(v);

/** A template's text: {name} becomes the prop's value, as plain text. */
function fill(template: unknown, props: Props) {
  return str(template).replace(/\{([^{}]*)\}/g, (_, key: string) => {
    const v = props[key];
    return typeof v === "string" || typeof v === "number" || typeof v === "boolean" ? String(v) : "";
  });
}

/** A control's name: what it is, then the block it's in. */
const named = (what: string, c: Ctx) => (what ? `${what} in ${c.name}` : c.name);

/** A built-in part, under the part's own short label. */
function Builtin({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div data-slot="custom-builtin">
      <span data-slot="custom-label">{label || " "}</span>
      {children}
    </div>
  );
}

function Part(props: { node: unknown; c: Ctx; path: string }): React.ReactNode {
  const out = PartInner(props);
  const part = (props.node as LayoutNode | null)?.part;
  if (out && (part === "rating" || part === "checklist" || part === "tags" || part === "poll" || part === "table")) {
    return <Builtin label={str((props.node as LayoutNode).label)}>{out}</Builtin>;
  }
  return out;
}

function PartInner({ node, c, path }: { node: unknown; c: Ctx; path: string }): React.ReactNode {
  if (!node || typeof node !== "object") return null;
  const n = node as LayoutNode;
  const prop = str(n.prop);
  const value = prop ? c.props[prop] : undefined;
  const label = str(n.label);
  switch (n.part) {
    case "stack":
      return (
        <div
          data-slot="custom-stack"
          data-direction={n.direction === "row" ? "row" : "column"}
          data-gap={str(n.gap) || "s"}
          data-align={str(n.align) || undefined}
          data-justify={str(n.justify) || undefined}
          data-tone={str(n.tone) || undefined}
          data-wrap={n.wrap === true || undefined}
        >
          {list(n.children).map((kid, i) => (
            <Part key={`${path}.${i}`} node={kid} c={c} path={`${path}.${i}`} />
          ))}
        </div>
      );
    case "heading":
      return (
        <div data-slot="custom-heading" data-size={str(n.size) || "m"}>
          {fill(n.text, c.props)}
        </div>
      );
    case "text":
      return (
        <p data-slot="custom-text" data-size={str(n.size) || "m"} data-tone={str(n.tone) || undefined} data-weight={str(n.weight) || undefined}>
          {fill(n.text, c.props)}
        </p>
      );
    case "badge": {
      const text = prop ? (value == null ? "" : String(value)) : fill(n.text, c.props);
      const colors = (n.colors && typeof n.colors === "object" ? n.colors : {}) as Record<string, unknown>;
      const color = prop && isColor(colors[text]) ? colors[text] : isColor(n.color) ? n.color : "grey";
      if (!text) return null;
      return (
        <span data-slot="custom-badge" data-color={color}>
          {text}
        </span>
      );
    }
    case "icon":
      return <Icon icon={n.icon} data-slot="custom-icon" data-color={isColor(n.color) ? n.color : undefined} />;
    case "divider":
      return <hr data-slot="custom-divider" />;
    case "field":
      return <FieldPart c={c} prop={prop} label={label} placeholder={str(n.placeholder)} multiline={n.multiline === true} />;
    case "checkbox":
      return (
        <label data-slot="custom-checkbox">
          <Checkbox
            aria-label={named(label, c)}
            checked={value === true}
            onCheckedChange={(checked, details) => c.set(prop, checked === true, changeOf(details))}
          >
            <CheckboxIndicator>
              <CheckIcon aria-hidden="true" />
            </CheckboxIndicator>
          </Checkbox>
          <span>{label}</span>
        </label>
      );
    case "choice": {
      const options = c.schema[prop]?.enum ?? [];
      return (
        <div data-slot="custom-choice" data-style={n.style === "segmented" ? "segmented" : "radio"}>
          {label && <span data-slot="custom-label">{label}</span>}
          <RadioGroup
            aria-label={named(label || prop, c)}
            value={typeof value === "string" ? value : null}
            onValueChange={(next, details) => {
              if (next != null) c.set(prop, String(next), changeOf(details));
            }}
          >
            {/* The whole option picks it: the segmented style hides the radio itself. */}
            {options.map((o) => (
              <label key={o} onClick={() => { if (value !== o) c.set(prop, o, { source: "person" }); }}>
                <Radio value={o} aria-label={o}>
                  <RadioIndicator />
                </Radio>
                <span>{o}</span>
              </label>
            ))}
          </RadioGroup>
        </div>
      );
    }
    case "counter": {
      const v = num(value) ?? 0;
      const s = c.schema[prop] ?? {};
      return (
        <div data-slot="custom-counter">
          {n.minus === true && (
            <Button
              aria-label={`${label} minus one in ${c.name}`}
              disabled={s.minimum !== undefined && v - 1 < s.minimum}
              onClick={() => c.set(prop, v - 1, {})}
            >
              <MinusIcon aria-hidden="true" />
            </Button>
          )}
          <Button
            aria-label={named(label, c)}
            disabled={s.maximum !== undefined && v + 1 > s.maximum}
            onClick={() => c.set(prop, v + 1, {})}
          >
            {n.icon ? <Icon icon={n.icon} /> : <PlusIcon aria-hidden="true" />}
            <span>{label}</span>
          </Button>
          <span data-slot="block-count" aria-label={`${label}: ${v}`}>
            {v}
          </span>
        </div>
      );
    }
    case "slider": {
      const s = c.schema[prop] ?? {};
      const min = num(n.min) ?? s.minimum ?? 0;
      const max = num(n.max) ?? s.maximum ?? 100;
      const step = num(n.step) ?? (s.type === "integer" ? 1 : (max - min) / 100 || 1);
      const v = Math.min(max, Math.max(min, num(value) ?? min));
      return (
        <div data-slot="custom-slider">
          <span data-slot="custom-label">
            {label} <span data-slot="block-count">{v}</span>
          </span>
          <Slider
            value={v}
            min={min}
            max={max}
            step={step}
            onValueChange={(next: number | number[], details: unknown) => {
              const x = Array.isArray(next) ? next[0] : next;
              c.set(prop, s.type === "integer" ? Math.round(x) : x, changeOf(details));
            }}
          >
            <SliderControl>
              <SliderTrack>
                <SliderIndicator />
                <SliderThumb aria-label={named(label, c)} />
              </SliderTrack>
            </SliderControl>
          </Slider>
        </div>
      );
    }
    case "progress": {
      const s = c.schema[prop] ?? {};
      const max = num(n.max) ?? s.maximum ?? 100;
      const v = Math.min(max, Math.max(0, num(value) ?? 0));
      return (
        <div data-slot="custom-progress">
          {label && (
            <span data-slot="custom-label">
              {label} <span data-slot="block-count">{`${v}/${max}`}</span>
            </span>
          )}
          <Meter value={v} min={0} max={max} aria-label={named(label || prop, c)}>
            <MeterTrack>
              <MeterIndicator />
            </MeterTrack>
          </Meter>
        </div>
      );
    }
    case "rating": {
      const Render = c.builtins.rating;
      if (!Render) return null;
      const max = Math.max(1, Math.min(10, Math.round(num(n.max) ?? c.schema[prop]?.maximum ?? 5)));
      const v = num(value);
      return (
        <Render
          id={`${c.id}-${path}`}
          label={named(label, c)}
          props={{ title: `${label} for ${c.name}`, max, ...(v !== undefined ? { value: v } : {}) }}
          onProps={(next, d) => {
            const x = num(next.value);
            return c.set(prop, x === undefined ? undefined : Math.round(x), d);
          }}
        />
      );
    }
    case "checklist": {
      const Render = c.builtins.checklist;
      if (!Render) return null;
      const items = list(value).filter((x) => x && typeof x === "object" && typeof (x as { text?: unknown }).text === "string");
      return (
        <Render
          id={`${c.id}-${path}`}
          label={named(label, c)}
          props={{ title: label ? `${label} in ${c.name}` : c.name, items }}
          onProps={(next, d) => c.set(prop, next.items, d)}
        />
      );
    }
    case "list":
      return <ListPart c={c} prop={prop} label={label} numbered={n.numbered === true} />;
    case "tags": {
      const Render = c.builtins["tag-list"];
      if (!Render) return null;
      const items = list(value).filter((x): x is string => typeof x === "string");
      return (
        <Render
          id={`${c.id}-${path}`}
          label={named(label, c)}
          props={{ title: label ? `${label} in ${c.name}` : c.name, items }}
          onProps={(next, d) => c.set(prop, next.items, d)}
        />
      );
    }
    case "poll": {
      const Render = c.builtins.poll;
      if (!Render) return null;
      const options = list(value).filter((x) => x && typeof x === "object" && typeof (x as { label?: unknown }).label === "string");
      return (
        <Render
          id={`${c.id}-${path}`}
          label={named(label, c)}
          props={{ question: label ? `${label} in ${c.name}` : c.name, options, multi: n.multi === true }}
          onProps={(next, d) => c.set(prop, next.options, d)}
        />
      );
    }
    case "table": {
      const Render = c.builtins.table;
      if (!Render) return null;
      const columns = list(n.columns).filter((x): x is string => typeof x === "string");
      const rows = list(value).filter(Array.isArray).map((r) => (r as unknown[]).map((x) => (typeof x === "string" ? x : "")));
      return (
        <Render
          id={`${c.id}-${path}`}
          label={named(label, c)}
          props={{ title: label ? `${label} in ${c.name}` : c.name, columns, rows }}
          onProps={(next, d) => {
            // The columns belong to the block type, not the block: a change to them isn't kept.
            const cols = list(next.columns);
            if (cols.length !== columns.length || cols.some((x, i) => x !== columns[i])) return false;
            return c.set(prop, next.rows ?? [], d);
          }}
        />
      );
    }
    case "image":
      return <ImagePart value={value} alt={str(n.alt)} />;
  }
  return null;
}

function FieldPart({ c, prop, label, placeholder, multiline }: { c: Ctx; prop: string; label: string; placeholder: string; multiline: boolean }) {
  const s = c.schema[prop] ?? {};
  const value = str(c.props[prop]);
  const required = (c.def.props as Schema).required?.includes(prop) && (s.minLength ?? 0) > 0;
  const [draft, setDraft] = React.useState(value);
  React.useEffect(() => setDraft(value), [value]);
  const common = {
    "aria-label": named(label || prop, c),
    maxLength: s.maxLength,
    placeholder: placeholder || label,
    value: draft,
    onValueChange: (next: string, details: unknown) => {
      // A text the schema needs shows as typed, and is kept once it's allowed.
      if (c.set(prop, next, changeOf(details)) || (required && !next.trim())) setDraft(next);
    },
    onBlur: () => setDraft(value),
  };
  return (
    <div data-slot="custom-field" data-multiline={multiline || undefined}>
      {label && <span data-slot="custom-label">{label}</span>}
      {multiline ? <Textarea {...common} /> : <Input {...common} />}
    </div>
  );
}

function ListPart({ c, prop, label, numbered }: { c: Ctx; prop: string; label: string; numbered: boolean }) {
  const items = list(c.props[prop]).filter((x): x is string => typeof x === "string");
  const [draft, setDraft] = React.useState("");
  const what = label || prop;
  const add = (details: Change) => {
    const text = draft.trim();
    if (text && c.set(prop, [...items, text], details)) setDraft("");
  };
  const Tag = numbered ? "ol" : "ul";
  return (
    <div data-slot="custom-list">
      {label && <span data-slot="custom-label">{label}</span>}
      <Tag>
        {items.map((it, i) => (
          <li key={`${i}-${it}`}>
            <span>{it}</span>
            <Button
              aria-label={`Remove item ${i + 1} from ${what} in ${c.name}`}
              data-part="block-remove"
              className="block-extra"
              data-agent-confirm={`Removes "${it}" from ${what} in ${c.name}`}
              onClick={() => c.set(prop, items.filter((_, j) => j !== i), {})}
            >
              <XIcon aria-hidden="true" />
            </Button>
          </li>
        ))}
      </Tag>
      <div data-slot="block-add">
        <Input
          aria-label={`New item for ${what} in ${c.name}`}
          placeholder="Add an item"
          value={draft}
          onValueChange={(v) => setDraft(v)}
          onKeyDown={(e) => {
            if (e.key === "Enter") add({ source: "person" });
          }}
        />
        <Button aria-label={`Add to ${what} in ${c.name}`} onClick={() => add({})} disabled={!draft.trim()}>
          <PlusIcon aria-hidden="true" />
        </Button>
      </div>
    </div>
  );
}

function ImagePart({ value, alt }: { value: unknown; alt: string }) {
  const images = useBoardImages();
  const asset = str(value);
  const url = asset ? images.url(asset) : "";
  if (!url) {
    return (
      <div data-slot="custom-image" data-empty="">
        <ImageIcon aria-hidden="true" />
      </div>
    );
  }
  return (
    <figure data-slot="custom-image">
      <img src={url} alt={alt} draggable={false} />
    </figure>
  );
}

/** A custom block: its type's icon and label, then its layout. */
function CustomBlock({
  id,
  def,
  name,
  props,
  onProps,
  builtins,
  footer,
}: {
  id: string;
  def: BlockDef;
  name: string;
  props: Props;
  onProps: (next: Props, details: Change) => boolean;
  builtins: Record<string, BuiltinRender | undefined>;
  footer?: React.ReactNode;
}) {
  const schema = ((def.props as Schema).properties ?? {}) as Record<string, Schema>;
  // Changes build on the latest props, not this render's: two quick changes
  // (a click on Vote right after one on a status) each keep the other.
  const latest = React.useRef(props);
  latest.current = props;
  const c: Ctx = {
    id,
    def,
    name,
    props,
    schema,
    builtins,
    set: (key, value, details) => {
      if (!key || !(key in schema)) return false;
      const next = { ...latest.current };
      if (value === undefined) delete next[key];
      else next[key] = value;
      const kept = onProps(next, details);
      if (kept) latest.current = next;
      return kept;
    },
  };
  return (
    <div data-slot="block-custom" data-type={def.type}>
      <div data-slot="custom-kind">
        <Icon icon={def.icon} />
        <span>{def.label}</span>
        {footer}
      </div>
      <div data-slot="custom-body">
        <Part node={def.layout} c={c} path="0" />
      </div>
    </div>
  );
}

/** A block the window can't draw: an unknown type, or a custom type whose layout broke. Never a crash. */
function PlaceholderBlock({ type, reason, footer }: { type: string; reason: string; footer?: React.ReactNode }) {
  return (
    <div data-slot="block-placeholder" role="note" aria-label={`Block of type ${type}: ${reason}`}>
      <BoxIcon aria-hidden="true" />
      <strong>{type}</strong>
      <span>{reason}</span>
      {footer}
    </div>
  );
}

/** Catches a custom block's render error and shows the placeholder in its place. */
class CustomBoundary extends React.Component<
  { type: string; resetKey: unknown; children: React.ReactNode },
  { failed: boolean; key: unknown }
> {
  state = { failed: false, key: this.props.resetKey };
  static getDerivedStateFromError() {
    return { failed: true };
  }
  static getDerivedStateFromProps(p: { resetKey: unknown }, s: { failed: boolean; key: unknown }) {
    return p.resetKey !== s.key ? { failed: false, key: p.resetKey } : null;
  }
  componentDidCatch() {}
  render() {
    if (this.state.failed) return <PlaceholderBlock type={this.props.type} reason="This block couldn't be drawn." />;
    return this.props.children;
  }
}

export { CustomBlock, CustomBoundary, Icon as CustomIcon, PlaceholderBlock, type BuiltinRender };
