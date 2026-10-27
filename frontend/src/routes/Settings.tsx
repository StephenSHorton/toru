import { useCallback, useEffect, useState } from "react";
import {
  Camera,
  Check,
  Circle,
  Clapperboard,
  Film,
  FolderOpen,
  Image as ImageIcon,
  Send,
  Settings2,
  SquareDashed,
  Trash2,
  Video,
  LayoutGrid,
  MousePointerClick,
  Radio,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { Updater } from "@/updater/Updater";
import { UpdateBanner } from "@/updater/UpdateBanner";
import { Shortcuts } from "@/shortcuts/Shortcuts";
import { ReplayPanel } from "@/replay/ReplayPanel";
import { NotificationsPanel } from "@/notifications/NotificationsPanel";
import SendFiles from "@/transfer/SendFiles";
import ClickerPage from "@/clicker/ClickerPage";
import StreamPage from "@/stream/StreamPage";
import { errText, reportLocalSendError } from "@/transfer/useTransfer";
import {
  TransferService,
  HistoryService,
  OverlayService,
  ReplayService,
  SettingsService,
  WindowsService,
  LibraryRetention,
} from "@/lib/api";
import type { CaptureItem, RecordingSession, ReplayClip } from "@/lib/api";
import { Events as WailsEvents } from "@wailsio/runtime";
import { cn } from "@/lib/utils";

// Dashboard / home window — tray-driven hub. Default view is a library of
// recent screenshots & recordings; Settings is a navigable panel (shortcuts,
// launch-at-login, freeze, updater).

type Page = "library" | "send" | "stream" | "clicker" | "settings";
const PAGES: readonly Page[] = ["library", "send", "stream", "clicker", "settings"];

// initialPage honours `?page=` so the tray's "Send files…" can open the hub
// straight onto Send Files (windows.go OpenSendFiles); anything else lands on Library.
function initialPage(): Page {
  const p = new URLSearchParams(window.location.search).get("page") ?? "";
  return (PAGES as readonly string[]).includes(p) ? (p as Page) : "library";
}

export default function Settings() {
  const [page, setPage] = useState<Page>(initialPage);

  // Go raises an already-open hub and asks it to switch pages via transfer:open
  // (no payload) instead of reloading the window, which would drop the live
  // transfer UI.
  useEffect(() => {
    const off = WailsEvents.On("transfer:open", () => setPage("send"));
    const offClicker = WailsEvents.On("clicker:open", () => setPage("clicker"));
    return () => {
      off();
      offClicker();
    };
  }, []);

  // Library card → Send Files: kick off the send, then show the page. Transfer
  // errors arrive as the "error" phase on transfer:send and render on the Send Files
  // page itself; a synchronous rejection (the capture file is gone) is routed
  // to the Send card the same way, since by then this page has switched.
  const sendItem = useCallback((item: CaptureItem) => {
    void TransferService.StartSend(item.path).catch((e: unknown) => reportLocalSendError(errText(e)));
    setPage("send");
  }, []);

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
        <NavBtn
          active={page === "send"}
          icon={Send}
          label="Send Files"
          onClick={() => setPage("send")}
        />
        <NavBtn
          active={page === "stream"}
          icon={Radio}
          label="Stream"
          onClick={() => setPage("stream")}
        />
        <NavBtn
          active={page === "clicker"}
          icon={MousePointerClick}
          label="Auto Clicker"
          onClick={() => setPage("clicker")}
        />

        <div
          className="flex min-h-0 flex-1 items-center justify-center overflow-hidden"
          aria-hidden="true"
        >
          {/* Appicon has generous padding; scale past the strip so the rooster
              fills the leftover sidebar space, then clip. Short heights clip
              further instead of shoving Settings off-screen. */}
          <img
            src="/appicon.png"
            alt=""
            className="pointer-events-none w-[210%] max-w-none shrink-0 select-none object-contain opacity-[0.2]"
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
        {/* Update announcement (ready / downloading / restarting) on every page. */}
        <UpdateBanner />
        {page === "library" ? (
          <LibraryPage onSendItem={sendItem} />
        ) : page === "send" ? (
          <SendFiles />
        ) : page === "stream" ? (
          <StreamPage />
        ) : page === "clicker" ? (
          <ClickerPage />
        ) : (
          <SettingsPage />
        )}
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

type LibraryView = "all" | "shots" | "recordings" | "clips" | "sessions";

const LIBRARY_VIEW_KEY = "toru.library.view";

function readLibraryView(): LibraryView {
  try {
    const v = window.localStorage.getItem(LIBRARY_VIEW_KEY);
    if (v === "all" || v === "shots" || v === "recordings" || v === "clips" || v === "sessions") return v;
  } catch {
    // storage unavailable: default view
  }
  return "all";
}

function LibraryPage({ onSendItem }: { onSendItem: (item: CaptureItem) => void }) {
  const [items, setItems] = useState<CaptureItem[]>([]);
  const [loading, setLoading] = useState(true);
  const [view, setViewState] = useState<LibraryView>(readLibraryView);
  const [clips, setClips] = useState<ReplayClip[]>([]);
  const [game, setGame] = useState("");
  const [pendingClip, setPendingClip] = useState<ReplayClip | null>(null);
  const [sessions, setSessions] = useState<RecordingSession[]>([]);
  const [pendingSession, setPendingSession] = useState<RecordingSession | null>(null);
  // Errors from opening/acting on clips and recordings (never fail silently).
  const [libNote, setLibNote] = useState("");
  useEffect(() => {
    if (!libNote) return;
    const t = window.setTimeout(() => setLibNote(""), 6000);
    return () => window.clearTimeout(t);
  }, [libNote]);
  const report = (e: unknown) => setLibNote(String(e).replace(/^Error:\s*/, ""));

  const refreshSessions = useCallback(() => {
    void ReplayService.ListSessions()
      .then((list) => setSessions(Array.isArray(list) ? list : []))
      .catch(() => setSessions([]));
  }, []);

  useEffect(() => {
    refreshSessions();
    const off = WailsEvents.On("recording:changed", () => refreshSessions());
    window.addEventListener("focus", refreshSessions);
    // Live recordings grow; keep their durations current while visible.
    const t = window.setInterval(refreshSessions, 10000);
    return () => {
      off();
      window.removeEventListener("focus", refreshSessions);
      window.clearInterval(t);
    };
  }, [refreshSessions]);

  const setView = (v: LibraryView) => {
    setViewState(v);
    try {
      window.localStorage.setItem(LIBRARY_VIEW_KEY, v);
    } catch {
      // per-viewer convenience only
    }
  };

  const refreshClips = useCallback(() => {
    void ReplayService.ListClips()
      .then((list) => setClips(Array.isArray(list) ? list : []))
      .catch(() => setClips([]));
  }, []);

  useEffect(() => {
    refreshClips();
    const off = WailsEvents.On("replay:saved", () => refreshClips());
    // Clips can also be added/removed in Explorer; re-list when the window
    // comes back into focus.
    window.addEventListener("focus", refreshClips);
    return () => {
      off();
      window.removeEventListener("focus", refreshClips);
    };
  }, [refreshClips]);
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

  const shots = items.filter((it) => it.kind !== "video");
  const recordings = items.filter((it) => it.kind === "video");
  const games = Array.from(new Set(clips.map((c) => c.game).filter(Boolean))).sort((a, b) =>
    a.localeCompare(b),
  );
  const gameClips = game ? clips.filter((c) => c.game === game) : clips;
  const visibleItems = view === "shots" ? shots : view === "recordings" ? recordings : view === "all" ? items : [];
  const visibleClips = view === "clips" ? gameClips : view === "all" ? clips : [];
  // "All" interleaves captures and clips by time, newest first.
  const entries: ({ type: "item"; item: CaptureItem; at: number } | { type: "clip"; clip: ReplayClip; at: number })[] = [
    ...visibleItems.map((item) => ({ type: "item" as const, item, at: new Date(item.takenAt).getTime() })),
    ...visibleClips.map((clip) => ({ type: "clip" as const, clip, at: new Date(clip.savedAt).getTime() })),
  ].sort((a, b) => b.at - a.at);

  const selectedItems = items.filter((it) => selected.has(it.id));
  const allSelected = visibleItems.length > 0 && visibleItems.every((it) => selected.has(it.id));

  const toggleSelect = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };

  const selectAll = () => setSelected(new Set(visibleItems.map((it) => it.id)));
  const clearSelection = () => setSelected(new Set());

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      if (pendingDelete) {
        if (!deleting) setPendingDelete(null);
        return;
      }
      if (pendingClip) {
        setPendingClip(null);
        return;
      }
      if (pendingSession) {
        setPendingSession(null);
        return;
      }
      if (selected.size > 0) clearSelection();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [pendingDelete, pendingClip, pendingSession, deleting, selected.size]);

  const confirmSessionDelete = async () => {
    if (!pendingSession) return;
    try {
      await ReplayService.DeleteSession(pendingSession.id);
    } catch (e) {
      report(e);
    }
    setPendingSession(null);
    refreshSessions();
  };

  const confirmClipDelete = async () => {
    if (!pendingClip) return;
    try {
      await ReplayService.DeleteClip(pendingClip.path);
    } catch {
      // the re-list below shows whether it went
    }
    setPendingClip(null);
    refreshClips();
  };

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
            {view === "sessions"
              ? "Game sessions from background and hotkey recording. Open one to scrub its timeline and cut clips"
              : view === "clips"
              ? "Clips saved from instant replay or cut from a game session, sorted by game"
              : view === "recordings"
              ? "Screen recordings you made with Record in the capture overlay"
              : view === "shots"
              ? "Screenshots save here when you hit Done"
              : "Screenshots and screen recordings from the capture overlay, plus game clips"}
          </p>
        </div>
        <div className="flex gap-2">
          {visibleItems.length > 0 ? (
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
            onClick={() =>
              void (view === "clips"
                ? ReplayService.OpenSaveDir()
                : view === "sessions"
                  ? ReplayService.OpenRecordingsDir()
                  : HistoryService.OpenFolder())
            }
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

      {libNote ? (
        <div className="frost border border-destructive/50 px-3 py-2 text-sm text-destructive" role="alert">
          {libNote}
        </div>
      ) : null}

      <div className="flex flex-wrap items-center gap-2">
        <div className="flex flex-wrap gap-1" role="tablist" aria-label="Show">
          {(
            [
              ["all", "All", items.length + clips.length],
              ["shots", "Screenshots", shots.length],
              ["recordings", "Screen recordings", recordings.length],
              ["clips", "Clips", clips.length],
              ["sessions", "Game sessions", sessions.length],
            ] as const
          ).map(([id, label, count]) => (
            <Button
              key={id}
              size="sm"
              role="tab"
              aria-selected={view === id}
              variant={view === id ? "default" : "ghost"}
              onClick={() => setView(id)}
            >
              {label}
              <span className={cn("text-xs", view === id ? "opacity-80" : "text-muted-foreground")}>
                {count}
              </span>
            </Button>
          ))}
        </div>
        {view === "clips" && games.length > 0 ? (
          <select
            className="frost ml-auto border border-border bg-transparent px-2 py-1 text-sm"
            value={game}
            onChange={(e) => setGame(e.target.value)}
            aria-label="Filter clips by game"
          >
            <option value="" className="bg-background text-foreground">
              All games
            </option>
            {games.map((g) => (
              <option key={g} value={g} className="bg-background text-foreground">
                {g} ({clips.filter((c) => c.game === g).length})
              </option>
            ))}
          </select>
        ) : null}
      </div>

      {view === "sessions" ? (
        sessions.length === 0 ? (
          <EmptySessions />
        ) : (
          <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-4">
            {sessions.map((s) => (
              <SessionCard
                key={s.id}
                session={s}
                onOpen={() => void ReplayService.OpenSession(s.id).catch(report)}
                onDelete={() => setPendingSession(s)}
              />
            ))}
          </div>
        )
      ) : loading ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : entries.length === 0 ? (
        view === "clips" ? (
          <EmptyClips />
        ) : view === "all" ? (
          <EmptyLibrary />
        ) : (
          <p className="text-sm text-muted-foreground">
            No {view === "shots" ? "screenshots" : "recordings"} yet.
          </p>
        )
      ) : (
        <div className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-4">
          {entries.map((e) =>
            e.type === "item" ? (
              <CaptureCard
                key={e.item.id}
                item={e.item}
                selected={selected.has(e.item.id)}
                onToggleSelect={() => toggleSelect(e.item.id)}
                onOpen={() => void HistoryService.Open(e.item.id)}
                onSend={() => onSendItem(e.item)}
                onDelete={() => setPendingDelete([e.item])}
              />
            ) : (
              <ClipCard
                key={e.clip.path}
                clip={e.clip}
                onOpen={() => void ReplayService.OpenClip(e.clip.path).catch(report)}
                onReveal={() => void ReplayService.RevealClip(e.clip.path).catch(report)}
                onDelete={() => setPendingClip(e.clip)}
              />
            ),
          )}
        </div>
      )}

      {pendingSession ? (
        <ConfirmDialog
          title="Delete this recording?"
          body={
            <>
              <span className="font-medium text-foreground">
                {pendingSession.game || "Recording"} · {formatDuration(pendingSession.durationSec)}
              </span>{" "}
              ({formatSize(pendingSession.sizeBytes)}) will be deleted from disk. Clips saved from it
              are kept. This can’t be undone.
              {pendingSession.live
                ? " Toru is still recording this one: recording pauses for a moment, this part is deleted, and it carries on as a new recording."
                : null}
            </>
          }
          onCancel={() => setPendingSession(null)}
          onConfirm={() => void confirmSessionDelete()}
        />
      ) : null}

      {pendingClip ? (
        <ConfirmDialog
          title="Delete this clip?"
          body={
            <>
              <span className="font-medium text-foreground">{pendingClip.name}</span> (
              {formatSize(pendingClip.sizeBytes)}) will be deleted from disk. This can’t be
              undone.
            </>
          }
          onCancel={() => setPendingClip(null)}
          onConfirm={() => void confirmClipDelete()}
        />
      ) : null}

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

function EmptyClips() {
  return (
    <div className="frost flex flex-1 flex-col items-center justify-center gap-3 p-10 text-center">
      <Clapperboard className="size-10 text-muted-foreground" />
      <div>
        <p className="font-medium">No clips yet</p>
        <p className="mt-1 text-sm text-muted-foreground">
          Turn on Instant replay in Settings, then press its hotkey while you play to save the last
          few minutes.
        </p>
      </div>
    </div>
  );
}

function EmptySessions() {
  return (
    <div className="frost flex flex-1 flex-col items-center justify-center gap-3 p-10 text-center">
      <Film className="size-10 text-muted-foreground" />
      <div>
        <p className="font-medium">No game sessions yet</p>
        <p className="mt-1 max-w-md text-sm text-muted-foreground">
          In Settings → Game recording, choose Background recording to keep your whole play
          sessions, or Hotkey recording to start and stop one with a hotkey.
        </p>
      </div>
    </div>
  );
}

function formatDuration(sec: number): string {
  const s = Math.max(0, Math.round(sec));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s % 60}s`;
  return `${s}s`;
}

// SessionCard is one background/hotkey recording; click opens the timeline.
function SessionCard({
  session,
  onOpen,
  onDelete,
}: {
  session: RecordingSession;
  onOpen: () => void;
  onDelete: () => void;
}) {
  const [thumbFailed, setThumbFailed] = useState(false);
  const title = session.game || (session.mode === "manual" ? "Hotkey recording" : "Desktop");
  return (
    <div
      className={cn(
        "frost group flex flex-col overflow-hidden border border-transparent",
        "transition-[border-color,box-shadow,transform] duration-150",
        "hover:-translate-y-0.5 hover:border-primary/70 hover:shadow-[0_0_0_1px_var(--color-ring),0_12px_28px_oklch(0_0_0/0.45)]",
      )}
    >
      <button
        type="button"
        onClick={onOpen}
        className="relative aspect-video w-full overflow-hidden bg-black/40 text-left"
        title="Open the timeline"
      >
        {thumbFailed ? (
          <span className="flex h-full w-full items-center justify-center">
            <Film className="size-8 text-muted-foreground" />
          </span>
        ) : (
          <img
            src={session.thumbUrl}
            alt=""
            loading="lazy"
            onError={() => setThumbFailed(true)}
            className="h-full w-full object-contain transition-transform duration-150 group-hover:scale-[1.03]"
          />
        )}
        <span className="pointer-events-none absolute inset-0 bg-primary/0 transition-colors duration-150 group-hover:bg-primary/10" />
        <span className="absolute left-1.5 top-1.5 flex max-w-[85%] items-center gap-1 bg-black/60 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-white">
          {session.live ? <Circle className="size-2.5 shrink-0 fill-red-500 text-red-500" /> : <Film className="size-3 shrink-0" />}
          <span className="truncate">{session.live ? "Recording" : session.mode === "manual" ? "Hotkey" : "Background"}</span>
        </span>
        <span className="absolute bottom-1.5 right-1.5 bg-black/60 px-1.5 py-0.5 text-[10px] tabular-nums text-white">
          {formatDuration(session.durationSec)}
        </span>
      </button>
      <div className="flex items-center gap-1 px-2 py-1.5">
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-medium" title={title}>
            {title}
          </div>
          <div className="truncate text-[10px] text-muted-foreground">
            {formatWhen(new Date(session.startMs).toISOString())} · {formatSize(session.sizeBytes)}
          </div>
        </div>
        <Button
          size="icon"
          variant="ghost"
          className="size-7 shrink-0 opacity-60 hover:opacity-100"
          title={session.live ? "Delete (pauses the recording for a moment)" : "Delete"}
          onClick={onDelete}
        >
          <Trash2 className="size-3.5" />
        </Button>
      </div>
    </div>
  );
}

function formatSize(bytes: number): string {
  if (bytes >= 1e9) return `${(bytes / 1e9).toFixed(1)} GB`;
  return `${Math.max(1, Math.round(bytes / 1e6))} MB`;
}

// ClipCard is one saved instant replay (files in the replay folder, not the
// capture library): click opens it in the default player.
function ClipCard({
  clip,
  onOpen,
  onReveal,
  onDelete,
}: {
  clip: ReplayClip;
  onOpen: () => void;
  onReveal: () => void;
  onDelete: () => void;
}) {
  const [thumbFailed, setThumbFailed] = useState(false);
  return (
    <div
      className={cn(
        "frost group flex flex-col overflow-hidden border border-transparent",
        "transition-[border-color,box-shadow,transform] duration-150",
        "hover:-translate-y-0.5 hover:border-primary/70 hover:shadow-[0_0_0_1px_var(--color-ring),0_12px_28px_oklch(0_0_0/0.45)]",
      )}
    >
      <button
        type="button"
        onClick={onOpen}
        className="relative aspect-video w-full overflow-hidden bg-black/40 text-left"
        title="Open in Toru: play, trim, save or share"
      >
        {thumbFailed ? (
          <span className="flex h-full w-full items-center justify-center">
            <Clapperboard className="size-8 text-muted-foreground" />
          </span>
        ) : (
          <img
            src={clip.thumbUrl}
            alt=""
            loading="lazy"
            onError={() => setThumbFailed(true)}
            className="h-full w-full object-contain transition-transform duration-150 group-hover:scale-[1.03]"
          />
        )}
        <span className="pointer-events-none absolute inset-0 bg-primary/0 transition-colors duration-150 group-hover:bg-primary/10" />
        <span className="absolute left-1.5 top-1.5 flex max-w-[85%] items-center gap-1 bg-black/60 px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-white">
          <Clapperboard className="size-3 shrink-0" />
          <span className="truncate">{clip.game || "Clip"}</span>
        </span>
        <span className="pointer-events-none absolute inset-x-0 bottom-0 translate-y-full bg-black/55 px-2 py-1 text-center text-[11px] text-white transition-transform duration-150 group-hover:translate-y-0">
          Click to play
        </span>
      </button>
      <div className="flex items-center gap-1 px-2 py-1.5">
        <div className="min-w-0 flex-1">
          <div className="truncate text-xs font-medium" title={clip.name}>
            {clip.name}
          </div>
          <div className="truncate text-[10px] text-muted-foreground">
            {formatWhen(clip.savedAt)} · {formatSize(clip.sizeBytes)}
          </div>
        </div>
        <Button
          size="icon"
          variant="ghost"
          className="size-7 shrink-0 opacity-60 hover:opacity-100"
          title="Show in folder"
          onClick={onReveal}
        >
          <FolderOpen className="size-3.5" />
        </Button>
        <Button
          size="icon"
          variant="ghost"
          className="size-7 shrink-0 opacity-60 hover:opacity-100"
          title="Delete"
          onClick={onDelete}
        >
          <Trash2 className="size-3.5" />
        </Button>
      </div>
    </div>
  );
}

function ConfirmDialog({
  title,
  body,
  onCancel,
  onConfirm,
}: {
  title: string;
  body: React.ReactNode;
  onCancel: () => void;
  onConfirm: () => void;
}) {
  return (
    <div
      className="absolute inset-0 z-50 flex items-center justify-center bg-black/55 p-4"
      role="dialog"
      aria-modal="true"
      onClick={onCancel}
    >
      <div className="frost w-full max-w-sm p-4 shadow-xl" onClick={(e) => e.stopPropagation()}>
        <h2 className="text-base font-semibold">{title}</h2>
        <p className="mt-1 text-sm text-muted-foreground">{body}</p>
        <div className="mt-4 flex justify-end gap-2">
          <Button size="sm" variant="ghost" onClick={onCancel}>
            Cancel
          </Button>
          <Button size="sm" variant="destructive" onClick={onConfirm}>
            <Trash2 /> Delete
          </Button>
        </div>
      </div>
    </div>
  );
}

function CaptureCard({
  item,
  selected,
  onToggleSelect,
  onOpen,
  onSend,
  onDelete,
}: {
  item: CaptureItem;
  selected: boolean;
  onToggleSelect: () => void;
  onOpen: () => void;
  onSend: () => void;
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
          title="Send"
          aria-label="Send file"
          onClick={(e) => {
            e.stopPropagation();
            onSend();
          }}
        >
          <Send className="size-3.5" />
        </Button>
        <Button
          size="icon"
          variant="ghost"
          className="size-7 shrink-0 opacity-60 hover:opacity-100"
          title="Delete"
          aria-label="Delete"
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

type SettingsTab = "general" | "recording" | "notifications" | "shortcuts" | "about";
const SETTINGS_TAB_KEY = "toru.settings.tab";

function SettingsPage() {
  const [tab, setTabState] = useState<SettingsTab>(() => {
    try {
      const v = window.localStorage.getItem(SETTINGS_TAB_KEY);
      if (v === "general" || v === "recording" || v === "notifications" || v === "shortcuts" || v === "about") return v;
    } catch {
      // default tab
    }
    return "general";
  });
  const setTab = (t: SettingsTab) => {
    setTabState(t);
    try {
      window.localStorage.setItem(SETTINGS_TAB_KEY, t);
    } catch {
      // per-viewer convenience only
    }
  };
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

  const tabs: { id: SettingsTab; label: string }[] = [
    { id: "general", label: "General" },
    { id: "recording", label: "Game recording" },
    { id: "notifications", label: "Notifications" },
    { id: "shortcuts", label: "Shortcuts" },
    { id: "about", label: "About" },
  ];

  return (
    <div className="mx-auto flex max-w-2xl flex-col gap-5">
      <div className="flex flex-col gap-3">
        <h1 className="text-xl font-semibold tracking-tight">Settings</h1>
        <div role="tablist" aria-label="Settings sections" className="flex gap-1 border-b border-border">
          {tabs.map((t) => (
            <button
              key={t.id}
              type="button"
              role="tab"
              aria-selected={tab === t.id}
              onClick={() => setTab(t.id)}
              className={cn(
                "-mb-px border-b-2 px-3 py-2 text-sm transition-colors",
                tab === t.id
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )}
            >
              {t.label}
            </button>
          ))}
        </div>
      </div>

      {tab === "recording" ? <ReplayPanel /> : null}
      {tab === "notifications" ? <NotificationsPanel onOpenRecording={() => setTab("recording")} /> : null}
      {tab === "shortcuts" ? <Shortcuts includeAll /> : null}
      {tab === "about" ? <Updater /> : null}
      {tab === "general" ? (
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

        {/* Keyed on the folder: switching it can pause auto-delete (store.go SetDir). */}
        <LibraryAutoDelete key={libraryDir} />
      </div>
      ) : null}
    </div>
  );
}

// Library auto-delete. Off (the default) keeps every capture until you delete
// it. On, a capture goes once it falls past "Keep at most" or is older than
// "Delete after", whichever comes first. A change that would delete captures
// you already have asks first, with the count.
const KEEP_COUNTS = [0, 25, 50, 100, 250, 500];
const KEEP_DAYS = [0, 7, 30, 90, 365];

const fmtKeepCount = (n: number) => (n === 0 ? "No limit" : String(n));
const fmtKeepDays = (d: number) => (d === 0 ? "Never" : d === 365 ? "1 year" : `${d} days`);

function LibraryAutoDelete() {
  const [saved, setSaved] = useState<LibraryRetention | null>(null);
  const [pending, setPending] = useState<{ next: LibraryRetention; count: number } | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    void HistoryService.GetRetention()
      .then(setSaved)
      .catch(() => setSaved(new LibraryRetention()));
  }, []);

  const apply = async (next: LibraryRetention) => {
    await HistoryService.SetRetention(next);
    setSaved(next);
    setPending(null);
  };

  // propose applies next straight away unless it would delete existing
  // captures; then it waits for the user to confirm the count.
  const propose = async (next: LibraryRetention) => {
    if (busy) return;
    setBusy(true);
    setError("");
    try {
      const count = await HistoryService.CountExpired(next);
      if (count > 0) setPending({ next, count });
      else await apply(next);
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  const confirm = async () => {
    if (!pending || busy) return;
    setBusy(true);
    setError("");
    try {
      await apply(pending.next);
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  if (!saved) return null;
  const shown = pending?.next ?? saved;
  const withPatch = (patch: Partial<LibraryRetention>) => new LibraryRetention({ ...shown, ...patch });

  return (
    <div className="flex flex-col gap-3 border-t border-border pt-4">
      <div className="flex items-center justify-between gap-3">
        <div className="flex flex-col">
          <span className="text-sm font-medium">Auto-delete old captures</span>
          <span className="text-xs text-muted-foreground">
            Off: screenshots and recordings stay in the library until you delete them
          </span>
        </div>
        <Switch
          checked={shown.autoDelete}
          disabled={busy}
          onCheckedChange={(on) =>
            void propose(
              // Turning it on with no limit set would do nothing; start at 30 days.
              on && !shown.keepCount && !shown.keepDays
                ? withPatch({ autoDelete: true, keepDays: 30 })
                : withPatch({ autoDelete: on }),
            )
          }
          aria-label="Automatically delete old library captures"
        />
      </div>
      {shown.autoDelete ? (
        <>
          <div className="flex flex-col gap-1.5">
            <span className="text-xs text-muted-foreground">Keep at most (newest first)</span>
            <ChoiceChips
              options={KEEP_COUNTS}
              value={shown.keepCount}
              format={fmtKeepCount}
              disabled={busy}
              onPick={(v) => void propose(withPatch({ keepCount: v }))}
            />
          </div>
          <div className="flex flex-col gap-1.5">
            <span className="text-xs text-muted-foreground">Delete after</span>
            <ChoiceChips
              options={KEEP_DAYS}
              value={shown.keepDays}
              format={fmtKeepDays}
              disabled={busy}
              onPick={(v) => void propose(withPatch({ keepDays: v }))}
            />
          </div>
          {!shown.keepCount && !shown.keepDays ? (
            <span className="text-xs text-muted-foreground">With no limit set, nothing is deleted.</span>
          ) : null}
        </>
      ) : null}
      {pending ? (
        <div className="flex flex-wrap items-center gap-2 border border-destructive/40 bg-destructive/10 px-3 py-2">
          <span className="text-xs">
            This deletes {pending.count} {pending.count === 1 ? "capture" : "captures"} already in your library.
          </span>
          <div className="ml-auto flex gap-2">
            <Button size="sm" variant="destructive" disabled={busy} onClick={() => void confirm()}>
              Delete {pending.count}
            </Button>
            <Button size="sm" variant="ghost" disabled={busy} onClick={() => setPending(null)}>
              Cancel
            </Button>
          </div>
        </div>
      ) : null}
      {error ? <span className="text-xs text-destructive">{error}</span> : null}
    </div>
  );
}

// ChoiceChips is a row of exclusive choice buttons.
function ChoiceChips({
  options,
  value,
  format,
  disabled,
  onPick,
}: {
  options: number[];
  value: number;
  format: (v: number) => string;
  disabled?: boolean;
  onPick: (v: number) => void;
}) {
  return (
    <div className="flex flex-wrap gap-1">
      {options.map((o) => (
        <Button
          key={o}
          size="sm"
          variant={value === o ? "default" : "outline"}
          disabled={disabled}
          onClick={() => onPick(o)}
          className="h-7 px-2.5 text-xs"
        >
          {format(o)}
        </Button>
      ))}
    </div>
  );
}
