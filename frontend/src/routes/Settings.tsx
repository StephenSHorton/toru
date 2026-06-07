import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { SquareDashed } from "lucide-react";
import { Updater } from "@/updater/Updater";
import { Shortcuts } from "@/shortcuts/Shortcuts";
import { SettingsService, WindowsService } from "@/lib/api";

// Settings / home window — the tray-driven hub. Toru lives in the system tray;
// this window (opened once on launch and from the tray menu / editor gear) shows
// that Toru is running, lets the user start a capture, customise the global
// shortcut, toggle launch-at-login, and check for updates. Dark / frosted /
// sharp-cornered theme.
export default function Settings() {
  // launchAtLogin mirrors the Windows "Run at sign-in" registry entry. We read
  // the live state from the backend on mount (the registry is the source of
  // truth) and update optimistically, reverting if the registry write fails.
  const [launchAtLogin, setLaunchAtLogin] = useState(false);
  const [launchBusy, setLaunchBusy] = useState(false);

  useEffect(() => {
    void SettingsService.GetLaunchAtLogin()
      .then(setLaunchAtLogin)
      .catch(() => {
        /* leave default false if the registry can't be read */
      });
  }, []);

  const toggleLaunchAtLogin = async (next: boolean) => {
    const prev = launchAtLogin;
    setLaunchBusy(true);
    setLaunchAtLogin(next); // optimistic
    try {
      await SettingsService.SetLaunchAtLogin(next);
    } catch {
      setLaunchAtLogin(prev); // revert on failure
    } finally {
      setLaunchBusy(false);
    }
  };

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

      <div className="frost flex flex-col gap-4 p-5" style={{ width: 360 }}>
        <Button
          className="justify-start"
          onClick={() => void WindowsService.OpenOverlay()}
        >
          <SquareDashed /> Capture
        </Button>

        <div className="flex items-center justify-between gap-3">
          <div className="flex flex-col">
            <span className="text-sm font-medium">Start with Windows</span>
            <span className="text-xs text-muted-foreground">
              Launch at sign-in, minimized to the tray
            </span>
          </div>
          <Switch
            checked={launchAtLogin}
            disabled={launchBusy}
            onCheckedChange={(v) => void toggleLaunchAtLogin(v)}
            aria-label="Start Toru with Windows"
          />
        </div>
      </div>

      <Shortcuts />

      <Updater />
    </div>
  );
}
