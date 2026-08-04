export type ReasoningLevel = "none" | "low" | "medium" | "high" | "xhigh" | "max";

export type ReasoningBudget = {
  contextPolicy: string;
  cachePolicy: string;
  planner: boolean;
};

export type ReasoningOption = {
  id: ReasoningLevel;
  label: string;
  description: string;
  budget: ReasoningBudget;
};

export type SkillIndexEntry = {
  name: string;
  version: number;
  trigger: string;
  triggerMode?: string;
  summary: string;
  sha256: string;
  description: string;
  disabled?: boolean;
  source?: string;
  path?: string;
};

export type SkillDetail = SkillIndexEntry & {
  content: string;
  canOpen: boolean;
};

export type SkillImportResult = {
  name: string;
  skill: SkillDetail;
  state: WorkbenchState;
};

export type ExtensionPermission = {
  id: string;
  required: boolean;
  reason: string;
};

export type ExtensionProjectAction = {
  id: string;
  label: string;
  args: string[];
  requiresConfirmation: boolean;
  writes?: string[];
};

export type ExtensionManifest = {
  schemaVersion: number;
  id: string;
  type: "mcp" | "plugin" | "skill" | string;
  name: string;
  version: string;
  channel: string;
  summary: string;
  description: string;
  publisher: { name: string; url: string };
  source: { repository: string; release: string; thirdParty: boolean };
  license: { spdx: string; file: string };
  categories: string[];
  capabilities: string[];
  permissions: ExtensionPermission[];
  projectActions?: ExtensionProjectAction[];
};

export type InstalledExtension = {
  id: string;
  type: string;
  name: string;
  version: string;
  platform: string;
  arch: string;
  installDir: string;
  executable: string;
  installedAt: string;
};

export type ExtensionCatalogPackage = {
  id: string;
  type: string;
  name: string;
  summary: string;
  publisher: string;
  featured: boolean;
  sourceVerified: boolean;
  manifestUrl: string;
  manifest: ExtensionManifest;
  installed?: InstalledExtension;
  updateAvailable: boolean;
  platformAvailable: boolean;
};

export type ExtensionCatalogState = {
  registryUrl: string;
  source: string;
  checkedAt: string;
  warning?: string;
  packages: ExtensionCatalogPackage[];
};

export type ExtensionOperationResult = {
  catalog: ExtensionCatalogState;
  state: WorkbenchState;
};

export type ExtensionActionResult = {
  id: string;
  output: string;
  exitCode: number;
  durationMs: number;
};

export type ToolDescriptor = {
  name: string;
  inputSchemaHash: string;
  outputPolicy: string;
};

export type ServerSnapshot = {
  server: string;
  toolsHash: string;
  tools: ToolDescriptor[];
};

export type Model = {
  id: string;
  displayName: string;
  provider: string;
  contextWindowTokens: number;
  contextWindowSource?: string;
  maxOutputTokens?: number;
  reasoningLevels?: ReasoningLevel[];
  thinkingModes?: string[];
  unsupportedParameters?: string[];
};

export type DeepSeekState = {
  configured: boolean;
  baseUrl: string;
  lastCheckStatus: "idle" | "ok" | "error" | string;
  lastCheckMessage: string;
  checkedAt?: string;
  models: Model[];
};

export type DeepSeekSessionState = {
  active: boolean;
  providerId: string;
  providerName: string;
  protocol: string;
  model: string;
  reasoning: ReasoningLevel | "";
  thinkingMode: string;
  reasoningEffort?: string;
  prefixHash: string;
  systemPromptHash: string;
  stablePromptTokens: number;
  messageCount: number;
  turnCount: number;
  startedAt?: string;
  resetReason: string;
  sessionCacheHitTokens: number;
  sessionCacheMissTokens: number;
  sessionCacheHitRate: number;
  appendOnlyPrefixStable: boolean;
  previousRequestMessageCount: number;
  commonPrefixMessageCount: number;
  contextWindowTokens?: number;
  contextWindowSource?: string;
  estimatedInputTokens?: number;
  inputBudgetTokens?: number;
  contextUsagePercent?: number;
  compressionCount?: number;
  compressedMessageCount?: number;
  lastCompressedAt?: string;
};

