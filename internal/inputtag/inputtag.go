// Package inputtag holds the marker Toru writes into dwExtraInfo of every input
// event it injects (the auto clicker's SendInput calls). Toru's own low-level
// hooks skip events carrying it, so a synthetic click or key never triggers a
// Toru hotkey, never shows up while recording a shortcut, and never confuses the
// physical key-state table.
package inputtag

// Toru is "TORU" in ASCII. Low-level hooks see it as KBDLLHOOKSTRUCT.DwExtraInfo
// / MSLLHOOKSTRUCT.DwExtraInfo.
const Toru uintptr = 0x544F5255
