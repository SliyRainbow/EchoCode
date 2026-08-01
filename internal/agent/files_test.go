package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
)

func TestResolveWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "page.html")
	if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = root
	svc.runtimeSettings.FilesystemAccess = "workspace-write"

	resolved, err := svc.ResolveWorkspaceFile("page.html")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != path {
		t.Fatalf("resolved = %q, want %q", resolved, path)
	}
	if _, err := svc.ResolveWorkspaceFile(filepath.Join(root, "missing.html")); err == nil {
		t.Fatal("不存在的文件不应允许打开")
	}
}

func TestOpenAndRevealWorkspaceFileUseHostCallbacks(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "page.html")
	if err := os.WriteFile(path, []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	opened, revealed := "", ""
	svc := NewService(ServiceConfig{
		SkillsDir: t.TempDir(),
		OpenFile: func(path string) error {
			opened = path
			return nil
		},
		RevealFile: func(path string) error {
			revealed = path
			return nil
		},
	})
	svc.runtimeSettings.WorkspaceRoot = root
	svc.runtimeSettings.FilesystemAccess = "workspace-write"
	if err := svc.OpenWorkspaceFile("page.html"); err != nil {
		t.Fatal(err)
	}
	if err := svc.RevealWorkspaceFile("page.html"); err != nil {
		t.Fatal(err)
	}
	if opened != path || revealed != path {
		t.Fatalf("opened=%q revealed=%q want=%q", opened, revealed, path)
	}
}

func TestReadWorkspaceFileReturnsSafeTextPreview(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "src", "sample.go")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("package sample\n\nconst Value = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = root
	svc.runtimeSettings.FilesystemAccess = "workspace-write"

	preview, err := svc.ReadWorkspaceFile("src/sample.go")
	if err != nil {
		t.Fatal(err)
	}
	if preview.Path != "src/sample.go" || preview.Name != "sample.go" {
		t.Fatalf("preview path = %q name = %q", preview.Path, preview.Name)
	}
	if preview.Content != "package sample\n\nconst Value = 1\n" || preview.LineCount != 3 {
		t.Fatalf("preview content = %q lines = %d", preview.Content, preview.LineCount)
	}
	if preview.Binary || preview.Truncated || preview.Encoding != "utf-8" {
		t.Fatalf("unexpected preview metadata: %+v", preview)
	}
}

func TestReadWorkspaceFileReturnsStructuredOfficePreviews(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name   string
		path   string
		kind   artifacts.Kind
		create func(string) error
	}{
		{
			name: "document", path: "report.docx", kind: artifacts.KindDocument,
			create: func(path string) error {
				return artifacts.CreateDocument(path, artifacts.DocumentSpec{Title: "Report"})
			},
		},
		{
			name: "spreadsheet", path: "report.xlsx", kind: artifacts.KindSpreadsheet,
			create: func(path string) error {
				return artifacts.WriteSpreadsheetRange(path, "Report", "A1", [][]any{{"Status", "Ready"}})
			},
		},
		{
			name: "presentation", path: "report.pptx", kind: artifacts.KindPresentation,
			create: func(path string) error {
				return artifacts.CreatePresentation(path, []artifacts.SlideSpec{{Title: "Review", Body: "Ready"}})
			},
		},
	}
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = root
	svc.runtimeSettings.FilesystemAccess = "read-only"
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.create(filepath.Join(root, test.path)); err != nil {
				t.Fatal(err)
			}
			preview, err := svc.ReadWorkspaceFile(test.path)
			if err != nil {
				t.Fatal(err)
			}
			if !preview.Binary || preview.Artifact == nil || preview.Artifact.Kind != test.kind {
				t.Fatalf("preview = %#v", preview)
			}
		})
	}
}

func TestReadWorkspaceFileReportsBinaryAndTruncatesLongText(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "binary.dat"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	longText := strings.Repeat("line\n", maxWorkspacePreviewLines+10)
	if err := os.WriteFile(filepath.Join(root, "long.txt"), []byte(longText), 0o644); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = root
	svc.runtimeSettings.FilesystemAccess = "read-only"

	binary, err := svc.ReadWorkspaceFile("binary.dat")
	if err != nil {
		t.Fatal(err)
	}
	if !binary.Binary || binary.Content != "" {
		t.Fatalf("binary preview = %+v", binary)
	}
	long, err := svc.ReadWorkspaceFile("long.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !long.Truncated || strings.Count(long.Content, "\n") != maxWorkspacePreviewLines {
		t.Fatalf("long preview was not truncated at %d lines: %+v", maxWorkspacePreviewLines, long)
	}
}

func TestListWorkspaceDirectoryIsBoundedToWorkspaceAndSorted(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "z.txt"), []byte("z"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main"), 0o600); err != nil {
		t.Fatal(err)
	}

	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	svc.runtimeSettings.WorkspaceRoot = root
	listing, err := svc.ListWorkspaceDirectory("")
	if err != nil {
		t.Fatal(err)
	}
	if len(listing.Entries) != 2 || !listing.Entries[0].IsDirectory || listing.Entries[0].Path != "src" || listing.Entries[1].Path != "z.txt" {
		t.Fatalf("root listing = %#v", listing)
	}
	nested, err := svc.ListWorkspaceDirectory("src")
	if err != nil {
		t.Fatal(err)
	}
	if len(nested.Entries) != 2 || !nested.Entries[0].IsDirectory || nested.Entries[1].Path != "src/main.go" {
		t.Fatalf("nested listing = %#v", nested)
	}
	if _, err := svc.ListWorkspaceDirectory("../"); err == nil {
		t.Fatal("expected parent traversal to be rejected")
	}
	if _, err := svc.ListWorkspaceDirectory("z.txt"); err == nil {
		t.Fatal("expected file path to be rejected as a directory")
	}
}
