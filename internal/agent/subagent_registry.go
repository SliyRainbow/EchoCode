package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	subagentTaskRegistryVersion  = 1
	subagentTaskRegistryFilename = "subagent-tasks.json"
	subagentRegistryFlushPeriod  = 2 * time.Second
)

var subagentRegistryLocks sync.Map

// SubagentArtifactSummary keeps the durable identity of a worker artifact
// without copying patches or file contents into the task registry.
type SubagentArtifactSummary struct {
	Kind   string `json:"kind"`
	Path   string `json:"path,omitempty"`
	Action string `json:"action,omitempty"`
	Status string `json:"status,omitempty"`
}

// SubagentTaskRecord is the recoverable, bounded registration for one worker.
// It is deliberately independent from the live goroutine and cancel function.
type SubagentTaskRecord struct {
	Version           int                       `json:"version"`
	TaskID            string                    `json:"taskId"`
	ParentTaskID      string                    `json:"parentTaskId,omitempty"`
	ParentTurnID      string                    `json:"parentTurnId,omitempty"`
	ParentThreadID    string                    `json:"parentThreadId,omitempty"`
	ParentSessionID   string                    `json:"parentSessionId,omitempty"`
	ProjectID         string                    `json:"projectId"`
	SessionID         string                    `json:"sessionId"`
	Generation        uint64                    `json:"generation"`
	CheckpointID      string                    `json:"checkpointId,omitempty"`
	AgentType         string                    `json:"agentType"`
	Label             string                    `json:"label"`
	Status            string                    `json:"status"`
	InputSummary      string                    `json:"inputSummary,omitempty"`
	ResultSummary     string                    `json:"resultSummary,omitempty"`
	Artifacts         []SubagentArtifactSummary `json:"artifacts,omitempty"`
	ProviderID        string                    `json:"providerId,omitempty"`
	Model             string                    `json:"model,omitempty"`
	CurrentAction     string                    `json:"currentAction,omitempty"`
	CreatedAt         string                    `json:"createdAt"`
	StartedAt         string                    `json:"startedAt,omitempty"`
	UpdatedAt         string                    `json:"updatedAt"`
	CompletedAt       string                    `json:"completedAt,omitempty"`
	CancelRequestedAt string                    `json:"cancelRequestedAt,omitempty"`
	Collected         bool                      `json:"collected,omitempty"`
	RecoveryState     string                    `json:"recoveryState"`
	NeedsResume       bool                      `json:"needsResume,omitempty"`
}

type subagentTaskRegistrySnapshot struct {
	Version   int                  `json:"version"`
	ProjectID string               `json:"projectId"`
	SessionID string               `json:"sessionId"`
	UpdatedAt string               `json:"updatedAt"`
	Tasks     []SubagentTaskRecord `json:"tasks"`
}

func (s *Service) newSubagentTaskRecord(scope subagentExecutionScope, spec delegateTaskSpec, part tools.ResultPart, generation uint64) SubagentTaskRecord {
	projectID, sessionID := s.subagentRegistryIdentity()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	parentTaskID := strings.TrimSpace(scope.BaseRequest.Metadata["task_id"])
	if parentTaskID == "" {
		parentTaskID = strings.TrimSpace(scope.BaseRequest.Metadata["run_id"])
	}
	if parentTaskID == "" {
		parentTaskID = strings.TrimSpace(scope.BaseRequest.TurnID)
	}
	record := normalizeSubagentTaskRecord(SubagentTaskRecord{
		Version:         subagentTaskRegistryVersion,
		TaskID:          part.TaskID,
		ParentTaskID:    parentTaskID,
		ParentTurnID:    scope.BaseRequest.TurnID,
		ParentThreadID:  scope.BaseRequest.ThreadID,
		ParentSessionID: scope.BaseRequest.SessionID,
		ProjectID:       projectID,
		SessionID:       sessionID,
		Generation:      generation,
		CheckpointID:    s.eventHead(),
		AgentType:       spec.AgentType,
		Label:           spec.Label,
		Status:          part.Status,
		InputSummary:    subagentInputSummary(scope.BaseRequest, spec),
		CreatedAt:       now,
		UpdatedAt:       now,
		RecoveryState:   "active",
	})
	record.ProviderID = strings.TrimSpace(part.ProviderID)
	record.Model = strings.TrimSpace(part.Model)
	record.CurrentAction = clipContextText(redactSensitiveText(strings.TrimSpace(part.CurrentAction)), 500)
	record.ResultSummary = clipContextText(redactSensitiveText(strings.TrimSpace(part.Summary)), 8_000)
	record.StartedAt = strings.TrimSpace(part.StartedAt)
	record.CompletedAt = strings.TrimSpace(part.CompletedAt)
	if subagentTaskTerminal(record.Status) {
		record.RecoveryState = "terminal"
	}
	return record
}

