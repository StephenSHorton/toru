import type { ProjectExport } from "@/trim/project";
import { textFontsReady, textPng, type TextImageJSON } from "@/trim/textRender";

// withTextImages draws every text of an export request with textRender.ts
// (the bitmap the player shows) and attaches it as a PNG with its place on
// the output frame (the canvas, or the crop when there is one). The export
// (internal/vid/project_text.go) composites these and refuses a text without
// one, so text can't silently go missing or draw differently. Opacity
// keyframe times go to ms, like the rest of the request.
export async function withTextImages(req: ProjectExport): Promise<ProjectExport> {
  const p = req.project;
  if (!p.texts.length) return req;
  const w = p.crop?.w ?? p.canvas.width;
  const h = p.crop?.h ?? p.canvas.height;
  await textFontsReady(p.texts);
  const texts = p.texts.flatMap((t) => {
    const image: TextImageJSON | null = textPng(t, w, h);
    if (!image) return []; // draws nothing (blank)
    const style = t.style?.opacityKeys ? { ...t.style, opacityKeys: t.style.opacityKeys.map((k) => ({ ...k, t: Math.round(k.t * 1000) })) } : t.style;
    return [{ ...t, style, image }];
  });
  return { ...req, project: { ...p, texts } };
}
