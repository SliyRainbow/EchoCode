package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/project"
)

type ProjectMemoryState struct {
	Enabled      bool   `json:"enabled"`
	ProjectID    string `json:"projectId,omitempty"`
	ProjectName  string `json:"projectName,omitempty"`
	SessionCount int    `json:"sessionCount"`
	TurnCount    int    `json:"turnCount"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
	SnapshotHash string `json:"snapshotHash,omitempty"`
	Summary      string `json:"summary"`
}

type projectSessionMemory struct {
	session project.Session
	turns   []projectMemoryTurn
	files   []string
}

type projectMemoryTurn struct {
	user      string
	assistant string
}

func (s *Service) refreshProjectMemory() {
	s.projectMemory = s.buildProjectMemory()
}

func (s *Service) projectMemorySummary() string {
	if strings.TrimSpace(s.projectMemory.Summary) != "" {
		return s.projectMemory.Summary
	}
	name := defaultProjectName(s.runtimeSettings.WorkspaceRoot)
	return "Project: " + name
}

func (s *Service) buildProjectMemory() ProjectMemoryState {
	settings := s.runtimeSettings.Normalized().Memory
	state := ProjectMemoryState{Enabled: settings.Enabled}
	projectName := defaultProjectName(s.runtimeSettings.WorkspaceRoot)
	if s.projects == nil || strings.TrimSpace(s.config.SessionsDir) == "" {
		state.ProjectName = projectName
		state.Summary = "Project: " + projectName
		state.SnapshotHash = memoryHash(state.Summary)
		return state
	}

	manifest := s.projects.Snapshot()
	activeProjectID := manifest.ActiveProjectID
	if strings.TrimSpace(s.projectID) != "" {
		activeProjectID = s.projectID
	}
	var active project.Project
	for _, candidate := range manifest.Projects {
		if candidate.ID == activeProjectID {
			active = candidate
			break
		}
	}
	if active.ID == "" {
		state.ProjectName = projectName
		state.Summary = "Project: " + projectName
		state.SnapshotHash = memoryHash(state.Summary)
		return state
	}
	state.ProjectID = active.ID
	state.ProjectName = active.Name
	if strings.TrimSpace(state.ProjectName) == "" {
		state.ProjectName = projectName
	}
	if !settings.Enabled {
		state.Summary = "Project: " + state.ProjectName
		state.SnapshotHash = memoryHash(state.Summary)
		return state
	}

	sessions := append([]project.Session(nil), active.Sessions...)
	sort.SliceStable(sessions, func(i, j int) bool { return sessions[i].UpdatedAt > sessions[j].UpdatedAt })
	memories := make([]projectSessionMemory, 0, settings.MaxSessions)
	for _, session := range sessions {
		if session.ID == s.sessionID || (!settings.IncludeArchived && session.Archived) {
			continue
		}
		store, err := eventlog.Open(filepath.Join(s.config.SessionsDir, active.ID, session.ID))
		if err != nil {
			continue
		}
		memory := summarizeProjectSession(session, store.Events())
		if len(memory.turns) == 0 {
			continue
		}
		memories = append(memories, memory)
		state.SessionCount++
		state.TurnCount += len(memory.turns)
		if state.UpdatedAt == "" || session.UpdatedAt > state.UpdatedAt {
			state.UpdatedAt = session.UpdatedAt
		}
		if len(memories) >= settings.MaxSessions {
			break
		}
	}

	state.Summary = formatProjectMemory(state.ProjectName, memories, settings.MaxCharacters)
	state.SnapshotHash = memoryHash(state.Summary)
	return state
}

func summarizeProjectSession(session project.Session, events []eventlog.Event) projectSessionMemory {
	memory := projectSessionMemory{session: session}
	fileSet := map[string]bool{}
	for _, event := range events {
		switch event.Type {
		case eventlog.EventUserMessage:
			memory.turns = append(memory.turns, projectMemoryTurn{user: event.Payload.Content})
		case eventlog.EventAssistantMessage:
			if len(memory.turns) == 0 {
				memory.turns = append(memory.turns, projectMemoryTurn{})
			}
			memory.turns[len(memory.turns)-1].assistant = event.Payload.Content
		case eventlog.EventFileSnapshot:
			path := filepath.ToSlash(strings.TrimSpace(event.Payload.Path))
			if path != "" && !fileSet[path] {
				fileSet[path] = true
				memory.files = append(memory.files, path)
			}
		}
	}
	return memory
}

func formatProjectMemory(projectName string, memories []projectSessionMemory, maxCharacters int) string {
	var builder strings.Builder
	builder.WriteString("Project: ")
	builder.WriteString(projectName)
	if len(memories) == 0 {
		return builder.String()
	}
	builder.WriteString("\nPrior session memory:")
	for _, memory := range memories {
		builder.WriteString("\n- Session: ")
		builder.WriteString(compactContextLine(memory.session.Title, 180))
		start := len(memory.turns) - 2
		if start < 0 {
			start = 0
		}
		for _, turn := range memory.turns[start:] {
			if text := compactContextLine(turn.user, 360); text != "" {
				builder.WriteString("\n  Request: ")
				builder.WriteString(text)
			}
			if text := compactContextLine(turn.assistant, 520); text != "" {
				builder.WriteString("\n  Outcome: ")
				builder.WriteString(text)
			}
		}
		if len(memory.files) > 0 {
			files := memory.files
			if len(files) > 8 {
				files = files[len(files)-8:]
			}
			builder.WriteString("\n  Files: ")
			builder.WriteString(strings.Join(files, ", "))
		}
		if builder.Len() >= maxCharacters {
			break
		}
	}
	return clipContextText(builder.String(), maxCharacters)
}

func memoryHash(summary string) string {
	sum := sha256.Sum256([]byte(summary))
	return fmt.Sprintf("sha256:%s", hex.EncodeToString(sum[:8]))
}
