import { useSyncExternalStore } from "react";
// The Go service, imported lazily so this module (and the text renderer)
// loads without the Wails runtime (scripts/text-style-check.ts).
const videoService = () => import("@/lib/api").then((m) => m.VideoService);

// fonts.ts is the installed fonts a text overlay can use (Go:
// internal/vid/fonts.go). A text stores a font's name; the preview draws it
// from the same file the export uses (served at Font.url, registered here
// with the FontFace API), so the browser can't swap in a different face or
// synthesise a weight. A text whose font isn't installed falls back to the
// default, as the export does.
//
// Vertical placement: drawtext advances lines by the font's hhea line
// height and puts each baseline the hhea ascender (rounded up to a pixel)
// below the line top; the browser uses its own ascent/descent (each rounded)
// and centres them in the line. baselineShift is the difference, so the
// preview can move the text to where the export draws it. Measured against
// real exports of every installed font: 0.4px mean error at 100px.

export type FontInfo = {
  name: string;
  family: string;
  url: string;
  lineHeight: number;
  unitsPerEm: number;
  ascent: number;
  cssAscent: number;
  cssDescent: number;
};

// The default face (Go: DefaultTextFont), drawn with the system's Segoe UI.
// It's also an installed font; the picker shows it once, as the default.
export const DEFAULT_FONT_LABEL = "Segoe UI Semibold";
export const DEFAULT_FONT_CSS = '"Segoe UI", sans-serif';
export const DEFAULT_LINE_HEIGHT = 1.33;

let fonts: FontInfo[] | null = null;
let byName = new Map<string, FontInfo>();
let loading: Promise<FontInfo[]> | null = null;
let failed = false;
let version = 0;
const listeners = new Set<() => void>();
const bump = () => {
  version++;
  listeners.forEach((l) => l());
};

// loadFonts fetches the installed fonts once (refresh re-reads them, after
// the user installs one).
export function loadFonts(refresh = false): Promise<FontInfo[]> {
  if (loading && !refresh) return loading;
  loading = videoService()
    .then((vs) => vs.ListFonts(refresh))
    .then((list) => {
      failed = false;
      fonts = (list ?? []) as FontInfo[];
      byName = new Map(fonts.map((f) => [f.name, f]));
      bump();
      return fonts;
    })
    .catch((err) => {
      // Kept as the result (no retry per render: the overlay re-renders every
      // frame while playing); the picker's reload button tries again.
      failed = true;
      fonts = [];
      byName = new Map();
      bump();
      throw err;
    });
  return loading;
}

// useFonts is the installed fonts (null until loaded); it also re-renders
// when a font file finishes loading.
export function useFonts(): FontInfo[] | null {
  useSyncExternalStore(
    (l) => {
      listeners.add(l);
      return () => listeners.delete(l);
    },
    () => version,
  );
  if (!fonts && !loading) void loadFonts().catch(() => {});
  return fonts;
}

// useFontsVersion changes whenever the font list or a font file loads.
export function useFontsVersion(): number {
  useFonts();
  return version;
}

// fontsFailed reports that the last attempt to list the fonts failed.
export const fontsFailed = () => failed;

// fontInfo is the installed font called name; undefined for the default
// ("" or unset), a font this PC doesn't have, or before the list loads.
export function fontInfo(name: string | undefined): FontInfo | undefined {
  return name ? byName.get(name) : undefined;
}

// fontFamily registers f's file as a CSS font family (once) and returns it.
const registered = new Map<string, string>();
export function fontFamily(f: FontInfo): string {
  const known = registered.get(f.name);
  if (known) return known;
  const fam = `toru-font-${registered.size}`;
  const css = `"${fam}", ${DEFAULT_FONT_CSS}`;
  registered.set(f.name, css);
  try {
    const face = new FontFace(fam, `url("${f.url}")`);
    document.fonts.add(face);
    face.load().then(bump, () => {});
  } catch {
    // An unparsable URL leaves the family unloaded; the text draws in the fallback.
  }
  return css;
}

// baselineShift is how far (output px, + = down) the preview moves a text in
// f at px so its lines sit where drawtext puts them.
export function baselineShift(f: FontInfo, px: number): number {
  const s = px / f.unitsPerEm;
  const a = Math.round(f.cssAscent * s);
  const d = Math.round(f.cssDescent * s);
  const line = Math.round(f.lineHeight * px);
  return Math.ceil(f.ascent * s) - ((line - (a + d)) / 2 + a);
}

// checkFont is "" when the export can draw in the font, else why not (a few
// fonts load but fail in ffmpeg). Cached per name.
const checks = new Map<string, Promise<string>>();
export function checkFont(name: string): Promise<string> {
  let p = checks.get(name);
  if (!p) {
    p = videoService()
      .then((vs) => vs.CheckFont(name))
      .catch((err) => {
      checks.delete(name);
      return String(err);
    });
    checks.set(name, p);
  }
  return p;
}
