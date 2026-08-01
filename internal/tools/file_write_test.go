package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileProducesDiff(t *testing.T) {
	root := t.TempDir()
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}
	tool := WriteFileTool{Policy: policy}

	args, _ := json.Marshal(map[string]string{"path": "a.txt", "content": "hello\nworld\n"})
	res, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if res.IsError {
		t.Fatalf("不应报错: %s", res.Summary)
	}
	if len(res.Parts) != 2 || res.Parts[0].Kind != PartDiff || res.Parts[1].Kind != PartFile {
		t.Fatalf("应产出 diff 与文件 part，实际: %+v", res.Parts)
	}
	if res.Parts[0].Additions != 2 {
		t.Fatalf("应新增 2 行，实际: %d", res.Parts[0].Additions)
	}
	if !res.Parts[1].Created || res.Parts[1].Path != "a.txt" || res.Parts[1].LineCount != 2 {
		t.Fatalf("文件产物信息不符: %+v", res.Parts[1])
	}
	if res.Parts[1].FileAction != "created" {
		t.Fatalf("新建文件 action = %q", res.Parts[1].FileAction)
	}
	// 回读验证内容落盘。
	back, _ := ReadFileText(filepath.Join(root, "a.txt"))
	if back.Content != "hello\nworld\n" {
		t.Fatalf("落盘内容不符: %q", back.Content)
	}
}

func TestWriteFileReadOnlyRejected(t *testing.T) {
	root := t.TempDir()
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}
	tool := WriteFileTool{Policy: policy}
	args, _ := json.Marshal(map[string]string{"path": "a.txt", "content": "x"})
	res, _ := tool.Execute(context.Background(), args)
	if !res.IsError {
		t.Fatal("只读模式写入应报错")
	}
}

func TestApplyPatchUniqueness(t *testing.T) {
	root := t.TempDir()
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}
	// 先写入初始内容。
	if err := WriteFileTextAtomic(filepath.Join(root, "b.txt"), "foo\nbar\nfoo\n", FileText{LineEnding: LineEndingLF}); err != nil {
		t.Fatal(err)
	}
	tool := ApplyPatchTool{Policy: policy}

	// old_string 不唯一应被拒绝。
	args, _ := json.Marshal(map[string]string{"path": "b.txt", "old_string": "foo", "new_string": "baz"})
	res, _ := tool.Execute(context.Background(), args)
	if !res.IsError {
		t.Fatal("非唯一 old_string 应报错")
	}

	// 唯一替换应成功并产出 diff。
	args2, _ := json.Marshal(map[string]string{"path": "b.txt", "old_string": "bar", "new_string": "BAR"})
	res2, _ := tool.Execute(context.Background(), args2)
	if res2.IsError {
		t.Fatalf("唯一替换应成功: %s", res2.Summary)
	}
	if len(res2.Parts) != 2 || res2.Parts[0].Kind != PartDiff || res2.Parts[1].Kind != PartFile {
		t.Fatal("应产出 diff 与文件 part")
	}
	if !strings.Contains(res2.Parts[0].Patch, "+BAR") {
		t.Fatalf("diff 应含 +BAR: %s", res2.Parts[0].Patch)
	}
	if res2.Parts[1].Created {
		t.Fatal("修改已有文件不应标记为新建")
	}
	if res2.Parts[1].FileAction != "modified" {
		t.Fatalf("修改文件 action = %q", res2.Parts[1].FileAction)
	}
}

func TestApplyPatchSupportsAtomicBatchAndHash(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "batch.txt")
	if err := WriteFileTextAtomic(path, "alpha\nbeta\n", FileText{LineEnding: LineEndingLF, Encoding: EncodingUTF8}); err != nil {
		t.Fatal(err)
	}
	tool := ApplyPatchTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}}
	args, _ := json.Marshal(map[string]any{
		"path":            "batch.txt",
		"expected_sha256": FileTextSHA256("alpha\nbeta\n"),
		"edits": []map[string]any{
			{"old_string": "alpha", "new_string": "ALPHA"},
			{"old_string": "beta", "new_string": "BETA"},
		},
	})
	result, _ := tool.Execute(context.Background(), args)
	if result.IsError {
		t.Fatalf("batch patch failed: %s", result.Summary)
	}
	back, _ := ReadFileText(path)
	if back.Content != "ALPHA\nBETA\n" {
		t.Fatalf("batch content = %q", back.Content)
	}
}

func TestPendingWriteRejectsExternalModification(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "stale.txt")
	if err := WriteFileTextAtomic(path, "before\n", FileText{LineEnding: LineEndingLF, Encoding: EncodingUTF8}); err != nil {
		t.Fatal(err)
	}
	tool := ApplyPatchTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}}
	args, _ := json.Marshal(map[string]string{"path": "stale.txt", "old_string": "before", "new_string": "after"})
	_, pending, err := tool.Preview(context.Background(), args)
	if err != nil || pending == nil {
		t.Fatalf("preview failed: pending=%#v err=%v", pending, err)
	}
	if err := WriteFileTextAtomic(path, "external\n", FileText{LineEnding: LineEndingLF, Encoding: EncodingUTF8}); err != nil {
		t.Fatal(err)
	}
	if err := ApplyPending(pending); err == nil || !strings.Contains(err.Error(), "已被修改") {
		t.Fatalf("stale apply error = %v", err)
	}
}

func TestRegistrySchemas(t *testing.T) {
	policy := SandboxPolicy{WorkspaceRoot: t.TempDir()}
	reg := NewRegistry(ReadFileTool{policy}, WriteFileTool{policy})
	schemas := reg.Schemas()
	if len(schemas) != 2 {
		t.Fatalf("应有 2 个 schema，实际: %d", len(schemas))
	}
	if schemas[0].Function.Name != "read_file" || schemas[0].Type != "function" {
		t.Fatalf("schema 顺序或格式不符: %+v", schemas[0])
	}
}