export type ContextSection = {
  name: string;
  content: string;
};

export type RequestContext = {
  stablePrefix: ContextSection[];
  volatileTail: ContextSection[];
  prefixHash: string;
  triggeredSkillNames: string[];
  triggeredSkillCharacters: number;
  triggeredSkillTokens: number;
};

export type UsageMetrics = {
  promptCacheHitTokens: number;
  promptCacheMissTokens: number;
  inputTokens: number;
  outputTokens: number;
  effectiveCost: number;
};

export type UsageLedgerState = {
  enabled: boolean;
  path?: string;
  sessionSamples: number;
  totalSamples: number;
  sessionInputTokens: number;
  sessionOutputTokens: number;
  totalInputTokens: number;
  totalOutputTokens: number;
  sessionEffectiveCost: number;
  totalEffectiveCost: number;
  lastRecordedAt?: string;
  lastError?: string;
};

export type UsageBillingModelUsage = {
  modelId: string;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  cacheWriteTokens: number;
  uncachedTokens: number;
  inputTextTokens: number;
  outputTextTokens: number;
  cachedTextTokens: number;
  inputAudioTokens: number;
  cachedAudioTokens: number;
  outputAudioTokens: number;
  inputImageTokens: number;
  cachedImageTokens: number;
  outputImageTokens: number;
  requests: number;
};

export type UsageBillingReport = {
  providerId: string;
  providerName: string;
  providerKind: string;
  officialProvider: boolean;
  status: string;
  message: string;
  verified: boolean;
  toleranceUsd: number;
  warningToleranceUsd: number;
  periodStart?: string;
  periodEnd?: string;
  estimatedCost: number;
  officialCost: number;
  difference: number;
  absoluteDifference: number;
  reconciliationSource?: string;
  reconciledAt?: string;
  recommendedSource: string;
  scope: string;
  scopeConfigured: boolean;
  officialUsage: UsageBillingModelUsage[];
  officialInputTokens: number;
  officialOutputTokens: number;
  officialCachedTokens: number;
  officialCacheWriteTokens: number;
  officialUncachedTokens: number;
  officialInputTextTokens: number;
  officialOutputTextTokens: number;
  officialCachedTextTokens: number;
  officialInputAudioTokens: number;
  officialCachedAudioTokens: number;
  officialOutputAudioTokens: number;
  officialInputImageTokens: number;
  officialCachedImageTokens: number;
  officialOutputImageTokens: number;
  officialRequests: number;
};

export type UsageBillingSyncInput = {
  providerId: string;
  periodStart: string;
  periodEnd: string;
};

export type UsageBillingReconciliationInput = UsageBillingSyncInput & {
  officialCost: number;
  source: string;
  note?: string;
};

export type CacheHealth = {
  status: "pending" | "ok" | "watch" | "warming" | "cold" | "low" | string;
  message: string;
  hitRate: number;
  targetHitRate: number;
  hitTokens: number;
  missTokens: number;
  totalCacheTokens: number;
  missTokenBudget: number;
  requiredHitTokens: number;
  additionalHitTokensNeeded: number;
  shortPrompt: boolean;
  sampleCount: number;
  consecutiveBelowTarget: number;
  hitTokensIncreasing: boolean;
  missTokensStable: boolean;
  missTokensImproving: boolean;
};

export type RuntimeSettings = {
  sandboxMode: "read-only" | "workspace-write" | "danger-full-access" | string;
  filesystemAccess: "read-only" | "workspace-write" | "unrestricted" | string;
  networkAccess: boolean;
  shellAccess: boolean;
  approvalPolicy: "never" | "on-request" | "on-failure" | "untrusted" | string;
  workspaceRoot: string;
  extraWritableRoots: string[];
  toolTimeoutSeconds: number;
  taskIdleTimeoutSeconds: number;
  maxConcurrentSubagents: number;
  maxCommandSeconds: number;
  maxCommandMemoryMb: number;
  maxCommandCpuPercent: number;
  maxCommandProcesses: number;
  allowDestructiveOps: boolean;
  toolResultPolicy: "summary-first" | "balanced" | "raw-local" | string;
  stablePrefixPolicy: "reuse-prefix" | "stable-prefix" | "strict-stable-prefix" | string;
  cacheTargetPercent: number;
  git: GitSettings;
  browser: BrowserSettings;
  computerControl: ComputerControlSettings;
  mcp: MCPSettings;
	plugins: PluginSettings;
  model: ModelSettings;
  team: TeamSettings;
  skills: SkillsSettings;
  update: UpdateSettings;
  workspace: WorkspaceSettings;
  memory: MemorySettings;
};

