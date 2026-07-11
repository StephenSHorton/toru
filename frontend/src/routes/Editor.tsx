// Screenshot annotation editor — standalone Wails window (opened after every
// capture: region, window, full screen, or straddle). The capture overlay is
// dismissed first; this is the only annotation surface.
//
// Done saves the annotated PNG to the Toru library and closes the window.
// Esc with nothing selected does the same (useEditorKeyboard onEscapeEmpty).

import { useCallback, useEffect, useRef, useState } from "react";
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

export default function Editor() {
  const stageRef = useRef<Konva.Stage | null>(null);
  const loadBaseImage = useEditorStore((s) => s.loadBaseImage);

  const imgPath = new URLSearchParams(window.location.search).get("img") ?? "";
  const [src] = useState(imgPath || "/sample.png");
  const [stageBox, setStageBox] = useState({ w: STAGE_W, h: STAGE_H });

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

  // Load the base image once: size the stage to fit the image in the window,
  // then seed the scene graph.
  useEffect(() => {
    const img = new window.Image();
    img.crossOrigin = "anonymous";
    img.onload = () => {
      const padX = 48;
      const padY = 120; // room for floating toolbar + chrome
      const maxW = Math.max(320, window.innerWidth - padX);
      const maxH = Math.max(240, window.innerHeight - padY);
      const nw = img.naturalWidth || STAGE_W;
      const nh = img.naturalHeight || STAGE_H;
      const scale = Math.min(maxW / nw, maxH / nh, 1);
      const sw = Math.max(1, Math.round(nw * scale));
      const sh = Math.max(1, Math.round(nh * scale));
      setStageSize(sw, sh);
      setStageBox({ w: sw, h: sh });
      loadBaseImage(src, nw, nh);
    };
    img.src = src;
  }, [src, loadBaseImage]);

  return (
    <div className="relative h-full w-full overflow-hidden bg-background">
      <div className="flex h-full w-full items-center justify-center p-4">
        <div
          className="relative border border-border shadow-lg"
          style={{ width: stageBox.w, height: stageBox.h }}
        >
          <EditorCanvas stageRef={stageRef} />
          <CropOverlay />
          <TextEditingOverlay stageRef={stageRef} />
        </div>
      </div>

      <Toolbar
        stageRef={stageRef}
        onNewCapture={() => void WindowsService.OpenOverlay()}
        onDone={finish}
      />
    </div>
  );
}
