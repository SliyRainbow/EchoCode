package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultDeepSeekBaseURL = "https://api.deepseek.com"

const (
	deepSeekMaxAttempts           = 3
	deepSeekRetryDelay            = 300 * time.Millisecond
	deepSeekDialTimeout           = 15 * time.Second
	deepSeekTLSHandshakeTimeout   = 15 * time.Second
	deepSeekResponseHeaderTimeout = 90 * time.Second
	deepSeekIdleConnTimeout       = 90 * time.Second
)

type DeepSeekProvider struct {
	APIKey           string
	BaseURL          string
	HTTPClient       *http.Client
	ExtraHeaders     string
	ExtraBodyJSON    string
	ReasoningProfile string
}

func NewDeepSeekProvider(apiKey string) DeepSeekProvider {
	return DeepSeekProvider{
		APIKey:  apiKey,
		BaseURL: DefaultDeepSeekBaseURL,
	}
}

func (p DeepSeekProvider) Name() string {
	return "deepseek"
}

func (p DeepSeekProvider) ListModels(ctx context.Context) ([]Model, error) {
	if strings.TrimSpace(p.APIKey) == "" {
		return nil, errors.New("deepseek api key is required")
	}

	resp, err := p.doRequestWithRetry(ctx, http.MethodGet, "/models", nil, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, deepSeekAPIError(resp)
	}

	var payload deepSeekModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode deepseek models: %w", err)
	}

	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		if item.ID == "" {
			continue
		}
		models = append(models, Model{
			ID:          item.ID,
			DisplayName: deepSeekDisplayName(item.ID),
			Provider:    p.Name(),
		})
	}
	return models, nil
}

func (p DeepSeekProvider) Stream(ctx context.Context, request ChatRequest) (<-chan StreamEvent, error) {
	if strings.TrimSpace(p.APIKey) == "" {
		return nil, errors.New("deepseek api key is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, errors.New("deepseek model is required")
	}
	if chatRequestHasAttachments(request) {
		return nil, errors.New("DeepSeek 原生协议暂不支持图片输入，请切换到支持视觉的 OpenAI-compatible、Anthropic 或 Gemini 模型")
	}
	reasoning := reasoningOptionsForRequest("deepseek-official", p.BaseURL, p.ReasoningProfile, request)
	temperature := request.Temperature
	if reasoning.Mode == "enabled" {
		temperature = 0
	}
	body := deepSeekChatRequest{
		Model:           request.Model,
		Messages:        request.Messages,
		Temperature:     temperature,
		Stream:          true,
		StreamOptions:   &deepSeekStreamOptions{IncludeUsage: true},
		ReasoningEffort: reasoning.Effort,
		Tools:           request.Tools,
	}
	if reasoning.Mode != "" {
		body.Thinking = &deepSeekThinking{Type: reasoning.Mode}
		if reasoning.Mode == "disabled" {
			body.ReasoningEffort = ""
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	encoded, err = mergeExtraJSONBody(encoded, p.ExtraBodyJSON, protectedBodyKeys("model", "messages", "temperature", "thinking", "reasoning_effort", "stream", "stream_options", "tools"))
	if err != nil {
		return nil, err
	}

	resp, err := p.doRequestWithRetry(ctx, http.MethodPost, "/chat/completions", encoded, "text/event-stream")
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		return nil, deepSeekAPIError(resp)
	}

	events := make(chan StreamEvent, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		parseDeepSeekStream(ctx, resp.Body, events)
	}()
	return events, nil
}

// Complete 实现 ToolCaller：非流式补全，可靠解析 tool_calls。
// 用于 agent 工具循环——流式增量拼 tool_calls 容易出错，非流式一次拿全更稳。
func (p DeepSeekProvider) Complete(ctx context.Context, request ChatRequest) (CompletionResult, error) {
	if strings.TrimSpace(p.APIKey) == "" {
		return CompletionResult{}, errors.New("deepseek api key is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return CompletionResult{}, errors.New("deepseek model is required")
	}
	if chatRequestHasAttachments(request) {
		return CompletionResult{}, errors.New("DeepSeek 原生协议暂不支持图片输入，请切换到支持视觉的 OpenAI-compatible、Anthropic 或 Gemini 模型")
	}
	reasoning := reasoningOptionsForRequest("deepseek-official", p.BaseURL, p.ReasoningProfile, request)
	temperature := request.Temperature
	if reasoning.Mode == "enabled" {
		temperature = 0
	}
	body := deepSeekChatRequest{
		Model:           request.Model,
		Messages:        request.Messages,
		Temperature:     temperature,
		Stream:          false,
		ReasoningEffort: reasoning.Effort,
		Tools:           request.Tools,
	}
	if reasoning.Mode != "" {
		body.Thinking = &deepSeekThinking{Type: reasoning.Mode}
		if reasoning.Mode == "disabled" {
			body.ReasoningEffort = ""
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return CompletionResult{}, err
	}
	encoded, err = mergeExtraJSONBody(encoded, p.ExtraBodyJSON, protectedBodyKeys("model", "messages", "temperature", "thinking", "reasoning_effort", "stream", "tools"))
	if err != nil {
		return CompletionResult{}, err
	}

	resp, err := p.doRequestWithRetry(ctx, http.MethodPost, "/chat/completions", encoded, "application/json")
	if err != nil {
		return CompletionResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return CompletionResult{}, deepSeekAPIError(resp)
	}

	var payload deepSeekCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return CompletionResult{}, fmt.Errorf("decode deepseek completion: %w", err)
	}
	if len(payload.Choices) == 0 {
		return CompletionResult{}, errors.New("deepseek 返回空 choices")
	}
	msg := payload.Choices[0].Message
	result := CompletionResult{
		Content:   msg.Content,
		Reasoning: msg.ReasoningContent,
		ToolCalls: msg.ToolCalls,
	}
	if payload.Usage != nil {
		result.Usage = payload.Usage.toTokenUsage()
	}
	return result, nil
}

func (p DeepSeekProvider) endpoint(path string) string {
	baseURL := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultDeepSeekBaseURL
	}
	return baseURL + path
}

func (p DeepSeekProvider) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return defaultDeepSeekHTTPClient()
}

func defaultDeepSeekHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   deepSeekDialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          32,
			IdleConnTimeout:       deepSeekIdleConnTimeout,
			TLSHandshakeTimeout:   deepSeekTLSHandshakeTimeout,
			ResponseHeaderTimeout: deepSeekResponseHeaderTimeout,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

func (p DeepSeekProvider) applyHeaders(req *http.Request) {
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(p.APIKey))
	req.Header.Set("Content-Type", "application/json")
}

