import {
  AlertTriangle,
  Archive,
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  BarChart3,
  Bot,
  Braces,
  Check,
  CheckCircle2,
  ChevronDown,
  ChevronRight,
  ClipboardList,
  Clock3,
  Command,
  Copy,
  Cpu,
  Database,
  FileText,
  Folder,
  FolderOpen,
  Gauge,
  GitBranch,
  GitFork,
  Globe2,
  HardDrive,
  History,
  ImagePlus,
  Keyboard,
  KeyRound,
  LayoutList,
  ListPlus,
  ListFilter,
  ListCollapse,
  LockKeyhole,
  MessageSquarePlus,
  Monitor,
  Moon,
  Network,
  Palette,
  Pencil,
  Pin,
  Plug,
  Plus,
  RefreshCw,
  Save,
  Search,
  Settings,
  ShieldCheck,
  SlidersHorizontal,
  Sparkles,
  Square,
  Sun,
  Terminal,
  Trash2,
  User,
  Users,
  Wrench,
  X,
  Zap,
  ExternalLink,
  Ellipsis,
  Undo2,
  Redo2,
} from "lucide-solid";
import { For, Match, Show, Switch, createEffect, createMemo, createSignal, onCleanup, onMount } from "solid-js";
import type { JSX } from "solid-js";
import { Portal } from "solid-js/web";
import { AppTitleBar } from "./components/AppTitleBar";
import { ReasoningMenu } from "./components/ReasoningMenu";
import { SidePanelHost } from "./components/SidePanelHost";
import type { SidePanelView } from "./components/SidePanelHost";
import { MessageContent, SubagentDock, TaskProgress, TeamRun, textToParts } from "./components/chat/MessageContent";
import type { SubagentPart, TaskProgressPart, TeamPart } from "./components/chat/MessageContent";
import { TimelinePanel } from "./components/TimelinePanel";
import { ApprovalModal } from "./components/ApprovalModal";
import { ConfirmDialog } from "./components/ConfirmDialog";
import type { ConfirmationRequest, ConfirmationResult } from "./components/ConfirmDialog";
import { ImagePreviewModal } from "./components/ImagePreviewModal";
import { WorkspaceToolsPanel } from "./components/WorkspaceToolsPanel";
import {
  clearDeepSeekAPIKey,
  getWorkbenchState,
  resetDeepSeekSession,
  refreshModelProviderModels,
  refreshMCPServer,
	refreshPlugins,
	selectPluginDirectory,
	installPlugin,
	uninstallPlugin,
	revealPlugin,
  saveDeepSeekAPIKey,
  saveModelProviderAPIKey,
  saveModelProviderBillingAPIKey,
  startChatMessageForSession,
	resumePausedTeamTask,
	abandonPausedTeamTask,
  getActiveChatTasks,
  guideChatMessage,
  stopChatMessage,
  stopSubagent,
  onChatTaskEvent,
  onMCPState,
  setReasoningLevel,
  saveRuntimeSettings,
  testDeepSeekConnection,
  clearModelProviderAPIKey,
  clearModelProviderBillingAPIKey,
  getUsageBillingReport,
  syncUsageBilling,
  reconcileUsageBilling,
  deleteModelProvider,
  listCheckpoints,
  rewindToCheckpoint,
  listBranches,
  switchBranch,
  forkFromMessage,
  onApprovalRequest,
  respondApproval,
  setPlanMode,
  listProjects,
  listSessions,
  getProjectTree,
  createProject,
  switchProject,
  setProjectPinned,
  renameProject,
  archiveProjectTasks,
  removeProject,
  openProjectInFileManager,
  createPermanentWorktree,
  newSession,
  switchSession,
  renameSession,
  archiveSession,
  deleteSession,
  selectDirectory,
  selectWorktreeParentDirectory,
  getSessionMessages,
  getSessionMessagesForSession,
  onBrowserPreviewOpen,
  onBrowserPreviewClose,
  openWorkspaceFile,
  previewWorkspaceFile,
  openBrowserURL,
  openURLInSystemBrowser,
	revealSecretResult,
  importSkillMarkdown,
  getUpdateState,
  onUpdateState,
} from "./services/workbench";
import { defaultReasoningLevel, reasoningOptions as fallbackReasoningOptions } from "./state/reasoning";
import type {
  ApprovalRequest,
  BranchInfo,
  ChatAttachment,
  BrowserPreview,
  ChatTaskEvent,
  CheckpointInfo,
  ProjectInfo,
  ProjectNode,
  SessionInfo,
  DeepSeekSessionState,
  MCPServerSetting,
  MessagePart,
  ModelProviderSetting,
  ReasoningLevel,
  RuntimeSettings,
  SkillImportResult,
  UpdateState,
  UsageBillingReconciliationInput,
  UsageBillingReport,
  UsageBillingSyncInput,
  UsageMetrics,
  WorkbenchState,
  WorkspaceFileRequest,
  WorkspaceFileView,
} from "./types";

const fallbackProfile =
  fallbackReasoningOptions.find((option) => option.id === defaultReasoningLevel) ?? fallbackReasoningOptions[0];
import { SettingsCenter, ModelRouteMenu } from "./settings-panels";
import type { ChatMessage, DrawerTab, ViewSnapshot, SidebarSession, SettingsCategory, ThemeMode } from "./ui-types";
import {
  defaultSidebarWidth, minSidebarWidth, maxSidebarWidth, defaultBrowserPanelWidth,
  minBrowserPanelWidth, minChatPaneWidth,
} from "./constants";
import {
  formatPercent, formatInteger, shortHash, categoryForDrawerTab, baseNameFromPath,
  selectedModelProvider, selectedModelName, providerReadyForChat, providerConnectionSummary,
  createChatMessage, emptyUsageMetrics, fallbackDeepSeekState, fallbackDeepSeekSession,
  fallbackCacheHealth, fallbackRuntimeSettings, fallbackConfigFiles, readStoredSidebarWidth,
  persistSidebarWidth, readStoredBrowserPanelWidth, persistBrowserPanelWidth,
  readStoredThemeMode, persistThemeMode, applyThemeMode,
  applySidebarWidth, clamp, messageTitle, formatClock,
} from "./format";
import { formatElapsedDuration } from "./lib/duration";
import { errorMessage } from "./lib/errors";
import { reconcileSessionMessages, rollbackOptimisticTurnState } from "./lib/session-history";
import { clearGuidanceMessages, dequeueMessage, enqueueMessage, prioritizeMessage, removeMessage, takeMessageForEditing } from "./lib/message-queue";
import type { ComposerLink, QueuedComposerMessage } from "./lib/message-queue";
import {
  composerSnapshotsEqual,
  emptyComposerHistory,
  recordComposerSnapshot,
  redoComposerSnapshot,
  undoComposerSnapshot,
} from "./lib/composer-history";
import type { ComposerHistory, ComposerSnapshot } from "./lib/composer-history";
import { hasMeaningfulTurnOutput } from "./lib/chat-results";
import { appendLiveAssistantText, displayMessageParts, liveTaskStatus, settleLiveTimelineParts, updateLiveTimelineParts } from "./lib/timeline";
import { createStableListViews } from "./lib/stable-list";
import { redactSensitiveTextForDisplay } from "./lib/sensitive-text";
import type { UIAppearancePreferences } from "./ui-appearance";
import {
  applyUIAppearance,
  defaultUIAppearance,
  normalizeUIAppearance,
  persistUIAppearance,
  readStoredUIAppearance,
  resolveEffectiveUIScale,
} from "./ui-appearance";

type ProjectMenuState = {
  project: ProjectNode;
  left: number;
  top: number;
};

type ProjectDialogState = {
  kind: "rename" | "worktree" | "archive" | "remove";
  project: ProjectNode;
  name: string;
  branch: string;
  destinationParent: string;
  destinationName: string;
};

type SessionMenuState = {
  project: ProjectNode;
  session: SessionInfo;
  left: number;
  top: number;
};

type SessionRenameDialogState = {
  project: ProjectNode;
  session: SessionInfo;
  title: string;
};

const updateBannerVisibleStatuses = new Set(["available", "downloading", "downloaded", "installing"]);

type ConfirmationState = ConfirmationRequest & {
  resolve: (result: ConfirmationResult) => void;
};

type SessionTaskRuntime = {
  taskID: string;
  projectID: string;
  sessionID: string;
  userMessageID: string;
  messageID: string;
  prompt: string;
  tail: string;
  attachments: ChatAttachment[];
  links: ComposerLink[];
  startedAt: string;
  assistantMessage: ChatMessage;
  progress?: TaskProgressPart;
  optimisticRolledBack?: boolean;
};

type SessionViewState = {
  messages: ChatMessage[];
  disclosures?: Record<string, boolean>;
  composerDraft?: string;
  composerTail?: string;
  composerAttachments?: ChatAttachment[];
  composerLinks?: ComposerLink[];
  activeTaskProgress?: TaskProgressPart;
  browserPreview?: BrowserPreview;
  reviewOpen: boolean;
  sidePanelView: SidePanelView;
  workspaceFileRequest?: WorkspaceFileRequest;
  selectedSubagentTaskID?: string;
};

function sessionIdentityKey(projectID: string, sessionID: string): string {
  return `${projectID.trim()}\u0000${sessionID.trim()}`;
}

function parentDirectory(path: string): string {
  const clean = path.trim().replace(/[\\/]+$/, "");
  const index = Math.max(clean.lastIndexOf("\\"), clean.lastIndexOf("/"));
  if (index < 0) return "";
  if (index === 0) return clean.slice(0, 1);
  if (index === 2 && clean[1] === ":") return clean.slice(0, 3);
  return clean.slice(0, index);
}

function joinNativePath(parent: string, child: string): string {
  const cleanParent = parent.trim().replace(/[\\/]+$/, "");
  if (!cleanParent) return child;
  const separator = cleanParent.includes("\\") ? "\\" : "/";
  return `${cleanParent}${separator}${child}`;
}

