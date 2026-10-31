import { useEffect, useRef } from "react";
import { cn } from "@/lib/utils";
import { renderThumb } from "@/trim/fx/thumb";
import type { EffectInstance } from "@/trim/project";

// Thumb is a live thumbnail: the clip's current frame drawn through an
// effect stack by the preview renderer (fx/thumb.ts). Tiles render one per
// animation frame from a shared queue, so opening a tab full of tiles never
// stalls the player.

const W = 128;
const H = 72;

type Job = { run: () => void };
const queue: Job[] = [];
let scheduled = false;

function pump() {
  scheduled = false;
  const start = performance.now();
  while (queue.length && performance.now() - start < 6) queue.shift()!.run();
  if (queue.length) schedule();
}

function schedule() {
  if (scheduled) return;
  scheduled = true;
  requestAnimationFrame(pump);
}

export function Thumb({ frame, stack, className }: { frame: HTMLCanvasElement | null; stack: EffectInstance[]; className?: string }) {
  const ref = useRef<HTMLCanvasElement>(null);
  const sig = JSON.stringify(stack);
  useEffect(() => {
    const cv = ref.current;
    if (!cv || !frame) return;
    const h = H;
    const w = Math.round((frame.width / frame.height) * h) || W;
    const job: Job = {
      run: () => {
        try {
          const bmp = renderThumb(frame, JSON.parse(sig) as EffectInstance[], w, h);
          cv.width = bmp.width;
          cv.height = bmp.height;
          cv.getContext("2d")!.drawImage(bmp, 0, 0);
          bmp.close();
        } catch {
          // no WebGL2: the tile stays blank
        }
      },
    };
    queue.push(job);
    schedule();
    return () => {
      const i = queue.indexOf(job);
      if (i >= 0) queue.splice(i, 1);
    };
  }, [frame, sig]);
  return <canvas ref={ref} width={W} height={H} className={cn("aspect-video w-full bg-black/60 object-cover", className)} />;
}
