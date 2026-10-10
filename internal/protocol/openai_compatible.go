package protocol

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

const (
	openAICompatibleMaxAttempts = 3
	openAICompatibleRetryDelay  = 300 * time.Millisecond
)

var defaultOpenAICompatibleHTTPClient = &http.Client{
	Transport: &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   15 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          32,
		MaxIdleConnsPerHost:   8,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   12 * time.Second,
		ResponseHeaderTimeout: 60 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	},
}

type OpenAICompatibleProvider struct {
	BaseURL          string
	APIKey           string
	ProviderID       string
	DisplayName      string
	ClientVersion    string
	APIType          string
	HTTPClient       *http.Client
	AllowNoAuth      bool
	ExtraHeaders     string
	ExtraBodyJSON    string
	ReasoningProfile string
}

func (p OpenAICompatibleProvider) Name() string {
	if strings.TrimSpace(p.ProviderID) != "" {
		return strings.TrimSpace(p.ProviderID)
	}
	return "openai-compatible"
}

func (p OpenAICompatibleProvider) ListModels(ctx context.Context) ([]Model, error) {
	if strings.TrimSpace(p.BaseURL) == "" {
		return nil, errors.New("OpenAI-compatible base url is required")
	}
	if strings.TrimSpace(p.APIKey) == "" && !p.AllowNoAuth {
		return nil, errors.New("OpenAI-compatible API Key is required")
	}

	resp, err := p.doRequestWithRetry(ctx, http.MethodGet, "/models", nil, "")
	if err != nil {
		return nil, fmt.Errorf("OpenAI-compatible models request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= http.StatusBadRequest {
		return nil, openAICompatibleAPIError(resp)
	}

	var payload openAIModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return nil, fmt.Errorf("decode OpenAI-compatible models: %w", err)
	}

	models := make([]Model, 0, len(payload.Data))
	for _, item := range payload.Data {
		model := openAICompatibleModelFromPayload(item, p.Name())
		if strings.TrimSpace(model.ID) == "" {
			continue
		}
		models = append(models, model)
	}
	return models, nil
}

