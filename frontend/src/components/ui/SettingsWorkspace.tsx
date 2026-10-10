import type { JSX } from "solid-js";

// Shared master/detail layout for settings that select a resource on the left.
// Child sections use sidebar, main and secondary classes; spacing belongs to
// the grid, not to padding that can be reset by generic form styles.
export function SettingsWorkspace(props: { class: string; overview?: boolean; children: JSX.Element }) {
  return (
    <div class={`settings-page-body settings-workspace ${props.class}`} classList={{ "has-overview": props.overview }}>
      {props.children}
    </div>
  );
}
