import { useCallback, useEffect, useState } from "react";
import {
  Camera,
  Check,
  FolderOpen,
  Image as ImageIcon,
  Settings2,
  SquareDashed,
  Trash2,
  Video,
  LayoutGrid,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Updater } from "@/updater/Updater";
import { Shortcuts } from "@/shortcuts/Shortcuts";
import {
  HistoryService,
  OverlayService,
  SettingsService,
  WindowsService,
} from "@/lib/api";
import type { CaptureItem } from "@/lib/api";
import { Events as WailsEvents } from "@wailsio/runtime";
import { cn } from "@/lib/utils";

// Dashboard / home window — tray-driven hub. Default view is a library of
// recent screenshots & recordings; Settings is a navigable panel (shortcuts,
// launch-at-login, freeze, updater).

type Page = "library" | "settings";

export default function Settings() {
  const [page, setPage] = useState<Page>("library");

  return (
    <div className="flex h-full w-full overflow-hidden">
      {/* Side nav — Library top, decorative mark in the leftover strip, Settings at bottom. */}
      <nav className="frost flex w-52 shrink-0 flex-col gap-1 overflow-hidden border-r border-border p-3">
        <div className="mb-3 shrink-0 px-2 pt-1">
          <div className="text-lg font-semibold tracking-tight">
            撮る <span className="text-muted-foreground text-sm font-normal">Toru</span>
          </div>
          <div className="text-[11px] text-muted-foreground">Screen capture</div>
        </div>

        <NavBtn
          active={page === "library"}
          icon={LayoutGrid}
          label="Library"
          onClick={() => setPage("library")}
        />

        <div
          className="flex min-h-0 flex-1 items-center justify-center overflow-hidden px-1 py-2"
          aria-hidden="true"
        >
          <img
            src="/appicon.png"
            alt=""
            className="pointer-events-none h-full w-full max-h-full select-none object-contain opacity-[0.22]"
            draggable={false}
          />
        </div>

        <div className="shrink-0 pt-1">
          <NavBtn
            active={page === "settings"}
            icon={Settings2}
            label="Settings"
            onClick={() => setPage("settings")}
          />
        </div>
      </nav>

      <main className="min-w-0 flex-1 overflow-auto p-6">
        {page === "library" ? <LibraryPage /> : <SettingsPage />}
      </main>
    </div>
  );
}

function NavBtn({
  active,
  icon: Icon,
  label,
  onClick,
}: {
  active: boolean;
  icon: React.ComponentType<{ className?: string }>;
  label: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      className={cn(
        "flex w-full items-center gap-2 px-2 py-2 text-sm transition-colors",
        active
          ? "bg-primary text-primary-foreground"
          : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
      )}
    >
      <Icon className="size-4 shrink-0" />
      {label}
    </button>
  );
}

// ---------------------------------------------------------------------------
// Library
// ---------------------------------------------------------------------------