export type SkillsSettings = {
  disabled: string[];
};

export type UpdateSettings = {
  autoCheck: boolean;
  autoDownload: boolean;
  channel: "stable" | string;
};

export type AppInfo = {
  name: string;
  version: string;
  commit?: string;
  buildDate?: string;
  goVersion: string;
  operatingSystem: string;
  architecture: string;
  executablePath: string;
  configPath: string;
  repositoryUrl: string;
};

export type OpenSourceLicense = {
  name: string;
  version?: string;
  description: string;
  license: string;
  url: string;
  text: string;
};

export type UpdateState = {
  currentVersion: string;
  latestVersion?: string;
  updateAvailable: boolean;
  status: "idle" | "checking" | "current" | "available" | "downloading" | "downloaded" | "installing" | "error" | string;
  message: string;
  progress: number;
  downloadedBytes: number;
  totalBytes: number;
  releaseName?: string;
  releaseNotes?: string;
  releaseUrl?: string;
  publishedAt?: string;
  assetName?: string;
  downloadUrl?: string;
  checksumUrl?: string;
  checksumVerified: boolean;
  downloadPath?: string;
  checkedAt?: string;
};

export type AutomationSchedule = {
  kind: "interval" | "daily" | string;
  intervalMinutes: number;
  dailyTime: string;
};

export type AutomationRun = {
  status: "starting" | "running" | "completed" | "failed" | "cancelled" | "interrupted" | string;
  startedAt?: string;
  finishedAt?: string;
  message?: string;
  chatTaskId?: string;
};

export type AutomationTask = {
  id: string;
  name: string;
  enabled: boolean;
  prompt: string;
  projectId: string;
  sessionId: string;
  providerId?: string;
  modelId?: string;
  schedule: AutomationSchedule;
  createdAt: string;
  updatedAt: string;
  nextRunAt?: string;
  lastRun?: AutomationRun;
  runCount: number;
  failureCount: number;
};

export type AutomationState = {
  tasks: AutomationTask[];
  running: boolean;
  updatedAt?: string;
};

export type SandboxCapabilities = {
  platform: string;
  backend: string;
  processTree: boolean;
  resourceLimits: boolean;
  privilegeIsolation: boolean;
  filesystemIsolation: boolean;
  networkIsolation: boolean;
  summary: string;
};

export type GitSettings = {
  branchPrefix: string;
  mergeMethod: "merge" | "squash" | "rebase" | string;
  showPullRequestIcon: boolean;
  forcePushWithLease: boolean;
  draftPullRequests: boolean;
  autoDeleteOldWorktrees: boolean;
  worktreeCleanupLimit: number;
  commitInstructions: string;
  pullRequestInstructions: string;
};

export type MemorySettings = {
  enabled: boolean;
  maxSessions: number;
  maxCharacters: number;
  includeArchived: boolean;
};

export type GitFileStatus = {
  path: string;
  originalPath?: string;
  indexStatus: string;
  worktreeStatus: string;
  staged: boolean;
  modified: boolean;
  untracked: boolean;
  conflicted: boolean;
};

export type GitBranchState = {
  name: string;
  upstream?: string;
  current: boolean;
};

export type GitStatus = {
  available: boolean;
  repositoryRoot?: string;
  branch?: string;
  upstream?: string;
  commit?: string;
  ahead: number;
  behind: number;
  clean: boolean;
  detached: boolean;
  stagedCount: number;
  modifiedCount: number;
  untrackedCount: number;
  conflictCount: number;
  files: GitFileStatus[];
  branches: GitBranchState[];
};

