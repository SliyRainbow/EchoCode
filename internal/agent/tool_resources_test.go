package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestResourcesForToolUsesLocalTargetsAndHierarchicalPaths(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	root := t.TempDir()
	service.runtimeSettings.WorkspaceRoot = root

	child := filepath.Join(root, "src", "report.xlsx")
	checks := []struct {
		name       string
		arguments  map[string]any
		wantPrefix string
		wantPath   string
		precise    bool
	}{
		{name: "list_dir", arguments: map[string]any{"path": "src"}, wantPrefix: "dir:", wantPath: filepath.Join(root, "src"), precise: true},
		{name: "write_file", arguments: map[string]any{"path": "src/report.xlsx"}, wantPrefix: "file:", wantPath: child, precise: true},
		{name: "download_file", arguments: map[string]any{"destination_directory": "exports", "filename": "report.xlsx"}, wantPrefix: "file:", wantPath: filepath.Join(root, "exports", "report.xlsx"), precise: true},
		{name: "download_file", arguments: map[string]any{"destination_directory": "exports"}, wantPrefix: "dir:", wantPath: filepath.Join(root, "exports"), precise: true},
		{name: "git_repository", arguments: map[string]any{"action": "clone", "url": "https://github.com/example/project.git", "destination": "vendor/project"}, wantPrefix: "dir:", wantPath: filepath.Join(root, "vendor", "project"), precise: true},
		{name: "git_repository", arguments: map[string]any{"action": "pull", "repository": "vendor/project"}, wantPrefix: "dir:", wantPath: filepath.Join(root, "vendor", "project"), precise: true},
	}

	for _, check := range checks {
		t.Run(check.name+"/"+check.wantPrefix+check.wantPath, func(t *testing.T) {
			raw, err := json.Marshal(check.arguments)
			if err != nil {
				t.Fatal(err)
			}
			plan := service.resourcesForTool(check.name, nil, raw)
			if len(plan.Requests) != 1 {
				t.Fatalf("requests=%#v", plan.Requests)
			}
			want := check.wantPrefix + service.canonicalToolResourcePath(check.wantPath)
			if plan.Requests[0].Key != want || plan.Precise != check.precise {
				t.Fatalf("plan=%#v, want key=%q precise=%v", plan, want, check.precise)
			}
			if strings.Contains(plan.Requests[0].Key, "github.com") {
				t.Fatalf("remote URL leaked into local resource key: %q", plan.Requests[0].Key)
			}
		})
	}
}

func TestResourceCoordinatorDirectoryReadBlocksChildWrite(t *testing.T) {
	coordinator := NewResourceCoordinator()
	root := t.TempDir()
	directoryPath := filepath.Join(root, "src")
	childPath := filepath.Join(directoryPath, "report.xlsx")
	directory, err := coordinator.Acquire(context.Background(), ResourceRequest{Key: "dir:" + directoryPath, Mode: ResourceRead})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Millisecond)
	defer cancel()
	_, err = coordinator.Acquire(ctx, ResourceRequest{Key: "file:" + childPath, Mode: ResourceWrite})
	if err == nil {
		directory.Release()
		t.Fatal("child write acquired while directory read was active")
	}
	directory.Release()

	lease, err := coordinator.Acquire(context.Background(), ResourceRequest{Key: "file:" + childPath, Mode: ResourceWrite})
	if err != nil {
		t.Fatalf("child write remained blocked after directory release: %v", err)
	}
	lease.Release()
}

func TestDetachedRuntimesShareResourceCoordinator(t *testing.T) {
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
	if runtimeA.resourceCoordinatorForWorkspace() != runtimeB.resourceCoordinatorForWorkspace() {
		t.Fatal("detached runtimes for the same workspace received independent resource coordinators")
	}
}

type resourceProbeTool struct {
	calls   atomic.Int32
	started chan struct{}
	release <-chan struct{}
}

func (t *resourceProbeTool) Name() string { return "write_file" }

func (t *resourceProbeTool) Description() string { return "resource coordinator integration probe" }

func (t *resourceProbeTool) InputSchema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}
}

func (t *resourceProbeTool) Execute(ctx context.Context, _ json.RawMessage) (tools.Result, error) {
	t.calls.Add(1)
	select {
	case t.started <- struct{}{}:
	default:
	}
	select {
	case <-t.release:
		return tools.Result{Summary: "probe completed"}, nil
	case <-ctx.Done():
		return tools.Result{}, ctx.Err()
	}
}

