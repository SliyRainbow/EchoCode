import { PanelTab } from "./ui/PanelTab";
import { Tabs } from "@kobalte/core/tabs";
import {
  ArrowLeft,
  ArrowRight,
  Braces,
  Camera,
  Contact,
  Download,
  ExternalLink,
  FileText,
  FolderOpen,
  Globe2,
  KeyRound,
  ListTree,
  LoaderCircle,
  Network,
  PanelRightOpen,
  Plus,
  RefreshCw,
  Tags,
  TerminalSquare,
  X,
} from "lucide-solid";
import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import type { BrowserCredential, BrowserFrame, BrowserInspector, BrowserPreview, BrowserState } from "../types";
import {
  browserActivateTab,
  browserAutofill,
  browserBack,
  browserClick,
  browserCloseTab,
  browserDismissError,
  browserEvaluate,
  browserForward,
  browserFillCredential,
  browserHandleDialog,
  browserKey,
  browserNavigate,
  browserOpenDownload,
  browserReload,
  browserResize,
  browserRevealDownload,
  browserSaveScreenshot,
  browserScroll,
  browserShowNativeSurface,
  browserHideNativeSurface,
  browserType,
  getBrowserFrame,
  getBrowserInspector,
  getBrowserState,
  openBrowserURL,
  openURLInSystemBrowser,
  openWorkspaceFile,
  revealWorkspaceFile,
} from "../services/workbench";

type InspectorTab = "dom" | "console" | "network" | "downloads";

export function BrowserPreviewPanel(props: {
  preview: BrowserPreview;
  annotationPolicy?: string;
  credentials?: BrowserCredential[];
	suspended?: boolean;
  onClose: () => void;
}) {
  const managed = createMemo(() => Boolean(props.preview.managed && props.preview.tabId));
  return (
    <Show
      when={managed()}
		fallback={<LocalPreview preview={props.preview} suspended={props.suspended} onClose={props.onClose} />}
    >
      <ManagedBrowser
        initialTabID={props.preview.tabId!}
        annotationPolicy={props.annotationPolicy ?? "never"}
        credentials={props.credentials ?? []}
		suspended={props.suspended}
        onClose={props.onClose}
      />
    </Show>
  );
}

