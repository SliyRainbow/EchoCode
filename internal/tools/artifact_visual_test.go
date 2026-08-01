package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type visualControllerProbe struct {
	renderRequest  ArtifactRenderRequest
	inspectRequest VisualInspectionRequest
}

func (p *visualControllerProbe) RenderArtifact(_ context.Context, request ArtifactRenderRequest) (ArtifactRenderResult, error) {
	p.renderRequest = request
	return ArtifactRenderResult{ID: "render-probe", Source: request.Source, Path: request.Path, Reference: "render.png", Renderer: "probe", MIMEType: "image/png"}, nil
}

func (p *visualControllerProbe) InspectVisual(_ context.Context, request VisualInspectionRequest) (VisualInspectionResult, error) {
	p.inspectRequest = request
	return VisualInspectionResult{RenderID: request.RenderID, Path: request.Path, Mode: "vision", Verdict: "passed", Summary: "looks correct", Issues: []VisualIssue{}}, nil
}

func TestRenderArtifactToolResolvesWorkspaceFile(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "report.html")
	if err := os.WriteFile(path, []byte("<h1>report</h1>"), 0o600); err != nil {
		t.Fatal(err)
	}
	probe := &visualControllerProbe{}
	tool := RenderArtifactTool{
		Policy:     SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"},
		Controller: probe,
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"source":"file","path":"report.html","width":900,"height":700}`))
	if err != nil || result.IsError {
		t.Fatalf("render result=%#v err=%v", result, err)
	}
	if probe.renderRequest.Path != path || probe.renderRequest.Width != 900 || probe.renderRequest.Height != 700 {
		t.Fatalf("render request=%#v", probe.renderRequest)
	}
	if len(result.Parts) != 1 || !strings.Contains(result.Parts[0].Output, `"renderId":"render-probe"`) {
		t.Fatalf("render parts=%#v", result.Parts)
	}
}

func TestVisualToolsRejectInvalidInputsAndForwardInspection(t *testing.T) {
	root := t.TempDir()
	probe := &visualControllerProbe{}
	renderTool := RenderArtifactTool{Policy: SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}, Controller: probe}
	result, err := renderTool.Execute(context.Background(), json.RawMessage(`{"source":"window"}`))
	if err != nil || !result.IsError || !strings.Contains(result.Summary, "window_id") {
		t.Fatalf("invalid window result=%#v err=%v", result, err)
	}

	inspectTool := InspectVisualTool{Policy: renderTool.Policy, Controller: probe}
	result, err = inspectTool.Execute(context.Background(), json.RawMessage(`{"render_id":"render-probe","criteria":"no clipped text"}`))
	if err != nil || result.IsError {
		t.Fatalf("inspect result=%#v err=%v", result, err)
	}
	if probe.inspectRequest.RenderID != "render-probe" || probe.inspectRequest.Criteria != "no clipped text" {
		t.Fatalf("inspect request=%#v", probe.inspectRequest)
	}
	if !strings.Contains(result.Summary, "passed") {
		t.Fatalf("inspect summary=%q", result.Summary)
	}
}

func TestInspectVisualSchemaUsesObjectRootWithoutComposition(t *testing.T) {
	schema := (InspectVisualTool{}).InputSchema()
	if schema["type"] != "object" {
		t.Fatalf("schema root type = %#v, want object", schema["type"])
	}
	if _, exists := schema["anyOf"]; exists {
		t.Fatalf("schema root must not use anyOf: %#v", schema)
	}
	if _, exists := schema["oneOf"]; exists {
		t.Fatalf("schema root must not use oneOf: %#v", schema)
	}
}
