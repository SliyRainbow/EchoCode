package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	localArtifactContextStart = "[EchoCode local artifact context]"
	localArtifactContextEnd   = "[/EchoCode local artifact context]"
	contextArtifactKind       = "artifact-context"
	maxLocalArtifactContext   = 24
)

type localArtifactReference struct {
	Path                   string `json:"path,omitempty"`
	Action                 string `json:"action"`
	FileType               string `json:"fileType,omitempty"`
	MIMEType               string `json:"mimeType,omitempty"`
	Size                   int64  `json:"size,omitempty"`
	SHA256                 string `json:"sha256,omitempty"`
	Status                 string `json:"status,omitempty"`
	StructuralVerification string `json:"structuralVerification,omitempty"`
	VisualVerification     string `json:"visualVerification,omitempty"`
	ToolCallID             string `json:"toolCallId,omitempty"`
	PreviewReference       string `json:"previewReference,omitempty"`
}

func (s *Service) protocolAssistantMessage(content string, parts []tools.ResultPart) protocol.Message {
	return protocol.Message{Role: "assistant", Content: s.protocolAssistantContent(content, parts)}
}

func (s *Service) protocolAssistantContent(content string, parts []tools.ResultPart) string {
	content = strings.TrimSpace(content)
	contexts := make([]string, 0, 2)
	if !strings.Contains(content, localArtifactContextStart) {
		if artifactContext := formatLocalArtifactContext(s.localArtifactReferences(parts), 0); artifactContext != "" {
			contexts = append(contexts, artifactContext)
		}
	}
	if !strings.Contains(content, executionContextStart) {
		if executionContext := s.formatExecutionCheckpoint(parts); executionContext != "" {
			contexts = append(contexts, executionContext)
		}
	}
	if len(contexts) == 0 {
		return content
	}
	if content != "" {
		contexts = append([]string{content}, contexts...)
	}
	return strings.Join(contexts, "\n\n")
}

func formatLocalArtifactContext(references []localArtifactReference, maxRunes int) string {
	if len(references) == 0 {
		return ""
	}
	if len(references) > maxLocalArtifactContext {
		references = references[len(references)-maxLocalArtifactContext:]
	}
	start := 0
	if maxRunes > 0 {
		start = len(references) - 1
		for index := len(references) - 2; index >= 0; index-- {
			candidate := renderLocalArtifactContext(references[index:])
			if len([]rune(candidate)) > maxRunes {
				break
			}
			start = index
		}
	}
	return renderLocalArtifactContext(references[start:])
}

func renderLocalArtifactContext(references []localArtifactReference) string {
	var context strings.Builder
	context.WriteString(localArtifactContextStart)
	context.WriteString("\nBranch-local tool-confirmed files for later turns. Reuse these exact absolute paths instead of searching the workspace. Structural and visual verification are separate states:\n")
	for _, reference := range references {
		context.WriteString("- absolute_path: ")
		context.WriteString(reference.Path)
		context.WriteByte('\n')
		metadata := reference
		metadata.Path = ""
		encoded, err := json.Marshal(metadata)
		if err != nil {
			continue
		}
		context.WriteString("  metadata: ")
		context.WriteString(string(encoded))
		context.WriteByte('\n')
	}
	context.WriteString(localArtifactContextEnd)
	return context.String()
}

func recentLocalArtifactReferences(contents ...string) []localArtifactReference {
	all := make([]localArtifactReference, 0, maxLocalArtifactContext)
	for _, content := range contents {
		all = append(all, parseLocalArtifactReferences(content)...)
	}
	if len(all) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(all))
	reversed := make([]localArtifactReference, 0, maxLocalArtifactContext)
	for index := len(all) - 1; index >= 0 && len(reversed) < maxLocalArtifactContext; index-- {
		key := strings.ToLower(filepath.Clean(all[index].Path))
		if seen[key] {
			continue
		}
		seen[key] = true
		reversed = append(reversed, all[index])
	}
	result := make([]localArtifactReference, len(reversed))
	for index := range reversed {
		result[len(reversed)-1-index] = reversed[index]
	}
	return result
}

