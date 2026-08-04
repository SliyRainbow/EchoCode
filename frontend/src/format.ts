// 纯工具函数与 fallback 构造器（从 app.tsx 抽离，无 JSX）。
import { defaultReasoningLevel } from "./state/reasoning";
import type {
  DeepSeekSessionState,
  ModelProviderSetting,
  RuntimeSettings,
  UsageMetrics,
  WorkbenchState,
} from "./types";
import type { ChatMessage, DrawerTab, SettingsCategory, ThemeMode, ProviderPreset } from "./ui-types";
import { defaultTeamSettings } from "./team-config";
import {
  settingsGroups,
  sitePermissionOptions,
  defaultSidebarWidth,
  minSidebarWidth,
  maxSidebarWidth,
  sidebarWidthStorageKey,
  defaultBrowserPanelWidth,
  minBrowserPanelWidth,
  maxBrowserPanelWidth,
  browserPanelWidthStorageKey,
  themeStorageKey,
} from "./constants";

export function createChatMessage(role: ChatMessage["role"], content: string): ChatMessage {
  return {
    id: `${role}-${Date.now()}-${Math.random().toString(16).slice(2)}`,
    role,
    content,
    createdAt: new Date().toISOString(),
  };
}

export function emptyUsageMetrics(): UsageMetrics {
  return {
    promptCacheHitTokens: 0,
    promptCacheMissTokens: 0,
    inputTokens: 0,
    outputTokens: 0,
    effectiveCost: 0,
  };
}

export function fallbackDeepSeekState() {
  return {
    configured: false,
    baseUrl: "https://api.deepseek.com",
    lastCheckStatus: "idle",
    lastCheckMessage: "等待保存 DeepSeek API Key。",
    models: [],
  };
}

export function fallbackDeepSeekSession(): DeepSeekSessionState {
  return {
    active: false,
    providerId: "",
    providerName: "",
    protocol: "",
    model: "",
    reasoning: defaultReasoningLevel,
    thinkingMode: "disabled",
    reasoningEffort: "",
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
  };
}

