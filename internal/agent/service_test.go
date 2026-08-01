package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/vault"
)

// requestIsStream 探测 OpenAI 风格请求体的 stream 字段，用于测试 mock 双模式应答。
func requestIsStream(body []byte) bool {
	var probe struct {
		Stream bool `json:"stream"`
	}
	_ = json.Unmarshal(body, &probe)
	return probe.Stream
}

// writeOpenAIReply 按 stream 标志写 SSE 或非流式 JSON 补全响应（choices[].message）。
// content 为回复文本，usageJSON 为可选的 usage 对象 JSON（为空则不带）。
func writeOpenAIReply(w http.ResponseWriter, stream bool, content, usageJSON string) {
	if stream {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q}}]}\n\n", content)
		if usageJSON != "" {
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[],\"usage\":%s}\n\n", usageJSON)
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if usageJSON != "" {
		_, _ = fmt.Fprintf(w, "{\"choices\":[{\"message\":{\"content\":%q}}],\"usage\":%s}", content, usageJSON)
	} else {
		_, _ = fmt.Fprintf(w, "{\"choices\":[{\"message\":{\"content\":%q}}]}", content)
	}
}

func TestServiceDeepSeekKeyLifecycle(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})

	state, err := service.SaveDeepSeekAPIKey(" sk-test ")
	if err != nil {
		t.Fatal(err)
	}
	if !state.DeepSeek.Configured {
		t.Fatal("deepseek should be configured after saving key")
	}
	if state.DeepSeek.Models != nil {
		t.Fatalf("models = %#v, want nil before connection test", state.DeepSeek.Models)
	}

	state, err = service.ClearDeepSeekAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if state.DeepSeek.Configured {
		t.Fatal("deepseek should not be configured after clearing key")
	}
}

func TestServiceDeepSeekConnectionWithoutKey(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})

	state, err := service.TestDeepSeekConnection(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if state.DeepSeek.LastCheckStatus != "error" {
		t.Fatalf("status = %q, want error", state.DeepSeek.LastCheckStatus)
	}
}

func TestServiceRuntimeSettingsPersist(t *testing.T) {
	settingsPath := t.TempDir() + "/runtime-settings.json"
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath})

	settings := DefaultRuntimeSettings()
	settings.SandboxMode = "read-only"
	settings.FilesystemAccess = "read-only"
	settings.NetworkAccess = false
	settings.ShellAccess = false
	settings.ApprovalPolicy = "never"
	settings.MaxCommandSeconds = 30
	settings.CacheTargetPercent = 97.5
	settings.Git.BranchPrefix = "codex/"
	settings.Git.MergeMethod = "squash"
	settings.Browser.Enabled = false
	settings.Browser.DefaultLocalURLDestination = "system"
	settings.ComputerControl.AlwaysAllowedApps = []string{"Code.exe", "chrome.exe"}
	settings.MCP.Servers = []MCPServerSetting{{
		ID:               "node-repl",
		Name:             "Node REPL",
		Command:          "node",
		Args:             []string{"server.js"},
		Enabled:          true,
		ToolResultPolicy: "balanced",
	}}
	settings.Model.SelectedProviderID = "openai-compatible"
	settings.Model.Providers = []ModelProviderSetting{{
		ID:                 "openai-compatible",
		Name:               "OpenAI 兼容",
		Protocol:           "openai-compatible",
		BaseURL:            "https://example.test/v1",
		Enabled:            true,
		LastSyncStatus:     "idle",
		SupportsModelFetch: true,
	}}

	state, err := service.SaveRuntimeSettings(settings)
	if err != nil {
		t.Fatal(err)
	}
	if state.RuntimeSettings.SandboxMode != "read-only" || state.RuntimeSettings.NetworkAccess {
		t.Fatalf("runtime settings not reflected in state: %#v", state.RuntimeSettings)
	}
	if state.CacheTarget != 0.975 {
		t.Fatalf("cache target = %f, want 0.975", state.CacheTarget)
	}
	if state.RuntimeSettings.Git.BranchPrefix != "codex/" || state.RuntimeSettings.Git.MergeMethod != "squash" {
		t.Fatalf("git settings not reflected in state: %#v", state.RuntimeSettings.Git)
	}
	if state.RuntimeSettings.Browser.Enabled {
		t.Fatalf("browser setting not reflected in state: %#v", state.RuntimeSettings.Browser)
	}
	if len(state.RuntimeSettings.MCP.Servers) != 1 || state.RuntimeSettings.MCP.Servers[0].Command != "node" {
		t.Fatalf("mcp settings not reflected in state: %#v", state.RuntimeSettings.MCP)
	}

	reloaded := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath})
	reloadedState := reloaded.WorkbenchState()
	if reloadedState.RuntimeSettings.SandboxMode != "read-only" || reloadedState.RuntimeSettings.ShellAccess {
		t.Fatalf("runtime settings not persisted: %#v", reloadedState.RuntimeSettings)
	}
	if reloadedState.RuntimeSettings.Git.BranchPrefix != "codex/" || reloadedState.RuntimeSettings.Browser.DefaultLocalURLDestination != "system" {
		t.Fatalf("nested runtime settings not persisted: %#v", reloadedState.RuntimeSettings)
	}
	if reloadedState.RuntimeSettings.Model.SelectedProviderID != "openai-compatible" {
		t.Fatalf("model settings not persisted: %#v", reloadedState.RuntimeSettings.Model)
	}
}

