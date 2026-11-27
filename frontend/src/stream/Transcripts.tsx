import { useCallback, useEffect, useRef, useState } from "react";
import { Events as WailsEvents } from "@wailsio/runtime";
import {
  Captions,
  CaptionsOff,
  Check,
  ChevronDown,
  ChevronRight,
  Copy,
  Download,
  FileText,
  Loader2,
  Pencil,
  ScrollText,
  Trash2,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Card, CardHead } from "@/clicker/cardParts";
import { StreamService, TranscriptService } from "@/lib/api";
import type { StreamState, TranscriptEntry, TranscriptEntryLine, TranscriptLine, TranscriptMeta } from "@/lib/api";
import { errText } from "@/transfer/useTransfer";
import { cn } from "@/lib/utils";

// Call transcripts on the Game Room page. TranscribeBar sits in the Call
// card: the Transcribe switch, the "Transcribing" notice everyone in the
// call sees (with who turned it on), this PC's speech model, and the live
// captions. TranscriptsCard lists every transcript saved on this PC, to
// open, copy, export, rename and delete. Go side: internal/stream
// (transcribe.go) and internal/transcript.

function payload<T>(d: unknown): T | null {
  const v = Array.isArray(d) ? d[0] : d;
  return v && typeof v === "object" ? (v as T) : null;
}

function clockTime(ms: number): string {
  return new Date(ms).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}

function mb(bytes: number): string {
  return `${Math.round(bytes / 1_000_000)} MB`;
}

function duration(sec: number): string {
  const h = Math.floor(sec / 3600);
  const m = Math.floor(sec / 60) % 60;
  const s = sec % 60;
  if (h > 0) return `${h}:${String(m).padStart(2, "0")}:${String(s).padStart(2, "0")}`;
  return `${m}:${String(s).padStart(2, "0")}`;
}

// useLiveEntries follows the call's transcript: what's saved so far, then
// each new entry as it arrives.
function useLiveEntries(id: string): TranscriptEntryLine[] {
  const [entries, setEntries] = useState<TranscriptEntryLine[]>([]);
  useEffect(() => {
    setEntries([]);
    if (!id) return;
    let alive = true;
    void TranscriptService.Open(id)
      .then((v) => {
        if (!alive) return;
        setEntries((cur) => {
          const seen = new Set(cur.map((e) => e.n));
          return [...v.lines.filter((l) => !seen.has(l.n)), ...cur].sort((a, b) => a.at - b.at);
        });
      })
      .catch(() => {});
    const off = WailsEvents.On("transcript:entry", (ev) => {
      const d = payload<TranscriptEntry>(ev.data);
      if (!d || d.transcript !== id || !d.entry) return;
      const e = d.entry;
      setEntries((cur) => (cur.some((x) => x.n === e.n) ? cur : [...cur, e].sort((a, b) => a.at - b.at)));
    });
    return () => {
      alive = false;
      off();
    };
  }, [id]);
  return entries;
}

