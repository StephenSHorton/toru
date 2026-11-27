import { useCallback, useEffect, useState } from "react";
import { Button } from "@ui-sdk/react/button";
import { Input } from "@ui-sdk/react/input";
import { Popover, PopoverContent, PopoverDescription, PopoverTitle, PopoverTrigger } from "@ui-sdk/react/popover";
import { CopyIcon, RadioTowerIcon, UsersIcon } from "lucide-react";

// The board window's Share button (docs/whiteboard.md, section 2, "Shared
// boards"): shares the board with the people in your Toru room, the same
// room Call and LAN use. Everyone in the room can join it from their own
// Toru, see every change live and make changes, and so can each person's
// agents through their own Toru. Go's ShareService (internal/boardshare)
// does the work; nothing here opens a port or a link.

/** One board's sharing, as Go's ShareService.Status gives it. */
export type ShareStatus = {
  inRoom: boolean;
  code: string;
  /** This Toru shares the board; viewers are who has it open. */
  sharing: boolean;
  viewers: string[];
  /** Set on a copy of someone else's board; live while it follows theirs. */
  hostName: string;
  live: boolean;
};

/** How the button reaches Go: ShareService and StreamService, or a fake in checks. */
export type ShareApi = {
  status: (id: string) => Promise<ShareStatus>;
  share: (id: string) => Promise<ShareStatus>;
  unshare: (id: string) => Promise<ShareStatus>;
  leave: (id: string) => Promise<ShareStatus>;
  joinRoom: (code: string) => Promise<void>;
  newRoom: () => Promise<void>;
  /** Calls back when sharing or the room changes. */
  onChange: (f: () => void) => () => void;
};

const shareService = () => import("../../bindings/github.com/StephenSHorton/toru/internal/boardshare/shareservice.js");
const streamService = () => import("../../bindings/github.com/StephenSHorton/toru/internal/stream/streamservice.js");

const plain = (s: ShareStatus): ShareStatus => ({
  inRoom: s.inRoom,
  code: s.code,
  sharing: s.sharing,
  viewers: s.viewers ?? [],
  hostName: s.hostName,
  live: s.live,
});

export const goShare: ShareApi = {
  status: async (id) => plain(await (await shareService()).Status(id)),
  share: async (id) => plain(await (await shareService()).Share(id)),
  unshare: async (id) => plain(await (await shareService()).Unshare(id)),
  leave: async (id) => plain(await (await shareService()).Leave(id)),
  joinRoom: async (code) => void (await (await streamService()).JoinRoom(code)),
  newRoom: async () => void (await (await streamService()).CreateRoom()),
  onChange: (f) => {
    let offs: (() => void)[] = [];
    let stopped = false;
    void import("@wailsio/runtime")
      .then(({ Events }) => {
        if (stopped) return;
        offs = ["board:share", "board:shared", "stream:state"].map((name) => Events.On(name, f));
      })
      .catch(() => {});
    return () => {
      stopped = true;
      offs.forEach((off) => off());
    };
  },
};

/** Go's shared board demo server (board_share_demo_test.go): it shows the
 * board's sharing; sharing and rooms are Toru's, not the demo's. */
export function httpShare(base: string): ShareApi {
  const status = async (id: string) => {
    const res = await fetch(`${base}/status`, { method: "POST", body: JSON.stringify({ board: id }) });
    if (!res.ok) throw new Error(await res.text());
    return plain((await res.json()) as ShareStatus);
  };
  const no = async (): Promise<never> => {
    throw new Error("The demo's sharing is set up by Go");
  };
  return {
    status,
    share: no,
    unshare: no,
    leave: no,
    joinRoom: no,
    newRoom: no,
    onChange: (f) => {
      const t = setInterval(f, 2000);
      return () => clearInterval(t);
    },
  };
}

const errText = (e: unknown) => String(e instanceof Error ? e.message : e).replace(/^Error:\s*/, "");

const listNames = (names: string[]) =>
  names.length <= 1 ? names.join("") : `${names.slice(0, -1).join(", ")} and ${names[names.length - 1]}`;

/** What the toolbar button says. */
function triggerText(s: ShareStatus | null) {
  if (!s) return "Share";
  if (s.sharing) return s.viewers.length ? `Shared · ${s.viewers.length + 1}` : "Shared";
  if (s.hostName && s.live) return `Live from ${s.hostName}`;
  return "Share";
}

