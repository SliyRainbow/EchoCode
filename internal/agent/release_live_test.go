package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
	"github.com/SliyRainbow/EchoCode/internal/plugins"
	"github.com/SliyRainbow/EchoCode/internal/vault"
)

func TestMain(m *testing.M) {
	// Builtin plugins spawn the current executable. Preserve the production
	// worker entry point in the test executable as well.
	if handled, code := plugins.HandleCommandLine(os.Args[1:]); handled {
		os.Exit(code)
	}
	os.Exit(m.Run())
}

// These tests are deliberately opt-in: they spend tokens at the exact saved
// endpoint named by the operator. Only synthetic workspaces leave this process.
type liveReleaseFixture struct {
	service   *Service
	config    ServiceConfig
	root      string
	key       string
	approvals atomic.Int32
}

func newLiveReleaseFixture(t *testing.T, dialect string) *liveReleaseFixture {
	t.Helper()
	if os.Getenv("ECHOCODE_LIVE_ACCEPTANCE") != "1" {
		t.Skip("set ECHOCODE_LIVE_ACCEPTANCE=1 and an explicit saved provider/base URL/model to run paid integration")
	}
	providerID, expectedBase, model := os.Getenv("ECHOCODE_LIVE_PROVIDER_ID"), os.Getenv("ECHOCODE_LIVE_BASE_URL"), os.Getenv("ECHOCODE_LIVE_MODEL")
	if providerID == "" || expectedBase == "" || model == "" {
		t.Fatal("an explicit provider ID, expected base URL and model are required")
	}
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(configDir, "EchoCode", "runtime-settings.json"))
	if err != nil {
		t.Fatal("cannot read saved provider settings")
	}
	var saved RuntimeSettings
	if json.Unmarshal(data, &saved) != nil {
		t.Fatal("cannot decode saved provider settings")
	}
	var provider ModelProviderSetting
	for _, candidate := range saved.Model.Providers {
		if candidate.ID == providerID && strings.TrimRight(candidate.BaseURL, "/") == strings.TrimRight(expectedBase, "/") {
			provider = candidate
			break
		}
	}
	if provider.ID == "" {
		t.Fatal("saved provider does not match the explicitly authorized endpoint")
	}
	key, err := vault.NewOSVault().Get("EchoCode", "model-provider:"+providerID+":api-key")
	if err != nil || key == "" {
		t.Fatal("the scoped provider credential is unavailable")
	}
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	settings := DefaultRuntimeSettings()
	settings.WorkspaceRoot = workspace
	settings.ExtraWritableRoots = nil
	settings.NetworkAccess, settings.ShellAccess = false, false
	settings.Browser.Enabled, settings.Team.Enabled, settings.Memory.Enabled = false, false, false
	settings.ApprovalPolicy = "always"
	settings.AllowDestructiveOps = true // synthetic fixture files only; path checks still apply
	protocolName, apiType := "openai-compatible", "chat-completions"
	if dialect == "responses" {
		apiType = "responses"
	}
	if dialect == "messages" {
		protocolName, apiType = "anthropic-compatible", "anthropic-messages"
	}
	settings.Model = ModelSettings{SelectedProviderID: "release-live", SelectedModelID: model, Providers: []ModelProviderSetting{{
		ID: "release-live", Name: "Release fixture", Protocol: protocolName, APIType: apiType, BaseURL: expectedBase,
		Enabled: true, ExtraHeaders: provider.ExtraHeaders, DefaultModelID: model,
		Models: []ProviderModel{{ID: model, Provider: "release-live", MaxOutputTokens: 4096}},
	}}}
	data, err = json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	memory := vault.NewMemoryVault()
	_ = memory.Set("EchoCode", "model-provider:release-live:api-key", key)
	fixture := &liveReleaseFixture{root: workspace, key: key, config: ServiceConfig{
		AppVersion: "0.3.20", Vault: memory, SettingsPath: settingsPath,
		SkillsDir: filepath.Join(root, "skills"), UserSkillsDir: filepath.Join(root, "user-skills"), PluginsDir: filepath.Join(root, "plugins"),
		SessionsDir: filepath.Join(root, "sessions"), ProjectsPath: filepath.Join(root, "projects.json"), TemporaryWorkspaceRoot: workspace,
	}}
	fixture.service = NewService(fixture.config)
	fixture.setApprovals()
	if _, err := fixture.service.SetReasoningLevel(ReasoningLow); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fixture.service.Close() })
	return fixture
}

