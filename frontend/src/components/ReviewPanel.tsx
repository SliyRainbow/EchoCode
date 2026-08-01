import {
  Check,
  ChevronDown,
  ChevronRight,
  ExternalLink,
  FileCode2,
  Folder,
  FolderOpen,
  GitBranch,
  ListFilter,
  Pilcrow,
  RefreshCw,
  Search,
  WrapText,
  X,
} from "lucide-solid";
import { For, Match, Show, Suspense, Switch, createEffect, createMemo, createSignal, lazy, untrack } from "solid-js";
import {
  getGitReviewDiff,
  getGitStatus,
  listWorkspaceDirectory,
  openWorkspaceFile,
  readWorkspaceFile,
  revealWorkspaceFile,
} from "../services/workbench";
import { countDiffChanges, decorateWordDifferences, parseUnifiedDiff } from "../lib/review-diff";
import type {
  GitDiff,
  GitFileStatus,
  GitStatus,
  WorkspaceDirectoryEntry,
  WorkspaceDirectoryListing,
  WorkspaceFilePreview,
  WorkspaceFileRequest,
  WorkspaceFileView,
} from "../types";
import { ArtifactViewer } from "./ArtifactViewer";

const CodeViewer = lazy(async () => {
  const module = await import("./CodeViewer");
  return { default: module.CodeViewer };
});

type ReviewPanelProps = {
  open: boolean;
  workspaceRoot: string;
  request?: WorkspaceFileRequest;
  dark: boolean;
  onClose: () => void;
};

type ReviewRailMode = "project" | "changes";

type WorkspaceTreeRow = {
  entry: WorkspaceDirectoryEntry;
  depth: number;
};

