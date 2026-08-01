import { defaultReasoningLevel, reasoningOptions } from "../state/reasoning";
import { defaultTeamSettings } from "../team-config";
import type { AppInfo, AutomationState, AutomationTask, ChatAttachment, ChatResult, ChatTaskEvent, ChatTaskState, ApprovalRequest, BranchInfo, BrowserFrame, BrowserInspector, BrowserPreview, BrowserState, CheckpointInfo, GitDiff, GitStatus, MessagePart, OpenSourceLicense, ProjectInfo, ProjectNode, SecretResultReveal, SessionInfo, SessionMessage, ReasoningLevel, RuntimeSettings, ServerSnapshot, SkillDetail, SkillImportResult, SkillIndexEntry, TerminalSessionState, UpdateState, UsageBillingReconciliationInput, UsageBillingReport, UsageBillingSyncInput, WorkbenchState, WorkspaceDirectoryListing, WorkspaceFilePreview } from "../types";

type WailsAppBinding = {
  GetWorkbenchState: () => Promise<WorkbenchState>;
  SetReasoningLevel: (level: ReasoningLevel) => Promise<WorkbenchState>;
  SaveDeepSeekAPIKey: (apiKey: string) => Promise<WorkbenchState>;
  ClearDeepSeekAPIKey: () => Promise<WorkbenchState>;
  TestDeepSeekConnection: () => Promise<WorkbenchState>;
  SendChatMessage?: (prompt: string) => Promise<ChatResult>;
  SendDeepSeekMessage: (prompt: string) => Promise<ChatResult>;
  StartChatMessage?: (prompt: string) => Promise<string>;
  StartChatMessageWithAttachments?: (prompt: string, attachments: ChatAttachment[]) => Promise<string>;
  StartChatMessageForSession?: (sessionID: string, prompt: string) => Promise<string>;
  StartChatMessageForSessionWithAttachments?: (sessionID: string, prompt: string, attachments: ChatAttachment[]) => Promise<string>;
  StartChatMessageForProjectSession?: (projectID: string, sessionID: string, prompt: string) => Promise<string>;
  StartChatMessageForProjectSessionWithAttachments?: (projectID: string, sessionID: string, prompt: string, attachments: ChatAttachment[]) => Promise<string>;
	ResumePausedTeamTask?: (projectID: string, sessionID: string) => Promise<string>;
	AbandonPausedTeamTask?: (projectID: string, sessionID: string) => Promise<WorkbenchState>;
  GuideChatMessage?: (taskID: string, guidanceID: string, prompt: string) => Promise<boolean>;
  GuideChatMessageWithAttachments?: (taskID: string, guidanceID: string, prompt: string, attachments: ChatAttachment[]) => Promise<boolean>;
  StopChatMessage?: (taskID: string) => Promise<boolean>;
  StopSubagent?: (parentTaskID: string, subagentTaskID: string) => Promise<boolean>;
  GetActiveChatTask?: () => Promise<ChatTaskState | null>;
  GetActiveChatTasks?: () => Promise<ChatTaskState[]>;
	RevealSecretResult?: (projectID: string, sessionID: string, secretID: string) => Promise<SecretResultReveal>;
  ResetDeepSeekSession: () => Promise<WorkbenchState>;
  SaveRuntimeSettings: (settings: RuntimeSettings) => Promise<WorkbenchState>;
  GetAppInfo?: () => Promise<AppInfo>;
  GetOpenSourceLicenses?: () => Promise<OpenSourceLicense[]>;
  GetUpdateState?: () => Promise<UpdateState>;
  CheckForUpdates?: () => Promise<UpdateState>;
  DownloadUpdate?: () => Promise<UpdateState>;
  InstallUpdate?: () => Promise<UpdateState>;
  OpenUpdateReleasePage?: () => Promise<void>;
  OpenAppRepositoryPage?: () => Promise<void>;
  RevealAppExecutable?: () => Promise<void>;
  RevealAppConfigFile?: () => Promise<void>;
  GetAutomationState?: () => Promise<AutomationState>;
  SaveAutomationTask?: (task: AutomationTask) => Promise<AutomationState>;
  DeleteAutomationTask?: (taskID: string) => Promise<AutomationState>;
  SetAutomationTaskEnabled?: (taskID: string, enabled: boolean) => Promise<AutomationState>;
  RunAutomationTaskNow?: (taskID: string) => Promise<AutomationState>;
  StopAutomationTask?: (taskID: string) => Promise<AutomationState>;
  SaveModelProviderAPIKey: (providerID: string, apiKey: string) => Promise<WorkbenchState>;
  ClearModelProviderAPIKey: (providerID: string) => Promise<WorkbenchState>;
	SaveModelProviderBillingAPIKey?: (providerID: string, apiKey: string) => Promise<WorkbenchState>;
	ClearModelProviderBillingAPIKey?: (providerID: string) => Promise<WorkbenchState>;
	GetUsageBillingReport?: (providerID: string) => Promise<UsageBillingReport>;
	ReconcileUsageBilling?: (input: UsageBillingReconciliationInput) => Promise<UsageBillingReport>;
	SyncUsageBilling?: (input: UsageBillingSyncInput) => Promise<UsageBillingReport>;
  DeleteModelProvider?: (providerID: string) => Promise<WorkbenchState>;
  RefreshModelProviderModels: (providerID: string) => Promise<WorkbenchState>;
  RefreshMCPServer?: (serverID: string) => Promise<WorkbenchState>;
	RefreshPlugins?: () => Promise<WorkbenchState>;
	SelectPluginDirectory?: () => Promise<string>;
	InstallPlugin?: (source: string) => Promise<WorkbenchState>;
	UninstallPlugin?: (id: string) => Promise<WorkbenchState>;
	RevealPlugin?: (id: string) => Promise<void>;
  ListCheckpoints?: () => Promise<CheckpointInfo[]>;
  RewindToCheckpoint?: (checkpointID: string) => Promise<WorkbenchState>;
  ListBranches?: () => Promise<BranchInfo[]>;
  SwitchBranch?: (leafID: string) => Promise<WorkbenchState>;
  ForkFromMessage?: (messageEventID: string) => Promise<WorkbenchState>;
  ForkFromMessageForProjectSession?: (projectID: string, sessionID: string, messageEventID: string) => Promise<WorkbenchState>;
  RespondApproval?: (id: string, tool: string, approved: boolean, scope: string) => Promise<void>;
  SetPlanMode?: (enabled: boolean) => Promise<WorkbenchState>;
  ListProjects?: () => Promise<ProjectInfo[]>;
  ListSessions?: () => Promise<SessionInfo[]>;
  GetProjectTree?: () => Promise<ProjectNode[]>;
  GetSessionMessages?: () => Promise<SessionMessage[]>;
  GetSessionMessagesForSession?: (sessionID: string) => Promise<SessionMessage[]>;
  GetSessionMessagesForProjectSession?: (projectID: string, sessionID: string) => Promise<SessionMessage[]>;
  CreateProject?: (name: string, workspaceRoot: string) => Promise<WorkbenchState>;
  SwitchProject?: (projectID: string) => Promise<WorkbenchState>;
  SetProjectPinned?: (projectID: string, pinned: boolean) => Promise<WorkbenchState>;
  RenameProject?: (projectID: string, name: string) => Promise<WorkbenchState>;
  ArchiveProjectTasks?: (projectID: string) => Promise<WorkbenchState>;
  RemoveProject?: (projectID: string) => Promise<WorkbenchState>;
  OpenProjectInFileManager?: (projectID: string) => Promise<void>;
  CreatePermanentWorktree?: (projectID: string, branchName: string, destination: string) => Promise<WorkbenchState>;
  NewSession?: () => Promise<WorkbenchState>;
  SwitchSession?: (sessionID: string) => Promise<WorkbenchState>;
  SwitchProjectSession?: (projectID: string, sessionID: string) => Promise<WorkbenchState>;
  RenameSession?: (sessionID: string, title: string) => Promise<WorkbenchState>;
  RenameProjectSession?: (projectID: string, sessionID: string, title: string) => Promise<WorkbenchState>;
  ArchiveSession?: (sessionID: string, archived: boolean) => Promise<WorkbenchState>;
  ArchiveProjectSession?: (projectID: string, sessionID: string, archived: boolean) => Promise<WorkbenchState>;
  DeleteSession?: (sessionID: string) => Promise<WorkbenchState>;
  DeleteProjectSession?: (projectID: string, sessionID: string) => Promise<WorkbenchState>;
  SelectDirectory?: () => Promise<string>;
  SelectWorktreeParentDirectory?: () => Promise<string>;
  OpenWorkspaceFile?: (path: string) => Promise<void>;
  ReadWorkspaceFile?: (path: string) => Promise<WorkspaceFilePreview>;
  ReadSkillDetail?: (name: string) => Promise<SkillDetail>;
  ImportSkillMarkdown?: (fileName: string, content: string) => Promise<SkillImportResult>;
  OpenSkillFile?: (name: string) => Promise<void>;
  RevealSkillFile?: (name: string) => Promise<void>;
  ListWorkspaceDirectory?: (path: string) => Promise<WorkspaceDirectoryListing>;
  PreviewWorkspaceFile?: (path: string) => Promise<BrowserPreview>;
  RevealWorkspaceFile?: (path: string) => Promise<void>;
  GetBrowserState?: () => Promise<BrowserState>;
  OpenBrowserURL?: (url: string) => Promise<BrowserState>;
  BrowserActivateTab?: (tabID: string) => Promise<BrowserState>;
  BrowserCloseTab?: (tabID: string) => Promise<BrowserState>;
  BrowserDismissError?: (tabID: string) => Promise<BrowserState>;
  BrowserNavigate?: (tabID: string, url: string) => Promise<void>;
  BrowserBack?: (tabID: string) => Promise<void>;
  BrowserForward?: (tabID: string) => Promise<void>;
  BrowserReload?: (tabID: string) => Promise<void>;
  BrowserResize?: (tabID: string, width: number, height: number) => Promise<void>;
  BrowserShowNativeSurface?: (tabID: string, x: number, y: number, width: number, height: number, viewportWidth: number, viewportHeight: number) => Promise<boolean>;
  BrowserHideNativeSurface?: () => Promise<void>;
  GetBrowserFrame?: (tabID: string, includeAnnotations: boolean) => Promise<BrowserFrame>;
  GetBrowserFrameDelta?: (tabID: string, includeAnnotations: boolean, capturedAt: string) => Promise<BrowserFrame>;
  BrowserClick?: (tabID: string, x: number, y: number, clickCount: number) => Promise<void>;
  BrowserScroll?: (tabID: string, deltaX: number, deltaY: number) => Promise<void>;
  BrowserType?: (tabID: string, text: string) => Promise<void>;
  BrowserKey?: (tabID: string, key: string, ctrl: boolean, alt: boolean, shift: boolean, meta: boolean) => Promise<void>;
  BrowserHandleDialog?: (tabID: string, accept: boolean, promptText: string) => Promise<void>;
  GetBrowserInspector?: (tabID: string) => Promise<BrowserInspector>;
  BrowserSaveScreenshot?: (tabID: string) => Promise<string>;
  BrowserEvaluate?: (tabID: string, expression: string) => Promise<string>;
  BrowserAutofill?: (tabID: string) => Promise<number>;
  SaveBrowserCredential?: (credentialID: string, origin: string, username: string, password: string) => Promise<WorkbenchState>;
  DeleteBrowserCredential?: (credentialID: string) => Promise<WorkbenchState>;
  BrowserFillCredential?: (tabID: string, credentialID: string) => Promise<number>;
  BrowserOpenDownload?: (downloadID: string) => Promise<void>;
  BrowserRevealDownload?: (downloadID: string) => Promise<void>;
  BrowserClearData?: () => Promise<BrowserState>;
  OpenURLInSystemBrowser?: (url: string) => Promise<void>;
  GetGitStatus?: () => Promise<GitStatus>;
  GetGitDiff?: (path: string, staged: boolean) => Promise<GitDiff>;
  GetGitReviewDiff?: (path: string, staged: boolean, ignoreWhitespace: boolean) => Promise<GitDiff>;
  StageGitPaths?: (paths: string[]) => Promise<GitStatus>;
  UnstageGitPaths?: (paths: string[]) => Promise<GitStatus>;
  CommitGitChanges?: (message: string) => Promise<GitStatus>;
  CreateGitBranch?: (name: string) => Promise<GitStatus>;
  SwitchGitBranch?: (name: string) => Promise<GitStatus>;
  StartTerminalSession?: () => Promise<TerminalSessionState>;
  GetTerminalSession?: (sessionID: string) => Promise<TerminalSessionState>;
  SendTerminalCommand?: (sessionID: string, command: string) => Promise<void>;
  StopTerminalSession?: (sessionID: string) => Promise<void>;
};