function ManagedBrowser(props: {
	initialTabID: string;
	annotationPolicy: string;
	credentials: BrowserCredential[];
	suspended?: boolean;
	onClose: () => void;
}) {
	const annotationPolicy = createMemo(() => props.annotationPolicy.toLowerCase());
	const surfaceSuspended = createMemo(() => Boolean(props.suspended));
  const [state, setState] = createSignal<BrowserState>(emptyState());
  const [frame, setFrame] = createSignal<BrowserFrame>();
  const [address, setAddress] = createSignal("");
  const [error, setError] = createSignal("");
  const [status, setStatus] = createSignal("");
  const [frameNotice, setFrameNotice] = createSignal("");
  const [busy, setBusy] = createSignal("");
  const [nativeAttached, setNativeAttached] = createSignal(false);
  const [inspectorOpen, setInspectorOpen] = createSignal(false);
  const [inspectorTab, setInspectorTab] = createSignal<InspectorTab>("dom");
  const [inspector, setInspector] = createSignal<BrowserInspector>();
  const [evaluateDraft, setEvaluateDraft] = createSignal("");
  const [evaluateResult, setEvaluateResult] = createSignal("");
  const [dialogPrompt, setDialogPrompt] = createSignal("");
	const [annotationsVisible, setAnnotationsVisible] = createSignal(annotationPolicy() === "always");
  let frameHost: HTMLDivElement | undefined;
  let frameImage: HTMLImageElement | undefined;
  let addressInput: HTMLInputElement | undefined;
  let keyCapture: HTMLTextAreaElement | undefined;
  let disposed = false;
  let pollTimer = 0;
  let polling = false;
  let resizeTimer = 0;
  let inspectorTick = 0;
  let inspectorPolling = false;
  let annotationTick = 0;
  let consecutiveFrameFailures = 0;
  let dialogKey = "";
  let nativeSurfaceKey = "";
	let nativeSurfaceOperation: Promise<unknown> = Promise.resolve();

  const activeTab = createMemo(() => state().tabs.find((tab) => tab.id === state().activeTabId));
  const activeTabID = createMemo(() => activeTab()?.id ?? props.initialTabID);
  const nativeMode = createMemo(() => state().renderMode === "native");

  createEffect(() => {
		if (annotationPolicy() === "always") setAnnotationsVisible(true);
		if (annotationPolicy() === "never") setAnnotationsVisible(false);
	});

	createEffect(() => {
    const url = activeTab()?.url;
    if (url && document.activeElement !== addressInput) {
      setAddress(url);
    }
  });

  createEffect(() => {
    const dialog = activeTab()?.dialog;
    const nextKey = dialog ? `${dialog.type}\u0000${dialog.message}\u0000${dialog.defaultValue ?? ""}` : "";
    if (nextKey !== dialogKey) {
      dialogKey = nextKey;
      setDialogPrompt(dialog?.defaultValue ?? "");
    }
  });

  onMount(() => {
    void initialise();
    const resizeObserver = new ResizeObserver(() => scheduleResize());
    const handleVisibilityChange = () => {
      if (document.hidden) {
        void hideNativeSurface();
      } else {
        nativeSurfaceKey = "";
        schedulePoll(0);
      }
    };
    if (frameHost) {
      resizeObserver.observe(frameHost);
    }
    document.addEventListener("visibilitychange", handleVisibilityChange);
    onCleanup(() => {
      resizeObserver.disconnect();
      document.removeEventListener("visibilitychange", handleVisibilityChange);
    });
  });

  onCleanup(() => {
    disposed = true;
    window.clearTimeout(pollTimer);
    window.clearTimeout(resizeTimer);
		void runNativeSurfaceOperation(() => browserHideNativeSurface());
  });

  const closePanel = () => {
    if (disposed) return;
    disposed = true;
    window.clearTimeout(pollTimer);
    window.clearTimeout(resizeTimer);
		void runNativeSurfaceOperation(() => browserHideNativeSurface());
    props.onClose();
  };

  const initialise = async () => {
    try {
      let next = normaliseBrowserCollections(await getBrowserState());
      if (props.initialTabID && next.tabs.some((tab) => tab.id === props.initialTabID)) {
        next = normaliseBrowserCollections(await browserActivateTab(props.initialTabID));
      }
      setState(next);
      setAddress(next.tabs.find((tab) => tab.id === next.activeTabId)?.url ?? "");
      nativeSurfaceKey = "";
    } catch (err) {
      setError(messageOf(err));
    }
    schedulePoll(10);
  };

  const schedulePoll = (delay = 120) => {
    window.clearTimeout(pollTimer);
    pollTimer = window.setTimeout(() => void poll(), delay);
  };

  const poll = async () => {
		if (disposed || polling || surfaceSuspended()) return;
    polling = true;
    try {
      const nextState = withoutTransientErrors(await getBrowserState());
      if (disposed) return;
      setState(nextState);
      const tabID = nextState.activeTabId;
      if (tabID) {
        const nextTab = nextState.tabs.find((tab) => tab.id === tabID);
        if (nextState.renderMode === "native") {
          setFrame(undefined);
          await syncNativeSurface();
          if (disposed) return;
          if (document.activeElement !== addressInput && nextTab?.url) setAddress(nextTab.url);
        } else {
          if (nativeAttached()) await hideNativeSurface();
          const previousFrame = frame();
          const hasCurrentFrame = previousFrame?.tab.id === tabID;
          const includeAnnotations = annotationsVisible() && hasCurrentFrame && annotationTick++ % 20 === 0;
          const nextFrame = await getBrowserFrame(tabID, includeAnnotations, hasCurrentFrame ? previousFrame?.capturedAt ?? "" : "");
          if (disposed) return;
          if (!nextFrame.imageDataUrl && hasCurrentFrame && previousFrame) {
            nextFrame.imageDataUrl = previousFrame.imageDataUrl;
          }
          if (!includeAnnotations && annotationsVisible() && previousFrame?.tab.id === nextFrame.tab.id) {
            nextFrame.elements = previousFrame.elements;
          }
          if (!previousFrame || previousFrame.tab.id !== nextFrame.tab.id || previousFrame.capturedAt !== nextFrame.capturedAt || includeAnnotations) {
            setFrame(nextFrame);
          }
          setState(withFrameState(nextState, nextFrame));
          if (document.activeElement !== addressInput) setAddress(nextFrame.tab.url);
        }
        consecutiveFrameFailures = 0;
        setFrameNotice("");
        setError("");
        if (inspectorOpen() && !inspectorPolling && inspectorTick++ % 20 === 0) {
          inspectorPolling = true;
          void getBrowserInspector(tabID)
            .then((nextInspector) => {
              if (!disposed) setInspector(nextInspector);
            })
            .catch((err) => {
              if (!disposed && !isTransientBrowserError(messageOf(err))) setError(messageOf(err));
            })
            .finally(() => {
              inspectorPolling = false;
            });
        }
      } else {
        setFrame(undefined);
        await hideNativeSurface();
        closePanel();
      }
    } catch (err) {
      if (disposed) return;
      const message = messageOf(err);
      if (isTransientBrowserError(message)) {
        consecutiveFrameFailures += 1;
        setError("");
        if (!frame() && consecutiveFrameFailures >= 2) {
          setFrameNotice("浏览器画面暂时未响应，正在重新连接");
        }
      } else {
        setError(message);
      }
    } finally {
      polling = false;
			if (!disposed && !surfaceSuspended()) {
        const delay = nativeMode() ? (activeTab()?.loading ? 150 : 320) : (activeTab()?.loading ? 80 : 120);
        schedulePoll(document.hidden ? 900 : delay);
      }
    }
  };

  const syncNativeSurface = async (force = false) => {
    const tabID = activeTabID();
		if (!tabID || !frameHost || state().renderMode !== "native" || document.hidden || surfaceSuspended()) return false;
    const rect = frameHost.getBoundingClientRect();
    if (rect.width < 1 || rect.height < 1) return false;
    const key = [
      tabID,
      Math.round(rect.left),
      Math.round(rect.top),
      Math.round(rect.width),
      Math.round(rect.height),
      window.innerWidth,
      window.innerHeight,
    ].join(":");
    if (!force && nativeSurfaceKey === key && nativeAttached()) return true;
		const shown = await runNativeSurfaceOperation(() => browserShowNativeSurface(
			tabID,
			rect.left,
			rect.top,
			rect.width,
			rect.height,
			window.innerWidth,
			window.innerHeight,
		));
		if (!disposed && !surfaceSuspended()) {
      nativeSurfaceKey = shown ? key : "";
      setNativeAttached(shown);
    }
    return shown;
  };

  const hideNativeSurface = async () => {
    nativeSurfaceKey = "";
    setNativeAttached(false);
		await runNativeSurfaceOperation(() => browserHideNativeSurface());
  };

	createEffect(() => {
		if (surfaceSuspended()) {
			window.clearTimeout(pollTimer);
			void hideNativeSurface();
			return;
		}
		nativeSurfaceKey = "";
		schedulePoll(0);
	});

  const retryFrame = () => {
    consecutiveFrameFailures = 0;
    setError("");
    setFrameNotice("");
    schedulePoll(0);
  };

  const dismissNotice = () => {
    consecutiveFrameFailures = 0;
    setError("");
    setStatus("");
    setFrameNotice("");
    const tabID = activeTabID();
    setState(clearBrowserErrors(state()));
    void browserDismissError(tabID)
      .then((nextState) => {
        if (!disposed) setState(clearBrowserErrors(nextState));
      })
      .catch(() => undefined);
  };

  const scheduleResize = () => {
    window.clearTimeout(resizeTimer);
    resizeTimer = window.setTimeout(() => {
      const tabID = activeTabID();
			if (!tabID || !frameHost || surfaceSuspended()) return;
      const rect = frameHost.getBoundingClientRect();
      if (state().renderMode === "native") {
        nativeSurfaceKey = "";
        void syncNativeSurface(true).catch((err) => setError(messageOf(err)));
      } else {
        void browserResize(tabID, Math.round(rect.width), Math.round(rect.height)).catch((err) => setError(messageOf(err)));
      }
    }, 140);
  };

	function runNativeSurfaceOperation<T>(operation: () => Promise<T>): Promise<T> {
		const next = nativeSurfaceOperation.then(operation, operation);
		nativeSurfaceOperation = next.then(() => undefined, () => undefined);
		return next;
	}

  const run = async (name: string, action: () => Promise<unknown>) => {
    setBusy(name);
    setError("");
    try {
      await action();
      schedulePoll(20);
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusy("");
    }
  };

  const navigate = async () => {
    const target = address().trim();
    if (!target) return;
    const tabID = activeTabID();
    if (tabID) {
      await run("navigate", () => browserNavigate(tabID, target));
    } else {
      await newTab(target);
    }
    keyCapture?.focus();
  };

  const newTab = async (target = "about:blank") => {
    await run("new-tab", async () => setState(await openBrowserURL(target)));
  };

  const activate = async (tabID: string) => {
    await run("activate", async () => {
      setState(await browserActivateTab(tabID));
      setFrame(undefined);
      nativeSurfaceKey = "";
      consecutiveFrameFailures = 0;
      annotationTick = 0;
    });
  };

  const closeTab = async (tabID: string) => {
    const previous = state();
    const closingIndex = previous.tabs.findIndex((tab) => tab.id === tabID);
    if (closingIndex < 0) return;

    const closeRequest = browserCloseTab(tabID);
    const tabs = previous.tabs.filter((tab) => tab.id !== tabID);
    const activeTabId = previous.activeTabId === tabID
      ? (tabs[Math.min(closingIndex, tabs.length - 1)]?.id ?? "")
      : previous.activeTabId;
    setState({ ...previous, tabs, activeTabId });
    setFrame(undefined);

    if (tabs.length === 0) {
      closePanel();
      await closeRequest.catch(() => undefined);
      return;
    }

    setBusy("close-tab");
    try {
      const next = normaliseBrowserCollections(await closeRequest);
      if (!disposed) setState(next);
    } catch (err) {
      if (!disposed) {
        setState(previous);
        setError(messageOf(err));
      }
    } finally {
      if (!disposed) {
        setBusy("");
        schedulePoll(20);
      }
    }
  };

  const pointerClick = (event: MouseEvent) => {
    if (event.button !== 0) return;
    const currentFrame = frame();
    const tabID = activeTabID();
    if (!currentFrame || !frameImage || !tabID) return;
    const rect = frameImage.getBoundingClientRect();
    if (!rect.width || !rect.height) return;
    const normalizedX = (event.clientX - rect.left) / rect.width;
    const normalizedY = (event.clientY - rect.top) / rect.height;
    if (normalizedX < 0 || normalizedX > 1 || normalizedY < 0 || normalizedY > 1) return;
    const x = normalizedX * currentFrame.width;
    const y = normalizedY * currentFrame.height;
    void browserClick(tabID, x, y, Math.max(1, event.detail)).then(() => schedulePoll(80)).catch((err) => setError(messageOf(err)));
    keyCapture?.focus();
  };

  const wheel = (event: WheelEvent) => {
    const tabID = activeTabID();
    if (!tabID) return;
    event.preventDefault();
    void browserScroll(tabID, event.deltaX, event.deltaY).then(() => schedulePoll(50)).catch((err) => setError(messageOf(err)));
  };

  const captureInput = (event: InputEvent & { currentTarget: HTMLTextAreaElement }) => {
    const text = event.currentTarget.value;
    event.currentTarget.value = "";
    const tabID = activeTabID();
    if (!tabID || !text) return;
    void browserType(tabID, text).then(() => schedulePoll(70)).catch((err) => setError(messageOf(err)));
  };

  const captureKey = (event: KeyboardEvent) => {
    const tabID = activeTabID();
    if (!tabID) return;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "l") {
      event.preventDefault();
      addressInput?.focus();
      addressInput?.select();
      return;
    }
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "r") {
      event.preventDefault();
      void run("reload", () => browserReload(tabID));
      return;
    }
    if (event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey) return;
    event.preventDefault();
    void browserKey(tabID, event.key, event.ctrlKey, event.altKey, event.shiftKey, event.metaKey)
      .then(() => schedulePoll(70))
      .catch((err) => setError(messageOf(err)));
  };

  const openInspector = async (tab: InspectorTab) => {
    setInspectorTab(tab);
    setInspectorOpen(true);
    const tabID = activeTabID();
    if (!tabID || tab === "downloads") return;
    await run("inspector", async () => setInspector(await getBrowserInspector(tabID)));
  };

  const evaluate = async () => {
    const tabID = activeTabID();
    const expression = evaluateDraft().trim();
    if (!tabID || !expression) return;
    await run("evaluate", async () => setEvaluateResult(await browserEvaluate(tabID, expression)));
  };

  const fillCredential = async () => {
    const tab = activeTab();
    if (!tab) return;
    const credential = credentialForURL(props.credentials, tab.url);
    if (!credential) {
      setStatus("当前网站没有已保存的凭据，请先到浏览器设置中添加");
      return;
    }
    await run("credential", async () => setStatus(`已填充 ${await browserFillCredential(tab.id, credential.id)} 个登录字段`));
  };

  const handleDialog = async (accept: boolean) => {
    const tabID = activeTabID();
    if (!tabID) return;
    await run("dialog", () => browserHandleDialog(tabID, accept, dialogPrompt()));
  };

  return (
		<Tabs as="aside" value={state().activeTabId} onChange={(value) => { if (value && value !== state().activeTabId) void activate(value); }}
			class="browser-preview browser-managed"
			classList={{ "browser-surface-suspended": surfaceSuspended() }}
			aria-label="EchoCode 内置浏览器"
		>
      <Tabs.List class="browser-tabs panel-tabs" aria-label="浏览器标签页">
        <For each={state().tabs}>
          {(tab) => (
            <div class="browser-tab" classList={{ active: tab.id === state().activeTabId }}>
              <PanelTab value={tab.id} title={tab.title || tab.url}>
                <Show when={!tab.loading} fallback={<LoaderCircle class="spinning" size={12} />}><Globe2 size={12} /></Show>
                <span>{tab.title || "新标签页"}</span>
              </PanelTab>
              <button
                class="browser-tab-close"
                type="button"
                title="关闭标签页"
                aria-label={`关闭 ${tab.title || "标签页"}`}
                onPointerDown={(event) => {
                  if (event.button !== 0) return;
                  event.preventDefault();
                  event.stopPropagation();
                  void closeTab(tab.id);
                }}
                onClick={(event) => {
                  event.stopPropagation();
                  if (event.detail === 0) void closeTab(tab.id);
                }}
              ><X size={12} /></button>
            </div>
          )}
        </For>
        <button class="browser-new-tab" type="button" title="新建标签页" aria-label="新建标签页" onClick={() => void newTab()}><Plus size={14} /></button>
        <span class="browser-engine" title={state().engine}>{state().engine}{nativeMode() ? " · 原生" : " · 兼容"}</span>
        <button
          class="browser-panel-close"
          type="button"
          title="关闭浏览器面板"
          aria-label="关闭浏览器面板"
          onPointerDown={(event) => {
            if (event.button !== 0) return;
            event.preventDefault();
            event.stopPropagation();
            closePanel();
          }}
          onClick={closePanel}
        ><X size={15} /></button>
      </Tabs.List>

      <div class="browser-toolbar">
        <button type="button" title="后退" aria-label="后退" disabled={!activeTab()?.canGoBack || Boolean(busy())} onClick={() => void run("back", () => browserBack(activeTabID()))}><ArrowLeft size={15} /></button>
        <button type="button" title="前进" aria-label="前进" disabled={!activeTab()?.canGoForward || Boolean(busy())} onClick={() => void run("forward", () => browserForward(activeTabID()))}><ArrowRight size={15} /></button>
        <button type="button" title="刷新" aria-label="刷新" disabled={!activeTabID() || Boolean(busy())} onClick={() => void run("reload", () => browserReload(activeTabID()))}>
          <Show when={busy() !== "reload"} fallback={<LoaderCircle class="spinning" size={15} />}><RefreshCw size={15} /></Show>
        </button>
        <form class="browser-address-form" onSubmit={(event) => { event.preventDefault(); void navigate(); }}>
          <Globe2 size={13} />
          <input ref={addressInput} value={address()} aria-label="浏览器地址" spellcheck={false} onInput={(event) => setAddress(event.currentTarget.value)} />
        </form>
        <button type="button" title="在系统浏览器中打开" aria-label="在系统浏览器中打开" disabled={!activeTab()?.url} onClick={() => void run("external", () => openURLInSystemBrowser(activeTab()!.url))}><ExternalLink size={15} /></button>
        <button type="button" title="保存截图" aria-label="保存截图" disabled={!activeTabID()} onClick={() => void run("screenshot", async () => setStatus(`截图已保存：${await browserSaveScreenshot(activeTabID())}`))}><Camera size={15} /></button>
        <button type="button" title="填充联系信息" aria-label="填充联系信息" disabled={!activeTabID()} onClick={() => void run("autofill", async () => setStatus(`已填充 ${await browserAutofill(activeTabID())} 个字段`))}><Contact size={15} /></button>
        <button type="button" title="填充已保存的登录凭据" aria-label="填充已保存的登录凭据" disabled={!activeTabID()} onClick={() => void fillCredential()}><KeyRound size={15} /></button>
        <button type="button" title={nativeMode() ? "原生画面无需截图批注" : "显示元素批注"} aria-label="显示元素批注" disabled={nativeMode() || annotationPolicy() === "never"} classList={{ active: !nativeMode() && annotationsVisible() }} onClick={() => { if (annotationPolicy() === "ask") setAnnotationsVisible((value) => !value); schedulePoll(10); }}><Tags size={15} /></button>
        <button type="button" title="页面检查器" aria-label="页面检查器" classList={{ active: inspectorOpen() }} onClick={() => inspectorOpen() ? setInspectorOpen(false) : void openInspector("dom")}><PanelRightOpen size={15} /></button>
        <button type="button" title="下载" aria-label="下载" classList={{ active: inspectorOpen() && inspectorTab() === "downloads" }} onClick={() => void openInspector("downloads")}><Download size={15} /></button>
      </div>

      <Tabs.Content value={state().activeTabId} forceMount class="browser-surface-layout" classList={{ "inspector-open": inspectorOpen() }}>
        <div class="browser-remote-frame" classList={{ native: nativeMode() }} ref={frameHost} onClick={(event) => { if (!nativeMode()) pointerClick(event); }} onWheel={(event) => { if (!nativeMode()) wheel(event); }}>
          <Show
            when={nativeMode()}
            fallback={(
              <>
                <Show when={frame()} fallback={(
                  <div class="browser-preview-loading" role="status" aria-live="polite">
                    <LoaderCircle class="spinning" size={18} />
                    <span>{frameNotice() || "正在连接浏览器引擎"}</span>
                    <Show when={frameNotice()}>
                      <button type="button" title="立即重试" aria-label="立即重试" onClick={retryFrame}><RefreshCw size={14} /></button>
                    </Show>
                  </div>
                )}>
                  {(current) => (
                    <div class="browser-frame-image-wrap">
                      <img ref={frameImage} src={current().imageDataUrl} alt={`${current().tab.title} 页面画面`} draggable={false} />
                      <Show when={annotationsVisible()}>
                        <For each={current().elements ?? []}>
                          {(element) => (
                            <span
                              class="browser-element-tag"
                              title={element.name || element.text || element.selector}
                              style={{
                                left: `${(element.x / current().width) * 100}%`,
                                top: `${(element.y / current().height) * 100}%`,
                                width: `${(element.width / current().width) * 100}%`,
                                height: `${(element.height / current().height) * 100}%`,
                              }}
                            ><b>{element.index}</b></span>
                          )}
                        </For>
                      </Show>
                    </div>
                  )}
                </Show>
                <textarea
                  ref={keyCapture}
                  class="browser-key-capture"
                  aria-label="浏览器键盘输入"
                  onInput={captureInput}
                  onKeyDown={captureKey}
                />
                <Show when={activeTab()?.loading}><span class="browser-loading-pill"><LoaderCircle class="spinning" size={12} />加载中</span></Show>
                <Show when={activeTab()?.dialog}>
                  {(dialog) => (
                    <div class="browser-dialog-overlay" role="dialog" aria-modal="true" aria-label="网页对话框">
                      <div class="browser-dialog-card">
                        <strong>{dialogTitle(dialog().type)}</strong>
                        <p>{dialog().message}</p>
                        <Show when={dialog().type === "prompt"}>
                          <input value={dialogPrompt()} autofocus onInput={(event) => setDialogPrompt(event.currentTarget.value)} />
                        </Show>
                        <div><button type="button" onClick={() => void handleDialog(false)}>取消</button><button class="primary" type="button" onClick={() => void handleDialog(true)}>确定</button></div>
                      </div>
                    </div>
                  )}
                </Show>
              </>
            )}
          >
            <div class="browser-native-surface" aria-label="浏览器原生画面">
              <Show when={!nativeAttached()}>
                <div class="browser-preview-loading" role="status" aria-live="polite">
                  <LoaderCircle class="spinning" size={18} />
                  <span>正在挂载浏览器原生画面</span>
                </div>
              </Show>
            </div>
          </Show>
        </div>

        <Show when={inspectorOpen()}>
          <Tabs as="section" class="browser-inspector" aria-label="浏览器检查器" value={inspectorTab()} onChange={(value) => void openInspector(value as InspectorTab)}>
            <Tabs.List class="browser-inspector-tabs panel-tabs" aria-label="检查器视图">
              <PanelTab value="dom"><ListTree size={14} />DOM</PanelTab>
              <PanelTab value="console"><TerminalSquare size={14} />控制台</PanelTab>
              <PanelTab value="network"><Network size={14} />网络</PanelTab>
              <PanelTab value="downloads"><Download size={14} />下载</PanelTab>
              <button class="browser-inspector-close" title="关闭检查器" aria-label="关闭检查器" onClick={() => setInspectorOpen(false)}><X size={14} /></button>
            </Tabs.List>
            <Tabs.Content value={inspectorTab()} class="browser-inspector-body">
              <Show when={inspectorTab() === "dom"}>
                <div class="browser-dom-summary"><strong>{inspector()?.snapshot.title}</strong><span>{inspector()?.snapshot.url}</span><p>{inspector()?.snapshot.text}</p></div>
                <div class="browser-element-list">
                  <For each={inspector()?.snapshot.elements ?? []} fallback={<span class="browser-empty-log">没有可交互元素</span>}>
                    {(element) => <div><b>{element.index}</b><code>{element.selector}</code><span>{element.name || element.text || element.tag}</span></div>}
                  </For>
                </div>
              </Show>
              <Show when={inspectorTab() === "console"}>
                <For each={inspector()?.console ?? []} fallback={<span class="browser-empty-log">控制台暂无输出</span>}>
                  {(entry) => <div class={`browser-log-row ${entry.level}`}><span>{entry.level}</span><code>{entry.message}</code></div>}
                </For>
                <Show when={state().cdpEnabled}>
                  <form class="browser-console-form" onSubmit={(event) => { event.preventDefault(); void evaluate(); }}>
                    <Braces size={14} /><input value={evaluateDraft()} placeholder="CDP JavaScript 表达式" onInput={(event) => setEvaluateDraft(event.currentTarget.value)} /><button type="submit">执行</button>
                  </form>
                  <Show when={evaluateResult()}><pre class="browser-evaluate-result">{evaluateResult()}</pre></Show>
                </Show>
              </Show>
              <Show when={inspectorTab() === "network"}>
                <For each={(inspector()?.network ?? []).slice().reverse()} fallback={<span class="browser-empty-log">尚未记录网络请求</span>}>
                  {(entry) => <div class="browser-network-row" classList={{ failed: entry.failed }}><b>{entry.status || "ERR"}</b><span>{entry.method}</span><code title={entry.url}>{entry.url}</code></div>}
                </For>
              </Show>
              <Show when={inspectorTab() === "downloads"}>
                <For each={state().downloads} fallback={<span class="browser-empty-log">暂无下载</span>}>
                  {(item) => <div class="browser-download-row"><FileText size={15} /><div><strong>{item.filename || "下载"}</strong><span>{downloadStatus(item.state, item.receivedBytes, item.totalBytes)}</span></div><button disabled={!item.path} title="打开" onClick={() => void run("open-download", () => browserOpenDownload(item.id))}><ExternalLink size={14} /></button><button disabled={!item.path} title="在文件夹中显示" onClick={() => void run("reveal-download", () => browserRevealDownload(item.id))}><FolderOpen size={14} /></button></div>}
                </For>
              </Show>
            </Tabs.Content>
          </Tabs>
        </Show>
      </Tabs.Content>

      <Show when={error() || activeTab()?.error || status()}>
        <div class="browser-preview-error" classList={{ status: !error() && !activeTab()?.error }} role="status">
          <span>{error() || activeTab()?.error || status()}</span>
          <button title="关闭提示" aria-label="关闭提示" onClick={dismissNotice}><X size={13} /></button>
        </div>
      </Show>
    </Tabs>
  );
}

