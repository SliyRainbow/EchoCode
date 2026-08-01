import { Check, ChevronDown } from "lucide-solid";
import { For, Show, createEffect, createSignal, onCleanup } from "solid-js";
import { Portal } from "solid-js/web";
import type { ReasoningLevel, ReasoningOption } from "../types";

type ReasoningMenuProps = {
  value: ReasoningLevel;
  options: ReasoningOption[];
  running?: boolean;
  onChange: (level: ReasoningLevel) => void;
};

type PopoverPosition = {
  top: number;
  left: number;
  width: number;
  maxHeight: number;
  placement: "top" | "bottom";
  ready: boolean;
};

const popoverWidth = 312;
const viewportPadding = 12;
const popoverGap = 8;

function clampPosition(value: number, minimum: number, maximum: number): number {
  return Math.min(Math.max(value, minimum), Math.max(minimum, maximum));
}

export function ReasoningMenu(props: ReasoningMenuProps) {
  const [open, setOpen] = createSignal(false);
  const [position, setPosition] = createSignal<PopoverPosition>({
    top: 0,
    left: 0,
    width: popoverWidth,
    maxHeight: 260,
    placement: "top",
    ready: false,
  });
  let triggerRef: HTMLButtonElement | undefined;
  let popoverRef: HTMLDivElement | undefined;

  const current = () => props.options.find((option) => option.id === props.value) ?? props.options[0];

  const close = () => setOpen(false);

  const updatePosition = () => {
    if (!open() || !triggerRef) return;

    const trigger = triggerRef.getBoundingClientRect();
    const viewportWidth = window.innerWidth;
    const viewportHeight = window.innerHeight;
    const width = Math.min(popoverWidth, Math.max(200, viewportWidth - viewportPadding * 2));
    const measuredHeight = popoverRef?.offsetHeight || 244;
    const roomAbove = Math.max(0, trigger.top - viewportPadding - popoverGap);
    const roomBelow = Math.max(0, viewportHeight - trigger.bottom - viewportPadding - popoverGap);
    const preferredHeight = Math.min(measuredHeight, 220);
    const placement = roomAbove >= preferredHeight || roomAbove > roomBelow ? "top" : "bottom";
    const availableHeight = placement === "top" ? roomAbove : roomBelow;
    const maxHeight = Math.max(72, availableHeight);
    const visibleHeight = Math.min(measuredHeight, maxHeight);
    const top = placement === "top"
      ? clampPosition(trigger.top - popoverGap - visibleHeight, viewportPadding, viewportHeight - viewportPadding - visibleHeight)
      : clampPosition(trigger.bottom + popoverGap, viewportPadding, viewportHeight - viewportPadding - visibleHeight);
    const left = clampPosition(
      trigger.right - width,
      viewportPadding,
      viewportWidth - viewportPadding - width,
    );

    setPosition({ top, left, width, maxHeight, placement, ready: true });
  };

  const toggle = () => {
    if (open()) {
      close();
      return;
    }
    setPosition((currentPosition) => ({ ...currentPosition, ready: false }));
    setOpen(true);
  };

  createEffect(() => {
    if (!open()) return;

    let frame = window.requestAnimationFrame(updatePosition);
    const handleOutsidePointer = (event: PointerEvent) => {
      const target = event.target as Node | null;
      if (target && (triggerRef?.contains(target) || popoverRef?.contains(target))) return;
      close();
    };
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        event.preventDefault();
        close();
        triggerRef?.focus();
      }
    };
    const reposition = () => {
      window.cancelAnimationFrame(frame);
      frame = window.requestAnimationFrame(updatePosition);
    };

    document.addEventListener("pointerdown", handleOutsidePointer);
    document.addEventListener("keydown", handleKeyDown);
    document.addEventListener("scroll", reposition, true);
    window.addEventListener("resize", reposition);

    onCleanup(() => {
      window.cancelAnimationFrame(frame);
      document.removeEventListener("pointerdown", handleOutsidePointer);
      document.removeEventListener("keydown", handleKeyDown);
      document.removeEventListener("scroll", reposition, true);
      window.removeEventListener("resize", reposition);
    });
  });

  const select = (level: ReasoningLevel) => {
    close();
    if (level !== props.value) {
      props.onChange(level);
    }
  };

  return (
    <div class="reasoning-menu">
      <button
        ref={triggerRef}
        class="reasoning-trigger"
        type="button"
        aria-haspopup="menu"
        aria-expanded={open()}
        title="选择后续请求的推理强度"
        onClick={toggle}
      >
        <span>推理</span>
        <strong>{current().label}</strong>
        <ChevronDown size={16} aria-hidden="true" />
      </button>
      <Show when={open()}>
        <Portal>
          <div
            ref={popoverRef}
            class="reasoning-popover"
            role="menu"
            aria-label="推理强度"
            data-placement={position().placement}
            style={{
              top: `${position().top}px`,
              left: `${position().left}px`,
              width: `${position().width}px`,
              "max-height": `${position().maxHeight}px`,
              visibility: position().ready ? "visible" : "hidden",
            }}
          >
            <For each={props.options}>
              {(option) => (
                <button
                  class="reasoning-option"
                  classList={{ selected: option.id === props.value }}
                  type="button"
                  role="menuitemradio"
                  aria-checked={option.id === props.value}
                  onClick={() => select(option.id)}
                >
                  <span>
                    <strong>{option.label}</strong>
                    <small>{option.description}</small>
                  </span>
                  <Show when={option.id === props.value}>
                    <Check size={16} aria-label="已选中" />
                  </Show>
                </button>
              )}
            </For>
          </div>
        </Portal>
      </Show>
      <Show when={props.running}>
        <span class="pending-note">下一轮生效</span>
      </Show>
    </div>
  );
}
