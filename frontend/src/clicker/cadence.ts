// Display-only helpers for the Auto Clicker page: rate/interval readouts,
// durations, the preset summary line and error text. Go's Normalize is the
// authority on every value; nothing here decides what gets stored, it only
// renders what Go sent (or previews an edit until Go's answer lands).

import type { ClickerProfile } from "@/lib/api";
import { errorMessage } from "@/lib/errors";
// The generated string enums. lib/api re-exports the clicker classes but not
// these, and comparing a string enum to a bare literal does not type-check, so
// the page takes them from here.
export {
  CadenceMode,
  HotkeyMode,
  InputKind,
  LimitKind,
  MouseButton,
  PauseCause,
  PressMode,
  RateUnit,
  RunState,
  StopCode,
} from "../../bindings/github.com/StephenSHorton/toru/internal/clicker/models.js";
import {
  CadenceMode,
  InputKind,
  LimitKind,
  MouseButton,
  PressMode,
  RateUnit,
} from "../../bindings/github.com/StephenSHorton/toru/internal/clicker/models.js";

export const UNIT_MS: Record<RateUnit, number> = {
  [RateUnit.$zero]: 1000,
  [RateUnit.UnitSecond]: 1000,
  [RateUnit.UnitMinute]: 60_000,
  [RateUnit.UnitHour]: 3_600_000,
  [RateUnit.UnitDay]: 86_400_000,
};

export const UNIT_WORD: Record<RateUnit, string> = {
  [RateUnit.$zero]: "second",
  [RateUnit.UnitSecond]: "second",
  [RateUnit.UnitMinute]: "minute",
  [RateUnit.UnitHour]: "hour",
  [RateUnit.UnitDay]: "day",
};

export const UNIT_SHORT: Record<RateUnit, string> = {
  [RateUnit.$zero]: "s",
  [RateUnit.UnitSecond]: "s",
  [RateUnit.UnitMinute]: "m",
  [RateUnit.UnitHour]: "h",
  [RateUnit.UnitDay]: "d",
};

export const RATE_UNITS = [RateUnit.UnitSecond, RateUnit.UnitMinute, RateUnit.UnitHour, RateUnit.UnitDay] as const;

/** Double click is switched off by Go at this rate and above. */
export const DOUBLE_CLICK_MAX_CPS = 11;

/**
 * maxCps is the rate ceiling: 500, or 1000 with HighRate. Like Go's
 * rateCeiling it caps the rate field in every unit (500/s, 500/m, 500/h).
 */
export function maxCps(highRate: boolean): number {
  return highRate ? 1000 : 500;
}

/** minIntervalMs is the shortest period the ceiling allows (2 ms, or 1 ms). */
export function minIntervalMs(highRate: boolean): number {
  return 1000 / maxCps(highRate);
}

/** intervalOf is the period in ms the cadence's edited form describes. */
export function intervalOf(c: { mode: CadenceMode; rate: number; unit: RateUnit; intervalMs: number }): number {
  if (c.mode === CadenceMode.CadenceInterval) return c.intervalMs;
  return c.rate > 0 ? UNIT_MS[c.unit] / c.rate : 0;
}

/** cpsOf is the effective clicks per second of a cadence. */
export function cpsOf(c: { mode: CadenceMode; rate: number; unit: RateUnit; intervalMs: number }): number {
  const ms = intervalOf(c);
  return ms > 0 ? 1000 / ms : 0;
}

/**
 * rateForInterval previews the rate form of a period, the way Go's
 * rateForInterval stores it: each unit is tried with its rate rounded to a
 * whole number and held to 1..maxCps (the ceiling is the same number in every
 * unit, like the rate field allows), and the one with the smallest timing
 * error wins, ties to the shorter unit (40 ms -> 25/s, 33 ms -> 30/s,
 * 90 s -> 40/h).
 */
export function rateForInterval(ms: number, highRate: boolean): { rate: number; unit: RateUnit } {
  let best = { rate: 1, unit: RateUnit.UnitSecond as RateUnit, err: Infinity };
  if (!(ms > 0)) return best;
  const ceiling = maxCps(highRate);
  for (const unit of RATE_UNITS) {
    const rate = Math.min(ceiling, Math.max(1, Math.round(UNIT_MS[unit] / ms)));
    const err = Math.abs(UNIT_MS[unit] / rate - ms);
    if (err < best.err) best = { rate, unit, err };
  }
  return { rate: best.rate, unit: best.unit };
}

export interface Hms {
  h: number;
  m: number;
  s: number;
  ms: number;
}

/** splitMs breaks a period into h/m/s/ms, rounded to the nearest ms. */
export function splitMs(total: number): Hms {
  let t = Math.max(0, Math.round(total));
  const h = Math.floor(t / 3_600_000);
  t -= h * 3_600_000;
  const m = Math.floor(t / 60_000);
  t -= m * 60_000;
  const s = Math.floor(t / 1000);
  return { h, m, s, ms: t - s * 1000 };
}

/** joinMs is splitMs's inverse; fields past their range carry (75 s = 1 m 15 s). */
export function joinMs(p: Hms): number {
  return p.h * 3_600_000 + p.m * 60_000 + p.s * 1000 + p.ms;
}

function trim(n: number, digits = 2): string {
  return String(Math.round(n * 10 ** digits) / 10 ** digits);
}

/** formatRate: "25 clicks per second", "1 click per minute", "2.5 clicks per second". */
export function formatRate(rate: number, unit: RateUnit): string {
  const r = trim(rate);
  return `${Number(r).toLocaleString()} ${r === "1" ? "click" : "clicks"} per ${UNIT_WORD[unit]}`;
}

