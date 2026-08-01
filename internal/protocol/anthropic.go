package protocol

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

const DefaultAnthropicBaseURL = "https://api.anthropic.com"

const anthropicVersion = "2023-06-01"

type AnthropicProvider struct {
	BaseURL               string
	APIKey                string
	ProviderID            string
	HTTPClient            *http.Client
	ExtraHeaders          string
	ExtraBodyJSON         string
	ReasoningProfile      string
	CompatibilityCache    *AnthropicCompatibilityCache
	CompatibilityFeedback func(AnthropicCompatibilityFeedback)
}

type AnthropicCompatibilityFeedback struct {
	ProviderID            string
	ModelID               string
	UnsupportedParameters []string
}

type AnthropicCompatibilityCache struct {
	mu     sync.RWMutex
	models map[string][]string
}

func NewAnthropicCompatibilityCache() *AnthropicCompatibilityCache {
	return &AnthropicCompatibilityCache{models: map[string][]string{}}
}

func (c *AnthropicCompatibilityCache) Parameters(providerID, modelID string) []string {
	if c == nil {
		return nil
	}
	key := anthropicCompatibilityCacheKey(providerID, modelID)
	c.mu.RLock()
	parameters := append([]string(nil), c.models[key]...)
	c.mu.RUnlock()
	return parameters
}

func (c *AnthropicCompatibilityCache) Learn(providerID, modelID string, parameters []string) {
	if c == nil {
		return
	}
	key := anthropicCompatibilityCacheKey(providerID, modelID)
	c.mu.Lock()
	c.models[key] = normalizeAnthropicUnsupportedParameters(append(c.models[key], parameters...))
	c.mu.Unlock()
}

func anthropicCompatibilityCacheKey(providerID, modelID string) string {
	return strings.ToLower(strings.TrimSpace(providerID)) + "\x00" + strings.ToLower(strings.TrimSpace(modelID))
}

func (p AnthropicProvider) Name() string {
	if strings.TrimSpace(p.ProviderID) != "" {
		return strings.TrimSpace(p.ProviderID)
	}
	return "anthropic"
}

func (p AnthropicProvider) ListModels(ctx context.Context) ([]Model, error) {
	if strings.TrimSpace(p.APIKey) == "" {
		return nil, errors.New("Anthropic API Key is required")
	}
	models := make([]Model, 0, 32)
	afterID := ""
	for page := 0; page < 20; page++ {
		endpoint := p.endpoint("/v1/models") + "?limit=100"
		if afterID != "" {
			endpoint += "&after_id=" + url.QueryEscape(afterID)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		p.applyHeaders(req, "")
		if err := applyExtraHeaders(req, p.ExtraHeaders); err != nil {
			return nil, err
		}
		resp, err := p.client().Do(req)
		if err != nil {
			return nil, fmt.Errorf("Anthropic models request failed: %w", err)
		}
		if resp.StatusCode >= http.StatusBadRequest {
			err := anthropicAPIError(resp)
			_ = resp.Body.Close()
			return nil, err
		}
		var payload anthropicModelsResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode Anthropic models: %w", decodeErr)
		}
		for _, item := range payload.Data {
			id := strings.TrimSpace(item.ID)
			if id == "" {
				continue
			}
			displayName := strings.TrimSpace(item.DisplayName)
			if displayName == "" {
				displayName = id
			}
			model := Model{ID: id, DisplayName: displayName, Provider: p.Name()}
			if item.MaxInputTokens > 0 {
				model.ContextWindowTokens = item.MaxInputTokens
				model.ContextWindowSource = "upstream"
			}
			model.MaxOutputTokens = item.MaxTokens
			model.ReasoningLevels = anthropicReportedReasoningLevels(id, item.Capabilities)
			model.ThinkingModes = anthropicReportedThinkingModes(item.Capabilities)
			models = append(models, model)
		}
		if !payload.HasMore || strings.TrimSpace(payload.LastID) == "" || payload.LastID == afterID {
			break
		}
		afterID = payload.LastID
	}
	return models, nil
}