export function fallbackCacheHealth(): WorkbenchState["cacheHealth"] {
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

export function fallbackRuntimeSettings(): RuntimeSettings {
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

export function fallbackConfigFiles(): WorkbenchState["configFiles"] {
  return {
    runtimeSettingsPath: "",
    modelProvidersPath: "",
    secretsStore: "系统凭据管理器 / 本地 vault",
  };
}

export function categoryForDrawerTab(tab: DrawerTab): SettingsCategory {
  switch (tab) {
    case "settings":
      return "general";
    case "cache":
      return "usage";
    case "context":
      return "index";
    case "tools":
      return "skills";
  }
}

export function findSettingsItem(category: SettingsCategory) {
  for (const group of settingsGroups) {
    const item = group.items.find((candidate) => candidate.id === category);
    if (item) {
      return item;
    }
  }
  return settingsGroups[0].items[0];
}

export function settingsGroupTitle(category: SettingsCategory) {
  return settingsGroups.find((group) => group.items.some((item) => item.id === category))?.title ?? "设置";
}

export function settingsCategoryDescription(category: SettingsCategory) {
  switch (category) {
    case "config":
      return "配置审批策略和沙盒设置。";
    case "mcp":
      return "连接外部工具和数据源。";
		case "extensions":
			return "发现、安装和管理经过校验的 MCP、插件与 Skills。";
		case "plugins":
			return "管理 Office、Access 和第三方扩展工具。";
    case "browser":
      return "管理 EchoCode 的浏览器。可在电脑使用设置中配置 Google Chrome。";
    case "computer":
      return "管理 EchoCode 如何使用您电脑上的其他应用程序。";
    case "git":
      return "设置分支、拉取请求和工作树清理策略。";
    case "environment":
      return "本地环境用于指示 EchoCode 如何为项目设置工作树。";
    case "general":
      return "管理 DeepSeek 连接和当前会话。";
    case "appearance":
      return "调整主题和工作区布局。";
    case "models":
      return "选择模型和推理强度。";
    case "team":
      return "配置规划、实现、测试、审阅和汇总角色使用的模型。";
    case "skills":
      return "查看模型可调用的内置 Agent 工具、Schema 快照和已加载 Skills。";
    case "commands":
      return "查看本地命令权限和工具路由。";
    case "memory":
      return "管理项目级跨会话记忆。";
    case "index":
      return "查看稳定前缀和易变上下文。";
    case "usage":
      return "查看缓存命中率、tokens 和诊断信息。";
    case "profile":
      return "查看当前用户和个性化摘要。";
    case "shortcuts":
      return "查看常用键盘快捷键。";
    case "about":
      return "查看版本与构建信息，并管理自动更新。";
    case "automation":
      return "让 Agent 按计划在指定项目与会话中执行任务。";
    case "archive":
      return "查看已归档的对话。";
  }
}

export function baseNameFromPath(path: string) {
  return path.replace(/[\\/]+$/, "").split(/[\\/]/).filter(Boolean).pop() ?? path;
}

export function parentPath(path: string) {
  const parts = path.replace(/[\\/]+$/, "").split(/[\\/]/);
  if (parts.length <= 1) {
    return path;
  }
  return parts.slice(0, -1).join("\\");
}

export function prefixStatusLabel(session: DeepSeekSessionState) {
  if (!session.active || session.previousRequestMessageCount === 0) {
    return "首轮";
  }
  if (session.appendOnlyPrefixStable) {
    return `稳定 ${session.commonPrefixMessageCount}/${session.previousRequestMessageCount}`;
  }
  return `变动 ${session.commonPrefixMessageCount}/${session.previousRequestMessageCount}`;
}

export function thinkingStatusLabel(session: DeepSeekSessionState) {
  if (session.thinkingMode === "enabled") {
    return session.reasoningEffort ? `开启 ${session.reasoningEffort}` : "开启";
  }
  if (session.thinkingMode === "disabled") {
    return "关闭";
  }
  return "下一轮选择";
}

export function messageTitle(message: ChatMessage) {
  if (message.role === "user") {
    return "你";
  }
  if (message.role === "assistant") {
    return message.model || "DeepSeek";
  }
  return "系统";
}

export function formatClock(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "";
  }
  return date.toLocaleTimeString("zh-CN", { hour: "2-digit", minute: "2-digit" });
}

export function formatPercent(value: number, available: boolean) {
  if (!available) {
    return "待采集";
  }
  return `${(value * 100).toFixed(1)}%`;
}

export function formatInteger(value: number) {
  return new Intl.NumberFormat("zh-CN").format(value);
}

export function formatCost(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return "$0.0000";
  }
  return new Intl.NumberFormat("en-US", {
    style: "currency",
    currency: "USD",
    minimumFractionDigits: value < 0.01 ? 4 : 2,
    maximumFractionDigits: value < 0.01 ? 6 : 2,
  }).format(value);
}

export function shortHash(value?: string) {
  if (!value) {
    return "sha256:pending";
  }
  if (value.length <= 22) {
    return value;
  }
  return `${value.slice(0, 13)}...${value.slice(-8)}`;
}

export function sectionLabel(name: string) {
  const labels: Record<string, string> = {
    product_identity: "产品身份",
    system_rules: "Agent 规则",
    reasoning: "当前推理",
    skills_index: "Skills 索引",
    mcp_schema_snapshot: "MCP schema",
    project_summary: "项目摘要",
    routing_policy: "路由策略",
    user_input: "用户输入",
    recent_diff: "最近 diff",
    tool_results: "工具结果",
    output_requirements: "输出要求",
  };
  return labels[name] ?? name;
}

export function statusLabel(status: string) {
  switch (status) {
    case "ok":
      return "已连接";
    case "error":
      return "连接异常";
    default:
      return "待检测";
  }
}

export function cacheStatusLabel(status: string) {
  switch (status) {
    case "ok":
      return "已达标";
    case "watch":
      return "观察中";
    case "warming":
      return "DS 预热中";
    case "cold":
      return "冷启动";
    case "low":
      return "需优化";
    default:
      return "待采集";
  }
}

