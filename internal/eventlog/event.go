// Package eventlog 是文件式的 append-only 会话事件日志。
// 设计目标：会话持久化 + Rewind（对话与文件一起回退）+ 分叉。
// 存储不依赖 SQLite：事件按行写入 events.jsonl，文件快照按内容寻址存 snapshots/<sha256>。
// 领域模型（Event）与存储解耦，日后可无痛迁移到 SQLite。
package eventlog

import "time"

// EventType 是事件种类。
type EventType string

const (
	EventUserMessage        EventType = "user_message"
	EventAssistantMessage   EventType = "assistant_message"
	EventAssistantDelta     EventType = "assistant_delta"
	EventAssistantCompleted EventType = "assistant_completed"
	EventToolStarted        EventType = "tool_started"
	EventToolOutput         EventType = "tool_output"
	EventToolCompleted      EventType = "tool_completed"
	EventToolFailed         EventType = "tool_failed"
	EventTurnInterrupted    EventType = "turn_interrupted"
	EventContextCondensed   EventType = "context_condensed"
	EventTaskTerminal       EventType = "task_terminal"
	EventFileSnapshot       EventType = "file_snapshot"
	EventCheckpoint         EventType = "checkpoint"
	EventBranchMarker       EventType = "branch_marker"
	EventPlanUpdate         EventType = "plan_update"
	EventTeamCheckpoint     EventType = "team_checkpoint"
	EventTurnTerminal       EventType = "turn_terminal"
	EventArtifactUpdate     EventType = "artifact_update"
)

// EventSchemaVersion identifies the shape of the event header. A zero value
// denotes JSONL records written before event headers were introduced.
type EventSchemaVersion int

const (
	LegacyEventSchemaVersion  EventSchemaVersion = 0
	CurrentEventSchemaVersion EventSchemaVersion = 1
)

// EventHeader contains cross-cutting execution metadata. It is embedded in
// Event so its fields are serialized at the JSONL record's top level while the
// existing id/parentId/seq/type/ts/payload layout remains unchanged.
//
// Event log readers intentionally do not reject newer schema versions or
// unknown JSON fields. Existing JSONL records therefore decode as legacy
// events, and future writers can add fields without blocking a log replay.
type EventHeader struct {
	SchemaVersion EventSchemaVersion `json:"schemaVersion,omitempty"`
	ProjectID     string             `json:"projectId,omitempty"`
	SessionID     string             `json:"sessionId,omitempty"`
	BranchID      string             `json:"branchId,omitempty"`
	RunID         string             `json:"runId,omitempty"`
	Generation    int64              `json:"generation,omitempty"`
	CausationID   string             `json:"causationId,omitempty"`
	ToolCallID    string             `json:"toolCallId,omitempty"`
}

// WithDefaultSchemaVersion returns a header ready to persist. It only fills
// the missing legacy value, allowing readers and future writers to preserve
// schema versions newer than this binary understands.
func (header EventHeader) WithDefaultSchemaVersion() EventHeader {
	if header.SchemaVersion == LegacyEventSchemaVersion {
		header.SchemaVersion = CurrentEventSchemaVersion
	}
	return header
}

// IsLegacy reports whether this header came from a JSONL record that predates
// the versioned event header.
func (header EventHeader) IsLegacy() bool {
	return header.SchemaVersion == LegacyEventSchemaVersion
}

// Event 是事件日志的一条记录。所有事件 append-only，靠 ParentID 串成树。
// 「当前对话线」= 从 head 沿 ParentID 回溯到根。Rewind 只移动 head，不删事件，
// 因此分叉是免费的（回退后再追加即从该点长出新线）。
type Event struct {
	EventHeader
	ID       string       `json:"id"`
	ParentID string       `json:"parentId"`
	Seq      int64        `json:"seq"`
	Type     EventType    `json:"type"`
	TS       time.Time    `json:"ts"`
	Payload  EventPayload `json:"payload"`
}

