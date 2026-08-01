package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/pathutil"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const artifactRegistryVersion = 2

// ArtifactRecord is exposed through WorkbenchState while eventlog remains the
// durable owner of the schema.
type ArtifactRecord = eventlog.ArtifactRecord

type artifactCandidate struct {
	path       string
	action     string
	toolCallID string
}

func (s *Service) recordMessageArtifacts(parts []tools.ResultPart) error {
	if len(parts) == 0 {
		return nil
	}
	toolNames := make(map[string]string)
	registered := make(map[string]bool)
	if s.eventStore != nil {
		for _, event := range s.eventStore.Events() {
			if event.Type != eventlog.EventArtifactUpdate {
				continue
			}
			for _, record := range event.Payload.Artifacts {
				registered[record.ToolCallID+"\x00"+artifactPathKey(record.Path)] = true
			}
		}
	}
	for _, part := range parts {
		if part.Kind == tools.PartToolCall && strings.TrimSpace(part.ToolCallID) != "" {
			toolNames[part.ToolCallID] = part.Name
		}
	}
	groups := make(map[string][]tools.ResultPart)
	order := make([]string, 0)
	for _, part := range parts {
		if part.Kind != tools.PartFile || strings.TrimSpace(part.Path) == "" {
			continue
		}
		absolute, _ := s.canonicalArtifactPath(part.Path)
		if registered[part.ToolCallID+"\x00"+artifactPathKey(absolute)] {
			continue
		}
		if _, exists := groups[part.ToolCallID]; !exists {
			order = append(order, part.ToolCallID)
		}
		groups[part.ToolCallID] = append(groups[part.ToolCallID], part)
	}
	for _, toolCallID := range order {
		if _, err := s.recordToolArtifacts(toolNames[toolCallID], toolCallID, tools.Result{Parts: groups[toolCallID]}); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) recordToolArtifacts(toolName, toolCallID string, result tools.Result) ([]ArtifactRecord, error) {
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()

	candidates := make(map[string]artifactCandidate)
	order := make([]string, 0, len(result.Parts)+len(result.Changes))
	add := func(path, action, sourceToolCallID string) {
		path = strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(path))
		if path == "" {
			return
		}
		absolute, _ := s.canonicalArtifactPath(path)
		key := artifactPathKey(absolute)
		if key == "" {
			key = artifactPathKey(path)
		}
		current, exists := candidates[key]
		action = normalizeArtifactAction(action)
		if strings.TrimSpace(sourceToolCallID) == "" {
			sourceToolCallID = toolCallID
		}
		if !exists {
			order = append(order, key)
			candidates[key] = artifactCandidate{path: path, action: action, toolCallID: sourceToolCallID}
			return
		}
		if artifactActionPriority(action) >= artifactActionPriority(current.action) {
			current.action = action
		}
		if current.toolCallID == "" {
			current.toolCallID = sourceToolCallID
		}
		candidates[key] = current
	}

	for _, part := range result.Parts {
		if part.Kind != tools.PartFile || strings.TrimSpace(part.Path) == "" {
			continue
		}
		action := part.FileAction
		if strings.TrimSpace(action) == "" && part.Created {
			action = "created"
		}
		add(part.Path, action, part.ToolCallID)
	}
	for _, change := range result.Changes {
		action := "modified"
		switch {
		case change.Deleted:
			action = "deleted"
		case !change.Existed:
			action = "created"
		}
		add(change.Path, action, toolCallID)
	}
	if len(order) == 0 {
		return nil, nil
	}

	messageID, branchAnchor := s.currentArtifactOwners()
	records := make([]ArtifactRecord, 0, len(order))
	for _, key := range order {
		candidate := candidates[key]
		record := s.inspectArtifact(candidate.path, candidate.action)
		record.Tool = strings.TrimSpace(toolName)
		record.ToolCallID = strings.TrimSpace(candidate.toolCallID)
		record.MessageID = messageID
		record.ProjectID = strings.TrimSpace(s.projectID)
		record.SessionID = strings.TrimSpace(s.sessionID)
		record.BranchID = branchAnchor
		record.ID = artifactRecordID(record)
		records = append(records, record)
	}

	if s.eventStore == nil {
		return records, nil
	}
	existing := make(map[string]bool)
	for _, event := range s.eventStore.Events() {
		if event.Type != eventlog.EventArtifactUpdate {
			continue
		}
		for _, record := range event.Payload.Artifacts {
			existing[record.ID] = true
		}
	}
	pending := make([]ArtifactRecord, 0, len(records))
	for _, record := range records {
		if !existing[record.ID] {
			pending = append(pending, record)
		}
	}
	if len(pending) == 0 {
		return records, nil
	}
	eventToolCallID := ""
	if len(pending) == 1 {
		eventToolCallID = pending[0].ToolCallID
	}
	event, err := s.appendEvent(eventlog.EventPayload{Artifacts: pending}, eventlog.EventArtifactUpdate, eventToolCallID)
	if err != nil {
		return records, fmt.Errorf("持久化产物登记失败: %w", err)
	}
	for index := range records {
		if !existing[records[index].ID] {
			records[index].EventID = event.ID
		}
	}
	return records, nil
}

