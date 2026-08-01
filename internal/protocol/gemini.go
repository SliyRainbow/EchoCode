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
	"strings"
)

const DefaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

type GeminiProvider struct {
	BaseURL          string
	APIKey           string
	ProviderID       string
	HTTPClient       *http.Client
	ExtraHeaders     string
	ExtraBodyJSON    string
	ReasoningProfile string
}

func (p GeminiProvider) Name() string {
	if strings.TrimSpace(p.ProviderID) != "" {
		return strings.TrimSpace(p.ProviderID)
	}
	return "gemini"
}

func (p GeminiProvider) ListModels(ctx context.Context) ([]Model, error) {
	if strings.TrimSpace(p.APIKey) == "" {
		return nil, errors.New("Gemini API Key is required")
	}
	models := make([]Model, 0, 32)
	pageToken := ""
	for page := 0; page < 20; page++ {
		values := url.Values{"pageSize": []string{"1000"}}
		if pageToken != "" {
			values.Set("pageToken", pageToken)
		}
		endpoint := p.endpoint("/models") + "?" + values.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, err
		}
		p.applyHeaders(req)
		if err := applyExtraHeaders(req, p.ExtraHeaders); err != nil {
			return nil, err
		}
		resp, err := p.client().Do(req)
		if err != nil {
			return nil, fmt.Errorf("Gemini models request failed: %w", err)
		}
		if resp.StatusCode >= http.StatusBadRequest {
			err := geminiAPIError(resp)
			_ = resp.Body.Close()
			return nil, err
		}
		var payload geminiModelsResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("decode Gemini models: %w", decodeErr)
		}
		for _, item := range payload.Models {
			if !geminiSupportsGenerateContent(item.SupportedGenerationMethods) {
				continue
			}
			id := strings.TrimPrefix(strings.TrimSpace(item.Name), "models/")
			if id == "" {
				continue
			}
			displayName := strings.TrimSpace(item.DisplayName)
			if displayName == "" {
				displayName = id
			}
			models = append(models, Model{ID: id, DisplayName: displayName, Provider: p.Name(), ContextWindowTokens: item.InputTokenLimit, ContextWindowSource: "upstream"})
		}
		if strings.TrimSpace(payload.NextPageToken) == "" || payload.NextPageToken == pageToken {
			break
		}
		pageToken = payload.NextPageToken
	}
	return models, nil
}

func (p GeminiProvider) Stream(ctx context.Context, request ChatRequest) (<-chan StreamEvent, error) {
	if strings.TrimSpace(p.APIKey) == "" {
		return nil, errors.New("Gemini API Key is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return nil, errors.New("Gemini model is required")
	}
	system, contents := geminiContentsFromProtocol(request.Messages)
	if len(contents) == 0 {
		contents = []geminiContent{{Role: "user", Parts: []geminiPart{{Text: ""}}}}
	}
	reasoning := reasoningOptionsForRequest("gemini", p.BaseURL, p.ReasoningProfile, request)
	body := geminiGenerateContentRequest{
		Contents: contents,
		GenerationConfig: geminiGenerationConfig{
			Temperature:    request.Temperature,
			ThinkingConfig: geminiThinkingConfigFor(reasoning),
		},
		Tools: geminiToolsFromProtocol(request.Tools),
	}
	if strings.TrimSpace(system) != "" {
		body.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: system}},
		}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	encoded, err = mergeExtraJSONBody(encoded, p.ExtraBodyJSON, protectedBodyKeys("contents", "generationConfig", "systemInstruction", "tools"))
	if err != nil {
		return nil, err
	}
	path := "/" + normalizeGeminiModelName(request.Model) + ":streamGenerateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(path)+"?alt=sse", bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	p.applyHeaders(req)
	req.Header.Set("Accept", "text/event-stream")
	if err := applyExtraHeaders(req, p.ExtraHeaders); err != nil {
		return nil, err
	}

	resp, err := p.client().Do(req)
	if err != nil {
		return nil, fmt.Errorf("Gemini chat request failed: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		return nil, geminiAPIError(resp)
	}

	events := make(chan StreamEvent, 16)
	go func() {
		defer close(events)
		defer resp.Body.Close()
		parseGeminiStream(ctx, resp.Body, events)
	}()
	return events, nil
}