func parseLocalArtifactReferences(content string) []localArtifactReference {
	var references []localArtifactReference
	remaining := content
	for {
		start := strings.Index(remaining, localArtifactContextStart)
		if start < 0 {
			break
		}
		remaining = remaining[start+len(localArtifactContextStart):]
		end := strings.Index(remaining, localArtifactContextEnd)
		if end < 0 {
			break
		}
		block := remaining[:end]
		remaining = remaining[end+len(localArtifactContextEnd):]
		lines := strings.Split(block, "\n")
		for index := 0; index < len(lines); index++ {
			line := lines[index]
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "- absolute_path: ") {
				path := filepath.Clean(strings.TrimSpace(strings.TrimPrefix(line, "- absolute_path: ")))
				reference := localArtifactReference{Path: path, Action: "available"}
				if index+1 < len(lines) {
					metadata := strings.TrimSpace(lines[index+1])
					if strings.HasPrefix(metadata, "metadata: ") {
						_ = json.Unmarshal([]byte(strings.TrimPrefix(metadata, "metadata: ")), &reference)
						reference.Path = path
						index++
					}
				}
				reference.Action = normalizeArtifactAction(reference.Action)
				references = append(references, reference)
				continue
			}
			if strings.HasPrefix(line, "{") {
				var reference localArtifactReference
				if json.Unmarshal([]byte(line), &reference) == nil && strings.TrimSpace(reference.Path) != "" {
					reference.Path = filepath.Clean(strings.TrimSpace(reference.Path))
					reference.Action = normalizeArtifactAction(reference.Action)
					references = append(references, reference)
				}
				continue
			}
			// Legacy path-only contexts remain readable after upgrading.
			line = strings.TrimSpace(strings.TrimPrefix(line, "- "))
			action, path, ok := strings.Cut(line, ": ")
			if !ok || strings.TrimSpace(action) == "" || strings.TrimSpace(path) == "" {
				continue
			}
			references = append(references, localArtifactReference{
				Action: strings.TrimSpace(action),
				Path:   filepath.Clean(strings.TrimSpace(path)),
			})
		}
	}
	return references
}

func stripLocalArtifactContext(content string) string {
	for {
		start := strings.Index(content, localArtifactContextStart)
		if start < 0 {
			return strings.TrimSpace(content)
		}
		relativeEnd := strings.Index(content[start+len(localArtifactContextStart):], localArtifactContextEnd)
		if relativeEnd < 0 {
			return strings.TrimSpace(content[:start])
		}
		end := start + len(localArtifactContextStart) + relativeEnd + len(localArtifactContextEnd)
		content = strings.TrimSpace(content[:start]) + "\n" + strings.TrimSpace(content[end:])
	}
}

func (s *Service) localArtifactReferences(parts []tools.ResultPart) []localArtifactReference {
	if len(parts) == 0 {
		return nil
	}
	workspaceRoot := strings.TrimSpace(s.runtimeSettings.WorkspaceRoot)
	references := make([]localArtifactReference, 0, 4)
	seen := make(map[string]bool)
	registered := make(map[string]localArtifactReference)
	for _, reference := range artifactReferencesFromRecords(s.sessionArtifactRecordsLocked()) {
		registered[artifactPathKey(reference.Path)] = reference
	}
	for _, part := range parts {
		if part.Kind != tools.PartFile || strings.TrimSpace(part.Path) == "" {
			continue
		}
		path := strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(part.Path))
		path = filepath.FromSlash(path)
		if !filepath.IsAbs(path) && workspaceRoot != "" {
			path = filepath.Join(workspaceRoot, path)
		}
		if absolute, err := filepath.Abs(path); err == nil {
			path = absolute
		}
		path = filepath.Clean(path)
		key := strings.ToLower(path)
		if seen[key] {
			continue
		}
		seen[key] = true
		action := strings.ToLower(strings.TrimSpace(part.FileAction))
		if action == "" {
			if part.Created {
				action = "created"
			} else {
				action = "available"
			}
		}
		if reference, ok := registered[artifactPathKey(path)]; ok {
			if action != "available" || reference.Action == "" {
				reference.Action = action
			}
			references = append(references, reference)
		} else {
			references = append(references, localArtifactReference{Path: path, Action: action, Status: "available"})
		}
		if len(references) >= maxLocalArtifactContext {
			break
		}
	}
	return references
}
