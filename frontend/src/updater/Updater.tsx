import { Button } from "@/components/ui/button";
import { RefreshCw, Download, CheckCircle2, AlertTriangle } from "lucide-react";
import { useUpdater } from "./useUpdater";

// Updater — the Check-for-Updates + auto-update UI. Auto-checks on mount; shows
// a prominent frosted banner when an update is available, otherwise a compact
// version line + manual "Check for Updates" control. Sharp / dark / frosted.
export function Updater() {
  const { status, version, info, error, check, install, dismiss } = useUpdater();
  const v = version || "dev";

  if (status === "available" && info) {
    return (
      <div className="frost flex w-full items-center gap-3 p-3" style={{ maxWidth: 420 }}>
        <Download className="size-5 text-primary" />
        <div className="min-w-0 flex-1">
          <div className="text-sm font-medium">Update available — v{info.version}</div>
          <div className="truncate text-xs text-muted-foreground">
            You're on v{v}. Installs and restarts Toru.
          </div>
        </div>
        <Button size="sm" variant="ghost" onClick={dismiss}>
          Later
        </Button>
        <Button size="sm" onClick={() => void install()}>
          Install &amp; Restart
        </Button>
      </div>
    );
  }

  if (status === "downloading") {
    return (
      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <RefreshCw className="size-3.5 animate-spin" />
        Downloading update… Toru will restart to finish.
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
