import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, AppWindow, RefreshCw, Search, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { ClickerService, ClickerSettings, ProcessEntry, ProcessInfo } from "@/lib/api";
import { cn } from "@/lib/utils";
import { CardHeader, CardProps, errText, inputClass } from "./PointsCardShared";

// Process list: limit runs by the app in front. Allow runs only while a checked
// app is focused; Block stops a run when one is. The list comes from
// ListProcesses (apps with windows plus saved entries); saved entries whose app
// is not running stay visible, greyed, so they can still be unchecked.
//
// Checkbox rule: checking a row saves {exe, enabled: true}; unchecking removes
// the entry. A saved entry with enabled=false (e.g. from an imported preset)
// shows unchecked and can be re-checked or removed with its X.

type Mode = ClickerSettings["profile"]["processList"]["mode"];
const ALLOW = "allow" as Mode;
const BLOCK = "block" as Mode;

interface Row {
  exe: string;
  title: string;
  iconUrl: string;
  running: boolean;
  saved: boolean;
  checked: boolean;
}

function withEntries(s: ClickerSettings, fn: (e: ProcessEntry[]) => ProcessEntry[]): ClickerSettings {
  const pl = s.profile.processList;
  return {
    ...s,
    profile: { ...s.profile, processList: { ...pl, entries: fn(pl.entries ?? []) } },
  };
}

export default function ProcessListCard({ settings, update }: CardProps) {
  const pl = settings.profile.processList;
  const entries = pl.entries ?? [];
  const [procs, setProcs] = useState<ProcessInfo[]>([]);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState("");
  const [query, setQuery] = useState("");

  const refresh = useCallback(async () => {
    setLoading(true);
    try {
      setProcs((await ClickerService.ListProcesses()) ?? []);
      setErr("");
    } catch (e) {
      setErr(errText(e));
    } finally {
      setLoading(false);
    }
  }, []);

  // Load when the list is turned on (and on mount when it already is). No
  // background poll: ListProcesses walks the process table and reads icons.
  useEffect(() => {
    if (pl.enabled) void refresh();
  }, [pl.enabled, refresh]);

  const rows = useMemo(() => {
    const byExe = new Map<string, Row>();
    for (const p of procs) {
      const exe = p.exe.toLowerCase();
      if (!exe || byExe.has(exe)) continue;
      byExe.set(exe, {
        exe,
        title: p.title || exe,
        iconUrl: p.iconUrl,
        running: p.running,
        saved: false,
        checked: false,
      });
    }
    // Saved entries always show, even if ListProcesses did not report them.
    for (const e of entries) {
      const exe = e.exe.toLowerCase();
      const row = byExe.get(exe) ?? {
        exe,
        title: exe,
        iconUrl: "",
        running: false,
        saved: true,
        checked: false,
      };
      row.saved = true;
      row.checked = row.checked || e.enabled;
      byExe.set(exe, row);
    }
    const q = query.trim().toLowerCase();
    const list = Array.from(byExe.values()).filter(
      (r) => !q || r.exe.includes(q) || r.title.toLowerCase().includes(q),
    );
    const byName = (a: Row, b: Row) =>
      a.title.localeCompare(b.title, undefined, { sensitivity: "base" });
    return {
      checked: list.filter((r) => r.checked).sort(byName),
      rest: list.filter((r) => !r.checked).sort(byName),
      total: byExe.size,
    };
  }, [procs, entries, query]);

  const setEnabled = (on: boolean) =>
    update((s) => ({
      ...s,
      profile: { ...s.profile, processList: { ...s.profile.processList, enabled: on } },
    }));

  const setMode = (mode: Mode) =>
    update((s) => ({
      ...s,
      profile: { ...s.profile, processList: { ...s.profile.processList, mode } },
    }));

  const setChecked = (exe: string, on: boolean) =>
    update((s) =>
      withEntries(s, (es) => {
        const rest = es.filter((e) => e.exe.toLowerCase() !== exe);
        return on ? [...rest, { exe, enabled: true }] : rest;
      }),
    );

  const remove = (exe: string) =>
    update((s) => withEntries(s, (es) => es.filter((e) => e.exe.toLowerCase() !== exe)));

  const emptyAllow = pl.mode === ALLOW && !entries.some((e) => e.enabled);

  return (
    <section className="frost flex flex-col gap-4 border border-border p-4" aria-label="Process list">
      <CardHeader title="Process list" caption="Run or stop depending on which app is in front.">
        <Switch checked={pl.enabled} onCheckedChange={setEnabled} aria-label="Use the process list" />
      </CardHeader>

      <fieldset disabled={!pl.enabled} className={cn("flex min-w-0 flex-col gap-3", !pl.enabled && "opacity-50")}>
        {/* Segmented Allow/Block: local buttons until components/ui has a segmented control. */}
        <div className="flex flex-col gap-1">
          <div role="radiogroup" aria-label="List mode" className="flex w-fit border border-border">
            {[
              { v: ALLOW, label: "Allow" },
              { v: BLOCK, label: "Block" },
            ].map((m) => (
              <button
                key={m.v}
                type="button"
                role="radio"
                aria-checked={pl.mode === m.v}
                onClick={() => setMode(m.v)}
                className={cn(
                  "px-3 py-1 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
                  pl.mode === m.v
                    ? "bg-primary text-primary-foreground"
                    : "text-muted-foreground hover:bg-accent hover:text-accent-foreground",
                )}
              >
                {m.label}
              </button>
            ))}
          </div>
          <span className="text-xs text-muted-foreground">
            {pl.mode === ALLOW
              ? "Click only while a checked app is in front."
              : "Stop when a checked app comes to the front."}
          </span>
        </div>

        {pl.enabled && emptyAllow && (
          <div className="flex items-start gap-2 border border-amber-500/40 bg-amber-500/10 px-2.5 py-2 text-xs text-amber-200">
            <AlertTriangle className="mt-0.5 size-3.5 shrink-0" />
            <span>Allow mode is on with no apps checked, so clicking is blocked everywhere.</span>
          </div>
        )}

        <div className="flex items-center gap-2">
          <div className="relative min-w-0 flex-1">
            <Search className="pointer-events-none absolute left-2 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
            <input
              type="search"
              aria-label="Search apps"
              placeholder={`Search ${rows.total} apps`}
              value={query}
              onChange={(e) => setQuery(e.target.value)}
              className={cn(inputClass, "h-8 pl-7")}
            />
          </div>
          <Button
            size="icon"
            variant="ghost"
            className="h-8 w-8"
            aria-label="Refresh the app list"
            title="Refresh"
            disabled={loading}
            onClick={() => void refresh()}
          >
            <RefreshCw className={cn(loading && "animate-spin")} />
          </Button>
        </div>
        {err && <p className="text-xs text-destructive">{err}</p>}

        <div className="flex max-h-80 flex-col overflow-y-auto border border-border">
          {rows.checked.length + rows.rest.length === 0 ? (
            <p className="px-3 py-6 text-center text-xs text-muted-foreground">
              {loading
                ? "Refreshing…"
                : query.trim()
                  ? `No app matches "${query.trim()}".`
                  : "No apps found."}
            </p>
          ) : (
            <>
              {rows.checked.map((r) => (
                <ProcessRow key={r.exe} row={r} onCheck={setChecked} onRemove={remove} />
              ))}
              {rows.checked.length > 0 && rows.rest.length > 0 && (
                <div className="border-t border-border" role="separator" />
              )}
              {rows.rest.map((r) => (
                <ProcessRow key={r.exe} row={r} onCheck={setChecked} onRemove={remove} />
              ))}
            </>
          )}
        </div>
      </fieldset>
    </section>
  );
}

