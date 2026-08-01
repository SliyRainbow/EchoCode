package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/storage"
)

// UsageBillingSyncInput requests an official usage and cost refresh for one
// complete, half-open billing period. Timestamps use RFC3339 UTC values.
type UsageBillingSyncInput struct {
	ProviderID  string `json:"providerId"`
	PeriodStart string `json:"periodStart"`
	PeriodEnd   string `json:"periodEnd"`
}

// SyncUsageBilling fetches a provider's official usage and cost data when the
// provider exposes a supported billing API. It falls back from an optional
// billing credential to the user's normal inference key, so users with an
// appropriately scoped ordinary key do not need to enter it twice.
func (s *Service) SyncUsageBilling(ctx context.Context, input UsageBillingSyncInput) (UsageBillingReport, error) {
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

	kind := officialBillingProviderKind(provider)
	if kind == "" {
		return UsageBillingReport{}, errors.New("自定义或中转地址不能自动读取官方账单，避免把账单凭据发送给第三方")
	}
	credential, err := s.usageBillingCredential(provider)
	if err != nil {
		return UsageBillingReport{}, err
	}

	var snapshot openAIBillingSnapshot
	switch kind {
	case "openai":
		snapshot, err = fetchOpenAIBillingSnapshot(ctx, nil, provider.BaseURL, credential, provider, start, end)
		if err != nil {
			return UsageBillingReport{}, fmt.Errorf("无法读取 OpenAI 官方账单：当前凭据可能没有组织账单权限；可使用用户自己的 OpenAI Admin Key 后重试: %w", err)
		}
	default:
		return UsageBillingReport{}, fmt.Errorf("%s 暂未提供已验证的自动账单适配；请先导入官方账单金额进行对账", provider.Name)
	}

	estimatedCost, err := store.UsageCostForProviderPeriod(provider.ID, start, end)
	if err != nil {
		return UsageBillingReport{}, fmt.Errorf("calculate local usage estimate: %w", err)
	}
	usageDetails, err := json.Marshal(snapshot.Usage)
	if err != nil {
		return UsageBillingReport{}, fmt.Errorf("encode official usage details: %w", err)
	}
	record, err := store.UpsertBillingReconciliation(storage.BillingReconciliation{
		ProviderID:       provider.ID,
		ProviderName:     provider.Name,
		PeriodStart:      start,
		PeriodEnd:        end,
		OfficialCost:     snapshot.CostUSD,
		EstimatedCost:    estimatedCost,
		Source:           "openai-costs-api",
		Note:             "由 OpenAI 官方 Costs API 与 Usage API 同步",
		UsageDetailsJSON: string(usageDetails),
	})
	if err != nil {
		return UsageBillingReport{}, fmt.Errorf("save official usage billing reconciliation: %w", err)
	}
	return usageBillingReportFromRecord(newUsageBillingReport(provider), record), nil
}

func (s *Service) usageBillingCredential(provider ModelProviderSetting) (string, error) {
	if billingKey, err := s.secretVault.Get(secretServiceName, providerBillingSecretAccountName(provider.ID)); err == nil && strings.TrimSpace(billingKey) != "" {
		return strings.TrimSpace(billingKey), nil
	}
	if inferenceKey, err := s.secretVault.Get(secretServiceName, providerSecretAccountName(provider.ID)); err == nil && strings.TrimSpace(inferenceKey) != "" {
		return strings.TrimSpace(inferenceKey), nil
	}
	return "", errors.New("请先保存账单读取凭据，或保存具有账单读取权限的 API Key")
}
