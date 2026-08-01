import { AlertTriangle, CircleHelp, X } from "lucide-solid";
import { Show, createEffect, onCleanup } from "solid-js";
import { Portal } from "solid-js/web";

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
  let dialogRef: HTMLElement | undefined;
  let cancelButtonRef: HTMLButtonElement | undefined;
  let confirmButtonRef: HTMLButtonElement | undefined;

  createEffect(() => {
    const request = props.request;
    if (!request) return;

    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : undefined;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        props.onResolve("dismiss");
      }
    };

    document.addEventListener("keydown", handleKeyDown);
    queueMicrotask(() => {
      (request.tone === "danger" ? cancelButtonRef : confirmButtonRef)?.focus();
    });

    onCleanup(() => {
      document.removeEventListener("keydown", handleKeyDown);
      previousFocus?.focus();
    });
  });

  return (
    <Show when={props.request}>
      {(request) => (
        <Portal>
          <div
            class="app-confirm-overlay"
            role="presentation"
            onPointerDown={(event) => {
              if (event.currentTarget === event.target) props.onResolve("dismiss");
            }}
          >
            <section
              ref={dialogRef}
              class="app-confirm-dialog"
              classList={{ danger: request().tone === "danger" }}
              role="dialog"
              aria-modal="true"
              aria-labelledby="app-confirm-title"
              aria-describedby="app-confirm-message"
            >
              <header class="app-confirm-head">
                <span class="app-confirm-icon" aria-hidden="true">
                  <Show when={request().tone === "danger"} fallback={<CircleHelp size={18} />}>
                    <AlertTriangle size={18} />
                  </Show>
                </span>
                <h2 id="app-confirm-title">{request().title}</h2>
                <button
                  type="button"
                  class="app-confirm-close"
                  title="关闭"
                  aria-label="关闭"
                  onClick={() => props.onResolve("dismiss")}
                >
                  <X size={15} />
                </button>
              </header>

              <div class="app-confirm-copy">
                <p id="app-confirm-message">{request().message}</p>
                <Show when={request().detail}>
                  <p class="app-confirm-detail">{request().detail}</p>
                </Show>
              </div>

              <footer class="app-confirm-actions">
                <button
                  ref={cancelButtonRef}
                  type="button"
                  class="secondary"
                  onClick={() => props.onResolve("cancel")}
                >
                  {request().cancelLabel || "取消"}
                </button>
                <button
                  ref={confirmButtonRef}
                  type="button"
                  class="primary"
                  onClick={() => props.onResolve("confirm")}
                >
                  {request().confirmLabel || "确认"}
                </button>
              </footer>
            </section>
          </div>
        </Portal>
      )}
    </Show>
  );
}
