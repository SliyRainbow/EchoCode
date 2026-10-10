package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/plugins"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type RuntimeSettings struct {
	SchemaVersion          int                     `json:"schemaVersion"`
	SandboxMode            string                  `json:"sandboxMode"`
	FilesystemAccess       string                  `json:"filesystemAccess"`
	NetworkAccess          bool                    `json:"networkAccess"`
	ShellAccess            bool                    `json:"shellAccess"`
	ApprovalPolicy         string                  `json:"approvalPolicy"`
	WorkspaceRoot          string                  `json:"workspaceRoot"`
	ExtraWritableRoots     []string                `json:"extraWritableRoots"`
	ToolTimeoutSeconds     int                     `json:"toolTimeoutSeconds"`
	TaskIdleTimeoutSeconds int                     `json:"taskIdleTimeoutSeconds"`
	MaxConcurrentSubagents int                     `json:"maxConcurrentSubagents"`
	MaxCommandSeconds      int                     `json:"maxCommandSeconds"`
	MaxCommandMemoryMB     int                     `json:"maxCommandMemoryMb"`
	MaxCommandCPUPercent   int                     `json:"maxCommandCpuPercent"`
	MaxCommandProcesses    int                     `json:"maxCommandProcesses"`
	AllowDestructiveOps    bool                    `json:"allowDestructiveOps"`
	ToolResultPolicy       string                  `json:"toolResultPolicy"`
	StablePrefixPolicy     string                  `json:"stablePrefixPolicy"`
	CacheTargetPercent     float64                 `json:"cacheTargetPercent"`
	Git                    GitSettings             `json:"git"`
	Browser                BrowserSettings         `json:"browser"`
	ComputerControl        ComputerControlSettings `json:"computerControl"`
	MCP                    MCPSettings             `json:"mcp"`
	Plugins                plugins.Settings        `json:"plugins"`
	Model                  ModelSettings           `json:"model"`
	Team                   TeamSettings            `json:"team"`
	Skills                 SkillsSettings          `json:"skills"`
	Update                 UpdateSettings          `json:"update"`
	Workspace              WorkspaceSettings       `json:"workspace"`
	Memory                 MemorySettings          `json:"memory"`
}

const runtimeSettingsSchemaVersion = 14

type SkillsSettings struct {
	Disabled []string `json:"disabled"`
}

type GitSettings struct {
	BranchPrefix            string `json:"branchPrefix"`
	MergeMethod             string `json:"mergeMethod"`
	ShowPullRequestIcon     bool   `json:"showPullRequestIcon"`
	ForcePushWithLease      bool   `json:"forcePushWithLease"`
	DraftPullRequests       bool   `json:"draftPullRequests"`
	AutoDeleteOldWorktrees  bool   `json:"autoDeleteOldWorktrees"`
	WorktreeCleanupLimit    int    `json:"worktreeCleanupLimit"`
	CommitInstructions      string `json:"commitInstructions"`
	PullRequestInstructions string `json:"pullRequestInstructions"`
}

type MemorySettings struct {
	Enabled         bool `json:"enabled"`
	MaxSessions     int  `json:"maxSessions"`
	MaxCharacters   int  `json:"maxCharacters"`
	IncludeArchived bool `json:"includeArchived"`
}

type BrowserSettings struct {
	Enabled                    bool                    `json:"enabled"`
	DefaultLocalURLDestination string                  `json:"defaultLocalUrlDestination"`
	ClearDataPolicy            string                  `json:"clearDataPolicy"`
	ScreenshotAnnotations      string                  `json:"screenshotAnnotations"`
	PasswordManagerEnabled     bool                    `json:"passwordManagerEnabled"`
	AutofillContactEnabled     bool                    `json:"autofillContactEnabled"`
	AutofillProfile            BrowserAutofillProfile  `json:"autofillProfile"`
	Credentials                []BrowserCredential     `json:"credentials"`
	SitePermissions            []BrowserSitePermission `json:"sitePermissions"`
	DeveloperCDPAccess         bool                    `json:"developerCdpAccess"`
}

type BrowserCredential struct {
	ID                 string `json:"id"`
	Origin             string `json:"origin"`
	Username           string `json:"username"`
	PasswordConfigured bool   `json:"passwordConfigured"`
}

type BrowserAutofillProfile struct {
	FullName      string `json:"fullName"`
	Email         string `json:"email"`
	Phone         string `json:"phone"`
	Organization  string `json:"organization"`
	StreetAddress string `json:"streetAddress"`
	City          string `json:"city"`
	Region        string `json:"region"`
	PostalCode    string `json:"postalCode"`
	Country       string `json:"country"`
}

type BrowserSitePermission struct {
	Origin     string `json:"origin"`
	Camera     string `json:"camera"`
	Microphone string `json:"microphone"`
	Clipboard  string `json:"clipboard"`
}

type ComputerControlSettings struct {
	AnyAppEnabled     bool     `json:"anyAppEnabled"`
	ChromeEnabled     bool     `json:"chromeEnabled"`
	AlwaysAllowedApps []string `json:"alwaysAllowedApps"`
}

type MCPSettings struct {
	Servers []MCPServerSetting `json:"servers"`
}

type MCPServerSetting struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	Transport          string     `json:"transport"`
	Command            string     `json:"command"`
	Args               []string   `json:"args"`
	Env                []KeyValue `json:"env"`
	PassEnvironment    []string   `json:"passEnvironment"`
	WorkingDirectory   string     `json:"workingDirectory"`
	URL                string     `json:"url"`
	Headers            []KeyValue `json:"headers"`
	Enabled            bool       `json:"enabled"`
	ToolResultPolicy   string     `json:"toolResultPolicy"`
	SchemaSnapshotHash string     `json:"schemaSnapshotHash,omitempty"`
	LastSnapshotAt     string     `json:"lastSnapshotAt,omitempty"`
}