export function runtimeLabel(options: Array<{ value: string; label: string }>, value: string) {
  return options.find((option) => option.value === value)?.label ?? value;
}

export function selectedModelProvider(settings: RuntimeSettings) {
  return (
    settings.model.providers.find((provider) => provider.id === settings.model.selectedProviderId) ??
    settings.model.providers.find((provider) => provider.enabled) ??
    settings.model.providers[0]
  );
}

export function selectedModelName(settings: RuntimeSettings, activeSessionModel?: string) {
  if (activeSessionModel) {
    return activeSessionModel;
  }
  const provider = selectedModelProvider(settings);
  if (!provider) {
    return "";
  }
  if (settings.model.selectedProviderId === provider.id && settings.model.selectedModelId) {
    return settings.model.selectedModelId;
  }
  if (provider.defaultModelId) {
    return provider.defaultModelId;
  }
  if (provider.models[0]?.id) {
    return provider.models[0].id;
  }
  if (provider.id === "deepseek") {
    return "deepseek-v4-flash";
  }
  return "";
}

export function modelOptionsForProvider(provider: ModelProviderSetting) {
  if (provider.models.length > 0) {
    return provider.models;
  }
  if (provider.id === "deepseek") {
    return [
      { id: "deepseek-v4-flash", displayName: "DeepSeek V4 Flash", provider: provider.id, contextWindowTokens: provider.contextWindowTokens || 128000, contextWindowSource: "catalog" },
      { id: "deepseek-v4-pro", displayName: "DeepSeek V4 Pro", provider: provider.id, contextWindowTokens: provider.contextWindowTokens || 128000, contextWindowSource: "catalog" },
    ];
  }
  return [];
}

export function shortModelName(modelID: string) {
  if (modelID.length <= 28) {
    return modelID;
  }
  return `${modelID.slice(0, 14)}...${modelID.slice(-10)}`;
}

export function providerReadyForChat(provider: ModelProviderSetting | undefined) {
  if (!provider) {
    return false;
  }
  return provider.apiKeyConfigured || provider.protocol === "local" || isLocalProviderURL(provider.baseUrl);
}

export function providerConnectionSummary(provider: ModelProviderSetting | undefined, deepSeek: WorkbenchState["deepSeek"]) {
  if (!provider) {
    return { label: "未连接", ok: false, ready: false };
  }
  const ready = providerReadyForChat(provider);
  if (provider.id === "deepseek") {
    return {
      label: deepSeek.configured ? statusLabel(deepSeek.lastCheckStatus) : "未连接",
      ok: deepSeek.lastCheckStatus === "ok",
      ready: deepSeek.configured,
    };
  }
  if (!ready) {
    return { label: "未连接", ok: false, ready: false };
  }
  return {
    label: provider.lastSyncStatus === "ok" ? "已连接" : provider.protocol === "local" ? "本地" : "已配置",
    ok: provider.lastSyncStatus === "ok" || provider.protocol === "local",
    ready: true,
  };
}

export function isLocalProviderURL(baseUrl: string) {
  const value = baseUrl.toLowerCase();
  return value.includes("localhost") || value.includes("127.0.0.1") || value.includes("[::1]") || value.includes("0.0.0.0");
}

export function permissionLabel(value: string) {
  return runtimeLabel(sitePermissionOptions, value);
}

export function providerBaseURLHint(protocol: string) {
  switch (protocol) {
    case "deepseek-official":
      return "DeepSeek 官方默认 https://api.deepseek.com";
    case "openai-compatible":
      return "填写兼容 /v1 接口的根地址，例如 https://api.openai.com/v1";
    case "local":
      return "填写本机兼容 /v1 地址，例如 http://127.0.0.1:11434/v1";
    case "anthropic-compatible":
      return "填写 Anthropic API 根地址，例如 https://api.anthropic.com";
    case "gemini":
      return "填写 Gemini API 根地址，例如 https://generativelanguage.googleapis.com/v1beta";
    default:
      return "填写上游模型服务的根地址。";
  }
}

