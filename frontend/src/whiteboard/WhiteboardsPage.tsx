import * as React from "react";
import { FileInput, Plus, Presentation, TriangleAlert } from "lucide-react";
import { Events as WailsEvents } from "@wailsio/runtime";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { WhiteboardsService } from "@/lib/api";
import type { BoardSummary } from "@/lib/api";
import { cn } from "@/lib/utils";

// How people reach whiteboards from Toru's main window (docs/whiteboard.md,
// section 5): the Whiteboards page lists boards and makes, opens and imports
// them, and the Library's Send to whiteboard puts a screenshot on one. Both
// talk to Go's WhiteboardsService (whiteboards.go) through WhiteboardsApi, so
// a screenshot of them can run on Vite alone with boards in memory
// (whiteboards-preview.tsx).

type Board = Pick<BoardSummary, "id" | "name" | "blocks" | "error"> & { updatedAt: string };

type WhiteboardsApi = {
  list: () => Promise<Board[]>;
  /** Makes an empty board and opens it; "" names it "Untitled whiteboard". */
  create: (name: string) => Promise<Board>;
  open: (id: string) => Promise<void>;
  /** Asks for a .toruboard file and adds it; null when cancelled. */
  pickImport: () => Promise<Board | null>;
  /** Puts a Library screenshot on a board ("" makes a new one) and opens it. */
  sendScreenshot: (boardId: string, libraryId: string) => Promise<Board>;
  /** Calls back when boards are made, removed, renamed or changed. */
  onChange: (f: () => void) => () => void;
};

const board = (s: BoardSummary): Board => ({
  id: s.id,
  name: s.name,
  blocks: s.blocks,
  error: s.error,
  updatedAt: String(s.updatedAt ?? ""),
});

const goWhiteboards: WhiteboardsApi = {
  list: async () => ((await WhiteboardsService.List()) ?? []).map(board),
  create: async (name) => board(await WhiteboardsService.Create(name)),
  open: async (id) => WhiteboardsService.Open(id),
  pickImport: async () => {
    const [s, ok] = await WhiteboardsService.PickImport();
    return ok ? board(s) : null;
  },
  sendScreenshot: async (boardId, libraryId) => board(await WhiteboardsService.SendScreenshot(boardId, libraryId)),
  onChange: (f) => {
    const offs = [WailsEvents.On("boards:changed", f), WailsEvents.On("board:op", f)];
    return () => offs.forEach((off) => off());
  },
};

const WhiteboardsContext = React.createContext<WhiteboardsApi>(goWhiteboards);
const useWhiteboards = () => React.useContext(WhiteboardsContext);

const errText = (e: unknown) => String(e instanceof Error ? e.message : e).replace(/^Error:\s*/, "");

function when(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime()) || d.getFullYear() < 2000) return "";
  return d.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

/** The boards, kept current while mounted. A burst of changes (an agent
 * filling a board) is listed again once, a moment after it settles. */
function useBoards() {
  const api = useWhiteboards();
  const [boards, setBoards] = React.useState<Board[] | null>(null);
  const [error, setError] = React.useState("");
  const refresh = React.useCallback(() => {
    api
      .list()
      .then((list) => {
        setBoards(list);
        setError("");
      })
      .catch((e: unknown) => {
        setBoards((b) => b ?? []);
        setError(errText(e));
      });
  }, [api]);
  React.useEffect(() => {
    refresh();
    let t = 0;
    const soon = () => {
      window.clearTimeout(t);
      t = window.setTimeout(refresh, 300);
    };
    const off = api.onChange(soon);
    window.addEventListener("focus", refresh);
    return () => {
      off();
      window.clearTimeout(t);
      window.removeEventListener("focus", refresh);
    };
  }, [api, refresh]);
  return { boards, error, refresh };
}

