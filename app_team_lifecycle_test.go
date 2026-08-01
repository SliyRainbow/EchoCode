package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/agent"
)

type appTeamLifecycleServer struct {
	mu                  sync.Mutex
	calls               map[string]int
	reviewerStarted     chan struct{}
	reviewerStartedOnce sync.Once
}

func newAppTeamLifecycleServer() *appTeamLifecycleServer {
	return &appTeamLifecycleServer{
		calls:           make(map[string]int),
		reviewerStarted: make(chan struct{}),
	}
}

func (server *appTeamLifecycleServer) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
		http.Error(writer, err.Error(), http.StatusBadRequest)
		return
	}
	role := strings.TrimPrefix(strings.TrimSpace(payload.Model), "team-")
	server.mu.Lock()
	server.calls[role]++
	attempt := server.calls[role]
	server.mu.Unlock()

	// Pause the first review call until the app cancels it. A resume must only
	// repeat this unfinished role, not the completed planner/implementer/tester.
	if role == agent.TeamRoleReviewer && attempt == 1 {
		server.reviewerStartedOnce.Do(func() { close(server.reviewerStarted) })
		<-request.Context().Done()
		return
	}

	responses := map[string]string{
		agent.TeamRolePlanner:     "1. Inspect the workspace\n2. Implement the requested change\n3. Verify the result",
		agent.TeamRoleImplementer: "Implemented the requested change.",
		agent.TeamRoleTester:      "VERDICT: APPROVED\nFocused verification passed.",
		agent.TeamRoleReviewer:    "VERDICT: APPROVED\nReview passed.",
		agent.TeamRoleSynthesizer: "The team completed the requested work.",
	}
	response := responses[role]
	if response == "" {
		http.Error(writer, "unexpected team role "+role, http.StatusBadRequest)
		return
	}
	chunk, err := json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"delta":         map[string]any{"content": response},
			"finish_reason": "stop",
		}},
	})
	if err != nil {
		http.Error(writer, err.Error(), http.StatusInternalServerError)
		return
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(writer, "data: %s\n\ndata: [DONE]\n\n", chunk)
	if flusher, ok := writer.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (server *appTeamLifecycleServer) callCount(role string) int {
	server.mu.Lock()
	defer server.mu.Unlock()
	return server.calls[role]
}

func newAppTeamLifecycleFixture(t *testing.T) (*App, *agent.Service, *appTeamLifecycleServer, string, string) {
	t.Helper()
	base := t.TempDir()
	serverState := newAppTeamLifecycleServer()
	server := httptest.NewServer(serverState)
	t.Cleanup(server.Close)

	service := agent.NewService(agent.ServiceConfig{
		SkillsDir:              filepath.Join(base, "skills"),
		SettingsPath:           filepath.Join(base, "runtime-settings.json"),
		SessionsDir:            filepath.Join(base, "sessions"),
		ProjectsPath:           filepath.Join(base, "projects.json"),
		TemporaryWorkspaceRoot: filepath.Join(base, "temporary"),
	})
	t.Cleanup(service.Close)
	settings := service.WorkbenchState().RuntimeSettings
	settings.SandboxMode = "workspace-write"
	settings.FilesystemAccess = "workspace-write"
	settings.ApprovalPolicy = "never"
	settings.TaskIdleTimeoutSeconds = 30
	settings.Team = agent.TeamSettings{
		Enabled:         true,
		MaxReviewRounds: 1,
		Roles: []agent.TeamRoleSetting{
			{Role: agent.TeamRolePlanner, Enabled: true, ProviderID: "team-test", ModelID: "team-planner"},
			{Role: agent.TeamRoleImplementer, Enabled: true, ProviderID: "team-test", ModelID: "team-implementer"},
			{Role: agent.TeamRoleTester, Enabled: true, ProviderID: "team-test", ModelID: "team-tester"},
			{Role: agent.TeamRoleReviewer, Enabled: true, ProviderID: "team-test", ModelID: "team-reviewer"},
			{Role: agent.TeamRoleSynthesizer, Enabled: true, ProviderID: "team-test", ModelID: "team-synthesizer"},
		},
	}
	settings.Model = agent.ModelSettings{
		SelectedProviderID: "team-test",
		SelectedModelID:    "team-implementer",
		Providers: []agent.ModelProviderSetting{{
			ID: "team-test", Name: "Team test", Protocol: "local", APIType: "chat-completions",
			BaseURL: server.URL, Enabled: true, DefaultModelID: "team-implementer",
			Models: []agent.ProviderModel{
				{ID: "team-planner", ContextWindowTokens: 128_000},
				{ID: "team-implementer", ContextWindowTokens: 128_000},
				{ID: "team-tester", ContextWindowTokens: 128_000},
				{ID: "team-reviewer", ContextWindowTokens: 128_000},
				{ID: "team-synthesizer", ContextWindowTokens: 128_000},
			},
		}},
	}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	state, err := service.CreateProject("team lifecycle", filepath.Join(base, "workspace"))
	if err != nil {
		t.Fatal(err)
	}
	return &App{service: service}, service, serverState, state.ActiveProjectID, state.ActiveSessionID
}

func pauseAppTeamTask(t *testing.T, app *App, server *appTeamLifecycleServer, projectID, sessionID string) {
	t.Helper()
	taskID, err := app.StartChatMessageForProjectSession(projectID, sessionID, "complete this task with the AI team")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-server.reviewerStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("team reviewer did not start")
	}
	if !app.StopChatMessage(taskID) {
		t.Fatal("failed to stop the running team task")
	}
	waitForAppChatTaskExit(t, app, taskID, 5*time.Second)
}