func (c *subagentControl) updateTaskRecordFromPartLocked(part tools.ResultPart, force bool) bool {
	if c == nil {
		return false
	}
	now := time.Now().UTC()
	statusChanged := strings.TrimSpace(c.taskRecord.Status) != strings.TrimSpace(part.Status)
	c.taskRecord.Status = normalizedSubagentTaskStatus(part.Status)
	c.taskRecord.ProviderID = strings.TrimSpace(part.ProviderID)
	c.taskRecord.Model = strings.TrimSpace(part.Model)
	c.taskRecord.CurrentAction = clipContextText(redactSensitiveText(strings.TrimSpace(part.CurrentAction)), 500)
	c.taskRecord.StartedAt = strings.TrimSpace(part.StartedAt)
	c.taskRecord.CompletedAt = strings.TrimSpace(part.CompletedAt)
	c.taskRecord.UpdatedAt = now.Format(time.RFC3339Nano)
	if subagentTaskTerminal(c.taskRecord.Status) {
		c.taskRecord.RecoveryState = "terminal"
		c.taskRecord.NeedsResume = false
	} else {
		c.taskRecord.RecoveryState = "active"
	}
	due := force || statusChanged || c.registryLastWrite.IsZero() || now.Sub(c.registryLastWrite) >= subagentRegistryFlushPeriod
	if due {
		c.registryLastWrite = now
	}
	return due
}