func (s *Service) currentArtifactOwners() (messageID, branchAnchor string) {
	if s.eventStore == nil {
		return "", ""
	}
	events := s.eventStore.Events()
	for index := len(events) - 1; index >= 0; index-- {
		if messageID == "" && events[index].Type == eventlog.EventUserMessage {
			messageID = events[index].ID
		}
		if messageID != "" {
			break
		}
	}
	return messageID, s.eventStore.Head()
}

func (s *Service) inspectArtifact(path, action string) ArtifactRecord {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	absolute, display := s.canonicalArtifactPath(path)
	record := ArtifactRecord{
		Path:                   absolute,
		DisplayPath:            display,
		Name:                   filepath.Base(absolute),
		Action:                 normalizeArtifactAction(action),
		Status:                 "available",
		StructuralVerification: "pending",
		VisualVerification:     "not_applicable",
		LastCheckedAt:          now,
	}
	if record.Action == "deleted" {
		if _, err := os.Stat(absolute); os.IsNotExist(err) {
			record.Status = "deleted"
			record.StructuralVerification = "not_applicable"
			return record
		}
	}

	info, err := os.Stat(absolute)
	if err != nil {
		record.Status = "missing"
		if !os.IsNotExist(err) {
			record.Status = "unreadable"
		}
		record.StructuralVerification = "failed"
		record.FailureReason = artifactFailureReason(err)
		return record
	}
	if info.IsDir() {
		record.Status = "invalid"
		record.StructuralVerification = "failed"
		record.FailureReason = "产物路径指向目录而不是文件"
		return record
	}
	record.Size = info.Size()
	record.ModifiedAt = info.ModTime().UTC().Format(time.RFC3339Nano)
	record.PreviewReference = absolute

	header, digest, readErr := artifactDigest(absolute)
	if readErr != nil {
		record.Status = "unreadable"
		record.StructuralVerification = "failed"
		record.FailureReason = artifactFailureReason(readErr)
		return record
	}
	record.SHA256 = digest
	record.MIMEType = artifactMIMEType(absolute, header)
	record.FileType = artifactFileType(absolute, record.MIMEType)
	record.StructuralVerification = "passed"
	if isVisuallyInspectableArtifact(record.FileType, record.MIMEType) {
		record.VisualVerification = "pending"
	}

	if _, _, supported := artifacts.Detect(absolute); supported {
		_, previewErr := artifacts.PreviewFile(absolute, artifacts.PreviewOptions{
			MaxTextChars: 64 << 10,
			MaxSheets:    20,
			MaxRows:      20,
			MaxColumns:   30,
		})
		if previewErr != nil {
			record.Status = "invalid"
			record.StructuralVerification = "failed"
			record.FailureReason = artifactFailureReason(previewErr)
		}
	}
	return record
}

