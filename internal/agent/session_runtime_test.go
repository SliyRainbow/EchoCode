package agent

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/storage"
)

type closeTrackingUsageStore struct {
	mu     sync.Mutex
	closed bool
}

func (s *closeTrackingUsageStore) Path() string { return "test-usage-store" }

func (s *closeTrackingUsageStore) AppendUsage(storage.UsageRecord) error { return nil }

func (s *closeTrackingUsageStore) RecentUsage(string, int) ([]storage.UsageRecord, error) {
	return nil, nil
}

func (s *closeTrackingUsageStore) Totals(string) (storage.UsageTotals, error) {
	return storage.UsageTotals{}, nil
}

func (s *closeTrackingUsageStore) Close() error {
	s.mu.Lock()
	s.closed = true
	s.mu.Unlock()
	return nil
}

func (s *closeTrackingUsageStore) isClosed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

func TestSessionRuntimesKeepConversationStateIsolated(t *testing.T) {
	base := t.TempDir()
	service := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	})
	_, sessionA := service.ActiveSessionIDs()
	if _, err := service.NewSession(); err != nil {
		t.Fatal(err)
	}
	projectID, sessionB := service.ActiveSessionIDs()

	runtimeA, err := service.NewSessionRuntime(sessionA)
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := service.NewSessionRuntime(sessionB)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	appendTurn := func(runtime *Service, user, assistant string) {
		defer wg.Done()
		runtime.recordUserEvent(user)
		runtime.sessionState.TurnCount = 1
		runtime.recordAssistantAndCheckpoint(assistant, "test-model", nil)
	}
	wg.Add(2)
	go appendTurn(runtimeA, "session-a-user", "session-a-assistant")
	go appendTurn(runtimeB, "session-b-user", "session-b-assistant")
	wg.Wait()

	assertSessionHistory(t, service.GetSessionMessagesForSession(sessionA), "session-a", "session-b")
	assertSessionHistory(t, service.GetSessionMessagesForSession(sessionB), "session-b", "session-a")
	if gotProject, gotSession := service.ActiveSessionIDs(); gotProject != projectID || gotSession != sessionB {
		t.Fatalf("background runtimes changed active pointers: project=%q session=%q", gotProject, gotSession)
	}
	if len(runtimeA.ListCheckpoints()) != 1 || len(runtimeB.ListCheckpoints()) != 1 {
		t.Fatalf("checkpoint isolation failed: a=%d b=%d", len(runtimeA.ListCheckpoints()), len(runtimeB.ListCheckpoints()))
	}

	manifest := service.projects.Snapshot()
	var titleA, titleB string
	for _, project := range manifest.Projects {
		for _, session := range project.Sessions {
			switch session.ID {
			case sessionA:
				titleA = session.Title
			case sessionB:
				titleB = session.Title
			}
		}
	}
	if !strings.Contains(titleA, "session-a") || !strings.Contains(titleB, "session-b") {
		t.Fatalf("background title updates crossed sessions: a=%q b=%q", titleA, titleB)
	}
}

