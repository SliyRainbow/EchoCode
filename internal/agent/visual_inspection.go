package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const maxVisualRenderStates = 24

const visualVerificationRecoveryKind = "visual-verification-recovery"

type visualRenderState struct {
	Result    tools.ArtifactRenderResult
	CreatedAt time.Time
}

type visualInspectionPayload struct {
	Verdict string              `json:"verdict"`
	Summary string              `json:"summary"`
	Issues  []tools.VisualIssue `json:"issues"`
}

func (s *Service) RenderArtifact(ctx context.Context, request tools.ArtifactRenderRequest) (tools.ArtifactRenderResult, error) {
	if s.config.ArtifactRenderer == nil {
		return tools.ArtifactRenderResult{}, errors.New("visual renderer is unavailable")
	}
	request.Source = strings.ToLower(strings.TrimSpace(request.Source))
	if request.Source == "" {
		request.Source = tools.VisualSourceFile
	}
	if request.Width <= 0 {
		request.Width = 1440
	}
	if request.Height <= 0 {
		request.Height = 1200
	}
	request.Width = clampVisualDimension(request.Width, 320, 2560)
	request.Height = clampVisualDimension(request.Height, 240, 4096)

	var sourceRecord ArtifactRecord
	if request.Source == tools.VisualSourceFile {
		sourceRecord = s.inspectArtifact(request.Path, "available")
		if sourceRecord.Status != "available" || sourceRecord.StructuralVerification == "failed" {
			return tools.ArtifactRenderResult{}, fmt.Errorf("artifact is not readable: %s", sourceRecord.FailureReason)
		}
		if !isVisuallyInspectableArtifact(sourceRecord.FileType, sourceRecord.MIMEType) {
			return tools.ArtifactRenderResult{}, fmt.Errorf("file type %s does not have a visual renderer", sourceRecord.FileType)
		}
	}

	tools.EmitProgress(ctx, tools.ResultPart{
		Kind: tools.PartToolCall, Name: "render_artifact", Status: "running",
		Input: visualRequestDisplay(request), Output: "Rendering a bounded visual preview",
	})
	rendered, err := s.config.ArtifactRenderer.RenderArtifact(ctx, request)
	if err != nil {
		return tools.ArtifactRenderResult{}, err
	}
	if err := validateVisualRenderReference(rendered.Reference); err != nil {
		return tools.ArtifactRenderResult{}, err
	}
	if rendered.Source == "" {
		rendered.Source = request.Source
	}
	if rendered.Path == "" {
		rendered.Path = request.Path
	}
	if rendered.MIMEType == "" {
		rendered.MIMEType = "image/png"
	}
	if rendered.Width <= 0 {
		rendered.Width = request.Width
	}
	if rendered.Height <= 0 {
		rendered.Height = request.Height
	}
	if request.Source == tools.VisualSourceFile {
		current := s.inspectArtifact(request.Path, "available")
		if current.SHA256 == "" || current.SHA256 != sourceRecord.SHA256 {
			return tools.ArtifactRenderResult{}, errors.New("artifact changed while it was being rendered; render the current version again")
		}
		rendered.SourceSHA256 = current.SHA256
	}
	if strings.TrimSpace(rendered.ID) == "" {
		rendered.ID = visualRenderID(rendered)
	}

	s.visualMu.Lock()
	if s.visualRenders == nil {
		s.visualRenders = make(map[string]visualRenderState)
	}
	if _, exists := s.visualRenders[rendered.ID]; !exists {
		s.visualRenderOrder = append(s.visualRenderOrder, rendered.ID)
	}
	s.visualRenders[rendered.ID] = visualRenderState{Result: rendered, CreatedAt: time.Now().UTC()}
	for len(s.visualRenderOrder) > maxVisualRenderStates {
		oldest := s.visualRenderOrder[0]
		s.visualRenderOrder = s.visualRenderOrder[1:]
		delete(s.visualRenders, oldest)
	}
	s.visualMu.Unlock()

	if request.Source == tools.VisualSourceFile {
		if err := s.persistVisualArtifactState(request.Path, rendered.SourceSHA256, "rendered", rendered.Reference, ""); err != nil {
			return tools.ArtifactRenderResult{}, err
		}
	}
	return rendered, nil
}

