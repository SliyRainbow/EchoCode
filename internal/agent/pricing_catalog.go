package agent

import "strings"

// The catalog is deliberately small and versioned. It is only used for an
// estimate; provider-reported Costs API data remains the authoritative amount.
// Sources are the official public pricing pages captured for this release.
const openAITextPricingCatalogVersion = "openai-text-standard-2026-07-31"

type officialModelPrice struct {
	Prefix      string
	Input       float64
	CachedInput float64
	Output      float64
}

// Prices are USD per million tokens for OpenAI's standard text tier. Entries
// with dated model IDs are matched by their stable family prefix. This is not
// used for custom endpoints, batch/flex/fast service tiers, or models absent
// from this catalog.
var openAITextPricingCatalog = []officialModelPrice{
	{Prefix: "gpt-5.6-sol", Input: 5, CachedInput: 0.5, Output: 30},
	{Prefix: "gpt-5.6-terra", Input: 2, CachedInput: 0.2, Output: 12},
	{Prefix: "gpt-5.6-luna", Input: 0.2, CachedInput: 0.02, Output: 1.2},
	{Prefix: "gpt-5.5-pro", Input: 30, Output: 180},
	{Prefix: "gpt-5.5", Input: 5, CachedInput: 0.5, Output: 30},
	{Prefix: "gpt-5.4-pro", Input: 30, Output: 180},
	{Prefix: "gpt-5.4-mini", Input: 0.75, CachedInput: 0.075, Output: 4.5},
	{Prefix: "gpt-5.4-nano", Input: 0.2, CachedInput: 0.02, Output: 1.25},
	{Prefix: "gpt-5.4", Input: 2.5, CachedInput: 0.25, Output: 15},
	{Prefix: "gpt-5.2-pro", Input: 21, Output: 168},
	{Prefix: "gpt-5.2", Input: 1.75, CachedInput: 0.175, Output: 14},
	{Prefix: "gpt-5-mini", Input: 0.25, CachedInput: 0.025, Output: 2},
	{Prefix: "gpt-5-nano", Input: 0.05, CachedInput: 0.005, Output: 0.4},
	{Prefix: "gpt-5-pro", Input: 15, Output: 120},
	{Prefix: "gpt-5.1", Input: 1.25, CachedInput: 0.125, Output: 10},
	{Prefix: "gpt-5", Input: 1.25, CachedInput: 0.125, Output: 10},
	{Prefix: "gpt-4.1-mini", Input: 0.4, CachedInput: 0.1, Output: 1.6},
	{Prefix: "gpt-4.1-nano", Input: 0.1, CachedInput: 0.025, Output: 0.4},
	{Prefix: "gpt-4.1", Input: 2, CachedInput: 0.5, Output: 8},
	{Prefix: "gpt-4o-mini", Input: 0.15, CachedInput: 0.075, Output: 0.6},
	{Prefix: "gpt-4o", Input: 2.5, CachedInput: 1.25, Output: 10},
	{Prefix: "o3-pro", Input: 20, Output: 80},
	{Prefix: "o3-mini", Input: 1.1, CachedInput: 0.55, Output: 4.4},
	{Prefix: "o4-mini", Input: 1.1, CachedInput: 0.275, Output: 4.4},
	{Prefix: "o3", Input: 2, CachedInput: 0.5, Output: 8},
	{Prefix: "o1-pro", Input: 150, Output: 600},
	{Prefix: "o1", Input: 15, CachedInput: 7.5, Output: 60},
}

func officialModelPricing(provider ModelProviderSetting, modelID string, inputTokens int64) (usagePricingSnapshot, bool) {
	if officialBillingProviderKind(provider) != "openai" || inputTokens < 0 {
		return usagePricingSnapshot{}, false
	}
	modelID = strings.ToLower(strings.TrimSpace(modelID))
	for _, entry := range openAITextPricingCatalog {
		if !catalogModelMatches(modelID, entry.Prefix) {
			continue
		}
		return usagePricingSnapshot{
			Source:                   "official-catalog",
			Version:                  openAITextPricingCatalogVersion,
			InputPricePerMillion:     entry.Input,
			OutputPricePerMillion:    entry.Output,
			CacheHitPricePerMillion:  entry.CachedInput,
			CacheMissPricePerMillion: entry.Input,
		}, true
	}
	return usagePricingSnapshot{}, false
}

func catalogModelMatches(modelID, prefix string) bool {
	return modelID == prefix || strings.HasPrefix(modelID, prefix+"-")
}
