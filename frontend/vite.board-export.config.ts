import { defineConfig, type Plugin } from "vite";
import { fileURLToPath, URL } from "node:url";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { groupSelectors } from "./group-selectors";

// The board export's viewer (src/board-export, docs/whiteboard.md section 3):
// one HTML file, dist/board-export.html, with every script, style and asset
// inlined, so an exported board opens offline in any browser. Go embeds it
// with the rest of dist and fills in the board (internal/boardexport). Built
// after the app (`bun run build`), into the same dist.

// singleFile inlines the built script and stylesheet into the HTML page and
// drops them from the output.
function singleFile(): Plugin {
  return {
    name: "toru-board-export-single-file",
    apply: "build",
    enforce: "post",
    generateBundle(_, bundle) {
      const page = Object.values(bundle).find((f) => f.type === "asset" && f.fileName.endsWith(".html"));
      if (!page || page.type !== "asset") throw new Error("board-export: no page");
      let html = String(page.source);
      const inlined = new Set<string>();
      for (const [name, f] of Object.entries(bundle)) {
        if (f.type === "chunk") {
          const tag = new RegExp(`<script[^>]*src="[^"]*${escape(f.fileName)}"[^>]*></script>`);
          if (!tag.test(html)) throw new Error(`board-export: ${f.fileName} isn't in the page`);
          // "</script" inside the code would end the tag early.
          const code = f.code.replace(/<\/(script)/gi, "<\\/$1");
          html = html.replace(tag, () => `<script type="module">${code}</script>`);
          inlined.add(name);
          delete bundle[name];
        } else if (f.fileName.endsWith(".css")) {
          const tag = new RegExp(`<link[^>]*href="[^"]*${escape(f.fileName)}"[^>]*>`);
          if (!tag.test(html)) throw new Error(`board-export: ${f.fileName} isn't in the page`);
          const css = String(f.source).replace(/<\/(style)/gi, "<\\/$1");
          html = html.replace(tag, () => `<style>${css}</style>`);
          inlined.add(name);
          delete bundle[name];
        }
      }
      for (const [name, f] of Object.entries(bundle)) {
        if (f !== page && !inlined.has(name)) throw new Error(`board-export: ${f.fileName} would be left beside the page`);
      }
      if (/<(script|link)[^>]+(src|href)="/.test(html.replace(/<script type="module">[\s\S]*?<\/script>/g, ""))) {
        throw new Error("board-export: the page still loads a file");
      }
      page.source = html;
    },
  };
}

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

export default defineConfig({
  // The same rewrite of group-* rules as the app (group-selectors.ts).
  css: { postcss: { plugins: [groupSelectors()] } },
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
  base: "./",
  plugins: [react(), tailwindcss(), singleFile()],
  build: {
    outDir: "dist",
    emptyOutDir: false,
    // Every asset inline: the page loads nothing.
    assetsInlineLimit: () => true,
    cssCodeSplit: false,
    modulePreload: false,
    rollupOptions: {
      input: fileURLToPath(new URL("./board-export.html", import.meta.url)),
      output: { codeSplitting: false },
    },
  },
});
