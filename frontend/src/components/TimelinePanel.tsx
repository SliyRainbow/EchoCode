import { Dialog } from "@kobalte/core/dialog";
import { For, Show, createMemo, createSignal } from "solid-js";
import { Check, GitBranch, History, RefreshCw, RotateCcw, X } from "lucide-solid";
import type { BranchInfo, CheckpointInfo } from "../types";
import { InlineNotice } from "./ui/InlineNotice";

export function TimelinePanel(props: {
  open: boolean;
  mount?: HTMLElement;
  sessionTitle: string;
  available: boolean;
  loading: boolean;
  error: string;
  notice: string;
  checkpoints: CheckpointInfo[];
  branches: BranchInfo[];
  onRewind: (checkpointID: string) => Promise<void> | void;
  onSwitchBranch: (leafID: string) => Promise<void> | void;
  onRefresh: () => Promise<void> | void;
  onClose: () => void;
  onRestoreFocus: () => void;
  busy: boolean;
  taskRunning: boolean;
}) {
  const [pendingID, setPendingID] = createSignal("");
  const blocked = () => props.busy || Boolean(pendingID());
  const actionsDisabled = () => blocked() || props.taskRunning || props.loading || Boolean(props.error);
  const checkpointsNewestFirst = createMemo(() => [...props.checkpoints].reverse());
  const showBranches = createMemo(() => props.branches.length > 1 || props.branches.some(branch => !branch.isCurrent));
  let closeButton: HTMLButtonElement | undefined;

  const runAction = async (kind: "rewind" | "branch", id: string) => {
    if (actionsDisabled()) return;
    setPendingID(kind + ":" + id);
    try {
      if (kind === "rewind") await props.onRewind(id);
      else await props.onSwitchBranch(id);
    } finally {
      setPendingID("");
    }
  };

  return (
    <Dialog open={props.open} modal={false} onOpenChange={(open) => { if (!open && !blocked()) props.onClose(); }}>
      <Dialog.Portal mount={props.mount}>
        <Dialog.Content
          class="timeline-panel"
          aria-label="回退时间线"
          onOpenAutoFocus={(event) => { event.preventDefault(); closeButton?.focus(); }}
          onCloseAutoFocus={(event) => { event.preventDefault(); props.onRestoreFocus(); }}
          onInteractOutside={(event) => event.preventDefault()}
          onEscapeKeyDown={(event) => { if (blocked()) event.preventDefault(); }}
        >
          <header class="timeline-head">
            <div class="timeline-title">
              <History size={17} aria-hidden="true" />
              <div><Dialog.Title>回退时间线</Dialog.Title><span title={props.sessionTitle}>{props.sessionTitle}</span></div>
            </div>
            <div class="timeline-head-actions">
              <button type="button" aria-label="刷新时间线" title="刷新时间线" disabled={!props.available || props.loading || blocked()} onClick={() => void props.onRefresh()}>
                <RefreshCw size={15} classList={{ spinning: props.loading }} />
              </button>
              <Dialog.CloseButton ref={closeButton} aria-label="关闭回退时间线" title="关闭回退时间线" disabled={blocked()}><X size={16} /></Dialog.CloseButton>
            </div>
          </header>
          <Dialog.Description class="timeline-hint">回退会恢复此对话和 EchoCode 记录的文件改动，后续进度保留在对话分支中。</Dialog.Description>
          <div class="timeline-scroll">
            <Show when={!props.available}>
              <div class="timeline-empty"><History size={25} aria-hidden="true" /><strong>请在桌面应用中查看时间线</strong><p>当前是网页预览，尚未连接本地会话与文件记录。</p></div>
            </Show>
            <Show when={props.available}>
              <Show when={props.taskRunning}><InlineNotice tone="info" message="当前会话正在执行任务。完成或停止后可回退与切换分支。" /></Show>
              <Show when={props.error}>
                <InlineNotice message={props.error} />
                <button class="timeline-retry" type="button" disabled={props.loading || blocked()} onClick={() => void props.onRefresh()}>重新加载时间线</button>
              </Show>
              <Show when={props.loading}><p class="timeline-loading" role="status">正在读取时间线…</p></Show>
              <Show when={!props.loading && !props.error}>
                <Show when={props.notice}><InlineNotice tone="info" message={props.notice} /></Show>
                <Show when={props.checkpoints.length > 0} fallback={
                  <div class="timeline-empty"><History size={25} aria-hidden="true" /><strong>此对话还没有检查点</strong><p>完成一轮对话后，可在这里查看和恢复当时的进度。</p></div>
                }>
                  <div class="timeline-section-head"><h3>检查点</h3><span>{props.checkpoints.length} 个</span></div>
                  <ol class="timeline-list">
                    <For each={checkpointsNewestFirst()}>{(checkpoint) => (
                      <li class="timeline-item" classList={{ current: checkpoint.isCurrent }} aria-current={checkpoint.isCurrent ? "step" : undefined}>
                        <span class="timeline-dot" aria-hidden="true"><Show when={checkpoint.isCurrent}><Check size={10} /></Show></span>
                        <div class="timeline-body">
                          <div class="timeline-row"><strong>第 {checkpoint.turnIndex} 轮</strong><time dateTime={checkpoint.timestamp}>{formatTimelineTime(checkpoint.timestamp)}</time></div>
                          <p class="timeline-label">{checkpoint.label || "无摘要"}</p>
                          <Show when={!checkpoint.isCurrent} fallback={<span class="timeline-current">当前位置</span>}>
                            <button class="timeline-rewind" type="button" disabled={actionsDisabled()} aria-label={`回退到第 ${checkpoint.turnIndex} 轮`} onClick={() => void runAction("rewind", checkpoint.id)}>
                              <RotateCcw size={13} classList={{ spinning: pendingID() === "rewind:" + checkpoint.id }} />回到此处
                            </button>
                          </Show>
                        </div>
                      </li>
                    )}</For>
                  </ol>
                </Show>
                <Show when={showBranches()}>
                  <section class="timeline-branches" aria-label="对话分支">
                    <div class="timeline-section-head"><h3><GitBranch size={14} />对话分支</h3><span>{props.branches.length} 条</span></div>
                    <p class="timeline-branch-hint">可切换到保留的后续进度。这些是对话记录，不是 Git 分支。</p>
                    <For each={props.branches}>{(branch) => (
                      <button class="timeline-branch" classList={{ current: branch.isCurrent }} type="button" disabled={actionsDisabled() || branch.isCurrent} aria-label={`切换到分支 ${branch.label || "未命名分支"}`} onClick={() => void runAction("branch", branch.leafId)}>
                        <GitBranch size={15} classList={{ spinning: pendingID() === "branch:" + branch.leafId }} />
                        <span><strong>{branch.label || "未命名分支"}</strong><small>{branch.turnCount} 轮 · {formatTimelineTime(branch.timestamp)}</small></span>
                        <Show when={branch.isCurrent}><small>当前</small></Show>
                      </button>
                    )}</For>
                  </section>
                </Show>
              </Show>
            </Show>
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog>
  );
}

function formatTimelineTime(iso: string): string {
  const date = new Date(iso);
  return Number.isNaN(date.getTime()) ? "" : date.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