func (p AnthropicProvider) Stream(ctx context.Context, request ChatRequest) (<-chan StreamEvent, error) {
	if strings.TrimSpace(p.APIKey) == "" {
		return nil, errors.New("Anthropic API Key is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, errors.New("Anthropic model is required")
	}
	resp, err := p.doMessagesRequest(ctx, request, true, "text/event-stream")
	if err != nil {
		return nil, err
	}

	events := make(chan StreamEvent, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		parseAnthropicStream(ctx, resp.Body, events)
	}()
	return events, nil
}

// Complete implements Anthropic's native tool_use response contract.
func (p AnthropicProvider) Complete(ctx context.Context, request ChatRequest) (CompletionResult, error) {
	if strings.TrimSpace(p.APIKey) == "" {
		return CompletionResult{}, errors.New("Anthropic API Key is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return CompletionResult{}, errors.New("Anthropic model is required")
	}
	resp, err := p.doMessagesRequest(ctx, request, false, "application/json")
	if err != nil {
		return CompletionResult{}, err
	}
	defer resp.Body.Close()

	var payload anthropicMessagesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return CompletionResult{}, fmt.Errorf("decode Anthropic completion: %w", err)
	}
	result := anthropicCompletionFromBlocks(payload.Content)
	if payload.Usage != nil {
		result.Usage = payload.Usage.toTokenUsage()
	}
	return result, nil
}

func (p AnthropicProvider) doMessagesRequest(ctx context.Context, request ChatRequest, stream bool, accept string) (*http.Response, error) {
	unsupported := normalizeAnthropicUnsupportedParameters(append(
		append([]string(nil), request.ModelUnsupportedParameters...),
		p.CompatibilityCache.Parameters(p.Name(), request.Model)...,
	))
	resp, sent, err := p.sendMessagesRequest(ctx, request, stream, accept, unsupported)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusBadRequest {
		return resp, nil
	}
	status := resp.StatusCode
	firstErr, detail := readAnthropicAPIError(resp)
	_ = resp.Body.Close()
	learned := anthropicUnsupportedParametersFromError(status, detail)
	learned = filterAnthropicSentParameters(learned, sent, unsupported)
	if len(learned) == 0 || ctx.Err() != nil {
		return nil, firstErr
	}

	p.CompatibilityCache.Learn(p.Name(), request.Model, learned)
	if p.CompatibilityFeedback != nil {
		p.CompatibilityFeedback(AnthropicCompatibilityFeedback{
			ProviderID:            p.Name(),
			ModelID:               strings.TrimSpace(request.Model),
			UnsupportedParameters: append([]string(nil), learned...),
		})
	}
	retryUnsupported := normalizeAnthropicUnsupportedParameters(append(unsupported, learned...))
	resp, _, retryErr := p.sendMessagesRequest(ctx, request, stream, accept, retryUnsupported)
	if retryErr != nil {
		return nil, errors.Join(firstErr, fmt.Errorf("Anthropic compatibility retry failed: %w", retryErr))
	}
	if resp.StatusCode >= http.StatusBadRequest {
		retryStatusErr, _ := readAnthropicAPIError(resp)
		_ = resp.Body.Close()
		return nil, errors.Join(firstErr, fmt.Errorf("Anthropic compatibility retry failed after omitting %s: %w", strings.Join(learned, ", "), retryStatusErr))
	}
	return resp, nil
}

func (p AnthropicProvider) sendMessagesRequest(ctx context.Context, request ChatRequest, stream bool, accept string, unsupported []string) (*http.Response, map[string]bool, error) {
	system, messages := anthropicMessagesFromProtocol(request.Messages)
	if len(messages) == 0 {
		messages = []anthropicMessage{{Role: "user", Content: []anthropicContentBlock{{Type: "text", Text: ""}}}}
	}
	reasoning := anthropicReasoningForRequest(p.BaseURL, p.ReasoningProfile, request)
	maxTokens := anthropicMaxTokens(request.Model, request.MaxOutputTokens, reasoning)
	reasoning = constrainAnthropicThinkingBudget(reasoning, maxTokens)
	temperature := request.Temperature
	if anthropicShouldOmitTemperature(request.Model, reasoning) {
		temperature = 0
	}
	body := anthropicMessagesRequest{
		Model:       request.Model,
		MaxTokens:   maxTokens,
		System:      system,
		Messages:    messages,
		Stream:      stream,
		Temperature: temperature,
		Tools:       anthropicToolsFromProtocol(request.Tools),
	}
	applyAnthropicReasoning(&body, reasoning)
	applyAnthropicUnsupportedParameters(&body, unsupported)
	sent := map[string]bool{
		"temperature":   body.Temperature != 0,
		"thinking":      body.Thinking != nil,
		"output_config": body.OutputConfig != nil,
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, sent, err
	}
	encoded, err = mergeExtraJSONBody(encoded, p.ExtraBodyJSON, protectedBodyKeys("model", "max_tokens", "messages", "output_config", "system", "temperature", "thinking", "stream", "tools"))
	if err != nil {
		return nil, sent, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint("/v1/messages"), bytes.NewReader(encoded))
	if err != nil {
		return nil, sent, err
	}
	p.applyHeaders(req, accept)
	if err := applyExtraHeaders(req, p.ExtraHeaders); err != nil {
		return nil, sent, err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return nil, sent, fmt.Errorf("Anthropic chat request failed: %w", err)
	}
	return resp, sent, nil
}

func (p AnthropicProvider) endpoint(path string) string {
	baseURL := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultAnthropicBaseURL
	}
	if strings.HasSuffix(baseURL, "/v1") && strings.HasPrefix(path, "/v1/") {
		return baseURL + strings.TrimPrefix(path, "/v1")
	}
	return baseURL + path
}

