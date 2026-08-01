package agent

import (
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/cache"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/storage"
)

const persistedUsageHistoryLimit = 24

type UsageStore interface {
	Path() string
	AppendUsage(storage.UsageRecord) error
	RecentUsage(sessionID string, limit int) ([]storage.UsageRecord, error)
	Totals(sessionID string) (storage.UsageTotals, error)
	Close() error
}

type UsageLedgerState struct {
	Enabled              bool    `json:"enabled"`
	Path                 string  `json:"path,omitempty"`
	SessionSamples       int64   `json:"sessionSamples"`
	TotalSamples         int64   `json:"totalSamples"`
	SessionInputTokens   int64   `json:"sessionInputTokens"`
	SessionOutputTokens  int64   `json:"sessionOutputTokens"`
	TotalInputTokens     int64   `json:"totalInputTokens"`
	TotalOutputTokens    int64   `json:"totalOutputTokens"`
	SessionEffectiveCost float64 `json:"sessionEffectiveCost"`
	TotalEffectiveCost   float64 `json:"totalEffectiveCost"`
	LastRecordedAt       string  `json:"lastRecordedAt,omitempty"`
	LastError            string  `json:"lastError,omitempty"`
}

// LiveUsageState is the small, task-scoped state patch sent while a detached
// session runtime is active. Sending only usage fields avoids replacing the
// application's active project/session state with a background runtime.
type LiveUsageState struct {
	UsageMetrics     cache.UsageMetrics   `json:"usageMetrics"`
	CacheHitRate     float64              `json:"cacheHitRate"`
	CacheHealth      cache.Health         `json:"cacheHealth"`
	DeepSeekSession  DeepSeekSessionState `json:"deepSeekSession"`
	CacheDiagnostics []string             `json:"cacheDiagnostics"`
	UsageLedger      UsageLedgerState     `json:"usageLedger"`
}

func usageMetricsFor(provider ModelProviderSetting, modelID string, usage *protocol.TokenUsage) cache.UsageMetrics {
	if usage == nil {
		return cache.UsageMetrics{}
	}
	metrics := cache.UsageMetrics{
		PromptCacheHitTokens:  usage.PromptCacheHitTokens,
		PromptCacheMissTokens: usage.PromptCacheMissTokens,
		InputTokens:           usage.PromptTokens,
		OutputTokens:          usage.CompletionTokens,
	}
	metrics.EffectiveCost = estimateUsageCost(usagePricingSnapshotFor(provider, modelID, metrics.InputTokens), metrics)
	return metrics
}

func estimateUsageCost(pricing usagePricingSnapshot, metrics cache.UsageMetrics) float64 {
	regularInput := metrics.InputTokens - metrics.PromptCacheHitTokens - metrics.PromptCacheMissTokens
	if regularInput < 0 {
		regularInput = 0
	}
	cacheHitPrice := pricing.CacheHitPricePerMillion
	if cacheHitPrice == 0 {
		cacheHitPrice = pricing.InputPricePerMillion
	}
	cacheMissPrice := pricing.CacheMissPricePerMillion
	if cacheMissPrice == 0 {
		cacheMissPrice = pricing.InputPricePerMillion
	}
	const perMillion = 1_000_000
	return (float64(regularInput)*pricing.InputPricePerMillion +
		float64(metrics.PromptCacheHitTokens)*cacheHitPrice +
		float64(metrics.PromptCacheMissTokens)*cacheMissPrice +
		float64(metrics.OutputTokens)*pricing.OutputPricePerMillion) / perMillion
}

func (s *Service) recordLiveUsage(usage *protocol.TokenUsage, route chatRoute, sink ChatEventSink) cache.UsageMetrics {
	metrics := usageMetricsFor(route.Provider, route.ModelID, usage)
	s.metrics = metrics
	s.recordUsageMetrics(metrics, route)
	snapshot := s.liveUsageState()
	emitChatEvent(sink, ChatStreamEvent{Type: "usage_state", Usage: usage, UsageState: &snapshot})
	return metrics
}

func (s *Service) liveUsageState() LiveUsageState {
	history := append([]cache.UsageMetrics(nil), s.metricsHistory...)
	return LiveUsageState{
		UsageMetrics:     s.metrics,
		CacheHitRate:     s.metrics.CacheHitRate(),
		CacheHealth:      cache.AnalyzeHistory(history),
		DeepSeekSession:  s.deepSeekSessionState(),
		CacheDiagnostics: cache.DiagnosticsHistory(history),
		UsageLedger:      s.usageLedger,
	}
}

func (s *Service) restoreUsageMetrics() {
	s.metrics = cache.UsageMetrics{}
	s.metricsHistory = nil
	s.sessionState.SessionCacheHitTokens = 0
	s.sessionState.SessionCacheMissTokens = 0
	s.sessionState.SessionCacheHitRate = 0
	if s.usageStore == nil {
		return
	}

	s.usageLedger.Enabled = true
	s.usageLedger.Path = s.usageStore.Path()
	records, err := s.usageStore.RecentUsage(s.sessionID, persistedUsageHistoryLimit)
	if err != nil {
		s.usageLedger.LastError = err.Error()
		return
	}
	for _, record := range records {
		metrics := cache.UsageMetrics{
			PromptCacheHitTokens:  record.PromptCacheHitTokens,
			PromptCacheMissTokens: record.PromptCacheMissTokens,
			InputTokens:           record.InputTokens,
			OutputTokens:          record.OutputTokens,
			EffectiveCost:         record.EffectiveCost,
		}
		s.metrics = metrics
		if metrics.HasCacheTokens() {
			s.metricsHistory = append(s.metricsHistory, metrics)
		}
		s.sessionState.SessionCacheHitTokens += metrics.PromptCacheHitTokens
		s.sessionState.SessionCacheMissTokens += metrics.PromptCacheMissTokens
	}
	if len(s.metricsHistory) > 6 {
		s.metricsHistory = s.metricsHistory[len(s.metricsHistory)-6:]
	}
	s.sessionState.SessionCacheHitRate = cache.UsageMetrics{
		PromptCacheHitTokens:  s.sessionState.SessionCacheHitTokens,
		PromptCacheMissTokens: s.sessionState.SessionCacheMissTokens,
	}.CacheHitRate()
	s.refreshUsageLedgerTotals(records)
}

