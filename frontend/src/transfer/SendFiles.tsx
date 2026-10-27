import { useCallback, useEffect, useRef, useState } from "react";
import {
  Check,
  Copy,
  File as FileIcon,
  FolderOpen,
  QrCode,
  Send,
  Square,
  X,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { ExportService, TransferService } from "@/lib/api";
import { cn } from "@/lib/utils";
import { humanBytes, percent } from "./format";
import {
  errText,
  isIdle,
  onLocalSendError,
  useReceiveState,
  useSendState,
} from "./useTransfer";

// Send Files — one page, two cards: send a file or folder and read out the
// short code; or type a code and receive. The transport is magic-wormhole (Go
// side, internal/transfer), so the other end can be another Toru or the
// `wormhole` CLI. State comes from transfer:send / transfer:receive events via
// useTransfer; this file is only rendering + button handlers.

export default function SendFiles() {
  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Send Files</h1>
        <p className="text-sm text-muted-foreground">
          Send a file to anyone with a short code. Works with the wormhole CLI too.
        </p>
      </div>

      <div className="grid gap-4 lg:grid-cols-2">
        <SendCard />
        <ReceiveCard />
      </div>
    </div>
  );
}

// PulseDot is the same "live" marker the Share card uses.
function PulseDot({ className }: { className?: string }) {
  return (
    <span className={cn("relative flex size-3 shrink-0", className)} aria-hidden="true">
      <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-sky-400 opacity-60" />
      <span className="relative inline-flex size-3 rounded-full bg-sky-400" />
    </span>
  );
}

function CardTitle({ children, caption }: { children: React.ReactNode; caption: string }) {
  return (
    <div className="flex flex-col">
      <span className="text-sm font-medium">{children}</span>
      <span className="text-xs text-muted-foreground">{caption}</span>
    </div>
  );
}

// ---------------------------------------------------------------------------
// Send
// ---------------------------------------------------------------------------

