import { Copy, Maximize2, Minus, X } from "lucide-solid";
import { Show, createSignal, onCleanup, onMount } from "solid-js";
import {
  Quit,
  WindowIsMaximised,
  WindowMinimise,
  WindowToggleMaximise,
} from "../../wailsjs/runtime/runtime";

function hasDesktopRuntime(): boolean {
  return typeof window !== "undefined" && "runtime" in window;
}

export function AppTitleBar() {
  const [maximised, setMaximised] = createSignal(false);
  let stateTimer: number | undefined;

  const refreshWindowState = () => {
    if (!hasDesktopRuntime()) return;
    void WindowIsMaximised().then(setMaximised).catch(() => undefined);
  };

  const scheduleWindowStateRefresh = (delay = 80) => {
    if (stateTimer !== undefined) window.clearTimeout(stateTimer);
    stateTimer = window.setTimeout(() => {
      stateTimer = undefined;
      refreshWindowState();
    }, delay);
  };

  const toggleMaximise = () => {
    if (!hasDesktopRuntime()) return;
    WindowToggleMaximise();
    scheduleWindowStateRefresh(120);
  };

  onMount(() => {
    refreshWindowState();
    const handleResize = () => scheduleWindowStateRefresh();
    window.addEventListener("resize", handleResize);
    onCleanup(() => {
      window.removeEventListener("resize", handleResize);
      if (stateTimer !== undefined) window.clearTimeout(stateTimer);
    });
  });

  return (
    <header class="app-titlebar" aria-label="EchoCode 窗口标题栏">
      <div class="app-titlebar-drag" onDblClick={toggleMaximise}>
        <span class="app-titlebar-name">EchoCode</span>
      </div>
      <div class="app-titlebar-controls" aria-label="窗口控制">
        <button
          type="button"
          title="最小化"
          aria-label="最小化窗口"
          onClick={() => { if (hasDesktopRuntime()) WindowMinimise(); }}
        >
          <Minus size={15} strokeWidth={1.7} />
        </button>
        <button
          type="button"
          title={maximised() ? "还原" : "最大化"}
          aria-label={maximised() ? "还原窗口" : "最大化窗口"}
          onClick={toggleMaximise}
        >
          <Show when={maximised()} fallback={<Maximize2 size={13} strokeWidth={1.65} />}>
            <Copy class="app-titlebar-restore-icon" size={13} strokeWidth={1.65} />
          </Show>
        </button>
        <button
          class="app-titlebar-close"
          type="button"
          title="关闭"
          aria-label="关闭窗口"
          onClick={() => { if (hasDesktopRuntime()) Quit(); }}
        >
          <X size={16} strokeWidth={1.65} />
        </button>
      </div>
    </header>
  );
}