export type GitDiff = {
  path?: string;
  staged: boolean;
  patch: string;
  truncated: boolean;
};

export type WorkspaceFileView = "file" | "changes";

export type WorkspaceFileRequest = {
  id: number;
  path: string;
  view: WorkspaceFileView;
  line?: number;
};

export type WorkspaceFilePreview = {
  path: string;
  name: string;
  content: string;
  encoding: string;
  lineEnding: string;
  lineCount: number;
  size: number;
  truncated: boolean;
  binary: boolean;
  tooLarge: boolean;
  artifact?: OfficeArtifactPreview;
};

export type OfficeArtifactPreview = {
  kind: "document" | "spreadsheet" | "presentation";
  mimeType: string;
  document?: {
    blocks: Array<{ type: "paragraph" | "table" | string; text?: string; style?: string; table?: string[][] }>;
    truncated: boolean;
  };
  spreadsheet?: {
    sheets: Array<{ name: string; rows: string[][]; rowCount: number; columnCount: number; truncated: boolean }>;
    activeSheet?: string;
    truncated: boolean;
  };
  presentation?: {
    slides: Array<{ number: number; title?: string; texts: string[] }>;
    truncated: boolean;
  };
};

export type WorkspaceDirectoryEntry = {
  name: string;
  path: string;
  isDirectory: boolean;
  isSymlink: boolean;
  size: number;
};

export type WorkspaceDirectoryListing = {
  path: string;
  entries: WorkspaceDirectoryEntry[];
  truncated: boolean;
};

export type TerminalSessionState = {
  id: string;
  shell: string;
  workdir: string;
  running: boolean;
  startedAt: string;
  exitCode: number;
  error?: string;
  output: string;
  sandboxed: boolean;
  sandboxBackend?: string;
  privilegeRestricted: boolean;
};

export type BrowserSettings = {
  enabled: boolean;
  defaultLocalUrlDestination: "echocode" | "system" | "ask" | "browser" | string;
  clearDataPolicy: "ask" | "session" | "all" | "never" | string;
  screenshotAnnotations: "always" | "ask" | "never" | string;
  passwordManagerEnabled: boolean;
  autofillContactEnabled: boolean;
  autofillProfile: BrowserAutofillProfile;
  credentials: BrowserCredential[];
  sitePermissions: BrowserSitePermission[];
  developerCdpAccess: boolean;
};

export type BrowserCredential = {
  id: string;
  origin: string;
  username: string;
  passwordConfigured: boolean;
};

export type BrowserAutofillProfile = {
  fullName: string;
  email: string;
  phone: string;
  organization: string;
  streetAddress: string;
  city: string;
  region: string;
  postalCode: string;
  country: string;
};

export type BrowserSitePermission = {
  origin: string;
  camera: "ask" | "allow" | "block" | string;
  microphone: "ask" | "allow" | "block" | string;
  clipboard: "ask" | "allow" | "block" | string;
};

export type ComputerControlSettings = {
  anyAppEnabled: boolean;
  chromeEnabled: boolean;
  alwaysAllowedApps: string[];
};

export type MCPSettings = {
  servers: MCPServerSetting[];
};

export type MCPServerSetting = {
  id: string;
  name: string;
  transport: "builtin" | "stdio" | "streamable-http" | "sse" | string;
  command: string;
  args: string[];
  env: KeyValue[];
  passEnvironment: string[];
  workingDirectory: string;
  url: string;
  headers: KeyValue[];
  enabled: boolean;
  toolResultPolicy: "summary-first" | "balanced" | "raw-local" | string;
  schemaSnapshotHash?: string;
  lastSnapshotAt?: string;
};

export type MCPServerStatus = {
  id: string;
  name: string;
  transport: string;
  state: "idle" | "disabled" | "ready" | "error" | string;
  message: string;
  toolCount: number;
  protocolVersion?: string;
  serverVersion?: string;
  checkedAt?: string;
};

export type PluginPermissionSet = {
	fileRead: boolean;
	fileWrite: boolean;
	network: boolean;
};

