// What the whiteboard window (routes/Whiteboard.tsx) needs around the live
// board: which board and link it opens, and whose a change is.
import type { BoardDoc } from "./document"
import { emptyBoard, httpLink, memoryLink, wailsLink } from "./links"
import { LiveBoard } from "./live"
import sample from "./sample-board.json"

/**
 * The live board for this window's address: `?id=<board>` is a board in
 * Toru's store (over Wails, or `&link=http` over Go's live board demo
 * server); no id is the sample board, one block of every type, in a store
 * of its own.
 */
function openLive(search = window.location.search): LiveBoard {
  const params = new URLSearchParams(search)
  const id = params.get("id") ?? ""
  if (id) return new LiveBoard(id, params.get("link") === "http" ? httpLink() : wailsLink())
  return new LiveBoard("sample", memoryLink(sampleDoc()))
}

/** The sample board as a document. */
function sampleDoc(): BoardDoc {
  return { ...emptyBoard("sample", "Sample board"), ...(structuredClone(sample) as Partial<BoardDoc>), rev: 0 }
}

/** An agent's source from a window call's caller, as Go's board.AgentNamed makes it. */
function agentSource(caller: string) {
  let name = [...caller.trim()]
    .slice(0, 64)
    .map((c) => (/[A-Za-z0-9 ._@()/+-]/.test(c) ? c : "-"))
    .join("")
    .trim()
    .replace(/^[ ._@()/+-]+/, "")
  if (!name) name = "unknown"
  return `agent:${name}`
}

// Who is using the live controls: set while `board.act` runs, so the changes
// its steps make (details.source "agent") go to Go as "agent:<name>".
let actingAs: string | null = null

/** Whose a canvas or block change is, from UI SDK's change details. */
const sourceOf = (details?: { source?: string }) =>
  details?.source === "agent" ? (actingAs ?? "agent:unknown") : "person"

/** Runs an agent's live-control steps as that agent, and sends their changes before it returns. */
async function withAgent<T>(source: string, live: LiveBoard, run: () => Promise<T> | T): Promise<T> {
  actingAs = source
  try {
    return await run()
  } finally {
    await live.flush()
    actingAs = null
  }
}

/** Whether the person is typing in a block, so the change waits for a pause. */
function typing() {
  const el = document.activeElement as HTMLElement | null
  if (!el) return false
  return (
    el.tagName === "TEXTAREA" ||
    (el.tagName === "INPUT" && ["text", "search", ""].includes((el as HTMLInputElement).type)) ||
    el.isContentEditable
  )
}

export { agentSource, openLive, sampleDoc, sourceOf, typing, withAgent }
