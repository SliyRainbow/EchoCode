package tools

import (
	"context"
	"encoding/json"
)

// PartKind 对齐前端 MessagePart union，工具执行结果据此点亮对应渲染卡片。
type PartKind string

const (
	PartText           PartKind = "text"
	PartDiff           PartKind = "diff"
	PartToolCall       PartKind = "tool_call"
	PartFile           PartKind = "file"
	PartProgress       PartKind = "task_progress"
	PartWebSearch      PartKind = "web_search_results"
	PartTeamRole       PartKind = "team_role"
	PartSubagent       PartKind = "subagent"
	PartProviderNotice PartKind = "provider_notice"
	PartSecretResult   PartKind = "secret_result"
	PartTimelineNote   PartKind = "timeline_note"
)

type ProgressStep struct {
	Title  string `json:"title"`
	Status string `json:"status"` // pending | in_progress | completed
}

type SearchSource struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type SubagentActivity struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"` // tool | status | provider
	Title       string `json:"title"`
	Status      string `json:"status,omitempty"`
	Input       string `json:"input,omitempty"`
	Output      string `json:"output,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
	DurationMs  int64  `json:"durationMs,omitempty"`
}

// ResultPart 是一段结构化输出，会被 agent 收集进 ChatResult.Parts。
type ResultPart struct {
	Kind PartKind `json:"kind"`

	// text
	Text string `json:"text,omitempty"`

	// diff
	Path      string `json:"path,omitempty"`
	Patch     string `json:"patch,omitempty"`
	Additions int    `json:"additions,omitempty"`
	Deletions int    `json:"deletions,omitempty"`

	// file
	LineCount  int    `json:"lineCount,omitempty"`
	Created    bool   `json:"created,omitempty"`
	FileAction string `json:"fileAction,omitempty"`

	// tool_call
	Name       string `json:"name,omitempty"`
	Status     string `json:"status,omitempty"` // running | waiting | retrying | ok | error
	Input      string `json:"input,omitempty"`
	Output     string `json:"output,omitempty"`
	ToolCallID string `json:"toolCallId,omitempty"`
	Stdout     string `json:"stdout,omitempty"`
	Stderr     string `json:"stderr,omitempty"`
	// Execution metadata is kept on the structured part so the UI can show
	// durable, per-tool diagnostics after a session switch or app restart.
	WorkingDirectory string `json:"workingDirectory,omitempty"`
	ExitCode         *int   `json:"exitCode,omitempty"`
	StartedAt        string `json:"startedAt,omitempty"`
	CompletedAt      string `json:"completedAt,omitempty"`
	DurationMs       int64  `json:"durationMs,omitempty"`

	// task_progress
	Steps        []ProgressStep `json:"steps,omitempty"`
	TaskStatus   string         `json:"taskStatus,omitempty"` // running | completed | failed | cancelled
	ChangedFiles int            `json:"changedFiles,omitempty"`

	// web_search_results
	Query   string         `json:"query,omitempty"`
	Sources []SearchSource `json:"sources,omitempty"`

	// team_role
	Role       string `json:"role,omitempty"`
	RoleLabel  string `json:"roleLabel,omitempty"`
	ProviderID string `json:"providerId,omitempty"`
	Model      string `json:"model,omitempty"`
	Summary    string `json:"summary,omitempty"`
	Verdict    string `json:"verdict,omitempty"`
	Attempt    int    `json:"attempt,omitempty"`

	// subagent
	TaskID            string             `json:"taskId,omitempty"`
	AgentType         string             `json:"agentType,omitempty"`
	Label             string             `json:"label,omitempty"`
	CurrentAction     string             `json:"currentAction,omitempty"`
	SubagentOutput    string             `json:"subagentOutput,omitempty"`
	SubagentReasoning string             `json:"subagentReasoning,omitempty"`
	Activities        []SubagentActivity `json:"activities,omitempty"`

	// provider_notice
	NoticeKind     string   `json:"noticeKind,omitempty"`
	Severity       string   `json:"severity,omitempty"`
	Message        string   `json:"message,omitempty"`
	RequestedModel string   `json:"requestedModel,omitempty"`
	EffectiveModel string   `json:"effectiveModel,omitempty"`
	RetryModel     string   `json:"retryModel,omitempty"`
	UseCases       []string `json:"useCases,omitempty"`
	Reasons        []string `json:"reasons,omitempty"`
	Verifications  []string `json:"verifications,omitempty"`
	MetadataKeys   []string `json:"metadataKeys,omitempty"`
	RequestID      string   `json:"requestId,omitempty"`
	ErrorCode      string   `json:"errorCode,omitempty"`
	HTTPStatus     int      `json:"httpStatus,omitempty"`
	Retryable      *bool    `json:"retryable,omitempty"`

	// secret_result. The value itself is never serialized into ResultPart.
	SecretID     string `json:"secretId,omitempty"`
	SecretLabel  string `json:"secretLabel,omitempty"`
	SecretSource string `json:"secretSource,omitempty"`
}

// Result 是一次工具执行的产出。
//   - Summary 回喂给模型（摘要优先，避免长输出吞 tokens）。
//   - Parts 用于前端渲染（diff 卡片、工具卡片）。
//   - Changes 记录文件改动的前后内容，供事件日志做快照与 Rewind。
type Result struct {
	Summary     string       `json:"summary"`
	Parts       []ResultPart `json:"parts,omitempty"`
	Changes     []FileChange `json:"changes,omitempty"`
	Attachments []Attachment `json:"attachments,omitempty"`
	IsError     bool         `json:"isError,omitempty"`
}

type Attachment struct {
	Name     string `json:"name"`
	MIMEType string `json:"mimeType"`
	Data     string `json:"data"`
}

// FileChange 描述一次文件修改的前后内容（LF/UTF-8 归一化文本），用于快照与回退。
type FileChange struct {
	Path            string `json:"path"`
	Before          string `json:"before"`            // 改动前内容（新建文件为空）
	After           string `json:"after"`             // 改动后内容
	Existed         bool   `json:"existed"`           // 改动前文件是否存在
	Deleted         bool   `json:"deleted,omitempty"` // 改动后文件是否不存在
	LineEnding      string `json:"lineEnding"`        // 原文件行尾风格
	Encoding        string `json:"encoding,omitempty"`
	HadBOM          bool   `json:"hadBom"` // 原文件是否带 BOM
	AfterLineEnding string `json:"afterLineEnding,omitempty"`
	AfterEncoding   string `json:"afterEncoding,omitempty"`
	AfterHadBOM     bool   `json:"afterHadBom,omitempty"`
}

// Tool 是一个可被模型调用的工具。
type Tool interface {
	// Name 是工具标识（对应 function name）。
	Name() string
	// Description 面向模型，说明工具用途。
	Description() string
	// InputSchema 返回 JSON Schema（parameters），描述参数。
	InputSchema() map[string]any
	// Execute 以 JSON 原始参数执行，返回结构化结果。
	Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error)
}

// ToolSchema 是传给 provider 的工具定义（OpenAI function-calling 格式）。
type ToolSchema struct {
	Type     string           `json:"type"` // 恒为 "function"
	Function ToolSchemaDetail `json:"function"`
}

type ToolSchemaDetail struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

// Registry 保存一组工具，负责查找与 schema 导出。
type Registry struct {
	tools map[string]Tool
	order []string
}

// NewRegistry 用给定工具集构造注册表（按传入顺序保留，schema 输出稳定）。
func NewRegistry(list ...Tool) *Registry {
	r := &Registry{tools: make(map[string]Tool)}
	for _, t := range list {
		r.Add(t)
	}
	return r
}

func (r *Registry) Add(t Tool) {
	if _, exists := r.tools[t.Name()]; !exists {
		r.order = append(r.order, t.Name())
	}
	r.tools[t.Name()] = t
}

// AddStructuredSearch registers the first-class, read-only grep and glob
// tools in a stable order. Callers can keep the legacy search tool while they
// migrate prompts and stored tool calls to the structured contracts.
func (r *Registry) AddStructuredSearch(policy SandboxPolicy) {
	r.Add(GrepTool{Policy: policy})
	r.Add(GlobTool{Policy: policy})
}

func (r *Registry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Len 返回工具数量。
func (r *Registry) Len() int {
	return len(r.order)
}

// Schemas 按稳定顺序导出工具 schema，供 provider 组装请求。
// 顺序稳定很重要：它会进入请求体，影响缓存前缀的可复现性。
func (r *Registry) Schemas() []ToolSchema {
	schemas := make([]ToolSchema, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		schemas = append(schemas, ToolSchema{
			Type: "function",
			Function: ToolSchemaDetail{
				Name:        t.Name(),
				Description: t.Description(),
				Parameters:  t.InputSchema(),
			},
		})
	}
	return schemas
}