func waitForAppChatTaskExit(t *testing.T, app *App, taskID string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		active := false
		for _, task := range app.GetActiveChatTasks() {
			if task.TaskID == taskID {
				active = true
				break
			}
		}
		if !active {
			return
		}
		time.Sleep(15 * time.Millisecond)
	}
	t.Fatalf("chat task %s did not reach a terminal state", taskID)
}

func TestAppResumesPausedTeamTaskOnlyThroughExplicitLifecycleAction(t *testing.T) {
	app, service, server, projectID, sessionID := newAppTeamLifecycleFixture(t)
	pauseAppTeamTask(t, app, server, projectID, sessionID)

	paused := service.WorkbenchState()
	if paused.Team.Status != "paused" || paused.Team.Active {
		t.Fatalf("paused state = %#v", paused.Team)
	}

	resumeTaskID, err := app.ResumePausedTeamTask(projectID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	waitForAppChatTaskExit(t, app, resumeTaskID, 5*time.Second)

	completed := service.WorkbenchState()
	if completed.Team.Status != "completed" || completed.Team.Active {
		t.Fatalf("completed state = %#v", completed.Team)
	}
	for _, role := range []string{agent.TeamRolePlanner, agent.TeamRoleImplementer, agent.TeamRoleTester} {
		if calls := server.callCount(role); calls != 1 {
			t.Fatalf("completed role %s was replayed %d times", role, calls)
		}
	}
	if calls := server.callCount(agent.TeamRoleReviewer); calls != 2 {
		t.Fatalf("unfinished reviewer calls = %d, want 2", calls)
	}
	if calls := server.callCount(agent.TeamRoleSynthesizer); calls != 1 {
		t.Fatalf("synthesizer calls = %d, want 1", calls)
	}
}

func TestAppAbandonsPausedTeamTaskWithoutDiscardingHistory(t *testing.T) {
	app, service, server, projectID, sessionID := newAppTeamLifecycleFixture(t)
	pauseAppTeamTask(t, app, server, projectID, sessionID)

	state, err := app.AbandonPausedTeamTask(projectID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.Team.Status != "cancelled" || state.Team.Active {
		t.Fatalf("abandoned state = %#v", state.Team)
	}
	history, err := service.GetSessionMessagesForProjectSession(projectID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) == 0 {
		t.Fatal("abandoning a paused team task discarded conversation history")
	}
}
