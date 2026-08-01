package storage

import (
	"path/filepath"
	"testing"
	"time"
)

func TestUsageLedgerPersistsAndFiltersBySession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	firstAt := time.Date(2026, 7, 16, 8, 0, 0, 0, time.UTC)
	records := []UsageRecord{
		{CreatedAt: firstAt, SessionID: "session-a", ProviderID: "deepseek", ModelID: "deepseek-chat", InputTokens: 100, OutputTokens: 10, PromptCacheHitTokens: 80, PromptCacheMissTokens: 20, EffectiveCost: 0.001},
		{CreatedAt: firstAt.Add(time.Minute), SessionID: "session-b", ProviderID: "anthropic", ModelID: "claude", InputTokens: 200, OutputTokens: 20, EffectiveCost: 0.004},
		{CreatedAt: firstAt.Add(2 * time.Minute), SessionID: "session-a", ProviderID: "deepseek", ModelID: "deepseek-chat", InputTokens: 120, OutputTokens: 12, PromptCacheHitTokens: 100, PromptCacheMissTokens: 20, EffectiveCost: 0.002},
	}
	for _, record := range records {
		if err := db.AppendUsage(record); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	recent, err := db.RecentUsage("session-a", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || !recent[0].CreatedAt.Equal(firstAt) || recent[1].InputTokens != 120 {
		t.Fatalf("recent usage = %#v", recent)
	}
	totals, err := db.Totals("session-a")
	if err != nil {
		t.Fatal(err)
	}
	if totals.Samples != 2 || totals.InputTokens != 220 || totals.OutputTokens != 22 || totals.PromptCacheHitTokens != 180 {
		t.Fatalf("totals = %#v", totals)
	}
	if totals.EffectiveCost < 0.0029 || totals.EffectiveCost > 0.0031 {
		t.Fatalf("effective cost = %f", totals.EffectiveCost)
	}
}

func TestUsageLedgerNormalizesNegativeValues(t *testing.T) {
	db, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.AppendUsage(UsageRecord{InputTokens: -1, OutputTokens: -2, EffectiveCost: -3}); err != nil {
		t.Fatal(err)
	}
	recent, err := db.RecentUsage("", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 1 || recent[0].InputTokens != 0 || recent[0].OutputTokens != 0 || recent[0].EffectiveCost != 0 {
		t.Fatalf("recent usage = %#v", recent)
	}
}

func TestUsageBillingReconciliationPersistsPriceAndOfficialUsageDetails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "usage.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	if err := db.AppendUsage(UsageRecord{
		CreatedAt:                start.Add(time.Hour),
		ProviderID:               "openai",
		ModelID:                  "gpt-test",
		InputTokens:              1000,
		OutputTokens:             100,
		EffectiveCost:            1.25,
		PricingSource:            "configured-rate",
		PricingVersion:           "provider-rate-v1",
		InputPricePerMillion:     2.5,
		OutputPricePerMillion:    10,
		CacheHitPricePerMillion:  1.25,
		CacheMissPricePerMillion: 2.5,
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.AppendUsage(UsageRecord{
		CreatedAt:     end.Add(time.Hour),
		ProviderID:    "openai",
		EffectiveCost: 9.99,
	}); err != nil {
		t.Fatal(err)
	}
	estimated, err := db.UsageCostForProviderPeriod("openai", start, end)
	if err != nil {
		t.Fatal(err)
	}
	if estimated != 1.25 {
		t.Fatalf("period estimate = %f, want 1.25", estimated)
	}

	record, err := db.UpsertBillingReconciliation(BillingReconciliation{
		ProviderID:       "openai",
		ProviderName:     "OpenAI",
		PeriodStart:      start,
		PeriodEnd:        end,
		OfficialCost:     1.5,
		EstimatedCost:    estimated,
		Source:           "openai-costs-api",
		UsageDetailsJSON: `[{"modelId":"gpt-test","inputTokens":1000}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Difference != 0.25 || record.UsageDetailsJSON == "" {
		t.Fatalf("reconciliation = %#v", record)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	latest, err := db.LatestBillingReconciliation("openai")
	if err != nil {
		t.Fatal(err)
	}
	if latest.OfficialCost != 1.5 || latest.EstimatedCost != 1.25 || latest.UsageDetailsJSON == "" {
		t.Fatalf("restored reconciliation = %#v", latest)
	}
	recent, err := db.RecentUsage("", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].PricingSource != "configured-rate" || recent[0].InputPricePerMillion != 2.5 {
		t.Fatalf("restored pricing snapshot = %#v", recent)
	}
}
