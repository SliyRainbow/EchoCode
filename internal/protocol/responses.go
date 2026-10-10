package protocol

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"unicode/utf16"
)

const (
	responsesInstallationMetadataKey = "x-codex-installation-id"
	responsesWindowMetadataKey       = "x-codex-window-id"
	responsesTurnMetadataKey         = "x-codex-turn-metadata"
)

type responsesRequest struct {
	Model             string               `json:"model"`
	Instructions      string               `json:"instructions,omitempty"`
	Input             []responsesInputItem `json:"input"`
	Stream            bool                 `json:"stream"`
	Tools             []responsesTool      `json:"tools,omitempty"`
	ToolChoice        string               `json:"tool_choice"`
	ParallelToolCalls *bool                `json:"parallel_tool_calls,omitempty"`
	MaxOutputTokens   int                  `json:"max_output_tokens,omitempty"`
	Reasoning         *responsesReasoning  `json:"reasoning,omitempty"`
	Store             bool                 `json:"store"`
	Include           []string             `json:"include"`
	PromptCacheKey    string               `json:"prompt_cache_key,omitempty"`
	Metadata          map[string]string    `json:"metadata,omitempty"`
}

type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type responsesInputItem struct {
	Type             string             `json:"type,omitempty"`
	ID               string             `json:"id,omitempty"`
	Role             string             `json:"role,omitempty"`
	Phase            string             `json:"phase,omitempty"`
	Content          []responsesContent `json:"content,omitempty"`
	Summary          []responsesSummary `json:"summary,omitempty"`
	EncryptedContent string             `json:"encrypted_content,omitempty"`
	CallID           string             `json:"call_id,omitempty"`
	Name             string             `json:"name,omitempty"`
	Arguments        string             `json:"arguments,omitempty"`
	Output           *string            `json:"output,omitempty"`
}

type responsesSummary struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

type responsesContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
	Refusal  string `json:"refusal,omitempty"`
}

type responsesTool struct {
	Strict      bool           `json:"strict"`
	Type        string         `json:"type"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters"`
}

type responsesResponse struct {
	Status            string `json:"status,omitempty"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details,omitempty"`
	ID              string                `json:"id,omitempty"`
	Model           string                `json:"model,omitempty"`
	Headers         map[string]any        `json:"headers,omitempty"`
	Metadata        map[string]any        `json:"metadata,omitempty"`
	SafetyBuffering json.RawMessage       `json:"safety_buffering,omitempty"`
	Output          []responsesOutputItem `json:"output"`
	Usage           *responsesUsage       `json:"usage,omitempty"`
	Error           *responsesError       `json:"error,omitempty"`
}

type responsesOutputItem struct {
	Role             string             `json:"role,omitempty"`
	Phase            string             `json:"phase,omitempty"`
	Type             string             `json:"type"`
	ID               string             `json:"id,omitempty"`
	CallID           string             `json:"call_id,omitempty"`
	Name             string             `json:"name,omitempty"`
	Arguments        string             `json:"arguments,omitempty"`
	Content          []responsesContent `json:"content,omitempty"`
	Summary          []responsesSummary `json:"summary,omitempty"`
	EncryptedContent string             `json:"encrypted_content,omitempty"`
}

type responsesUsage struct {
	InputTokens        int64 `json:"input_tokens"`
	OutputTokens       int64 `json:"output_tokens"`
	TotalTokens        int64 `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int64 `json:"cached_tokens"`
	} `json:"input_tokens_details"`
}

type responsesError struct {
	Message string `json:"message"`
	Type    string `json:"type,omitempty"`
	Code    any    `json:"code,omitempty"`
}

type responsesStreamEnvelope struct {
	OutputIndex     int                 `json:"output_index"`
	ContentIndex    int                 `json:"content_index"`
	SummaryIndex    int                 `json:"summary_index"`
	Arguments       string              `json:"arguments"`
	Text            string              `json:"text"`
	Type            string              `json:"type"`
	Delta           string              `json:"delta,omitempty"`
	Item            responsesOutputItem `json:"item,omitempty"`
	Headers         map[string]any      `json:"headers,omitempty"`
	Metadata        map[string]any      `json:"metadata,omitempty"`
	SafetyBuffering json.RawMessage     `json:"safety_buffering,omitempty"`
	Response        responsesResponse   `json:"response,omitempty"`
	Error           *responsesError     `json:"error,omitempty"`
}

type responsesSafetyBuffering struct {
	UseCases   []string `json:"use_cases,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
	RetryModel string   `json:"retry_model,omitempty"`
}

type responsesTransportMetadata struct {
	ServerModel      string
	RequestID        string
	SafetyRetryModel string
	HTTPStatus       int
}

