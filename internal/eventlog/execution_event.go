package eventlog

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ExecutionEventKind is the stable discriminator for the typed execution log.
// It is intentionally separate from EventType while Store still persists the
// legacy EventPayload shape.
type ExecutionEventKind string

const (
	ExecutionKindUserMessage        ExecutionEventKind = "user_message"
	ExecutionKindAssistantDelta     ExecutionEventKind = "assistant_delta"
	ExecutionKindAssistantCompleted ExecutionEventKind = "assistant_completed"
	ExecutionKindToolStarted        ExecutionEventKind = "tool_started"
	ExecutionKindToolOutput         ExecutionEventKind = "tool_output"
	ExecutionKindToolCompleted      ExecutionEventKind = "tool_completed"
	ExecutionKindToolFailed         ExecutionEventKind = "tool_failed"
	ExecutionKindTurnInterrupted    ExecutionEventKind = "turn_interrupted"
	ExecutionKindContextCondensed   ExecutionEventKind = "context_condensed"
	ExecutionKindTaskTerminal       ExecutionEventKind = "task_terminal"
)

// ExecutionEventEnvelope is the versioned wire shape for new execution
// events. Payload stays raw in the envelope so future and unknown kinds can be
// replayed without this binary understanding their schema.
type ExecutionEventEnvelope struct {
	EventHeader
	ID       string             `json:"id"`
	ParentID string             `json:"parentId"`
	Seq      int64              `json:"seq"`
	TS       time.Time          `json:"ts"`
	Kind     ExecutionEventKind `json:"kind"`
	Payload  json.RawMessage    `json:"payload"`
}

// UnmarshalJSON also accepts the legacy type discriminator. Marshal always
// writes kind, while the raw payload remains available for lossless replay.
func (event *ExecutionEventEnvelope) UnmarshalJSON(data []byte) error {
	type plainEnvelope ExecutionEventEnvelope
	var decoded plainEnvelope
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if decoded.Kind == "" {
		var legacy struct {
			Type ExecutionEventKind `json:"type"`
		}
		if err := json.Unmarshal(data, &legacy); err != nil {
			return err
		}
		decoded.Kind = legacy.Type
	}
	*event = ExecutionEventEnvelope(decoded)
	return nil
}

// ExecutionEventPayload is implemented by every known typed payload and by
// UnknownExecutionPayload, which keeps forward-compatible event data opaque.
type ExecutionEventPayload interface {
	ExecutionKind() ExecutionEventKind
	Validate() error
}

// UnknownExecutionPayload preserves an event kind and its payload verbatim
// when the current binary does not know that kind.
type UnknownExecutionPayload struct {
	Kind ExecutionEventKind
	Raw  json.RawMessage
}

func (payload UnknownExecutionPayload) ExecutionKind() ExecutionEventKind { return payload.Kind }

func (payload UnknownExecutionPayload) Validate() error {
	if payload.Kind == "" {
		return fmt.Errorf("execution event kind is required")
	}
	if len(payload.Raw) > 0 && !json.Valid(payload.Raw) {
		return fmt.Errorf("unknown execution payload for %q is not valid JSON", payload.Kind)
	}
	return nil
}

func (payload UnknownExecutionPayload) MarshalJSON() ([]byte, error) {
	if err := payload.Validate(); err != nil {
		return nil, err
	}
	if len(payload.Raw) == 0 {
		return []byte("null"), nil
	}
	return append([]byte(nil), payload.Raw...), nil
}

type UserMessagePayload struct {
	MessageID   string              `json:"messageId,omitempty"`
	Content     string              `json:"content,omitempty"`
	Parts       []MessagePart       `json:"parts,omitempty"`
	Attachments []MessageAttachment `json:"attachments,omitempty"`
}

func (UserMessagePayload) ExecutionKind() ExecutionEventKind { return ExecutionKindUserMessage }

func (payload UserMessagePayload) Validate() error {
	if strings.TrimSpace(payload.Content) == "" && len(payload.Parts) == 0 && len(payload.Attachments) == 0 {
		return fmt.Errorf("user_message payload requires content, parts, or attachments")
	}
	return nil
}

type AssistantDeltaPayload struct {
	MessageID string `json:"messageId,omitempty"`
	Delta     string `json:"delta"`
	Index     int64  `json:"index,omitempty"`
}

func (AssistantDeltaPayload) ExecutionKind() ExecutionEventKind {
	return ExecutionKindAssistantDelta
}

func (payload AssistantDeltaPayload) Validate() error {
	if payload.Delta == "" {
		return fmt.Errorf("assistant_delta payload requires delta")
	}
	return nonNegative("assistant_delta index", payload.Index)
}