func TestRuntimePolicyContextDescribesEffectiveAuthorization(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	settings := DefaultRuntimeSettings()
	settings.SandboxMode = "workspace-write"
	settings.FilesystemAccess = "workspace-write"
	settings.NetworkAccess = false
	settings.ShellAccess = true
	settings.ApprovalPolicy = "on-request"
	settings.AllowDestructiveOps = false
	settings.WorkspaceRoot = t.TempDir()
	settings.ExtraWritableRoots = []string{t.TempDir()}
	service.runtimeSettings = settings.Normalized()

	policy := service.runtimePolicyContext()
	for _, expected := range []string{
		"sandbox_mode=workspace-write",
		"filesystem_access=workspace-write",
		"network_access=disabled",
		"shell_access=enabled",
		"approval_policy=on-request",
		"destructive_operations=disabled",
		"workspace_root=" + service.runtimeSettings.WorkspaceRoot,
		"extra_writable_roots=" + strings.Join(service.runtimeSettings.ExtraWritableRoots, "; "),
		"sandbox_backend=",
		"User-supplied credentials authorize only the explicitly named target, account, and requested operation.",
		"Never echo secrets in replies or persist them in files, logs, project memory, plans, or tool summaries.",
		"A user's scoped authorization does not disable provider safety policy or EchoCode approval requirements.",
	} {
		if !strings.Contains(policy, expected) {
			t.Errorf("runtime policy missing %q:\n%s", expected, policy)
		}
	}
}

func TestServicePersistsMultipleCustomModelProviders(t *testing.T) {
	settingsPath := t.TempDir() + "/runtime-settings.json"
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath})

	settings := service.WorkbenchState().RuntimeSettings
	settings.Model.SelectedProviderID = "custom-anthropic"
	settings.Model.SelectedModelID = "claude-custom"
	settings.Model.Providers = []ModelProviderSetting{
		{
			ID:                  "custom-anthropic",
			Name:                "Custom Anthropic",
			Protocol:            "anthropic-compatible",
			APIType:             "anthropic-messages",
			BaseURL:             "https://anthropic.example",
			BalanceURL:          "https://anthropic.example/balance",
			Enabled:             true,
			DefaultModelID:      "claude-custom",
			ContextWindowTokens: 200000,
			Models: []ProviderModel{{
				ID:                  "claude-custom",
				DisplayName:         "Claude Custom",
				Provider:            "custom-anthropic",
				ContextWindowTokens: 200000,
			}},
			LastSyncStatus: "idle",
		},
		{
			ID:                  "custom-gemini",
			Name:                "Custom Gemini",
			Protocol:            "gemini",
			APIType:             "gemini-generate-content",
			BaseURL:             "https://gemini.example/v1beta",
			Enabled:             true,
			DefaultModelID:      "gemini-custom",
			ContextWindowTokens: 1000000,
			Models: []ProviderModel{{
				ID:                  "gemini-custom",
				DisplayName:         "Gemini Custom",
				Provider:            "custom-gemini",
				ContextWindowTokens: 1000000,
			}},
			LastSyncStatus: "idle",
		},
	}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}

	reloaded := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath})
	state := reloaded.WorkbenchState()
	anthropic, _, ok := findModelProvider(state.RuntimeSettings.Model.Providers, "custom-anthropic")
	if !ok {
		t.Fatalf("custom-anthropic missing from providers: %#v", state.RuntimeSettings.Model.Providers)
	}
	if anthropic.Protocol != "anthropic-compatible" || anthropic.APIType != "anthropic-messages" || anthropic.BalanceURL == "" {
		t.Fatalf("anthropic provider not persisted: %#v", anthropic)
	}
	if len(anthropic.Models) != 1 || anthropic.Models[0].ContextWindowTokens != 200000 {
		t.Fatalf("anthropic models not persisted: %#v", anthropic.Models)
	}
	gemini, _, ok := findModelProvider(state.RuntimeSettings.Model.Providers, "custom-gemini")
	if !ok {
		t.Fatalf("custom-gemini missing from providers: %#v", state.RuntimeSettings.Model.Providers)
	}
	if gemini.Protocol != "gemini" || gemini.APIType != "gemini-generate-content" || gemini.ContextWindowTokens != 1000000 {
		t.Fatalf("gemini provider not persisted: %#v", gemini)
	}
	if state.ConfigFiles.RuntimeSettingsPath != settingsPath || state.ConfigFiles.ModelProvidersPath != settingsPath {
		t.Fatalf("config files = %#v, want runtime settings path", state.ConfigFiles)
	}
}

func TestServiceSendDeepSeekMessageUpdatesUsage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		body, _ := io.ReadAll(r.Body)
		writeOpenAIReply(w, requestIsStream(body), "hello",
			"{\"prompt_tokens\":100,\"completion_tokens\":12,\"total_tokens\":112,\"prompt_cache_hit_tokens\":96,\"prompt_cache_miss_tokens\":4}")
	}))
	defer server.Close()

	service := NewService(ServiceConfig{
		SkillsDir:       t.TempDir(),
		DeepSeekBaseURL: server.URL,
	})
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}

	result, err := service.SendDeepSeekMessage(context.Background(), "ping")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "hello" {
		t.Fatalf("content = %q, want hello", result.Content)
	}
	if result.State.UsageMetrics.PromptCacheHitTokens != 96 {
		t.Fatalf("hit tokens = %d, want 96", result.State.UsageMetrics.PromptCacheHitTokens)
	}
	if result.State.CacheHitRate != 0.96 {
		t.Fatalf("cache hit rate = %f, want 0.96", result.State.CacheHitRate)
	}
	if result.State.DeepSeekSession.SessionCacheHitTokens != 96 || result.State.DeepSeekSession.SessionCacheMissTokens != 4 {
		t.Fatalf("session cache = hit %d / miss %d, want 96 / 4", result.State.DeepSeekSession.SessionCacheHitTokens, result.State.DeepSeekSession.SessionCacheMissTokens)
	}
	if result.State.DeepSeekSession.SessionCacheHitRate != 0.96 {
		t.Fatalf("session cache hit rate = %f, want 0.96", result.State.DeepSeekSession.SessionCacheHitRate)
	}
	if result.State.DeepSeek.LastCheckStatus != "ok" {
		t.Fatalf("deepseek status = %q, want ok", result.State.DeepSeek.LastCheckStatus)
	}
	if !strings.Contains(result.State.DeepSeek.LastCheckMessage, "试聊成功") {
		t.Fatalf("deepseek message = %q, want chat success notice", result.State.DeepSeek.LastCheckMessage)
	}
}