func (p DeepSeekProvider) doRequestWithRetry(ctx context.Context, method string, path string, body []byte, accept string) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= deepSeekMaxAttempts; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, p.endpoint(path), reader)
		if err != nil {
			return nil, err
		}
		p.applyHeaders(req)
		if accept != "" {
			req.Header.Set("Accept", accept)
		}
		if err := applyExtraHeaders(req, p.ExtraHeaders); err != nil {
			return nil, err
		}

		resp, err := p.client().Do(req)
		if err == nil {
			if shouldRetryDeepSeekStatus(resp.StatusCode) && attempt < deepSeekMaxAttempts {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
				_ = resp.Body.Close()
				if err := sleepBeforeDeepSeekRetry(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			return resp, nil
		}

		lastErr = err
		if !isRetryableDeepSeekNetworkError(err) || attempt == deepSeekMaxAttempts {
			return nil, deepSeekNetworkError(err, attempt)
		}
		if err := sleepBeforeDeepSeekRetry(ctx, attempt); err != nil {
			return nil, err
		}
	}
	return nil, deepSeekNetworkError(lastErr, deepSeekMaxAttempts)
}

func sleepBeforeDeepSeekRetry(ctx context.Context, attempt int) error {
	delay := time.Duration(attempt) * deepSeekRetryDelay
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func shouldRetryDeepSeekStatus(statusCode int) bool {
	return statusCode == http.StatusBadGateway ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout
}

func isRetryableDeepSeekNetworkError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return isRetryableDeepSeekNetworkError(urlErr.Err)
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "connection reset") ||
		strings.Contains(message, "server closed idle connection") ||
		strings.Contains(message, "unexpected eof")
}

func deepSeekNetworkError(err error, attempts int) error {
	if err == nil {
		return errors.New("DeepSeek 网络请求失败")
	}
	if isRetryableDeepSeekNetworkError(err) {
		return fmt.Errorf("DeepSeek 网络连接被中断，已重试 %d 次仍未成功：%w", attempts-1, err)
	}
	return fmt.Errorf("DeepSeek 网络请求失败：%w", err)
}