func (p OpenAICompatibleProvider) streamResponses(ctx context.Context, request ChatRequest) (<-chan StreamEvent, error) {
	if err := p.validateResponsesRequest(request); err != nil {
		return nil, err
	}
	body := p.responsesRequest(request, true)
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	encoded, err = mergeExtraJSONBody(encoded, p.ExtraBodyJSON, protectedBodyKeys(
		"model", "instructions", "input", "reasoning", "stream", "tools", "tool_choice",
		"parallel_tool_calls", "store", "include", "prompt_cache_key", "client_metadata",
	))
	if err != nil {
		return nil, err
	}
	resp, err := p.doRequestWithRetryHeaders(ctx, http.MethodPost, "/responses", encoded, "text/event-stream", responsesHeadersForRequest(request, p.ClientVersion))
	if err != nil {
		return nil, fmt.Errorf("OpenAI Responses request failed: %w", err)
	}
	if resp.StatusCode >= http.StatusBadRequest {
		defer resp.Body.Close()
		return nil, openAICompatibleAPIError(resp)
	}
	events := make(chan StreamEvent, 16)
	transportMetadata := responsesTransportMetadataFromHeaders(resp.Header)
	transportMetadata.HTTPStatus = resp.StatusCode
	go func() {
		defer close(events)
		defer resp.Body.Close()
		parseResponsesStream(ctx, resp.Body, events, request.Model, transportMetadata)
	}()
	return events, nil
}

func (p OpenAICompatibleProvider) completeResponses(ctx context.Context, request ChatRequest) (CompletionResult, error) {
	if err := p.validateResponsesRequest(request); err != nil {
		return CompletionResult{}, err
	}
	body := p.responsesRequest(request, false)
	encoded, err := json.Marshal(body)
	if err != nil {
		return CompletionResult{}, err
	}
	encoded, err = mergeExtraJSONBody(encoded, p.ExtraBodyJSON, protectedBodyKeys(
		"model", "instructions", "input", "reasoning", "stream", "tools", "tool_choice",
		"parallel_tool_calls", "store", "include", "prompt_cache_key", "client_metadata",
	))
	if err != nil {
		return CompletionResult{}, err
	}
	resp, err := p.doRequestWithRetryHeaders(ctx, http.MethodPost, "/responses", encoded, "application/json", responsesHeadersForRequest(request, p.ClientVersion))
	if err != nil {
		return CompletionResult{}, fmt.Errorf("OpenAI Responses request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return CompletionResult{}, openAICompatibleAPIError(resp)
	}
	var payload responsesResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return CompletionResult{}, fmt.Errorf("decode OpenAI Responses completion: %w", err)
	}
	result := completionFromResponses(payload)
	transportMetadata := responsesTransportMetadataFromHeaders(resp.Header)
	result.Notices = responsesNotices(request.Model, payload, nil, transportMetadata)
	result.EffectiveModel = effectiveResponsesModel(payload, nil, transportMetadata.ServerModel)
	if payload.Error != nil {
		return result, NewProviderError(providerErrorFromResponses(payload.Error, resp.StatusCode, responseRequestID(resp.Header)))
	}
	if payload.Status != "" && payload.Status != "completed" {
		reason := ""
		if payload.IncompleteDetails != nil {
			reason = payload.IncompleteDetails.Reason
		}
		if failure := finishReasonFailure("openai-responses", reason); failure != nil {
			return result, NewProviderError(*failure.ProviderError)
		}
		failure := streamFailure("openai-responses", "response_incomplete", "模型响应尚未完整结束，已保留收到的内容。", false)
		return result, NewProviderError(*failure.ProviderError)
	}
	return result, nil
}

func (p OpenAICompatibleProvider) responsesRequest(request ChatRequest, stream bool) responsesRequest {
	instructions, input := responsesInputFromProtocol(request.Messages)
	reasoning := reasoningOptionsForRequest("openai-compatible", p.BaseURL, p.ReasoningProfile, request)
	toolChoice := strings.TrimSpace(request.ToolChoice)
	if toolChoice == "" {
		toolChoice = "auto"
	}
	include := append([]string(nil), request.Include...)
	if len(include) == 0 {
		include = []string{"reasoning.encrypted_content"}
	}
	promptCacheKey := strings.TrimSpace(request.PromptCacheKey)
	if promptCacheKey == "" {
		promptCacheKey = strings.TrimSpace(request.SessionID)
	}
	metadata, _ := responsesClientMetadata(request, p.ClientVersion)
	body := responsesRequest{
		Model:           request.Model,
		Instructions:    instructions,
		Input:           input,
		Stream:          stream,
		Tools:           responsesToolsFromProtocol(request.Tools),
		ToolChoice:      toolChoice,
		MaxOutputTokens: request.MaxOutputTokens,
		Store:           request.Store,
		Include:         include,
		PromptCacheKey:  promptCacheKey,
		Metadata:        responsesPublicMetadata(metadata),
	}
	if !request.OmitParallelToolCalls {
		body.ParallelToolCalls = &request.ParallelToolCalls
	}
	if reasoning.Effort != "" {
		body.Reasoning = &responsesReasoning{Effort: reasoning.Effort, Summary: "auto"}
	}
	return body
}

