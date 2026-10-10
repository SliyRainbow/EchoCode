import { onCleanup, onMount } from "solid-js";
import { getUpdateState, onApprovalRequest, onBrowserPreviewClose, onBrowserPreviewOpen, onChatTaskEvent, onMCPState, onUpdateState } from "../services/workbench";
import type { ApprovalRequest, BrowserPreview, ChatTaskEvent, UpdateState, WorkbenchState } from "../types";

// One lifecycle owns host subscriptions and initial restoration. Subscriptions
// are installed before loading history so early task events are not missed.
export function createWorkbenchRuntime(handlers: {
  initialize: () => Promise<void>;
  approval: (request: ApprovalRequest) => void;
  browserOpen: (preview: BrowserPreview) => void;
  browserClose: () => void;
  task: (event: ChatTaskEvent) => void;
  state: (state: WorkbenchState) => void;
  update: (state: UpdateState) => void;
  error: (error: unknown) => void;
}) {
  onMount(() => {
    let disposed = false;
    const subscriptions = [
      onApprovalRequest(handlers.approval),
      onBrowserPreviewOpen(handlers.browserOpen),
      onBrowserPreviewClose(handlers.browserClose),
      onChatTaskEvent(handlers.task),
      onMCPState(handlers.state),
      onUpdateState(handlers.update),
    ];
    onCleanup(() => {
      disposed = true;
      subscriptions.forEach(unsubscribe => unsubscribe());
    });
    void getUpdateState().then(state => { if (!disposed) handlers.update(state); }).catch(() => undefined);
    void handlers.initialize().catch(error => { if (!disposed) handlers.error(error); });
  });
}
