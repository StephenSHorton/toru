import {
  AlertDialog,
  AlertDialogBackdrop,
  AlertDialogClose,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogTitle,
  createAlertDialogController,
} from "@ui-sdk/react/alert-dialog";
import type { AgentAction } from "@ui-sdk/react/agent";

// Asking the person before an agent's step runs on a control marked
// data-agent-confirm (removing a checklist item, poll option or table column).
// The board window passes `allow` to createAgent and renders <AskPerson>; the
// bridge then waits on the person's answer instead of failing the step with
// "Needs a person". A no fails it with `The person said no to "…"`.

/** The question an agent's step puts to the person. */
export type PersonQuestion = { confirm: string };

export type AskPersonApi = ReturnType<typeof createAskPerson>;

export function createAskPerson() {
  const controller = createAlertDialogController<PersonQuestion, string>();
  /** Shows `confirm` and resolves true only on Allow. Closes the dialog when `signal` aborts (the bridge's timeout, or the agent's turn stopped). */
  const ask = async (confirm: string, signal?: AbortSignal) => {
    const result = await controller.open({ payload: { confirm }, signal });
    return result.returnValue === "allow";
  };
  /** createAgent's allow: steps on unmarked controls run; marked ones wait for the person. */
  const allow = (step: AgentAction, { signal }: { signal: AbortSignal }) =>
    step.confirm ? ask(step.confirm, signal) : true;
  return { controller, ask, allow };
}

/** The dialog the person answers. Kept from the agent, so only the person can say yes. */
export function AskPerson({ api }: { api: AskPersonApi }) {
  return (
    <AlertDialog controller={api.controller}>
      <AlertDialogBackdrop data-agent-hidden />
      <AlertDialogContent<PersonQuestion> data-agent-hidden data-ask-person className="frost flex max-w-md flex-col gap-4 p-5">
        {(state) => (
          <>
            <div className="flex flex-col gap-1.5">
              <AlertDialogTitle>An agent wants to change this board</AlertDialogTitle>
              <AlertDialogDescription>{state.payload?.confirm}</AlertDialogDescription>
            </div>
            <div className="flex justify-end gap-2">
              <AlertDialogClose value="deny">Don't allow</AlertDialogClose>
              <AlertDialogClose data-variant="solid" value="allow">
                Allow
              </AlertDialogClose>
            </div>
          </>
        )}
      </AlertDialogContent>
    </AlertDialog>
  );
}

