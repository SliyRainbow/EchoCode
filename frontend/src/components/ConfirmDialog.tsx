import { Dialog } from "@kobalte/core/dialog";
import { AlertTriangle, CircleHelp, X } from "lucide-solid";
import { Show } from "solid-js";

export type ConfirmationResult = "confirm" | "cancel" | "dismiss";
export type ConfirmationRequest = {
  title: string;
  message: string;
  detail?: string;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: "default" | "danger";
};

export function ConfirmDialog(props: {
  request?: ConfirmationRequest;
  onResolve: (result: ConfirmationResult) => void;
}) {
  let cancelButtonRef: HTMLButtonElement | undefined;
  let confirmButtonRef: HTMLButtonElement | undefined;
  return (
    <Dialog open={Boolean(props.request)} onOpenChange={(open) => { if (!open) props.onResolve("dismiss"); }}>
      <Dialog.Portal>
        <Dialog.Overlay class="ui-modal-backdrop" />
        <div class="ui-modal-positioner">
          <Dialog.Content class="app-confirm-dialog" classList={{ danger: props.request?.tone === "danger" }} onOpenAutoFocus={(event) => {
            event.preventDefault();
            (props.request?.tone === "danger" ? cancelButtonRef : confirmButtonRef)?.focus();
          }}>
            <header class="app-confirm-head">
              <span class="app-confirm-icon" aria-hidden="true"><Show when={props.request?.tone === "danger"} fallback={<CircleHelp size={18} />}><AlertTriangle size={18} /></Show></span>
              <Dialog.Title>{props.request?.title}</Dialog.Title>
              <Dialog.CloseButton class="app-confirm-close" aria-label="关闭"><X size={15} /></Dialog.CloseButton>
            </header>
            <div class="app-confirm-copy">
              <Dialog.Description>{props.request?.message}</Dialog.Description>
              <Show when={props.request?.detail}><p class="app-confirm-detail">{props.request?.detail}</p></Show>
            </div>
            <footer class="app-confirm-actions">
              <button ref={cancelButtonRef} type="button" class="secondary" onClick={() => props.onResolve("cancel")}>{props.request?.cancelLabel || "取消"}</button>
              <button ref={confirmButtonRef} type="button" class="primary" onClick={() => props.onResolve("confirm")}>{props.request?.confirmLabel || "确认"}</button>
            </footer>
          </Dialog.Content>
        </div>
      </Dialog.Portal>
    </Dialog>
  );
}
