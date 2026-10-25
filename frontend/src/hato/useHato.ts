import { useCallback, useEffect, useReducer, useRef, useState } from "react";
import { Events as WailsEvents } from "@wailsio/runtime";
import { HatoService, ReceiveState, SendState } from "@/lib/api";

// useSendState / useReceiveState mirror the ONE outgoing and ONE incoming
// transfer the Go side tracks. Each hook subscribes to its hato:* event FIRST,
// then fetches the current status once, so a transfer started from the tray or
// a Library card (which emits before this page mounts) is on screen immediately
// and a late-resolving fetch can never clobber a newer event.
//
// The backend has no "clear the finished transfer" call: SendStatus() keeps
// reporting phase "done" until the next send. "Send another" / "Receive
// another" are therefore a local dismissal keyed on the finished state. The key
// lives at module scope so switching Library → Hato (which remounts this page
// and refetches the same "done" state) does not bring the result card back.

export const IDLE_SEND: SendState = new SendState({ phase: "idle" });
export const IDLE_RECEIVE: ReceiveState = new ReceiveState({ phase: "idle" });

let dismissedSend = "";
let dismissedReceive = "";

// A StartSend kicked off from OUTSIDE this page (the Library card) can be
// rejected synchronously — the capture file was deleted behind Toru's back —
// after the caller has already switched to the Hato page. The rejection has
// nowhere to render there, so it is parked here and handed to the Send card
// when it subscribes (or immediately, if it already has).
let pendingSendError = "";
const sendErrorSubs = new Set<(msg: string) => void>();

/** reportLocalSendError routes a synchronous StartSend rejection to the Send card. */
export function reportLocalSendError(msg: string): void {
  if (!msg) return;
  if (sendErrorSubs.size === 0) {
    pendingSendError = msg;
    return;
  }
  pendingSendError = "";
  sendErrorSubs.forEach((fn) => fn(msg));
}

/** onLocalSendError subscribes to reportLocalSendError; delivers any parked message at once. */
export function onLocalSendError(fn: (msg: string) => void): () => void {
  sendErrorSubs.add(fn);
  if (pendingSendError) {
    const msg = pendingSendError;
    pendingSendError = "";
    fn(msg);
  }
  return () => {
    sendErrorSubs.delete(fn);
  };
}

const sendKey = (s: SendState) => `${s.phase}|${s.path}|${s.code}`;
const receiveKey = (s: ReceiveState) => `${s.phase}|${s.code}|${s.outPath}`;

/** isIdle reports whether a state should render the empty/start UI. */
export function isIdle(s: { active: boolean; phase: string }): boolean {
  return !s.active && (s.phase === "" || s.phase === "idle");
}

// Event payloads are typed SendState/ReceiveState via eventdata.d.ts; this
// guards against the array-wrapped shape some runtime versions deliver.
function payload<T>(d: unknown): T | null {
  const v = Array.isArray(d) ? d[0] : d;
  return v && typeof v === "object" ? (v as T) : null;
}

export function useSendState(): [SendState, () => void] {
  const [raw, setRaw] = useState<SendState>(IDLE_SEND);
  const rawRef = useRef(raw);
  rawRef.current = raw;
  const [, bump] = useReducer((n: number) => n + 1, 0);

  useEffect(() => {
    let gone = false;
    let gotEvent = false;
    const off = WailsEvents.On("hato:send", (ev) => {
      const d = payload<SendState>(ev.data);
      if (!d) return;
      gotEvent = true;
      // A fresh transfer, or a fresh failure (a rejected drop lands as an
      // error-phase event with no active phase before it), un-dismisses.
      if (d.active || d.phase === "error") dismissedSend = "";
      setRaw(d);
    });
    void HatoService.SendStatus()
      .then((s) => {
        if (!gone && !gotEvent && s) setRaw(s);
      })
      .catch(() => {});
    return () => {
      gone = true;
      off();
    };
  }, []);

  const dismiss = useCallback(() => {
    dismissedSend = sendKey(rawRef.current);
    bump();
  }, []);

  const state = !raw.active && dismissedSend === sendKey(raw) ? IDLE_SEND : raw;
  return [state, dismiss];
}

export function useReceiveState(): [ReceiveState, () => void] {
  const [raw, setRaw] = useState<ReceiveState>(IDLE_RECEIVE);
  const rawRef = useRef(raw);
  rawRef.current = raw;
  const [, bump] = useReducer((n: number) => n + 1, 0);

  useEffect(() => {
    let gone = false;
    let gotEvent = false;
    const off = WailsEvents.On("hato:receive", (ev) => {
      const d = payload<ReceiveState>(ev.data);
      if (!d) return;
      gotEvent = true;
      if (d.active) dismissedReceive = "";
      setRaw(d);
    });
    void HatoService.ReceiveStatus()
      .then((s) => {
        if (!gone && !gotEvent && s) setRaw(s);
      })
      .catch(() => {});
    return () => {
      gone = true;
      off();
    };
  }, []);

  const dismiss = useCallback(() => {
    dismissedReceive = receiveKey(rawRef.current);
    bump();
  }, []);

  const state = !raw.active && dismissedReceive === receiveKey(raw) ? IDLE_RECEIVE : raw;
  return [state, dismiss];
}

/** errText turns a rejected binding call into one line for the page. */
export function errText(e: unknown): string {
  if (e instanceof Error) return e.message;
  const s = String(e ?? "");
  return s || "Something went wrong";
}
