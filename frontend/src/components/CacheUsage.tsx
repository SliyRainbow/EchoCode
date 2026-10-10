import { Popover } from "@kobalte/core/popover";
import { Database } from "lucide-solid";
import { createEffect, createSignal, onCleanup } from "solid-js";
import type { DeepSeekSessionState, UsageMetrics } from "../types";

const count = (value: number) => Math.max(0, Number.isFinite(value) ? value : 0);
const tokens = (value: number) => count(value).toLocaleString();
const rate = (hit: number, miss: number) => hit + miss > 0 ? `${Math.round(hit / (hit + miss) * 100)}%` : "—";

export function CacheUsage(props: {
  sessionKey: string;
  session: DeepSeekSessionState;
  latest: UsageMetrics;
  onUsage: () => void;
  onOpenChange: (open: boolean) => void;
}) {
  const [open, setOpen] = createSignal(false);
  const changeOpen = (value: boolean) => { setOpen(value); props.onOpenChange(value); };
  createEffect(() => { props.sessionKey; changeOpen(false); });
  onCleanup(() => props.onOpenChange(false));
  const hit = () => count(props.session.sessionCacheHitTokens);
  const miss = () => count(props.session.sessionCacheMissTokens);
  const latestHit = () => count(props.latest.promptCacheHitTokens);
  const latestMiss = () => count(props.latest.promptCacheMissTokens);
  return (
    <Popover placement="bottom-end" gutter={8} open={open()} onOpenChange={changeOpen}>
      <Popover.Trigger class="header-context context-trigger cache-trigger" aria-label="缓存命中率" title="当前会话缓存命中率">
        <Database size={13} aria-hidden="true" /><span>{rate(hit(), miss())}</span>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content class="context-popover cache-popover">
          <Popover.Title>缓存命中率</Popover.Title>
          <p class="context-estimate"><strong>{rate(hit(), miss())}</strong> <span>当前会话</span></p>
          <div class="context-meter" role="progressbar" aria-label="当前会话缓存命中率" aria-valuemin={0} aria-valuemax={100} aria-valuenow={hit() + miss() > 0 ? hit() / (hit() + miss()) * 100 : 0} aria-valuetext={hit() + miss() > 0 ? rate(hit(), miss()) : "暂无缓存数据"}><span style={{ width: hit() + miss() > 0 ? rate(hit(), miss()) : "0%" }} /></div>
          <Popover.Description>{hit() + miss() > 0 ? "按当前会话累计的输入缓存数据计算，与上下文占用分别统计。" : "暂无缓存数据，收到输入缓存计数后会自动更新。未统计的数据用破折号表示。"}</Popover.Description>
          <dl>
            <div><dt>命中输入</dt><dd>{tokens(hit())} tokens</dd></div>
            <div><dt>未命中输入</dt><dd>{tokens(miss())} tokens</dd></div>
            <div><dt>最近一次请求</dt><dd>{rate(latestHit(), latestMiss())}</dd></div>
          </dl>
          <p class="context-help">命中率 = 命中输入 ÷（命中输入 + 未命中输入）。数据由模型服务返回，缓存是否命中由上游决定。</p>
          <div class="context-actions"><button type="button" onClick={() => { changeOpen(false); props.onUsage(); }}>查看使用统计</button></div>
        </Popover.Content>
      </Popover.Portal>
    </Popover>
  );
}
