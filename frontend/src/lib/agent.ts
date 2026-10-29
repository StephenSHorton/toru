import { Events as WailsEvents, Window } from "@wailsio/runtime";
import { AgentService } from "@/lib/api";

// Window commands for agent control (docs/agent-control.md). A window
// registers what an agent can do with it while it's open:
//
//   const off = registerAgentCommand("editor.split", {
//     description: "Split the clip under the playhead (or at t seconds).",
//     input: { type: "object", properties: { t: { type: "number" } } },
//   }, async ({ t }) => { ...; return { ok: true }; });
//   // later: off()
//
// Go lists them with the app's own commands and, when an agent calls one,
// sends "agent:call" to this window; the handler's result (or thrown error)
// goes back with AgentService.Respond. Handlers should change state through
// the window's undo history so the person sees each change and can undo it.
// A command that deletes or overwrites things sets destructive: Go then
// refuses it unless the input has "confirm": true.

export interface AgentCommandSpec {
  description: string;
  input?: Record<string, unknown>; // JSON Schema for the input object
  destructive?: boolean;
}

// eslint-disable-next-line @typescript-eslint/no-explicit-any -- inputs are whatever the schema says
export type AgentHandler = (input: any) => unknown | Promise<unknown>;

type Entry = { spec: AgentCommandSpec; handler: AgentHandler };

const commands = new Map<string, Entry>();
let windowName: Promise<string> | null = null;
let syncTimer = 0;
let started = false;

// registerAgentCommand offers name to agents while this window is open. It
// returns a function that withdraws it. Registering a name again replaces it.
export function registerAgentCommand(name: string, spec: AgentCommandSpec, handler: AgentHandler): () => void {
  start();
  const entry = { spec, handler };
  commands.set(name, entry);
  scheduleSync();
  return () => {
    if (commands.get(name) === entry) {
      commands.delete(name);
      scheduleSync();
    }
  };
}

function myWindow(): Promise<string> {
  windowName ??= Window.Name().catch(() => "");
  return windowName;
}

// Registrations come in bursts (a window mounting); send the set once.
function scheduleSync() {
  window.clearTimeout(syncTimer);
  syncTimer = window.setTimeout(() => void sync(), 0);
}

async function sync() {
  const win = await myWindow();
  if (!win) return;
  const cmds = [...commands].map(([name, { spec }]) => ({
    name,
    description: spec.description,
    input: spec.input ?? null,
    destructive: !!spec.destructive,
  }));
  try {
    if (cmds.length) await AgentService.RegisterWindowCommands(win, cmds);
    else await AgentService.UnregisterWindowCommands(win);
  } catch (err) {
    console.warn("agent commands:", err);
  }
}

function start() {
  if (started) return;
  started = true;
  WailsEvents.On("agent:call", (ev: { data: unknown }) => void onCall(ev.data));
  // The most recently focused window answers when an agent doesn't name one.
  window.addEventListener("focus", () => {
    void myWindow().then((w) => {
      if (w && commands.size) AgentService.TouchWindow(w).catch(() => {});
    });
  });
  // Best effort: Go also forgets a window it finds closed.
  window.addEventListener("beforeunload", () => {
    void myWindow().then((w) => {
      if (w) AgentService.UnregisterWindowCommands(w).catch(() => {});
    });
  });
}

async function onCall(data: unknown) {
  const call = data as { id?: string; window?: string; name?: string; input?: unknown };
  if (!call?.id || !call.name) return;
  if (call.window !== (await myWindow())) return; // another window's call
  const entry = commands.get(call.name);
  let result: unknown = null;
  let error = "";
  if (!entry) {
    error = `${call.name} isn't available in this window any more`;
  } else {
    try {
      result = (await entry.handler(call.input ?? {})) ?? null;
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    }
  }
  try {
    await AgentService.Respond(call.id, result, error);
  } catch (err) {
    console.warn("agent respond:", err);
  }
}
