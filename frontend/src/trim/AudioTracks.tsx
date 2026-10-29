import { useEffect, useRef, useState } from "react";
import { Volume2, VolumeX } from "lucide-react";
import { Button } from "@/components/ui/button";
import { VideoService, type MediaInfo, type TrackLevel } from "@/lib/api";
import { cn } from "@/lib/utils";

// Per-track audio levels for the video editor (docs/video-editor.md, v1
// scope 5). One row per source track: a source recorded with separate
// tracks shows its source tracks (game, Discord, mic; not the mix), a source
// with one track shows one row. Each row has a 0..200% volume and mute.
//
// levels lists only sources the user changed: a source whose rows are all at
// 100% and unmuted is left out, so the export (and preview) keeps its
// original mix track untouched. Changing any row lists every row of that
// source.

export const DEFAULT_VOLUME = 100;

type Row = { source: number; index: number; label: string };

export function AudioTracks({
  sources,
  levels,
  onChange,
  className,
}: {
  sources: { path: string }[];
  levels: TrackLevel[];
  onChange: (levels: TrackLevel[]) => void;
  className?: string;
}) {
  const infos = useMediaInfos(sources.map((s) => s.path));
  const multi = sources.length > 1;

  const rowsOf = (k: number): Row[] => {
    const info = infos[sources[k].path];
    if (!info || !info.tracks.length) return [];
    const tracks = info.tracks.length > 1 ? info.tracks.filter((t) => !t.isMix) : info.tracks;
    return tracks.map((t, i) => ({
      source: k,
      index: t.index,
      label: t.title || (tracks.length > 1 ? `Track ${i + 1}` : "Audio"),
    }));
  };

  const valueOf = (r: Row) =>
    levels.find((l) => l.source === r.source && l.index === r.index) ?? {
      source: r.source,
      index: r.index,
      volume: DEFAULT_VOLUME,
      muted: false,
    };

  const set = (r: Row, patch: Partial<Pick<TrackLevel, "volume" | "muted">>) => {
    const rows = rowsOf(r.source).map((row) => {
      const v = valueOf(row);
      return row.index === r.index ? { ...v, ...patch } : v;
    });
    const rest = levels.filter((l) => l.source !== r.source);
    const changed = rows.some((l) => l.volume !== DEFAULT_VOLUME || l.muted);
    onChange(changed ? [...rest, ...rows] : rest);
  };

  return (
    <div className={cn("flex flex-col gap-1", className)}>
      {sources.map((s, k) => {
        const info = infos[s.path];
        const rows = rowsOf(k);
        return (
          <div key={`${k}:${s.path}`} className="flex flex-col gap-1">
            {multi ? (
              <div className="truncate text-[11px] text-muted-foreground" title={s.path}>
                Clip {k + 1} · {baseName(s.path)}
              </div>
            ) : null}
            {info === undefined ? (
              <div className="text-xs text-muted-foreground">Reading audio tracks…</div>
            ) : info === null ? (
              <div className="text-xs text-muted-foreground">Couldn&rsquo;t read this clip&rsquo;s audio tracks.</div>
            ) : !rows.length ? (
              <div className="text-xs text-muted-foreground">No audio</div>
            ) : (
              rows.map((r) => {
                const v = valueOf(r);
                return (
                  <div key={r.index} className="flex items-center gap-2">
                    <Button
                      size="icon"
                      variant="ghost"
                      className="size-7 shrink-0"
                      onClick={() => set(r, { muted: !v.muted })}
                      title={v.muted ? `Unmute ${r.label}` : `Mute ${r.label}`}
                      aria-label={v.muted ? `Unmute ${r.label}` : `Mute ${r.label}`}
                      aria-pressed={v.muted}
                    >
                      {v.muted || v.volume === 0 ? <VolumeX /> : <Volume2 />}
                    </Button>
                    <span className={cn("w-36 truncate text-xs", v.muted && "text-muted-foreground line-through")} title={r.label}>
                      {r.label}
                    </span>
                    <input
                      type="range"
                      min={0}
                      max={200}
                      step={5}
                      value={v.volume}
                      disabled={v.muted}
                      onChange={(e) => set(r, { volume: Number(e.target.value) })}
                      onDoubleClick={() => set(r, { volume: DEFAULT_VOLUME })}
                      className="min-w-24 flex-1 accent-primary disabled:opacity-40"
                      title={`${r.label} volume (double-click for 100%)`}
                      aria-label={`${r.label} volume`}
                    />
                    <span className="w-10 text-right text-xs tabular-nums text-muted-foreground">{v.volume}%</span>
                  </div>
                );
              })
            )}
          </div>
        );
      })}
    </div>
  );
}

// useMediaInfos probes each path once (ProbeMedia) and keeps the results by
// path: undefined while probing, null when the probe failed.
export function useMediaInfos(paths: string[]): Record<string, MediaInfo | null | undefined> {
  const [infos, setInfos] = useState<Record<string, MediaInfo | null>>({});
  const requested = useRef(new Set<string>());
  const mounted = useRef(true);
  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const key = paths.join("\n");
  useEffect(() => {
    for (const p of paths) {
      if (!p || requested.current.has(p)) continue;
      requested.current.add(p);
      VideoService.ProbeMedia(p)
        .then((info) => mounted.current && setInfos((m) => ({ ...m, [p]: info })))
        .catch(() => mounted.current && setInfos((m) => ({ ...m, [p]: null })));
    }
    // Keyed on the joined paths: a new array with the same paths is no change.
  }, [key]);
  return infos;
}

function baseName(p: string): string {
  return p.split(/[\\/]/).pop() ?? p;
}