export function providerBaseURLPlaceholder(protocol: string) {
  switch (protocol) {
    case "deepseek-official":
      return "https://api.deepseek.com";
    case "local":
      return "http://127.0.0.1:11434/v1";
    case "anthropic-compatible":
      return "https://api.anthropic.com";
    case "gemini":
      return "https://generativelanguage.googleapis.com/v1beta";
    default:
      return "https://api.openai.com/v1";
  }
}

export function providerFromPreset(preset: ProviderPreset, existing?: ModelProviderSetting): ModelProviderSetting {
  return {
    id: preset.id,
    name: preset.name,
    protocol: preset.protocol,
    apiType: preset.apiType,
    baseUrl: preset.baseUrl,
    balanceUrl: preset.balanceUrl ?? existing?.balanceUrl ?? "",
    extraHeaders: existing?.extraHeaders ?? "",
    extraBodyJson: existing?.extraBodyJson ?? "",
    reasoningProfile: existing?.reasoningProfile ?? "auto",
    enabled: existing?.enabled ?? true,
    apiKeyConfigured: existing?.apiKeyConfigured ?? false,
    billingKeyConfigured: existing?.billingKeyConfigured ?? false,
    billingProjectId: existing?.billingProjectId ?? "",
    billingApiKeyId: existing?.billingApiKeyId ?? "",
    defaultModelId: existing?.defaultModelId ?? "",
    contextWindowTokens: preset.contextWindowTokens ?? existing?.contextWindowTokens ?? 0,
    inputPricePerMillion: existing?.inputPricePerMillion ?? 0,
    outputPricePerMillion: existing?.outputPricePerMillion ?? 0,
    cacheHitPricePerMillion: existing?.cacheHitPricePerMillion ?? 0,
    cacheMissPricePerMillion: existing?.cacheMissPricePerMillion ?? 0,
    models: existing?.models ?? [],
    lastSyncStatus: existing?.lastSyncStatus ?? "idle",
    lastSyncMessage: existing?.lastSyncMessage ?? "填写密钥后可测试并获取模型。",
    checkedAt: existing?.checkedAt,
    supportsModelFetch: supportsModelFetchForProtocol(preset.protocol),
  };
}

export function createEmptyProvider(providers: ModelProviderSetting[]): ModelProviderSetting {
  const nextIndex = providers.length + 1;
  const id = uniqueProviderID(providers, "custom-provider");
  return {
    id,
    name: `自定义供应商 ${nextIndex}`,
    protocol: "openai-compatible",
    apiType: "chat-completions",
    baseUrl: "",
    balanceUrl: "",
    extraHeaders: "",
    extraBodyJson: "",
    reasoningProfile: "auto",
    enabled: true,
    apiKeyConfigured: false,
    billingKeyConfigured: false,
    billingProjectId: "",
    billingApiKeyId: "",
    defaultModelId: "",
    contextWindowTokens: 0,
    inputPricePerMillion: 0,
    outputPricePerMillion: 0,
    cacheHitPricePerMillion: 0,
    cacheMissPricePerMillion: 0,
    models: [],
    lastSyncStatus: "idle",
    lastSyncMessage: "填写 API 地址与密钥后可测试并获取模型。",
    supportsModelFetch: true,
  };
}

export function uniqueProviderID(providers: ModelProviderSetting[], prefix: string) {
  const ids = new Set(providers.map((provider) => provider.id));
  let index = providers.length + 1;
  let id = `${prefix}-${index}`;
  while (ids.has(id)) {
    index += 1;
    id = `${prefix}-${index}`;
  }
  return id;
}

export function defaultAPITypeForProtocol(protocol: string) {
  if (protocol === "anthropic-compatible" || protocol === "anthropic") {
    return "anthropic-messages";
  }
  if (protocol === "gemini") {
    return "gemini-generate-content";
  }
  return "chat-completions";
}

export function supportsModelFetchForProtocol(protocol: string) {
  return (
    protocol === "deepseek-official" ||
    protocol === "openai-compatible" ||
    protocol === "anthropic" ||
    protocol === "anthropic-compatible" ||
    protocol === "gemini" ||
    protocol === "local"
  );
}