function LibraryPage() {
  const [items, setItems] = useState<CaptureItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [selected, setSelected] = useState<Set<string>>(() => new Set());
  const [pendingDelete, setPendingDelete] = useState<CaptureItem[] | null>(null);
  const [deleting, setDeleting] = useState(false);

  const refresh = useCallback(() => {
    void HistoryService.List()
      .then((list) => setItems(Array.isArray(list) ? list : []))
      .catch(() => setItems([]))
      .finally(() => setLoading(false));
  }, []);

  useEffect(() => {
    refresh();
    const off = WailsEvents.On("history:changed", () => refresh());
    return () => {
      off();
    };
  }, [refresh]);

  // Drop selection ids that vanished after a refresh / bulk delete.
  useEffect(() => {
    setSelected((prev) => {
      if (prev.size === 0) return prev;
      const live = new Set(items.map((it) => it.id));
      let changed = false;
      const next = new Set<string>();
      for (const id of prev) {
        if (live.has(id)) next.add(id);
        else changed = true;
      }
      return changed ? next : prev;
    });
  }, [items]);

  const selectedItems = items.filter((it) => selected.has(it.id));
  const allSelected = items.length > 0 && selected.size === items.length;

  const toggleSelect = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const selectAll = () => setSelected(new Set(items.map((it) => it.id)));
  const clearSelection = () => setSelected(new Set());

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (pendingDelete) {
        if (!deleting) setPendingDelete(null);
        return;
      }
      if (selected.size > 0) clearSelection();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [pendingDelete, deleting, selected.size]);

  const confirmDelete = async () => {
    if (!pendingDelete || pendingDelete.length === 0 || deleting) return;
    setDeleting(true);
    const failed: string[] = [];
    try {
      // HistoryService has no bulk API; loop Delete so each file + index row is
      // removed, then refresh once. Continue on individual failures.
      for (const it of pendingDelete) {
        try {
          await HistoryService.Delete(it.id);
        } catch {
          failed.push(it.id);
        }
      }
      setPendingDelete(null);
      if (failed.length === 0) clearSelection();
      else setSelected(new Set(failed));
      refresh();
    } finally {
      setDeleting(false);
    }
  };

  return (
    <div className="relative flex h-full flex-col gap-4">
      <div className="flex items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Library</h1>
          <p className="text-sm text-muted-foreground">
            Screenshots save here when you hit Done; recordings save when they finish
          </p>
        </div>
        <div className="flex gap-2">
          {items.length > 0 ? (
            <Button
              size="sm"
              variant="ghost"
              onClick={allSelected ? clearSelection : selectAll}
            >
              {allSelected ? "Clear selection" : "Select all"}
            </Button>
          ) : null}
          <Button
            size="sm"
            variant="ghost"
            onClick={() => void HistoryService.OpenFolder()}
          >
            <FolderOpen /> Folder
          </Button>
          <Button size="sm" onClick={() => void WindowsService.OpenOverlay()}>
            <Camera /> New capture
          </Button>
        </div>
      </div>

      {selectedItems.length > 0 ? (
        <div className="frost flex items-center justify-between gap-3 px-3 py-2">
          <p className="text-sm">
            <span className="font-medium">{selectedItems.length} selected</span>
            <span className="text-muted-foreground">
              {" "}
              · {describeKinds(selectedItems)}
            </span>
          </p>
          <div className="flex gap-2">
            <Button size="sm" variant="ghost" onClick={clearSelection}>
              Clear
            </Button>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => setPendingDelete(selectedItems)}
            >
              <Trash2 /> Delete
            </Button>
          </div>
        </div>
      ) : null}

      {loading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : items.length === 0 ? (
        <EmptyLibrary />
      ) : (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-4">
          {items.map((it) => (
            <CaptureCard
              key={it.id}
              item={it}
              selected={selected.has(it.id)}
              onToggleSelect={() => toggleSelect(it.id)}
              onOpen={() => void HistoryService.Open(it.id)}
              onDelete={() => setPendingDelete([it])}
            />
          ))}
        </div>
      )}

      {pendingDelete ? (
        <DeleteConfirmDialog
          items={pendingDelete}
          busy={deleting}
          onCancel={() => setPendingDelete(null)}
          onConfirm={() => void confirmDelete()}
        />
      ) : null}
    </div>
  );
}

function EmptyLibrary() {
  return (
    <div className="frost flex flex-1 flex-col items-center justify-center gap-3 p-10 text-center">
      <ImageIcon className="size-10 text-muted-foreground" />
      <div>
        <p className="font-medium">No captures yet</p>
        <p className="mt-1 text-sm text-muted-foreground">
          Press <kbd className="border border-border px-1.5 py-0.5 text-xs">⊞ Shift S</kbd>{" "}
          or use Capture to take a screenshot or recording.
        </p>
      </div>
      <Button onClick={() => void WindowsService.OpenOverlay()}>
        <SquareDashed /> Capture now
      </Button>
    </div>
  );
}

