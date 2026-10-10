package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

type persistenceTestProvider struct{ beforeReply func() }

func (p persistenceTestProvider) Name() string { return "persistence-test" }
func (p persistenceTestProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return nil, nil
}
func (p persistenceTestProvider) Stream(context.Context, protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	p.beforeReply()
	events := make(chan protocol.StreamEvent, 3)
	events <- protocol.StreamEvent{Type: "reasoning", Delta: "visible provider reasoning"}
	events <- protocol.StreamEvent{Type: "delta", Delta: "completed answer"}
	events <- protocol.StreamEvent{Type: "done"}
	close(events)
	return events, nil
}

func TestHistoryFailureRetainsAnswerAndIsNotSuccessful(t *testing.T) {
	sessions := t.TempDir()
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: sessions})
	defer service.Close()
	service.runtimeSettings.Model = ModelSettings{SelectedProviderID: "local", SelectedModelID: "test", Providers: []ModelProviderSetting{{ID: "local", Name: "Local", Protocol: "local", Enabled: true, DefaultModelID: "test", Models: []ProviderModel{{ID: "test"}}}}}
	service.providerFactory = func(chatRoute) (protocol.Provider, error) {
		return persistenceTestProvider{beforeReply: func() {
			head := filepath.Join(sessions, "default", "default", "HEAD")
			if err := os.Rename(head, head+".saved"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(head, 0o700); err != nil {
				t.Fatal(err)
			}
		}}, nil
	}
	result, err := service.SendChatMessage(context.Background(), "test history persistence")
	if err == nil || result.HistoryError == "" || !result.TurnCommitted {
		t.Fatalf("persistence failure was hidden: %v, %#v", err, result)
	}
	if result.Content != "completed answer" || result.Reasoning != "visible provider reasoning" {
		t.Fatalf("output was discarded: %#v", result)
	}
	if len(service.ListCheckpoints()) != 0 {
		t.Fatal("failed assistant write produced a checkpoint")
	}
	if len(service.GetSessionMessages()) != 1 {
		t.Fatal("failed assistant write duplicated terminal history")
	}
}

func TestInvalidStorageIsVisibleAndCannotBeOverwritten(t *testing.T) {
	for _, kind := range []string{"settings", "projects", "session"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(root, "sessions")}
			path := filepath.Join(root, "settings.json")
			switch kind {
			case "settings":
				config.SettingsPath = path
			case "projects":
				path = filepath.Join(root, "projects.json")
				config.ProjectsPath = path
			case "session":
				path = filepath.Join(config.SessionsDir, "default", "default", "HEAD")
			}
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("corrupt-storage"), 0o600); err != nil {
				t.Fatal(err)
			}
			service := NewService(config)
			defer service.Close()
			if service.WorkbenchState().StorageError == "" {
				t.Fatal("storage failure was hidden")
			}
			if _, err := service.SendChatMessage(context.Background(), "must not run"); err == nil {
				t.Fatal("chat continued with unavailable storage")
			}
			if _, err := service.NewProjectSessionRuntime("default", "default"); err == nil {
				t.Fatal("detached runtime bypassed storage failure")
			}
			if data, _ := os.ReadFile(path); !strings.Contains(string(data), "corrupt-storage") {
				t.Fatal("original corrupt storage was overwritten")
			}
		})
	}
}

func TestManagedProjectPathsRemainConsistentAcrossRuntimes(t *testing.T) {
	root := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(root, "sessions"), ProjectsPath: filepath.Join(root, "projects.json"), SettingsPath: filepath.Join(root, "settings.json")}
	service := NewService(config)
	defer service.Close()
	settings := service.WorkbenchState().RuntimeSettings
	settings.WorkspaceRoot = t.TempDir()
	if _, err := service.SaveRuntimeSettings(settings); err == nil {
		t.Fatal("managed root changed without switching project")
	}
	settings = service.WorkbenchState().RuntimeSettings
	extra := t.TempDir()
	settings.ExtraWritableRoots = []string{extra}
	if _, err := service.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	pid, sid := service.ActiveSessionIDs()
	runtime, err := service.NewProjectSessionRuntime(pid, sid)
	if err != nil {
		t.Fatal(err)
	}
	if got := runtime.WorkbenchState().RuntimeSettings.ExtraWritableRoots; len(got) != 1 || got[0] != extra {
		t.Fatalf("detached runtime lost project roots: %v", got)
	}
	restored := NewService(config)
	defer restored.Close()
	if got := restored.WorkbenchState().RuntimeSettings.ExtraWritableRoots; len(got) != 1 || got[0] != extra {
		t.Fatalf("restart lost project roots: %v", got)
	}
}
