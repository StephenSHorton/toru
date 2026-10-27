/**
 * errorMessage reads a rejected binding call as plain text. The Wails v3 HTTP
 * transport rejects with an Error whose message is the JSON error body
 * ({"message":"...","cause":{},"kind":"RuntimeError"}), so that is unwrapped
 * to its message; a leading "Error: " is dropped. "" when there is nothing.
 */
export function errorMessage(e: unknown): string {
  let raw = (e instanceof Error ? e.message : typeof e === "string" ? e : String(e ?? ""))
    .replace(/^Error:\s*/, "")
    .trim();
  if (raw.startsWith("{")) {
    try {
      const body = JSON.parse(raw) as { message?: unknown };
      if (typeof body?.message === "string") raw = body.message.trim();
    } catch {
      // not JSON after all: show it as is
    }
  }
  return raw;
}
