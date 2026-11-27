import { useEffect, useState } from "react";
import { Check, Copy } from "lucide-react";
import { Button } from "@/components/ui/button";
import { AgentService, ExportService } from "@/lib/api";
import { errText } from "@/transfer/useTransfer";

// Settings → Connect your agent: how to add Toru's MCP server
// (`toru.exe mcp`, docs/agent-control.md) to Claude Code or any other MCP
// client, filled in with this install's toru.exe. Agents and scripts can
// always reach a running Toru over a pipe only this Windows user can open.

type Info = { exe: string; claudeCommand: string; mcpConfig: string };

export function ConnectAgentPanel() {
  const [info, setInfo] = useState<Info | null>(null);
  const [error, setError] = useState("");

  useEffect(() => {
    void AgentService.GetConnectInfo()
      .then((i) => setInfo(i))
      .catch((e: unknown) => setError(errText(e)));
  }, []);

  return (
    <div className="frost flex flex-col gap-5 p-5" data-testid="connect-agent">
      <div className="flex flex-col gap-1">
        <span className="text-sm font-medium">Connect your agent</span>
        <span className="text-xs text-muted-foreground">
          Once connected, an AI agent can build and edit whiteboards, edit videos, take screenshots, manage the
          Library and run the rest of Toru's commands. Toru only answers on this computer, for this Windows user.
        </span>
      </div>

      {error ? <p className="text-xs text-destructive">Couldn't find Toru's program file: {error}</p> : null}

      <CopyBlock
        title="Claude Code"
        hint="Run this in a terminal. It adds Toru for every project."
        text={info?.claudeCommand ?? ""}
        label="Claude Code command"
      />
      <CopyBlock
        title="Other MCP clients"
        hint="Add this to the client's MCP servers config."
        text={info?.mcpConfig ?? ""}
        label="MCP config"
      />

      <span className="text-xs text-muted-foreground">
        Scripts can run the same commands with <code className="font-mono">toru.exe ctl list</code> and{" "}
        <code className="font-mono">toru.exe ctl &lt;command&gt;</code>.
      </span>
    </div>
  );
}

function CopyBlock({ title, hint, text, label }: { title: string; hint: string; text: string; label: string }) {
  const [copied, setCopied] = useState(false);

  useEffect(() => {
    if (!copied) return;
    const t = window.setTimeout(() => setCopied(false), 1500);
    return () => window.clearTimeout(t);
  }, [copied]);

  const copy = async () => {
    try {
      await ExportService.CopyTextAs(text, label);
      setCopied(true);
    } catch {
      // the toast reports a failed copy
    }
  };

  return (
    <div className="flex flex-col gap-2">
      <div className="flex items-end justify-between gap-3">
        <div className="flex flex-col">
          <span className="text-sm font-medium">{title}</span>
          <span className="text-xs text-muted-foreground">{hint}</span>
        </div>
        <Button size="sm" variant="secondary" disabled={!text} onClick={() => void copy()} aria-label={`Copy ${label}`}>
          {copied ? <Check /> : <Copy />} {copied ? "Copied" : "Copy"}
        </Button>
      </div>
      <pre className="overflow-x-auto whitespace-pre rounded-none border border-border bg-background/40 px-3 py-2 font-mono text-[11px] text-muted-foreground select-text">
        {text || "…"}
      </pre>
    </div>
  );
}
