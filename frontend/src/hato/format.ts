// Small formatting helpers for the Hato page.

const UNITS = ["KB", "MB", "GB", "TB"];

/** humanBytes renders a byte count the way a file manager would: "812 B", "4.2 MB", "1.3 GB". */
export function humanBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return "0 B";
  if (n < 1024) return `${Math.floor(n)} B`;
  let v = n / 1024;
  let i = 0;
  while (v >= 1024 && i < UNITS.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${v < 10 ? v.toFixed(1) : Math.round(v).toString()} ${UNITS[i]}`;
}

/** percent returns 0–100 for done/total, or null when the total is not known yet. */
export function percent(done: number, total: number): number | null {
  if (!Number.isFinite(total) || total <= 0) return null;
  return Math.max(0, Math.min(100, Math.round((done / total) * 100)));
}