function CaptureCard({
  item,
  selected,
  onToggleSelect,
  onOpen,
  onDelete,
}: {
  item: CaptureItem;
  selected: boolean;
  onToggleSelect: () => void;
  onOpen: () => void;
  onDelete: () => void;
}) {
  const isVideo = item.kind === "video";
  const src = servedUrl(item.path);
  const when = formatWhen(item.takenAt);

  return (
    <div
      className={cn(
        "frost group flex flex-col overflow-hidden border",
        "transition-[border-color,box-shadow,transform] duration-150",
        selected
          ? "border-primary/80 shadow-[0_0_0_1px_var(--color-ring),0_12px_28px_oklch(0_0_0/0.45)]"
          : "border-transparent hover:border-primary/70 hover:shadow-[0_0_0_1px_var(--color-ring),0_12px_28px_oklch(0_0_0/0.45)] hover:-translate-y-0.5",
      )}
    >
      <div className="relative aspect-video w-full overflow-hidden bg-black/40">
        <button
          type="button"
          role="checkbox"
          aria-checked={selected}
          aria-label={selected ? `Deselect ${item.label}` : `Select ${item.label}`}
          onClick={(e) => {
            e.stopPropagation();
            onToggleSelect();
          }}
          className={cn(
            "absolute right-1.5 top-1.5 z-10 flex size-5 items-center justify-center border transition-opacity",
            selected
              ? "border-primary bg-primary text-primary-foreground opacity-100"
              : "border-white/55 bg-black/45 text-white opacity-0 group-hover:opacity-100 group-focus-within:opacity-100",
          )}
        >
          {selected ? <Check className="size-3" strokeWidth={3} /> : null}
        </button>
        <button
          type="button"
          onClick={onOpen}
          className="absolute inset-0 text-left"
          title="Open"
        >
          {isVideo ? (
            <video
              src={src}
              className="h-full w-full object-contain transition-transform duration-150 group-hover:scale-[1.03]"
              muted
              preload="metadata"
            />
          ) : (
            <img
              src={src}
              alt=""
              className="h-full w-full object-contain transition-transform duration-150 group-hover:scale-[1.03]"
              loading="lazy"
            />
          )}
          <span className="pointer-events-none absolute inset-0 bg-primary/0 transition-colors duration-150 group-hover:bg-primary/10" />
          <span className="absolute left-1.5 top-1.5 flex items-center gap-1 bg-black/60 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-white">
            {isVideo ? <Video className="size-3" /> : <ImageIcon className="size-3" />}
            {isVideo ? "Video" : "Shot"}
          </span>
          <span className="pointer-events-none absolute inset-x-0 bottom-0 translate-y-full bg-black/55 px-2 py-1 text-center text-[11px] text-white transition-transform duration-150 group-hover:translate-y-0">
            Click to open
          </span>
        </button>
      </div>
      <div className="flex items-center gap-1 px-2 py-1.5">
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-medium">{item.label}</div>
          <div className="truncate text-[10px] text-muted-foreground">{when}</div>
        </div>
        <Button
          size="icon"
          variant="ghost"
          className="size-7 shrink-0 opacity-60 hover:opacity-100"
          title="Delete"
          onClick={(e) => {
            e.stopPropagation();
            onDelete();
          }}
        >
          <Trash2 className="size-3.5" />
        </Button>
      </div>
    </div>
  );
}

function describeKinds(items: CaptureItem[]): string {
  let shots = 0;
  let recs = 0;
  for (const it of items) {
    if (it.kind === "video") recs += 1;
    else shots += 1;
  }
  const parts: string[] = [];
  if (shots > 0) parts.push(`${shots} screenshot${shots === 1 ? "" : "s"}`);
  if (recs > 0) parts.push(`${recs} recording${recs === 1 ? "" : "s"}`);
  return parts.join(" and ");
}

