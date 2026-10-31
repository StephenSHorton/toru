import { Button } from "@/components/ui/button";
import { RefreshCw, CheckCircle2, AlertTriangle, Download } from "lucide-react";
import { useUpdater } from "./useUpdater";

// Updater — the Settings › About version line. "Check for Updates" runs the
// backend's CheckAndDownload (check + download + verify, never install) and
// shows checking/downloading state. When an update is ready it offers
// "Restart now" here too, so a user who clicked "Later" on the banner still has
// a way in; otherwise it shows the green "Up to date" or the error state. The
// backend never restarts Toru on its own — it installs when the user quits.
export function Updater() {
  const { status, version, checking, upToDate, restartError, check, restart } = useUpdater();
  const v = version || "dev";
  const target = status.version ? `v${status.version}` : "";
  const busy = checking || status.phase === "downloading";

  if (status.phase === "installing") {
    return (
      <div className="flex items-center gap-2 text-xs text-muted-foreground" role="status">
        <RefreshCw className="size-3.5 animate-spin" aria-hidden="true" />
        Restarting to update{target ? ` to ${target}` : ""}…
      </div>
    );
  }

  return (
    <div className="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
      <span>Toru v{v}</span>
      <span className="opacity-40">·</span>
      {status.phase === "ready" ? (
        <>
          <span className="flex items-center gap-1 text-primary">
            <Download className="size-3.5" aria-hidden="true" /> {target} is ready
          </span>
          <Button
            size="sm"
            className="h-6 px-2 text-xs"
            onClick={() => void restart()}
            aria-label={`Restart now to update to Toru ${target}`}
            title={`Restart Toru and install ${target}`}
          >
            <RefreshCw className="size-3.5" aria-hidden="true" /> Restart now
          </Button>
          {restartError && (
            <span className="flex items-center gap-1 text-destructive" title={restartError}>
              <AlertTriangle className="size-3.5" aria-hidden="true" /> Restart failed
            </span>
          )}
        </>
      ) : (
        <>
          <Button
            size="sm"
            variant="ghost"
            className="h-6 px-2 text-xs"
            disabled={busy}
            onClick={() => void check()}
            aria-label="Check for Toru updates"
            title="Check GitHub Releases for a newer Toru"
          >
            <RefreshCw className={"size-3.5" + (busy ? " animate-spin" : "")} aria-hidden="true" />
            {status.phase === "downloading"
              ? `Downloading ${target}…`
              : checking
                ? "Checking…"
                : "Check for Updates"}
          </Button>
          {upToDate && !busy && (
            <span className="flex items-center gap-1 text-green-500">
              <CheckCircle2 className="size-3.5" aria-hidden="true" /> Up to date
            </span>
          )}
          {status.phase === "error" && (
            // fail() keeps the version once the check has found one, so a set
            // version means the download/verify step failed, not the check.
            // installFailed: the last silent install couldn't replace the app.
            <span className="flex items-center gap-1 text-destructive" title={status.error}>
              <AlertTriangle className="size-3.5" aria-hidden="true" />{" "}
              {status.installFailed
                ? `Update to ${target} didn't install`
                : status.version
                  ? `Download of ${target} failed`
                  : "Check failed"}
            </span>
          )}
        </>
      )}
    </div>
  );
}
