// Screenshot annotation editor — standalone Wails window (opened after every
// capture). The capture overlay is dismissed first; this is the only annotation
// surface.
//
// Window chrome is sized around the image + docked toolbar:
//   • ≥ 40px padding around the capture (all sides relative to the content stack)
//   • ≥ 40px between image and toolbar, and below the toolbar
//   • width at least wide enough for the full toolbar (+ side padding)
//   • max size ~95% of the work area so huge captures scale down
//
// Done saves the annotated PNG to the Toru library and closes the window.
// Esc with nothing selected does the same (useEditorKeyboard onEscapeEmpty).

import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import type Konva from "konva";
import { Window } from "@wailsio/runtime";
import { EditorCanvas, STAGE_W, STAGE_H } from "@/editor/EditorCanvas";
import { Toolbar } from "@/editor/Toolbar";
import { useEditorStore } from "@/editor/store";
import { useEditorKeyboard } from "@/editor/useEditorKeyboard";
import { useClipboardPaste } from "@/editor/useClipboardPaste";
import { TextEditingOverlay } from "@/editor/tools/text";
import { CropOverlay } from "@/editor/tools/crop";
import { setStageSize } from "@/editor/viewStore";
import { saveToLibrary } from "@/editor/exportActions";
import { WindowsService } from "@/lib/api";

/** Padding between window edge ↔ capture, capture ↔ toolbar, toolbar ↔ edge. */
const PAD = 40;
/** Fallback toolbar size before first measure (icon row + labels). */
const TOOLBAR_FALLBACK_W = 820;
const TOOLBAR_FALLBACK_H = 48;

export default function Editor() {
  const stageRef = useRef<Konva.Stage | null>(null);
  const toolbarRef = useRef<HTMLDivElement | null>(null);
  const loadBaseImage = useEditorStore((s) => s.loadBaseImage);
  const imgNatural = useRef({ w: STAGE_W, h: STAGE_H });
  const sizedOnce = useRef(false);

  const imgPath = new URLSearchParams(window.location.search).get("img") ?? "";
  const [src] = useState(imgPath || "/sample.png");
  const [stageBox, setStageBox] = useState({ w: STAGE_W, h: STAGE_H });
  const [ready, setReady] = useState(false);

  const finish = useCallback(async () => {
    const stage = stageRef.current;
    if (stage) {
      try {
        await saveToLibrary(stage);
      } catch {
        // Still close on library failure so the user is never stuck.
      }
    }
    try {
      await Window.Close();
    } catch {
      // Dev browser has no Window.Close — ignore.
    }
  }, []);

  useEditorKeyboard(true, () => void finish());
  useClipboardPaste();

  // Load image; stage/window sizing runs after toolbar is measurable.
  useEffect(() => {
    const img = new window.Image();
    img.crossOrigin = "anonymous";
    img.onload = () => {
      imgNatural.current = {
        w: img.naturalWidth || STAGE_W,
        h: img.naturalHeight || STAGE_H,
      };
      loadBaseImage(src, imgNatural.current.w, imgNatural.current.h);
      setReady(true);
    };
    img.src = src;
  }, [src, loadBaseImage]);

  // After image + toolbar paint: fit stage into max work area, size the Wails
  // window to content with PAD chrome, re-center.
  useLayoutEffect(() => {
    if (!ready) return;

    const apply = () => {
      const bar = toolbarRef.current;
      const barW = Math.max(bar?.offsetWidth ?? 0, TOOLBAR_FALLBACK_W);
      const barH = Math.max(bar?.offsetHeight ?? 0, TOOLBAR_FALLBACK_H);

      const screenW = window.screen?.availWidth || 1600;
      const screenH = window.screen?.availHeight || 1000;
      const maxWinW = Math.round(screenW * 0.95);
      const maxWinH = Math.round(screenH * 0.92);

      // Content stack vertically: PAD | stage | PAD | toolbar | PAD
      // Horizontally: PAD | max(stage, toolbar) | PAD
      const maxStageW = Math.max(160, maxWinW - 2 * PAD);
      const maxStageH = Math.max(120, maxWinH - 3 * PAD - barH);

      const nw = imgNatural.current.w;
      const nh = imgNatural.current.h;
      const scale = Math.min(maxStageW / nw, maxStageH / nh, 1);
      const sw = Math.max(1, Math.round(nw * scale));
      const sh = Math.max(1, Math.round(nh * scale));

      const contentW = Math.max(sw, barW);
      const winW = Math.min(maxWinW, contentW + 2 * PAD);
      const winH = Math.min(maxWinH, PAD + sh + PAD + barH + PAD);

      setStageSize(sw, sh);
      setStageBox({ w: sw, h: sh });

      void (async () => {
        try {
          await Window.SetSize(winW, winH);
          await Window.Center();
        } catch {
          /* dev browser — layout still applies inside current frame */
        }
        sizedOnce.current = true;
      })();
    };

    // Double rAF: toolbar is in the tree; first frame may still be 0×0.
    let raf2 = 0;
    const raf1 = requestAnimationFrame(() => {
      raf2 = requestAnimationFrame(apply);
    });
    return () => {
      cancelAnimationFrame(raf1);
      cancelAnimationFrame(raf2);
    };
  }, [ready]);

  return (
    <div
      className="flex h-full w-full flex-col overflow-hidden bg-background"
      style={{ padding: PAD }}
    >
      <div className="flex min-h-0 flex-1 items-center justify-center">
        <div
          className="relative border border-border shadow-lg"
          style={{ width: stageBox.w, height: stageBox.h }}
        >
          <EditorCanvas stageRef={stageRef} />
          <CropOverlay />
          <TextEditingOverlay stageRef={stageRef} />
        </div>
      </div>

      {/* Gap between capture and toolbar = PAD (marginTop); bottom pad is parent padding. */}
      <div className="flex shrink-0 justify-center" style={{ marginTop: PAD }}>
        <Toolbar
          stageRef={stageRef}
          docked
          barRef={toolbarRef}
          onNewCapture={() => void WindowsService.OpenOverlay()}
          onDone={finish}
        />
      </div>
    </div>
  );
}