// The public Responses API accepts metadata, not Codex's client_metadata body
// extension. Keep the larger local turn context in its existing headers and
// send only bounded identifiers as standard response metadata.
func responsesPublicMetadata(metadata map[string]string) map[string]string {
	result := make(map[string]string)
	for key, value := range metadata {
		if key != responsesTurnMetadataKey && len(key) <= 64 && len([]rune(value)) <= 512 {
			result[key] = value
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func responsesHeadersForRequest(request ChatRequest, clientVersion string) http.Header {
	headers := make(http.Header)
	for name, value := range map[string]string{
		"session-id":          request.SessionID,
		"thread-id":           request.ThreadID,
		"x-client-request-id": request.ThreadID,
		"x-echocode-turn-id":  request.TurnID,
	} {
		if validInternalHeaderValue(value) {
			headers.Set(name, strings.TrimSpace(value))
		}
	}
	metadata, turnMetadata := responsesClientMetadata(request, clientVersion)
	if value := metadata[responsesWindowMetadataKey]; validInternalHeaderValue(value) {
		headers.Set(responsesWindowMetadataKey, value)
	}
	if validInternalHeaderValue(turnMetadata) {
		headers.Set(responsesTurnMetadataKey, turnMetadata)
	}
	return headers
}

func responsesClientMetadata(request ChatRequest, clientVersion string) (map[string]string, string) {
	sessionID := strings.TrimSpace(request.SessionID)
	threadID := strings.TrimSpace(request.ThreadID)
	if threadID == "" {
		threadID = sessionID
	}
	context := request.ResponsesContext
	installationID := strings.TrimSpace(context.InstallationID)
	windowID := strings.TrimSpace(context.WindowID)
	if windowID == "" && threadID != "" {
		windowID = threadID + ":0"
	}
	turnID := strings.TrimSpace(request.TurnID)
	requestKind := strings.TrimSpace(context.RequestKind)
	if requestKind == "" && (sessionID != "" || threadID != "") {
		requestKind = "turn"
	}
	threadSource := strings.TrimSpace(context.ThreadSource)
	if threadSource == "" && requestKind == "turn" {
		threadSource = "user"
	}

	payload := make(map[string]any, len(request.Metadata)+12)
	setStringValue(payload, "installation_id", installationID)
	setStringValue(payload, "session_id", sessionID)
	setStringValue(payload, "thread_id", threadID)
	setStringValue(payload, "turn_id", turnID)
	setStringValue(payload, "window_id", windowID)
	setStringValue(payload, "request_kind", requestKind)
	setStringValue(payload, "thread_source", threadSource)
	setStringValue(payload, "sandbox", strings.TrimSpace(context.Sandbox))
	if context.TurnStartedAtUnixMS > 0 {
		payload["turn_started_at_unix_ms"] = context.TurnStartedAtUnixMS
	}
	if workspaces := responsesWorkspaceMetadata(context.WorkspaceRoots); len(workspaces) > 0 {
		payload["workspaces"] = workspaces
	}
	setStringValue(payload, "client_name", "EchoCode")
	setStringValue(payload, "client_version", strings.TrimSpace(clientVersion))
	for key, value := range request.Metadata {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" || reservedResponsesMetadataKey(key) {
			continue
		}
		payload[key] = value
	}

	if len(payload) == 1 && payload["client_name"] == "EchoCode" {
		return nil, ""
	}
	turnMetadata, err := asciiJSON(payload)
	if err != nil {
		return nil, ""
	}
	metadata := make(map[string]string, 6)
	setStringMetadata(metadata, responsesInstallationMetadataKey, installationID)
	setStringMetadata(metadata, "session_id", sessionID)
	setStringMetadata(metadata, "thread_id", threadID)
	setStringMetadata(metadata, "turn_id", turnID)
	setStringMetadata(metadata, responsesWindowMetadataKey, windowID)
	metadata[responsesTurnMetadataKey] = turnMetadata
	return metadata, turnMetadata
}

func responsesWorkspaceMetadata(roots []string) map[string]map[string]any {
	workspaces := make(map[string]map[string]any)
	for _, root := range roots {
		root = strings.TrimSpace(root)
		if root != "" {
			workspaces[root] = map[string]any{}
		}
	}
	return workspaces
}

func setStringValue(target map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		target[key] = value
	}
}

func setStringMetadata(target map[string]string, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		target[key] = value
	}
}

func reservedResponsesMetadataKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "installation_id", responsesInstallationMetadataKey,
		"session_id", "thread_id", "turn_id", "window_id", responsesWindowMetadataKey,
		"request_kind", "thread_source", "sandbox", "workspaces", "turn_started_at_unix_ms",
		responsesTurnMetadataKey:
		return true
	default:
		return false
	}
}

