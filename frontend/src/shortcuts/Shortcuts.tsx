import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Kbd, KbdGroup } from "@/components/ui/kbd";
import { Switch } from "@/components/ui/switch";
import { Keyboard, SlidersHorizontal } from "lucide-react";
import { HotkeyService, Shortcut } from "@/lib/api";
import { cn } from "@/lib/utils";
import { useShortcuts } from "./useShortcuts";

// Actions that render their shortcut row inside their own Settings panel instead
// of this generic list (the replay hotkey lives in the Instant replay panel).
const PANEL_OWNED_ACTIONS = new Set(["replaySave", "recordToggle", "timelineMarker"]);

// The four modifiers in display order, with their wire side names.
const MODIFIERS = [
  { id: "win", label: "Win", left: "lwin", right: "rwin" },
  { id: "ctrl", label: "Ctrl", left: "lctrl", right: "rctrl" },
  { id: "alt", label: "Alt", left: "lalt", right: "ralt" },
  { id: "shift", label: "Shift", left: "lshift", right: "rshift" },
] as const;
type Modifier = (typeof MODIFIERS)[number];
type Side = "either" | "left" | "right";

// sideOf reports which side of a modifier the shortcut requires.
function sideOf(sc: Shortcut, mod: Modifier): Side {
  const sides = sc.sides ?? [];
  if (sides.includes(mod.left)) return "left";
  if (sides.includes(mod.right)) return "right";
  return "either";
}

// Keys RegisterHotKey can't take as a shortcut's key: mouse buttons and a
// modifier used alone. Mirrors hookOnly in internal/hotkey/match.go.
const HOOK_ONLY_KEYS = new Set([
  "MouseMiddle",
  "Mouse4",
  "Mouse5",
  "LCtrl",
  "RCtrl",
  "LShift",
  "RShift",
  "LAlt",
  "RAlt",
]);

// hookOnly reports whether only Toru's keyboard/mouse hooks can match the
// shortcut, so it doesn't fire while an administrator window is focused.
function hookOnly(sc: Shortcut): boolean {
  return (
    (sc.sides ?? []).length > 0 ||
    sc.loose ||
    (sc.extra ?? []).length > 0 ||
    HOOK_ONLY_KEYS.has(sc.key)
  );
}

// isSnip reports whether a combo is Win+Shift+S (the Snipping Tool combo we
// override). Shown as a small note so the behavior is never a surprise.
function isSnip(sc: Shortcut): boolean {
  return (
    sc.win && sc.shift && !sc.ctrl && !sc.alt && sc.key.toUpperCase() === "S" && (sc.extra ?? []).length === 0
  );
}

// The Windows-logo key has no reliable single character in system fonts, so ⊞
// (U+229E) is the conventional stand-in (Microsoft's own docs use it). The title
// gives a hover label since the glyph isn't self-evident.
function WinKey({ side }: { side: Side }) {
  const prefix = side === "left" ? "L" : side === "right" ? "R" : "";
  return <Kbd title="Windows key">{prefix}⊞</Kbd>;
}

// ComboKeys renders a combo as key-caps: ⊞ + Ctrl + Alt + Shift + KEY (+ more
// chord keys). A modifier narrowed to one side shows as LCtrl / RShift.
function ComboKeys({ sc }: { sc: Shortcut }) {
  if (!sc.key) return <span className="px-1 text-xs text-muted-foreground">Not set</span>;
  const keys: React.ReactNode[] = [];
  for (const mod of MODIFIERS) {
    if (!sc[mod.id]) continue;
    const side = sideOf(sc, mod);
    if (mod.id === "win") {
      keys.push(<WinKey key="win" side={side} />);
      continue;
    }
    const prefix = side === "left" ? "L" : side === "right" ? "R" : "";
    keys.push(<Kbd key={mod.id}>{prefix + mod.label}</Kbd>);
  }
  [sc.key, ...(sc.extra ?? [])].forEach((k) => keys.push(<Kbd key={`k-${k}`}>{k}</Kbd>));
  return (
    <KbdGroup>
      {keys.flatMap((k, i) =>
        i === 0
          ? [k]
          : [
              <span key={`plus${i}`} className="text-xs text-muted-foreground">
                +
              </span>,
              k,
            ],
      )}
    </KbdGroup>
  );
}

