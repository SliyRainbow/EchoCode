package tools

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestBinarySnapshotRestoresExactBytesAndRejectsCorruption(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "payload.bin")
	original := []byte{0x00, 0xff, '\r', '\n', '\n', 0x80, 0x42}
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := ReadFileSnapshot(path)
	if err != nil {
		t.Fatal(err)
	}
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}
	if err := os.WriteFile(path, []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RestoreFile(policy, path, snapshot.Content, true, "", string(snapshot.Encoding), false); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, original) {
		t.Fatal("binary bytes were not restored exactly")
	}
	if err := RestoreFile(policy, path, "invalid%%base64", true, "", string(EncodingBinary), false); err == nil {
		t.Fatal("corrupt snapshot was accepted")
	}
	if current, err := os.ReadFile(path); err != nil || !bytes.Equal(current, original) {
		t.Fatal("corrupt snapshot modified the existing file")
	}
}