type WailsWindow = Window & {
  go?: {
    main?: {
      App?: WailsAppBinding;
    };
  };
  runtime?: {
    EventsOn?: (event: string, callback: (data: unknown) => void) => () => void;
  };
};

const fallbackSkillsIndex: SkillIndexEntry[] = [
  {
    name: "echocode-agent-core",
    version: 1,
    trigger: "echocode agent | echocode 工具注册 | echocode 上下文组装",
    triggerMode: "explicit",
    summary: "修改 EchoCode 自身 Agent 内核时使用的架构与验证约束",
    sha256: "sha256:local-preview",
    description: "仅在修改 EchoCode 自身 Agent 内核时使用。",
    source: "preview",
    path: "skills/echocode-agent-core/SKILL.md",
  },
  {
    name: "echocode-office-artifacts",
    version: 1,
    trigger: ".docx | .xlsx | .pptx | excel 工作簿 | 考勤表",
    triggerMode: "explicit",
    summary: "创建和修改 DOCX、XLSX、PPTX 办公产物时使用的质量约束",
    sha256: "sha256:local-preview-office",
    description: "创建或修改办公产物时使用。",
    source: "preview",
    path: "skills/echocode-office-artifacts/SKILL.md",
  },
];

const fallbackSnapshots: ServerSnapshot[] = [
  {
    server: "builtin",
    toolsHash: "sha256:local-preview",
    tools: [
      {
        name: "read_file",
        inputSchemaHash: "sha256:path-schema",
        outputPolicy: "summary-first",
      },
      {
        name: "file_info",
        inputSchemaHash: "sha256:path-schema",
        outputPolicy: "summary-first",
      },
      {
        name: "list_dir",
        inputSchemaHash: "sha256:path-schema",
        outputPolicy: "summary-first",
      },
      { name: "search", inputSchemaHash: "sha256:search-schema", outputPolicy: "summary-first" },
      { name: "write_file", inputSchemaHash: "sha256:write-schema", outputPolicy: "summary-first" },
      { name: "apply_patch", inputSchemaHash: "sha256:patch-schema", outputPolicy: "summary-first" },
      { name: "copy_file", inputSchemaHash: "sha256:copy-schema", outputPolicy: "summary-first" },
      { name: "delete_file", inputSchemaHash: "sha256:delete-schema", outputPolicy: "summary-first" },
    ],
  },
];

let fallbackState = createFallbackState(defaultReasoningLevel);
const fallbackChatHandlers = new Set<(event: ChatTaskEvent) => void>();
let fallbackActiveChatTaskID = "";
let fallbackTerminal: TerminalSessionState | undefined;

function emitFallbackChatTask(event: ChatTaskEvent) {
  for (const handler of fallbackChatHandlers) {
    handler(event);
  }
}

export async function getWorkbenchState(): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding) {
    return binding.GetWorkbenchState();
  }
  return cloneState(fallbackState);
}

export async function setReasoningLevel(level: ReasoningLevel): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding) {
    return binding.SetReasoningLevel(level);
  }
  fallbackState = {
    ...createFallbackState(level),
    deepSeek: fallbackState.deepSeek,
  };
  return cloneState(fallbackState);
}

export async function saveDeepSeekAPIKey(apiKey: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding) {
    return binding.SaveDeepSeekAPIKey(apiKey);
  }
  fallbackState = {
    ...fallbackState,
    deepSeek: {
      ...fallbackState.deepSeek,
      configured: true,
      lastCheckStatus: "idle",
      lastCheckMessage: "DeepSeek API Key 已保存，等待连接测试。",
      checkedAt: undefined,
      models: [],
    },
  };
  fallbackState = updateFallbackProvider("deepseek", (provider) => ({
    ...provider,
    apiKeyConfigured: Boolean(apiKey.trim()),
    lastSyncStatus: "idle",
    lastSyncMessage: "DeepSeek API Key 已保存，等待连接测试。",
    checkedAt: undefined,
  }));
  return cloneState(fallbackState);
}

export async function clearDeepSeekAPIKey(): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding) {
    return binding.ClearDeepSeekAPIKey();
  }
  fallbackState = {
    ...fallbackState,
    deepSeek: {
      configured: false,
      baseUrl: "https://api.deepseek.com",
      lastCheckStatus: "idle",
      lastCheckMessage: "DeepSeek API Key 已清除。",
      models: [],
    },
  };
  fallbackState = updateFallbackProvider("deepseek", (provider) => ({
    ...provider,
    apiKeyConfigured: false,
    models: [],
    lastSyncStatus: "idle",
    lastSyncMessage: "API Key 已清除。",
    checkedAt: undefined,
  }));
  return cloneState(fallbackState);
}

export async function testDeepSeekConnection(): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding) {
    return binding.TestDeepSeekConnection();
  }
  fallbackState = {
    ...fallbackState,
    deepSeek: {
      ...fallbackState.deepSeek,
      configured: true,
      lastCheckStatus: "ok",
      lastCheckMessage: "预览模式连接模拟成功，发现 2 个模型。",
      checkedAt: new Date().toISOString(),
      models: [
        { id: "deepseek-v4-flash", displayName: "DeepSeek V4 Flash", provider: "deepseek", contextWindowTokens: 128000, contextWindowSource: "catalog" },
        { id: "deepseek-v4-pro", displayName: "DeepSeek V4 Pro", provider: "deepseek", contextWindowTokens: 128000, contextWindowSource: "catalog" },
      ],
    },
  };
  return cloneState(fallbackState);
}

export async function sendDeepSeekMessage(prompt: string): Promise<ChatResult> {
  const binding = wailsBinding();
  if (binding) {
    return binding.SendChatMessage ? binding.SendChatMessage(prompt) : binding.SendDeepSeekMessage(prompt);
  }
  const usage = {
    promptCacheHitTokens: 96,
    promptCacheMissTokens: 4,
    inputTokens: 100,
    outputTokens: 12,
    effectiveCost: 0,
  };
  const routeProvider =
    fallbackState.runtimeSettings.model.providers.find(
      (provider) => provider.id === fallbackState.runtimeSettings.model.selectedProviderId,
    ) ?? fallbackState.runtimeSettings.model.providers[0];
  const routeModel =
    fallbackState.runtimeSettings.model.selectedModelId ||
    routeProvider?.defaultModelId ||
    routeProvider?.models[0]?.id ||
    (routeProvider?.id === "deepseek" ? "deepseek-v4-flash" : "preview-model");
  fallbackState = {
    ...fallbackState,
    deepSeek: {
      ...fallbackState.deepSeek,
      configured: true,
      lastCheckStatus: "ok",
      lastCheckMessage: `预览模式试聊成功，${routeProvider?.name ?? "模型服务"} / ${routeModel} 流式通道正常。`,
      checkedAt: new Date().toISOString(),
    },
    deepSeekSession: {
      active: true,
      providerId: routeProvider?.id ?? "deepseek",
      providerName: routeProvider?.name ?? "DeepSeek 官方",
      protocol: routeProvider?.protocol ?? "deepseek-official",
      model: routeModel,
      reasoning: fallbackState.reasoning.id,
      thinkingMode: thinkingModeForReasoning(fallbackState.reasoning.id),
      reasoningEffort: reasoningEffortForReasoning(fallbackState.reasoning.id),
      prefixHash: fallbackState.contextPreview.prefixHash,
      systemPromptHash: "sha256:local-preview-system",
      stablePromptTokens: 900,
      messageCount: (fallbackState.deepSeekSession.messageCount || 1) + 2,
      turnCount: fallbackState.deepSeekSession.turnCount + 1,
      sessionCacheHitTokens: fallbackState.deepSeekSession.sessionCacheHitTokens + usage.promptCacheHitTokens,
      sessionCacheMissTokens: fallbackState.deepSeekSession.sessionCacheMissTokens + usage.promptCacheMissTokens,
      sessionCacheHitRate: 0.96,
      appendOnlyPrefixStable: true,
      previousRequestMessageCount: fallbackState.deepSeekSession.messageCount,
      commonPrefixMessageCount: fallbackState.deepSeekSession.messageCount,
      startedAt: fallbackState.deepSeekSession.startedAt ?? new Date().toISOString(),
      resetReason: "预览模式会话已初始化。",
    },
    usageMetrics: usage,
    cacheHitRate: 0.96,
    cacheHealth: {
      status: "ok",
      message: "缓存命中率达到 96% 目标。",
      hitRate: 0.96,
      targetHitRate: 0.96,
      hitTokens: usage.promptCacheHitTokens,
      missTokens: usage.promptCacheMissTokens,
      totalCacheTokens: usage.promptCacheHitTokens + usage.promptCacheMissTokens,
      missTokenBudget: 4,
      requiredHitTokens: 96,
      additionalHitTokensNeeded: 0,
      shortPrompt: true,
      sampleCount: 1,
      consecutiveBelowTarget: 0,
      hitTokensIncreasing: false,
      missTokensStable: false,
      missTokensImproving: false,
    },
    cacheDiagnostics: ["缓存命中率达到 96% 目标。"],
  };
  const content = "预览模式返回：当前模型路由已接入，桌面环境会调用真实 Agent 接口。";
  const parts = previewFileParts(prompt, content);
  return cloneChatResult({
    content,
    model: routeModel,
    usage,
    state: fallbackState,
    parts,
  });
}

export async function startChatMessage(prompt: string, attachments: ChatAttachment[] = []): Promise<string> {
	return startChatMessageForSession("", "", prompt, attachments);
}