func (p AnthropicProvider) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return defaultOpenAICompatibleHTTPClient
}

func (p AnthropicProvider) applyHeaders(req *http.Request, accept string) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", strings.TrimSpace(p.APIKey))
	req.Header.Set("anthropic-version", anthropicVersion)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
}

func anthropicMessagesFromProtocol(messages []Message) (string, []anthropicMessage) {
	var systemParts []string
	converted := make([]anthropicMessage, 0, len(messages))
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		switch message.Role {
		case "system":
			if content != "" {
				systemParts = append(systemParts, content)
			}
		case "assistant":
			blocks := anthropicContinuationBlocks(message.Continuation)
			if content != "" {
				blocks = append(blocks, anthropicContentBlock{Type: "text", Text: content})
			}
			for _, call := range message.ToolCalls {
				blocks = append(blocks, anthropicContentBlock{
					Type:  "tool_use",
					ID:    call.ID,
					Name:  call.Function.Name,
					Input: normalizedJSONObject(call.Function.Arguments),
				})
			}
			if len(blocks) > 0 {
				converted = append(converted, anthropicMessage{Role: "assistant", Content: blocks})
			}
		case "tool":
			toolContent := any(content)
			if len(message.Attachments) > 0 {
				blocks := make([]anthropicContentBlock, 0, len(message.Attachments)+1)
				if content != "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: content})
				}
				for _, attachment := range message.Attachments {
					blocks = append(blocks, anthropicContentBlock{Type: "image", Source: &anthropicImageSource{Type: "base64", MediaType: attachment.MIMEType, Data: attachment.Data}})
				}
				toolContent = blocks
			}
			converted = append(converted, anthropicMessage{Role: "user", Content: []anthropicContentBlock{{
				Type:      "tool_result",
				ToolUseID: message.ToolCallID,
				Content:   toolContent,
			}}})
		default:
			if content != "" || len(message.Attachments) > 0 {
				blocks := make([]anthropicContentBlock, 0, len(message.Attachments)+1)
				for _, attachment := range message.Attachments {
					blocks = append(blocks, anthropicContentBlock{
						Type: "image",
						Source: &anthropicImageSource{
							Type:      "base64",
							MediaType: attachment.MIMEType,
							Data:      attachment.Data,
						},
					})
				}
				if content != "" {
					blocks = append(blocks, anthropicContentBlock{Type: "text", Text: content})
				}
				converted = append(converted, anthropicMessage{
					Role:    "user",
					Content: blocks,
				})
			}
		}
	}
	return strings.Join(systemParts, "\n\n"), converted
}

