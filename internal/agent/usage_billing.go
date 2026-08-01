package agent

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/storage"
)

const (
	usageBillingVerifiedToleranceUSD = 2.0
	usageBillingWarningToleranceUSD  = 3.0
)

// UsageBillingReport describes whether a locally estimated amount has been
// reconciled against an amount from the actual provider billing surface.
// It deliberately keeps "estimated" and "verified" as distinct states.
type UsageBillingReport struct {
	ProviderID                string                   `json:"providerId"`
	ProviderName              string                   `json:"providerName"`
	ProviderKind              string                   `json:"providerKind"`
	OfficialProvider          bool                     `json:"officialProvider"`
	Status                    string                   `json:"status"`
	Message                   string                   `json:"message"`
	Verified                  bool                     `json:"verified"`
	ToleranceUSD              float64                  `json:"toleranceUsd"`
	WarningToleranceUSD       float64                  `json:"warningToleranceUsd"`
	PeriodStart               string                   `json:"periodStart,omitempty"`
	PeriodEnd                 string                   `json:"periodEnd,omitempty"`
	EstimatedCost             float64                  `json:"estimatedCost"`
	OfficialCost              float64                  `json:"officialCost"`
	Difference                float64                  `json:"difference"`
	AbsoluteDifference        float64                  `json:"absoluteDifference"`
	ReconciliationSource      string                   `json:"reconciliationSource,omitempty"`
	ReconciledAt              string                   `json:"reconciledAt,omitempty"`
	RecommendedSource         string                   `json:"recommendedSource"`
	Scope                     string                   `json:"scope"`
	ScopeConfigured           bool                     `json:"scopeConfigured"`
	OfficialUsage             []UsageBillingModelUsage `json:"officialUsage,omitempty"`
	OfficialInputTokens       int64                    `json:"officialInputTokens"`
	OfficialOutputTokens      int64                    `json:"officialOutputTokens"`
	OfficialCachedTokens      int64                    `json:"officialCachedTokens"`
	OfficialCacheWriteTokens  int64                    `json:"officialCacheWriteTokens"`
	OfficialUncachedTokens    int64                    `json:"officialUncachedTokens"`
	OfficialInputTextTokens   int64                    `json:"officialInputTextTokens"`
	OfficialOutputTextTokens  int64                    `json:"officialOutputTextTokens"`
	OfficialCachedTextTokens  int64                    `json:"officialCachedTextTokens"`
	OfficialInputAudioTokens  int64                    `json:"officialInputAudioTokens"`
	OfficialCachedAudioTokens int64                    `json:"officialCachedAudioTokens"`
	OfficialOutputAudioTokens int64                    `json:"officialOutputAudioTokens"`
	OfficialInputImageTokens  int64                    `json:"officialInputImageTokens"`
	OfficialCachedImageTokens int64                    `json:"officialCachedImageTokens"`
	OfficialOutputImageTokens int64                    `json:"officialOutputImageTokens"`
	OfficialRequests          int64                    `json:"officialRequests"`
}

// UsageBillingModelUsage is the token breakdown returned by an official
// provider usage API. InputTokens is already the total input token count; its
// cache fields are explanatory subcategories and must not be summed into it.
type UsageBillingModelUsage struct {
	ModelID           string `json:"modelId"`
	InputTokens       int64  `json:"inputTokens"`
	OutputTokens      int64  `json:"outputTokens"`
	CachedTokens      int64  `json:"cachedTokens"`
	CacheWriteTokens  int64  `json:"cacheWriteTokens"`
	UncachedTokens    int64  `json:"uncachedTokens"`
	InputTextTokens   int64  `json:"inputTextTokens"`
	OutputTextTokens  int64  `json:"outputTextTokens"`
	CachedTextTokens  int64  `json:"cachedTextTokens"`
	InputAudioTokens  int64  `json:"inputAudioTokens"`
	CachedAudioTokens int64  `json:"cachedAudioTokens"`
	OutputAudioTokens int64  `json:"outputAudioTokens"`
	InputImageTokens  int64  `json:"inputImageTokens"`
	CachedImageTokens int64  `json:"cachedImageTokens"`
	OutputImageTokens int64  `json:"outputImageTokens"`
	Requests          int64  `json:"requests"`
}