function LocalPreview(props: { preview: BrowserPreview; suspended?: boolean; onClose: () => void }) {
  const [revision, setRevision] = createSignal(0);
  const [loading, setLoading] = createSignal(true);
  const [busyAction, setBusyAction] = createSignal<"system" | "reveal" | "">("");
  const [error, setError] = createSignal("");
  const frameURL = createMemo(() => {
    const target = new URL(props.preview.url);
    target.searchParams.set("echocode_reload", String(revision()));
    return target.toString();
  });
  createEffect(() => {
    props.preview.url;
    revision();
    setLoading(true);
    setError("");
  });
  const run = async (action: "system" | "reveal") => {
    setBusyAction(action);
    try {
      if (action === "system") await openWorkspaceFile(props.preview.path);
      else await revealWorkspaceFile(props.preview.path);
    } catch (err) {
      setError(messageOf(err));
    } finally {
      setBusyAction("");
    }
  };
  return (
		<aside
			class="browser-preview"
			classList={{ "browser-surface-suspended": Boolean(props.suspended) }}
			aria-label="EchoCode 内置浏览器"
		>
      <header class="browser-preview-head">
        <span class="browser-preview-mark"><Globe2 size={16} /></span>
        <div class="browser-preview-title" title={props.preview.path}><strong>{props.preview.name}</strong><span>本地预览兜底模式</span></div>
        <div class="browser-preview-actions">
          <button title="刷新" aria-label="刷新" onClick={() => setRevision((value) => value + 1)}><RefreshCw size={15} /></button>
          <button title="使用系统浏览器打开" aria-label="使用系统浏览器打开" disabled={Boolean(busyAction())} onClick={() => void run("system")}><ExternalLink size={15} /></button>
          <button title="在文件夹中显示" aria-label="在文件夹中显示" disabled={Boolean(busyAction())} onClick={() => void run("reveal")}><FolderOpen size={15} /></button>
          <button title="关闭预览" aria-label="关闭预览" onClick={props.onClose}><X size={16} /></button>
        </div>
      </header>
      <div class="browser-preview-address"><Globe2 size={13} /><span>{props.preview.path}</span></div>
      <div class="browser-preview-frame">
        <iframe src={frameURL()} title={`${props.preview.name} 预览`} sandbox="allow-scripts allow-forms allow-modals allow-popups allow-downloads allow-same-origin" referrerPolicy="no-referrer" allow="camera 'none'; microphone 'none'; geolocation 'none'" onLoad={() => setLoading(false)} />
        <Show when={loading()}><div class="browser-preview-loading"><LoaderCircle class="spinning" size={18} /><span>正在加载</span></div></Show>
      </div>
      <Show when={error()}><div class="browser-preview-error" role="alert">{error()}</div></Show>
    </aside>
  );
}

