package agent

import (
	"path/filepath"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/storage"
)

func TestUsageLedgerRecordsCostAndRestoresCurrentSession(t *testing.T) {
	root := t.TempDir()
	usagePath := filepath.Join(root, "usage.db")
	sessions := filepath.Join(root, "sessions")
	projects := filepath.Join(root, "projects.json")

	store, err := storage.Open(usagePath)
	if err != nil {
		t.Fatal(err)
	}
	config := ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  sessions,
		ProjectsPath: projects,
		UsageStore:   store,
	}
	service := NewService(config)
	provider := ModelProviderSetting{
		ID:                    "custom",
		Name:                  "Custom",
		InputPricePerMillion:  1,
		OutputPricePerMillion: 2,
	}
	route := chatRoute{Provider: provider, ModelID: "model-a"}
	metrics := usageMetricsFor(provider, "model-a", &protocol.TokenUsage{
		PromptTokens:          100,
		CompletionTokens:      10,
		PromptCacheHitTokens:  80,
		PromptCacheMissTokens: 20,
	})
	if metrics.EffectiveCost < 0.000119 || metrics.EffectiveCost > 0.000121 {
		t.Fatalf("cost = %f, want 0.00012", metrics.EffectiveCost)
	}
	service.metrics = metrics
	service.recordUsageMetrics(metrics, route)
	state := service.WorkbenchState()
	if state.UsageLedger.TotalSamples != 1 || state.UsageLedger.TotalInputTokens != 100 {
		t.Fatalf("ledger state = %#v", state.UsageLedger)
	}
	service.Close()

	store, err = storage.Open(usagePath)
	if err != nil {
		t.Fatal(err)
	}
	reloaded := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  sessions,
		ProjectsPath: projects,
		UsageStore:   store,
	})
	defer reloaded.Close()
	reloadedState := reloaded.WorkbenchState()
	if reloadedState.UsageMetrics.InputTokens != 100 || reloadedState.UsageMetrics.OutputTokens != 10 {
		t.Fatalf("restored usage = %#v", reloadedState.UsageMetrics)
	}
	if reloadedState.DeepSeekSession.SessionCacheHitTokens != 80 || reloadedState.DeepSeekSession.SessionCacheMissTokens != 20 {
		t.Fatalf("restored session cache = %#v", reloadedState.DeepSeekSession)
	}
}

func TestUsageMetricsUseOfficialOpenAIPricingCatalog(t *testing.T) {
	provider := ModelProviderSetting{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1"}
	metrics := usageMetricsFor(provider, "gpt-5.6-sol-2026-07-31", &protocol.TokenUsage{
		PromptTokens:          1_000_000,
		CompletionTokens:      1_000_000,
		PromptCacheHitTokens:  200_000,
		PromptCacheMissTokens: 800_000,
	})
	if metrics.EffectiveCost < 34.099 || metrics.EffectiveCost > 34.101 {
		t.Fatalf("catalog estimate = %f, want 34.1", metrics.EffectiveCost)
	}
	pricing := usagePricingSnapshotFor(provider, "gpt-5.6-sol-2026-07-31", 1_000_000)
	if pricing.Source != "official-catalog" || pricing.Version != openAITextPricingCatalogVersion {
		t.Fatalf("pricing snapshot = %#v", pricing)
	}
}

func TestRecordLiveUsageEmitsUpdatedCacheState(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	route := chatRoute{Provider: ModelProviderSetting{ID: "deepseek", Name: "DeepSeek", Protocol: "deepseek"}, ModelID: "deepseek-chat"}
	var emitted ChatStreamEvent
	service.recordLiveUsage(&protocol.TokenUsage{
		PromptTokens: 200, CompletionTokens: 20, PromptCacheHitTokens: 192, PromptCacheMissTokens: 8,
	}, route, func(event ChatStreamEvent) {
		emitted = event
	})
	if emitted.Type != "usage_state" || emitted.UsageState == nil {
		t.Fatalf("event = %#v", emitted)
	}
	if emitted.UsageState.UsageMetrics.InputTokens != 200 || emitted.UsageState.CacheHitRate != 0.96 {
		t.Fatalf("usage state = %#v", emitted.UsageState)
	}
	if emitted.UsageState.DeepSeekSession.SessionCacheHitTokens != 192 || emitted.UsageState.DeepSeekSession.SessionCacheMissTokens != 8 {
		t.Fatalf("session state = %#v", emitted.UsageState.DeepSeekSession)
	}
	if emitted.UsageState.DeepSeekSession.EstimatedInputTokens != 200 || emitted.UsageState.DeepSeekSession.ContextUsageSource != "provider" {
		t.Fatalf("live context did not include provider's tool-loop input usage: %+v", emitted.UsageState.DeepSeekSession)
	}
}