func TestServiceReplacesSSHPasswordBeforeProviderRequestAndPersistence(t *testing.T) {
	const password = "provider-must-not-see-this-password"
	var requestBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestBody, _ = io.ReadAll(r.Body)
		writeOpenAIReply(w, requestIsStream(requestBody), "ready", "")
	}))
	defer server.Close()

	secrets := vault.NewMemoryVault()
	service := NewService(ServiceConfig{
		SkillsDir:       t.TempDir(),
		DeepSeekBaseURL: server.URL,
		SessionsDir:     t.TempDir(),
		SettingsPath:    filepath.Join(t.TempDir(), "runtime-settings.json"),
		Vault:           secrets,
	})
	defer service.Close()
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}
	_, err := service.SendDeepSeekMessage(context.Background(), "P:192.0.2.10用户名：root\n密码："+password+"\n请部署网站")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(requestBody), password) || !strings.Contains(string(requestBody), scopedCredentialScheme) {
		t.Fatalf("provider request did not use an opaque credential reference: %s", requestBody)
	}
	history := service.GetSessionMessages()
	if len(history) < 1 || strings.Contains(history[0].Content, password) || !strings.Contains(history[0].Content, scopedCredentialScheme) {
		t.Fatalf("persisted user message = %#v", history)
	}
}

func TestServiceRefreshOpenAICompatibleProviderModels(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("path = %s, want /models", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{
				{"id": "upstream-a"},
				{"id": "upstream-b"},
			},
		})
	}))
	defer server.Close()

	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	settings := service.WorkbenchState().RuntimeSettings
	for index, provider := range settings.Model.Providers {
		if provider.ID == "openai-compatible" {
			settings.Model.Providers[index].BaseURL = server.URL
			settings.Model.Providers[index].Enabled = true
		}
	}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveModelProviderAPIKey("openai-compatible", "sk-test"); err != nil {
		t.Fatal(err)
	}

	state, err := service.RefreshModelProviderModels(context.Background(), "openai-compatible")
	if err != nil {
		t.Fatal(err)
	}
	provider, _, ok := findModelProvider(state.RuntimeSettings.Model.Providers, "openai-compatible")
	if !ok {
		t.Fatal("openai-compatible provider missing")
	}
	if provider.LastSyncStatus != "ok" {
		t.Fatalf("sync status = %q, want ok: %s", provider.LastSyncStatus, provider.LastSyncMessage)
	}
	if len(provider.Models) != 2 || provider.DefaultModelID != "upstream-a" {
		t.Fatalf("provider models = %#v, default = %q", provider.Models, provider.DefaultModelID)
	}
	if !provider.APIKeyConfigured {
		t.Fatal("provider should report saved API key")
	}
}

func TestServiceSendsMessageThroughSelectedOpenAICompatibleProvider(t *testing.T) {
	var receivedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %s, want /chat/completions", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-openai" {
			t.Fatalf("authorization = %q", got)
		}
		var payload struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		receivedModel = payload.Model
		writeOpenAIReply(w, payload.Stream, "from upstream",
			"{\"prompt_tokens\":42,\"completion_tokens\":7,\"total_tokens\":49}")
	}))
	defer server.Close()

	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	settings := service.WorkbenchState().RuntimeSettings
	settings.Model.SelectedProviderID = "openai-compatible"
	settings.Model.SelectedModelID = "upstream-chat"
	for index, provider := range settings.Model.Providers {
		if provider.ID == "openai-compatible" {
			settings.Model.Providers[index].BaseURL = server.URL
			settings.Model.Providers[index].Enabled = true
			settings.Model.Providers[index].DefaultModelID = "upstream-chat"
			settings.Model.Providers[index].Models = []ProviderModel{{
				ID:          "upstream-chat",
				DisplayName: "Upstream Chat",
				Provider:    "openai-compatible",
			}}
		}
	}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveModelProviderAPIKey("openai-compatible", "sk-openai"); err != nil {
		t.Fatal(err)
	}

	result, err := service.SendDeepSeekMessage(context.Background(), "ping")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "from upstream" {
		t.Fatalf("content = %q, want upstream response", result.Content)
	}
	if receivedModel != "upstream-chat" || result.Model != "upstream-chat" {
		t.Fatalf("model = request %q result %q, want upstream-chat", receivedModel, result.Model)
	}
	if result.State.DeepSeekSession.ProviderID != "openai-compatible" || result.State.DeepSeekSession.Protocol != "openai-compatible" {
		t.Fatalf("session route = %#v, want openai-compatible", result.State.DeepSeekSession)
	}
	provider, _, ok := findModelProvider(result.State.RuntimeSettings.Model.Providers, "openai-compatible")
	if !ok {
		t.Fatal("openai-compatible provider missing")
	}
	if provider.LastSyncStatus != "ok" {
		t.Fatalf("provider status = %q, want ok: %s", provider.LastSyncStatus, provider.LastSyncMessage)
	}
}