func asciiJSON(value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	output.Grow(len(encoded))
	for _, char := range string(encoded) {
		switch {
		case char <= 0x7f:
			output.WriteRune(char)
		case char <= 0xffff:
			_, _ = fmt.Fprintf(&output, `\u%04x`, char)
		default:
			high, low := utf16.EncodeRune(char)
			_, _ = fmt.Fprintf(&output, `\u%04x\u%04x`, high, low)
		}
	}
	return output.String(), nil
}

func (p OpenAICompatibleProvider) validateResponsesRequest(request ChatRequest) error {
	if strings.TrimSpace(p.BaseURL) == "" {
		return errors.New("OpenAI-compatible base url is required")
	}
	if strings.TrimSpace(p.APIKey) == "" && !p.AllowNoAuth {
		return errors.New("OpenAI-compatible API Key is required")
	}
	if strings.TrimSpace(request.Model) == "" {
		return errors.New("OpenAI Responses model is required")
	}
	return nil
}

func responsesInputFromProtocol(messages []Message) (string, []responsesInputItem) {
	system := make([]string, 0, 2)
	input := make([]responsesInputItem, 0, len(messages))
	var toolImages []responsesInputItem
	for _, message := range messages {
		if message.Role != "tool" && len(toolImages) > 0 {
			input = append(input, toolImages...)
			toolImages = nil
		}
		switch message.Role {
		case "system":
			if text := strings.TrimSpace(message.Content); text != "" {
				system = append(system, text)
			}
		case "tool":
			input = append(input, responsesInputItem{
				Type:   "function_call_output",
				CallID: message.ToolCallID,
				Output: &message.Content,
			})
			if len(message.Attachments) > 0 {
				content := []responsesContent{{Type: "input_text", Text: "Visual output from tool call " + message.ToolCallID}}
				for _, attachment := range message.Attachments {
					content = append(content, responsesContent{Type: "input_image", ImageURL: "data:" + attachment.MIMEType + ";base64," + attachment.Data})
				}
				toolImages = append(toolImages, responsesInputItem{Type: "message", Role: "user", Content: content})
			}
		default:
			phase := ""
			if message.Role == "assistant" {
				replay, complete, replayPhase := responsesReplayForMessage(message)
				input = append(input, replay...)
				if complete {
					continue
				}
				phase = replayPhase
			}
			contentType := "input_text"
			if message.Role == "assistant" {
				contentType = "output_text"
			}
			content := make([]responsesContent, 0, len(message.Attachments)+1)
			if message.Content != "" {
				content = append(content, responsesContent{Type: contentType, Text: message.Content})
			}
			for _, attachment := range message.Attachments {
				content = append(content, responsesContent{
					Type:     "input_image",
					ImageURL: "data:" + attachment.MIMEType + ";base64," + attachment.Data,
				})
			}
			if len(content) > 0 {
				input = append(input, responsesInputItem{Type: "message", Role: message.Role, Phase: phase, Content: content})
			}
			for _, call := range message.ToolCalls {
				arguments := toolCallArgumentsString(call.Function.Arguments)
				input = append(input, responsesInputItem{
					Type:      "function_call",
					CallID:    call.ID,
					Name:      call.Function.Name,
					Arguments: arguments,
				})
			}
		}
	}
	input = append(input, toolImages...)
	return strings.Join(system, "\n\n"), input
}

func responsesToolsFromProtocol(definitions []ToolDefinition) []responsesTool {
	tools := make([]responsesTool, 0, len(definitions))
	for _, definition := range definitions {
		tools = append(tools, responsesTool{
			Type:        "function",
			Name:        definition.Function.Name,
			Description: definition.Function.Description,
			Parameters:  definition.Function.Parameters,
		})
	}
	return tools
}

