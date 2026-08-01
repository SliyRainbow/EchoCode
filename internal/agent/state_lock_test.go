package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWorkbenchStateReturnsStableSnapshotDuringChat(t *testing.T) {
	requestStarted := make(chan struct{})
	releaseResponse := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		select {
		case <-requestStarted:
		default:
			close(requestStarted)
		}
		<-releaseResponse
		writeOpenAIReply(w, requestIsStream(body), "done", `{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}`)
	}))
	defer server.Close()

	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), DeepSeekBaseURL: server.URL})
	defer service.Close()
	if _, err := service.SaveDeepSeekAPIKey("sk-test"); err != nil {
		t.Fatal(err)
	}

	result := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	go func() {
		_, err := service.SendChatMessage(ctx, "wait")
		result <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("chat request did not start")
	}
	started := time.Now()
	state := service.WorkbenchState()
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("WorkbenchState blocked for %v during chat", elapsed)
	}
	if state.Reasoning.ID == "" || state.RuntimeSettings.WorkspaceRoot == "" {
		t.Fatalf("snapshot is incomplete: %#v", state)
	}
	started = time.Now()
	if _, err := service.SetReasoningLevel(ReasoningLow); err == nil {
		t.Fatal("expected settings mutation to be rejected while chat is active")
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("activity rejection blocked for %v", elapsed)
	}

	close(releaseResponse)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("chat did not finish after response release")
	}
}
