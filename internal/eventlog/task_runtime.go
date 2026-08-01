package eventlog

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const taskRuntimeFilename = "task-runtime.json"

// TaskRuntimeRecord is the crash-recovery snapshot for the one task allowed
// to run in a session. It is stored outside events.jsonl so streaming updates
// do not move the conversation head or grow the append-only log quadratically.
// AnchorID still binds the snapshot to the selected event branch.
type TaskRuntimeRecord struct {
	Version      int           `json:"version"`
	TaskID       string        `json:"taskId"`
	Generation   uint64        `json:"generation,omitempty"`
	AnchorID     string        `json:"anchorId,omitempty"`
	StartedAt    string        `json:"startedAt"`
	UpdatedAt    string        `json:"updatedAt"`
	Status       string        `json:"status"`
	Message      string        `json:"message,omitempty"`
	Model        string        `json:"model,omitempty"`
	Content      string        `json:"content,omitempty"`
	Reasoning    string        `json:"reasoning,omitempty"`
	DurationMs   int64         `json:"durationMs,omitempty"`
	Parts        []MessagePart `json:"parts,omitempty"`
	LastEvent    string        `json:"lastEvent,omitempty"`
	TurnSequence int           `json:"turnSequence,omitempty"`
}

func (record TaskRuntimeRecord) Normalized() TaskRuntimeRecord {
	record.Version = 1
	record.TaskID = strings.TrimSpace(record.TaskID)
	record.AnchorID = strings.TrimSpace(record.AnchorID)
	record.StartedAt = strings.TrimSpace(record.StartedAt)
	record.UpdatedAt = strings.TrimSpace(record.UpdatedAt)
	record.Status = strings.TrimSpace(record.Status)
	if record.Status == "" {
		record.Status = "running"
	}
	if record.StartedAt == "" {
		record.StartedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if record.UpdatedAt == "" {
		record.UpdatedAt = record.StartedAt
	}
	return record
}

func (record TaskRuntimeRecord) Terminal() bool {
	switch strings.TrimSpace(record.Status) {
	case "completed", "failed", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}

func (s *Store) taskRuntimePath() string {
	return filepath.Join(s.dir, taskRuntimeFilename)
}

// WriteTaskRuntime atomically replaces the recoverable runtime snapshot. The
// event log remains append-only; this file is intentionally mutable and small.
func (s *Store) WriteTaskRuntime(record TaskRuntimeRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	record = record.Normalized()
	if record.TaskID == "" {
		return errors.New("task runtime ID is empty")
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	return tools.WriteBytesAtomic(s.taskRuntimePath(), data, 0o600)
}

// ReadTaskRuntime returns the most recent task snapshot without changing the
// event head. A missing file is a normal state for sessions created by older
// EchoCode versions.
func (s *Store) ReadTaskRuntime() (TaskRuntimeRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.taskRuntimePath())
	if err != nil {
		if os.IsNotExist(err) {
			return TaskRuntimeRecord{}, false, nil
		}
		return TaskRuntimeRecord{}, false, err
	}
	var record TaskRuntimeRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return TaskRuntimeRecord{}, false, err
	}
	record = record.Normalized()
	if record.TaskID == "" {
		return TaskRuntimeRecord{}, false, errors.New("task runtime ID is empty")
	}
	return record, true, nil
}
