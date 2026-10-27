import { useState } from "react";
import { BarChart3, RotateCcw } from "lucide-react";
import { Button } from "@/components/ui/button";
import { ClickerStats } from "@/lib/api";
import { Card, CardHead } from "./cardParts";
import { formatCount, formatSeconds } from "./cadence";

// Stats — lifetime totals across every run, with a two-step reset.

export default function StatsCard({
  stats,
  disabled,
  onReset,
}: {
  stats: ClickerStats | null;
  disabled?: boolean;
  onReset: () => Promise<void>;
}) {
  const [confirm, setConfirm] = useState(false);
  const empty = !stats || (stats.clicks === 0 && stats.runs === 0);

  return (
    <Card label="Stats">
      <CardHead icon={BarChart3} title="All time" caption="Totals across every run.">
        {confirm ? (
          <>
            <Button
              size="sm"
              variant="destructive"
              onClick={() => {
                setConfirm(false);
                void onReset();
              }}
            >
              Reset?
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setConfirm(false)}>
              Cancel
            </Button>
          </>
        ) : (
          <Button size="sm" variant="ghost" disabled={disabled || empty} onClick={() => setConfirm(true)}>
            <RotateCcw /> Reset
          </Button>
        )}
      </CardHead>
      <div className="grid grid-cols-3 gap-2 lg:grid-cols-1">
        {(
          [
            ["Clicks", formatCount(stats?.clicks ?? 0)],
            ["Time", formatSeconds(stats?.seconds ?? 0)],
            ["Runs", formatCount(stats?.runs ?? 0)],
          ] as const
        ).map(([label, value]) => (
          <div key={label} className="flex flex-col border border-border bg-background/40 px-3 py-2">
            <span className="text-[11px] uppercase tracking-wide text-muted-foreground">{label}</span>
            <span className="font-mono text-base tabular-nums">{value}</span>
          </div>
        ))}
      </div>
    </Card>
  );
}
