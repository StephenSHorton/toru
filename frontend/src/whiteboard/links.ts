// The ways a board window reaches a board store (live.ts, BoardLink):
//
// - `wailsLink`: Toru's Go store, through board.Service (Wails bindings) and
//   the board:op event every change sends. What the window uses in Toru.
// - `httpLink`: the same store over plain HTTP and server-sent events, for
//   the window running in a browser against Go's live board demo server
//   (board_live_demo_test.go), where there's no Wails.
// - `memoryLink`: a store in the page that applies ops the way Go does,
//   for the sample board (no board id) and the window's checks.
import { findItem, listKeys, put, putTypes, type BoardDoc, type BoardOp, type DocItem, type ListKey } from "./document"
import type { BoardLink } from "./live"

// ── Toru ────────────────────────────────────────────────────────────────

const service = () => import("../../bindings/github.com/StephenSHorton/toru/internal/board/service.js")

const plain = <T>(v: unknown) => JSON.parse(JSON.stringify(v)) as T

function wailsLink(): BoardLink {
  return {
    load: async (id) => plain<BoardDoc>(await (await service()).Load(id)),
    // eslint-disable-next-line @typescript-eslint/no-explicit-any -- the bindings' Op class is this shape
    apply: async (id, op) => plain<BoardOp>(await (await service()).Apply(id, op as any)),
    undo: async (id) => plain<BoardOp | null>(await (await service()).Undo(id)),
    redo: async (id) => plain<BoardOp | null>(await (await service()).Redo(id)),
    subscribe: (id, onOp) => {
      let off: (() => void) | null = null
      let stopped = false
      void import("@wailsio/runtime").then(({ Events }) => {
        if (stopped) return
        off = Events.On("board:op", (ev: { data: unknown }) => {
          const data = ev.data as { board?: string; op?: BoardOp }
          if (data?.board === id && data.op) onOp(data.op)
        })
      })
      return () => {
        stopped = true
        off?.()
      }
    },
  }
}

// ── Go's demo server, over HTTP ─────────────────────────────────────────

function httpLink(base = "/__board/live"): BoardLink {
  const post = async <T>(what: string, body: unknown): Promise<T> => {
    const res = await fetch(`${base}/${what}`, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) })
    const text = await res.text()
    if (!res.ok) throw new Error(text.trim() || res.statusText)
    return JSON.parse(text) as T
  }
  return {
    load: (id) => post("load", { board: id }),
    apply: (id, op) => post("apply", { board: id, op }),
    undo: (id) => post("undo", { board: id }),
    redo: (id) => post("redo", { board: id }),
    subscribe: (id, onOp) => {
      const events = new EventSource(`${base}/events?board=${encodeURIComponent(id)}`)
      events.onmessage = (ev) => onOp(JSON.parse(ev.data) as BoardOp)
      return () => events.close()
    },
  }
}

// ── In the page ─────────────────────────────────────────────────────────

type MemoryLink = BoardLink & {
  /** The board as the store has it. */
  board: () => BoardDoc
  /** Applies an op as if it came from somewhere else, like an agent's board command, and tells subscribers. */
  applyFrom: (op: BoardOp) => BoardOp
  /** How subscribers hear ops; replace it to delay, drop, double or reorder them. */
  deliver: (op: BoardOp, send: (op: BoardOp) => void) => void
}

let made = 0
const newId = (prefix: string) => `${prefix}-m${(++made).toString(36)}${Math.random().toString(36).slice(2, 6)}`
const prefixes: Record<ListKey, string> = { frames: "frm", blocks: "blk", ink: "ink", links: "lnk" }

/**
 * A board store in the page that applies ops as Go's board.Doc does: deletes
 * (and the links to what they delete, and frames emptied), puts that set who
 * made and changed each item, a rev per op, the as-applied op back, and an
 * undo history shared by everyone.
 */
