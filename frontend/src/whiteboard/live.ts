// The live board (docs/whiteboard.md, section 2): the board window and Go's
// board store kept in sync both ways.
//
// Go owns the board. The window keeps a copy (`doc`) at a rev, and every
// change, the person's, an agent's, an undo, comes back from Go as one op
// with the rev it made. `receive` applies each op exactly once and in rev
// order: an op at or below the copy's rev is one it has (an echo of its own
// change, or an event that came twice), one past a gap waits for the ops
// before it, and a gap that doesn't fill reloads the board.
//
// The person's changes show at once: they're kept as `pending` edits laid
// over the copy, and sent as one op at a time (typing is gathered until a
// pause, so undo takes back words, not letters). An edit stays laid over
// the copy until the copy reaches the rev Go gave it, so an agent's op that
// lands in between never undoes what the person just did on screen. If Go
// refuses an op, its edits are dropped and the board reloads.
//
// Undo and redo are Go's: one history for the person and agents alike.
import { nameLines, type BoardBlock, type BoardBlocks, type BoardLine } from "./board"
import {
  applyOp,
  findItem,
  listKeys,
  sameItem,
  toCanvas,
  toDocItem,
  toDocLink,
  type BoardDoc,
  type BoardOp,
  type DocItem,
  type DocLink,
  type ListKey,
} from "./document"
import { isBuiltin } from "./catalog"
import { libraryType, setBoardTypes } from "./custom-types"

/** How the window reaches Go's board store. */
type BoardLink = {
  load: (id: string) => Promise<BoardDoc>
  apply: (id: string, op: BoardOp) => Promise<BoardOp>
  /** null when there's nothing to undo. */
  undo: (id: string) => Promise<BoardOp | null>
  redo: (id: string) => Promise<BoardOp | null>
  /** Hears every op applied to the board, from anyone, as Go applied it. */
  subscribe: (id: string, onOp: (op: BoardOp) => void) => () => void
}

type View = { blocks: BoardBlocks; lines: BoardLine[] }

/** Fields to set on one document item, by its id (LiveBoard.update). */
type DocPatch = { id: string; [field: string]: unknown }

type LiveState = {
  /** The board as Go has it, at `doc.rev`; null until it loads. */
  doc: BoardDoc | null
  /** What the canvas shows: the copy with the person's pending edits laid over it. */
  view: View
  /** The last thing that went wrong, for the window to show. */
  error: string | null
}

type Pending = {
  kind: "block" | "line"
  /** The edit; null when it removes the block or line. */
  value: BoardBlock | BoardLine | null
  source: string
  /** Counts edits to this id, so a send or an ack only clears the edit it carried. */
  seq: number
  /** The seq last sent, and the rev Go gave the op that carried it. */
  sentSeq?: number
  ackRev?: number
}

type LiveOptions = {
  /** How long a gap in revs may wait for its ops before the board reloads. */
  gapMs?: number
  /** How long typing waits for a pause before it's sent. */
  typingMs?: number
}

const emptyView: View = { blocks: {}, lines: [] }

class LiveBoard {
  readonly id: string
  private link: BoardLink
  private opts: Required<LiveOptions>
  private doc: BoardDoc | null = null
  private pending = new Map<string, Pending>()
  private seq = 0
  private buffered = new Map<number, BoardOp>()
  private gapTimer: ReturnType<typeof setTimeout> | null = null
  private typingTimer: ReturnType<typeof setTimeout> | null = null
  private flushQueued = false
  private chain: Promise<void> = Promise.resolve()
  private off: (() => void) | null = null
  private listeners = new Set<() => void>()
  private state: LiveState = { doc: null, view: emptyView, error: null }
  private stopped = false
  // Canvas blocks made from document items, so an item Go didn't touch
  // keeps its block (and React doesn't redraw it).
  private made = new WeakMap<DocItem, BoardBlock>()

  constructor(id: string, link: BoardLink, opts: LiveOptions = {}) {
    this.id = id
    this.link = link
    this.opts = { gapMs: opts.gapMs ?? 400, typingMs: opts.typingMs ?? 700 }
  }

  /** Listens first, then loads, so no op falls between the two. Starting again after stop (React's dev mode does) carries on. */
  async start() {
    this.stopped = false
    this.off?.()
    this.off = this.link.subscribe(this.id, (op) => this.receive(op))
    await this.reload()
  }

  stop() {
    this.stopped = true
    this.off?.()
    this.off = null
    if (this.gapTimer) clearTimeout(this.gapTimer)
    if (this.typingTimer) clearTimeout(this.typingTimer)
  }