export function SharePopover({ boardId, api = goShare }: { boardId: string; api?: ShareApi }) {
  const [status, setStatus] = useState<ShareStatus | null>(null);
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");

  const refresh = useCallback(() => {
    api
      .status(boardId)
      .then(setStatus)
      .catch(() => {});
  }, [api, boardId]);
  useEffect(() => {
    refresh();
    return api.onChange(refresh);
  }, [api, refresh]);

  const run = (f: () => Promise<unknown>) => {
    setBusy(true);
    setNote("");
    f()
      .then(refresh)
      .catch((e: unknown) => setNote(errText(e)))
      .finally(() => setBusy(false));
  };
  const copyCode = (c: string) =>
    void navigator.clipboard
      ?.writeText(c)
      .then(() => setNote("Room code copied"))
      .catch(() => {});

  const s = status;
  return (
    <Popover>
      <PopoverTrigger data-share={s?.sharing ? "sharing" : s?.live ? "live" : "off"}>
        {s?.sharing || s?.live ? <RadioTowerIcon aria-hidden="true" /> : <UsersIcon aria-hidden="true" />}
        {triggerText(s)}
      </PopoverTrigger>
      <PopoverContent className="frost" data-slot="share-popover" style={{ maxWidth: "22rem" }}>
        {!s ? (
          <PopoverDescription>Loading…</PopoverDescription>
        ) : s.hostName ? (
          <>
            <PopoverTitle>{s.live ? `${s.hostName}'s board, live` : `A copy of ${s.hostName}'s board`}</PopoverTitle>
            <PopoverDescription>
              {s.live
                ? `Changes you and your agents make go to ${s.hostName}'s Toru, and theirs show here as they happen.`
                : `It no longer follows ${s.hostName}'s board: changes here stay on this Toru.`}
            </PopoverDescription>
            {s.live && (
              <div className="flex justify-end gap-2">
                <Button data-share-action="leave" disabled={busy} onClick={() => run(() => api.leave(boardId))}>
                  Stop following
                </Button>
              </div>
            )}
          </>
        ) : !s.inRoom ? (
          <>
            <PopoverTitle>Share this board</PopoverTitle>
            <PopoverDescription>
              Boards are shared with the people in your Toru room, the same room Call uses. Join the room your friends are in, or start one and send them its code.
            </PopoverDescription>
            <form
              className="flex gap-2"
              onSubmit={(e) => {
                e.preventDefault();
                run(() => api.joinRoom(code));
              }}
            >
              <Input aria-label="Room code" placeholder="ABCD-EFGH-JKMN-PQRS" value={code} onChange={(e) => setCode(e.currentTarget.value)} />
              <Button type="submit" disabled={busy || !code.trim()} data-share-action="join-room">
                Join room
              </Button>
            </form>
            <div className="flex justify-end">
              <Button disabled={busy} onClick={() => run(api.newRoom)} data-share-action="new-room">
                Start a new room
              </Button>
            </div>
          </>
        ) : s.sharing ? (
          <>
            <PopoverTitle>Shared in your room</PopoverTitle>
            <PopoverDescription>
              {s.viewers.length
                ? `${listNames(s.viewers)} ${s.viewers.length === 1 ? "has" : "have"} it open. Everyone in the room can join it, edit it, and let their agents build on it.`
                : "Everyone in the room can join it from their Whiteboards page, edit it, and let their agents build on it. Nobody has it open yet."}
            </PopoverDescription>
            <RoomCode code={s.code} onCopy={copyCode} />
            <div className="flex justify-end gap-2">
              <Button data-share-action="unshare" disabled={busy} onClick={() => run(() => api.unshare(boardId))}>
                Stop sharing
              </Button>
            </div>
          </>
        ) : (
          <>
            <PopoverTitle>Share this board</PopoverTitle>
            <PopoverDescription>
              Everyone in your room will be able to join it from their own Toru, see changes live, edit it, and let their agents build on it. Your Toru keeps the board; theirs keep a copy.
            </PopoverDescription>
            <RoomCode code={s.code} onCopy={copyCode} />
            <div className="flex justify-end gap-2">
              <Button data-variant="solid" data-share-action="share" disabled={busy} onClick={() => run(() => api.share(boardId))}>
                Share in room
              </Button>
            </div>
          </>
        )}
        {note && (
          <p role="status" data-slot="share-note" className="text-sm">
            {note}
          </p>
        )}
      </PopoverContent>
    </Popover>
  );
}

function RoomCode({ code, onCopy }: { code: string; onCopy: (code: string) => void }) {
  return (
    <div className="flex items-center justify-between gap-2" data-slot="share-room">
      <span className="text-sm">
        Room <span className="font-mono">{code}</span>
      </span>
      <Button aria-label="Copy room code" onClick={() => onCopy(code)}>
        <CopyIcon aria-hidden="true" />
      </Button>
    </div>
  );
}
