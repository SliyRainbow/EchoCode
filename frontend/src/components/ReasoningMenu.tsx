import { DropdownMenu } from "@kobalte/core/dropdown-menu";
import { Check, ChevronDown } from "lucide-solid";
import { For, Show, onCleanup } from "solid-js";
import type { ReasoningLevel, ReasoningOption } from "../types";

export function ReasoningMenu(props: {
  value: ReasoningLevel;
  options: ReasoningOption[];
  running?: boolean;
  onChange: (level: ReasoningLevel) => void;
  onOpenChange?: (open: boolean) => void;
}) {
  onCleanup(() => props.onOpenChange?.(false));
  const current = () => props.options.find((option) => option.id === props.value) ?? props.options[0];
  return (
    <div class="reasoning-menu">
      <DropdownMenu placement="top-end" gutter={10} onOpenChange={props.onOpenChange}>
        <DropdownMenu.Trigger class="reasoning-trigger" title="推理强度" aria-label="选择推理强度">
          <span>{current()?.label ?? "推理强度"}</span><ChevronDown size={12} aria-hidden="true" />
        </DropdownMenu.Trigger>
        <DropdownMenu.Portal>
          <DropdownMenu.Content class="ui-menu reasoning-options" aria-label="推理强度">
            <div class="ui-menu-label">推理强度</div>
            <DropdownMenu.RadioGroup value={props.value} onChange={(value) => { if (value !== props.value) props.onChange(value as ReasoningLevel); }}>
              <For each={props.options}>
                {(option) => (
                  <DropdownMenu.RadioItem value={option.id} class="ui-menu-item reasoning-option" closeOnSelect>
                    <div class="ui-menu-copy"><DropdownMenu.ItemLabel>{option.label}</DropdownMenu.ItemLabel><DropdownMenu.ItemDescription class="ui-item-description">{option.description}</DropdownMenu.ItemDescription></div>
                    <DropdownMenu.ItemIndicator class="ui-menu-indicator"><Check size={15} aria-hidden="true" /></DropdownMenu.ItemIndicator>
                  </DropdownMenu.RadioItem>
                )}
              </For>
            </DropdownMenu.RadioGroup>
          </DropdownMenu.Content>
        </DropdownMenu.Portal>
      </DropdownMenu>
      <Show when={props.running}><span class="pending-note">下一轮生效</span></Show>
    </div>
  );
}
