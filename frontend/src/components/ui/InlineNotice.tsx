import { Alert } from "@kobalte/core/alert";
import { AlertCircle, Info, X } from "lucide-solid";
import { Show } from "solid-js";

// Capability limits in the web preview are informational, not request failures.
function messageTone(message: string): "info" | "error" {
  return /仅在 EchoCode 桌面应用中可用[。.]?$/.test(message.trim()) ? "info" : "error";
}

export function InlineNotice(props: {
  message: string;
  tone?: "info" | "error";
  class?: string;
  dismissLabel?: string;
  onDismiss?: () => void;
}) {
  const tone = () => props.tone ?? messageTone(props.message);
  return (
    <Alert class={`inline-notice ${props.class ?? ""}`} data-tone={tone()} role={tone() === "info" ? "status" : "alert"}>
      <Show when={tone() === "info"} fallback={<AlertCircle size={16} aria-hidden="true" />}><Info size={16} aria-hidden="true" /></Show>
      <span class="inline-notice-message">{props.message}</span>
      <Show when={props.onDismiss}>
        <button type="button" aria-label={props.dismissLabel || "关闭提示"} onClick={props.onDismiss}><X size={14} /></button>
      </Show>
    </Alert>
  );
}