// UsageBillingReconciliationInput uses RFC3339 timestamps for a half-open
// interval [periodStart, periodEnd). Both values must use the same official
// billing timezone boundary chosen by the caller.
type UsageBillingReconciliationInput struct {
	ProviderID   string  `json:"providerId"`
	PeriodStart  string  `json:"periodStart"`
	PeriodEnd    string  `json:"periodEnd"`
	OfficialCost float64 `json:"officialCost"`
	Source       string  `json:"source"`
	Note         string  `json:"note,omitempty"`
}

type usageBillingStore interface {
	UsageCostForProviderPeriod(providerID string, start, end time.Time) (float64, error)
	UpsertBillingReconciliation(storage.BillingReconciliation) (storage.BillingReconciliation, error)
	LatestBillingReconciliation(providerID string) (storage.BillingReconciliation, error)
}

type usagePricingSnapshot struct {
	Source                   string
	Version                  string
	InputPricePerMillion     float64
	OutputPricePerMillion    float64
	CacheHitPricePerMillion  float64
	CacheMissPricePerMillion float64
}

// usagePricingSnapshotFor freezes the effective rate beside each request. A
// user-supplied rate always wins, because it can represent a contract, batch
// tier, regional endpoint, or other pricing different from the public list.
// Otherwise a known official provider/model uses the bundled, versioned catalog.
func usagePricingSnapshotFor(provider ModelProviderSetting, modelID string, inputTokens int64) usagePricingSnapshot {
	snapshot := usagePricingSnapshot{
		InputPricePerMillion:     provider.InputPricePerMillion,
		OutputPricePerMillion:    provider.OutputPricePerMillion,
		CacheHitPricePerMillion:  provider.CacheHitPricePerMillion,
		CacheMissPricePerMillion: provider.CacheMissPricePerMillion,
	}
	if snapshot.InputPricePerMillion != 0 ||
		snapshot.OutputPricePerMillion != 0 ||
		snapshot.CacheHitPricePerMillion != 0 ||
		snapshot.CacheMissPricePerMillion != 0 {
		snapshot.Source = "configured-rate"
		snapshot.Version = "provider-rate-v1"
		return snapshot
	}
	if catalog, ok := officialModelPricing(provider, modelID, inputTokens); ok {
		return catalog
	}
	snapshot.Source = "unpriced"
	return snapshot
}

func (s *Service) UsageBillingReport(providerID string) (UsageBillingReport, error) {
	provider, err := s.usageBillingProvider(providerID)
	if err != nil {
		return UsageBillingReport{}, err
	}
	report := newUsageBillingReport(provider)
	store, ok := s.usageStore.(usageBillingStore)
	if !ok || store == nil {
		report.Status = "unavailable"
		report.Message = "本地用量账本不支持账单对账。"
		return report, nil
	}
	record, err := store.LatestBillingReconciliation(provider.ID)
	if errors.Is(err, sql.ErrNoRows) {
		return report, nil
	}
	if err != nil {
		return UsageBillingReport{}, fmt.Errorf("read usage billing reconciliation: %w", err)
	}
	return usageBillingReportFromRecord(report, record), nil
}

