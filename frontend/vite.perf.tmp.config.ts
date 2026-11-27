import base from "./vite.config";
export default { ...base, build: { outDir: "node_modules/.perfdist", emptyOutDir: true, rollupOptions: { input: "C:/Users/4step/.hearth-app.hearth.cards/worktrees/32ce3e35-b278-409c-aa80-c8376406c3e9/whiteboard-23-6821c370/frontend/board-perf.html" } }, preview: { host: "127.0.0.1", port: 47831, strictPort: true } };