function SendCard() {
  const [send, dismiss] = useSendState();
  const [localErr, setLocalErr] = useState("");
  const [picking, setPicking] = useState(false);
  const [stopping, setStopping] = useState(false);
  const [copied, setCopied] = useState(false);
  const copyTimer = useRef<number | null>(null);
  const [showQR, setShowQR] = useState(false);
  const [qr, setQr] = useState<{ code: string; png: string } | null>(null);
  const [qrErr, setQrErr] = useState("");

  const idle = isIdle(send);
  const code = send.code;

  useEffect(() => {
    return () => {
      if (copyTimer.current !== null) window.clearTimeout(copyTimer.current);
    };
  }, []);

  // A Library-card send that was rejected before this page mounted (the file
  // is gone) lands here instead of vanishing.
  useEffect(() => onLocalSendError((msg) => setLocalErr(msg)), []);

  // A new send issues a new code: drop the cached QR and fold the toggle.
  useEffect(() => {
    setShowQR(false);
    setQr(null);
    setQrErr("");
  }, [code]);

  const start = useCallback(async (path: string) => {
    setLocalErr("");
    try {
      await TransferService.StartSend(path);
    } catch (e) {
      setLocalErr(errText(e));
    }
  }, []);

  const pick = useCallback(
    async (kind: "file" | "folder") => {
      if (picking) return;
      setPicking(true);
      setLocalErr("");
      try {
        const path =
          kind === "file"
            ? await TransferService.PickSendFile()
            : await TransferService.PickSendFolder();
        if (path) await start(path);
      } catch (e) {
        setLocalErr(errText(e));
      } finally {
        setPicking(false);
      }
    },
    [picking, start],
  );

  const stop = useCallback(async () => {
    if (stopping) return;
    setStopping(true);
    try {
      await TransferService.StopSend();
    } catch (e) {
      setLocalErr(errText(e));
    } finally {
      setStopping(false);
    }
  }, [stopping]);

  const copy = useCallback(async () => {
    if (!code) return;
    try {
      await ExportService.CopyTextAs(code, "Send code");
      setCopied(true);
      if (copyTimer.current !== null) window.clearTimeout(copyTimer.current);
      copyTimer.current = window.setTimeout(() => setCopied(false), 1400);
    } catch {
      setCopied(false);
    }
  }, [code]);

  const toggleQR = useCallback(async () => {
    if (!code) return;
    if (showQR) {
      setShowQR(false);
      return;
    }
    setShowQR(true);
    if (qr?.code === code) return;
    setQrErr("");
    try {
      const png = await TransferService.CodeQR(code);
      setQr({ code, png });
    } catch (e) {
      setQrErr(errText(e));
    }
  }, [code, showQR, qr]);

  const sendAnother = useCallback(() => {
    setLocalErr("");
    dismiss();
  }, [dismiss]);

  const pct = percent(send.sent, send.total);
  const kindLabel = send.kind === "folder" ? "Folder" : "File";

  let body: React.ReactNode;
  if (idle) {
    body = (
      <div
        data-file-drop-target
        className={cn(
          "flex flex-col items-center justify-center gap-3 border border-dashed border-border px-4 py-10 text-center transition-colors",
          "[&.file-drop-target-active]:border-primary [&.file-drop-target-active]:bg-primary/10",
        )}
      >
        <Send className="size-8 text-muted-foreground" />
        <div>
          <p className="text-sm font-medium">Drop a file or folder here</p>
          <p className="mt-1 text-xs text-muted-foreground">or pick one</p>
        </div>
        <div className="flex flex-wrap justify-center gap-2">
          <Button
            size="sm"
            variant="secondary"
            disabled={picking}
            onClick={() => void pick("file")}
          >
            <FileIcon /> Choose file…
          </Button>
          <Button
            size="sm"
            variant="secondary"
            disabled={picking}
            onClick={() => void pick("folder")}
          >
            <FolderOpen /> Choose folder…
          </Button>
        </div>
      </div>
    );
  } else if (send.active) {
    const status =
      send.phase === "preparing"
        ? send.message || "Preparing…"
        : send.phase === "waiting"
          ? "Waiting for the receiver…"
          : pct === null
            ? "Sending…"
            : `Sending… ${pct}%`;
    body = (
      <div className="flex flex-col gap-3">
        <div className="flex items-center gap-2">
          <PulseDot />
          <span className="min-w-0 truncate text-sm font-medium" title={send.path}>
            {send.name}
          </span>
          <span className="shrink-0 text-xs text-muted-foreground">· {kindLabel}</span>
        </div>

        {code ? (
          <div
            className="select-all font-mono text-2xl tracking-wide"
            aria-label="Wormhole code"
          >
            {code}
          </div>
        ) : (
          <div className="font-mono text-2xl tracking-wide text-muted-foreground">
            …
          </div>
        )}

        <div className="flex flex-wrap items-center gap-2">
          <Button size="sm" variant="secondary" disabled={!code} onClick={() => void copy()}>
            {copied ? <Check className="text-emerald-400" /> : <Copy />}
            {copied ? "Copied" : "Copy code"}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={!code}
            aria-pressed={showQR}
            onClick={() => void toggleQR()}
          >
            <QrCode /> QR
          </Button>
          <Button
            size="sm"
            variant="destructive"
            className="ml-auto"
            disabled={stopping}
            onClick={() => void stop()}
          >
            <Square className="size-3.5 fill-current" />
            {stopping ? "Stopping…" : "Stop"}
          </Button>
        </div>

        {showQR ? (
          qr?.code === code ? (
            <img
              alt={`QR code for ${code}`}
              className="mx-auto bg-white p-2"
              width={180}
              height={180}
              src={`data:image/png;base64,${qr.png}`}
            />
          ) : qrErr ? (
            <p className="text-xs text-red-400">{qrErr}</p>
          ) : (
            <p className="text-xs text-muted-foreground">Rendering QR…</p>
          )
        ) : null}

        <div className="flex flex-col gap-1.5">
          <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
            <span>{status}</span>
            {send.phase === "sending" && send.total > 0 ? (
              <span className="font-mono">
                {humanBytes(send.sent)} / {humanBytes(send.total)}
              </span>
            ) : null}
          </div>
          {send.phase === "sending" ? (
            <Progress value={pct ?? 0} indeterminate={pct === null} />
          ) : null}
        </div>

        <p className="text-xs leading-relaxed text-muted-foreground">
          They open Toru → Send Files → Receive and type the code, or run{" "}
          <code className="font-mono text-foreground/80">
            {`wormhole receive ${code || "<code>"}`}
          </code>
          . Keep Toru open until it finishes.
        </p>
      </div>
    );
  } else {
    const ok = send.phase === "done";
    body = (
      <div className="flex flex-col gap-3">
        <div className="flex items-start gap-2">
          {ok ? (
            <Check className="mt-0.5 size-4 shrink-0 text-emerald-400" />
          ) : (
            <X className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          )}
          <div className="min-w-0">
            <p className="truncate text-sm font-medium" title={send.path}>
              {ok
                ? `Sent ${send.name}`
                : send.phase === "stopped"
                  ? `Stopped sending ${send.name}`
                  : `Couldn’t send ${send.name}`}
            </p>
            {send.phase === "error" && send.error ? (
              <p className="mt-0.5 text-xs text-red-400">{send.error}</p>
            ) : null}
          </div>
        </div>
        <div>
          <Button size="sm" variant="secondary" onClick={sendAnother}>
            <Send /> Send another
          </Button>
        </div>
      </div>
    );
  }

  return (
    <section className="frost flex flex-col gap-4 border border-border p-4" aria-label="Send">
      <CardTitle caption="Share a file or folder with a short code">Send</CardTitle>
      {body}
      {localErr ? <p className="text-xs text-red-400">{localErr}</p> : null}
    </section>
  );
}

