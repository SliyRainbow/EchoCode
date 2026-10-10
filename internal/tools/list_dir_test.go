package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestListDirPathsCanBeUsedDirectlyByReadFile(t *testing.T) {
	root, extra := t.TempDir(), t.TempDir()
	for _, path := range []string{
		filepath.Join(root, "check.txt"),
		filepath.Join(root, "f check.txt"),
		filepath.Join(root, "资料 子目录", "nested file.txt"),
		filepath.Join(extra, "external.txt"),
	} {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only", ExtraWritableRoots: []string{extra}}
	for _, test := range []struct {
		path  string
		files []string
	}{
		{".", []string{"check.txt", "f check.txt", "资料 子目录/nested file.txt"}},
		{"资料 子目录", []string{"资料 子目录/nested file.txt"}},
		{extra, []string{filepath.ToSlash(filepath.Join(extra, "external.txt"))}},
	} {
		t.Run(test.path, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"path": test.path, "max_depth": 2})
			result, err := (ListDirTool{Policy: policy}).Execute(context.Background(), args)
			if err != nil || result.IsError || len(result.Parts) != 1 {
				t.Fatalf("listing failed: %#v, %v", result, err)
			}
			var listing listDirOutput
			if err := json.Unmarshal([]byte(result.Parts[0].Output), &listing); err != nil {
				t.Fatal(err)
			}
			files := make(map[string]bool)
			for _, entry := range listing.Entries {
				if entry.Type != "file" {
					continue
				}
				files[entry.Path] = true
				readArgs, _ := json.Marshal(map[string]string{"path": entry.Path})
				read, readErr := (ReadFileTool{Policy: policy}).Execute(context.Background(), readArgs)
				if readErr != nil || read.IsError || read.Parts[0].Output != "fixture" {
					t.Fatalf("listing path %q cannot be read directly: %#v, %v", entry.Path, read, readErr)
				}
				if entry.SizeBytes == nil || *entry.SizeBytes != 7 {
					t.Fatalf("file size lost: %#v", entry)
				}
			}
			if listing.Truncated || len(files) != len(test.files) {
				t.Fatalf("unexpected listing: %#v", listing)
			}
			for _, path := range test.files {
				if !files[path] {
					t.Errorf("missing exact path %q", path)
				}
			}
		})
	}
}

func TestListDirEmptyAndTruncatedResults(t *testing.T) {
	root := t.TempDir()
	tool := ListDirTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}}
	list := func() listDirOutput {
		t.Helper()
		result, err := tool.Execute(context.Background(), json.RawMessage(`{"max_entries":1}`))
		if err != nil || result.IsError {
			t.Fatalf("listing failed: %#v, %v", result, err)
		}
		var output listDirOutput
		if err := json.Unmarshal([]byte(result.Parts[0].Output), &output); err != nil {
			t.Fatal(err)
		}
		return output
	}
	if output := list(); output.Entries == nil || len(output.Entries) != 0 || output.Truncated {
		t.Fatalf("empty directory was not an empty array: %#v", output)
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if output := list(); len(output.Entries) != 1 || output.Truncated || output.Entries[0].SizeBytes == nil || *output.Entries[0].SizeBytes != 0 {
		t.Fatalf("exact limit/empty file result: %#v", output)
	}
	if err := os.WriteFile(filepath.Join(root, "b.txt"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if output := list(); len(output.Entries) != 1 || !output.Truncated {
		t.Fatalf("missing truncation indicator: %#v", output)
	}
}