function ProcessRow({
  row,
  onCheck,
  onRemove,
}: {
  row: Row;
  onCheck: (exe: string, on: boolean) => void;
  onRemove: (exe: string) => void;
}) {
  return (
    <div
      className={cn(
        "flex items-center gap-2 px-2 py-1.5 hover:bg-accent/40",
        !row.running && "text-muted-foreground",
      )}
    >
      <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-2">
        <input
          type="checkbox"
          checked={row.checked}
          onChange={(e) => onCheck(row.exe, e.target.checked)}
          className="size-3.5 shrink-0 accent-primary"
        />
        {row.iconUrl ? (
          <img src={row.iconUrl} alt="" className={cn("size-5 shrink-0", !row.running && "opacity-50 grayscale")} />
        ) : (
          <AppWindow className="size-5 shrink-0 text-muted-foreground" />
        )}
        <span className="flex min-w-0 flex-col">
          <span className="truncate text-sm" title={row.title}>
            {row.title}
          </span>
          <span className="truncate font-mono text-[11px] text-muted-foreground">
            {row.exe}
            {!row.running && " · not running"}
          </span>
        </span>
      </label>
      {row.saved && !row.checked && (
        <Button
          size="icon"
          variant="ghost"
          className="h-6 w-6"
          aria-label={`Remove ${row.exe}`}
          title="Remove from the list"
          onClick={() => onRemove(row.exe)}
        >
          <X />
        </Button>
      )}
    </div>
  );
}
