import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { Events as WailsEvents } from "@wailsio/runtime";
import { Check, Copy, Download, FolderOpen, Loader2, Save, Trash2, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { NumberInput } from "@/components/ui/number-input";
import { Progress } from "@/components/ui/progress";
import { Segmented } from "@/components/ui/segmented";
import { Select } from "@/components/ui/select";
import { Switch } from "@/components/ui/switch";
import { ExportService, VideoService } from "@/lib/api";
import { cn } from "@/lib/utils";
import type { ExportFormat, ExportSettings, ProjectExport } from "@/trim/project";
import {
  BUILTIN_PRESETS,
  baseOf,
  dirOf,
  estimateBytes,
  FORMAT_EXT,
  FORMAT_LABELS,
  formatBytes,
  formatTime,
  isVideo,
  loadFolder,
  loadPresets,
  loadState,
  outputSize,
  saveFolder,
  savePresets,
  saveState,
  toOptions,
  type ExportState,
  type Preset,
  type SizeMode,
} from "@/trim/exportSettings";

// ExportDialog is the editor's export screen: file name and folder, format
// (MP4 / HEVC / WebM / GIF / audio only), built-in and saved presets, video
// and audio settings, the range, a preview frame and a live summary with
// the estimated size. It runs the export (progress, cancel), then offers
// Copy / Show in folder. Settings and the folder are remembered.
//
// The parent runs the export with the choice (toExportRequest +
// VideoService.ExportProject) and gives the timeline's facts.

export interface ExportDialogProps {
  open: boolean;
  onClose: () => void;
  run: (choice: ExportChoice) => Promise<string>;
  // The first source's absolute path: the default name and folder.
  firstSource: string;
  projectName?: string;
  // The output frame (after crop), the timeline's length (s) and rate.
  frameWidth: number;
  frameHeight: number;
  duration: number;
  fps: number;
  playhead?: number;
  // A copy of the preview's current frame, cropped to the output frame.
  snapshot?: () => Promise<HTMLCanvasElement | null>;
}

export type ExportChoice = { preset: ProjectExport["preset"]; outPath: string; options?: ExportSettings };

type Phase =
  | { kind: "pick"; error?: string }
  | { kind: "working"; pct: number; fitting: boolean }
  | { kind: "done"; path: string; copied?: boolean; note?: string };

const isCancelled = (err: unknown) => String(err).includes("export cancelled");

const SIZE_OPTIONS: { value: SizeMode; label: string }[] = [
  { value: "original", label: "Same as the edit" },
  { value: "2160", label: "2160p (4K)" },
  { value: "1440", label: "1440p" },
  { value: "1080", label: "1080p" },
  { value: "720", label: "720p" },
  { value: "480", label: "480p" },
  { value: "custom", label: "Custom size" },
];
const FPS_OPTIONS = [
  { value: "0", label: "Same as the edit" },
  { value: "120", label: "120 fps" },
  { value: "60", label: "60 fps" },
  { value: "30", label: "30 fps" },
  { value: "24", label: "24 fps" },
  { value: "15", label: "15 fps" },
];
const AUDIO_KBPS = ["96", "128", "160", "192", "256", "320"].map((v) => ({ value: v, label: `${v} kbps` }));
const DITHERS = [
  { value: "sierra2_4a", label: "Smooth (default)" },
  { value: "floyd_steinberg", label: "Fine grain" },
  { value: "bayer", label: "Pattern (smaller file)" },
  { value: "none", label: "None (banding)" },
] as const;
const LOOPS = [
  { value: "0", label: "Forever" },
  { value: "-1", label: "Play once" },
  { value: "1", label: "Twice" },
  { value: "2", label: "3 times" },
];
const ENCODER_LABELS: Record<string, string> = {
  libx264: "Software (x264)",
  libx265: "Software (x265)",
  "libvpx-vp9": "Software (VP9)",
  h264_nvenc: "NVIDIA GPU",
  hevc_nvenc: "NVIDIA GPU",
  h264_amf: "AMD GPU",
  hevc_amf: "AMD GPU",
  h264_qsv: "Intel GPU",
  hevc_qsv: "Intel GPU",
  h264_mf: "Windows encoder",
  hevc_mf: "Windows encoder",
};

// Capabilities are probed once per session (each encoder probe runs ffmpeg).
let capsPromise: Promise<{ formats: string[]; encoders: Record<string, string[]> }> | null = null;
function capabilities() {
  capsPromise ??= VideoService.ExportCapabilities()
    .then((c) => ({ formats: c.formats ?? [], encoders: (c.encoders ?? {}) as Record<string, string[]> }))
    .catch(() => ({ formats: ["mp4", "webm", "gif", "m4a"], encoders: {} }));
  return capsPromise;
}

export function ExportDialog({ open, onClose, run, firstSource, projectName, frameWidth, frameHeight, duration, fps, playhead = 0, snapshot }: ExportDialogProps) {
  const [s, setS] = useState<ExportState>(loadState);
  const [presets, setPresets] = useState<Preset[]>(loadPresets);
  const [folder, setFolder] = useState(() => loadFolder());
  const [name, setName] = useState("");
  const [range, setRange] = useState<{ on: boolean; in: number; out: number }>({ on: false, in: 0, out: 0 });
  const [caps, setCaps] = useState<{ formats: string[]; encoders: Record<string, string[]> } | null>(null);
  const [preview, setPreview] = useState("");
  const [presetName, setPresetName] = useState("");
  const [phase, setPhase] = useState<Phase>({ kind: "pick" });
  const cancelled = useRef(false);

  // Fresh each time it opens: the name from the edit, the folder remembered
  // (else the first file's), the whole timeline, the current frame.
  useEffect(() => {
    if (!open) return;
    setPhase({ kind: "pick" });
    // A project is named after its first file, extension and all.
    const base = (projectName ?? "").trim().replace(/\.(mp4|mkv|mov|webm|avi|m4v|ts|gif|png|jpe?g|mp3|wav|m4a)$/i, "");
    setName(`${base || baseOf(firstSource) || "video"}-edited`);
    setFolder((f) => f || dirOf(firstSource));
    setRange({ on: false, in: 0, out: duration });
    void capabilities().then(setCaps);
    let alive = true;
    void snapshot?.()
      .then((c) => alive && c && setPreview(c.toDataURL("image/jpeg", 0.85)))
      .catch(() => {});
    return () => {
      alive = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps -- once per open
  }, [open]);

  const set = (patch: Partial<ExportState>) =>
    setS((cur) => {
      const next = { ...cur, ...patch };
      if (next.discord && next.format !== "mp4") next.discord = false;
      saveState(next);
      return next;
    });

  const encoders = caps?.encoders[s.format] ?? [];
  const formats = (["mp4", "hevc", "webm", "gif", "m4a"] as ExportFormat[]).filter((f) => !caps || caps.formats.includes(f));
  const seconds = range.on ? Math.max(0, range.out - range.in) : duration;
  const out = outputSize(s, frameWidth, frameHeight);
  const est = estimateBytes(s, seconds, frameWidth, frameHeight, fps);
  const outFps = s.format === "gif" ? s.gifFps : s.fps || fps;
  const ext = FORMAT_EXT[s.format];
  const upscale = isVideo(s.format) && !s.discord && s.size === "custom" && (out.w > frameWidth || out.h > frameHeight);
  const rangeBad = range.on && (range.out - range.in < 0.1 || range.in < 0 || range.out > duration + 0.001);
  const activePreset = useMemo(
    () => [...BUILTIN_PRESETS, ...presets].find((p) => Object.entries(p.state).every(([k, v]) => s[k as keyof ExportState] === v))?.id ?? "",
    [s, presets],
  );

  const pickFolder = async () => {
    try {
      const dir = await VideoService.PickExportFolder(folder);
      if (dir) {
        setFolder(dir);
        saveFolder(dir);
      }
    } catch (err) {
      setPhase({ kind: "pick", error: String(err) });
    }
  };

  const savePreset = () => {
    const n = presetName.trim();
    if (!n) return;
    const p: Preset = { id: `user-${Date.now().toString(36)}`, name: n, state: { ...s } };
    const next = [...presets.filter((x) => x.name !== n), p];
    setPresets(next);
    savePresets(next);
    setPresetName("");
  };
  const deletePreset = (id: string) => {
    const next = presets.filter((p) => p.id !== id);
    setPresets(next);
    savePresets(next);
  };

  const start = async () => {
    let outPath: string;
    try {
      const [p] = await VideoService.ExportPath(folder, name, s.format);
      outPath = p;
    } catch (err) {
      setPhase({ kind: "pick", error: String(err) });
      return;
    }
    saveFolder(folder);
    cancelled.current = false;
    setPhase({ kind: "working", pct: 0, fitting: false });
    const off = WailsEvents.On("edit:progress", (ev) => {
      const raw = Array.isArray(ev.data) ? ev.data[0] : ev.data;
      const pct = Number((raw as { pct?: number } | undefined)?.pct ?? 0);
      setPhase((p) => (p.kind === "working" ? { kind: "working", pct, fitting: s.discord && pct >= 0.5 } : p));
    });
    try {
      const options = toOptions(s, range.on ? { in: range.in, out: range.out } : null, frameWidth, frameHeight);
      const path = await run({ preset: s.discord ? "discord" : "original", outPath, options });
      setPhase({ kind: "done", path });
    } catch (err) {
      setPhase(cancelled.current || isCancelled(err) ? { kind: "pick", error: "Export cancelled." } : { kind: "pick", error: String(err) });
    } finally {
      off();
    }
  };

  const cancel = () => {
    cancelled.current = true;
    void VideoService.CancelExport();
  };

  const working = phase.kind === "working";

  return (
    <Dialog open={open} onClose={onClose} dismissable={!working} className={phase.kind === "pick" ? "w-[60rem] max-w-[95vw] p-0" : "w-[26rem]"}>
      {phase.kind === "pick" ? (
        <div className="flex max-h-[88vh] flex-col">
          <div className="flex items-center gap-2 border-b border-border/60 px-5 py-3">
            <Download className="size-4 text-primary" />
            <h2 className="text-sm font-medium">Export</h2>
            <span className="ml-auto text-[11px] text-muted-foreground">Your original clips are never changed.</span>
          </div>

          <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-5 md:flex-row">
            {/* Left: preview + summary */}
            <div className="flex w-full shrink-0 flex-col gap-3 md:w-[21rem]">
              <div className="flex aspect-video items-center justify-center overflow-hidden border border-border/60 bg-black">
                {preview ? <img src={preview} alt="Preview frame" className="max-h-full max-w-full object-contain" /> : <span className="text-[11px] text-muted-foreground">No preview</span>}
              </div>
              <div className="border border-border/60 bg-background/40 p-3 text-xs">
                <div className="mb-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">Output</div>
                <Row k="File">
                  <span className="break-all font-mono text-[11px]">
                    {(name.trim() || "video") + ext}
                  </span>
                </Row>
                <Row k="Format">{FORMAT_LABELS[s.format]}{s.discord ? ", fitted to 10 MB" : ""}</Row>
                {s.format !== "m4a" ? (
                  <Row k="Picture">
                    {out.w} × {out.h} at {Math.round(outFps * 100) / 100} fps
                  </Row>
                ) : null}
                <Row k="Sound">
                  {s.format === "gif" || (isVideo(s.format) && !s.audio)
                    ? "None"
                    : `${s.audioKbps} kbps, ${(s.format === "webm" ? 48000 : s.sampleRate) / 1000} kHz`}
                </Row>
                <Row k="Length">
                  {formatTime(seconds)}
                  {range.on ? ` (${formatTime(range.in)} to ${formatTime(range.out)})` : ""}
                </Row>
                <Row k="Size">
                  <span className="font-medium text-foreground">
                    {s.discord ? "under 10 MB" : `${est.exact ? "" : "about "}${formatBytes(est.bytes)}`}
                  </span>
                </Row>
                {s.format === "gif" && seconds > 30 ? (
                  <p className="mt-2 text-[11px] text-primary">Long GIFs get very big. Try a shorter range or a smaller width.</p>
                ) : null}
                {upscale ? <p className="mt-2 text-[11px] text-primary">This size is bigger than the edit, so the picture is scaled up.</p> : null}
              </div>
            </div>

            {/* Right: settings */}
            <div className="flex min-w-0 flex-1 flex-col gap-4">
              <Section title="Presets">
                <div className="flex flex-wrap gap-1.5">
                  {[...BUILTIN_PRESETS, ...presets].map((p) => (
                    <span key={p.id} className="inline-flex">
                      <Button
                        size="sm"
                        variant={activePreset === p.id ? "default" : "outline"}
                        onClick={() => set(p.state)}
                        disabled={p.state.format !== undefined && !formats.includes(p.state.format)}
                      >
                        {p.name}
                      </Button>
                      {!p.builtin ? (
                        <Button size="sm" variant="ghost" aria-label={`Delete preset ${p.name}`} onClick={() => deletePreset(p.id)}>
                          <Trash2 />
                        </Button>
                      ) : null}
                    </span>
                  ))}
                </div>
                <div className="flex items-center gap-2">
                  <input
                    value={presetName}
                    onChange={(e) => setPresetName(e.target.value)}
                    onKeyDown={(e) => e.key === "Enter" && savePreset()}
                    placeholder="Save these settings as…"
                    maxLength={40}
                    className="h-8 min-w-0 flex-1 border border-border bg-background px-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                  />
                  <Button size="sm" variant="outline" onClick={savePreset} disabled={!presetName.trim()}>
                    <Save /> Save preset
                  </Button>
                </div>
              </Section>

              <Section title="File">
                <Field label="Name">
                  <div className="flex min-w-0 flex-1 items-center">
                    <input
                      value={name}
                      onChange={(e) => setName(e.target.value)}
                      aria-label="File name"
                      className="h-8 min-w-0 flex-1 border border-border bg-background px-2 text-sm focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
                    />
                    <span className="px-2 font-mono text-xs text-muted-foreground">{ext}</span>
                  </div>
                </Field>
                <Field label="Folder">
                  <div className="flex min-w-0 flex-1 items-center gap-2">
                    <span className="min-w-0 flex-1 truncate font-mono text-[11px] text-muted-foreground" title={folder}>
                      {folder || "Choose a folder"}
                    </span>
                    <Button size="sm" variant="outline" onClick={() => void pickFolder()}>
                      <FolderOpen /> Choose…
                    </Button>
                  </div>
                </Field>
                <Field label="Format">
                  <Select
                    value={s.format}
                    options={formats.map((f) => ({ value: f, label: FORMAT_LABELS[f] }))}
                    onChange={(v) => set({ format: v, encoder: "" })}
                    boxClassName="w-60"
                    aria-label="Format"
                  />
                </Field>
              </Section>

              {isVideo(s.format) ? (
                <Section title="Video">
                  {s.discord ? (
                    <p className="text-[11px] text-muted-foreground">The Discord preset picks the size and quality that fit 10 MB. Long videos lose quality.</p>
                  ) : (
                    <>
                      <Field label="Resolution">
                        <Select value={s.size} options={SIZE_OPTIONS} onChange={(v) => set({ size: v })} boxClassName="w-44" aria-label="Resolution" />
                        {s.size === "custom" ? (
                          <span className="flex items-center gap-1.5">
                            <NumberInput value={s.width} onChange={(v) => set({ width: v })} min={16} max={7680} step={2} suffix="px" boxClassName="w-24" aria-label="Width" />
                            <span className="text-xs text-muted-foreground">×</span>
                            <NumberInput value={s.height} onChange={(v) => set({ height: v })} min={16} max={4320} step={2} suffix="px" boxClassName="w-24" aria-label="Height" />
                          </span>
                        ) : null}
                      </Field>
                      {s.size === "custom" ? (
                        <Field label="Scaling">
                          <Segmented
                            aria-label="Scaling"
                            size="sm"
                            value={s.fit}
                            options={[
                              { value: "fit", label: "Fit", title: "The whole picture, with black bars if the shape differs" },
                              { value: "fill", label: "Fill", title: "Fill the frame, cropping the edges if the shape differs" },
                            ]}
                            onChange={(v) => set({ fit: v })}
                          />
                        </Field>
                      ) : null}
                      <Field label="Frame rate">
                        <Select value={String(s.fps)} options={FPS_OPTIONS} onChange={(v) => set({ fps: Number(v) })} boxClassName="w-44" aria-label="Frame rate" />
                      </Field>
                      <Field label="Quality">
                        <Segmented
                          aria-label="Rate control"
                          size="sm"
                          value={s.rateMode}
                          options={[
                            { value: "quality", label: "Quality" },
                            { value: "bitrate", label: "Bitrate" },
                          ]}
                          onChange={(v) => set({ rateMode: v })}
                        />
                        {s.rateMode === "quality" ? (
                          <Segmented
                            aria-label="Quality level"
                            size="sm"
                            value={s.quality}
                            options={[
                              { value: "low", label: "Low" },
                              { value: "medium", label: "Medium" },
                              { value: "high", label: "High" },
                              { value: "best", label: "Best" },
                            ]}
                            onChange={(v) => set({ quality: v })}
                          />
                        ) : (
                          <NumberInput value={s.kbps} onChange={(v) => set({ kbps: v })} min={100} max={200000} step={500} suffix="kbps" boxClassName="w-32" aria-label="Video bitrate" />
                        )}
                      </Field>
                    </>
                  )}
                  {encoders.length > 1 && !s.discord ? (
                    <Field label="Encoder">
                      <Select
                        value={s.encoder}
                        options={[{ value: "", label: "Automatic" }, ...encoders.map((e) => ({ value: e, label: ENCODER_LABELS[e] ?? e }))]}
                        onChange={(v) => set({ encoder: v })}
                        boxClassName="w-44"
                        aria-label="Encoder"
                      />
                    </Field>
                  ) : null}
                </Section>
              ) : null}

              {s.format === "gif" ? (
                <Section title="GIF">
                  <Field label="Width">
                    <NumberInput value={s.gifWidth} onChange={(v) => set({ gifWidth: v })} min={16} max={1920} step={16} suffix="px" boxClassName="w-28" aria-label="GIF width" />
                    <span className="text-[11px] text-muted-foreground">Height follows the picture.</span>
                  </Field>
                  <Field label="Frame rate">
                    <NumberInput value={s.gifFps} onChange={(v) => set({ gifFps: v })} min={1} max={50} step={1} suffix="fps" boxClassName="w-24" aria-label="GIF frame rate" />
                  </Field>
                  <Field label="Colours">
                    <NumberInput value={s.gifColors} onChange={(v) => set({ gifColors: v })} min={2} max={256} step={8} boxClassName="w-24" aria-label="GIF colours" />
                    <Select value={s.gifDither} options={DITHERS} onChange={(v) => set({ gifDither: v })} boxClassName="w-48" aria-label="Dithering" />
                  </Field>
                  <Field label="Loop">
                    <Select value={String(s.gifLoop)} options={LOOPS} onChange={(v) => set({ gifLoop: Number(v) })} boxClassName="w-36" aria-label="Loop" />
                  </Field>
                </Section>
              ) : null}

              {s.format !== "gif" ? (
                <Section title="Audio">
                  {isVideo(s.format) ? (
                    <Field label="Include sound">
                      <Switch checked={s.audio} onCheckedChange={(v) => set({ audio: v })} aria-label="Include sound" />
                    </Field>
                  ) : null}
                  {s.format === "m4a" || s.audio ? (
                    <Field label="Bitrate">
                      <Select value={String(s.audioKbps)} options={AUDIO_KBPS} onChange={(v) => set({ audioKbps: Number(v) })} boxClassName="w-32" aria-label="Audio bitrate" />
                      {s.format !== "webm" ? (
                        <Segmented
                          aria-label="Sample rate"
                          size="sm"
                          value={String(s.sampleRate)}
                          options={[
                            { value: "44100", label: "44.1 kHz" },
                            { value: "48000", label: "48 kHz" },
                          ]}
                          onChange={(v) => set({ sampleRate: Number(v) })}
                        />
                      ) : null}
                    </Field>
                  ) : null}
                </Section>
              ) : null}

              <Section title="Range">
                <Field label="Export">
                  <Segmented
                    aria-label="Range"
                    size="sm"
                    value={range.on ? "part" : "all"}
                    options={[
                      { value: "all", label: "Whole video" },
                      { value: "part", label: "Part of it" },
                    ]}
                    onChange={(v) => setRange((r) => ({ ...r, on: v === "part" }))}
                  />
                </Field>
                {range.on ? (
                  <Field label="From / to">
                    <NumberInput value={range.in} onChange={(v) => setRange((r) => ({ ...r, in: v }))} min={0} max={duration} step={0.1} decimals={2} suffix="s" boxClassName="w-24" aria-label="Range start" />
                    <span className="text-xs text-muted-foreground">to</span>
                    <NumberInput value={range.out} onChange={(v) => setRange((r) => ({ ...r, out: v }))} min={0} max={duration} step={0.1} decimals={2} suffix="s" boxClassName="w-24" aria-label="Range end" />
                    <Button size="sm" variant="ghost" title="Start the range at the playhead" onClick={() => setRange((r) => ({ ...r, in: Math.min(playhead, Math.max(0, r.out - 0.1)) }))}>
                      Start here
                    </Button>
                    <Button size="sm" variant="ghost" title="End the range at the playhead" onClick={() => setRange((r) => ({ ...r, out: Math.max(playhead, r.in + 0.1) }))}>
                      End here
                    </Button>
                  </Field>
                ) : null}
                {rangeBad ? <p className="text-[11px] text-destructive">The range must be inside the video and at least 0.1 s long.</p> : null}
              </Section>
            </div>
          </div>

          <div className="flex items-center gap-2 border-t border-border/60 px-5 py-3">
            {phase.error ? (
              <p className="flex min-w-0 items-start gap-1.5 text-xs text-destructive">
                <TriangleAlert className="mt-px size-3.5 shrink-0" />
                <span className="break-words">{phase.error}</span>
              </p>
            ) : null}
            <div className="ml-auto flex gap-2">
              <Button size="sm" variant="ghost" onClick={onClose}>
                Cancel
              </Button>
              <Button size="sm" onClick={() => void start()} disabled={!firstSource || !folder || rangeBad || duration <= 0}>
                <Download /> Export
              </Button>
            </div>
          </div>
        </div>
      ) : phase.kind === "working" ? (
        <div className="flex flex-col gap-3">
          <div className="flex items-center gap-2">
            <Loader2 className="size-4 animate-spin text-primary" />
            <h2 className="text-sm font-medium">{phase.fitting ? "Fitting to 10 MB…" : "Exporting…"}</h2>
            {!phase.fitting ? <span className="ml-auto text-xs tabular-nums text-muted-foreground">{Math.round(phase.pct * 100)}%</span> : null}
          </div>
          <Progress value={phase.pct * 100} indeterminate={phase.fitting} />
          <div className="flex justify-end">
            <Button size="sm" variant="ghost" onClick={cancel}>
              Cancel
            </Button>
          </div>
        </div>
      ) : (
        <div className="flex flex-col gap-3">
          <div className="flex items-center gap-2">
            <Check className="size-4 text-primary" />
            <h2 className="text-sm font-medium">Exported</h2>
          </div>
          <p className="break-all font-mono text-[11px] text-muted-foreground">{phase.path}</p>
          {phase.note ? <p className="text-xs text-destructive">{phase.note}</p> : null}
          <div className="flex justify-end gap-2">
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                const path = phase.path;
                // As a file (a GIF stays animated where it's pasted).
                ExportService.CopyToClipboardAs(path, "video", "Export")
                  .then(() => setPhase({ kind: "done", path, copied: true }))
                  .catch((err) => setPhase({ kind: "done", path, note: `Copy failed: ${err}` }));
              }}
            >
              {phase.copied ? <Check /> : <Copy />} {phase.copied ? "Copied" : "Copy"}
            </Button>
            <Button
              size="sm"
              variant="outline"
              onClick={() => {
                const path = phase.path;
                VideoService.RevealFile(path).catch((err) => setPhase({ kind: "done", path, note: String(err) }));
              }}
            >
              <FolderOpen /> Show in folder
            </Button>
            <Button size="sm" onClick={onClose}>
              Done
            </Button>
          </div>
        </div>
      )}
    </Dialog>
  );
}

function Section({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className="flex flex-col gap-2">
      <h3 className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{title}</h3>
      {children}
    </section>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-h-8 flex-wrap items-center gap-2">
      <span className="w-24 shrink-0 text-xs text-muted-foreground">{label}</span>
      {children}
    </div>
  );
}

function Row({ k, children }: { k: string; children: ReactNode }) {
  return (
    <div className={cn("flex gap-2 py-0.5")}>
      <span className="w-14 shrink-0 text-muted-foreground">{k}</span>
      <span className="min-w-0">{children}</span>
    </div>
  );
}
