import { useCallback, useEffect, useState } from "react";
import { UpdateService } from "@/lib/api";
import { Events as WailsEvents } from "@wailsio/runtime";
import { Events } from "@/lib/contract";

export type UpdaterStatus =
  | "idle"
  | "checking"
  | "uptodate"
  | "downloading"
  | "error";

export interface UpdaterState {
  status: UpdaterStatus;
  version: string; // running version ("dev" in non-release builds)
  updatingTo: string; // version being installed, when status === "downloading"
  error: string;
  check: (manual?: boolean) => Promise<void>;
}

/**
 * useUpdater drives the in-app updater UI. Updates are MANDATORY: a found update
 * installs immediately with no opt-out (keeping Toru current is part of using
 * it). The backend AutoUpdate goroutine enforces the same on every startup —
 * even a launch-at-login boot with no window — so this hook is the UI reflection,
 * not the source of truth. It reads the running version, lets the user force a
 * manual check, and listens for the backend's "update:installing" event so an
 * open window shows "Updating…" before the app quits to relaunch on the new
 * version. Concurrent installs (this vs. the startup goroutine) are deduped in
 * the backend, so racing is harmless.
 *
 * In the browser preview (no Wails backend) the binding calls reject; an
 * auto/background failure is swallowed, while a manual check surfaces it.
 */
export function useUpdater(): UpdaterState {
  const [status, setStatus] = useState<UpdaterStatus>("idle");
  const [version, setVersion] = useState<string>("");
  const [updatingTo, setUpdatingTo] = useState<string>("");
  const [error, setError] = useState<string>("");

  const check = useCallback(async (manual = false) => {
    setStatus("checking");
    setError("");
    try {
      const result = await UpdateService.CheckForUpdate();
      if (result) {
        // Mandatory update — install immediately, no prompt, no "Later".
        setUpdatingTo(result.version);
        setStatus("downloading");
        // Resolves once the installer launches; the app then quits and the
        // installer relaunches the updated Toru. Deduped backend-side, so this is
        // safe even if the startup AutoUpdate is already installing.
        await UpdateService.DownloadAndInstall(result);
      } else {
        setStatus("uptodate");
      }
    } catch (e) {
      if (manual) {
        setError(String(e));
        setStatus("error");
      } else {
        setStatus("idle"); // backend unavailable (e.g. browser preview)
      }
    }
  }, []);

  useEffect(() => {
    UpdateService.GetVersion()
      .then(setVersion)
      .catch(() => setVersion(""));

    // The backend AutoUpdate goroutine can start an install on its own (notably a
    // launch-at-login boot, where no window ran a check). Reflect it so an open
    // window shows "Updating…" rather than abruptly closing.
    const off = WailsEvents.On(Events.UpdateInstalling, (ev) => {
      const raw = Array.isArray(ev.data) ? ev.data[0] : ev.data;
      setUpdatingTo(typeof raw === "string" ? raw : "");
      setStatus("downloading");
    });

    // Also re-check when the window opens (covers a long-running instance whose
    // startup check predates a newer release). Auto-installs on found; deduped.
    void check(false);

    return () => off();
  }, [check]);

  return { status, version, updatingTo, error, check };
}
