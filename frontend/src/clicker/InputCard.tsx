import { useRef, useState } from "react";
import { ArrowDown, ArrowUp, Keyboard, Mouse, MousePointer2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/segmented";
import { ClickerService } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Card, CardHead, Note } from "./cardParts";
import { BUTTON_LABEL, InputKind, MouseButton, isLetterKey, keyLabel } from "./cadence";
import type { CardProps } from "./useClicker";

// Input — what a run presses: a mouse button, or a key. The key field captures
// the next key pressed while it has focus (a DOM keydown, mapped to the hotkey
// package's KeyName spellings), and focusing it stops a running clicker so the
// clicker's own presses can't be captured.

const INPUTS = [
  { value: InputKind.InputMouse, label: "Mouse", icon: Mouse },
  { value: InputKind.InputKeyboard, label: "Keyboard", icon: Keyboard },
] as const;

const BUTTONS = [
  { value: MouseButton.ButtonLeft, label: "Left" },
  { value: MouseButton.ButtonMiddle, label: "Middle" },
  { value: MouseButton.ButtonRight, label: "Right" },
  { value: MouseButton.ButtonX1, label: "X1", title: BUTTON_LABEL[MouseButton.ButtonX1] },
  { value: MouseButton.ButtonX2, label: "X2", title: BUTTON_LABEL[MouseButton.ButtonX2] },
] as const;

// KeyboardEvent.code -> KeyName for keys that aren't letters, digits or F-keys.
// Physical codes keep the mapping independent of the keyboard layout.
const CODE_NAMES: Record<string, string> = {
  Space: "Space",
  PageUp: "PageUp",
  PageDown: "PageDown",
  End: "End",
  Home: "Home",
  ArrowLeft: "Left",
  ArrowUp: "Up",
  ArrowRight: "Right",
  ArrowDown: "Down",
  PrintScreen: "PrintScreen",
  Insert: "Insert",
  Delete: "Delete",
  Pause: "Pause",
  ScrollLock: "ScrollLock",
  NumpadMultiply: "Num*",
  NumpadAdd: "Num+",
  NumpadSubtract: "Num-",
  NumpadDecimal: "Num.",
  NumpadDivide: "Num/",
  Semicolon: ";",
  Equal: "=",
  Comma: ",",
  Minus: "-",
  Period: ".",
  Slash: "/",
  Backquote: "`",
  BracketLeft: "[",
  Backslash: "\\",
  BracketRight: "]",
  Quote: "'",
  ControlLeft: "LCtrl",
  ControlRight: "RCtrl",
  ShiftLeft: "LShift",
  ShiftRight: "RShift",
  AltLeft: "LAlt",
  AltRight: "RAlt",
};

const MODIFIER_CODES = new Set(["ControlLeft", "ControlRight", "ShiftLeft", "ShiftRight", "AltLeft", "AltRight"]);

/** keyNameForCode maps a DOM KeyboardEvent.code to a KeyName, or "" if unsupported. */
function keyNameForCode(code: string): string {
  let m = /^Key([A-Z])$/.exec(code);
  if (m) return m[1];
  m = /^Digit([0-9])$/.exec(code);
  if (m) return m[1];
  m = /^F([1-9]|1[0-9]|2[0-4])$/.exec(code);
  if (m) return code;
  m = /^Numpad([0-9])$/.exec(code);
  if (m) return `Num${m[1]}`;
  return CODE_NAMES[code] ?? "";
}

// DOM MouseEvent.button -> the clicker button a click on the key field selects.
const MOUSE_BUTTONS: Record<number, MouseButton> = {
  1: MouseButton.ButtonMiddle,
  2: MouseButton.ButtonRight,
  3: MouseButton.ButtonX1,
  4: MouseButton.ButtonX2,
};

