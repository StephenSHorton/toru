import { useCallback, useEffect, useRef, useState } from "react";
import { BoxSelect, Pencil, Plus, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { ClickerService, ClickerSettings, StopZone } from "@/lib/api";
import { cn } from "@/lib/utils";
import {
  CardHeader,
  CardProps,
  NumField,
  errText,
  optionClass,
  selectClass,
  usePickActive,
} from "./PointsCardShared";

// Zones: screen rectangles that stop, pause or start a run when the cursor is
// inside. Drawing happens on screen (right-drag); Go writes the rectangle and
// sends the new list as clicker:settings. The list itself lives in Settings,
// not the preset profile; only the on/off switch is per preset.

type Action = StopZone["action"];

// ZoneAction values, typed through StopZone so the enum need not be imported
// from the raw bindings (it is not re-exported by lib/api).
const ACTIONS: { value: Action; label: string }[] = [
  { value: "stop" as Action, label: "Stop" },
  { value: "pause" as Action, label: "Pause" },
  { value: "start" as Action, label: "Start" },
];

const COORD = 99_999;

function withZones(s: ClickerSettings, fn: (z: StopZone[]) => StopZone[]): ClickerSettings {
  return { ...s, stopZones: fn(s.stopZones ?? []) };
}

export default function ZonesCard({ settings, update }: CardProps) {
  const enabled = settings.profile.zonesEnabled;
  const zones = settings.stopZones ?? [];
  const [pick, setPick] = usePickActive("zone");
  const [err, setErr] = useState("");
  // Which zone this card asked to draw: an id, "" for a new zone, null when
  // none. A pick event for an existing zone (index >= 0) overrides it.
  const [target, setTarget] = useState<string | null>(null);

  const picking = pick !== null;
  const drawing: string | null = !picking
    ? null
    : pick.index >= 0 && zones[pick.index]
      ? zones[pick.index].id
      : target ?? "";

  useEffect(() => {
    if (!picking) setTarget(null);
  }, [picking]);

  const draw = useCallback(
    async (id: string) => {
      setErr("");
      setTarget(id);
      setPick(true);
      try {
        await ClickerService.StartZonePick(id);
      } catch (e) {
        setPick(false);
        setErr(errText(e));
      }
    },
    [setPick],
  );

  const cancel = useCallback(() => {
    setPick(false);
    void ClickerService.CancelPick().catch(() => {});
  }, [setPick]);

  useEffect(() => {
    if (!picking) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") cancel();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [picking, cancel]);

  const pickingRef = useRef(false);
  pickingRef.current = picking;
  useEffect(
    () => () => {
      if (pickingRef.current) void ClickerService.CancelPick().catch(() => {});
    },
    [],
  );

  const setEnabled = (on: boolean) => {
    if (!on && picking) cancel();
    update((s) => ({ ...s, profile: { ...s.profile, zonesEnabled: on } }));
  };

  const editZone = (id: string, patch: Partial<StopZone>) =>
    update((s) => withZones(s, (zs) => zs.map((z) => (z.id === id ? { ...z, ...patch } : z))));

  const removeZone = (id: string) => {
    if (drawing === id) cancel();
    update((s) => withZones(s, (zs) => zs.filter((z) => z.id !== id)));
  };

  return (
    <section className="frost flex flex-col gap-4 border border-border p-4" aria-label="Zones">
      <CardHeader title="Zones" caption="Draw areas on screen that stop, pause or start clicking.">
        <Switch checked={enabled} onCheckedChange={setEnabled} aria-label="Use zones" />
      </CardHeader>

      <fieldset disabled={!enabled} className={cn("flex min-w-0 flex-col gap-3", !enabled && "opacity-50")}>
        <ul className="flex flex-col gap-1 text-xs text-muted-foreground">
          <li>
            <span className="font-medium text-foreground">Stop</span> ends the run when the cursor
            enters.
          </li>
          <li>
            <span className="font-medium text-foreground">Pause</span> holds clicking while the cursor
            is inside and resumes when it leaves.
          </li>
          <li>
            <span className="font-medium text-foreground">Start</span> keeps clicking inside pause
            zones. While armed and idle, entering one starts a run that stops when you leave.
          </li>
        </ul>

        <div className="flex flex-wrap items-center gap-2">
          {drawing === "" ? (
            <Button size="sm" variant="secondary" onClick={cancel}>
              <X /> Cancel
            </Button>
          ) : (
            <Button size="sm" variant={zones.length ? "secondary" : "default"} disabled={picking} onClick={() => void draw("")}>
              <Plus /> Add zone
            </Button>
          )}
          {picking && (
            <span className="text-xs text-muted-foreground">
              Right-drag on screen to draw. Esc cancels.
            </span>
          )}
        </div>
        {err && <p className="text-xs text-destructive">{err}</p>}

        {zones.length === 0 ? (
          <div className="flex flex-col items-center gap-2 border border-dashed border-border px-4 py-8 text-center">
            <BoxSelect className="size-6 text-muted-foreground" />
            <p className="text-sm font-medium">No zones yet</p>
            <p className="text-xs text-muted-foreground">
              Press Add zone, then right-drag on screen to draw one.
            </p>
          </div>
        ) : (
          <div className="flex max-h-80 flex-col overflow-y-auto">
            {zones.map((z, i) => (
              <div
                key={z.id || i}
                className={cn(
                  "flex flex-col gap-1.5 border-b border-border/60 px-1 py-2",
                  drawing === z.id && "bg-primary/10",
                )}
              >
                <div className="flex items-center gap-2">
                  <span className="w-5 text-xs tabular-nums text-muted-foreground">{i + 1}</span>
                  <select
                    aria-label={`Zone ${i + 1} action`}
                    value={z.action}
                    onChange={(e) => editZone(z.id, { action: e.target.value as Action })}
                    className={cn(selectClass, "w-24")}
                  >
                    {ACTIONS.map((a) => (
                      <option key={a.value} value={a.value} className={optionClass}>
                        {a.label}
                      </option>
                    ))}
                  </select>
                  <div className="ml-auto flex items-center gap-1">
                    {drawing === z.id ? (
                      <Button size="sm" variant="secondary" onClick={cancel}>
                        <X /> Cancel
                      </Button>
                    ) : (
                      <Button size="sm" variant="ghost" disabled={picking} onClick={() => void draw(z.id)}>
                        <Pencil /> Draw
                      </Button>
                    )}
                    <Button
                      size="icon"
                      variant="ghost"
                      className="h-7 w-7"
                      aria-label={`Delete zone ${i + 1}`}
                      title="Delete"
                      onClick={() => removeZone(z.id)}
                    >
                      <Trash2 />
                    </Button>
                  </div>
                </div>
                <div className="grid grid-cols-4 gap-2 pl-7">
                  <Field label="X">
                    <NumField label={`Zone ${i + 1} X`} value={z.x} min={-COORD} max={COORD} onCommit={(x) => editZone(z.id, { x })} />
                  </Field>
                  <Field label="Y">
                    <NumField label={`Zone ${i + 1} Y`} value={z.y} min={-COORD} max={COORD} onCommit={(y) => editZone(z.id, { y })} />
                  </Field>
                  <Field label="W">
                    <NumField label={`Zone ${i + 1} width`} value={z.w} min={1} max={COORD} onCommit={(w) => editZone(z.id, { w })} />
                  </Field>
                  <Field label="H">
                    <NumField label={`Zone ${i + 1} height`} value={z.h} min={1} max={COORD} onCommit={(h) => editZone(z.id, { h })} />
                  </Field>
                </div>
              </div>
            ))}
          </div>
        )}
      </fieldset>
    </section>
  );
}

function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="text-[11px] text-muted-foreground">{label}</span>
      {children}
    </div>
  );
}
