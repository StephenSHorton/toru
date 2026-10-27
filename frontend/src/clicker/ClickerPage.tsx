// Auto Clicker — clicks a mouse button or presses a key at a set rate, with
// click points, failsafes and presets. All logic lives in Go
// (internal/clicker); this page renders ClickerService state.
export default function ClickerPage() {
  return (
    <div className="flex flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Auto Clicker</h1>
        <p className="text-sm text-muted-foreground">
          Click or press a key at a set rate, with failsafes that stop it when you need them to.
        </p>
      </div>
    </div>
  );
}