export default function InputCard({ settings, update }: CardProps) {
  const p = settings.profile;
  const keyboard = p.input === InputKind.InputKeyboard;
  const [capturing, setCapturing] = useState(false);
  const [hint, setHint] = useState("");
  // A modifier pressed alone is committed on its release, unless another key
  // came down meanwhile.
  const soloModifier = useRef("");

  function setProfile(patch: Partial<typeof p>) {
    update((s) => ({ ...s, profile: { ...s.profile, ...patch } }));
  }

  function commitKey(name: string, field: HTMLElement) {
    setProfile({ key: name });
    setHint("");
    field.blur();
  }

  function onKeyDown(e: React.KeyboardEvent<HTMLButtonElement>) {
    if (!capturing) return;
    e.preventDefault();
    e.stopPropagation();
    if (e.repeat) return;
    if (MODIFIER_CODES.has(e.code)) {
      soloModifier.current = e.code;
      return;
    }
    soloModifier.current = "";
    if (e.code === "Escape") {
      e.currentTarget.blur();
      return;
    }
    const name = keyNameForCode(e.code);
    if (!name) {
      setHint(`${e.key || e.code} can't be auto-pressed. Try a letter, digit, F-key, numpad or navigation key.`);
      return;
    }
    commitKey(name, e.currentTarget);
  }

  function onKeyUp(e: React.KeyboardEvent<HTMLButtonElement>) {
    if (!capturing) return;
    e.preventDefault();
    if (soloModifier.current && soloModifier.current === e.code) {
      const name = CODE_NAMES[e.code];
      soloModifier.current = "";
      if (name) commitKey(name, e.currentTarget);
    }
  }

  // A non-left mouse press on the field switches to clicking that button.
  function onMouseDown(e: React.MouseEvent<HTMLButtonElement>) {
    if (!capturing) return;
    const btn = MOUSE_BUTTONS[e.button];
    if (!btn) return;
    e.preventDefault();
    setProfile({ input: InputKind.InputMouse, button: btn });
    e.currentTarget.blur();
  }

  const shown = keyLabel(p.key, p.uppercase);
  const letter = isLetterKey(p.key);

  return (
    <Card label="Input">
      <CardHead icon={MousePointer2} title="Input" caption="The mouse button or key each click presses.">
        <Segmented
          aria-label="Input"
          value={keyboard ? InputKind.InputKeyboard : InputKind.InputMouse}
          options={INPUTS}
          onChange={(input) => setProfile({ input })}
        />
      </CardHead>

      {keyboard ? (
        <div className="flex flex-col gap-2">
          <div className="flex items-center gap-2">
            <button
              type="button"
              aria-label="Key to press"
              onFocus={() => {
                setCapturing(true);
                setHint("");
                soloModifier.current = "";
                // The clicker must not be pressing keys into this field.
                void ClickerService.Stop().catch(() => {});
              }}
              onBlur={() => setCapturing(false)}
              onKeyDown={onKeyDown}
              onKeyUp={onKeyUp}
              onMouseDown={onMouseDown}
              // Keep the side buttons from navigating the hub back/forward.
              onMouseUp={(e) => (e.button === 3 || e.button === 4) && e.preventDefault()}
              onContextMenu={(e) => e.preventDefault()}
              className={cn(
                "flex h-8 min-w-0 flex-1 items-center border bg-background px-2 text-left font-mono text-sm focus-visible:outline-none",
                capturing ? "animate-pulse border-primary bg-primary/10 text-primary" : "border-border",
              )}
            >
              {capturing ? "Press a key…" : shown || <span className="text-muted-foreground">Click to choose a key</span>}
            </button>
            <Button
              size="sm"
              variant="secondary"
              disabled={!letter}
              title={letter ? "Switch between lower and upper case" : "Only letters have a case"}
              aria-label={p.uppercase ? "Uppercase (switch to lowercase)" : "Lowercase (switch to uppercase)"}
              onClick={() => setProfile({ uppercase: !p.uppercase })}
            >
              {p.uppercase ? <ArrowUp /> : <ArrowDown />}
              {p.uppercase ? "Upper" : "Lower"}
            </Button>
          </div>
          {hint ? (
            <Note warn>{hint}</Note>
          ) : capturing ? (
            <Note>Esc cancels. Clicking the field with the right, middle or side button picks that mouse button instead.</Note>
          ) : !p.key ? (
            <Note warn>Choose a key before starting.</Note>
          ) : null}
        </div>
      ) : (
        <Segmented
          aria-label="Mouse button"
          value={p.button || MouseButton.ButtonLeft}
          options={BUTTONS}
          onChange={(button) => setProfile({ button })}
          className="self-start"
        />
      )}
    </Card>
  );
}