func parseDeepSeekStream(ctx context.Context, body io.Reader, events chan<- StreamEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	toolCalls := map[int]*streamToolCallAccumulator{}
	toolCallsEmitted := false
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- StreamEvent{Type: "error", Error: deepSeekStreamContextError()}
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			if !toolCallsEmitted {
				emitAccumulatedToolCalls(events, toolCalls)
			}
			events <- StreamEvent{Type: "done"}
			return
		}

		var chunk deepSeekStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			events <- StreamEvent{Type: "error", Error: "无法解析 DeepSeek 流式响应: " + err.Error()}
			return
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.ReasoningContent != "" {
				events <- StreamEvent{Type: "reasoning", Delta: choice.Delta.ReasoningContent}
			}
			if choice.Delta.Content != "" {
				events <- StreamEvent{Type: "delta", Delta: choice.Delta.Content}
			}
			accumulateToolCallDeltas(toolCalls, choice.Delta.ToolCalls)
			if choice.FinishReason != "" {
				if len(toolCalls) > 0 && !toolCallsEmitted {
					emitAccumulatedToolCalls(events, toolCalls)
					toolCallsEmitted = true
				}
				events <- StreamEvent{Type: "finish", FinishReason: choice.FinishReason}
			}
		}
		if chunk.Usage != nil {
			events <- StreamEvent{Type: "usage", Usage: chunk.Usage.toTokenUsage()}
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			events <- StreamEvent{Type: "error", Error: deepSeekStreamContextError()}
			return
		}
		events <- StreamEvent{Type: "error", Error: "读取 DeepSeek 流式响应失败: " + err.Error()}
		return
	}
	if !toolCallsEmitted {
		emitAccumulatedToolCalls(events, toolCalls)
	}
	events <- StreamEvent{Type: "done"}
}

func deepSeekStreamContextError() string {
	return "读取 DeepSeek 流式响应被中断：请求上下文已取消或超时。"
}

func deepSeekAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope deepSeekErrorEnvelope
	_ = json.Unmarshal(body, &envelope)

	detail := strings.TrimSpace(envelope.Error.Message)
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}
	message := deepSeekStatusMessage(resp.StatusCode)
	if detail == "" {
		return fmt.Errorf("%s (HTTP %d)", message, resp.StatusCode)
	}
	return fmt.Errorf("%s：%s (HTTP %d)", message, detail, resp.StatusCode)
}

func deepSeekStatusMessage(statusCode int) string {
	switch statusCode {
	case http.StatusBadRequest:
		return "DeepSeek 请求参数无效"
	case http.StatusUnauthorized:
		return "DeepSeek API Key 无效或缺失"
	case http.StatusPaymentRequired:
		return "DeepSeek 账户余额不足"
	case http.StatusUnprocessableEntity:
		return "DeepSeek 请求体校验失败"
	case http.StatusTooManyRequests:
		return "DeepSeek 请求触发限流"
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return "DeepSeek 服务暂时不可用"
	default:
		return "DeepSeek 请求失败"
	}
}

func deepSeekDisplayName(id string) string {
	parts := strings.Split(id, "-")
	for i, part := range parts {
		if part == "" {
			continue
		}
		switch strings.ToLower(part) {
		case "deepseek":
			parts[i] = "DeepSeek"
		case "v4":
			parts[i] = "V4"
		default:
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

type deepSeekModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

type deepSeekChatRequest struct {
	Model           string                 `json:"model"`
	Messages        []Message              `json:"messages"`
	Temperature     float64                `json:"temperature,omitempty"`
	Thinking        *deepSeekThinking      `json:"thinking,omitempty"`
	ReasoningEffort string                 `json:"reasoning_effort,omitempty"`
	Stream          bool                   `json:"stream"`
	StreamOptions   *deepSeekStreamOptions `json:"stream_options,omitempty"`
	Tools           []ToolDefinition       `json:"tools,omitempty"`
}

type deepSeekStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type deepSeekThinking struct {
	Type string `json:"type"`
}

type deepSeekStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content          string                `json:"content"`
			ReasoningContent string                `json:"reasoning_content"`
			ToolCalls        []streamToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *deepSeekUsage `json:"usage"`
}

// deepSeekCompletionResponse 是非流式补全响应（含 tool_calls）。
type deepSeekCompletionResponse struct {
	Choices []struct {
		Message struct {
			Content          string     `json:"content"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *deepSeekUsage `json:"usage"`
}

type deepSeekUsage struct {
	PromptTokens          int64 `json:"prompt_tokens"`
	CompletionTokens      int64 `json:"completion_tokens"`
	TotalTokens           int64 `json:"total_tokens"`
	PromptCacheHitTokens  int64 `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens int64 `json:"prompt_cache_miss_tokens"`
}

func (u deepSeekUsage) toTokenUsage() *TokenUsage {
	return &TokenUsage{
		PromptTokens:          u.PromptTokens,
		CompletionTokens:      u.CompletionTokens,
		TotalTokens:           u.TotalTokens,
		PromptCacheHitTokens:  u.PromptCacheHitTokens,
		PromptCacheMissTokens: u.PromptCacheMissTokens,
	}
}

type deepSeekErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}
