// DEVELOPER 1 — screenshot annotation editor route.
//
// Composes the frosted Toolbar over the Konva EditorCanvas, both driven by the
// Zustand store (the single source of truth). The base image is loaded from the
// ?img=<path> query param, falling back to /sample.png in dev. Global keyboard
// shortcuts and the clipboard-paste listener are mounted here.
//
// FOUNDATION: only the SELECT tool is wired. Tool-builders add tools by dropping
// a Tool into the TOOLS registry (see editor/tools/index.ts) — no changes here.

import { useEffect, useRef, useState } from 'react';
import type Konva from 'konva';
import { EditorCanvas, STAGE_W, STAGE_H } from '@/editor/EditorCanvas';
import { Toolbar } from '@/editor/Toolbar';
import { useEditorStore } from '@/editor/store';
import { useEditorKeyboard } from '@/editor/useEditorKeyboard';
import { useClipboardPaste } from '@/editor/useClipboardPaste';
import { TextEditingOverlay } from '@/editor/tools/text';
import { CropOverlay } from '@/editor/tools/crop';

export default function Editor() {
  const stageRef = useRef<Konva.Stage>(null);
  const loadBaseImage = useEditorStore((s) => s.loadBaseImage);

  const imgPath = new URLSearchParams(window.location.search).get('img') ?? '';
  const [src] = useState(imgPath || '/sample.png');

  useEditorKeyboard();
  const pasteFromBackend = useClipboardPaste();

  // Load the base image once: read natural size, then seed the scene graph.
  useEffect(() => {
    const img = new window.Image();
    img.crossOrigin = 'anonymous';
    img.onload = () => loadBaseImage(src, img.naturalWidth, img.naturalHeight);
    img.src = src;
  }, [src, loadBaseImage]);

  return (
    <div className="flex h-full flex-col">
      <Toolbar stageRef={stageRef} onPaste={pasteFromBackend} />

      <div className="flex flex-1 items-center justify-center p-4">
        <div className="relative border" style={{ width: STAGE_W, height: STAGE_H }}>
          <EditorCanvas stageRef={stageRef} />
          <CropOverlay />
          <TextEditingOverlay stageRef={stageRef} />
        </div>
      </div>

      <div className="px-3 pb-2 text-[11px] text-muted-foreground">
        source:{' '}
        <span className="font-mono">{imgPath || '(dev sample.png)'}</span>
      </div>
    </div>
  );
}
