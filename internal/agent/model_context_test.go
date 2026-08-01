package agent

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

func TestResolveProviderModelContextsUsesBestAvailableSource(t *testing.T) {
	provider := ModelProviderSetting{
		ID:                  "custom",
		Protocol:            "openai-compatible",
		ContextWindowTokens: 32_768,
		Models: []ProviderModel{{
			ID:                  "manual-model",
			DisplayName:         "My Manual Model",
			ContextWindowTokens: 77_777,
			ContextWindowSource: ContextWindowSourceManual,
		}},
	}
	models := resolveProviderModelContexts(provider, []protocol.Model{
		{ID: "manual-model", ContextWindowTokens: 100_000, ContextWindowSource: ContextWindowSourceUpstream},
		{ID: "gpt-4o-mini"},
		{ID: "reported-model", ContextWindowTokens: 222_000, MaxOutputTokens: 32_000, ReasoningLevels: []string{"none", "high"}, ThinkingModes: []string{"adaptive"}},
		{ID: "unknown-model"},
	})

	assertModelContext(t, models[0], 77_777, ContextWindowSourceManual)
	if models[0].DisplayName != "My Manual Model" {
		t.Fatalf("manual display name = %q", models[0].DisplayName)
	}
	assertModelContext(t, models[1], 128_000, ContextWindowSourceCatalog)
	assertModelContext(t, models[2], 222_000, ContextWindowSourceUpstream)
	if models[2].MaxOutputTokens != 32_000 || len(models[2].ReasoningLevels) != 2 || len(models[2].ThinkingModes) != 1 {
		t.Fatalf("reported capabilities = %#v", models[2])
	}
	assertModelContext(t, models[3], 32_768, ContextWindowSourceProvider)
}

func TestResolveProviderModelContextsFallsBackSafely(t *testing.T) {
	models := resolveProviderModelContexts(ModelProviderSetting{ID: "custom", Protocol: "local"}, []protocol.Model{{ID: "unknown"}})
	assertModelContext(t, models[0], safeDefaultContextWindowTokens, ContextWindowSourceFallback)
}

func TestInferModelContextWindowUsesExactCatalogIDs(t *testing.T) {
	tests := []struct {
		modelID string
		tokens  int
		source  string
	}{
		{modelID: "gpt-5.4", tokens: 1_050_000, source: ContextWindowSourceCatalog},
		{modelID: "gpt-5.2-chat-latest", tokens: 128_000, source: ContextWindowSourceCatalog},
		{modelID: "gpt-5.6-sol", tokens: 1_050_000, source: ContextWindowSourceCatalog},
		{modelID: "gpt-5.6-sol-custom"},
		{modelID: "proxy/gpt-5.4"},
		{modelID: "gpt-5.7"},
		{modelID: "grok-4.5", tokens: 500_000, source: ContextWindowSourceCatalog},
		{modelID: "grok-build-latest", tokens: 500_000, source: ContextWindowSourceCatalog},
		{modelID: "grok-build-0.1", tokens: 256_000, source: ContextWindowSourceCatalog},
		{modelID: "grok-code-fast-1", tokens: 256_000, source: ContextWindowSourceCatalog},
		{modelID: "grok-4.3", tokens: 1_000_000, source: ContextWindowSourceCatalog},
		{modelID: "grok-4.20-multi-agent", tokens: 1_000_000, source: ContextWindowSourceCatalog},
		{modelID: "grok-chat-fast"},
		{modelID: "claude-fable-5"},
		{modelID: "anthropic/claude-opus-5"},
		{modelID: "claude-opus-5-20260724"},
		{modelID: "claude-opus-4-5-20251101", tokens: 200_000, source: ContextWindowSourceCatalog},
		{modelID: "claude-unknown"},
	}

	for _, test := range tests {
		t.Run(test.modelID, func(t *testing.T) {
			tokens, source := inferModelContextWindow(test.modelID, "local")
			if tokens != test.tokens || source != test.source {
				t.Fatalf("context = %d/%s, want %d/%s", tokens, source, test.tokens, test.source)
			}
		})
	}
}

func TestUnknownAnthropicModelsDoNotClaimProtocolWide200K(t *testing.T) {
	tokens, source := inferModelContextWindow("claude-custom-proxy-model", "anthropic-compatible")
	if tokens != 0 || source != "" {
		t.Fatalf("context = %d/%s, want unknown", tokens, source)
	}
	models := resolveProviderModelContexts(
		ModelProviderSetting{ID: "custom", Protocol: "anthropic-compatible"},
		[]protocol.Model{{ID: "claude-custom-proxy-model"}},
	)
	assertModelContext(t, models[0], safeDefaultContextWindowTokens, ContextWindowSourceFallback)
}

