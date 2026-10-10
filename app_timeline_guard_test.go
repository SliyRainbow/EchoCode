package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/agent"
)

func TestTimelineRejectsDetachedTasksInSameWorkspace(t *testing.T) {
	service := agent.NewService(agent.ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	projectID, sessionID := service.ActiveSessionIDs()
	task := &chatTask{id: "background", projectID: projectID, sessionID: "other-session"}
	app := &App{service: service}
	app.chat.tasks = map[string]*chatTask{task.id: task}
	for name, action := range map[string]func() error{
		"rewind": func() error { _, err := app.RewindToCheckpoint("unused"); return err },
		"branch": func() error { _, err := app.SwitchBranch("unused"); return err },
		"fork": func() error {
			_, err := app.ForkFromMessageForProjectSession(projectID, sessionID, "unused")
			return err
		},
	} {
		if err := action(); err == nil || !strings.Contains(err.Error(), "任务运行") {
			t.Fatalf("%s bypassed host guard: %v", name, err)
		}
	}
	task.projectID = "unrelated-project"
	if err := app.requireTimelineIdleChat(projectID, sessionID); err != nil {
		t.Fatalf("unrelated task blocked timeline: %v", err)
	}
	app.chat.tasks = nil
	app.chat.active = &chatTask{id: "legacy", projectID: projectID}
	if err := app.requireTimelineIdleChat(projectID, sessionID); err == nil {
		t.Fatal("legacy task bypassed guard")
	}
}

func TestWorkspaceOverlapIncludesNestedAndSharedDirectories(t *testing.T) {
	root := t.TempDir()
	for _, pair := range [][2]string{{root, root}, {root, filepath.Join(root, "child")}, {filepath.Join(root, "child"), root}} {
		if !workspacePathsOverlap(pair[0], pair[1]) {
			t.Fatalf("overlap missed: %v", pair)
		}
	}
	if workspacePathsOverlap(root, root+"-different") || workspacePathsOverlap(root, "") {
		t.Fatal("unrelated or empty roots overlap")
	}
}

func TestManualCompressionRejectsDetachedTaskForSameSession(t *testing.T) {
	service := agent.NewService(agent.ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: t.TempDir()})
	defer service.Close()
	p, id := service.ActiveSessionIDs()
	app := &App{service: service}
	app.chat.tasks = map[string]*chatTask{"running": {id: "running", projectID: p, sessionID: id}}
	if _, err := app.CompressSessionContext(p, id); err == nil || !strings.Contains(err.Error(), "任务正在运行") {
		t.Fatalf("detached task bypassed compression guard: %v", err)
	}
}