function DeleteConfirmDialog({
  items,
  busy,
  onCancel,
  onConfirm,
}: {
  items: CaptureItem[];
  busy: boolean;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  const bulk = items.length > 1;
  const title = bulk
    ? `Delete ${items.length} items?`
    : `Delete this ${items[0]?.kind === "video" ? "recording" : "screenshot"}?`;
  const body = bulk ? (
    <>
      <span className="font-medium text-foreground">{describeKinds(items)}</span>{" "}
      will be removed from your library and deleted from disk. This can’t be undone.
    </>
  ) : (
    <>
      <span className="font-medium text-foreground">{items[0]?.label}</span> will
      be removed from your library and deleted from disk. This can’t be undone.
    </>
  );
  return (
    <div
      className="absolute inset-0 z-50 flex items-center justify-center bg-black/55 p-4"
      role="dialog"
      aria-modal="true"
      aria-labelledby="delete-title"
      onClick={busy ? undefined : onCancel}
    >
      <div
        className="frost w-full max-w-sm p-4 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <h2 id="delete-title" className="text-base font-semibold">
          {title}
        </h2>
        <p className="mt-1 text-sm text-muted-foreground">{body}</p>
        <div className="mt-4 flex justify-end gap-2">
          <Button size="sm" variant="ghost" disabled={busy} onClick={onCancel}>
            Cancel
          </Button>
          <Button
            size="sm"
            variant="destructive"
            disabled={busy}
            onClick={onConfirm}
          >
            <Trash2 /> {busy ? "Deleting…" : "Delete"}
          </Button>
        </div>
      </div>
    </div>
  );
}

function servedUrl(absPath: string): string {
  const base = absPath.replace(/\\/g, "/").split("/").pop() ?? "";
  return "/__file/" + encodeURIComponent(base);
}

function formatWhen(iso: string | Date): string {
  try {
    const d = typeof iso === "string" ? new Date(iso) : iso;
    if (Number.isNaN(d.getTime())) return "";
    return d.toLocaleString(undefined, {
      month: "short",
      day: "numeric",
      hour: "numeric",
      minute: "2-digit",
    });
  } catch {
    return "";
  }
}

// ---------------------------------------------------------------------------
// Settings panel
// ---------------------------------------------------------------------------

function SettingsPage() {
  const [launchAtLogin, setLaunchAtLogin] = useState(false);
  const [launchBusy, setLaunchBusy] = useState(false);
  const [freezeOnCapture, setFreezeOnCapture] = useState(true);
  const [freezeBusy, setFreezeBusy] = useState(false);
  const [openEditor, setOpenEditor] = useState(true);
  const [openEditorBusy, setOpenEditorBusy] = useState(false);
  const [copyOnDone, setCopyOnDone] = useState(true);
  const [copyOnDoneBusy, setCopyOnDoneBusy] = useState(false);
  const [libraryDir, setLibraryDir] = useState("");
  const [libraryDefault, setLibraryDefault] = useState(true);
  const [libraryBusy, setLibraryBusy] = useState(false);

  const refreshLibraryPath = useCallback(() => {
    void HistoryService.GetDir()
      .then((d) => setLibraryDir(typeof d === "string" ? d : ""))
      .catch(() => setLibraryDir(""));
    void HistoryService.IsDefaultDir()
      .then((v) => setLibraryDefault(!!v))
      .catch(() => setLibraryDefault(true));
  }, []);

  useEffect(() => {
    void SettingsService.GetLaunchAtLogin()
      .then(setLaunchAtLogin)
      .catch(() => {});
    void OverlayService.GetFreezeOnCapture()
      .then(setFreezeOnCapture)
      .catch(() => {});
    void OverlayService.GetOpenEditorAfterCapture()
      .then(setOpenEditor)
      .catch(() => {});
    void OverlayService.GetCopyOnDone()
      .then(setCopyOnDone)
      .catch(() => {});
    refreshLibraryPath();
  }, [refreshLibraryPath]);

  const toggleFreezeOnCapture = async (next: boolean) => {
    const prev = freezeOnCapture;
    setFreezeBusy(true);
    setFreezeOnCapture(next);
    try {
      await OverlayService.SetFreezeOnCapture(next);
    } catch {
      setFreezeOnCapture(prev);
    } finally {
      setFreezeBusy(false);
    }
  };

  const toggleOpenEditor = async (next: boolean) => {
    const prev = openEditor;
    setOpenEditorBusy(true);
    setOpenEditor(next);
    try {
      await OverlayService.SetOpenEditorAfterCapture(next);
    } catch {
      setOpenEditor(prev);
    } finally {
      setOpenEditorBusy(false);
    }
  };

  const toggleCopyOnDone = async (next: boolean) => {
    const prev = copyOnDone;
    setCopyOnDoneBusy(true);
    setCopyOnDone(next);
    try {
      await OverlayService.SetCopyOnDone(next);
    } catch {
      setCopyOnDone(prev);
    } finally {
      setCopyOnDoneBusy(false);
    }
  };

  const toggleLaunchAtLogin = async (next: boolean) => {
    const prev = launchAtLogin;
    setLaunchBusy(true);
    setLaunchAtLogin(next);
    try {
      await SettingsService.SetLaunchAtLogin(next);
    } catch {
      setLaunchAtLogin(prev);
    } finally {
      setLaunchBusy(false);
    }
  };

  const pickLibraryDir = async () => {
    if (libraryBusy) return;
    setLibraryBusy(true);
    try {
      const chosen = await HistoryService.PickDir();
      if (chosen) {
        setLibraryDir(chosen);
        setLibraryDefault(false);
      } else {
        refreshLibraryPath();
      }
    } catch {
      refreshLibraryPath();
    } finally {
      setLibraryBusy(false);
    }
  };

  const resetLibraryDir = async () => {
    if (libraryBusy) return;
    setLibraryBusy(true);
    try {
      await HistoryService.ResetDir();
      refreshLibraryPath();
    } catch {
      refreshLibraryPath();
    } finally {
      setLibraryBusy(false);
    }
  };

  return (
    <div className="mx-auto flex max-w-lg flex-col gap-6">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Settings</h1>
        <p className="text-sm text-muted-foreground">
          Preferences, shortcuts, and updates
        </p>
      </div>

      <div className="frost flex flex-col gap-4 p-5">
        <div className="flex items-center justify-between gap-3">
          <div className="flex flex-col">
            <span className="text-sm font-medium">Start with Windows</span>
            <span className="text-xs text-muted-foreground">
              Launch at sign-in, minimized to the tray
            </span>
          </div>
          <Switch
            checked={launchAtLogin}
            disabled={launchBusy}
            onCheckedChange={(v) => void toggleLaunchAtLogin(v)}
            aria-label="Start Toru with Windows"
          />
        </div>

        <div className="flex items-center justify-between gap-3">
          <div className="flex flex-col">
            <span className="text-sm font-medium">Freeze screen while capturing</span>
            <span className="text-xs text-muted-foreground">
              Off: the overlay stays live so you see motion while selecting
            </span>
          </div>
          <Switch
            checked={freezeOnCapture}
            disabled={freezeBusy}
            onCheckedChange={(v) => void toggleFreezeOnCapture(v)}
            aria-label="Freeze the screen while capturing"
          />
        </div>

        <div className="flex items-center justify-between gap-3">
          <div className="flex flex-col">
            <span className="text-sm font-medium">Open editor after screenshot</span>
            <span className="text-xs text-muted-foreground">
              Off: copy to clipboard and save to the library, skip annotation
            </span>
          </div>
          <Switch
            checked={openEditor}
            disabled={openEditorBusy}
            onCheckedChange={(v) => void toggleOpenEditor(v)}
            aria-label="Open the screenshot editor after capture"
          />
        </div>

        <div className="flex items-center justify-between gap-3">
          <div className="flex flex-col">
            <span className="text-sm font-medium">Copy to clipboard when finishing</span>
            <span className="text-xs text-muted-foreground">
              Off: Done or Esc saves to the library without copying
            </span>
          </div>
          <Switch
            checked={copyOnDone}
            disabled={copyOnDoneBusy}
            onCheckedChange={(v) => void toggleCopyOnDone(v)}
            aria-label="Copy to clipboard when finishing with Done or Esc"
          />
        </div>

        <div className="flex flex-col gap-2 border-t border-border pt-4">
          <div className="flex flex-col">
            <span className="text-sm font-medium">Library folder</span>
            <span className="text-xs text-muted-foreground">
              Screenshots and recordings are saved here (and loaded into the Library)
            </span>
          </div>
          <div
            className="truncate rounded-none border border-border bg-background/40 px-2 py-1.5 font-mono text-[11px] text-muted-foreground"
            title={libraryDir || "—"}
          >
            {libraryDir || "—"}
          </div>
          <div className="flex flex-wrap gap-2">
            <Button
              size="sm"
              variant="secondary"
              disabled={libraryBusy}
              onClick={() => void pickLibraryDir()}
            >
              <FolderOpen /> {libraryBusy ? "…" : "Change…"}
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={libraryBusy || libraryDefault}
              onClick={() => void resetLibraryDir()}
            >
              Reset default
            </Button>
            <Button
              size="sm"
              variant="ghost"
              disabled={!libraryDir}
              onClick={() => void HistoryService.OpenFolder()}
            >
              Open folder
            </Button>
          </div>
        </div>
      </div>

      <Shortcuts />
      <Updater />
    </div>
  );
}
