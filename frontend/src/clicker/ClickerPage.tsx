import { useEffect, useRef, useState } from "react";
import { AlertTriangle, X } from "lucide-react";
import { cn } from "@/lib/utils";
import CadenceCard from "./CadenceCard";
import InputCard from "./InputCard";
import LimitsCard from "./LimitsCard";
import PointsCard from "./PointsCard";
import PresetsCard from "./PresetsCard";
import ProcessListCard from "./ProcessListCard";
import RunCard from "./RunCard";
import SafetyCard from "./SafetyCard";
import StatsCard from "./StatsCard";
import TimingCard from "./TimingCard";
import ZonesCard from "./ZonesCard";
import { isActive, useClicker } from "./useClicker";

// Auto Clicker — clicks a mouse button or presses a key at a set rate, with
// click points, failsafes and presets. All logic lives in Go
// (internal/clicker); this page renders ClickerService state through
// useClicker and sends edits back. A sticky mini-nav jumps between the
// sections and the last one viewed is reopened next time.

const SECTIONS = [
  { id: "run", label: "Run" },
  { id: "clicking", label: "Clicking" },
  { id: "points", label: "Points" },
  { id: "safety", label: "Safety" },
  { id: "presets", label: "Presets" },
] as const;
type SectionId = (typeof SECTIONS)[number]["id"];

const SECTION_KEY = "toru.clicker.section";

function loadSection(): SectionId {
  try {
    const v = localStorage.getItem(SECTION_KEY);
    if (v && SECTIONS.some((s) => s.id === v)) return v as SectionId;
  } catch {
    /* storage unavailable: start at the top */
  }
  return "run";
}

function saveSection(id: SectionId) {
  try {
    localStorage.setItem(SECTION_KEY, id);
  } catch {
    /* not critical */
  }
}

const anchorId = (id: SectionId) => `clicker-${id}`;

export default function ClickerPage() {
  const c = useClicker();
  const [current, setCurrent] = useState<SectionId>(loadSection);
  const restored = useRef(false);
  const settings = c.settings;

  function jump(id: SectionId, smooth = true) {
    setCurrent(id);
    saveSection(id);
    document.getElementById(anchorId(id))?.scrollIntoView({ behavior: smooth ? "smooth" : "auto", block: "start" });
  }

  // Reopen the remembered section once the cards exist to scroll to.
  useEffect(() => {
    if (!settings || restored.current) return;
    restored.current = true;
    const id = loadSection();
    if (id !== "run") requestAnimationFrame(() => jump(id, false));
  }, [settings]);

  // Follow the reader: the topmost section in view becomes the current one.
  useEffect(() => {
    if (!settings) return;
    const els = SECTIONS.map((s) => document.getElementById(anchorId(s.id))).filter(
      (el): el is HTMLElement => el !== null,
    );
    const seen = new Map<string, boolean>();
    const io = new IntersectionObserver(
      (entries) => {
        for (const e of entries) seen.set(e.target.id, e.isIntersecting);
        const first = SECTIONS.find((s) => seen.get(anchorId(s.id)));
        if (first) {
          setCurrent(first.id);
          saveSection(first.id);
        }
      },
      { rootMargin: "-56px 0px -55% 0px" },
    );
    els.forEach((el) => io.observe(el));
    return () => io.disconnect();
  }, [settings]);

  return (
    <div className="mx-auto flex max-w-4xl flex-col gap-4">
      <div>
        <h1 className="text-xl font-semibold tracking-tight">Auto Clicker</h1>
        <p className="text-sm text-muted-foreground">
          Click or press a key at a set rate, with failsafes that stop it when you need them to.
        </p>
      </div>

      <nav aria-label="Auto Clicker sections" className="frost sticky top-0 z-20 flex gap-1 border border-border p-1">
        {SECTIONS.map((s) => (
          <button
            key={s.id}
            type="button"
            onClick={() => jump(s.id)}
            aria-current={current === s.id ? "true" : undefined}
            className={cn(
              "h-7 px-3 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring",
              current === s.id ? "bg-primary text-primary-foreground" : "text-muted-foreground hover:bg-accent hover:text-foreground",
            )}
          >
            {s.label}
          </button>
        ))}
      </nav>

      {c.error ? (
        <div role="alert" className="flex items-start gap-2 border border-destructive/60 bg-destructive/10 px-3 py-2 text-sm">
          <AlertTriangle className="mt-0.5 size-4 shrink-0 text-destructive" />
          <span className="min-w-0 flex-1">{c.error}</span>
          <button type="button" aria-label="Dismiss" onClick={c.clearError} className="text-muted-foreground hover:text-foreground">
            <X className="size-4" />
          </button>
        </div>
      ) : null}

      {!settings ? (
        <p className="text-sm text-muted-foreground">Loading…</p>
      ) : (
        <>
          <div id={anchorId("run")} className="grid scroll-mt-14 gap-4 lg:grid-cols-2">
            <RunCard
              settings={settings}
              status={c.status}
              update={c.update}
              stats={c.stats}
              apply={c.apply}
              run={c.run}
            />
          </div>

          <div id={anchorId("clicking")} className="grid scroll-mt-14 gap-4 lg:grid-cols-2">
            <CadenceCard settings={settings} status={c.status} update={c.update} />
            <InputCard settings={settings} status={c.status} update={c.update} />
            <TimingCard settings={settings} status={c.status} update={c.update} />
            <LimitsCard settings={settings} status={c.status} update={c.update} />
          </div>

          <div id={anchorId("points")} className="flex scroll-mt-14 flex-col gap-4">
            <PointsCard settings={settings} status={c.status} update={c.update} />
          </div>

          <div id={anchorId("safety")} className="grid scroll-mt-14 gap-4 lg:grid-cols-2">
            <SafetyCard settings={settings} status={c.status} update={c.update} />
            <ZonesCard settings={settings} status={c.status} update={c.update} />
            <ProcessListCard settings={settings} status={c.status} update={c.update} />
          </div>

          <div id={anchorId("presets")} className="grid scroll-mt-14 gap-4 lg:grid-cols-3">
            <PresetsCard settings={settings} status={c.status} update={c.update} apply={c.apply} run={c.run} />
            <StatsCard stats={c.stats} disabled={isActive(c.status)} onReset={c.resetStats} />
          </div>
        </>
      )}
    </div>
  );
}
