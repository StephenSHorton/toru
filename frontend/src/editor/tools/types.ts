// Tool interface — VERBATIM CONTRACT (per BUILD SPEC §2.3). Every tool file
// exports a singleton object literal implementing `Tool`. EditorCanvas resolves
// TOOLS[activeTool] and forwards Stage pointer events with a freshly-built
// ToolContext per event.
//
// Event types are exact:
//   pointer handlers -> Konva.KonvaEventObject<PointerEvent>
//   click/dblclick   -> Konva.KonvaEventObject<MouseEvent>
//   transform        -> Konva.KonvaEventObject<Event>
//   drag             -> Konva.KonvaEventObject<DragEvent>

import type Konva from 'konva';
import type { EditorState } from '../store';

export interface ToolContext {
  store: EditorState;                                // get() snapshot at event time
  stage: Konva.Stage;
  getPointer(): { x: number; y: number } | null;     // image-space (offset/scale corrected)
}

export interface Tool {
  id: import('../types').ToolId;
  cursor: string;                                    // CSS cursor for the Stage container
  onPointerDown(e: Konva.KonvaEventObject<PointerEvent>, ctx: ToolContext): void;
  onPointerMove(e: Konva.KonvaEventObject<PointerEvent>, ctx: ToolContext): void;
  onPointerUp(e: Konva.KonvaEventObject<PointerEvent>, ctx: ToolContext): void;
}
