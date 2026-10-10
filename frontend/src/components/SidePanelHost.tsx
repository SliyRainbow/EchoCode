import { PanelTab } from "./ui/PanelTab";
import { Tabs } from "@kobalte/core/tabs";
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
    <Tabs as="aside" class="side-panel-host" classList={{ "has-mode-tabs": showModeTabs() }} aria-label="右侧工作面板" value={activeView()} onChange={(value) => props.onSelectView(value as SidePanelView)}>
      <Show when={showModeTabs()}>
        <Tabs.List class="side-panel-mode-tabs panel-tabs" aria-label="右侧面板标签">
          <Show when={hasBrowser()}>
            <PanelTab value="browser">
              <Globe2 size={13} />
              <span>浏览器</span>
            </PanelTab>
          </Show>
          <Show when={hasSubagent()}>
            <PanelTab value="subagent"
              title={props.subagent?.label || "子代理"}
            >
              <Bot size={13} />
              <span>{props.subagent?.label || "子代理"}</span>
            </PanelTab>
          </Show>
          <Show when={hasFiles()}>
            <PanelTab value="files"
              title={props.fileRequest?.path || "文件"}
            >
              <FileCode2 size={13} />
              <span>{fileTabLabel(props.fileRequest?.path)}</span>
            </PanelTab>
          </Show>
        </Tabs.List>
      </Show>

      <div class="side-panel-pages">
        <Show when={props.browser}>
          {(preview) => (
            <Tabs.Content as="section" value="browser" forceMount
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
            </Tabs.Content>
          )}
        </Show>
        <Show when={props.reviewOpen}>
          <Tabs.Content as="section" value="files" forceMount
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
          </Tabs.Content>
        </Show>
        <Show when={props.subagent}>
          {(part) => (
            <Tabs.Content as="section" value="subagent" forceMount
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
            </Tabs.Content>
          )}
        </Show>
      </div>
    </Tabs>
  );
}

function fileTabLabel(path = ""): string {
  const normalized = path.replaceAll("\\", "/");
  return normalized.split("/").at(-1) || "文件";
}