func TestRuntimeSettingsMigratesUnverifiedClaude5CatalogContext(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "runtime-settings.json")
	stored := DefaultRuntimeSettings()
	stored.SchemaVersion = 11
	stored.Model.Providers = []ModelProviderSetting{{
		ID: "anthropic", Protocol: "anthropic", Models: []ProviderModel{
			{ID: "claude-fable-5", ContextWindowTokens: 1_000_000, ContextWindowSource: ContextWindowSourceCatalog},
			{ID: "claude-opus-5", ContextWindowTokens: 900_000, ContextWindowSource: ContextWindowSourceManual},
			{ID: "claude-sonnet-5", ContextWindowTokens: 1_000_000, ContextWindowSource: ContextWindowSourceUpstream},
		},
	}}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	loaded, ok := loadRuntimeSettings(settingsPath)
	if !ok || len(loaded.Model.Providers) != 1 || len(loaded.Model.Providers[0].Models) != 3 {
		t.Fatalf("loaded settings = %#v ok=%v", loaded.Model.Providers, ok)
	}
	models := loaded.Model.Providers[0].Models
	assertProviderModelContext(t, models[0], safeDefaultContextWindowTokens, ContextWindowSourceFallback)
	assertProviderModelContext(t, models[1], 900_000, ContextWindowSourceManual)
	assertProviderModelContext(t, models[2], 1_000_000, ContextWindowSourceUpstream)
}

func TestRuntimeSettingsMigratesStaleXAIContextGuess(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "runtime-settings.json")
	stored := RuntimeSettings{
		SchemaVersion: 5,
		Model: ModelSettings{
			SelectedProviderID: "xai",
			SelectedModelID:    "grok-4.5",
			Providers: []ModelProviderSetting{{
				ID:       "xai",
				Name:     "xAI",
				Protocol: "openai-compatible",
				Models: []ProviderModel{
					{ID: "grok-4.5", ContextWindowTokens: 1_000_000, ContextWindowSource: ContextWindowSourceManual},
					{ID: "grok-build-0.1", ContextWindowTokens: 1_000_000, ContextWindowSource: ContextWindowSourceCatalog},
					{ID: "grok-build-latest", ContextWindowTokens: 1_000_000, ContextWindowSource: ContextWindowSourceUpstream},
					{ID: "grok-4.20", ContextWindowTokens: 1_000_000, ContextWindowSource: ContextWindowSourceCatalog},
					{ID: "grok-chat-fast", ContextWindowTokens: 1_000_000, ContextWindowSource: ContextWindowSourceManual},
				},
			}},
		},
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath})
	provider, _, ok := findModelProvider(service.WorkbenchState().RuntimeSettings.Model.Providers, "xai")
	if !ok || len(provider.Models) != 5 {
		t.Fatalf("provider = %#v", provider)
	}
	assertProviderModelContext(t, provider.Models[0], 500_000, ContextWindowSourceCatalog)
	assertProviderModelContext(t, provider.Models[1], 256_000, ContextWindowSourceCatalog)
	assertProviderModelContext(t, provider.Models[2], 1_000_000, ContextWindowSourceUpstream)
	assertProviderModelContext(t, provider.Models[3], 1_000_000, ContextWindowSourceCatalog)
	assertProviderModelContext(t, provider.Models[4], 1_000_000, ContextWindowSourceManual)
}

func TestOfficialXAIModelAliasesUseDocumentedContextWindows(t *testing.T) {
	groups := []struct {
		tokens int
		ids    []string
	}{
		{tokens: 500_000, ids: []string{"grok-4.5", "grok-4.5-latest", "grok-build-latest"}},
		{tokens: 1_000_000, ids: []string{"grok-4.3", "grok-4.3-latest", "grok-latest"}},
		{tokens: 1_000_000, ids: []string{
			"grok-4.20-0309-reasoning", "grok-4.20-reasoning-latest", "grok-4.20",
			"grok-4.20-0309-non-reasoning", "grok-4.20-non-reasoning-latest",
			"grok-4.20-multi-agent-0309", "grok-4.20-multi-agent-latest",
		}},
		{tokens: 256_000, ids: []string{"grok-build-0.1", "grok-code-fast-1", "grok-code-fast", "grok-code-fast-1-0825"}},
	}
	for _, group := range groups {
		for _, id := range group.ids {
			t.Run(id, func(t *testing.T) {
				tokens, source := inferModelContextWindow(id, "openai-compatible")
				if tokens != group.tokens || source != ContextWindowSourceCatalog {
					t.Fatalf("context = %d/%s, want %d/%s", tokens, source, group.tokens, ContextWindowSourceCatalog)
				}
			})
		}
	}
}

