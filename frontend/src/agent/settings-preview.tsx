import React from "react";
import ReactDOM from "react-dom/client";
import "../index.css";
import Settings from "@/routes/Settings";

// settings-preview.html (Vite only, never built): the main window opened on
// Settings, with Go's bound methods answered here instead of by Toru, so
// Settings > Connect your agent can be seen and screenshotted without Toru
// running. ?tab= picks the Settings tab (default "agent").

const exe = String.raw`C:\Users\you\AppData\Local\Programs\Toru\toru.exe`;
const answers: Record<number, unknown> = {
  // AgentService.GetConnectInfo (frontend/bindings/.../agentctl/service.ts)
  2299698287: {
    exe,
    claudeCommand: `claude mcp add --scope user toru -- "${exe}" mcp`,
    mcpConfig: JSON.stringify({ mcpServers: { toru: { command: exe, args: ["mcp"] } } }, null, 2),
  },
};

const realFetch = window.fetch.bind(window);
window.fetch = async (input, init) => {
  const url = typeof input === "string" ? input : input instanceof URL ? input.href : input.url;
  if (!url.includes("/wails/runtime")) return realFetch(input, init);
  let id = 0;
  try {
    id = JSON.parse(String(init?.body ?? "{}"))?.args?.methodID ?? 0;
  } catch {
    // not a bound-method call
  }
  // Anything else fails, so the page keeps its own defaults.
  if (!(id in answers)) return new Response("not in the preview", { status: 500 });
  return new Response(JSON.stringify(answers[id]), { headers: { "Content-Type": "application/json" } });
};

const params = new URLSearchParams(window.location.search);
window.localStorage.setItem("toru.settings.tab", params.get("tab") ?? "agent");
if (!params.has("page")) window.history.replaceState(null, "", `?page=settings&${params.toString()}`);

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <Settings />
  </React.StrictMode>,
);