  // ── for React's useSyncExternalStore ────────────────────────────────
  subscribe = (listener: () => void) => {
    this.listeners.add(listener)
    return () => this.listeners.delete(listener)
  }
  getState = () => this.state

  /** The board's rev the window has. */
  get rev() {
    return this.doc?.rev ?? -1
  }

  private set(change: Partial<LiveState>) {
    this.state = { ...this.state, ...change }
    for (const l of this.listeners) l()
  }

  /** Lays the pending edits over the copy. */
  private render() {
    if (!this.doc) return
    setBoardTypes(this.doc.types)
    const base = toCanvas(this.doc)
    // Reuse the blocks made for items Go didn't touch.
    const blocks: BoardBlocks = {}
    const itemsById = new Map<string, DocItem>()
    for (const key of listKeys) for (const x of this.doc[key] as DocItem[]) itemsById.set(x.id, x)
    for (const [id, b] of Object.entries(base.blocks)) {
      const item = itemsById.get(id)
      const was = item && this.made.get(item)
      if (was && sameBlock(was, b)) blocks[id] = was
      else {
        blocks[id] = b
        if (item) this.made.set(item, b)
      }
    }
    let lines = base.lines
    for (const [id, p] of this.pending) {
      if (p.kind !== "block") continue
      if (p.value) blocks[id] = p.value as BoardBlock
      else delete blocks[id]
    }
    const editedLines = [...this.pending].filter(([, p]) => p.kind === "line")
    if (editedLines.length) {
      const byId = new Map(lines.map((l) => [l.id, l]))
      for (const [id, p] of editedLines) {
        if (p.value) byId.set(id, p.value as BoardLine)
        else byId.delete(id)
      }
      lines = [...byId.values()]
    }
    const ends = (l: BoardLine) => !!blocks[l.from.slice(0, l.from.lastIndexOf(":"))] && !!blocks[l.to.slice(0, l.to.lastIndexOf(":"))]
    // Named as the canvas names them, so handing them to it changes nothing.
    lines = nameLines(blocks, lines.filter(ends))
    this.set({ doc: this.doc, view: { blocks, lines } })
  }

  // ── Go → window ─────────────────────────────────────────────────────

  /** An op Go applied, from its event or as an answer. Each rev is applied once, in order. */
  receive(op: BoardOp) {
    if (this.stopped || op.rev === undefined) return
    if (this.doc && op.rev <= this.doc.rev) return
    this.buffered.set(op.rev, op)
    this.drain()
  }

  private drain() {
    if (!this.doc) return
    let doc = this.doc
    for (let next = this.buffered.get(doc.rev + 1); next; next = this.buffered.get(doc.rev + 1)) {
      this.buffered.delete(next.rev!)
      doc = applyOp(doc, next)
    }
    for (const rev of this.buffered.keys()) if (rev <= doc.rev) this.buffered.delete(rev)
    const moved = doc !== this.doc
    this.doc = doc
    if (this.buffered.size && !this.gapTimer) {
      this.gapTimer = setTimeout(() => {
        this.gapTimer = null
        if (this.buffered.size) void this.reload()
      }, this.opts.gapMs)
    } else if (!this.buffered.size && this.gapTimer) {
      clearTimeout(this.gapTimer)
      this.gapTimer = null
    }
    if (moved) {
      this.settle()
      this.render()
    }
  }

  /** Clears the edits Go has confirmed and the copy now holds. */
  private settle() {
    for (const [id, p] of this.pending) {
      if (p.ackRev !== undefined && p.sentSeq === p.seq && p.ackRev <= this.rev) this.pending.delete(id)
    }
  }

  /** Loads the board as Go has it, and goes on from its rev. */
  async reload() {
    try {
      const doc = await this.link.load(this.id)
      if (this.stopped) return
      if (!this.doc || doc.rev >= this.doc.rev) this.doc = doc
      this.drain()
      this.settle()
      this.render()
      if (this.state.error?.startsWith("Couldn't open")) this.set({ error: null })
    } catch (err) {
      this.set({ error: `Couldn't open the board: ${message(err)}` })
      // Never opened: try again, as Go may still be starting.
      if (!this.doc && !this.stopped) setTimeout(() => void this.reload(), 1500)
    }
  }

  // ── window → Go ─────────────────────────────────────────────────────