export async function startChatMessageForSession(projectID: string, sessionID: string, prompt: string, attachments: ChatAttachment[] = []): Promise<string> {
  const binding = wailsBinding();
  if (binding?.StartChatMessageForProjectSessionWithAttachments) {
    return binding.StartChatMessageForProjectSessionWithAttachments(projectID, sessionID, prompt, attachments);
  }
  if (binding?.StartChatMessageForProjectSession && attachments.length === 0) {
    return binding.StartChatMessageForProjectSession(projectID, sessionID, prompt);
  }
  if (binding?.StartChatMessageForSessionWithAttachments) {
    return binding.StartChatMessageForSessionWithAttachments(sessionID, prompt, attachments);
  }
  if (binding?.StartChatMessageForSession && attachments.length === 0) {
    return binding.StartChatMessageForSession(sessionID, prompt);
  }
  if (binding?.StartChatMessageWithAttachments) {
    return binding.StartChatMessageWithAttachments(prompt, attachments);
  }
  if (binding?.StartChatMessage) {
    if (attachments.length > 0) {
      throw new Error("当前桌面后端版本不支持图片附件，请重启 EchoCode。");
    }
    return binding.StartChatMessage(prompt);
  }
  if (fallbackActiveChatTaskID) {
    throw new Error("已有对话任务正在运行，请先停止当前任务");
  }
  const taskID = `preview-chat-${Date.now()}`;
  fallbackActiveChatTaskID = taskID;
  queueMicrotask(async () => {
    emitFallbackChatTask({ taskId: taskID, type: "started", message: "正在准备上下文" });
    try {
      const result = await sendDeepSeekMessage(prompt);
      if (fallbackActiveChatTaskID !== taskID) {
        return;
      }
      emitFallbackChatTask({ taskId: taskID, type: "delta", delta: result.content, model: result.model });
      emitFallbackChatTask({ taskId: taskID, type: "completed", model: result.model, result });
    } catch (err) {
      if (fallbackActiveChatTaskID === taskID) {
        emitFallbackChatTask({ taskId: taskID, type: "failed", message: String(err) });
      }
    } finally {
      if (fallbackActiveChatTaskID === taskID) {
        fallbackActiveChatTaskID = "";
      }
    }
  });
  return taskID;
}

export async function resumePausedTeamTask(projectID: string, sessionID: string): Promise<string> {
  const binding = wailsBinding();
  if (!binding?.ResumePausedTeamTask) {
    throw new Error("当前桌面后端版本不支持继续 AI 团队任务，请重启 EchoCode。");
  }
  return binding.ResumePausedTeamTask(projectID, sessionID);
}

export async function abandonPausedTeamTask(projectID: string, sessionID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (!binding?.AbandonPausedTeamTask) {
    throw new Error("当前桌面后端版本不支持结束 AI 团队任务，请重启 EchoCode。");
  }
  return binding.AbandonPausedTeamTask(projectID, sessionID);
}

export async function stopChatMessage(taskID: string): Promise<boolean> {
  const binding = wailsBinding();
  if (binding?.StopChatMessage) {
    return binding.StopChatMessage(taskID);
  }
  if (!fallbackActiveChatTaskID || (taskID && fallbackActiveChatTaskID !== taskID)) {
    return false;
  }
  const cancelledID = fallbackActiveChatTaskID;
  fallbackActiveChatTaskID = "";
  emitFallbackChatTask({ taskId: cancelledID, type: "cancelled", message: "已停止生成" });
  return true;
}

export async function stopSubagent(parentTaskID: string, subagentTaskID: string): Promise<boolean> {
  const binding = wailsBinding();
  if (binding?.StopSubagent) {
    return binding.StopSubagent(parentTaskID, subagentTaskID);
  }
  return false;
}

export async function guideChatMessage(
  taskID: string,
  guidanceID: string,
  prompt: string,
  attachments: ChatAttachment[] = [],
): Promise<boolean> {
  const binding = wailsBinding();
  if (binding?.GuideChatMessageWithAttachments) {
    return binding.GuideChatMessageWithAttachments(taskID, guidanceID, prompt, attachments);
  }
  if (binding?.GuideChatMessage && attachments.length === 0) {
    return binding.GuideChatMessage(taskID, guidanceID, prompt);
  }
  // Browser preview finishes immediately and has no long-lived Agent turn to steer.
  return false;
}

export async function getActiveChatTask(): Promise<ChatTaskState | null> {
  const binding = wailsBinding();
  if (binding?.GetActiveChatTask) {
    return binding.GetActiveChatTask();
  }
  return fallbackActiveChatTaskID ? { taskId: fallbackActiveChatTaskID, startedAt: new Date().toISOString(), status: "running" } : null;
}

export async function getActiveChatTasks(): Promise<ChatTaskState[]> {
  const binding = wailsBinding();
  if (binding?.GetActiveChatTasks) {
    return binding.GetActiveChatTasks();
  }
  const task = await getActiveChatTask();
  return task ? [task] : [];
}

export async function revealSecretResult(projectID: string, sessionID: string, secretID: string): Promise<SecretResultReveal> {
  const binding = wailsBinding();
  if (!binding?.RevealSecretResult) {
    throw new Error("当前桌面后端版本不支持查看敏感结果，请重启 EchoCode。");
  }
  return binding.RevealSecretResult(projectID, sessionID, secretID);
}

export function onChatTaskEvent(handler: (event: ChatTaskEvent) => void): () => void {
  const runtime = (window as WailsWindow).runtime;
  if (runtime?.EventsOn) {
    return runtime.EventsOn("chat:task", (data) => handler(data as ChatTaskEvent));
  }
  fallbackChatHandlers.add(handler);
  return () => fallbackChatHandlers.delete(handler);
}

export function onMCPState(handler: (state: WorkbenchState) => void): () => void {
  const runtime = (window as WailsWindow).runtime;
  if (runtime?.EventsOn) {
    return runtime.EventsOn("mcp:state", (data) => handler(data as WorkbenchState));
  }
  return () => undefined;
}

const fallbackAppInfo: AppInfo = {
  name: "EchoCode",
  version: "0.3.17",
  goVersion: "浏览器预览",
  operatingSystem: "web",
  architecture: "preview",
  executablePath: "",
  configPath: "",
  repositoryUrl: "https://github.com/SliyRainbow/EchoCode",
};

const fallbackUpdateState: UpdateState = {
  currentVersion: fallbackAppInfo.version,
  updateAvailable: false,
  status: "idle",
  message: "请在 EchoCode 桌面应用中检查更新。",
  progress: 0,
  downloadedBytes: 0,
  totalBytes: 0,
  checksumVerified: false,
};

export async function getAppInfo(): Promise<AppInfo> {
  const binding = wailsBinding();
  return binding?.GetAppInfo ? binding.GetAppInfo() : { ...fallbackAppInfo };
}

const fallbackOpenSourceLicenses: OpenSourceLicense[] = [
  {
    name: "EchoCode",
    description: "EchoCode application source and binaries",
    license: "MIT",
    url: fallbackAppInfo.repositoryUrl,
    text: "The complete offline license catalog is available in the EchoCode desktop application.",
  },
];

export async function getOpenSourceLicenses(): Promise<OpenSourceLicense[]> {
  const binding = wailsBinding();
  return binding?.GetOpenSourceLicenses ? binding.GetOpenSourceLicenses() : fallbackOpenSourceLicenses.map((item) => ({ ...item }));
}

export async function getUpdateState(): Promise<UpdateState> {
  const binding = wailsBinding();
  return binding?.GetUpdateState ? binding.GetUpdateState() : { ...fallbackUpdateState };
}

export async function checkForUpdates(): Promise<UpdateState> {
  const binding = wailsBinding();
  return binding?.CheckForUpdates ? binding.CheckForUpdates() : { ...fallbackUpdateState };
}

export async function downloadUpdate(): Promise<UpdateState> {
  const binding = wailsBinding();
  if (!binding?.DownloadUpdate) throw new Error("更新下载仅在 EchoCode 桌面应用中可用。");
  return binding.DownloadUpdate();
}

export async function installUpdate(): Promise<UpdateState> {
  const binding = wailsBinding();
  if (!binding?.InstallUpdate) throw new Error("更新安装仅在 EchoCode 桌面应用中可用。");
  return binding.InstallUpdate();
}

export async function openUpdateReleasePage(): Promise<void> {
  const binding = wailsBinding();
  if (binding?.OpenUpdateReleasePage) {
    await binding.OpenUpdateReleasePage();
    return;
  }
  window.open(fallbackAppInfo.repositoryUrl + "/releases", "_blank", "noopener,noreferrer");
}

export async function openAppRepositoryPage(): Promise<void> {
  const binding = wailsBinding();
  if (binding?.OpenAppRepositoryPage) {
    await binding.OpenAppRepositoryPage();
    return;
  }
  window.open(fallbackAppInfo.repositoryUrl, "_blank", "noopener,noreferrer");
}

export async function revealAppExecutable(): Promise<void> {
  const binding = wailsBinding();
  if (!binding?.RevealAppExecutable) throw new Error("程序位置仅在 EchoCode 桌面应用中可用。");
  await binding.RevealAppExecutable();
}

export async function revealAppConfigFile(): Promise<void> {
  const binding = wailsBinding();
  if (!binding?.RevealAppConfigFile) throw new Error("配置位置仅在 EchoCode 桌面应用中可用。");
  await binding.RevealAppConfigFile();
}

export function onUpdateState(handler: (state: UpdateState) => void): () => void {
  const runtime = (window as WailsWindow).runtime;
  if (runtime?.EventsOn) {
    return runtime.EventsOn("update:state", (data) => handler(data as UpdateState));
  }
  return () => undefined;
}

const fallbackAutomationState: AutomationState = { tasks: [], running: false };

export async function getAutomationState(): Promise<AutomationState> {
  const binding = wailsBinding();
  return binding?.GetAutomationState ? binding.GetAutomationState() : { ...fallbackAutomationState, tasks: [] };
}

export async function saveAutomationTask(task: AutomationTask): Promise<AutomationState> {
  const binding = wailsBinding();
  if (!binding?.SaveAutomationTask) throw new Error("自动化任务仅在 EchoCode 桌面应用中可用。");
  return binding.SaveAutomationTask(task);
}

export async function deleteAutomationTask(taskID: string): Promise<AutomationState> {
  const binding = wailsBinding();
  if (!binding?.DeleteAutomationTask) throw new Error("自动化任务仅在 EchoCode 桌面应用中可用。");
  return binding.DeleteAutomationTask(taskID);
}

export async function setAutomationTaskEnabled(taskID: string, enabled: boolean): Promise<AutomationState> {
  const binding = wailsBinding();
  if (!binding?.SetAutomationTaskEnabled) throw new Error("自动化任务仅在 EchoCode 桌面应用中可用。");
  return binding.SetAutomationTaskEnabled(taskID, enabled);
}

export async function runAutomationTaskNow(taskID: string): Promise<AutomationState> {
  const binding = wailsBinding();
  if (!binding?.RunAutomationTaskNow) throw new Error("自动化任务仅在 EchoCode 桌面应用中可用。");
  return binding.RunAutomationTaskNow(taskID);
}

