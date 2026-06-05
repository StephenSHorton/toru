// TOOLS registry barrel — the single map EditorCanvas resolves activeTool from.
//
// HOW TO REGISTER A TOOL (for downstream tool-builders):
//   1. Create `frontend/src/editor/tools/<name>Tool.ts` exporting a singleton
//      object literal implementing `Tool` (see ./types.ts and ./selectTool.ts).
//      It needs an `id` (a ToolId), a `cursor`, and onPointerDown/Move/Up.
//   2. Import it here and replace the corresponding `noop('<id>')` entry in the
//      TOOLS map below with your tool. Example:
//        import { penTool } from './penTool';
//        ...
//        pen: penTool,   // was: pen: noop('pen'),
//   3. That's it — the Toolbar reads tool buttons from the registry-driven list
//      in Toolbar.tsx, and EditorCanvas forwards Stage pointer events to
//      TOOLS[activeTool] with a fresh ToolContext.
//
// Every ToolId now maps to a real, functional Tool. (The foundation shipped with
// inert `noop` placeholders for every tool but `select`; Integrate replaced each
// one with its implementation below.)

import type { Tool } from './types';
import type { ToolId } from '../types';
import { selectTool } from './selectTool';
import { penTool } from './pen';
import { rectTool, ellipseTool } from './shapes';
import { arrowTool, lineTool } from './arrowline';
import { textTool } from './text';
import { emojiTool } from './emoji';
import { pasteImageTool } from './pasteImage';
import { cropTool } from './crop';

export const TOOLS: Record<ToolId, Tool> = {
  select: selectTool,
  pen: penTool,
  rect: rectTool,
  ellipse: ellipseTool,
  line: lineTool,
  arrow: arrowTool,
  text: textTool,
  emoji: emojiTool,
  image: pasteImageTool,
  crop: cropTool,
};