func TestServiceSendsMessageThroughNativeAnthropicAndGeminiProviders(t *testing.T) {
	tests := []struct {
		name      string
		provider  ModelProviderSetting
		apiKey    string
		handler   func(t *testing.T, w http.ResponseWriter, r *http.Request)
		wantModel string
	}{
		{
			name: "anthropic",
			provider: ModelProviderSetting{
				ID:             "custom-anthropic",
				Name:           "Custom Anthropic",
				Protocol:       "anthropic-compatible",
				APIType:        "anthropic-messages",
				Enabled:        true,
				DefaultModelID: "claude-custom",
				ExtraHeaders:   "X-Route: echocode",
				ExtraBodyJSON:  `{"metadata":{"source":"echocode"},"model":"blocked"}`,
				Models: []ProviderModel{{
					ID:          "claude-custom",
					DisplayName: "Claude Custom",
					Provider:    "custom-anthropic",
				}},
			},
			apiKey: "sk-ant",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				t.Helper()
				if r.URL.Path != "/v1/messages" {
					t.Fatalf("path = %s, want /v1/messages", r.URL.Path)
				}
				if got := r.Header.Get("x-api-key"); got != "sk-ant" {
					t.Fatalf("x-api-key = %q", got)
				}
				if got := r.Header.Get("X-Route"); got != "echocode" {
					t.Fatalf("X-Route = %q", got)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode payload: %v", err)
				}
				if payload["model"] != "claude-custom" {
					t.Fatalf("model = %v, want claude-custom", payload["model"])
				}
				if metadata, ok := payload["metadata"].(map[string]any); !ok || metadata["source"] != "echocode" {
					t.Fatalf("metadata = %#v, want source echocode", payload["metadata"])
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"anthropic ok\"}}\n\n"))
				_, _ = w.Write([]byte("data: {\"type\":\"message_stop\"}\n\n"))
			},
			wantModel: "claude-custom",
		},
		{
			name: "gemini",
			provider: ModelProviderSetting{
				ID:             "custom-gemini",
				Name:           "Custom Gemini",
				Protocol:       "gemini",
				APIType:        "gemini-generate-content",
				Enabled:        true,
				DefaultModelID: "gemini-custom",
				ExtraHeaders:   "X-Route: echocode",
				ExtraBodyJSON:  `{"safetySettings":[{"category":"HARM_CATEGORY_DANGEROUS_CONTENT","threshold":"BLOCK_NONE"}],"contents":[{"role":"user","parts":[{"text":"blocked"}]}]}`,
				Models: []ProviderModel{{
					ID:          "gemini-custom",
					DisplayName: "Gemini Custom",
					Provider:    "custom-gemini",
				}},
			},
			apiKey: "sk-gemini",
			handler: func(t *testing.T, w http.ResponseWriter, r *http.Request) {
				t.Helper()
				if r.URL.Path != "/models/gemini-custom:streamGenerateContent" {
					t.Fatalf("path = %s, want gemini stream path", r.URL.Path)
				}
				if got := r.URL.Query().Get("key"); got != "" {
					t.Fatalf("API key leaked into query: %q", got)
				}
				if got := r.Header.Get("x-goog-api-key"); got != "sk-gemini" {
					t.Fatalf("x-goog-api-key = %q", got)
				}
				if got := r.Header.Get("X-Route"); got != "echocode" {
					t.Fatalf("X-Route = %q", got)
				}
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode payload: %v", err)
				}
				if safety, ok := payload["safetySettings"].([]any); !ok || len(safety) != 1 {
					t.Fatalf("safetySettings = %#v", payload["safetySettings"])
				}
				contents, ok := payload["contents"].([]any)
				if !ok || len(contents) == 0 {
					t.Fatalf("contents = %#v", payload["contents"])
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("data: {\"candidates\":[{\"content\":{\"parts\":[{\"text\":\"gemini ok\"}]}}]}\n\n"))
			},
			wantModel: "gemini-custom",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				tt.handler(t, w, r)
			}))
			defer server.Close()

			service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
			settings := service.WorkbenchState().RuntimeSettings
			tt.provider.BaseURL = server.URL
			settings.Model.SelectedProviderID = tt.provider.ID
			settings.Model.SelectedModelID = tt.wantModel
			settings.Model.Providers = []ModelProviderSetting{tt.provider}
			if _, err := service.SaveRuntimeSettings(settings); err != nil {
				t.Fatal(err)
			}
			if _, err := service.SaveModelProviderAPIKey(tt.provider.ID, tt.apiKey); err != nil {
				t.Fatal(err)
			}
			result, err := service.SendDeepSeekMessage(context.Background(), "ping")
			if err != nil {
				t.Fatal(err)
			}
			if result.Model != tt.wantModel || result.State.DeepSeekSession.ProviderID != tt.provider.ID {
				t.Fatalf("result route = model %q session %#v", result.Model, result.State.DeepSeekSession)
			}
			if result.Content == "" {
				t.Fatalf("content = %q", result.Content)
			}
		})
	}
}

