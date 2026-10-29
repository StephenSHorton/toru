import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Events as WailsEvents, Window } from "@wailsio/runtime";
import { Clapperboard, FileAudio, Image as ImageIcon, Import, Loader2, Music, Video } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Segmented } from "@/components/ui/segmented";
import { HistoryService, MediaService, ReplayService, type BinMedia } from "@/lib/api";
import { cn } from "@/lib/utils";
import { MEDIA_MIME } from "@/trim/MultiTimeline";
import type { Media, MediaKind } from "@/trim/project";

// The v2 editor's media bin (docs/video-editor-v2.md, v2 scope 1): a sidebar
// with two tabs. Library lists your clips, recordings and screenshots;
// Imported lists files you added this session, by dragging them from
// Explorer onto the bin or with Import (multi-select dialog). Every item is
// registered with MediaService, which probes it and serves it at
// /__media/<id>/<name>; imported files stay where they are.
//
// Drag an item onto a timeline track (HTML5 drag, MEDIA_MIME = media id; the
// timeline resolves the id with bin.byId as its mediaFor), or double-click
// to append it (onAppend).
//
// useMediaBin owns the state so the window can hand bin.byId to the
// timeline; <MediaBin bin={bin} /> renders it.

export type BinItem = {
  media: Media;
  name: string;
  sub: string; // what / when, under the name
  thumb: string; // "" = an icon
  group: "clip" | "recording" | "screenshot" | "imported";
};

export type MediaBinState = ReturnType<typeof useMediaBin>;

export function useMediaBin() {
  const [library, setLibrary] = useState<BinItem[] | null>(null); // null = loading
  const [imported, setImported] = useState<BinItem[]>([]);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");

  // Every media the bin has handed out, by id, for the timeline's drops.
  const known = useRef(new Map<string, Media>());
  const remember = (items: BinItem[]) => items.forEach((it) => known.current.set(it.media.id, it.media));
  const byId = useCallback((id: string) => known.current.get(id), []);

  const loadLibrary = useCallback(async () => {
    try {
      const [clips, captures] = await Promise.all([ReplayService.ListClips(), HistoryService.List()]);
      const wanted = [
        ...clips.map((c) => ({ path: c.path, name: c.name, sub: [c.game, when(c.savedAt)].filter(Boolean).join(" · "), thumb: c.thumbUrl, group: "clip" as const, at: at(c.savedAt) })),
        ...captures.map((it) => ({
          path: it.path,
          name: it.label || baseName(it.path),
          sub: when(it.takenAt),
          thumb: "",
          group: it.kind === "video" ? ("recording" as const) : ("screenshot" as const),
          at: at(it.takenAt),
        })),
      ].sort((a, b) => b.at - a.at);
      const registered = await MediaService.Register(wanted.map((w) => w.path));
      const byPath = new Map(registered.map((m) => [normPath(m.path), m]));
      const items = (
        await Promise.all(
          wanted.map(async (w): Promise<BinItem | null> => {
            const m = byPath.get(normPath(w.path));
            if (!m) return null; // deleted since it was listed, or unreadable
            const media = toMedia(m);
            return { media, name: w.name, sub: w.sub || describe(media), thumb: w.thumb || (await thumbFor(media)), group: w.group };
          }),
        )
      ).filter((it): it is BinItem => it !== null);
      remember(items);
      setLibrary(items);
    } catch (err) {
      setLibrary([]);
      setNote(`Couldn't read the Library: ${errText(err)}`);
    }
  }, []);

  useEffect(() => {
    void loadLibrary();
  }, [loadLibrary]);

  const addImported = useCallback(async (got: BinMedia[], asked: number) => {
    const items = await Promise.all(
      got.map(async (m): Promise<BinItem> => {
        const media = toMedia(m);
        return { media, name: baseName(media.path), sub: describe(media), thumb: await thumbFor(media), group: "imported" };
      }),
    );
    remember(items);
    setImported((cur) => {
      const ids = new Set(items.map((i) => i.media.id));
      return [...items, ...cur.filter((i) => !ids.has(i.media.id))];
    });
    const skipped = asked - got.length;
    setNote(
      skipped > 0
        ? `${skipped} file${skipped === 1 ? "" : "s"} skipped: Toru takes video (mp4, mkv, mov, webm), audio (mp3, wav, m4a, aac, ogg, flac) and images (png, jpg, webp, gif).`
        : "",
    );
    return items;
  }, []);

  const importPaths = useCallback(
    async (paths: string[]) => {
      if (!paths.length) return [];
      setBusy(true);
      try {
        return await addImported(await MediaService.Register(paths), paths.length);
      } catch (err) {
        setNote(`Couldn't import: ${errText(err)}`);
        return [];
      } finally {
        setBusy(false);
      }
    },
    [addImported],
  );

  const pick = useCallback(async () => {
    setBusy(true);
    try {
      const got = await MediaService.PickMedia();
      return await addImported(got, got.length);
    } catch (err) {
      setNote(`Couldn't import: ${errText(err)}`);
      return [];
    } finally {
      setBusy(false);
    }
  }, [addImported]);

  // Files dropped from Explorer: Go forwards the window's drop as
  // "media:dropped". Every edit window hears it, so keep only our own.
  useEffect(() => {
    let mine = "";
    void Window.Name()
      .then((n) => (mine = n))
      .catch(() => {});
    const off = WailsEvents.On("media:dropped", (ev: { data: unknown; sender?: string }) => {
      if (mine && ev.sender && ev.sender !== mine) return;
      const paths = Array.isArray(ev.data) ? (ev.data as unknown[]).filter((p): p is string => typeof p === "string") : [];
      void importPaths(paths);
    });
    return () => off();
  }, [importPaths]);

  return { library, imported, busy, note, setNote, byId, importPaths, pick, reloadLibrary: loadLibrary };
}