func anthropicToolsFromProtocol(tools []ToolDefinition) []anthropicTool {
	converted := make([]anthropicTool, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}
		converted = append(converted, anthropicTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		})
	}
	return converted
}

func anthropicMaxTokens(modelID string, reportedMaximum int, reasoning ReasoningOptions) int {
	if reportedMaximum > 0 {
		return reportedMaximum
	}
	if maximum := anthropicModelMaxOutputTokens(modelID); maximum > 0 {
		return maximum
	}
	const visibleOutputReserve = 4096
	if reasoning.BudgetTokens > 0 {
		return reasoning.BudgetTokens + visibleOutputReserve
	}
	return visibleOutputReserve
}

func constrainAnthropicThinkingBudget(reasoning ReasoningOptions, maxTokens int) ReasoningOptions {
	if reasoning.Mode != "enabled" || reasoning.BudgetTokens <= 0 {
		return reasoning
	}
	const minimumBudgetTokens = 1024
	budgetLimit := maxTokens * 3 / 4
	if budgetLimit < minimumBudgetTokens {
		return ReasoningOptions{}
	}
	if reasoning.BudgetTokens > budgetLimit {
		reasoning.BudgetTokens = budgetLimit
	}
	return reasoning
}

func applyAnthropicReasoning(body *anthropicMessagesRequest, reasoning ReasoningOptions) {
	if body == nil || reasoning.Mode == "" {
		return
	}
	body.Thinking = &anthropicThinking{Type: reasoning.Mode, BudgetTokens: reasoning.BudgetTokens}
	if reasoning.Effort != "" {
		body.OutputConfig = &anthropicOutputConfig{Effort: reasoning.Effort}
	}
}

func normalizedJSONObject(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var object map[string]any
	if json.Unmarshal(raw, &object) != nil {
		return json.RawMessage(`{}`)
	}
	encoded, _ := json.Marshal(object)
	return encoded
}

func parseAnthropicStream(ctx context.Context, body io.Reader, events chan<- StreamEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	toolUses := map[int]*anthropicToolUseAccumulator{}
	thinkingBlocks := map[int]*anthropicContentBlock{}
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- StreamEvent{Type: "error", Error: "读取 Anthropic 流式响应被中断：请求上下文已取消或超时。"}
			return
		default:
		}

		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}

		var chunk anthropicStreamEvent
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			events <- StreamEvent{Type: "error", Error: "无法解析 Anthropic 流式响应: " + err.Error()}
			return
		}
		switch chunk.Type {
		case "content_block_start":
			if chunk.ContentBlock.Type == "thinking" || chunk.ContentBlock.Type == "redacted_thinking" {
				block := chunk.ContentBlock
				thinkingBlocks[chunk.Index] = &block
			}
			if chunk.ContentBlock.Type == "tool_use" {
				toolUses[chunk.Index] = &anthropicToolUseAccumulator{
					ID:    chunk.ContentBlock.ID,
					Name:  chunk.ContentBlock.Name,
					Input: append(json.RawMessage(nil), chunk.ContentBlock.Input...),
				}
			}
		case "content_block_delta":
			if chunk.Delta.Text != "" {
				events <- StreamEvent{Type: "delta", Delta: chunk.Delta.Text}
			}
			if chunk.Delta.Thinking != "" {
				events <- StreamEvent{Type: "reasoning", Delta: chunk.Delta.Thinking}
				if block := thinkingBlocks[chunk.Index]; block != nil {
					block.Thinking += chunk.Delta.Thinking
				}
			}
			if chunk.Delta.Signature != "" {
				if block := thinkingBlocks[chunk.Index]; block != nil {
					block.Signature += chunk.Delta.Signature
				}
			}
			if chunk.Delta.PartialJSON != "" {
				if accumulator := toolUses[chunk.Index]; accumulator != nil {
					accumulator.PartialJSON.WriteString(chunk.Delta.PartialJSON)
				}
			}
		case "content_block_stop":
			if accumulator := toolUses[chunk.Index]; accumulator != nil {
				arguments := normalizedJSONObject(accumulator.Input)
				if partial := strings.TrimSpace(accumulator.PartialJSON.String()); partial != "" {
					arguments = normalizedJSONObject(json.RawMessage(partial))
				}
				id := strings.TrimSpace(accumulator.ID)
				if id == "" {
					id = fmt.Sprintf("anthropic-tool-%d", chunk.Index)
				}
				events <- StreamEvent{Type: "tool_calls", ToolCalls: []ToolCall{{
					ID:   id,
					Type: "function",
					Function: ToolCallFunction{
						Name:      accumulator.Name,
						Arguments: arguments,
					},
				}}}
				delete(toolUses, chunk.Index)
			}
		case "message_start":
			if chunk.Message != nil && chunk.Message.Usage != nil {
				events <- StreamEvent{Type: "usage", Usage: chunk.Message.Usage.toTokenUsage()}
			}
		case "message_delta":
			if chunk.Usage != nil {
				events <- StreamEvent{Type: "usage", Usage: chunk.Usage.toTokenUsage()}
			}
		case "error":
			if chunk.Error.Message != "" {
				events <- StreamEvent{Type: "error", Error: chunk.Error.Message}
				return
			}
		case "message_stop":
			if continuation := anthropicStreamContinuation(thinkingBlocks); continuation != nil {
				events <- StreamEvent{Type: "continuation", Continuation: continuation}
			}
			events <- StreamEvent{Type: "done"}
			return
		}
	}
	if err := scanner.Err(); err != nil {
		events <- StreamEvent{Type: "error", Error: "读取 Anthropic 流式响应失败: " + err.Error()}
		return
	}
	events <- StreamEvent{Type: "done"}
}