export function TranscribeBar({ state, onState }: { state: StreamState; onState: (s: StreamState) => void }) {
  const tx = state.call.transcribe;
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const entries = useLiveEntries(state.call.inCall ? tx.transcript : "");
  const scroller = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const el = scroller.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [entries.length]);

  const run = async (fn: () => Promise<StreamState>) => {
    setBusy(true);
    setError("");
    try {
      onState(await fn());
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  if (!state.call.inCall) return null;
  const engine = tx.engine;
  const shown = entries.slice(-40);

  return (
    <div className="flex flex-col gap-2" data-testid="transcribe">
      {tx.on ? (
        <div
          role="status"
          data-testid="transcribing-indicator"
          className="flex flex-wrap items-center gap-2 border border-red-500/50 bg-red-500/10 px-3 py-2 text-sm"
        >
          <span className="relative flex size-2.5">
            <span className="absolute inline-flex size-full animate-ping rounded-full bg-red-500 opacity-60" />
            <span className="relative inline-flex size-2.5 rounded-full bg-red-500" />
          </span>
          <span className="font-medium">Transcribing</span>
          <span className="text-muted-foreground">
            {tx.by} turned it on at {clockTime(tx.since)}. What everyone in the call says is written down and saved.
          </span>
        </div>
      ) : null}

      {tx.on && engine.phase !== "ready" ? (
        <SpeechModelNote
          phase={engine.phase}
          done={engine.done}
          total={engine.total}
          error={engine.error}
          busy={busy}
          onDownload={() => void run(() => StreamService.DownloadSpeechModel())}
        />
      ) : null}

      {tx.on || entries.length > 0 ? (
        <div
          ref={scroller}
          data-testid="live-captions"
          className="flex max-h-56 min-h-16 flex-col gap-1 overflow-y-auto border border-border bg-background/40 px-3 py-2 text-sm"
        >
          {shown.length === 0 ? (
            <span className="text-xs text-muted-foreground">Captions show here as people talk.</span>
          ) : (
            shown.map((e) => <CaptionLine key={e.n} entry={e} />)
          )}
        </div>
      ) : null}

      {error ? <p className="text-xs text-destructive">{error}</p> : null}
    </div>
  );
}

export function TranscribeButton({ state, onState, onError }: {
  state: StreamState;
  onState: (s: StreamState) => void;
  onError: (e: string) => void;
}) {
  const on = state.call.transcribe.on;
  const [busy, setBusy] = useState(false);
  const toggle = async () => {
    setBusy(true);
    try {
      onState(await StreamService.SetTranscribe(!on));
    } catch (e) {
      onError(errText(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Button
      variant={on ? "destructive" : "outline"}
      disabled={busy}
      onClick={() => void toggle()}
      data-testid="transcribe-toggle"
      title={
        on
          ? "Stop transcribing the call for everyone"
          : "Transcribe the call: everyone in it is told, and each person's speech is turned into text on their own PC"
      }
    >
      {on ? <CaptionsOff /> : <Captions />}
      {on ? "Stop transcribing" : "Transcribe"}
    </Button>
  );
}

function SpeechModelNote({
  phase,
  done,
  total,
  error,
  busy,
  onDownload,
}: {
  phase: string;
  done: number;
  total: number;
  error: string;
  busy: boolean;
  onDownload: () => void;
}) {
  if (phase === "unsupported") {
    return <p className="text-xs text-muted-foreground">Your speech can't be transcribed on this PC. You still get everyone else's lines.</p>;
  }
  if (phase === "downloading") {
    const pct = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0;
    return (
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Loader2 className="size-3.5 animate-spin" />
        Getting the speech model ({mb(done)} of {mb(total)}): your own speech is transcribed once it's here.
        <span className="ml-auto tabular-nums">{pct}%</span>
      </div>
    );
  }
  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <span className="flex-1">
        {phase === "error" ? `The speech model didn't download: ${error}. ` : null}
        Your own speech isn't transcribed yet: this PC needs the speech model ({mb(total)}, once). It runs on this PC only;
        your voice never leaves it.
      </span>
      <Button size="sm" variant="secondary" disabled={busy} onClick={onDownload}>
        <Download />
        {phase === "error" ? "Try again" : "Download"}
      </Button>
    </div>
  );
}

function CaptionLine({ entry: e }: { entry: TranscriptEntryLine }) {
  if (e.kind !== "line") {
    return (
      <span className="text-xs italic text-muted-foreground">
        {clockTime(e.at)} · {e.speaker} {e.kind === "started" ? "started" : "stopped"} transcription
      </span>
    );
  }
  return (
    <span>
      <span className="font-medium">{e.speaker}</span>
      <span className="text-muted-foreground"> · {clockTime(e.at)}</span>
      <span className="text-foreground">: {e.text}</span>
    </span>
  );
}

// ---------------------------------------------------------------------------
// Transcripts list
// ---------------------------------------------------------------------------

export function TranscriptsCard({ state }: { state: StreamState }) {
  const [list, setList] = useState<TranscriptMeta[] | null>(null);
  const [open, setOpen] = useState("");
  const [error, setError] = useState("");

  const refresh = useCallback(() => {
    void TranscriptService.List()
      .then((l) => setList(l ?? []))
      .catch((e: unknown) => setError(errText(e)));
  }, []);

  // A call starting or ending, or a new line, changes the list.
  const active = state.call.inCall ? state.call.transcribe.transcript : "";
  useEffect(refresh, [refresh, active, state.call.inCall]);
  useEffect(() => {
    let t: number | undefined;
    const off = WailsEvents.On("transcript:entry", () => {
      window.clearTimeout(t);
      t = window.setTimeout(refresh, 800);
    });
    return () => {
      off();
      window.clearTimeout(t);
    };
  }, [refresh]);

  return (
    <Card label="Transcripts" wide>
      <CardHead
        icon={ScrollText}
        title="Transcripts"
        caption="Calls transcribed on this PC. Agents connected to Toru can list, read, search and follow them."
      />
      {error ? <p className="text-xs text-destructive">{error}</p> : null}
      {list === null ? null : list.length === 0 ? (
        <p className="text-xs text-muted-foreground" data-testid="transcripts-empty">
          No transcripts yet. Press Transcribe in a call and everyone's speech is saved here when the call ends.
        </p>
      ) : (
        <div className="flex flex-col divide-y divide-border border border-border" data-testid="transcripts-list">
          {list.map((t) => (
            <TranscriptRow
              key={t.id}
              meta={t}
              open={open === t.id}
              onToggle={() => setOpen(open === t.id ? "" : t.id)}
              onChanged={refresh}
              onError={setError}
            />
          ))}
        </div>
      )}
    </Card>
  );
}

function TranscriptRow({
  meta: t,
  open,
  onToggle,
  onChanged,
  onError,
}: {
  meta: TranscriptMeta;
  open: boolean;
  onToggle: () => void;
  onChanged: () => void;
  onError: (e: string) => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [title, setTitle] = useState(t.title);
  const [confirmDelete, setConfirmDelete] = useState(false);
  const [lines, setLines] = useState<TranscriptLine[] | null>(null);

  useEffect(() => {
    if (!open) return;
    void TranscriptService.Open(t.id)
      .then((v) => setLines(v.lines ?? []))
      .catch((e: unknown) => onError(errText(e)));
  }, [open, t.id, t.lines, onError]);

  const act = (fn: () => Promise<unknown>) => void fn().then(onChanged).catch((e: unknown) => onError(errText(e)));

  return (
    <div className="flex flex-col gap-2 px-3 py-2" data-testid="transcript-row">
      <div className="flex items-center gap-2">
        <button
          type="button"
          onClick={onToggle}
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
          aria-expanded={open}
          title={open ? "Close" : "Open"}
        >
          {open ? <ChevronDown className="size-4 shrink-0" /> : <ChevronRight className="size-4 shrink-0" />}
          <FileText className="size-4 shrink-0 text-muted-foreground" />
          {renaming ? null : (
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm font-medium">
                {t.title}
                {t.live ? <span className="ml-2 text-xs font-normal text-red-500">● in progress</span> : null}
              </span>
              <span className="block truncate text-xs text-muted-foreground">
                {new Date(t.startedAt).toLocaleString([], { dateStyle: "medium", timeStyle: "short" })} ·{" "}
                {t.people.length > 0 ? t.people.join(", ") : "no one yet"} · {t.lines} {t.lines === 1 ? "line" : "lines"} ·{" "}
                {duration(t.durationSeconds)}
              </span>
            </span>
          )}
        </button>
        {renaming ? (
          <form
            className="flex flex-1 items-center gap-1"
            onSubmit={(e) => {
              e.preventDefault();
              setRenaming(false);
              act(() => TranscriptService.Rename(t.id, title));
            }}
          >
            <input
              autoFocus
              value={title}
              onChange={(e) => setTitle(e.target.value)}
              maxLength={120}
              aria-label="Transcript title"
              className="h-8 min-w-0 flex-1 border border-border bg-background px-2 text-sm"
            />
            <Button size="icon" variant="ghost" type="submit" aria-label="Save title">
              <Check />
            </Button>
            <Button size="icon" variant="ghost" type="button" aria-label="Cancel" onClick={() => setRenaming(false)}>
              <X />
            </Button>
          </form>
        ) : (
          <div className="flex shrink-0 items-center gap-1">
            <Button
              size="icon"
              variant="ghost"
              aria-label="Rename"
              title="Rename"
              onClick={() => {
                setTitle(t.title);
                setRenaming(true);
              }}
            >
              <Pencil />
            </Button>
            <Button size="icon" variant="ghost" aria-label="Copy as text" title="Copy as text" onClick={() => act(() => TranscriptService.Copy(t.id))}>
              <Copy />
            </Button>
            <Button size="sm" variant="ghost" title="Export as Markdown" onClick={() => act(() => TranscriptService.Export(t.id, "md"))}>
              .md
            </Button>
            <Button size="sm" variant="ghost" title="Export as text" onClick={() => act(() => TranscriptService.Export(t.id, "txt"))}>
              .txt
            </Button>
            {confirmDelete ? (
              <>
                <Button size="sm" variant="destructive" onClick={() => act(() => TranscriptService.Delete(t.id))}>
                  Delete
                </Button>
                <Button size="sm" variant="ghost" onClick={() => setConfirmDelete(false)}>
                  Keep
                </Button>
              </>
            ) : (
              <Button
                size="icon"
                variant="ghost"
                aria-label="Delete"
                title={t.live ? "Hang up to delete this one" : "Delete"}
                disabled={t.live}
                onClick={() => setConfirmDelete(true)}
              >
                <Trash2 />
              </Button>
            )}
          </div>
        )}
      </div>
      {open ? (
        <div className={cn("flex max-h-80 flex-col gap-1 overflow-y-auto border border-border bg-background/40 px-3 py-2 text-sm select-text")}>
          {lines === null ? (
            <Loader2 className="size-4 animate-spin" />
          ) : lines.length === 0 ? (
            <span className="text-xs text-muted-foreground">Nothing said yet.</span>
          ) : (
            lines.map((l) =>
              l.kind === "line" ? (
                <span key={l.n}>
                  <span className="tabular-nums text-muted-foreground">[{l.time}] </span>
                  <span className="font-medium">{l.speaker}</span>: {l.text}
                </span>
              ) : (
                <span key={l.n} className="text-xs italic text-muted-foreground">
                  [{l.time}] {l.speaker} {l.kind === "started" ? "started" : "stopped"} transcription
                </span>
              ),
            )
          )}
        </div>
      ) : null}
    </div>
  );
}
