import { useEffect, useRef, useState } from "react";
import { Bookmark, Copy, Download, Pencil, Save, Trash2, Upload } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ClickerPreset, ClickerService } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Card, CardHead, Note } from "./cardParts";
import { presetSummary, sameProfile } from "./cadence";
import { isActive, type CardProps, type Clicker } from "./useClicker";

// Presets — named snapshots of the clicking setup (Profile). Every change goes
// through ClickerService (names are checked in Go) and the page adopts the
// Settings it returns. While a run is going everything but Export is locked.

const INPUT_CLASS =
  "h-8 min-w-0 flex-1 border border-border bg-background px-2 text-sm placeholder:text-muted-foreground/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50";

export interface PresetsCardProps extends CardProps {
  apply: Clicker["apply"];
  run: Clicker["run"];
}

function updatedLabel(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? "" : `Updated ${d.toLocaleDateString()}`;
}

export default function PresetsCard({ settings, status, apply, run }: PresetsCardProps) {
  const locked = isActive(status);
  const [name, setName] = useState("");
  const [saved, setSaved] = useState("");
  const savedTimer = useRef<number | null>(null);
  const presets = settings.presets ?? [];

  useEffect(
    () => () => {
      if (savedTimer.current !== null) window.clearTimeout(savedTimer.current);
    },
    [],
  );

  async function saveNew() {
    const n = name.trim();
    if (!n || locked) return;
    if (await apply(() => ClickerService.SavePreset(n))) setName("");
  }

  async function exportPreset(id: string) {
    const path = await run(() => ClickerService.ExportPreset(id));
    if (!path) return; // cancelled or failed (the error shows on the page)
    setSaved(`Exported to ${path}`);
    if (savedTimer.current !== null) window.clearTimeout(savedTimer.current);
    savedTimer.current = window.setTimeout(() => setSaved(""), 6000);
  }

  return (
    <Card label="Presets" wide>
      <CardHead
        icon={Bookmark}
        title="Presets"
        caption={`${presets.length} ${presets.length === 1 ? "preset" : "presets"}. A preset saves the clicking, points and safety setup.`}
      >
        <Button size="sm" variant="secondary" disabled={locked} onClick={() => void apply(() => ClickerService.ImportPreset())}>
          <Upload /> Import…
        </Button>
      </CardHead>

      <div className="flex gap-2">
        <input
          aria-label="New preset name"
          value={name}
          maxLength={40}
          disabled={locked}
          placeholder="Name the current setup"
          autoComplete="off"
          spellCheck={false}
          onChange={(e) => setName(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.nativeEvent.isComposing) {
              e.preventDefault();
              void saveNew();
            } else if (e.key === "Escape") {
              setName("");
            }
          }}
          className={INPUT_CLASS}
        />
        <Button size="sm" disabled={locked || !name.trim()} onClick={() => void saveNew()}>
          <Save /> Save
        </Button>
      </div>

      {locked ? <Note>Presets are locked while clicking. Export still works.</Note> : null}
      {saved ? <p className="break-all font-mono text-[11px] text-muted-foreground">{saved}</p> : null}

      {presets.length === 0 ? (
        <Note>No presets yet.</Note>
      ) : (
        <div className="flex max-h-96 flex-col gap-2 overflow-auto pr-1">
          {presets.map((p) => (
            <PresetRow
              key={p.id}
              preset={p}
              active={settings.activePresetId === p.id}
              modified={settings.activePresetId === p.id && !sameProfile(settings.profile, p.profile)}
              locked={locked}
              apply={apply}
              onExport={() => void exportPreset(p.id)}
            />
          ))}
        </div>
      )}
    </Card>
  );
}

