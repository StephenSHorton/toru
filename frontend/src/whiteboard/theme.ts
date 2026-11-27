// The whiteboard's Light/Dark theme (docs/whiteboard.md section 4). The
// colours are toru-ui-sdk.css's two --ui-* token sets, picked by
// <html data-theme>. The choice is Go's (whiteboard_theme.go): one for every
// board, saved in %AppData%\toru\whiteboard.json, and the theme the PNG, PDF
// and HTML exports are drawn in. Light is the default.
//
// Go puts the saved theme in the window's address (&theme=), so the first
// paint is right; the window then asks Go, and follows whiteboard:theme when
// another board window changes it.
import { useEffect, useState, useSyncExternalStore } from "react";

export type BoardTheme = "light" | "dark";

export const boardThemes: { theme: BoardTheme; title: string }[] = [
  { theme: "light", title: "Light" },
  { theme: "dark", title: "Dark" },
];

export function parseTheme(s: unknown): BoardTheme | null {
  return s === "light" || s === "dark" ? s : null;
}

let current: BoardTheme = "light";
const listeners = new Set<() => void>();

/** Shows theme on this page: <html data-theme>. */
export function applyTheme(theme: BoardTheme) {
  current = theme;
  if (typeof document !== "undefined") {
    document.documentElement.dataset.theme = theme;
    // main.tsx's pre-paint canvas colour: the theme's stylesheet has it now.
    document.body?.style.removeProperty("background");
  }
  for (const f of listeners) f();
}

/** The theme on this page. */
export function boardTheme(): BoardTheme {
  return current;
}

function subscribe(f: () => void) {
  listeners.add(f);
  return () => listeners.delete(f);
}

/** The theme on this page, as React state. */
export function useBoardTheme(): BoardTheme {
  return useSyncExternalStore(subscribe, boardTheme, boardTheme);
}

const service = () => import("../../bindings/github.com/StephenSHorton/toru/whiteboardsservice.js");

/**
 * The board window's theme: the address's first (Go's saved theme), then Go's
 * own answer, then whatever another board window switches to.
 */
export function useWindowTheme() {
  // Before the first paint.
  useState(() => applyTheme(parseTheme(new URLSearchParams(location.search).get("theme")) ?? "light"));
  useEffect(() => {
    let live = true;
    let off = () => {};
    void (async () => {
      try {
        const saved = parseTheme(await (await service()).Theme());
        if (live && saved) applyTheme(saved);
        const { Events } = await import("@wailsio/runtime");
        if (!live) return;
        off = Events.On("whiteboard:theme", (ev: { data: unknown }) => {
          const t = parseTheme(ev.data);
          if (t) applyTheme(t);
        });
      } catch {
        // no Go (a check, or Vite alone): the address's theme stands
      }
    })();
    return () => {
      live = false;
      off();
    };
  }, []);
}

/** Switches the board window's theme and saves it in Go. */
export async function setBoardTheme(theme: BoardTheme) {
  applyTheme(theme);
  try {
    await (await service()).SetTheme(theme);
  } catch {
    // no Go: the switch still shows, unsaved
  }
}
