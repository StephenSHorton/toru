import type { TrackLevel } from "@/lib/api";
import { addMedia, findClip, mediaById, type Clip, type ClipVoice, type Media, type Project } from "@/trim/project";

// voice.ts applies a voice clean-up preset (internal/voice) to an audio
// clip. Go renders the clip's sound through the preset into a FLAC copy
// (voicesvc, cached; voiceRender.ts asks for it); the clip then plays that
// copy, in the preview and in the export alike, so what you hear is what
// you get. The clip remembers what it played before (ClipVoice), so another
// preset re-renders from the original and Off puts it back. Pure: no Go.

// canVoice says whether a clip can take a preset: a sound clip on an audio
// track (a voiceover take, or a recording's sound after Extract audio).
export function canVoice(p: Project, clipId: string): boolean {
  const hit = findClip(p, clipId);
  if (!hit || hit.track.locked || hit.track.kind !== "audio" || hit.clip.hold) return false;
  const m = mediaById(p, hit.clip.voice?.media ?? hit.clip.media);
  return !!m && m.hasAudio && !!m.path;
}

// voicePreset is the preset a clip plays ("off" when none).
export function voicePreset(c: Clip): string {
  return c.voice?.preset ?? "off";
}

// voiceSource is what a clip played before any preset: its original media
// and per-track levels.
export function voiceSource(p: Project, c: Clip): { media: Media | undefined; levels: TrackLevel[] | undefined } {
  if (c.voice) return { media: mediaById(p, c.voice.media), levels: c.voice.levels };
  return { media: mediaById(p, c.media), levels: c.levels };
}

// mixStream is the stream a source plays without levels: the mix, else the
// first (vid mixTrack).
export function mixStream(m: Media): number {
  return (m.tracks.find((t) => t.isMix) ?? m.tracks[0])?.index ?? 0;
}

// withVoice is the project with a clip playing `processed` (the preset's
// render of its original) or, with preset "off", its original again. The
// copy carries the levels' mix, so the clip's own levels go; the source
// range, timing, volume and fades stay.
export function withVoice(p: Project, clipId: string, preset: string, processed: Media | null): Project {
  const hit = findClip(p, clipId);
  if (!hit || hit.track.locked) return p;
  const c = hit.clip;
  const orig: ClipVoice = c.voice ?? { preset: "off", media: c.media, levels: c.levels };
  let next: Clip;
  if (preset === "off" || !processed) {
    next = { ...c, media: orig.media };
    delete next.voice;
    if (orig.levels) next.levels = orig.levels.map((l) => ({ ...l }));
    else delete next.levels;
  } else {
    p = addMedia(p, processed);
    next = { ...c, media: processed.id, voice: { preset, media: orig.media, ...(orig.levels ? { levels: orig.levels } : {}) } };
    delete next.levels;
  }
  const clips = hit.track.clips.slice();
  clips[hit.index] = next;
  return { ...p, tracks: p.tracks.map((t) => (t.id === hit.track.id ? { ...t, clips } : t)) };
}