func (c *subagentControl) finishTaskRecordLocked(result delegatedTaskResult) {
	if c == nil {
		return
	}
	c.updateTaskRecordFromPartLocked(result.part, true)
	c.taskRecord.ResultSummary = clipContextText(redactSensitiveText(strings.TrimSpace(result.part.Summary)), 8_000)
	c.taskRecord.Artifacts = summarizeSubagentArtifacts(result.artifacts)
	if c.taskRecord.CompletedAt == "" {
		c.taskRecord.CompletedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	c.taskRecord.RecoveryState = "terminal"
	c.taskRecord.NeedsResume = false
}

func (c *subagentControl) taskRecordSnapshot() SubagentTaskRecord {
	if c == nil {
		return SubagentTaskRecord{}
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return cloneSubagentTaskRecord(c.taskRecord)
}

func (c *subagentControl) markCancelRequested() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if subagentTaskTerminal(c.taskRecord.Status) {
		return false
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	c.taskRecord.CancelRequestedAt = now
	c.taskRecord.UpdatedAt = now
	c.registryLastWrite = time.Now().UTC()
	return true
}

func summarizeSubagentArtifacts(parts []tools.ResultPart) []SubagentArtifactSummary {
	if len(parts) == 0 {
		return nil
	}
	output := make([]SubagentArtifactSummary, 0, len(parts))
	seen := make(map[string]bool, len(parts))
	for _, part := range parts {
		if part.Kind != tools.PartDiff && part.Kind != tools.PartFile {
			continue
		}
		summary := SubagentArtifactSummary{
			Kind:   string(part.Kind),
			Path:   clipContextText(redactSensitiveText(strings.TrimSpace(part.Path)), 1_000),
			Action: strings.TrimSpace(part.FileAction),
			Status: strings.TrimSpace(part.Status),
		}
		if summary.Action == "" && part.Kind == tools.PartDiff {
			summary.Action = "modified"
		}
		key := summary.Kind + "\x00" + summary.Path + "\x00" + summary.Action
		if seen[key] {
			continue
		}
		seen[key] = true
		output = append(output, summary)
	}
	return output
}

// ListSubagentTasks returns the durable registration plus any newer live
// controls. It does not mutate unfinished records; recovery is explicit.
func (s *Service) ListSubagentTasks() ([]SubagentTaskRecord, error) {
	registry, err := s.readSubagentTaskRegistry()
	if err != nil {
		return nil, err
	}
	byID := make(map[string]SubagentTaskRecord, len(registry.Tasks))
	for _, record := range registry.Tasks {
		byID[record.TaskID] = cloneSubagentTaskRecord(record)
	}
	for _, record := range s.liveSubagentTaskRecords() {
		byID[record.TaskID] = record
	}
	output := make([]SubagentTaskRecord, 0, len(byID))
	for _, record := range byID {
		output = append(output, cloneSubagentTaskRecord(record))
	}
	sortSubagentTaskRecords(output)
	return output, nil
}

// RecoverInterruptedSubagentTasks marks non-terminal registrations that have
// no live worker as interrupted. Restarting the worker is a later Plan E step;
// this method makes the current limitation durable and observable.
func (s *Service) RecoverInterruptedSubagentTasks() (int, error) {
	path, err := s.subagentRegistryPath()
	if err != nil || path == "" {
		return 0, err
	}
	lock := subagentRegistryLock(path)
	lock.Lock()
	defer lock.Unlock()
	registry, err := readSubagentTaskRegistryFile(path)
	if err != nil {
		return 0, err
	}
	live := make(map[string]bool)
	for _, record := range s.liveSubagentTaskRecords() {
		live[record.TaskID] = true
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	recovered := 0
	for index := range registry.Tasks {
		record := &registry.Tasks[index]
		if live[record.TaskID] || subagentTaskTerminal(record.Status) {
			continue
		}
		record.Status = "interrupted"
		record.RecoveryState = "worker_not_restored"
		record.NeedsResume = true
		record.ResultSummary = "EchoCode restarted while this subagent was active; the task registration was preserved, but the worker was not automatically resumed."
		record.CompletedAt = now
		record.UpdatedAt = now
		recovered++
	}
	if recovered == 0 {
		return 0, nil
	}
	registry.UpdatedAt = now
	return recovered, writeSubagentTaskRegistryFile(path, registry)
}

func (s *Service) persistSubagentControl(control *subagentControl) error {
	if control == nil {
		return nil
	}
	return s.persistSubagentTaskRecords([]SubagentTaskRecord{control.taskRecordSnapshot()})
}

func (s *Service) persistSubagentTaskRecords(records []SubagentTaskRecord) error {
	if len(records) == 0 {
		return nil
	}
	path, err := s.subagentRegistryPath()
	if err != nil || path == "" {
		return err
	}
	lock := subagentRegistryLock(path)
	lock.Lock()
	defer lock.Unlock()
	registry, err := readSubagentTaskRegistryFile(path)
	if err != nil {
		return err
	}
	byID := make(map[string]int, len(registry.Tasks))
	for index := range registry.Tasks {
		byID[registry.Tasks[index].TaskID] = index
	}
	for _, incoming := range records {
		incoming = normalizeSubagentTaskRecord(incoming)
		if incoming.TaskID == "" {
			return errors.New("subagent task ID is empty")
		}
		if index, ok := byID[incoming.TaskID]; ok {
			current := registry.Tasks[index]
			if incoming.Generation < current.Generation {
				continue
			}
			if incoming.Generation == current.Generation {
				incoming = mergeSubagentTaskRecord(current, incoming)
			}
			registry.Tasks[index] = incoming
			continue
		}
		byID[incoming.TaskID] = len(registry.Tasks)
		registry.Tasks = append(registry.Tasks, incoming)
	}
	projectID, sessionID := s.subagentRegistryIdentity()
	registry.Version = subagentTaskRegistryVersion
	registry.ProjectID = projectID
	registry.SessionID = sessionID
	registry.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	sortSubagentTaskRecords(registry.Tasks)
	return writeSubagentTaskRegistryFile(path, registry)
}

func (s *Service) readSubagentTaskRegistry() (subagentTaskRegistrySnapshot, error) {
	path, err := s.subagentRegistryPath()
	if err != nil || path == "" {
		projectID, sessionID := s.subagentRegistryIdentity()
		return subagentTaskRegistrySnapshot{Version: subagentTaskRegistryVersion, ProjectID: projectID, SessionID: sessionID, Tasks: []SubagentTaskRecord{}}, err
	}
	lock := subagentRegistryLock(path)
	lock.Lock()
	defer lock.Unlock()
	return readSubagentTaskRegistryFile(path)
}

func (s *Service) subagentRegistryPath() (string, error) {
	root := strings.TrimSpace(s.config.SessionsDir)
	if root == "" {
		return "", nil
	}
	projectID, sessionID := s.subagentRegistryIdentity()
	dir, err := safeSessionEventDir(root, projectID, sessionID)
	if err != nil {
		return "", fmt.Errorf("resolve subagent registry directory: %w", err)
	}
	return filepath.Join(dir, subagentTaskRegistryFilename), nil
}

func (s *Service) subagentRegistryIdentity() (projectID, sessionID string) {
	if s != nil {
		projectID = strings.TrimSpace(s.projectID)
		sessionID = strings.TrimSpace(s.sessionID)
	}
	if projectID == "" {
		projectID = "default"
	}
	if sessionID == "" {
		sessionID = "default"
	}
	return projectID, sessionID
}

func (s *Service) liveSubagentTaskRecords() []SubagentTaskRecord {
	if s == nil {
		return nil
	}
	s.subagentMu.Lock()
	controls := make([]*subagentControl, 0, len(s.subagents))
	for _, control := range s.subagents {
		controls = append(controls, control)
	}
	s.subagentMu.Unlock()
	output := make([]SubagentTaskRecord, 0, len(controls))
	for _, control := range controls {
		record := control.taskRecordSnapshot()
		if record.TaskID != "" {
			output = append(output, record)
		}
	}
	return output
}

func readSubagentTaskRegistryFile(path string) (subagentTaskRegistrySnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return subagentTaskRegistrySnapshot{Version: subagentTaskRegistryVersion, Tasks: []SubagentTaskRecord{}}, nil
		}
		return subagentTaskRegistrySnapshot{}, err
	}
	var registry subagentTaskRegistrySnapshot
	if err := json.Unmarshal(data, &registry); err != nil {
		return subagentTaskRegistrySnapshot{}, fmt.Errorf("decode subagent task registry: %w", err)
	}
	registry.Version = subagentTaskRegistryVersion
	for index := range registry.Tasks {
		registry.Tasks[index] = normalizeSubagentTaskRecord(registry.Tasks[index])
	}
	sortSubagentTaskRecords(registry.Tasks)
	return registry, nil
}

func writeSubagentTaskRegistryFile(path string, registry subagentTaskRegistrySnapshot) error {
	data, err := json.MarshalIndent(registry, "", "  ")
	if err != nil {
		return err
	}
	return tools.WriteBytesAtomic(path, data, 0o600)
}

func normalizeSubagentTaskRecord(record SubagentTaskRecord) SubagentTaskRecord {
	record.Version = subagentTaskRegistryVersion
	record.TaskID = strings.TrimSpace(record.TaskID)
	record.ParentTaskID = strings.TrimSpace(record.ParentTaskID)
	record.ParentTurnID = strings.TrimSpace(record.ParentTurnID)
	record.ParentThreadID = strings.TrimSpace(record.ParentThreadID)
	record.ParentSessionID = strings.TrimSpace(record.ParentSessionID)
	record.ProjectID = strings.TrimSpace(record.ProjectID)
	record.SessionID = strings.TrimSpace(record.SessionID)
	record.CheckpointID = strings.TrimSpace(record.CheckpointID)
	record.AgentType = strings.TrimSpace(record.AgentType)
	record.Label = clipContextText(strings.TrimSpace(record.Label), 200)
	record.Status = normalizedSubagentTaskStatus(record.Status)
	record.InputSummary = clipContextText(redactSensitiveText(strings.TrimSpace(record.InputSummary)), 6_000)
	record.ResultSummary = clipContextText(redactSensitiveText(strings.TrimSpace(record.ResultSummary)), 8_000)
	record.CurrentAction = clipContextText(redactSensitiveText(strings.TrimSpace(record.CurrentAction)), 500)
	record.Artifacts = append([]SubagentArtifactSummary(nil), record.Artifacts...)
	if record.RecoveryState == "" {
		if subagentTaskTerminal(record.Status) {
			record.RecoveryState = "terminal"
		} else {
			record.RecoveryState = "active"
		}
	}
	return record
}

func normalizedSubagentTaskStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "ok", "completed":
		return "completed"
	case "error", "failed":
		return "failed"
	case "cancelled", "interrupted", "pending", "running", "waiting", "retrying":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return "pending"
	}
}