type ExecutionTokenUsage struct {
	InputTokens       int64 `json:"inputTokens,omitempty"`
	OutputTokens      int64 `json:"outputTokens,omitempty"`
	CachedInputTokens int64 `json:"cachedInputTokens,omitempty"`
	ReasoningTokens   int64 `json:"reasoningTokens,omitempty"`
}

func (usage ExecutionTokenUsage) validate() error {
	for name, value := range map[string]int64{
		"inputTokens":       usage.InputTokens,
		"outputTokens":      usage.OutputTokens,
		"cachedInputTokens": usage.CachedInputTokens,
		"reasoningTokens":   usage.ReasoningTokens,
	} {
		if err := nonNegative(name, value); err != nil {
			return err
		}
	}
	return nil
}

type AssistantCompletedPayload struct {
	MessageID    string               `json:"messageId,omitempty"`
	Content      string               `json:"content,omitempty"`
	Parts        []MessagePart        `json:"parts,omitempty"`
	Model        string               `json:"model,omitempty"`
	FinishReason string               `json:"finishReason,omitempty"`
	DurationMs   int64                `json:"durationMs,omitempty"`
	Usage        *ExecutionTokenUsage `json:"usage,omitempty"`
}

func (AssistantCompletedPayload) ExecutionKind() ExecutionEventKind {
	return ExecutionKindAssistantCompleted
}

func (payload AssistantCompletedPayload) Validate() error {
	if strings.TrimSpace(payload.Content) == "" && len(payload.Parts) == 0 {
		return fmt.Errorf("assistant_completed payload requires content or parts")
	}
	if err := nonNegative("assistant_completed durationMs", payload.DurationMs); err != nil {
		return err
	}
	if payload.Usage != nil {
		return payload.Usage.validate()
	}
	return nil
}

type ToolStartedPayload struct {
	Name             string          `json:"name"`
	Input            json.RawMessage `json:"input,omitempty"`
	WorkingDirectory string          `json:"workingDirectory,omitempty"`
}

func (ToolStartedPayload) ExecutionKind() ExecutionEventKind { return ExecutionKindToolStarted }

func (payload ToolStartedPayload) Validate() error {
	if strings.TrimSpace(payload.Name) == "" {
		return fmt.Errorf("tool_started payload requires name")
	}
	return validOptionalJSON("tool_started input", payload.Input)
}

type ToolOutputChannel string

const (
	ToolOutputStdout   ToolOutputChannel = "stdout"
	ToolOutputStderr   ToolOutputChannel = "stderr"
	ToolOutputResult   ToolOutputChannel = "result"
	ToolOutputProgress ToolOutputChannel = "progress"
)

type ToolOutputPayload struct {
	Name     string            `json:"name,omitempty"`
	Channel  ToolOutputChannel `json:"channel"`
	Content  string            `json:"content"`
	Sequence int64             `json:"sequence,omitempty"`
}

func (ToolOutputPayload) ExecutionKind() ExecutionEventKind { return ExecutionKindToolOutput }

func (payload ToolOutputPayload) Validate() error {
	if strings.TrimSpace(string(payload.Channel)) == "" {
		return fmt.Errorf("tool_output payload requires channel")
	}
	if payload.Content == "" {
		return fmt.Errorf("tool_output payload requires content")
	}
	return nonNegative("tool_output sequence", payload.Sequence)
}

type ToolCompletedPayload struct {
	Name       string          `json:"name"`
	Result     json.RawMessage `json:"result,omitempty"`
	ExitCode   *int            `json:"exitCode,omitempty"`
	DurationMs int64           `json:"durationMs,omitempty"`
}

func (ToolCompletedPayload) ExecutionKind() ExecutionEventKind { return ExecutionKindToolCompleted }

func (payload ToolCompletedPayload) Validate() error {
	if strings.TrimSpace(payload.Name) == "" {
		return fmt.Errorf("tool_completed payload requires name")
	}
	if err := validOptionalJSON("tool_completed result", payload.Result); err != nil {
		return err
	}
	return nonNegative("tool_completed durationMs", payload.DurationMs)
}

type ToolFailedPayload struct {
	Name       string `json:"name"`
	Error      string `json:"error"`
	ErrorCode  string `json:"errorCode,omitempty"`
	Retryable  bool   `json:"retryable,omitempty"`
	ExitCode   *int   `json:"exitCode,omitempty"`
	DurationMs int64  `json:"durationMs,omitempty"`
}

func (ToolFailedPayload) ExecutionKind() ExecutionEventKind { return ExecutionKindToolFailed }

func (payload ToolFailedPayload) Validate() error {
	if strings.TrimSpace(payload.Name) == "" {
		return fmt.Errorf("tool_failed payload requires name")
	}
	if strings.TrimSpace(payload.Error) == "" {
		return fmt.Errorf("tool_failed payload requires error")
	}
	return nonNegative("tool_failed durationMs", payload.DurationMs)
}