/** The main window's Whiteboards page. */
function WhiteboardsPage() {
  const api = useWhiteboards();
  const { boards, error } = useBoards();
  const [note, setNote] = React.useState("");
  const [busy, setBusy] = React.useState(false);
  React.useEffect(() => {
    if (!note) return;
    const t = window.setTimeout(() => setNote(""), 6000);
    return () => window.clearTimeout(t);
  }, [note]);
  const run = (f: () => Promise<unknown>) => {
    setBusy(true);
    f()
      .catch((e: unknown) => setNote(errText(e)))
      .finally(() => setBusy(false));
  };

  return (
    <div className="flex min-h-full flex-col gap-6" data-slot="whiteboards-page">
      <div className="flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="text-xl font-semibold tracking-tight">Whiteboards</h1>
          <p className="text-sm text-muted-foreground">
            Boards you and your agents build together. Open one to carry on.
          </p>
        </div>
        <div className="flex gap-2">
          <Button variant="outline" disabled={busy} onClick={() => run(api.pickImport)} title="Add a board from a .toruboard file">
            <FileInput /> Import…
          </Button>
          <Button disabled={busy} onClick={() => run(() => api.create(""))}>
            <Plus /> New whiteboard
          </Button>
        </div>
      </div>

      {note || error ? (
        <div className="frost border border-destructive/50 px-3 py-2 text-sm text-destructive" role="alert">
          {note || `Couldn't list the boards: ${error}`}
        </div>
      ) : null}

      {boards === null ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : boards.length === 0 ? (
        <div className="flex flex-1 flex-col items-center justify-center gap-2 py-16 text-center">
          <Presentation className="size-10 text-muted-foreground" />
          <p className="font-medium">No whiteboards yet</p>
          <p className="max-w-md text-sm text-muted-foreground">
            Start one with New whiteboard, import a .toruboard file, or send a screenshot from the Library.
            Agents can make boards too.
          </p>
        </div>
      ) : (
        <ul className="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-4" aria-label="Whiteboards">
          {boards.map((b) => (
            <li key={b.id} className="flex">
              <BoardCard board={b} onOpen={() => run(() => api.open(b.id))} />
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}

function BoardCard({ board: b, onOpen }: { board: Board; onOpen: () => void }) {
  const changed = when(b.updatedAt);
  return (
    <button
      type="button"
      onClick={onOpen}
      disabled={!!b.error}
      title={b.error ? `Can't open: ${b.error}` : `Open ${b.name}`}
      className={cn(
        "frost group flex w-full flex-col overflow-hidden border border-transparent text-left",
        "transition-[border-color,box-shadow,transform] duration-150",
        b.error
          ? "cursor-not-allowed opacity-80"
          : "hover:-translate-y-0.5 hover:border-primary/70 hover:shadow-[0_0_0_1px_var(--color-ring),0_12px_28px_oklch(0_0_0/0.45)]",
      )}
    >
      <span className="relative flex aspect-video w-full items-center justify-center bg-black/40">
        <Presentation className="size-9 text-muted-foreground transition-transform duration-150 group-hover:scale-110" />
        {b.error ? (
          <span className="absolute left-1.5 top-1.5 flex items-center gap-1 bg-black/70 px-1.5 py-0.5 text-[10px] text-amber-300">
            <TriangleAlert className="size-3" /> Can't be read
          </span>
        ) : null}
      </span>
      <span className="flex flex-col px-2 py-1.5">
        <span className="truncate text-xs font-medium">{b.name}</span>
        <span className="truncate text-[10px] text-muted-foreground">
          {b.error
            ? b.error
            : [`${b.blocks} ${b.blocks === 1 ? "block" : "blocks"}`, changed].filter(Boolean).join(" · ")}
        </span>
      </span>
    </button>
  );
}

type Shot = { id: string; label: string };

/** Send to whiteboard: pick a board (or a new one) for a Library screenshot.
 * The boards are listed afresh each time it opens. */
function SendToWhiteboard({ shot, onClose }: { shot: Shot | null; onClose: () => void }) {
  return shot ? <SendChooser key={shot.id} shot={shot} onClose={onClose} /> : null;
}

function SendChooser({ shot, onClose }: { shot: Shot; onClose: () => void }) {
  const api = useWhiteboards();
  const { boards } = useBoards();
  const [choice, setChoice] = React.useState<string | null>(null);
  const [busy, setBusy] = React.useState(false);
  const [error, setError] = React.useState("");
  const usable = (boards ?? []).filter((b) => !b.error);
  // The most recently changed board is chosen until the person picks.
  const picked = choice ?? usable[0]?.id ?? "";
  const send = () => {
    setBusy(true);
    setError("");
    api
      .sendScreenshot(picked, shot.id)
      .then(onClose)
      .catch((e: unknown) => setError(errText(e)))
      .finally(() => setBusy(false));
  };
  const option = (id: string, label: React.ReactNode, sub: string) => (
    <label
      key={id || "new"}
      className={cn(
        "flex cursor-pointer items-center gap-2 border px-2 py-1.5 text-sm",
        picked === id ? "border-primary bg-primary/10" : "border-transparent hover:bg-accent",
      )}
    >
      <input
        type="radio"
        name="send-to-whiteboard"
        className="accent-[var(--color-primary)]"
        checked={picked === id}
        onChange={() => setChoice(id)}
      />
      <span className="min-w-0 flex-1">
        <span className="block truncate">{label}</span>
        {sub ? <span className="block truncate text-[11px] text-muted-foreground">{sub}</span> : null}
      </span>
    </label>
  );

  return (
    <Dialog open onClose={busy ? undefined : onClose} dismissable={!busy} className="w-[26rem]">
      <div data-slot="send-to-whiteboard">
        <h2 className="text-base font-semibold">Send to whiteboard</h2>
        <p className="mt-1 text-sm break-words text-muted-foreground">
          {shot.label} goes on the board as a picture, below what's there.
        </p>
        <div className="mt-3 flex max-h-64 flex-col gap-0.5 overflow-auto" role="radiogroup" aria-label="Board">
          {boards === null ? (
            <p className="text-sm text-muted-foreground">Loading…</p>
          ) : (
            <>
              {usable.map((b) =>
                option(b.id, b.name, [`${b.blocks} ${b.blocks === 1 ? "block" : "blocks"}`, when(b.updatedAt)].filter(Boolean).join(" · ")),
              )}
              {option(
                "",
                <span className="flex items-center gap-1.5">
                  <Plus className="size-3.5" /> New whiteboard
                </span>,
                "",
              )}
            </>
          )}
        </div>
        {error ? (
          <p className="mt-2 text-sm text-destructive" role="alert">
            {error}
          </p>
        ) : null}
        <div className="mt-4 flex justify-end gap-2">
          <Button size="sm" variant="ghost" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button size="sm" onClick={send} disabled={busy || boards === null}>
            <Presentation /> {busy ? "Sending…" : "Send"}
          </Button>
        </div>
      </div>
    </Dialog>
  );
}

export { SendToWhiteboard, WhiteboardsContext, WhiteboardsPage, goWhiteboards, type Board as WhiteboardSummary, type WhiteboardsApi };
