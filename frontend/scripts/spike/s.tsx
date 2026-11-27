import { GlobalRegistrator } from "@happy-dom/global-registrator";
GlobalRegistrator.register({ url: "http://127.0.0.1/" });
(globalThis as any).__UI_SDK_STRICT__ = true;
(globalThis as any).IS_REACT_ACT_ENVIRONMENT = true;
const React = await import("react");
const { createRoot } = await import("react-dom/client");
const { act } = await import("react");
const { createAgent } = await import("@ui-sdk/react/agent");
const { Checkbox, CheckboxIndicator } = await import("@ui-sdk/react/checkbox");
const { RadioGroup, Radio, RadioIndicator } = await import("@ui-sdk/react/radio");
const { Rating, RatingItem } = await import("@ui-sdk/react/rating");
const { TokenInput, TokenInputEditor } = await import("@ui-sdk/react/token-input");
const { Textarea } = await import("@ui-sdk/react/textarea");
const { Input } = await import("@ui-sdk/react/input");
const { Button } = await import("@ui-sdk/react/button");
const { FileInput, FileInputTrigger } = await import("@ui-sdk/react/file-input");
const agent = createAgent();
const log: any[] = [];
function App() {
  const [done, setDone] = React.useState(false);
  return (
    <div>
      <label><Checkbox checked={done} onCheckedChange={(v: any, d: any) => { log.push(["cb", v, d.source]); setDone(v); }}><CheckboxIndicator /></Checkbox>Write docs</label>
      <RadioGroup aria-label="Lunch?" onValueChange={(v: any, d: any) => log.push(["radio", v, d.source])}>
        <label><Radio value="Pizza"><RadioIndicator /></Radio>Pizza (2)</label>
        <label><Radio value="Sushi"><RadioIndicator /></Radio>Sushi (0)</label>
      </RadioGroup>
      <Rating aria-label="Score idea" max={5} onValueChange={(v: any, d: any) => log.push(["rating", v, d.source])}><RatingItem /></Rating>
      <TokenInput defaultValue={[{ type: "tag", value: "ux", label: "ux" }]} onValueChange={(v: any, d: any) => log.push(["tok", JSON.stringify(v), d?.source])}>
        <TokenInputEditor aria-label="Tags of Themes" />
      </TokenInput>
      <Textarea aria-label="Sticky: hello" defaultValue="hello" onValueChange={(v: any, d: any) => log.push(["ta", v, d.source])} />
      <Input aria-label="Tasks row 1 Owner" onValueChange={(v: any, d: any) => log.push(["in", v, d.source])} />
      <Button onClick={() => log.push(["btn"])}>Sort Tasks by Owner</Button>
      <FileInput accept="image/*" onValueChange={(v: any, d: any) => log.push(["file", v.length, d?.source])}><FileInputTrigger>Choose image file</FileInputTrigger></FileInput>
    </div>
  );
}
const el = document.createElement("div"); document.body.append(el);
await act(async () => { createRoot(el).render(<App />); });
await act(async () => { await new Promise(r => setTimeout(r, 20)); });
console.log(JSON.stringify(agent.snapshot(), null, 1));
for (const step of [
  { ref: "Write docs", action: "toggle" },
  { ref: "Lunch?", action: "setValue", args: ["Sushi"] },
  { ref: "Score idea", action: "setValue", args: [4] },
  { ref: "Sticky: hello", action: "setValue", args: ["bye"] },
  { ref: "Tasks row 1 Owner", action: "setValue", args: ["Ana"] },
  { ref: "Sort Tasks by Owner", action: "press" },
  { ref: "Tags of Themes", action: "setValue", args: [[{ type: "tag", value: "a", label: "a" }, " "]] },
]) {
  let r: any;
  await act(async () => { r = await agent.act({ steps: [step as any] }); });
  console.log(JSON.stringify(step.ref), r.ok, r.error?.message ?? "");
}
console.log(log);
process.exit(0);
