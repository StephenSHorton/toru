import { Mic } from "lucide-react";
import type { AgentHandler } from "@/lib/agent";
import type { EditorCommandSpec } from "@/trim/actions";
import type { VoiceControl } from "@/trim/voice/useVoiceSession";

// The editor's mic voiceover command (Irori #129): editor.recordVoice
// start / stop / cancel / mics, and the "Record voiceover" action in the
// palette and the player and track menus (it toggles: Stop while recording).
// agentCommands.ts registers it with its helpers.

export interface VoiceCommandCtx {
  voice: () => VoiceControl | undefined;
  reg: (name: string, spec: EditorCommandSpec, handler: AgentHandler) => void;
  obj: (props?: Record<string, unknown>, required?: string[]) => Record<string, unknown>;
}

export function registerVoiceCommands(x: VoiceCommandCtx) {
  const { reg, obj } = x;
  const voice = () => {
    const v = x.voice();
    if (!v) throw new Error("this editor window can't record a voiceover");
    return v;
  };
  const phase = () => x.voice()?.state.phase ?? "idle";

  reg(
    "editor.recordVoice",
    {
      description:
        "Record a mic voiceover over the edit. action start: opens the mic (deviceId from action mics; default: the mic chosen in Replay settings), counts in 3-2-1 (countIn false skips it), then plays the preview from the playhead and records; it returns once recording. action stop: ends the take and puts it on a 'Voice' audio track exactly where recording started (minus the measured mic + speaker latency) as one undo step; returns the clip, its track and the WAV path (%LOCALAPPDATA%/toru/voice). action cancel: throws the take away. action mics: the mics the editor can use and the one Replay settings picked. Takes are the raw mic (no echo cancellation, noise suppression or auto gain).",
      input: obj(
        {
          action: { type: "string", enum: ["start", "stop", "cancel", "mics"], description: "What to do." },
          deviceId: { type: "string", description: "start: a mic id from action mics. Default: the Replay settings mic." },
          countIn: { type: "boolean", description: "start: count in 3-2-1 first (default true)." },
        },
        ["action"],
      ),
      timeoutSec: 60,
      ui: {
        id: "voice.record",
        label: () => (phase() === "idle" ? "Record voiceover" : "Stop voiceover"),
        group: "Audio",
        icon: Mic,
        keywords: "mic microphone voice narration narrate talk record dub",
        scopes: ["viewer", "track"],
        hidden: (c) => !x.voice() || (!!c.trackId && c.project.tracks.find((t) => t.id === c.trackId)?.kind === "video"),
        enabled: (c) => {
          const p = phase();
          if (p === "recording") return true;
          if (p !== "idle") return "A take is starting or saving";
          return c.project.tracks.some((t) => t.clips.length) ? true : "Add a clip first";
        },
        hint: () => (phase() === "idle" ? "3-2-1, then talk over the video from the playhead" : "Put the take on the Voice track"),
        run: () => {
          const v = x.voice();
          if (!v) return;
          if (v.state.phase === "recording") void v.stop().catch(() => {});
          else if (v.state.phase === "idle") void v.start().catch(() => {});
        },
      },
    },
    async (input: { action: string; deviceId?: string; countIn?: boolean }) => {
      const v = voice();
      switch (input.action) {
        case "mics": {
          const r = await v.mics();
          return { mics: r.mics, replayMic: r.preferred || null, state: v.state.phase };
        }
        case "start":
          if (v.state.phase !== "idle") throw new Error(`a take is already ${v.state.phase}: stop or cancel it first`);
          await v.start({ deviceId: input.deviceId ? String(input.deviceId) : "", countIn: input.countIn !== false });
          return { recording: true };
        case "stop":
          if (v.state.phase !== "recording") throw new Error(v.state.phase === "idle" ? "nothing is recording (action start first)" : `the take is ${v.state.phase}; try again in a moment`);
          return await v.stop();
        case "cancel":
          await v.cancel();
          return { cancelled: true };
        default:
          throw new Error('action must be "start", "stop", "cancel" or "mics"');
      }
    },
  );
}
