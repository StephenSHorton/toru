import { useState } from "react";
import { Button } from "@/components/ui/button";
import { Kbd, KbdGroup } from "@/components/ui/kbd";
import { Keyboard } from "lucide-react";
import { Shortcut } from "@/lib/api";
import { useShortcuts } from "./useShortcuts";

// Keys offered in the combo builder: A-Z then 0-9 (the v1 trigger-key set).
const KEY_OPTIONS: string[] = [
  ..."ABCDEFGHIJKLMNOPQRSTUVWXYZ".split(""),
  ..."0123456789".split(""),
];

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

// ComboKeys renders a combo as key-caps: ⊞ / Ctrl / Alt / Shift / KEY.
function ComboKeys({ sc }: { sc: Shortcut }) {
  return (
    <KbdGroup>
      {sc.win && <WinKey />}
      {sc.ctrl && <Kbd>Ctrl</Kbd>}
      {sc.alt && <Kbd>Alt</Kbd>}
      {sc.shift && <Kbd>Shift</Kbd>}
      {sc.key && <Kbd>{sc.key}</Kbd>}
    </KbdGroup>
  );
}

// ShortcutRow is one editable action (display mode + combo-builder edit mode).
function ShortcutRow({
  sc,
  onSave,
  onReset,
}: {
  sc: Shortcut;
  onSave: (action: string, draft: Shortcut) => Promise<string>;
  onReset: (action: string) => Promise<void>;
}) {
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState<Shortcut>(sc);
  const [err, setErr] = useState("");
  const [busy, setBusy] = useState(false);

  function beginEdit() {
    setDraft(Shortcut.createFrom({ ...sc }));
    setErr("");
    setEditing(true);
  }

  function cancel() {
    setDraft(Shortcut.createFrom({ ...sc }));
    setErr("");
    setEditing(false);
  }

  function toggleMod(mod: "win" | "ctrl" | "alt" | "shift") {
    setDraft((d) => Shortcut.createFrom({ ...d, [mod]: !d[mod] }));
  }

  function setKey(key: string) {
    setDraft((d) => Shortcut.createFrom({ ...d, key }));
  }

  async function save() {
    setBusy(true);
    const e = await onSave(sc.action, draft);
    setBusy(false);
    if (e) {
      setErr(e);
      return;
    }
    setErr("");
    setEditing(false);
  }

  async function reset() {
    setBusy(true);
    await onReset(sc.action);
    setBusy(false);
    setErr("");
    setEditing(false);
  }

  const shown = editing ? draft : sc;

  // Mirror the Go rule (validateShortcut: needs >=1 modifier) on the client for
  // instant feedback. The <select> always holds a valid A-Z/0-9 key, so the only
  // invalid state the builder can produce is zero modifiers. The Go side stays the
  // authoritative backstop; this just avoids a pointless round-trip + hints early.
  const noModifiers = !draft.win && !draft.ctrl && !draft.alt && !draft.shift;

  return (
    <div className="flex flex-col gap-2 border-t border-border pt-2 first:border-t-0 first:pt-0">
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm">{sc.label}</span>
        {!editing && (
          <div className="flex items-center gap-2">
            <ComboKeys sc={sc} />
            <Button size="sm" variant="ghost" onClick={beginEdit}>
              Edit
            </Button>
          </div>
        )}
      </div>

      {editing && (
        <div className="flex flex-col gap-2">
          <div className="flex flex-wrap items-center gap-1.5">
            <Button
              size="sm"
              variant={draft.win ? "default" : "outline"}
              title="Windows key"
              onClick={() => toggleMod("win")}
            >
              ⊞
            </Button>
            <Button
              size="sm"
              variant={draft.ctrl ? "default" : "outline"}
              onClick={() => toggleMod("ctrl")}
            >
              Ctrl
            </Button>
            <Button
              size="sm"
              variant={draft.alt ? "default" : "outline"}
              onClick={() => toggleMod("alt")}
            >
              Alt
            </Button>
            <Button
              size="sm"
              variant={draft.shift ? "default" : "outline"}
              onClick={() => toggleMod("shift")}
            >
              Shift
            </Button>
            <select
              className="frost border border-border bg-transparent px-2 py-1 text-sm"
              value={draft.key}
              onChange={(e) => setKey(e.target.value)}
            >
              {KEY_OPTIONS.map((k) => (
                <option key={k} value={k} className="bg-background text-foreground">
                  {k}
                </option>
              ))}
            </select>
          </div>

          {noModifiers && (
            <p className="text-xs text-muted-foreground">
              Pick at least one modifier (Win / Ctrl / Alt / Shift).
            </p>
          )}
          {err && <p className="text-xs text-destructive">{err}</p>}

          <div className="flex items-center gap-2">
            <Button size="sm" onClick={() => void save()} disabled={busy || noModifiers}>
              Save
            </Button>
            <Button size="sm" variant="ghost" onClick={cancel} disabled={busy}>
              Cancel
            </Button>
            <Button size="sm" variant="ghost" onClick={() => void reset()} disabled={busy}>
              Reset to default
            </Button>
          </div>
        </div>
      )}

      {isSnip(shown) && (
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
  const { shortcuts, loading, save, reset } = useShortcuts();

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
