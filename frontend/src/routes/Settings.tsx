import { Button } from "@/components/ui/button";
import { SquareDashed } from "lucide-react";
import { Updater } from "@/updater/Updater";
import { Shortcuts } from "@/shortcuts/Shortcuts";
import { WindowsService } from "@/lib/api";

// Settings / home window — the tray-driven hub. Toru lives in the system tray;
// this window (opened once on launch and from the tray menu / editor gear) shows
// that Toru is running, lets the user start a capture, customise the global
// shortcut, and check for updates. Dark / frosted / sharp-cornered theme.
export default function Settings() {
  return (
    <div className="flex h-full flex-col items-center justify-center gap-8 p-8">
      <div className="text-center">
        <h1 className="text-3xl font-semibold tracking-tight">
          撮る <span className="text-muted-foreground">· Toru</span>
        </h1>
        <p className="mt-1 text-sm text-muted-foreground">
          macOS-style screenshot &amp; screen recording for Windows
        </p>
      </div>

      <div className="frost flex flex-col gap-3 p-5" style={{ width: 360 }}>
        <Button
          className="justify-start"
          onClick={() => void WindowsService.OpenOverlay()}
        >
          <SquareDashed /> Capture (Win+Shift+S)
        </Button>
      </div>

      <Shortcuts />

      <Updater />
    </div>
  );
}