func (s *Service) canonicalArtifactPath(path string) (absolute, display string) {
	path = filepath.FromSlash(strings.TrimSpace(path))
	if !filepath.IsAbs(path) {
		if root := strings.TrimSpace(s.runtimeSettings.WorkspaceRoot); root != "" {
			path = filepath.Join(root, path)
		}
	}
	absolute, err := pathutil.Canonical(path)
	if err != nil {
		absolute, err = filepath.Abs(path)
		if err != nil {
			absolute = filepath.Clean(path)
		}
	}
	absolute = filepath.Clean(absolute)
	display = absolute
	if root := strings.TrimSpace(s.runtimeSettings.WorkspaceRoot); root != "" {
		canonicalRoot, rootErr := pathutil.Canonical(root)
		if rootErr != nil {
			canonicalRoot = filepath.Clean(root)
		}
		if relative, relativeErr := filepath.Rel(canonicalRoot, absolute); relativeErr == nil &&
			relative != "." && relative != ".." && !filepath.IsAbs(relative) &&
			!strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			display = filepath.ToSlash(relative)
		}
	}
	return absolute, display
}

func artifactDigest(path string) ([]byte, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	header := make([]byte, 512)
	read, readErr := io.ReadFull(file, header)
	if readErr != nil && readErr != io.EOF && readErr != io.ErrUnexpectedEOF {
		return nil, "", readErr
	}
	header = header[:read]
	hasher := sha256.New()
	if _, err := hasher.Write(header); err != nil {
		return nil, "", err
	}
	if _, err := io.Copy(hasher, file); err != nil {
		return nil, "", err
	}
	return header, hex.EncodeToString(hasher.Sum(nil)), nil
}

func artifactMIMEType(path string, header []byte) string {
	if _, mimeType, supported := artifacts.Detect(path); supported {
		return mimeType
	}
	if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(path))); value != "" {
		if index := strings.IndexByte(value, ';'); index >= 0 {
			value = value[:index]
		}
		return value
	}
	if len(header) > 0 {
		return http.DetectContentType(header)
	}
	return "application/octet-stream"
}

func artifactFileType(path, mimeType string) string {
	if kind, _, supported := artifacts.Detect(path); supported {
		return string(kind)
	}
	extension := strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")
	switch {
	case strings.HasPrefix(mimeType, "image/"):
		return "image"
	case mimeType == "application/pdf":
		return "pdf"
	case mimeType == "text/html":
		return "html"
	case strings.HasPrefix(mimeType, "text/"):
		return "text"
	case extension != "":
		return extension
	default:
		return "binary"
	}
}

func isVisuallyInspectableArtifact(fileType, mimeType string) bool {
	switch fileType {
	case "document", "spreadsheet", "presentation", "image", "pdf", "html":
		return true
	}
	return strings.HasPrefix(mimeType, "image/")
}

func artifactFailureReason(err error) string {
	if err == nil {
		return ""
	}
	value := strings.TrimSpace(redactSensitiveText(err.Error()))
	if len([]rune(value)) > 1000 {
		value = string([]rune(value)[:1000]) + "..."
	}
	return value
}

func normalizeArtifactAction(action string) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "created", "modified", "deleted", "available":
		return strings.ToLower(strings.TrimSpace(action))
	default:
		return "available"
	}
}

func artifactActionPriority(action string) int {
	switch normalizeArtifactAction(action) {
	case "deleted":
		return 4
	case "created", "modified":
		return 3
	default:
		return 1
	}
}

func artifactPathKey(path string) string {
	path = filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	if filepath.IsAbs(path) {
		if canonical, err := pathutil.Canonical(path); err == nil {
			path = filepath.Clean(canonical)
		}
	}
	if runtime.GOOS == "windows" {
		path = strings.ToLower(path)
	}
	return path
}