func (s *Service) InspectVisual(ctx context.Context, request tools.VisualInspectionRequest) (tools.VisualInspectionResult, error) {
	rendered, err := s.resolveVisualRender(request)
	if err != nil {
		return tools.VisualInspectionResult{}, err
	}
	if rendered.Path != "" {
		current := s.inspectArtifact(rendered.Path, "available")
		if rendered.SourceSHA256 == "" || current.SHA256 != rendered.SourceSHA256 {
			return tools.VisualInspectionResult{}, errors.New("artifact changed after rendering; call render_artifact again before visual inspection")
		}
	}
	attachment, err := tools.AttachmentFromFile(rendered.Reference)
	if err != nil {
		return tools.VisualInspectionResult{}, fmt.Errorf("read rendered image: %w", err)
	}
	if !strings.HasPrefix(strings.ToLower(attachment.MIMEType), "image/") {
		return tools.VisualInspectionResult{}, fmt.Errorf("render output is not an image: %s", attachment.MIMEType)
	}

	routes := s.visualInspectionRoutes()
	failures := make([]string, 0, len(routes))
	for _, route := range routes {
		if err := ctx.Err(); err != nil {
			return tools.VisualInspectionResult{}, err
		}
		tools.EmitProgress(ctx, tools.ResultPart{
			Kind: tools.PartToolCall, Name: "inspect_visual", Status: "waiting", Input: rendered.ID,
			Output: fmt.Sprintf("Inspecting with %s / %s", route.Provider.Name, route.ModelID),
		})
		provider, providerErr := s.chatProviderForRoute(route)
		if providerErr != nil {
			failures = append(failures, route.Provider.Name+": "+redactSensitiveText(providerErr.Error()))
			continue
		}
		chatRequest := visualInspectionChatRequest(rendered, request.Criteria, attachment)
		applyRouteToChatRequest(&chatRequest, route)
		completion, completionErr := collectProviderStream(ctx, provider, chatRequest, nil)
		if completion.Usage != nil {
			s.recordLiveUsage(completion.Usage, route, nil)
		}
		if completionErr != nil {
			failures = append(failures, route.Provider.Name+" / "+route.ModelID+": "+redactSensitiveText(completionErr.Error()))
			continue
		}
		payload, parseErr := parseVisualInspectionPayload(completion.Content)
		if parseErr != nil {
			failures = append(failures, route.Provider.Name+" / "+route.ModelID+": "+parseErr.Error())
			continue
		}
		result := tools.VisualInspectionResult{
			RenderID:  rendered.ID,
			Path:      rendered.Path,
			Mode:      "vision",
			Verdict:   payload.Verdict,
			Summary:   payload.Summary,
			Issues:    payload.Issues,
			Provider:  route.Provider.Name,
			Model:     route.ModelID,
			CheckedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		status := "passed"
		failureReason := ""
		if result.Verdict == "changes_required" {
			status = "failed"
			failureReason = visualIssueSummary(result)
		}
		if rendered.Path != "" {
			if err := s.persistVisualArtifactState(rendered.Path, rendered.SourceSHA256, status, rendered.Reference, failureReason); err != nil {
				return tools.VisualInspectionResult{}, err
			}
		}
		return result, nil
	}

	reason := "No configured image-capable route completed the inspection."
	if len(failures) > 0 {
		reason += " " + strings.Join(failures, "; ")
	}
	tools.EmitProgress(ctx, tools.ResultPart{
		Kind: tools.PartToolCall, Name: "inspect_visual", Status: "retrying", Input: rendered.ID,
		Output: "Vision routing was unavailable; running an explicit structural fallback",
	})
	result := s.structuralVisualFallback(rendered, reason)
	if rendered.Path != "" {
		status := "degraded"
		if result.Verdict == "changes_required" {
			status = "failed"
		}
		if err := s.persistVisualArtifactState(rendered.Path, rendered.SourceSHA256, status, rendered.Reference, visualIssueSummary(result)); err != nil {
			return tools.VisualInspectionResult{}, err
		}
	}
	return result, nil
}

func (s *Service) resolveVisualRender(request tools.VisualInspectionRequest) (tools.ArtifactRenderResult, error) {
	if strings.TrimSpace(request.RenderID) != "" {
		s.visualMu.Lock()
		state, ok := s.visualRenders[strings.TrimSpace(request.RenderID)]
		s.visualMu.Unlock()
		if ok {
			if request.Path != "" && artifactPathKey(state.Result.Path) != artifactPathKey(request.Path) {
				return tools.ArtifactRenderResult{}, errors.New("render_id belongs to a different artifact path")
			}
			return state.Result, nil
		}
	}
	if strings.TrimSpace(request.Path) == "" {
		return tools.ArtifactRenderResult{}, errors.New("render state is no longer available; call render_artifact again")
	}

	s.artifactMu.Lock()
	record, ok := s.latestArtifactRecordLocked(request.Path)
	s.artifactMu.Unlock()
	if !ok || strings.TrimSpace(record.RenderReference) == "" {
		return tools.ArtifactRenderResult{}, errors.New("artifact has no current render; call render_artifact first")
	}
	result := tools.ArtifactRenderResult{
		ID: visualRenderID(tools.ArtifactRenderResult{
			Source: tools.VisualSourceFile, Path: record.Path, Reference: record.RenderReference, SourceSHA256: record.SHA256,
		}),
		Source:       tools.VisualSourceFile,
		Path:         record.Path,
		Reference:    record.RenderReference,
		Renderer:     "restored-render",
		MIMEType:     "image/png",
		SourceSHA256: record.SHA256,
	}
	return result, nil
}

func (s *Service) visualInspectionRoutes() []chatRoute {
	settings := s.runtimeSettings.Normalized()
	primary, primaryErr := s.selectChatRoute()
	candidates := make([]chatRoute, 0, 12)
	seen := make(map[string]bool)
	add := func(route chatRoute) {
		if route.Provider.ID == "" || route.ModelID == "" || visualRouteCapability(route) == 0 {
			return
		}
		key := strings.ToLower(route.Provider.ID + "\x00" + route.ModelID)
		if seen[key] {
			return
		}
		seen[key] = true
		if model, ok := providerModelByID(route.Provider.Models, route.ModelID); ok {
			route.Model = model
		}
		candidates = append(candidates, route)
	}
	if primaryErr == nil {
		add(primary)
	}
	for _, provider := range settings.Model.Providers {
		if !provider.Enabled {
			continue
		}
		base, err := s.chatRouteForProvider(settings, provider)
		if err != nil {
			continue
		}
		for _, model := range provider.Models {
			route := base
			route.ModelID = model.ID
			route.Model = model
			add(route)
		}
		add(base)
	}
	primaryKey := ""
	if primaryErr == nil {
		primaryKey = strings.ToLower(primary.Provider.ID + "\x00" + primary.ModelID)
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		leftKey := strings.ToLower(candidates[left].Provider.ID + "\x00" + candidates[left].ModelID)
		rightKey := strings.ToLower(candidates[right].Provider.ID + "\x00" + candidates[right].ModelID)
		if leftKey == primaryKey || rightKey == primaryKey {
			return leftKey == primaryKey
		}
		return visualRouteCapability(candidates[left]) > visualRouteCapability(candidates[right])
	})
	if len(candidates) > 4 {
		candidates = candidates[:4]
	}
	return candidates
}

