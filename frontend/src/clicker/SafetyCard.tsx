import { useState } from "react";
import { Eye } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { ClickerService, ClickerSettings } from "@/lib/api";
import { cn } from "@/lib/utils";
import { CardHeader, CardProps, NumField, SwitchRow, errText } from "./PointsCardShared";

// Safety: the corner squares and edge bands that stop a run when the cursor
// reaches them (checked per monitor, in physical px; 0 turns one off), plus
// the other stop/notice switches and a button that flashes the areas on screen.

const MAX_PX = 10_000;

type Corners = ClickerSettings["profile"]["corners"];
type Edges = ClickerSettings["profile"]["edges"];

export default function SafetyCard({ settings, update }: CardProps) {
  const corners = settings.profile.corners;
  const edges = settings.profile.edges;
  const [err, setErr] = useState("");

  const setCorners = (patch: Partial<Corners>) =>
    update((s) => ({
      ...s,
      profile: { ...s.profile, corners: { ...s.profile.corners, ...patch } },
    }));
  const setEdges = (patch: Partial<Edges>) =>
    update((s) => ({ ...s, profile: { ...s.profile, edges: { ...s.profile.edges, ...patch } } }));
  const setTop = (
    key: "stopOnTaskSwitcher" | "showSafetyOverlay" | "showIndicator" | "toastOnStop",
    on: boolean,
  ) => update((s) => ({ ...s, [key]: on }));

  const preview = () => {
    setErr("");
    void ClickerService.PreviewSafety().catch((e) => setErr(errText(e)));
  };

  return (
    <section className="frost flex flex-col gap-4 border border-border p-4" aria-label="Safety">
      <CardHeader title="Safety" caption="Failsafes that stop a run when the cursor reaches them.">
        {/* PreviewSafety only draws when Show safety overlay is on (PLAN 2.7).
            The title sits on a wrapper because a disabled button gets no hover. */}
        <span
          title={
            settings.showSafetyOverlay
              ? "Show the failsafe areas on screen"
              : "Turn on Show safety overlay to preview"
          }
        >
          <Button size="sm" variant="secondary" onClick={preview} disabled={!settings.showSafetyOverlay}>
            <Eye /> Preview
          </Button>
        </span>
      </CardHeader>
      {err && <p className="text-xs text-destructive">{err}</p>}

      <div className="flex flex-col gap-2">
        <SwitchRow label="Corner stop" caption="Stop when the cursor enters a screen corner.">
          <Switch
            checked={corners.enabled}
            onCheckedChange={(enabled) => setCorners({ enabled })}
            aria-label="Corner stop"
          />
        </SwitchRow>
        <div
          className={cn("grid grid-cols-2 gap-2", !corners.enabled && "opacity-50")}
          title={corners.enabled ? undefined : "Turn on Corner stop to edit the corner sizes."}
        >
          <SizeField label="Top left" value={corners.tl} disabled={!corners.enabled} onCommit={(tl) => setCorners({ tl })} />
          <SizeField label="Top right" value={corners.tr} disabled={!corners.enabled} onCommit={(tr) => setCorners({ tr })} />
          <SizeField label="Bottom left" value={corners.bl} disabled={!corners.enabled} onCommit={(bl) => setCorners({ bl })} />
          <SizeField label="Bottom right" value={corners.br} disabled={!corners.enabled} onCommit={(br) => setCorners({ br })} />
        </div>
      </div>

      <div className="flex flex-col gap-2">
        <SwitchRow label="Edge stop" caption="Stop when the cursor reaches a screen edge.">
          <Switch
            checked={edges.enabled}
            onCheckedChange={(enabled) => setEdges({ enabled })}
            aria-label="Edge stop"
          />
        </SwitchRow>
        <div
          className={cn("grid grid-cols-2 gap-2", !edges.enabled && "opacity-50")}
          title={edges.enabled ? undefined : "Turn on Edge stop to edit the edge sizes."}
        >
          <SizeField label="Top" value={edges.top} disabled={!edges.enabled} onCommit={(top) => setEdges({ top })} />
          <SizeField label="Right" value={edges.right} disabled={!edges.enabled} onCommit={(right) => setEdges({ right })} />
          <SizeField label="Left" value={edges.left} disabled={!edges.enabled} onCommit={(left) => setEdges({ left })} />
          <SizeField label="Bottom" value={edges.bottom} disabled={!edges.enabled} onCommit={(bottom) => setEdges({ bottom })} />
        </div>
        <SwitchRow
          label="Outer edges only"
          caption="Ignore edges shared with another monitor, so moving between screens keeps clicking."
        >
          <Switch
            checked={edges.outerOnly}
            disabled={!edges.enabled}
            onCheckedChange={(outerOnly) => setEdges({ outerOnly })}
            aria-label="Outer edges only"
          />
        </SwitchRow>
      </div>

      <div className="flex flex-col gap-2 border-t border-border pt-3">
        <SwitchRow label="Stop on Alt+Tab" caption="Opening the task switcher stops a run.">
          <Switch
            checked={settings.stopOnTaskSwitcher}
            onCheckedChange={(on) => setTop("stopOnTaskSwitcher", on)}
            aria-label="Stop on Alt+Tab"
          />
        </SwitchRow>
        <SwitchRow label="Show safety overlay" caption="Flash the failsafe areas when you change them.">
          <Switch
            checked={settings.showSafetyOverlay}
            onCheckedChange={(on) => setTop("showSafetyOverlay", on)}
            aria-label="Show safety overlay"
          />
        </SwitchRow>
        <SwitchRow label="Show indicator" caption="A small pill on screen while clicking.">
          <Switch
            checked={settings.showIndicator}
            onCheckedChange={(on) => setTop("showIndicator", on)}
            aria-label="Show indicator"
          />
        </SwitchRow>
        <SwitchRow label="Notify on stop" caption="A notification when a failsafe or limit stops a run, or Windows blocks clicks.">
          <Switch
            checked={settings.toastOnStop}
            onCheckedChange={(on) => setTop("toastOnStop", on)}
            aria-label="Notify on stop"
          />
        </SwitchRow>
      </div>
    </section>
  );
}

function SizeField({
  label,
  value,
  disabled,
  onCommit,
}: {
  label: string;
  value: number;
  disabled: boolean;
  onCommit: (v: number) => void;
}) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5">
      <span className="text-[11px] text-muted-foreground">{label}</span>
      <NumField label={label} value={value} min={0} max={MAX_PX} suffix="px" disabled={disabled} onCommit={onCommit} />
    </div>
  );
}
