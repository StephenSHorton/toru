// Pure helpers for mic voiceover takes (recorder.ts): no browser or Go calls,
// so scripts/voice-check.ts can run them under bun (listMics only touches
// navigator when called).

export type MicOption = { id: string; label: string };

// listMics is the mic inputs the WebView sees. Labels are empty until the
// mic was allowed once; then they read like the dshow names in Settings.
export async function listMics(): Promise<MicOption[]> {
  const all = await navigator.mediaDevices.enumerateDevices().catch(() => []);
  return all
    .filter((d) => d.kind === "audioinput" && d.deviceId !== "communications")
    .map((d) => ({ id: d.deviceId, label: d.label || (d.deviceId === "default" ? "Default mic" : "Mic") }));
}

// matchMic finds the WebView device for a dshow name: the same label, else
// the label that starts with it (dshow cuts names at 31 characters), else
// one whose label contains it. Undefined = use the default device.
export function matchMic(mics: MicOption[], dshow: string): MicOption | undefined {
  const name = dshow.trim().toLowerCase();
  if (!name) return undefined;
  const real = mics.filter((m) => m.id !== "default");
  const strip = (l: string) => l.toLowerCase().replace(/^default - /, "");
  return (
    real.find((m) => strip(m.label) === name) ??
    real.find((m) => strip(m.label).startsWith(name)) ??
    real.find((m) => name.length >= 8 && strip(m.label).includes(name))
  );
}

// toPcm16 converts float samples (-1..1) to little-endian 16-bit, clipped.
export function toPcm16(f: Float32Array): Uint8Array {
  const out = new Uint8Array(f.length * 2);
  const v = new DataView(out.buffer);
  for (let i = 0; i < f.length; i++) {
    const s = Math.max(-1, Math.min(1, f[i]));
    v.setInt16(i * 2, s < 0 ? Math.round(s * 32768) : Math.round(s * 32767), true);
  }
  return out;
}