func TestProjectSessionSwitchKeepsTwoNewConversationsDistinct(t *testing.T) {
	base := t.TempDir()
	service := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	})
	firstProjectID, firstSessionID := service.ActiveSessionIDs()
	firstRuntime, err := service.NewProjectSessionRuntime(firstProjectID, firstSessionID)
	if err != nil {
		t.Fatal(err)
	}
	firstRuntime.recordUserEvent("first-project-history")
	firstRuntime.recordAssistantAndCheckpoint("first-project-reply", "test-model", nil)

	if _, err := service.CreateProject("second", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	secondProjectID, secondSessionID := service.ActiveSessionIDs()
	if firstProjectID == secondProjectID || firstSessionID == secondSessionID {
		t.Fatalf("new project reused identity: %q/%q and %q/%q", firstProjectID, firstSessionID, secondProjectID, secondSessionID)
	}

	state, err := service.SwitchProjectSession(firstProjectID, firstSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveProjectID != firstProjectID || state.ActiveSessionID != firstSessionID {
		t.Fatalf("state identity = %q/%q", state.ActiveProjectID, state.ActiveSessionID)
	}
	history, err := service.GetSessionMessagesForProjectSession(firstProjectID, firstSessionID)
	if err != nil {
		t.Fatal(err)
	}
	assertSessionHistory(t, history, "first-project", "second-project")

	if _, err := service.GetSessionMessagesForProjectSession(secondProjectID, firstSessionID); err == nil {
		t.Fatal("mismatched project/session lookup should fail instead of returning empty history")
	}
	historyAfterFailure, err := service.GetSessionMessagesForProjectSession(firstProjectID, firstSessionID)
	if err != nil || len(historyAfterFailure) != len(history) {
		t.Fatalf("valid history changed after failed lookup: before=%d after=%d err=%v", len(history), len(historyAfterFailure), err)
	}
}

func TestActiveServiceReloadsDetachedRuntimeBeforeMessageFork(t *testing.T) {
	base := t.TempDir()
	service := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	})
	projectID, sessionID := service.ActiveSessionIDs()
	runtime, err := service.NewProjectSessionRuntime(projectID, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	runtime.recordUserEvent("detached user message")
	runtime.sessionState.TurnCount = 1
	runtime.recordAssistantAndCheckpoint("detached reply", "test-model", nil)

	if checkpoints := service.ListCheckpoints(); len(checkpoints) != 0 {
		t.Fatalf("main service unexpectedly observed detached head before reload: %#v", checkpoints)
	}
	state, reloaded, err := service.ReloadProjectSessionIfActive(projectID, sessionID)
	if err != nil || !reloaded {
		t.Fatalf("reload active=%v err=%v", reloaded, err)
	}
	if state.ActiveProjectID != projectID || state.ActiveSessionID != sessionID || len(service.ListCheckpoints()) != 1 {
		t.Fatalf("reloaded state=%#v checkpoints=%#v", state, service.ListCheckpoints())
	}
	history := service.GetSessionMessages()
	if len(history) != 2 || history[0].Content != "detached user message" {
		t.Fatalf("reloaded history=%#v", history)
	}
	if _, err := service.ForkFromMessageForProjectSession(projectID, sessionID, history[0].ID); err != nil {
		t.Fatal(err)
	}
	if forked := service.GetSessionMessages(); len(forked) != 0 {
		t.Fatalf("fork retained replaced detached messages: %#v", forked)
	}
}

func TestSessionRuntimesUseIndependentApprovalBrokers(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	runtimeA, err := service.NewSessionRuntime("session-a")
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := service.NewSessionRuntime("session-b")
	if err != nil {
		t.Fatal(err)
	}

	requestA := make(chan ApprovalRequest, 1)
	requestB := make(chan ApprovalRequest, 1)
	runtimeA.SetApprovalNotify(func(request ApprovalRequest) { requestA <- request })
	runtimeB.SetApprovalNotify(func(request ApprovalRequest) { requestB <- request })

	type approvalResult struct {
		decision ApprovalDecision
		err      error
	}
	resultA := make(chan approvalResult, 1)
	resultB := make(chan approvalResult, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		decision, requestErr := runtimeA.approvals.request(ctx, ApprovalRequest{Tool: "write_file", Summary: "A"})
		resultA <- approvalResult{decision: decision, err: requestErr}
	}()
	go func() {
		decision, requestErr := runtimeB.approvals.request(ctx, ApprovalRequest{Tool: "run_command", Summary: "B"})
		resultB <- approvalResult{decision: decision, err: requestErr}
	}()

	pendingA := <-requestA
	pendingB := <-requestB
	if pendingA.ID == pendingB.ID {
		t.Fatalf("approval request IDs must be unique: %q", pendingA.ID)
	}
	if err := runtimeA.RespondApproval(pendingA.ID, pendingA.Tool, true, "once"); err != nil {
		t.Fatal(err)
	}
	if err := runtimeB.RespondApproval(pendingB.ID, pendingB.Tool, false, "once"); err != nil {
		t.Fatal(err)
	}
	if result := <-resultA; result.err != nil || !result.decision.Approved {
		t.Fatalf("runtime A decision = %+v, err=%v", result.decision, result.err)
	}
	if result := <-resultB; result.err != nil || result.decision.Approved {
		t.Fatalf("runtime B decision = %+v, err=%v", result.decision, result.err)
	}
}

