import { useCallback, useEffect, useState } from "react";
import { HotkeyService, Shortcut } from "@/lib/api";

export interface ShortcutsState {
  shortcuts: Shortcut[];
  loading: boolean;
  error: string;
  /** Reload the shortcut list from the backend. */
  reload: () => Promise<void>;
  /**
   * Persist a new combo for action. Resolves to "" on success, or the backend
   * validation/persist error string on failure (does NOT throw, so the panel can
   * show it inline).
   */
  save: (action: string, sc: Shortcut) => Promise<string>;
  /** Reset action to its default combo, then reload. */
  reset: (action: string) => Promise<void>;
}

/**
 * useShortcuts drives the Shortcuts panel. It loads the configured shortcuts on
 * mount and exposes save/reset actions.
 *
 * In the browser preview (no Wails backend) the binding calls reject; the
 * auto-load swallows that silently (empty list), mirroring useUpdater.
 */
export function useShortcuts(): ShortcutsState {
  const [shortcuts, setShortcuts] = useState<Shortcut[]>([]);
  const [loading, setLoading] = useState<boolean>(true);
  const [error, setError] = useState<string>("");

  const reload = useCallback(async () => {
    setLoading(true);
    try {
      const list = await HotkeyService.GetShortcuts();
      setShortcuts(list ?? []);
      setError("");
    } catch {
      setShortcuts([]); // backend unavailable (e.g. browser preview)
    } finally {
      setLoading(false);
    }
  }, []);

  const save = useCallback(
    async (action: string, sc: Shortcut): Promise<string> => {
      try {
        await HotkeyService.SetShortcut(action, sc);
        await reload();
        return "";
      } catch (e) {
        return String(e);
      }
    },
    [reload],
  );

  const reset = useCallback(
    async (action: string) => {
      try {
        await HotkeyService.ResetShortcut(action);
        await reload();
      } catch (e) {
        setError(String(e));
      }
    },
    [reload],
  );

  useEffect(() => {
    void reload();
  }, [reload]);

  return { shortcuts, loading, error, reload, save, reset };
}