func parseResponsesStream(
	ctx context.Context,
	body io.Reader,
	events chan<- StreamEvent,
	requestedModel string,
	transport responsesTransportMetadata,
) {
	scanner := newSSEReader(body)
	items := make(map[int]responsesOutputItem)
	textBlocks := make(map[string]string)
	reasoningBlocks := make(map[string]string)
	noticesSeen := make(map[string]bool)
	lastServerModel := ""
	emitText := func(kind, key, text string, complete bool) {
		if text == "" {
			return
		}
		blocks := textBlocks
		if kind == "reasoning" {
			blocks = reasoningBlocks
		}
		previous, exists := blocks[key]
		if complete {
			if !strings.HasPrefix(text, previous) {
				return
			}
			text = strings.TrimPrefix(text, previous)
		}
		if text == "" {
			return
		}
		if kind == "reasoning" && !exists && len(blocks) > 0 {
			events <- StreamEvent{Type: kind, Delta: "\n\n"}
		}
		blocks[key] = previous + text
		events <- StreamEvent{Type: kind, Delta: text}
	}
	rememberItem := func(index int, item responsesOutputItem) {
		old := items[index]
		if item.ID == "" {
			item.ID = old.ID
		}
		if item.Role == "" {
			item.Role = old.Role
		}
		if item.Phase == "" {
			item.Phase = old.Phase
		}
		if item.Type == "" {
			item.Type = old.Type
		}
		if item.Name == "" {
			item.Name = old.Name
		}
		if item.CallID == "" {
			item.CallID = old.CallID
		}
		if item.Arguments == "" {
			item.Arguments = old.Arguments
		}
		if item.EncryptedContent == "" {
			item.EncryptedContent = old.EncryptedContent
		}
		if item.Summary == nil {
			item.Summary = old.Summary
		}
		if item.Content == nil {
			item.Content = old.Content
		}
		items[index] = item
		for partIndex, content := range item.Content {
			if item.Type == "reasoning" || content.Type == "reasoning_text" {
				emitText("reasoning", fmt.Sprintf("content:%d:%d", index, partIndex), content.Text, true)
				continue
			}
			if item.Type != "message" {
				continue
			}
			value := content.Text
			if content.Type == "refusal" {
				value = content.Refusal
			}
			emitText("delta", fmt.Sprintf("%d:%d", index, partIndex), value, true)
		}
		for partIndex, summary := range item.Summary {
			emitText("reasoning", fmt.Sprintf("summary:%d:%d", index, partIndex), summary.Text, true)
		}
	}
	finish := func(response responsesResponse, failed bool) {
		for index, item := range response.Output {
			rememberItem(index, item)
		}
		indices := make([]int, 0, len(items))
		for index := range items {
			indices = append(indices, index)
		}
		sort.Ints(indices)
		response.Output = make([]responsesOutputItem, 0, len(indices))
		for _, index := range indices {
			response.Output = append(response.Output, items[index])
		}
		completion := completionFromResponses(response)
		if completion.Usage != nil {
			events <- StreamEvent{Type: "usage", Usage: completion.Usage}
		}
		if response.Error != nil {
			info := providerErrorFromResponses(response.Error, transport.HTTPStatus, transport.RequestID)
			events <- StreamEvent{Type: "error", Error: info.Message, ProviderError: &info}
			return
		}
		if failed || (response.Status != "" && response.Status != "completed") {
			reason := ""
			if response.IncompleteDetails != nil {
				reason = response.IncompleteDetails.Reason
			}
			if failure := finishReasonFailure("openai-responses", reason); failure != nil {
				events <- *failure
			} else {
				events <- streamFailure("openai-responses", "response_incomplete", "模型响应尚未完整结束，已保留收到的内容。", false)
			}
			return
		}
		for _, item := range response.Output {
			if item.Type == "function_call" && (item.Name == "" || (item.CallID == "" && item.ID == "")) {
				events <- streamFailure("openai-responses", "invalid_tool_call", "上游工具调用缺少名称或调用 ID。", false)
				return
			}
		}
		if len(completion.ToolCalls) > 0 {
			events <- StreamEvent{Type: "tool_calls", ToolCalls: completion.ToolCalls}
		}
		if completion.Continuation != nil {
			events <- StreamEvent{Type: "continuation", Continuation: completion.Continuation}
		}
		events <- StreamEvent{Type: "finish", FinishReason: "stop"}
		events <- StreamEvent{Type: "done"}
	}
	emitServerModel := func(model string) {
		model = strings.TrimSpace(model)
		if model == "" || strings.EqualFold(model, lastServerModel) {
			return
		}
		lastServerModel = model
		events <- StreamEvent{Type: "server_model", Delta: model}
	}
	emitNotice := func(notice ProviderNotice) {
		key := providerNoticeKey(notice)
		if noticesSeen[key] {
			return
		}
		noticesSeen[key] = true
		copy := notice
		events <- StreamEvent{Type: "provider_notice", Notice: &copy}
	}
	emitServerModel(transport.ServerModel)
	for _, notice := range responsesNotices(requestedModel, responsesResponse{}, nil, transport) {
		emitNotice(notice)
	}
	for {
		frame, ok := scanner.Next()
		if !ok {
			break
		}
		select {
		case <-ctx.Done():
			events <- StreamEvent{Type: "error", Error: ctx.Err().Error()}
			return
		default:
		}
		payload := strings.TrimSpace(frame.Data)
		if payload == "" {
			continue
		}
		if payload == "[DONE]" {
			finish(responsesResponse{}, false)
			return
		}
		var envelope responsesStreamEnvelope
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			events <- StreamEvent{Type: "error", Error: "decode OpenAI Responses stream: " + err.Error()}
			return
		}
		if envelope.Type == "" {
			envelope.Type = frame.Type
		}
		effectiveModel := effectiveResponsesModel(envelope.Response, &envelope, transport.ServerModel)
		emitServerModel(effectiveModel)
		for _, notice := range responsesNotices(requestedModel, envelope.Response, &envelope, transport) {
			emitNotice(notice)
		}
		switch envelope.Type {
		case "response.output_text.delta":
			emitText("delta", fmt.Sprintf("%d:%d", envelope.OutputIndex, envelope.ContentIndex), envelope.Delta, false)
		case "response.output_text.done":
			emitText("delta", fmt.Sprintf("%d:%d", envelope.OutputIndex, envelope.ContentIndex), envelope.Text, true)
		case "response.refusal.delta":
			emitText("delta", fmt.Sprintf("%d:%d", envelope.OutputIndex, envelope.ContentIndex), envelope.Delta, false)
		case "response.reasoning_summary_text.delta":
			emitText("reasoning", fmt.Sprintf("summary:%d:%d", envelope.OutputIndex, envelope.SummaryIndex), envelope.Delta, false)
		case "response.reasoning_summary_text.done":
			emitText("reasoning", fmt.Sprintf("summary:%d:%d", envelope.OutputIndex, envelope.SummaryIndex), envelope.Text, true)
		case "response.reasoning_text.delta":
			emitText("reasoning", fmt.Sprintf("content:%d:%d", envelope.OutputIndex, envelope.ContentIndex), envelope.Delta, false)
		case "response.reasoning_text.done":
			emitText("reasoning", fmt.Sprintf("content:%d:%d", envelope.OutputIndex, envelope.ContentIndex), envelope.Text, true)
		case "response.output_item.added":
			rememberItem(envelope.OutputIndex, envelope.Item)
		case "response.output_item.done":
			rememberItem(envelope.OutputIndex, envelope.Item)
		case "response.function_call_arguments.delta":
			item := items[envelope.OutputIndex]
			item.Arguments += envelope.Delta
			items[envelope.OutputIndex] = item
		case "response.function_call_arguments.done":
			item := items[envelope.OutputIndex]
			item.Arguments = envelope.Arguments
			items[envelope.OutputIndex] = item
		case "response.completed":
			finish(envelope.Response, false)
			return
		case "response.incomplete":
			finish(envelope.Response, true)
			return
		case "response.failed", "error":
			if completion := completionFromResponses(envelope.Response); completion.Usage != nil {
				events <- StreamEvent{Type: "usage", Usage: completion.Usage}
			}
			responseError := envelope.Error
			if responseError == nil {
				responseError = envelope.Response.Error
			}
			info := providerErrorFromResponses(responseError, transport.HTTPStatus, transport.RequestID)
			events <- StreamEvent{Type: "error", Error: info.Message, ProviderError: &info}
			return
		}
	}
	if err := scanner.Err(); err != nil {
		events <- StreamEvent{Type: "error", Error: "read OpenAI Responses stream: " + err.Error()}
		return
	}
	events <- interruptedStream("openai-responses")
}