export async function stopAutomationTask(taskID: string): Promise<AutomationState> {
  const binding = wailsBinding();
  if (!binding?.StopAutomationTask) throw new Error("自动化任务仅在 EchoCode 桌面应用中可用。");
  return binding.StopAutomationTask(taskID);
}

export function onAutomationState(handler: (state: AutomationState) => void): () => void {
  const runtime = (window as WailsWindow).runtime;
  if (runtime?.EventsOn) {
    return runtime.EventsOn("automation:state", (data) => handler(data as AutomationState));
  }
  return () => undefined;
}

function previewFileParts(prompt: string, content: string): MessagePart[] | undefined {
  if (!/(创建|生成|create|generate)/i.test(prompt) || !/(html|文件|file)/i.test(prompt)) {
    return undefined;
  }
  return [
    { kind: "tool_call", name: "list_dir", status: "ok", input: ".", output: "frontend/\ninternal/\nREADME.md" },
    {
      kind: "diff",
      path: "preview.html",
      additions: 18,
      deletions: 0,
      patch: "diff --git a/preview.html b/preview.html\n--- a/preview.html\n+++ b/preview.html\n+<!doctype html>\n+<html lang=\"zh-CN\">\n+  <head><title>EchoCode Preview</title></head>\n+  <body><main>Preview</main></body>\n+</html>",
    },
    { kind: "file", path: "preview.html", lineCount: 18, created: true },
    { kind: "text", text: content },
  ];
}

export async function resetDeepSeekSession(): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding) {
    return binding.ResetDeepSeekSession();
  }
  fallbackState = {
    ...fallbackState,
    usageMetrics: {
      promptCacheHitTokens: 0,
      promptCacheMissTokens: 0,
      inputTokens: 0,
      outputTokens: 0,
      effectiveCost: 0,
    },
    cacheHitRate: 0,
    cacheHealth: pendingCacheHealth(),
    deepSeekSession: {
      active: false,
      providerId: "",
      providerName: "",
      protocol: "",
      model: "",
      reasoning: fallbackState.reasoning.id,
      thinkingMode: thinkingModeForReasoning(fallbackState.reasoning.id),
      reasoningEffort: reasoningEffortForReasoning(fallbackState.reasoning.id),
      prefixHash: "",
      systemPromptHash: "",
      stablePromptTokens: 0,
      messageCount: 0,
      turnCount: 0,
      sessionCacheHitTokens: 0,
      sessionCacheMissTokens: 0,
      sessionCacheHitRate: 0,
      appendOnlyPrefixStable: true,
      previousRequestMessageCount: 0,
      commonPrefixMessageCount: 0,
      resetReason: "用户手动开启新会话。",
    },
    cacheDiagnostics: ["等待首轮模型请求记录缓存命中数据。"],
  };
  return cloneState(fallbackState);
}

export async function saveRuntimeSettings(settings: RuntimeSettings): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding) {
    return binding.SaveRuntimeSettings(settings);
  }
  fallbackState = {
    ...fallbackState,
    runtimeSettings: normalizeRuntimeSettings(settings),
    cacheTarget: normalizeRuntimeSettings(settings).cacheTargetPercent / 100,
  };
  return cloneState(fallbackState);
}

export async function saveModelProviderAPIKey(providerID: string, apiKey: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding) {
    return binding.SaveModelProviderAPIKey(providerID, apiKey);
  }
  fallbackState = updateFallbackProvider(providerID, (provider) => ({
    ...provider,
    apiKeyConfigured: Boolean(apiKey.trim()),
    lastSyncStatus: "idle",
    lastSyncMessage: apiKey.trim() ? "API Key 已保存，等待刷新模型。" : "API Key 不能为空。",
    checkedAt: undefined,
  }));
  return cloneState(fallbackState);
}

export async function clearModelProviderAPIKey(providerID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding) {
    return binding.ClearModelProviderAPIKey(providerID);
  }
  fallbackState = updateFallbackProvider(providerID, (provider) => ({
    ...provider,
    apiKeyConfigured: false,
    models: [],
    defaultModelId: "",
    lastSyncStatus: "idle",
    lastSyncMessage: "API Key 已清除。",
    checkedAt: undefined,
  }));
  return cloneState(fallbackState);
}

export async function saveModelProviderBillingAPIKey(providerID: string, apiKey: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.SaveModelProviderBillingAPIKey) {
    return binding.SaveModelProviderBillingAPIKey(providerID, apiKey);
  }
  fallbackState = updateFallbackProvider(providerID, (provider) => ({
    ...provider,
    billingKeyConfigured: Boolean(apiKey.trim()),
  }));
  return cloneState(fallbackState);
}

export async function clearModelProviderBillingAPIKey(providerID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.ClearModelProviderBillingAPIKey) {
    return binding.ClearModelProviderBillingAPIKey(providerID);
  }
  fallbackState = updateFallbackProvider(providerID, (provider) => ({
    ...provider,
    billingKeyConfigured: false,
  }));
  return cloneState(fallbackState);
}

export async function getUsageBillingReport(providerID: string): Promise<UsageBillingReport> {
  const binding = wailsBinding();
  if (binding?.GetUsageBillingReport) {
    return binding.GetUsageBillingReport(providerID);
  }
  return fallbackUsageBillingReport(providerID);
}

export async function syncUsageBilling(input: UsageBillingSyncInput): Promise<UsageBillingReport> {
  const binding = wailsBinding();
  if (binding?.SyncUsageBilling) {
    return binding.SyncUsageBilling(input);
  }
  return {
    ...fallbackUsageBillingReport(input.providerId),
    status: "preview",
    message: "预览模式不连接官方账单接口。请在桌面应用中同步。",
    periodStart: input.periodStart,
    periodEnd: input.periodEnd,
  };
}

export async function reconcileUsageBilling(input: UsageBillingReconciliationInput): Promise<UsageBillingReport> {
  const binding = wailsBinding();
  if (binding?.ReconcileUsageBilling) {
    return binding.ReconcileUsageBilling(input);
  }
  const report = fallbackUsageBillingReport(input.providerId);
  return {
    ...report,
    status: report.officialProvider ? "preview" : "custom_reconciled",
    message: "预览模式已模拟保存账单对账；桌面应用会写入本地用量账本。",
    periodStart: input.periodStart,
    periodEnd: input.periodEnd,
    officialCost: input.officialCost,
    estimatedCost: 0,
    difference: input.officialCost,
    absoluteDifference: Math.abs(input.officialCost),
    reconciliationSource: input.source,
  };
}

export async function deleteModelProvider(providerID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.DeleteModelProvider) {
    return binding.DeleteModelProvider(providerID);
  }
  const providers = fallbackState.runtimeSettings.model.providers.filter((provider) => provider.id !== providerID);
  const selectedProviderId = fallbackState.runtimeSettings.model.selectedProviderId === providerID
    ? providers[0]?.id || ""
    : fallbackState.runtimeSettings.model.selectedProviderId;
  const selectedProvider = providers.find((provider) => provider.id === selectedProviderId) ?? providers[0];
  fallbackState = {
    ...fallbackState,
    runtimeSettings: {
      ...fallbackState.runtimeSettings,
      model: {
        ...fallbackState.runtimeSettings.model,
        providers,
        selectedProviderId,
        selectedModelId: selectedProvider?.defaultModelId || selectedProvider?.models[0]?.id || "",
      },
    },
  };
  return cloneState(fallbackState);
}

export async function refreshModelProviderModels(providerID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding) {
    return binding.RefreshModelProviderModels(providerID);
  }
  const now = new Date().toISOString();
  const fallbackModels = providerID === "deepseek"
    ? [
        { id: "deepseek-v4-flash", displayName: "DeepSeek V4 Flash", provider: "deepseek", contextWindowTokens: 128000, contextWindowSource: "catalog" },
        { id: "deepseek-v4-pro", displayName: "DeepSeek V4 Pro", provider: "deepseek", contextWindowTokens: 128000, contextWindowSource: "catalog" },
      ]
    : [
        { id: "upstream-chat", displayName: "upstream-chat", provider: providerID, contextWindowTokens: 128000, contextWindowSource: "upstream" },
        { id: "upstream-reasoner", displayName: "upstream-reasoner", provider: providerID, contextWindowTokens: 128000, contextWindowSource: "upstream" },
      ];
  fallbackState = updateFallbackProvider(providerID, (provider) => ({
    ...provider,
    models: fallbackModels,
    defaultModelId: provider.defaultModelId || fallbackModels[0]?.id || "",
    lastSyncStatus: "ok",
    lastSyncMessage: `预览模式连接成功，发现 ${fallbackModels.length} 个模型。`,
    checkedAt: now,
  }));
  const runtimeSettings = fallbackState.runtimeSettings;
  if (providerID === "deepseek") {
    fallbackState = {
      ...fallbackState,
      deepSeek: {
        ...fallbackState.deepSeek,
        configured: true,
        models: fallbackModels,
        lastCheckStatus: "ok",
        lastCheckMessage: `预览模式连接成功，发现 ${fallbackModels.length} 个模型。`,
        checkedAt: now,
      },
      runtimeSettings,
    };
  }
  return cloneState(fallbackState);
}

export async function refreshMCPServer(serverID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.RefreshMCPServer) {
    return binding.RefreshMCPServer(serverID);
  }
  const now = new Date().toISOString();
  fallbackState = {
    ...fallbackState,
    mcpServers: fallbackState.runtimeSettings.mcp.servers.map((server) => ({
      id: server.id,
      name: server.name,
      transport: server.transport,
      state: !server.enabled ? "disabled" : server.transport === "builtin" ? "ready" : "error",
      message: !server.enabled
        ? "服务器已停用"
        : server.transport === "builtin"
          ? "内置工具由 EchoCode 运行时提供"
          : "浏览器预览模式不会启动外部 MCP 服务器",
      toolCount: server.transport === "builtin" ? 4 : 0,
      checkedAt: now,
    })),
  };
  return cloneState(fallbackState);
}

export async function refreshPlugins(): Promise<WorkbenchState> {
	const binding = wailsBinding();
	if (binding?.RefreshPlugins) {
		return binding.RefreshPlugins();
	}
	return cloneState(fallbackState);
}

export async function selectPluginDirectory(): Promise<string> {
	const binding = wailsBinding();
	if (binding?.SelectPluginDirectory) {
		return binding.SelectPluginDirectory();
	}
	throw new Error("插件安装仅在 EchoCode 桌面应用中可用。");
}

export async function installPlugin(source: string): Promise<WorkbenchState> {
	const binding = wailsBinding();
	if (binding?.InstallPlugin) {
		return binding.InstallPlugin(source);
	}
	throw new Error("插件安装仅在 EchoCode 桌面应用中可用。");
}

export async function uninstallPlugin(id: string): Promise<WorkbenchState> {
	const binding = wailsBinding();
	if (binding?.UninstallPlugin) {
		return binding.UninstallPlugin(id);
	}
	throw new Error("插件卸载仅在 EchoCode 桌面应用中可用。");
}

export async function revealPlugin(id: string): Promise<void> {
	const binding = wailsBinding();
	if (binding?.RevealPlugin) {
		return binding.RevealPlugin(id);
	}
	throw new Error("插件目录仅在 EchoCode 桌面应用中可用。");
}

