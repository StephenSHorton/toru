import { useCallback, useEffect, useState } from "react";
import { UpdateService, UpdateInfo } from "@/lib/api";

export type UpdaterStatus =
  | "idle"
  | "checking"
  | "available"
  | "uptodate"
  | "downloading"
  | "error";

export interface UpdaterState {
  status: UpdaterStatus;
  version: string; // running version ("dev" in non-release builds)
  info: UpdateInfo | null; // the available update, when status === "available"
  error: string;
  check: (manual?: boolean) => Promise<void>;
  install: () => Promise<void>;
  dismiss: () => void;
}

/**
 * useUpdater drives the in-app updater UI. It reads the running version, does a
 * silent auto-check on mount, and exposes manual check + install actions.
 *
 * In the browser preview (no Wails backend) the binding calls reject; the
 * auto-check swallows that silently, while a manual check surfaces it.
 */
export function useUpdater(): UpdaterState {
  const [status, setStatus] = useState<UpdaterStatus>("idle");
  const [version, setVersion] = useState<string>("");
  const [info, setInfo] = useState<UpdateInfo | null>(null);
  const [error, setError] = useState<string>("");

  const check = useCallback(async (manual = false) => {
    setStatus("checking");
    setError("");
    try {
      const result = await UpdateService.CheckForUpdate();
      if (result) {
        setInfo(result);
        setStatus("available");
      } else {
        setInfo(null);
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

  const install = useCallback(async () => {
    if (!info) return;
    setStatus("downloading");
    setError("");
    try {
      // Resolves after the installer launches; the app then quits to update.
      await UpdateService.DownloadAndInstall(info);
    } catch (e) {
      setError(String(e));
      setStatus("error");
    }
  }, [info]);

  const dismiss = useCallback(() => setStatus("idle"), []);

  useEffect(() => {
    UpdateService.GetVersion()
      .then(setVersion)
      .catch(() => setVersion(""));
    void check(false); // silent auto-check on startup
  }, [check]);

  return { status, version, info, error, check, install, dismiss };
}