func TestResolveProviderModelContextsRecalculatesStaleInferredValues(t *testing.T) {
	provider := ModelProviderSetting{
		ID:       "custom",
		Protocol: "local",
		Models: []ProviderModel{
			{ID: "gpt-5.4", ContextWindowTokens: 400_000, ContextWindowSource: ContextWindowSourceCatalog},
			{ID: "gpt-5.2-chat-latest", ContextWindowTokens: 400_000, ContextWindowSource: ContextWindowSourceCatalog},
			{ID: "gpt-5.6-sol-custom", ContextWindowTokens: 400_000, ContextWindowSource: ContextWindowSourceCatalog},
			{ID: "manual-model", ContextWindowTokens: 333_000, ContextWindowSource: ContextWindowSourceManual},
			{ID: "upstream-model", ContextWindowTokens: 222_000, ContextWindowSource: ContextWindowSourceUpstream},
		},
	}

	models := resolveProviderModelContexts(provider, providerProtocolModels(provider.Models))
	assertModelContext(t, models[0], 1_050_000, ContextWindowSourceCatalog)
	assertModelContext(t, models[1], 128_000, ContextWindowSourceCatalog)
	assertModelContext(t, models[2], safeDefaultContextWindowTokens, ContextWindowSourceFallback)
	assertModelContext(t, models[3], 333_000, ContextWindowSourceManual)
	assertModelContext(t, models[4], 222_000, ContextWindowSourceUpstream)
}

func TestContextBudgetReusesPersistedUpstreamWindow(t *testing.T) {
	provider := ModelProviderSetting{
		ID:       "custom",
		Protocol: "openai-compatible",
		Models: []ProviderModel{{
			ID:                  "reported-model",
			ContextWindowTokens: 222_000,
			ContextWindowSource: ContextWindowSourceUpstream,
		}},
	}
	budget := contextBudgetForRoute(chatRoute{Provider: provider, ModelID: "reported-model"})
	if budget.WindowTokens != 222_000 || budget.WindowSource != ContextWindowSourceUpstream {
		t.Fatalf("budget = %#v, want persisted upstream window", budget)
	}
}

func TestRuntimeSettingsBackfillAndPersistModelContext(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "runtime-settings.json")
	stored := RuntimeSettings{
		Model: ModelSettings{
			SelectedProviderID: "custom",
			SelectedModelID:    "gpt-5.5",
			Providers: []ModelProviderSetting{{
				ID:       "custom",
				Name:     "Custom",
				Protocol: "openai-compatible",
				Models: []ProviderModel{{
					ID: "gpt-5.5", DisplayName: "gpt-5.5", Provider: "custom", MaxOutputTokens: 64_000,
					ReasoningLevels: []string{"none", "high", "high", "invalid"}, ThinkingModes: []string{"adaptive", "invalid"},
				}},
			}},
		},
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath})
	provider, _, ok := findModelProvider(service.WorkbenchState().RuntimeSettings.Model.Providers, "custom")
	if !ok || len(provider.Models) != 1 {
		t.Fatalf("backfilled provider = %#v", provider)
	}
	if provider.Models[0].ContextWindowTokens != 1_050_000 || provider.Models[0].ContextWindowSource != ContextWindowSourceCatalog {
		t.Fatalf("backfilled model = %#v", provider.Models[0])
	}
	if provider.Models[0].MaxOutputTokens != 64_000 || len(provider.Models[0].ReasoningLevels) != 2 || len(provider.Models[0].ThinkingModes) != 1 {
		t.Fatalf("normalized model capabilities = %#v", provider.Models[0])
	}

	persistedData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted RuntimeSettings
	if err := json.Unmarshal(persistedData, &persisted); err != nil {
		t.Fatal(err)
	}
	persistedProvider, _, ok := findModelProvider(persisted.Model.Providers, "custom")
	if !ok || len(persistedProvider.Models) != 1 || persistedProvider.Models[0].ContextWindowTokens != 1_050_000 || persistedProvider.Models[0].MaxOutputTokens != 64_000 {
		t.Fatalf("persisted provider = %#v", persistedProvider)
	}
}