func (p OpenAICompatibleProvider) Stream(ctx context.Context, request ChatRequest) (<-chan StreamEvent, error) {
	if strings.EqualFold(strings.TrimSpace(p.APIType), "responses") {
		return p.streamResponses(ctx, request)
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return nil, errors.New("OpenAI-compatible base url is required")
	}
	if strings.TrimSpace(p.APIKey) == "" && !p.AllowNoAuth {
		return nil, errors.New("OpenAI-compatible API Key is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, errors.New("OpenAI-compatible model is required")
	}

	reasoning := reasoningOptionsForRequest("openai-compatible", p.BaseURL, p.ReasoningProfile, request)
	if err := p.validateChatToolSupport(request, reasoning); err != nil {
		return nil, err
	}
	temperature := request.Temperature
	if reasoning.Effort != "" && reasoning.Effort != "none" {
		temperature = 0
	}
	body := openAIChatRequest{
		Model:               request.Model,
		Messages:            openAIMessagesFromProtocol(request.Messages, requiresDeepSeekReasoningReplay(p.ReasoningProfile, request)),
		Temperature:         temperature,
		ReasoningEffort:     reasoning.Effort,
		Stream:              true,
		StreamOptions:       &openAIStreamOptions{IncludeUsage: true},
		Tools:               request.Tools,
		ToolChoice:          request.ToolChoice,
		MaxCompletionTokens: request.MaxOutputTokens,
	}
	if len(request.Tools) > 0 && !request.OmitParallelToolCalls {
		body.ParallelToolCalls = &request.ParallelToolCalls
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	encoded, err = mergeExtraJSONBody(encoded, p.ExtraBodyJSON, protectedBodyKeys("model", "messages", "temperature", "reasoning_effort", "stream", "stream_options", "tools", "tool_choice", "parallel_tool_calls"))
	if err != nil {
		return nil, err
	}
	resp, err := p.doRequestWithRetry(ctx, http.MethodPost, "/chat/completions", encoded, "text/event-stream")
	if err != nil {
		return nil, fmt.Errorf("OpenAI-compatible chat request failed: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		return nil, openAICompatibleAPIError(resp)
	}

	events := make(chan StreamEvent, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		parseOpenAICompatibleStream(ctx, resp.Body, events)
	}()
	return events, nil
}

// Complete 实现 ToolCaller：非流式补全以可靠解析 tool_calls。
func (p OpenAICompatibleProvider) Complete(ctx context.Context, request ChatRequest) (CompletionResult, error) {
	if strings.EqualFold(strings.TrimSpace(p.APIType), "responses") {
		return p.completeResponses(ctx, request)
	}
	if strings.TrimSpace(p.BaseURL) == "" {
		return CompletionResult{}, errors.New("OpenAI-compatible base url is required")
	}
	if strings.TrimSpace(p.APIKey) == "" && !p.AllowNoAuth {
		return CompletionResult{}, errors.New("OpenAI-compatible API Key is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return CompletionResult{}, errors.New("OpenAI-compatible model is required")
	}

	reasoning := reasoningOptionsForRequest("openai-compatible", p.BaseURL, p.ReasoningProfile, request)
	if err := p.validateChatToolSupport(request, reasoning); err != nil {
		return CompletionResult{}, err
	}
	temperature := request.Temperature
	if reasoning.Effort != "" && reasoning.Effort != "none" {
		temperature = 0
	}
	body := openAIChatRequest{
		Model:               request.Model,
		Messages:            openAIMessagesFromProtocol(request.Messages, requiresDeepSeekReasoningReplay(p.ReasoningProfile, request)),
		Temperature:         temperature,
		ReasoningEffort:     reasoning.Effort,
		Stream:              false,
		Tools:               request.Tools,
		ToolChoice:          request.ToolChoice,
		MaxCompletionTokens: request.MaxOutputTokens,
	}
	if len(request.Tools) > 0 && !request.OmitParallelToolCalls {
		body.ParallelToolCalls = &request.ParallelToolCalls
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return CompletionResult{}, err
	}
	encoded, err = mergeExtraJSONBody(encoded, p.ExtraBodyJSON, protectedBodyKeys("model", "messages", "temperature", "reasoning_effort", "stream", "tools", "tool_choice", "parallel_tool_calls"))
	if err != nil {
		return CompletionResult{}, err
	}
	resp, err := p.doRequestWithRetry(ctx, http.MethodPost, "/chat/completions", encoded, "application/json")
	if err != nil {
		return CompletionResult{}, fmt.Errorf("OpenAI-compatible chat request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return CompletionResult{}, openAICompatibleAPIError(resp)
	}

	var payload openAICompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return CompletionResult{}, fmt.Errorf("decode OpenAI-compatible completion: %w", err)
	}
	if len(payload.Choices) == 0 {
		return CompletionResult{}, errors.New("OpenAI-compatible 返回空 choices")
	}
	msg := payload.Choices[0].Message
	result := CompletionResult{
		Content:   msg.Content,
		Reasoning: msg.ReasoningContent,
		ToolCalls: msg.ToolCalls,
	}
	if result.Content == "" {
		result.Content = msg.Refusal
	}
	if payload.Usage != nil {
		result.Usage = payload.Usage.toTokenUsage()
	}
	if failure := finishReasonFailure("openai-compatible", payload.Choices[0].FinishReason); failure != nil {
		return result, NewProviderError(*failure.ProviderError)
	}
	return result, nil
}

func (p OpenAICompatibleProvider) endpoint(path string) string {
	return strings.TrimRight(strings.TrimSpace(p.BaseURL), "/") + path
}

func (p OpenAICompatibleProvider) validateChatToolSupport(request ChatRequest, reasoning ReasoningOptions) error {
	if len(request.Tools) == 0 || reasoningEndpointHost(p.BaseURL) != "api.openai.com" {
		return nil
	}
	model := normalizeReasoningModelID(request.Model)
	requiresResponses := modelFamily(model, "gpt-6-astra") || modelFamily(model, "gpt-6.1-sol") || ((modelFamily(model, "gpt-6-sol") || modelFamily(model, "gpt-6-luna")) && reasoning.Effort != "none")
	if requiresResponses {
		return NewProviderError(ProviderErrorInfo{Provider: p.Name(), Type: "configuration_error", Code: "responses_required", Message: "该模型在当前推理设置下执行工具需要 Responses API；请在“模型与连接”中将 API 类型改为 Responses。"})
	}
	return nil
}

func (p OpenAICompatibleProvider) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return defaultOpenAICompatibleHTTPClient
}

func (p OpenAICompatibleProvider) doRequestWithRetry(
	ctx context.Context,
	method string,
	path string,
	body []byte,
	accept string,
) (*http.Response, error) {
	return p.doRequestWithRetryHeaders(ctx, method, path, body, accept, nil)
}

func (p OpenAICompatibleProvider) doRequestWithRetryHeaders(
	ctx context.Context,
	method string,
	path string,
	body []byte,
	accept string,
	internalHeaders http.Header,
) (*http.Response, error) {
	client := p.client()
	var lastErr error
	for attempt := 1; attempt <= openAICompatibleMaxAttempts; attempt++ {
		var reader io.Reader
		if body != nil {
			reader = bytes.NewReader(body)
		}
		req, err := http.NewRequestWithContext(ctx, method, p.endpoint(path), reader)
		if err != nil {
			return nil, err
		}
		p.applyHeaders(req, accept)
		// Defaults precede the provider's explicit compatibility settings.
		// Apply this on every attempt, for model discovery as well as chat.
		p.applyClientIdentity(req)
		if err := applyExtraHeaders(req, p.ExtraHeaders); err != nil {
			return nil, err
		}
		for name, values := range internalHeaders {
			for _, value := range values {
				if validInternalHeaderValue(value) {
					req.Header.Set(name, value)
				}
			}
		}

		resp, err := client.Do(req)
		if err == nil {
			if shouldRetryOpenAICompatibleStatus(resp.StatusCode) &&
				attempt < openAICompatibleMaxAttempts &&
				!openAICompatibleResponseHasNonRetryableError(resp) {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1024))
				_ = resp.Body.Close()
				if err := sleepBeforeOpenAICompatibleRetry(ctx, attempt); err != nil {
					return nil, err
				}
				continue
			}
			return resp, nil
		}

		lastErr = err
		if !isRetryableOpenAICompatibleError(err) || attempt == openAICompatibleMaxAttempts {
			return nil, err
		}
		if err := sleepBeforeOpenAICompatibleRetry(ctx, attempt); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func openAICompatibleResponseHasNonRetryableError(resp *http.Response) bool {
	if resp == nil || resp.Body == nil {
		return false
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	_ = resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(body))
	var envelope openAIErrorEnvelope
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	return isNonRetryableProviderErrorCode(strings.TrimSpace(fmt.Sprint(envelope.Error.Code)))
}

func shouldRetryOpenAICompatibleStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		statusCode == http.StatusInternalServerError ||
		statusCode == http.StatusBadGateway ||
		statusCode == http.StatusServiceUnavailable ||
		statusCode == http.StatusGatewayTimeout
}

func isRetryableOpenAICompatibleError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "connection reset") ||
		strings.Contains(message, "server closed idle connection") ||
		strings.Contains(message, "unexpected eof")
}

func sleepBeforeOpenAICompatibleRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(attempt) * openAICompatibleRetryDelay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (p OpenAICompatibleProvider) applyHeaders(req *http.Request, accept string) {
	req.Header.Set("Content-Type", "application/json")
	if strings.TrimSpace(p.APIKey) != "" {
		req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(p.APIKey))
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
}

func (p OpenAICompatibleProvider) applyClientIdentity(req *http.Request) {
	userAgent := "EchoCode"
	if version := strings.TrimSpace(p.ClientVersion); version != "" {
		userAgent += "/" + version
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("originator", "echocode")
}

func validInternalHeaderValue(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.ContainsAny(value, "\r\n")
}

func parseOpenAICompatibleStream(ctx context.Context, body io.Reader, events chan<- StreamEvent) {
	scanner := newSSEReader(body)
	toolCalls := map[int]*streamToolCallAccumulator{}
	toolCallsEmitted := false
	finishReason := ""
	var terminalFailure *StreamEvent
	for {
		frame, ok := scanner.Next()
		if !ok {
			break
		}
		select {
		case <-ctx.Done():
			events <- StreamEvent{Type: "error", Error: "读取 OpenAI-compatible 流式响应被中断：请求上下文已取消或超时。"}
			return
		default:
		}

		payload := strings.TrimSpace(frame.Data)
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			if terminalFailure != nil {
				events <- *terminalFailure
				return
			}
			if !toolCallsEmitted {
				emitAccumulatedToolCalls(events, toolCalls)
			}
			events <- StreamEvent{Type: "done"}
			return
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			events <- StreamEvent{Type: "error", Error: "无法解析 OpenAI-compatible 流式响应: " + err.Error()}
			return
		}
		if chunk.Error != nil {
			info := providerErrorFromUpstream("openai-compatible", chunk.Error, 0, "")
			events <- StreamEvent{Type: "error", Error: info.Message, ProviderError: &info}
			return
		}
		for _, choice := range chunk.Choices {
			if choice.Delta.ReasoningContent != "" {
				events <- StreamEvent{Type: "reasoning", Delta: choice.Delta.ReasoningContent}
			}
			if choice.Delta.Content != "" {
				events <- StreamEvent{Type: "delta", Delta: choice.Delta.Content}
			}
			if choice.Delta.Refusal != "" {
				events <- StreamEvent{Type: "delta", Delta: choice.Delta.Refusal}
			}
			accumulateToolCallDeltas(toolCalls, choice.Delta.ToolCalls)
			if choice.FinishReason != "" {
				finishReason = choice.FinishReason
				terminalFailure = finishReasonFailure("openai-compatible", finishReason)
				if terminalFailure == nil && len(toolCalls) > 0 && !toolCallsEmitted {
					emitAccumulatedToolCalls(events, toolCalls)
					toolCallsEmitted = true
				}
				if terminalFailure == nil {
					events <- StreamEvent{Type: "finish", FinishReason: choice.FinishReason}
				}
			}
		}
		if chunk.Usage != nil {
			events <- StreamEvent{Type: "usage", Usage: chunk.Usage.toTokenUsage()}
		}
	}
	if err := scanner.Err(); err != nil {
		events <- StreamEvent{Type: "error", Error: "读取 OpenAI-compatible 流式响应失败: " + err.Error()}
		return
	}
	if terminalFailure != nil {
		events <- *terminalFailure
		return
	}
	if finishReason == "" {
		events <- interruptedStream("openai-compatible")
		return
	}
	if !toolCallsEmitted {
		emitAccumulatedToolCalls(events, toolCalls)
	}
	events <- StreamEvent{Type: "done"}
}

func openAICompatibleAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope openAIErrorEnvelope
	_ = json.Unmarshal(body, &envelope)

	detail := compactOpenAICompatibleError(envelope.Error.Message)
	if detail == "" {
		detail = compactOpenAICompatibleError(string(body))
	}
	if detail == "" {
		detail = "OpenAI-compatible request failed"
	}
	code := strings.TrimSpace(fmt.Sprint(envelope.Error.Code))
	if code == "<nil>" {
		code = ""
	}
	return NewProviderError(ProviderErrorInfo{
		Provider:   "openai-compatible",
		HTTPStatus: resp.StatusCode,
		Type:       strings.TrimSpace(envelope.Error.Type),
		Code:       code,
		Message:    detail,
		RequestID:  responseRequestID(resp.Header),
		Retryable:  shouldRetryOpenAICompatibleStatus(resp.StatusCode) && !isNonRetryableProviderErrorCode(code),
	})
}

func isNonRetryableProviderErrorCode(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "cyber_policy", "invalid_prompt", "bio_policy":
		return true
	default:
		return false
	}
}

func responseRequestID(headers http.Header) string {
	for _, name := range []string{"x-request-id", "x-oai-request-id"} {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func compactOpenAICompatibleError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") {
		if document, err := xhtml.Parse(strings.NewReader(value)); err == nil {
			fragments := make([]string, 0, 2)
			seen := map[string]bool{}
			var walk func(*xhtml.Node)
			walk = func(node *xhtml.Node) {
				if node.Type == xhtml.ElementNode && (node.Data == "title" || node.Data == "h1") {
					text := compactHTMLNodeText(node)
					if text != "" && !seen[text] {
						seen[text] = true
						fragments = append(fragments, text)
					}
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					walk(child)
				}
			}
			walk(document)
			if len(fragments) > 0 {
				value = strings.Join(fragments, " · ")
			} else {
				value = compactHTMLNodeText(document)
			}
		}
	} else {
		value = strings.Join(strings.Fields(value), " ")
	}
	const maxRunes = 220
	runes := []rune(value)
	if len(runes) > maxRunes {
		return string(runes[:maxRunes]) + "…"
	}
	return value
}