  /**
   * The canvas changed: new blocks and/or lines, as the board hands them
   * over. Only what differs from the view becomes an edit. `source` is
   * "person", or "agent:<name>" for an agent using the live controls;
   * `typing` waits for a pause before sending.
   */
  edit(next: { blocks?: BoardBlocks; lines?: BoardLine[] }, opts: { source?: string; typing?: boolean } = {}) {
    const source = opts.source ?? "person"
    const { view } = this.state
    let changed = false
    const mark = (id: string, kind: Pending["kind"], value: Pending["value"]) => {
      const p = this.pending.get(id)
      this.pending.set(id, { ...p, kind, value, source, seq: ++this.seq })
      changed = true
    }
    if (next.blocks) {
      for (const [id, b] of Object.entries(next.blocks)) if (view.blocks[id] !== b) mark(id, "block", b)
      for (const id of Object.keys(view.blocks)) if (!(id in next.blocks)) mark(id, "block", null)
    }
    if (next.lines) {
      const was = new Map(view.lines.map((l) => [l.id, l]))
      const now = new Map(next.lines.map((l) => [l.id, l]))
      for (const [id, l] of now) {
        const w = was.get(id)
        if (!w || w.from !== l.from || w.to !== l.to) mark(id, "line", l)
      }
      for (const id of was.keys()) if (!now.has(id)) mark(id, "line", null)
    }
    if (!changed) return
    this.render()
    if (opts.typing) {
      if (this.typingTimer) clearTimeout(this.typingTimer)
      this.typingTimer = setTimeout(() => {
        this.typingTimer = null
        void this.flush()
      }, this.opts.typingMs)
    } else if (!this.flushQueued) {
      // Changes made together (a delete takes its block and its lines) go as one op.
      this.flushQueued = true
      queueMicrotask(() => {
        this.flushQueued = false
        void this.flush()
      })
    }
  }

  /** Sends every edit not yet sent, one op per source, one op at a time. Resolves once Go has answered. */
  flush(): Promise<void> {
    if (this.typingTimer) {
      clearTimeout(this.typingTimer)
      this.typingTimer = null
    }
    this.chain = this.chain.then(() => this.sendAll())
    return this.chain
  }

  private async sendAll() {
    for (;;) {
      if (this.stopped || !this.doc) return
      const unsent = [...this.pending].filter(([, p]) => p.sentSeq !== p.seq)
      if (!unsent.length) return
      const source = unsent[0][1].source
      const batch = unsent.filter(([, p]) => p.source === source)
      const op = this.buildOp(source, batch)
      const sent = batch.map(([id, p]) => [id, p.seq] as const)
      for (const [, p] of batch) p.sentSeq = p.seq
      if (!op) {
        // Nothing Go doesn't have already: the edits are done.
        for (const [id, seq] of sent) if (this.pending.get(id)?.seq === seq) this.pending.delete(id)
        this.render()
        continue
      }
      try {
        const applied = await this.link.apply(this.id, op)
        for (const [id, seq] of sent) {
          const p = this.pending.get(id)
          if (p?.seq === seq) p.ackRev = applied.rev
        }
        this.receive(applied)
        this.settle()
        this.render()
      } catch (err) {
        for (const [id, seq] of sent) if (this.pending.get(id)?.seq === seq) this.pending.delete(id)
        this.set({ error: `That change wasn't saved: ${message(err)}` })
        await this.reload()
      }
    }
  }

  /** The op for a batch of edits, against the copy; null if it changes nothing. */
  private buildOp(source: string, batch: [string, Pending][]): BoardOp | null {
    const doc = this.doc!
    const op: BoardOp = { source }
    const lists: Partial<Record<ListKey, DocItem[]>> = {}
    const del: string[] = []
    const putting = new Set<string>()
    for (const [id, p] of batch) {
      const was = findItem(doc, id)
      if (!p.value) {
        if (was) del.push(id)
        continue
      }
      if (p.kind === "block") {
        const next = toDocItem(id, p.value as BoardBlock, was?.item)
        if (!next || (was && sameItem(next.item, was.item))) continue
        ;(lists[next.key] ??= []).push(next.item)
        putting.add(id)
      }
    }
    const gone = new Set(del)
    const has = (id: string) => putting.has(id) || (!!findItem(doc, id) && !gone.has(id))
    for (const [id, p] of batch) {
      if (p.kind !== "line" || !p.value) continue
      const was = findItem(doc, id)?.item as DocLink | undefined
      const link = toDocLink(p.value as BoardLine, was)
      // A line drawn on the canvas is named for its ports ("a:right->b:left"),
      // which Go's ids don't allow; it goes to Go under an id made from that.
      link.id = docId(link.id)
      if (!has(link.from) || !has(link.to)) continue
      if (was && sameItem(link, was)) continue
      ;(lists.links ??= []).push(link)
    }
    if (del.length) op.delete = del
    for (const key of listKeys) if (lists[key]?.length) (op as Record<ListKey, DocItem[]>)[key] = lists[key]!
    // A block of one of the person's own types the board doesn't carry yet
    // takes the type's definition with it, so the board shows it anywhere.
    for (const b of (lists.blocks ?? []) as { type?: string }[]) {
      const t = b.type
      if (!t || isBuiltin(t) || doc.types?.some((d) => d.type === t) || op.types?.some((d) => d.type === t)) continue
      const def = libraryType(t)
      if (def) (op.types ??= []).push(def)
    }
    return del.length || Object.keys(lists).length ? op : null
  }