func (f *liveReleaseFixture) setApprovals() {
	f.service.SetApprovalNotify(func(request ApprovalRequest) {
		f.approvals.Add(1)
		// Host sandbox checks restrict every tool to this fixture; there are no
		// browser/network/shell tools or user-installed extensions in this service.
		go func() { _ = f.service.RespondApproval(request.ID, request.Tool, true, "once") }()
	})
}

func (f *liveReleaseFixture) send(t *testing.T, prompt string) ChatResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	result, err := f.service.SendChatMessage(ctx, prompt)
	if err != nil {
		t.Fatal(strings.ReplaceAll(err.Error(), f.key, "[REDACTED]"))
	}
	if !result.TurnCommitted || result.HistoryError != "" {
		t.Fatal("live turn was not durably committed")
	}
	var names []string
	for _, part := range result.Parts {
		if part.Kind == "tool_call" {
			names = append(names, part.Name+":"+part.Status)
			if part.Status == "error" {
				t.Logf("tool %s failure: %s", part.Name, strings.ReplaceAll(clipContextText(part.Output, 1200), f.key, "[REDACTED]"))
			}
		}
	}
	t.Logf("tools=%s, reasoning characters=%d, committed=%t", strings.Join(names, ","), len([]rune(result.Reasoning)), result.TurnCommitted)
	return result
}

func requireLiveTool(t *testing.T, result ChatResult, name string) {
	t.Helper()
	for _, part := range result.Parts {
		if part.Kind == "tool_call" && part.Name == name && part.Status == "ok" {
			return
		}
	}
	t.Fatalf("required successful tool %s was not observed", name)
}

func TestLiveReleaseFileLifecycleAcrossProtocols(t *testing.T) {
	for _, dialect := range []string{"chat", "responses", "messages"} {
		t.Run(dialect, func(t *testing.T) {
			f := newLiveReleaseFixture(t, dialect)
			if err := os.WriteFile(filepath.Join(f.root, "source.txt"), []byte("ALPHA"), 0600); err != nil {
				t.Fatal(err)
			}
			result := f.send(t, "这是隔离工作区的工具验收。依次执行：1.list_dir列目录并read_file读取source.txt；2.write_file创建result.txt，内容ALPHA；3.apply_patch将result.txt中的ALPHA替换成BETA；4.copy_file把result.txt复制为copy.txt；5.分别用glob列出txt文件、grep查找BETA、file_info检查copy.txt；6.delete_file删除copy.txt；7.read_file确认result.txt为BETA，最后仅回复LIFECYCLE_OK。不要使用子代理、其他工具或创建其他文件。")
			for _, name := range []string{"list_dir", "read_file", "write_file", "apply_patch", "copy_file", "glob", "grep", "file_info", "delete_file"} {
				requireLiveTool(t, result, name)
			}
			data, err := os.ReadFile(filepath.Join(f.root, "result.txt"))
			if err != nil || strings.TrimSpace(string(data)) != "BETA" {
				t.Fatal("result.txt did not contain the patched content")
			}
			if _, err := os.Stat(filepath.Join(f.root, "copy.txt")); !os.IsNotExist(err) {
				t.Fatal("copy.txt was not removed")
			}
			if f.approvals.Load() == 0 {
				t.Fatal("mutations bypassed on-request approval")
			}
			checkpoints := f.service.ListCheckpoints()
			if len(checkpoints) != 1 {
				t.Fatalf("checkpoints=%d", len(checkpoints))
			}
			first := checkpoints[0].ID
			f.send(t, "仅使用write_file将result.txt内容覆盖为GAMMA，最后回复UPDATED。")
			if _, err := f.service.RewindToCheckpoint(first); err != nil {
				t.Fatal(err)
			}
			data, err = os.ReadFile(filepath.Join(f.root, "result.txt"))
			if err != nil || strings.TrimSpace(string(data)) != "BETA" {
				t.Fatal("rewind did not restore the previous file")
			}
			history := f.service.GetSessionMessages()
			f.service.Close()
			f.service = NewService(f.config)
			f.setApprovals()
			if len(f.service.GetSessionMessages()) != len(history) || len(f.service.ListCheckpoints()) != 1 {
				t.Fatal("restart lost the rewound history")
			}
		})
	}
}

