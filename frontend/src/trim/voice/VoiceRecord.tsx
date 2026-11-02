import { useEffect, useState } from "react";
import { Mic, Square, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Select } from "@/components/ui/select";
import { cn } from "@/lib/utils";
import type { MicOption } from "@/trim/voice/recorder";
import type { VoiceControl } from "@/trim/voice/useVoiceSession";

// VoiceRecord is the player toolbar's mic voiceover control (Irori #129):
// a mic picker (default: the mic chosen in Replay settings) and Record.
// While counting in or recording it turns into the take's status: the
// count or elapsed time, a live input meter, Stop and Cancel.

const DEFAULT = "__replay__";

export function VoiceRecord({ voice, disabled }: { voice: VoiceControl; disabled?: boolean }) {
  const { state } = voice;
  const [mics, setMics] = useState<MicOption[]>([]);
  const [preferred, setPreferred] = useState("");
  const [pick, setPick] = useState(DEFAULT);

  const refresh = () =>
    void voice
      .mics()
      .then((r) => {
        setMics(r.mics);
        setPreferred(r.preferred);
      })
      .catch(() => {});
  useEffect(refresh, [voice.mics]);
  // A take opened the mic: the labels are readable now.
  useEffect(() => {
    if (state.phase === "idle") refresh();
  }, [state.phase]);

  const preferredLabel = mics.find((m) => m.id === preferred)?.label;
  const options = [
    { value: DEFAULT, label: preferredLabel ? `Replay mic (${preferredLabel})` : "Replay settings mic" },
    ...mics.filter((m) => m.id !== "default").map((m) => ({ value: m.id, label: m.label })),
  ];
  const deviceId = pick === DEFAULT ? "" : pick;

  if (state.phase === "idle" || state.phase === "opening") {
    return (
      <div className="flex items-center gap-1">
        <Button
          size="sm"
          variant="ghost"
          disabled={disabled || state.phase === "opening"}
          onClick={() => void voice.start({ deviceId }).catch(() => {})}
          title={state.error || "Record a voiceover from the mic: a 3-2-1 count-in, then the video plays from the playhead while you talk"}
          className={cn(state.error && "text-destructive")}
        >
          <Mic /> {state.phase === "opening" ? "Opening mic…" : "Record voice"}
        </Button>
        <Select
          value={pick}
          options={options}
          onChange={setPick}
          onFocus={refresh}
          disabled={disabled || state.phase === "opening"}
          boxClassName="h-7 w-40"
          className="text-xs"
          title="Mic for the voiceover"
          aria-label="Voiceover mic"
        />
      </div>
    );
  }

  const counting = state.phase === "counting";
  return (
    <div className="flex items-center gap-2" role="status" aria-live="polite">
      <span className={cn("size-2.5 shrink-0 rounded-full", counting ? "bg-amber-400" : "animate-pulse bg-red-500")} aria-hidden />
      <span className="w-24 whitespace-nowrap text-xs tabular-nums">
        {counting ? `Recording in ${state.count}…` : state.phase === "saving" ? "Saving take…" : `Rec ${fmt(state.elapsed)}`}
      </span>
      <LevelMeter level={state.level} />
      <Button size="sm" variant="destructive" disabled={state.phase !== "recording"} onClick={() => void voice.stop().catch(() => {})} title="Stop and put the take on the Voice track">
        <Square /> Stop
      </Button>
      <Button size="icon" variant="ghost" className="size-7" disabled={state.phase === "saving"} onClick={() => void voice.cancel()} title="Cancel (the take is thrown away)" aria-label="Cancel the take">
        <X />
      </Button>
    </div>
  );
}

// LevelMeter shows the mic's peak on a dB scale (-60..0), red near clipping.
export function LevelMeter({ level, className }: { level: number; className?: string }) {
  const db = level > 0 ? 20 * Math.log10(level) : -60;
  const pct = Math.max(0, Math.min(1, (db + 60) / 60));
  return (
    <div className={cn("relative h-2 w-24 overflow-hidden bg-muted", className)} title={`Mic level ${db <= -60 ? "silent" : `${db.toFixed(0)} dB`}`} aria-label="Mic level">
      <div className={cn("h-full", db > -3 ? "bg-red-500" : db > -12 ? "bg-amber-400" : "bg-emerald-500")} style={{ width: `${pct * 100}%` }} />
    </div>
  );
}

// CountIn is the big 3-2-1 over the player.
export function CountIn({ count }: { count: number }) {
  return (
    <div className="pointer-events-none absolute inset-0 z-40 flex items-center justify-center">
      <span className="flex size-28 items-center justify-center rounded-full bg-black/60 text-6xl font-semibold tabular-nums text-white">{count}</span>
    </div>
  );
}

function fmt(s: number): string {
  const m = Math.floor(s / 60);
  const r = s - m * 60;
  return `${m}:${r < 10 ? "0" : ""}${r.toFixed(1)}`;
}
