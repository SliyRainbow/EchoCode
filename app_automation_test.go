package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/automation"
)

func TestUnavailableAutomationNeverMasqueradesAsEmpty(t *testing.T) {
	app := &App{automationLoadError: errors.New("invalid saved JSON")}
	if state := app.GetAutomationState(); state.Tasks == nil || !strings.Contains(state.Error, "原文件已保留") {
		t.Fatalf("load failure hidden: %#v", state)
	}
	if _, err := app.SaveAutomationTask(automation.Task{}); err == nil {
		t.Fatal("unavailable store accepted a mutation")
	}
}

func TestAutomationStopStillCancelsWhenPersistenceFails(t *testing.T) {
	path := filepath.Join(t.TempDir(), "tasks.json")
	service, err := automation.New(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := service.Save(automation.Task{Name: "audit", Prompt: "inspect", ProjectID: "project", SessionID: "session", Schedule: automation.Schedule{Kind: "interval", IntervalMinutes: 60}})
	if err != nil {
		t.Fatal(err)
	}
	service.SetRunner(func(automation.Task) (string, error) { return "chat", nil })
	if _, err := service.RunNow(state.Tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+".saved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	task := &chatTask{id: "chat", projectID: "project", sessionID: "session", cancel: cancel, done: make(chan struct{})}
	defer task.markDone()
	app := &App{automations: service}
	app.chat.tasks = map[string]*chatTask{task.id: task}
	if _, err := app.StopAutomationTask(state.Tasks[0].ID); err == nil {
		t.Fatal("persistence failure hidden")
	}
	if ctx.Err() != context.Canceled {
		t.Fatal("storage error prevented task cancellation")
	}
}