export type PluginSetting = {
	id: string;
	enabled: boolean;
	permissions: PluginPermissionSet;
};

export type PluginSettings = {
	maxExecutionSeconds: number;
	maxOutputBytes: number;
	entries: PluginSetting[];
};

export type PluginToolStatus = {
	name: string;
	fullName: string;
	description: string;
	readOnly: boolean;
	permissions: PluginPermissionSet;
};

export type PluginStatus = {
	id: string;
	name: string;
	version: string;
	description: string;
	author?: string;
	homepage?: string;
	source: "builtin" | "installed" | string;
	state: "ready" | "disabled" | "unavailable" | "error" | string;
	message: string;
	path?: string;
	toolCount: number;
	availableToolCount: number;
	permissions: PluginPermissionSet;
	grantedPermissions: PluginPermissionSet;
	canUninstall: boolean;
	manifestSchema: number;
	protocolVersion: string;
	tools: PluginToolStatus[];
};

export type KeyValue = {
  key: string;
  value: string;
};

export type ModelSettings = {
  selectedProviderId: string;
  selectedModelId: string;
  providers: ModelProviderSetting[];
};

export type TeamSettings = {
  enabled: boolean;
  maxReviewRounds: number;
  roles: TeamRoleSetting[];
};

export type TeamRole = "planner" | "implementer" | "tester" | "reviewer" | "synthesizer";

export type TeamRoleSetting = {
  role: TeamRole;
  enabled: boolean;
  providerId: string;
  modelId: string;
};

export type ModelProviderSetting = {
  id: string;
  name: string;
  protocol: "deepseek-official" | "openai-compatible" | "anthropic" | "anthropic-compatible" | "gemini" | "local" | string;
  apiType: "chat-completions" | "responses" | "anthropic-messages" | "gemini-generate-content" | string;
  baseUrl: string;
  balanceUrl: string;
  extraHeaders: string;
  extraBodyJson: string;
  reasoningProfile?: string;
  enabled: boolean;
  apiKeyConfigured: boolean;
  billingKeyConfigured?: boolean;
  billingProjectId?: string;
  billingApiKeyId?: string;
  defaultModelId: string;
  contextWindowTokens: number;
  inputPricePerMillion?: number;
  outputPricePerMillion?: number;
  cacheHitPricePerMillion?: number;
  cacheMissPricePerMillion?: number;
  models: Model[];
  lastSyncStatus: "idle" | "ok" | "error" | string;
  lastSyncMessage: string;
  checkedAt?: string;
  supportsModelFetch: boolean;
};

export type WorkspaceSettings = {
  configured: boolean;
  dependenciesEnabled: boolean;
  lastDiagnosticAt?: string;
  lastDiagnosticNote?: string;
};

export type WorkbenchState = {
  activeProjectId: string;
  activeSessionId: string;
  reasoning: ReasoningOption;
  reasoningOptions: ReasoningOption[];
  cacheTarget: number;
  usageMetrics: UsageMetrics;
  cacheHitRate: number;
  cacheHealth: CacheHealth;
  deepSeek: DeepSeekState;
  deepSeekSession: DeepSeekSessionState;
  skillsIndex: SkillIndexEntry[];
  mcpSnapshots: ServerSnapshot[];
  mcpServers: MCPServerStatus[];
	plugins: PluginStatus[];
  contextPreview: RequestContext;
  cacheDiagnostics: string[];
  runtimeSettings: RuntimeSettings;
  sandboxCapabilities: SandboxCapabilities;
  configFiles: ConfigFilesState;
  planMode?: boolean;
  planState?: PlanState;
  team: TeamState;
  projectMemory: ProjectMemoryState;
  usageLedger?: UsageLedgerState;
	artifacts?: SessionArtifactRecord[];
};