type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ModelSettings struct {
	SelectedProviderID string                 `json:"selectedProviderId"`
	SelectedModelID    string                 `json:"selectedModelId"`
	Providers          []ModelProviderSetting `json:"providers"`
}

type TeamSettings struct {
	Enabled         bool              `json:"enabled"`
	MaxReviewRounds int               `json:"maxReviewRounds"`
	Roles           []TeamRoleSetting `json:"roles"`
}

type UpdateSettings struct {
	AutoCheck    bool   `json:"autoCheck"`
	AutoDownload bool   `json:"autoDownload"`
	Channel      string `json:"channel"`
}

type TeamRoleSetting struct {
	Role       string `json:"role"`
	Enabled    bool   `json:"enabled"`
	ProviderID string `json:"providerId"`
	ModelID    string `json:"modelId"`
}

type ModelProviderSetting struct {
	ID                       string          `json:"id"`
	Name                     string          `json:"name"`
	Protocol                 string          `json:"protocol"`
	APIType                  string          `json:"apiType"`
	BaseURL                  string          `json:"baseUrl"`
	BalanceURL               string          `json:"balanceUrl"`
	ExtraHeaders             string          `json:"extraHeaders"`
	ExtraBodyJSON            string          `json:"extraBodyJson"`
	ReasoningProfile         string          `json:"reasoningProfile"`
	Enabled                  bool            `json:"enabled"`
	APIKeyConfigured         bool            `json:"apiKeyConfigured"`
	BillingKeyConfigured     bool            `json:"billingKeyConfigured"`
	BillingProjectID         string          `json:"billingProjectId,omitempty"`
	BillingAPIKeyID          string          `json:"billingApiKeyId,omitempty"`
	DefaultModelID           string          `json:"defaultModelId"`
	ContextWindowTokens      int             `json:"contextWindowTokens"`
	InputPricePerMillion     float64         `json:"inputPricePerMillion"`
	OutputPricePerMillion    float64         `json:"outputPricePerMillion"`
	CacheHitPricePerMillion  float64         `json:"cacheHitPricePerMillion"`
	CacheMissPricePerMillion float64         `json:"cacheMissPricePerMillion"`
	Models                   []ProviderModel `json:"models"`
	LastSyncStatus           string          `json:"lastSyncStatus"`
	LastSyncMessage          string          `json:"lastSyncMessage"`
	CheckedAt                string          `json:"checkedAt,omitempty"`
	SupportsModelFetch       bool            `json:"supportsModelFetch"`
}

type ProviderModel struct {
	ID                    string   `json:"id"`
	DisplayName           string   `json:"displayName"`
	Provider              string   `json:"provider"`
	ContextWindowTokens   int      `json:"contextWindowTokens"`
	ContextWindowSource   string   `json:"contextWindowSource,omitempty"`
	MaxOutputTokens       int      `json:"maxOutputTokens,omitempty"`
	ReasoningLevels       []string `json:"reasoningLevels,omitempty"`
	ThinkingModes         []string `json:"thinkingModes,omitempty"`
	UnsupportedParameters []string `json:"unsupportedParameters,omitempty"`
}

type WorkspaceSettings struct {
	Configured          bool   `json:"configured"`
	DependenciesEnabled bool   `json:"dependenciesEnabled"`
	LastDiagnosticAt    string `json:"lastDiagnosticAt,omitempty"`
	LastDiagnosticNote  string `json:"lastDiagnosticNote,omitempty"`
}