type TurnInterruptedPayload struct {
	Reason             string `json:"reason"`
	Message            string `json:"message,omitempty"`
	LastEventID        string `json:"lastEventId,omitempty"`
	HadAssistantOutput bool   `json:"hadAssistantOutput,omitempty"`
}

func (TurnInterruptedPayload) ExecutionKind() ExecutionEventKind {
	return ExecutionKindTurnInterrupted
}

func (payload TurnInterruptedPayload) Validate() error {
	if strings.TrimSpace(payload.Reason) == "" {
		return fmt.Errorf("turn_interrupted payload requires reason")
	}
	return nil
}

type ContextCondensedPayload struct {
	Summary              string   `json:"summary"`
	ContextViewHash      string   `json:"contextViewHash,omitempty"`
	SourceEventIDs       []string `json:"sourceEventIds,omitempty"`
	FromEventID          string   `json:"fromEventId,omitempty"`
	ThroughEventID       string   `json:"throughEventId,omitempty"`
	PreservedToolCallIDs []string `json:"preservedToolCallIds,omitempty"`
	PreservedArtifactIDs []string `json:"preservedArtifactIds,omitempty"`
	InputTokenCount      int64    `json:"inputTokenCount,omitempty"`
	OutputTokenCount     int64    `json:"outputTokenCount,omitempty"`
	RemovedMessageCount  int64    `json:"removedMessageCount,omitempty"`
}

func (ContextCondensedPayload) ExecutionKind() ExecutionEventKind {
	return ExecutionKindContextCondensed
}

func (payload ContextCondensedPayload) Validate() error {
	if strings.TrimSpace(payload.Summary) == "" {
		return fmt.Errorf("context_condensed payload requires summary")
	}
	hasRange := strings.TrimSpace(payload.FromEventID) != "" && strings.TrimSpace(payload.ThroughEventID) != ""
	if len(payload.SourceEventIDs) == 0 && !hasRange {
		return fmt.Errorf("context_condensed payload requires sourceEventIds or a complete event range")
	}
	if err := nonNegative("context_condensed inputTokenCount", payload.InputTokenCount); err != nil {
		return err
	}
	if err := nonNegative("context_condensed outputTokenCount", payload.OutputTokenCount); err != nil {
		return err
	}
	return nonNegative("context_condensed removedMessageCount", payload.RemovedMessageCount)
}

type TaskTerminalStatus string

const (
	TaskTerminalCompleted   TaskTerminalStatus = "completed"
	TaskTerminalFailed      TaskTerminalStatus = "failed"
	TaskTerminalStopped     TaskTerminalStatus = "stopped"
	TaskTerminalCancelled   TaskTerminalStatus = "cancelled"
	TaskTerminalInterrupted TaskTerminalStatus = "interrupted"
)

type TaskTerminalPayload struct {
	Status       TaskTerminalStatus `json:"status"`
	Summary      string             `json:"summary,omitempty"`
	Error        string             `json:"error,omitempty"`
	CheckpointID string             `json:"checkpointId,omitempty"`
	DurationMs   int64              `json:"durationMs,omitempty"`
}

func (TaskTerminalPayload) ExecutionKind() ExecutionEventKind { return ExecutionKindTaskTerminal }

func (payload TaskTerminalPayload) Validate() error {
	switch payload.Status {
	case TaskTerminalCompleted, TaskTerminalFailed, TaskTerminalStopped, TaskTerminalCancelled, TaskTerminalInterrupted:
	default:
		return fmt.Errorf("task_terminal payload has unsupported status %q", payload.Status)
	}
	return nonNegative("task_terminal durationMs", payload.DurationMs)
}

