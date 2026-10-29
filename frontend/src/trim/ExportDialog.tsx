import { useEffect, useRef, useState } from "react";
import { Events as WailsEvents } from "@wailsio/runtime";
import { Check, Copy, Download, FolderOpen, Loader2, TriangleAlert } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog } from "@/components/ui/dialog";
import { NumberInput } from "@/components/ui/number-input";
import { Progress } from "@/components/ui/progress";
import { Segmented } from "@/components/ui/segmented";
import { ExportService, VideoService } from "@/lib/api";
import { EditRequest } from "../../bindings/github.com/StephenSHorton/toru/internal/vid/models.js";
import type { Preset } from "@/trim/edl";

// ExportDialog runs the editor's export (docs/video-editor.md): pick a
// preset, choose where in the Save As dialog, watch progress (cancel stops
// ffmpeg and leaves nothing behind), then Copy / Show in folder.
//
// The parent builds the request (edl.ts toRequest with crop, tracks and
// texts); this dialog adds the preset, custom size and the output path.
//
// Discord: the edit pass is the first half of the progress bar, then the
// two-pass fit to 10MB reports nothing, so that half shows an indeterminate
// "Fitting to 10 MB". Its output is .webm whatever name was picked, so the
// done state shows the path Export returned, not the one picked.

export interface ExportDialogProps {
  open: boolean;
  onClose: () => void;
  // The request without preset/size/outPath, built when Export is pressed.
  buildRequest: () => Omit<EditRequest, "preset" | "height" | "kbps" | "outPath">;
  // The first source's absolute path: the Save As dialog suggests
  // <name>-edited.mp4 next to it.
  firstSource: string;
  // Height of the output frame (after crop), for which presets can shrink it.
  frameHeight: number;
}

type Phase =
  | { kind: "pick"; error?: string }
  | { kind: "working"; pct: number; fitting: boolean }
  | { kind: "done"; path: string; copied?: boolean; note?: string };

const PRESETS: { value: Preset; label: string; title: string }[] = [
  { value: "original", label: "Original", title: "Same size and frame rate as the first clip" },
  { value: "1080p", label: "1080p", title: "Scaled down to 1080 pixels tall (MP4)" },
  { value: "720p", label: "720p", title: "Scaled down to 720 pixels tall (MP4)" },
  { value: "discord", label: "Discord", title: "Fits Discord's 10 MB upload limit (WebM)" },
  { value: "custom", label: "Custom", title: "Choose the height and bitrate" },
];

const PRESET_KEY = "toru.edit.exportPreset";

function loadPreset(): { preset: Preset; height: number; kbps: number } {
  try {
    const v = JSON.parse(window.localStorage.getItem(PRESET_KEY) ?? "null");
    if (v && PRESETS.some((p) => p.value === v.preset)) {
      return { preset: v.preset, height: Number(v.height) || 1080, kbps: Number(v.kbps) || 8000 };
    }
  } catch {
    // fall through to the defaults
  }
  return { preset: "original", height: 1080, kbps: 8000 };
}

const isCancelled = (err: unknown) => String(err).includes("export cancelled");