func DefaultRuntimeSettings() RuntimeSettings {
	workspaceRoot := defaultWorkspaceRoot()
	return RuntimeSettings{
		SchemaVersion:          runtimeSettingsSchemaVersion,
		SandboxMode:            "workspace-write",
		FilesystemAccess:       "workspace-write",
		NetworkAccess:          true,
		ShellAccess:            true,
		ApprovalPolicy:         "on-request",
		WorkspaceRoot:          workspaceRoot,
		ExtraWritableRoots:     []string{},
		ToolTimeoutSeconds:     180,
		TaskIdleTimeoutSeconds: 300,
		MaxConcurrentSubagents: defaultMaxConcurrentSubagents,
		MaxCommandSeconds:      120,
		MaxCommandMemoryMB:     4096,
		MaxCommandCPUPercent:   100,
		MaxCommandProcesses:    128,
		AllowDestructiveOps:    false,
		ToolResultPolicy:       "summary-first",
		StablePrefixPolicy:     "strict-stable-prefix",
		CacheTargetPercent:     96,
		Git: GitSettings{
			BranchPrefix:           "echocode/",
			MergeMethod:            "merge",
			ShowPullRequestIcon:    true,
			ForcePushWithLease:     false,
			DraftPullRequests:      true,
			AutoDeleteOldWorktrees: true,
			WorktreeCleanupLimit:   15,
		},
		Memory: MemorySettings{
			Enabled:         true,
			MaxSessions:     12,
			MaxCharacters:   6000,
			IncludeArchived: true,
		},
		Browser: BrowserSettings{
			Enabled:                    true,
			DefaultLocalURLDestination: "echocode",
			ClearDataPolicy:            "ask",
			ScreenshotAnnotations:      "never",
			PasswordManagerEnabled:     false,
			AutofillContactEnabled:     false,
			SitePermissions:            []BrowserSitePermission{},
			DeveloperCDPAccess:         false,
		},
		ComputerControl: ComputerControlSettings{
			AnyAppEnabled:     false,
			ChromeEnabled:     false,
			AlwaysAllowedApps: []string{},
		},
		MCP: MCPSettings{
			Servers: []MCPServerSetting{
				{
					ID:               "filesystem",
					Name:             "filesystem",
					Transport:        "builtin",
					Command:          "builtin:filesystem",
					Args:             []string{},
					Env:              []KeyValue{},
					PassEnvironment:  []string{},
					Enabled:          true,
					ToolResultPolicy: "summary-first",
				},
			},
		},
		Plugins: plugins.DefaultSettings(),
		Model: ModelSettings{
			SelectedProviderID: "deepseek",
			SelectedModelID:    "",
			Providers: []ModelProviderSetting{
				{
					ID:                 "deepseek",
					Name:               "DeepSeek 官方",
					Protocol:           "deepseek-official",
					APIType:            "chat-completions",
					BaseURL:            "https://api.deepseek.com",
					ReasoningProfile:   "auto",
					Enabled:            true,
					LastSyncStatus:     "idle",
					LastSyncMessage:    "等待保存 API Key 后刷新模型。",
					SupportsModelFetch: true,
				},
				{
					ID:                 "openai-compatible",
					Name:               "OpenAI 兼容",
					Protocol:           "openai-compatible",
					APIType:            "chat-completions",
					BaseURL:            "https://api.openai.com/v1",
					ReasoningProfile:   "auto",
					Enabled:            false,
					LastSyncStatus:     "idle",
					LastSyncMessage:    "填写 Base URL 与 API Key 后可自动获取模型。",
					SupportsModelFetch: true,
				},
				{
					ID:                 "local-openai",
					Name:               "本地 OpenAI 兼容",
					Protocol:           "openai-compatible",
					APIType:            "chat-completions",
					BaseURL:            "http://127.0.0.1:11434/v1",
					ReasoningProfile:   "none",
					Enabled:            false,
					LastSyncStatus:     "idle",
					LastSyncMessage:    "适用于 Ollama、LM Studio 等本地兼容服务。",
					SupportsModelFetch: true,
				},
			},
		},
		Team: TeamSettings{
			Enabled:         false,
			MaxReviewRounds: 1,
			Roles: []TeamRoleSetting{
				{Role: TeamRolePlanner, Enabled: true},
				{Role: TeamRoleImplementer, Enabled: true},
				{Role: TeamRoleTester, Enabled: true},
				{Role: TeamRoleReviewer, Enabled: true},
				{Role: TeamRoleSynthesizer, Enabled: true},
			},
		},
		Skills: SkillsSettings{
			Disabled: []string{},
		},
		Update: UpdateSettings{
			AutoCheck:    true,
			AutoDownload: false,
			Channel:      "stable",
		},
		Workspace: WorkspaceSettings{
			Configured:          true,
			DependenciesEnabled: true,
		},
	}
}

func defaultWorkspaceRoot() string {
	cwd, _ := os.Getwd()
	executable, _ := os.Executable()
	home, _ := os.UserHomeDir()
	return resolveDefaultWorkspaceRoot(cwd, executable, home)
}

func resolveDefaultWorkspaceRoot(cwd, executable, home string) string {
	cwd = strings.TrimSpace(cwd)
	executable = strings.TrimSpace(executable)
	home = strings.TrimSpace(home)
	if cwd == "" {
		cwd = home
	}
	if cwd == "" {
		return "."
	}

	executableDir := ""
	if executable != "" {
		executableDir = filepath.Dir(executable)
	}
	if executableDir != "" && sameWorkspacePath(cwd, executableDir) {
		for candidate, depth := filepath.Dir(executableDir), 0; depth < 4; candidate, depth = filepath.Dir(candidate), depth+1 {
			if hasWorkspaceMarker(candidate) {
				return candidate
			}
			if filepath.Dir(candidate) == candidate {
				break
			}
		}
		if home != "" {
			return home
		}
	}

	if windowsDir := strings.TrimSpace(os.Getenv("WINDIR")); windowsDir != "" && sameWorkspacePath(cwd, filepath.Join(windowsDir, "System32")) && home != "" {
		return home
	}
	return cwd
}

func hasWorkspaceMarker(path string) bool {
	for _, marker := range []string{".git", "go.mod", "package.json", "wails.json", "Cargo.toml", "pyproject.toml"} {
		if _, err := os.Stat(filepath.Join(path, marker)); err == nil {
			return true
		}
	}
	return false
}

