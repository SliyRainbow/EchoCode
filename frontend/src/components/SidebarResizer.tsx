import type { JSX } from "solid-js";
import { defaultSidebarWidth, maxSidebarWidth, minSidebarWidth } from "../constants";

export type SidebarResizeControl = {
  value: () => number;
  onChange: (width: number) => void;
  onPointerDown: JSX.EventHandler<HTMLDivElement, PointerEvent>;
  onPointerMove: JSX.EventHandler<HTMLDivElement, PointerEvent>;
  onPointerUp: JSX.EventHandler<HTMLDivElement, PointerEvent>;
  onMouseDown: JSX.EventHandler<HTMLDivElement, MouseEvent>;
};

export function SidebarResizer(props: { control: SidebarResizeControl; settings?: boolean; hidden?: boolean }) {
  return (
    <div
      class="sidebar-resizer"
      classList={{ "settings-sidebar-resizer": props.settings }}
      inert={props.hidden}
      aria-hidden={props.hidden}
      role="separator"
      aria-label="调整侧栏宽度"
      aria-orientation="vertical"
      aria-valuemin={minSidebarWidth}
      aria-valuemax={maxSidebarWidth}
      aria-valuenow={Math.round(props.control.value())}
      tabIndex={props.hidden ? -1 : 0}
      title="拖拽调整宽度，双击恢复默认"
      onPointerDown={props.control.onPointerDown}
      onPointerMove={props.control.onPointerMove}
      onPointerUp={props.control.onPointerUp}
      onPointerCancel={props.control.onPointerUp}
      onMouseDown={props.control.onMouseDown}
      onDblClick={() => props.control.onChange(defaultSidebarWidth)}
      onKeyDown={event => {
        const widths: Record<string, number> = { ArrowLeft: props.control.value() - 16, ArrowRight: props.control.value() + 16, Home: minSidebarWidth, End: maxSidebarWidth, Enter: defaultSidebarWidth };
        if (Number.isFinite(widths[event.key])) { event.preventDefault(); props.control.onChange(widths[event.key]); }
      }}
    />
  );
}