function memoryLink(start: BoardDoc): MemoryLink {
  let doc = structuredClone(start)
  const undo: { fwd: BoardOp; inv: BoardOp }[] = []
  const redo: { fwd: BoardOp; inv: BoardOp }[] = []
  const subs = new Set<(op: BoardOp) => void>()

  const run = (op: BoardOp, kind: "change" | "undo" | "redo", restore: boolean) => {
    if (!/^(person|agent:[A-Za-z0-9][A-Za-z0-9 ._@()/+-]{0,63})$/.test(op.source)) throw new Error(`bad source "${op.source}"`)
    const before = doc
    let b: BoardDoc = structuredClone(doc)
    const touched: string[] = []
    const touch = (id: string) => !touched.includes(id) && touched.push(id)
    const gone = new Set<string>()
    for (const id of op.delete ?? []) {
      if (!findItem(b, id)) throw new Error(`nothing on the board has the id "${id}"`)
      gone.add(id)
    }
    for (const l of b.links) if (gone.has(l.from) || gone.has(l.to)) gone.add(l.id)
    for (const id of op.delete ?? []) touch(id)
    for (const l of b.links) if (gone.has(l.id)) touch(l.id)
    for (const key of listKeys) (b as Record<ListKey, DocItem[]>)[key] = (b[key] as DocItem[]).filter((x) => !gone.has(x.id))
    for (const x of [...b.blocks, ...b.ink]) {
      if (x.frame && gone.has(x.frame)) {
        delete x.frame
        if (!restore) x.updatedBy = op.source
        touch(x.id)
      }
    }
    for (const key of listKeys) {
      const items: DocItem[] = []
      for (const sent of (op[key] as DocItem[] | undefined) ?? []) {
        const x = structuredClone(sent)
        if (!x.id) x.id = newId(prefixes[key])
        if (!/^[A-Za-z0-9_-]{1,64}$/.test(x.id)) throw new Error(`bad id "${x.id}"`)
        if (key === "blocks" && !/^[a-z][a-z0-9-]{0,39}$/.test((x as { type: string }).type)) throw new Error(`block "${x.id}" has a bad type`)
        if (key === "links" && (x as { from: string }).from === (x as { to: string }).to) throw new Error(`link "${x.id}" goes to itself`)
        if (items.some((y) => y.id === x.id)) throw new Error(`the change puts "${x.id}" twice`)
        const was = (b[key] as DocItem[]).find((y) => y.id === x.id)
        const other = findItem(b, x.id)
        if (other && other.key !== key) throw new Error(`id "${x.id}" is already a ${other.key}`)
        if (!restore) {
          x.createdBy = was ? was.createdBy : op.source
          x.updatedBy = op.source
        }
        touch(x.id)
        items.push(x)
      }
      ;(b as Record<ListKey, DocItem[]>)[key] = put(b[key] as DocItem[], items, op.pos)
    }
    for (const l of b.links) {
      for (const end of [l.from, l.to]) {
        const k = findItem(b, end)?.key
        if (k !== "blocks" && k !== "frames") throw new Error(`link "${l.id}" ends at "${end}", which isn't a block or frame on the board`)
      }
    }
    // Custom block types, as Go's applier.types: a type the board has is
    // only replaced by a newer version of the same block.
    const touchedTypes: string[] = []
    for (const t of op.dropTypes ?? []) {
      if (!b.types?.some((d) => d.type === t)) throw new Error(`the board has no custom block type "${t}"`)
      touchedTypes.push(t)
    }
    const putting = (op.types ?? []).filter((d) => {
      const was = b.types?.find((x) => x.type === d.type)
      if (!was || restore) return true
      if (was.origin !== d.origin) throw new Error(`this board already has a different block type called "${d.type}"`)
      if (d.version < was.version) throw new Error(`this board has a newer version of block type "${d.type}"`)
      return d.version > was.version
    })
    for (const d of putting) touchedTypes.push(d.type)
    if (touchedTypes.length) b.types = putTypes(b.types, structuredClone(putting), op.dropTypes)
    if (op.name !== undefined) b.name = op.name
    b = { ...b, rev: before.rev + 1, updatedAt: new Date().toISOString() }
    const fwd = { ...restoreOp(b, touched), rev: b.rev, at: b.updatedAt, kind, source: op.source } as BoardOp
    const inv = restoreOp(before, touched)
    const typesIn = (d: BoardDoc, into: BoardOp) => {
      for (const t of touchedTypes) {
        const def = d.types?.find((x) => x.type === t)
        if (def) (into.types ??= []).push(structuredClone(def))
        else (into.dropTypes ??= []).push(t)
      }
    }
    typesIn(b, fwd)
    typesIn(before, inv)
    if (op.name !== undefined) {
      fwd.name = b.name
      inv.name = before.name
    }
    doc = b
    return { fwd, inv }
  }

  const tell = (op: BoardOp) => {
    for (const s of subs) link.deliver(structuredClone(op), s)
  }

  const link: MemoryLink = {
    board: () => structuredClone(doc),
    deliver: (op, send) => queueMicrotask(() => send(op)),
    load: async () => structuredClone(doc),
    applyFrom: (op) => {
      const { fwd, inv } = run(op, "change", false)
      undo.push({ fwd, inv })
      redo.length = 0
      tell(fwd)
      return structuredClone(fwd)
    },
    apply: async (_id, op) => link.applyFrom(op),
    undo: async () => {
      const e = undo.pop()
      if (!e) return null
      const { fwd } = run({ ...e.inv, source: "person" }, "undo", true)
      redo.push(e)
      tell(fwd)
      return structuredClone(fwd)
    },
    redo: async () => {
      const e = redo.pop()
      if (!e) return null
      const { fwd } = run({ ...e.fwd, source: "person" }, "redo", true)
      undo.push(e)
      tell(fwd)
      return structuredClone(fwd)
    },
    subscribe: (_id, onOp) => {
      subs.add(onOp)
      return () => subs.delete(onOp)
    },
  }
  return link
}

/** The op that brings these ids to their state in b, as Go's restoreOp. */
function restoreOp(b: BoardDoc, ids: string[]): BoardOp {
  const op: BoardOp = { source: "" }
  const pos: Record<string, number> = {}
  const want = new Set(ids)
  const found = new Set<string>()
  for (const key of listKeys) {
    ;(b[key] as DocItem[]).forEach((x, i) => {
      if (!want.has(x.id)) return
      found.add(x.id)
      pos[x.id] = i
      ;((op as Record<ListKey, DocItem[]>)[key] ??= []).push(structuredClone(x))
    })
  }
  const del = ids.filter((id) => !found.has(id))
  if (del.length) op.delete = del
  if (Object.keys(pos).length) op.pos = pos
  return op
}

/** An empty board, as Go's board.New. */
function emptyBoard(id: string, name: string): BoardDoc {
  const now = new Date().toISOString()
  return { format: "toruboard", version: 1, id, name, createdAt: now, updatedAt: now, rev: 0, frames: [], blocks: [], links: [], ink: [] }
}

export { emptyBoard, httpLink, memoryLink, wailsLink, type MemoryLink }