// SideChoice is the per-modifier "either / left only / right only" picker.
function SideChoice({
  mod,
  value,
  disabled,
  onChange,
}: {
  mod: Modifier;
  value: Side;
  disabled: boolean;
  onChange: (side: Side) => void;
}) {
  const options: { side: Side; label: string }[] = [
    { side: "either", label: "Either" },
    { side: "left", label: "Left" },
    { side: "right", label: "Right" },
  ];
  return (
    <div className="flex items-center justify-between gap-3">
      <span className="text-xs text-muted-foreground">{mod.label}</span>
      <div className="flex border border-border" role="radiogroup" aria-label={`${mod.label} side`}>
        {options.map((o) => (
          <button
            key={o.side}
            type="button"
            role="radio"
            aria-checked={value === o.side}
            disabled={disabled}
            onClick={() => onChange(o.side)}
            className={cn(
              "px-2 py-0.5 text-xs disabled:opacity-50",
              value === o.side ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:text-foreground",
            )}
          >
            {o.label}
          </button>
        ))}
      </div>
    </div>
  );
}

// ShortcutRow is one rebindable action. "Change" (or clicking the keys) starts
// a recording: the user presses the new combo and it's saved straight away.
// Recording happens in Go's keyboard hook (HotkeyService.RecordShortcut), so
// Win+… combos, PrintScreen and Toru's own hotkeys are captured instead of
// firing, and Esc cancels. Keys held together record as a chord, and the
// middle and side mouse buttons can be recorded too. The options panel narrows
// modifiers to one side, turns on loose matching and (with onClear) unbinds.
export function ShortcutRow({
  sc,
  onSave,
  onReset,
  onClear,
}: {
  sc: Shortcut;
  onSave: (action: string, draft: Shortcut) => Promise<string>;
  onReset: (action: string) => Promise<void>;
  onClear?: (action: string) => Promise<string>;
}) {
  const [recording, setRecording] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const [options, setOptions] = useState(false);
  const recordingRef = useRef(false);

  // Don't leave the hook swallowing keys if the panel goes away mid-recording.
  useEffect(
    () => () => {
      if (recordingRef.current) void HotkeyService.CancelRecording();
    },
    [],
  );

  async function save(draft: Partial<Shortcut>) {
    setBusy(true);
    try {
      const e = await onSave(sc.action, Shortcut.createFrom({ ...sc, ...draft, action: sc.action, label: sc.label }));
      setErr(e ? e.replace(/^Error:\s*/, "") : "");
    } finally {
      setBusy(false);
    }
  }

  async function record() {
    setErr("");
    setRecording(true);
    recordingRef.current = true;
    try {
      const got = await HotkeyService.RecordShortcut();
      if (!got.key) return; // cancelled (Esc / Cancel / timed out)
      // A new combo starts with either-side modifiers; keep the loose choice.
      await save({ ...got, sides: [], loose: sc.loose });
    } catch (e) {
      setErr(String(e).replace(/^Error:\s*/, ""));
    } finally {
      recordingRef.current = false;
      setRecording(false);
    }
  }

  async function reset() {
    setBusy(true);
    await onReset(sc.action);
    setBusy(false);
    setErr("");
  }

  async function clear() {
    if (!onClear) return;
    setBusy(true);
    const e = await onClear(sc.action);
    setErr(e ? e.replace(/^Error:\s*/, "") : "");
    setBusy(false);
  }

  function setSide(mod: Modifier, side: Side) {
    const rest = (sc.sides ?? []).filter((s) => s !== mod.left && s !== mod.right);
    const sides = side === "left" ? [...rest, mod.left] : side === "right" ? [...rest, mod.right] : rest;
    void save({ sides });
  }

  const heldMods = MODIFIERS.filter((m) => sc[m.id]);
  const extended = hookOnly(sc);

  return (
    <div className="flex flex-col gap-1.5 border-t border-border pt-2 first:border-t-0 first:pt-0">
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm">{sc.label}</span>
        <div className="flex items-center gap-2">
          {recording ? (
            <>
              <span
                className="animate-pulse border border-primary bg-primary/10 px-2.5 py-1 text-xs text-primary"
                role="status"
              >
                Press your new shortcut…
              </span>
              <Button size="sm" variant="ghost" onClick={() => void HotkeyService.CancelRecording()}>
                Cancel
              </Button>
            </>
          ) : (
            <>
              <button
                type="button"
                onClick={() => void record()}
                disabled={busy}
                title="Click, then press the keys you want"
                className={cn("border border-transparent p-0.5 hover:border-primary/60")}
              >
                <ComboKeys sc={sc} />
              </button>
              <Button size="sm" variant="secondary" onClick={() => void record()} disabled={busy}>
                Change
              </Button>
              <Button
                size="sm"
                variant="ghost"
                onClick={() => setOptions((o) => !o)}
                aria-expanded={options}
                title="Shortcut options"
                className={cn(options && "text-primary")}
              >
                <SlidersHorizontal className="size-3.5" />
              </Button>
            </>
          )}
        </div>
      </div>

      {recording && (
        <p className="text-xs text-muted-foreground">
          Hold any of Ctrl, Alt, Shift or Win and press a key, or press a single key like F9,
          PrintScreen, a numpad key or a mouse side button. Hold several keys together for a chord.
          Esc cancels. Your shortcuts are paused until you're done.
        </p>
      )}

      {options && !recording && (
        <div className="flex flex-col gap-2 border border-border bg-muted/30 p-2.5">
          {heldMods.map((mod) => (
            <SideChoice
              key={mod.id}
              mod={mod}
              value={sideOf(sc, mod)}
              disabled={busy || !sc.key}
              onChange={(side) => setSide(mod, side)}
            />
          ))}
          <div className="flex items-center justify-between gap-3">
            <span className="text-xs text-muted-foreground">Fire even if other modifiers are held</span>
            <Switch
              checked={sc.loose}
              disabled={busy || !sc.key}
              onCheckedChange={(loose) => void save({ loose })}
              aria-label="Loose matching"
            />
          </div>
          {extended && (
            <p className="text-xs text-muted-foreground">
              This shortcut doesn't work while a window running as administrator is focused
              (true of chords, left/right-only, loose, mouse-button and modifier-only shortcuts).
            </p>
          )}
          {onClear && sc.key && (
            <Button size="sm" variant="ghost" className="self-start" onClick={() => void clear()} disabled={busy}>
              Clear shortcut
            </Button>
          )}
        </div>
      )}

      {err && <p className="text-xs text-destructive">{err}</p>}
      {!recording && !sc.isDefault && (
        <button
          type="button"
          className="self-end text-xs text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
          onClick={() => void reset()}
          disabled={busy}
        >
          Reset to default
        </button>
      )}
      {isSnip(sc) && (
        <p className="text-xs text-muted-foreground">
          Overrides the Windows Snipping Tool shortcut while Toru is running.
        </p>
      )}
    </div>
  );
}