function wailsBinding(): WailsAppBinding | undefined {
  return (window as WailsWindow).go?.main?.App;
}

// 列出当前会话的可回退检查点。浏览器预览模式返回空列表。
export async function listCheckpoints(): Promise<CheckpointInfo[]> {
  const binding = wailsBinding();
  if (binding?.ListCheckpoints) {
    return binding.ListCheckpoints();
  }
  return [];
}

// 回退到指定检查点：对话与文件一起回退。预览模式下无操作，返回当前状态。
export async function rewindToCheckpoint(checkpointID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.RewindToCheckpoint) {
    return binding.RewindToCheckpoint(checkpointID);
  }
  return cloneState(fallbackState);
}

// 列出所有对话线（分支）。预览模式返回空列表。
export async function listBranches(): Promise<BranchInfo[]> {
  const binding = wailsBinding();
  if (binding?.ListBranches) {
    return binding.ListBranches();
  }
  return [];
}

// 切换到另一条对话线：文件与对话一起切换。预览模式无操作。
export async function switchBranch(leafID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.SwitchBranch) {
    return binding.SwitchBranch(leafID);
  }
  return cloneState(fallbackState);
}

// 从历史消息创建新分支，旧分支仍保留在事件树中。
export async function forkFromMessage(messageEventID: string, projectID = "", sessionID = ""): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.ForkFromMessageForProjectSession && projectID && sessionID) {
    return binding.ForkFromMessageForProjectSession(projectID, sessionID, messageEventID);
  }
  if (binding?.ForkFromMessage) {
    return binding.ForkFromMessage(messageEventID);
  }
  return cloneState(fallbackState);
}

// 监听后端审批请求事件。返回取消订阅函数。浏览器预览模式无事件。
export function onApprovalRequest(handler: (req: ApprovalRequest) => void): () => void {
  const runtime = (window as WailsWindow).runtime;
  if (runtime?.EventsOn) {
    return runtime.EventsOn("approval:request", (data) => handler(data as ApprovalRequest));
  }
  return () => undefined;
}

export function onBrowserPreviewOpen(handler: (preview: BrowserPreview) => void): () => void {
  const runtime = (window as WailsWindow).runtime;
  if (runtime?.EventsOn) {
    return runtime.EventsOn("browser:open", (data) => handler(data as BrowserPreview));
  }
  return () => undefined;
}

export function onBrowserPreviewClose(handler: () => void): () => void {
  const runtime = (window as WailsWindow).runtime;
  if (runtime?.EventsOn) {
    return runtime.EventsOn("browser:close", handler);
  }
  return () => undefined;
}

export function onTerminalSessionUpdate(handler: (state: TerminalSessionState) => void): () => void {
  const runtime = (window as WailsWindow).runtime;
  if (runtime?.EventsOn) {
    return runtime.EventsOn("terminal:update", (data) => handler(data as TerminalSessionState));
  }
  return () => undefined;
}

// 答复审批：approved=是否批准，scope="once"|"session"。
export async function respondApproval(id: string, tool: string, approved: boolean, scope: "once" | "session"): Promise<void> {
  const binding = wailsBinding();
  if (binding?.RespondApproval) {
    await binding.RespondApproval(id, tool, approved, scope);
  }
}

// 开关 Plan 两段式（先规划后执行）。
export async function setPlanMode(enabled: boolean): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.SetPlanMode) {
    return binding.SetPlanMode(enabled);
  }
  fallbackState = { ...fallbackState, planMode: enabled };
  return cloneState(fallbackState);
}

// --- 多项目 / 多会话 ---

export async function listProjects(): Promise<ProjectInfo[]> {
  const binding = wailsBinding();
  if (binding?.ListProjects) {
    return binding.ListProjects();
  }
  return [];
}

export async function listSessions(): Promise<SessionInfo[]> {
  const binding = wailsBinding();
  if (binding?.ListSessions) {
    return binding.ListSessions();
  }
  return [];
}

// 项目树（所有项目 + 各自会话），Codex 式侧边栏数据源。
export async function getProjectTree(): Promise<ProjectNode[]> {
  const binding = wailsBinding();
  if (binding?.GetProjectTree) {
    return binding.GetProjectTree();
  }
  return [];
}

// 读取当前活动会话的历史消息（启动/切换会话时恢复对话）。预览模式返回空。
export async function getSessionMessages(): Promise<SessionMessage[]> {
  const binding = wailsBinding();
  if (binding?.GetSessionMessages) {
    return binding.GetSessionMessages();
  }
  return [];
}

export async function getSessionMessagesForSession(projectID: string, sessionID: string): Promise<SessionMessage[]> {
  const binding = wailsBinding();
  if (binding?.GetSessionMessagesForProjectSession) {
    return binding.GetSessionMessagesForProjectSession(projectID, sessionID);
  }
  if (binding?.GetSessionMessagesForSession) {
    return binding.GetSessionMessagesForSession(sessionID);
  }
  return getSessionMessages();
}

export async function createProject(name: string, workspaceRoot: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.CreateProject) {
    return binding.CreateProject(name, workspaceRoot);
  }
  return cloneState(fallbackState);
}

export async function switchProject(projectID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.SwitchProject) {
    return binding.SwitchProject(projectID);
  }
  return cloneState(fallbackState);
}

export async function setProjectPinned(projectID: string, pinned: boolean): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.SetProjectPinned) {
    return binding.SetProjectPinned(projectID, pinned);
  }
  return cloneState(fallbackState);
}

export async function renameProject(projectID: string, name: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.RenameProject) {
    return binding.RenameProject(projectID, name);
  }
  return cloneState(fallbackState);
}

export async function archiveProjectTasks(projectID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.ArchiveProjectTasks) {
    return binding.ArchiveProjectTasks(projectID);
  }
  return cloneState(fallbackState);
}

export async function removeProject(projectID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.RemoveProject) {
    return binding.RemoveProject(projectID);
  }
  return cloneState(fallbackState);
}

export async function openProjectInFileManager(projectID: string): Promise<void> {
  const binding = wailsBinding();
  if (binding?.OpenProjectInFileManager) {
    await binding.OpenProjectInFileManager(projectID);
    return;
  }
  throw new Error("项目目录打开功能仅在 EchoCode 桌面应用中可用。");
}

export async function createPermanentWorktree(
  projectID: string,
  branchName: string,
  destination: string,
): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.CreatePermanentWorktree) {
    return binding.CreatePermanentWorktree(projectID, branchName, destination);
  }
  return cloneState(fallbackState);
}

export async function newSession(): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.NewSession) {
    return binding.NewSession();
  }
  return resetDeepSeekSession();
}

export async function switchSession(projectID: string, sessionID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.SwitchProjectSession) {
    return binding.SwitchProjectSession(projectID, sessionID);
  }
  if (binding?.SwitchSession) {
    return binding.SwitchSession(sessionID);
  }
  return cloneState(fallbackState);
}

export async function renameSession(projectID: string, sessionID: string, title: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.RenameProjectSession) {
    return binding.RenameProjectSession(projectID, sessionID, title);
  }
  if (binding?.RenameSession) {
    return binding.RenameSession(sessionID, title);
  }
  return cloneState(fallbackState);
}

export async function archiveSession(projectID: string, sessionID: string, archived: boolean): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.ArchiveProjectSession) {
    return binding.ArchiveProjectSession(projectID, sessionID, archived);
  }
  if (binding?.ArchiveSession) {
    return binding.ArchiveSession(sessionID, archived);
  }
  return cloneState(fallbackState);
}

export async function deleteSession(projectID: string, sessionID: string): Promise<WorkbenchState> {
  const binding = wailsBinding();
  if (binding?.DeleteProjectSession) {
    return binding.DeleteProjectSession(projectID, sessionID);
  }
  if (binding?.DeleteSession) {
    return binding.DeleteSession(sessionID);
  }
  return cloneState(fallbackState);
}

// 弹系统目录选择框，返回所选路径（取消返回空串）。
export async function selectDirectory(): Promise<string> {
  const binding = wailsBinding();
  if (binding?.SelectDirectory) {
    return binding.SelectDirectory();
  }
  return "";
}

export async function selectWorktreeParentDirectory(): Promise<string> {
  const binding = wailsBinding();
  if (binding?.SelectWorktreeParentDirectory) {
    return binding.SelectWorktreeParentDirectory();
  }
  return "";
}

export async function openWorkspaceFile(path: string): Promise<void> {
  const binding = wailsBinding();
  if (binding?.OpenWorkspaceFile) {
    await binding.OpenWorkspaceFile(path);
    return;
  }
  throw new Error("文件打开功能仅在 EchoCode 桌面应用中可用。");
}

export async function readWorkspaceFile(path: string): Promise<WorkspaceFilePreview> {
  const binding = wailsBinding();
  if (binding?.ReadWorkspaceFile) {
    return binding.ReadWorkspaceFile(path);
  }
  throw new Error("文件预览仅在 EchoCode 桌面应用中可用。");
}

export async function readSkillDetail(name: string): Promise<SkillDetail> {
  const binding = wailsBinding();
  if (binding?.ReadSkillDetail) {
    return binding.ReadSkillDetail(name);
  }
  const skill = fallbackSkillsIndex.find((entry) => entry.name === name);
  if (!skill) throw new Error(`未找到 Skill：${name}`);
  return {
    ...skill,
    content: `# ${skill.name}\n\n${skill.description || skill.summary}\n`,
    canOpen: false,
  };
}

export async function importSkillMarkdown(fileName: string, content: string): Promise<SkillImportResult> {
  const binding = wailsBinding();
  if (binding?.ImportSkillMarkdown) {
    return binding.ImportSkillMarkdown(fileName, content);
  }
  throw new Error("Skill 导入功能仅在 EchoCode 桌面应用中可用。");
}

export async function openSkillFile(name: string): Promise<void> {
  const binding = wailsBinding();
  if (binding?.OpenSkillFile) {
    await binding.OpenSkillFile(name);
    return;
  }
  throw new Error("Skill 文件打开功能仅在 EchoCode 桌面应用中可用。");
}

export async function revealSkillFile(name: string): Promise<void> {
  const binding = wailsBinding();
  if (binding?.RevealSkillFile) {
    await binding.RevealSkillFile(name);
    return;
  }
  throw new Error("Skill 文件定位功能仅在 EchoCode 桌面应用中可用。");
}

export async function listWorkspaceDirectory(path = ""): Promise<WorkspaceDirectoryListing> {
  const binding = wailsBinding();
  if (binding?.ListWorkspaceDirectory) {
    return binding.ListWorkspaceDirectory(path);
  }
  return { path, entries: [], truncated: false };
}

export async function previewWorkspaceFile(path: string): Promise<BrowserPreview> {
  const binding = wailsBinding();
  if (binding?.PreviewWorkspaceFile) {
    return binding.PreviewWorkspaceFile(path);
  }
  throw new Error("内置浏览器仅在 EchoCode 桌面应用中可用。");
}

export async function revealWorkspaceFile(path: string): Promise<void> {
  const binding = wailsBinding();
  if (binding?.RevealWorkspaceFile) {
    await binding.RevealWorkspaceFile(path);
    return;
  }
  throw new Error("文件定位功能仅在 EchoCode 桌面应用中可用。");
}