func sameWorkspacePath(left, right string) bool {
	left = filepath.Clean(left)
	right = filepath.Clean(right)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func (settings RuntimeSettings) Normalized() RuntimeSettings {
	defaults := DefaultRuntimeSettings()
	settings.SchemaVersion = runtimeSettingsSchemaVersion

	settings.SandboxMode = normalizeChoice(settings.SandboxMode, defaults.SandboxMode, map[string]bool{
		"read-only":          true,
		"workspace-write":    true,
		"danger-full-access": true,
	})
	settings.FilesystemAccess = normalizeChoice(settings.FilesystemAccess, defaults.FilesystemAccess, map[string]bool{
		"read-only":       true,
		"workspace-write": true,
		"unrestricted":    true,
	})
	settings.ApprovalPolicy = normalizeChoice(settings.ApprovalPolicy, defaults.ApprovalPolicy, map[string]bool{
		"always":     true,
		"never":      true,
		"on-request": true,
		"on-failure": true,
		"untrusted":  true,
	})
	// Older labels shared one conservative implementation. Preserve its actual
	// behaviour under an explicit name rather than promise unsupported policies.
	if settings.ApprovalPolicy == "on-failure" || settings.ApprovalPolicy == "untrusted" {
		settings.ApprovalPolicy = "always"
	}
	settings.ToolResultPolicy = normalizeChoice(settings.ToolResultPolicy, defaults.ToolResultPolicy, map[string]bool{
		"summary-first": true,
		"balanced":      true,
		"raw-local":     true,
	})
	settings.StablePrefixPolicy = normalizeChoice(settings.StablePrefixPolicy, defaults.StablePrefixPolicy, map[string]bool{
		"reuse-prefix":         true,
		"stable-prefix":        true,
		"strict-stable-prefix": true,
	})

	settings.WorkspaceRoot = strings.TrimSpace(settings.WorkspaceRoot)
	if settings.WorkspaceRoot == "" {
		settings.WorkspaceRoot = defaults.WorkspaceRoot
	}
	settings.ExtraWritableRoots = cleanStringList(settings.ExtraWritableRoots)
	if settings.ToolTimeoutSeconds <= 0 {
		settings.ToolTimeoutSeconds = defaults.ToolTimeoutSeconds
	}
	if settings.ToolTimeoutSeconds < 5 {
		settings.ToolTimeoutSeconds = 5
	}
	if settings.ToolTimeoutSeconds > 3600 {
		settings.ToolTimeoutSeconds = 3600
	}
	if settings.TaskIdleTimeoutSeconds <= 0 {
		settings.TaskIdleTimeoutSeconds = defaults.TaskIdleTimeoutSeconds
	}
	if settings.TaskIdleTimeoutSeconds < 15 {
		settings.TaskIdleTimeoutSeconds = 15
	}
	if settings.TaskIdleTimeoutSeconds > 7200 {
		settings.TaskIdleTimeoutSeconds = 7200
	}
	settings.MaxConcurrentSubagents = normalizeSubagentConcurrencyLimit(settings.MaxConcurrentSubagents)
	if settings.MaxCommandSeconds < 5 {
		settings.MaxCommandSeconds = 5
	}
	if settings.MaxCommandSeconds > 3600 {
		settings.MaxCommandSeconds = 3600
	}
	if settings.MaxCommandMemoryMB < 256 {
		settings.MaxCommandMemoryMB = defaults.MaxCommandMemoryMB
	}
	if settings.MaxCommandMemoryMB > 65536 {
		settings.MaxCommandMemoryMB = 65536
	}
	if settings.MaxCommandCPUPercent < 10 {
		settings.MaxCommandCPUPercent = defaults.MaxCommandCPUPercent
	}
	if settings.MaxCommandCPUPercent > 100 {
		settings.MaxCommandCPUPercent = 100
	}
	if settings.MaxCommandProcesses < 4 {
		settings.MaxCommandProcesses = defaults.MaxCommandProcesses
	}
	if settings.MaxCommandProcesses > 1024 {
		settings.MaxCommandProcesses = 1024
	}
	if settings.CacheTargetPercent < 0 {
		settings.CacheTargetPercent = 0
	}
	if settings.CacheTargetPercent > 100 {
		settings.CacheTargetPercent = 100
	}
	settings.Git = normalizeGitSettings(settings.Git, defaults.Git)
	settings.Memory = normalizeMemorySettings(settings.Memory, defaults.Memory)
	settings.Browser = normalizeBrowserSettings(settings.Browser, defaults.Browser)
	settings.ComputerControl = normalizeComputerControlSettings(settings.ComputerControl, defaults.ComputerControl)
	settings.MCP = normalizeMCPSettings(settings.MCP, defaults.MCP)
	settings.Plugins = plugins.NormalizeSettings(settings.Plugins)
	settings.Model = normalizeModelSettings(settings.Model, defaults.Model)
	settings.Team = normalizeTeamSettings(settings.Team, defaults.Team, settings.Model)
	settings.Skills = normalizeSkillsSettings(settings.Skills, defaults.Skills)
	settings.Update = normalizeUpdateSettings(settings.Update, defaults.Update)
	settings.Workspace = normalizeWorkspaceSettings(settings.Workspace, defaults.Workspace)
	return settings
}

func normalizeSkillsSettings(settings, defaults SkillsSettings) SkillsSettings {
	settings.Disabled = cleanStringList(settings.Disabled)
	if settings.Disabled == nil {
		settings.Disabled = cleanStringList(defaults.Disabled)
	}
	sort.Strings(settings.Disabled)
	return settings
}

func normalizeUpdateSettings(settings, defaults UpdateSettings) UpdateSettings {
	settings.Channel = normalizeChoice(settings.Channel, defaults.Channel, map[string]bool{"stable": true})
	return settings
}

func (settings RuntimeSettings) Validate() error {
	settings = settings.Normalized()
	if settings.SandboxMode == "read-only" && settings.FilesystemAccess != "read-only" {
		return errors.New("只读沙箱下文件权限必须为只读")
	}
	if settings.SandboxMode == "danger-full-access" && settings.ApprovalPolicy == "never" && settings.AllowDestructiveOps {
		return errors.New("危险全权限 + 永不审批时不能开启破坏性操作")
	}
	return nil
}

func loadRuntimeSettings(path string) (RuntimeSettings, bool) {
	settings, loaded, _ := loadRuntimeSettingsWithError(path)
	return settings, loaded
}

func loadRuntimeSettingsWithError(path string) (RuntimeSettings, bool, error) {
	settings := DefaultRuntimeSettings()
	if strings.TrimSpace(path) == "" {
		return settings, false, nil
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return settings, false, nil
	}
	if err != nil {
		return settings, false, fmt.Errorf("读取运行设置 %s 失败，原文件已保留，请修复后重启: %w", path, err)
	}
	var stored RuntimeSettings
	if err := json.Unmarshal(data, &stored); err != nil {
		return settings, false, fmt.Errorf("解析运行设置 %s 失败，原文件已保留，请修复后重启: %w", path, err)
	}
	if stored.SchemaVersion < 2 && stored.ApprovalPolicy == "never" && !stored.AllowDestructiveOps {
		stored.ApprovalPolicy = "on-request"
	}
	stored = migrateRuntimeSettings(stored)
	return stored.Normalized(), true, nil
}

func migrateRuntimeSettings(stored RuntimeSettings) RuntimeSettings {
	if stored.SchemaVersion < 14 && stored.ApprovalPolicy != "never" {
		stored.ApprovalPolicy = "always"
	}
	if stored.SchemaVersion < 9 {
		stored.Plugins = plugins.DefaultSettings()
	}
	if stored.SchemaVersion < 8 {
		stored.Skills = DefaultRuntimeSettings().Skills
	}
	if stored.SchemaVersion < 7 {
		stored.Update = DefaultRuntimeSettings().Update
	}
	if stored.SchemaVersion < 12 {
		for providerIndex := range stored.Model.Providers {
			provider := &stored.Model.Providers[providerIndex]
			for modelIndex := range provider.Models {
				model := &provider.Models[modelIndex]
				if !unverifiedAnthropicCatalogContext(model.ID) || model.ContextWindowTokens != 1_000_000 ||
					normalizeContextWindowSource(model.ContextWindowSource) != ContextWindowSourceCatalog {
					continue
				}
				model.ContextWindowTokens = 0
				model.ContextWindowSource = ""
			}
		}
	}
	if stored.SchemaVersion >= 6 {
		return stored
	}
	for providerIndex := range stored.Model.Providers {
		provider := &stored.Model.Providers[providerIndex]
		for modelIndex := range provider.Models {
			model := &provider.Models[modelIndex]
			id := strings.ToLower(strings.TrimSpace(model.ID))
			if !strings.HasPrefix(id, "grok-") {
				continue
			}
			expected, known := exactModelContextWindows[id]
			if !known || expected == 1_000_000 || model.ContextWindowTokens != 1_000_000 {
				continue
			}
			source := normalizeContextWindowSource(model.ContextWindowSource)
			if source == ContextWindowSourceUpstream {
				continue
			}
			// Intermediate builds could persist the old broad xAI 1M guess under
			// several inferred/manual sources. Clear only that known stale shape.
			model.ContextWindowTokens = 0
			model.ContextWindowSource = ""
		}
	}
	return stored
}

func saveRuntimeSettings(path string, settings RuntimeSettings) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(settings.Normalized(), "", "  ")
	if err != nil {
		return err
	}
	return tools.WriteBytesAtomic(path, data, 0o600)
}