func anthropicStreamContinuation(blocks map[int]*anthropicContentBlock) *ProviderContinuation {
	if len(blocks) == 0 {
		return nil
	}
	indices := make([]int, 0, len(blocks))
	for index := range blocks {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	ordered := make([]anthropicContentBlock, 0, len(indices))
	for _, index := range indices {
		if blocks[index] != nil {
			ordered = append(ordered, *blocks[index])
		}
	}
	return anthropicContinuation(ordered)
}

func anthropicAPIError(resp *http.Response) error {
	err, _ := readAnthropicAPIError(resp)
	return err
}

func readAnthropicAPIError(resp *http.Response) (error, string) {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope anthropicErrorEnvelope
	_ = json.Unmarshal(body, &envelope)
	detail := strings.TrimSpace(envelope.Error.Message)
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}
	if detail == "" {
		return fmt.Errorf("Anthropic request failed (HTTP %d)", resp.StatusCode), ""
	}
	return fmt.Errorf("Anthropic request failed: %s (HTTP %d)", detail, resp.StatusCode), detail
}

type anthropicModelsResponse struct {
	Data []struct {
		ID             string                              `json:"id"`
		DisplayName    string                              `json:"display_name"`
		MaxInputTokens int                                 `json:"max_input_tokens"`
		MaxTokens      int                                 `json:"max_tokens"`
		Capabilities   *anthropicReportedModelCapabilities `json:"capabilities"`
	} `json:"data"`
	HasMore bool   `json:"has_more"`
	LastID  string `json:"last_id"`
}

type anthropicReportedCapabilitySupport struct {
	Supported bool `json:"supported"`
}

type anthropicReportedModelCapabilities struct {
	Effort struct {
		Supported bool                               `json:"supported"`
		Low       anthropicReportedCapabilitySupport `json:"low"`
		Medium    anthropicReportedCapabilitySupport `json:"medium"`
		High      anthropicReportedCapabilitySupport `json:"high"`
		XHigh     anthropicReportedCapabilitySupport `json:"xhigh"`
		Max       anthropicReportedCapabilitySupport `json:"max"`
	} `json:"effort"`
	Thinking struct {
		Supported bool `json:"supported"`
		Types     struct {
			Adaptive anthropicReportedCapabilitySupport `json:"adaptive"`
			Enabled  anthropicReportedCapabilitySupport `json:"enabled"`
		} `json:"types"`
	} `json:"thinking"`
}

