// SELECT tool: pointer-down hit-tests. Clicking an annotation node selects it;
// clicking the Stage background or the base image clears selection. Actual
// drag/resize/rotate is handled by each node's `draggable` flag + the
// Transformer, written back to the store in onDragEnd/onTransformEnd (see node
// views). The tool itself owns only selection.

import type { Tool } from './types';
import { BASE_IMAGE_ID } from '../store';

export const selectTool: Tool = {
  id: 'select',
  cursor: 'default',

  onPointerDown(e, ctx) {
    const stage = e.target.getStage();
    const target = e.target;

    // Background or base image -> clear selection.
    if (target === stage) {
      ctx.store.select(null);
      return;
    }

    // Walk up to the node carrying a stored id (set via node `name`/`id` attr).
    // Node views set `id={node.id}` on the Konva node, so target.id() is the id.
    let id = target.id();
    if (!id) {
      const parent = target.getParent();
      id = parent ? parent.id() : '';
    }

    if (!id || id === BASE_IMAGE_ID) {
      ctx.store.select(null);
      return;
    }
    ctx.store.select(id);
  },

  onPointerMove() {
    // no-op: dragging is handled by Konva draggable + node onDragEnd.
  },

  onPointerUp() {
    // no-op.
  },
};
