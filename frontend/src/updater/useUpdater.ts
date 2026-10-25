import { useCallback, useEffect, useState } from "react";
import { UpdateService, UpdateStatus } from "@/lib/api";
import { Events } from "@/lib/contract";
import { Events as WailsEvents } from "@wailsio/runtime";

// localStorage key for the per-viewer "Later" choice: the dismissed version.
// The banner stays hidden for that version only; a newer release shows again.
const DISMISS_KEY = "toru.update.dismissed";

export type UpdatePhase = "idle" | "checking" | "downloading" | "ready" | "installing" | "error";

export interface UpdaterState {
  /** Backend update state (phase/version/notes/error). */
  status: UpdateStatus;
  /** Running version ("dev" in non-release builds; "" until read). */
  version: string;
  /** A check is in flight (manual or the backend's own). */
  checking: boolean;
  /** The last manual check finished and found nothing newer. */
  upToDate: boolean;
  /** The ready banner was dismissed for status.version ("Later"). */
  dismissed: boolean;
  /** Why the last "Restart now" failed ("" if it did not). */
  restartError: string;
  /** Manual "Check for Updates": checks + downloads, never installs. */
  check: () => Promise<void>;
  /** "Restart now": launches the installer and quits Toru. */
  restart: () => Promise<void>;
  /** "Later": hide the banner for this version (Settings keeps a Restart button). */
  dismiss: () => void;
}

function readDismissed(): string {
  try {
    return window.localStorage.getItem(DISMISS_KEY) ?? "";
  } catch {
    return "";
  }
}

function toStatus(raw: unknown): UpdateStatus {
  // createFrom JSON-parses a string payload itself; anything else falls back to idle.
  return UpdateStatus.createFrom(
    typeof raw === "string" || (raw && typeof raw === "object") ? raw : {},
  );
}

/**
 * useUpdater mirrors the backend update state machine for the UI. The backend
 * (internal/update) owns the policy: on startup it checks, downloads and verifies
 * a newer release in the background, then stops at phase "ready" and announces
 * it — it never installs or quits on its own. The user restarts when they like
 * (banner, Settings line, or tray), or the installer runs when Toru quits.
 *
 * This hook reads GetStatus() on mount, follows "update:status" events, and
 * exposes the manual check / restart / dismiss actions. It does NOT trigger a
 * check on mount; the startup AutoUpdate goroutine already does.
 *
 * In the browser preview (no Wails backend) binding calls reject; mount-time
 * reads are swallowed, while a manual check surfaces the failure.
 */
export function useUpdater(): UpdaterState {
  const [status, setStatus] = useState<UpdateStatus>(() => new UpdateStatus({ phase: "idle" }));
  const [version, setVersion] = useState<string>("");
  const [inFlight, setInFlight] = useState(false);
  const [upToDate, setUpToDate] = useState(false);
  const [dismissedVersion, setDismissedVersion] = useState<string>(readDismissed);
  const [restartError, setRestartError] = useState("");

  useEffect(() => {
    UpdateService.GetVersion()
      .then(setVersion)
      .catch(() => setVersion(""));
    UpdateService.GetStatus()
      .then((s) => setStatus(toStatus(s)))
      .catch(() => {});

    const off = WailsEvents.On(Events.UpdateStatus, (ev) => {
      const raw = Array.isArray(ev.data) ? ev.data[0] : ev.data;
      const next = toStatus(raw);
      setStatus(next);
      // Any movement off idle (a new check, a download, a ready state) makes a
      // stale "Up to date" badge wrong.
      if (next.phase !== "idle") setUpToDate(false);
    });
    return () => off();
  }, []);

  const check = useCallback(async () => {
    setInFlight(true);
    setUpToDate(false);
    try {
      const result = toStatus(await UpdateService.CheckAndDownload());
      setStatus(result);
      // "Up to date" is not a backend phase: a finished check that found nothing
      // newer lands on idle. A transitional phase (checking/downloading) means we
      // raced the startup check and got its in-flight status; the next
      // update:status event settles it.
      if (result.phase === "idle") setUpToDate(true);
    } catch (e) {
      setStatus(new UpdateStatus({ phase: "error", error: String(e) }));
    } finally {
      setInFlight(false);
    }
  }, []);

  // A failed restart is reported beside the Restart button; the backend keeps
  // (and re-emits) the authoritative phase — it reverts to "ready" when the
  // installer fails to launch, so the user can retry or use the tray.
  const restart = useCallback(async () => {
    setRestartError("");
    try {
      await UpdateService.RestartToUpdate();
    } catch (e) {
      setRestartError(String(e));
    }
  }, []);

  const dismiss = useCallback(() => {
    const v = status.version;
    setDismissedVersion(v);
    try {
      window.localStorage.setItem(DISMISS_KEY, v);
    } catch {
      // per-viewer convenience only; in-memory state still hides the banner
    }
  }, [status.version]);

  const checking = inFlight || status.phase === "checking";
  const dismissed = status.version !== "" && dismissedVersion === status.version;

  return { status, version, checking, upToDate, dismissed, restartError, check, restart, dismiss };
}