// EventPayload 承载各类事件的数据。用扁平可选字段而非 interface，便于 JSONL 序列化与回读。
type EventPayload struct {
	// message 类
	Role         string                `json:"role,omitempty"`
	Content      string                `json:"content,omitempty"`
	Model        string                `json:"model,omitempty"`
	DurationMs   int64                 `json:"durationMs,omitempty"`
	Parts        []MessagePart         `json:"parts,omitempty"`
	Attachments  []MessageAttachment   `json:"attachments,omitempty"`
	Status       string                `json:"status,omitempty"`
	FailureState *FailureStrategyState `json:"failureState,omitempty"`

	// file_snapshot 类
	Path            string `json:"path,omitempty"`
	BeforeHash      string `json:"beforeHash,omitempty"` // 改动前内容的快照哈希（空=新建文件）
	AfterHash       string `json:"afterHash,omitempty"`  // 改动后内容的快照哈希
	LineEnding      string `json:"lineEnding,omitempty"`
	Encoding        string `json:"encoding,omitempty"`
	HadBOM          bool   `json:"hadBOM,omitempty"`
	Existed         bool   `json:"existed,omitempty"` // 改动前文件是否存在（用于 rewind 时决定恢复或删除）
	Deleted         bool   `json:"deleted,omitempty"` // 改动后文件不存在
	AfterLineEnding string `json:"afterLineEnding,omitempty"`
	AfterEncoding   string `json:"afterEncoding,omitempty"`
	AfterHadBOM     bool   `json:"afterHadBOM,omitempty"`

	// checkpoint 类
	Label      string                `json:"label,omitempty"`
	TurnIndex  int                   `json:"turnIndex,omitempty"`
	PlanSteps  []MessageProgressStep `json:"planSteps,omitempty"`
	PlanStatus string                `json:"planStatus,omitempty"`

	// team_checkpoint stores a content-addressed JSON checkpoint so repeated
	// role transitions do not inflate events.jsonl.
	TeamCheckpointHash string `json:"teamCheckpointHash,omitempty"`

	// artifact_update stores tool-confirmed file metadata on the same event
	// branch as the conversation. Rewind and branch switching therefore change
	// the visible artifact registry without a second mutable database.
	Artifacts []ArtifactRecord `json:"artifacts,omitempty"`

	// context_condensed records a replayable Context View while preserving all
	// original events. The full view is content-addressed to keep JSONL bounded.
	ContextSummary              string   `json:"contextSummary,omitempty"`
	ContextViewHash             string   `json:"contextViewHash,omitempty"`
	ContextFromEventID          string   `json:"contextFromEventId,omitempty"`
	ContextThroughEventID       string   `json:"contextThroughEventId,omitempty"`
	ContextPreservedToolCallIDs []string `json:"contextPreservedToolCallIds,omitempty"`
	ContextPreservedArtifactIDs []string `json:"contextPreservedArtifactIds,omitempty"`
	ContextBeforeTokens         int64    `json:"contextBeforeTokens,omitempty"`
	ContextAfterTokens          int64    `json:"contextAfterTokens,omitempty"`
	ContextRemovedMessages      int      `json:"contextRemovedMessages,omitempty"`
}

// ArtifactRecord is a durable observation of a file created, modified, read,
// or deleted by a tool. EventID, BranchID, and CheckpointID are derived again
// when a branch is read so they always describe the selected branch.
type ArtifactRecord struct {
	ID                     string `json:"id"`
	EventID                string `json:"eventId,omitempty"`
	Path                   string `json:"path"`
	DisplayPath            string `json:"displayPath,omitempty"`
	Name                   string `json:"name,omitempty"`
	FileType               string `json:"fileType,omitempty"`
	MIMEType               string `json:"mimeType,omitempty"`
	Size                   int64  `json:"size,omitempty"`
	ModifiedAt             string `json:"modifiedAt,omitempty"`
	SHA256                 string `json:"sha256,omitempty"`
	Action                 string `json:"action,omitempty"`
	Status                 string `json:"status"`
	Tool                   string `json:"tool,omitempty"`
	ToolCallID             string `json:"toolCallId,omitempty"`
	MessageID              string `json:"messageId,omitempty"`
	ProjectID              string `json:"projectId,omitempty"`
	SessionID              string `json:"sessionId,omitempty"`
	BranchID               string `json:"branchId,omitempty"`
	CheckpointID           string `json:"checkpointId,omitempty"`
	StructuralVerification string `json:"structuralVerification,omitempty"`
	VisualVerification     string `json:"visualVerification,omitempty"`
	FailureReason          string `json:"failureReason,omitempty"`
	PreviewReference       string `json:"previewReference,omitempty"`
	RenderReference        string `json:"renderReference,omitempty"`
	LastCheckedAt          string `json:"lastCheckedAt,omitempty"`
}

// FailureStrategyState is the durable, branch-local summary of tool strategies
// that failed without any intervening progress. It intentionally stores hashes
// and redacted summaries instead of raw arguments so credentials never enter
// the event log.
type FailureStrategyState struct {
	Version          int                     `json:"version"`
	Revision         int                     `json:"revision"`
	ProgressRevision int                     `json:"progressRevision"`
	Records          []FailureStrategyRecord `json:"records,omitempty"`
}

type FailureStrategyRecord struct {
	Fingerprint      string   `json:"fingerprint"`
	StrategyKey      string   `json:"strategyKey"`
	Tool             string   `json:"tool"`
	Category         string   `json:"category"`
	FailureClass     string   `json:"failureClass"`
	ExitCode         *int     `json:"exitCode,omitempty"`
	Attempts         int      `json:"attempts"`
	BlockedAttempts  int      `json:"blockedAttempts,omitempty"`
	ProgressRevision int      `json:"progressRevision"`
	FirstTurn        int      `json:"firstTurn"`
	LastTurn         int      `json:"lastTurn"`
	Summary          string   `json:"summary,omitempty"`
	Alternatives     []string `json:"alternatives,omitempty"`
	Retryable        bool     `json:"retryable,omitempty"`
}