func TestSessionRuntimesShareProtocolCompatibilityState(t *testing.T) {
	tests := []struct {
		name   string
		config func(t *testing.T) ServiceConfig
	}{
		{
			name: "in memory",
			config: func(t *testing.T) ServiceConfig {
				return ServiceConfig{SkillsDir: t.TempDir()}
			},
		},
		{
			name: "persistent project session",
			config: func(t *testing.T) ServiceConfig {
				base := t.TempDir()
				return ServiceConfig{
					SkillsDir:    t.TempDir(),
					SessionsDir:  filepath.Join(base, "sessions"),
					ProjectsPath: filepath.Join(base, "projects.json"),
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := NewService(test.config(t))
			defer service.Close()

			projectID, sessionID := service.ActiveSessionIDs()
			first, err := service.NewProjectSessionRuntime(projectID, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if first.installationID == "" || first.installationID != service.installationID {
				t.Fatalf("runtime installation ID = %q, service = %q", first.installationID, service.installationID)
			}
			if first.anthropicCompatibilityCache != service.anthropicCompatibilityCache {
				t.Fatal("runtime must share the service Anthropic compatibility cache")
			}

			first.anthropicCompatibilityCache.Learn("relay", "claude-test", []string{"temperature"})
			second, err := service.NewProjectSessionRuntime(projectID, sessionID)
			if err != nil {
				t.Fatal(err)
			}
			if second.installationID != service.installationID {
				t.Fatalf("second runtime installation ID = %q, service = %q", second.installationID, service.installationID)
			}
			if got := strings.Join(second.anthropicCompatibilityCache.Parameters("relay", "claude-test"), ","); got != "temperature" {
				t.Fatalf("compatibility cache was not reused: %q", got)
			}
		})
	}
}

func TestSessionRuntimeFeedbackMergesIntoOwnerSettings(t *testing.T) {
	base := t.TempDir()
	settingsPath := filepath.Join(base, "runtime-settings.json")
	service := NewService(ServiceConfig{
		SkillsDir:    t.TempDir(),
		SettingsPath: settingsPath,
		SessionsDir:  filepath.Join(base, "sessions"),
		ProjectsPath: filepath.Join(base, "projects.json"),
	})
	defer service.Close()

	settings := service.WorkbenchState().RuntimeSettings
	settings.Model = ModelSettings{
		SelectedProviderID: "relay",
		SelectedModelID:    "claude-test",
		Providers: []ModelProviderSetting{{
			ID: "relay", Name: "Relay", Protocol: "anthropic-compatible", Enabled: true,
			Models: []ProviderModel{{ID: "claude-test", Provider: "relay"}},
		}},
	}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	projectID, sessionID := service.ActiveSessionIDs()
	runtime, err := service.NewProjectSessionRuntime(projectID, sessionID)
	if err != nil {
		t.Fatal(err)
	}

	latest := service.WorkbenchState().RuntimeSettings
	latest.TaskIdleTimeoutSeconds = 777
	if _, err := service.SaveRuntimeSettings(latest); err != nil {
		t.Fatal(err)
	}

	runtime.markChatProviderStatus("relay", "ok", "runtime connected")
	runtime.rememberAnthropicCompatibility(protocol.AnthropicCompatibilityFeedback{
		ProviderID: "relay", ModelID: "claude-test", UnsupportedParameters: []string{"temperature"},
	})

	assertSettings := func(label string, got RuntimeSettings) {
		t.Helper()
		if got.TaskIdleTimeoutSeconds != 777 {
			t.Fatalf("%s replaced newer settings: idle timeout = %d", label, got.TaskIdleTimeoutSeconds)
		}
		provider, _, ok := findModelProvider(got.Model.Providers, "relay")
		if !ok || provider.LastSyncStatus != "ok" || provider.LastSyncMessage != "runtime connected" {
			t.Fatalf("%s provider status = %#v", label, provider)
		}
		model, _, ok := findProviderModel(provider.Models, "claude-test")
		if !ok || strings.Join(model.UnsupportedParameters, ",") != "temperature" {
			t.Fatalf("%s model compatibility = %#v", label, model)
		}
	}
	assertSettings("owner", service.WorkbenchState().RuntimeSettings)
	persisted, ok := loadRuntimeSettings(settingsPath)
	if !ok {
		t.Fatal("runtime settings were not persisted")
	}
	assertSettings("persisted", persisted)
}

func TestSessionRuntimeCloseDoesNotCloseSharedResources(t *testing.T) {
	usageStore := &closeTrackingUsageStore{}
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), UsageStore: usageStore})
	runtime, err := service.NewSessionRuntime("background-session")
	if err != nil {
		t.Fatal(err)
	}

	runtime.Close()
	if usageStore.isClosed() {
		t.Fatal("closing a detached session runtime closed the shared usage store")
	}
	service.Close()
	if !usageStore.isClosed() {
		t.Fatal("closing the owner service did not close the shared usage store")
	}
}

