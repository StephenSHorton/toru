import { soundProject, toExportRequest, type Project, type ProjectExport } from "@/trim/project";

// saveAudio.ts is "Save audio as file…" (the clip menu, editor.saveAudio):
// the clip's sound exported on its own through the project export
// (VideoService.ExportProject), so it comes out exactly as the clip plays
// it: its range, speed, reverse, volume, fades and audio streams. The file
// name picks the format: .wav is 16-bit PCM, anything else AAC in .m4a.

export const SOUND_KBPS = 192;

// soundExportRequest is the export of clipId's sound to outPath. It throws
// when the clip has no sound.
export function soundExportRequest(p: Project, clipId: string, outPath: string): ProjectExport {
  const q = soundProject(p, clipId);
  if (!q) throw new Error("this clip has no sound");
  const wav = /\.wav$/i.test(outPath);
  return toExportRequest(q, {
    preset: "original",
    outPath,
    options: wav ? { format: "wav" } : { format: "m4a", audioKbps: SOUND_KBPS },
  });
}