function worktreePathSegment(value: string): string {
  const leaf = value.trim().split(/[\\/]/).filter(Boolean).pop() || "worktree";
  return leaf.replace(/[<>:"/\\|?*\x00-\x1f]+/g, "-").replace(/[. ]+$/g, "").slice(0, 64) || "worktree";
}

function App() {
  const storedBrowserPanelWidth = readStoredBrowserPanelWidth();
  const storedUIAppearance = readStoredUIAppearance();
  const [state, setState] = createSignal<WorkbenchState>();
  const [loading, setLoading] = createSignal(true);
  const [updatingReasoning, setUpdatingReasoning] = createSignal(false);
  const [pendingReasoningLevel, setPendingReasoningLevel] = createSignal<ReasoningLevel | undefined>();
  const [savingKey, setSavingKey] = createSignal(false);
  const [testingDeepSeek, setTestingDeepSeek] = createSignal(false);
  const [clearingKey, setClearingKey] = createSignal(false);
  const [savingRuntime, setSavingRuntime] = createSignal(false);
  const [sendingMessage, setSendingMessage] = createSignal(false);
	const [teamLifecycleBusy, setTeamLifecycleBusy] = createSignal(false);
  const [activeChatTaskID, setActiveChatTaskID] = createSignal("");
  const [streamingMessageID, setStreamingMessageID] = createSignal("");
  const [activeTaskProgress, setActiveTaskProgress] = createSignal<TaskProgressPart>();
  const [queuedMessages, setQueuedMessages] = createSignal<QueuedComposerMessage[]>([]);
  const [queuedMessagesBySession, setQueuedMessagesBySession] = createSignal<Record<string, QueuedComposerMessage[]>>({});
  const [resettingSession, setResettingSession] = createSignal(false);
  const [apiKeyDraft, setAPIKeyDraft] = createSignal("");
  const [providerKeyDrafts, setProviderKeyDrafts] = createSignal<Record<string, string>>({});
  const [providerBillingKeyDrafts, setProviderBillingKeyDrafts] = createSignal<Record<string, string>>({});
  const [savingProviderID, setSavingProviderID] = createSignal("");
  const [savingProviderBillingID, setSavingProviderBillingID] = createSignal("");
  const [clearingProviderID, setClearingProviderID] = createSignal("");
  const [clearingProviderBillingID, setClearingProviderBillingID] = createSignal("");
  const [usageBillingReports, setUsageBillingReports] = createSignal<Record<string, UsageBillingReport | undefined>>({});
  const [syncingProviderID, setSyncingProviderID] = createSignal("");
  const [deletingProviderID, setDeletingProviderID] = createSignal("");
  const [refreshingMCPID, setRefreshingMCPID] = createSignal("");
	const [pluginBusy, setPluginBusy] = createSignal("");
  const [promptDraft, setPromptDraft] = createSignal("");
  const [composerTailDraft, setComposerTailDraftSignal] = createSignal("");
  const [composerAttachments, setComposerAttachments] = createSignal<ChatAttachment[]>([]);
  const [composerLinks, setComposerLinks] = createSignal<ComposerLink[]>([]);
  const [composerUndoDepth, setComposerUndoDepth] = createSignal(0);
  const [composerRedoDepth, setComposerRedoDepth] = createSignal(0);
  const [addingImages, setAddingImages] = createSignal(false);
  const [addingDocuments, setAddingDocuments] = createSignal(false);
  const [pendingLinkURL, setPendingLinkURL] = createSignal("");
  const [linkOpenBusy, setLinkOpenBusy] = createSignal<"internal" | "external" | "">("");
  const [previewAttachment, setPreviewAttachment] = createSignal<ChatAttachment>();
  const [copiedMessageID, setCopiedMessageID] = createSignal("");
  const [messages, setMessages] = createSignal<ChatMessage[]>([]);
  const renderedMessages = createStableListViews(messages, (message) => message.id);
  const [messageDisclosures, setMessageDisclosures] = createSignal<Record<string, boolean>>({});
  const [chatNearBottom, setChatNearBottom] = createSignal(true);
  const [runtimeDraft, setRuntimeDraft] = createSignal<RuntimeSettings>();
  const [drawerOpen, setDrawerOpen] = createSignal(false);
  const [activeSettingsCategory, setActiveSettingsCategory] = createSignal<SettingsCategory>("general");
  const [error, setError] = createSignal("");
  const [updateBannerState, setUpdateBannerState] = createSignal<UpdateState>();
  const [updateBannerVisible, setUpdateBannerVisible] = createSignal(false);
  const [updateBannerProgress, setUpdateBannerProgress] = createSignal(1);
  const [confirmation, setConfirmation] = createSignal<ConfirmationState>();
  const [sidebarWidth, setSidebarWidth] = createSignal(readStoredSidebarWidth());
  const [resizingSidebar, setResizingSidebar] = createSignal(false);
  const [browserPanelWidth, setBrowserPanelWidth] = createSignal(storedBrowserPanelWidth ?? defaultBrowserPanelWidth);
  const [resizingBrowserPanel, setResizingBrowserPanel] = createSignal(false);
  const [themeMode, setThemeMode] = createSignal<ThemeMode>(readStoredThemeMode());
  const [uiAppearance, setUIAppearance] = createSignal<UIAppearancePreferences>(storedUIAppearance);
  const [effectiveUIScale, setEffectiveUIScale] = createSignal(resolveEffectiveUIScale(storedUIAppearance));
  // 侧边栏交互状态（此前为写死的假 UI，现改为真实信号驱动）
  const [sidebarTab, setSidebarTab] = createSignal<"groups" | "projects">("groups");
  const [showAllSessions, setShowAllSessions] = createSignal(false);
  // 多项目 / 多会话状态。
  const [projects, setProjects] = createSignal<ProjectInfo[]>([]);
  const [sessions, setSessions] = createSignal<SessionInfo[]>([]);
  const [projectTree, setProjectTree] = createSignal<ProjectNode[]>([]);
  const [sessionTaskRuntimes, setSessionTaskRuntimes] = createSignal<Record<string, SessionTaskRuntime>>({});
  const [sessionViewStates, setSessionViewStates] = createSignal<Record<string, SessionViewState>>({});
  const [selectedSessionID, setSelectedSessionID] = createSignal("");
  const [sessionSort, setSessionSort] = createSignal<"recent" | "name">("recent");
  const [showArchived, setShowArchived] = createSignal(false);
  const [switchingSession, setSwitchingSession] = createSignal(false);
  const [deletingSessionID, setDeletingSessionID] = createSignal("");
  const [sessionActionBusyID, setSessionActionBusyID] = createSignal("");
  const [sessionMenu, setSessionMenu] = createSignal<SessionMenuState>();
  const [sessionRenameDialog, setSessionRenameDialog] = createSignal<SessionRenameDialogState>();
  const [projectMenu, setProjectMenu] = createSignal<ProjectMenuState>();
  const [projectDialog, setProjectDialog] = createSignal<ProjectDialogState>();
  const [projectActionBusy, setProjectActionBusy] = createSignal(false);
  // 视图历史栈：记录“抽屉是否打开 + 当前设置分类”，供前进/后退按钮导航。
  const [viewHistory, setViewHistory] = createSignal<ViewSnapshot[]>([{ drawer: false, category: "general" }]);
  const [viewCursor, setViewCursor] = createSignal(0);
  // Rewind 时间线状态。
  const [timelineOpen, setTimelineOpen] = createSignal(false);
  const [checkpoints, setCheckpoints] = createSignal<CheckpointInfo[]>([]);
  const [branches, setBranches] = createSignal<BranchInfo[]>([]);
  const [rewinding, setRewinding] = createSignal(false);
  const [forkingMessageID, setForkingMessageID] = createSignal("");
  const [editingMessageID, setEditingMessageID] = createSignal("");
  const [editMessageDraft, setEditMessageDraft] = createSignal("");
  const [editMessageAttachments, setEditMessageAttachments] = createSignal<ChatAttachment[]>([]);
  const [editingMessageBusy, setEditingMessageBusy] = createSignal(false);
  // 审批弹窗：待决请求队列（可能连续多次），逐个处理。
  const [approvalQueue, setApprovalQueue] = createSignal<ApprovalRequest[]>([]);
  const [approvalBusy, setApprovalBusy] = createSignal(false);
  const [browserPreview, setBrowserPreview] = createSignal<BrowserPreview>();
  const [workspaceToolsOpen, setWorkspaceToolsOpen] = createSignal(false);
  const [reviewOpen, setReviewOpen] = createSignal(false);
  const [sidePanelView, setSidePanelView] = createSignal<SidePanelView>("browser");
  const [workspaceFileRequest, setWorkspaceFileRequest] = createSignal<WorkspaceFileRequest>();
  const [selectedSubagentTaskID, setSelectedSubagentTaskID] = createSignal("");
  const [stoppingSubagentTaskID, setStoppingSubagentTaskID] = createSignal("");
  let chatScrollRef: HTMLElement | undefined;
  let shellRef: HTMLElement | undefined;
  let workbenchRef: HTMLDivElement | undefined;
	let composerEditorRef: HTMLTextAreaElement | undefined;
  let composerImageInputRef: HTMLInputElement | undefined;
  let composerMarkdownInputRef: HTMLInputElement | undefined;
  let workspaceFileRequestID = 0;
  let projectActionMenuRef: HTMLDivElement | undefined;
  let sessionActionMenuRef: HTMLDivElement | undefined;
  let pointerSidebarResizeActive = false;
  let mouseSidebarResizeActive = false;
  let pointerBrowserResizeActive = false;
  let mouseBrowserResizeActive = false;
  let browserPanelWidthInitialized = storedBrowserPanelWidth !== undefined;
  let composerHistory: ComposerHistory = emptyComposerHistory();
  let updateBannerTimer: number | undefined;
  let updateBannerDeadline = 0;
  let updateBannerSignature = "";

  const syncUIAppearance = () => {
    const next = applyUIAppearance(uiAppearance(), shellRef);
    setEffectiveUIScale((current) => current === next ? current : next);
  };

  const updateUIAppearance = (patch: Partial<UIAppearancePreferences>) => {
    const next = normalizeUIAppearance({ ...uiAppearance(), ...patch });
    setUIAppearance(next);
    persistUIAppearance(next);
  };

  const resetUIAppearance = () => {
    const next = { ...defaultUIAppearance };
    setUIAppearance(next);
    persistUIAppearance(next);
  };

  const profile = createMemo(() => state()?.reasoning ?? fallbackProfile);
  const options = createMemo(() => state()?.reasoningOptions ?? fallbackReasoningOptions);
  const usage = createMemo(() => state()?.usageMetrics ?? emptyUsageMetrics());
  const cacheTarget = createMemo(() => state()?.cacheTarget ?? 0.96);
  const cacheHitRate = createMemo(() => state()?.cacheHitRate ?? 0);
  const hasCacheTokens = createMemo(() => usage().promptCacheHitTokens + usage().promptCacheMissTokens > 0);
  const cacheHealth = createMemo(() => state()?.cacheHealth ?? fallbackCacheHealth());
  const snapshots = createMemo(() => state()?.mcpSnapshots ?? []);
  const builtinToolCount = createMemo(() => snapshots().find((snapshot) => snapshot.server === "builtin")?.tools.length ?? 0);
  const mcpServers = createMemo(() => state()?.mcpServers ?? []);
	const pluginStatuses = createMemo(() => state()?.plugins ?? []);
  const skillsIndex = createMemo(() => state()?.skillsIndex ?? []);
  const contextPreview = createMemo(() => state()?.contextPreview);
  const runtimeSettings = createMemo(() => state()?.runtimeSettings ?? fallbackRuntimeSettings());
  const configFiles = createMemo(() => state()?.configFiles ?? fallbackConfigFiles());
  const activeRuntimeDraft = createMemo(() => runtimeDraft() ?? runtimeSettings());
  const hasProviderKeyDrafts = createMemo(() =>
    Object.values(providerKeyDrafts()).some((apiKey) => apiKey.trim()) ||
    Object.values(providerBillingKeyDrafts()).some((apiKey) => apiKey.trim()),
  );
  const diagnostics = createMemo(() => state()?.cacheDiagnostics ?? []);
  const deepSeek = createMemo(() => state()?.deepSeek ?? fallbackDeepSeekState());
  const deepSeekSession = createMemo(() => state()?.deepSeekSession ?? fallbackDeepSeekSession());
  const sessionHasCacheTokens = createMemo(
    () => deepSeekSession().sessionCacheHitTokens + deepSeekSession().sessionCacheMissTokens > 0,
  );
  const activeChatProvider = createMemo(() => selectedModelProvider(runtimeSettings()));
  const activeChatModel = createMemo(() => selectedModelName(runtimeSettings()));
  const activeProviderReady = createMemo(() => providerReadyForChat(activeChatProvider()));
  const activeProviderConnection = createMemo(() => providerConnectionSummary(activeChatProvider(), deepSeek()));
  // 项目名绑定真实工作区根路径（缺失时回退占位，不再写死 EchoCodeProject）。
  const workspaceName = createMemo(() => {
    const root = runtimeSettings().workspaceRoot?.trim();
    return root ? baseNameFromPath(root) : "未选择项目";
  });
  const activeProjectID = createMemo(() => state()?.activeProjectId?.trim() || projectTree().find((project) => project.isActive)?.id || "");
  const activeSessionID = createMemo(() => {
    if (state()?.activeSessionId?.trim()) return state()!.activeSessionId.trim();
    const activeProject = projectTree().find((project) => project.id === activeProjectID())
      ?? projectTree().find((project) => project.isActive);
    return activeProject?.sessions.find((session) => session.isActive)?.id
      ?? selectedSessionID()
      ?? sessions().find((session) => session.isActive)?.id
      ?? "";
  });
  const activeSessionKey = createMemo(() => sessionIdentityKey(activeProjectID(), activeSessionID()));
  const activeSessionTask = createMemo(() => sessionTaskRuntimes()[activeSessionKey()]);
  const isSessionBusy = (projectID: string, sessionID: string) => Boolean(sessionID && sessionTaskRuntimes()[sessionIdentityKey(projectID, sessionID)]);
  const currentSessionBusy = createMemo(() => isSessionBusy(activeProjectID(), activeSessionID()));
  const anySessionBusy = createMemo(() => Object.keys(sessionTaskRuntimes()).length > 0);
  const backgroundTaskCount = createMemo(
    () => Object.keys(sessionTaskRuntimes()).filter((key) => key !== activeSessionKey()).length,
  );
  const activeSessionTitle = createMemo(() => {
    const sessionID = activeSessionID();
    const project = projectTree().find((candidate) => candidate.id === activeProjectID());
    const session = project?.sessions.find((candidate) => candidate.id === sessionID);
    if (session) return session.title || "新对话";
    return sessions().find((session) => session.id === sessionID)?.title || "新对话";
  });

  const rememberCurrentSessionQueue = (projectID = activeProjectID(), sessionID = activeSessionID()) => {
    if (!projectID || !sessionID) return;
    setQueuedMessagesBySession((current) => ({ ...current, [sessionIdentityKey(projectID, sessionID)]: [...queuedMessages()] }));
  };

  const restoreSessionQueue = (projectID: string, sessionID: string) => {
    setQueuedMessages(queuedMessagesBySession()[sessionIdentityKey(projectID, sessionID)] ?? []);
  };

  const rememberCurrentSessionView = (projectID = activeProjectID(), sessionID = activeSessionID()) => {
    if (!projectID || !sessionID) return;
    const key = sessionIdentityKey(projectID, sessionID);
    setSessionViewStates((current) => ({
      ...current,
      [key]: {
        messages: [...messages()],
        disclosures: { ...messageDisclosures() },
        composerDraft: promptDraft(),
        composerTail: composerTailDraft(),
        composerAttachments: composerAttachments().map((attachment) => ({ ...attachment })),
        composerLinks: composerLinks().map((link) => ({ ...link })),
        activeTaskProgress: activeTaskProgress() ? cloneTaskProgress(activeTaskProgress()!) : undefined,
        browserPreview: browserPreview(),
        reviewOpen: reviewOpen(),
        sidePanelView: sidePanelView(),
        workspaceFileRequest: workspaceFileRequest(),
        selectedSubagentTaskID: selectedSubagentTaskID(),
      },
    }));
  };

  const restoreSessionView = (projectID: string, sessionID: string): boolean => {
    const view = sessionViewStates()[sessionIdentityKey(projectID, sessionID)];
    if (!view) {
      setMessages([]);
      setMessageDisclosures({});
      setComposerDraft("");
      setComposerTail("");
      setComposerAttachments([]);
      setComposerLinks([]);
      setActiveTaskProgress(undefined);
      setBrowserPreview(undefined);
      setReviewOpen(false);
      setSidePanelView("browser");
      setWorkspaceFileRequest(undefined);
      setSelectedSubagentTaskID("");
      return false;
    }
    setMessages([...view.messages]);
    setMessageDisclosures({ ...(view.disclosures ?? {}) });
    setComposerDraft(view.composerDraft ?? "");
    setComposerTail(view.composerTail ?? "");
    setComposerAttachments(view.composerAttachments?.map((attachment) => ({ ...attachment })) ?? []);
    setComposerLinks(view.composerLinks?.map((link) => ({ ...link })) ?? []);
    setActiveTaskProgress(view.activeTaskProgress ? cloneTaskProgress(view.activeTaskProgress) : undefined);
    setBrowserPreview(view.browserPreview);
    setReviewOpen(view.reviewOpen);
    setSidePanelView(view.sidePanelView);
    setWorkspaceFileRequest(view.workspaceFileRequest);
    setSelectedSubagentTaskID(view.selectedSubagentTaskID ?? "");
    return true;
  };

  const messageDisclosureKey = (messageID: string, disclosureKey: string) => `${messageID}\u0000${disclosureKey}`;
  const isMessageDisclosureOpen = (messageID: string, disclosureKey: string) => (
    messageDisclosures()[messageDisclosureKey(messageID, disclosureKey)] ?? false
  );
  const setMessageDisclosureOpen = (messageID: string, disclosureKey: string, open: boolean) => {
    const key = messageDisclosureKey(messageID, disclosureKey);
    setMessageDisclosures((current) => {
      if ((current[key] ?? false) === open) return current;
      if (!open) {
        const next = { ...current };
        delete next[key];
        return next;
      }
      return { ...current, [key]: true };
    });
  };
  const revealMessageSecret = async (secretID: string) => {
    const projectID = activeProjectID();
    const sessionID = activeSessionID();
    if (!projectID || !sessionID) throw new Error("当前没有可用会话。");
    return (await revealSecretResult(projectID, sessionID, secretID)).value;
  };
  const planMode = createMemo(() => state()?.planMode ?? false);
  const teamMode = createMemo(() => runtimeSettings().team.enabled);
	const pausedTeamTask = createMemo(() => {
		const team = state()?.team;
		return !currentSessionBusy() && team?.status === "paused";
	});
  const activeTeamParts = createMemo<TeamPart[]>(() => {
    const task = activeSessionTask();
    if (!task) return [];

    const latestByRole = new Map<string, TeamPart>();
    for (const part of task.assistantMessage.parts ?? []) {
      if (part.kind !== "team_role") continue;
      const current = latestByRole.get(part.role);
      if (!current || (part.attempt ?? 1) >= (current.attempt ?? 1)) latestByRole.set(part.role, part);
    }

    const configuredRoles = teamMode() ? runtimeSettings().team.roles.filter((role) => role.enabled) : [];
    for (const role of configuredRoles) {
      if (latestByRole.has(role.role)) continue;
      const provider = runtimeSettings().model.providers.find((candidate) => candidate.id === role.providerId);
      latestByRole.set(role.role, {
        kind: "team_role",
        role: role.role,
        roleLabel: teamRoleLabel(role.role),
        providerId: role.providerId,
        model: role.modelId || provider?.defaultModelId,
        status: "pending",
        attempt: 1,
      });
    }

    const roleOrder = new Map(configuredRoles.map((role, index) => [role.role, index]));
    return [...latestByRole.values()].sort((left, right) =>
      (roleOrder.get(left.role) ?? 99) - (roleOrder.get(right.role) ?? 99),
    );
  });
  const activeSubagentParts = createMemo<SubagentPart[]>(() => {
    const task = activeSessionTask();
    if (!task) return [];
    return (task.assistantMessage.parts ?? []).filter(
	  (part): part is SubagentPart => part.kind === "subagent" && (part.status === "pending" || part.status === "running"),
	);
  });
  const selectedSubagent = createMemo<SubagentPart | undefined>(() => {
    const taskID = selectedSubagentTaskID();
    if (!taskID) return undefined;
    for (const message of [...messages()].reverse()) {
      const part = message.parts?.find((candidate): candidate is SubagentPart => candidate.kind === "subagent" && candidate.taskId === taskID);
      if (part) return part;
    }
    return activeSessionTask()?.assistantMessage.parts?.find(
	  (candidate): candidate is SubagentPart => candidate.kind === "subagent" && candidate.taskId === taskID,
	);
  });
  const undoableFileChangeMessageID = createMemo(() => {
    const latestAssistant = [...messages()].reverse().find((message) => message.role === "assistant" && !message.streaming);
    return latestAssistant && messageHasTrackedFileChanges(latestAssistant) ? latestAssistant.id : "";
  });
  // 完全访问 = 审批策略为 never（命令/文件修改不再逐次弹框确认）。
  const fullAccess = createMemo(() => runtimeSettings().approvalPolicy === "never");
  const modelName = createMemo(() => {
    const provider = activeChatProvider();
    const model = deepSeekSession().model || activeChatModel();
    if (!provider) {
      return model || "选择模型";
    }
    return model ? `${provider.name} · ${model}` : provider.name;
  });
  const canSend = createMemo(() => (promptDraft().trim().length > 0 || composerTailDraft().trim().length > 0 || composerLinks().length > 0 || composerAttachments().length > 0) && activeProviderReady() && Boolean(activeChatModel()));

  const requestConfirmationDecision = (request: ConfirmationRequest): Promise<ConfirmationResult> =>
    new Promise((resolve) => {
      confirmation()?.resolve("dismiss");
      setConfirmation({ ...request, resolve });
    });

  const confirmAction = async (request: ConfirmationRequest): Promise<boolean> =>
    (await requestConfirmationDecision(request)) === "confirm";

  const resolveConfirmation = (result: ConfirmationResult) => {
    const current = confirmation();
    if (!current) return;
    setConfirmation(undefined);
    current.resolve(result);
  };

  onCleanup(() => {
    confirmation()?.resolve("dismiss");
  });

  createEffect(() => {
	const value = composerEditableText(promptDraft(), composerTailDraft());
    const editor = composerEditorRef;
	if (!editor || editor.value === value) return;
	editor.value = value;
	resizeComposerEditor(editor);
  });

  const handleBrowserPreviewRequest = async (preview: BrowserPreview) => {
    try {
      if (preview.ask) {
        const decision = await requestConfirmationDecision({
          title: "选择打开方式",
          message: `打开“${preview.name}”`,
          detail: "可在 EchoCode 内查看，也可交给系统默认应用。",
          confirmLabel: "内置打开",
          cancelLabel: "系统打开",
        });
        if (decision === "dismiss") return;
        if (decision === "cancel") {
          await openWorkspaceFile(preview.path);
          return;
        }
      }
      setBrowserPreview({ ...preview, ask: false });
      setSidePanelView("browser");
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const handlePreviewFile = async (path: string) => {
    setError("");
    try {
      setBrowserPreview(await previewWorkspaceFile(path));
      setSidePanelView("browser");
    } catch (err) {
      setError(errorMessage(err));
      throw err;
    }
  };

  const handleOpenWorkspaceFile = (path: string, view: WorkspaceFileView = "file", line?: number) => {
    const target = path.trim();
    if (!target) return;
    setError("");
    setWorkspaceToolsOpen(false);
    setWorkspaceFileRequest({ id: ++workspaceFileRequestID, path: target, view, line });
    setReviewOpen(true);
    setSidePanelView("files");
    queueMicrotask(constrainBrowserPanelWidth);
  };

  const handleOpenBrowser = async () => {
    setError("");
    try {
      const browserState = await openBrowserURL("about:blank");
      setBrowserPreview({
        path: "",
        name: "浏览器",
        url: "about:blank",
        tabId: browserState.activeTabId,
        managed: true,
      });
      setSidePanelView("browser");
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const openLinkInternally = async (url: string) => {
    setError("");
    const browserState = await openBrowserURL(url);
    let name = "网页";
    try {
      name = new URL(url).hostname || name;
    } catch {
      // Browser service performs the authoritative URL validation.
    }
    setBrowserPreview({
      path: "",
      name,
      url,
      tabId: browserState.activeTabId,
      managed: true,
    });
    setSidePanelView("browser");
  };

  const closeBrowserPanel = () => {
    setBrowserPreview(undefined);
    if (reviewOpen()) setSidePanelView("files");
    else if (selectedSubagent()) setSidePanelView("subagent");
  };

  const closeReviewPanel = () => {
    setReviewOpen(false);
    if (browserPreview()) setSidePanelView("browser");
    else if (selectedSubagent()) setSidePanelView("subagent");
  };

  const openReviewPanel = () => {
    setReviewOpen(true);
    setSidePanelView("files");
    setWorkspaceToolsOpen(false);
    queueMicrotask(constrainBrowserPanelWidth);
  };

  const openSubagentPanel = (part: SubagentPart) => {
    setSelectedSubagentTaskID(part.taskId);
    setSidePanelView("subagent");
    setWorkspaceToolsOpen(false);
    queueMicrotask(constrainBrowserPanelWidth);
  };

  const closeSubagentPanel = () => {
    setSelectedSubagentTaskID("");
    if (browserPreview()) setSidePanelView("browser");
    else if (reviewOpen()) setSidePanelView("files");
  };

  const stopOneSubagent = async (part: SubagentPart) => {
    const parent = activeSessionTask();
    if (!parent || stoppingSubagentTaskID()) return;
    setStoppingSubagentTaskID(part.taskId);
    setError("");
    try {
      const stopped = await stopSubagent(parent.taskID, part.taskId);
      if (!stopped) setError("该子代理已经结束或不属于当前任务。");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setStoppingSubagentTaskID("");
    }
  };

  const toggleReviewPanel = () => {
    if (reviewOpen() && sidePanelView() === "files") {
      closeReviewPanel();
      return;
    }
    openReviewPanel();
  };

  const requestOpenURL = (url: string) => {
    const target = url.trim();
    if (!/^https?:\/\//i.test(target)) {
      setError("只能打开 HTTP 或 HTTPS 链接。");
      return;
    }
    setPendingLinkURL(target);
  };

  const openPendingLink = async (destination: "internal" | "external") => {
    const url = pendingLinkURL();
    if (!url || linkOpenBusy()) return;
    setLinkOpenBusy(destination);
    setError("");
    try {
      if (destination === "internal") {
        await openLinkInternally(url);
      } else {
        await openURLInSystemBrowser(url);
      }
      setPendingLinkURL("");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLinkOpenBusy("");
    }
  };

  const copyMessage = async (message: ChatMessage) => {
    const text = message.content.trim() || (message.parts ?? [])
      .filter((part): part is Extract<MessagePart, { kind: "text" }> => part.kind === "text")
      .map((part) => part.text)
      .join("\n\n")
      .trim();
    if (!text) return;
    try {
      await writeClipboardText(text);
      setCopiedMessageID(message.id);
      window.setTimeout(() => setCopiedMessageID((current) => current === message.id ? "" : current), 1400);
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  // 真实会话列表。当前后端仅支持单会话，故此处只反映“当前对话”；
  // 多会话历史待后端事件流存储落地后再填充（不再摆放写死的演示条目）。
  const sidebarSessions = createMemo(() => {
    const list: SidebarSession[] = [];
    if (messages().length > 0) {
      list.push({
        title: messages()[messages().length - 1]?.content || "当前对话",
        meta: deepSeekSession().active ? `${formatInteger(deepSeekSession().turnCount)} 轮` : "进行中",
        active: !drawerOpen(),
        dot: true,
        onClick: () => setDrawerOpen(false),
      });
    }
    return list;
  });

  // 侧边栏“快捷入口”：真实指标驱动，指向设置抽屉的对应分类。
  const sidebarShortcuts = createMemo<SidebarSession[]>(() => [
    {
      title: "缓存命中率",
      meta: formatPercent(cacheHitRate(), hasCacheTokens()),
      active: activeSettingsCategory() === "usage" && drawerOpen(),
      dot: cacheHealth().status === "low",
      onClick: () => openDrawer("cache"),
    },
    {
      title: "工具链与 Skills",
      meta: `${formatInteger(builtinToolCount())} 工具 · ${formatInteger(skillsIndex().length)} Skills`,
      active: activeSettingsCategory() === "skills" && drawerOpen(),
      dot: false,
      onClick: () => openDrawer("tools"),
    },
  ]);

  // 折叠时最多显示 4 条会话，其余由“显示更多”展开。
  const visibleSessions = createMemo(() =>
    showAllSessions() ? sidebarSessions() : sidebarSessions().slice(0, 4),
  );
  const hasMoreSessions = createMemo(() => sidebarSessions().length > 4);

  const updateSessionStreamingMessage = (
    projectID: string,
    sessionID: string,
    update: (message: ChatMessage) => ChatMessage,
  ) => {
    const key = sessionIdentityKey(projectID, sessionID);
    let messageID = "";
    setSessionTaskRuntimes((current) => {
      const task = current[key];
      if (!task) return current;
      messageID = task.messageID;
      return {
        ...current,
        [key]: { ...task, assistantMessage: update(task.assistantMessage) },
      };
    });
    if (!messageID || projectID !== activeProjectID() || sessionID !== activeSessionID()) return;
    setMessages((current) => current.map((message) => (message.id === messageID ? update(message) : message)));
  };

  const updateStreamingMessage = (update: (message: ChatMessage) => ChatMessage) => {
    updateSessionStreamingMessage(activeProjectID(), activeSessionID(), update);
  };

  const updateSessionTaskProgress = (projectID: string, sessionID: string, progress?: TaskProgressPart) => {
    const key = sessionIdentityKey(projectID, sessionID);
    setSessionTaskRuntimes((current) => {
      const task = current[key];
      if (!task) return current;
      return { ...current, [key]: { ...task, progress: progress ? cloneTaskProgress(progress) : undefined } };
    });
    if (projectID === activeProjectID() && sessionID === activeSessionID()) {
      setActiveTaskProgress(progress ? cloneTaskProgress(progress) : undefined);
    }
  };

  const cacheSessionTaskView = (projectID: string, sessionID: string) => {
    const key = sessionIdentityKey(projectID, sessionID);
    const task = sessionTaskRuntimes()[key];
    if (!task) return;
    setSessionViewStates((current) => {
      const existing = current[key] ?? {
        messages: [],
        reviewOpen: false,
        sidePanelView: "browser" as SidePanelView,
      };
      const composed = redactSensitiveTextForDisplay(composeComposerPrompt(task.prompt, task.links, task.tail));
      let nextMessages = [...existing.messages];
      if ((composed || task.attachments.length > 0) && !nextMessages.some((message) => message.id === task.userMessageID)) {
        nextMessages.push({
          ...createChatMessage("user", composed),
          id: task.userMessageID || `user-task-${task.taskID || Date.now()}`,
          attachments: task.attachments,
        });
      }
      const assistantIndex = nextMessages.findIndex((message) => message.id === task.messageID);
      if (assistantIndex >= 0) nextMessages[assistantIndex] = task.assistantMessage;
      else nextMessages.push(task.assistantMessage);
      return {
        ...current,
        [key]: {
          ...existing,
          messages: nextMessages,
          activeTaskProgress: task.progress ? cloneTaskProgress(task.progress) : existing.activeTaskProgress,
        },
      };
    });
  };

  const rollbackOptimisticTurn = (projectID: string, sessionID: string) => {
    const key = sessionIdentityKey(projectID, sessionID);
    const task = sessionTaskRuntimes()[key];
    if (!task) return;
    const restoreComposer = !task.optimisticRolledBack && Boolean(
      task.userMessageID || task.prompt || task.tail || task.attachments.length > 0 || task.links.length > 0,
    );
    const turn = {
      userMessageID: task.userMessageID,
      assistantMessageID: task.messageID,
      draft: task.prompt,
      tail: task.tail,
      attachments: task.attachments,
      links: task.links,
    };
    const restoredComposer = rollbackOptimisticTurnState([], turn).composer;
    setSessionTaskRuntimes((current) => {
      const currentTask = current[key];
      if (!currentTask || currentTask.optimisticRolledBack) return current;
      return { ...current, [key]: { ...currentTask, optimisticRolledBack: true } };
    });
    setSessionViewStates((current) => {
      const existing = current[key];
      if (!existing) return current;
      const rollback = rollbackOptimisticTurnState(existing.messages, turn);
      return {
        ...current,
        [key]: {
          ...existing,
          messages: rollback.messages,
          activeTaskProgress: undefined,
          composerDraft: restoreComposer ? rollback.composer.draft : existing.composerDraft,
          composerTail: restoreComposer ? rollback.composer.tail : existing.composerTail,
          composerAttachments: restoreComposer ? rollback.composer.attachments : existing.composerAttachments,
          composerLinks: restoreComposer ? rollback.composer.links : existing.composerLinks,
        },
      };
    });
    if (projectID === activeProjectID() && sessionID === activeSessionID()) {
      setMessages((current) => rollbackOptimisticTurnState(current, turn).messages);
      setActiveTaskProgress(undefined);
      if (restoreComposer) {
        setComposerDraft(restoredComposer.draft);
        setComposerTail(restoredComposer.tail);
        setComposerAttachments(restoredComposer.attachments);
        setComposerLinks(restoredComposer.links);
        resetComposerHistory();
      }
    }
  };

  const beginGuidedTaskMessage = (projectID: string, sessionID: string, event: ChatTaskEvent): { userMessage: ChatMessage; assistantMessage: ChatMessage } | undefined => {
    const key = sessionIdentityKey(projectID, sessionID);
    const guidance = event.guidance?.trim() ?? "";
    const attachments = event.attachments?.map((attachment) => ({ ...attachment })) ?? [];
    const startedAt = new Date().toISOString();
    const userMessage: ChatMessage = {
      ...createChatMessage("user", redactSensitiveTextForDisplay(guidance)),
      id: `user-guidance-${event.guidanceId || Date.now()}`,
      attachments,
    };
    const message: ChatMessage = {
      id: `assistant-guidance-${event.guidanceId || Date.now()}`,
      role: "assistant",
      content: "",
      createdAt: startedAt,
      model: event.model || activeChatModel(),
      streaming: true,
      status: event.message || "正在应用引导",
    };
    let updated = false;
    setSessionTaskRuntimes((current) => {
      const task = current[key];
      if (!task) return current;
      updated = true;
      return {
        ...current,
        [key]: {
          ...task,
          userMessageID: userMessage.id,
          messageID: message.id,
          prompt: guidance,
          tail: "",
          attachments,
          links: [],
          startedAt,
          assistantMessage: message,
        },
      };
    });
    return updated ? { userMessage, assistantMessage: message } : undefined;
  };

  const reduceBackgroundTaskEvent = (projectID: string, sessionID: string, event: ChatTaskEvent) => {
    switch (event.type) {
      case "started":
      case "status":
		updateSessionStreamingMessage(projectID, sessionID, (message) => {
		  const live = flushLiveAssistantMessage(message);
		  return {
			...live,
			parts: updateLiveTimelineParts(live.parts, event),
			status: liveTaskStatus(event.message),
			statusKind: streamStatusKind(event.status),
			compressionStatus: undefined,
		  };
		});
        break;
      case "context_compression":
        updateSessionStreamingMessage(projectID, sessionID, (message) => {
		  const live = flushLiveAssistantMessage(message);
		  return {
			...live,
			parts: updateLiveTimelineParts(live.parts, event),
			status: event.message || (event.compression?.status === "error" ? "自动压缩上下文失败" : "正在自动压缩上下文"),
			statusKind: "compression",
			compressionStatus: event.compression?.status === "completed" ? "completed" : event.compression?.status === "error" ? "error" : "running",
		  };
		});
        break;
      case "heartbeat":
        break;
      case "delta":
        updateSessionStreamingMessage(projectID, sessionID, (message) => ({ ...message, content: message.content + (event.delta ?? ""), model: event.model || message.model, status: "正在生成" }));
        break;
      case "reasoning":
        updateSessionStreamingMessage(projectID, sessionID, (message) => ({ ...message, reasoning: (message.reasoning ?? "") + (event.delta ?? ""), status: "正在推理" }));
        break;
      case "usage":
      case "usage_state":
        break;
      case "provider_notice":
        updateSessionStreamingMessage(projectID, sessionID, (message) => {
		  const live = flushLiveAssistantMessage(message);
		  return { ...live, parts: mergeLiveToolResultParts(live.parts ?? [], event.parts), status: event.message || message.status || "正在处理供应商响应" };
		});
        break;
      case "tool":
        updateSessionStreamingMessage(projectID, sessionID, (message) => {
		  const live = flushLiveAssistantMessage(message);
		  return { ...live, parts: mergeLiveToolResultParts(updateLiveToolParts(live.parts, event), event.parts), status: toolEventMessage(event), statusKind: toolEventStatusKind(event.status) };
		});
        break;
      case "subagent":
        updateSessionStreamingMessage(projectID, sessionID, (message) => {
		  const live = flushLiveAssistantMessage(message);
		  return { ...live, parts: mergeLiveToolResultParts(live.parts ?? [], event.parts), status: event.message || "子代理正在工作" };
		});
        break;
      case "progress":
        if (event.progress) updateSessionTaskProgress(projectID, sessionID, event.progress);
        updateSessionStreamingMessage(projectID, sessionID, (message) => {
		  const live = flushLiveAssistantMessage(message);
		  return { ...live, parts: updateLiveProgressPart(live.parts, event.progress), status: "正在执行任务" };
		});
        break;
      case "team":
        updateSessionStreamingMessage(projectID, sessionID, (message) => {
		  const live = flushLiveAssistantMessage(message);
		  return { ...live, parts: updateLiveTeamPart(live.parts, event), model: event.model || message.model, status: event.message || `${event.team?.label || "团队角色"}正在工作` };
		});
        break;
      case "guidance": {
        const previousResult = event.result;
        if (previousResult) {
          updateSessionStreamingMessage(projectID, sessionID, (message) => ({
            ...message,
            content: previousResult.content || message.content || "本轮没有返回可展示内容。",
            reasoning: previousResult.reasoning,
            model: previousResult.model,
            usage: previousResult.usage,
            parts: previousResult.parts,
            durationMs: previousResult.durationMs,
            streaming: false,
            status: undefined,
          }));
          cacheSessionTaskView(projectID, sessionID);
        }
        beginGuidedTaskMessage(projectID, sessionID, event);
        break;
      }
      case "completed": {
        const result = event.result;
        updateSessionStreamingMessage(projectID, sessionID, (message) => ({
          ...message,
          content: result?.content || message.content || "本轮没有返回可展示内容。",
          reasoning: result?.reasoning || message.reasoning,
          model: result?.model || message.model,
          usage: result?.usage || message.usage,
          parts: result?.parts || message.parts,
          durationMs: result?.durationMs ?? message.durationMs,
          streaming: false,
          status: undefined,
        }));
        break;
      }
      case "cancelled": {
        const result = event.result;
        const task = sessionTaskRuntimes()[sessionIdentityKey(projectID, sessionID)];
        const retainedTurn = hasMeaningfulTurnOutput(result, task?.assistantMessage);
        if (!retainedTurn) {
          rollbackOptimisticTurn(projectID, sessionID);
          return;
        }
        updateSessionStreamingMessage(projectID, sessionID, (message) => ({
          ...message,
          content: result?.content || message.content,
          reasoning: result?.reasoning || message.reasoning,
          model: result?.model || message.model,
          usage: result?.usage || message.usage,
          parts: settleLiveProgress(result?.parts?.length ? result.parts : message.parts, "cancelled"),
          durationMs: result?.durationMs ?? message.durationMs,
          failed: false,
          cancelled: true,
          streaming: false,
          status: "已停止",
        }));
        break;
      }
      case "failed": {
        const result = event.result;
        const task = sessionTaskRuntimes()[sessionIdentityKey(projectID, sessionID)];
        const retainedTurn = hasMeaningfulTurnOutput(result, task?.assistantMessage);
        if (!retainedTurn) {
          rollbackOptimisticTurn(projectID, sessionID);
          return;
        }
        updateSessionStreamingMessage(projectID, sessionID, (message) => ({
          ...message,
          content: result?.content || message.content || chatFailureMessage(event.message || "模型请求失败。"),
          reasoning: result?.reasoning || message.reasoning,
          model: result?.model || message.model,
          usage: result?.usage || message.usage,
          parts: settleLiveProgress(result?.parts?.length ? result.parts : message.parts, "failed"),
          durationMs: result?.durationMs ?? message.durationMs,
          failed: true,
          cancelled: false,
          streaming: false,
          status: undefined,
        }));
        break;
      }
    }
    cacheSessionTaskView(projectID, sessionID);
  };

  const settleActiveTaskProgress = (taskStatus: "completed" | "failed" | "cancelled") => {
    setActiveTaskProgress((current) => current ? { ...current, taskStatus } : current);
  };

  const finishChatTask = (finishedProjectID = activeProjectID(), finishedSessionID = activeSessionID(), finishedTaskID = "") => {
    const finishedKey = sessionIdentityKey(finishedProjectID, finishedSessionID);
    const visibleProjectID = activeProjectID();
    const visibleSessionID = activeSessionID();
    const visibleTaskID = activeChatTaskID();
    const pendingReasoning = pendingReasoningLevel();
    if (!finishedSessionID) return undefined;
    let removed = false;
    setSessionTaskRuntimes((current) => {
      const task = current[finishedKey];
      if (!task || (finishedTaskID && task.taskID && task.taskID !== finishedTaskID)) return current;
      const next = { ...current };
      delete next[finishedKey];
      removed = true;
      return next;
    });
	if (removed) {
	  setSessionViewStates((current) => {
		const view = current[finishedKey];
		if (!view || !view.activeTaskProgress) return current;
		return { ...current, [finishedKey]: { ...view, activeTaskProgress: undefined } };
	  });
	}
    const belongsToVisibleTask = visibleProjectID === finishedProjectID && visibleSessionID === finishedSessionID
      && (!finishedTaskID || !visibleTaskID || visibleTaskID === finishedTaskID);
    if (removed && belongsToVisibleTask) {
      setSendingMessage(false);
      setActiveChatTaskID("");
      setStreamingMessageID("");
	  setActiveTaskProgress(undefined);
      setPendingReasoningLevel(undefined);
      return pendingReasoning && pendingReasoning !== profile().id ? pendingReasoning : undefined;
    }
    return undefined;
  };

  const applyPendingReasoning = async (pending?: ReasoningLevel) => {
    if (pending) await changeReasoning(pending);
  };

  const updateChatScrollState = () => {
    const element = chatScrollRef;
    if (!element) {
      return;
    }
    const remaining = element.scrollHeight - element.scrollTop - element.clientHeight;
    setChatNearBottom(remaining <= 72);
  };

  const scrollChatToBottom = (behavior: ScrollBehavior = "smooth") => {
    const element = chatScrollRef;
    if (!element) {
      return;
    }
    element.scrollTo({ top: element.scrollHeight, behavior });
    if (behavior === "auto") {
      setChatNearBottom(true);
    }
  };

  const applyLiveUsageState = (event: ChatTaskEvent) => {
    const usageState = event.usageState;
    if (!usageState) return;
    setState((current) => current ? {
      ...current,
      usageMetrics: usageState.usageMetrics,
      cacheHitRate: usageState.cacheHitRate,
      cacheHealth: usageState.cacheHealth,
      deepSeekSession: usageState.deepSeekSession,
      cacheDiagnostics: usageState.cacheDiagnostics,
      usageLedger: usageState.usageLedger,
    } : current);
  };

  const handleChatTaskEvent = (event: ChatTaskEvent) => {
    const eventProjectID = event.projectId?.trim() || activeProjectID();
    const eventSessionID = event.sessionId?.trim() || activeSessionID();
    const eventSessionKey = sessionIdentityKey(eventProjectID, eventSessionID);
    const currentTaskID = activeChatTaskID();
    const currentProjectID = activeProjectID();
    const currentSessionID = activeSessionID();
    const eventTask = eventSessionID ? sessionTaskRuntimes()[eventSessionKey] : undefined;
    const isCurrentSession = (!eventSessionID || !currentSessionID || eventSessionID === currentSessionID)
      && (!eventProjectID || !currentProjectID || eventProjectID === currentProjectID);
    const matchesSessionTask = !eventTask || !eventTask.taskID || eventTask.taskID === event.taskId;
    if (!isCurrentSession || !matchesSessionTask || (currentTaskID && event.taskId !== currentTaskID)) {
      if (!isCurrentSession && eventTask && matchesSessionTask) {
        reduceBackgroundTaskEvent(eventProjectID, eventSessionID, event);
      }
      if (event.type === "completed" || event.type === "failed" || event.type === "cancelled") {
        if (eventSessionID && matchesSessionTask) finishChatTask(eventProjectID, eventSessionID, event.taskId);
        void refreshProjectsAndSessions();
      }
      return;
    }
    if (currentTaskID && event.taskId !== currentTaskID) {
      return;
    }
    if (!eventTask && !sendingMessage()) {
      return;
    }
    if (!currentTaskID) {
      setActiveChatTaskID(event.taskId);
    }

    switch (event.type) {
      case "started":
      case "status":
		updateStreamingMessage((message) => {
		  const live = flushLiveAssistantMessage(message);
		  return {
			...live,
			parts: updateLiveTimelineParts(live.parts, event),
			status: liveTaskStatus(event.message),
			statusKind: streamStatusKind(event.status),
			compressionStatus: undefined,
		  };
		});
        break;
      case "context_compression":
        updateStreamingMessage((message) => {
		  const live = flushLiveAssistantMessage(message);
		  return {
			...live,
			parts: updateLiveTimelineParts(live.parts, event),
			status: event.message || (event.compression?.status === "error" ? "自动压缩上下文失败" : "正在自动压缩上下文"),
			statusKind: "compression",
			compressionStatus: event.compression?.status === "completed"
			  ? "completed"
			  : event.compression?.status === "error"
				? "error"
				: "running",
		  };
		});
        break;
      case "heartbeat":
        break;
      case "delta":
        updateStreamingMessage((message) => ({
          ...message,
          content: message.content + (event.delta ?? ""),
          model: event.model || message.model,
          status: "正在生成",
          statusKind: undefined,
          compressionStatus: undefined,
        }));
        break;
      case "reasoning":
        updateStreamingMessage((message) => ({
          ...message,
          reasoning: (message.reasoning ?? "") + (event.delta ?? ""),
          status: "正在推理",
          statusKind: undefined,
          compressionStatus: undefined,
        }));
        break;
      case "usage":
        break;
      case "usage_state":
        applyLiveUsageState(event);
        if (event.usageState) {
          updateStreamingMessage((message) => ({ ...message, usage: event.usageState!.usageMetrics }));
        }
        break;
      case "provider_notice":
        updateStreamingMessage((message) => {
		  const live = flushLiveAssistantMessage(message);
		  return { ...live, parts: mergeLiveToolResultParts(live.parts ?? [], event.parts), status: event.message || message.status || "正在处理供应商响应", statusKind: undefined, compressionStatus: undefined };
		});
        break;
      case "tool":
        updateStreamingMessage((message) => {
		  const live = flushLiveAssistantMessage(message);
		  return { ...live, parts: mergeLiveToolResultParts(updateLiveToolParts(live.parts, event), event.parts), status: toolEventMessage(event), statusKind: toolEventStatusKind(event.status), compressionStatus: undefined };
		});
        break;
      case "subagent":
        updateStreamingMessage((message) => {
		  const live = flushLiveAssistantMessage(message);
		  return { ...live, parts: mergeLiveToolResultParts(live.parts ?? [], event.parts), status: event.message || "子代理正在工作", statusKind: undefined, compressionStatus: undefined };
		});
        break;
      case "progress":
        if (event.progress) {
          updateSessionTaskProgress(eventProjectID, eventSessionID, event.progress);
        }
        updateStreamingMessage((message) => {
		  const live = flushLiveAssistantMessage(message);
		  return { ...live, parts: updateLiveProgressPart(live.parts, event.progress), status: "正在执行任务", statusKind: undefined, compressionStatus: undefined };
		});
        break;
      case "team":
        updateStreamingMessage((message) => {
		  const live = flushLiveAssistantMessage(message);
		  return { ...live, parts: updateLiveTeamPart(live.parts, event), model: event.model || message.model, status: event.message || `${event.team?.label || "团队角色"}正在工作`, statusKind: undefined, compressionStatus: undefined };
		});
        break;
      case "guidance": {
        const previousResult = event.result;
        const previousProgress = findTaskProgress(previousResult?.parts);
        if (previousProgress) {
          updateSessionTaskProgress(eventProjectID, eventSessionID, previousProgress);
        }
        if (previousResult) {
          setState(previousResult.state);
          updateStreamingMessage((message) => ({
            ...message,
            content: previousResult.content || message.content || "本轮没有返回可展示内容。",
            reasoning: previousResult.reasoning,
            model: previousResult.model,
            usage: previousResult.usage,
            parts: previousResult.parts,
            durationMs: previousResult.durationMs,
            streaming: false,
            status: undefined,
          }));
        }
        if (event.guidanceId) {
          setQueuedMessages((current) => removeMessage(current, event.guidanceId!));
        }
        const guidance = event.guidance?.trim() ?? "";
        const guidanceAttachments = event.attachments?.map((attachment) => ({ ...attachment })) ?? [];
        cacheSessionTaskView(eventProjectID, eventSessionID);
        const guidedTask = beginGuidedTaskMessage(eventProjectID, eventSessionID, event);
        if (!guidedTask) break;
        setMessages((current) => [
          ...current,
          guidedTask.userMessage,
          guidedTask.assistantMessage,
        ]);
        setStreamingMessageID(guidedTask.assistantMessage.id);
        break;
      }
      case "completed": {
        const result = event.result;
        const resultProgress = findTaskProgress(result?.parts);
        if (resultProgress) {
          updateSessionTaskProgress(eventProjectID, eventSessionID, { ...cloneTaskProgress(resultProgress), taskStatus: "completed" });
        } else {
          settleActiveTaskProgress("completed");
        }
        if (result) {
          setState(result.state);
          updateStreamingMessage((message) => ({
            ...message,
            content: result.content || message.content || "本轮没有返回可展示内容。",
            reasoning: result.reasoning,
            model: result.model,
            usage: result.usage,
            parts: result.parts,
            durationMs: result.durationMs,
            streaming: false,
            status: undefined,
          }));
        } else {
          updateStreamingMessage((message) => ({ ...message, streaming: false, status: undefined }));
        }
        const pendingReasoning = finishChatTask(eventProjectID, eventSessionID, event.taskId);
        void (async () => {
          if (eventProjectID === activeProjectID() && eventSessionID === activeSessionID()) {
            await restoreSessionMessages(true, eventProjectID, eventSessionID);
            await applyPendingReasoning(pendingReasoning);
            await startNextQueuedMessage();
          }
          void refreshProjectsAndSessions();
          if (eventProjectID === activeProjectID() && eventSessionID === activeSessionID()) void refreshCheckpoints();
        })();
        break;
      }
      case "cancelled": {
        setQueuedMessages(clearGuidanceMessages);
        const result = event.result;
        const retainedTurn = hasMeaningfulTurnOutput(result, eventTask?.assistantMessage);
        if (result) {
          setState(result.state);
        }
        if (!retainedTurn) {
          rollbackOptimisticTurn(eventProjectID, eventSessionID);
        } else {
          setError("");
          updateStreamingMessage((current) => ({
            ...current,
            content: result?.content || current.content,
            reasoning: result?.reasoning || current.reasoning,
            model: result?.model || current.model,
            usage: result?.usage || current.usage,
            parts: settleLiveProgress(result?.parts?.length ? result.parts : current.parts, "cancelled"),
            durationMs: result?.durationMs ?? current.durationMs,
            failed: false,
            cancelled: true,
            streaming: false,
            status: "已停止",
          }));
        }
        const resultProgress = findTaskProgress(result?.parts);
        if (resultProgress) {
          updateSessionTaskProgress(eventProjectID, eventSessionID, {
            ...cloneTaskProgress(resultProgress),
            taskStatus: "cancelled",
          });
        } else {
          settleActiveTaskProgress("cancelled");
        }
        const pendingReasoning = finishChatTask(eventProjectID, eventSessionID, event.taskId);
        void (async () => {
          if (!event.forced && eventProjectID === activeProjectID() && eventSessionID === activeSessionID()) {
            await restoreSessionMessages(true, eventProjectID, eventSessionID);
          }
          await applyPendingReasoning(pendingReasoning);
          void refreshProjectsAndSessions();
        })();
        break;
      }
      case "failed": {
        setQueuedMessages(clearGuidanceMessages);
        const message = chatFailureMessage(event.message || "模型请求失败。");
        const result = event.result;
        const retainedTurn = hasMeaningfulTurnOutput(result, eventTask?.assistantMessage);
        if (result) {
          setState(result.state);
        }
        setError(retainedTurn ? "" : message);
        if (!retainedTurn) {
          rollbackOptimisticTurn(eventProjectID, eventSessionID);
        }
        if (retainedTurn) {
          updateStreamingMessage((current) => ({
            ...current,
            content: result?.content || current.content || message,
            reasoning: result?.reasoning || current.reasoning,
            model: result?.model || current.model,
            usage: result?.usage || current.usage,
            parts: settleLiveProgress(result?.parts?.length ? result.parts : current.parts, "failed"),
            durationMs: result?.durationMs ?? current.durationMs,
            failed: true,
            cancelled: false,
            streaming: false,
            status: undefined,
          }));
        }
        const resultProgress = findTaskProgress(result?.parts);
        if (resultProgress) {
          updateSessionTaskProgress(eventProjectID, eventSessionID, {
            ...cloneTaskProgress(resultProgress),
            taskStatus: "failed",
          });
        } else {
          settleActiveTaskProgress("failed");
        }
		const pendingReasoning = finishChatTask(eventProjectID, eventSessionID, event.taskId);
		void (async () => {
		  if (eventProjectID === activeProjectID() && eventSessionID === activeSessionID()) {
			await restoreSessionMessages(true, eventProjectID, eventSessionID);
		  }
		  await applyPendingReasoning(pendingReasoning);
		  void refreshProjectsAndSessions();
		})();
        break;
      }
    }
  };

  const clearUpdateBannerTimer = () => {
    if (updateBannerTimer !== undefined) {
      window.clearInterval(updateBannerTimer);
      updateBannerTimer = undefined;
    }
  };

  const dismissUpdateBanner = () => {
    clearUpdateBannerTimer();
    setUpdateBannerVisible(false);
  };

  const startUpdateBannerCountdown = () => {
    clearUpdateBannerTimer();
    const durationMs = 8_000;
    updateBannerDeadline = Date.now() + durationMs;
    setUpdateBannerProgress(1);
    updateBannerTimer = window.setInterval(() => {
      const remaining = Math.max(0, updateBannerDeadline - Date.now());
      setUpdateBannerProgress(remaining / durationMs);
      if (remaining === 0) dismissUpdateBanner();
    }, 80);
  };

  const presentUpdateBanner = (next: UpdateState) => {
    setUpdateBannerState(next);
    const status = next.status?.trim().toLowerCase() ?? "";
    if (!updateBannerVisibleStatuses.has(status)) {
      updateBannerSignature = "";
      dismissUpdateBanner();
      return;
    }
    const signature = [status, next.latestVersion ?? "", next.message ?? ""].join("\u0000");
    if (signature === updateBannerSignature) return;
    updateBannerSignature = signature;
    setUpdateBannerVisible(true);
    if (["downloading", "installing"].includes(status)) {
      clearUpdateBannerTimer();
      setUpdateBannerProgress(1);
      return;
    }
    startUpdateBannerCountdown();
  };

  const updateBannerBusy = createMemo(() => ["downloading", "installing"].includes(updateBannerState()?.status ?? ""));
  const updateBannerTitle = createMemo(() => {
    const update = updateBannerState();
    switch (update?.status) {
      case "checking": return "正在检查更新";
      case "available": return `发现新版本 ${update.latestVersion || ""}`.trim();
      case "downloading": return `正在下载 ${update.latestVersion || "更新"}`;
      case "downloaded": return `${update.latestVersion || "新版本"} 已下载`;
      case "installing": return "正在准备安装";
      case "current": return "EchoCode 已是最新版本";
      case "error": return "自动更新检查失败";
      default: return "软件更新";
    }
  });

  onMount(() => {
    applyThemeMode(themeMode());
    applySidebarWidth(sidebarWidth(), shellRef);
    syncUIAppearance();
    void (async () => {
      await recoverActiveChatTasks();
      await refreshState();
      await refreshProjectsAndSessions();
      await restoreSessionMessages();
      activateSessionTask(activeProjectID(), activeSessionID());
    })();
    // 订阅后端审批请求，入队等待用户处理。
    const unsubscribe = onApprovalRequest((req) => {
      setApprovalQueue((queue) => [...queue, req]);
    });
    const unsubscribeBrowserOpen = onBrowserPreviewOpen((preview) => {
      void handleBrowserPreviewRequest(preview);
    });
    const unsubscribeBrowserClose = onBrowserPreviewClose(closeBrowserPanel);
    const unsubscribeChatTask = onChatTaskEvent(handleChatTaskEvent);
    const unsubscribeMCPState = onMCPState(setState);
    const unsubscribeUpdateState = onUpdateState(presentUpdateBanner);
    void getUpdateState().then(presentUpdateBanner).catch(() => undefined);
    const closeActionMenusOnOutsidePress = (event: PointerEvent) => {
      const target = event.target;
      if (projectMenu()) {
        if (!(target instanceof Node && projectActionMenuRef?.contains(target))
          && !(target instanceof Element && target.closest("[data-project-menu-trigger]"))) {
          setProjectMenu(undefined);
        }
      }
      if (sessionMenu()) {
        if (!(target instanceof Node && sessionActionMenuRef?.contains(target))
          && !(target instanceof Element && target.closest("[data-session-menu-target]"))) {
          setSessionMenu(undefined);
        }
      }
    };
    const closeProjectUIOnEscape = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return;
      if (sessionRenameDialog() && !sessionActionBusyID()) {
        setSessionRenameDialog(undefined);
        return;
      }
      if (projectDialog() && !projectActionBusy()) {
        setProjectDialog(undefined);
        return;
      }
      setSessionMenu(undefined);
      setProjectMenu(undefined);
    };
    window.addEventListener("pointerdown", closeActionMenusOnOutsidePress);
    window.addEventListener("keydown", closeProjectUIOnEscape);
    onCleanup(unsubscribe);
    onCleanup(unsubscribeBrowserOpen);
    onCleanup(unsubscribeBrowserClose);
    onCleanup(unsubscribeChatTask);
    onCleanup(unsubscribeMCPState);
    onCleanup(unsubscribeUpdateState);
    onCleanup(clearUpdateBannerTimer);
    onCleanup(() => window.removeEventListener("pointerdown", closeActionMenusOnOutsidePress));
    onCleanup(() => window.removeEventListener("keydown", closeProjectUIOnEscape));
  });

  // 当前正在处理的审批请求（队首）。
  const activeApproval = createMemo(() => approvalQueue()[0]);
	const browserSurfaceSuspended = createMemo(() => Boolean(
		drawerOpen()
		|| timelineOpen()
		|| activeApproval()
		|| confirmation()
		|| projectDialog()
		|| sessionRenameDialog()
		|| pendingLinkURL()
		|| previewAttachment()
	));

  // 用户对审批弹窗的决定。
  const decideApproval = async (approved: boolean, scope: "once" | "session") => {
    const req = activeApproval();
    if (!req) {
      return;
    }
    setApprovalBusy(true);
    try {
      await respondApproval(req.id, req.tool, approved, scope);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setApprovalBusy(false);
      setApprovalQueue((queue) => queue.slice(1));
    }
  };

  createEffect(() => {
    const mode = themeMode();
    applyThemeMode(mode, shellRef);
  });

  createEffect(() => {
    applySidebarWidth(sidebarWidth(), shellRef);
  });

  createEffect(() => {
    uiAppearance();
    syncUIAppearance();
  });

  createEffect(() => {
    messages();
    currentSessionBusy();
    const shouldFollow = chatNearBottom();
    queueMicrotask(() => {
      if (shouldFollow) {
        scrollChatToBottom("auto");
      } else {
        updateChatScrollState();
      }
    });
  });

  const refreshState = async () => {
    setLoading(true);
    setError("");
    try {
      setState(await getWorkbenchState());
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setLoading(false);
    }
  };

  const changeReasoning = async (level: ReasoningLevel) => {
    if (currentSessionBusy()) {
      setPendingReasoningLevel(level);
      setError("");
      return;
    }
    setPendingReasoningLevel(undefined);
    setUpdatingReasoning(true);
    setError("");
    try {
      setState(await setReasoningLevel(level));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setUpdatingReasoning(false);
    }
  };

  const saveKey = async () => {
    setSavingKey(true);
    setError("");
    try {
      setState(await saveDeepSeekAPIKey(apiKeyDraft()));
      setAPIKeyDraft("");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSavingKey(false);
    }
  };

  const testConnection = async () => {
    setTestingDeepSeek(true);
    setError("");
    try {
      setState(await testDeepSeekConnection());
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setTestingDeepSeek(false);
    }
  };

  const clearKey = async () => {
    setClearingKey(true);
    setError("");
    try {
      setState(await clearDeepSeekAPIKey());
      setAPIKeyDraft("");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setClearingKey(false);
    }
  };

  const setProviderKeyDraft = (providerID: string, value: string) => {
    setProviderKeyDrafts((current) => ({
      ...current,
      [providerID]: value,
    }));
  };

  const setProviderBillingKeyDraft = (providerID: string, value: string) => {
    setProviderBillingKeyDrafts((current) => ({
      ...current,
      [providerID]: value,
    }));
  };

  const saveProviderKey = async (providerID: string) => {
    const apiKey = providerKeyDrafts()[providerID]?.trim() ?? "";
    if (!apiKey) {
      setError("请先填写 API Key。");
      return;
    }
    setSavingProviderID(providerID);
    setError("");
    try {
      if (runtimeDraft()) {
        setState(await saveRuntimeSettings(activeRuntimeDraft()));
        setRuntimeDraft(undefined);
      }
      setState(await saveModelProviderAPIKey(providerID, apiKey));
      setProviderKeyDraft(providerID, "");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSavingProviderID("");
    }
  };

  const clearProviderKey = async (providerID: string) => {
    setClearingProviderID(providerID);
    setError("");
    try {
      if (runtimeDraft()) {
        setState(await saveRuntimeSettings(activeRuntimeDraft()));
        setRuntimeDraft(undefined);
      }
      setState(await clearModelProviderAPIKey(providerID));
      setProviderKeyDraft(providerID, "");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setClearingProviderID("");
    }
  };

  const saveProviderBillingKey = async (providerID: string) => {
    const apiKey = providerBillingKeyDrafts()[providerID]?.trim() ?? "";
    if (!apiKey) {
      setError("请先填写官方账单读取凭据。");
      return;
    }
    setSavingProviderBillingID(providerID);
    setError("");
    try {
      if (runtimeDraft()) {
        setState(await saveRuntimeSettings(activeRuntimeDraft()));
        setRuntimeDraft(undefined);
      }
      setState(await saveModelProviderBillingAPIKey(providerID, apiKey));
      setProviderBillingKeyDraft(providerID, "");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSavingProviderBillingID("");
    }
  };

  const clearProviderBillingKey = async (providerID: string) => {
    setClearingProviderBillingID(providerID);
    setError("");
    try {
      if (runtimeDraft()) {
        setState(await saveRuntimeSettings(activeRuntimeDraft()));
        setRuntimeDraft(undefined);
      }
      setState(await clearModelProviderBillingAPIKey(providerID));
      setProviderBillingKeyDraft(providerID, "");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setClearingProviderBillingID("");
    }
  };

  const rememberUsageBillingReport = (report: UsageBillingReport) => {
    if (!report.providerId) return;
    setUsageBillingReports((current) => ({ ...current, [report.providerId]: report }));
  };

  const loadUsageBillingReport = async (providerID: string) => {
    if (!providerID) return;
    try {
      rememberUsageBillingReport(await getUsageBillingReport(providerID));
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const syncUsageBillingReport = async (input: UsageBillingSyncInput) => {
    setError("");
    try {
      rememberUsageBillingReport(await syncUsageBilling(input));
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const reconcileUsageBillingReport = async (input: UsageBillingReconciliationInput) => {
    setError("");
    try {
      rememberUsageBillingReport(await reconcileUsageBilling(input));
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const removeModelProvider = async (providerID: string) => {
    const provider = activeRuntimeDraft().model.providers.find((item) => item.id === providerID);
    if (!provider) return;
    const confirmed = await confirmAction({
      title: "删除模型供应商？",
      message: `“${provider.name}”将从模型配置中移除。`,
      detail: "对应的本地 API Key 也会被清除，此操作无法撤销。",
      confirmLabel: "删除",
      tone: "danger",
    });
    if (!confirmed) return;
    setDeletingProviderID(providerID);
    setError("");
    try {
      if (runtimeDraft()) {
        setState(await saveRuntimeSettings(activeRuntimeDraft()));
        setRuntimeDraft(undefined);
      }
      setState(await deleteModelProvider(providerID));
      setProviderKeyDrafts((current) => {
        const next = { ...current };
        delete next[providerID];
        return next;
      });
      setProviderBillingKeyDrafts((current) => {
        const next = { ...current };
        delete next[providerID];
        return next;
      });
      setUsageBillingReports((current) => {
        const next = { ...current };
        delete next[providerID];
        return next;
      });
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setDeletingProviderID("");
    }
  };

  const syncProviderModels = async (providerID: string) => {
    setSyncingProviderID(providerID);
    setError("");
    try {
      if (runtimeDraft()) {
        setState(await saveRuntimeSettings(activeRuntimeDraft()));
        setRuntimeDraft(undefined);
      }
      const apiKey = providerKeyDrafts()[providerID]?.trim() ?? "";
      if (apiKey) {
        setState(await saveModelProviderAPIKey(providerID, apiKey));
        setProviderKeyDraft(providerID, "");
      }
      setState(await refreshModelProviderModels(providerID));
      setRuntimeDraft(undefined);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSyncingProviderID("");
    }
  };

  const refreshMCPRuntime = async (serverID: string) => {
    setRefreshingMCPID(serverID);
    setError("");
    try {
      if (runtimeDraft()) {
        setState(await saveRuntimeSettings(activeRuntimeDraft()));
        setRuntimeDraft(undefined);
      }
      setState(await refreshMCPServer(serverID));
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setRefreshingMCPID("");
    }
  };

	const refreshPluginRuntime = async () => {
		setPluginBusy("refresh");
		setError("");
		try {
			if (runtimeDraft()) {
				setState(await saveRuntimeSettings(activeRuntimeDraft()));
				setRuntimeDraft(undefined);
			}
			setState(await refreshPlugins());
		} catch (err) {
			setError(errorMessage(err));
		} finally {
			setPluginBusy("");
		}
	};

	const installLocalPlugin = async () => {
		setPluginBusy("install");
		setError("");
		try {
			const source = await selectPluginDirectory();
			if (!source.trim()) return;
			if (runtimeDraft()) {
				setState(await saveRuntimeSettings(activeRuntimeDraft()));
				setRuntimeDraft(undefined);
			}
			setState(await installPlugin(source));
		} catch (err) {
			setError(errorMessage(err));
		} finally {
			setPluginBusy("");
		}
	};

  const importUserSkillMarkdown = async (fileName: string, content: string): Promise<SkillImportResult> => {
    setError("");
    const result = await importSkillMarkdown(fileName, content);
    setState(result.state);
    return result;
  };

	const removeInstalledPlugin = async (id: string) => {
		setPluginBusy(`uninstall:${id}`);
		setError("");
		try {
			if (runtimeDraft()) {
				setState(await saveRuntimeSettings(activeRuntimeDraft()));
				setRuntimeDraft(undefined);
			}
			setState(await uninstallPlugin(id));
		} catch (err) {
			setError(errorMessage(err));
		} finally {
			setPluginBusy("");
		}
	};

	const openInstalledPlugin = async (id: string) => {
		setPluginBusy(`reveal:${id}`);
		setError("");
		try {
			await revealPlugin(id);
		} catch (err) {
			setError(errorMessage(err));
		} finally {
			setPluginBusy("");
		}
	};

  const resetSession = async () => {
    setResettingSession(true);
    setError("");
    try {
      setState(await resetDeepSeekSession());
      setMessages([]);
      setActiveTaskProgress(undefined);
      setQueuedMessages([]);
      setComposerDraft("");
      setComposerTail("");
      setComposerAttachments([]);
      setComposerLinks([]);
      resetComposerHistory();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setResettingSession(false);
    }
  };

  const saveRuntime = async () => {
    setSavingRuntime(true);
    setError("");
    try {
      let next = await saveRuntimeSettings(activeRuntimeDraft());
      const pendingProviderKeys = Object.entries(providerKeyDrafts()).filter(([, apiKey]) => apiKey.trim());
      const pendingProviderBillingKeys = Object.entries(providerBillingKeyDrafts()).filter(([, apiKey]) => apiKey.trim());
      const savedProviderIDs: string[] = [];
      const savedProviderBillingIDs: string[] = [];
      for (const [providerID, apiKey] of pendingProviderKeys) {
        next = await saveModelProviderAPIKey(providerID, apiKey.trim());
        savedProviderIDs.push(providerID);
      }
      for (const [providerID, apiKey] of pendingProviderBillingKeys) {
        next = await saveModelProviderBillingAPIKey(providerID, apiKey.trim());
        savedProviderBillingIDs.push(providerID);
      }
      setState(next);
      setRuntimeDraft(undefined);
      if (savedProviderIDs.length > 0) {
        setProviderKeyDrafts((current) => {
          const copy = { ...current };
          for (const providerID of savedProviderIDs) {
            delete copy[providerID];
          }
          return copy;
        });
      }
      if (savedProviderBillingIDs.length > 0) {
        setProviderBillingKeyDrafts((current) => {
          const copy = { ...current };
          for (const providerID of savedProviderBillingIDs) {
            delete copy[providerID];
          }
          return copy;
        });
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSavingRuntime(false);
    }
  };

  const selectModelRoute = async (providerID: string, modelID: string) => {
    const base = activeRuntimeDraft();
    const provider = base.model.providers.find((item) => item.id === providerID);
    if (!provider) {
      setError("未找到模型供应商。");
      return;
    }
    const nextSettings: RuntimeSettings = {
      ...base,
      model: {
        ...base.model,
        selectedProviderId: providerID,
        selectedModelId: modelID,
        providers: base.model.providers.map((item) =>
          item.id === providerID
            ? {
                ...item,
                enabled: true,
                defaultModelId: modelID || item.defaultModelId,
              }
            : item,
        ),
      },
    };
    setSavingRuntime(true);
    setError("");
    try {
      const next = await saveRuntimeSettings(nextSettings);
      setState(next);
      setRuntimeDraft(undefined);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSavingRuntime(false);
    }
  };

  const updateRuntimeDraft = (patch: Partial<RuntimeSettings>) => {
    setRuntimeDraft((current) => ({
      ...runtimeSettings(),
      ...current,
      ...patch,
    }));
  };

  const resetRuntimeDraft = () => {
    setRuntimeDraft(undefined);
    setProviderKeyDrafts({});
  };

  const setComposerDraft = (value: string) => {
    setPromptDraft(value);
	const editable = composerEditableText(value, composerTailDraft());
	if (composerEditorRef && composerEditorRef.value !== editable) {
	  composerEditorRef.value = editable;
	  resizeComposerEditor(composerEditorRef);
    }
  };

  const setComposerTail = (value: string) => {
    setComposerTailDraftSignal(value);
	const editable = composerEditableText(promptDraft(), value);
	if (composerEditorRef && composerEditorRef.value !== editable) {
	  composerEditorRef.value = editable;
	  resizeComposerEditor(composerEditorRef);
    }
  };

  const currentComposerSnapshot = (): ComposerSnapshot => ({
    draft: promptDraft(),
    tail: composerTailDraft(),
    attachments: composerAttachments().map((attachment) => ({ ...attachment })),
    links: composerLinks().map((link) => ({ ...link })),
  });

  const syncComposerHistoryDepth = () => {
    setComposerUndoDepth(composerHistory.past.length);
    setComposerRedoDepth(composerHistory.future.length);
  };

  const resetComposerHistory = () => {
    composerHistory = emptyComposerHistory();
    syncComposerHistoryDepth();
  };

  const applyComposerSnapshot = (snapshot: ComposerSnapshot) => {
    setComposerDraft(snapshot.draft);
    setComposerTail(snapshot.tail);
    setComposerAttachments(snapshot.attachments.map((attachment) => ({ ...attachment })));
    setComposerLinks(snapshot.links.map((link) => ({ ...link })));
  };

  const commitComposerSnapshot = (snapshot: ComposerSnapshot) => {
    const current = currentComposerSnapshot();
    if (composerSnapshotsEqual(current, snapshot)) return;
    composerHistory = recordComposerSnapshot(composerHistory, current);
    syncComposerHistoryDepth();
    applyComposerSnapshot(snapshot);
  };

  const focusComposerAfterHistoryMove = () => {
    queueMicrotask(() => {
	  const editor = composerEditorRef;
      editor?.focus();
      if (editor) placeCaretAtEnd(editor);
    });
  };

  const undoComposerInput = () => {
    const move = undoComposerSnapshot(composerHistory, currentComposerSnapshot());
    composerHistory = move.history;
    syncComposerHistoryDepth();
    if (!move.snapshot) return;
    applyComposerSnapshot(move.snapshot);
    focusComposerAfterHistoryMove();
  };

  const redoComposerInput = () => {
    const move = redoComposerSnapshot(composerHistory, currentComposerSnapshot());
    composerHistory = move.history;
    syncComposerHistoryDepth();
    if (!move.snapshot) return;
    applyComposerSnapshot(move.snapshot);
    focusComposerAfterHistoryMove();
  };

  const focusComposerEnd = () => {
	const editor = composerEditorRef;
    editor?.focus();
    if (editor) placeCaretAtEnd(editor);
  };

  const primeWelcomePrompt = (prompt: string) => {
    setComposerDraft(prompt);
    setComposerTail("");
    setComposerLinks([]);
    resetComposerHistory();
    queueMicrotask(focusComposerEnd);
  };

  const removeComposerLink = (url: string) => {
    const current = currentComposerSnapshot();
    const links = current.links.filter((link) => link.url !== url);
    let draft = current.draft;
    let tail = current.tail;
    if (links.length === 0 && tail.trim()) {
      draft = [draft.trim(), tail.trim()].filter(Boolean).join(" ");
      tail = "";
    }
    commitComposerSnapshot({ ...current, draft, tail, links });
    if (links.length > 0) return;
    queueMicrotask(() => {
      composerEditorRef?.focus();
      if (composerEditorRef) placeCaretAtEnd(composerEditorRef);
    });
  };

	const absorbComposerURLs = (value: string) => {
    const links = extractComposerLinks(value);
    if (links.length === 0) return false;
	const current = currentComposerSnapshot();
    const text = removeComposerURLs(value, links);
	commitComposerSnapshot({
	  ...current,
	  draft: text,
	  tail: "",
	  links: mergeComposerLinks(current.links, links),
	});
    queueMicrotask(() => {
	  const editor = composerEditorRef;
      editor?.focus();
      if (editor) placeCaretAtEnd(editor);
    });
    return true;
  };

  const addComposerImages = async (files: File[]) => {
    if (files.length === 0 || addingImages()) return;
    const imageCount = composerAttachments().filter(isImageChatAttachment).length;
    const available = 4 - imageCount;
    if (available <= 0) {
      setError("一次最多添加 4 张图片。");
      return;
    }
    setAddingImages(true);
    setError("");
    try {
      const selected = files.slice(0, available);
      const next = await Promise.all(selected.map((file, index) => readChatImage(file, composerAttachments().length + index)));
      const combined = [...composerAttachments(), ...next];
      const totalBytes = combined
        .filter(isImageChatAttachment)
        .reduce((sum, attachment) => sum + approximateBase64Bytes(attachment.data), 0);
      if (totalBytes > 12 * 1024 * 1024) {
        throw new Error("图片总大小不能超过 12 MB。");
      }
      commitComposerSnapshot({ ...currentComposerSnapshot(), attachments: combined });
      if (files.length > available) setError("一次最多添加 4 张图片，其余图片未加入。");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setAddingImages(false);
      if (composerImageInputRef) composerImageInputRef.value = "";
    }
  };

  const addComposerMarkdown = async (files: File[]) => {
    if (files.length === 0 || addingDocuments()) return;
    const documentCount = composerAttachments().filter(isMarkdownChatAttachment).length;
    const available = 5 - documentCount;
    if (available <= 0) {
      setError("一次最多添加 5 个 Markdown 文件。");
      return;
    }
    setAddingDocuments(true);
    setError("");
    try {
      const selected = files.slice(0, available);
      const next = await Promise.all(selected.map((file, index) => readChatMarkdown(file, documentCount + index)));
      const combined = [...composerAttachments(), ...next];
      const totalBytes = combined
        .filter(isMarkdownChatAttachment)
        .reduce((sum, attachment) => sum + (attachment.size ?? approximateBase64Bytes(attachment.data)), 0);
      if (totalBytes > 256 * 1024) {
        throw new Error("Markdown 文件总大小不能超过 256 KiB。");
      }
      commitComposerSnapshot({ ...currentComposerSnapshot(), attachments: combined });
      if (files.length > available) setError("一次最多添加 5 个 Markdown 文件，其余文件未加入。");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setAddingDocuments(false);
      if (composerMarkdownInputRef) composerMarkdownInputRef.value = "";
    }
  };

	const handleComposerPaste = (event: ClipboardEvent) => {
	  const pastedFiles = Array.from(event.clipboardData?.items ?? [])
	    .filter((item) => item.kind === "file")
	    .map((item) => item.getAsFile())
	    .filter((file): file is File => Boolean(file));
    const files = pastedFiles.filter((file) => file.type.startsWith("image/"));
    const documents = pastedFiles.filter(isMarkdownFile);
    const pastedText = event.clipboardData?.getData("text/plain") ?? "";
    const links = extractComposerLinks(pastedText);
    if (files.length === 0 && documents.length === 0 && links.length === 0) return;
    event.preventDefault();
    if (files.length > 0) void addComposerImages(files);
    if (documents.length > 0) void addComposerMarkdown(documents);
    const remainingText = links.length > 0 ? removeComposerURLs(pastedText, links) : pastedText;
	const editor = composerEditorRef;
    if (remainingText) insertTextAtSelection(editor, remainingText);
	if (editor) {
	  const current = currentComposerSnapshot();
	  commitComposerSnapshot({
		...current,
		draft: editor.value,
		tail: "",
		links: mergeComposerLinks(current.links, links),
	  });
	  resizeComposerEditor(editor);
    }
    if (links.length > 0) queueMicrotask(focusComposerEnd);
  };

  const sendPrompt = async (rawPrompt: string, attachmentOverride?: ChatAttachment[], linkOverride?: ComposerLink[], tailOverride?: string) => {
    const draft = rawPrompt.trim();
    const tail = (tailOverride ?? composerTailDraft()).trim();
    const attachments = (attachmentOverride ?? composerAttachments()).map((attachment) => ({ ...attachment }));
    const links = (linkOverride ?? composerLinks()).map((link) => ({ ...link }));
    const prompt = composeComposerPrompt(draft, links, tail);
    const projectID = activeProjectID();
    const sessionID = activeSessionID();
    if (!prompt && attachments.length === 0) {
      return;
    }
    if (!projectID || !sessionID) {
      setError("当前没有可用会话，请先新建或选择一个会话");
      return;
    }
    if (isSessionBusy(projectID, sessionID)) {
      return;
    }
    if (!activeProviderReady() || !activeChatModel()) {
      setError("请先在模型设置里完成当前供应商的密钥和模型配置。");
      openSettings("models");
      return;
    }

    const assistantMessageID = `assistant-stream-${Date.now()}`;
    const optimisticUserMessageID = `user-optimistic-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`;
    const taskStartedAt = new Date().toISOString();
    const assistantMessage: ChatMessage = {
      id: assistantMessageID,
      role: "assistant",
      content: "",
      createdAt: taskStartedAt,
      model: activeChatModel(),
      streaming: true,
      status: "正在执行任务",
    };
    setActiveTaskProgress(undefined);
    setChatNearBottom(true);
    setMessages((current) => [
      ...current,
      { ...createChatMessage("user", redactSensitiveTextForDisplay(prompt)), id: optimisticUserMessageID, attachments },
      assistantMessage,
    ]);
    setComposerDraft("");
    setComposerTail("");
    setComposerAttachments([]);
    setComposerLinks([]);
    resetComposerHistory();
    setStreamingMessageID(assistantMessageID);
    setSendingMessage(true);
    const sessionKey = sessionIdentityKey(projectID, sessionID);
    setSessionTaskRuntimes((current) => ({
      ...current,
      [sessionKey]: {
        taskID: "",
        projectID,
        sessionID,
        userMessageID: optimisticUserMessageID,
        messageID: assistantMessageID,
        prompt: draft,
        tail,
        attachments,
        links,
        startedAt: taskStartedAt,
        assistantMessage,
      },
    }));
    setError("");
    let taskID = "";
    try {
      taskID = await startChatMessageForSession(projectID, sessionID, prompt, attachments);
      let registered = false;
      setSessionTaskRuntimes((current) => {
        const task = current[sessionKey];
        if (!task) return current;
        registered = true;
        return { ...current, [sessionKey]: { ...task, taskID } };
      });
      if (registered && projectID === activeProjectID() && sessionID === activeSessionID()) {
        setActiveChatTaskID(taskID);
      }
    } catch (err) {
      const message = errorMessage(err);
      if (projectID === activeProjectID() && sessionID === activeSessionID()) {
        setError(message);
      }
      rollbackOptimisticTurn(projectID, sessionID);
      void applyPendingReasoning(finishChatTask(projectID, sessionID, taskID));
    }
  };

	const resumePausedTeamRun = async () => {
		const projectID = activeProjectID();
		const sessionID = activeSessionID();
		if (teamLifecycleBusy() || currentSessionBusy()) return;
		if (!projectID || !sessionID) {
			setError("当前没有可继续团队任务的会话。");
			return;
		}

		const taskStartedAt = new Date().toISOString();
		const assistantMessage: ChatMessage = {
			id: `assistant-team-resume-${Date.now()}`,
			role: "assistant",
			content: "",
			createdAt: taskStartedAt,
			model: activeChatModel(),
			streaming: true,
			status: "正在恢复 AI 团队任务",
		};
		const sessionKey = sessionIdentityKey(projectID, sessionID);
		setActiveTaskProgress(undefined);
		setChatNearBottom(true);
		setMessages((current) => [...current, assistantMessage]);
		setStreamingMessageID(assistantMessage.id);
		setSendingMessage(true);
		setSessionTaskRuntimes((current) => ({
			...current,
			[sessionKey]: {
				taskID: "",
				projectID,
				sessionID,
				userMessageID: "",
				messageID: assistantMessage.id,
				prompt: "",
				tail: "",
				attachments: [],
				links: [],
				startedAt: taskStartedAt,
				assistantMessage,
			},
		}));
		setError("");

		let taskID = "";
		try {
			taskID = await resumePausedTeamTask(projectID, sessionID);
			let registered = false;
			setSessionTaskRuntimes((current) => {
				const task = current[sessionKey];
				if (!task) return current;
				registered = true;
				return { ...current, [sessionKey]: { ...task, taskID } };
			});
			if (registered && projectID === activeProjectID() && sessionID === activeSessionID()) {
				setActiveChatTaskID(taskID);
			}
		} catch (err) {
			const message = errorMessage(err);
			if (projectID === activeProjectID() && sessionID === activeSessionID()) {
				setError(message);
				setMessages((current) => current.filter((item) => item.id !== assistantMessage.id));
				setSendingMessage(false);
				setActiveChatTaskID("");
				setStreamingMessageID("");
			}
			setSessionTaskRuntimes((current) => {
				const next = { ...current };
				delete next[sessionKey];
				return next;
			});
		}
	};

	const abandonPausedTeamRun = async () => {
		const projectID = activeProjectID();
		const sessionID = activeSessionID();
		if (teamLifecycleBusy() || currentSessionBusy()) return;
		if (!projectID || !sessionID) {
			setError("当前没有可结束团队任务的会话。");
			return;
		}
		const confirmed = await confirmAction({
			title: "结束 AI 团队任务",
			message: "已完成的角色结果和文件修改会保留，但该团队任务将不能再继续。",
			confirmLabel: "结束任务",
			cancelLabel: "取消",
			tone: "danger",
		});
		if (!confirmed) return;
		setTeamLifecycleBusy(true);
		setError("");
		try {
			const next = await abandonPausedTeamTask(projectID, sessionID);
			setState(next);
			await restoreSessionMessages(false, projectID, sessionID);
			void refreshProjectsAndSessions();
		} catch (err) {
			setError(errorMessage(err));
		} finally {
			setTeamLifecycleBusy(false);
		}
	};

  const enqueueCurrentMessage = (guidance = false) => {
    const draft = promptDraft().trim();
    const tail = composerTailDraft().trim();
    const attachments = composerAttachments().map((attachment) => ({ ...attachment }));
    const links = composerLinks().map((link) => ({ ...link }));
    const prompt = composeComposerPrompt(draft, links, tail);
    if (!prompt && attachments.length === 0) return;
    const queued: QueuedComposerMessage = {
      id: `queued-${Date.now()}-${Math.random().toString(36).slice(2, 8)}`,
      draft,
      tail,
      attachments,
      links,
      createdAt: new Date().toISOString(),
      guidance,
    };
    setQueuedMessages((current) => enqueueMessage(current, queued));
    setComposerDraft("");
    setComposerTail("");
    setComposerAttachments([]);
    setComposerLinks([]);
    resetComposerHistory();
    setError("");
    queueMicrotask(() => composerEditorRef?.focus());
  };

  const startNextQueuedMessage = async () => {
    if (currentSessionBusy()) return;
    const dequeued = dequeueMessage(queuedMessages());
    if (!dequeued.message) return;
    setQueuedMessages(dequeued.queue);
    await sendPrompt(dequeued.message.draft, dequeued.message.attachments, dequeued.message.links, dequeued.message.tail);
  };

  const guideQueuedMessage = async (id: string) => {
    const queued = queuedMessages().find((message) => message.id === id);
    if (!queued) return;
    const pendingGuidance = queuedMessages().find((message) => message.guidance && message.id !== id);
    if (pendingGuidance) {
      setError("已有一条引导等待当前 Agent 应用，请稍后再引导下一条。");
      return;
    }
    setQueuedMessages((current) => prioritizeMessage(current, id));
    const taskID = activeChatTaskID();
    if (!currentSessionBusy() || !taskID) {
      setQueuedMessages(clearGuidanceMessages);
      return;
    }
    try {
      const accepted = await guideChatMessage(
        taskID,
        queued.id,
        composeComposerPrompt(queued.draft, queued.links, queued.tail),
        queued.attachments,
      );
      if (!accepted) {
        setQueuedMessages(clearGuidanceMessages);
      }
    } catch (err) {
      setQueuedMessages(clearGuidanceMessages);
      setError(errorMessage(err));
    }
  };

  const editQueuedMessage = (id: string) => {
    const editing = takeMessageForEditing(queuedMessages(), id);
    if (!editing.message) return;
    setQueuedMessages(editing.queue);
    setComposerDraft(editing.message.draft);
    setComposerTail(editing.message.tail);
    setComposerAttachments(editing.message.attachments);
    setComposerLinks(editing.message.links);
    resetComposerHistory();
    queueMicrotask(focusComposerEnd);
  };

  const sendMessage = async () => {
    if (currentSessionBusy()) {
      enqueueCurrentMessage();
      return;
    }
    await sendPrompt(promptDraft());
  };

  const stopMessage = async () => {
    const projectID = activeProjectID();
    const sessionID = activeSessionID();
    const taskID = activeChatTaskID();
    if (!sessionID || !taskID) {
      return;
    }
    updateStreamingMessage((message) => ({ ...message, status: "正在停止" }));
    try {
      const accepted = await stopChatMessage(taskID);
      if (accepted) {
        // The terminal event is authoritative: it knows whether text, reasoning
        // or tool activity must be retained. Do not erase the turn here.
        return;
      }

      const activeTasks = await getActiveChatTasks();
      const stillRunning = activeTasks.some((task) => task.taskId === taskID || (task.projectId === projectID && task.sessionId === sessionID));
      if (stillRunning) {
        setError("停止请求未被当前任务接受，请重试。");
        return;
      }
      const pendingReasoning = finishChatTask(projectID, sessionID, taskID);
      await restoreSessionMessages(true, projectID, sessionID);
      await applyPendingReasoning(pendingReasoning);
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  // 拉取项目与会话列表（含 Codex 式项目树）。
  const refreshProjectsAndSessions = async () => {
    try {
      const [projs, sess, tree] = await Promise.all([listProjects(), listSessions(), getProjectTree()]);
      setProjects(projs);
      setSessions(sess);
      setProjectTree(tree);
      const activeProject = tree.find((project) => project.isActive);
      const activeSession = activeProject?.sessions.find((session) => session.isActive);
      if (activeSession) setSelectedSessionID(activeSession.id);
    } catch {
      // 辅助功能失败不打断主流程。
    }
  };

  // 相对时间：5小时 / 2天 / 刚刚（Codex 式）。
  const relativeTime = (iso: string): string => {
    const t = new Date(iso).getTime();
    if (Number.isNaN(t)) return "";
    const diff = Date.now() - t;
    const min = Math.floor(diff / 60000);
    if (min < 1) return "刚刚";
    if (min < 60) return `${min}分钟`;
    const hr = Math.floor(min / 60);
    if (hr < 24) return `${hr}小时`;
    const day = Math.floor(hr / 24);
    if (day < 30) return `${day}天`;
    const mon = Math.floor(day / 30);
    if (mon < 12) return `${mon}个月`;
    return `${Math.floor(mon / 12)}年`;
  };

  // 从后端事件日志恢复当前会话的历史消息到前端（修复关闭打开对话消失）。
  let sessionRestoreGeneration = 0;
  const restoreSessionMessages = async (
    preserveCurrentOnEmpty = false,
    projectID = activeProjectID(),
    sessionID = activeSessionID(),
  ) => {
    if (!projectID || !sessionID) return;
    const generation = ++sessionRestoreGeneration;
    try {
      const history = await getSessionMessagesForSession(projectID, sessionID);
      if (generation !== sessionRestoreGeneration || projectID !== activeProjectID() || sessionID !== activeSessionID()) return;
      setMessages((current) => reconcileSessionMessages(current, history, preserveCurrentOnEmpty));
    } catch (err) {
      // Lookup failures preserve the current UI. They must never masquerade as
      // a valid empty conversation and erase visible history.
      if (generation === sessionRestoreGeneration) setError(errorMessage(err));
    }
  };

  // 当前活动会话内容需在切换后重载：从后端事件日志恢复消息。
  const activateSessionTask = (projectID: string, sessionID: string) => {
    const task = sessionTaskRuntimes()[sessionIdentityKey(projectID, sessionID)];
    if (!task) {
      setSendingMessage(false);
      setActiveChatTaskID("");
      setStreamingMessageID("");
      return;
    }
    setSendingMessage(true);
    setActiveChatTaskID(task.taskID);
    setStreamingMessageID(task.messageID);
    setActiveTaskProgress(task.progress ? cloneTaskProgress(task.progress) : undefined);
    setMessages((current) => {
      const composed = redactSensitiveTextForDisplay(composeComposerPrompt(task.prompt, task.links, task.tail));
      const hasUser = Boolean(task.userMessageID) && current.some((message) => message.id === task.userMessageID);
      const withUser = [
        ...current,
        ...(!composed || hasUser ? [] : [{ ...createChatMessage("user", composed), id: task.userMessageID, attachments: task.attachments }]),
      ];
      const existingIndex = withUser.findIndex((message) => message.id === task.messageID);
      if (existingIndex < 0) return [...withUser, task.assistantMessage];
      return withUser.map((message, index) => index === existingIndex ? task.assistantMessage : message);
    });
  };

  const recoverActiveChatTasks = async () => {
    try {
      const activeTasks = await getActiveChatTasks();
      if (activeTasks.length === 0) return;
      const recovered: Record<string, SessionTaskRuntime> = {};
      for (const task of activeTasks) {
        const projectID = task.projectId?.trim();
        const sessionID = task.sessionId?.trim();
        if (!projectID || !sessionID) continue;
        const startedAt = task.startedAt || new Date().toISOString();
        const messageID = "assistant-recovered-" + task.taskId;
		const progress = findTaskProgress(task.parts);
        recovered[sessionIdentityKey(projectID, sessionID)] = {
          taskID: task.taskId,
          projectID,
          sessionID,
          userMessageID: "",
          messageID,
          prompt: "",
          tail: "",
          attachments: [],
          links: [],
          startedAt,
		  progress: progress ? cloneTaskProgress(progress) : undefined,
          assistantMessage: {
            id: messageID,
            role: "assistant",
			content: task.content || "",
			reasoning: task.reasoning,
			createdAt: startedAt,
			model: task.model,
			parts: task.parts,
			durationMs: task.durationMs,
			streaming: true,
			status: liveTaskStatus(task.message),
			statusKind: liveTaskStatus(task.message) === "正在执行任务" ? "running" : streamStatusKind(task.status),
          },
        };
      }
      setSessionTaskRuntimes((current) => ({ ...recovered, ...current }));
    } catch {
      // A failed recovery must not block opening the workspace.
    }
  };

  const reloadAfterSessionChange = (nextState: WorkbenchState, rememberQueue = true): boolean => {
    if (rememberQueue) {
      rememberCurrentSessionQueue();
      rememberCurrentSessionView();
    }
    setState(nextState);
    setSelectedSessionID(nextState.activeSessionId || "");
    setQueuedMessages([]);
    setComposerDraft("");
    setComposerTail("");
    setComposerAttachments([]);
    setComposerLinks([]);
    resetComposerHistory();
    setChatNearBottom(true);
    const restored = restoreSessionView(nextState.activeProjectId, nextState.activeSessionId);
    restoreSessionQueue(nextState.activeProjectId, nextState.activeSessionId);
    activateSessionTask(nextState.activeProjectId, nextState.activeSessionId);
    if (rememberQueue) {
      void (async () => {
        await restoreSessionMessages(restored || isSessionBusy(nextState.activeProjectId, nextState.activeSessionId), nextState.activeProjectId, nextState.activeSessionId);
        activateSessionTask(nextState.activeProjectId, nextState.activeSessionId);
      })();
    }
    void refreshProjectsAndSessions();
    void refreshCheckpoints();
    return restored;
  };

  const handleNewSession = async () => {
    try {
      reloadAfterSessionChange(await newSession());
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const handleSwitchSession = async (projectID: string, sessionID: string) => {
    setSwitchingSession(true);
    try {
      rememberCurrentSessionQueue();
      rememberCurrentSessionView();
      const nextState = await switchSession(projectID, sessionID);
      setSelectedSessionID(sessionID);
      const restored = reloadAfterSessionChange(nextState, false);
      await restoreSessionMessages(restored || isSessionBusy(projectID, sessionID), projectID, sessionID);
      activateSessionTask(projectID, sessionID);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSwitchingSession(false);
    }
  };

  const handleSwitchProject = async (projectID: string) => {
    try {
      reloadAfterSessionChange(await switchProject(projectID));
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const handleAddProject = async () => {
    try {
      const dir = await selectDirectory();
      if (!dir) {
        return;
      }
      const name = baseNameFromPath(dir);
      reloadAfterSessionChange(await createProject(name, dir));
      setSidebarTab("projects");
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const defaultWorktreeBranch = () => {
    const now = new Date();
    const stamp = [now.getFullYear(), String(now.getMonth() + 1).padStart(2, "0"), String(now.getDate()).padStart(2, "0")].join("")
      + "-" + [String(now.getHours()).padStart(2, "0"), String(now.getMinutes()).padStart(2, "0"), String(now.getSeconds()).padStart(2, "0")].join("");
    const prefix = runtimeSettings().git.branchPrefix.trim().replace(/\/+$/, "");
    const leaf = `worktree-${stamp}`;
    return prefix ? `${prefix}/${leaf}` : leaf;
  };

  const defaultWorktreeLocation = (project: ProjectNode) => {
    const sourceRoot = project.workspaceRoot.trim();
    const parent = parentDirectory(sourceRoot);
    const sourceName = worktreePathSegment(baseNameFromPath(sourceRoot) || project.name);
    return { parent, name: `${sourceName}-worktree` };
  };

  const openSessionMenu = (event: MouseEvent, project: ProjectNode, session: SessionInfo) => {
    event.preventDefault();
    event.stopPropagation();
    const rect = event.currentTarget instanceof HTMLElement
      ? event.currentTarget.getBoundingClientRect()
      : undefined;
    const width = 224;
    const height = 252;
    const margin = 8;
    const anchorX = event.clientX || rect?.right || margin;
    const anchorY = event.clientY || rect?.bottom || margin;
    const left = Math.min(Math.max(margin, anchorX), Math.max(margin, window.innerWidth - width - margin));
    const top = Math.min(Math.max(margin, anchorY), Math.max(margin, window.innerHeight - height - margin));
    setProjectMenu(undefined);
    setSessionMenu({ project, session, left: Math.round(left), top: Math.round(top) });
  };

  const openSessionRenameDialog = (menu: SessionMenuState) => {
    setSessionMenu(undefined);
    setError("");
    setSessionRenameDialog({
      project: menu.project,
      session: menu.session,
      title: menu.session.title || "新对话",
    });
  };

  const openProjectMenu = (event: MouseEvent, project: ProjectNode) => {
    event.preventDefault();
    event.stopPropagation();
    if (projectActionBusy()) return;
    setSessionMenu(undefined);
    if (projectMenu()?.project.id === project.id) {
      setProjectMenu(undefined);
      return;
    }
    const rect = event.currentTarget instanceof HTMLElement
      ? event.currentTarget.getBoundingClientRect()
      : undefined;
    if (!rect) return;
    const width = 208;
    const height = 246;
    const margin = 8;
    const left = Math.min(Math.max(margin, rect.right - width), Math.max(margin, window.innerWidth - width - margin));
    const top = rect.bottom + height + margin <= window.innerHeight
      ? rect.bottom + 4
      : Math.max(margin, rect.top - height - 4);
    setProjectMenu({ project, left: Math.round(left), top: Math.round(top) });
  };

  const openProjectDialog = (kind: ProjectDialogState["kind"], project: ProjectNode) => {
    const branch = kind === "worktree" ? defaultWorktreeBranch() : "";
    const worktreeLocation = defaultWorktreeLocation(project);
    setProjectMenu(undefined);
    setError("");
    setProjectDialog({
      kind,
      project,
      name: kind === "rename" ? project.name : "",
      branch,
      destinationParent: kind === "worktree" ? worktreeLocation.parent : "",
      destinationName: kind === "worktree" ? worktreeLocation.name : "",
    });
  };

  const handleSetProjectPinned = async (project: ProjectNode) => {
    setProjectMenu(undefined);
    setProjectActionBusy(true);
    setError("");
    try {
      setState(await setProjectPinned(project.id, !project.pinned));
      await refreshProjectsAndSessions();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setProjectActionBusy(false);
    }
  };

  const handleOpenProjectInFileManager = async (project: ProjectNode) => {
    setProjectMenu(undefined);
    setProjectActionBusy(true);
    setError("");
    try {
      await openProjectInFileManager(project.id);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setProjectActionBusy(false);
    }
  };

  const handleChooseWorktreeParent = async () => {
    const dialog = projectDialog();
    if (!dialog || dialog.kind !== "worktree" || projectActionBusy()) return;
    try {
      const parent = await selectWorktreeParentDirectory();
      if (!parent) return;
      setProjectDialog((current) => current && current.kind === "worktree"
        ? { ...current, destinationParent: parent }
        : current);
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const submitProjectDialog = async () => {
    const dialog = projectDialog();
    if (!dialog || projectActionBusy()) return;
    if (dialog.kind === "rename" && !dialog.name.trim()) {
      setError("项目名称不能为空");
      return;
    }
    if (dialog.kind === "worktree" && (!dialog.branch.trim() || !dialog.destinationParent.trim() || !dialog.destinationName.trim())) {
      setError("分支名和工作树目录不能为空");
      return;
    }
    setProjectActionBusy(true);
    setError("");
    try {
      let nextState: WorkbenchState;
      switch (dialog.kind) {
        case "rename":
          nextState = await renameProject(dialog.project.id, dialog.name.trim());
          setProjectDialog(undefined);
          setState(nextState);
          await refreshProjectsAndSessions();
          break;
        case "archive":
          nextState = await archiveProjectTasks(dialog.project.id);
          setProjectDialog(undefined);
          if (dialog.project.isActive) {
            reloadAfterSessionChange(nextState);
          } else {
            setState(nextState);
            await refreshProjectsAndSessions();
          }
          break;
        case "remove":
          nextState = await removeProject(dialog.project.id);
          setProjectDialog(undefined);
          if (dialog.project.isActive) {
            reloadAfterSessionChange(nextState);
          } else {
            setState(nextState);
            await refreshProjectsAndSessions();
          }
          break;
        case "worktree":
          nextState = await createPermanentWorktree(
            dialog.project.id,
            dialog.branch.trim(),
            joinNativePath(dialog.destinationParent, dialog.destinationName.trim()),
          );
          setProjectDialog(undefined);
          reloadAfterSessionChange(nextState);
          break;
      }
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setProjectActionBusy(false);
    }
  };

  const submitSessionRename = async () => {
    const dialog = sessionRenameDialog();
    if (!dialog) return;
    const title = dialog.title.trim();
    if (!title) {
      setError("会话名称不能为空。");
      return;
    }
    const sessionKey = sessionIdentityKey(dialog.project.id, dialog.session.id);
    if (sessionActionBusyID() || isSessionBusy(dialog.project.id, dialog.session.id)) return;
    setSessionActionBusyID(sessionKey);
    setError("");
    try {
      setState(await renameSession(dialog.project.id, dialog.session.id, title));
      setSessionRenameDialog(undefined);
      await refreshProjectsAndSessions();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSessionActionBusyID("");
    }
  };

  const handleOpenSessionProjectDirectory = async (projectID: string) => {
    setSessionMenu(undefined);
    setError("");
    try {
      await openProjectInFileManager(projectID);
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const handleCopySessionValue = async (value: string) => {
    setSessionMenu(undefined);
    setError("");
    try {
      await writeClipboardText(value);
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const handleArchiveSession = async (projectID: string, sessionID: string, archived: boolean) => {
    const sessionKey = sessionIdentityKey(projectID, sessionID);
    if (sessionActionBusyID() || isSessionBusy(projectID, sessionID)) return;
    setSessionMenu(undefined);
    setSessionActionBusyID(sessionKey);
    setError("");
    try {
      setState(await archiveSession(projectID, sessionID, archived));
      await refreshProjectsAndSessions();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setSessionActionBusyID("");
    }
  };

  const handleDeleteSession = async (projectID: string, session: SessionInfo) => {
    const sessionKey = sessionIdentityKey(projectID, session.id);
    if (isSessionBusy(projectID, session.id) || deletingSessionID() || sessionActionBusyID()) {
      return;
    }
    setSessionMenu(undefined);
    const title = session.title || "新对话";
    const confirmed = await confirmAction({
      title: "删除对话？",
      message: `“${title}”将从当前项目中永久删除。`,
      detail: "对话记录与事件快照会一并删除，此操作无法撤销。",
      confirmLabel: "删除",
      tone: "danger",
    });
    if (!confirmed) return;
    const currentProjectID = activeProjectID();
    const currentSessionID = activeSessionID();
    const deletingActiveSession = projectID === currentProjectID && session.id === currentSessionID;
    setDeletingSessionID(sessionKey);
    setError("");
    try {
      const nextState = await deleteSession(projectID, session.id);
      setQueuedMessagesBySession((current) => {
        if (!(sessionKey in current)) return current;
        const next = { ...current };
        delete next[sessionKey];
        return next;
      });
      setSessionViewStates((current) => {
        if (!(sessionKey in current)) return current;
        const next = { ...current };
        delete next[sessionKey];
        return next;
      });
      setSessionTaskRuntimes((current) => {
        if (!(sessionKey in current)) return current;
        const next = { ...current };
        delete next[sessionKey];
        return next;
      });

      const activeIdentityChanged = deletingActiveSession
        || nextState.activeProjectId !== currentProjectID
        || nextState.activeSessionId !== currentSessionID;
      if (activeIdentityChanged) {
        const restored = reloadAfterSessionChange(nextState, false);
        await restoreSessionMessages(
          restored || isSessionBusy(nextState.activeProjectId, nextState.activeSessionId),
          nextState.activeProjectId,
          nextState.activeSessionId,
        );
        activateSessionTask(nextState.activeProjectId, nextState.activeSessionId);
        await refreshProjectsAndSessions();
      } else {
        setState(nextState);
        await refreshProjectsAndSessions();
      }
    } catch (err) {
      setError(errorMessage(err));
      await refreshProjectsAndSessions();
      try {
        setState(await getWorkbenchState());
      } catch {
        // 保留原始删除错误。
      }
      await restoreSessionMessages();
    } finally {
      setDeletingSessionID("");
    }
  };

  const beginEditMessage = (message: ChatMessage) => {
    if (!message.eventId || currentSessionBusy()) {
      return;
    }
    setEditingMessageID(message.id);
    setEditMessageDraft(message.content);
    setEditMessageAttachments(message.attachments ?? []);
    queueMicrotask(() => {
      const editor = document.querySelector<HTMLTextAreaElement>(".op-message-editor textarea");
      editor?.focus();
      editor?.setSelectionRange(editor.value.length, editor.value.length);
      editor?.closest(".op-message-editor")?.scrollIntoView({ block: "end" });
    });
  };

  const cancelEditMessage = () => {
    if (editingMessageBusy()) {
      return;
    }
    setEditingMessageID("");
    setEditMessageDraft("");
    setEditMessageAttachments([]);
  };

  const handleEditResend = async (message: ChatMessage) => {
    const prompt = editMessageDraft().trim();
    const attachments = editMessageAttachments();
    if (!message.eventId || (!prompt && attachments.length === 0) || currentSessionBusy() || editingMessageBusy()) {
      return;
    }
    setEditingMessageBusy(true);
    setError("");
    try {
      setState(await forkFromMessage(message.eventId, activeProjectID(), activeSessionID()));
      await restoreSessionMessages();
      await refreshCheckpoints();
      await refreshProjectsAndSessions();
      setEditingMessageID("");
      setEditMessageDraft("");
      setEditMessageAttachments([]);
      setComposerDraft(prompt);
      setComposerTail("");
      await sendPrompt(prompt, attachments, [], "");
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setEditingMessageBusy(false);
    }
  };

  const handleForkMessage = async (message: ChatMessage) => {
    if (!message.eventId || currentSessionBusy() || forkingMessageID()) {
      return;
    }
    setForkingMessageID(message.id);
    setError("");
    try {
      setState(await forkFromMessage(message.eventId, activeProjectID(), activeSessionID()));
      await restoreSessionMessages();
      await refreshCheckpoints();
      await refreshProjectsAndSessions();
      setTimelineOpen(false);
      setEditingMessageID("");
      setEditMessageDraft("");
      const forkDraft = message.role === "user" ? splitComposerPrompt(message.content) : { text: "", tail: "", links: [] };
      setComposerDraft(forkDraft.text);
      setComposerTail(forkDraft.tail);
      setComposerLinks(forkDraft.links);
      setComposerAttachments(message.role === "user" ? (message.attachments ?? []) : []);
      resetComposerHistory();
      queueMicrotask(focusComposerEnd);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setForkingMessageID("");
    }
  };

  const handleUndoMessageChanges = async (message: ChatMessage) => {
    if (message.id !== undoableFileChangeMessageID() || currentSessionBusy() || rewinding()) return;
    const messageIndex = messages().findIndex((candidate) => candidate.id === message.id);
    const sourceMessage = messages()
      .slice(0, messageIndex)
      .reverse()
      .find((candidate) => candidate.role === "user" && candidate.eventId);
    if (!sourceMessage?.eventId) {
      setError("找不到本轮修改之前的会话检查点。");
      return;
    }

    setRewinding(true);
    setError("");
    try {
      setState(await forkFromMessage(sourceMessage.eventId, activeProjectID(), activeSessionID()));
      await restoreSessionMessages();
      await refreshCheckpoints();
      await refreshProjectsAndSessions();
      const restored = splitComposerPrompt(sourceMessage.content);
      setComposerDraft(restored.text);
      setComposerTail(restored.tail);
      setComposerLinks(restored.links);
      setComposerAttachments(sourceMessage.attachments ?? []);
      resetComposerHistory();
      queueMicrotask(focusComposerEnd);
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setRewinding(false);
    }
  };

  // 会话列表：按排序与归档筛选。
  const displayedSessions = createMemo(() => {
    let list = sessions().filter((s) => showArchived() || !s.archived);
    if (sessionSort() === "name") {
      list = [...list].sort((a, b) => a.title.localeCompare(b.title));
    } else {
      list = [...list].sort((a, b) => b.updatedAt.localeCompare(a.updatedAt));
    }
    return list;
  });

  // 拉取当前会话的可回退检查点与分支。
  const refreshCheckpoints = async () => {
    try {
      const [cps, brs] = await Promise.all([listCheckpoints(), listBranches()]);
      setCheckpoints(cps);
      setBranches(brs);
    } catch {
      // 时间线是辅助功能，失败不打断主流程。
    }
  };

  const openTimeline = async () => {
    await refreshCheckpoints();
    setTimelineOpen(true);
  };

  // 开关 Plan 两段式。
  const togglePlanMode = async () => {
    try {
      setState(await setPlanMode(!planMode()));
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  const toggleTeamMode = async () => {
    if (!teamMode() && !profile().budget.planner) {
      setError("AI 团队模式需要高或超高推理强度。");
      return;
    }
    try {
      const current = runtimeSettings();
      setState(await saveRuntimeSettings({
        ...current,
        team: { ...current.team, enabled: !teamMode() },
      }));
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  // 切换完全访问：在 never（不弹框）与 on-request（每次确认）之间切换审批策略。
  const toggleFullAccess = async () => {
    try {
      const nextPolicy = fullAccess() ? "on-request" : "never";
      const nextSettings = { ...runtimeSettings(), approvalPolicy: nextPolicy };
      setState(await saveRuntimeSettings(nextSettings));
    } catch (err) {
      setError(errorMessage(err));
    }
  };

  // 回退到某个检查点：后端会同时还原对话与文件，随后重建前端消息列表。
  const handleRewind = async (checkpointID: string) => {
    setRewinding(true);
    try {
      const nextState = await rewindToCheckpoint(checkpointID);
      setState(nextState);
      await restoreSessionMessages();
      await refreshCheckpoints();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setRewinding(false);
    }
  };

  // 切换分支：文件与对话在后端一起切换，再从事件日志恢复该分支消息。
  const handleSwitchBranch = async (leafID: string) => {
    setRewinding(true);
    try {
      const nextState = await switchBranch(leafID);
      setState(nextState);
      await restoreSessionMessages();
      await refreshCheckpoints();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setRewinding(false);
    }
  };

  function openDrawer(tab: DrawerTab) {
    openSettings(categoryForDrawerTab(tab));
  }

  function openSettings(category: SettingsCategory) {
    setActiveSettingsCategory(category);
    setDrawerOpen(true);
    pushView({ drawer: true, category });
  }

  function closeDrawer() {
    setDrawerOpen(false);
    pushView({ drawer: false, category: activeSettingsCategory() });
  }

  // 把一个视图快照压入历史（截断游标之后的“前进”记录，与浏览器一致）。
  function pushView(snapshot: ViewSnapshot) {
    const current = viewHistory()[viewCursor()];
    if (current && current.drawer === snapshot.drawer && current.category === snapshot.category) {
      return;
    }
    const trimmed = viewHistory().slice(0, viewCursor() + 1);
    trimmed.push(snapshot);
    setViewHistory(trimmed);
    setViewCursor(trimmed.length - 1);
  }

  // 应用某个历史快照（后退/前进时调用，不再写入历史）。
  function applyView(snapshot: ViewSnapshot) {
    setActiveSettingsCategory(snapshot.category);
    setDrawerOpen(snapshot.drawer);
  }

  const canGoBack = createMemo(() => viewCursor() > 0);
  const canGoForward = createMemo(() => viewCursor() < viewHistory().length - 1);

  function navigateBack() {
    if (!canGoBack()) {
      return;
    }
    const next = viewCursor() - 1;
    setViewCursor(next);
    applyView(viewHistory()[next]);
  }

  function navigateForward() {
    if (!canGoForward()) {
      return;
    }
    const next = viewCursor() + 1;
    setViewCursor(next);
    applyView(viewHistory()[next]);
  }

  const resizeSidebar = (clientX: number) => {
    const left = shellRef?.getBoundingClientRect().left ?? 0;
    const width = clamp((clientX - left) / Math.max(effectiveUIScale(), 0.01), minSidebarWidth, maxSidebarWidth);
    setSidebarWidth(width);
    applySidebarWidth(width, shellRef);
  };

  const moveWindowPointerSidebarResize = (event: PointerEvent) => {
    if (!pointerSidebarResizeActive) {
      return;
    }
    resizeSidebar(event.clientX);
  };

  const stopWindowPointerSidebarResize = () => {
    if (!pointerSidebarResizeActive) {
      return;
    }
    pointerSidebarResizeActive = false;
    setResizingSidebar(false);
    persistSidebarWidth(sidebarWidth());
    window.removeEventListener("pointermove", moveWindowPointerSidebarResize);
    window.removeEventListener("pointerup", stopWindowPointerSidebarResize);
    window.removeEventListener("pointercancel", stopWindowPointerSidebarResize);
  };

  const startSidebarResize = (event: PointerEvent & { currentTarget: HTMLElement }) => {
    event.preventDefault();
    pointerSidebarResizeActive = true;
    setResizingSidebar(true);
    resizeSidebar(event.clientX);
    if (event.currentTarget.setPointerCapture) {
      event.currentTarget.setPointerCapture(event.pointerId);
    }
    window.addEventListener("pointermove", moveWindowPointerSidebarResize);
    window.addEventListener("pointerup", stopWindowPointerSidebarResize);
    window.addEventListener("pointercancel", stopWindowPointerSidebarResize);
  };

  const moveSidebarResize = (event: PointerEvent) => {
    moveWindowPointerSidebarResize(event);
  };

  const stopSidebarResize = (event: PointerEvent & { currentTarget: HTMLElement }) => {
    if (event.currentTarget.hasPointerCapture?.(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    stopWindowPointerSidebarResize();
  };

  const browserPanelLimits = () => {
    const scale = Math.max(effectiveUIScale(), 0.01);
    const available = (workbenchRef?.getBoundingClientRect().width ?? window.innerWidth) / scale;
    const overlay = window.innerWidth / scale <= 1080;
    const min = Math.min(minBrowserPanelWidth, available);
    const max = overlay
      ? available
      : Math.max(min, available - minChatPaneWidth - 8);
    return { min, max };
  };

  const defaultBrowserWidth = () => {
    const scale = Math.max(effectiveUIScale(), 0.01);
    const available = (workbenchRef?.getBoundingClientRect().width ?? window.innerWidth) / scale;
    const desired = window.innerWidth / scale <= 1080 ? defaultBrowserPanelWidth : available * 0.48;
    const limits = browserPanelLimits();
    return clamp(desired, limits.min, limits.max);
  };

  const constrainBrowserPanelWidth = () => {
    const limits = browserPanelLimits();
    const desired = browserPanelWidthInitialized ? browserPanelWidth() : defaultBrowserWidth();
    browserPanelWidthInitialized = true;
    const next = clamp(desired, limits.min, limits.max);
    if (next !== browserPanelWidth()) {
      setBrowserPanelWidth(next);
    }
  };

  const resizeBrowserPanel = (clientX: number) => {
    const rect = workbenchRef?.getBoundingClientRect();
    if (!rect) {
      return;
    }
    const limits = browserPanelLimits();
    setBrowserPanelWidth(clamp((rect.right - clientX) / Math.max(effectiveUIScale(), 0.01), limits.min, limits.max));
  };

  const moveWindowPointerBrowserResize = (event: PointerEvent) => {
    if (pointerBrowserResizeActive) {
      resizeBrowserPanel(event.clientX);
    }
  };

  const stopWindowPointerBrowserResize = () => {
    if (!pointerBrowserResizeActive) {
      return;
    }
    pointerBrowserResizeActive = false;
    setResizingBrowserPanel(false);
    persistBrowserPanelWidth(browserPanelWidth());
    window.removeEventListener("pointermove", moveWindowPointerBrowserResize);
    window.removeEventListener("pointerup", stopWindowPointerBrowserResize);
    window.removeEventListener("pointercancel", stopWindowPointerBrowserResize);
  };

  const startBrowserResize = (event: PointerEvent & { currentTarget: HTMLElement }) => {
    event.preventDefault();
    pointerBrowserResizeActive = true;
    setResizingBrowserPanel(true);
    resizeBrowserPanel(event.clientX);
    event.currentTarget.setPointerCapture?.(event.pointerId);
    window.addEventListener("pointermove", moveWindowPointerBrowserResize);
    window.addEventListener("pointerup", stopWindowPointerBrowserResize);
    window.addEventListener("pointercancel", stopWindowPointerBrowserResize);
  };

  const stopBrowserResize = (event: PointerEvent & { currentTarget: HTMLElement }) => {
    if (event.currentTarget.hasPointerCapture?.(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    stopWindowPointerBrowserResize();
  };

  const moveMouseBrowserResize = (event: MouseEvent) => {
    if (mouseBrowserResizeActive) {
      resizeBrowserPanel(event.clientX);
    }
  };

  const stopMouseBrowserResize = () => {
    if (!mouseBrowserResizeActive) {
      return;
    }
    mouseBrowserResizeActive = false;
    setResizingBrowserPanel(false);
    persistBrowserPanelWidth(browserPanelWidth());
    window.removeEventListener("mousemove", moveMouseBrowserResize);
    window.removeEventListener("mouseup", stopMouseBrowserResize);
  };

  const startMouseBrowserResize = (event: MouseEvent) => {
    if (event.button !== 0 || resizingBrowserPanel()) {
      return;
    }
    event.preventDefault();
    mouseBrowserResizeActive = true;
    setResizingBrowserPanel(true);
    resizeBrowserPanel(event.clientX);
    window.addEventListener("mousemove", moveMouseBrowserResize);
    window.addEventListener("mouseup", stopMouseBrowserResize);
  };

  const moveMouseSidebarResize = (event: MouseEvent) => {
    if (!mouseSidebarResizeActive) {
      return;
    }
    resizeSidebar(event.clientX);
  };

  const stopMouseSidebarResize = () => {
    if (!mouseSidebarResizeActive) {
      return;
    }
    mouseSidebarResizeActive = false;
    setResizingSidebar(false);
    persistSidebarWidth(sidebarWidth());
    window.removeEventListener("mousemove", moveMouseSidebarResize);
    window.removeEventListener("mouseup", stopMouseSidebarResize);
  };

  const startMouseSidebarResize = (event: MouseEvent) => {
    if (event.button !== 0 || resizingSidebar()) {
      return;
    }
    event.preventDefault();
    mouseSidebarResizeActive = true;
    setResizingSidebar(true);
    resizeSidebar(event.clientX);
    window.addEventListener("mousemove", moveMouseSidebarResize);
    window.addEventListener("mouseup", stopMouseSidebarResize);
  };

  onCleanup(() => {
    window.removeEventListener("pointermove", moveWindowPointerSidebarResize);
    window.removeEventListener("pointerup", stopWindowPointerSidebarResize);
    window.removeEventListener("pointercancel", stopWindowPointerSidebarResize);
    window.removeEventListener("mousemove", moveMouseSidebarResize);
    window.removeEventListener("mouseup", stopMouseSidebarResize);
    window.removeEventListener("pointermove", moveWindowPointerBrowserResize);
    window.removeEventListener("pointerup", stopWindowPointerBrowserResize);
    window.removeEventListener("pointercancel", stopWindowPointerBrowserResize);
    window.removeEventListener("mousemove", moveMouseBrowserResize);
    window.removeEventListener("mouseup", stopMouseBrowserResize);
  });

  const resetSidebarWidth = () => {
    setSidebarWidth(defaultSidebarWidth);
    applySidebarWidth(defaultSidebarWidth, shellRef);
    persistSidebarWidth(defaultSidebarWidth);
  };

  const toggleTheme = () => {
    const next = themeMode() === "dark" ? "light" : "dark";
    setThemeMode(next);
    applyThemeMode(next, shellRef);
    persistThemeMode(next);
  };

  const nudgeSidebarWidth = (direction: -1 | 1) => {
    const next = clamp(sidebarWidth() + direction * 16, minSidebarWidth, maxSidebarWidth);
    setSidebarWidth(next);
    applySidebarWidth(next, shellRef);
    persistSidebarWidth(next);
  };

  const setAndPersistBrowserPanelWidth = (width: number) => {
    const limits = browserPanelLimits();
    const next = clamp(width, limits.min, limits.max);
    setBrowserPanelWidth(next);
    persistBrowserPanelWidth(next);
  };

  const resetBrowserPanelWidth = () => {
    setAndPersistBrowserPanelWidth(defaultBrowserWidth());
  };

  const nudgeBrowserPanelWidth = (direction: -1 | 1) => {
    setAndPersistBrowserPanelWidth(browserPanelWidth() + direction * 16);
  };

  onMount(() => {
    const observer = new ResizeObserver(constrainBrowserPanelWidth);
    if (workbenchRef) {
      observer.observe(workbenchRef);
    }
    constrainBrowserPanelWidth();
    window.addEventListener("resize", constrainBrowserPanelWidth);
    window.addEventListener("resize", syncUIAppearance);
    window.visualViewport?.addEventListener("resize", syncUIAppearance);
    onCleanup(() => {
      observer.disconnect();
      window.removeEventListener("resize", constrainBrowserPanelWidth);
      window.removeEventListener("resize", syncUIAppearance);
      window.visualViewport?.removeEventListener("resize", syncUIAppearance);
    });
  });

  createEffect(() => {
    if (browserPreview() || reviewOpen() || selectedSubagent()) {
      queueMicrotask(constrainBrowserPanelWidth);
    }
  });

  return (
    <main
      class="mh-shell"
      classList={{
        resizing: resizingSidebar(),
        "resizing-browser": resizingBrowserPanel(),
        "theme-light": themeMode() === "light",
        "theme-dark": themeMode() === "dark",
      }}
      ref={shellRef}
      style={{ "--sidebar-width": `${sidebarWidth()}px` } as JSX.CSSProperties}
    >
      <AppTitleBar />
      <Show when={updateBannerVisible() && updateBannerState()}>
        {(update) => (
          <section
            class="app-update-banner"
            classList={{ busy: updateBannerBusy(), error: update().status === "error", available: update().status === "available" }}
            aria-live="polite"
            aria-label="软件更新状态"
          >
            <div class="app-update-banner-icon" aria-hidden="true">
              <Show when={update().status === "error"} fallback={
                <Show when={updateBannerBusy()} fallback={<CheckCircle2 size={17} />}>
                  <RefreshCw size={17} class="spinning" />
                </Show>
              }>
                <AlertTriangle size={17} />
              </Show>
            </div>
            <div class="app-update-banner-copy">
              <strong>{updateBannerTitle()}</strong>
              <span>{update().message}</span>
            </div>
            <Show when={["available", "downloaded", "error"].includes(update().status)}>
              <button
                class="app-update-banner-action"
                type="button"
                onClick={() => {
                  dismissUpdateBanner();
                  openSettings("about");
                }}
              >
                {update().status === "downloaded" ? "安装" : update().status === "available" ? "查看" : "设置"}
              </button>
            </Show>
            <button class="app-update-banner-close" type="button" title="关闭更新提示" onClick={dismissUpdateBanner}>
              <X size={15} />
            </button>
            <div class="app-update-banner-timer" aria-hidden="true">
              <span classList={{ indeterminate: updateBannerBusy() }} style={{ width: `${updateBannerProgress() * 100}%` }} />
            </div>
          </section>
        )}
      </Show>
      <aside class="mh-sidebar" aria-label="EchoCode 导航">
        <div class="sidebar-top">
          <button
            class="ghost-icon"
            type="button"
            title="后退"
            disabled={!canGoBack()}
            onClick={navigateBack}
          >
            <ArrowLeft size={16} />
          </button>
          <button
            class="ghost-icon"
            type="button"
            title="前进"
            disabled={!canGoForward()}
            onClick={navigateForward}
          >
            <ArrowRight size={16} />
          </button>
          <button
            class="connection-pill"
            classList={{ ok: activeProviderConnection().ok, warn: !activeProviderConnection().ready }}
            type="button"
            onClick={() => openSettings("models")}
          >
            <RefreshCw size={13} classList={{ spinning: loading() || testingDeepSeek() }} />
            {activeProviderConnection().label}
          </button>
        </div>

        <div class="quick-menu">
          <button type="button" onClick={() => void handleNewSession()} disabled={resettingSession()}>
            <MessageSquarePlus size={16} />
            <span>新建任务</span>
          </button>
          <button type="button" onClick={() => openSettings("index")}>
            <Search size={16} />
            <span>搜索</span>
          </button>
          <button type="button" onClick={() => openSettings("skills")}>
            <Sparkles size={16} />
            <span>技能</span>
          </button>
        </div>

        <div class="proj-tree-head">
          <span>项目</span>
          <div class="proj-tree-tools">
            <button
              type="button"
              class="proj-tool-btn"
              classList={{ active: showArchived() }}
              title={showArchived() ? "隐藏已归档会话" : "显示已归档会话"}
              onClick={() => setShowArchived((v) => !v)}
            >
              <Archive size={14} />
            </button>
            <button type="button" class="proj-tool-btn" title="添加项目" onClick={() => void handleAddProject()}>
              <Plus size={15} />
            </button>
          </div>
        </div>

        <div class="proj-tree" onScroll={() => setProjectMenu(undefined)}>
          <Show
            when={projectTree().length > 0}
            fallback={<div class="sidebar-empty"><span>暂无项目</span><small>点右上角 + 添加项目</small></div>}
          >
            <For each={projectTree()}>
              {(project) => (
                <div class="proj-node">
                  <div class="proj-row-wrap">
                    <button
                      class="proj-row"
                      classList={{ active: project.isActive }}
                      type="button"
                      disabled={projectActionBusy()}
                      onClick={() => void handleSwitchProject(project.id)}
                      title={project.workspaceRoot}
                    >
                      <Folder size={14} />
                      <span class="proj-name">{project.name}</span>
                      <Show when={project.pinned}><Pin class="proj-pin" size={11} /></Show>
                    </button>
                    <button
                      class="proj-menu-trigger"
                      classList={{ active: projectMenu()?.project.id === project.id }}
                      type="button"
                      data-project-menu-trigger
                      title="项目操作"
                      aria-label={`${project.name} 项目操作`}
                      aria-haspopup="menu"
                      aria-expanded={projectMenu()?.project.id === project.id}
                      disabled={projectActionBusy()}
                      onClick={(event) => openProjectMenu(event, project)}
                    >
                      <Ellipsis size={15} />
                    </button>
                  </div>
                  <For each={project.sessions.filter((s) => showArchived() || !s.archived)}>
                    {(session) => (
                      <div
                        class="sess-row-wrap"
                        classList={{ archived: session.archived }}
                        data-session-menu-target
                        onContextMenu={(event) => openSessionMenu(event, project, session)}
                      >
                        <button
                          class="sess-row"
                          classList={{ active: session.isActive }}
                          type="button"
                          disabled={switchingSession()
                            || deletingSessionID() === sessionIdentityKey(project.id, session.id)
                            || sessionActionBusyID() === sessionIdentityKey(project.id, session.id)}
                          onClick={() => void handleSwitchSession(project.id, session.id)}
                          title={session.title}
                        >
                          <span class="sess-title">{session.title || "新对话"}</span>
                          <Show when={sessionTaskRuntimes()[sessionIdentityKey(project.id, session.id)]}>
                            <span class="sess-running-dot" title="后台生成中" aria-label="后台生成中" />
                          </Show>
                          <span class="sess-time">{relativeTime(session.updatedAt)}</span>
                        </button>
                        <div class="sess-actions">
                          <button
                            class="sess-action"
                            type="button"
                            title={session.archived ? "取消归档" : "归档"}
                            aria-label={session.archived ? "取消归档" : "归档"}
                            disabled={isSessionBusy(project.id, session.id) || Boolean(deletingSessionID()) || Boolean(sessionActionBusyID())}
                            onClick={() => void handleArchiveSession(project.id, session.id, !session.archived)}
                          >
                            <Archive size={12} />
                          </button>
                          <button
                            class="sess-action danger"
                            type="button"
                            title="永久删除对话"
                            aria-label="永久删除对话"
                            disabled={isSessionBusy(project.id, session.id) || Boolean(deletingSessionID()) || Boolean(sessionActionBusyID())}
                            onClick={() => void handleDeleteSession(project.id, session)}
                          >
                            <Trash2 size={12} />
                          </button>
                        </div>
                      </div>
                    )}
                  </For>
                </div>
              )}
            </For>
          </Show>
        </div>

        <Show when={sessionMenu()}>
          {(menu) => (
            <Portal>
              <div
                ref={sessionActionMenuRef}
                class="project-action-menu session-action-menu"
                role="menu"
                aria-label={`${menu().session.title || "新对话"} 会话操作`}
                style={{ left: `${menu().left}px`, top: `${menu().top}px` }}
              >
                <button
                  type="button"
                  role="menuitem"
                  disabled={Boolean(sessionActionBusyID()) || isSessionBusy(menu().project.id, menu().session.id)}
                  onClick={() => openSessionRenameDialog(menu())}
                >
                  <Pencil size={15} />
                  <span>重命名</span>
                </button>
                <button
                  type="button"
                  role="menuitem"
                  disabled={Boolean(sessionActionBusyID()) || Boolean(deletingSessionID()) || isSessionBusy(menu().project.id, menu().session.id)}
                  onClick={() => void handleArchiveSession(menu().project.id, menu().session.id, !menu().session.archived)}
                >
                  <Archive size={15} />
                  <span>{menu().session.archived ? "取消归档" : "归档"}</span>
                </button>
                <button type="button" role="menuitem" onClick={() => void handleOpenSessionProjectDirectory(menu().project.id)}>
                  <FolderOpen size={15} />
                  <span>打开项目目录</span>
                </button>
                <button type="button" role="menuitem" onClick={() => void handleCopySessionValue(menu().project.workspaceRoot)}>
                  <Copy size={15} />
                  <span>复制工作目录</span>
                </button>
                <button type="button" role="menuitem" onClick={() => void handleCopySessionValue(menu().session.id)}>
                  <Braces size={15} />
                  <span>复制会话 ID</span>
                </button>
                <button
                  class="danger"
                  type="button"
                  role="menuitem"
                  disabled={Boolean(sessionActionBusyID()) || Boolean(deletingSessionID()) || isSessionBusy(menu().project.id, menu().session.id)}
                  onClick={() => void handleDeleteSession(menu().project.id, menu().session)}
                >
                  <Trash2 size={15} />
                  <span>永久删除</span>
                </button>
              </div>
            </Portal>
          )}
        </Show>

        <Show when={sessionRenameDialog()}>
          {(dialog) => {
            const busy = () => sessionActionBusyID() === sessionIdentityKey(dialog().project.id, dialog().session.id);
            return (
              <Portal>
                <div
                  class="project-action-overlay"
                  role="presentation"
                  onPointerDown={(event) => {
                    if (event.currentTarget === event.target && !busy()) setSessionRenameDialog(undefined);
                  }}
                >
                  <form
                    class="project-action-dialog session-rename-dialog"
                    aria-label="重命名会话"
                    onSubmit={(event) => {
                      event.preventDefault();
                      void submitSessionRename();
                    }}
                  >
                    <header>
                      <div class="project-action-title-icon"><Pencil size={17} /></div>
                      <div>
                        <strong>重命名会话</strong>
                        <span>{dialog().project.name}</span>
                      </div>
                      <button type="button" title="关闭" aria-label="关闭" disabled={busy()} onClick={() => setSessionRenameDialog(undefined)}><X size={15} /></button>
                    </header>
                    <div class="project-action-body">
                      <label class="project-action-field">
                        <span>会话名称</span>
                        <input
                          value={dialog().title}
                          maxlength={200}
                          autofocus
                          disabled={busy()}
                          onFocus={(event) => event.currentTarget.select()}
                          onInput={(event) => setSessionRenameDialog((current) => current ? { ...current, title: event.currentTarget.value } : current)}
                        />
                      </label>
                      <p class="project-action-note">只修改当前项目中的这条会话。</p>
                    </div>
                    <footer>
                      <button class="secondary" type="button" disabled={busy()} onClick={() => setSessionRenameDialog(undefined)}>取消</button>
                      <button type="submit" disabled={busy() || !dialog().title.trim()}>{busy() ? "保存中…" : "保存"}</button>
                    </footer>
                  </form>
                </div>
              </Portal>
            );
          }}
        </Show>

        <Show when={projectMenu()}>
          {(menu) => (
            <Portal>
              <div
                ref={projectActionMenuRef}
                class="project-action-menu"
                role="menu"
                aria-label={`${menu().project.name} 项目操作`}
                style={{ left: `${menu().left}px`, top: `${menu().top}px` }}
              >
                <button type="button" role="menuitem" disabled={projectActionBusy() || anySessionBusy()} onClick={() => void handleSetProjectPinned(menu().project)}>
                  <Pin size={15} />
                  <span>{menu().project.pinned ? "取消置顶" : "置顶项目"}</span>
                </button>
                <button type="button" role="menuitem" disabled={projectActionBusy()} onClick={() => void handleOpenProjectInFileManager(menu().project)}>
                  <FolderOpen size={15} />
                  <span>在资源管理器中打开</span>
                </button>
                <button
                  type="button"
                  role="menuitem"
                  disabled={projectActionBusy() || anySessionBusy() || runtimeSettings().filesystemAccess === "read-only" || runtimeSettings().sandboxMode === "read-only"}
                  title={runtimeSettings().filesystemAccess === "read-only" || runtimeSettings().sandboxMode === "read-only" ? "只读模式下不可创建工作树" : ""}
                  onClick={() => openProjectDialog("worktree", menu().project)}
                >
                  <GitBranch size={15} />
                  <span>创建永久工作树</span>
                </button>
                <button type="button" role="menuitem" disabled={projectActionBusy() || anySessionBusy()} onClick={() => openProjectDialog("rename", menu().project)}>
                  <Pencil size={15} />
                  <span>重命名项目</span>
                </button>
                <button type="button" role="menuitem" disabled={projectActionBusy() || anySessionBusy()} onClick={() => openProjectDialog("archive", menu().project)}>
                  <Archive size={15} />
                  <span>归档任务</span>
                </button>
                <button
                  class="danger"
                  type="button"
                  role="menuitem"
                  disabled={projectActionBusy() || anySessionBusy()}
                  onClick={() => openProjectDialog("remove", menu().project)}
                >
                  <X size={15} />
                  <span>移除</span>
                </button>
              </div>
            </Portal>
          )}
        </Show>

        <Show when={projectDialog()}>
          {(dialog) => (
            <Portal>
              <div
                class="project-action-overlay"
                role="presentation"
                onPointerDown={(event) => {
                  if (event.currentTarget === event.target && !projectActionBusy()) setProjectDialog(undefined);
                }}
              >
                <form
                  class="project-action-dialog"
                  aria-label={dialog().kind === "rename" ? "重命名项目" : dialog().kind === "worktree" ? "创建永久工作树" : dialog().kind === "archive" ? "归档项目任务" : "移除项目"}
                  onSubmit={(event) => {
                    event.preventDefault();
                    void submitProjectDialog();
                  }}
                >
                  <header>
                    <div class="project-action-title-icon">
                      <Show when={dialog().kind === "rename"}><Pencil size={17} /></Show>
                      <Show when={dialog().kind === "worktree"}><GitBranch size={17} /></Show>
                      <Show when={dialog().kind === "archive"}><Archive size={17} /></Show>
                      <Show when={dialog().kind === "remove"}><X size={17} /></Show>
                    </div>
                    <div>
                      <strong>{dialog().kind === "rename" ? "重命名项目" : dialog().kind === "worktree" ? "创建永久工作树" : dialog().kind === "archive" ? "归档任务" : "移除项目"}</strong>
                      <span>{dialog().project.name}</span>
                    </div>
                    <button type="button" title="关闭" aria-label="关闭" disabled={projectActionBusy()} onClick={() => setProjectDialog(undefined)}><X size={15} /></button>
                  </header>

                  <div class="project-action-body">
                    <Show when={dialog().kind === "rename"}>
                      <label class="project-action-field">
                        <span>项目名称</span>
                        <input
                          value={dialog().name}
                          maxlength={200}
                          disabled={projectActionBusy()}
                          onInput={(event) => setProjectDialog((current) => current ? { ...current, name: event.currentTarget.value } : current)}
                        />
                      </label>
                      <p class="project-action-note">工作区位置保持不变。</p>
                    </Show>

                    <Show when={dialog().kind === "worktree"}>
                      <div class="project-action-source"><Folder size={14} /><span>{dialog().project.workspaceRoot}</span></div>
                      <label class="project-action-field">
                        <span>新分支</span>
                        <input
                          value={dialog().branch}
                          spellcheck={false}
                          disabled={projectActionBusy()}
                          onInput={(event) => {
                            const branch = event.currentTarget.value;
                            setProjectDialog((current) => current && current.kind === "worktree"
                              ? { ...current, branch }
                              : current);
                          }}
                        />
                      </label>
                      <label class="project-action-field">
                        <span>父目录</span>
                        <div class="project-action-path-input">
                          <div class="project-action-source"><Folder size={14} /><span>{dialog().destinationParent}</span></div>
                          <button type="button" title="选择父目录" aria-label="选择父目录" disabled={projectActionBusy()} onClick={() => void handleChooseWorktreeParent()}><FolderOpen size={15} /></button>
                        </div>
                      </label>
                      <label class="project-action-field">
                        <span>工作树名称</span>
                        <input
                          value={dialog().destinationName}
                          spellcheck={false}
                          disabled={projectActionBusy()}
                          onInput={(event) => setProjectDialog((current) => current && current.kind === "worktree" ? { ...current, destinationName: event.currentTarget.value } : current)}
                        />
                      </label>
                    </Show>

                    <Show when={dialog().kind === "archive"}>
                      <div class="project-action-confirm">
                        <Archive size={18} />
                        <div>
                          <strong>归档 {dialog().project.sessions.filter((session) => !session.archived).length} 个任务</strong>
                          <span>任务记录会保留，可通过侧栏的归档筛选重新查看。</span>
                        </div>
                      </div>
                    </Show>

                    <Show when={dialog().kind === "remove"}>
                      <div class="project-action-confirm danger">
                        <AlertTriangle size={18} />
                        <div>
                          <strong>从 EchoCode 中移除此项目</strong>
                          <span>{projectTree().length <= 1 ? "会清理本地任务记录，并切换到 EchoCodeProject 临时工作区；不会删除源码。" : "会清理本地任务记录，但不会删除工作区源码。"}</span>
                        </div>
                      </div>
                      <div class="project-action-source"><Folder size={14} /><span>{dialog().project.workspaceRoot}</span></div>
                    </Show>
                  </div>

                  <footer>
                    <button class="secondary" type="button" disabled={projectActionBusy()} onClick={() => setProjectDialog(undefined)}>取消</button>
                    <button
                      classList={{ danger: dialog().kind === "remove" }}
                      type="submit"
                      disabled={projectActionBusy() || (dialog().kind === "rename" && !dialog().name.trim()) || (dialog().kind === "worktree" && (!dialog().branch.trim() || !dialog().destinationParent.trim() || !dialog().destinationName.trim()))}
                    >
                      {projectActionBusy() ? "处理中" : dialog().kind === "rename" ? "保存" : dialog().kind === "worktree" ? "创建" : dialog().kind === "archive" ? "归档" : "移除"}
                    </button>
                  </footer>
                </form>
              </div>
            </Portal>
          )}
        </Show>

        <div class="sidebar-footer">
          <div class="avatar">M</div>
          <div>
            <strong>本地用户</strong>
            <small>{profile().label} · {profile().budget.cachePolicy}</small>
          </div>
          <button class="ghost-icon" type="button" title="设置" onClick={() => openDrawer("settings")}>
            <Settings size={16} />
          </button>
        </div>
      </aside>

      <div
        class="sidebar-resizer"
        role="separator"
        aria-label="调整侧栏宽度"
        aria-orientation="vertical"
        aria-valuemin={minSidebarWidth}
        aria-valuemax={maxSidebarWidth}
        aria-valuenow={Math.round(sidebarWidth())}
        tabIndex={0}
        title="拖拽调整宽度，双击恢复默认"
        onPointerDown={startSidebarResize}
        onPointerMove={moveSidebarResize}
        onPointerUp={stopSidebarResize}
        onPointerCancel={stopSidebarResize}
        onMouseDown={startMouseSidebarResize}
        onDblClick={resetSidebarWidth}
        onKeyDown={(event) => {
          if (event.key === "ArrowLeft") {
            event.preventDefault();
            nudgeSidebarWidth(-1);
          }
          if (event.key === "ArrowRight") {
            event.preventDefault();
            nudgeSidebarWidth(1);
          }
          if (event.key === "Home") {
            event.preventDefault();
            setSidebarWidth(minSidebarWidth);
            applySidebarWidth(minSidebarWidth, shellRef);
            persistSidebarWidth(minSidebarWidth);
          }
          if (event.key === "End") {
            event.preventDefault();
            setSidebarWidth(maxSidebarWidth);
            applySidebarWidth(maxSidebarWidth, shellRef);
            persistSidebarWidth(maxSidebarWidth);
          }
          if (event.key === "Enter") {
            event.preventDefault();
            resetSidebarWidth();
          }
        }}
      />

      <div
        class="workbench-main"
        classList={{
          "side-panel-open": !drawerOpen() && (Boolean(browserPreview()) || reviewOpen() || Boolean(selectedSubagent())),
          "browser-open": !drawerOpen() && sidePanelView() === "browser" && Boolean(browserPreview()),
          "review-open": !drawerOpen() && sidePanelView() === "files" && reviewOpen(),
		  "subagent-open": !drawerOpen() && sidePanelView() === "subagent" && Boolean(selectedSubagent()),
          "resizing-browser": resizingBrowserPanel(),
        }}
        ref={workbenchRef}
        style={{ "--browser-panel-width": `${Math.round(browserPanelWidth())}px` } as JSX.CSSProperties}
      >
      <section class="chat-pane" classList={{ "workspace-tools-open": workspaceToolsOpen() }}>
        <header class="chat-header">
          <div>
            <strong>{activeSessionTitle()}</strong>
            <span>{modelName()}</span>
          </div>
          <div class="header-actions">
            <button type="button" onClick={() => openDrawer("cache")}>
              <ShieldCheck size={15} />
              {formatPercent(cacheHitRate(), hasCacheTokens())}
            </button>
            <Show when={backgroundTaskCount() > 0}>
              <span class="background-task-indicator" role="status" title={String(backgroundTaskCount()) + " 个会话正在后台生成"}>
                <Bot size={14} />
                {backgroundTaskCount()} 后台
              </span>
            </Show>
            <button type="button" title="回退时间线" aria-label="回退时间线" onClick={() => void openTimeline()}>
              <History size={15} />
            </button>
            <button
              type="button"
              classList={{ active: workspaceToolsOpen() }}
              title="Git 与终端"
              aria-label="Git 与终端"
              onClick={() => setWorkspaceToolsOpen((open) => !open)}
            >
              <Terminal size={15} />
            </button>
            <button
              type="button"
              classList={{ active: reviewOpen() }}
              title="文件与更改"
              aria-label="文件与更改"
              onClick={toggleReviewPanel}
            >
              <FileText size={15} />
            </button>
            <button type="button" title="内置浏览器" aria-label="内置浏览器" onClick={() => void handleOpenBrowser()}>
              <Globe2 size={15} />
            </button>
            <button
              class="theme-toggle"
              type="button"
              title={themeMode() === "dark" ? "切换浅色主题" : "切换暗色主题"}
              aria-pressed={themeMode() === "light"}
              onClick={toggleTheme}
            >
              <Show when={themeMode() === "dark"} fallback={<Moon size={15} />}>
                <Sun size={15} />
              </Show>
            </button>
            <button type="button" title="设置" aria-label="设置" onClick={() => openDrawer("settings")}>
              <Settings size={15} />
            </button>
          </div>
        </header>

        <div class="error-slot">
          <Show when={error()}>
            <div class="error-strip" role="alert">
              <AlertTriangle size={16} />
              <span>{error()}</span>
              <button type="button" title="关闭错误提示" aria-label="关闭错误提示" onClick={() => setError("")}>
                <X size={14} />
              </button>
            </div>
          </Show>
        </div>

        <section
          class="chat-scroll"
          classList={{ empty: messages().length === 0 }}
          ref={chatScrollRef}
          aria-live="polite"
          onScroll={updateChatScrollState}
        >
          <For
            each={renderedMessages()}
            fallback={
              <div class="welcome-state">
                <div class="welcome-brand">
                  <span class="welcome-mark"><Command size={16} /></span>
                  <span>EchoCode</span>
                </div>
                <div class="welcome-heading">
                  <span>当前工作区</span>
                  <h1>{runtimeSettings().workspaceRoot?.trim() ? workspaceName() : "今天处理什么？"}</h1>
                  <Show when={runtimeSettings().workspaceRoot?.trim()}>
                    {(root) => <p class="welcome-path" title={root()}><FolderOpen size={14} /><span>{root()}</span></p>}
                  </Show>
                </div>
                <Show when={!runtimeSettings().workspaceRoot?.trim()}>
                  <button class="welcome-add-project" type="button" onClick={() => void handleAddProject()}>
                    <FolderOpen size={15} />添加项目
                  </button>
                </Show>
                <div class="welcome-prompt-grid" aria-label="常用任务">
                  <button class="welcome-prompt" type="button" onClick={() => primeWelcomePrompt("梳理当前项目的结构、关键模块和运行方式")}>
                    <Search size={16} /><span>梳理当前项目</span><ArrowRight size={14} />
                  </button>
                  <button class="welcome-prompt" type="button" onClick={() => primeWelcomePrompt("检查当前工作区未提交的修改并指出风险")}>
                    <GitBranch size={16} /><span>检查当前修改</span><ArrowRight size={14} />
                  </button>
                  <button class="welcome-prompt" type="button" onClick={() => primeWelcomePrompt("定位当前项目的问题并给出修复方案")}>
                    <Wrench size={16} /><span>定位并修复问题</span><ArrowRight size={14} />
                  </button>
                  <button class="welcome-prompt" type="button" onClick={() => primeWelcomePrompt("为接下来的开发任务制定一份可执行计划")}>
                    <ClipboardList size={16} /><span>制定实现计划</span><ArrowRight size={14} />
                  </button>
                </div>
              </div>
            }
          >
            {(message) => (
              <Show
                when={message.role === "user"}
                fallback={
				  <article class="op-msg assistant" classList={{ system: message.role === "system", failed: message.failed, interrupted: message.interrupted }}>
                    <MessageContent
                      parts={displayMessageParts(message.parts, message.content, message.streaming)}
                      hideTeamRun={message.streaming && activeSessionTask()?.messageID === message.id}
                      hideFileChangesSummary={message.streaming}
                      undoingChanges={rewinding()}
                      onUndoChanges={message.id === undoableFileChangeMessageID() && message.eventId
                        ? () => handleUndoMessageChanges(message)
                        : undefined}
                      onReviewChanges={openReviewPanel}
                      onPreviewFile={handlePreviewFile}
                      onOpenWorkspaceFile={handleOpenWorkspaceFile}
                      onOpenURL={requestOpenURL}
                      onRevealSecret={revealMessageSecret}
					  onOpenSubagent={openSubagentPanel}
					  onStopSubagent={stopOneSubagent}
                      isDisclosureOpen={(key) => isMessageDisclosureOpen(message.id, key)}
                      onDisclosureChange={(key, open) => setMessageDisclosureOpen(message.id, key, open)}
                    />
					<Show when={message.streaming || message.cancelled || message.interrupted}>
                      <div
                        class="op-stream-state"
                        classList={{
                          cancelled: message.cancelled || message.statusKind === "cancelled",
						  interrupted: message.interrupted,
                          waiting: message.statusKind === "waiting",
                          retrying: message.statusKind === "retrying",
                          failed: message.statusKind === "failed",
                        }}
                      >
                        <Show when={message.streaming}>
                          <Switch fallback={<span class="op-thinking-spinner" />}>
                            <Match when={message.statusKind === "compression"}>
                              <ListCollapse
                                class="op-compression-icon"
                                classList={{ completed: message.compressionStatus === "completed", error: message.compressionStatus === "error" }}
                                size={14}
                                aria-hidden="true"
                              />
                            </Match>
                            <Match when={message.statusKind === "waiting"}><Clock3 size={14} aria-hidden="true" /></Match>
                            <Match when={message.statusKind === "retrying"}><RefreshCw class="op-status-retrying" size={14} aria-hidden="true" /></Match>
                            <Match when={message.statusKind === "failed"}><AlertTriangle size={14} aria-hidden="true" /></Match>
                          </Switch>
                        </Show>
						<Show when={message.interrupted && !message.streaming}><AlertTriangle size={14} aria-hidden="true" /></Show>
						<span>{message.status || (message.interrupted ? "上次运行中断" : message.cancelled ? "已停止" : "正在生成")}</span>
						<Show when={message.streaming}>
						  <span class="op-stream-elapsed"><Clock3 size={12} aria-hidden="true" /><LiveElapsed startedAt={message.createdAt} /></span>
						</Show>
                      </div>
                    </Show>
                    <Show when={!message.streaming && message.durationMs !== undefined && (message.content || message.parts?.length)}>
                      <div class="op-message-runtime" title="本轮从提交到完成的总耗时">
                        <Clock3 size={12} aria-hidden="true" />
                        <span>已处理 {formatElapsedDuration(message.durationMs)}</span>
                      </div>
                    </Show>
                    <Show when={message.role === "assistant" && !message.streaming && (message.content || message.parts?.length)}>
                      <div class="op-message-actions assistant-actions">
                        <button type="button" title="复制回复" aria-label="复制回复" onClick={() => void copyMessage(message)}>
                          <Show when={copiedMessageID() === message.id} fallback={<Copy size={14} />}><Check size={14} /></Show>
                        </button>
                        <Show when={message.eventId}>
                          <button
                            type="button"
                            title="从这条回复分叉"
                            aria-label="从这条回复分叉"
                            disabled={currentSessionBusy() || Boolean(forkingMessageID())}
                            onClick={() => void handleForkMessage(message)}
                          >
                            <GitFork size={14} />
                          </button>
                        </Show>
                      </div>
                    </Show>
                  </article>
                }
              >
                <article class="op-msg user">
                  <div class="op-user-stack">
                    <Show
                      when={editingMessageID() === message.id}
                      fallback={
                        <div class="op-user-bubble">
                          <Show when={message.attachments?.length}>
                            <div class="op-user-images">
                              <For each={message.attachments}>
                                {(attachment) => (
                                  <Show
                                    when={isImageChatAttachment(attachment)}
                                    fallback={
                                      <div class="op-user-document" title={attachment.name}>
                                        <FileText size={18} />
                                        <span><strong>{attachment.name}</strong><small>{formatAttachmentMeta(attachment)}</small></span>
                                      </div>
                                    }
                                  >
                                    <img
                                      class="previewable-image"
                                      src={chatAttachmentURL(attachment)}
                                      alt={attachment.name}
                                      title="双击查看原图"
                                      tabIndex={0}
                                      draggable={false}
                                      onDblClick={() => setPreviewAttachment(attachment)}
                                      onKeyDown={(event) => {
                                        if (event.key === "Enter" || event.key === " ") {
                                          event.preventDefault();
                                          setPreviewAttachment(attachment);
                                        }
                                      }}
                                    />
                                  </Show>
                                )}
                              </For>
                            </div>
                          </Show>
                          <Show when={message.content}>
                            <MessageContent
                              parts={textToParts(message.content)}
                              inferFileArtifacts={false}
                              onOpenWorkspaceFile={handleOpenWorkspaceFile}
                              onOpenURL={requestOpenURL}
                            />
                          </Show>
                        </div>
                      }
                    >
                      <div class="op-message-editor">
                        <Show when={editMessageAttachments().length > 0}>
                          <div class="composer-attachments compact">
                            <For each={editMessageAttachments()}>
                              {(attachment, index) => (
                                <figure classList={{ document: isMarkdownChatAttachment(attachment) }}>
                                  <Show
                                    when={isImageChatAttachment(attachment)}
                                    fallback={<div class="composer-document-preview"><FileText size={20} /><span>{formatAttachmentMeta(attachment)}</span></div>}
                                  >
                                    <img
                                      class="previewable-image"
                                      src={chatAttachmentURL(attachment)}
                                      alt={attachment.name}
                                      title="双击查看原图"
                                      tabIndex={0}
                                      draggable={false}
                                      onDblClick={() => setPreviewAttachment(attachment)}
                                      onKeyDown={(event) => {
                                        if (event.key === "Enter" || event.key === " ") {
                                          event.preventDefault();
                                          setPreviewAttachment(attachment);
                                        }
                                      }}
                                    />
                                  </Show>
                                  <figcaption>{attachment.name}</figcaption>
                                  <button type="button" title="移除附件" aria-label={`移除 ${attachment.name}`} onClick={() => setEditMessageAttachments((current) => current.filter((_, itemIndex) => itemIndex !== index()))}><X size={12} /></button>
                                </figure>
                              )}
                            </For>
                          </div>
                        </Show>
                        <textarea
                          value={editMessageDraft()}
                          onInput={(event) => setEditMessageDraft(event.currentTarget.value)}
                          onKeyDown={(event) => {
                            if (event.key === "Escape") {
                              event.preventDefault();
                              cancelEditMessage();
                            }
                            if (event.key === "Enter" && (event.ctrlKey || event.metaKey)) {
                              event.preventDefault();
                              void handleEditResend(message);
                            }
                          }}
                          rows={Math.min(8, Math.max(2, editMessageDraft().split("\n").length))}
                          spellcheck={false}
                          disabled={editingMessageBusy()}
                        />
                        <div class="op-editor-actions">
                          <button
                            type="button"
                            title="取消编辑"
                            aria-label="取消编辑"
                            disabled={editingMessageBusy()}
                            onClick={cancelEditMessage}
                          >
                            <X size={14} />
                          </button>
                          <button
                            type="button"
                            class="primary"
                            title="发送修改"
                            aria-label="发送修改"
                            disabled={(!editMessageDraft().trim() && editMessageAttachments().length === 0) || editingMessageBusy()}
                            onClick={() => void handleEditResend(message)}
                          >
                            <ArrowUp size={14} />
                          </button>
                        </div>
                      </div>
                    </Show>
                    <Show when={editingMessageID() !== message.id}>
                      <div class="op-message-actions user-actions">
                        <button type="button" title="复制消息" aria-label="复制消息" onClick={() => void copyMessage(message)}>
                          <Show when={copiedMessageID() === message.id} fallback={<Copy size={14} />}><Check size={14} /></Show>
                        </button>
                        <Show when={message.eventId}>
                          <button
                            type="button"
                            title="编辑并重新发送"
                            aria-label="编辑并重新发送"
                            disabled={currentSessionBusy() || Boolean(forkingMessageID())}
                            onClick={() => beginEditMessage(message)}
                          >
                            <Pencil size={14} />
                          </button>
                          <button
                            type="button"
                            title="从这条消息分叉"
                            aria-label="从这条消息分叉"
                            disabled={currentSessionBusy() || Boolean(forkingMessageID())}
                            onClick={() => void handleForkMessage(message)}
                          >
                            <GitFork size={14} />
                          </button>
                        </Show>
                      </div>
                    </Show>
                  </div>
                </article>
              </Show>
            )}
          </For>
        </section>

        <div class="chat-jump-bottom-dock">
          <Show when={!chatNearBottom() && messages().length > 0}>
            <button
              class="chat-jump-bottom"
              type="button"
              title="跳到最新消息"
              aria-label="跳到最新消息"
              onClick={() => scrollChatToBottom()}
            >
              <ArrowDown size={16} />
            </button>
          </Show>
        </div>

		<Show when={Boolean(activeTaskProgress()) || activeTeamParts().length > 0 || activeSubagentParts().length > 0 || pausedTeamTask()}>
          <section
            class="execution-status-dock"
			classList={{ combined: [Boolean(activeTaskProgress()), activeTeamParts().length > 0, activeSubagentParts().length > 0, pausedTeamTask()].filter(Boolean).length > 1 }}
            aria-label="当前执行状态"
            aria-live="polite"
          >
            <Show when={activeTaskProgress()}>
              {(progress) => <TaskProgress part={progress()} />}
            </Show>
            <Show when={activeTeamParts().length > 0}>
              <TeamRun parts={activeTeamParts()} docked />
            </Show>
			<Show when={activeSubagentParts().length > 0}>
			  <SubagentDock
				parts={activeSubagentParts()}
				stoppingTaskID={stoppingSubagentTaskID()}
				onOpen={openSubagentPanel}
				onStop={stopOneSubagent}
			  />
			</Show>
			<Show when={pausedTeamTask()}>
			  <section class="team-pause-control" aria-label="暂停的 AI 团队任务">
				<div>
				  <strong>AI 团队已暂停</strong>
				  <small>{state()?.team.currentRole ? `将从 ${teamStageLabel(state()!.team.currentRole!)} 继续` : "已保留当前团队进度"}</small>
				</div>
				<div class="team-pause-actions">
				  <button type="button" disabled={teamLifecycleBusy()} onClick={() => void resumePausedTeamRun()}>
					<ArrowRight size={14} />
					继续任务
				  </button>
				  <button class="danger" type="button" disabled={teamLifecycleBusy()} onClick={() => void abandonPausedTeamRun()}>
					<X size={14} />
					结束任务
				  </button>
				</div>
			  </section>
			</Show>
          </section>
        </Show>

        <section class="composer-dock">
          <div class="composer-box">
            <div class="composer-project">
              <Folder size={14} />
              <button type="button" onClick={() => openSettings("index")}>
                {workspaceName()}
              </button>
            </div>
            <Show when={composerAttachments().length > 0}>
              <div class="composer-attachments">
                <For each={composerAttachments()}>
                  {(attachment, index) => (
                    <figure classList={{ document: isMarkdownChatAttachment(attachment) }}>
                      <Show
                        when={isImageChatAttachment(attachment)}
                        fallback={<div class="composer-document-preview"><FileText size={23} /><span>{formatAttachmentMeta(attachment)}</span></div>}
                      >
                        <img
                          class="previewable-image"
                          src={chatAttachmentURL(attachment)}
                          alt={attachment.name}
                          title="双击查看原图"
                          tabIndex={0}
                          draggable={false}
                          onDblClick={() => setPreviewAttachment(attachment)}
                          onKeyDown={(event) => {
                            if (event.key === "Enter" || event.key === " ") {
                              event.preventDefault();
                              setPreviewAttachment(attachment);
                            }
                          }}
                        />
                      </Show>
                      <figcaption>{attachment.name}</figcaption>
                      <button
                        type="button"
                        title="移除附件"
                        aria-label={`移除 ${attachment.name}`}
                        onClick={() => commitComposerSnapshot({
                          ...currentComposerSnapshot(),
                          attachments: composerAttachments().filter((_, itemIndex) => itemIndex !== index()),
                        })}
                      >
                        <X size={12} />
                      </button>
                    </figure>
                  )}
                </For>
              </div>
            </Show>
            <Show when={queuedMessages().length > 0}>
              <div class="composer-queue" aria-label={`已排队 ${queuedMessages().length} 条消息`}>
                <For each={queuedMessages()}>
                  {(queued, index) => (
                    <div class="composer-queue-item" classList={{ guidance: queued.guidance }}>
                      <span class="composer-queue-index" title={queued.guidance ? "下一条引导" : `队列第 ${index() + 1} 条`}>
                        <ListPlus size={13} />
                        {index() + 1}
                      </span>
                      <button class="composer-queue-copy" type="button" disabled={queued.guidance} title={queued.guidance ? "等待当前 Agent 应用" : "编辑这条排队消息"} onClick={() => editQueuedMessage(queued.id)}>
                        {redactSensitiveTextForDisplay(composeComposerPrompt(queued.draft, queued.links, queued.tail)) || `${queued.attachments.length} 个附件`}
                      </button>
                      <button
                        class="composer-queue-guide"
                        type="button"
                        disabled={index() === 0 && queued.guidance}
                        title={currentSessionBusy() ? "在当前步骤结束后引导正在执行的任务" : "设为队列下一条"}
                        onClick={() => void guideQueuedMessage(queued.id)}
                      >
                        <ArrowUp size={13} />
                        {queued.guidance ? "等待" : "引导"}
                      </button>
                      <button type="button" disabled={queued.guidance} title="编辑排队消息" aria-label="编辑排队消息" onClick={() => editQueuedMessage(queued.id)}><Pencil size={13} /></button>
                      <button type="button" disabled={queued.guidance} title="移除排队消息" aria-label="移除排队消息" onClick={() => setQueuedMessages((current) => removeMessage(current, queued.id))}><Trash2 size={13} /></button>
                    </div>
                  )}
                </For>
              </div>
            </Show>
			<div class="composer-rich-input" onClick={(event) => {
			  if (event.currentTarget === event.target) focusComposerEnd();
			}}>
			  <textarea
				ref={composerEditorRef}
				class="composer-text-editor"
				aria-label="向 EchoCode 提问，或描述要修改的代码"
				placeholder="向 EchoCode 提问，或描述要修改的代码"
				rows={1}
				spellcheck={false}
				onInput={(event) => {
				  const value = event.currentTarget.value;
				  commitComposerSnapshot({ ...currentComposerSnapshot(), draft: value, tail: "" });
				  resizeComposerEditor(event.currentTarget);
				  if (/\s$/.test(value)) absorbComposerURLs(value);
				}}
				onBlur={(event) => absorbComposerURLs(event.currentTarget.value)}
				onPaste={handleComposerPaste}
				onKeyDown={(event) => {
				  if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
					event.preventDefault();
					void sendMessage();
				  }
				}}
			  />
			</div>
			<Show when={composerLinks().length > 0}>
			  <div class="composer-link-row" aria-label="已识别的链接">
              <For each={composerLinks()}>
                {(link) => (
                  <span class="composer-link-chip" title={link.url}>
                    <Globe2 class="composer-link-icon" size={12} aria-hidden="true" />
                    <button type="button" class="composer-link-target" aria-label={`打开 ${link.domain}`} onClick={() => requestOpenURL(link.url)}>{link.url}</button>
                    <button type="button" class="composer-link-remove" title="移除链接" aria-label={`移除 ${link.domain}`} onClick={() => removeComposerLink(link.url)}><X size={11} /></button>
                  </span>
                )}
              </For>
			  </div>
			</Show>
            <div class="composer-toolbar">
              <div class="composer-tools">
                <input
                  ref={composerImageInputRef}
                  class="composer-image-input"
                  type="file"
                  accept="image/png,image/jpeg,image/webp,image/gif"
                  multiple
                  onChange={(event) => void addComposerImages(Array.from(event.currentTarget.files ?? []))}
                />
                <input
                  ref={composerMarkdownInputRef}
                  class="composer-image-input"
                  type="file"
                  accept=".md,.markdown,text/markdown,text/x-markdown"
                  multiple
                  onChange={(event) => void addComposerMarkdown(Array.from(event.currentTarget.files ?? []))}
                />
                <button type="button" title="添加图片" aria-label="添加图片" disabled={addingImages() || composerAttachments().filter(isImageChatAttachment).length >= 4} onClick={() => composerImageInputRef?.click()}>
                  <ImagePlus size={17} />
                </button>
                <button
                  type="button"
                  title="添加 Markdown 参考资料"
                  aria-label="添加 Markdown 参考资料"
                  disabled={addingDocuments() || composerAttachments().filter(isMarkdownChatAttachment).length >= 5}
                  onClick={() => composerMarkdownInputRef?.click()}
                >
                  <FileText size={17} />
                </button>
                <button
                  type="button"
                  title="撤销输入 (Ctrl+Z)"
                  aria-label="撤销输入"
                  disabled={composerUndoDepth() === 0}
                  onClick={undoComposerInput}
                >
                  <Undo2 size={16} />
                </button>
                <button
                  type="button"
                  title="重做输入 (Ctrl+Y)"
                  aria-label="重做输入"
                  disabled={composerRedoDepth() === 0}
                  onClick={redoComposerInput}
                >
                  <Redo2 size={16} />
                </button>
                <button
                  type="button"
                  class="access-toggle"
                  classList={{ active: fullAccess() }}
                  disabled={currentSessionBusy()}
                  title={fullAccess() ? "完全访问已开启：命令/文件修改不再逐次确认" : "点击开启完全访问（不再弹审批框）"}
                  onClick={() => void toggleFullAccess()}
                >
                  <ShieldCheck size={15} />
                  {fullAccess() ? "完全访问：开" : "完全访问"}
                </button>
                <button
                  type="button"
                  class="plan-toggle"
                  classList={{ active: planMode() }}
                  disabled={currentSessionBusy()}
                  title="Plan 模式：先规划后执行（需高/超高推理强度）"
                  onClick={() => void togglePlanMode()}
                >
                  <ClipboardList size={15} />
                  {planMode() ? "计划模式：开" : "计划模式"}
                </button>
                <button
                  type="button"
                  class="team-toggle"
                  classList={{ active: teamMode() }}
                  disabled={currentSessionBusy()}
                  title="AI 团队：规划、实现、测试、审阅和汇总使用可独立配置的模型"
                  onClick={() => void toggleTeamMode()}
                >
                  <Users size={15} />
                  {teamMode() ? "团队：开" : "AI 团队"}
                </button>
              </div>
              <div class="composer-send-controls">
                <ModelRouteMenu
                  settings={runtimeSettings()}
                  saving={savingRuntime() || currentSessionBusy()}
                  onManage={() => openSettings("models")}
                  onSelect={(providerID, modelID) => void selectModelRoute(providerID, modelID)}
                />
                <ReasoningMenu
                  value={pendingReasoningLevel() ?? profile().id}
                  options={options()}
                  running={updatingReasoning() || (currentSessionBusy() && pendingReasoningLevel() !== undefined)}
                  onChange={changeReasoning}
                />
                <Show when={currentSessionBusy()}>
                  <button
                    class="queue-send-button"
                    type="button"
                    disabled={!canSend()}
                    onClick={() => enqueueCurrentMessage()}
                    title="将当前消息加入队列"
                    aria-label="将当前消息加入队列"
                  >
                    <ListPlus size={16} />
                  </button>
                </Show>
                <button
                  class="send-button"
                  classList={{ stop: currentSessionBusy() }}
                  type="button"
                  disabled={currentSessionBusy() ? !activeChatTaskID() : !canSend()}
                  onClick={() => void (currentSessionBusy() ? stopMessage() : sendMessage())}
                  title={currentSessionBusy() ? "停止生成" : "发送"}
                >
                  <Show when={currentSessionBusy()} fallback={<ArrowUp size={17} />}>
                    <Square size={13} fill="currentColor" />
                  </Show>
                </button>
              </div>
            </div>
          </div>
        </section>

        <WorkspaceToolsPanel
          open={workspaceToolsOpen()}
          workspaceRoot={runtimeSettings().workspaceRoot}
          shellAccess={runtimeSettings().shellAccess}
          readOnly={runtimeSettings().sandboxMode === "read-only" || runtimeSettings().filesystemAccess === "read-only"}
          onClose={() => setWorkspaceToolsOpen(false)}
        />
      </section>

      <Show when={!drawerOpen() && (Boolean(browserPreview()) || reviewOpen() || Boolean(selectedSubagent()))}>
        <>
          <div
            class="browser-panel-resizer"
            role="separator"
            aria-label="调整右侧面板宽度"
            aria-orientation="vertical"
            aria-valuemin={Math.round(browserPanelLimits().min)}
            aria-valuemax={Math.round(browserPanelLimits().max)}
            aria-valuenow={Math.round(browserPanelWidth())}
            tabIndex={0}
            title="拖拽调整右侧面板宽度，双击恢复默认"
            onPointerDown={startBrowserResize}
            onPointerMove={moveWindowPointerBrowserResize}
            onPointerUp={stopBrowserResize}
            onPointerCancel={stopBrowserResize}
            onMouseDown={startMouseBrowserResize}
            onDblClick={resetBrowserPanelWidth}
            onKeyDown={(event) => {
              if (event.key === "ArrowLeft") {
                event.preventDefault();
                nudgeBrowserPanelWidth(1);
              }
              if (event.key === "ArrowRight") {
                event.preventDefault();
                nudgeBrowserPanelWidth(-1);
              }
              if (event.key === "Home") {
                event.preventDefault();
                setAndPersistBrowserPanelWidth(browserPanelLimits().min);
              }
              if (event.key === "End") {
                event.preventDefault();
                setAndPersistBrowserPanelWidth(browserPanelLimits().max);
              }
              if (event.key === "Enter") {
                event.preventDefault();
                resetBrowserPanelWidth();
              }
            }}
          />
          <SidePanelHost
            browser={browserPreview()}
            reviewOpen={reviewOpen()}
			subagent={selectedSubagent()}
			parentTaskID={activeSessionTask()?.taskID}
			stoppingSubagentID={stoppingSubagentTaskID()}
            activeView={sidePanelView()}
            workspaceRoot={runtimeSettings().workspaceRoot}
            fileRequest={workspaceFileRequest()}
            dark={themeMode() === "dark"}
            browserSuspended={browserSurfaceSuspended()}
            annotationPolicy={runtimeDraft()?.browser.screenshotAnnotations ?? "never"}
            credentials={runtimeDraft()?.browser.credentials ?? []}
            onSelectView={setSidePanelView}
            onCloseBrowser={closeBrowserPanel}
            onCloseFiles={closeReviewPanel}
			onStopSubagent={stopOneSubagent}
			onCloseSubagent={closeSubagentPanel}
          />
        </>
      </Show>
      </div>

      <Show when={drawerOpen()}>
        <aside class="settings-screen" aria-label="EchoCode 设置">
          <div class="drawer-head">
            <button class="settings-back" type="button" onClick={closeDrawer}>
              <ArrowLeft size={16} />
              <span>返回工作区</span>
            </button>
            <strong>EchoCode 设置</strong>
            <button class="ghost-icon" type="button" title="关闭设置" onClick={closeDrawer}>
              <X size={16} />
            </button>
          </div>
          <SettingsCenter
            activeCategory={activeSettingsCategory()}
			applyWorkbenchState={setState}
            apiKeyDraft={apiKeyDraft()}
            cacheHealth={cacheHealth()}
            cacheHitRate={cacheHitRate()}
            cacheTarget={cacheTarget()}
            clearingKey={clearingKey()}
            clearKey={clearKey}
            contextPreview={contextPreview()}
            deepSeek={deepSeek()}
            deepSeekSession={deepSeekSession()}
            diagnostics={diagnostics()}
            hasCacheTokens={hasCacheTokens()}
            mcpServers={mcpServers()}
            models={deepSeek().models}
            nudgeSidebarWidth={nudgeSidebarWidth}
            profile={profile()}
            projectMemory={state()?.projectMemory ?? {
              enabled: true,
              projectName: "EchoCode",
              sessionCount: 0,
              turnCount: 0,
              summary: "Project: EchoCode",
            }}
            projectTree={projectTree()}
            uiAppearance={uiAppearance()}
            effectiveUIScale={effectiveUIScale()}
            updateUIAppearance={updateUIAppearance}
            resetUIAppearance={resetUIAppearance}
            refreshMCPServer={refreshMCPRuntime}
            refreshingMCPID={refreshingMCPID()}
			plugins={pluginStatuses()}
			pluginBusy={pluginBusy()}
			installPlugin={installLocalPlugin}
			importSkillMarkdown={importUserSkillMarkdown}
			refreshPlugins={refreshPluginRuntime}
			revealPlugin={openInstalledPlugin}
			uninstallPlugin={removeInstalledPlugin}
			providerKeyDrafts={providerKeyDrafts()}
			providerBillingKeyDrafts={providerBillingKeyDrafts()}
            reasoningOptions={options()}
            runtimeDraft={activeRuntimeDraft()}
            sandboxCapabilities={state()?.sandboxCapabilities ?? {
              platform: "browser",
              backend: "preview-only",
              processTree: false,
              resourceLimits: false,
              privilegeIsolation: false,
              filesystemIsolation: false,
              networkIsolation: false,
              summary: "桌面运行时连接后显示系统沙箱能力。",
            }}
            runtimeDirty={Boolean(runtimeDraft()) || hasProviderKeyDrafts()}
			configFiles={configFiles()}
			clearProviderKey={clearProviderKey}
			clearProviderBillingKey={clearProviderBillingKey}
            deleteProvider={removeModelProvider}
			saveKey={saveKey}
			saveProviderKey={saveProviderKey}
			saveProviderBillingKey={saveProviderBillingKey}
            saveRuntime={saveRuntime}
			clearingProviderID={clearingProviderID()}
			clearingProviderBillingID={clearingProviderBillingID()}
            deletingProviderID={deletingProviderID()}
			savingProviderID={savingProviderID()}
			savingProviderBillingID={savingProviderBillingID()}
            savingKey={savingKey()}
            savingRuntime={savingRuntime()}
            selectCategory={setActiveSettingsCategory}
            sessionHasCacheTokens={sessionHasCacheTokens()}
            setAPIKeyDraft={setAPIKeyDraft}
            sidebarWidth={sidebarWidth()}
            skills={skillsIndex()}
            snapshots={snapshots()}
            testConnection={testConnection}
            testingDeepSeek={testingDeepSeek()}
			setProviderKeyDraft={setProviderKeyDraft}
			setProviderBillingKeyDraft={setProviderBillingKeyDraft}
            syncProviderModels={syncProviderModels}
            syncingProviderID={syncingProviderID()}
            themeMode={themeMode()}
            toggleTheme={toggleTheme}
            updateReasoning={changeReasoning}
            updateRuntimeDraft={updateRuntimeDraft}
            updatingReasoning={updatingReasoning()}
            resetRuntimeDraft={resetRuntimeDraft}
            resetSidebarWidth={resetSidebarWidth}
			usage={usage()}
			usageLedger={state()?.usageLedger}
			usageBillingReports={usageBillingReports()}
			loadUsageBillingReport={loadUsageBillingReport}
			syncUsageBilling={syncUsageBillingReport}
			reconcileUsageBilling={reconcileUsageBillingReport}
            confirmAction={confirmAction}
          />
        </aside>
      </Show>

      <Show when={timelineOpen()}>
        <TimelinePanel
          checkpoints={checkpoints()}
          branches={branches()}
          onRewind={handleRewind}
          onSwitchBranch={handleSwitchBranch}
          onClose={() => setTimelineOpen(false)}
          busy={rewinding()}
        />
      </Show>

      <Show when={activeApproval()}>
        {(req) => <ApprovalModal request={req()} busy={approvalBusy()} onDecide={decideApproval} />}
      </Show>

      <ConfirmDialog request={confirmation()} onResolve={resolveConfirmation} />

      <Show when={pendingLinkURL()}>
        <div class="link-open-overlay" role="presentation" onPointerDown={(event) => {
          if (event.currentTarget === event.target && !linkOpenBusy()) setPendingLinkURL("");
        }}>
          <section class="link-open-dialog" role="dialog" aria-modal="true" aria-label="打开链接">
            <div class="link-open-head">
              <Globe2 size={16} />
              <strong>打开链接</strong>
              <button type="button" title="取消" aria-label="取消" disabled={Boolean(linkOpenBusy())} onClick={() => setPendingLinkURL("")}><X size={14} /></button>
            </div>
            <code>{pendingLinkURL()}</code>
            <div class="link-open-actions">
              <button type="button" disabled={Boolean(linkOpenBusy())} onClick={() => void openPendingLink("internal")}>
                <Globe2 size={15} />内置浏览器
              </button>
              <button type="button" disabled={Boolean(linkOpenBusy())} onClick={() => void openPendingLink("external")}>
                <ExternalLink size={15} />系统浏览器
              </button>
            </div>
          </section>
        </div>
      </Show>

      <Show when={previewAttachment()}>
        {(attachment) => <ImagePreviewModal attachment={attachment()} onClose={() => setPreviewAttachment(undefined)} />}
      </Show>
    </main>
  );
}

async function writeClipboardText(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }
  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.style.position = "fixed";
  textarea.style.opacity = "0";
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  textarea.remove();
  if (!copied) throw new Error("复制失败，请检查剪贴板权限。");
}

function chatAttachmentURL(attachment: ChatAttachment): string {
  return `data:${attachment.mimeType};base64,${attachment.data}`;
}

function chatAttachmentKind(attachment: ChatAttachment): "image" | "document" | "unknown" {
  if (attachment.kind === "image" || attachment.kind === "document") return attachment.kind;
  const mimeType = attachment.mimeType.toLowerCase();
  if (mimeType.startsWith("image/")) return "image";
  if (["text/markdown", "text/x-markdown", "text/plain"].includes(mimeType)) return "document";
  return "unknown";
}

function isImageChatAttachment(attachment: ChatAttachment): boolean {
  return chatAttachmentKind(attachment) === "image";
}

function isMarkdownChatAttachment(attachment: ChatAttachment): boolean {
  return chatAttachmentKind(attachment) === "document";
}

function isMarkdownFile(file: File): boolean {
  const extension = file.name.toLowerCase();
  return extension.endsWith(".md") || extension.endsWith(".markdown") || ["text/markdown", "text/x-markdown"].includes(file.type.toLowerCase());
}

function formatAttachmentMeta(attachment: ChatAttachment): string {
  if (isMarkdownChatAttachment(attachment)) {
    const characters = attachment.characterCount;
    if (typeof characters === "number" && characters >= 0) return `${characters.toLocaleString("zh-CN")} 字符`;
    return "Markdown";
  }
  return attachment.mimeType;
}

function approximateBase64Bytes(data: string): number {
  const padding = data.endsWith("==") ? 2 : data.endsWith("=") ? 1 : 0;
  return Math.max(0, Math.floor((data.length * 3) / 4) - padding);
}

async function readChatImage(file: File, index: number): Promise<ChatAttachment> {
  const mimeType = file.type.toLowerCase() === "image/jpg" ? "image/jpeg" : file.type.toLowerCase();
  if (!["image/png", "image/jpeg", "image/webp", "image/gif"].includes(mimeType)) {
    throw new Error(`不支持图片格式 ${file.type || file.name}，请使用 PNG、JPEG、WebP 或 GIF。`);
  }
  if (file.size === 0) throw new Error(`${file.name || "图片"} 为空。`);
  if (file.size > 6 * 1024 * 1024) throw new Error(`${file.name || "图片"} 超过 6 MB。`);
  const dataURL = await new Promise<string>((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error ?? new Error("读取图片失败。"));
    reader.readAsDataURL(file);
  });
  const comma = dataURL.indexOf(",");
  if (comma < 0 || !dataURL.slice(comma + 1)) throw new Error("读取图片失败。");
  const extension = mimeType === "image/jpeg" ? "jpg" : mimeType.slice("image/".length);
  return {
    kind: "image",
    name: file.name || `clipboard-${Date.now()}-${index + 1}.${extension}`,
    mimeType,
    data: dataURL.slice(comma + 1),
    size: file.size,
  };
}

async function readChatMarkdown(file: File, index: number): Promise<ChatAttachment> {
  if (!isMarkdownFile(file)) {
    throw new Error(`不支持文档格式 ${file.type || file.name}，请选择 .md 或 .markdown 文件。`);
  }
  if (file.size === 0) throw new Error(`${file.name || "Markdown 文件"} 为空。`);
  if (file.size > 128 * 1024) throw new Error(`${file.name || "Markdown 文件"} 超过 128 KiB。`);
  let text: string;
  try {
    text = new TextDecoder("utf-8", { fatal: true }).decode(await file.arrayBuffer()).replace(/^\uFEFF/, "");
  } catch {
    throw new Error(`${file.name || "Markdown 文件"} 不是有效的 UTF-8 文本。`);
  }
  if (!text || text.includes("\0")) throw new Error(`${file.name || "Markdown 文件"} 不是有效的 Markdown 文本。`);
  const bytes = new TextEncoder().encode(text);
  return {
    kind: "document",
    name: file.name || `reference-${Date.now()}-${index + 1}.md`,
    mimeType: "text/markdown",
    data: bytesToBase64(bytes),
    size: bytes.byteLength,
    characterCount: Array.from(text).length,
  };
}

function bytesToBase64(bytes: Uint8Array): string {
  let binary = "";
  for (let offset = 0; offset < bytes.length; offset += 0x8000) {
    binary += String.fromCharCode(...bytes.subarray(offset, offset + 0x8000));
  }
  return btoa(binary);
}

function extractComposerLinks(value: string): ComposerLink[] {
  const matches = value.match(/https?:\/\/[^\s<>"'`]+/gi) ?? [];
  const links: ComposerLink[] = [];
  const seen = new Set<string>();
  for (const match of matches) {
    const raw = match.replace(/[.,;:!?，。；：！？)\]}>》】）]+$/g, "");
    if (!raw || seen.has(raw)) continue;
    try {
      const parsed = new URL(raw);
      if (parsed.protocol !== "http:" && parsed.protocol !== "https:") continue;
      seen.add(raw);
      const domain = parsed.hostname.replace(/^www\./, "").toLowerCase();
      const segments = parsed.pathname.split("/").filter(Boolean);
      const label = domain === "github.com" && segments.length >= 2
        ? `${segments[0]}/${segments[1]}`
        : `${decodeURIComponent(parsed.pathname || "/")}${parsed.search}`;
      links.push({ url: raw, domain, label });
      if (links.length >= 3) break;
    } catch {
      // 输入中的半成品 URL 保持普通文本，完成后再显示预览。
    }
  }
  return links;
}

function removeComposerURLs(value: string, links: ComposerLink[]): string {
  let result = value;
  for (const link of links) {
    result = result.replace(`\`${link.url}\``, "").replace(link.url, "");
  }
  return result
    .replace(/[ \t]{2,}/g, " ")
    .replace(/ +\n/g, "\n")
    .trim();
}

function mergeComposerLinks(current: ComposerLink[], incoming: ComposerLink[]): ComposerLink[] {
  const merged = [...current];
  const seen = new Set(current.map((link) => link.url));
  for (const link of incoming) {
    if (seen.has(link.url)) continue;
    seen.add(link.url);
    merged.push(link);
    if (merged.length >= 3) break;
  }
  return merged;
}

function composeComposerPrompt(text: string, links: ComposerLink[], tail = ""): string {
  return [text.trim(), ...links.map((link) => link.url), tail.trim()].filter(Boolean).join(" ");
}

function splitComposerPrompt(value: string): { text: string; tail: string; links: ComposerLink[] } {
  const links = extractComposerLinks(value);
  return { text: removeComposerURLs(value, links), tail: "", links };
}

function composerEditableText(draft: string, tail: string): string {
  if (!tail) return draft;
  if (!draft) return tail;
  return `${draft}${/\s$/.test(draft) || /^\s/.test(tail) ? "" : " "}${tail}`;
}

function resizeComposerEditor(editor: HTMLTextAreaElement): void {
  editor.style.height = "auto";
  editor.style.height = `${Math.min(118, Math.max(30, editor.scrollHeight))}px`;
}

function placeCaretAtEnd(element: HTMLElement): void {
	if (element instanceof HTMLTextAreaElement || element instanceof HTMLInputElement) {
	  const end = element.value.length;
	  element.setSelectionRange(end, end);
	  return;
	}
  const selection = window.getSelection();
  if (!selection) return;
  const range = document.createRange();
  range.selectNodeContents(element);
  range.collapse(false);
  selection.removeAllRanges();
  selection.addRange(range);
}

function insertTextAtSelection(editor: HTMLTextAreaElement | undefined, text: string): void {
  if (!editor || !text) return;
  editor.focus();
	const start = editor.selectionStart ?? editor.value.length;
	const end = editor.selectionEnd ?? start;
	editor.setRangeText(text, start, end, "end");
}

function streamStatusKind(status?: string): ChatMessage["statusKind"] {
  switch (status) {
    case "waiting":
    case "retrying":
    case "failed":
    case "cancelled":
    case "completed":
      return status;
    case "running":
      return "running";
    default:
      return undefined;
  }
}

function toolEventMessage(event: ChatTaskEvent): string {
	if (event.message?.trim()) return event.message.trim();
	const name = event.toolName || "工具";
	switch (event.status) {
		case "waiting":
			return `正在等待 ${name}`;
		case "retrying":
			return `正在重试 ${name}`;
		case "error":
		case "failed":
			return `${name} 执行失败`;
		case "completed":
		case "ok":
			return `${name} 已完成`;
		default:
			return `正在运行 ${name}`;
	}
}

function toolEventStatusKind(status?: string): ChatMessage["statusKind"] {
	return status === "waiting" || status === "retrying" ? status : undefined;
}

function chatFailureMessage(message: string): string {
  const normalized = message.trim();
  if (/\b502\b|bad gateway/i.test(normalized)) {
    return "模型服务暂时不可用（HTTP 502）。EchoCode 已自动重试，请稍后重试或切换模型供应商。";
  }
  if (/\b429\b|rate.?limit|too many requests/i.test(normalized)) {
    return "模型服务请求过于频繁（HTTP 429）。请稍后重试或切换模型供应商。";
  }
  if (/context deadline exceeded|timed? out|\bEOF\b/i.test(normalized)) {
    return "模型服务连接超时。请检查网络后重试，或切换模型供应商。";
  }
  return normalized || "模型请求失败。";
}

function LiveElapsed(props: { startedAt: string }) {
	const startedAt = new Date(props.startedAt).getTime();
	const elapsedNow = () => Math.max(0, Date.now() - (Number.isFinite(startedAt) ? startedAt : Date.now()));
	const [elapsed, setElapsed] = createSignal(elapsedNow());
	const timer = window.setInterval(() => setElapsed(elapsedNow()), 1_000);
	onCleanup(() => window.clearInterval(timer));
	return <span>{formatElapsedDuration(elapsed())}</span>;
}

function updateLiveToolParts(parts: MessagePart[] | undefined, event: ChatTaskEvent): MessagePart[] {
  const name = event.toolName?.trim();
  if (!name) {
    return parts ?? [];
  }
  const toolCallId = name === "web_search" ? "live-web_search" : event.toolCallId || `live-${name}`;
  const status: "running" | "ok" | "error" = event.status === "error"
    ? "error"
    : event.status === "running"
      ? "running"
      : "ok";
  const next = settleLiveTimelineParts(parts);
  const index = next.findIndex(
    (part) => part.kind === "tool_call" && part.toolCallId === toolCallId,
  );
  const livePart: MessagePart = {
    kind: "tool_call",
    name,
    status,
    toolCallId,
    input: event.toolInput,
    output: status === "running" ? undefined : event.message,
  };
  if (index >= 0) {
    const currentPart = next[index] as Extract<MessagePart, { kind: "tool_call" }>;
    next[index] = isTerminalToolStatus(currentPart.status) && livePart.status === "running"
      ? currentPart
      : { ...currentPart, ...livePart } as MessagePart;
  } else {
    next.push(livePart);
  }
  return next;
}

function flushLiveAssistantMessage(message: ChatMessage): ChatMessage {
  if (!message.content.trim()) return message;
  return {
    ...message,
    content: "",
    parts: appendLiveAssistantText(message.parts, message.content),
  };
}

function findTaskProgress(parts: MessagePart[] | undefined): TaskProgressPart | undefined {
  return parts?.find((part): part is TaskProgressPart => part.kind === "task_progress");
}

function cloneTaskProgress(progress: TaskProgressPart): TaskProgressPart {
  return {
    ...progress,
    steps: progress.steps.map((step) => ({ ...step })),
  };
}

function updateLiveProgressPart(
  parts: MessagePart[] | undefined,
  progress: Extract<MessagePart, { kind: "task_progress" }> | undefined,
): MessagePart[] {
  if (!progress) {
    return parts ?? [];
  }
  const next = [...(parts ?? [])];
  const index = next.findIndex((part) => part.kind === "task_progress");
  if (index >= 0) {
    next[index] = progress;
  } else {
    next.push(progress);
  }
  return next;
}

function updateLiveTeamPart(parts: MessagePart[] | undefined, event: ChatTaskEvent): MessagePart[] {
  const team = event.team;
  if (!team) {
    return parts ?? [];
  }
  const next = [...(parts ?? [])];
  const attempt = team.attempt || 1;
  const index = next.findIndex(
    (part) => part.kind === "team_role" && part.role === team.role && (part.attempt || 1) === attempt,
  );
  const rolePart: MessagePart = {
    kind: "team_role",
    role: team.role,
    roleLabel: team.label,
    providerId: team.providerId,
    model: team.model,
    status: team.status,
    summary: team.error || team.summary,
    verdict: team.verdict,
    attempt,
  };
  if (index >= 0) {
    next[index] = rolePart;
  } else {
    next.push(rolePart);
  }
  return next;
}

function teamRoleLabel(role: TeamPart["role"]): string {
  switch (role) {
    case "planner": return "规划";
    case "implementer": return "实现";
    case "tester": return "测试";
    case "reviewer": return "审阅";
    case "synthesizer": return "汇总";
    default: {
      const normalized = String(role || "").trim();
      return normalized ? `协作任务（${normalized}）` : "协作任务";
    }
  }
}

function teamStageLabel(stage: string): string {
  switch (stage) {
    case "plan_approval": return "计划确认";
    case "finalize": return "结果整理";
    default: return teamRoleLabel(stage as TeamPart["role"]);
  }
}

function messageHasTrackedFileChanges(message: ChatMessage): boolean {
  return Boolean(message.parts?.some((part) =>
    part.kind === "diff"
    || (part.kind === "file" && (part.created === true || part.fileAction === "created" || part.fileAction === "modified")),
  ));
}

function mergeLiveToolResultParts(
  current: MessagePart[],
  resultParts: MessagePart[] | undefined,
): MessagePart[] {
  if (!resultParts?.length) {
    return current;
  }
  const next = [...current];
  for (const part of resultParts) {
    if (part.kind === "tool_call") {
      let index = -1;
	  if (part.toolCallId) {
		for (let candidate = next.length - 1; candidate >= 0; candidate--) {
		  const currentPart = next[candidate];
		  if (currentPart.kind === "tool_call" && currentPart.toolCallId === part.toolCallId) {
			index = candidate;
			break;
		  }
		}
	  }
	  if (index < 0) {
      for (let candidate = next.length - 1; candidate >= 0; candidate--) {
        const currentPart = next[candidate];
        if (currentPart.kind !== "tool_call" || currentPart.name !== part.name) continue;
		if (part.toolCallId && currentPart.toolCallId && part.toolCallId !== currentPart.toolCallId) continue;
        if (part.input && currentPart.input && part.input !== currentPart.input) continue;
        index = candidate;
        break;
      }
	  }
      if (index >= 0) {
        const currentPart = next[index] as Extract<MessagePart, { kind: "tool_call" }>;
        next[index] = isTerminalToolStatus(currentPart.status) && part.status === "running"
          ? currentPart
		  : { ...currentPart, ...part, toolCallId: part.toolCallId || currentPart.toolCallId };
      } else {
        next.push(part);
      }
      continue;
    }
    if (part.kind === "task_progress") {
      continue;
    }
	if (part.kind === "timeline_note") {
	  const index = next.findIndex((item) => item.kind === "timeline_note" && (
		part.toolCallId ? item.toolCallId === part.toolCallId : item.message === part.message
	  ));
	  if (index >= 0) {
		const currentPart = next[index] as Extract<MessagePart, { kind: "timeline_note" }>;
		const currentTerminal = ["completed", "failed", "cancelled", "interrupted"].includes(currentPart.status || "");
		const incomingTerminal = ["completed", "failed", "cancelled", "interrupted"].includes(part.status || "");
		next[index] = currentTerminal && !incomingTerminal
		  ? { ...part, status: currentPart.status, startedAt: currentPart.startedAt, completedAt: currentPart.completedAt }
		  : { ...currentPart, ...part };
	  } else {
		next.push(part);
	  }
	  continue;
	}
    if (part.kind === "web_search_results") {
      const index = next.findIndex((item) => item.kind === "web_search_results");
      if (index >= 0) {
        next[index] = mergeWebSearchMessageParts(next[index] as Extract<MessagePart, { kind: "web_search_results" }>, part);
      } else {
        next.push(part);
      }
      continue;
    }
    if (part.kind === "provider_notice") {
      const identity = providerNoticeIdentity(part);
      const index = next.findIndex((item) => item.kind === "provider_notice" && providerNoticeIdentity(item) === identity);
      if (index >= 0) {
        next[index] = { ...next[index], ...part } as MessagePart;
      } else {
        next.push(part);
      }
      continue;
    }
    if (part.kind === "subagent") {
      const index = next.findIndex((item) => item.kind === "subagent" && item.taskId === part.taskId);
      if (index >= 0) {
        const currentPart = next[index] as Extract<MessagePart, { kind: "subagent" }>;
        const currentTerminal = ["completed", "error", "cancelled"].includes(currentPart.status || "");
        const incomingTerminal = ["completed", "error", "cancelled"].includes(part.status || "");
        next[index] = currentTerminal && !incomingTerminal
          ? currentPart
          : { ...currentPart, ...part } as MessagePart;
      } else {
        next.push(part);
      }
      continue;
    }
    next.push(part);
  }
  return next;
}

function isTerminalToolStatus(status: Extract<MessagePart, { kind: "tool_call" }>["status"]): boolean {
  return status === "ok" || status === "error";
}

function providerNoticeIdentity(part: Extract<MessagePart, { kind: "provider_notice" }>): string {
  return [
    part.noticeKind,
    part.requestedModel,
    part.effectiveModel,
    part.retryModel,
    part.errorCode,
    part.httpStatus,
    part.message,
    ...(part.useCases ?? []),
    ...(part.reasons ?? []),
    ...(part.verifications ?? []),
    ...(part.metadataKeys ?? []),
  ].join("\u0000");
}

function mergeWebSearchMessageParts(
  current: Extract<MessagePart, { kind: "web_search_results" }>,
  incoming: Extract<MessagePart, { kind: "web_search_results" }>,
): Extract<MessagePart, { kind: "web_search_results" }> {
  const sources = [...current.sources];
  const seen = new Set(sources.map((source) => source.url.trim()));
  for (const source of incoming.sources) {
    const key = source.url.trim();
    if (!key || seen.has(key)) continue;
    if (sources.length >= 16) break;
    seen.add(key);
    sources.push(source);
  }
  const query = current.query && incoming.query && current.query !== incoming.query && !current.query.includes("含补充搜索")
    ? `${current.query}（含补充搜索）`
    : current.query || incoming.query;
  return { ...current, query, sources };
}

function settleLiveProgress(
  parts: MessagePart[] | undefined,
  taskStatus: "completed" | "failed" | "cancelled",
): MessagePart[] | undefined {
  if (!parts) {
    return parts;
  }
  return parts.map((part) => part.kind === "task_progress" ? { ...part, taskStatus } : part);
}

export default App;