export async function getGitStatus(): Promise<GitStatus> {
  const binding = wailsBinding();
  if (binding?.GetGitStatus) {
    return binding.GetGitStatus();
  }
  return emptyGitStatus();
}

export async function getGitDiff(path: string, staged: boolean): Promise<GitDiff> {
  const binding = wailsBinding();
  if (binding?.GetGitDiff) {
    return binding.GetGitDiff(path, staged);
  }
  return { path, staged, patch: "", truncated: false };
}

export async function getGitReviewDiff(path: string, staged: boolean, ignoreWhitespace: boolean): Promise<GitDiff> {
  const binding = wailsBinding();
  if (binding?.GetGitReviewDiff) {
    return binding.GetGitReviewDiff(path, staged, ignoreWhitespace);
  }
  return getGitDiff(path, staged);
}

export async function stageGitPaths(paths: string[]): Promise<GitStatus> {
  const binding = wailsBinding();
  if (binding?.StageGitPaths) {
    return binding.StageGitPaths(paths);
  }
  return emptyGitStatus();
}

export async function unstageGitPaths(paths: string[]): Promise<GitStatus> {
  const binding = wailsBinding();
  if (binding?.UnstageGitPaths) {
    return binding.UnstageGitPaths(paths);
  }
  return emptyGitStatus();
}

export async function commitGitChanges(message: string): Promise<GitStatus> {
  const binding = wailsBinding();
  if (binding?.CommitGitChanges) {
    return binding.CommitGitChanges(message);
  }
  return emptyGitStatus();
}

export async function createGitBranch(name: string): Promise<GitStatus> {
  const binding = wailsBinding();
  if (binding?.CreateGitBranch) {
    return binding.CreateGitBranch(name);
  }
  return { ...emptyGitStatus(), available: true, branch: name };
}

export async function switchGitBranch(name: string): Promise<GitStatus> {
  const binding = wailsBinding();
  if (binding?.SwitchGitBranch) {
    return binding.SwitchGitBranch(name);
  }
  return { ...emptyGitStatus(), available: true, branch: name };
}

export async function startTerminalSession(): Promise<TerminalSessionState> {
  const binding = wailsBinding();
  if (binding?.StartTerminalSession) {
    return binding.StartTerminalSession();
  }
  fallbackTerminal = {
    id: `preview-${Date.now()}`,
    shell: "Preview shell",
    workdir: fallbackState.runtimeSettings.workspaceRoot,
    running: true,
    startedAt: new Date().toISOString(),
    exitCode: -1,
    output: "",
    sandboxed: false,
    sandboxBackend: "preview-only",
    privilegeRestricted: false,
  };
  return { ...fallbackTerminal };
}

export async function getTerminalSession(sessionID: string): Promise<TerminalSessionState> {
  const binding = wailsBinding();
  if (binding?.GetTerminalSession) {
    return binding.GetTerminalSession(sessionID);
  }
  if (!fallbackTerminal || fallbackTerminal.id !== sessionID) {
    throw new Error("Terminal session was not found");
  }
  return { ...fallbackTerminal };
}

export async function sendTerminalCommand(sessionID: string, command: string): Promise<void> {
  const binding = wailsBinding();
  if (binding?.SendTerminalCommand) {
    await binding.SendTerminalCommand(sessionID, command);
    return;
  }
  if (!fallbackTerminal || fallbackTerminal.id !== sessionID || !fallbackTerminal.running) {
    throw new Error("Terminal session is not running");
  }
  fallbackTerminal = { ...fallbackTerminal, output: `${fallbackTerminal.output}> ${command}\n` };
}

export async function stopTerminalSession(sessionID: string): Promise<void> {
  const binding = wailsBinding();
  if (binding?.StopTerminalSession) {
    await binding.StopTerminalSession(sessionID);
    return;
  }
  if (fallbackTerminal?.id === sessionID) {
    fallbackTerminal = { ...fallbackTerminal, running: false, exitCode: 0 };
  }
}

export async function getBrowserState(): Promise<BrowserState> {
  const binding = wailsBinding();
  if (binding?.GetBrowserState) {
    return binding.GetBrowserState();
  }
  return emptyBrowserState("内置浏览器仅在 EchoCode 桌面应用中可用。");
}

export async function openBrowserURL(url: string): Promise<BrowserState> {
  const binding = requireBrowserBinding("OpenBrowserURL");
  return binding.OpenBrowserURL!(url);
}

export async function browserActivateTab(tabID: string): Promise<BrowserState> {
  return requireBrowserBinding("BrowserActivateTab").BrowserActivateTab!(tabID);
}

export async function browserCloseTab(tabID: string): Promise<BrowserState> {
  return requireBrowserBinding("BrowserCloseTab").BrowserCloseTab!(tabID);
}

export async function browserDismissError(tabID: string): Promise<BrowserState> {
  return requireBrowserBinding("BrowserDismissError").BrowserDismissError!(tabID);
}

export async function browserNavigate(tabID: string, url: string): Promise<void> {
  await requireBrowserBinding("BrowserNavigate").BrowserNavigate!(tabID, url);
}

export async function browserBack(tabID: string): Promise<void> {
  await requireBrowserBinding("BrowserBack").BrowserBack!(tabID);
}

export async function browserForward(tabID: string): Promise<void> {
  await requireBrowserBinding("BrowserForward").BrowserForward!(tabID);
}

export async function browserReload(tabID: string): Promise<void> {
  await requireBrowserBinding("BrowserReload").BrowserReload!(tabID);
}

export async function browserResize(tabID: string, width: number, height: number): Promise<void> {
  await requireBrowserBinding("BrowserResize").BrowserResize!(tabID, width, height);
}

export async function browserShowNativeSurface(
  tabID: string,
  x: number,
  y: number,
  width: number,
  height: number,
  viewportWidth: number,
  viewportHeight: number,
): Promise<boolean> {
  const binding = requireBrowserBinding("BrowserShowNativeSurface");
  return binding.BrowserShowNativeSurface!(tabID, x, y, width, height, viewportWidth, viewportHeight);
}

export async function browserHideNativeSurface(): Promise<void> {
  const binding = wailsBinding();
  if (binding?.BrowserHideNativeSurface) {
    await binding.BrowserHideNativeSurface();
  }
}

export async function getBrowserFrame(tabID: string, includeAnnotations: boolean, capturedAt = ""): Promise<BrowserFrame> {
	const binding = requireBrowserBinding("GetBrowserFrame");
	if (binding.GetBrowserFrameDelta) {
		return binding.GetBrowserFrameDelta(tabID, includeAnnotations, capturedAt);
	}
	return binding.GetBrowserFrame!(tabID, includeAnnotations);
}

export async function browserClick(tabID: string, x: number, y: number, clickCount = 1): Promise<void> {
  await requireBrowserBinding("BrowserClick").BrowserClick!(tabID, x, y, clickCount);
}

export async function browserScroll(tabID: string, deltaX: number, deltaY: number): Promise<void> {
  await requireBrowserBinding("BrowserScroll").BrowserScroll!(tabID, deltaX, deltaY);
}

export async function browserType(tabID: string, text: string): Promise<void> {
  await requireBrowserBinding("BrowserType").BrowserType!(tabID, text);
}

export async function browserKey(
  tabID: string,
  key: string,
  ctrl = false,
  alt = false,
  shift = false,
  meta = false,
): Promise<void> {
  await requireBrowserBinding("BrowserKey").BrowserKey!(tabID, key, ctrl, alt, shift, meta);
}

export async function browserHandleDialog(tabID: string, accept: boolean, promptText = ""): Promise<void> {
  await requireBrowserBinding("BrowserHandleDialog").BrowserHandleDialog!(tabID, accept, promptText);
}

export async function getBrowserInspector(tabID: string): Promise<BrowserInspector> {
  return requireBrowserBinding("GetBrowserInspector").GetBrowserInspector!(tabID);
}

export async function browserSaveScreenshot(tabID: string): Promise<string> {
  return requireBrowserBinding("BrowserSaveScreenshot").BrowserSaveScreenshot!(tabID);
}

export async function browserEvaluate(tabID: string, expression: string): Promise<string> {
  return requireBrowserBinding("BrowserEvaluate").BrowserEvaluate!(tabID, expression);
}

export async function browserAutofill(tabID: string): Promise<number> {
  return requireBrowserBinding("BrowserAutofill").BrowserAutofill!(tabID);
}

export async function saveBrowserCredential(credentialID: string, origin: string, username: string, password: string): Promise<WorkbenchState> {
  return requireBrowserBinding("SaveBrowserCredential").SaveBrowserCredential!(credentialID, origin, username, password);
}

export async function deleteBrowserCredential(credentialID: string): Promise<WorkbenchState> {
  return requireBrowserBinding("DeleteBrowserCredential").DeleteBrowserCredential!(credentialID);
}

export async function browserFillCredential(tabID: string, credentialID: string): Promise<number> {
  return requireBrowserBinding("BrowserFillCredential").BrowserFillCredential!(tabID, credentialID);
}

export async function browserOpenDownload(downloadID: string): Promise<void> {
  await requireBrowserBinding("BrowserOpenDownload").BrowserOpenDownload!(downloadID);
}

export async function browserRevealDownload(downloadID: string): Promise<void> {
  await requireBrowserBinding("BrowserRevealDownload").BrowserRevealDownload!(downloadID);
}

export async function browserClearData(): Promise<BrowserState> {
  return requireBrowserBinding("BrowserClearData").BrowserClearData!();
}

export async function openURLInSystemBrowser(url: string): Promise<void> {
  await requireBrowserBinding("OpenURLInSystemBrowser").OpenURLInSystemBrowser!(url);
}