// Shortcuts — frosted panel listing the configurable global shortcuts. Sharp
// corners, dark, lucide + shadcn Button. Reads/writes only via @/lib/api.
export function Shortcuts({ includeAll = false }: { includeAll?: boolean }) {
  const all = useShortcuts();
  const { loading, save, reset, clear } = all;
  // The recording hotkeys also live in the Recording settings; the Shortcuts
  // tab shows everything in one place.
  const shortcuts = includeAll ? all.shortcuts : all.shortcuts.filter((sc) => !PANEL_OWNED_ACTIONS.has(sc.action));

  return (
    <div className="frost flex w-full flex-col gap-3 p-4">
      <div className="flex items-center gap-2">
        <Keyboard className="size-4" />
        <span className="text-sm font-medium">Shortcuts</span>
      </div>

      {loading && shortcuts.length === 0 ? (
        <p className="text-xs text-muted-foreground">Loading…</p>
      ) : shortcuts.length === 0 ? (
        <p className="text-xs text-muted-foreground">No shortcuts configured.</p>
      ) : (
        <div className="flex flex-col gap-2">
          {shortcuts.map((sc) => (
            <ShortcutRow key={sc.action} sc={sc} onSave={save} onReset={reset} onClear={clear} />
          ))}
        </div>
      )}
    </div>
  );
}

// ShortcutRows — the editable rows for just the given actions, without the
// panel frame, for feature pages that own their hotkeys (the Auto Clicker page
// shows clickerToggle / clickerArm).
export function ShortcutRows({ actions }: { actions: readonly string[] }) {
  const { shortcuts, save, reset, clear } = useShortcuts();
  const rows = actions
    .map((a) => shortcuts.find((sc) => sc.action === a))
    .filter((sc): sc is NonNullable<typeof sc> => sc !== undefined);
  return (
    <div className="flex flex-col gap-2">
      {rows.map((sc) => (
        <ShortcutRow key={sc.action} sc={sc} onSave={save} onReset={reset} onClear={clear} />
      ))}
    </div>
  );
}