func TestServiceMapsReasoningToDeepSeekThinking(t *testing.T) {
	var mu sync.Mutex
	type requestPayload struct {
		Model    string `json:"model"`
		Thinking *struct {
			Type string `json:"type"`
		} `json:"thinking"`
		ReasoningEffort string `json:"reasoning_effort"`
		Stream          bool   `json:"stream"`
	}
	var payloads []requestPayload
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var payload requestPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		payloads = append(payloads, payload)
		mu.Unlock()

		writeOpenAIReply(w, payload.Stream, "ok", "")
	}))
	defer server.Close()

	service := NewService(ServiceConfig{
		SkillsDir:       t.TempDir(),
		DeepSeekBaseURL: server.URL,
	})
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}
	ultra, err := service.SendDeepSeekMessage(context.Background(), "ultra")
	if err != nil {
		t.Fatal(err)
	}
	if ultra.State.DeepSeekSession.ThinkingMode != "enabled" || ultra.State.DeepSeekSession.ReasoningEffort != "max" {
		t.Fatalf("ultra session thinking = %#v, want enabled/max", ultra.State.DeepSeekSession)
	}

	if _, err := service.SetReasoningLevel(ReasoningMedium); err != nil {
		t.Fatal(err)
	}
	medium, err := service.SendDeepSeekMessage(context.Background(), "medium")
	if err != nil {
		t.Fatal(err)
	}
	if medium.State.DeepSeekSession.ThinkingMode != "enabled" || medium.State.DeepSeekSession.ReasoningEffort != "high" {
		t.Fatalf("medium session thinking = %#v, want enabled/high", medium.State.DeepSeekSession)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(payloads) != 2 {
		t.Fatalf("payload count = %d, want 2", len(payloads))
	}
	if payloads[0].Model != "deepseek-v4-pro" || payloads[0].Thinking == nil || payloads[0].Thinking.Type != "enabled" || payloads[0].ReasoningEffort != "max" {
		t.Fatalf("ultra payload = %#v, want v4-pro enabled/max", payloads[0])
	}
	if payloads[1].Model != "deepseek-v4-flash" || payloads[1].Thinking == nil || payloads[1].Thinking.Type != "enabled" || payloads[1].ReasoningEffort != "high" {
		t.Fatalf("medium payload = %#v, want v4-flash enabled/high", payloads[1])
	}
}