func TestAnthropicCompatibilityLearningPersistsPerProviderModel(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "runtime-settings.json")
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath})
	settings := service.WorkbenchState().RuntimeSettings
	settings.Model = ModelSettings{
		SelectedProviderID: "relay",
		SelectedModelID:    "relay-claude",
		Providers: []ModelProviderSetting{{
			ID: "relay", Name: "Relay", Protocol: "anthropic-compatible", Enabled: true,
			Models: []ProviderModel{{ID: "relay-claude", DisplayName: "Relay Claude", Provider: "relay"}},
		}},
	}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	providerSetting, _, ok := findModelProvider(service.runtimeSettings.Model.Providers, "relay")
	if !ok {
		t.Fatal("relay provider was not saved")
	}
	model, _, ok := findProviderModel(providerSetting.Models, "relay-claude")
	if !ok {
		t.Fatal("relay model was not saved")
	}
	provider, err := service.chatProviderForRoute(chatRoute{
		Provider: providerSetting, ModelID: model.ID, Model: model, APIKey: "test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	client, ok := provider.(protocol.AnthropicProvider)
	if !ok || client.CompatibilityFeedback == nil {
		t.Fatalf("provider = %#v", provider)
	}
	client.CompatibilityFeedback(protocol.AnthropicCompatibilityFeedback{
		ProviderID: "relay", ModelID: "relay-claude", UnsupportedParameters: []string{"temperature", "temperature", "invalid"},
	})

	persistedData, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted RuntimeSettings
	if err := json.Unmarshal(persistedData, &persisted); err != nil {
		t.Fatal(err)
	}
	persistedProvider, _, ok := findModelProvider(persisted.Model.Providers, "relay")
	if !ok {
		t.Fatal("persisted relay provider is missing")
	}
	persistedModel, _, ok := findProviderModel(persistedProvider.Models, "relay-claude")
	if !ok || strings.Join(persistedModel.UnsupportedParameters, ",") != "temperature" {
		t.Fatalf("persisted model = %#v", persistedModel)
	}
	var request protocol.ChatRequest
	applyRouteToChatRequest(&request, chatRoute{ModelID: persistedModel.ID, Model: persistedModel})
	if strings.Join(request.ModelUnsupportedParameters, ",") != "temperature" {
		t.Fatalf("request compatibility = %#v", request.ModelUnsupportedParameters)
	}
}

func TestInvalidRuntimeSettingsAreNotOverwrittenByMigration(t *testing.T) {
	settingsPath := filepath.Join(t.TempDir(), "runtime-settings.json")
	if err := os.WriteFile(settingsPath, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: settingsPath})
	data, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "{" {
		t.Fatalf("invalid settings were overwritten: %q", data)
	}
}

func TestProviderKeyChangesPreserveModelCatalog(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	settings := service.WorkbenchState().RuntimeSettings
	settings.Model = ModelSettings{
		SelectedProviderID: "custom",
		SelectedModelID:    "gpt-5.5",
		Providers: []ModelProviderSetting{{
			ID:       "custom",
			Name:     "Custom",
			Protocol: "openai-compatible",
			Enabled:  true,
			Models:   []ProviderModel{{ID: "gpt-5.5", Provider: "custom"}},
		}},
	}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	state, err := service.SaveModelProviderAPIKey("custom", "sk-test")
	if err != nil {
		t.Fatal(err)
	}
	provider, _, ok := findModelProvider(state.RuntimeSettings.Model.Providers, "custom")
	if !ok || len(provider.Models) != 1 || provider.Models[0].ContextWindowTokens != 1_050_000 {
		t.Fatalf("models after key save = %#v", provider.Models)
	}
	state, err = service.ClearModelProviderAPIKey("custom")
	if err != nil {
		t.Fatal(err)
	}
	provider, _, ok = findModelProvider(state.RuntimeSettings.Model.Providers, "custom")
	if !ok || len(provider.Models) != 1 {
		t.Fatalf("models after key clear = %#v", provider.Models)
	}
}

func assertModelContext(t *testing.T, model protocol.Model, tokens int, source string) {
	t.Helper()
	if model.ContextWindowTokens != tokens || model.ContextWindowSource != source {
		t.Fatalf("model %s context = %d/%s, want %d/%s", model.ID, model.ContextWindowTokens, model.ContextWindowSource, tokens, source)
	}
}

func assertProviderModelContext(t *testing.T, model ProviderModel, tokens int, source string) {
	t.Helper()
	if model.ContextWindowTokens != tokens || model.ContextWindowSource != source {
		t.Fatalf("model %s context = %d/%s, want %d/%s", model.ID, model.ContextWindowTokens, model.ContextWindowSource, tokens, source)
	}
}
