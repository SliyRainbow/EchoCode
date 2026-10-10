import { RadioGroup } from "@kobalte/core/radio-group";
import { Check, Moon, Sun } from "lucide-solid";
import { For, Show } from "solid-js";
import type { ThemeMode } from "../ui-types";

export function ThemePicker(props: { value: ThemeMode; onChange: (value: ThemeMode) => void }) {
  return (
    <RadioGroup class="settings-theme-picker" value={props.value} onChange={(value) => props.onChange(value as ThemeMode)} aria-label="界面主题" orientation="horizontal">
      <For each={(["dark", "light"] as const)}>{(theme) => (
        <RadioGroup.Item class="settings-theme-option" value={theme}>
          <RadioGroup.ItemInput />
          <RadioGroup.ItemControl>
            <div class={`theme-thumbnail theme-thumbnail-${theme}`} aria-hidden="true">
              <div class="theme-thumbnail-nav"><i /><i /><i /></div>
              <div class="theme-thumbnail-content"><i /><i /><div /></div>
            </div>
            <div class="theme-option-label">
              <Show when={theme === "dark"} fallback={<Sun size={14} />}><Moon size={14} /></Show>
              <RadioGroup.ItemLabel>{theme === "dark" ? "深色" : "浅色"}</RadioGroup.ItemLabel>
              <RadioGroup.ItemIndicator class="theme-choice-check"><Check size={14} /></RadioGroup.ItemIndicator>
            </div>
          </RadioGroup.ItemControl>
        </RadioGroup.Item>
      )}</For>
    </RadioGroup>
  );
}
