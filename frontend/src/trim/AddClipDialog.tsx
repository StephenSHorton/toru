import { useEffect, useState } from "react";
import { Clapperboard, Loader2, Video } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { HistoryService, ReplayService, type ReplayClip } from "@/lib/api";
import { cn } from "@/lib/utils";

// AddClipDialog lets the video editor pick another video from the Library to
// append to the timeline: saved clips (ReplayService.ListClips) and screen
// recordings (HistoryService, kind "video"). It only returns the chosen
// absolute path; the window probes it (duration, size) and appends it with
// addSource. Recording sessions (long background recordings) aren't listed:
// save a clip from one first.

type Entry = { kind: "clip" | "recording"; path: string; name: string; sub: string; at: number; thumb: string };
type Tab = "clips" | "recordings";

export interface AddClipDialogProps {
  open: boolean;
  onClose: () => void;
  onPick: (path: string) => void;
  // Paths already on the timeline, marked "Added" (they can still be added
  // again: the same clip twice is a valid edit).
  added?: string[];
}

export function AddClipDialog({ open, onClose, onPick, added = [] }: AddClipDialogProps) {
  const [tab, setTab] = useState<Tab>("clips");
  const [clips, setClips] = useState<Entry[] | null>(null);
  const [recs, setRecs] = useState<Entry[] | null>(null);

  // Re-list each time it opens (clips may have been saved since).
  useEffect(() => {
    if (!open) return;
    let live = true;
    void ReplayService.ListClips()
      .then((list) => live && setClips((Array.isArray(list) ? list : []).map(clipEntry).sort(newest)))
      .catch(() => live && setClips([]));
    void HistoryService.List()
      .then(
        (list) =>
          live &&
          setRecs(
            (Array.isArray(list) ? list : [])
              .filter((it) => it.kind === "video")
              .map((it) => ({
                kind: "recording" as const,
                path: it.path,
                name: it.label || baseName(it.path),
                sub: formatWhen(it.takenAt),
                at: new Date(it.takenAt).getTime(),
                thumb: "",
              }))
              .sort(newest),
          ),
      )
      .catch(() => live && setRecs([]));
    return () => {
      live = false;
    };
  }, [open]);

  const list = tab === "clips" ? clips : recs;
  const addedSet = new Set(added.map(normPath));

  return (
    <Dialog open={open} onClose={onClose} className="flex max-h-[80vh] w-[46rem] flex-col gap-3">
      <div className="flex items-center gap-2">
        <h2 className="text-sm font-medium">Add a clip to the end</h2>
        <div className="ml-auto flex gap-1">
          <Button size="sm" variant={tab === "clips" ? "secondary" : "ghost"} onClick={() => setTab("clips")}>
            <Clapperboard /> Clips{clips ? ` (${clips.length})` : ""}
          </Button>
          <Button size="sm" variant={tab === "recordings" ? "secondary" : "ghost"} onClick={() => setTab("recordings")}>
            <Video /> Screen recordings{recs ? ` (${recs.length})` : ""}
          </Button>
        </div>
      </div>

      <div className="min-h-40 flex-1 overflow-y-auto">
        {list === null ? (
          <div className="flex h-40 items-center justify-center gap-2 text-xs text-muted-foreground">
            <Loader2 className="size-4 animate-spin" /> Loading…
          </div>
        ) : list.length === 0 ? (
          <div className="flex h-40 items-center justify-center text-xs text-muted-foreground">
            {tab === "clips" ? "No saved clips yet." : "No screen recordings in the Library yet."}
          </div>
        ) : (
          <div className="grid grid-cols-3 gap-2">
            {list.map((e) => (
              <EntryCard key={e.path} entry={e} added={addedSet.has(normPath(e.path))} onPick={() => onPick(e.path)} />
            ))}
          </div>
        )}
      </div>

      <div className="flex justify-end">
        <Button size="sm" variant="ghost" onClick={onClose}>
          Cancel
        </Button>
      </div>
    </Dialog>
  );
}

function EntryCard({ entry, added, onPick }: { entry: Entry; added: boolean; onPick: () => void }) {
  const [thumbFailed, setThumbFailed] = useState(false);
  const Icon = entry.kind === "clip" ? Clapperboard : Video;
  return (
    <button
      type="button"
      onClick={onPick}
      title={`Add ${entry.name} to the end of the timeline`}
      className={cn(
        "group flex flex-col overflow-hidden border border-transparent bg-card/40 text-left",
        "transition-[border-color] duration-150 hover:border-primary/70 focus-visible:border-primary focus-visible:outline-none",
      )}
    >
      <div className="relative aspect-video w-full overflow-hidden bg-black/40">
        {entry.thumb && !thumbFailed ? (
          <img src={entry.thumb} alt="" loading="lazy" onError={() => setThumbFailed(true)} className="h-full w-full object-contain" />
        ) : entry.kind === "recording" ? (
          // Recordings have no cached thumbnail; the first frame stands in.
          <video src={servedUrl(entry.path)} muted preload="metadata" className="h-full w-full object-contain" />
        ) : (
          <span className="flex h-full w-full items-center justify-center">
            <Icon className="size-7 text-muted-foreground" />
          </span>
        )}
        <span className="pointer-events-none absolute inset-0 bg-primary/0 transition-colors duration-150 group-hover:bg-primary/10" />
        {added ? (
          <span className="absolute right-1.5 top-1.5 bg-primary px-1.5 py-0.5 text-[10px] uppercase tracking-wide text-primary-foreground">
            Added
          </span>
        ) : null}
      </div>
      <div className="px-2 py-1.5">
        <div className="truncate text-xs font-medium" title={entry.name}>
          {entry.name}
        </div>
        <div className="truncate text-[10px] text-muted-foreground">{entry.sub}</div>
      </div>
    </button>
  );
}

function clipEntry(c: ReplayClip): Entry {
  const when = formatWhen(c.savedAt);
  return {
    kind: "clip",
    path: c.path,
    name: c.name,
    sub: [c.game, when].filter(Boolean).join(" · "),
    at: new Date(c.savedAt as unknown as string).getTime(),
    thumb: c.thumbUrl,
  };
}

const newest = (a: Entry, b: Entry) => (b.at || 0) - (a.at || 0);
const normPath = (p: string) => p.replace(/\\/g, "/").toLowerCase();

// Same served URL the Library uses for capture files (/__file/<basename>).
function servedUrl(absPath: string): string {
  return "/__file/" + encodeURIComponent(baseName(absPath));
}

function baseName(p: string): string {
  return p.replace(/\\/g, "/").split("/").pop() ?? p;
}

function formatWhen(iso: unknown): string {
  try {
    const d = iso instanceof Date ? iso : new Date(iso as string);
    if (Number.isNaN(d.getTime())) return "";
    return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
  } catch {
    return "";
  }
}