func normalizeChoice(value string, fallback string, allowed map[string]bool) string {
	value = strings.TrimSpace(value)
	if allowed[value] {
		return value
	}
	return fallback
}

func cleanStringList(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func cleanOrderedStrings(values []string) []string {
	cleaned := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			cleaned = append(cleaned, value)
		}
	}
	return cleaned
}

func normalizeGitSettings(settings GitSettings, defaults GitSettings) GitSettings {
	if settings == (GitSettings{}) {
		return defaults
	}
	settings.BranchPrefix = strings.TrimSpace(settings.BranchPrefix)
	if settings.BranchPrefix == "" {
		settings.BranchPrefix = defaults.BranchPrefix
	}
	settings.MergeMethod = normalizeChoice(settings.MergeMethod, defaults.MergeMethod, map[string]bool{
		"merge":  true,
		"squash": true,
		"rebase": true,
	})
	if settings.WorktreeCleanupLimit < 1 {
		settings.WorktreeCleanupLimit = defaults.WorktreeCleanupLimit
	}
	if settings.WorktreeCleanupLimit > 99 {
		settings.WorktreeCleanupLimit = 99
	}
	settings.CommitInstructions = strings.TrimSpace(settings.CommitInstructions)
	settings.PullRequestInstructions = strings.TrimSpace(settings.PullRequestInstructions)
	return settings
}

func normalizeMemorySettings(settings MemorySettings, defaults MemorySettings) MemorySettings {
	if settings == (MemorySettings{}) {
		return defaults
	}
	if settings.MaxSessions < 1 {
		settings.MaxSessions = defaults.MaxSessions
	}
	if settings.MaxSessions > 100 {
		settings.MaxSessions = 100
	}
	if settings.MaxCharacters < 1000 {
		settings.MaxCharacters = 1000
	}
	if settings.MaxCharacters > 20000 {
		settings.MaxCharacters = 20000
	}
	return settings
}

