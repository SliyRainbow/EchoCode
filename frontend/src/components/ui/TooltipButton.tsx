import { Tooltip } from "@kobalte/core/tooltip";
import type { JSX } from "solid-js";

export function TooltipButton(props: {
  label: string;
  children: JSX.Element;
  active?: boolean;
  disabled?: boolean;
  class?: string;
  placement?: "right" | "bottom" | "top";
  onClick: () => void;
}) {
  return (
    <Tooltip placement={props.placement ?? "right"} openDelay={500} gutter={10}>
      <Tooltip.Trigger
        class={`ui-icon-button ${props.class ?? ""}`}
        classList={{ active: props.active }}
        aria-label={props.label}
        aria-pressed={props.active}
        disabled={props.disabled}
        onClick={props.onClick}
      >
        {props.children}
      </Tooltip.Trigger>
      <Tooltip.Portal>
        <Tooltip.Content class="ui-tooltip">{props.label}</Tooltip.Content>
      </Tooltip.Portal>
    </Tooltip>
  );
}