function createFallbackState(level: ReasoningLevel): WorkbenchState {
  const reasoning = reasoningOptions.find((option) => option.id === level) ?? reasoningOptions[0];
  return {
    activeProjectId: "",
    activeSessionId: "",
    reasoning,
    reasoningOptions,
    cacheTarget: 0.96,
    usageMetrics: {
      promptCacheHitTokens: 0,
      promptCacheMissTokens: 0,
      inputTokens: 0,
      outputTokens: 0,
      effectiveCost: 0,
    },
    cacheHitRate: 0,
    cacheHealth: pendingCacheHealth(),
    deepSeek: {
      configured: false,
      baseUrl: "https://api.deepseek.com",
      lastCheckStatus: "idle",
      lastCheckMessage: "等待保存 DeepSeek API Key。",
      models: [],
    },
    deepSeekSession: {
      active: false,
      providerId: "",
      providerName: "",
      protocol: "",
      model: "",
      reasoning: reasoning.id,
      thinkingMode: thinkingModeForReasoning(reasoning.id),
      reasoningEffort: reasoningEffortForReasoning(reasoning.id),
      prefixHash: "",
      systemPromptHash: "",
      stablePromptTokens: 0,
      messageCount: 0,
      turnCount: 0,
      sessionCacheHitTokens: 0,
      sessionCacheMissTokens: 0,
      sessionCacheHitRate: 0,
      appendOnlyPrefixStable: true,
      previousRequestMessageCount: 0,
      commonPrefixMessageCount: 0,
      resetReason: "等待首轮会话初始化。",
    },
    skillsIndex: fallbackSkillsIndex,
    mcpSnapshots: fallbackSnapshots,
    mcpServers: [
      {
        id: "filesystem",
        name: "filesystem",
        transport: "builtin",
        state: "ready",
        message: "内置工具由 EchoCode 运行时提供",
        toolCount: 4,
      },
    ],
		plugins: [
			{
				id: "office-artifacts",
				name: "办公产物",
				version: "1.0.0",
				description: "独立读取、创建和编辑 DOCX、XLS/XLSX 与 PPTX。",
				author: "EchoCode",
				source: "builtin",
				state: "ready",
				message: "内置产物引擎不依赖本机 Office。",
				toolCount: 11,
				availableToolCount: 11,
				permissions: {fileRead: true, fileWrite: true, network: false},
				grantedPermissions: {fileRead: true, fileWrite: true, network: false},
				canUninstall: false,
				manifestSchema: 1,
				protocolVersion: "1.0",
				tools: [],
			},
		],
    contextPreview: {
      stablePrefix: [
        { name: "product_identity", content: "EchoCode 是面向开发者的 AI 协议交换台。" },
        { name: "system_rules", content: "稳定前缀保持顺序、文本和 schema 哈希可复现。" },
        { name: "reasoning", content: `${reasoning.id}:${reasoning.budget.cachePolicy}` },
        { name: "skills_index", content: "skill: echocode-agent-core\n---\nskill: echocode-office-artifacts" },
        { name: "mcp_schema_snapshot", content: "filesystem tools summary-first" },
        { name: "project_summary", content: "Go 核心引擎 + Wails v2 + SolidJS 前端。" },
        { name: "routing_policy", content: "DeepSeek official first, OpenAI-compatible later." },
      ],
      volatileTail: [
        { name: "user_input", content: "用户本轮输入会进入易变尾部。" },
        { name: "recent_diff", content: "" },
        { name: "tool_results", content: "[]" },
        { name: "output_requirements", content: "输出结构化摘要。" },
      ],
      prefixHash: "sha256:local-preview",
      triggeredSkillNames: [],
      triggeredSkillCharacters: 0,
      triggeredSkillTokens: 0,
    },
    cacheDiagnostics: ["等待首轮模型请求记录缓存命中数据。"],
    runtimeSettings: defaultRuntimeSettings(),
    sandboxCapabilities: {
      platform: "browser",
      backend: "preview-only",
      processTree: false,
      resourceLimits: false,
      privilegeIsolation: false,
      filesystemIsolation: false,
      networkIsolation: false,
      summary: "桌面运行时连接后显示系统沙箱能力。",
    },
    configFiles: {
      runtimeSettingsPath: "",
      modelProvidersPath: "",
      secretsStore: "系统凭据管理器 / 本地 vault",
    },
    team: {
      enabled: false,
      active: false,
      status: "idle",
      roles: [],
    },
    projectMemory: {
      enabled: true,
      projectName: "EchoCode",
      sessionCount: 0,
      turnCount: 0,
      snapshotHash: "sha256:local-preview",
      summary: "Project: EchoCode",
    },
  };
}

function requireBrowserBinding(method: keyof WailsAppBinding): WailsAppBinding {
  const binding = wailsBinding();
  if (!binding || typeof binding[method] !== "function") {
    throw new Error("内置浏览器仅在 EchoCode 桌面应用中可用。");
  }
  return binding;
}

function emptyGitStatus(): GitStatus {
  return {
    available: false,
    ahead: 0,
    behind: 0,
    clean: true,
    detached: false,
    stagedCount: 0,
    modifiedCount: 0,
    untrackedCount: 0,
    conflictCount: 0,
    files: [],
    branches: [],
  };
}

function emptyBrowserState(message: string): BrowserState {
  return {
    available: false,
    running: false,
    engine: "",
    renderMode: "stream",
    activeTabId: "",
    tabs: [],
    downloads: [],
    lastError: message,
    cdpEnabled: false,
  };
}

function cloneState(state: WorkbenchState): WorkbenchState {
  return JSON.parse(JSON.stringify(state)) as WorkbenchState;
}

function cloneChatResult(result: ChatResult): ChatResult {
  return JSON.parse(JSON.stringify(result)) as ChatResult;
}

function pendingCacheHealth() {
  return {
    status: "pending",
    message: "等待首轮模型请求记录缓存命中数据。",
    hitRate: 0,
    targetHitRate: 0.96,
    hitTokens: 0,
    missTokens: 0,
    totalCacheTokens: 0,
    missTokenBudget: 0,
    requiredHitTokens: 0,
    additionalHitTokensNeeded: 0,
    shortPrompt: false,
    sampleCount: 0,
    consecutiveBelowTarget: 0,
    hitTokensIncreasing: false,
    missTokensStable: false,
    missTokensImproving: false,
  };
}

function defaultRuntimeSettings(): RuntimeSettings {
  return {
    sandboxMode: "workspace-write",
    filesystemAccess: "workspace-write",
    networkAccess: true,
    shellAccess: true,
    approvalPolicy: "on-request",
    workspaceRoot: "",
    extraWritableRoots: [],
    toolTimeoutSeconds: 180,
    taskIdleTimeoutSeconds: 300,
    maxConcurrentSubagents: 4,
    maxCommandSeconds: 120,
    maxCommandMemoryMb: 4096,
    maxCommandCpuPercent: 100,
    maxCommandProcesses: 128,
    allowDestructiveOps: false,
    toolResultPolicy: "summary-first",
    stablePrefixPolicy: "strict-stable-prefix",
    cacheTargetPercent: 96,
    git: {
      branchPrefix: "echocode/",
      mergeMethod: "merge",
      showPullRequestIcon: true,
      forcePushWithLease: false,
      draftPullRequests: true,
      autoDeleteOldWorktrees: true,
      worktreeCleanupLimit: 15,
      commitInstructions: "",
      pullRequestInstructions: "",
    },
    memory: {
      enabled: true,
      maxSessions: 12,
      maxCharacters: 6000,
      includeArchived: true,
    },
    browser: {
      enabled: true,
      defaultLocalUrlDestination: "echocode",
      clearDataPolicy: "ask",
      screenshotAnnotations: "always",
      passwordManagerEnabled: false,
      autofillContactEnabled: false,
      autofillProfile: {
        fullName: "",
        email: "",
        phone: "",
        organization: "",
        streetAddress: "",
        city: "",
        region: "",
        postalCode: "",
        country: "",
      },
      credentials: [],
      sitePermissions: [],
      developerCdpAccess: false,
    },
    computerControl: {
      anyAppEnabled: false,
      chromeEnabled: false,
      alwaysAllowedApps: [],
    },
    mcp: {
      servers: [
        {
          id: "filesystem",
          name: "filesystem",
          transport: "builtin",
          command: "builtin:filesystem",
          args: [],
          env: [],
          passEnvironment: [],
          workingDirectory: "",
          url: "",
          headers: [],
          enabled: true,
          toolResultPolicy: "summary-first",
        },
      ],
    },
		plugins: {
			maxExecutionSeconds: 120,
			maxOutputBytes: 1024 * 1024,
			entries: [
				{id: "office-artifacts", enabled: true, permissions: {fileRead: true, fileWrite: true, network: false}},
			],
		},
    model: {
      selectedProviderId: "deepseek",
      selectedModelId: "",
      providers: [
        {
          id: "deepseek",
          name: "DeepSeek 官方",
          protocol: "deepseek-official",
          apiType: "chat-completions",
          baseUrl: "https://api.deepseek.com",
          balanceUrl: "",
          extraHeaders: "",
          extraBodyJson: "",
          enabled: true,
          apiKeyConfigured: false,
          defaultModelId: "",
          contextWindowTokens: 128000,
          models: [],
          lastSyncStatus: "idle",
          lastSyncMessage: "等待保存 API Key 后刷新模型。",
          supportsModelFetch: true,
        },
        {
          id: "openai-compatible",
          name: "OpenAI 兼容",
          protocol: "openai-compatible",
          apiType: "chat-completions",
          baseUrl: "https://api.openai.com/v1",
          balanceUrl: "",
          extraHeaders: "",
          extraBodyJson: "",
          enabled: false,
          apiKeyConfigured: false,
          defaultModelId: "",
          contextWindowTokens: 0,
          models: [],
          lastSyncStatus: "idle",
          lastSyncMessage: "填写 Base URL 与 API Key 后可自动获取模型。",
          supportsModelFetch: true,
        },
        {
          id: "local-openai",
          name: "本地 OpenAI 兼容",
          protocol: "openai-compatible",
          apiType: "chat-completions",
          baseUrl: "http://127.0.0.1:11434/v1",
          balanceUrl: "",
          extraHeaders: "",
          extraBodyJson: "",
          enabled: false,
          apiKeyConfigured: false,
          defaultModelId: "",
          contextWindowTokens: 0,
          models: [],
          lastSyncStatus: "idle",
          lastSyncMessage: "适用于 Ollama、LM Studio 等本地兼容服务。",
          supportsModelFetch: true,
        },
      ],
    },
    team: defaultTeamSettings(),
    skills: {
      disabled: [],
    },
    update: {
      autoCheck: true,
      autoDownload: false,
      channel: "stable",
    },
    workspace: {
      configured: true,
      dependenciesEnabled: true,
    },
  };
}

