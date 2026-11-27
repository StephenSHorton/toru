import { useRef, useState, type PointerEvent } from "react";
import { Button } from "@ui-sdk/react/button";
import { Checkbox, CheckboxIndicator } from "@ui-sdk/react/checkbox";
import {
  Popover,
  PopoverClose,
  PopoverContent,
  PopoverDescription,
  PopoverTitle,
  PopoverTrigger,
} from "@ui-sdk/react/popover";
import { Select, SelectContent, SelectItem, SelectItemIndicator, SelectTrigger, SelectValue } from "@ui-sdk/react/select";
import { Slider, SliderControl, SliderIndicator, SliderThumb, SliderTrack } from "@ui-sdk/react/slider";
import { CheckIcon, ChevronDownIcon, PlusIcon } from "lucide-react";
import "@/whiteboard/toru-ui-sdk.css";

// The whiteboard window (?view=whiteboard&id=<board>, WindowsService.OpenWhiteboard).
// For now an empty surface that pans with a drag, and a toolbar of UI SDK
// controls in Toru's theme. The board canvas (pan, zoom, select, move,
// resize, connect on UI SDK's viewport) replaces the surface in a later card.
// The whiteboard uses UI SDK only, never Toru's shadcn components/ui.

const GRID = 24;

export default function Whiteboard() {
  const id = new URLSearchParams(window.location.search).get("id") ?? "";
  const [pan, setPan] = useState({ x: 0, y: 0 });
  const drag = useRef<{ x: number; y: number; px: number; py: number } | null>(null);

  const down = (e: PointerEvent<HTMLDivElement>) => {
    if (e.target !== e.currentTarget || e.button !== 0) return;
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { x: e.clientX, y: e.clientY, px: pan.x, py: pan.y };
  };
  const move = (e: PointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (d) setPan({ x: d.px + e.clientX - d.x, y: d.py + e.clientY - d.y });
  };
  const up = () => {
    drag.current = null;
  };

  return (
    <div className="relative h-full overflow-hidden">
      <div
        data-board-surface
        aria-label={`Whiteboard ${id}`}
        className="absolute inset-0 cursor-grab touch-none active:cursor-grabbing"
        style={{
          backgroundImage: "radial-gradient(circle, color-mix(in oklch, var(--muted-foreground) 35%, transparent) 1px, transparent 1.5px)",
          backgroundSize: `${GRID}px ${GRID}px`,
          backgroundPosition: `${pan.x}px ${pan.y}px`,
        }}
        onPointerDown={down}
        onPointerMove={move}
        onPointerUp={up}
        onPointerCancel={up}
      />
      <SampleToolbar />
    </div>
  );
}

// A sample of UI SDK controls, to see the theme next to Toru's own windows.
function SampleToolbar() {
  return (
    <div className="frost absolute top-3 left-1/2 flex -translate-x-1/2 items-center gap-3 px-3 py-2">
      <Button data-variant="solid">
        <PlusIcon aria-hidden="true" />
        Add block
      </Button>
      <Button>Fit</Button>
      <Select defaultValue="sticky" aria-label="Block kind">
        <SelectTrigger>
          <SelectValue />
          <ChevronDownIcon aria-hidden="true" />
        </SelectTrigger>
        <SelectContent className="frost">
          {[
            ["sticky", "Sticky note"],
            ["text", "Text"],
            ["shape", "Shape"],
            ["image", "Image"],
          ].map(([value, label]) => (
            <SelectItem key={value} value={value}>
              {label}
              <SelectItemIndicator>
                <CheckIcon aria-hidden="true" />
              </SelectItemIndicator>
            </SelectItem>
          ))}
        </SelectContent>
      </Select>
      <label data-slot="label" className="flex items-center gap-2 whitespace-nowrap">
        <Checkbox defaultChecked>
          <CheckboxIndicator>
            <CheckIcon aria-hidden="true" />
          </CheckboxIndicator>
        </Checkbox>
        Snap to grid
      </label>
      <Slider defaultValue={60} style={{ minWidth: "8rem" }}>
        <SliderControl>
          <SliderTrack>
            <SliderIndicator />
            <SliderThumb aria-label="Zoom" />
          </SliderTrack>
        </SliderControl>
      </Slider>
      <Popover>
        <PopoverTrigger>Share</PopoverTrigger>
        <PopoverContent className="frost">
          <PopoverTitle>Share this board</PopoverTitle>
          <PopoverDescription>Anyone with the link can view it.</PopoverDescription>
          <div className="flex justify-end gap-2">
            <PopoverClose>Cancel</PopoverClose>
            <PopoverClose data-variant="solid">Copy link</PopoverClose>
          </div>
        </PopoverContent>
      </Popover>
    </div>
  );
}