func TestRunToolWithWatchdogDifferentFileWritesProceedInParallel(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.WorkspaceRoot = t.TempDir()
	service.runtimeSettings.ApprovalPolicy = "never"

	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	probeA := &resourceProbeTool{started: make(chan struct{}, 1), release: releaseA}
	probeB := &resourceProbeTool{started: make(chan struct{}, 1), release: releaseB}
	results := make(chan error, 2)
	go func() {
		_, err := service.runToolWithWatchdog(context.Background(), probeA, "write_file", mustJSONResourceArgs(t, "a.txt"), time.Second)
		results <- err
	}()
	go func() {
		_, err := service.runToolWithWatchdog(context.Background(), probeB, "write_file", mustJSONResourceArgs(t, "b.txt"), time.Second)
		results <- err
	}()

	waitResourceProbe(t, probeA.started)
	waitResourceProbe(t, probeB.started)
	close(releaseA)
	close(releaseB)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if probeA.calls.Load() != 1 || probeB.calls.Load() != 1 {
		t.Fatalf("probe calls = %d, %d", probeA.calls.Load(), probeB.calls.Load())
	}
}

func TestRunToolWithWatchdogSameFileWritesAreSerialized(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.WorkspaceRoot = t.TempDir()
	service.runtimeSettings.ApprovalPolicy = "never"

	release := make(chan struct{})
	probe := &resourceProbeTool{started: make(chan struct{}, 2), release: release}
	results := make(chan error, 2)
	args := mustJSONResourceArgs(t, "same.txt")
	go func() {
		_, err := service.runToolWithWatchdog(context.Background(), probe, "write_file", args, time.Second)
		results <- err
	}()
	waitResourceProbe(t, probe.started)
	go func() {
		_, err := service.runToolWithWatchdog(context.Background(), probe, "write_file", args, time.Second)
		results <- err
	}()

	select {
	case <-probe.started:
		t.Fatal("second same-file write started before the first released")
	case <-time.After(45 * time.Millisecond):
	}
	close(release)
	waitResourceProbe(t, probe.started)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if probe.calls.Load() != 2 {
		t.Fatalf("probe calls = %d, want 2", probe.calls.Load())
	}
}

func TestRunToolWithWatchdogCancelledWhileWaitingDoesNotExecute(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	defer service.Close()
	service.runtimeSettings.WorkspaceRoot = t.TempDir()
	service.runtimeSettings.ApprovalPolicy = "never"

	release := make(chan struct{})
	holder := &resourceProbeTool{started: make(chan struct{}, 1), release: release}
	waiting := &resourceProbeTool{started: make(chan struct{}, 1), release: make(chan struct{})}
	args := mustJSONResourceArgs(t, "blocked.txt")
	holderResult := make(chan error, 1)
	go func() {
		_, err := service.runToolWithWatchdog(context.Background(), holder, "write_file", args, time.Second)
		holderResult <- err
	}()
	waitResourceProbe(t, holder.started)

	ctx, cancel := context.WithCancel(context.Background())
	waitingResult := make(chan error, 1)
	go func() {
		_, err := service.runToolWithWatchdog(ctx, waiting, "write_file", args, time.Second)
		waitingResult <- err
	}()
	time.Sleep(25 * time.Millisecond)
	cancel()
	select {
	case <-waiting.started:
		t.Fatal("cancelled resource waiter executed the tool")
	case err := <-waitingResult:
		if err == nil {
			t.Fatal("cancelled resource waiter returned nil error")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled resource waiter did not return")
	}
	if waiting.calls.Load() != 0 {
		t.Fatalf("cancelled waiter calls = %d", waiting.calls.Load())
	}
	close(release)
	if err := <-holderResult; err != nil {
		t.Fatal(err)
	}
}

func mustJSONResourceArgs(t *testing.T, path string) json.RawMessage {
	t.Helper()
	arguments, err := json.Marshal(map[string]string{"path": path})
	if err != nil {
		t.Fatal(err)
	}
	return arguments
}

func waitResourceProbe(t *testing.T, started <-chan struct{}) {
	t.Helper()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("resource probe did not start")
	}
}
