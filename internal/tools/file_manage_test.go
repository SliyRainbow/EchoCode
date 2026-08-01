package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyAndDeleteFileProduceRewindableChanges(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.ps1")
	sourceMeta := FileText{LineEnding: LineEndingCRLF, Encoding: EncodingUTF8, HadBOM: true}
	if err := WriteFileTextAtomic(sourcePath, "Write-Output '你好'\n", sourceMeta); err != nil {
		t.Fatal(err)
	}
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}
	copyTool := CopyFileTool{Policy: policy}
	copyArgs, _ := json.Marshal(map[string]string{"source": "source.ps1", "destination": "nested/copied.ps1"})
	copyResult, err := copyTool.Execute(context.Background(), copyArgs)
	if err != nil || copyResult.IsError || len(copyResult.Changes) != 1 {
		t.Fatalf("copy result=%#v err=%v", copyResult, err)
	}
	copied, err := ReadFileText(filepath.Join(root, "nested", "copied.ps1"))
	if err != nil || copied.Content != "Write-Output '你好'\n" || copied.Encoding != EncodingUTF8 || !copied.HadBOM || copied.LineEnding != LineEndingCRLF {
		t.Fatalf("copied file=%#v err=%v", copied, err)
	}

	deleteTool := DeleteFileTool{Policy: policy}
	deleteArgs, _ := json.Marshal(map[string]string{"path": "nested/copied.ps1", "expected_sha256": FileTextSHA256(copied.Content)})
	deleteResult, err := deleteTool.Execute(context.Background(), deleteArgs)
	if err != nil || deleteResult.IsError || len(deleteResult.Changes) != 1 || !deleteResult.Changes[0].Deleted {
		t.Fatalf("delete result=%#v err=%v", deleteResult, err)
	}
	if _, err := os.Stat(filepath.Join(root, "nested", "copied.ps1")); !os.IsNotExist(err) {
		t.Fatalf("deleted file still exists: %v", err)
	}
}