func artifactRecordID(record ArtifactRecord) string {
	identity := strings.Join([]string{
		fmt.Sprintf("v%d", artifactRegistryVersion),
		record.ProjectID,
		record.SessionID,
		artifactPathKey(record.Path),
		record.Action,
		record.ToolCallID,
		record.SHA256,
		record.Status,
		record.ModifiedAt,
		record.StructuralVerification,
		record.VisualVerification,
		record.RenderReference,
		record.FailureReason,
	}, "\x00")
	sum := sha256.Sum256([]byte(identity))
	return "artifact-" + hex.EncodeToString(sum[:16])
}

func (s *Service) sessionArtifactRecordsLocked() []ArtifactRecord {
	if s.eventStore == nil {
		return []ArtifactRecord{}
	}
	return artifactRecordsFromEvents(s.eventStore.Events(), s.projectID, s.sessionID)
}

// ListSessionArtifacts returns the current branch's latest record for each
// canonical path. It never scans the workspace to rediscover missing state.
func (s *Service) ListSessionArtifacts() []ArtifactRecord {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.sessionArtifactRecordsLocked()
}

func artifactRecordsFromEvents(events []eventlog.Event, projectID, sessionID string) []ArtifactRecord {
	if len(events) == 0 {
		return []ArtifactRecord{}
	}
	nextCheckpoint := make([]string, len(events))
	next := ""
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].Type == eventlog.EventCheckpoint {
			next = events[index].ID
		}
		nextCheckpoint[index] = next
	}

	type indexedRecord struct {
		record ArtifactRecord
		seq    int64
	}
	latest := make(map[string]indexedRecord)
	messageID := ""
	branchID := events[len(events)-1].ID
	for index, event := range events {
		if event.Type == eventlog.EventUserMessage {
			messageID = event.ID
		}
		if event.Type != eventlog.EventArtifactUpdate {
			continue
		}
		for _, stored := range event.Payload.Artifacts {
			record := stored
			record.EventID = event.ID
			record.BranchID = branchID
			if record.MessageID == "" {
				record.MessageID = messageID
			}
			if record.ProjectID == "" {
				record.ProjectID = projectID
			}
			if record.SessionID == "" {
				record.SessionID = sessionID
			}
			if record.CheckpointID == "" {
				record.CheckpointID = nextCheckpoint[index]
			}
			key := artifactPathKey(record.Path)
			if previous, ok := latest[key]; ok && record.Action == "available" && previous.record.Action != "deleted" {
				record.Action = previous.record.Action
				record.Tool = previous.record.Tool
				record.ToolCallID = previous.record.ToolCallID
				record.MessageID = previous.record.MessageID
				record.CheckpointID = previous.record.CheckpointID
			}
			latest[key] = indexedRecord{record: record, seq: event.Seq}
		}
	}
	ordered := make([]indexedRecord, 0, len(latest))
	for _, record := range latest {
		ordered = append(ordered, record)
	}
	sort.SliceStable(ordered, func(left, right int) bool { return ordered[left].seq < ordered[right].seq })
	result := make([]ArtifactRecord, 0, len(ordered))
	for _, record := range ordered {
		result = append(result, record.record)
	}
	return result
}

func artifactReferencesFromRecords(records []ArtifactRecord) []localArtifactReference {
	references := make([]localArtifactReference, 0, len(records))
	for _, record := range records {
		references = append(references, localArtifactReference{
			Path:                   record.Path,
			Action:                 record.Action,
			FileType:               record.FileType,
			MIMEType:               record.MIMEType,
			Size:                   record.Size,
			SHA256:                 record.SHA256,
			Status:                 record.Status,
			StructuralVerification: record.StructuralVerification,
			VisualVerification:     record.VisualVerification,
			ToolCallID:             record.ToolCallID,
			PreviewReference:       record.PreviewReference,
		})
	}
	return references
}