func normalizeBrowserSettings(settings BrowserSettings, defaults BrowserSettings) BrowserSettings {
	if isZeroBrowserSettings(settings) {
		return defaults
	}
	settings.DefaultLocalURLDestination = normalizeChoice(settings.DefaultLocalURLDestination, defaults.DefaultLocalURLDestination, map[string]bool{
		"echocode": true,
		"system":   true,
		"ask":      true,
		"browser":  true,
	})
	settings.ClearDataPolicy = normalizeChoice(settings.ClearDataPolicy, defaults.ClearDataPolicy, map[string]bool{
		"ask":     true,
		"session": true,
		"all":     true,
		"never":   true,
	})
	settings.ScreenshotAnnotations = normalizeChoice(settings.ScreenshotAnnotations, defaults.ScreenshotAnnotations, map[string]bool{
		"always": true,
		"ask":    true,
		"never":  true,
	})
	settings.AutofillProfile = normalizeBrowserAutofillProfile(settings.AutofillProfile)
	credentials := make([]BrowserCredential, 0, len(settings.Credentials))
	credentialIDs := map[string]bool{}
	for _, credential := range settings.Credentials {
		credential.ID = strings.TrimSpace(credential.ID)
		credential.Origin = strings.TrimSpace(credential.Origin)
		credential.Username = strings.TrimSpace(credential.Username)
		if credential.ID == "" || credential.Origin == "" || credential.Username == "" || credentialIDs[credential.ID] {
			continue
		}
		credentialIDs[credential.ID] = true
		credentials = append(credentials, credential)
	}
	settings.Credentials = credentials
	cleaned := make([]BrowserSitePermission, 0, len(settings.SitePermissions))
	seen := map[string]bool{}
	for _, permission := range settings.SitePermissions {
		permission.Origin = strings.TrimSpace(permission.Origin)
		if permission.Origin == "" || seen[permission.Origin] {
			continue
		}
		seen[permission.Origin] = true
		permission.Camera = normalizeChoice(permission.Camera, "ask", permissionChoices())
		permission.Microphone = normalizeChoice(permission.Microphone, "ask", permissionChoices())
		permission.Clipboard = normalizeChoice(permission.Clipboard, "ask", permissionChoices())
		cleaned = append(cleaned, permission)
	}
	settings.SitePermissions = cleaned
	return settings
}

func normalizeBrowserAutofillProfile(profile BrowserAutofillProfile) BrowserAutofillProfile {
	profile.FullName = strings.TrimSpace(profile.FullName)
	profile.Email = strings.TrimSpace(profile.Email)
	profile.Phone = strings.TrimSpace(profile.Phone)
	profile.Organization = strings.TrimSpace(profile.Organization)
	profile.StreetAddress = strings.TrimSpace(profile.StreetAddress)
	profile.City = strings.TrimSpace(profile.City)
	profile.Region = strings.TrimSpace(profile.Region)
	profile.PostalCode = strings.TrimSpace(profile.PostalCode)
	profile.Country = strings.TrimSpace(profile.Country)
	return profile
}

func permissionChoices() map[string]bool {
	return map[string]bool{
		"ask":   true,
		"allow": true,
		"block": true,
	}
}

func normalizeComputerControlSettings(settings ComputerControlSettings, defaults ComputerControlSettings) ComputerControlSettings {
	settings.AlwaysAllowedApps = cleanStringList(settings.AlwaysAllowedApps)
	if settings.AlwaysAllowedApps == nil {
		settings.AlwaysAllowedApps = defaults.AlwaysAllowedApps
	}
	return settings
}

func normalizeMCPSettings(settings MCPSettings, defaults MCPSettings) MCPSettings {
	if len(settings.Servers) == 0 {
		settings.Servers = defaults.Servers
	}
	cleaned := make([]MCPServerSetting, 0, len(settings.Servers))
	seen := map[string]bool{}
	for _, server := range settings.Servers {
		server.ID = stableID(server.ID, server.Name, server.Command)
		if server.ID == "" || seen[server.ID] {
			continue
		}
		seen[server.ID] = true
		server.Name = strings.TrimSpace(server.Name)
		if server.Name == "" {
			server.Name = server.ID
		}
		server.Command = strings.TrimSpace(server.Command)
		server.WorkingDirectory = strings.TrimSpace(server.WorkingDirectory)
		server.URL = strings.TrimSpace(server.URL)
		server.Args = cleanOrderedStrings(server.Args)
		server.PassEnvironment = cleanStringList(server.PassEnvironment)
		server.Env = cleanKeyValues(server.Env)
		server.Headers = cleanKeyValues(server.Headers)
		if strings.HasPrefix(server.Command, "builtin:") {
			server.Transport = "builtin"
		} else if strings.TrimSpace(server.Transport) == "" && server.URL != "" {
			server.Transport = "streamable-http"
		}
		server.Transport = normalizeChoice(server.Transport, "stdio", map[string]bool{
			"builtin":         true,
			"stdio":           true,
			"streamable-http": true,
			"sse":             true,
		})
		server.ToolResultPolicy = normalizeChoice(server.ToolResultPolicy, defaults.Servers[0].ToolResultPolicy, map[string]bool{
			"summary-first": true,
			"balanced":      true,
			"raw-local":     true,
		})
		cleaned = append(cleaned, server)
	}
	settings.Servers = cleaned
	return settings
}

