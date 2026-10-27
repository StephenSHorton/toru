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
// Edits: update(fn) keeps fn itself, not a finished snapshot. What the page
// shows is Go's last known Settings (base) with the unsaved edits replayed on
// top, so a change Go makes meanwhile (hotkey disarm, tray, point/zone pick)
// survives: the next save replays the edits onto it instead of sending a stale
// copy back. Saves are debounced and, like apply/run, go through one serial
// queue, so a preset apply or an arm toggle never races a SetSettings that is
// still on the wire. Go is the only judge of a value; the page renders what
// comes back.

const SAVE_DEBOUNCE_MS = 250;
const ERROR_CLEAR_MS = 6000;

type Edit = (s: ClickerSettings) => ClickerSettings;

const replay = (edits: Edit[], base: ClickerSettings) => edits.reduce((s, f) => f(s), base);
const same = (a: ClickerSettings, b: ClickerSettings) => JSON.stringify(a) === JSON.stringify(b);

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

  // base is Go's last known Settings; queued holds edits not sent yet and sent
  // the ones in the SetSettings call on the wire. heard keeps the newest
  // clicker:settings payload that arrived during that call.
  const base = useRef<ClickerSettings | null>(null);
  const queued = useRef<Edit[]>([]);
  const sent = useRef<Edit[]>([]);
  const sending = useRef(false);
  const heard = useRef<ClickerSettings | null>(null);
  const chain = useRef<Promise<unknown>>(Promise.resolve());
  const timer = useRef<number | null>(null);
  const errTimer = useRef<number | null>(null);

  // show renders base with every unsaved edit replayed on top.
  const show = useCallback(() => {
    if (base.current) setSettings(replay([...sent.current, ...queued.current], base.current));
  }, []);

  const rebase = useCallback(
    (s: ClickerSettings) => {
      base.current = s;
      show();
    },
    [show],
  );

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

  // serial runs task after every earlier save/apply/run has settled.
  const serial = useCallback(<T,>(task: () => Promise<T>): Promise<T> => {
    const p = chain.current.then(task);
    chain.current = p.catch(() => {});
    return p;
  }, []);

  // save sends the queued edits, replayed onto base. If Go broadcast new
  // Settings while the call was on the wire, that change may have landed
  // before ours and been overwritten, so the same edits are replayed onto the
  // broadcast and sent once more when that differs from it. On a rejection the
  // page re-reads what Go has, so the controls never show a value that was not
  // stored. Callers hold the serial queue.
  const save = useCallback(async () => {
    if (timer.current !== null) {
      window.clearTimeout(timer.current);
      timer.current = null;
    }
    let retried = false;
    while (queued.current.length > 0 && base.current) {
      const edits = queued.current;
      queued.current = [];
      sent.current = edits;
      heard.current = null;
      sending.current = true;
      let stored: ClickerSettings | null = null;
      let failed = false;
      try {
        stored = await ClickerService.SetSettings(replay(edits, base.current));
      } catch (e) {
        showError(parseError(e).message);
        failed = true;
      }
      sending.current = false;
      sent.current = [];
      const ev = heard.current;
      heard.current = null;
      if (failed) {
        if (ev) rebase(ev);
        else {
          try {
            rebase(await ClickerService.GetSettings());
          } catch {
            show(); // keep the last known copy, minus the rejected edit
          }
        }
        return;
      }
      if (ev && !retried && !same(replay(edits, ev), ev)) {
        retried = true;
        queued.current = [...edits, ...queued.current];
        rebase(ev);
        continue;
      }
      rebase((retried ? stored : ev) ?? stored ?? base.current);
      return;
    }
  }, [rebase, show, showError]);

  const flush = useCallback(() => serial(save), [serial, save]);

  const update = useCallback(
    (fn: Edit) => {
      if (!base.current) return;
      queued.current.push(fn);
      show();
      if (timer.current !== null) window.clearTimeout(timer.current);
      timer.current = window.setTimeout(() => void flush(), SAVE_DEBOUNCE_MS);
    },
    [show, flush],
  );

  // run saves pending edits, then runs call in the same serial slot, so no
  // SetSettings can land between them; done sees the result inside that slot.
  const runThen = useCallback(
    <T,>(call: () => Promise<T>, done?: (got: T) => void): Promise<T | undefined> =>
      serial(async () => {
        await save();
        try {
          const got = await call();
          done?.(got);
          return got;
        } catch (e) {
          showError(parseError(e).message);
          return undefined;
        }
      }),
    [serial, save, showError],
  );

  const run = useCallback(<T,>(call: () => Promise<T>) => runThen(call), [runThen]);

  const apply = useCallback(
    async (call: () => Promise<ClickerSettings>): Promise<boolean> => {
      let ok = false;
      await runThen(call, (got) => {
        if (!got) return;
        ok = true;
        rebase(got);
      });
      return ok;
    },
    [runThen, rebase],
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
      // Unsaved edits are replayed on top, so neither side's change is lost.
      if (sending.current) heard.current = d;
      rebase(d);
    });
    void ClickerService.Status()
      .then((s) => {
        if (!gone && !gotStatus && s) setStatus(s);
      })
      .catch(() => {});
    void ClickerService.GetSettings()
      .then((s) => {
        if (!gone && !gotSettings && s && !base.current) rebase(s);
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
  }, [rebase, flush, refreshStats, showError]);

  // Lifetime totals change when a run ends.
  const wasActive = useRef(false);
  useEffect(() => {
    const active = isActive(status);
    if (wasActive.current && !active) refreshStats();
    wasActive.current = active;
  }, [status, refreshStats]);

  return { settings, status, stats, update, apply, run, error, showError, clearError, resetStats };
}
