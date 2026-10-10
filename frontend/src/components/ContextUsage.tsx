import { Popover } from "@kobalte/core/popover";
import { Gauge, Minimize2 } from "lucide-solid";
import { createEffect, createSignal, onCleanup, Show } from "solid-js";
import type { DeepSeekSessionState } from "../types";
import { errorMessage } from "../lib/errors";

const tokens = (value?: number) => Math.max(0, Math.round(value ?? 0)).toLocaleString();
const sources: Record<string, string> = { manual: "手动设置", upstream: "上游元数据", catalog: "模型目录", "provider-default": "供应商默认", "protocol-default": "协议默认", "safe-default": "安全估算" };

export function ContextUsage(props: {
  sessionKey: string;
  state: DeepSeekSessionState;
  disabled: boolean;
  onCompress: () => Promise<string>;
  onUsage: () => void;
  onOpenChange?: (open: boolean) => void;
}) {
  const [open, setOpen] = createSignal(false);
  const [pending, setPending] = createSignal(false);
  const [notice, setNotice] = createSignal("");
  const [failed, setFailed] = createSignal(false);
  const percent = () => Math.max(0, props.state.contextUsagePercent ?? 0);
  const available = () => (props.state.inputBudgetTokens ?? 0) > 0;
  const changeOpen = (value: boolean) => { setOpen(value); props.onOpenChange?.(value); };
  createEffect(() => { props.sessionKey; setNotice(""); setFailed(false); setPending(false); changeOpen(false); });
  onCleanup(() => props.onOpenChange?.(false));
  const compress = async () => {
    if (pending() || props.disabled) return;
    const key = props.sessionKey;
    setPending(true); setNotice(""); setFailed(false);
    try {
      const message = await props.onCompress();
      if (key === props.sessionKey) setNotice(message);
    } catch (error) {
      if (key === props.sessionKey) { setFailed(true); setNotice(errorMessage(error)); }
    } finally {
      if (key === props.sessionKey) setPending(false);
    }
  };
  return (
    <Popover placement="bottom-end" gutter={8} open={open()} onOpenChange={changeOpen}>
      <Popover.Trigger class="header-context context-trigger" aria-label="上下文用量" title="查看上下文用量与压缩" data-warning={percent() >= 85 || undefined}>
        <Gauge size={14} aria-hidden="true" /><span>{available() ? `${Math.round(percent())}%` : "上下文"}</span>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content class="context-popover">
          <Popover.Title>上下文用量</Popover.Title>
          <p class="context-estimate"><strong>{tokens(props.state.estimatedInputTokens)}</strong> / {tokens(props.state.inputBudgetTokens)} tokens</p>
          <div class="context-meter" role="progressbar" aria-label="可用输入预算占用" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.min(100, Math.round(percent()))} aria-valuetext={`约 ${Math.round(percent())}%`}><span style={{ width: `${Math.min(100, percent())}%` }} /></div>
          <Popover.Description>{props.state.contextUsageSource === "provider" ? "本轮最近一次模型请求报告的输入用量，包含工具结果。" : "当前会话输入的估算值，不等同于账单用量。"}已为模型输出、工具和安全余量预留空间。</Popover.Description>
          <dl>
            <div><dt>模型上下文窗口</dt><dd>{available() ? `${tokens(props.state.contextWindowTokens)} tokens` : "尚未选择模型"}</dd></div>
            <div><dt>窗口来源</dt><dd>{sources[props.state.contextWindowSource ?? ""] ?? props.state.contextWindowSource ?? "待确认"}</dd></div>
            <div><dt>自动压缩阈值</dt><dd>{tokens(props.state.compressionTriggerTokens)} tokens</dd></div>
            <div><dt>已压缩</dt><dd>{props.state.compressionCount ?? 0} 次</dd></div>
          </dl>
          <p class="context-help">接近阈值时自动整理；也可提前压缩。较早内容会提取为摘要，聊天记录和文件检查点继续保留。</p>
          <Show when={props.disabled}><p class="context-help">任务运行或会话切换期间不可手动压缩。</p></Show>
          <Show when={notice()}><p class="context-result" classList={{ error: failed() }} role={failed() ? "alert" : "status"}>{notice()}</p></Show>
          <div class="context-actions">
            <button type="button" class="settings-secondary-button" onClick={() => { changeOpen(false); props.onUsage(); }}>使用统计</button>
            <button type="button" class="settings-secondary-button" disabled={props.disabled || pending() || !available() || props.state.messageCount < 3} onClick={() => void compress()}><Minimize2 size={14} />{pending() ? "正在压缩…" : "压缩上下文"}</button>
          </div>
        </Popover.Content>
      </Popover.Portal>
    </Popover>
  );
}
