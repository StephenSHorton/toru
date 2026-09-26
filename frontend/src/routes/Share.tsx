import { useCallback, useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Square } from "lucide-react";
import { Window } from "@wailsio/runtime";
import { OverlayService } from "@/lib/api";

// The share card. Go opens it after the overlay hides, then this page reads
// the link and QR code. Stop ends the stream; closing the window does too.
type ShareCard = {
  url: string;
  urls: string[];
  kind: string;
  qrPng: string;
  hint: string;
};

export default function Share() {
  const [info, setInfo] = useState<ShareCard | null>(null);
  const [error, setError] = useState("");
  const [stopping, setStopping] = useState(false);
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    let gone = false;
    const load = async () => {
      try {
        const got = (await OverlayService.ShareInfo()) as ShareCard;
        if (!gone) setInfo(got);
      } catch (e) {
        if (!gone) setError(e instanceof Error ? e.message : String(e));
      }
    };
    void load();
    return () => {
      gone = true;
    };
  }, []);

  const stop = useCallback(async () => {
    if (stopping) return;
    setStopping(true);
    try {
      await OverlayService.StopShare();
      await Window.Close();
    } catch (e) {
      setError(e instanceof Error ? e.message : String(e));
      setStopping(false);
    }
  }, [stopping]);

  const copy = useCallback(async () => {
    if (!info?.url) return;
    try {
      await navigator.clipboard.writeText(info.url);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }, [info]);

  return (
    <div className="flex h-screen flex-col gap-3 bg-background p-4 text-foreground">
      <div
        className="flex items-center gap-2"
        style={{ "--wails-draggable": "drag" } as React.CSSProperties}
      >
        <span className="relative flex size-3">
          <span className="absolute inline-flex h-full w-full animate-ping rounded-full bg-sky-400 opacity-60" />
          <span className="relative inline-flex size-3 rounded-full bg-sky-400" />
        </span>
        <span className="text-sm font-medium">Sharing</span>
      </div>
      <p className="text-[11px] text-muted-foreground">
        Drag this card off the shared area if it shows up on the other screen.
      </p>

      {error ? <p className="text-xs text-red-400">{error}</p> : null}

      {info ? (
        <>
          <p className="text-xs leading-relaxed text-muted-foreground">{info.hint}</p>
          {info.qrPng ? (
            <img
              alt="QR code for the share link"
              className="mx-auto bg-white p-2"
              width={220}
              height={220}
              src={`data:image/png;base64,${info.qrPng}`}
            />
          ) : null}
          <input
            readOnly
            value={info.url}
            className="w-full border border-border bg-background px-2 py-1 font-mono text-xs"
            onFocus={(e) => e.currentTarget.select()}
          />
          {(info.urls ?? []).filter((u) => u !== info.url).length > 0 ? (
            <ul className="space-y-1 font-mono text-[11px] text-muted-foreground">
              {info.urls
                .filter((u) => u !== info.url)
                .map((u) => (
                  <li key={u}>{u}</li>
                ))}
            </ul>
          ) : null}
          <div className="mt-auto flex gap-2">
            <Button size="sm" variant="ghost" onClick={() => void copy()}>
              {copied ? "Copied" : "Copy link"}
            </Button>
            <Button
              size="sm"
              variant="destructive"
              className="ml-auto"
              disabled={stopping}
              onClick={() => void stop()}
            >
              <Square className="size-3.5 fill-current" />
              {stopping ? "Stopping…" : "Stop"}
            </Button>
          </div>
        </>
      ) : !error ? (
        <p className="text-xs text-muted-foreground">Starting the stream…</p>
      ) : null}
    </div>
  );
}