function emptyState(): BrowserState {
  return { available: true, running: true, engine: "", renderMode: "stream", activeTabId: "", tabs: [], downloads: [], cdpEnabled: false };
}

function messageOf(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function isTransientBrowserError(message: string): boolean {
  return /context deadline exceeded|deadline exceeded|context canceled|target closed|浏览标签页不存在/i.test(message);
}

function withoutTransientErrors(state: BrowserState): BrowserState {
  state = normaliseBrowserCollections(state);
  return {
    ...state,
    lastError: isTransientBrowserError(state.lastError ?? "") ? "" : state.lastError,
    tabs: state.tabs.map((tab) => ({
      ...tab,
      error: isTransientBrowserError(tab.error ?? "") ? "" : tab.error,
    })),
  };
}

function normaliseBrowserCollections(state: BrowserState): BrowserState {
  return {
    ...state,
    renderMode: state.renderMode === "native" ? "native" : "stream",
    tabs: Array.isArray(state.tabs) ? state.tabs : [],
    downloads: Array.isArray(state.downloads) ? state.downloads : [],
  };
}

function clearBrowserErrors(state: BrowserState): BrowserState {
  return {
    ...state,
    lastError: "",
    tabs: state.tabs.map((tab) => ({ ...tab, error: "" })),
  };
}

function withFrameState(state: BrowserState, frame: BrowserFrame): BrowserState {
  return {
    ...state,
    lastError: "",
    tabs: state.tabs.map((tab) => tab.id === frame.tab.id ? { ...frame.tab, error: "" } : tab),
  };
}

function downloadStatus(state: string, received: number, total: number): string {
  if (state === "completed") return "下载完成";
  if (state === "canceled") return "已取消";
  if (total > 0) return `${Math.round((received / total) * 100)}%`;
  return "下载中";
}

function credentialForURL(credentials: BrowserCredential[], rawURL: string): BrowserCredential | undefined {
  try {
    const origin = new URL(rawURL).origin.toLowerCase();
    return credentials.find((credential) => credential.origin.toLowerCase() === origin && credential.passwordConfigured);
  } catch {
    return undefined;
  }
}

function dialogTitle(type: string): string {
  if (type === "confirm") return "网页确认";
  if (type === "prompt") return "网页输入";
  if (type === "beforeunload") return "离开网页";
  return "网页提示";
}
