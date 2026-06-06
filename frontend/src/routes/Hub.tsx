import { Button } from "@/components/ui/button";
import { Camera, Video, SquareDashed, Keyboard } from "lucide-react";
import { Updater } from "@/updater/Updater";
import { WindowsService } from "@/lib/api";

// Dev hub — a Phase-0 convenience to open each surface. The shipping app opens
// the overlay from a global hotkey and lives in the tray; this hub just lets
// both developers jump straight to their route during development.
function go(view: string) {
  window.location.search = `?view=${view}`;
}

// The capture overlay is NOT an in-window route: it needs the real Go session
// (frozen stills + one window per monitor + query params). Navigating this
// window to ?view=overlay renders a dead black overlay with no params.
function openOverlay() {
  void WindowsService.OpenOverlay();
}

export default function Hub() {
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
        <Button onClick={openOverlay} className="justify-start">
          <SquareDashed /> Open capture overlay
        </Button>
        <Button variant="secondary" onClick={() => go("editor")} className="justify-start">
          <Camera /> Open screenshot editor (Dev 1)
        </Button>
        <Button variant="secondary" onClick={() => go("trim")} className="justify-start">
          <Video /> Open trim editor (Dev 2)
        </Button>
      </div>

      <div className="flex items-center gap-2 text-xs text-muted-foreground">
        <Keyboard className="size-3.5" />
        <span>
          Ctrl+Shift+2 overlay · Ctrl+Shift+1 shot · Ctrl+Shift+3 record
          (stubbed — wired in Phase 0)
        </span>
      </div>

      <Updater />
    </div>
  );
}