// visualRouteCapability returns 0 for known text-only routes, 1 for unknown
// custom aliases, and 2 for model families with image input support.
func visualRouteCapability(route chatRoute) int {
	id := strings.ToLower(strings.TrimSpace(route.ModelID))
	protocolName := strings.ToLower(strings.TrimSpace(route.Provider.Protocol))
	for _, marker := range []string{"embedding", "rerank", "whisper", "tts", "moderation"} {
		if strings.Contains(id, marker) {
			return 0
		}
	}
	if protocolName == "deepseek-official" || strings.Contains(id, "deepseek-chat") || strings.Contains(id, "deepseek-reasoner") {
		return 0
	}
	if protocolName == "anthropic" || protocolName == "anthropic-compatible" || protocolName == "gemini" {
		return 2
	}
	for _, marker := range []string{
		"vision", "gpt-4o", "gpt-4.1", "gpt-5", "o3", "o4", "claude-3", "claude-4", "claude-opus-5", "claude-fable-5", "gemini", "grok-4",
	} {
		if strings.Contains(id, marker) {
			return 2
		}
	}
	return 1
}

func visualInspectionChatRequest(rendered tools.ArtifactRenderResult, criteria string, attachment tools.Attachment) protocol.ChatRequest {
	criteria = strings.TrimSpace(criteria)
	if criteria == "" {
		criteria = "Check readability, clipping, overlap, spacing, alignment, blank or corrupted regions, inconsistent visual hierarchy, and any obvious rendering defect."
	}
	prompt := strings.Join([]string{
		"Inspect the attached rendered artifact as a visual QA reviewer.",
		"Return only one JSON object with this exact shape:",
		`{"verdict":"passed|changes_required","summary":"short factual summary","issues":[{"severity":"critical|major|minor","location":"visible location","description":"observable problem","suggestion":"concrete correction"}]}`,
		"Use passed only when no visible correction is required. Do not describe hidden reasoning.",
		"Artifact type: " + rendered.Renderer,
		"Acceptance criteria: " + criteria,
	}, "\n")
	return protocol.ChatRequest{
		Messages: []protocol.Message{
			{Role: "system", Content: "You are a visual quality inspector. Report only observable evidence in valid JSON."},
			{Role: "user", Content: prompt, Attachments: []protocol.Attachment{{Name: attachment.Name, MIMEType: attachment.MIMEType, Data: attachment.Data}}},
		},
		Temperature: 0.1,
		ToolChoice:  "none",
		Metadata:    map[string]string{"request_kind": "visual_inspection", "render_id": rendered.ID},
	}
}