export type SessionArtifactRecord = {
	id: string;
	eventId?: string;
	path: string;
	displayPath?: string;
	name?: string;
	fileType?: string;
	mimeType?: string;
	size?: number;
	modifiedAt?: string;
	sha256?: string;
	action?: "created" | "modified" | "deleted" | "available" | string;
	status: "available" | "deleted" | "missing" | "unreadable" | "invalid" | string;
	tool?: string;
	toolCallId?: string;
	messageId?: string;
	projectId?: string;
	sessionId?: string;
	branchId?: string;
	checkpointId?: string;
	structuralVerification?: "passed" | "failed" | "pending" | "not_applicable" | string;
	visualVerification?: "passed" | "failed" | "pending" | "not_applicable" | string;
	failureReason?: string;
	previewReference?: string;
	renderReference?: string;
	lastCheckedAt?: string;
};

export type PlanState = {
  revision: number;
  status: "running" | "completed" | string;
  steps: Array<{ title: string; status: "pending" | "in_progress" | "completed" | string }>;
  updatedAt?: string;
};

export type TeamState = {
  enabled: boolean;
  active: boolean;
  runId?: string;
  status: "idle" | "running" | "paused" | "completed" | "failed" | "cancelled" | string;
  currentRole?: TeamRole | string;
  roles: TeamRoleState[];
  startedAt?: string;
  completedAt?: string;
  summary?: string;
};

export type TeamRoleState = {
  role: TeamRole;
  label: string;
  enabled: boolean;
  status: "pending" | "running" | "paused" | "completed" | "error" | "cancelled" | "skipped" | string;
  providerId?: string;
  model?: string;
  attempt: number;
  verdict?: "approved" | "changes_required" | "unknown" | string;
  summary?: string;
  error?: string;
  usage: UsageMetrics;
  startedAt?: string;
  finishedAt?: string;
};

export type ProjectMemoryState = {
  enabled: boolean;
  projectId?: string;
  projectName?: string;
  sessionCount: number;
  turnCount: number;
  updatedAt?: string;
  snapshotHash?: string;
  summary: string;
};

export type ConfigFilesState = {
  runtimeSettingsPath: string;
  modelProvidersPath: string;
  secretsStore: string;
};

export type ChatResult = {
  content: string;
  reasoning?: string;
  model: string;
  durationMs?: number;
  usage: UsageMetrics;
  state: WorkbenchState;
  parts?: MessagePart[];
  turnCommitted?: boolean;
  providerError?: {
    provider?: string;
    httpStatus?: number;
    type?: string;
    code?: string;
    message: string;
    requestId?: string;
    retryable: boolean;
  };
};

export type ChatTaskState = {
  taskId: string;
  startedAt: string;
	updatedAt?: string;
  projectId?: string;
  sessionId?: string;
  status: "running" | "waiting" | "retrying" | "failed" | "cancelled" | "completed" | string;
	message?: string;
	model?: string;
	content?: string;
	reasoning?: string;
	durationMs?: number;
	parts?: MessagePart[];
};

export type ChatAttachment = {
  kind?: "image" | "document" | string;
  name: string;
  mimeType: string;
  data: string;
  size?: number;
  characterCount?: number;
};

export type LiveUsageState = {
  usageMetrics: UsageMetrics;
  cacheHitRate: number;
  cacheHealth: CacheHealth;
  deepSeekSession: DeepSeekSessionState;
  cacheDiagnostics: string[];
  usageLedger: UsageLedgerState;
};

export type ChatTaskEvent = {
  taskId: string;
  projectId?: string;
  sessionId?: string;
  type: "started" | "status" | "heartbeat" | "context_compression" | "delta" | "reasoning" | "provider_notice" | "usage" | "usage_state" | "tool" | "subagent" | "completed" | "failed" | "cancelled" | string;
  delta?: string;
  message?: string;
  phase?: string;
  elapsedMs?: number;
  stageDurationMs?: number;
  model?: string;
  toolName?: string;
  toolCallId?: string;
  toolInput?: string;
  status?: "running" | "waiting" | "retrying" | "completed" | "failed" | "cancelled" | "error" | string;
  usage?: {
    promptTokens: number;
    completionTokens: number;
    totalTokens: number;
    promptCacheHitTokens: number;
    promptCacheMissTokens: number;
  };
  usageState?: LiveUsageState;
  progress?: Extract<MessagePart, { kind: "task_progress" }>;
  parts?: MessagePart[];
  compression?: {
    status: "running" | "completed" | "error" | string;
    beforeTokens: number;
    afterTokens?: number;
    removedMessages?: number;
    targetTokens: number;
  };
  team?: {
    runId: string;
    role: TeamRole;
    label: string;
    status: string;
    providerId?: string;
    model?: string;
    attempt: number;
    verdict?: string;
    summary?: string;
    error?: string;
    usage: UsageMetrics;
  };
  guidanceId?: string;
  guidance?: string;
  attachments?: ChatAttachment[];
  result?: ChatResult;
  forced?: boolean;
};