type anthropicMessagesRequest struct {
	Model        string                 `json:"model"`
	MaxTokens    int                    `json:"max_tokens"`
	System       string                 `json:"system,omitempty"`
	Messages     []anthropicMessage     `json:"messages"`
	Stream       bool                   `json:"stream"`
	Temperature  float64                `json:"temperature,omitempty"`
	Tools        []anthropicTool        `json:"tools,omitempty"`
	Thinking     *anthropicThinking     `json:"thinking,omitempty"`
	OutputConfig *anthropicOutputConfig `json:"output_config,omitempty"`
}

type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens,omitempty"`
}

type anthropicOutputConfig struct {
	Effort string `json:"effort,omitempty"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	Thinking  string                `json:"thinking,omitempty"`
	Signature string                `json:"signature,omitempty"`
	Data      string                `json:"data,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     json.RawMessage       `json:"input,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   any                   `json:"content,omitempty"`
	Source    *anthropicImageSource `json:"source,omitempty"`
}

type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	InputSchema map[string]any `json:"input_schema"`
}

type anthropicMessagesResponse struct {
	Content []anthropicContentBlock `json:"content"`
	Usage   *anthropicUsage         `json:"usage"`
}

func anthropicCompletionFromBlocks(blocks []anthropicContentBlock) CompletionResult {
	var content strings.Builder
	var reasoning strings.Builder
	result := CompletionResult{}
	for index, block := range blocks {
		switch block.Type {
		case "text":
			content.WriteString(block.Text)
		case "thinking":
			reasoning.WriteString(block.Thinking)
		case "tool_use":
			arguments := normalizedJSONObject(block.Input)
			id := strings.TrimSpace(block.ID)
			if id == "" {
				id = fmt.Sprintf("anthropic-tool-%d", index)
			}
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:   id,
				Type: "function",
				Function: ToolCallFunction{
					Name:      block.Name,
					Arguments: arguments,
				},
			})
		}
	}
	result.Content = content.String()
	result.Reasoning = reasoning.String()
	result.Continuation = anthropicContinuation(blocks)
	return result
}

func anthropicContinuation(blocks []anthropicContentBlock) *ProviderContinuation {
	continuation := make([]anthropicContentBlock, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == "thinking" || block.Type == "redacted_thinking" {
			continuation = append(continuation, block)
		}
	}
	if len(continuation) == 0 {
		return nil
	}
	encoded, err := json.Marshal(continuation)
	if err != nil {
		return nil
	}
	return &ProviderContinuation{Protocol: "anthropic", Data: encoded}
}

func anthropicContinuationBlocks(continuation *ProviderContinuation) []anthropicContentBlock {
	if continuation == nil || continuation.Protocol != "anthropic" || len(continuation.Data) == 0 {
		return nil
	}
	var blocks []anthropicContentBlock
	if json.Unmarshal(continuation.Data, &blocks) != nil {
		return nil
	}
	return blocks
}

type anthropicToolUseAccumulator struct {
	ID          string
	Name        string
	Input       json.RawMessage
	PartialJSON strings.Builder
}

type anthropicStreamEvent struct {
	Type         string                `json:"type"`
	Index        int                   `json:"index"`
	ContentBlock anthropicContentBlock `json:"content_block"`
	Delta        struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		Signature   string `json:"signature"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
	Message *struct {
		Usage *anthropicUsage `json:"usage"`
	} `json:"message"`
	Usage *anthropicUsage `json:"usage"`
	Error struct {
		Message string `json:"message"`
	} `json:"error"`
}

type anthropicUsage struct {
	InputTokens              int64 `json:"input_tokens"`
	OutputTokens             int64 `json:"output_tokens"`
	CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
}

func (u anthropicUsage) toTokenUsage() *TokenUsage {
	return &TokenUsage{
		PromptTokens:          u.InputTokens,
		CompletionTokens:      u.OutputTokens,
		TotalTokens:           u.InputTokens + u.OutputTokens,
		PromptCacheHitTokens:  u.CacheReadInputTokens,
		PromptCacheMissTokens: u.CacheCreationInputTokens,
	}
}

type anthropicErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error"`
}
