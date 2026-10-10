package eventlog

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func blockHeadWrites(t *testing.T, store *Store) func() {
	t.Helper()
	backup := filepath.Join(store.dir, "head-backup")
	if err := os.Rename(store.headFilePath(), backup); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(store.headFilePath(), 0o700); err != nil {
		t.Fatal(err)
	}
	return func() {
		if err := os.Remove(store.headFilePath()); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(backup, store.headFilePath()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAppendFailureKeepsCommittedHeadAndLog(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Append(EventPayload{Content: "committed"}, EventAssistantMessage)
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(store.eventsF)
	if err != nil {
		t.Fatal(err)
	}
	restore := blockHeadWrites(t, store)
	if _, err := store.Append(EventPayload{Content: "uncommitted"}, EventAssistantMessage); err == nil {
		t.Fatal("HEAD persistence failure was ignored")
	}
	after, err := os.ReadFile(store.eventsF)
	if err != nil {
		t.Fatal(err)
	}
	if store.Head() != first.ID || len(store.Events()) != 1 || !bytes.Equal(before, after) {
		t.Fatal("failed append changed committed history")
	}
	restore()
	next, err := store.Append(EventPayload{Content: "retry"}, EventAssistantMessage)
	if err != nil {
		t.Fatal(err)
	}
	if next.ParentID != first.ID || next.Seq != first.Seq+1 {
		t.Fatalf("retry is not based on the committed event: %#v", next)
	}
	reloaded, err := Open(store.dir)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Head() != next.ID || len(reloaded.Events()) != 2 {
		t.Fatal("history did not survive reopening")
	}
}

func TestSetHeadFailureDoesNotMoveMemoryHead(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Append(EventPayload{Content: "first"}, EventAssistantMessage)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Append(EventPayload{Content: "second"}, EventAssistantMessage)
	if err != nil {
		t.Fatal(err)
	}
	restore := blockHeadWrites(t, store)
	if err := store.SetHead(first.ID); err == nil {
		t.Fatal("expected a HEAD write failure")
	}
	if store.Head() != second.ID {
		t.Fatal("failed rewind moved the in-memory HEAD")
	}
	restore()
}

func TestOpenRejectsCorruptHeadAndEventGraph(t *testing.T) {
	for name, payload := range map[string]string{
		"self parent":        `{"id":"a","parentId":"a","seq":1}`,
		"missing parent":     `{"id":"a","parentId":"missing","seq":1}`,
		"duplicate identity": "{\"id\":\"a\",\"seq\":1}\n{\"id\":\"a\",\"seq\":2}",
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(payload+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Open(dir); err == nil {
				t.Fatal("accepted a corrupt event graph")
			}
		})
	}
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Append(EventPayload{}, EventUserMessage); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(store.headFilePath(), []byte("missing-event"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(store.dir); err == nil {
		t.Fatal("corrupt HEAD silently selected a different branch")
	}
}

func TestSnapshotRejectsPathsOutsideHashNamespace(t *testing.T) {
	store, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, hash := range []string{"../HEAD", "../../outside", "C:\\private.txt", "not-a-hash"} {
		if _, err := store.ReadSnapshot(hash); err == nil {
			t.Fatalf("accepted invalid snapshot hash %q", hash)
		}
	}
}
