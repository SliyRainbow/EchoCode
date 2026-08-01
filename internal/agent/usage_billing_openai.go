package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	openAIBillingCostPageLimit  = 180
	openAIBillingUsagePageLimit = 31
	openAIBillingMaxPages       = 64
	openAIBillingMaxBodyBytes   = 4 << 20
)

var defaultOpenAIBillingHTTPClient = &http.Client{Timeout: 45 * time.Second}

type openAIBillingSnapshot struct {
	CostUSD float64
	Usage   []UsageBillingModelUsage
}

type openAIBillingPage struct {
	Data     json.RawMessage `json:"data"`
	HasMore  bool            `json:"has_more"`
	NextPage string          `json:"next_page"`
}

type openAICostBucket struct {
	Results []struct {
		Amount struct {
			Value    float64 `json:"value"`
			Currency string  `json:"currency"`
		} `json:"amount"`
	} `json:"results"`
}

type openAIUsageBucket struct {
	Results []struct {
		Model                  string `json:"model"`
		InputTokens            int64  `json:"input_tokens"`
		OutputTokens           int64  `json:"output_tokens"`
		InputCachedTokens      int64  `json:"input_cached_tokens"`
		InputCacheWriteTokens  int64  `json:"input_cache_write_tokens"`
		InputUncachedTokens    int64  `json:"input_uncached_tokens"`
		InputTextTokens        int64  `json:"input_text_tokens"`
		OutputTextTokens       int64  `json:"output_text_tokens"`
		InputCachedTextTokens  int64  `json:"input_cached_text_tokens"`
		InputAudioTokens       int64  `json:"input_audio_tokens"`
		InputCachedAudioTokens int64  `json:"input_cached_audio_tokens"`
		OutputAudioTokens      int64  `json:"output_audio_tokens"`
		InputImageTokens       int64  `json:"input_image_tokens"`
		InputCachedImageTokens int64  `json:"input_cached_image_tokens"`
		OutputImageTokens      int64  `json:"output_image_tokens"`
		ModelRequests          int64  `json:"num_model_requests"`
	} `json:"results"`
}

func fetchOpenAIBillingSnapshot(ctx context.Context, client *http.Client, baseURL, apiKey string, provider ModelProviderSetting, start, end time.Time) (openAIBillingSnapshot, error) {
	if client == nil {
		client = defaultOpenAIBillingHTTPClient
	}
	if strings.TrimSpace(apiKey) == "" {
		return openAIBillingSnapshot{}, errors.New("OpenAI 账单读取凭据不能为空")
	}
	if start.IsZero() || end.IsZero() || !end.After(start) {
		return openAIBillingSnapshot{}, errors.New("账单周期必须是有效的半开区间")
	}

	query := openAIBillingQuery(start, end, provider)
	cost, err := fetchOpenAIBillingCosts(ctx, client, baseURL, apiKey, query)
	if err != nil {
		return openAIBillingSnapshot{}, err
	}
	usage, err := fetchOpenAIBillingUsage(ctx, client, baseURL, apiKey, query)
	if err != nil {
		return openAIBillingSnapshot{}, err
	}
	return openAIBillingSnapshot{CostUSD: cost, Usage: usage}, nil
}

func openAIBillingQuery(start, end time.Time, provider ModelProviderSetting) url.Values {
	query := url.Values{}
	query.Set("start_time", strconv.FormatInt(start.UTC().Unix(), 10))
	query.Set("end_time", strconv.FormatInt(end.UTC().Unix(), 10))
	query.Set("bucket_width", "1d")
	if projectID := strings.TrimSpace(provider.BillingProjectID); projectID != "" {
		query.Add("project_ids", projectID)
	}
	if apiKeyID := strings.TrimSpace(provider.BillingAPIKeyID); apiKeyID != "" {
		query.Add("api_key_ids", apiKeyID)
	}
	return query
}

