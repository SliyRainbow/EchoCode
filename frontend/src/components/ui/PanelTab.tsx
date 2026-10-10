import { Tabs, useTabsContext, type TabsTriggerOptions } from "@kobalte/core/tabs";
import type { JSX } from "solid-js";

/** Keep the panel relationship stable when its body is mounted on selection. */
export function PanelTab(props: TabsTriggerOptions & Omit<JSX.ButtonHTMLAttributes<HTMLButtonElement>, "type" | "role">) {
  const tabs = useTabsContext();
  return <Tabs.Trigger {...props} aria-controls={tabs.generateContentId(props.value)} />;
}