func (s *Service) refreshUsageLedgerTotals(sessionRecords []storage.UsageRecord) {
	if s.usageStore == nil {
		return
	}
	sessionTotals, sessionErr := s.usageStore.Totals(s.sessionID)
	if sessionErr != nil {
		// Keep the recent in-memory sample useful even when the aggregate query is
		// temporarily unavailable (for example while another process migrates DB).
		sessionTotals = storage.UsageTotals{}
		for _, record := range sessionRecords {
			sessionTotals.Samples++
			sessionTotals.InputTokens += record.InputTokens
			sessionTotals.OutputTokens += record.OutputTokens
			sessionTotals.EffectiveCost += record.EffectiveCost
			if record.CreatedAt.Format(time.RFC3339Nano) > sessionTotals.LastRecordedAt {
				sessionTotals.LastRecordedAt = record.CreatedAt.Format(time.RFC3339Nano)
			}
		}
		s.usageLedger.LastError = sessionErr.Error()
	}
	if totals, err := s.usageStore.Totals(""); err == nil {
		s.usageLedger.TotalSamples = totals.Samples
		s.usageLedger.TotalInputTokens = totals.InputTokens
		s.usageLedger.TotalOutputTokens = totals.OutputTokens
		s.usageLedger.TotalEffectiveCost = totals.EffectiveCost
		if totals.LastRecordedAt != "" {
			s.usageLedger.LastRecordedAt = totals.LastRecordedAt
		}
	} else {
		s.usageLedger.LastError = err.Error()
	}
	s.usageLedger.SessionSamples = sessionTotals.Samples
	s.usageLedger.SessionInputTokens = sessionTotals.InputTokens
	s.usageLedger.SessionOutputTokens = sessionTotals.OutputTokens
	s.usageLedger.SessionEffectiveCost = sessionTotals.EffectiveCost
	if s.usageLedger.LastRecordedAt == "" {
		s.usageLedger.LastRecordedAt = sessionTotals.LastRecordedAt
	}
}

func (s *Service) recordUsageMetrics(metrics cache.UsageMetrics, route chatRoute) {
	if metrics.HasCacheTokens() {
		s.sessionState.SessionCacheHitTokens += metrics.PromptCacheHitTokens
		s.sessionState.SessionCacheMissTokens += metrics.PromptCacheMissTokens
		s.sessionState.SessionCacheHitRate = cache.UsageMetrics{
			PromptCacheHitTokens:  s.sessionState.SessionCacheHitTokens,
			PromptCacheMissTokens: s.sessionState.SessionCacheMissTokens,
		}.CacheHitRate()
		s.metricsHistory = append(s.metricsHistory, metrics)
		if len(s.metricsHistory) > 6 {
			s.metricsHistory = s.metricsHistory[len(s.metricsHistory)-6:]
		}
	}
	if s.usageStore == nil || (metrics.InputTokens == 0 && metrics.OutputTokens == 0 && !metrics.HasCacheTokens()) {
		return
	}

	recordedAt := time.Now().UTC()
	pricing := usagePricingSnapshotFor(route.Provider, route.ModelID, metrics.InputTokens)
	record := storage.UsageRecord{
		CreatedAt:                recordedAt,
		SessionID:                strings.TrimSpace(s.sessionID),
		ProviderID:               route.Provider.ID,
		ProviderName:             route.Provider.Name,
		Protocol:                 route.Provider.Protocol,
		ModelID:                  route.ModelID,
		Reasoning:                string(s.reasoning),
		PromptCacheHitTokens:     metrics.PromptCacheHitTokens,
		PromptCacheMissTokens:    metrics.PromptCacheMissTokens,
		InputTokens:              metrics.InputTokens,
		OutputTokens:             metrics.OutputTokens,
		EffectiveCost:            metrics.EffectiveCost,
		PricingSource:            pricing.Source,
		PricingVersion:           pricing.Version,
		InputPricePerMillion:     pricing.InputPricePerMillion,
		OutputPricePerMillion:    pricing.OutputPricePerMillion,
		CacheHitPricePerMillion:  pricing.CacheHitPricePerMillion,
		CacheMissPricePerMillion: pricing.CacheMissPricePerMillion,
	}
	if err := s.usageStore.AppendUsage(record); err != nil {
		s.usageLedger.LastError = err.Error()
		return
	}
	s.usageLedger.Enabled = true
	s.usageLedger.Path = s.usageStore.Path()
	s.usageLedger.LastError = ""
	s.usageLedger.LastRecordedAt = recordedAt.Format(time.RFC3339Nano)
	s.usageLedger.SessionSamples++
	s.usageLedger.TotalSamples++
	s.usageLedger.SessionInputTokens += metrics.InputTokens
	s.usageLedger.SessionOutputTokens += metrics.OutputTokens
	s.usageLedger.TotalInputTokens += metrics.InputTokens
	s.usageLedger.TotalOutputTokens += metrics.OutputTokens
	s.usageLedger.SessionEffectiveCost += metrics.EffectiveCost
	s.usageLedger.TotalEffectiveCost += metrics.EffectiveCost
}
