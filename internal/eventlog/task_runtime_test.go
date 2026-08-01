package eventlog

import (
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestTaskRuntimeRoundTripAndMissingFile(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := store.ReadTaskRuntime(); err != nil || ok {
		t.Fatalf("missing runtime ok=%v err=%v", ok, err)
	}
	record := TaskRuntimeRecord{
		TaskID: "task-1", Generation: 17, StartedAt: "2026-07-27T01:02:03Z", Status: "waiting",
		Content: "partial", Parts: []MessagePart{{Kind: string(tools.PartToolCall), Name: "run_command", Status: "running"}},
	}
	if err := store.WriteTaskRuntime(record); err != nil {
		t.Fatal(err)
	}
	restored, ok, err := store.ReadTaskRuntime()
	if err != nil || !ok {
		t.Fatalf("restored runtime ok=%v err=%v", ok, err)
	}
	if restored.Version != 1 || restored.TaskID != "task-1" || restored.Generation != 17 || restored.Status != "waiting" || len(restored.Parts) != 1 {
		t.Fatalf("restored runtime = %#v", restored)
	}
	if restored.Terminal() {
		t.Fatal("waiting runtime was classified as terminal")
	}
	restored.Status = "interrupted"
	if !restored.Terminal() {
		t.Fatal("interrupted runtime was not classified as terminal")
	}
}
