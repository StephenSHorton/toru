import type { ClickerSettings, ClickerStatus } from "@/lib/api";

// Placeholder: S7b replaces this card. The signature is the page's contract.
export default function SafetyCard(_props: {
  settings: ClickerSettings;
  status: ClickerStatus;
  update: (fn: (s: ClickerSettings) => ClickerSettings) => void;
}) {
  return (
    <section aria-label="Safety" className="frost flex flex-col gap-2 border border-border p-4">
      <span className="text-sm font-medium">Safety</span>
      <span className="text-xs text-muted-foreground">Coming soon.</span>
    </section>
  );
}