// Complete implements Gemini's native functionCall response contract.
func (p GeminiProvider) Complete(ctx context.Context, request ChatRequest) (CompletionResult, error) {
	if strings.TrimSpace(p.APIKey) == "" {
		return CompletionResult{}, errors.New("Gemini API Key is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return CompletionResult{}, errors.New("Gemini model is required")
	}
	system, contents := geminiContentsFromProtocol(request.Messages)
	if len(contents) == 0 {
		contents = []geminiContent{{Role: "user", Parts: []geminiPart{{Text: ""}}}}
	}
	reasoning := reasoningOptionsForRequest("gemini", p.BaseURL, p.ReasoningProfile, request)
	body := geminiGenerateContentRequest{
		Contents: contents,
		GenerationConfig: geminiGenerationConfig{
			Temperature:    request.Temperature,
			ThinkingConfig: geminiThinkingConfigFor(reasoning),
		},
		Tools: geminiToolsFromProtocol(request.Tools),
	}
	if strings.TrimSpace(system) != "" {
		body.SystemInstruction = &geminiContent{Parts: []geminiPart{{Text: system}}}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return CompletionResult{}, err
	}
	encoded, err = mergeExtraJSONBody(encoded, p.ExtraBodyJSON, protectedBodyKeys("contents", "generationConfig", "systemInstruction", "tools"))
	if err != nil {
		return CompletionResult{}, err
	}
	path := "/" + normalizeGeminiModelName(request.Model) + ":generateContent"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint(path), bytes.NewReader(encoded))
	if err != nil {
		return CompletionResult{}, err
	}
	p.applyHeaders(req)
	if err := applyExtraHeaders(req, p.ExtraHeaders); err != nil {
		return CompletionResult{}, err
	}
	resp, err := p.client().Do(req)
	if err != nil {
		return CompletionResult{}, fmt.Errorf("Gemini chat request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return CompletionResult{}, geminiAPIError(resp)
	}

	var payload geminiGenerateContentResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return CompletionResult{}, fmt.Errorf("decode Gemini completion: %w", err)
	}
	result := geminiCompletionFromCandidates(payload.Candidates)
	if payload.UsageMetadata != nil {
		result.Usage = payload.UsageMetadata.toTokenUsage()
	}
	return result, nil
}

func (p GeminiProvider) endpoint(path string) string {
	baseURL := strings.TrimRight(strings.TrimSpace(p.BaseURL), "/")
	if baseURL == "" {
		baseURL = DefaultGeminiBaseURL
	}
	return baseURL + path
}

func (p GeminiProvider) client() *http.Client {
	if p.HTTPClient != nil {
		return p.HTTPClient
	}
	return defaultOpenAICompatibleHTTPClient
}

func (p GeminiProvider) applyHeaders(req *http.Request) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-goog-api-key", strings.TrimSpace(p.APIKey))
}

func geminiContentsFromProtocol(messages []Message) (string, []geminiContent) {
	var systemParts []string
	contents := make([]geminiContent, 0, len(messages))
	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		switch message.Role {
		case "system":
			if content != "" {
				systemParts = append(systemParts, content)
			}
		case "assistant":
			parts := geminiContinuationParts(message.Continuation)
			if len(parts) == 0 {
				parts = make([]geminiPart, 0, len(message.ToolCalls)+1)
				if content != "" {
					parts = append(parts, geminiPart{Text: content})
				}
				for _, call := range message.ToolCalls {
					parts = append(parts, geminiPart{FunctionCall: &geminiFunctionCall{
						ID:   call.ID,
						Name: call.Function.Name,
						Args: rawJSONObjectMap(call.Function.Arguments),
					}})
				}
			}
			if len(parts) > 0 {
				contents = append(contents, geminiContent{Role: "model", Parts: parts})
			}
		case "tool":
			parts := []geminiPart{{
				FunctionResponse: &geminiFunctionResponse{
					ID:       message.ToolCallID,
					Name:     message.Name,
					Response: geminiFunctionResponsePayload(content),
				},
			}}
			for _, attachment := range message.Attachments {
				parts = append(parts, geminiPart{InlineData: &geminiInlineData{MIMEType: attachment.MIMEType, Data: attachment.Data}})
			}
			contents = append(contents, geminiContent{Role: "user", Parts: parts})
		default:
			if content != "" || len(message.Attachments) > 0 {
				parts := make([]geminiPart, 0, len(message.Attachments)+1)
				if content != "" {
					parts = append(parts, geminiPart{Text: content})
				}
				for _, attachment := range message.Attachments {
					parts = append(parts, geminiPart{InlineData: &geminiInlineData{
						MIMEType: attachment.MIMEType,
						Data:     attachment.Data,
					}})
				}
				contents = append(contents, geminiContent{Role: "user", Parts: parts})
			}
		}
	}
	return strings.Join(systemParts, "\n\n"), contents
}