export function formatTokenWindow(value: number) {
  if (!value || value <= 0) {
    return "默认";
  }
  return `${formatInteger(value)} tokens`;
}

export function parseEnvLines(value: string) {
  return value
    .split(/\r?\n/)
    .map((line) => {
      const separatorIndex = line.indexOf("=");
      if (separatorIndex < 0) {
        return { key: line.trim(), value: "" };
      }
      return {
        key: line.slice(0, separatorIndex).trim(),
        value: line.slice(separatorIndex + 1).trim(),
      };
    })
    .filter((item) => item.key);
}

export function parseHeaderLines(value: string) {
  return value
    .split(/\r?\n/)
    .map((line) => {
      const separatorIndex = line.indexOf(":");
      if (separatorIndex < 0) {
        return { key: line.trim(), value: "" };
      }
      return {
        key: line.slice(0, separatorIndex).trim(),
        value: line.slice(separatorIndex + 1).trim(),
      };
    })
    .filter((item) => item.key);
}

export function compactPath(value: string) {
  if (!value) {
    return "未设置";
  }
  const normalized = value.replaceAll("/", "\\");
  const parts = normalized.split("\\").filter(Boolean);
  if (parts.length <= 3) {
    return normalized;
  }
  return `${parts[0]}\\...\\${parts.slice(-2).join("\\")}`;
}

export function readStoredSidebarWidth() {
  const stored = Number(readLocalStorage(sidebarWidthStorageKey));
  if (!Number.isFinite(stored)) {
    return defaultSidebarWidth;
  }
  return clamp(stored, minSidebarWidth, maxSidebarWidth);
}

export function persistSidebarWidth(width: number) {
  writeLocalStorage(sidebarWidthStorageKey, String(Math.round(clamp(width, minSidebarWidth, maxSidebarWidth))));
}

export function readStoredBrowserPanelWidth() {
  const raw = readLocalStorage(browserPanelWidthStorageKey);
  if (raw === null || raw.trim() === "") {
    return undefined;
  }
  const stored = Number(raw);
  if (!Number.isFinite(stored)) {
    return undefined;
  }
  return clamp(stored, minBrowserPanelWidth, maxBrowserPanelWidth);
}

export function persistBrowserPanelWidth(width: number) {
  writeLocalStorage(
    browserPanelWidthStorageKey,
    String(Math.round(clamp(width, minBrowserPanelWidth, maxBrowserPanelWidth))),
  );
}

export function readStoredThemeMode(): ThemeMode {
  const stored = readLocalStorage(themeStorageKey);
  return stored === "light" ? "light" : "dark";
}

export function persistThemeMode(mode: ThemeMode) {
  writeLocalStorage(themeStorageKey, mode);
}

export function applyThemeMode(mode: ThemeMode, shell?: HTMLElement) {
  document.documentElement.dataset.theme = mode;
  document.body.dataset.theme = mode;
  shell?.classList.toggle("theme-light", mode === "light");
  shell?.classList.toggle("theme-dark", mode === "dark");
  forceStyleFlush(shell);
}

export function applySidebarWidth(width: number, shell?: HTMLElement) {
  const next = `${Math.round(clamp(width, minSidebarWidth, maxSidebarWidth))}px`;
  document.documentElement.style.setProperty("--sidebar-width", next);
  shell?.style.setProperty("--sidebar-width", next);
  forceStyleFlush(shell);
}

export function forceStyleFlush(element?: HTMLElement) {
  // WebView2 can occasionally defer repaint on first launch; reading layout flushes pending style changes.
  void document.documentElement.offsetWidth;
  if (element) {
    void element.offsetWidth;
  }
}

export function clamp(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max);
}

export function readLocalStorage(key: string) {
  try {
    return window.localStorage.getItem(key);
  } catch {
    return null;
  }
}

export function writeLocalStorage(key: string, value: string) {
  try {
    window.localStorage.setItem(key, value);
  } catch {
    // Storage can be unavailable in a locked-down WebView; keep the in-memory UI state.
  }
}
