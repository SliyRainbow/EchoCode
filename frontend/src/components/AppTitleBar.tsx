import { ArrowLeft, ArrowRight, Copy, Maximize2, Minus, PanelLeft, X } from "lucide-solid";
import { Show, createSignal, onCleanup, onMount } from "solid-js";

type DesktopRuntime = {
  Quit: () => void;
  WindowIsMaximised: () => Promise<boolean>;
  WindowMinimise: () => void;
  WindowToggleMaximise: () => void;
};

function desktopRuntime(): DesktopRuntime | undefined {
  if (typeof window === "undefined") return undefined;
  return (window as Window & { runtime?: DesktopRuntime }).runtime;
}

export function AppTitleBar(props: {
  pageTitle?: string;
  sidebarCollapsed?: boolean;
  sidebarControls?: string;
  onToggleSidebar?: () => void;
  canGoBack?: boolean;
  canGoForward?: boolean;
  onBack?: () => void;
  onForward?: () => void;
}) {
  const [maximised, setMaximised] = createSignal(false);
  let stateTimer: number | undefined;

  const refreshWindowState = () => {
    const runtime = desktopRuntime();
    if (!runtime) return;
    void runtime.WindowIsMaximised().then(setMaximised).catch(() => undefined);
  };

  const scheduleWindowStateRefresh = (delay = 80) => {
    if (stateTimer !== undefined) window.clearTimeout(stateTimer);
    stateTimer = window.setTimeout(() => {
      stateTimer = undefined;
      refreshWindowState();
    }, delay);
  };

  const toggleMaximise = () => {
    const runtime = desktopRuntime();
    if (!runtime) return;
    runtime.WindowToggleMaximise();
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
      <div class="app-titlebar-navigation">
        <button type="button" aria-label={props.sidebarCollapsed ? "展开侧栏" : "收起侧栏"} aria-expanded={!props.sidebarCollapsed} aria-controls={props.sidebarControls} title="切换侧栏 (Ctrl+B)" onClick={props.onToggleSidebar}><PanelLeft size={15} /></button>
        <button type="button" aria-label="后退" title="后退" disabled={!props.canGoBack} onClick={props.onBack}><ArrowLeft size={14} /></button>
        <button type="button" aria-label="前进" title="前进" disabled={!props.canGoForward} onClick={props.onForward}><ArrowRight size={14} /></button>
      </div>
      <div class="app-titlebar-drag" onDblClick={toggleMaximise}>
        <span class="app-titlebar-name" title={props.pageTitle || "EchoCode"}>{props.pageTitle || "EchoCode"}</span>
      </div>
      <div class="app-titlebar-controls" aria-label="窗口控制">
        <button
          type="button"
          title="最小化"
          aria-label="最小化窗口"
          onClick={() => desktopRuntime()?.WindowMinimise()}
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
          onClick={() => desktopRuntime()?.Quit()}
        >
          <X size={16} strokeWidth={1.65} />
        </button>
      </div>
    </header>
  );
}
