import { AlertCircle, Bot, Check, CheckCircle2, ChevronRight, Clock3, Copy, Square, Wrench, X } from "lucide-solid";
import { For, Show, createMemo, createSignal } from "solid-js";
import type { SubagentPart } from "./chat/MessageContent";
import { formatElapsedDuration } from "../lib/duration";
import { writeClipboardText } from "../lib/clipboard";

type SubagentPanelProps = {
  part: SubagentPart;
  parentTaskID?: string;
  stopping?: boolean;
  onStop: (part: SubagentPart) => void | Promise<void>;
  onClose: () => void;
};

export function SubagentPanel(props: SubagentPanelProps) {
  const [copied, setCopied] = createSignal(false);
  const running = createMemo(() => props.part.status === "pending" || props.part.status === "running");
  const output = createMemo(() => props.part.subagentOutput || props.part.summary || "");
  const copyOutput = async () => {
    if (!output()) return;
    await writeClipboardText(output());
    setCopied(true);
    window.setTimeout(() => setCopied(false), 1200);
  };

  return (
    <section class="subagent-panel" aria-label={`子代理：${props.part.label || "子任务"}`}>
      <header class="subagent-panel-head">
        <span class="subagent-panel-icon" aria-hidden="true"><Bot size={17} /></span>
        <span class="subagent-panel-title">
          <strong>{props.part.label || "子任务"}</strong>
          <small>{subagentTypeLabel(props.part.agentType)} · {props.part.model || "当前模型"}</small>
        </span>
        <span class="subagent-panel-actions">
          <Show when={running()}>
            <button
              type="button"
              class="subagent-stop"
              title="停止此子代理"
              aria-label="停止此子代理"
              disabled={props.stopping || !props.parentTaskID}
              onClick={() => void props.onStop(props.part)}
            >
              <Square size={12} fill="currentColor" />
            </button>
          </Show>
          <button type="button" title="关闭子代理窗口" aria-label="关闭子代理窗口" onClick={props.onClose}>
            <X size={15} />
          </button>
        </span>
      </header>

      <div class="subagent-panel-state" classList={{ [props.part.status || "pending"]: true }}>
        <span class="subagent-panel-state-icon" aria-hidden="true"><SubagentPanelStatus status={props.part.status} /></span>
        <span>
          <strong>{subagentStatusLabel(props.part.status)}</strong>
          <small>{props.part.currentAction || props.part.summary || "等待运行状态"}</small>
        </span>
      </div>

      <div class="subagent-panel-meta">
        <span><Clock3 size={12} />{props.part.durationMs !== undefined ? formatElapsedDuration(props.part.durationMs) : "运行中"}</span>
        <Show when={props.part.changedFiles}><span>{props.part.changedFiles} 个文件</span></Show>
        <Show when={(props.part.additions ?? 0) + (props.part.deletions ?? 0) > 0}>
          <span class="subagent-change-stats"><b>+{props.part.additions ?? 0}</b><i>-{props.part.deletions ?? 0}</i></span>
        </Show>
      </div>

      <div class="subagent-panel-scroll">
        <section class="subagent-output-section">
          <header>
            <strong>输出</strong>
            <button type="button" title="复制子代理输出" aria-label="复制子代理输出" disabled={!output()} onClick={() => void copyOutput()}>
              <Show when={copied()} fallback={<Copy size={13} />}><Check size={13} /></Show>
            </button>
          </header>
          <Show when={output()} fallback={<p class="subagent-output-empty">正在等待输出</p>}>
            <pre class="subagent-output-text">{output()}</pre>
          </Show>
        </section>

        <Show when={(props.part.activities?.length ?? 0) > 0}>
          <section class="subagent-activity-section">
            <header><strong>工具活动</strong><small>{props.part.activities?.length}</small></header>
            <div class="subagent-activity-list">
              <For each={props.part.activities ?? []}>
                {(activity) => (
                  <details class="subagent-activity" classList={{ [activity.status || "completed"]: true }}>
                    <summary>
                      <span class="subagent-activity-icon" aria-hidden="true"><Wrench size={12} /></span>
                      <span>
                        <strong>{activity.title || "工具调用"}</strong>
                        <small>{activity.durationMs !== undefined ? formatElapsedDuration(activity.durationMs) : subagentStatusLabel(activity.status)}</small>
                      </span>
                      <ChevronRight size={12} />
                    </summary>
                    <Show when={activity.input}><pre><b>输入</b>{"\n"}{activity.input}</pre></Show>
                    <Show when={activity.output}><pre><b>输出</b>{"\n"}{activity.output}</pre></Show>
                  </details>
                )}
              </For>
            </div>
          </section>
        </Show>
      </div>
    </section>
  );
}

function SubagentPanelStatus(props: { status?: string }) {
  return (
    <Show when={props.status === "completed"} fallback={
      <Show when={props.status === "error" || props.status === "cancelled"} fallback={<span class="subagent-panel-spinner" />}>
        <AlertCircle size={14} />
      </Show>
    }>
      <CheckCircle2 size={14} />
    </Show>
  );
}

function subagentStatusLabel(status?: string): string {
  switch (status) {
    case "pending": return "等待启动";
    case "running": return "正在运行";
    case "completed":
    case "ok": return "已完成";
    case "cancelled": return "已停止";
    case "error": return "执行失败";
    default: return status || "等待状态";
  }
}

function subagentTypeLabel(agentType: string): string {
  switch (agentType) {
    case "explore": return "探索";
    case "review": return "审阅";
    case "implement": return "实现";
    default: return agentType || "任务";
  }
}