func responsesTransportMetadataFromHeaders(headers http.Header) responsesTransportMetadata {
	return responsesTransportMetadata{
		ServerModel:      firstHeaderValue(headers, "OpenAI-Model", "X-OpenAI-Model"),
		RequestID:        responseRequestID(headers),
		SafetyRetryModel: firstHeaderValue(headers, "x-codex-safety-buffering-faster-model"),
	}
}

func firstHeaderValue(headers http.Header, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(headers.Get(name)); value != "" {
			return value
		}
	}
	return ""
}

func responsesNotices(
	requestedModel string,
	response responsesResponse,
	envelope *responsesStreamEnvelope,
	transport responsesTransportMetadata,
) []ProviderNotice {
	notices := make([]ProviderNotice, 0, 4)
	effectiveModel := effectiveResponsesModel(response, envelope, transport.ServerModel)
	if effectiveModel != "" && requestedModel != "" && !strings.EqualFold(effectiveModel, requestedModel) {
		notices = append(notices, ProviderNotice{
			Kind:           ProviderNoticeModelReroute,
			Severity:       "warning",
			RequestedModel: requestedModel,
			EffectiveModel: effectiveModel,
			RequestID:      transport.RequestID,
		})
	}

	bufferingPayload := response.SafetyBuffering
	if envelope != nil && len(envelope.SafetyBuffering) > 0 {
		bufferingPayload = envelope.SafetyBuffering
	}
	buffering := parseResponsesSafetyBuffering(bufferingPayload, transport.SafetyRetryModel)
	if buffering != nil {
		notices = append(notices, ProviderNotice{
			Kind:       ProviderNoticeSafetyBuffering,
			Severity:   "info",
			RetryModel: strings.TrimSpace(buffering.RetryModel),
			UseCases:   cleanNoticeValues(buffering.UseCases),
			Reasons:    cleanNoticeValues(buffering.Reasons),
			RequestID:  transport.RequestID,
		})
	}

	metadata := response.Metadata
	if envelope != nil && len(envelope.Metadata) > 0 {
		metadata = envelope.Metadata
	}
	if len(metadata) > 0 {
		verifications := knownModelVerifications(metadata["openai_verification_recommendation"])
		if len(verifications) > 0 {
			notices = append(notices, ProviderNotice{
				Kind:          ProviderNoticeModelVerification,
				Severity:      "warning",
				Verifications: verifications,
				RequestID:     transport.RequestID,
			})
		}
		if moderation, ok := metadata["openai_chatgpt_moderation_metadata"]; ok && moderation != nil {
			notices = append(notices, ProviderNotice{
				Kind:         ProviderNoticeModeration,
				Severity:     "info",
				MetadataKeys: metadataKeys(moderation),
				RequestID:    transport.RequestID,
			})
		}
	}
	return notices
}