/** formatInterval: "40 ms", "1 m 15 s", "2 h 5 m", "1.5 ms". */
export function formatInterval(ms: number): string {
  if (!(ms > 0)) return "-";
  if (ms < 1000) return `${trim(ms)} ms`;
  const p = splitMs(ms);
  const parts: string[] = [];
  if (p.h) parts.push(`${p.h} h`);
  if (p.m) parts.push(`${p.m} m`);
  if (p.s) parts.push(`${p.s} s`);
  if (p.ms) parts.push(`${p.ms} ms`);
  return parts.join(" ");
}

/** formatElapsed renders a run clock: "0:07", "12:30", "1:02:03". */
export function formatElapsed(ms: number): string {
  const total = Math.max(0, Math.floor(ms / 1000));
  const h = Math.floor(total / 3600);
  const m = Math.floor((total % 3600) / 60);
  const s = total % 60;
  const ss = String(s).padStart(2, "0");
  return h > 0 ? `${h}:${String(m).padStart(2, "0")}:${ss}` : `${m}:${ss}`;
}

/** formatSeconds renders a time limit or lifetime total: "45s", "5m", "1h 30m", "2d 3h". */
export function formatSeconds(sec: number): string {
  const t = Math.max(0, Math.round(sec));
  if (t < 60) return `${t}s`;
  const d = Math.floor(t / 86_400);
  const h = Math.floor((t % 86_400) / 3600);
  const m = Math.floor((t % 3600) / 60);
  const s = t % 60;
  if (d) return h ? `${d}d ${h}h` : `${d}d`;
  if (h) return m ? `${h}h ${m}m` : `${h}h`;
  return s ? `${m}m ${s}s` : `${m}m`;
}

export function formatCount(n: number): string {
  return Math.round(n).toLocaleString();
}

export const BUTTON_LABEL: Record<MouseButton, string> = {
  [MouseButton.$zero]: "Left",
  [MouseButton.ButtonLeft]: "Left",
  [MouseButton.ButtonRight]: "Right",
  [MouseButton.ButtonMiddle]: "Middle",
  [MouseButton.ButtonX1]: "Back (X1)",
  [MouseButton.ButtonX2]: "Forward (X2)",
};

/** isLetterKey reports whether a Profile.Key is A-Z, the only keys with a case. */
export function isLetterKey(key: string): boolean {
  return /^[A-Za-z]$/.test(key);
}

/** keyLabel shows the auto-press key the way it will be typed ("a" or "A"). */
export function keyLabel(key: string, uppercase: boolean): string {
  if (!key) return "";
  return isLetterKey(key) ? (uppercase ? key.toUpperCase() : key.toLowerCase()) : key;
}

/** presetSummary is a preset's one-line description: "25/s Left | Limit 5,000 | 3 pts". */
export function presetSummary(p: ClickerProfile): string {
  const c = p.cadence;
  const parts: string[] = [];
  const keyHold = p.input === InputKind.InputKeyboard && p.press === PressMode.PressHold;
  if (!keyHold) {
    parts.push(
      c.mode === CadenceMode.CadenceInterval
        ? `every ${formatInterval(c.intervalMs)}`
        : `${trim(c.rate)}/${UNIT_SHORT[c.unit]}`,
    );
  }
  const what =
    p.input === InputKind.InputKeyboard ? `Key ${keyLabel(p.key, p.uppercase) || "(none)"}` : BUTTON_LABEL[p.button];
  parts.push(p.press === PressMode.PressHold ? `${what} hold` : p.doubleClick ? `${what} double` : what);
  if (p.limit.kind === LimitKind.LimitClicks) parts.push(`Limit ${formatCount(p.limit.clicks)}`);
  if (p.limit.kind === LimitKind.LimitTime) parts.push(`Time ${formatSeconds(p.limit.seconds)}`);
  if (p.clickPoints.enabled && p.clickPoints.points.length > 0) {
    const n = p.clickPoints.points.length;
    parts.push(`${n} ${n === 1 ? "pt" : "pts"}`);
  }
  return parts.join(" | ");
}

// stable serializes with sorted keys, so two profiles compare equal whatever
// order their fields were built in (Go JSON vs a local spread).
function stable(v: unknown): string {
  if (Array.isArray(v)) return `[${v.map(stable).join(",")}]`;
  if (v && typeof v === "object") {
    const o = v as Record<string, unknown>;
    return `{${Object.keys(o)
      .sort()
      .map((k) => `${JSON.stringify(k)}:${stable(o[k])}`)
      .join(",")}}`;
  }
  return JSON.stringify(v ?? null);
}

/** sameProfile is a deep compare, for the preset "Modified" badge. */
export function sameProfile(a: ClickerProfile, b: ClickerProfile): boolean {
  return stable(a) === stable(b);
}

export interface ClickerError {
  code: string; // "" when the error did not come from ClickerService
  message: string;
}

/**
 * parseError reads a rejected ClickerService call. Go errors look like
 * "clicker: CODE: message" (the message is already user-facing) once
 * errorMessage has unwrapped the transport's JSON; anything else is shown as
 * is.
 */
export function parseError(e: unknown): ClickerError {
  const raw = errorMessage(e);
  const m = /clicker:\s*([A-Z_]+):\s*([\s\S]*)$/.exec(raw);
  if (m) return { code: m[1], message: m[2].trim() || m[1] };
  return { code: "", message: raw || "Something went wrong" };
}