  /**
   * Changes items as the document has them, fields the canvas doesn't carry
   * too (a block's rotation, frame or lock; a link's label and style), as
   * one op after what's pending: one change, one undo, through the same
   * store Go's board.update changes. Each patch names an item by id and
   * sets fields on it; a field set to undefined is removed. Items that
   * wouldn't change are left out. Given a function, the patches are made
   * from the board as it is once what's pending has gone, so they build on
   * it; a throw there rejects. Resolves to whether Go took it (true when
   * there was nothing to change).
   */
  update(patches: DocPatch[] | ((doc: BoardDoc) => DocPatch[]), opts: { source?: string } = {}): Promise<boolean> {
    void this.flush()
    const run = this.chain.then(async () => {
      if (this.stopped || !this.doc) return false
      if (typeof patches === "function") patches = patches(this.doc)
      const op: BoardOp = { source: opts.source ?? "person" }
      let any = false
      for (const patch of patches) {
        const found = findItem(this.doc, patch.id)
        if (!found) continue
        const next = { ...found.item, ...patch } as Record<string, unknown>
        for (const k of Object.keys(next)) if (next[k] === undefined) delete next[k]
        delete next.createdBy
        delete next.updatedBy
        if (sameItem(next, found.item)) continue
        ;(((op as Record<ListKey, DocItem[]>)[found.key] ??= []) as DocItem[]).push(next as DocItem)
        any = true
      }
      if (!any) return true
      try {
        this.receive(await this.link.apply(this.id, op))
        return true
      } catch (err) {
        this.set({ error: `That change wasn't saved: ${message(err)}` })
        await this.reload()
        return false
      }
    })
    this.chain = run.then(
      () => undefined,
      () => undefined,
    )
    return run
  }

  /**
   * Gives the board a new name, as the person: one op, after what's
   * pending, so it undoes like any other change. Resolves to whether Go took it.
   */
  rename(name: string): Promise<boolean> {
    const run = this.chain.then(async () => {
      name = name.trim()
      if (this.stopped || !this.doc || !name || name === this.doc.name) return false
      try {
        this.receive(await this.link.apply(this.id, { source: "person", name }))
        return true
      } catch (err) {
        this.set({ error: `Couldn't rename the board: ${message(err)}` })
        return false
      }
    })
    this.chain = run.then(() => undefined)
    return run
  }

  /** Takes back the board's latest change, whoever made it, after sending what's pending. */
  undo() {
    return this.step("undo")
  }

  /** Makes the latest undone change again. */
  redo() {
    return this.step("redo")
  }

  private async step(which: "undo" | "redo") {
    await this.flush()
    try {
      const op = await this.link[which](this.id)
      if (op) this.receive(op)
      return !!op
    } catch (err) {
      this.set({ error: `Couldn't ${which}: ${message(err)}` })
      return false
    }
  }

  dismissError() {
    this.set({ error: null })
  }
}

const idPattern = /^[A-Za-z0-9_-]{1,64}$/

/** An id Go takes: the id itself if it can be one, else "lnk-" and a hash of it. */
function docId(id: string) {
  if (idPattern.test(id)) return id
  let h = 2166136261
  for (let i = 0; i < id.length; i++) h = Math.imul(h ^ id.charCodeAt(i), 16777619) >>> 0
  return `lnk-${h.toString(36)}${id.length.toString(36)}`
}

function sameBlock(a: BoardBlock, b: BoardBlock) {
  return sameItem(a, b) && a.createdBy === b.createdBy && a.updatedBy === b.updatedBy
}

function message(err: unknown) {
  return err instanceof Error ? err.message : String(err)
}

export { docId, LiveBoard, type BoardLink, type DocPatch, type LiveOptions, type LiveState, type View }