func fetchOpenAIBillingCosts(ctx context.Context, client *http.Client, baseURL, apiKey string, query url.Values) (float64, error) {
	var total float64
	costQuery := cloneURLValues(query)
	costQuery.Set("limit", strconv.Itoa(openAIBillingCostPageLimit))
	err := fetchOpenAIBillingPages(ctx, client, baseURL, apiKey, "/organization/costs", costQuery, func(page openAIBillingPage) error {
		var buckets []openAICostBucket
		if err := json.Unmarshal(page.Data, &buckets); err != nil {
			return fmt.Errorf("解析 OpenAI 成本账单失败: %w", err)
		}
		for _, bucket := range buckets {
			for _, result := range bucket.Results {
				currency := strings.ToLower(strings.TrimSpace(result.Amount.Currency))
				if currency != "" && currency != "usd" {
					return fmt.Errorf("OpenAI 成本接口返回了暂不支持的币种：%s", currency)
				}
				if result.Amount.Value < 0 {
					return errors.New("OpenAI 成本接口返回了无效的负数金额")
				}
				total += result.Amount.Value
			}
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return total, nil
}

func fetchOpenAIBillingUsage(ctx context.Context, client *http.Client, baseURL, apiKey string, query url.Values) ([]UsageBillingModelUsage, error) {
	usageQuery := cloneURLValues(query)
	usageQuery.Set("limit", strconv.Itoa(openAIBillingUsagePageLimit))
	usageQuery.Add("group_by", "model")
	byModel := map[string]UsageBillingModelUsage{}
	err := fetchOpenAIBillingPages(ctx, client, baseURL, apiKey, "/organization/usage/completions", usageQuery, func(page openAIBillingPage) error {
		var buckets []openAIUsageBucket
		if err := json.Unmarshal(page.Data, &buckets); err != nil {
			return fmt.Errorf("解析 OpenAI Token 用量失败: %w", err)
		}
		for _, bucket := range buckets {
			for _, result := range bucket.Results {
				modelID := strings.TrimSpace(result.Model)
				if modelID == "" {
					modelID = "未分组模型"
				}
				current := byModel[modelID]
				current.ModelID = modelID
				current.InputTokens += nonNegativeBillingTokens(result.InputTokens)
				current.OutputTokens += nonNegativeBillingTokens(result.OutputTokens)
				current.CachedTokens += nonNegativeBillingTokens(result.InputCachedTokens)
				current.CacheWriteTokens += nonNegativeBillingTokens(result.InputCacheWriteTokens)
				current.UncachedTokens += nonNegativeBillingTokens(result.InputUncachedTokens)
				current.InputTextTokens += nonNegativeBillingTokens(result.InputTextTokens)
				current.OutputTextTokens += nonNegativeBillingTokens(result.OutputTextTokens)
				current.CachedTextTokens += nonNegativeBillingTokens(result.InputCachedTextTokens)
				current.InputAudioTokens += nonNegativeBillingTokens(result.InputAudioTokens)
				current.CachedAudioTokens += nonNegativeBillingTokens(result.InputCachedAudioTokens)
				current.OutputAudioTokens += nonNegativeBillingTokens(result.OutputAudioTokens)
				current.InputImageTokens += nonNegativeBillingTokens(result.InputImageTokens)
				current.CachedImageTokens += nonNegativeBillingTokens(result.InputCachedImageTokens)
				current.OutputImageTokens += nonNegativeBillingTokens(result.OutputImageTokens)
				current.Requests += nonNegativeBillingTokens(result.ModelRequests)
				byModel[modelID] = current
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	models := make([]string, 0, len(byModel))
	for modelID := range byModel {
		models = append(models, modelID)
	}
	sort.Strings(models)
	result := make([]UsageBillingModelUsage, 0, len(models))
	for _, modelID := range models {
		result = append(result, byModel[modelID])
	}
	return result, nil
}

func fetchOpenAIBillingPages(ctx context.Context, client *http.Client, baseURL, apiKey, endpoint string, query url.Values, handle func(openAIBillingPage) error) error {
	query = cloneURLValues(query)
	for pageNumber := 0; pageNumber < openAIBillingMaxPages; pageNumber++ {
		payload := openAIBillingPage{}
		if err := openAIBillingGet(ctx, client, baseURL, apiKey, endpoint, query, &payload); err != nil {
			return err
		}
		if err := handle(payload); err != nil {
			return err
		}
		if !payload.HasMore {
			return nil
		}
		if strings.TrimSpace(payload.NextPage) == "" {
			return errors.New("OpenAI 账单接口标记仍有下一页，但没有返回分页游标")
		}
		query.Set("page", payload.NextPage)
	}
	return fmt.Errorf("OpenAI 账单接口分页超过 %d 页，已停止以避免重复计费", openAIBillingMaxPages)
}

func openAIBillingGet(ctx context.Context, client *http.Client, baseURL, apiKey, endpoint string, query url.Values, target any) error {
	requestURL, err := openAIBillingURL(baseURL, endpoint, query)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("创建 OpenAI 账单请求失败: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(apiKey))
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("请求 OpenAI 账单接口失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return openAIBillingAPIError(resp)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, openAIBillingMaxBodyBytes)).Decode(target); err != nil {
		return fmt.Errorf("解析 OpenAI 账单接口响应失败: %w", err)
	}
	return nil
}

func openAIBillingURL(baseURL, endpoint string, query url.Values) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("OpenAI 官方账单地址无效")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + endpoint
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}

func openAIBillingAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<10))
	message := strings.Join(strings.Fields(string(body)), " ")
	if message == "" {
		return fmt.Errorf("OpenAI 账单接口 HTTP %d", resp.StatusCode)
	}
	return fmt.Errorf("OpenAI 账单接口 HTTP %d: %s", resp.StatusCode, message)
}

func cloneURLValues(values url.Values) url.Values {
	cloned := make(url.Values, len(values))
	for key, value := range values {
		cloned[key] = append([]string(nil), value...)
	}
	return cloned
}

func nonNegativeBillingTokens(value int64) int64 {
	if value < 0 {
		return 0
	}
	return value
}
