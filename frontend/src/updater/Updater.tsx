import { Button } from "@/components/ui/button";
import { RefreshCw, CheckCircle2, AlertTriangle } from "lucide-react";
import { useUpdater } from "./useUpdater";

// Updater — the version line + auto-update status. Updates are MANDATORY: there
// is no "Later" / opt-out. A found update (startup, manual check, or backend
// goroutine) installs immediately, and Toru quits + relaunches on the new
// version. While that happens we show a non-dismissible "Updating…" line.
export function Updater() {
  const { status, version, updatingTo, error, check } = useUpdater();
  const v = version || "dev";

  if (status === "downloading") {
    return (
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <RefreshCw className="size-3.5 animate-spin" />
        {updatingTo ? `Updating to v${updatingTo}…` : "Updating…"} Toru will restart.
      </div>
    );
  }

  return (
    <div className="flex items-center gap-2 text-xs text-muted-foreground">
      <span>Toru v{v}</span>
      <span className="opacity-40">·</span>
      <Button
        size="sm"
        variant="ghost"
        className="h-6 px-2 text-xs"
        disabled={status === "checking"}
        onClick={() => void check(true)}
      >
        <RefreshCw className={"size-3.5" + (status === "checking" ? " animate-spin" : "")} />
        {status === "checking" ? "Checking…" : "Check for Updates"}
      </Button>
      {status === "uptodate" && (
        <span className="flex items-center gap-1 text-green-500">
          <CheckCircle2 className="size-3.5" /> Up to date
        </span>
      )}
      {status === "error" && (
        <span className="flex items-center gap-1 text-destructive" title={error}>
          <AlertTriangle className="size-3.5" /> Check failed
        </span>
      )}
    </div>
  );
}
