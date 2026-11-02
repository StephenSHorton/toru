import { MediaService, VoiceRenderRequest, VoiceService, type TrackLevel } from "@/lib/api";
import { toMedia } from "@/trim/MediaBin";
import { findClip, type Media, type Project } from "@/trim/project";
import { mixStream, voiceSource } from "@/trim/voice";

// voiceRender.ts asks Go (voicesvc) for a clip's sound through a voice
// preset and makes the result project media (voice.ts applies it).

// renderRequest asks Go for a clip's original sound through a preset; a
// section (start, duration in source seconds) for an audition.
export function renderRequest(src: Media, levels: TrackLevel[] | undefined, preset: string, start = 0, duration = 0): VoiceRenderRequest {
  return new VoiceRenderRequest({
    path: src.path,
    stream: mixStream(src),
    levels: (levels ?? []).map((l) => ({ index: l.index, volume: l.volume, muted: l.muted })),
    preset,
    start,
    duration,
  });
}

// registerFile makes a rendered file servable and returns it as project
// media.
export async function registerFile(path: string): Promise<Media> {
  const [m] = await MediaService.Register([path]);
  if (!m) throw new Error("the processed sound couldn't be read");
  return toMedia(m);
}

// renderVoice renders a clip's original sound through a preset (the whole
// file, so the clip's in/out still line up) and returns the copy as media;
// null for "off".
export async function renderVoice(p: Project, clipId: string, preset: string): Promise<Media | null> {
  if (preset === "off") return null;
  const hit = findClip(p, clipId);
  if (!hit) throw new Error("that clip is gone");
  const { media, levels } = voiceSource(p, hit.clip);
  if (!media?.path) throw new Error("this clip's file isn't on disk");
  return registerFile(await VoiceService.Render(renderRequest(media, levels, preset)));
}