export function ExportDialog({ open, onClose, buildRequest, firstSource, frameHeight }: ExportDialogProps) {
  const [{ preset, height, kbps }, setChoice] = useState(loadPreset);
  const [phase, setPhase] = useState<Phase>({ kind: "pick" });
  const cancelled = useRef(false);

  // Back to the preset picker each time the dialog opens.
  useEffect(() => {
    if (open) setPhase({ kind: "pick" });
  }, [open]);

  const choose = (next: Partial<{ preset: Preset; height: number; kbps: number }>) => {
    setChoice((c) => {
      const v = { ...c, ...next };
      try {
        window.localStorage.setItem(PRESET_KEY, JSON.stringify(v));
      } catch {
        // not remembered; fine
      }
      return v;
    });
  };

  const customHeight = Math.max(2, Math.floor(height / 2) * 2);
  const shrinks = (h: number) => frameHeight > h;
  const presetNote =
    preset === "1080p" && !shrinks(1080)
      ? `The video is ${frameHeight}p already, so it keeps its size.`
      : preset === "720p" && !shrinks(720)
        ? `The video is ${frameHeight}p already, so it keeps its size.`
        : preset === "custom" && !shrinks(customHeight)
          ? `Videos are never scaled up: this one stays ${frameHeight}p.`
          : preset === "discord"
            ? "Re-encoded to about 9 MB as WebM. Long videos lose quality."
            : null;

  const start = async () => {
    let outPath: string;
    try {
      outPath = await VideoService.PickExportPath(firstSource);
    } catch (err) {
      setPhase({ kind: "pick", error: String(err) });
      return;
    }
    if (!outPath) return; // Save As cancelled
    cancelled.current = false;
    setPhase({ kind: "working", pct: 0, fitting: false });
    const off = WailsEvents.On("edit:progress", (ev) => {
      const raw = Array.isArray(ev.data) ? ev.data[0] : ev.data;
      const pct = Number((raw as { pct?: number } | undefined)?.pct ?? 0);
      setPhase((p) =>
        p.kind === "working" ? { kind: "working", pct, fitting: preset === "discord" && pct >= 0.5 } : p,
      );
    });
    try {
      const req = EditRequest.createFrom({
        ...buildRequest(),
        preset,
        outPath,
        ...(preset === "custom" ? { height: customHeight, kbps } : {}),
      });
      const path = await VideoService.Export(req);
      setPhase({ kind: "done", path });
    } catch (err) {
      setPhase(
        cancelled.current || isCancelled(err) ? { kind: "pick", error: "Export cancelled." } : { kind: "pick", error: String(err) },
      );
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
    <Dialog open={open} onClose={onClose} dismissable={!working} className="w-[26rem]">
      {phase.kind === "pick" ? (
        <div className="flex flex-col gap-3">
          <div className="flex items-center gap-2">
            <Download className="size-4 text-primary" />
            <h2 className="text-sm font-medium">Export video</h2>
          </div>
          <Segmented
            aria-label="Export preset"
            value={preset}
            options={PRESETS}
            onChange={(v) => choose({ preset: v })}
            className="self-start"
          />
          {preset === "custom" ? (
            <div className="flex items-center gap-3 text-xs">
              <label className="flex items-center gap-2">
                Height
                <NumberInput
                  value={height}
                  onChange={(v) => choose({ height: v })}
                  min={144}
                  max={4320}
                  step={2}
                  suffix="px"
                  boxClassName="w-24"
                  aria-label="Output height"
                />
              </label>
              <label className="flex items-center gap-2">
                Bitrate
                <NumberInput
                  value={kbps}
                  onChange={(v) => choose({ kbps: v })}
                  min={200}
                  max={100000}
                  step={500}
                  suffix="kbps"
                  boxClassName="w-28"
                  aria-label="Video bitrate"
                />
              </label>
            </div>
          ) : null}
          {presetNote ? <p className="text-xs text-muted-foreground">{presetNote}</p> : null}
          {phase.error ? (
            <p className="flex items-start gap-1.5 text-xs text-destructive">
              <TriangleAlert className="mt-px size-3.5 shrink-0" />
              <span className="break-words">{phase.error}</span>
            </p>
          ) : null}
          <p className="text-[11px] text-muted-foreground">Your original clips are never changed.</p>
          <div className="flex justify-end gap-2">
            <Button size="sm" variant="ghost" onClick={onClose}>
              Cancel
            </Button>
            <Button size="sm" onClick={() => void start()} disabled={!firstSource}>
              <Download /> Export…
            </Button>
          </div>
        </div>
      ) : phase.kind === "working" ? (
        <div className="flex flex-col gap-3">
          <div className="flex items-center gap-2">
            <Loader2 className="size-4 animate-spin text-primary" />
            <h2 className="text-sm font-medium">{phase.fitting ? "Fitting to 10 MB…" : "Exporting…"}</h2>
            {!phase.fitting ? (
              <span className="ml-auto text-xs tabular-nums text-muted-foreground">
                {Math.round(phase.pct * 100)}%
              </span>
            ) : null}
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
                ExportService.CopyToClipboardAs(path, "video", "Edited video")
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
