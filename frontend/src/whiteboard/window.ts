// What the whiteboard window (routes/Whiteboard.tsx) needs around the live
// board: which board and link it opens, and whose a change is.
import type { BoardDoc } from "./document"
import { emptyBoard, httpLink, memoryLink, wailsLink } from "./links"
import { LiveBoard } from "./live"
import sample from "./sample-board.json"
import { boardTemplates } from "./templates"

/**
 * The live board for this window's address: `?id=<board>` is a board in
 * Toru's store (over Wails, or `&link=http` over Go's live board demo
 * server); no id is a board in a store of its own: the brainstorm template
 * `?template=<name>` names, else the sample board, one block of every type.
 */
function openLive(search = window.location.search): LiveBoard {
  const params = new URLSearchParams(search)
  const id = params.get("id") ?? ""
  if (id) return new LiveBoard(id, params.get("link") === "http" ? httpLink() : wailsLink())
  const t = boardTemplates.find((t) => t.name === params.get("template"))
  if (t) return new LiveBoard(t.name, memoryLink(fileDoc(t.name, t.title, t)))
  return new LiveBoard("sample", memoryLink(sampleDoc()))
}

/** A board file's frames, blocks and links (sample-board.json, templates.json) as a document. */
function fileDoc(id: string, name: string, file: object): BoardDoc {
  const { frames, blocks, links } = structuredClone(file) as Partial<BoardDoc>
  return { ...emptyBoard(id, name), frames: frames ?? [], blocks: blocks ?? [], links: links ?? [] }
}

/** The sample board as a document. */
function sampleDoc(): BoardDoc {
  return fileDoc("sample", "Sample board", sample)
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

// Who is using the board through a window command: set while `board.act` or
// `board.arrange` runs, so the changes it makes go to Go as "agent:<name>".
let actingAs: string | null = null

/** Whose a canvas or block change is: the agent running a window command, else UI SDK's change details say. */
const sourceOf = (details?: { source?: string }) =>
  actingAs ?? (details?.source === "agent" ? "agent:unknown" : "person")

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