// NewExecutionEventEnvelope encodes a typed payload into the stable wire
// envelope. Header schema version zero is upgraded only for newly built events.
func NewExecutionEventEnvelope(
	header EventHeader,
	id string,
	parentID string,
	seq int64,
	ts time.Time,
	payload ExecutionEventPayload,
) (ExecutionEventEnvelope, error) {
	if payload == nil {
		return ExecutionEventEnvelope{}, fmt.Errorf("execution event payload is required")
	}
	if err := payload.Validate(); err != nil {
		return ExecutionEventEnvelope{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return ExecutionEventEnvelope{}, fmt.Errorf("encode %q payload: %w", payload.ExecutionKind(), err)
	}
	event := ExecutionEventEnvelope{
		EventHeader: header.WithDefaultSchemaVersion(),
		ID:          id,
		ParentID:    parentID,
		Seq:         seq,
		TS:          ts,
		Kind:        payload.ExecutionKind(),
		Payload:     raw,
	}
	if err := event.Validate(); err != nil {
		return ExecutionEventEnvelope{}, err
	}
	return event, nil
}

// DecodeExecutionEvent validates the envelope and returns both the replayable
// raw event and its known typed payload (or UnknownExecutionPayload).
func DecodeExecutionEvent(data []byte) (ExecutionEventEnvelope, ExecutionEventPayload, error) {
	var event ExecutionEventEnvelope
	if err := json.Unmarshal(data, &event); err != nil {
		return ExecutionEventEnvelope{}, nil, fmt.Errorf("decode execution event: %w", err)
	}
	if err := event.validateEnvelope(); err != nil {
		return ExecutionEventEnvelope{}, nil, err
	}
	payload, err := event.DecodePayload()
	if err != nil {
		return ExecutionEventEnvelope{}, nil, err
	}
	return event, payload, nil
}

// DecodePayload decodes and validates a payload based on the envelope kind.
// Unknown kinds are returned as opaque payloads and are not rejected.
func (event ExecutionEventEnvelope) DecodePayload() (ExecutionEventPayload, error) {
	return DecodeExecutionPayload(event.Kind, event.Payload)
}

// DecodeExecutionPayload is the standalone payload decoding entry point.
func DecodeExecutionPayload(kind ExecutionEventKind, raw json.RawMessage) (ExecutionEventPayload, error) {
	var payload ExecutionEventPayload
	switch kind {
	case ExecutionKindUserMessage:
		payload = &UserMessagePayload{}
	case ExecutionKindAssistantDelta:
		payload = &AssistantDeltaPayload{}
	case ExecutionKindAssistantCompleted:
		payload = &AssistantCompletedPayload{}
	case ExecutionKindToolStarted:
		payload = &ToolStartedPayload{}
	case ExecutionKindToolOutput:
		payload = &ToolOutputPayload{}
	case ExecutionKindToolCompleted:
		payload = &ToolCompletedPayload{}
	case ExecutionKindToolFailed:
		payload = &ToolFailedPayload{}
	case ExecutionKindTurnInterrupted:
		payload = &TurnInterruptedPayload{}
	case ExecutionKindContextCondensed:
		payload = &ContextCondensedPayload{}
	case ExecutionKindTaskTerminal:
		payload = &TaskTerminalPayload{}
	default:
		unknown := &UnknownExecutionPayload{Kind: kind, Raw: append(json.RawMessage(nil), raw...)}
		if err := unknown.Validate(); err != nil {
			return nil, err
		}
		return unknown, nil
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("%q payload is required", kind)
	}
	if err := json.Unmarshal(raw, payload); err != nil {
		return nil, fmt.Errorf("decode %q payload: %w", kind, err)
	}
	if err := payload.Validate(); err != nil {
		return nil, fmt.Errorf("validate %q payload: %w", kind, err)
	}
	return payload, nil
}

// Validate checks common envelope invariants and the kind-specific payload.
func (event ExecutionEventEnvelope) Validate() error {
	if err := event.validateEnvelope(); err != nil {
		return err
	}
	_, err := event.DecodePayload()
	return err
}

func (event ExecutionEventEnvelope) validateEnvelope() error {
	if strings.TrimSpace(event.ID) == "" {
		return fmt.Errorf("execution event id is required")
	}
	if event.Seq <= 0 {
		return fmt.Errorf("execution event seq must be positive")
	}
	if event.TS.IsZero() {
		return fmt.Errorf("execution event timestamp is required")
	}
	if event.SchemaVersion < LegacyEventSchemaVersion {
		return fmt.Errorf("execution event schemaVersion must not be negative")
	}
	if event.Generation < 0 {
		return fmt.Errorf("execution event generation must not be negative")
	}
	if event.Kind == "" {
		return fmt.Errorf("execution event kind is required")
	}
	if requiresToolCallID(event.Kind) && strings.TrimSpace(event.ToolCallID) == "" {
		return fmt.Errorf("%q event requires toolCallId", event.Kind)
	}
	return nil
}

func requiresToolCallID(kind ExecutionEventKind) bool {
	switch kind {
	case ExecutionKindToolStarted, ExecutionKindToolOutput, ExecutionKindToolCompleted, ExecutionKindToolFailed:
		return true
	default:
		return false
	}
}

func validOptionalJSON(name string, raw json.RawMessage) error {
	if len(raw) > 0 && !json.Valid(raw) {
		return fmt.Errorf("%s must be valid JSON", name)
	}
	return nil
}

func nonNegative(name string, value int64) error {
	if value < 0 {
		return fmt.Errorf("%s must not be negative", name)
	}
	return nil
}
