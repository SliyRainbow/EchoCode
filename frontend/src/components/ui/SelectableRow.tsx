import { Button } from "@kobalte/core/button";
import { splitProps, type JSX } from "solid-js";

/** Shared selection surface for file and workspace lists. */
export function SelectableRow(props: JSX.ButtonHTMLAttributes<HTMLButtonElement> & { selected: boolean }) {
  const [local, others] = splitProps(props, ["selected", "class", "children"]);
  return (
    <Button {...others} class={`panel-option ${local.class ?? ""}`} data-selected={local.selected ? "" : undefined}>
      {local.children}
    </Button>
  );
}