// ---------------------------------------------------------------------------
// Receive
// ---------------------------------------------------------------------------

function ReceiveCard() {
  const [recv, dismiss] = useReceiveState();
  const [code, setCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [cancelling, setCancelling] = useState(false);
  const [localErr, setLocalErr] = useState("");

  const [dir, setDir] = useState("");
  const [dirDefault, setDirDefault] = useState(true);
  const [dirBusy, setDirBusy] = useState(false);

  const refreshDir = useCallback(() => {
    void TransferService.GetReceiveDir()
      .then((d) => setDir(typeof d === "string" ? d : ""))
      .catch(() => setDir(""));
    void TransferService.IsDefaultReceiveDir()
      .then((v) => setDirDefault(!!v))
      .catch(() => setDirDefault(true));
  }, []);

  useEffect(() => {
    refreshDir();
  }, [refreshDir]);

  const pickDir = async () => {
    if (dirBusy) return;
    setDirBusy(true);
    try {
      const chosen = await TransferService.PickReceiveDir();
      if (chosen) {
        setDir(chosen);
        setDirDefault(false);
      } else {
        refreshDir();
      }
    } catch {
      refreshDir();
    } finally {
      setDirBusy(false);
    }
  };

  const resetDir = async () => {
    if (dirBusy) return;
    setDirBusy(true);
    try {
      await TransferService.ResetReceiveDir();
    } catch {
      // refresh below shows whatever the backend settled on
    } finally {
      refreshDir();
      setDirBusy(false);
    }
  };

  const trimmed = code.trim();
  const canSubmit = trimmed !== "" && !recv.active && !submitting;

  const submit = useCallback(async () => {
    const c = code.trim();
    if (!c || recv.active || submitting) return;
    setSubmitting(true);
    setLocalErr("");
    try {
      await TransferService.StartReceive(c);
    } catch (e) {
      setLocalErr(errText(e));
    } finally {
      setSubmitting(false);
    }
  }, [code, recv.active, submitting]);

  // The input keeps the code while the transfer runs and if it fails, so "Try
  // again" after a relay hiccup is one click; it clears once a receive lands.
  useEffect(() => {
    if (recv.phase === "done") setCode("");
  }, [recv.phase]);

  const cancel = useCallback(async () => {
    if (cancelling) return;
    setCancelling(true);
    try {
      await TransferService.CancelReceive();
    } catch (e) {
      setLocalErr(errText(e));
    } finally {
      setCancelling(false);
    }
  }, [cancelling]);

  const reveal = useCallback(async () => {
    setLocalErr("");
    try {
      await TransferService.RevealReceived();
    } catch (e) {
      setLocalErr(errText(e));
    }
  }, []);

  const receiveAnother = useCallback(() => {
    setLocalErr("");
    dismiss();
  }, [dismiss]);

  const pct = percent(recv.received, recv.total);
  const idle = isIdle(recv);
  // The backend never overwrites: a collision lands as "name (2)", so the
  // on-disk name comes from outPath, not the offered name.
  const savedName = recv.outPath ? (recv.outPath.split(/[\\/]/).pop() ?? recv.name) : recv.name;

  let status: React.ReactNode = null;
  if (recv.active) {
    const connecting = recv.phase === "connecting";
    status = (
      <div className="flex flex-col gap-2 border-t border-border pt-4">
        <div className="flex items-center gap-2">
          <PulseDot />
          <span className="min-w-0 truncate text-sm font-medium">
            {connecting ? "Connecting…" : recv.name || "Receiving…"}
          </span>
          {!connecting && recv.kind ? (
            <span className="shrink-0 text-xs text-muted-foreground">
              · {recv.kind === "folder" ? "Folder" : "File"}
              {recv.fileCount > 1 ? ` · ${recv.fileCount} files` : ""}
            </span>
          ) : null}
          <Button
            size="sm"
            variant="destructive"
            className="ml-auto"
            disabled={cancelling}
            onClick={() => void cancel()}
          >
            <X className="size-3.5" />
            {cancelling ? "Cancelling…" : "Cancel"}
          </Button>
        </div>
        <Progress value={pct ?? 0} indeterminate={connecting || pct === null} />
        <div className="flex items-center justify-between gap-2 text-xs text-muted-foreground">
          <span>
            {connecting ? (
              <>
                Code <span className="font-mono">{recv.code}</span>
              </>
            ) : (
              recv.message || "Receiving…"
            )}
          </span>
          {!connecting ? (
            <span className="font-mono">
              {humanBytes(recv.received)}
              {recv.total > 0 ? ` / ${humanBytes(recv.total)}` : ""}
            </span>
          ) : null}
        </div>
      </div>
    );
  } else if (!idle) {
    const ok = recv.phase === "done";
    status = (
      <div className="flex flex-col gap-3 border-t border-border pt-4">
        <div className="flex items-start gap-2">
          {ok ? (
            <Check className="mt-0.5 size-4 shrink-0 text-emerald-400" />
          ) : (
            <X className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          )}
          <div className="min-w-0">
            {ok ? (
              <p className="text-sm font-medium">
                Saved <span className="break-all">{savedName}</span>
                <span className="text-muted-foreground"> to </span>
                <span className="break-all font-mono text-xs" title={recv.outPath}>
                  {recv.dir}
                </span>
              </p>
            ) : (
              <p className="text-sm font-medium">
                {recv.phase === "cancelled" ? "Receive cancelled" : "Couldn’t receive"}
              </p>
            )}
            {recv.phase === "error" && recv.error ? (
              <p className="mt-0.5 text-xs text-red-400">{recv.error}</p>
            ) : null}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {ok ? (
            <Button size="sm" variant="secondary" onClick={() => void reveal()}>
              <FolderOpen /> Show in Explorer
            </Button>
          ) : null}
          <Button size="sm" variant={ok ? "ghost" : "secondary"} onClick={receiveAnother}>
            {ok ? "Receive another" : "Try again"}
          </Button>
        </div>
      </div>
    );
  }

  return (
    <section className="frost flex flex-col gap-4 border border-border p-4" aria-label="Receive">
      <CardTitle caption="Type the code the sender reads out">Receive</CardTitle>

      <div className="flex flex-col gap-1.5">
        <label htmlFor="transfer-code" className="text-xs text-muted-foreground">
          Code
        </label>
        <div className="flex gap-2">
          <input
            id="transfer-code"
            value={code}
            disabled={recv.active}
            placeholder="7-guitarist-revenge"
            autoComplete="off"
            autoCapitalize="off"
            autoCorrect="off"
            spellCheck={false}
            onChange={(e) => setCode(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.nativeEvent.isComposing) {
                e.preventDefault();
                void submit();
              }
            }}
            className="h-8 min-w-0 flex-1 border border-border bg-background px-2 font-mono text-sm placeholder:text-muted-foreground/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring disabled:opacity-50"
          />
          <Button size="sm" disabled={!canSubmit} onClick={() => void submit()}>
            {submitting ? "…" : "Receive"}
          </Button>
        </div>
      </div>

      {status}

      <div className="flex flex-col gap-2 border-t border-border pt-4">
        <div className="flex flex-col">
          <span className="text-sm font-medium">Save to</span>
          <span className="text-xs text-muted-foreground">
            Incoming files and folders land here. Existing files are never overwritten.
          </span>
        </div>
        <div
          className="truncate rounded-none border border-border bg-background/40 px-2 py-1.5 font-mono text-[11px] text-muted-foreground"
          title={dir || "—"}
        >
          {dir || "—"}
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            size="sm"
            variant="secondary"
            disabled={dirBusy || recv.active}
            onClick={() => void pickDir()}
          >
            <FolderOpen /> {dirBusy ? "…" : "Change…"}
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={dirBusy || dirDefault || recv.active}
            onClick={() => void resetDir()}
          >
            Reset default
          </Button>
          <Button
            size="sm"
            variant="ghost"
            disabled={!dir}
            onClick={() => void TransferService.OpenReceiveDir()}
          >
            Open folder
          </Button>
        </div>
      </div>

      {localErr ? <p className="text-xs text-red-400">{localErr}</p> : null}
    </section>
  );
}
