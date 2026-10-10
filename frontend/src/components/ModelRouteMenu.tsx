import { DropdownMenu } from "@kobalte/core/dropdown-menu";
import { Check, ChevronDown, SlidersHorizontal } from "lucide-solid";
import { For, createMemo, onCleanup } from "solid-js";
import { modelOptionsForProvider, selectedModelName, selectedModelProvider, shortModelName } from "../format";
import type { RuntimeSettings } from "../types";

export function ModelRouteMenu(props: {
  onManage: () => void;
  onSelect: (providerID: string, modelID: string) => void;
  saving: boolean;
  settings: RuntimeSettings;
  onOpenChange?: (open: boolean) => void;
}) {
  onCleanup(() => props.onOpenChange?.(false));
  const selectedProvider = createMemo(() => selectedModelProvider(props.settings));
  const selectedModel = createMemo(() => selectedModelName(props.settings));
  const providers = createMemo(() => {
    const available = props.settings.model.providers.filter((provider) => modelOptionsForProvider(provider).length > 0);
    const enabled = available.filter((provider) => provider.enabled);
    return enabled.length ? enabled : available;
  });
  const label = createMemo(() => {
    const provider = selectedProvider();
    if (!provider) return "选择模型";
    return modelOptionsForProvider(provider).find((model) => model.id === selectedModel())?.displayName || shortModelName(selectedModel()) || provider.name;
  });
  return (
    <DropdownMenu placement="top-end" gutter={10} onOpenChange={props.onOpenChange}>
      <DropdownMenu.Trigger class="model-route-trigger" aria-label="选择模型" title={label()} disabled={props.saving}>
        <span class="model-route-current">{label()}</span><ChevronDown size={12} />
      </DropdownMenu.Trigger>
      <DropdownMenu.Portal>
        <DropdownMenu.Content class="ui-menu model-picker" aria-label="选择模型">
          <div class="ui-menu-label">选择模型</div>
          <For each={providers()} fallback={<p class="ui-menu-help">暂时没有可用模型，请先配置供应商。</p>}>
            {(provider) => (
              <DropdownMenu.Group class="model-picker-group">
                <DropdownMenu.GroupLabel class="ui-menu-label">{provider.name}</DropdownMenu.GroupLabel>
                <DropdownMenu.RadioGroup value={selectedProvider()?.id === provider.id ? selectedModel() : ""} onChange={(modelID) => props.onSelect(provider.id, modelID)}>
                  <For each={modelOptionsForProvider(provider)}>
                    {(model) => (
                      <DropdownMenu.RadioItem class="ui-menu-item" value={model.id} title={model.displayName || model.id} closeOnSelect>
                        <span class="ui-menu-text">{model.displayName || model.id}</span><DropdownMenu.ItemIndicator class="ui-menu-indicator"><Check size={14} aria-hidden="true" /></DropdownMenu.ItemIndicator>
                      </DropdownMenu.RadioItem>
                    )}
                  </For>
                </DropdownMenu.RadioGroup>
              </DropdownMenu.Group>
            )}
          </For>
          <DropdownMenu.Separator class="ui-menu-separator" />
          <DropdownMenu.Item class="ui-menu-item" onSelect={props.onManage}><SlidersHorizontal size={15} /><span class="ui-menu-text">管理模型</span></DropdownMenu.Item>
        </DropdownMenu.Content>
      </DropdownMenu.Portal>
    </DropdownMenu>
  );
}
