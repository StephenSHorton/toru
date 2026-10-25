import { useEffect, useState } from "react";
import { Download, RefreshCw } from "lucide-react";
import { Events as WailsEvents } from "@wailsio/runtime";
import { Button } from "@/components/ui/button";
import { Progress } from "@/components/ui/progress";
import { ReplayService } from "@/lib/api";
import { useUpdater } from "./useUpdater";

// UpdateBanner — the in-window announcement that a new Toru is ready. Mounted
// at the top of the hub's <main> on every page. The backend never installs on
// its own, so this is where the user decides: "Restart now" launches the
// installer and quits; "Later" hides the bar for this version (the Settings
// About line and the tray keep a Restart entry), and the update installs
// when Toru quits anyway.
//
// Also reflects the other visible phases: a thin "Downloading…" line while the
// startup/manual check fetches the installer, and "Restarting to update…" once
// the installer has been launched.
export function UpdateBanner() {
  const { status, dismissed, restartError, restart, dismiss } = useUpdater();
  const [recording, setRecording] = useState(false);

  // Same source as ReplayPanel: GetRecorderState on mount + "recording:changed".
  useEffect(() => {
    const refresh = () => {
      void ReplayService.GetRecorderState()
        .then((r) => setRecording(!!r?.recording))
        .catch(() => {});
    };
    refresh();
    const off = WailsEvents.On("recording:changed", () => refresh());
    return () => off();
  }, []);

  const v = status.version ? `v${status.version}` : "";

  if (status.phase === "downloading") {
    return (
      <div
        role="status"
        aria-live="polite"
        aria-label={`Downloading Toru ${v}`}
        className="mb-4 flex flex-col gap-1.5 text-xs text-muted-foreground"
      >
        <span className="flex items-center gap-2">
          <Download className="size-3.5" aria-hidden="true" />
          Downloading Toru {v}…
        </span>
        <Progress indeterminate className="h-1" />
      </div>
    );
  }

  if (status.phase === "installing") {
    return (
      <div
        role="status"
        aria-live="polite"
        className="mb-4 flex items-center gap-2 text-xs text-muted-foreground"
      >
        <RefreshCw className="size-3.5 animate-spin" aria-hidden="true" />
        Restarting to update…
      </div>
    );
  }

  if (status.phase !== "ready" || dismissed) return null;

  return (
    <div
      role="status"
      aria-live="polite"
      aria-label={`Toru ${v} is ready to install`}
      className="frost mb-4 flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3"
    >
      <Download className="size-5 shrink-0 text-primary" aria-hidden="true" />
      <div className="min-w-0 flex-1">
        <div className="text-sm font-medium">Toru {v} is ready.</div>
        <div className="text-xs text-muted-foreground">
          Restart now to update, or keep working — it installs when you quit Toru.
        </div>
        {recording ? (
          <div className="mt-0.5 text-xs text-destructive">Restarting stops the current recording.</div>
        ) : null}
        {restartError ? (
          <div className="mt-0.5 text-xs text-destructive" title={restartError}>
            Couldn't start the installer — try again, or quit Toru to install.
          </div>
        ) : null}
      </div>
      <div className="flex shrink-0 items-center gap-2">
        <Button
          size="sm"
          onClick={() => void restart()}
          aria-label={`Restart now to update to Toru ${v}`}
          title={`Restart Toru and install ${v}`}
        >
          <RefreshCw aria-hidden="true" /> Restart now
        </Button>
        <Button
          size="sm"
          variant="ghost"
          onClick={dismiss}
          aria-label="Later — keep working; the update installs when you quit Toru"
          title="Keep working; it installs when you quit Toru"
        >
          Later
        </Button>
      </div>
    </div>
  );
}
