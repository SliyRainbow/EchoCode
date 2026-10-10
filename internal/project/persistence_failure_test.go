package project

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestFailedManifestSaveDoesNotChangeSelectedProject(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), "projects.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsureBootstrap(t.TempDir(), "first"); err != nil {
		t.Fatal(err)
	}
	before := store.Snapshot()
	blocked := filepath.Join(t.TempDir(), "blocked.json")
	if err := os.Mkdir(blocked, 0o700); err != nil {
		t.Fatal(err)
	}
	store.path = blocked
	err = store.RenameProject(before.ActiveProjectID, "uncommitted name")
	if err == nil {
		t.Fatal("expected persistence failure")
	}
	if !reflect.DeepEqual(before, store.Snapshot()) {
		t.Fatal("failed persistence changed the project manifest")
	}
}