func (s *Service) ReconcileUsageBilling(input UsageBillingReconciliationInput) (UsageBillingReport, error) {
	provider, err := s.usageBillingProvider(input.ProviderID)
	if err != nil {
		return UsageBillingReport{}, err
	}
	store, ok := s.usageStore.(usageBillingStore)
	if !ok || store == nil {
		return UsageBillingReport{}, errors.New("本地用量账本不支持账单对账")
	}
	start, err := parseUsageBillingTime(input.PeriodStart)
	if err != nil {
		return UsageBillingReport{}, fmt.Errorf("账单开始时间无效: %w", err)
	}
	end, err := parseUsageBillingTime(input.PeriodEnd)
	if err != nil {
		return UsageBillingReport{}, fmt.Errorf("账单结束时间无效: %w", err)
	}
	if !end.After(start) {
		return UsageBillingReport{}, errors.New("账单结束时间必须晚于开始时间")
	}
	if input.OfficialCost < 0 {
		return UsageBillingReport{}, errors.New("官方账单金额不能为负数")
	}
	estimatedCost, err := store.UsageCostForProviderPeriod(provider.ID, start, end)
	if err != nil {
		return UsageBillingReport{}, fmt.Errorf("calculate local usage estimate: %w", err)
	}
	record, err := store.UpsertBillingReconciliation(storage.BillingReconciliation{
		ProviderID:    provider.ID,
		ProviderName:  provider.Name,
		PeriodStart:   start,
		PeriodEnd:     end,
		OfficialCost:  input.OfficialCost,
		EstimatedCost: estimatedCost,
		Source:        strings.TrimSpace(input.Source),
		Note:          strings.TrimSpace(input.Note),
	})
	if err != nil {
		return UsageBillingReport{}, fmt.Errorf("save usage billing reconciliation: %w", err)
	}
	return usageBillingReportFromRecord(newUsageBillingReport(provider), record), nil
}

func (s *Service) usageBillingProvider(providerID string) (ModelProviderSetting, error) {
	providerID = strings.TrimSpace(providerID)
	s.stateMu.RLock()
	settings := s.runtimeSettings
	s.stateMu.RUnlock()
	if providerID == "" {
		providerID = settings.Model.SelectedProviderID
	}
	provider, _, ok := findModelProvider(settings.Model.Providers, providerID)
	if !ok {
		return ModelProviderSetting{}, fmt.Errorf("未找到模型提供商：%s", providerID)
	}
	return provider, nil
}

func newUsageBillingReport(provider ModelProviderSetting) UsageBillingReport {
	kind := officialBillingProviderKind(provider)
	report := UsageBillingReport{
		ProviderID:          provider.ID,
		ProviderName:        provider.Name,
		ProviderKind:        kind,
		OfficialProvider:    kind != "",
		Status:              "needs_reconciliation",
		Message:             "尚未导入该供应商的官方账单周期，当前金额只能作为本地估算。",
		ToleranceUSD:        usageBillingVerifiedToleranceUSD,
		WarningToleranceUSD: usageBillingWarningToleranceUSD,
		RecommendedSource:   usageBillingRecommendation(kind),
		Scope:               usageBillingScope(provider, kind),
		ScopeConfigured:     usageBillingScopeConfigured(provider, kind),
	}
	if kind == "" {
		report.Status = "custom_provider"
		report.Message = "自定义或中转供应商不会自动标记为官方准确费用；请先填写实际费率，再按供应商账单对账。"
	}
	return report
}

