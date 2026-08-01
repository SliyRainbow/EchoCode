package mcp

import "testing"

func TestServerSnapshotSortsToolsAndHashes(t *testing.T) {
	snapshot := NewServerSnapshot("filesystem", []ToolDescriptor{
		{Name: "write_file", InputSchemaHash: "sha256:b", OutputPolicy: "summary-first"},
		{Name: "read_file", InputSchemaHash: "sha256:a", OutputPolicy: "summary-first"},
	})
	if snapshot.Tools[0].Name != "read_file" {
		t.Fatalf("first tool = %s, want read_file", snapshot.Tools[0].Name)
	}
	if snapshot.ToolsHash == "" {
		t.Fatal("ToolsHash should be set")
	}
}