func geminiToolsFromProtocol(tools []ToolDefinition) []geminiTool {
	declarations := make([]geminiFunctionDeclaration, 0, len(tools))
	for _, tool := range tools {
		if strings.TrimSpace(tool.Function.Name) == "" {
			continue
		}
		declarations = append(declarations, geminiFunctionDeclaration{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		})
	}
	if len(declarations) == 0 {
		return nil
	}
	return []geminiTool{{FunctionDeclarations: declarations}}
}

func geminiThinkingConfigFor(reasoning ReasoningOptions) *geminiThinkingConfig {
	if strings.TrimSpace(reasoning.ThinkingLevel) == "" {
		return nil
	}
	return &geminiThinkingConfig{ThinkingLevel: reasoning.ThinkingLevel}
}

func rawJSONObjectMap(raw json.RawMessage) map[string]any {
	object := map[string]any{}
	_ = json.Unmarshal(normalizedJSONObject(raw), &object)
	return object
}

func geminiFunctionResponsePayload(content string) map[string]any {
	var value any
	if json.Unmarshal([]byte(content), &value) == nil {
		if object, ok := value.(map[string]any); ok {
			return object
		}
		return map[string]any{"result": value}
	}
	return map[string]any{"result": content}
}

func parseGeminiStream(ctx context.Context, body io.Reader, events chan<- StreamEvent) {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	toolIndex := 0
	continuationParts := make([]geminiPart, 0, 8)
	hasToolCall := false
	emitTerminal := func(finishReason string) {
		if hasToolCall {
			if continuation := geminiContinuation(continuationParts); continuation != nil {
				events <- StreamEvent{Type: "continuation", Continuation: continuation}
			}
		}
		if finishReason != "" {
			events <- StreamEvent{Type: "finish", FinishReason: finishReason}
		}
		events <- StreamEvent{Type: "done"}
	}
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			events <- StreamEvent{Type: "error", Error: "读取 Gemini 流式响应被中断：请求上下文已取消或超时。"}
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
			emitTerminal("")
			return
		}
		var chunk geminiStreamChunk
		if err := json.Unmarshal([]byte(payload), &chunk); err != nil {
			events <- StreamEvent{Type: "error", Error: "无法解析 Gemini 流式响应: " + err.Error()}
			return
		}
		finishReason := ""
		for _, candidate := range chunk.Candidates {
			if finishReason == "" {
				finishReason = strings.TrimSpace(candidate.FinishReason)
			}
			for _, part := range candidate.Content.Parts {
				continuationParts = append(continuationParts, part)
				if part.Text != "" {
					if part.Thought {
						events <- StreamEvent{Type: "reasoning", Delta: part.Text}
					} else {
						events <- StreamEvent{Type: "delta", Delta: part.Text}
					}
				}
				if part.FunctionCall != nil && strings.TrimSpace(part.FunctionCall.Name) != "" {
					hasToolCall = true
					id := strings.TrimSpace(part.FunctionCall.ID)
					if id == "" {
						id = fmt.Sprintf("gemini-tool-%d", toolIndex)
					}
					toolIndex++
					arguments, _ := json.Marshal(part.FunctionCall.Args)
					events <- StreamEvent{Type: "tool_calls", ToolCalls: []ToolCall{{
						ID:   id,
						Type: "function",
						Function: ToolCallFunction{
							Name:      part.FunctionCall.Name,
							Arguments: normalizedJSONObject(arguments),
						},
					}}}
				}
			}
		}
		if chunk.UsageMetadata != nil {
			events <- StreamEvent{Type: "usage", Usage: chunk.UsageMetadata.toTokenUsage()}
		}
		if finishReason != "" {
			emitTerminal(finishReason)
			return
		}
	}
	if err := scanner.Err(); err != nil {
		events <- StreamEvent{Type: "error", Error: "读取 Gemini 流式响应失败: " + err.Error()}
		return
	}
	emitTerminal("")
}

func geminiAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	var envelope geminiErrorEnvelope
	_ = json.Unmarshal(body, &envelope)
	detail := strings.TrimSpace(envelope.Error.Message)
	if detail == "" {
		detail = strings.TrimSpace(string(body))
	}
	if detail == "" {
		return fmt.Errorf("Gemini request failed (HTTP %d)", resp.StatusCode)
	}
	return fmt.Errorf("Gemini request failed: %s (HTTP %d)", detail, resp.StatusCode)
}

func normalizeGeminiModelName(model string) string {
	model = strings.TrimSpace(model)
	if strings.HasPrefix(model, "models/") {
		return model
	}
	return "models/" + model
}

func geminiSupportsGenerateContent(methods []string) bool {
	if len(methods) == 0 {
		return true
	}
	for _, method := range methods {
		if method == "generateContent" || method == "streamGenerateContent" {
			return true
		}
	}
	return false
}