func parseResponsesSafetyBuffering(raw json.RawMessage, fallbackModel string) *responsesSafetyBuffering {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "false" || trimmed == "null" {
		return nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil
	}
	useCasesRaw, hasUseCases := fields["use_cases"]
	reasonsRaw, hasReasons := fields["reasons"]
	if !hasUseCases || !hasReasons {
		return nil
	}
	buffering := &responsesSafetyBuffering{}
	if json.Unmarshal(useCasesRaw, &buffering.UseCases) != nil || json.Unmarshal(reasonsRaw, &buffering.Reasons) != nil {
		return nil
	}
	if retryModelRaw, present := fields["retry_model"]; present {
		// Explicit null intentionally disables the header fallback, matching Codex.
		_ = json.Unmarshal(retryModelRaw, &buffering.RetryModel)
	} else {
		buffering.RetryModel = strings.TrimSpace(fallbackModel)
	}
	return buffering
}

func knownModelVerifications(value any) []string {
	values := stringValues(value)
	known := make([]string, 0, len(values))
	for _, value := range values {
		if value == "trusted_access_for_cyber" {
			known = append(known, value)
		}
	}
	return known
}

func effectiveResponsesModel(response responsesResponse, envelope *responsesStreamEnvelope, transportModel string) string {
	if model := headerModelValue(response.Headers); model != "" {
		return model
	}
	if envelope != nil {
		if model := headerModelValue(envelope.Headers); model != "" {
			return model
		}
	}
	if model := strings.TrimSpace(transportModel); model != "" {
		return model
	}
	if model := strings.TrimSpace(response.Model); model != "" {
		return model
	}
	return ""
}

func headerModelValue(headers map[string]any) string {
	for name, value := range headers {
		if !strings.EqualFold(name, "openai-model") && !strings.EqualFold(name, "x-openai-model") {
			continue
		}
		values := stringValues(value)
		if len(values) > 0 {
			return values[0]
		}
	}
	return ""
}

func stringValues(value any) []string {
	switch typed := value.(type) {
	case string:
		return cleanNoticeValues([]string{typed})
	case []string:
		return cleanNoticeValues(typed)
	case []any:
		values := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				values = append(values, text)
			}
		}
		return cleanNoticeValues(values)
	default:
		return nil
	}
}

func cleanNoticeValues(values []string) []string {
	cleaned := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		cleaned = append(cleaned, value)
	}
	return cleaned
}

func metadataKeys(value any) []string {
	object, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		if key = strings.TrimSpace(key); key != "" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func providerNoticeKey(notice ProviderNotice) string {
	return strings.Join([]string{
		notice.Kind,
		notice.RequestedModel,
		notice.EffectiveModel,
		notice.RetryModel,
		strings.Join(notice.UseCases, ","),
		strings.Join(notice.Reasons, ","),
		strings.Join(notice.Verifications, ","),
		strings.Join(notice.MetadataKeys, ","),
	}, "\x00")
}

func providerErrorFromResponses(responseError *responsesError, status int, requestID string) ProviderErrorInfo {
	info := ProviderErrorInfo{
		Provider:   "openai-compatible",
		HTTPStatus: status,
		Message:    "OpenAI Responses request failed",
		RequestID:  requestID,
	}
	if responseError == nil {
		return info
	}
	if message := compactOpenAICompatibleError(responseError.Message); message != "" {
		info.Message = message
	}
	info.Type = strings.TrimSpace(responseError.Type)
	info.Code = strings.TrimSpace(fmt.Sprint(responseError.Code))
	if info.Code == "<nil>" {
		info.Code = ""
	}
	info.Retryable = providerErrorRetryable(info.Code, info.Type, status)
	return info
}

func providerErrorRetryable(code, errorType string, status int) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	errorType = strings.ToLower(strings.TrimSpace(errorType))
	if isNonRetryableProviderErrorCode(code) {
		return false
	}
	if status >= 400 {
		return shouldRetryOpenAICompatibleStatus(status)
	}
	for _, value := range []string{code, errorType} {
		switch value {
		case "rate_limit_exceeded", "server_error", "overloaded", "timeout", "request_timeout":
			return true
		}
	}
	return false
}

