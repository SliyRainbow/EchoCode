import { Show, createEffect } from "solid-js";
import { Dialog } from "@kobalte/core/dialog";
import { Check, ShieldAlert, X } from "lucide-solid";
import type { ApprovalRequest } from "../types";
import { MessageContent } from "./chat/MessageContent";

// 代码审核 / 权限审批弹窗：展示待执行的文件 diff 或命令，等用户批准/拒绝。
// 复用 MessageContent 渲染 diff 卡片。
export function ApprovalModal(props: {
  request: ApprovalRequest;
  busy: boolean;
  onDecide: (approved: boolean, scope: "once" | "session") => void;
}) {
  let rejectButton: HTMLButtonElement | undefined;
  createEffect(() => {
    props.request.id;
    queueMicrotask(() => rejectButton?.focus());
  });
  return (
    <Dialog open onOpenChange={(open) => { if (!open && !props.busy) props.onDecide(false, "once"); }}>
      <Dialog.Portal>
        <Dialog.Overlay class="ui-modal-backdrop" />
        <div class="ui-modal-positioner">
      <Dialog.Content class="approval-card" onOpenAutoFocus={(event) => { event.preventDefault(); rejectButton?.focus(); }} onInteractOutside={(event) => event.preventDefault()} onEscapeKeyDown={(event) => { if (props.busy) event.preventDefault(); }}>
        <div class="approval-head">
          <ShieldAlert size={18} />
          <div class="approval-headings">
            <Dialog.Title>{approvalTitle(props.request.kind)}</Dialog.Title>
            <Dialog.Description>{props.request.summary}</Dialog.Description>
          </div>
        </div>

        <div class="approval-body">
          <Show when={props.request.path}>
            <pre class="approval-command" aria-label="请求访问的文件">{props.request.path}</pre>
          </Show>
          <Show when={props.request.kind === "command"}>
            <pre class="approval-command">{props.request.command}</pre>
          </Show>
          <Show when={props.request.kind === "file" && props.request.command && !props.request.parts?.length}>
            <pre class="approval-command" aria-label="请求执行的文件操作">{props.request.command}</pre>
          </Show>
          <Show when={props.request.kind === "browser"}>
            <pre class="approval-command">{props.request.url}</pre>
          </Show>
          <Show when={props.request.parts && props.request.parts.length > 0}>
            <MessageContent parts={props.request.parts!} inferFileArtifacts={false} />
          </Show>
        </div>

        <div class="approval-actions">
          <button ref={rejectButton} class="approval-reject" type="button" disabled={props.busy} onClick={() => props.onDecide(false, "once")}>
            <X size={14} />
            {props.request.kind === "plan" ? "不执行" : "拒绝"}
          </button>
          <Show when={props.request.kind !== "plan"}>
            <button class="approval-allow-session" type="button" disabled={props.busy} onClick={() => props.onDecide(true, "session")}>
              本会话允许此操作
            </button>
          </Show>
          <button class="approval-approve" type="button" disabled={props.busy} onClick={() => props.onDecide(true, "once")}>
            <Check size={14} />
            {props.request.kind === "plan" ? "执行此计划" : "批准"}
          </button>
        </div>
      </Dialog.Content>
        </div>
      </Dialog.Portal>
    </Dialog>
  );
}

function approvalTitle(kind: string): string {
  switch (kind) {
    case "command":
      return "命令执行请求";
    case "plan":
      return "执行计划审阅";
    case "browser":
      return "网站访问请求";
    default:
      return "文件修改请求";
  }
}