func subagentTaskTerminal(status string) bool {
	switch normalizedSubagentTaskStatus(status) {
	case "completed", "failed", "cancelled", "interrupted":
		return true
	default:
		return false
	}
}

func cloneSubagentTaskRecord(record SubagentTaskRecord) SubagentTaskRecord {
	record.Artifacts = append([]SubagentArtifactSummary(nil), record.Artifacts...)
	return record
}

func mergeSubagentTaskRecord(current, incoming SubagentTaskRecord) SubagentTaskRecord {
	currentTerminal := subagentTaskTerminal(current.Status)
	incomingTerminal := subagentTaskTerminal(incoming.Status)
	merged := incoming
	switch {
	case currentTerminal && !incomingTerminal:
		merged = current
	case !currentTerminal && incomingTerminal:
		merged = incoming
	case subagentTaskRecordUpdatedBefore(incoming, current):
		merged = current
	}

	// Collection and cancellation are monotonic observations. A delayed atomic
	// write must not erase either after another goroutine has persisted it.
	merged.Collected = current.Collected || incoming.Collected
	if merged.CancelRequestedAt == "" {
		if current.CancelRequestedAt != "" {
			merged.CancelRequestedAt = current.CancelRequestedAt
		} else {
			merged.CancelRequestedAt = incoming.CancelRequestedAt
		}
	}
	return merged
}

func subagentTaskRecordUpdatedBefore(left, right SubagentTaskRecord) bool {
	leftValue := strings.TrimSpace(left.UpdatedAt)
	rightValue := strings.TrimSpace(right.UpdatedAt)
	if leftValue == "" {
		return rightValue != ""
	}
	if rightValue == "" {
		return false
	}
	leftTime, leftErr := time.Parse(time.RFC3339Nano, leftValue)
	rightTime, rightErr := time.Parse(time.RFC3339Nano, rightValue)
	if leftErr == nil && rightErr == nil {
		return leftTime.Before(rightTime)
	}
	return leftValue < rightValue
}

func sortSubagentTaskRecords(records []SubagentTaskRecord) {
	sort.SliceStable(records, func(left, right int) bool {
		if records[left].Generation != records[right].Generation {
			return records[left].Generation < records[right].Generation
		}
		return records[left].TaskID < records[right].TaskID
	})
}

func subagentRegistryLock(path string) *sync.Mutex {
	value, _ := subagentRegistryLocks.LoadOrStore(filepath.Clean(path), &sync.Mutex{})
	return value.(*sync.Mutex)
}
