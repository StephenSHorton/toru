import { defineConfig, type Plugin } from "vite";
import { fileURLToPath, URL } from "node:url";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import wails from "@wailsio/runtime/plugins/vite";

// stripWasmCustomSections drops the custom sections (debug names, producer
// info) from bundled .wasm files. Nothing reads them at run time, and in
// DeepFilterNet's build the function names alone are ~7.7 MB of the binary
// Toru embeds.
function stripWasmCustomSections(): Plugin {
  const strip = (b: Uint8Array): Uint8Array => {
    if (b.length < 8 || b[0] !== 0 || b[1] !== 0x61 || b[2] !== 0x73 || b[3] !== 0x6d) return b;
    const keep: Uint8Array[] = [b.subarray(0, 8)];
    let o = 8;
    while (o < b.length) {
      const start = o;
      const id = b[o++];
      let size = 0;
      for (let shift = 0; ; shift += 7) {
        const x = b[o++];
        size += (x & 0x7f) * 2 ** shift;
        if (!(x & 0x80)) break;
      }
      o += size;
      if (o > b.length) return b; // malformed: leave it alone
      if (id !== 0) keep.push(b.subarray(start, o));
    }
    const out = new Uint8Array(keep.reduce((n, k) => n + k.length, 0));
    let p = 0;
    for (const k of keep) {
      out.set(k, p);
      p += k.length;
    }
    return out;
  };
  return {
    name: "toru-strip-wasm-custom-sections",
    apply: "build",
    generateBundle(_, bundle) {
      for (const f of Object.values(bundle)) {
        if (f.type === "asset" && f.fileName.endsWith(".wasm") && typeof f.source !== "string") f.source = strip(f.source);
      }
    },
  };
}

// https://vitejs.dev/config/
export default defineConfig({
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  server: {
    host: "127.0.0.1",
    port: Number(process.env.WAILS_VITE_PORT) || 9245,
    strictPort: true,
  },
  plugins: [react(), tailwindcss(), wails("./bindings"), stripWasmCustomSections()],
});
