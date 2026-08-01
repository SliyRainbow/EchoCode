package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
)

func TestReadFileSupportsRangesLineNumbersAndHash(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "lines.txt")
	content := "one\ntwo\nthree\nfour\n"
	if err := WriteFileTextAtomic(path, content, FileText{LineEnding: LineEndingLF, Encoding: EncodingUTF8}); err != nil {
		t.Fatal(err)
	}
	tool := ReadFileTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}}
	args, _ := json.Marshal(map[string]any{"path": "lines.txt", "start_line": 2, "end_line": 3, "include_line_numbers": true})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if !strings.Contains(result.Summary, FileTextSHA256(content)) || !strings.Contains(result.Parts[0].Output, "2 | two") || !strings.Contains(result.Parts[0].Output, "3 | three") {
		t.Fatalf("range result = %#v", result)
	}
}

func TestReadFileParsesOfficeArtifacts(t *testing.T) {
	root := t.TempDir()
	tests := []struct {
		name   string
		path   string
		marker string
		create func(string) error
	}{
		{
			name:   "document",
			path:   "report.docx",
			marker: "Document marker",
			create: func(path string) error {
				return artifacts.CreateDocument(path, artifacts.DocumentSpec{Title: "Document marker"})
			},
		},
		{
			name:   "spreadsheet",
			path:   "report.xlsx",
			marker: "Spreadsheet marker",
			create: func(path string) error {
				return artifacts.WriteSpreadsheetRange(path, "Report", "A1", [][]any{{"Spreadsheet marker", 42}})
			},
		},
		{
			name:   "presentation",
			path:   "report.pptx",
			marker: "Presentation marker",
			create: func(path string) error {
				return artifacts.CreatePresentation(path, []artifacts.SlideSpec{{Title: "Presentation marker", Body: "Slide body"}})
			},
		},
	}
	tool := ReadFileTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.create(filepath.Join(root, test.path)); err != nil {
				t.Fatal(err)
			}
			args, _ := json.Marshal(map[string]any{"path": test.path})
			result, err := tool.Execute(context.Background(), args)
			if err != nil || result.IsError {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if len(result.Parts) != 2 || result.Parts[0].Kind != PartToolCall || result.Parts[1].Kind != PartFile {
				t.Fatalf("parts=%#v", result.Parts)
			}
			if !strings.Contains(result.Parts[0].Output, test.marker) || result.Parts[1].Path != test.path {
				t.Fatalf("artifact result=%#v", result)
			}
		})
	}
}

func TestSearchSupportsRegexAndGlob(t *testing.T) {
	root := t.TempDir()
	if err := WriteFileTextAtomic(filepath.Join(root, "main.go"), "package main\n// TODO: fix\n", FileText{LineEnding: LineEndingLF, Encoding: EncodingUTF8}); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileTextAtomic(filepath.Join(root, "notes.txt"), "TODO: ignore\n", FileText{LineEnding: LineEndingLF, Encoding: EncodingUTF8}); err != nil {
		t.Fatal(err)
	}
	tool := SearchTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}}
	args, _ := json.Marshal(map[string]any{"query": `TODO:\s+fix`, "regex": true, "include_glob": "*.go"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil || result.IsError {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	if !strings.Contains(result.Parts[0].Output, "main.go:2:") || strings.Contains(result.Parts[0].Output, "notes.txt") {
		t.Fatalf("search output = %q", result.Parts[0].Output)
	}
}