function normalizeRuntimeSettings(settings: RuntimeSettings): RuntimeSettings {
  const defaults = defaultRuntimeSettings();
  const merged = {
    ...defaults,
    ...settings,
    git: { ...defaults.git, ...settings.git },
    browser: {
      ...defaults.browser,
      ...settings.browser,
      autofillProfile: { ...defaults.browser.autofillProfile, ...settings.browser?.autofillProfile },
      credentials: Array.isArray(settings.browser?.credentials) ? settings.browser.credentials : [],
      sitePermissions: Array.isArray(settings.browser?.sitePermissions) ? settings.browser.sitePermissions : [],
    },
    computerControl: {
      ...defaults.computerControl,
      ...settings.computerControl,
      alwaysAllowedApps: Array.isArray(settings.computerControl?.alwaysAllowedApps)
        ? settings.computerControl.alwaysAllowedApps
        : [],
    },
    mcp: {
      ...defaults.mcp,
      ...settings.mcp,
      servers: Array.isArray(settings.mcp?.servers) && settings.mcp.servers.length > 0 ? settings.mcp.servers : defaults.mcp.servers,
    },
		plugins: {
			...defaults.plugins,
			...settings.plugins,
			entries: Array.isArray(settings.plugins?.entries) ? settings.plugins.entries : defaults.plugins.entries,
		},
    team: {
      ...defaults.team,
      ...settings.team,
      roles: Array.isArray(settings.team?.roles) && settings.team.roles.length > 0 ? settings.team.roles : defaults.team.roles,
    },
    update: { ...defaults.update, ...settings.update },
    model: {
      ...defaults.model,
      ...settings.model,
      providers: Array.isArray(settings.model?.providers) ? settings.model.providers : defaults.model.providers,
    },
    workspace: { ...defaults.workspace, ...settings.workspace },
    memory: { ...defaults.memory, ...settings.memory },
    skills: { ...defaults.skills, ...settings.skills },
  };
  return {
    ...merged,
    extraWritableRoots: Array.isArray(merged.extraWritableRoots)
      ? merged.extraWritableRoots.map((item) => item.trim()).filter(Boolean)
      : [],
    toolTimeoutSeconds: clampNumber(Number(merged.toolTimeoutSeconds), 5, 3600),
    taskIdleTimeoutSeconds: clampNumber(Number(merged.taskIdleTimeoutSeconds), 15, 7200),
    maxCommandSeconds: clampNumber(Number(merged.maxCommandSeconds), 5, 3600),
    maxCommandMemoryMb: clampNumber(Number(merged.maxCommandMemoryMb), 256, 65536),
    maxCommandCpuPercent: clampNumber(Number(merged.maxCommandCpuPercent), 10, 100),
    maxCommandProcesses: clampNumber(Number(merged.maxCommandProcesses), 4, 1024),
    cacheTargetPercent: clampNumber(Number(merged.cacheTargetPercent), 0, 100),
    skills: {
      ...merged.skills,
      disabled: Array.isArray(merged.skills.disabled)
        ? merged.skills.disabled.map((item) => String(item).trim()).filter(Boolean).filter((item, index, values) => values.indexOf(item) === index).sort()
        : [],
    },
    git: {
      ...merged.git,
      worktreeCleanupLimit: clampNumber(Number(merged.git.worktreeCleanupLimit), 1, 99),
    },
    memory: {
      ...merged.memory,
      maxSessions: clampNumber(Number(merged.memory.maxSessions), 1, 100),
      maxCharacters: clampNumber(Number(merged.memory.maxCharacters), 1000, 20000),
    },
    browser: {
      ...merged.browser,
      autofillProfile: Object.fromEntries(
        Object.entries(merged.browser.autofillProfile).map(([key, value]) => [key, String(value ?? "").trim()]),
      ) as RuntimeSettings["browser"]["autofillProfile"],
      credentials: merged.browser.credentials
        .map((credential) => ({
          ...credential,
          id: credential.id.trim(),
          origin: credential.origin.trim(),
          username: credential.username.trim(),
        }))
        .filter((credential) => credential.id && credential.origin && credential.username),
      sitePermissions: merged.browser.sitePermissions
        .map((item) => ({
          origin: item.origin.trim(),
          camera: item.camera || "ask",
          microphone: item.microphone || "ask",
          clipboard: item.clipboard || "ask",
        }))
        .filter((item) => item.origin),
    },
    computerControl: {
      ...merged.computerControl,
      alwaysAllowedApps: merged.computerControl.alwaysAllowedApps.map((item) => item.trim()).filter(Boolean),
    },
    mcp: {
      ...merged.mcp,
      servers: merged.mcp.servers.map((server) => ({
        ...server,
        id: server.id.trim() || server.name.trim() || server.command.trim(),
        name: server.name.trim() || server.id.trim() || "MCP Server",
        transport: server.command?.startsWith("builtin:") ? "builtin" : server.transport || (server.url ? "streamable-http" : "stdio"),
        command: server.command.trim(),
        args: Array.isArray(server.args) ? server.args.map((item) => item.trim()).filter(Boolean) : [],
        env: Array.isArray(server.env)
          ? server.env.map((item) => ({ key: item.key.trim(), value: item.value.trim() })).filter((item) => item.key)
          : [],
        passEnvironment: Array.isArray(server.passEnvironment) ? server.passEnvironment.map((item) => item.trim()).filter(Boolean) : [],
        workingDirectory: server.workingDirectory?.trim() ?? "",
        url: server.url?.trim() ?? "",
        headers: Array.isArray(server.headers)
          ? server.headers.map((item) => ({ key: item.key.trim(), value: item.value.trim() })).filter((item) => item.key)
          : [],
        toolResultPolicy: server.toolResultPolicy || "summary-first",
      })),
    },
		plugins: {
			...merged.plugins,
			maxExecutionSeconds: clampNumber(Number(merged.plugins.maxExecutionSeconds), 5, 3600),
			maxOutputBytes: clampNumber(Number(merged.plugins.maxOutputBytes), 64 * 1024, 16 * 1024 * 1024),
			entries: merged.plugins.entries
				.map((entry) => ({
					id: entry.id.trim().toLowerCase(),
					enabled: Boolean(entry.enabled),
					permissions: {
						fileRead: Boolean(entry.permissions?.fileRead),
						fileWrite: Boolean(entry.permissions?.fileWrite),
						network: Boolean(entry.permissions?.network),
					},
				}))
				.filter((entry, index, entries) => entry.id && entries.findIndex((candidate) => candidate.id === entry.id) === index)
				.sort((left, right) => left.id.localeCompare(right.id)),
		},
    model: {
      ...merged.model,
      selectedProviderId: merged.model.selectedProviderId || merged.model.providers[0]?.id || "",
      providers: merged.model.providers.map((provider) => ({
        ...provider,
        id: (provider.id ?? "").trim(),
        name: (provider.name ?? "").trim() || provider.id,
        protocol: provider.protocol || "openai-compatible",
        apiType: provider.apiType || defaultAPITypeForProtocol(provider.protocol),
        baseUrl: (provider.baseUrl ?? "").trim(),
        balanceUrl: provider.balanceUrl?.trim() ?? "",
        extraHeaders: provider.extraHeaders?.trim() ?? "",
        extraBodyJson: provider.extraBodyJson?.trim() ?? "",
        contextWindowTokens: normalizeTokenWindow(provider.contextWindowTokens),
        models: Array.isArray(provider.models)
          ? provider.models.map((model) => ({
              ...model,
              id: (model.id ?? "").trim(),
              displayName: model.displayName?.trim() || model.id,
              provider: model.provider?.trim() || provider.id,
              contextWindowTokens: normalizeTokenWindow(model.contextWindowTokens),
              contextWindowSource: normalizeContextWindowSource(model.contextWindowSource, model.contextWindowTokens),
              maxOutputTokens: normalizeTokenWindow(model.maxOutputTokens ?? 0),
              reasoningLevels: Array.isArray(model.reasoningLevels)
                ? model.reasoningLevels.filter((level) => ["none", "low", "medium", "high", "xhigh", "max"].includes(level))
                : undefined,
              thinkingModes: Array.isArray(model.thinkingModes)
                ? model.thinkingModes.filter((mode) => ["adaptive", "enabled", "disabled"].includes(mode))
                : undefined,
              unsupportedParameters: Array.isArray(model.unsupportedParameters)
                ? model.unsupportedParameters.filter((parameter) => ["temperature", "thinking", "output_config"].includes(parameter))
                : undefined,
            }))
          : [],
        supportsModelFetch: supportsProviderModelFetch(provider.protocol),
      })),
    },
  };
}

function normalizeContextWindowSource(source: string | undefined, tokens: number) {
  const normalized = (source ?? "").trim().toLowerCase();
  const allowed = new Set(["upstream", "catalog", "protocol-default", "provider-default", "manual", "safe-default"]);
  if (tokens <= 0) {
    return "";
  }
  return allowed.has(normalized) ? normalized : "manual";
}

function updateFallbackProvider(
  providerID: string,
  updater: (provider: RuntimeSettings["model"]["providers"][number]) => RuntimeSettings["model"]["providers"][number],
) {
  const runtimeSettings = normalizeRuntimeSettings(fallbackState.runtimeSettings);
  const providers = runtimeSettings.model.providers.map((provider) => (provider.id === providerID ? updater(provider) : provider));
  return {
    ...fallbackState,
    runtimeSettings: normalizeRuntimeSettings({
      ...runtimeSettings,
      model: {
        ...runtimeSettings.model,
        providers,
      },
    }),
  };
}

function fallbackUsageBillingReport(providerID: string): UsageBillingReport {
  const provider = fallbackState.runtimeSettings.model.providers.find((candidate) => candidate.id === providerID)
    ?? fallbackState.runtimeSettings.model.providers.find((candidate) => candidate.id === fallbackState.runtimeSettings.model.selectedProviderId);
  const host = (() => {
    try {
      return new URL(provider?.baseUrl ?? "").hostname.toLowerCase();
    } catch {
      return "";
    }
  })();
  const kind = host === "api.openai.com" ? "openai"
    : host === "api.anthropic.com" ? "anthropic"
      : host === "generativelanguage.googleapis.com" ? "gemini"
        : host === "api.x.ai" ? "xai"
          : host === "api.deepseek.com" ? "deepseek"
            : "";
  const officialProvider = Boolean(kind);
  return {
    providerId: provider?.id ?? providerID,
    providerName: provider?.name ?? "未选择供应商",
    providerKind: kind,
    officialProvider,
    status: officialProvider ? "needs_reconciliation" : "custom_provider",
    message: officialProvider
      ? "尚未同步该供应商的官方账单。"
      : "自定义或中转供应商仅显示本地估算，不会标记为官方准确费用。",
    verified: false,
    toleranceUsd: 2,
    warningToleranceUsd: 3,
    estimatedCost: 0,
    officialCost: 0,
    difference: 0,
    absoluteDifference: 0,
    recommendedSource: officialProvider ? "使用该供应商的官方账单接口或账单导出对账。" : "填写供应商实际费率后，可手动录入账单金额对账。",
    scope: kind === "openai" ? "整个 OpenAI 组织" : "供应商账单周期",
    scopeConfigured: kind !== "openai" || Boolean(provider?.billingProjectId || provider?.billingApiKeyId),
    officialUsage: [],
    officialInputTokens: 0,
    officialOutputTokens: 0,
    officialCachedTokens: 0,
    officialCacheWriteTokens: 0,
    officialUncachedTokens: 0,
    officialInputTextTokens: 0,
    officialOutputTextTokens: 0,
    officialCachedTextTokens: 0,
    officialInputAudioTokens: 0,
    officialCachedAudioTokens: 0,
    officialOutputAudioTokens: 0,
    officialInputImageTokens: 0,
    officialCachedImageTokens: 0,
    officialOutputImageTokens: 0,
    officialRequests: 0,
  };
}

function clampNumber(value: number, min: number, max: number) {
  if (!Number.isFinite(value)) {
    return min;
  }
  return Math.min(Math.max(value, min), max);
}

function normalizeTokenWindow(value: number) {
  const parsed = Number(value);
  if (!Number.isFinite(parsed) || parsed < 0) {
    return 0;
  }
  return Math.floor(parsed);
}

function defaultAPITypeForProtocol(protocol: string) {
  if (protocol === "anthropic" || protocol === "anthropic-compatible") {
    return "anthropic-messages";
  }
  if (protocol === "gemini") {
    return "gemini-generate-content";
  }
  return "chat-completions";
}

function supportsProviderModelFetch(protocol: string) {
  return (
    protocol === "deepseek-official" ||
    protocol === "openai-compatible" ||
    protocol === "anthropic" ||
    protocol === "anthropic-compatible" ||
    protocol === "gemini" ||
    protocol === "local"
  );
}

function thinkingModeForReasoning(level: ReasoningLevel) {
  return level === "none" ? "disabled" : "enabled";
}

function reasoningEffortForReasoning(level: ReasoningLevel) {
  return level;
}