func compactHTMLNodeText(root *xhtml.Node) string {
	if root == nil {
		return ""
	}
	var text strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.TextNode {
			text.WriteByte(' ')
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return strings.Join(strings.Fields(text.String()), " ")
}

func openAICompatibleDisplayName(id string) string {
	id = strings.TrimSpace(id)
	if id == "" {
		return id
	}
	return id
}

type openAIModelsResponse struct {
	Data []json.RawMessage `json:"data"`
}

func openAICompatibleModelFromPayload(payload json.RawMessage, providerID string) Model {
	var item map[string]any
	if err := json.Unmarshal(payload, &item); err != nil {
		return Model{}
	}
	id, _ := item["id"].(string)
	id = strings.TrimSpace(id)
	if id == "" {
		return Model{}
	}
	contextWindowTokens := openAICompatibleContextWindow(item)
	model := Model{
		ID:                  id,
		DisplayName:         openAICompatibleDisplayName(id),
		Provider:            providerID,
		ContextWindowTokens: contextWindowTokens,
	}
	if contextWindowTokens > 0 {
		model.ContextWindowSource = "upstream"
	}
	return model
}

func openAICompatibleContextWindow(item map[string]any) int {
	for _, key := range []string{
		"contextWindowTokens",
		"context_window_tokens",
		"context_length",
		"context_window",
		"max_context_length",
		"max_context_window",
		"max_model_len",
		"max_input_tokens",
		"n_ctx",
		"max_tokens",
	} {
		if value := positiveIntFromAny(item[key]); value > 0 {
			return value
		}
	}
	for _, key := range []string{"metadata", "details", "top_provider"} {
		nested, ok := item[key].(map[string]any)
		if !ok {
			continue
		}
		if value := openAICompatibleContextWindow(nested); value > 0 {
			return value
		}
	}
	return 0
}

func positiveIntFromAny(value any) int {
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return int(typed)
		}
	case int:
		if typed > 0 {
			return typed
		}
	case json.Number:
		if parsed, err := typed.Int64(); err == nil && parsed > 0 {
			return int(parsed)
		}
	case string:
		var parsed int
		if _, err := fmt.Sscanf(strings.TrimSpace(typed), "%d", &parsed); err == nil && parsed > 0 {
			return parsed
		}
	}
	return 0
}

type openAIChatRequest struct {
	ToolChoice          string               `json:"tool_choice,omitempty"`
	ParallelToolCalls   *bool                `json:"parallel_tool_calls,omitempty"`
	MaxCompletionTokens int                  `json:"max_completion_tokens,omitempty"`
	Model               string               `json:"model"`
	Messages            []openAIMessage      `json:"messages"`
	Temperature         float64              `json:"temperature,omitempty"`
	ReasoningEffort     string               `json:"reasoning_effort,omitempty"`
	Stream              bool                 `json:"stream"`
	StreamOptions       *openAIStreamOptions `json:"stream_options,omitempty"`
	Tools               []ToolDefinition     `json:"tools,omitempty"`
}

