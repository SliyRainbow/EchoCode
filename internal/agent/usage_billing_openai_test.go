package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/storage"
)

func TestFetchOpenAIBillingSnapshotReadsCostsAndTokenBreakdown(t *testing.T) {
	start := time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 2)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer billing-key" {
			t.Fatalf("authorization = %q", got)
		}
		query := request.URL.Query()
		if got := query.Get("start_time"); got != strconv.FormatInt(start.Unix(), 10) {
			t.Fatalf("start_time = %q", got)
		}
		if got := query.Get("end_time"); got != strconv.FormatInt(end.Unix(), 10) {
			t.Fatalf("end_time = %q", got)
		}
		if got := query.Get("project_ids"); got != "proj_echocode" {
			t.Fatalf("project_ids = %q", got)
		}
		if got := query.Get("api_key_ids"); got != "key_echocode" {
			t.Fatalf("api_key_ids = %q", got)
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/organization/costs":
			if got := query.Get("limit"); got != strconv.Itoa(openAIBillingCostPageLimit) {
				t.Fatalf("cost limit = %q", got)
			}
			_, _ = writer.Write([]byte(`{"data":[{"results":[{"amount":{"value":1.25,"currency":"usd"}},{"amount":{"value":0.75,"currency":"usd"}}]}],"has_more":false}`))
		case "/v1/organization/usage/completions":
			if got := query.Get("group_by"); got != "model" {
				t.Fatalf("group_by = %q", got)
			}
			if got := query.Get("limit"); got != strconv.Itoa(openAIBillingUsagePageLimit) {
				t.Fatalf("usage limit = %q", got)
			}
			_, _ = writer.Write([]byte(`{"data":[{"results":[{"model":"gpt-test","input_tokens":1000,"output_tokens":250,"input_cached_tokens":600,"input_cache_write_tokens":80,"input_uncached_tokens":320,"input_text_tokens":800,"output_text_tokens":200,"input_cached_text_tokens":500,"input_audio_tokens":150,"input_cached_audio_tokens":50,"output_audio_tokens":30,"input_image_tokens":50,"input_cached_image_tokens":50,"output_image_tokens":20,"num_model_requests":3},{"model":"gpt-test","input_tokens":500,"output_tokens":125,"input_cached_tokens":0,"input_cache_write_tokens":0,"input_uncached_tokens":500,"input_text_tokens":500,"output_text_tokens":125,"num_model_requests":1},{"model":"gpt-other","input_tokens":200,"output_tokens":50,"input_cached_tokens":100,"input_cache_write_tokens":10,"input_uncached_tokens":90,"input_text_tokens":150,"output_text_tokens":40,"input_cached_text_tokens":80,"input_audio_tokens":50,"input_cached_audio_tokens":20,"output_audio_tokens":10,"num_model_requests":2}]}],"has_more":false}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	snapshot, err := fetchOpenAIBillingSnapshot(context.Background(), server.Client(), server.URL+"/v1", "billing-key", ModelProviderSetting{
		BillingProjectID: "proj_echocode",
		BillingAPIKeyID:  "key_echocode",
	}, start, end)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CostUSD != 2 {
		t.Fatalf("cost = %f, want 2", snapshot.CostUSD)
	}
	if len(snapshot.Usage) != 2 {
		t.Fatalf("usage = %#v", snapshot.Usage)
	}
	if got := snapshot.Usage[0]; got.ModelID != "gpt-other" || got.InputTokens != 200 || got.OutputTokens != 50 || got.CachedTokens != 100 || got.CacheWriteTokens != 10 || got.UncachedTokens != 90 || got.InputTextTokens != 150 || got.OutputTextTokens != 40 || got.CachedTextTokens != 80 || got.InputAudioTokens != 50 || got.CachedAudioTokens != 20 || got.OutputAudioTokens != 10 || got.Requests != 2 {
		t.Fatalf("first model usage = %#v", got)
	}
	if got := snapshot.Usage[1]; got.ModelID != "gpt-test" || got.InputTokens != 1500 || got.OutputTokens != 375 || got.CachedTokens != 600 || got.CacheWriteTokens != 80 || got.UncachedTokens != 820 || got.InputTextTokens != 1300 || got.OutputTextTokens != 325 || got.CachedTextTokens != 500 || got.InputAudioTokens != 150 || got.CachedAudioTokens != 50 || got.OutputAudioTokens != 30 || got.InputImageTokens != 50 || got.CachedImageTokens != 50 || got.OutputImageTokens != 20 || got.Requests != 4 {
		t.Fatalf("second model usage = %#v", got)
	}
}

func TestUsageBillingReportDoesNotVerifyUnscopedOpenAICosts(t *testing.T) {
	provider := ModelProviderSetting{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1"}
	report := usageBillingReportFromRecord(newUsageBillingReport(provider), storage.BillingReconciliation{
		PeriodStart:   time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:     time.Date(2026, time.July, 2, 0, 0, 0, 0, time.UTC),
		OfficialCost:  1,
		EstimatedCost: 1,
		Difference:    0,
		Source:        "openai-costs-api",
		UpdatedAt:     time.Date(2026, time.July, 3, 0, 0, 0, 0, time.UTC),
	})
	if report.Status != "scope_required" || report.Verified {
		t.Fatalf("unscoped report = %#v", report)
	}
}

func TestUsageBillingReportVerifiesScopedOpenAICosts(t *testing.T) {
	provider := ModelProviderSetting{ID: "openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1", BillingProjectID: "proj_echocode"}
	report := usageBillingReportFromRecord(newUsageBillingReport(provider), storage.BillingReconciliation{
		PeriodStart:   time.Date(2026, time.July, 1, 0, 0, 0, 0, time.UTC),
		PeriodEnd:     time.Date(2026, time.July, 2, 0, 0, 0, 0, time.UTC),
		OfficialCost:  4,
		EstimatedCost: 2.25,
		Difference:    1.75,
		Source:        "openai-costs-api",
		UpdatedAt:     time.Date(2026, time.July, 3, 0, 0, 0, 0, time.UTC),
	})
	if report.Status != "verified" || !report.Verified || report.AbsoluteDifference != 1.75 {
		t.Fatalf("scoped report = %#v", report)
	}
}
