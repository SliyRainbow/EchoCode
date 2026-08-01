import { Bot, FileCode2, Globe2 } from "lucide-solid";
import { Show, createMemo } from "solid-js";
import type { BrowserCredential, BrowserPreview, WorkspaceFileRequest } from "../types";
import { BrowserPreviewPanel } from "./BrowserPreviewPanel";
import { ReviewPanel } from "./ReviewPanel";
import { SubagentPanel } from "./SubagentPanel";
import type { SubagentPart } from "./chat/MessageContent";

export type SidePanelView = "browser" | "files" | "subagent";

type SidePanelHostProps = {
  browser?: BrowserPreview;
  reviewOpen: boolean;
  subagent?: SubagentPart;
  parentTaskID?: string;
  stoppingSubagentID?: string;
  activeView: SidePanelView;
  workspaceRoot: string;
  fileRequest?: WorkspaceFileRequest;
  dark: boolean;
  browserSuspended: boolean;
  annotationPolicy?: string;
  credentials?: BrowserCredential[];
  onSelectView: (view: SidePanelView) => void;
  onCloseBrowser: () => void;
  onCloseFiles: () => void;
  onStopSubagent: (part: SubagentPart) => void | Promise<void>;
  onCloseSubagent: () => void;
};

export function SidePanelHost(props: SidePanelHostProps) {
  const hasBrowser = createMemo(() => Boolean(props.browser));
  const hasFiles = createMemo(() => props.reviewOpen);
  const hasSubagent = createMemo(() => Boolean(props.subagent));
  const showModeTabs = createMemo(() => [hasBrowser(), hasFiles(), hasSubagent()].filter(Boolean).length > 1);
  const activeView = createMemo<SidePanelView>(() => {
    if (props.activeView === "browser" && hasBrowser()) return "browser";
    if (props.activeView === "files" && hasFiles()) return "files";
    if (props.activeView === "subagent" && hasSubagent()) return "subagent";
    if (hasBrowser()) return "browser";
    if (hasFiles()) return "files";
    return "subagent";
  });

  return (
    <aside class="side-panel-host" classList={{ "has-mode-tabs": showModeTabs() }} aria-label="右侧工作面板">
      <Show when={showModeTabs()}>
        <div class="side-panel-mode-tabs" role="tablist" aria-label="右侧面板标签">
          <Show when={hasBrowser()}>
            <button
              type="button"
              role="tab"
              classList={{ active: activeView() === "browser" }}
              aria-selected={activeView() === "browser"}
              onClick={() => props.onSelectView("browser")}
            >
              <Globe2 size={13} />
              <span>浏览器</span>
            </button>
          </Show>
          <Show when={hasSubagent()}>
            <button
              type="button"
              role="tab"
              classList={{ active: activeView() === "subagent" }}
              aria-selected={activeView() === "subagent"}
              title={props.subagent?.label || "子代理"}
              onClick={() => props.onSelectView("subagent")}
            >
              <Bot size={13} />
              <span>{props.subagent?.label || "子代理"}</span>
            </button>
          </Show>
          <Show when={hasFiles()}>
            <button
              type="button"
              role="tab"
              classList={{ active: activeView() === "files" }}
              aria-selected={activeView() === "files"}
              title={props.fileRequest?.path || "文件"}
              onClick={() => props.onSelectView("files")}
            >
              <FileCode2 size={13} />
              <span>{fileTabLabel(props.fileRequest?.path)}</span>
            </button>
          </Show>
        </div>
      </Show>

      <div class="side-panel-pages">
        <Show when={props.browser}>
          {(preview) => (
            <section
              class="side-panel-page"
              classList={{ inactive: activeView() !== "browser" }}
              aria-hidden={activeView() !== "browser"}
            >
              <BrowserPreviewPanel
                preview={preview()}
                annotationPolicy={props.annotationPolicy ?? "never"}
                credentials={props.credentials ?? []}
                suspended={props.browserSuspended || activeView() !== "browser"}
                onClose={props.onCloseBrowser}
              />
            </section>
          )}
        </Show>
        <Show when={props.reviewOpen}>
          <section
            class="side-panel-page"
            classList={{ inactive: activeView() !== "files" }}
            aria-hidden={activeView() !== "files"}
          >
            <ReviewPanel
              open={props.reviewOpen}
              workspaceRoot={props.workspaceRoot}
              request={props.fileRequest}
              dark={props.dark}
              onClose={props.onCloseFiles}
            />
          </section>
        </Show>
        <Show when={props.subagent}>
          {(part) => (
            <section
              class="side-panel-page"
              classList={{ inactive: activeView() !== "subagent" }}
              aria-hidden={activeView() !== "subagent"}
            >
              <SubagentPanel
                part={part()}
                parentTaskID={props.parentTaskID}
                stopping={props.stoppingSubagentID === part().taskId}
                onStop={props.onStopSubagent}
                onClose={props.onCloseSubagent}
              />
            </section>
          )}
        </Show>
      </div>
    </aside>
  );
}

function fileTabLabel(path = ""): string {
  const normalized = path.replaceAll("\\", "/");
  return normalized.split("/").at(-1) || "文件";
}
