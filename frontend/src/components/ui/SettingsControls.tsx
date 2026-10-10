import { Button } from "@kobalte/core/button";
import { RadioGroup } from "@kobalte/core/radio-group";
import { Select } from "@kobalte/core/select";
import { Slider } from "@kobalte/core/slider";
import { Switch } from "@kobalte/core/switch";
import { Check, ChevronDown, LoaderCircle } from "lucide-solid";
import { For, Show, createContext, createMemo, untrack, useContext } from "solid-js";
import type { JSX } from "solid-js";
import { TooltipButton } from "./TooltipButton";

export const SettingsControlLabel = createContext<string>();

export function SettingsButton(props: {
  children: JSX.Element;
  icon?: JSX.Element;
  variant?: "primary" | "secondary" | "danger" | "ghost";
  disabled?: boolean;
  loading?: boolean;
  onClick: () => void;
}) {
  return (
    <Button
      class="settings-button"
      data-variant={props.variant ?? "secondary"}
      disabled={props.disabled || props.loading}
      aria-busy={props.loading}
      onClick={props.onClick}
    >
      <Show when={props.loading} fallback={props.icon}>
        <LoaderCircle size={15} class="spinning" aria-hidden="true" />
      </Show>
      <span>{props.children}</span>
    </Button>
  );
}

export function SettingsIconButton(props: {
  children: JSX.Element;
  danger?: boolean;
  disabled?: boolean;
  onClick: () => void;
  title: string;
}) {
  return (
    <TooltipButton class={`settings-icon-action ${props.danger ? "danger" : ""}`} label={props.title} placement="top" disabled={props.disabled} onClick={props.onClick}>
      {props.children}
    </TooltipButton>
  );
}

type SelectOption = { value: string; label: string };
type KeyedSelectOption = SelectOption & { key: string };

export function SettingsSelect(props: {
  ariaLabel?: string;
  value: string;
  options: SelectOption[];
  disabled?: boolean;
  onChange: (value: string) => void;
}) {
  const rowLabel = useContext(SettingsControlLabel);
  // Empty values mean "follow the current route" in several settings. Give
  // every item a nonempty key so Kobalte can select that option as well.
  const options = createMemo(() => props.options.map(option => ({ ...option, key: `value:${option.value}` })));
  return (
    <Select<KeyedSelectOption>
      class="settings-select-root"
      options={options()}
      optionValue="key"
      optionTextValue="label"
      value={options().find((option) => option.value === props.value) ?? null}
      onChange={(option) => untrack(() => {
        // Kobalte also reconciles selection when option objects are replaced.
        // Only a changed value is an edit; rebroadcasting the same value can
        // recursively rebuild controlled provider settings.
        if (option && option.value !== props.value) props.onChange(option.value);
      })}
      disabled={props.disabled}
      placeholder={props.value || "请选择"}
      placement="bottom-end"
      gutter={6}
      sameWidth
      itemComponent={(item) => (
        <Select.Item item={item.item} class="settings-select-option">
          <Select.ItemLabel>{item.item.rawValue.label}</Select.ItemLabel>
          <Select.ItemIndicator><Check size={14} /></Select.ItemIndicator>
        </Select.Item>
      )}
    >
      <Select.Trigger class="settings-select settings-select-trigger" aria-label={props.ariaLabel || rowLabel || "选择设置"}>
        <Select.Value<KeyedSelectOption>>{(state) => state.selectedOption()?.label}</Select.Value>
        <Select.Icon><ChevronDown size={13} /></Select.Icon>
      </Select.Trigger>
      <Select.Portal>
        <Select.Content class="settings-select-popover"><Select.Listbox class="settings-select-list" /></Select.Content>
      </Select.Portal>
    </Select>
  );
}

export function SettingsSlider(props: {
  label: string;
  value: number;
  min: number;
  max: number;
  step?: number;
  disabled?: boolean;
  format?: (value: number) => string;
  onChange: (value: number) => void;
}) {
  const format = (value: number) => props.format?.(value) ?? String(value);
  return (
    <Slider class="settings-slider" value={[props.value]} minValue={props.min} maxValue={props.max} step={props.step ?? 1} disabled={props.disabled} onChange={(values) => props.onChange(values[0])} getValueLabel={({ values }) => format(values[0])}>
      <Slider.Label class="sr-only">{props.label}</Slider.Label>
      <div class="settings-slider-control">
        <Slider.Track class="settings-slider-track">
          <Slider.Fill class="settings-slider-fill" />
          <Slider.Thumb class="settings-slider-thumb" aria-disabled={props.disabled} aria-valuetext={format(props.value)}><Slider.Input tabIndex={-1} aria-hidden="true" /></Slider.Thumb>
        </Slider.Track>
      </div>
      <Slider.ValueLabel class="settings-slider-value" />
    </Slider>
  );
}

export function SettingsSwitch(props: { checked: boolean; label?: string; onChange: (checked: boolean) => void }) {
  const rowLabel = useContext(SettingsControlLabel);
  return (
    <Switch class="settings-switch" checked={props.checked} onChange={props.onChange}>
      <Switch.Input aria-label={props.label || rowLabel || "切换设置"} />
      <Switch.Control class="ui-switch-track"><Switch.Thumb class="ui-switch-thumb" /></Switch.Control>
    </Switch>
  );
}

export function SettingsSegmentedControl(props: {
  ariaLabel?: string;
  value: string;
  options: Array<SelectOption & { description?: string }>;
  onChange: (value: string) => void;
}) {
  const rowLabel = useContext(SettingsControlLabel);
  return (
    <RadioGroup class="segmented-control" value={props.value} onChange={props.onChange} aria-label={props.ariaLabel || rowLabel || "选择选项"} orientation="horizontal">
      <For each={props.options}>{(option) => (
        <RadioGroup.Item class="settings-segment" value={option.value} title={option.description}>
          <RadioGroup.ItemInput aria-label={option.label} />
          <RadioGroup.ItemControl><RadioGroup.ItemLabel>{option.label}</RadioGroup.ItemLabel></RadioGroup.ItemControl>
        </RadioGroup.Item>
      )}</For>
    </RadioGroup>
  );
}