func completionFromResponses(response responsesResponse) CompletionResult {
	result := CompletionResult{EffectiveModel: effectiveResponsesModel(response, nil, "")}
	var text strings.Builder
	var reasoning []string
	replayItems := make([]responsesInputItem, 0, len(response.Output))
	for _, item := range response.Output {
		if item.Type == "reasoning" || item.Type == "message" || item.Type == "function_call" {
			role := item.Role
			if item.Type == "message" && role == "" {
				role = "assistant"
			}
			replayItems = append(replayItems, responsesInputItem{Type: item.Type, ID: item.ID, Role: role, Phase: item.Phase, Content: item.Content, Summary: item.Summary, EncryptedContent: item.EncryptedContent, CallID: item.CallID, Name: item.Name, Arguments: item.Arguments})
		}
		if item.Type == "reasoning" {
			for _, content := range item.Content {
				if content.Text != "" {
					reasoning = append(reasoning, content.Text)
				}
			}
			for _, summary := range item.Summary {
				if summary.Text != "" {
					reasoning = append(reasoning, summary.Text)
				}
			}
			continue
		}
		if call, ok := toolCallFromResponsesItem(item); ok {
			result.ToolCalls = append(result.ToolCalls, call)
			continue
		}
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type == "refusal" {
				text.WriteString(content.Refusal)
			}
			if content.Type == "output_text" || content.Type == "text" {
				text.WriteString(content.Text)
			}
		}
	}
	result.Content = text.String()
	result.Reasoning = strings.Join(reasoning, "\n\n")
	if len(result.ToolCalls) > 0 && len(replayItems) > 0 {
		if encoded, err := json.Marshal(responsesReplayState{Version: 1, Items: replayItems}); err == nil {
			result.Continuation = &ProviderContinuation{Protocol: "openai-responses", Data: encoded}
		}
	}
	if response.Usage != nil {
		cached := response.Usage.InputTokensDetails.CachedTokens
		miss := response.Usage.InputTokens - cached
		if miss < 0 {
			miss = 0
		}
		result.Usage = &TokenUsage{
			PromptTokens:          response.Usage.InputTokens,
			CompletionTokens:      response.Usage.OutputTokens,
			TotalTokens:           response.Usage.TotalTokens,
			PromptCacheHitTokens:  cached,
			PromptCacheMissTokens: miss,
		}
	}
	return result
}

func responsesContinuationItems(continuation *ProviderContinuation) []responsesInputItem {
	if continuation == nil || continuation.Protocol != "openai-responses" || len(continuation.Data) == 0 {
		return nil
	}
	var state responsesReplayState
	if json.Unmarshal(continuation.Data, &state) == nil && state.Version == 1 {
		return state.Items
	}
	var legacy []responsesInputItem
	if json.Unmarshal(continuation.Data, &legacy) == nil {
		return legacy
	}
	return nil
}

type responsesReplayState struct {
	Version int                  `json:"version"`
	Items   []responsesInputItem `json:"items"`
}

func responsesReplayForMessage(message Message) ([]responsesInputItem, bool, string) {
	items := responsesContinuationItems(message.Continuation)
	var state responsesReplayState
	if message.Continuation == nil || json.Unmarshal(message.Continuation.Data, &state) != nil || state.Version != 1 {
		return items, false, ""
	}
	var text strings.Builder
	var calls []responsesInputItem
	phase := ""
	reasoning := make([]responsesInputItem, 0)
	for _, item := range items {
		switch item.Type {
		case "reasoning":
			reasoning = append(reasoning, item)
		case "function_call":
			calls = append(calls, item)
		case "message":
			if item.Phase != "" {
				phase = item.Phase
			}
			for _, part := range item.Content {
				if part.Type == "refusal" {
					text.WriteString(part.Refusal)
				} else {
					text.WriteString(part.Text)
				}
			}
		}
	}
	complete := strings.TrimSpace(text.String()) == strings.TrimSpace(message.Content) && len(calls) == len(message.ToolCalls)
	for index, call := range calls {
		if !complete {
			break
		}
		expected := message.ToolCalls[index]
		complete = call.CallID == expected.ID && call.Name == expected.Function.Name && strings.TrimSpace(call.Arguments) == strings.TrimSpace(toolCallArgumentsString(expected.Function.Arguments))
	}
	if complete {
		return items, true, phase
	}
	// Context clipping may have shortened visible text. Preserve opaque
	// reasoning and phase, while rebuilding the clipped text/tool messages.
	return reasoning, false, phase
}

func (item responsesInputItem) MarshalJSON() ([]byte, error) {
	type alias responsesInputItem
	if item.Type == "reasoning" {
		summary := item.Summary
		if summary == nil {
			summary = []responsesSummary{}
		}
		return json.Marshal(struct {
			alias
			Summary []responsesSummary `json:"summary"`
		}{alias(item), summary})
	}
	return json.Marshal(alias(item))
}

func toolCallFromResponsesItem(item responsesOutputItem) (ToolCall, bool) {
	if item.Type != "function_call" || strings.TrimSpace(item.Name) == "" {
		return ToolCall{}, false
	}
	callID := strings.TrimSpace(item.CallID)
	if callID == "" {
		callID = strings.TrimSpace(item.ID)
	}
	arguments := strings.TrimSpace(item.Arguments)
	if arguments == "" {
		arguments = "{}"
	}
	return ToolCall{
		ID:   callID,
		Type: "function",
		Function: ToolCallFunction{
			Name:      item.Name,
			Arguments: preserveToolArguments(arguments),
		},
	}, true
}
