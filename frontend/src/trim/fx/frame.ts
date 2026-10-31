import { useEffect, useState } from "react";
import { correctPixels, exactFrame, matrixFix } from "@/trim/colorspace";
import type { Media } from "@/trim/project";

// frame.ts grabs one source frame of a clip (the frame at the playhead) as a
// small canvas, for the inspector's live thumbnails (the effect browser and
// the filter tiles render it through each effect with renderThumb). It uses
// its own hidden <video>, so it never disturbs playback; the grab follows
// the playhead a moment after it settles. Video frames are decoded with the
// source's colour matrix, as the export does (colorspace.ts).

const THUMB_H = 180; // px; tiles draw at about half of this
const SETTLE_MS = 250;

// useClipFrame is media's frame at source time srcT, or null while loading
// (and for audio).
export function useClipFrame(media: Media | undefined, srcT: number): HTMLCanvasElement | null {
  const [frame, setFrame] = useState<HTMLCanvasElement | null>(null);
  const url = media?.kind === "audio" ? "" : (media?.url ?? "");
  const kind = media?.kind;
  // Quarter-second steps: close enough for a thumbnail, and scrubbing
  // doesn't re-render every tile on every frame.
  const at = Math.round(Math.max(0, srcT) * 4) / 4;

  useEffect(() => {
    if (!url) {
      setFrame(null);
      return;
    }
    let live = true;
    let el: HTMLVideoElement | HTMLImageElement | null = null;
    const grab = async (src: HTMLVideoElement | HTMLImageElement, w: number, h: number) => {
      if (!live || !w || !h) return;
      const c = document.createElement("canvas");
      c.height = Math.min(THUMB_H, h);
      c.width = Math.max(1, Math.round((w * c.height) / h));
      const g = c.getContext("2d", { willReadFrequently: true })!;
      const fix = src instanceof HTMLVideoElement ? matrixFix(media, h) : null;
      const vf = fix && src instanceof HTMLVideoElement ? await exactFrame(src, fix.to) : null;
      if (!live) {
        vf?.close();
        return;
      }
      g.drawImage(vf ?? src, 0, 0, c.width, c.height);
      vf?.close();
      if (fix && !vf) {
        const px = g.getImageData(0, 0, c.width, c.height);
        correctPixels(px.data, fix);
        g.putImageData(px, 0, 0);
      }
      setFrame(c);
    };
    const timer = window.setTimeout(() => {
      if (kind === "image") {
        const img = new Image();
        el = img;
        img.onload = () => void grab(img, img.naturalWidth, img.naturalHeight);
        img.src = url;
        return;
      }
      const v = document.createElement("video");
      el = v;
      v.muted = true;
      v.preload = "auto";
      v.onloadedmetadata = () => {
        v.currentTime = Math.min(at, Math.max(0, v.duration - 0.05));
      };
      v.onseeked = () => void grab(v, v.videoWidth, v.videoHeight);
      v.src = url;
    }, SETTLE_MS);
    return () => {
      live = false;
      window.clearTimeout(timer);
      if (el instanceof HTMLVideoElement) {
        el.removeAttribute("src");
        el.load();
      }
    };
  }, [url, kind, at]);

  return frame;
}