func normalizeModelSettings(settings ModelSettings, _ ModelSettings) ModelSettings {
	settings.SelectedProviderID = strings.TrimSpace(settings.SelectedProviderID)
	settings.SelectedModelID = strings.TrimSpace(settings.SelectedModelID)
	cleaned := make([]ModelProviderSetting, 0, len(settings.Providers))
	seen := map[string]bool{}
	for _, provider := range settings.Providers {
		provider.ID = stableID(provider.ID, provider.Name, provider.Protocol)
		if provider.ID == "" || seen[provider.ID] {
			continue
		}
		seen[provider.ID] = true
		provider.Name = strings.TrimSpace(provider.Name)
		if provider.Name == "" {
			provider.Name = provider.ID
		}
		provider.Protocol = normalizeChoice(provider.Protocol, "openai-compatible", map[string]bool{
			"deepseek-official":    true,
			"openai-compatible":    true,
			"anthropic":            true,
			"anthropic-compatible": true,
			"gemini":               true,
			"local":                true,
		})
		provider.APIType = normalizeAPIType(provider.APIType, provider.Protocol)
		provider.BaseURL = strings.TrimRight(strings.TrimSpace(provider.BaseURL), "/")
		if provider.BaseURL == "" {
			provider.BaseURL = defaultBaseURLForProtocol(provider.Protocol)
		}
		provider.BalanceURL = strings.TrimSpace(provider.BalanceURL)
		provider.BillingProjectID = strings.TrimSpace(provider.BillingProjectID)
		provider.BillingAPIKeyID = strings.TrimSpace(provider.BillingAPIKeyID)
		provider.ExtraHeaders = strings.TrimSpace(provider.ExtraHeaders)
		provider.ExtraBodyJSON = strings.TrimSpace(provider.ExtraBodyJSON)
		provider.ReasoningProfile = normalizeReasoningProfile(provider.ReasoningProfile, provider.Protocol)
		provider.DefaultModelID = strings.TrimSpace(provider.DefaultModelID)
		if provider.ContextWindowTokens < 0 {
			provider.ContextWindowTokens = 0
		}
		provider.InputPricePerMillion = normalizeTokenPrice(provider.InputPricePerMillion)
		provider.OutputPricePerMillion = normalizeTokenPrice(provider.OutputPricePerMillion)
		provider.CacheHitPricePerMillion = normalizeTokenPrice(provider.CacheHitPricePerMillion)
		provider.CacheMissPricePerMillion = normalizeTokenPrice(provider.CacheMissPricePerMillion)
		provider.LastSyncStatus = normalizeChoice(provider.LastSyncStatus, "idle", map[string]bool{
			"idle":  true,
			"ok":    true,
			"error": true,
		})
		provider.LastSyncMessage = strings.TrimSpace(provider.LastSyncMessage)
		provider.Models = normalizeProviderModels(provider.Models, provider.ID)
		if len(provider.Models) > 0 {
			provider.Models = providerModelsFromProtocolModels(resolveProviderModelContexts(provider, providerProtocolModels(provider.Models)))
		}
		provider.SupportsModelFetch = supportsModelFetch(provider.Protocol)
		cleaned = append(cleaned, provider)
	}
	settings.Providers = cleaned
	if len(settings.Providers) == 0 {
		settings.SelectedProviderID = ""
		settings.SelectedModelID = ""
		return settings
	}
	if settings.SelectedProviderID == "" || !hasProvider(settings.Providers, settings.SelectedProviderID) {
		settings.SelectedProviderID = settings.Providers[0].ID
	}
	for _, provider := range settings.Providers {
		if provider.ID != settings.SelectedProviderID || len(provider.Models) == 0 {
			continue
		}
		if !hasProviderModel(provider.Models, settings.SelectedModelID) {
			settings.SelectedModelID = provider.DefaultModelID
			if settings.SelectedModelID == "" || !hasProviderModel(provider.Models, settings.SelectedModelID) {
				settings.SelectedModelID = provider.Models[0].ID
			}
		}
		break
	}
	return settings
}

func normalizeReasoningProfile(profile, providerProtocol string) string {
	profile = strings.ToLower(strings.TrimSpace(profile))
	if profile == "" {
		profile = "auto"
	}
	allowed := map[string]bool{"auto": true, "none": true}
	switch providerProtocol {
	case "deepseek-official":
		allowed["deepseek"] = true
	case "anthropic", "anthropic-compatible":
		allowed["anthropic"] = true
	case "gemini":
		allowed["gemini"] = true
	case "openai-compatible", "local":
		allowed["openai"] = true
		allowed["openai-effort"] = true
		allowed["xai"] = true
		allowed["deepseek"] = true
	}
	if allowed[profile] {
		return profile
	}
	return "auto"
}

func normalizeTokenPrice(value float64) float64 {
	if value < 0 {
		return 0
	}
	if value > 1_000_000 {
		return 1_000_000
	}
	return value
}

func normalizeTeamSettings(settings TeamSettings, defaults TeamSettings, models ModelSettings) TeamSettings {
	if len(settings.Roles) == 0 {
		enabled := settings.Enabled
		settings = defaults
		settings.Enabled = enabled
	}
	if settings.MaxReviewRounds < 0 {
		settings.MaxReviewRounds = 0
	}
	if settings.MaxReviewRounds > 2 {
		settings.MaxReviewRounds = 2
	}

	configured := make(map[string]TeamRoleSetting, len(settings.Roles))
	for _, role := range settings.Roles {
		role.Role = strings.ToLower(strings.TrimSpace(role.Role))
		if !isTeamRole(role.Role) {
			continue
		}
		role.ProviderID = strings.TrimSpace(role.ProviderID)
		role.ModelID = strings.TrimSpace(role.ModelID)
		configured[role.Role] = role
	}

	roles := make([]TeamRoleSetting, 0, len(defaults.Roles))
	for _, fallback := range defaults.Roles {
		role, ok := configured[fallback.Role]
		if !ok {
			role = fallback
		}
		if role.Role == TeamRoleImplementer || role.Role == TeamRoleSynthesizer {
			role.Enabled = true
		}
		if role.ProviderID == "" {
			role.ModelID = ""
		} else {
			provider, _, found := findModelProvider(models.Providers, role.ProviderID)
			if !found {
				role.ProviderID = ""
				role.ModelID = ""
			} else if role.ModelID != "" && len(provider.Models) > 0 && !hasProviderModel(provider.Models, role.ModelID) {
				role.ModelID = ""
			}
		}
		roles = append(roles, role)
	}
	settings.Roles = roles
	return settings
}