type openAIMessage struct {
	Role             string                   `json:"role"`
	Content          any                      `json:"content"`
	ReasoningContent *string                  `json:"reasoning_content,omitempty"`
	ToolCalls        []chatCompletionToolCall `json:"tool_calls,omitempty"`
	ToolCallID       string                   `json:"tool_call_id,omitempty"`
	Name             string                   `json:"name,omitempty"`
}

type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

type openAIImageURL struct {
	URL string `json:"url"`
}

// DeepSeek tool sub-turns can return no reasoning text. An explicitly empty
// field still has to survive wire encoding; omitting it causes a 400 on replay.
func requiresDeepSeekReasoningReplay(profile string, request ChatRequest) bool {
	return len(request.Tools) > 0 && (strings.EqualFold(strings.TrimSpace(profile), "deepseek") || strings.HasPrefix(normalizeReasoningModelID(request.Model), "deepseek-"))
}

func openAIMessagesFromProtocol(messages []Message, requireReasoning ...bool) []openAIMessage {
	converted := make([]openAIMessage, 0, len(messages))
	var toolImages []openAIMessage
	for _, message := range messages {
		if message.Role != "tool" && len(toolImages) > 0 {
			converted = append(converted, toolImages...)
			toolImages = nil
		}
		content := any(message.Content)
		if len(message.Attachments) > 0 {
			parts := make([]openAIContentPart, 0, len(message.Attachments)+1)
			if message.Role == "tool" {
				parts = append(parts, openAIContentPart{Type: "text", Text: "Visual output from tool call " + message.ToolCallID})
			} else if message.Content != "" {
				parts = append(parts, openAIContentPart{Type: "text", Text: message.Content})
			}
			for _, attachment := range message.Attachments {
				parts = append(parts, openAIContentPart{
					Type: "image_url",
					ImageURL: &openAIImageURL{
						URL: "data:" + attachment.MIMEType + ";base64," + attachment.Data,
					},
				})
			}
			if message.Role == "tool" {
				toolImages = append(toolImages, openAIMessage{Role: "user", Content: parts})
			} else {
				content = parts
			}
		}
		var reasoning *string
		if message.ReasoningContent != "" || (message.Role == "assistant" && len(requireReasoning) > 0 && requireReasoning[0]) {
			value := message.ReasoningContent
			reasoning = &value
		}
		converted = append(converted, openAIMessage{
			Role:             message.Role,
			Content:          content,
			ReasoningContent: reasoning,
			ToolCalls:        chatCompletionToolCalls(message.ToolCalls),
			ToolCallID:       message.ToolCallID,
			Name:             message.Name,
		})
	}
	converted = append(converted, toolImages...)
	return converted
}

type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIStreamChunk struct {
	Error   *upstreamError `json:"error,omitempty"`
	Choices []struct {
		Delta struct {
			Content          string                `json:"content"`
			Refusal          string                `json:"refusal"`
			ReasoningContent string                `json:"reasoning_content"`
			ToolCalls        []streamToolCallDelta `json:"tool_calls"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
}

// openAICompletionResponse 是非流式补全响应（含 tool_calls）。
type openAICompletionResponse struct {
	Choices []struct {
		Message struct {
			Content          string     `json:"content"`
			Refusal          string     `json:"refusal"`
			ReasoningContent string     `json:"reasoning_content"`
			ToolCalls        []ToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage"`
}

type openAIUsage struct {
	PromptTokens        int64 `json:"prompt_tokens"`
	CompletionTokens    int64 `json:"completion_tokens"`
	TotalTokens         int64 `json:"total_tokens"`
	PromptTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"prompt_tokens_details"`
}

func (u openAIUsage) toTokenUsage() *TokenUsage {
	cached := u.PromptTokensDetails.CachedTokens
	if cached < 0 {
		cached = 0
	}
	if cached > u.PromptTokens {
		cached = u.PromptTokens
	}
	uncached := u.PromptTokens - cached
	return &TokenUsage{
		PromptTokens:          u.PromptTokens,
		CompletionTokens:      u.CompletionTokens,
		TotalTokens:           u.TotalTokens,
		PromptCacheHitTokens:  cached,
		PromptCacheMissTokens: uncached,
	}
}

type openAIErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
		Code    any    `json:"code"`
	} `json:"error"`
}
