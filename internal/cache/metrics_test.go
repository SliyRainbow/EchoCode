package cache

import (
	"strings"
	"testing"
)

func TestCacheHitRate(t *testing.T) {
	metrics := UsageMetrics{PromptCacheHitTokens: 96, PromptCacheMissTokens: 4}
	if got := metrics.CacheHitRate(); got != 0.96 {
		t.Fatalf("CacheHitRate() = %f, want 0.96", got)
	}
	if metrics.BelowTarget() {
		t.Fatal("0.96 should meet target")
	}
}

func TestDiagnosticsWhenBelowTarget(t *testing.T) {
	metrics := UsageMetrics{PromptCacheHitTokens: 10, PromptCacheMissTokens: 10}
	diagnostics := Diagnostics(metrics)
	if len(diagnostics) < 2 {
		t.Fatalf("Diagnostics() returned %d items, want actionable checks", len(diagnostics))
	}
}

func TestDiagnosticsWaitsForFirstUsage(t *testing.T) {
	metrics := UsageMetrics{}
	if metrics.BelowTarget() {
		t.Fatal("empty usage should not be below target")
	}
	diagnostics := Diagnostics(metrics)
	if len(diagnostics) != 1 {
		t.Fatalf("Diagnostics() returned %d items, want a waiting message", len(diagnostics))
	}
}

func TestAnalyzeShortPromptBelowTarget(t *testing.T) {
	metrics := UsageMetrics{PromptCacheHitTokens: 384, PromptCacheMissTokens: 110}
	health := Analyze(metrics)
	if health.Status != "watch" {
		t.Fatalf("status = %q, want watch", health.Status)
	}
	if health.MissTokenBudget != 16 {
		t.Fatalf("miss budget = %d, want 16", health.MissTokenBudget)
	}
	if health.RequiredHitTokens != 2640 {
		t.Fatalf("required hit tokens = %d, want 2640", health.RequiredHitTokens)
	}
	if health.AdditionalHitTokensNeeded != 2256 {
		t.Fatalf("additional hit tokens = %d, want 2256", health.AdditionalHitTokensNeeded)
	}
	if !health.ShortPrompt {
		t.Fatal("expected short prompt to be marked")
	}
}

func TestAnalyzeWarmingCache(t *testing.T) {
	metrics := UsageMetrics{PromptCacheMissTokens: 495}
	health := Analyze(metrics)
	if health.Status != "warming" {
		t.Fatalf("status = %q, want warming", health.Status)
	}
	diagnostics := Diagnostics(metrics)
	if len(diagnostics) < 2 {
		t.Fatalf("Diagnostics() returned %d items, want warming cache guidance", len(diagnostics))
	}
	if !containsDiagnostic(diagnostics, "公共前缀") {
		t.Fatalf("Diagnostics() = %#v, want DeepSeek public-prefix guidance", diagnostics)
	}
}

func TestAnalyzeTinyPromptDoesNotWarnAsLow(t *testing.T) {
	metrics := UsageMetrics{PromptCacheMissTokens: 19}
	health := Analyze(metrics)
	if health.Status != "watch" {
		t.Fatalf("status = %q, want watch", health.Status)
	}
	if !health.ShortPrompt {
		t.Fatal("expected tiny prompt to be marked as short")
	}
}

func TestAnalyzeNearTargetDeepSeekResidualMiss(t *testing.T) {
	metrics := UsageMetrics{PromptCacheHitTokens: 2688, PromptCacheMissTokens: 131}
	health := Analyze(metrics)
	if health.Status != "watch" {
		t.Fatalf("status = %q, want watch", health.Status)
	}
	if health.HitRate < nearTargetHitRate {
		t.Fatalf("hit rate = %f, want near target", health.HitRate)
	}
	if !containsDiagnostic(Diagnostics(metrics), "缓存粒度") {
		t.Fatalf("Diagnostics() = %#v, want cache granularity guidance", Diagnostics(metrics))
	}
	if !containsDiagnostic(Diagnostics(metrics), "上一轮 assistant") {
		t.Fatalf("Diagnostics() = %#v, want assistant tail guidance", Diagnostics(metrics))
	}
}

func TestAnalyzeHistoryKeepsImprovingShortSamplesInWatch(t *testing.T) {
	history := []UsageMetrics{
		{PromptCacheHitTokens: 0, PromptCacheMissTokens: 495},
		{PromptCacheHitTokens: 512, PromptCacheMissTokens: 97},
	}
	health := AnalyzeHistory(history)
	if health.Status != "watch" {
		t.Fatalf("status = %q, want watch", health.Status)
	}
	if !health.HitTokensIncreasing {
		t.Fatal("expected hit tokens to be increasing")
	}
	if !health.MissTokensImproving {
		t.Fatal("expected miss tokens to be improving")
	}
	diagnostics := DiagnosticsHistory(history)
	if len(diagnostics) < 3 {
		t.Fatalf("DiagnosticsHistory returned %d items, want trend details", len(diagnostics))
	}
}

func TestAnalyzeHistoryRequiresRepeatedLowBeforeLowStatus(t *testing.T) {
	twoSamples := []UsageMetrics{
		{PromptCacheHitTokens: 1000, PromptCacheMissTokens: 500},
		{PromptCacheHitTokens: 1000, PromptCacheMissTokens: 500},
	}
	if got := AnalyzeHistory(twoSamples).Status; got != "watch" {
		t.Fatalf("status with two samples = %q, want watch", got)
	}

	threeSamples := append(twoSamples, UsageMetrics{PromptCacheHitTokens: 1000, PromptCacheMissTokens: 500})
	if got := AnalyzeHistory(threeSamples).Status; got != "low" {
		t.Fatalf("status with three samples = %q, want low", got)
	}
}

func containsDiagnostic(diagnostics []string, needle string) bool {
	for _, item := range diagnostics {
		if strings.Contains(item, needle) {
			return true
		}
	}
	return false
}
