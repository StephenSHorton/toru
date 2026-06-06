import { useState } from "react";
import { Button } from "@/components/ui/button";
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

// Chip — a small sharp-bordered token used to render the current combo.
function Chip({ children }: { children: React.ReactNode }) {
  return <span className="border border-border px-1.5 py-0.5 text-xs">{children}</span>;
}

// ComboChips renders a combo as ⊞ Win / Ctrl / Alt / Shift / KEY chips.
function ComboChips({ sc }: { sc: Shortcut }) {
  return (
    <div className="flex items-center gap-1">
      {sc.win && <Chip>⊞ Win</Chip>}
      {sc.ctrl && <Chip>Ctrl</Chip>}
      {sc.alt && <Chip>Alt</Chip>}
      {sc.shift && <Chip>Shift</Chip>}
      {sc.key && <Chip>{sc.key}</Chip>}
    </div>
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

  return (
    <div className="flex flex-col gap-2 border-t border-border pt-2 first:border-t-0 first:pt-0">
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm">{sc.label}</span>
        {!editing && (
          <div className="flex items-center gap-2">
            <ComboChips sc={sc} />
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
              onClick={() => toggleMod("win")}
            >
              ⊞ Win
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

          {err && <p className="text-xs text-destructive">{err}</p>}

          <div className="flex items-center gap-2">
            <Button size="sm" onClick={() => void save()} disabled={busy}>
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
