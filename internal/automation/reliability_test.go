package automation

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func auditTask() Task {
	return Task{Name: "audit", Prompt: "inspect", ProjectID: "project", SessionID: "session", Schedule: Schedule{Kind: "interval", IntervalMinutes: 60}}
}

func TestFailedChangesPreserveAutomationState(t *testing.T) {
	for _, operation := range []string{"create", "edit", "delete", "enable", "start"} {
		t.Run(operation, func(t *testing.T) {
			service, err := New(filepath.Join(t.TempDir(), "tasks.json"))
			if err != nil {
				t.Fatal(err)
			}
			initial, err := service.Save(auditTask())
			if err != nil {
				t.Fatal(err)
			}
			blocked := filepath.Join(t.TempDir(), "blocked.json")
			if err := os.Mkdir(blocked, 0o700); err != nil {
				t.Fatal(err)
			}
			service.path = blocked
			called := false
			service.SetRunner(func(Task) (string, error) { called = true; return "chat", nil })
			task := initial.Tasks[0]
			switch operation {
			case "create":
				_, err = service.Save(auditTask())
			case "edit":
				task.Name = "changed"
				_, err = service.Save(task)
			case "delete":
				_, err = service.Delete(task.ID)
			case "enable":
				_, err = service.SetEnabled(task.ID, true)
			case "start":
				_, err = service.RunNow(task.ID)
			}
			if err == nil {
				t.Fatal("expected persistence failure")
			}
			after := service.State()
			if !reflect.DeepEqual(after.Tasks, initial.Tasks) || after.UpdatedAt != initial.UpdatedAt || after.Error == "" || called {
				t.Fatalf("failed operation changed state or launched a task: %#v", after)
			}
		})
	}
}

func TestStartingTaskCannotLaunchTwiceOrBeRemoved(t *testing.T) {
	service, _ := New("")
	state, err := service.Save(auditTask())
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	service.SetRunner(func(Task) (string, error) { entered <- struct{}{}; <-release; return "chat", nil })
	first := make(chan error, 1)
	go func() { _, err := service.RunNow(state.Tasks[0].ID); first <- err }()
	<-entered
	second := make(chan error, 1)
	go func() { _, err := service.RunNow(state.Tasks[0].ID); second <- err }()
	select {
	case err := <-second:
		if err == nil {
			close(release)
			t.Fatal("duplicate starting task was accepted")
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("second launch reached the runner")
	}
	if _, err := service.Delete(state.Tasks[0].ID); err == nil {
		t.Error("starting task was removed")
	}
	if _, err := service.Save(state.Tasks[0]); err == nil {
		t.Error("starting task was edited")
	}
	close(release)
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if service.State().Tasks[0].RunCount != 1 {
		t.Fatal("task was started more than once")
	}
}

func TestStoppedLoopCannotClearRestartedScheduler(t *testing.T) {
	service, _ := New("")
	service.Start(context.Background())
	service.Stop()
	service.Start(context.Background())
	defer service.Stop()
	time.Sleep(20 * time.Millisecond)
	if !service.State().Running {
		t.Fatal("old loop cleared the restarted scheduler")
	}
}