type Tab = "library" | "imported";

export function MediaBin({
  bin,
  onAppend,
  className,
}: {
  bin: MediaBinState;
  onAppend?: (m: Media) => void;
  className?: string;
}) {
  const [tab, setTab] = useState<Tab>("library");
  // A drop or an Import switches to the Imported tab to show what came in.
  const importedCount = bin.imported.length;
  const lastCount = useRef(importedCount);
  useEffect(() => {
    if (importedCount > lastCount.current) setTab("imported");
    lastCount.current = importedCount;
  }, [importedCount]);

  const items = tab === "library" ? bin.library : bin.imported;
  const groups = useMemo(() => {
    if (!items) return [];
    const order: [BinItem["group"], string][] =
      tab === "library"
        ? [
            ["clip", "Clips"],
            ["recording", "Recordings"],
            ["screenshot", "Screenshots"],
          ]
        : [["imported", ""]];
    return order.map(([g, label]) => ({ label, items: items.filter((i) => i.group === g) })).filter((g) => g.items.length);
  }, [items, tab]);

  return (
    <aside
      // The runtime forwards Explorer drops only onto elements with this.
      data-file-drop-target
      className={cn(
        "flex min-h-0 flex-col border-r bg-card/30",
        "[&.file-drop-target-active]:bg-primary/10 [&.file-drop-target-active]:outline [&.file-drop-target-active]:outline-2 [&.file-drop-target-active]:-outline-offset-2 [&.file-drop-target-active]:outline-primary",
        className,
      )}
    >
      <div className="flex items-center gap-2 p-2">
        <Segmented
          size="xs"
          value={tab}
          onChange={setTab}
          aria-label="Media"
          options={[
            { value: "library", label: "Library" },
            { value: "imported", label: importedCount ? `Imported (${importedCount})` : "Imported" },
          ]}
        />
        <Button
          size="sm"
          variant="ghost"
          className="ml-auto"
          disabled={bin.busy}
          onClick={() => void bin.pick()}
          title="Add video, audio or image files"
        >
          {bin.busy ? <Loader2 className="animate-spin" /> : <Import />} Import
        </Button>
      </div>
      {bin.note ? (
        <button
          type="button"
          className="mx-2 mb-2 text-left text-[11px] text-muted-foreground hover:text-foreground"
          onClick={() => bin.setNote("")}
          title="Dismiss"
        >
          {bin.note}
        </button>
      ) : null}

      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-2">
        {items === null ? (
          <div className="flex items-center gap-2 p-2 text-xs text-muted-foreground">
            <Loader2 className="size-3.5 animate-spin" /> Loading your Library…
          </div>
        ) : !groups.length ? (
          <div className="flex flex-col items-center gap-2 px-3 py-8 text-center text-xs text-muted-foreground">
            <Import className="size-6 opacity-60" />
            {tab === "library"
              ? "No clips, recordings or screenshots yet."
              : "Drag video, music or images here from File Explorer, or press Import."}
          </div>
        ) : (
          groups.map((g) => (
            <section key={g.label || "all"} className="mb-3">
              {g.label ? <h3 className="mb-1 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{g.label}</h3> : null}
              <div className="grid grid-cols-2 gap-1.5">
                {g.items.map((it) => (
                  <BinTile key={it.media.id} item={it} onAppend={onAppend} />
                ))}
              </div>
            </section>
          ))
        )}
      </div>
    </aside>
  );
}

function BinTile({ item, onAppend }: { item: BinItem; onAppend?: (m: Media) => void }) {
  const [thumbFailed, setThumbFailed] = useState(false);
  const { media } = item;
  const Icon = media.kind === "audio" ? Music : media.kind === "image" ? ImageIcon : item.group === "clip" ? Clapperboard : Video;
  return (
    <div
      role="button"
      tabIndex={0}
      draggable
      onDragStart={(e) => {
        e.dataTransfer.setData(MEDIA_MIME, media.id);
        e.dataTransfer.setData("text/plain", item.name);
        e.dataTransfer.effectAllowed = "copy";
      }}
      onDoubleClick={() => onAppend?.(media)}
      onKeyDown={(e) => {
        if (e.key === "Enter") onAppend?.(media);
      }}
      title={`${item.name}\n${media.path}\nDrag onto a track, or double-click to add it to the end`}
      className="group flex cursor-grab flex-col overflow-hidden border border-transparent bg-black/20 outline-none hover:border-primary/70 focus-visible:border-primary active:cursor-grabbing"
    >
      <div className="relative aspect-video w-full overflow-hidden bg-black/40">
        {item.thumb && !thumbFailed ? (
          <img
            src={item.thumb}
            alt=""
            loading="lazy"
            draggable={false}
            onError={() => setThumbFailed(true)}
            className="h-full w-full object-contain"
          />
        ) : (
          <span className="flex h-full w-full items-center justify-center">
            {media.kind === "audio" ? <FileAudio className="size-7 text-muted-foreground" /> : <Icon className="size-7 text-muted-foreground" />}
          </span>
        )}
        <span className="absolute left-1 top-1 flex items-center bg-black/60 p-0.5 text-white">
          <Icon className="size-3" />
        </span>
        {media.duration > 0 ? (
          <span className="absolute bottom-1 right-1 bg-black/60 px-1 text-[10px] tabular-nums text-white">{fmtLen(media.duration)}</span>
        ) : null}
      </div>
      <div className="px-1.5 py-1">
        <div className="truncate text-[11px] font-medium">{item.name}</div>
        <div className="truncate text-[10px] text-muted-foreground">{item.sub}</div>
      </div>
    </div>
  );
}

// toMedia turns MediaService's Media into the project's (same fields; the
// Go kind enum is the same strings).
export function toMedia(m: BinMedia): Media {
  return {
    id: m.id,
    path: m.path,
    url: m.url,
    kind: m.kind as string as MediaKind,
    duration: m.duration,
    width: m.width,
    height: m.height,
    fps: m.fps,
    hasAudio: m.hasAudio,
    tracks: m.tracks ?? [],
  };
}

async function thumbFor(m: Media): Promise<string> {
  try {
    return await MediaService.Thumbnail(m.id);
  } catch {
    return "";
  }
}

function describe(m: Media): string {
  if (m.kind === "image") return `Image · ${m.width}×${m.height}`;
  if (m.kind === "audio") return m.tracks.length > 1 ? `Audio · ${m.tracks.length} tracks` : "Audio";
  return `${m.width}×${m.height}${m.fps ? ` · ${Math.round(m.fps)} fps` : ""}`;
}

// fmtLen renders a length as m:ss (h:mm:ss past an hour).
export function fmtLen(sec: number): string {
  const s = Math.max(0, Math.round(sec));
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  const r = String(s % 60).padStart(2, "0");
  return h ? `${h}:${String(m).padStart(2, "0")}:${r}` : `${m}:${r}`;
}

function at(iso: unknown): number {
  const t = new Date(iso as string).getTime();
  return Number.isFinite(t) ? t : 0;
}

function when(iso: unknown): string {
  const t = at(iso);
  if (!t) return "";
  return new Date(t).toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

function baseName(p: string): string {
  return p.replace(/\\/g, "/").split("/").pop() ?? p;
}

const normPath = (p: string) => p.replace(/\\/g, "/").toLowerCase();

function errText(err: unknown): string {
  return String(err).replace(/^Error:\s*/, "");
}
