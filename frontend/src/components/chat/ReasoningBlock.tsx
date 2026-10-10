import { Collapsible } from "@kobalte/core/collapsible";
import { Brain, ChevronDown } from "lucide-solid";
import { Show, createEffect, createSignal } from "solid-js";

export function ReasoningBlock(props: {
  reasoning: string;
  active?: boolean;
  open?: boolean;
  onOpenChange?: (open: boolean) => void;
}) {
  const [localOpen, setLocalOpen] = createSignal(Boolean(props.active));
  const [touched, setTouched] = createSignal(false);
  const open = () => props.open ?? localOpen();
  const changeOpen = (next: boolean) => {
    setTouched(true);
    setLocalOpen(next);
    props.onOpenChange?.(next);
  };

  // Open the first live trace, then preserve the reader's choice through text,
  // tools, completion and history reconciliation. Never collapse on a delta.
  createEffect(() => {
    if (props.active && props.open === undefined && !touched()) {
      setLocalOpen(true);
      props.onOpenChange?.(true);
    }
  });

  return (
    <Collapsible class="op-reasoning" classList={{ active: Boolean(props.active) }} open={open()} onOpenChange={changeOpen}>
      <Collapsible.Trigger class="op-reasoning-trigger" aria-label="思考过程" title="查看模型返回的思考过程">
        <Brain size={14} class="op-reasoning-icon" aria-hidden="true" />
        <span class="op-reasoning-label">思考过程</span>
        <span class="op-reasoning-state">
          <Show when={props.active}><span class="op-activity-spinner" aria-hidden="true" /></Show>
          {props.active ? "正在思考" : "已记录"}
        </span>
        <ChevronDown class="op-reasoning-chevron" size={14} aria-hidden="true" />
      </Collapsible.Trigger>
      <Collapsible.Content class="op-reasoning-content">
        <div class="op-reasoning-body" tabIndex={0} role="region" aria-label="模型思考内容">{props.reasoning}</div>
      </Collapsible.Content>
    </Collapsible>
  );
}
