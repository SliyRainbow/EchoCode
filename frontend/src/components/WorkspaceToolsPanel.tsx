import {
  Check,
  GitBranch,
  GitCommitHorizontal,
  Minus,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Square,
  TerminalSquare,
  Trash2,
  X,
} from "lucide-solid";
import { For, Show, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import {
  commitGitChanges,
  createGitBranch,
  getGitDiff,
  getGitStatus,
  getTerminalSession,
  onTerminalSessionUpdate,
  sendTerminalCommand,
  stageGitPaths,
  startTerminalSession,
  stopTerminalSession,
  switchGitBranch,
  unstageGitPaths,
} from "../services/workbench";
import type { GitDiff, GitFileStatus, GitStatus, TerminalSessionState } from "../types";

type WorkspaceToolsPanelProps = {
  open: boolean;
  workspaceRoot: string;
  shellAccess: boolean;
  readOnly: boolean;
  onClose: () => void;
};

const workspaceToolsHeightKey = "echocode.workspace-tools-height";
const defaultWorkspaceToolsHeight = 280;
const minWorkspaceToolsHeight = 180;
const maxWorkspaceToolsHeight = 520;

function readWorkspaceToolsHeight(): number {
  try {
    const value = Number(window.localStorage.getItem(workspaceToolsHeightKey));
    return Number.isFinite(value) ? Math.min(maxWorkspaceToolsHeight, Math.max(minWorkspaceToolsHeight, value)) : defaultWorkspaceToolsHeight;
  } catch {
    return defaultWorkspaceToolsHeight;
  }
}

function persistWorkspaceToolsHeight(value: number) {
  try {
    window.localStorage.setItem(workspaceToolsHeightKey, String(Math.round(value)));
  } catch {
    // Preview environments may not expose localStorage.
  }
}

export function WorkspaceToolsPanel(props: WorkspaceToolsPanelProps) {
  const [activeTab, setActiveTab] = createSignal<"git" | "terminal">("git");
  const [status, setStatus] = createSignal<GitStatus>();
  const [diff, setDiff] = createSignal<GitDiff>();
  const [selectedPath, setSelectedPath] = createSignal("");
  const [diffStaged, setDiffStaged] = createSignal(false);
  const [gitBusy, setGitBusy] = createSignal(false);
  const [gitError, setGitError] = createSignal("");
  const [commitMessage, setCommitMessage] = createSignal("");
  const [branchName, setBranchName] = createSignal("");

  const [terminal, setTerminal] = createSignal<TerminalSessionState>();
  const [terminalBusy, setTerminalBusy] = createSignal(false);
  const [terminalError, setTerminalError] = createSignal("");
  const [terminalCommand, setTerminalCommand] = createSignal("");
  const [terminalOutputBase, setTerminalOutputBase] = createSignal(0);
  const [panelHeight, setPanelHeight] = createSignal(readWorkspaceToolsHeight());
  let terminalOutputRef: HTMLPreElement | undefined;
  let workspace = props.workspaceRoot;
  let resizingPanel = false;
  let resizeStartY = 0;
  let resizeStartHeight = 0;

  const clampPanelHeight = (value: number) => Math.min(maxWorkspaceToolsHeight, Math.max(minWorkspaceToolsHeight, value));
  const setAndPersistPanelHeight = (value: number) => {
    const next = clampPanelHeight(value);
    setPanelHeight(next);
    persistWorkspaceToolsHeight(next);
  };
  const movePanelResize = (event: PointerEvent) => {
    if (!resizingPanel) return;
    setAndPersistPanelHeight(resizeStartHeight + (resizeStartY - event.clientY));
  };
  const stopPanelResize = () => {
    if (!resizingPanel) return;
    resizingPanel = false;
    window.removeEventListener("pointermove", movePanelResize);
    window.removeEventListener("pointerup", stopPanelResize);
    window.removeEventListener("pointercancel", stopPanelResize);
  };
  const startPanelResize = (event: PointerEvent) => {
    event.preventDefault();
    resizingPanel = true;
    resizeStartY = event.clientY;
    resizeStartHeight = panelHeight();
    window.addEventListener("pointermove", movePanelResize);
    window.addEventListener("pointerup", stopPanelResize);
    window.addEventListener("pointercancel", stopPanelResize);
  };

  onCleanup(() => {
    window.removeEventListener("pointermove", movePanelResize);
    window.removeEventListener("pointerup", stopPanelResize);
    window.removeEventListener("pointercancel", stopPanelResize);
  });

  onMount(() => {
    const unsubscribe = onTerminalSessionUpdate((next) => {
      const current = terminal();
      if (!current || current.id === next.id) {
        setTerminal(next);
      }
    });
    onCleanup(unsubscribe);
  });

  const selectedFile = createMemo(() => status()?.files.find((file) => file.path === selectedPath()));
  const visibleTerminalOutput = createMemo(() => {
    const output = terminal()?.output ?? "";
    return output.slice(Math.min(terminalOutputBase(), output.length));
  });

  createEffect(() => {
    const nextWorkspace = props.workspaceRoot;
    if (workspace && workspace !== nextWorkspace) {
      const current = terminal();
      if (current?.running) void stopTerminalSession(current.id);
      setTerminal(undefined);
      setTerminalOutputBase(0);
      setSelectedPath("");
      setDiff(undefined);
    }
    workspace = nextWorkspace;
  });

  createEffect(() => {
    if (props.open && activeTab() === "git") {
      props.workspaceRoot;
      void refreshGit();
    }
  });

  createEffect(() => {
    if (!props.open || activeTab() !== "terminal" || !props.shellAccess) return;
    let cancelled = false;
    if (!terminal()) {
      void ensureTerminal();
    }
    const timer = window.setInterval(async () => {
      const current = terminal();
      if (!current || cancelled) return;
      try {
        setTerminal(await getTerminalSession(current.id));
      } catch (error) {
        if (!cancelled) setTerminalError(errorText(error));
      }
    }, 2000);
    onCleanup(() => {
      cancelled = true;
      window.clearInterval(timer);
    });
  });

  createEffect(() => {
    visibleTerminalOutput();
    queueMicrotask(() => {
      if (terminalOutputRef) terminalOutputRef.scrollTop = terminalOutputRef.scrollHeight;
    });
  });

  async function refreshGit() {
    setGitBusy(true);
    setGitError("");
    try {
      const next = await getGitStatus();
      setStatus(next);
      const currentPath = selectedPath();
      const nextFile = next.files.find((file) => file.path === currentPath) ?? next.files[0];
      if (nextFile) {
        await selectFile(nextFile, nextFile.staged && !nextFile.modified);
      } else {
        setSelectedPath("");
        setDiff(undefined);
      }
    } catch (error) {
      setGitError(errorText(error));
    } finally {
      setGitBusy(false);
    }
  }

  async function selectFile(file: GitFileStatus, staged = false) {
    setSelectedPath(file.path);
    setDiffStaged(staged);
    setGitError("");
    try {
      setDiff(await getGitDiff(file.path, staged));
    } catch (error) {
      setDiff(undefined);
      setGitError(errorText(error));
    }
  }

  async function toggleDiffSide(staged: boolean) {
    const file = selectedFile();
    if (!file) return;
    await selectFile(file, staged);
  }

  async function mutateGit(operation: () => Promise<GitStatus>) {
    setGitBusy(true);
    setGitError("");
    try {
      const next = await operation();
      setStatus(next);
      const file = next.files.find((item) => item.path === selectedPath());
      if (file) await selectFile(file, file.staged && !file.modified);
      else {
        setSelectedPath("");
        setDiff(undefined);
      }
    } catch (error) {
      setGitError(errorText(error));
    } finally {
      setGitBusy(false);
    }
  }

  async function commit() {
    const message = commitMessage().trim();
    if (!message) return;
    await mutateGit(() => commitGitChanges(message));
    if (!gitError()) setCommitMessage("");
  }

  async function createBranch() {
    const name = branchName().trim();
    if (!name) return;
    await mutateGit(() => createGitBranch(name));
    if (!gitError()) setBranchName("");
  }

  async function ensureTerminal() {
    if (terminalBusy() || !props.shellAccess) return;
    setTerminalBusy(true);
    setTerminalError("");
    try {
      const next = await startTerminalSession();
      setTerminal(next);
      setTerminalOutputBase(0);
    } catch (error) {
      setTerminalError(errorText(error));
    } finally {
      setTerminalBusy(false);
    }
  }

  async function submitTerminalCommand() {
    const current = terminal();
    const command = terminalCommand().trim();
    if (!current?.running || !command) return;
    setTerminalBusy(true);
    setTerminalError("");
    try {
      await sendTerminalCommand(current.id, command);
      setTerminalCommand("");
      window.setTimeout(async () => {
        try {
          setTerminal(await getTerminalSession(current.id));
        } catch {
          // The regular poll reports persistent failures.
        }
      }, 80);
    } catch (error) {
      setTerminalError(errorText(error));
    } finally {
      setTerminalBusy(false);
    }
  }

  async function stopTerminal() {
    const current = terminal();
    if (!current) return;
    setTerminalBusy(true);
    try {
      await stopTerminalSession(current.id);
      setTerminal(await getTerminalSession(current.id));
    } catch (error) {
      setTerminalError(errorText(error));
    } finally {
      setTerminalBusy(false);
    }
  }

  return (
    <Show when={props.open}>
      <div class="workspace-tools-dock" style={{ height: `${panelHeight()}px` }}>
        <div
          class="workspace-tools-resizer"
          role="separator"
          aria-label="调整终端与 Git 面板高度"
          aria-orientation="horizontal"
          aria-valuemin={minWorkspaceToolsHeight}
          aria-valuemax={maxWorkspaceToolsHeight}
          aria-valuenow={Math.round(panelHeight())}
          tabIndex={0}
          title="拖拽调整面板高度，双击恢复默认"
          onPointerDown={startPanelResize}
          onDblClick={() => setAndPersistPanelHeight(defaultWorkspaceToolsHeight)}
          onKeyDown={(event) => {
            if (event.key === "ArrowUp") {
              event.preventDefault();
              setAndPersistPanelHeight(panelHeight() + 16);
            }
            if (event.key === "ArrowDown") {
              event.preventDefault();
              setAndPersistPanelHeight(panelHeight() - 16);
            }
            if (event.key === "Home") {
              event.preventDefault();
              setAndPersistPanelHeight(minWorkspaceToolsHeight);
            }
            if (event.key === "End") {
              event.preventDefault();
              setAndPersistPanelHeight(maxWorkspaceToolsHeight);
            }
          }}
        />
        <section class="workspace-tools-panel" aria-label="工作区工具">
        <header class="workspace-tools-head">
          <div class="workspace-tools-tabs" role="tablist">
            <button classList={{ active: activeTab() === "git" }} type="button" onClick={() => setActiveTab("git")}>
              <GitBranch size={14} />
              Git
              <Show when={(status()?.files.length ?? 0) > 0}><span>{status()?.files.length}</span></Show>
            </button>
            <button classList={{ active: activeTab() === "terminal" }} type="button" onClick={() => setActiveTab("terminal")}>
              <TerminalSquare size={14} />
              终端
              <Show when={terminal()?.running}><i /></Show>
            </button>
          </div>
          <div class="workspace-tools-actions">
            <Show when={activeTab() === "git"}>
              <button type="button" title="刷新 Git 状态" disabled={gitBusy()} onClick={() => void refreshGit()}>
                <RefreshCw size={14} classList={{ spinning: gitBusy() }} />
              </button>
            </Show>
            <button type="button" title="关闭工作区工具" onClick={props.onClose}><X size={14} /></button>
          </div>
        </header>

        <Show when={activeTab() === "git"} fallback={
          <div class="terminal-panel">
            <div class="terminal-toolbar">
              <div>
                <TerminalSquare size={14} />
                <strong>{terminal()?.shell || "终端"}</strong>
                <span>
                  {terminal()?.running ? "运行中" : terminal() ? `已退出 ${terminal()?.exitCode}` : "未启动"}
                  {terminal()?.sandboxed ? terminal()?.privilegeRestricted ? " · 受限 Job" : " · Job 隔离" : ""}
                </span>
              </div>
              <div>
                <button type="button" title="清空显示" disabled={!terminal()} onClick={() => setTerminalOutputBase(terminal()?.output.length ?? 0)}><Trash2 size={13} /></button>
                <Show when={terminal()?.running} fallback={<button type="button" title="启动终端" disabled={terminalBusy() || !props.shellAccess} onClick={() => void ensureTerminal()}><Play size={13} /></button>}>
                  <button type="button" title="停止终端" disabled={terminalBusy()} onClick={() => void stopTerminal()}><Square size={12} fill="currentColor" /></button>
                </Show>
              </div>
            </div>
            <Show when={props.shellAccess} fallback={<div class="workspace-tools-empty">Shell 已关闭</div>}>
              <pre class="terminal-output" ref={terminalOutputRef}>{visibleTerminalOutput() || (terminalBusy() ? "正在启动..." : "")}</pre>
              <form class="terminal-input-row" onSubmit={(event) => { event.preventDefault(); void submitTerminalCommand(); }}>
                <span>&gt;</span>
                <input
                  value={terminalCommand()}
                  disabled={!terminal()?.running || terminalBusy()}
                  spellcheck={false}
                  onInput={(event) => setTerminalCommand(event.currentTarget.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" && !event.isComposing) {
                      event.preventDefault();
                      void submitTerminalCommand();
                    }
                  }}
                />
                <button type="submit" title="运行命令" disabled={!terminal()?.running || !terminalCommand().trim() || terminalBusy()}><Play size={13} /></button>
              </form>
            </Show>
            <Show when={terminalError()}><div class="workspace-tools-error">{terminalError()}</div></Show>
          </div>
        }>
          <div class="git-panel">
            <Show when={status()?.available} fallback={
              <div class="workspace-tools-empty workspace-tools-git-empty">
                <GitBranch size={20} />
                <strong>这个文件夹没有启用版本记录</strong>
                <span>不影响文件查看、编辑或运行项目；只有提交历史和版本差异功能不可用。</span>
              </div>
            }>
              <div class="git-toolbar">
                <div class="git-branch-control">
                  <GitBranch size={14} />
                  <select value={status()?.branch || ""} disabled={gitBusy() || props.readOnly} onChange={(event) => void mutateGit(() => switchGitBranch(event.currentTarget.value))}>
                    <For each={status()?.branches}>{(branch) => <option value={branch.name}>{branch.name}</option>}</For>
                  </select>
                  <span>{status()?.ahead ? `↑${status()?.ahead}` : ""}{status()?.behind ? ` ↓${status()?.behind}` : ""}</span>
                </div>
                <form class="git-new-branch" onSubmit={(event) => { event.preventDefault(); void createBranch(); }}>
                  <input value={branchName()} disabled={gitBusy() || props.readOnly} placeholder="新分支" spellcheck={false} onInput={(event) => setBranchName(event.currentTarget.value)} />
                  <button type="submit" title="创建分支" disabled={gitBusy() || props.readOnly || !branchName().trim()}><Plus size={13} /></button>
                </form>
                <div class="git-counts">
                  <span class="staged">{status()?.stagedCount ?? 0} 暂存</span>
                  <span>{status()?.modifiedCount ?? 0} 修改</span>
                  <span>{status()?.untrackedCount ?? 0} 新增</span>
                </div>
              </div>

              <div class="git-workspace">
                <div class="git-file-list">
                  <div class="git-file-list-head">
                    <strong>更改</strong>
                    <div>
                      <button type="button" title="全部取消暂存" disabled={gitBusy() || props.readOnly || !(status()?.stagedCount)} onClick={() => void mutateGit(() => unstageGitPaths([]))}><RotateCcw size={12} /></button>
                      <button type="button" title="全部暂存" disabled={gitBusy() || props.readOnly || status()?.clean} onClick={() => void mutateGit(() => stageGitPaths([]))}><Plus size={13} /></button>
                    </div>
                  </div>
                  <For each={status()?.files} fallback={<div class="git-clean"><Check size={14} />工作区干净</div>}>
                    {(file) => (
                      <div class="git-file-row" classList={{ active: selectedPath() === file.path, conflict: file.conflicted }}>
                        <button class="git-file-select" type="button" title={file.path} onClick={() => void selectFile(file, file.staged && !file.modified)}>
                          <span class="git-status-code">{file.untracked ? "U" : file.conflicted ? "!" : `${file.indexStatus === "." ? "" : file.indexStatus}${file.worktreeStatus === "." ? "" : file.worktreeStatus}`}</span>
                          <span>{file.path}</span>
                        </button>
                        <Show when={!props.readOnly}>
                          <button
                            class="git-file-action"
                            type="button"
                            title={file.staged && !file.modified ? "取消暂存" : "暂存"}
                            onClick={(event) => {
                              event.stopPropagation();
                              void mutateGit(() => file.staged && !file.modified ? unstageGitPaths([file.path]) : stageGitPaths([file.path]));
                            }}
                          >
                            <Show when={file.staged && !file.modified} fallback={<Plus size={12} />}><Minus size={12} /></Show>
                          </button>
                        </Show>
                      </div>
                    )}
                  </For>
                </div>

                <div class="git-diff-pane">
                  <div class="git-diff-head">
                    <strong title={selectedPath()}>{selectedPath() || "Diff"}</strong>
                    <Show when={selectedFile()?.staged && selectedFile()?.modified}>
                      <div>
                        <button type="button" classList={{ active: !diffStaged() }} onClick={() => void toggleDiffSide(false)}>工作区</button>
                        <button type="button" classList={{ active: diffStaged() }} onClick={() => void toggleDiffSide(true)}>暂存区</button>
                      </div>
                    </Show>
                  </div>
                  <pre>{diff()?.patch || (selectedPath() ? "没有可显示的差异" : "选择文件查看差异")}</pre>
                  <Show when={diff()?.truncated}><span class="git-diff-truncated">Diff 已截断</span></Show>
                </div>
              </div>

              <form class="git-commit-row" onSubmit={(event) => { event.preventDefault(); void commit(); }}>
                <GitCommitHorizontal size={14} />
                <input value={commitMessage()} disabled={gitBusy() || props.readOnly} placeholder="提交信息" onInput={(event) => setCommitMessage(event.currentTarget.value)} />
                <button type="submit" disabled={gitBusy() || props.readOnly || !(status()?.stagedCount) || !commitMessage().trim()}>提交</button>
              </form>
            </Show>
            <Show when={gitError()}><div class="workspace-tools-error">{gitError()}</div></Show>
          </div>
        </Show>
        </section>
      </div>
    </Show>
  );
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
