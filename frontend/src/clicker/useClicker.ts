import { useCallback, useEffect, useRef, useState } from "react";
import { Events as WailsEvents } from "@wailsio/runtime";
import { ClickerService, ClickerSettings, ClickerStats, ClickerStatus } from "@/lib/api";
import { RunState, parseError } from "./cadence";

// useClicker mirrors ClickerService for the Auto Clicker page. Like Send Files
// (useTransfer.ts) it subscribes to clicker:status / clicker:settings FIRST,
// then fetches Status() / GetSettings() once, and drops a fetch that resolves
// after an event already arrived, so a run started from the tray or a hotkey
// is on screen at once and a slow fetch can't clobber newer state.
//
// Edits: update(fn) applies locally right away, then sends the whole Settings
// to SetSettings after a short debounce and adopts the normalized result Go
// returns (unless the user has edited again meanwhile). Go is the only judge of
// a value; the page renders whatever comes back.

const SAVE_DEBOUNCE_MS = 250;
const ERROR_CLEAR_MS = 6000;

export const IDLE_STATUS: ClickerStatus = new ClickerStatus({ state: RunState.StateIdle, activePoint: -1 });

// Event payloads are typed via eventdata.d.ts; this also accepts the
// array-wrapped shape some runtime versions deliver.
function payload<T>(d: unknown): T | null {
  const v = Array.isArray(d) ? d[0] : d;
  return v && typeof v === "object" ? (v as T) : null;
}

/** isActive reports whether a run (or its countdown) is in progress. */
export function isActive(s: ClickerStatus): boolean {
  return s.state !== RunState.StateIdle && s.state !== RunState.$zero;
}

export interface Clicker {
  settings: ClickerSettings | null; // null until the first fetch/event
  status: ClickerStatus;
  stats: ClickerStats | null;
  /** update edits settings locally and saves them (debounced). */
  update: (fn: (s: ClickerSettings) => ClickerSettings) => void;
  /**
   * apply runs a ClickerService call that returns Settings (presets, arm) after
   * flushing any unsaved edit, adopts the result and reports errors. Resolves
   * true on success.
   */
  apply: (call: () => Promise<ClickerSettings>) => Promise<boolean>;
  /** run is apply for calls with other results (Start, Stop, Export). */
  run: <T>(call: () => Promise<T>) => Promise<T | undefined>;
  error: string;
  showError: (msg: string) => void;
  clearError: () => void;
  resetStats: () => Promise<void>;
}

/** CardProps is what every Auto Clicker card receives from the page. */
export interface CardProps {
  settings: ClickerSettings;
  status: ClickerStatus;
  update: (fn: (s: ClickerSettings) => ClickerSettings) => void;
}

export function useClicker(): Clicker {
  const [settings, setSettings] = useState<ClickerSettings | null>(null);
  const [status, setStatus] = useState<ClickerStatus>(IDLE_STATUS);
  const [stats, setStats] = useState<ClickerStats | null>(null);
  const [error, setError] = useState("");

  const settingsRef = useRef<ClickerSettings | null>(null);
  // editSeq counts local edits; a SetSettings answer is adopted only if no
  // edit happened after it was sent.
  const editSeq = useRef(0);
  const pending = useRef<ClickerSettings | null>(null);
  const timer = useRef<number | null>(null);
  const errTimer = useRef<number | null>(null);

  const adopt = useCallback((s: ClickerSettings) => {
    settingsRef.current = s;
    setSettings(s);
  }, []);

  const showError = useCallback((msg: string) => {
    setError(msg);
    if (errTimer.current !== null) window.clearTimeout(errTimer.current);
    errTimer.current = window.setTimeout(() => setError(""), ERROR_CLEAR_MS);
  }, []);

  const clearError = useCallback(() => {
    if (errTimer.current !== null) window.clearTimeout(errTimer.current);
    setError("");
  }, []);

  const refreshStats = useCallback(() => {
    void ClickerService.Stats()
      .then((s) => s && setStats(s))
      .catch(() => {});
  }, []);

  // flush sends the pending edit now. On a rejection the page re-reads what Go
  // has, so the controls never show a value that was not stored.
  const flush = useCallback(async () => {
    if (timer.current !== null) {
      window.clearTimeout(timer.current);
      timer.current = null;
    }
    const next = pending.current;
    if (!next) return;
    pending.current = null;
    const seq = editSeq.current;
    try {
      const stored = await ClickerService.SetSettings(next);
      if (stored && seq === editSeq.current) adopt(stored);
    } catch (e) {
      showError(parseError(e).message);
      if (seq === editSeq.current) {
        try {
          adopt(await ClickerService.GetSettings());
        } catch {
          /* keep the local copy */
        }
      }
    }
  }, [adopt, showError]);

  const update = useCallback(
    (fn: (s: ClickerSettings) => ClickerSettings) => {
      const cur = settingsRef.current;
      if (!cur) return;
      const next = fn(cur);
      editSeq.current += 1;
      pending.current = next;
      adopt(next);
      if (timer.current !== null) window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => void flush(), SAVE_DEBOUNCE_MS);
    },
    [adopt, flush],
  );

  const run = useCallback(
    async <T>(call: () => Promise<T>): Promise<T | undefined> => {
      await flush();
      try {
        return await call();
      } catch (e) {
        showError(parseError(e).message);
        return undefined;
      }
    },
    [flush, showError],
  );

  const apply = useCallback(
    async (call: () => Promise<ClickerSettings>): Promise<boolean> => {
      const got = await run(call);
      if (!got) return false;
      editSeq.current += 1; // an older SetSettings answer must not undo this
      adopt(got);
      return true;
    },
    [run, adopt],
  );

  const resetStats = useCallback(async () => {
    const s = await run(() => ClickerService.ResetStats());
    if (s) setStats(s);
  }, [run]);

  useEffect(() => {
    let gone = false;
    let gotStatus = false;
    let gotSettings = false;
    const offStatus = WailsEvents.On("clicker:status", (ev) => {
      const d = payload<ClickerStatus>(ev.data);
      if (!d) return;
      gotStatus = true;
      setStatus(d);
    });
    const offSettings = WailsEvents.On("clicker:settings", (ev) => {
      const d = payload<ClickerSettings>(ev.data);
      if (!d) return;
      gotSettings = true;
      // An unsent local edit wins: it is about to overwrite Go's copy anyway.
      if (pending.current) return;
      editSeq.current += 1;
      adopt(d);
    });
    void ClickerService.Status()
      .then((s) => {
        if (!gone && !gotStatus && s) setStatus(s);
      })
      .catch(() => {});
    void ClickerService.GetSettings()
      .then((s) => {
        if (!gone && !gotSettings && s && !settingsRef.current) adopt(s);
      })
      .catch((e) => {
        if (!gone) showError(parseError(e).message);
      });
    refreshStats();
    return () => {
      gone = true;
      offStatus();
      offSettings();
      // Leaving the page must not lose the last edit.
      void flush();
      if (errTimer.current !== null) window.clearTimeout(errTimer.current);
    };
  }, [adopt, flush, refreshStats, showError]);

  // Lifetime totals change when a run ends.
  const wasActive = useRef(false);
  useEffect(() => {
    const active = isActive(status);
    if (wasActive.current && !active) refreshStats();
    wasActive.current = active;
  }, [status, refreshStats]);

  return { settings, status, stats, update, apply, run, error, showError, clearError, resetStats };
}