export type BrowserPreview = {
  path: string;
  name: string;
  url: string;
  ask?: boolean;
  tabId?: string;
  managed?: boolean;
};

export type BrowserTab = {
  id: string;
  title: string;
  url: string;
  loading: boolean;
  canGoBack: boolean;
  canGoForward: boolean;
  error?: string;
  viewportWidth: number;
  viewportHeight: number;
  dialog?: BrowserDialog;
};

export type BrowserDialog = {
  type: string;
  message: string;
  defaultValue?: string;
};

export type BrowserDownload = {
  id: string;
  url: string;
  filename: string;
  path?: string;
  state: string;
  receivedBytes: number;
  totalBytes: number;
  startedAt: string;
};

export type BrowserState = {
  available: boolean;
  running: boolean;
  engine: string;
  renderMode: "native" | "stream" | string;
  activeTabId: string;
  tabs: BrowserTab[];
  downloads: BrowserDownload[];
  lastError?: string;
  cdpEnabled: boolean;
};

export type BrowserElement = {
  index: number;
  selector: string;
  tag: string;
  role?: string;
  name?: string;
  text?: string;
  type?: string;
  placeholder?: string;
  href?: string;
  x: number;
  y: number;
  width: number;
  height: number;
};

export type BrowserFrame = {
  tab: BrowserTab;
  imageDataUrl: string;
  width: number;
  height: number;
  elements?: BrowserElement[];
  capturedAt: string;
};

export type BrowserSnapshot = {
  title: string;
  url: string;
  text: string;
  elements: BrowserElement[];
  capturedAt: string;
};

export type BrowserConsoleEntry = {
  level: string;
  message: string;
  timestamp: string;
};

export type BrowserNetworkEntry = {
  method: string;
  url: string;
  status: number;
  type: string;
  failed: boolean;
  error?: string;
  timestamp: string;
};

export type BrowserInspector = {
  snapshot: BrowserSnapshot;
  console: BrowserConsoleEntry[];
  network: BrowserNetworkEntry[];
};