export function ReviewPanel(props: ReviewPanelProps) {
  const [status, setStatus] = createSignal<GitStatus>();
  const [diff, setDiff] = createSignal<GitDiff>();
  const [preview, setPreview] = createSignal<WorkspaceFilePreview>();
  const [selectedPath, setSelectedPath] = createSignal("");
  const [viewMode, setViewMode] = createSignal<WorkspaceFileView>("file");
  const [requestedLine, setRequestedLine] = createSignal<number>();
  const [stagedDiff, setStagedDiff] = createSignal(false);
  const [filter, setFilter] = createSignal("");
  const [railMode, setRailMode] = createSignal<ReviewRailMode>("project");
  const [directoryListings, setDirectoryListings] = createSignal<Record<string, WorkspaceDirectoryListing>>({});
  const [expandedDirectories, setExpandedDirectories] = createSignal<Set<string>>(new Set([""]));
  const [loadingDirectories, setLoadingDirectories] = createSignal<Set<string>>(new Set());
  const [treeError, setTreeError] = createSignal("");
  const [ignoreWhitespace, setIgnoreWhitespace] = createSignal(false);
  const [wordDiff, setWordDiff] = createSignal(true);
  const [wrapLines, setWrapLines] = createSignal(false);
  const [busy, setBusy] = createSignal(false);
  const [error, setError] = createSignal("");
  const [previewError, setPreviewError] = createSignal("");
  const [diffError, setDiffError] = createSignal("");
  let activeWorkspace = "";
  let fileRequest = 0;

  const selectedFile = createMemo(() => findGitFile(status(), selectedPath()));
  const filteredFiles = createMemo(() => {
    const query = filter().trim().toLowerCase();
    const files = status()?.files ?? [];
    return query ? files.filter((file) => file.path.toLowerCase().includes(query)) : files;
  });
  const parsedDiff = createMemo(() => {
    const rows = parseUnifiedDiff(diff()?.patch ?? "");
    return wordDiff() ? decorateWordDifferences(rows) : rows;
  });
  const diffStats = createMemo(() => countDiffChanges(diff()?.patch ?? ""));
  const projectRows = createMemo(() => flattenWorkspaceRows(directoryListings(), expandedDirectories()));

  createEffect(() => {
    const open = props.open;
    const workspaceRoot = props.workspaceRoot;
    const request = props.request;
    if (!open) return;
    const previousPath = untrack(selectedPath);
    void refresh(workspaceRoot, request, previousPath);
  });

  async function refresh(workspaceRoot: string, request?: WorkspaceFileRequest, previousPath = "") {
    if (!workspaceRoot.trim()) {
      setStatus(undefined);
      setPreview(undefined);
      setDiff(undefined);
      setSelectedPath("");
      setDirectoryListings({});
      setExpandedDirectories(new Set([""]));
      setLoadingDirectories(new Set<string>());
      return;
    }
    if (activeWorkspace !== workspaceRoot) {
      activeWorkspace = workspaceRoot;
      previousPath = "";
      setSelectedPath("");
      setPreview(undefined);
      setDiff(undefined);
      setFilter("");
      setRailMode("project");
      setDirectoryListings({});
      setExpandedDirectories(new Set([""]));
      setLoadingDirectories(new Set<string>());
    }
    setBusy(true);
    setError("");
    setTreeError("");
    let nextStatus: GitStatus | undefined;
    const [statusResult, directoryResult] = await Promise.allSettled([
      getGitStatus(),
      listWorkspaceDirectory(""),
    ]);
    if (statusResult.status === "fulfilled") {
      nextStatus = statusResult.value;
      setStatus(statusResult.value);
    } else {
      setStatus(undefined);
    }
    if (directoryResult.status === "fulfilled") {
      setDirectoryListings((current) => ({ ...current, "": directoryResult.value }));
    } else {
      setTreeError(errorText(directoryResult.reason));
    }
    if (request?.view === "changes" && (nextStatus?.files.length ?? 0) > 0) {
      setRailMode("changes");
    }
    const path = request?.path.trim() || previousPath || nextStatus?.files[0]?.path || "";
    if (!path) {
      setSelectedPath("");
      setPreview(undefined);
      setDiff(undefined);
      setBusy(false);
      return;
    }
    await loadFile(path, request?.view ?? "file", nextStatus, request?.line, false);
    setBusy(false);
  }

  async function toggleDirectory(entry: WorkspaceDirectoryEntry) {
    if (!entry.isDirectory || entry.isSymlink) return;
    const expanded = expandedDirectories();
    if (expanded.has(entry.path)) {
      const next = new Set(expanded);
      next.delete(entry.path);
      setExpandedDirectories(next);
      return;
    }
    const next = new Set(expanded);
    next.add(entry.path);
    setExpandedDirectories(next);
    if (directoryListings()[entry.path]) return;

    const loading = new Set(loadingDirectories());
    loading.add(entry.path);
    setLoadingDirectories(loading);
    setTreeError("");
    try {
      const listing = await listWorkspaceDirectory(entry.path);
      setDirectoryListings((current) => ({ ...current, [entry.path]: listing }));
    } catch (cause) {
      const collapsed = new Set(expandedDirectories());
      collapsed.delete(entry.path);
      setExpandedDirectories(collapsed);
      setTreeError(errorText(cause));
    } finally {
      const finished = new Set(loadingDirectories());
      finished.delete(entry.path);
      setLoadingDirectories(finished);
    }
  }

  function openProjectEntry(entry: WorkspaceDirectoryEntry) {
    if (entry.isDirectory) {
      void toggleDirectory(entry);
      return;
    }
    if (!entry.isSymlink) {
      void loadFile(entry.path, "file", status());
    }
  }

  async function loadFile(
    path: string,
    preferredView: WorkspaceFileView,
    sourceStatus = status(),
    line?: number,
    manageBusy = true,
  ) {
    const target = path.trim();
    if (!target) return;
    const request = ++fileRequest;
    const gitFile = findGitFile(sourceStatus, target);
    const staged = gitFile ? preferredDiffSide(gitFile) : false;
    setSelectedPath(target);
    setRequestedLine(line);
    setStagedDiff(staged);
    setPreviewError("");
    setDiffError("");
    setError("");
    if (manageBusy) setBusy(true);

    const previewPromise = readWorkspaceFile(target);
    const diffPromise = gitFile
      ? getGitReviewDiff(gitFile.path, staged, ignoreWhitespace())
      : Promise.resolve<GitDiff | undefined>(undefined);
    const [previewResult, diffResult] = await Promise.allSettled([previewPromise, diffPromise]);
    if (request !== fileRequest) return;

    if (previewResult.status === "fulfilled") {
      setPreview(previewResult.value);
    } else {
      setPreview(undefined);
      setPreviewError(errorText(previewResult.reason));
    }
    if (diffResult.status === "fulfilled") {
      setDiff(diffResult.value);
    } else {
      setDiff(undefined);
      setDiffError(errorText(diffResult.reason));
    }

    const canShowChanges = Boolean(gitFile && diffResult.status === "fulfilled");
    const canShowFile = previewResult.status === "fulfilled";
    if (preferredView === "changes" && canShowChanges) {
      setViewMode("changes");
    } else if (canShowFile) {
      setViewMode("file");
    } else if (canShowChanges) {
      setViewMode("changes");
    } else {
      setViewMode("file");
    }
    if (manageBusy) setBusy(false);
  }

  async function reloadDiff(whitespace: boolean) {
    const file = selectedFile();
    if (!file) return;
    setBusy(true);
    setDiffError("");
    try {
      setDiff(await getGitReviewDiff(file.path, stagedDiff(), whitespace));
    } catch (cause) {
      setDiff(undefined);
      setDiffError(errorText(cause));
    } finally {
      setBusy(false);
    }
  }

  async function toggleWhitespace() {
    const next = !ignoreWhitespace();
    setIgnoreWhitespace(next);
    await reloadDiff(next);
  }

  async function runFileAction(action: "open" | "reveal") {
    const path = selectedPath();
    if (!path) return;
    setError("");
    try {
      if (action === "open") await openWorkspaceFile(path);
      else await revealWorkspaceFile(path);
    } catch (cause) {
      setError(errorText(cause));
    }
  }

  return (
    <Show when={props.open}>
      <aside class="review-panel" aria-label="文件查看">
        <header class="review-head">
          <div class="review-title">
            <FileCode2 size={16} />
            <strong>文件</strong>
            <Show when={status()?.branch}><span>{status()?.branch}</span></Show>
            <Show when={viewMode() === "changes" && selectedPath()}>
              <b class="review-additions">+{diffStats().additions}</b>
              <b class="review-deletions">-{diffStats().deletions}</b>
            </Show>
          </div>
          <div class="review-actions">
            <Show when={viewMode() === "changes" && selectedFile()}>
              <button
                type="button"
                classList={{ active: ignoreWhitespace() }}
                title="隐藏仅空白字符差异"
                aria-pressed={ignoreWhitespace()}
                disabled={busy()}
                onClick={() => void toggleWhitespace()}
              >
                <Pilcrow size={14} />
              </button>
              <button type="button" classList={{ active: wordDiff() }} title="突出词内变化" aria-pressed={wordDiff()} onClick={() => setWordDiff((value) => !value)}>
                <ListFilter size={14} />
              </button>
            </Show>
            <button type="button" classList={{ active: wrapLines() }} title="自动换行" aria-pressed={wrapLines()} onClick={() => setWrapLines((value) => !value)}>
              <WrapText size={14} />
            </button>
            <button type="button" title="刷新文件" disabled={busy()} onClick={() => void refresh(props.workspaceRoot, props.request, selectedPath())}>
              <RefreshCw size={14} classList={{ spinning: busy() }} />
            </button>
            <button type="button" title="关闭文件面板" aria-label="关闭文件面板" onClick={props.onClose}><X size={15} /></button>
          </div>
        </header>

        <Show when={props.workspaceRoot.trim()} fallback={<div class="review-empty">请先选择项目工作区</div>}>
          <div class="review-layout">
            <section class="review-diff-surface">
              <div class="review-diff-toolbar">
                <div class="review-file-heading">
                  <strong title={selectedPath()}>{selectedPath() || "文件预览"}</strong>
                  <Show when={preview()?.truncated}><span>预览已截断</span></Show>
                  <Show when={preview() && !preview()?.binary && !preview()?.tooLarge}>
                    <small>{previewMeta(preview()!)}</small>
                  </Show>
                </div>
                <div class="review-diff-controls">
                  <Show when={selectedPath()}>
                    <div class="review-view-tabs" role="tablist" aria-label="文件视图">
                      <button type="button" role="tab" classList={{ active: viewMode() === "file" }} aria-selected={viewMode() === "file"} onClick={() => setViewMode("file")}>文件</button>
                      <Show when={selectedFile()}>
                        <button type="button" role="tab" classList={{ active: viewMode() === "changes" }} aria-selected={viewMode() === "changes"} onClick={() => setViewMode("changes")}>更改</button>
                      </Show>
                    </div>
                    <button type="button" class="review-open-action" title="使用系统应用打开" aria-label="使用系统应用打开" onClick={() => void runFileAction("open")}><ExternalLink size={13} /></button>
                    <button type="button" class="review-open-action" title="在文件夹中显示" aria-label="在文件夹中显示" onClick={() => void runFileAction("reveal")}><FolderOpen size={13} /></button>
                  </Show>
                </div>
              </div>

              <Show when={!busy() || preview() || diff()} fallback={<div class="review-preview-empty"><RefreshCw class="spinning" size={18} /><span>正在读取文件…</span></div>}>
                <Switch fallback={
                  <div class="review-preview-empty">
                    <FileCode2 size={22} />
                    <strong>选择项目文件</strong>
                    <span>文件会在这里打开，不需要 Git 仓库。</span>
                  </div>
                }>
                  <Match when={selectedPath() && viewMode() === "changes"}>
                    <div class="review-diff-scroll" classList={{ wrap: wrapLines() }}>
                      <For each={parsedDiff()} fallback={<div class="review-diff-empty">{diffError() || "这个文件目前没有可显示的更改"}</div>}>
                        {(line) => (
                          <div class={`review-diff-line ${line.kind}`}>
                            <span class="review-line-number">{line.oldLine ?? ""}</span>
                            <span class="review-line-number">{line.newLine ?? ""}</span>
                            <span class="review-line-marker">{line.marker}</span>
                            <code>
                              <For each={line.segments ?? [{ text: line.text, changed: false }]}>
                                {(segment) => <span classList={{ changed: segment.changed }}>{segment.text}</span>}
                              </For>
                            </code>
                          </div>
                        )}
                      </For>
                    </div>
                  </Match>
                  <Match when={preview()?.artifact}>
                    {(artifact) => <ArtifactViewer artifact={artifact()} />}
                  </Match>
                  <Match when={preview()?.binary}>
                    <PreviewUnavailable title="这是二进制文件" detail="文本查看器无法安全显示它，请使用系统应用打开。" onOpen={() => void runFileAction("open")} onReveal={() => void runFileAction("reveal")} />
                  </Match>
                  <Match when={preview()?.tooLarge}>
                    <PreviewUnavailable title="文件过大，未加载文本预览" detail={`文件大小 ${formatBytes(preview()?.size ?? 0)}，请使用系统应用打开。`} onOpen={() => void runFileAction("open")} onReveal={() => void runFileAction("reveal")} />
                  </Match>
                  <Match when={preview()}>
                    <Suspense fallback={<div class="review-preview-empty"><RefreshCw class="spinning" size={18} /><span>正在加载代码查看器...</span></div>}>
                      <CodeViewer
                        content={preview()?.content ?? ""}
                        path={preview()?.path ?? selectedPath()}
                        line={requestedLine()}
                        wrap={wrapLines()}
                        dark={props.dark}
                      />
                    </Suspense>
                  </Match>
                  <Match when={previewError()}>
                    <div class="review-preview-empty"><FileCode2 size={22} /><strong>无法预览文件</strong><span>{previewError()}</span></div>
                  </Match>
                </Switch>
              </Show>
            </section>

            <aside class="review-file-rail">
              <div class="review-rail-tabs" role="tablist" aria-label="项目文件与 Git 更改">
                <button
                  type="button"
                  role="tab"
                  classList={{ active: railMode() === "project" }}
                  aria-selected={railMode() === "project"}
                  onClick={() => setRailMode("project")}
                >
                  <Folder size={13} />项目
                </button>
                <button
                  type="button"
                  role="tab"
                  classList={{ active: railMode() === "changes" }}
                  aria-selected={railMode() === "changes"}
                  onClick={() => setRailMode("changes")}
                >
                  <GitBranch size={13} />更改
                  <Show when={(status()?.files.length ?? 0) > 0}><span>{status()?.files.length}</span></Show>
                </button>
              </div>

              <Show when={railMode() === "project"} fallback={
                <div class="review-rail-content changes">
                  <div class="review-file-summary">
                    <div><strong>Git 更改</strong><span>{status()?.files.length ?? 0}</span></div>
                  </div>
                  <label class="review-file-filter">
                    <Search size={13} />
                    <input value={filter()} placeholder="筛选更改" spellcheck={false} onInput={(event) => setFilter(event.currentTarget.value)} />
                  </label>
                  <div class="review-file-list">
                    <For each={filteredFiles()} fallback={
                      <div class="review-clean"><Check size={15} />{status()?.available ? "工作区没有文件更改" : "此项目没有可显示的 Git 更改"}</div>
                    }>
                      {(file) => (
                        <div class="review-file-row" classList={{ active: pathsEqual(file.path, selectedPath()), conflict: file.conflicted }}>
                          <button type="button" class="review-file-select" title={file.path} onClick={() => void loadFile(file.path, "changes", status())}>
                            <span class={`review-file-code ${file.untracked ? "untracked" : file.conflicted ? "conflict" : file.staged ? "staged" : "modified"}`}>
                              {file.untracked ? "N" : file.conflicted ? "!" : file.staged && !file.modified ? "A" : "M"}
                            </span>
                            <span><strong>{fileName(file.path)}</strong><small>{fileDirectory(file.path)}</small></span>
                          </button>
                        </div>
                      )}
                    </For>
                  </div>
                </div>
              }>
                <div class="review-rail-content project">
                  <div class="review-file-summary">
                    <div><strong title={props.workspaceRoot}>{workspaceName(props.workspaceRoot)}</strong><span>{projectRows().length}</span></div>
                  </div>
                  <div class="review-file-list review-project-tree" role="tree" aria-label="项目文件">
                  <For each={projectRows()} fallback={
                    <div class="review-clean">
                      <Show when={!treeError()} fallback={<span>{treeError()}</span>}>
                        <Folder size={15} />项目目录为空
                      </Show>
                    </div>
                  }>
                    {(row) => {
                      const expanded = () => expandedDirectories().has(row.entry.path);
                      const loading = () => loadingDirectories().has(row.entry.path);
                      return (
                        <button
                          type="button"
                          class="review-tree-row"
                          classList={{ active: pathsEqual(row.entry.path, selectedPath()), directory: row.entry.isDirectory, symlink: row.entry.isSymlink }}
                          style={`--tree-indent:${row.depth * 14}px`}
                          title={row.entry.isSymlink ? `${row.entry.path}（符号链接不在项目树中展开）` : row.entry.path}
                          role="treeitem"
                          aria-expanded={row.entry.isDirectory ? expanded() : undefined}
                          onClick={() => openProjectEntry(row.entry)}
                        >
                          <span class="review-tree-chevron">
                            <Show when={loading()} fallback={row.entry.isDirectory ? (expanded() ? <ChevronDown size={13} /> : <ChevronRight size={13} />) : null}>
                              <RefreshCw class="spinning" size={12} />
                            </Show>
                          </span>
                          <Show when={row.entry.isDirectory} fallback={<FileCode2 size={14} />}>
                            {expanded() ? <FolderOpen size={14} /> : <Folder size={14} />}
                          </Show>
                          <span>{row.entry.name}</span>
                        </button>
                      );
                    }}
                  </For>
                  <Show when={directoryListings()[""]?.truncated}>
                    <div class="review-tree-note">根目录项目过多，仅显示前 1000 项</div>
                  </Show>
                  <Show when={treeError() && projectRows().length > 0}>
                    <div class="review-tree-note error">{treeError()}</div>
                  </Show>
                  </div>
                </div>
              </Show>
            </aside>
          </div>
        </Show>
        <Show when={error()}><div class="review-error" role="alert">{error()}</div></Show>
      </aside>
    </Show>
  );
}