func parseVisualInspectionPayload(content string) (visualInspectionPayload, error) {
	content = strings.TrimSpace(content)
	if strings.HasPrefix(content, "```") {
		if newline := strings.IndexByte(content, '\n'); newline >= 0 {
			content = content[newline+1:]
		}
		content = strings.TrimSpace(strings.TrimSuffix(content, "```"))
	}
	if start, end := strings.IndexByte(content, '{'), strings.LastIndexByte(content, '}'); start >= 0 && end > start {
		content = content[start : end+1]
	}
	var payload visualInspectionPayload
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return visualInspectionPayload{}, fmt.Errorf("visual model did not return valid structured JSON")
	}
	payload.Verdict = strings.ToLower(strings.TrimSpace(payload.Verdict))
	if payload.Verdict == "fail" || payload.Verdict == "failed" || payload.Verdict == "needs_changes" {
		payload.Verdict = "changes_required"
	}
	payload.Summary = strings.TrimSpace(payload.Summary)
	cleaned := make([]tools.VisualIssue, 0, len(payload.Issues))
	for _, issue := range payload.Issues {
		issue.Description = strings.TrimSpace(issue.Description)
		if issue.Description == "" {
			continue
		}
		issue.Severity = strings.ToLower(strings.TrimSpace(issue.Severity))
		if issue.Severity != "critical" && issue.Severity != "major" && issue.Severity != "minor" {
			issue.Severity = "major"
		}
		issue.Location = strings.TrimSpace(issue.Location)
		issue.Suggestion = strings.TrimSpace(issue.Suggestion)
		cleaned = append(cleaned, issue)
		if len(cleaned) == 20 {
			break
		}
	}
	payload.Issues = cleaned
	if len(payload.Issues) > 0 {
		payload.Verdict = "changes_required"
	}
	if payload.Verdict != "passed" && payload.Verdict != "changes_required" {
		return visualInspectionPayload{}, fmt.Errorf("visual model returned an unsupported verdict")
	}
	if payload.Summary == "" {
		if payload.Verdict == "passed" {
			payload.Summary = "No visible defects were reported."
		} else {
			payload.Summary = "Visible corrections are required."
		}
	}
	return payload, nil
}