function PresetRow({
  preset,
  active,
  modified,
  locked,
  apply,
  onExport,
}: {
  preset: ClickerPreset;
  active: boolean;
  modified: boolean;
  locked: boolean;
  apply: Clicker["apply"];
  onExport: () => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [draft, setDraft] = useState(preset.name);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const rowRef = useRef<HTMLDivElement>(null);

  // A pending delete is cancelled by clicking anywhere outside this row.
  useEffect(() => {
    if (!confirmDelete) return;
    const onDown = (e: MouseEvent) => {
      if (rowRef.current && !rowRef.current.contains(e.target as Node)) setConfirmDelete(false);
    };
    document.addEventListener("mousedown", onDown);
    return () => document.removeEventListener("mousedown", onDown);
  }, [confirmDelete]);

  // Locking (a run started) abandons a half-done rename or delete.
  useEffect(() => {
    if (locked) {
      setRenaming(false);
      setConfirmDelete(false);
    }
  }, [locked]);

  async function commitRename() {
    const n = draft.trim();
    if (n === preset.name) {
      setRenaming(false);
      return;
    }
    if (await apply(() => ClickerService.RenamePreset(preset.id, n))) setRenaming(false);
  }

  return (
    <div
      ref={rowRef}
      className={cn(
        "flex flex-col gap-2 border bg-background/40 p-3",
        active ? "border-primary/50" : "border-border",
      )}
    >
      <div className="flex items-start justify-between gap-3">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          {renaming ? (
            <input
              autoFocus
              aria-label="Preset name"
              value={draft}
              maxLength={40}
              onChange={(e) => setDraft(e.target.value)}
              onBlur={() => void commitRename()}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  void commitRename();
                } else if (e.key === "Escape") {
                  e.preventDefault();
                  setDraft(preset.name);
                  setRenaming(false);
                }
              }}
              className={INPUT_CLASS}
            />
          ) : (
            <div className="flex min-w-0 items-center gap-2">
              <span className="truncate text-sm font-medium">{preset.name}</span>
              {active ? (
                <span
                  className={cn(
                    "shrink-0 border px-1.5 text-[10px] font-medium uppercase tracking-wide",
                    modified ? "border-amber-400/60 text-amber-300" : "border-primary/60 text-primary",
                  )}
                  title={modified ? "The current setup differs from this preset" : "The current setup matches this preset"}
                >
                  {modified ? "Modified" : "Active"}
                </span>
              ) : null}
            </div>
          )}
          <span className="truncate font-mono text-[11px] text-muted-foreground">{presetSummary(preset.profile)}</span>
          <span className="text-[11px] text-muted-foreground">{updatedLabel(preset.updatedAt)}</span>
        </div>
        <Button
          size="sm"
          variant={active && !modified ? "secondary" : "default"}
          disabled={locked}
          onClick={() => void apply(() => ClickerService.ApplyPreset(preset.id))}
        >
          Apply
        </Button>
      </div>

      <div className="flex flex-wrap items-center gap-1">
        <Button
          size="sm"
          variant="ghost"
          disabled={locked}
          title="Overwrite with the current setup"
          onClick={() => void apply(() => ClickerService.UpdatePreset(preset.id))}
        >
          <Save /> Update
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={locked || renaming}
          onClick={() => {
            setDraft(preset.name);
            setRenaming(true);
          }}
        >
          <Pencil /> Rename
        </Button>
        <Button
          size="sm"
          variant="ghost"
          disabled={locked}
          onClick={() => void apply(() => ClickerService.DuplicatePreset(preset.id))}
        >
          <Copy /> Duplicate
        </Button>
        <Button size="sm" variant="ghost" onClick={onExport}>
          <Download /> Export
        </Button>
        <div className="ml-auto flex items-center gap-1">
          {confirmDelete ? (
            <>
              <Button
                size="sm"
                variant="destructive"
                disabled={locked}
                onClick={() => {
                  setConfirmDelete(false);
                  void apply(() => ClickerService.DeletePreset(preset.id));
                }}
              >
                Confirm?
              </Button>
              <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(false)}>
                Cancel
              </Button>
            </>
          ) : (
            <Button size="sm" variant="ghost" disabled={locked} onClick={() => setConfirmDelete(true)}>
              <Trash2 /> Delete
            </Button>
          )}
        </div>
      </div>
    </div>
  );
}
