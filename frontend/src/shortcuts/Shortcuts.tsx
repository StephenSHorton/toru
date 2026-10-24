import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Kbd, KbdGroup } from "@/components/ui/kbd";
import { Keyboard } from "lucide-react";
import { HotkeyService, Shortcut } from "@/lib/api";
import { cn } from "@/lib/utils";
import { useShortcuts } from "./useShortcuts";

// Actions that render their shortcut row inside their own Settings panel instead
// of this generic list (the replay hotkey lives in the Instant replay panel).
const PANEL_OWNED_ACTIONS = new Set(["replaySave", "recordToggle", "timelineMarker"]);

// isSnip reports whether a combo is Win+Shift+S (the Snipping Tool combo we
// override). Shown as a small note so the behavior is never a surprise.
function isSnip(sc: { win: boolean; shift: boolean; ctrl: boolean; alt: boolean; key: string }): boolean {
  return sc.win && sc.shift && !sc.ctrl && !sc.alt && sc.key.toUpperCase() === "S";
}

// The Windows-logo key has no reliable single character in system fonts, so ⊞
// (U+229E) is the conventional stand-in (Microsoft's own docs use it). The title
// gives a hover label since the glyph isn't self-evident.
function WinKey() {
  return <Kbd title="Windows key">⊞</Kbd>;
}

// ComboKeys renders a combo as key-caps: ⊞ + Ctrl + Alt + Shift + KEY.
function ComboKeys({ sc }: { sc: Shortcut }) {
  const keys: React.ReactNode[] = [];
  if (sc.win) keys.push(<WinKey key="win" />);
  if (sc.ctrl) keys.push(<Kbd key="ctrl">Ctrl</Kbd>);
  if (sc.alt) keys.push(<Kbd key="alt">Alt</Kbd>);
  if (sc.shift) keys.push(<Kbd key="shift">Shift</Kbd>);
  if (sc.key) keys.push(<Kbd key="key">{sc.key}</Kbd>);
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

// ShortcutRow is one rebindable action. "Change" (or clicking the keys) starts
// a recording: the user presses the new combo and it's saved straight away.
// Recording happens in Go's keyboard hook (HotkeyService.RecordShortcut), so
// Win+… combos, PrintScreen and Toru's own hotkeys are captured instead of
// firing, and Esc cancels.
export function ShortcutRow({
  sc,
  onSave,
  onReset,
}: {
  sc: Shortcut;
  onSave: (action: string, draft: Shortcut) => Promise<string>;
  onReset: (action: string) => Promise<void>;
}) {
  const [recording, setRecording] = useState(false);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);
  const recordingRef = useRef(false);

  // Don't leave the hook swallowing keys if the panel goes away mid-recording.
  useEffect(
    () => () => {
      if (recordingRef.current) void HotkeyService.CancelRecording();
    },
    [],
  );

  async function record() {
    setErr("");
    setRecording(true);
    recordingRef.current = true;
    try {
      const got = await HotkeyService.RecordShortcut();
      if (!got.key) return; // cancelled (Esc / Cancel / timed out)
      setBusy(true);
      const e = await onSave(sc.action, Shortcut.createFrom({ ...sc, ...got, action: sc.action, label: sc.label }));
      if (e) setErr(e.replace(/^Error:\s*/, ""));
    } catch (e) {
      setErr(String(e).replace(/^Error:\s*/, ""));
    } finally {
      recordingRef.current = false;
      setRecording(false);
      setBusy(false);
    }
  }

  async function reset() {
    setBusy(true);
    await onReset(sc.action);
    setBusy(false);
    setErr("");
  }

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
            </>
          )}
        </div>
      </div>

      {recording && (
        <p className="text-xs text-muted-foreground">
          Hold any of Ctrl, Alt, Shift or Win and press a key, or press a single key like F9,
          PrintScreen or a numpad key. Esc cancels. Your shortcuts are paused until you're done.
        </p>
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
export function Shortcuts() {
  const all = useShortcuts();
  const { loading, save, reset } = all;
  const shortcuts = all.shortcuts.filter((sc) => !PANEL_OWNED_ACTIONS.has(sc.action));

  return (
    <div className="frost flex w-full flex-col gap-3 p-4" style={{ maxWidth: 420 }}>
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
            <ShortcutRow key={sc.action} sc={sc} onSave={save} onReset={reset} />
          ))}
        </div>
      )}
    </div>
  );
}
