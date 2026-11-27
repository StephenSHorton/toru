// The person's own block library in the board window (whiteboard#27): the
// custom block types their agents designed with board.block.define, from
// Go's BlockLibraryService. Loaded when the window opens, again on every
// blocks:changed, and handed to custom-types.ts, so the gallery lists them
// and BlockBody draws them. It also lets "Save to my blocks" keep a type
// this board carries (from someone else) in the library.
import { useEffect } from "react"
import { setLibraryTypes, setSaveHandler, type BlockDef } from "./custom-types"

const service = () => import("../../bindings/github.com/StephenSHorton/toru/blocklibraryservice.js")

/** Keeps custom-types.ts's library in step with Go's, for board `boardId`. Off: nothing (a check, an exported page). */
function useBlockLibrary(boardId: string, on: boolean) {
  useEffect(() => {
    if (!on) return
    let stopped = false
    let off: (() => void) | undefined
    const refresh = async () => {
      try {
        const lib = await (await service()).List()
        if (!stopped) setLibraryTypes((lib?.blocks ?? []) as unknown as BlockDef[])
      } catch {
        // No Go behind the window: the board's own types still draw.
      }
    }
    void refresh()
    void import("@wailsio/runtime")
      .then(({ Events }) => {
        if (stopped) return
        off = Events.On("blocks:changed", () => void refresh())
      })
      .catch(() => {})
    setSaveHandler(async (type, as) => {
      await (await service()).SaveFromBoard(boardId, type, as)
      await refresh()
    })
    return () => {
      stopped = true
      off?.()
      setSaveHandler(null)
    }
  }, [boardId, on])
}

export { useBlockLibrary }