// 前向兼容的消息片段模型：当前后端仅产出纯文本（走 text 片段），
// 但渲染管线已按类型分派，后续接入工具执行 / 文件编辑时可直接产出
// tool_call / diff 片段，无需改动前端渲染架构。
export type MessagePart =
  | { kind: "text"; text: string }
  | { kind: "diff"; path: string; patch: string; additions?: number; deletions?: number }
  | { kind: "file"; path: string; lineCount?: number; created?: boolean; fileAction?: "created" | "modified" | "available" | string }
  | {
      kind: "tool_call";
      name: string;
      status?: "running" | "waiting" | "retrying" | "ok" | "error";
      input?: string;
      output?: string;
	  stdout?: string;
	  stderr?: string;
      toolCallId?: string;
      workingDirectory?: string;
      exitCode?: number;
      startedAt?: string;
      completedAt?: string;
      durationMs?: number;
    }
  | {
      kind: "task_progress";
      steps: Array<{ title: string; status: "pending" | "in_progress" | "completed" | string }>;
      taskStatus?: "running" | "completed" | "failed" | "cancelled" | string;
      changedFiles?: number;
      additions?: number;
      deletions?: number;
    }
  | {
      kind: "web_search_results";
      query: string;
      sources: Array<{ title: string; url: string; snippet?: string }>;
    }
  | {
      kind: "team_role";
      role: TeamRole;
      roleLabel: string;
      providerId?: string;
      model?: string;
      status?: "running" | "completed" | "error" | string;
      summary?: string;
      verdict?: "approved" | "changes_required" | "unknown" | string;
      attempt?: number;
    }
  | {
      kind: "subagent";
      taskId: string;
      agentType: "explore" | "review" | "implement" | string;
      label: string;
      status?: "pending" | "running" | "completed" | "error" | "cancelled" | string;
      providerId?: string;
      model?: string;
      summary?: string;
      currentAction?: string;
	  subagentOutput?: string;
	  subagentReasoning?: string;
	  activities?: Array<{
		id: string;
		kind: "tool" | "status" | "provider" | string;
		title: string;
		status?: "running" | "completed" | "ok" | "error" | string;
		input?: string;
		output?: string;
		startedAt?: string;
		completedAt?: string;
		durationMs?: number;
	  }>;
      steps?: Array<{ title: string; status: "pending" | "in_progress" | "completed" | "error" | "cancelled" | string }>;
      changedFiles?: number;
      additions?: number;
      deletions?: number;
      startedAt?: string;
      completedAt?: string;
      durationMs?: number;
    }
  | {
      kind: "provider_notice";
      noticeKind: "model_reroute" | "safety_buffering" | "model_verification" | "moderation" | "policy_error" | string;
      severity?: "info" | "warning" | "error" | string;
      message?: string;
      requestedModel?: string;
      effectiveModel?: string;
      retryModel?: string;
      useCases?: string[];
      reasons?: string[];
      verifications?: string[];
      metadataKeys?: string[];
      requestId?: string;
      errorCode?: string;
      httpStatus?: number;
      retryable?: boolean;
	}
  | {
      kind: "timeline_note";
      message: string;
      status?: "running" | "waiting" | "retrying" | "completed" | "failed" | "cancelled" | "interrupted" | string;
	  toolCallId?: string;
      startedAt?: string;
      completedAt?: string;
      durationMs?: number;
	}
  | {
      kind: "secret_result";
      status?: "ok" | "error" | string;
      secretId: string;
      secretLabel: string;
      secretSource?: string;
    };

export type SecretResultReveal = {
  id: string;
  label: string;
  source?: string;
  value: string;
  createdAt: string;
};

// 事件日志的可回退检查点（对应后端 agent.CheckpointInfo）。
export type CheckpointInfo = {
  id: string;
  label: string;
  turnIndex: number;
  timestamp: string;
  preview: string;
};

// 对话线（分支）摘要（对应后端 agent.BranchInfo）。
export type BranchInfo = {
  leafId: string;
  label: string;
  turnCount: number;
  timestamp: string;
  isCurrent: boolean;
};

// 待审批请求（对应后端 agent.ApprovalRequest），用于代码审核弹窗。
export type ApprovalRequest = {
  id: string;
  tool: string;
  kind: "file" | "command" | string;
  path?: string;
  command?: string;
  url?: string;
  summary: string;
  parts?: MessagePart[];
};

// 项目摘要（对应后端 agent.ProjectInfo）。
export type ProjectInfo = {
  id: string;
  name: string;
  workspaceRoot: string;
  pinned: boolean;
  isActive: boolean;
  sessionCount: number;
};

// 会话摘要（对应后端 agent.SessionInfo）。
export type SessionInfo = {
  projectId: string;
  id: string;
  title: string;
  createdAt: string;
  updatedAt: string;
  archived: boolean;
  isActive: boolean;
};

// 会话历史消息（对应后端 agent.SessionMessage），用于恢复对话。
export type SessionMessage = {
  id: string;
  role: "user" | "assistant" | "system" | string;
  content: string;
  model?: string;
  createdAt: string;
  durationMs?: number;
  parts?: MessagePart[];
  attachments?: ChatAttachment[];
	status?: "failed" | "cancelled" | "interrupted" | string;
};

// 侧边栏树的项目节点（对应后端 agent.ProjectNode），含它的会话。
export type ProjectNode = {
  id: string;
  name: string;
  workspaceRoot: string;
  pinned: boolean;
  isActive: boolean;
  sessions: SessionInfo[];
};