function PreviewUnavailable(props: {
  title: string;
  detail: string;
  onOpen: () => void;
  onReveal: () => void;
}) {
  return (
    <div class="review-preview-empty">
      <FileCode2 size={24} />
      <strong>{props.title}</strong>
      <span>{props.detail}</span>
      <div class="review-preview-actions">
        <button type="button" onClick={props.onOpen}><ExternalLink size={14} />系统打开</button>
        <button type="button" onClick={props.onReveal}><FolderOpen size={14} />在文件夹中显示</button>
      </div>
    </div>
  );
}

function findGitFile(status: GitStatus | undefined, path: string): GitFileStatus | undefined {
  return status?.files.find((file) => pathsEqual(file.path, path));
}

function pathsEqual(left: string, right: string): boolean {
  const normalizedLeft = left.replaceAll("\\", "/").toLowerCase();
  const normalizedRight = right.replaceAll("\\", "/").toLowerCase();
  return normalizedLeft === normalizedRight
    || normalizedLeft.endsWith(`/${normalizedRight}`)
    || normalizedRight.endsWith(`/${normalizedLeft}`);
}

function preferredDiffSide(file: GitFileStatus): boolean {
  return file.staged && !file.modified && !file.untracked;
}

function previewMeta(preview: WorkspaceFilePreview): string {
  const lineEnding = preview.lineEnding === "crlf" ? "CRLF" : "LF";
  return `${preview.lineCount} 行 · ${preview.encoding.toUpperCase()} · ${lineEnding} · ${formatBytes(preview.size)}`;
}

function formatBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}

function fileName(path: string): string {
  const parts = path.replace(/\\/g, "/").split("/");
  return parts.pop() || path;
}

function fileDirectory(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  const index = normalized.lastIndexOf("/");
  return index >= 0 ? normalized.slice(0, index) : "项目根目录";
}

function flattenWorkspaceRows(
  listings: Record<string, WorkspaceDirectoryListing>,
  expanded: Set<string>,
): WorkspaceTreeRow[] {
  const rows: WorkspaceTreeRow[] = [];
  const append = (directory: string, depth: number) => {
    if (depth > 64) return;
    for (const entry of listings[directory]?.entries ?? []) {
      rows.push({ entry, depth });
      if (entry.isDirectory && expanded.has(entry.path)) {
        append(entry.path, depth + 1);
      }
    }
  };
  append("", 0);
  return rows;
}

function workspaceName(path: string): string {
  const normalized = path.replace(/[\\/]+$/, "").replace(/\\/g, "/");
  return normalized.split("/").pop() || "项目文件";
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