func TestSessionRuntimesSendModelRequestsConcurrently(t *testing.T) {
	var requestsMu sync.Mutex
	requestCount := 0
	bothRequestsStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		requestsMu.Lock()
		requestCount++
		if requestCount == 2 {
			close(bothRequestsStarted)
		}
		requestsMu.Unlock()

		select {
		case <-bothRequestsStarted:
		case <-time.After(2 * time.Second):
			http.Error(w, "requests were serialized", http.StatusGatewayTimeout)
			return
		}

		reply := "reply-b"
		if bytes.Contains(body, []byte("prompt-a")) {
			reply = "reply-a"
		}
		writeOpenAIReply(w, requestIsStream(body), reply, "{\"prompt_tokens\":10,\"completion_tokens\":2,\"total_tokens\":12}")
	}))
	defer server.Close()

	base := t.TempDir()
	service := NewService(ServiceConfig{
		SkillsDir:       t.TempDir(),
		DeepSeekBaseURL: server.URL,
		SessionsDir:     filepath.Join(base, "sessions"),
		ProjectsPath:    filepath.Join(base, "projects.json"),
	})
	defer service.Close()
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}
	_, sessionA := service.ActiveSessionIDs()
	if _, err := service.NewSession(); err != nil {
		t.Fatal(err)
	}
	_, sessionB := service.ActiveSessionIDs()
	runtimeA, err := service.NewSessionRuntime(sessionA)
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := service.NewSessionRuntime(sessionB)
	if err != nil {
		t.Fatal(err)
	}

	type chatResponse struct {
		result ChatResult
		err    error
	}
	responseA := make(chan chatResponse, 1)
	responseB := make(chan chatResponse, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		result, sendErr := runtimeA.SendChatMessage(ctx, "prompt-a")
		responseA <- chatResponse{result: result, err: sendErr}
	}()
	go func() {
		result, sendErr := runtimeB.SendChatMessage(ctx, "prompt-b")
		responseB <- chatResponse{result: result, err: sendErr}
	}()

	resultA := <-responseA
	resultB := <-responseB
	if resultA.err != nil || resultA.result.Content != "reply-a" {
		t.Fatalf("session A result = %q, err=%v", resultA.result.Content, resultA.err)
	}
	if resultB.err != nil || resultB.result.Content != "reply-b" {
		t.Fatalf("session B result = %q, err=%v", resultB.result.Content, resultB.err)
	}
	assertSessionHistory(t, service.GetSessionMessagesForSession(sessionA), "prompt-a", "prompt-b")
	assertSessionHistory(t, service.GetSessionMessagesForSession(sessionB), "prompt-b", "prompt-a")
}

func TestSessionRuntimesShareWorkspaceMutationGate(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.WorkspaceRoot = t.TempDir()

	runtimeA, err := service.NewSessionRuntime("session-a")
	if err != nil {
		t.Fatal(err)
	}
	runtimeB, err := service.NewSessionRuntime("session-b")
	if err != nil {
		t.Fatal(err)
	}
	if runtimeA.toolMutationGateForWorkspace() != runtimeB.toolMutationGateForWorkspace() {
		t.Fatal("detached runtimes for the same workspace received independent mutation gates")
	}
}

func assertSessionHistory(t *testing.T, history []SessionMessage, included, excluded string) {
	t.Helper()
	if len(history) != 2 {
		t.Fatalf("history length = %d, want 2: %+v", len(history), history)
	}
	var content strings.Builder
	for _, message := range history {
		content.WriteString(message.Content)
		content.WriteByte('\n')
	}
	if !strings.Contains(content.String(), included) || strings.Contains(content.String(), excluded) {
		t.Fatalf("history isolation failed: %s", content.String())
	}
}
