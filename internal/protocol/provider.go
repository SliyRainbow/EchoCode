package protocol

import (
	"context"
	"encoding/json"
)

type Model struct {
	ID                    string   `json:"id"`
	DisplayName           string   `json:"displayName"`
	Provider              string   `json:"provider"`
	ContextWindowTokens   int      `json:"contextWindowTokens"`
	ContextWindowSource   string   `json:"contextWindowSource,omitempty"`
	MaxOutputTokens       int      `json:"maxOutputTokens,omitempty"`
	ReasoningLevels       []string `json:"reasoningLevels,omitempty"`
	ThinkingModes         []string `json:"thinkingModes,omitempty"`
	UnsupportedParameters []string `json:"unsupportedParameters,omitempty"`
}

type ChatRequest struct {
	Model           string            `json:"model"`
	Messages        []Message         `json:"messages"`
	Temperature     float64           `json:"temperature"`
	ReasoningLevel  string            `json:"-"`
	ThinkingMode    string            `json:"thinkingMode,omitempty"`
	ReasoningEffort string            `json:"reasoningEffort,omitempty"`
	Metadata        map[string]string `json:"metadata,omitempty"`
	// Responses API controls. They remain internal for other protocols.
	ToolChoice            string                 `json:"-"`
	ParallelToolCalls     bool                   `json:"-"`
	OmitParallelToolCalls bool                   `json:"-"`
	Store                 bool                   `json:"-"`
	Include               []string               `json:"-"`
	PromptCacheKey        string                 `json:"-"`
	SessionID             string                 `json:"-"`
	ThreadID              string                 `json:"-"`
	TurnID                string                 `json:"-"`
	ResponsesContext      ResponsesClientContext `json:"-"`
	// Tools 为空时表示普通对话；非空时启用 function-calling（仅支持 ToolCaller 的 provider 生效）。
	Tools []ToolDefinition `json:"tools,omitempty"`
	// Internal context budget hints. Providers never serialize these fields.
	MaxInputTokens    int `json:"-"`
	TargetInputTokens int `json:"-"`
	// Model metadata reported by providers. It is transport-only and lets a
	// native protocol avoid guessing capabilities for custom model aliases.
	MaxOutputTokens            int      `json:"-"`
	ModelReasoningLevels       []string `json:"-"`
	ModelThinkingModes         []string `json:"-"`
	ModelUnsupportedParameters []string `json:"-"`
}

// ResponsesClientContext describes the real EchoCode turn using the metadata
// shape understood by Codex-compatible Responses endpoints. It is transport
// context only: it never claims Codex identity, authentication, or account
// entitlements.
type ResponsesClientContext struct {
	InstallationID      string   `json:"-"`
	WindowID            string   `json:"-"`
	RequestKind         string   `json:"-"`
	ThreadSource        string   `json:"-"`
	Sandbox             string   `json:"-"`
	WorkspaceRoots      []string `json:"-"`
	TurnStartedAtUnixMS int64    `json:"-"`
}

type Message struct {
	Role             string                `json:"role"`
	Content          string                `json:"content"`
	ReasoningContent string                `json:"reasoning_content,omitempty"`
	Attachments      []Attachment          `json:"-"`
	Continuation     *ProviderContinuation `json:"-"`
	// 以下字段用于 function-calling 多轮：
	// assistant 轮携带 ToolCalls；tool 轮携带 ToolCallID + Name + Content(结果)。
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID   string     `json:"tool_call_id,omitempty"`
	Name         string     `json:"name,omitempty"`
	InternalKind string     `json:"-"`
	ToolError    bool       `json:"tool_error,omitempty"`
}

type Attachment struct {
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

func chatRequestHasAttachments(request ChatRequest) bool {
	for _, message := range request.Messages {
		if len(message.Attachments) > 0 {
			return true
		}
	}
	return false
}

// ToolDefinition 是传给模型的工具声明（OpenAI function-calling 格式）。
type ToolDefinition struct {
	Type     string             `json:"type"` // 恒为 "function"
	Function ToolDefinitionFunc `json:"function"`
}

type ToolDefinitionFunc struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// ToolCall 是模型返回的一次工具调用请求。
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function ToolCallFunction `json:"function"`
}

type ToolCallFunction struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// CompletionResult 是一次非流式补全结果：要么带文本，要么带工具调用。
type CompletionResult struct {
	Content        string
	Reasoning      string
	ToolCalls      []ToolCall
	Usage          *TokenUsage
	Continuation   *ProviderContinuation
	EffectiveModel string
	Notices        []ProviderNotice
}

// ProviderContinuation carries opaque, signed reasoning state between tool
// sub-turns. Each provider only reads continuations matching its own protocol.
type ProviderContinuation struct {
	Protocol string
	Scope    string `json:"scope,omitempty"`
	Data     json.RawMessage
}

type TokenUsage struct {
	PromptTokens          int64 `json:"promptTokens"`
	CompletionTokens      int64 `json:"completionTokens"`
	TotalTokens           int64 `json:"totalTokens"`
	PromptCacheHitTokens  int64 `json:"promptCacheHitTokens"`
	PromptCacheMissTokens int64 `json:"promptCacheMissTokens"`
}

type StreamEvent struct {
	Type          string                `json:"type"`
	Delta         string                `json:"delta,omitempty"`
	Error         string                `json:"error,omitempty"`
	ProviderError *ProviderErrorInfo    `json:"providerError,omitempty"`
	Notice        *ProviderNotice       `json:"notice,omitempty"`
	Usage         *TokenUsage           `json:"usage,omitempty"`
	ToolCalls     []ToolCall            `json:"toolCalls,omitempty"`
	FinishReason  string                `json:"finishReason,omitempty"`
	Continuation  *ProviderContinuation `json:"-"`
}

type Provider interface {
	Name() string
	ListModels(ctx context.Context) ([]Model, error)
	Stream(ctx context.Context, request ChatRequest) (<-chan StreamEvent, error)
}

// ToolCaller 是可选能力接口：支持 function-calling 的 provider 额外实现 Complete()。
// agent 的工具循环用它做非流式补全以可靠解析 tool_calls；不支持的 provider 走纯流式文本路径。
type ToolCaller interface {
	Complete(ctx context.Context, request ChatRequest) (CompletionResult, error)
}