type geminiModelsResponse struct {
	Models []struct {
		Name                       string   `json:"name"`
		DisplayName                string   `json:"displayName"`
		InputTokenLimit            int      `json:"inputTokenLimit"`
		OutputTokenLimit           int      `json:"outputTokenLimit"`
		SupportedGenerationMethods []string `json:"supportedGenerationMethods"`
	} `json:"models"`
	NextPageToken string `json:"nextPageToken"`
}

type geminiGenerateContentRequest struct {
	SystemInstruction *geminiContent         `json:"systemInstruction,omitempty"`
	Contents          []geminiContent        `json:"contents"`
	GenerationConfig  geminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []geminiTool           `json:"tools,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature    float64               `json:"temperature,omitempty"`
	ThinkingConfig *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

type geminiThinkingConfig struct {
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	ThoughtSignature string                  `json:"thoughtSignature,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
}

type geminiInlineData struct {
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDeclaration `json:"functionDeclarations"`
}

type geminiFunctionDeclaration struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type geminiFunctionCall struct {
	ID   string         `json:"id,omitempty"`
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiFunctionResponse struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Response map[string]any `json:"response"`
}

type geminiStreamChunk struct {
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata"`
}

type geminiGenerateContentResponse struct {
	Candidates    []geminiCandidate    `json:"candidates"`
	UsageMetadata *geminiUsageMetadata `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
}

func geminiCompletionFromCandidates(candidates []geminiCandidate) CompletionResult {
	result := CompletionResult{}
	if len(candidates) == 0 {
		return result
	}
	var content strings.Builder
	var reasoning strings.Builder
	for index, part := range candidates[0].Content.Parts {
		if part.Text != "" {
			if part.Thought {
				reasoning.WriteString(part.Text)
			} else {
				content.WriteString(part.Text)
			}
		}
		if part.FunctionCall != nil && strings.TrimSpace(part.FunctionCall.Name) != "" {
			id := strings.TrimSpace(part.FunctionCall.ID)
			if id == "" {
				id = fmt.Sprintf("gemini-tool-%d", index)
			}
			arguments, _ := json.Marshal(part.FunctionCall.Args)
			result.ToolCalls = append(result.ToolCalls, ToolCall{
				ID:   id,
				Type: "function",
				Function: ToolCallFunction{
					Name:      part.FunctionCall.Name,
					Arguments: normalizedJSONObject(arguments),
				},
			})
		}
	}
	result.Content = content.String()
	result.Reasoning = reasoning.String()
	if len(result.ToolCalls) > 0 {
		result.Continuation = geminiContinuation(candidates[0].Content.Parts)
	}
	return result
}

func geminiContinuation(parts []geminiPart) *ProviderContinuation {
	if len(parts) == 0 {
		return nil
	}
	needed := false
	for _, part := range parts {
		if part.Thought || strings.TrimSpace(part.ThoughtSignature) != "" {
			needed = true
			break
		}
	}
	if !needed {
		return nil
	}
	encoded, err := json.Marshal(parts)
	if err != nil {
		return nil
	}
	return &ProviderContinuation{Protocol: "gemini", Data: encoded}
}

func geminiContinuationParts(continuation *ProviderContinuation) []geminiPart {
	if continuation == nil || continuation.Protocol != "gemini" || len(continuation.Data) == 0 {
		return nil
	}
	var parts []geminiPart
	if json.Unmarshal(continuation.Data, &parts) != nil {
		return nil
	}
	return parts
}

type geminiUsageMetadata struct {
	PromptTokenCount        int64 `json:"promptTokenCount"`
	CandidatesTokenCount    int64 `json:"candidatesTokenCount"`
	TotalTokenCount         int64 `json:"totalTokenCount"`
	CachedContentTokenCount int64 `json:"cachedContentTokenCount"`
}

func (u geminiUsageMetadata) toTokenUsage() *TokenUsage {
	missTokens := u.PromptTokenCount - u.CachedContentTokenCount
	if missTokens < 0 {
		missTokens = 0
	}
	return &TokenUsage{
		PromptTokens:          u.PromptTokenCount,
		CompletionTokens:      u.CandidatesTokenCount,
		TotalTokens:           u.TotalTokenCount,
		PromptCacheHitTokens:  u.CachedContentTokenCount,
		PromptCacheMissTokens: missTokens,
	}
}

type geminiErrorEnvelope struct {
	Error struct {
		Message string `json:"message"`
		Status  string `json:"status"`
		Code    int    `json:"code"`
	} `json:"error"`
}