func normalizeWorkspaceSettings(settings WorkspaceSettings, defaults WorkspaceSettings) WorkspaceSettings {
	if !settings.Configured {
		return defaults
	}
	settings.Configured = true
	settings.LastDiagnosticAt = strings.TrimSpace(settings.LastDiagnosticAt)
	settings.LastDiagnosticNote = strings.TrimSpace(settings.LastDiagnosticNote)
	return settings
}

func cleanKeyValues(values []KeyValue) []KeyValue {
	cleaned := make([]KeyValue, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value.Key = strings.TrimSpace(value.Key)
		if value.Key == "" || seen[value.Key] {
			continue
		}
		seen[value.Key] = true
		value.Value = strings.TrimSpace(value.Value)
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func stableID(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func defaultBaseURLForProtocol(protocol string) string {
	switch protocol {
	case "deepseek-official":
		return "https://api.deepseek.com"
	case "openai-compatible":
		return "https://api.openai.com/v1"
	case "anthropic":
		return "https://api.anthropic.com"
	case "anthropic-compatible":
		return "https://api.anthropic.com"
	case "gemini":
		return "https://generativelanguage.googleapis.com/v1beta"
	case "local":
		return "http://127.0.0.1:11434/v1"
	default:
		return ""
	}
}

func normalizeAPIType(apiType string, protocol string) string {
	return normalizeChoice(apiType, defaultAPITypeForProtocol(protocol), map[string]bool{
		"chat-completions":        true,
		"responses":               true,
		"anthropic-messages":      true,
		"gemini-generate-content": true,
	})
}

func defaultAPITypeForProtocol(protocol string) string {
	switch protocol {
	case "anthropic", "anthropic-compatible":
		return "anthropic-messages"
	case "gemini":
		return "gemini-generate-content"
	default:
		return "chat-completions"
	}
}

func supportsModelFetch(protocol string) bool {
	return protocol == "deepseek-official" ||
		protocol == "openai-compatible" ||
		protocol == "anthropic" ||
		protocol == "anthropic-compatible" ||
		protocol == "gemini" ||
		protocol == "local"
}

func normalizeProviderModels(models []ProviderModel, providerID string) []ProviderModel {
	cleaned := make([]ProviderModel, 0, len(models))
	seen := map[string]bool{}
	for _, model := range models {
		model.ID = strings.TrimSpace(model.ID)
		if model.ID == "" || seen[model.ID] {
			continue
		}
		seen[model.ID] = true
		model.DisplayName = strings.TrimSpace(model.DisplayName)
		if model.DisplayName == "" {
			model.DisplayName = model.ID
		}
		model.Provider = strings.TrimSpace(model.Provider)
		if model.Provider == "" {
			model.Provider = providerID
		}
		if model.ContextWindowTokens < 0 {
			model.ContextWindowTokens = 0
		}
		if model.MaxOutputTokens < 0 {
			model.MaxOutputTokens = 0
		}
		model.ReasoningLevels = normalizeProviderReasoningLevels(model.ReasoningLevels)
		model.ThinkingModes = normalizeProviderThinkingModes(model.ThinkingModes)
		model.UnsupportedParameters = normalizeProviderUnsupportedParameters(model.UnsupportedParameters)
		model.ContextWindowSource = normalizeContextWindowSource(model.ContextWindowSource)
		if model.ContextWindowTokens == 0 {
			model.ContextWindowSource = ""
		} else if model.ContextWindowSource == "" {
			// Values stored before source tracking existed were user-editable values.
			model.ContextWindowSource = ContextWindowSourceManual
		}
		cleaned = append(cleaned, model)
	}
	return cleaned
}

func normalizeProviderReasoningLevels(levels []string) []string {
	allowed := map[string]bool{"none": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true}
	return normalizeUniqueValues(levels, allowed)
}

func normalizeProviderThinkingModes(modes []string) []string {
	allowed := map[string]bool{"adaptive": true, "enabled": true, "disabled": true}
	return normalizeUniqueValues(modes, allowed)
}

func normalizeProviderUnsupportedParameters(parameters []string) []string {
	allowed := map[string]bool{"temperature": true, "thinking": true, "output_config": true}
	return normalizeUniqueValues(parameters, allowed)
}

func normalizeUniqueValues(values []string, allowed map[string]bool) []string {
	result := make([]string, 0, len(values))
	seen := map[string]bool{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if !allowed[value] || seen[value] {
			continue
		}
		seen[value] = true
		result = append(result, value)
	}
	return result
}

func normalizeContextWindowSource(source string) string {
	source = strings.TrimSpace(strings.ToLower(source))
	switch source {
	case ContextWindowSourceUpstream,
		ContextWindowSourceCatalog,
		ContextWindowSourceProtocol,
		ContextWindowSourceProvider,
		ContextWindowSourceManual,
		ContextWindowSourceFallback:
		return source
	default:
		return ""
	}
}

func hasProvider(providers []ModelProviderSetting, id string) bool {
	for _, provider := range providers {
		if provider.ID == id {
			return true
		}
	}
	return false
}

func hasProviderModel(models []ProviderModel, id string) bool {
	id = strings.TrimSpace(id)
	for _, model := range models {
		if model.ID == id {
			return true
		}
	}
	return false
}

func isZeroBrowserSettings(settings BrowserSettings) bool {
	return !settings.Enabled &&
		settings.DefaultLocalURLDestination == "" &&
		settings.ClearDataPolicy == "" &&
		settings.ScreenshotAnnotations == "" &&
		!settings.PasswordManagerEnabled &&
		!settings.AutofillContactEnabled &&
		settings.AutofillProfile == (BrowserAutofillProfile{}) &&
		len(settings.Credentials) == 0 &&
		len(settings.SitePermissions) == 0 &&
		!settings.DeveloperCDPAccess
}