func TestLiveReleasePlanAndTeam(t *testing.T) {
	t.Run("plan", func(t *testing.T) {
		f := newLiveReleaseFixture(t, "chat")
		if _, err := f.service.SetReasoningLevel(ReasoningHigh); err != nil {
			t.Fatal(err)
		}
		f.service.SetPlanMode(true)
		f.send(t, "在当前测试工作区创建planned.txt，内容PLAN_OK，然后读回验证。不委派子任务。")
		data, err := os.ReadFile(filepath.Join(f.root, "planned.txt"))
		if err != nil || strings.TrimSpace(string(data)) != "PLAN_OK" || f.approvals.Load() < 1 {
			t.Fatal("approved plan did not create its verified artifact")
		}
		if f.service.WorkbenchState().PlanState.Status != "completed" {
			t.Fatal("plan did not reach completed state")
		}
	})
	t.Run("team", func(t *testing.T) {
		f := newLiveReleaseFixture(t, "chat")
		if _, err := f.service.SetReasoningLevel(ReasoningHigh); err != nil {
			t.Fatal(err)
		}
		settings := f.service.WorkbenchState().RuntimeSettings
		settings.Team.Enabled = true
		settings.Team.Roles = nil
		for _, role := range teamRoleOrder {
			settings.Team.Roles = append(settings.Team.Roles, TeamRoleSetting{Role: role, Enabled: true, ProviderID: "release-live", ModelID: settings.Model.SelectedModelID})
		}
		if _, err := f.service.SaveRuntimeSettings(settings); err != nil {
			t.Fatal(err)
		}
		result := f.send(t, "在当前隔离工作区创建team.txt，内容TEAM_OK；通过读取文件验证。任务不需要代码、命令、外网或额外子任务。审阅和测试角色应实际读回文件，按各自角色协议给出结论。")
		data, err := os.ReadFile(filepath.Join(f.root, "team.txt"))
		if err != nil || strings.TrimSpace(string(data)) != "TEAM_OK" || result.State.Team.Status != "completed" {
			t.Fatal("team did not finish its verified artifact")
		}
		for _, role := range result.State.Team.Roles {
			if role.Enabled && role.Status != "completed" {
				t.Fatalf("team role %s status=%s", role.Role, role.Status)
			}
		}
	})
}

func TestLiveReleaseDelegationAndOfficePlugin(t *testing.T) {
	t.Run("delegation", func(t *testing.T) {
		f := newLiveReleaseFixture(t, "chat")
		if err := os.WriteFile(filepath.Join(f.root, "marker.txt"), []byte("DELEGATION_OK"), 0600); err != nil {
			t.Fatal(err)
		}
		result := f.send(t, "这是子代理功能验收。用delegate_task启动一个explore只读子任务，让它read_file读取marker.txt并报告文件标识。主任务调用await_subagents等待并收集结果，最后仅回复子任务读到的标识。")
		if !strings.Contains(result.Content, "DELEGATION_OK") {
			t.Fatal("delegation result missing")
		}
		found := false
		for _, part := range result.Parts {
			if part.Kind == "subagent" && part.Status == "completed" {
				found = true
			}
		}
		if !found {
			t.Fatal("no completed child task recorded")
		}
	})
	t.Run("office-plugin", func(t *testing.T) {
		f := newLiveReleaseFixture(t, "chat")
		result := f.send(t, "使用已启用的办公插件创建report.docx，标题Release acceptance，正文OFFICE_PLUGIN_OK；随后调用该插件的document_inspect核对正文。不要使用write_file生成DOCX，也不要操作其他文件。最后仅回复OFFICE_OK。")
		requireLiveTool(t, result, "plugin__office-artifacts__document_create")
		requireLiveTool(t, result, "plugin__office-artifacts__document_inspect")
		text, err := artifacts.ArtifactText(filepath.Join(f.root, "report.docx"), 4096)
		if err != nil || !strings.Contains(text, "OFFICE_PLUGIN_OK") {
			t.Fatal("office plugin did not generate a readable DOCX")
		}
		if len(f.service.ListSessionArtifacts()) == 0 {
			t.Fatal("office artifact was not registered")
		}
	})
}