func (s *Service) structuralVisualFallback(rendered tools.ArtifactRenderResult, reason string) tools.VisualInspectionResult {
	result := tools.VisualInspectionResult{
		RenderID: rendered.ID, Path: rendered.Path, Mode: "structural", Verdict: "degraded",
		Summary: reason, Issues: []tools.VisualIssue{}, CheckedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	if rendered.Path == "" {
		return result
	}
	record := s.inspectArtifact(rendered.Path, "available")
	if record.StructuralVerification == "failed" || record.Status != "available" {
		result.Verdict = "changes_required"
		result.Issues = append(result.Issues, tools.VisualIssue{
			Severity: "critical", Location: filepath.Base(rendered.Path), Description: record.FailureReason,
			Suggestion: "Repair the artifact structure before rendering it again.",
		})
		return result
	}
	if kind, _, supported := artifacts.Detect(rendered.Path); supported {
		preview, err := artifacts.PreviewFile(rendered.Path, artifacts.DefaultPreviewOptions())
		if err != nil {
			result.Verdict = "changes_required"
			result.Issues = append(result.Issues, tools.VisualIssue{Severity: "critical", Description: err.Error(), Suggestion: "Repair the Office package and rerun validation."})
			return result
		}
		switch kind {
		case artifacts.KindDocument:
			if preview.Document != nil {
				result.Summary += fmt.Sprintf(" Structural fallback read %d document blocks.", len(preview.Document.Blocks))
			}
		case artifacts.KindSpreadsheet:
			if preview.Spreadsheet != nil {
				result.Summary += fmt.Sprintf(" Structural fallback read %d worksheet(s).", len(preview.Spreadsheet.Sheets))
			}
		case artifacts.KindPresentation:
			if preview.Presentation != nil {
				result.Summary += fmt.Sprintf(" Structural fallback read %d slide(s).", len(preview.Presentation.Slides))
			}
		}
		return result
	}
	if strings.HasPrefix(record.MIMEType, "image/") {
		file, err := os.Open(rendered.Path)
		if err == nil {
			configuration, _, decodeErr := image.DecodeConfig(file)
			_ = file.Close()
			if decodeErr == nil {
				result.Summary += fmt.Sprintf(" Structural fallback confirmed image dimensions %dx%d.", configuration.Width, configuration.Height)
			}
		}
	}
	return result
}

func (s *Service) persistVisualArtifactState(path, expectedSHA, status, renderReference, failureReason string) error {
	s.artifactMu.Lock()
	defer s.artifactMu.Unlock()

	current := s.inspectArtifact(path, "available")
	if expectedSHA != "" && current.SHA256 != expectedSHA {
		return errors.New("artifact changed before visual verification state could be saved")
	}
	if previous, ok := s.latestArtifactRecordLocked(path); ok {
		preserveArtifactLineage(&current, previous)
	} else {
		current.MessageID, current.BranchID = s.currentArtifactOwners()
		current.ProjectID = strings.TrimSpace(s.projectID)
		current.SessionID = strings.TrimSpace(s.sessionID)
	}
	current.VisualVerification = status
	current.RenderReference = strings.TrimSpace(renderReference)
	current.FailureReason = strings.TrimSpace(failureReason)
	current.LastCheckedAt = time.Now().UTC().Format(time.RFC3339Nano)
	current.ID = artifactRecordID(current)
	if s.eventStore == nil {
		return nil
	}
	for _, event := range s.eventStore.Events() {
		if event.Type != eventlog.EventArtifactUpdate {
			continue
		}
		for _, record := range event.Payload.Artifacts {
			if record.ID == current.ID {
				return nil
			}
		}
	}
	_, err := s.appendEvent(eventlog.EventPayload{Artifacts: []ArtifactRecord{current}}, eventlog.EventArtifactUpdate, current.ToolCallID)
	return err
}

func (s *Service) latestArtifactRecordLocked(path string) (ArtifactRecord, bool) {
	key := artifactPathKey(path)
	records := s.sessionArtifactRecordsLocked()
	for index := len(records) - 1; index >= 0; index-- {
		if artifactPathKey(records[index].Path) == key {
			return records[index], true
		}
	}
	return ArtifactRecord{}, false
}

func preserveArtifactLineage(current *ArtifactRecord, previous ArtifactRecord) {
	if current == nil {
		return
	}
	current.Action = previous.Action
	current.Tool = previous.Tool
	current.ToolCallID = previous.ToolCallID
	current.MessageID = previous.MessageID
	current.ProjectID = previous.ProjectID
	current.SessionID = previous.SessionID
	current.BranchID = previous.BranchID
	current.CheckpointID = previous.CheckpointID
	if current.PreviewReference == "" {
		current.PreviewReference = previous.PreviewReference
	}
}

func (s *Service) pendingVisualArtifactsForCurrentTurn() []ArtifactRecord {
	if s.eventStore == nil {
		return nil
	}
	messageID, _ := s.currentArtifactOwners()
	if messageID == "" {
		return nil
	}
	records := s.sessionArtifactRecordsLocked()
	pending := make([]ArtifactRecord, 0)
	for _, record := range records {
		if record.MessageID != messageID || (record.Action != "created" && record.Action != "modified") {
			continue
		}
		if record.Tool == "download_file" || record.Tool == "browser" || record.Tool == "computer" {
			continue
		}
		if !isVisuallyInspectableArtifact(record.FileType, record.MIMEType) {
			continue
		}
		switch record.VisualVerification {
		case "passed", "degraded", "not_applicable":
			continue
		default:
			pending = append(pending, record)
		}
	}
	return pending
}

func (s *Service) degradedVisualArtifactsForCurrentTurn() []ArtifactRecord {
	if s.eventStore == nil {
		return nil
	}
	messageID, _ := s.currentArtifactOwners()
	if messageID == "" {
		return nil
	}
	records := s.sessionArtifactRecordsLocked()
	degraded := make([]ArtifactRecord, 0)
	for _, record := range records {
		if record.MessageID == messageID && record.VisualVerification == "degraded" {
			degraded = append(degraded, record)
		}
	}
	return degraded
}

func visualVerificationRecoveryMessage(records []ArtifactRecord) protocol.Message {
	paths := visualArtifactDisplayPaths(records, 8)
	content := strings.Join([]string{
		"[EchoCode private visual verification recovery]",
		"The current turn created or modified visual artifacts that have not completed visual QA:",
		strings.Join(paths, "\n"),
		"Before finishing, call render_artifact and then inspect_visual for each current artifact. If inspect_visual returns changes_required, fix the artifact, render the new SHA, and inspect it again. A degraded structural result is not visual approval. Do not repeat this recovery message or claim approval without a current-SHA passed result.",
		"[/EchoCode private visual verification recovery]",
	}, "\n")
	return protocol.Message{Role: "user", Content: content, InternalKind: visualVerificationRecoveryKind}
}

func (s *Service) appendVisualVerificationDisclosure(content string) string {
	pending := s.pendingVisualArtifactsForCurrentTurn()
	degraded := s.degradedVisualArtifactsForCurrentTurn()
	if len(pending) == 0 && len(degraded) == 0 {
		return strings.TrimSpace(content)
	}
	lines := make([]string, 0, 2)
	if len(pending) > 0 {
		lines = append(lines, "视觉验收未完成："+strings.Join(visualArtifactDisplayPaths(pending, 5), "、")+"。这些产物不能视为已经通过视觉检查。")
	}
	if len(degraded) > 0 {
		lines = append(lines, "视觉模型不可用，以下产物仅完成结构检查："+strings.Join(visualArtifactDisplayPaths(degraded, 5), "、")+"。这不是视觉通过结论。")
	}
	disclosure := "> " + strings.Join(lines, "\n> ")
	content = strings.TrimSpace(content)
	if content == "" {
		return disclosure
	}
	return content + "\n\n" + disclosure
}

func visualArtifactDisplayPaths(records []ArtifactRecord, limit int) []string {
	if limit <= 0 {
		limit = len(records)
	}
	paths := make([]string, 0, min(len(records), limit))
	for _, record := range records {
		path := strings.TrimSpace(record.DisplayPath)
		if path == "" {
			path = strings.TrimSpace(record.Path)
		}
		if path == "" {
			continue
		}
		paths = append(paths, path)
		if len(paths) == limit {
			break
		}
	}
	if len(records) > len(paths) && len(paths) == limit {
		paths = append(paths, fmt.Sprintf("另有 %d 个产物", len(records)-len(paths)))
	}
	return paths
}

func validateVisualRenderReference(path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return errors.New("renderer did not return an image reference")
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("rendered image is unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		return errors.New("renderer returned an empty or non-file image reference")
	}
	return nil
}

func visualRenderID(rendered tools.ArtifactRenderResult) string {
	identity := strings.Join([]string{rendered.Source, rendered.Path, rendered.Reference, rendered.SourceSHA256, time.Now().UTC().Format(time.RFC3339Nano)}, "\x00")
	digest := sha256.Sum256([]byte(identity))
	return "render-" + hex.EncodeToString(digest[:12])
}

func visualRequestDisplay(request tools.ArtifactRenderRequest) string {
	if request.Source == tools.VisualSourceFile {
		return request.Path
	}
	if request.Source == tools.VisualSourceWindow {
		return strings.TrimSpace(request.Source + " " + request.WindowID)
	}
	return request.Source
}

func clampVisualDimension(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func visualIssueSummary(result tools.VisualInspectionResult) string {
	parts := make([]string, 0, len(result.Issues)+1)
	if strings.TrimSpace(result.Summary) != "" {
		parts = append(parts, strings.TrimSpace(result.Summary))
	}
	for _, issue := range result.Issues {
		value := strings.TrimSpace(issue.Description)
		if issue.Location != "" {
			value = strings.TrimSpace(issue.Location) + ": " + value
		}
		if value != "" {
			parts = append(parts, value)
		}
		if len(parts) == 6 {
			break
		}
	}
	return clipContextText(strings.Join(parts, "; "), 1200)
}
