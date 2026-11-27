import { defineConfig, type Plugin } from "vite";
import { fileURLToPath, URL } from "node:url";
import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { join } from "node:path";
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

// keepDistPlaceholder puts back the empty dist/.gitkeep that Vite's emptyOutDir
// removes. Git tracks that file so `go test ./...` compiles on a fresh clone:
// main.go's //go:embed all:frontend/dist needs at least one file there.
function keepDistPlaceholder(): Plugin {
  let outDir = "";
  return {
    name: "toru-keep-dist-placeholder",
    apply: "build",
    configResolved(c) {
      outDir = join(c.root, c.build.outDir);
    },
    closeBundle() {
      writeFileSync(join(outDir, ".gitkeep"), "");
    },
  };
}

// boardImagesWithoutGo serves the whiteboard's sample picture at
// /__board/image/<name> (and /__file/<name>) from internal/board/testdata/images when the page runs
// on Vite alone (no Go behind it, as for a screenshot of the sample board).
// Under Wails, Go's board.ImageService answers these paths first.
function boardImagesWithoutGo(): Plugin {
  const dir = fileURLToPath(new URL("../internal/board/testdata/images", import.meta.url));
  return {
    name: "toru-board-images-without-go",
    apply: "serve",
    configureServer(server) {
      const serve = (req: { url?: string }, res: { setHeader: (k: string, v: string) => void; end: (b: Buffer) => void }, next: () => void) => {
        const name = decodeURIComponent((req.url ?? "").replace(/^\//, "").split("?")[0]);
        const file = join(dir, name);
        if (!/^[0-9a-f]{64}\.png$/.test(name) || !existsSync(file)) return next();
        res.setHeader("Content-Type", "image/png");
        res.end(readFileSync(file));
      };
      server.middlewares.use("/__board/image/", serve);
      // The same picture as a Library screenshot, for whiteboards-preview.html.
      server.middlewares.use("/__file/", serve);
    },
  };
}

// boardLiveDemo, in `vite --mode board-demo` (or with TORU_BOARD_DEMO=1), starts Go's live board demo server
// (board_live_demo_test.go: Toru's real board store and agent board commands
// on a temporary folder, an agent building a board once a window watches)
// and passes /__board/live/ to it, so the whiteboard window runs live in a
// browser at /?view=whiteboard&id=brd-demo&link=http. Vite listens once Go
// answers; Go stops when Vite does.
function boardLiveDemo(): Plugin {
  const port = 47_900 + Math.floor(Math.random() * 90);
  const target = `http://127.0.0.1:${port}`;
  let on = false;
  // `vite --mode share-demo`: two Torus sharing one board through the real
  // room instead (board_share_demo_test.go), shown side by side at
  // /share-demo.html.
  let share = false;
  return {
    name: "toru-board-live-demo",
    apply: "serve",
    config: (_, env) => {
      share = env.mode === "share-demo";
      on = share || env.mode === "board-demo" || !!process.env.TORU_BOARD_DEMO;
      return on ? { server: { proxy: { "/__board/live/": target, "/__board/live-b/": target } } } : {};
    },
    async configureServer() {
      if (!on) return;
      const { spawn } = await import("node:child_process");
      const root = fileURLToPath(new URL("..", import.meta.url));
      const go = spawn("go", ["test", "-run", share ? "^TestBoardShareDemo$" : "^TestBoardLiveDemo$", "-count=1", "-timeout", "0", "."], {
        cwd: root,
        env: {
          ...process.env,
          [share ? "TORU_BOARD_SHARE_DEMO_PORT" : "TORU_BOARD_DEMO_PORT"]: String(port),
          TORU_BOARD_DEMO_PARENT: String(process.pid),
        },
        stdio: ["ignore", "inherit", "inherit"],
      });
      process.on("exit", () => go.kill());
      for (let i = 0; i < 600; i++) {
        if (go.exitCode !== null) throw new Error("the live board demo server stopped");
        try {
          const res = await fetch(`${target}/__board/live/load`, { method: "POST", body: '{"board":"brd-demo"}' });
          if (res.ok) return;
        } catch {
          /* not up yet */
        }
        await new Promise((r) => setTimeout(r, 200));
      }
      throw new Error("the live board demo server didn't start");
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
    // The effect definitions live in internal/fx/defs (Go embeds them too);
    // the dev server may serve files from the whole repo.
    fs: { allow: [fileURLToPath(new URL("..", import.meta.url))] },
  },
  plugins: [react(), tailwindcss(), wails("./bindings"), stripWasmCustomSections(), keepDistPlaceholder(), boardImagesWithoutGo(), boardLiveDemo()],
});
