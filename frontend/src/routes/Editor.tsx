import { useEffect, useRef, useState } from "react";
import { Stage, Layer, Image as KImage } from "react-konva";
import { Button } from "@/components/ui/button";
import {
  MousePointer2, Pen, Square, Circle, ArrowUpRight, Type, Smile,
  Clipboard, Copy, Save,
} from "lucide-react";

// DEVELOPER 1 — screenshot annotation editor (skeleton).
// Loads the captured PNG as a Konva base layer; the toolbar tools are
// placeholders to be implemented (shapes, multi-color pen, emoji, paste-image-
// as-layer, copy/save via the shared ExportService).
const TOOLS = [
  { id: "select", icon: MousePointer2, label: "Select" },
  { id: "pen", icon: Pen, label: "Pen" },
  { id: "rect", icon: Square, label: "Rectangle" },
  { id: "ellipse", icon: Circle, label: "Ellipse" },
  { id: "arrow", icon: ArrowUpRight, label: "Arrow" },
  { id: "text", icon: Type, label: "Text" },
  { id: "emoji", icon: Smile, label: "Emoji" },
];

const STAGE_W = 900;
const STAGE_H = 540;

function useImage(src: string) {
  const [img, setImg] = useState<HTMLImageElement | null>(null);
  useEffect(() => {
    const i = new window.Image();
    i.crossOrigin = "anonymous";
    i.src = src;
    i.onload = () => setImg(i);
  }, [src]);
  return img;
}

export default function Editor() {
  const [tool, setTool] = useState("select");
  const [color, setColor] = useState("#ff3b30");
  const imgPath = new URLSearchParams(window.location.search).get("img") ?? "";
  // In dev we display the bundled sample; the real editor loads the captured path.
  const image = useImage("/sample.png");
  const stageRef = useRef<any>(null);

  // Fit the image inside the stage.
  let dw = STAGE_W, dh = STAGE_H, dx = 0, dy = 0;
  if (image) {
    const scale = Math.min(STAGE_W / image.width, STAGE_H / image.height);
    dw = image.width * scale;
    dh = image.height * scale;
    dx = (STAGE_W - dw) / 2;
    dy = (STAGE_H - dh) / 2;
  }

  const swatches = ["#ff3b30", "#ff9500", "#ffcc00", "#34c759", "#0a84ff", "#bf5af2", "#ffffff", "#000000"];

  return (
    <div className="flex h-full flex-col">
      {/* frosted Markup toolbar */}
      <div className="frost z-10 flex items-center gap-1 px-2 py-1.5">
        {TOOLS.map((t) => (
          <Button
            key={t.id}
            size="icon"
            variant={tool === t.id ? "default" : "ghost"}
            title={t.label}
            onClick={() => setTool(t.id)}
          >
            <t.icon />
          </Button>
        ))}
        <div className="mx-1 h-6 w-px bg-border" />
        {swatches.map((c) => (
          <button
            key={c}
            title={c}
            onClick={() => setColor(c)}
            className="size-6 border"
            style={{ background: c, outline: color === c ? "2px solid var(--color-ring)" : "none" }}
          />
        ))}
        <div className="ml-auto flex items-center gap-1">
          <Button size="sm" variant="ghost" title="Paste image"><Clipboard /> Paste</Button>
          <Button size="sm" variant="ghost" title="Copy to clipboard"><Copy /> Copy</Button>
          <Button size="sm" title="Save as…"><Save /> Save</Button>
        </div>
      </div>

      {/* canvas */}
      <div className="flex flex-1 items-center justify-center p-4">
        <div className="border" style={{ width: STAGE_W, height: STAGE_H }}>
          <Stage ref={stageRef} width={STAGE_W} height={STAGE_H}>
            <Layer>
              {image && <KImage image={image} x={dx} y={dy} width={dw} height={dh} />}
            </Layer>
            {/* annotation layers (shapes/pen/emoji/pasted images) go here */}
            <Layer />
          </Stage>
        </div>
      </div>

      <div className="px-3 pb-2 text-[11px] text-muted-foreground">
        tool: {tool} · color: {color} · source:{" "}
        <span className="font-mono">{imgPath || "(dev sample.png)"}</span>
      </div>
    </div>
  );
}