func usageBillingReportFromRecord(report UsageBillingReport, record storage.BillingReconciliation) UsageBillingReport {
	report.PeriodStart = record.PeriodStart.UTC().Format(time.RFC3339)
	report.PeriodEnd = record.PeriodEnd.UTC().Format(time.RFC3339)
	report.EstimatedCost = record.EstimatedCost
	report.OfficialCost = record.OfficialCost
	report.Difference = record.Difference
	report.AbsoluteDifference = math.Abs(record.Difference)
	report.ReconciliationSource = record.Source
	report.ReconciledAt = record.UpdatedAt.UTC().Format(time.RFC3339)
	if rawUsage := strings.TrimSpace(record.UsageDetailsJSON); rawUsage != "" {
		if err := json.Unmarshal([]byte(rawUsage), &report.OfficialUsage); err == nil {
			for _, usage := range report.OfficialUsage {
				report.OfficialInputTokens += usage.InputTokens
				report.OfficialOutputTokens += usage.OutputTokens
				report.OfficialCachedTokens += usage.CachedTokens
				report.OfficialCacheWriteTokens += usage.CacheWriteTokens
				report.OfficialUncachedTokens += usage.UncachedTokens
				report.OfficialInputTextTokens += usage.InputTextTokens
				report.OfficialOutputTextTokens += usage.OutputTextTokens
				report.OfficialCachedTextTokens += usage.CachedTextTokens
				report.OfficialInputAudioTokens += usage.InputAudioTokens
				report.OfficialCachedAudioTokens += usage.CachedAudioTokens
				report.OfficialOutputAudioTokens += usage.OutputAudioTokens
				report.OfficialInputImageTokens += usage.InputImageTokens
				report.OfficialCachedImageTokens += usage.CachedImageTokens
				report.OfficialOutputImageTokens += usage.OutputImageTokens
				report.OfficialRequests += usage.Requests
			}
		}
	}
	switch {
	case !report.OfficialProvider:
		report.Status = "custom_reconciled"
		report.Message = "已保存供应商账单对账结果；自定义或中转渠道仍不标记为官方准确费用。"
	case report.ProviderKind == "openai" && !report.ScopeConfigured:
		report.Status = "scope_required"
		report.Message = "已读取 OpenAI 组织账单，但未限定项目或 API Key 范围，不能用于核验 EchoCode 的单独费用。"
	case report.AbsoluteDifference <= usageBillingVerifiedToleranceUSD:
		report.Status = "verified"
		report.Verified = true
		report.Message = "已与官方账单核验，误差不超过 $2。"
	case report.AbsoluteDifference <= usageBillingWarningToleranceUSD:
		report.Status = "warning"
		report.Message = "与官方账单的误差介于 $2 和 $3，建议检查模型单价、缓存计费和账单周期。"
	default:
		report.Status = "outside_tolerance"
		report.Message = "与官方账单的误差超过 $3，不能作为准确费用展示。"
	}
	return report
}

func parseUsageBillingTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, errors.New("不能为空")
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func officialBillingProviderKind(provider ModelProviderSetting) string {
	baseURL := strings.TrimSpace(provider.BaseURL)
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return ""
	}
	host := strings.ToLower(parsed.Hostname())
	switch host {
	case "api.openai.com":
		return "openai"
	case "api.anthropic.com":
		return "anthropic"
	case "generativelanguage.googleapis.com":
		return "gemini"
	case "api.x.ai":
		return "xai"
	case "api.deepseek.com":
		return "deepseek"
	default:
		return ""
	}
}

func usageBillingRecommendation(kind string) string {
	switch kind {
	case "openai":
		return "使用 OpenAI 组织管理员账单/Costs API 对账。"
	case "anthropic":
		return "使用 Anthropic 管理员用量或成本报表对账。"
	case "gemini":
		return "使用 Google Cloud Billing 报表或导出数据对账。"
	case "xai":
		return "使用 xAI 官方控制台或可用的账单导出对账。"
	case "deepseek":
		return "使用 DeepSeek 官方控制台账单或余额流水对账。"
	default:
		return "请使用该供应商的实际账单或账单导出对账。"
	}
}

func usageBillingScopeConfigured(provider ModelProviderSetting, kind string) bool {
	if kind != "openai" {
		return true
	}
	return strings.TrimSpace(provider.BillingProjectID) != "" || strings.TrimSpace(provider.BillingAPIKeyID) != ""
}

func usageBillingScope(provider ModelProviderSetting, kind string) string {
	if kind != "openai" {
		return "供应商账单周期"
	}
	if projectID := strings.TrimSpace(provider.BillingProjectID); projectID != "" {
		return "OpenAI 项目 " + projectID
	}
	if apiKeyID := strings.TrimSpace(provider.BillingAPIKeyID); apiKeyID != "" {
		return "OpenAI API Key " + apiKeyID
	}
	return "整个 OpenAI 组织"
}
