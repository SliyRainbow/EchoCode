import { For, Show, createSignal } from "solid-js";
import { GitBranch, History, RotateCcw, X } from "lucide-solid";
import type { BranchInfo, CheckpointInfo } from "../types";

// Rewind 时间线面板：上半部按检查点回退，下半部列出并切换分支（对话线）。
export function TimelinePanel(props: {
  checkpoints: CheckpointInfo[];
  branches: BranchInfo[];
  onRewind: (checkpointID: string) => Promise<void> | void;
  onSwitchBranch: (leafID: string) => Promise<void> | void;
  onClose: () => void;
  busy: boolean;
}) {
  const [pendingID, setPendingID] = createSignal("");

  const handleRewind = async (id: string) => {
    setPendingID(id);
    try {
      await props.onRewind(id);
    } finally {
      setPendingID("");
    }
  };

  const handleSwitch = async (leafID: string) => {
    setPendingID(leafID);
    try {
      await props.onSwitchBranch(leafID);
    } finally {
      setPendingID("");
    }
  };

  return (
    <aside class="timeline-panel" aria-label="回退时间线">
      <div class="timeline-head">
        <div class="timeline-title">
          <History size={16} />
          <strong>回退时间线</strong>
        </div>
        <button class="ghost-icon" type="button" title="关闭" onClick={props.onClose}>
          <X size={16} />
        </button>
      </div>

      <p class="timeline-hint">回退会同时还原对话与被修改的文件。回退后继续对话将从该点分叉。</p>

      <div class="timeline-list">
        <Show
          when={props.checkpoints.length > 0}
          fallback={<div class="timeline-empty">暂无可回退的检查点。完成一轮对话后会出现在这里。</div>}
        >
          <For each={props.checkpoints}>
            {(cp) => (
              <div class="timeline-item">
                <div class="timeline-dot" aria-hidden="true" />
                <div class="timeline-body">
                  <div class="timeline-row">
                    <span class="timeline-turn">第 {cp.turnIndex} 轮</span>
                    <span class="timeline-time">{formatTimelineTime(cp.timestamp)}</span>
                  </div>
                  <div class="timeline-label" title={cp.label}>
                    {cp.label || "（无摘要）"}
                  </div>
                  <button
                    class="timeline-rewind"
                    type="button"
                    disabled={props.busy}
                    onClick={() => void handleRewind(cp.id)}
                  >
                    <RotateCcw size={13} classList={{ spinning: pendingID() === cp.id }} />
                    回到此处
                  </button>
                </div>
              </div>
            )}
          </For>
        </Show>
      </div>

      <Show when={props.branches.length > 1}>
        <div class="timeline-branches">
          <div class="timeline-branches-head">
            <GitBranch size={14} />
            <strong>分支（{props.branches.length}）</strong>
          </div>
          <For each={props.branches}>
            {(branch) => (
              <button
                class="branch-item"
                classList={{ current: branch.isCurrent }}
                type="button"
                disabled={props.busy || branch.isCurrent}
                onClick={() => void handleSwitch(branch.leafId)}
                title={branch.label}
              >
                <GitBranch size={13} classList={{ spinning: pendingID() === branch.leafId }} />
                <span class="branch-label">{branch.label || "（空分支）"}</span>
                <small>{branch.turnCount} 轮{branch.isCurrent ? " · 当前" : ""}</small>
              </button>
            )}
          </For>
        </div>
      </Show>
    </aside>
  );
}

function formatTimelineTime(iso: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toLocaleString("zh-CN", { month: "2-digit", day: "2-digit", hour: "2-digit", minute: "2-digit" });
}
