import { useEffect, useRef, useState } from "react";
import { Loader2, Play, Square } from "lucide-react";
import { Button } from "@/components/ui/button";
import { VoiceService, type VoicePreset } from "@/lib/api";
import { cn } from "@/lib/utils";
import { Section } from "@/trim/inspector/controls";
import type { TabProps } from "@/trim/inspector/Inspector";
import { voicePreset, voiceSource, withVoice } from "@/trim/voice";
import { registerFile, renderRequest, renderVoice } from "@/trim/voiceRender";

// VoiceSection: voice clean-up presets for an audio clip. Pick one, listen
// to a few seconds of every preset from the playhead (switching while it
// plays keeps the spot, for an A/B), then Apply: Go renders the clip's
// sound through it once and the clip plays that copy in the preview and
// the export alike.

const AUDITION_S = 8;

let presetList: Promise<VoicePreset[]> | null = null;
function loadPresets(): Promise<VoicePreset[]> {
  presetList ??= VoiceService.Presets().catch((err) => {
    presetList = null;
    throw err;
  });
  return presetList;
}

type Audition = { key: string; urls: Record<string, string> };

export function VoiceSection({ project, clip: c, srcT, edit }: TabProps) {
  const [presets, setPresets] = useState<VoicePreset[]>([]);
  const applied = voicePreset(c);
  const [pick, setPick] = useState(applied);
  const [busy, setBusy] = useState<"" | "apply" | "audition">("");
  const [err, setErr] = useState("");
  const [aud, setAud] = useState<Audition | null>(null);
  const [playing, setPlaying] = useState(false);
  const audio = useRef<HTMLAudioElement | null>(null);

  useEffect(() => {
    let live = true;
    loadPresets()
      .then((p) => live && setPresets(p))
      .catch((e) => live && setErr(String(e)));
    return () => {
      live = false;
    };
  }, []);
  useEffect(() => setPick(applied), [applied, c.id]);

  const { media: src, levels } = voiceSource(project, c);
  const start = src ? Math.max(0, Math.min(srcT, (src.duration || 0) - AUDITION_S)) : 0;
  const key = `${src?.id}|${JSON.stringify(levels ?? null)}|${start.toFixed(1)}`;
  const current = aud?.key === key ? aud : null;

  // Stop listening when the clip or the spot changes.
  useEffect(() => () => stop(), [c.id]);
  useEffect(() => {
    if (aud && aud.key !== key) stop();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key]);

  function stop() {
    audio.current?.pause();
    setPlaying(false);
  }

  function play(urls: Record<string, string>, id: string) {
    const url = urls[id];
    if (!url) return;
    const at = audio.current && !audio.current.paused ? audio.current.currentTime : 0;
    audio.current?.pause();
    const a = new Audio(url);
    a.currentTime = at;
    a.onended = () => setPlaying(false);
    audio.current = a;
    void a.play().then(
      () => setPlaying(true),
      () => setPlaying(false),
    );
  }

  async function listen() {
    if (playing) return stop();
    if (current) return play(current.urls, pick);
    if (!src) return;
    setBusy("audition");
    setErr("");
    try {
      const paths = await VoiceService.Audition(renderRequest(src, levels, "off", start, AUDITION_S));
      const urls: Record<string, string> = {};
      await Promise.all(
        Object.entries(paths).map(async ([id, p]) => {
          if (p) urls[id] = (await registerFile(p)).url;
        }),
      );
      setAud({ key, urls });
      play(urls, pick);
    } catch (e) {
      setErr(`Couldn't prepare the samples: ${e}`);
    } finally {
      setBusy("");
    }
  }

  function choose(id: string) {
    setPick(id);
    if (playing && current) play(current.urls, id);
  }

  async function apply() {
    stop();
    setBusy("apply");
    setErr("");
    try {
      const processed = await renderVoice(project, c.id, pick);
      edit((p) => withVoice(p, c.id, pick, processed), "voice");
    } catch (e) {
      setErr(`Couldn't apply it: ${e}`);
    } finally {
      setBusy("");
    }
  }

  return (
    <Section title="Voice clean-up">
      <div role="radiogroup" aria-label="Voice clean-up preset" className="flex flex-col gap-0.5">
        {presets.map((p) => (
          <button
            key={p.id}
            type="button"
            role="radio"
            aria-checked={pick === p.id}
            onClick={() => choose(p.id)}
            title={p.description}
            className={cn(
              "flex flex-col items-start rounded px-2 py-1 text-left hover:bg-accent",
              pick === p.id && "bg-accent ring-1 ring-primary/60",
            )}
          >
            <span className="flex w-full items-center gap-1.5">
              <span className={cn("size-2 shrink-0 rounded-full border", pick === p.id ? "border-primary bg-primary" : "border-muted-foreground")} />
              <span className="font-medium">{p.name}</span>
              {applied === p.id ? <span className="ml-auto text-[10px] text-primary">applied</span> : null}
            </span>
            <span className="pl-3.5 text-[11px] leading-snug text-muted-foreground">{p.description}</span>
          </button>
        ))}
      </div>
      <div className="flex items-center gap-1.5">
        <Button
          size="sm"
          variant="secondary"
          onClick={() => void listen()}
          disabled={!!busy || !src}
          title={`Hear ${AUDITION_S} s from the playhead in the picked preset; pick another while it plays to compare`}
        >
          {busy === "audition" ? <Loader2 className="animate-spin" /> : playing ? <Square /> : <Play />}
          {busy === "audition" ? "Preparing…" : playing ? "Stop" : "Listen"}
        </Button>
        <Button size="sm" onClick={() => void apply()} disabled={!!busy || pick === applied} title="Use this preset for the clip, in the preview and the export">
          {busy === "apply" ? <Loader2 className="animate-spin" /> : null}
          {busy === "apply" ? "Applying…" : "Apply"}
        </Button>
      </div>
      {applied !== "off" && levels?.length ? (
        <p className="text-[11px] text-muted-foreground">The recorded tracks&apos; levels are part of the cleaned-up sound. Apply Off to change them.</p>
      ) : null}
      {err ? <p className="text-[11px] text-destructive">{err}</p> : null}
    </Section>
  );
}