type MessageAttachment struct {
	Kind           string `json:"kind,omitempty"`
	Name           string `json:"name"`
	MIMEType       string `json:"mimeType"`
	Data           string `json:"data"`
	Size           int    `json:"size,omitempty"`
	CharacterCount int    `json:"characterCount,omitempty"`
}

// MessagePart 对齐前端/tools 的结构化片段，供 Timeline 与重建对话使用。
type MessagePart struct {
	Kind              string                `json:"kind"`
	Text              string                `json:"text,omitempty"`
	Path              string                `json:"path,omitempty"`
	Patch             string                `json:"patch,omitempty"`
	Additions         int                   `json:"additions,omitempty"`
	Deletions         int                   `json:"deletions,omitempty"`
	LineCount         int                   `json:"lineCount,omitempty"`
	Created           bool                  `json:"created,omitempty"`
	FileAction        string                `json:"fileAction,omitempty"`
	Name              string                `json:"name,omitempty"`
	Status            string                `json:"status,omitempty"`
	Input             string                `json:"input,omitempty"`
	Output            string                `json:"output,omitempty"`
	ToolCallID        string                `json:"toolCallId,omitempty"`
	Stdout            string                `json:"stdout,omitempty"`
	Stderr            string                `json:"stderr,omitempty"`
	WorkingDirectory  string                `json:"workingDirectory,omitempty"`
	ExitCode          *int                  `json:"exitCode,omitempty"`
	StartedAt         string                `json:"startedAt,omitempty"`
	CompletedAt       string                `json:"completedAt,omitempty"`
	DurationMs        int64                 `json:"durationMs,omitempty"`
	Steps             []MessageProgressStep `json:"steps,omitempty"`
	TaskStatus        string                `json:"taskStatus,omitempty"`
	ChangedFiles      int                   `json:"changedFiles,omitempty"`
	Query             string                `json:"query,omitempty"`
	Sources           []MessageSearchSource `json:"sources,omitempty"`
	Role              string                `json:"role,omitempty"`
	RoleLabel         string                `json:"roleLabel,omitempty"`
	ProviderID        string                `json:"providerId,omitempty"`
	Model             string                `json:"model,omitempty"`
	Summary           string                `json:"summary,omitempty"`
	Verdict           string                `json:"verdict,omitempty"`
	Attempt           int                   `json:"attempt,omitempty"`
	TaskID            string                `json:"taskId,omitempty"`
	AgentType         string                `json:"agentType,omitempty"`
	Label             string                `json:"label,omitempty"`
	CurrentAction     string                `json:"currentAction,omitempty"`
	SubagentOutput    string                `json:"subagentOutput,omitempty"`
	SubagentReasoning string                `json:"subagentReasoning,omitempty"`
	Activities        []SubagentActivity    `json:"activities,omitempty"`
	NoticeKind        string                `json:"noticeKind,omitempty"`
	Severity          string                `json:"severity,omitempty"`
	Message           string                `json:"message,omitempty"`
	RequestedModel    string                `json:"requestedModel,omitempty"`
	EffectiveModel    string                `json:"effectiveModel,omitempty"`
	RetryModel        string                `json:"retryModel,omitempty"`
	UseCases          []string              `json:"useCases,omitempty"`
	Reasons           []string              `json:"reasons,omitempty"`
	Verifications     []string              `json:"verifications,omitempty"`
	MetadataKeys      []string              `json:"metadataKeys,omitempty"`
	RequestID         string                `json:"requestId,omitempty"`
	ErrorCode         string                `json:"errorCode,omitempty"`
	HTTPStatus        int                   `json:"httpStatus,omitempty"`
	Retryable         *bool                 `json:"retryable,omitempty"`
	SecretID          string                `json:"secretId,omitempty"`
	SecretLabel       string                `json:"secretLabel,omitempty"`
	SecretSource      string                `json:"secretSource,omitempty"`
}

type MessageProgressStep struct {
	Title  string `json:"title"`
	Status string `json:"status"`
}

type MessageSearchSource struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet,omitempty"`
}

type SubagentActivity struct {
	ID          string `json:"id"`
	Kind        string `json:"kind"`
	Title       string `json:"title"`
	Status      string `json:"status,omitempty"`
	Input       string `json:"input,omitempty"`
	Output      string `json:"output,omitempty"`
	StartedAt   string `json:"startedAt,omitempty"`
	CompletedAt string `json:"completedAt,omitempty"`
	DurationMs  int64  `json:"durationMs,omitempty"`
}