func TestServiceSendDeepSeekMessageReusesAppendOnlySession(t *testing.T) {
	var mu sync.Mutex
	var requests [][]protocol.Message
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			http.NotFound(w, r)
			return
		}
		var payload struct {
			Messages []protocol.Message `json:"messages"`
			Stream   bool               `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode request: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		mu.Lock()
		callCount++
		reply := fmt.Sprintf("answer-%d", callCount)
		requests = append(requests, cloneProtocolMessages(payload.Messages))
		mu.Unlock()

		writeOpenAIReply(w, payload.Stream, reply, "")
	}))
	defer server.Close()

	service := NewService(ServiceConfig{
		SkillsDir:       t.TempDir(),
		DeepSeekBaseURL: server.URL,
	})
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}

	first, err := service.SendDeepSeekMessage(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	if first.Content != "answer-1" {
		t.Fatalf("first content = %q, want answer-1", first.Content)
	}
	if !first.State.DeepSeekSession.AppendOnlyPrefixStable || first.State.DeepSeekSession.PreviousRequestMessageCount != 0 {
		t.Fatalf("first prefix diagnostic = %#v, want stable first request with no previous request", first.State.DeepSeekSession)
	}
	second, err := service.SendDeepSeekMessage(context.Background(), "second")
	if err != nil {
		t.Fatal(err)
	}
	if second.Content != "answer-2" {
		t.Fatalf("second content = %q, want answer-2", second.Content)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 2 {
		t.Fatalf("captured requests = %d, want 2", len(requests))
	}
	if len(requests[0]) != 3 {
		t.Fatalf("first request message count = %d, want 3", len(requests[0]))
	}
	assertMessage(t, requests[0], 0, "system", "")
	assertMessage(t, requests[0], 1, "user", "")
	assertMessage(t, requests[0], 2, "user", "first")
	if strings.Contains(requests[0][0].Content, "cache_prefix_hash=") {
		t.Fatalf("system prompt leaked host-only prefix metadata: %q", requests[0][0].Content)
	}
	if !strings.Contains(requests[0][1].Content, requestContextStart) || strings.Contains(requests[0][1].Content, "first") {
		t.Fatalf("private turn context should be present without duplicating user input: %q", requests[0][1].Content)
	}

	if len(requests[1]) != 6 {
		t.Fatalf("second request message count = %d, want 6", len(requests[1]))
	}
	assertMessage(t, requests[1], 0, "system", requests[0][0].Content)
	assertMessage(t, requests[1], 1, "user", requests[0][1].Content)
	assertMessage(t, requests[1], 2, "user", "first")
	assertMessage(t, requests[1], 3, "assistant", "answer-1")
	assertMessage(t, requests[1], 4, "user", "")
	assertMessage(t, requests[1], 5, "user", "second")
	if !strings.Contains(requests[1][4].Content, requestContextStart) || strings.Contains(requests[1][4].Content, "second") {
		t.Fatalf("second private turn context should not duplicate user input: %q", requests[1][4].Content)
	}
	if second.State.DeepSeekSession.TurnCount != 2 {
		t.Fatalf("turn count = %d, want 2", second.State.DeepSeekSession.TurnCount)
	}
	if second.State.DeepSeekSession.MessageCount != 7 {
		t.Fatalf("session message count = %d, want 7", second.State.DeepSeekSession.MessageCount)
	}
	if !second.State.DeepSeekSession.AppendOnlyPrefixStable {
		t.Fatalf("append-only prefix stable = false, want true")
	}
	if second.State.DeepSeekSession.PreviousRequestMessageCount != len(requests[0]) {
		t.Fatalf("previous request messages = %d, want %d", second.State.DeepSeekSession.PreviousRequestMessageCount, len(requests[0]))
	}
	if second.State.DeepSeekSession.CommonPrefixMessageCount != len(requests[0]) {
		t.Fatalf("common prefix messages = %d, want %d", second.State.DeepSeekSession.CommonPrefixMessageCount, len(requests[0]))
	}
}

func TestServiceResetDeepSeekSessionClearsSessionAndMetrics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		writeOpenAIReply(w, requestIsStream(body), "hello",
			"{\"prompt_tokens\":100,\"completion_tokens\":12,\"total_tokens\":112,\"prompt_cache_hit_tokens\":80,\"prompt_cache_miss_tokens\":20}")
	}))
	defer server.Close()

	service := NewService(ServiceConfig{
		SkillsDir:       t.TempDir(),
		DeepSeekBaseURL: server.URL,
	})
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}
	result, err := service.SendDeepSeekMessage(context.Background(), "ping")
	if err != nil {
		t.Fatal(err)
	}
	if !result.State.DeepSeekSession.Active || result.State.DeepSeekSession.TurnCount != 1 {
		t.Fatalf("session state after send = %#v, want active one-turn session", result.State.DeepSeekSession)
	}
	if result.State.UsageMetrics.PromptCacheHitTokens != 80 {
		t.Fatalf("hit tokens = %d, want 80", result.State.UsageMetrics.PromptCacheHitTokens)
	}

	state, err := service.ResetDeepSeekSession()
	if err != nil {
		t.Fatal(err)
	}
	if state.DeepSeekSession.Active {
		t.Fatal("session should be inactive after reset")
	}
	if state.DeepSeekSession.MessageCount != 0 || state.DeepSeekSession.TurnCount != 0 {
		t.Fatalf("session counts after reset = messages %d turns %d, want 0/0", state.DeepSeekSession.MessageCount, state.DeepSeekSession.TurnCount)
	}
	if state.UsageMetrics.PromptCacheHitTokens != 0 || state.UsageMetrics.PromptCacheMissTokens != 0 {
		t.Fatalf("usage after reset = %#v, want zero cache tokens", state.UsageMetrics)
	}
	if state.CacheHitRate != 0 {
		t.Fatalf("cache hit rate after reset = %f, want 0", state.CacheHitRate)
	}
	if state.DeepSeekSession.SessionCacheHitTokens != 0 || state.DeepSeekSession.SessionCacheMissTokens != 0 || state.DeepSeekSession.SessionCacheHitRate != 0 {
		t.Fatalf("session cache after reset = %#v, want zero cache totals", state.DeepSeekSession)
	}
	if len(service.metricsHistory) != 0 {
		t.Fatalf("metrics history length after reset = %d, want 0", len(service.metricsHistory))
	}
}

func TestServicePreservesLegitimateInternalIdentifierDiscussion(t *testing.T) {
	const answer = "The code uses stable_prefix, product_identity, skills_index, and routing_policy as ordinary identifiers."
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		writeOpenAIReply(w, requestIsStream(body), answer, "")
	}))
	defer server.Close()

	service := NewService(ServiceConfig{
		SkillsDir:       t.TempDir(),
		DeepSeekBaseURL: server.URL,
	})
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}

	result, err := service.SendDeepSeekMessage(context.Background(), "ping")
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != answer {
		t.Fatalf("content = %q, want legitimate code discussion to remain intact", result.Content)
	}
}

func TestFormatStablePromptMarksInternalsAsHidden(t *testing.T) {
	ctx := NewContextBuilder().Build(StableContext{
		ProductIdentity: "EchoCode",
		SystemRules:     []string{"rule"},
		Reasoning:       ReasoningProfile{ID: ReasoningUltra, Budget: ReasoningBudget{CachePolicy: "strict-stable-prefix"}},
		ProjectSummary:  "summary",
		RoutingPolicy:   "route",
	}, VolatileContext{UserInput: "hidden user input"})

	prompt := formatStablePrompt(ctx)
	if !strings.Contains(prompt, "Never quote") {
		t.Fatalf("prompt should forbid quoting internals: %q", prompt)
	}
	if strings.Contains(prompt, "hidden user input") {
		t.Fatalf("stable prompt should not include volatile user input: %q", prompt)
	}
	if strings.Contains(prompt, "skills_index") || strings.Contains(prompt, "mcp_schema_snapshot") {
		t.Fatalf("stable prompt should not expand long internal sections: %q", prompt)
	}
	if !strings.Contains(prompt, "use another structured read when more detail is needed") {
		t.Fatalf("stable prompt should describe the real clipped-result recovery path: %q", prompt)
	}
	for _, forbidden := range []string{
		"concise developer assistant",
		"after each step changes state",
		"Use the next user message as the only user-facing request",
		"cache_prefix_hash=",
		"prompt-cache reuse depends",
		"byte-stable across turns",
		"Keep raw outputs in local references",
		"raw_result_id",
	} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("stable prompt still contains capability-limiting instruction %q: %q", forbidden, prompt)
		}
	}
	for _, expected := range []string{
		"enough detail to fully handle",
		"Decide the task strategy yourself",
		"visible plan will materially help",
		"call report_progress before the first action",
		"length proportional to the task",
		"immediately following any [EchoCode private turn context] block",
		"user explicitly supplies an absolute target",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("stable prompt is missing completeness guidance %q: %q", expected, prompt)
		}
	}
}

func TestContextPreviewDoesNotForceStructuredSummary(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	ctx := service.contextPreviewForInput("检查并修复当前任务")
	var requirements string
	for _, section := range ctx.VolatileTail {
		if section.Name == "output_requirements" {
			requirements = section.Content
			break
		}
	}
	if requirements == "" {
		t.Fatal("output requirements were not included in the volatile context")
	}
	if strings.Contains(requirements, "输出结构化摘要") {
		t.Fatalf("output requirements still force a fixed summary: %q", requirements)
	}
	if !strings.Contains(requirements, "完整、准确") || !strings.Contains(requirements, "不强制使用固定摘要模板") {
		t.Fatalf("output requirements do not preserve answer completeness: %q", requirements)
	}
}

func TestContextPreviewLeavesLocalDiagnosticStrategyToModel(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	input := "你帮我看看，我的 Claude 桌面版是不是出了问题，CCSwitch 当前模型仍然是另一个中转的"
	ctx := service.contextPreviewForInput(input)
	if !strings.Contains(stableSection(ctx, "system_rules", ""), "模型负责理解任务") {
		t.Fatalf("system rules do not assign task strategy to the model: %#v", ctx.StablePrefix)
	}
	var requirements string
	for _, section := range ctx.VolatileTail {
		if section.Name == "output_requirements" {
			requirements = section.Content
			break
		}
	}
	if strings.Contains(requirements, "本机状态诊断任务") {
		t.Fatalf("host should not keyword-route a local diagnostic: %s", requirements)
	}
}

func TestDeleteModelProviderPersistsRemovalAndClearsSecret(t *testing.T) {
	settingsPath := t.TempDir() + "/runtime-settings.json"
	secrets := vault.NewMemoryVault()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath, Vault: secrets})
	if _, err := service.SaveDeepSeekAPIKey("sk-delete-me"); err != nil {
		t.Fatal(err)
	}

	state, err := service.DeleteModelProvider("deepseek")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, ok := findModelProvider(state.RuntimeSettings.Model.Providers, "deepseek"); ok {
		t.Fatal("deleted DeepSeek provider is still present")
	}
	if state.RuntimeSettings.Model.SelectedProviderID == "deepseek" {
		t.Fatal("selected route still points to deleted provider")
	}
	if _, err := secrets.Get(secretServiceName, providerSecretAccountName("deepseek")); !errors.Is(err, vault.ErrSecretNotFound) {
		t.Fatalf("secret lookup error = %v, want ErrSecretNotFound", err)
	}

	reloaded := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath, Vault: secrets})
	if _, _, ok := findModelProvider(reloaded.WorkbenchState().RuntimeSettings.Model.Providers, "deepseek"); ok {
		t.Fatal("deleted default provider was restored after reload")
	}
}

func TestDeleteLastModelProviderLeavesEmptyConfiguration(t *testing.T) {
	settingsPath := t.TempDir() + "/runtime-settings.json"
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath, Vault: vault.NewMemoryVault()})
	for _, providerID := range []string{"deepseek", "openai-compatible", "local-openai"} {
		if _, err := service.DeleteModelProvider(providerID); err != nil {
			t.Fatalf("delete %s: %v", providerID, err)
		}
	}
	state := service.WorkbenchState()
	if len(state.RuntimeSettings.Model.Providers) != 0 {
		t.Fatalf("providers = %#v, want empty", state.RuntimeSettings.Model.Providers)
	}
	if state.RuntimeSettings.Model.SelectedProviderID != "" || state.RuntimeSettings.Model.SelectedModelID != "" {
		t.Fatalf("selected route = %q/%q, want empty", state.RuntimeSettings.Model.SelectedProviderID, state.RuntimeSettings.Model.SelectedModelID)
	}

	reloaded := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath, Vault: vault.NewMemoryVault()})
	if len(reloaded.WorkbenchState().RuntimeSettings.Model.Providers) != 0 {
		t.Fatal("empty provider configuration was repopulated after reload")
	}
}

func TestSendChatMessageWithEventsEmitsDeltas(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"world\"},\"finish_reason\":\"stop\"}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	sessions := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), DeepSeekBaseURL: server.URL, SessionsDir: sessions}
	service := NewService(config)
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}
	var deltas []string
	result, err := service.SendChatMessageWithEvents(context.Background(), "ping", func(event ChatStreamEvent) {
		if event.Type == "delta" {
			deltas = append(deltas, event.Delta)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Content != "hello world" {
		t.Fatalf("content = %q", result.Content)
	}
	if strings.Join(deltas, "") != result.Content || len(deltas) != 2 {
		t.Fatalf("deltas = %#v, result = %q", deltas, result.Content)
	}
}

func TestConfiguredRelayCarriesSelectedGPT56ReasoningToResponsesPayload(t *testing.T) {
	var payload map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("request path = %q, want /responses", r.URL.Path)
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, `{"output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
	}))
	defer server.Close()

	secrets := vault.NewMemoryVault()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: t.TempDir() + "/settings.json", Vault: secrets})
	settings := DefaultRuntimeSettings()
	settings.Model.SelectedProviderID = "relay"
	settings.Model.SelectedModelID = "gpt-5.6-sol"
	settings.Model.Providers = []ModelProviderSetting{{
		ID:                 "relay",
		Name:               "Relay",
		Protocol:           "local",
		APIType:            "responses",
		BaseURL:            server.URL,
		Enabled:            true,
		ReasoningProfile:   "auto",
		DefaultModelID:     "gpt-5.6-sol",
		Models:             []ProviderModel{{ID: "gpt-5.6-sol", DisplayName: "GPT-5.6 Sol", Provider: "relay"}},
		SupportsModelFetch: true,
	}}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveModelProviderAPIKey("relay", "sk-test"); err != nil {
		t.Fatal(err)
	}
	state, err := service.SetReasoningLevel(ReasoningMax)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.ReasoningOptions) != 6 || state.Reasoning.ID != ReasoningMax {
		t.Fatalf("dynamic reasoning state = %#v / %#v", state.Reasoning, state.ReasoningOptions)
	}
	route, err := service.selectChatRoute()
	if err != nil {
		t.Fatal(err)
	}
	provider, err := service.chatProviderForRoute(route)
	if err != nil {
		t.Fatal(err)
	}
	caller, ok := provider.(protocol.ToolCaller)
	if !ok {
		t.Fatal("configured Responses provider does not implement ToolCaller")
	}
	_, err = caller.Complete(context.Background(), protocol.ChatRequest{
		Model:          route.ModelID,
		ReasoningLevel: string(service.reasoning),
		Messages:       []protocol.Message{{Role: "user", Content: "ping"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	reasoning, ok := payload["reasoning"].(map[string]any)
	if !ok || reasoning["effort"] != "max" {
		t.Fatalf("reasoning payload = %#v, want effort=max", payload["reasoning"])
	}
}

func TestSendChatMessageWithEventsCancellationRetainsPartialTurn(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	sessions := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), DeepSeekBaseURL: server.URL, SessionsDir: sessions}
	service := NewService(config)
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result, err := service.SendChatMessageWithEvents(ctx, "cancel me", func(event ChatStreamEvent) {
		if event.Type == "delta" {
			cancel()
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if !result.TurnCommitted || result.Content != "partial" {
		t.Fatalf("cancelled result = %#v", result)
	}
	if service.sessionState.TurnCount != 1 {
		t.Fatalf("turn count = %d after cancelled turn", service.sessionState.TurnCount)
	}
	if len(service.sessionMessages) < 2 || service.sessionMessages[len(service.sessionMessages)-2].Content != "cancel me" || service.sessionMessages[len(service.sessionMessages)-1].Content != "partial" {
		t.Fatalf("cancelled partial turn was not retained: %#v", service.sessionMessages)
	}
	reloaded := NewService(config)
	foundUser := false
	foundCancelled := false
	for _, message := range reloaded.GetSessionMessages() {
		if message.Content == "cancel me" {
			foundUser = true
		}
		if message.Status == "cancelled" {
			foundCancelled = true
			if message.Content != "partial" {
				t.Fatalf("cancelled partial response = %#v", message)
			}
		}
	}
	if !foundUser || !foundCancelled {
		t.Fatalf("retained cancelled turn missing after reload: user=%v cancelled=%v", foundUser, foundCancelled)
	}
	foundContext := false
	for _, message := range reloaded.sessionMessages {
		if message.Role == "user" && message.Content == "cancel me" {
			foundContext = true
		}
	}
	if !foundContext {
		t.Fatalf("cancelled user request is missing from continuation context: %#v", reloaded.sessionMessages)
	}
}

func TestSendChatMessageWithEventsCancellationRollsBackZeroOutputTurn(t *testing.T) {
	started := make(chan struct{})
	var startedOnce sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if flusher, ok := w.(http.Flusher); ok {
			flusher.Flush()
		}
		startedOnce.Do(func() { close(started) })
		<-r.Context().Done()
	}))
	defer server.Close()

	config := ServiceConfig{SkillsDir: t.TempDir(), DeepSeekBaseURL: server.URL, SessionsDir: t.TempDir()}
	service := NewService(config)
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type sendResult struct {
		result ChatResult
		err    error
	}
	done := make(chan sendResult, 1)
	go func() {
		result, err := service.SendChatMessageWithEvents(ctx, "restore this draft", nil)
		done <- sendResult{result: result, err: err}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("provider request did not start")
	}
	cancel()
	result := <-done
	if !errors.Is(result.err, context.Canceled) || result.result.TurnCommitted {
		t.Fatalf("zero-output cancellation = %#v, err=%v", result.result, result.err)
	}
	if service.sessionState.TurnCount != 0 {
		t.Fatalf("turn count = %d after zero-output cancellation", service.sessionState.TurnCount)
	}
	for _, message := range service.GetSessionMessages() {
		if message.Content == "restore this draft" {
			t.Fatalf("zero-output user turn survived rollback: %#v", message)
		}
	}
}

func TestSendChatMessageFailureRollsBackTurnAndPersistsTerminalState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	sessions := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), DeepSeekBaseURL: server.URL, SessionsDir: sessions}
	service := NewService(config)
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}

	_, err := service.SendChatMessageWithEvents(context.Background(), "fail me", nil)
	if err == nil {
		t.Fatal("provider failure unexpectedly succeeded")
	}
	if service.sessionState.TurnCount != 0 {
		t.Fatalf("turn count = %d after failed turn", service.sessionState.TurnCount)
	}

	reloaded := NewService(config)
	foundFailed := false
	for _, message := range reloaded.GetSessionMessages() {
		if message.Content == "fail me" {
			t.Fatalf("failed optimistic message survived restart: %#v", message)
		}
		if message.Status == "failed" {
			foundFailed = true
			if strings.TrimSpace(message.Content) == "" {
				t.Fatalf("failed terminal record has no explanation: %#v", message)
			}
		}
	}
	if !foundFailed {
		t.Fatal("failed turn did not leave a durable terminal record")
	}
}

func assertMessage(t *testing.T, messages []protocol.Message, index int, role string, content string) {
	t.Helper()
	if index >= len(messages) {
		t.Fatalf("message index %d out of range for %d messages", index, len(messages))
	}
	if messages[index].Role != role {
		t.Fatalf("message %d role = %q, want %q", index, messages[index].Role, role)
	}
	if content != "" && messages[index].Content != content {
		t.Fatalf("message %d content = %q, want %q", index, messages[index].Content, content)
	}
}
