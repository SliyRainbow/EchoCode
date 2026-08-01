package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/plugins"
)

func TestResolveDefaultWorkspaceRootUsesRepositoryAboveBuildBin(t *testing.T) {
	repo := t.TempDir()
	if err := os.WriteFile(filepath.Join(repo, "go.mod"), []byte("module example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(repo, "build", "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	got := resolveDefaultWorkspaceRoot(bin, filepath.Join(bin, "EchoCode.exe"), filepath.Join(repo, "home"))
	if !sameWorkspacePath(got, repo) {
		t.Fatalf("default workspace = %q, want repository %q", got, repo)
	}
}

func TestResolveDefaultWorkspaceRootUsesHomeForInstalledExecutable(t *testing.T) {
	install := filepath.Join(t.TempDir(), "EchoCode")
	if err := os.MkdirAll(install, 0o755); err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	got := resolveDefaultWorkspaceRoot(install, filepath.Join(install, "EchoCode.exe"), home)
	if !sameWorkspacePath(got, home) {
		t.Fatalf("default workspace = %q, want home %q", got, home)
	}
}

func TestResolveDefaultWorkspaceRootKeepsExplicitWorkingDirectory(t *testing.T) {
	workspace := t.TempDir()
	got := resolveDefaultWorkspaceRoot(workspace, filepath.Join(t.TempDir(), "EchoCode.exe"), t.TempDir())
	if !sameWorkspacePath(got, workspace) {
		t.Fatalf("default workspace = %q, want cwd %q", got, workspace)
	}
}

func TestRuntimeSettingsNormalizeProcessResourceLimits(t *testing.T) {
	settings := DefaultRuntimeSettings()
	settings.MaxCommandMemoryMB = 1
	settings.MaxCommandCPUPercent = 101
	settings.MaxCommandProcesses = 2
	normalized := settings.Normalized()
	if normalized.MaxCommandMemoryMB != 4096 {
		t.Fatalf("memory limit = %d", normalized.MaxCommandMemoryMB)
	}
	if normalized.MaxCommandCPUPercent != 100 {
		t.Fatalf("CPU limit = %d", normalized.MaxCommandCPUPercent)
	}
	if normalized.MaxCommandProcesses != 128 {
		t.Fatalf("process limit = %d", normalized.MaxCommandProcesses)
	}
}

func TestRuntimeSettingsNormalizesSubagentConcurrencyLimit(t *testing.T) {
	settings := DefaultRuntimeSettings()
	settings.MaxConcurrentSubagents = 0
	if normalized := settings.Normalized(); normalized.MaxConcurrentSubagents != defaultMaxConcurrentSubagents {
		t.Fatalf("default subagent concurrency = %d", normalized.MaxConcurrentSubagents)
	}
	settings.MaxConcurrentSubagents = maxConcurrentSubagents + 1
	if normalized := settings.Normalized(); normalized.MaxConcurrentSubagents != maxConcurrentSubagents {
		t.Fatalf("capped subagent concurrency = %d", normalized.MaxConcurrentSubagents)
	}
}

func TestRuntimeSettingsMigrationBackfillsProcessResourceLimits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-settings.json")
	data := []byte(`{"schemaVersion":2,"sandboxMode":"workspace-write","filesystemAccess":"workspace-write"}`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	settings, loaded := loadRuntimeSettings(path)
	if !loaded {
		t.Fatal("runtime settings were not loaded")
	}
	if settings.SchemaVersion != runtimeSettingsSchemaVersion || settings.ToolTimeoutSeconds != 180 || settings.TaskIdleTimeoutSeconds != 300 || settings.MaxConcurrentSubagents != defaultMaxConcurrentSubagents || settings.MaxCommandMemoryMB != 4096 || settings.MaxCommandCPUPercent != 100 || settings.MaxCommandProcesses != 128 {
		t.Fatalf("migrated settings = %#v", settings)
	}
	if settings.Team.Enabled || settings.Team.MaxReviewRounds != 1 || len(settings.Team.Roles) != 5 {
		t.Fatalf("migrated team settings = %#v", settings.Team)
	}
	artifactTools, artifactToolsOK := plugins.SettingFor(settings.Plugins, plugins.ArtifactPluginID)
	if !artifactToolsOK || !artifactTools.Enabled || !artifactTools.Permissions.FileRead || !artifactTools.Permissions.FileWrite {
		t.Fatalf("migrated plugin settings = %#v", settings.Plugins)
	}
}

func TestRuntimeSettingsNormalizesTeamRoutesAndRequiredRoles(t *testing.T) {
	settings := DefaultRuntimeSettings()
	settings.Team = TeamSettings{
		Enabled:         true,
		MaxReviewRounds: 9,
		Roles: []TeamRoleSetting{
			{Role: TeamRoleImplementer, Enabled: false, ProviderID: "missing", ModelID: "missing-model"},
			{Role: TeamRoleSynthesizer, Enabled: false},
			{Role: "unknown", Enabled: true},
		},
	}
	normalized := settings.Normalized()
	if !normalized.Team.Enabled || normalized.Team.MaxReviewRounds != 2 || len(normalized.Team.Roles) != 5 {
		t.Fatalf("normalized team = %#v", normalized.Team)
	}
	implementer := teamRoleSettings(normalized.Team, TeamRoleImplementer)
	if !implementer.Enabled || implementer.ProviderID != "" || implementer.ModelID != "" {
		t.Fatalf("normalized implementer = %#v", implementer)
	}
	if !teamRoleSettings(normalized.Team, TeamRoleSynthesizer).Enabled {
		t.Fatal("synthesizer must remain enabled")
	}
}

func TestNormalizeModelReasoningProfiles(t *testing.T) {
	settings := DefaultRuntimeSettings()
	settings.Model.Providers = []ModelProviderSetting{
		{ID: "proxy", Protocol: "openai-compatible", ReasoningProfile: "openai-effort"},
		{ID: "native-anthropic", Protocol: "anthropic-compatible", ReasoningProfile: "anthropic"},
		{ID: "invalid", Protocol: "gemini", ReasoningProfile: "xai"},
	}
	providers := settings.Normalized().Model.Providers
	if providers[0].ReasoningProfile != "openai-effort" || providers[1].ReasoningProfile != "anthropic" || providers[2].ReasoningProfile != "auto" {
		t.Fatalf("reasoning profiles = %#v", providers)
	}
}
