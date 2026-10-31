import { useEffect, useLayoutEffect, useRef, useState, type RefObject } from "react";
import type { Clip, Media, Track } from "@/trim/project";
import { columnHeights, loadPeaks, type Peaks } from "@/trim/waveform";

// ClipWave draws a clip's waveform on a canvas inside its timeline box. The
// canvas only covers the part of the clip on screen (plus a margin), so an
// hours-long clip zoomed in costs no more than the timeline's width; it
// redraws itself on scroll without a React render.

const MARGIN = 64; // px drawn past each side of the view

export function ClipWave({
  clip,
  media,
  track,
  left,
  width,
  pxPerSec,
  scroller,
  color,
  dim,
  top = 14,
}: {
  clip: Clip;
  media: Media | undefined;
  track: Track;
  left: number; // the clip's x in the scrolled content
  width: number;
  pxPerSec: number;
  scroller: RefObject<HTMLDivElement | null>;
  color: string;
  dim: boolean;
  top?: number; // px left clear for the clip's label
}) {
  const [peaks, setPeaks] = useState<Peaks | null>(null);
  const canvas = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    if (!media) return;
    let live = true;
    loadPeaks(media)
      .then((p) => live && setPeaks(p))
      .catch(() => live && setPeaks(null));
    return () => {
      live = false;
    };
  }, [media]);

  // draw reads the scroll position itself, so scrolling only redraws.
  const draw = useRef<() => void>(() => {});
  draw.current = () => {
    const cv = canvas.current;
    const el = scroller.current;
    if (!cv || !el) return;
    if (!peaks || pxPerSec <= 0) {
      cv.style.display = "none";
      return;
    }
    const x0 = Math.max(0, Math.floor(el.scrollLeft - left - MARGIN));
    const x1 = Math.min(Math.ceil(width), Math.ceil(el.scrollLeft + el.clientWidth - left + MARGIN));
    const w = x1 - x0;
    const h = cv.parentElement?.clientHeight ?? 0;
    if (w <= 0 || h <= 0) {
      cv.style.display = "none";
      return;
    }
    const dpr = window.devicePixelRatio || 1;
    cv.style.display = "block";
    cv.style.left = `${x0}px`;
    cv.style.width = `${w}px`;
    cv.style.height = `${h}px`;
    const pw = Math.round(w * dpr);
    const ph = Math.round(h * dpr);
    if (cv.width !== pw) cv.width = pw;
    if (cv.height !== ph) cv.height = ph;
    const g = cv.getContext("2d");
    if (!g) return;
    g.setTransform(1, 0, 0, 1, 0, 0);
    g.clearRect(0, 0, pw, ph);
    const cols = new Float32Array(pw);
    columnHeights(cols, peaks, clip, track, x0 * dpr, pxPerSec * dpr);
    // Mirrored about the middle, below the label.
    const y0 = Math.round(top * dpr);
    const mid = (y0 + ph) / 2;
    const half = (ph - y0) / 2 - dpr;
    g.fillStyle = dim ? "rgb(160 160 160 / 0.45)" : color;
    g.beginPath();
    for (let i = 0; i < pw; i++) {
      const a = Math.max(0.5, cols[i] * half);
      g.rect(i, mid - a, 1, a * 2);
    }
    g.fill();
  };

  useLayoutEffect(() => draw.current());

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    let raf = 0;
    const onScroll = () => {
      if (raf) return;
      raf = requestAnimationFrame(() => {
        raf = 0;
        draw.current();
      });
    };
    el.addEventListener("scroll", onScroll, { passive: true });
    return () => {
      el.removeEventListener("scroll", onScroll);
      if (raf) cancelAnimationFrame(raf);
    };
  }, [scroller]);

  if (!media?.hasAudio || media.kind === "image") return null;
  return <canvas ref={canvas} className="pointer-events-none absolute top-0" style={{ display: "none" }} aria-hidden="true" />;
}
