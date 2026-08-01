package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	VisualSourceFile     = "file"
	VisualSourceBrowser  = "browser"
	VisualSourceWindow   = "window"
	VisualSourceEchoCode = "echocode"
)

type ArtifactRenderRequest struct {
	Source   string `json:"source"`
	Path     string `json:"path,omitempty"`
	WindowID string `json:"windowId,omitempty"`
	Sheet    string `json:"sheet,omitempty"`
	Page     int    `json:"page,omitempty"`
	Slide    int    `json:"slide,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
}

type ArtifactRenderResult struct {
	ID           string `json:"renderId"`
	Source       string `json:"source"`
	Path         string `json:"path,omitempty"`
	Reference    string `json:"renderReference"`
	Renderer     string `json:"renderer"`
	MIMEType     string `json:"mimeType"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	SourceSHA256 string `json:"sourceSha256,omitempty"`
}

type VisualInspectionRequest struct {
	RenderID string `json:"renderId,omitempty"`
	Path     string `json:"path,omitempty"`
	Criteria string `json:"criteria,omitempty"`
}

type VisualIssue struct {
	Severity    string `json:"severity"`
	Location    string `json:"location,omitempty"`
	Description string `json:"description"`
	Suggestion  string `json:"suggestion,omitempty"`
}

type VisualInspectionResult struct {
	RenderID  string        `json:"renderId,omitempty"`
	Path      string        `json:"path,omitempty"`
	Mode      string        `json:"mode"`
	Verdict   string        `json:"verdict"`
	Summary   string        `json:"summary"`
	Issues    []VisualIssue `json:"issues"`
	Provider  string        `json:"provider,omitempty"`
	Model     string        `json:"model,omitempty"`
	CheckedAt string        `json:"checkedAt,omitempty"`
}

// ArtifactRenderer turns a file or live surface into a bounded raster image.
// The returned path belongs to EchoCode's internal render cache, not the user's
// project, and must remain readable until the visual inspection finishes.
type ArtifactRenderer interface {
	RenderArtifact(context.Context, ArtifactRenderRequest) (ArtifactRenderResult, error)
}

// ArtifactVisualController owns render registration, model routing, and the
// durable verification state. It is implemented by the Agent service.
type ArtifactVisualController interface {
	RenderArtifact(context.Context, ArtifactRenderRequest) (ArtifactRenderResult, error)
	InspectVisual(context.Context, VisualInspectionRequest) (VisualInspectionResult, error)
}

type RenderArtifactTool struct {
	Policy     SandboxPolicy
	Controller ArtifactVisualController
}

func (t RenderArtifactTool) Name() string { return "render_artifact" }

func (t RenderArtifactTool) Description() string {
	return "Render a visual artifact or live surface into a bounded image for QA. Use source=file for images, HTML, PDF, DOCX, XLS/XLSX, and PPTX; source=browser for the active managed browser tab; source=window for an allowed desktop window; source=echocode for the EchoCode application window. Rendering is not visual approval: call inspect_visual with the returned renderId, fix reported issues, then render and inspect again."
}

func (t RenderArtifactTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source":    map[string]any{"type": "string", "enum": []string{VisualSourceFile, VisualSourceBrowser, VisualSourceWindow, VisualSourceEchoCode}, "description": "Visual source; defaults to file when path is present."},
			"path":      map[string]any{"type": "string", "description": "Workspace or explicitly authorized absolute file path for source=file."},
			"window_id": map[string]any{"type": "string", "description": "Allowed window ID for source=window."},
			"sheet":     map[string]any{"type": "string", "description": "Optional XLS/XLSX sheet name."},
			"page":      map[string]any{"type": "integer", "minimum": 1, "description": "Optional one-based document or PDF page."},
			"slide":     map[string]any{"type": "integer", "minimum": 1, "description": "Optional one-based PPTX slide."},
			"width":     map[string]any{"type": "integer", "minimum": 320, "maximum": 2560, "description": "Render viewport width."},
			"height":    map[string]any{"type": "integer", "minimum": 240, "maximum": 4096, "description": "Render viewport height."},
		},
	}
}

func (t RenderArtifactTool) ReadOnly() bool { return true }

func (t RenderArtifactTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	if t.Controller == nil {
		return visualToolError(t.Name(), "visual renderer is unavailable", ""), nil
	}
	var raw struct {
		Source   string `json:"source"`
		Path     string `json:"path"`
		WindowID string `json:"window_id"`
		Sheet    string `json:"sheet"`
		Page     int    `json:"page"`
		Slide    int    `json:"slide"`
		Width    int    `json:"width"`
		Height   int    `json:"height"`
	}
	if err := json.Unmarshal(rawArgs, &raw); err != nil {
		return visualToolError(t.Name(), "invalid render arguments: "+err.Error(), ""), nil
	}
	source := strings.ToLower(strings.TrimSpace(raw.Source))
	if source == "" && strings.TrimSpace(raw.Path) != "" {
		source = VisualSourceFile
	}
	if source == "" {
		source = VisualSourceBrowser
	}
	request := ArtifactRenderRequest{
		Source: source, WindowID: strings.TrimSpace(raw.WindowID), Sheet: strings.TrimSpace(raw.Sheet),
		Page: raw.Page, Slide: raw.Slide, Width: raw.Width, Height: raw.Height,
	}
	if source == VisualSourceFile {
		if strings.TrimSpace(raw.Path) == "" {
			return visualToolError(t.Name(), "source=file requires path", source), nil
		}
		resolved, err := t.Policy.ResolveReadPath(raw.Path)
		if err != nil {
			return visualToolError(t.Name(), err.Error(), raw.Path), nil
		}
		request.Path = resolved
	} else if source == VisualSourceWindow && request.WindowID == "" {
		return visualToolError(t.Name(), "source=window requires window_id", source), nil
	} else if source != VisualSourceBrowser && source != VisualSourceWindow && source != VisualSourceEchoCode {
		return visualToolError(t.Name(), "unsupported visual source: "+source, source), nil
	}

	rendered, err := t.Controller.RenderArtifact(ctx, request)
	if err != nil {
		return visualToolError(t.Name(), err.Error(), visualRenderInput(request)), nil
	}
	encoded, _ := json.Marshal(rendered)
	return Result{
		Summary: fmt.Sprintf("Rendered %s with %s; call inspect_visual using renderId %s.", visualRenderInput(request), rendered.Renderer, rendered.ID),
		Parts:   []ResultPart{{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: visualRenderInput(request), Output: string(encoded)}},
	}, nil
}

type InspectVisualTool struct {
	Policy     SandboxPolicy
	Controller ArtifactVisualController
}

func (t InspectVisualTool) Name() string { return "inspect_visual" }

func (t InspectVisualTool) Description() string {
	return "Inspect a render with an image-capable model and return a structured pass/fail issue list. Pass the renderId from render_artifact. A path may be supplied to recover the latest render for a file after a context restart. A changes_required verdict means the artifact must be fixed, rendered again, and reinspected. A degraded verdict is structural checking only and must never be described as visual approval."
}

func (t InspectVisualTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"render_id": map[string]any{"type": "string", "description": "renderId returned by render_artifact."},
			"path":      map[string]any{"type": "string", "description": "Optional file path used to recover its latest render."},
			"criteria":  map[string]any{"type": "string", "description": "Task-specific visible acceptance criteria."},
		},
	}
}

func (t InspectVisualTool) ReadOnly() bool { return true }

func (t InspectVisualTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	if t.Controller == nil {
		return visualToolError(t.Name(), "visual inspector is unavailable", ""), nil
	}
	var raw struct {
		RenderID string `json:"render_id"`
		Path     string `json:"path"`
		Criteria string `json:"criteria"`
	}
	if err := json.Unmarshal(rawArgs, &raw); err != nil {
		return visualToolError(t.Name(), "invalid visual inspection arguments: "+err.Error(), ""), nil
	}
	request := VisualInspectionRequest{RenderID: strings.TrimSpace(raw.RenderID), Criteria: strings.TrimSpace(raw.Criteria)}
	if strings.TrimSpace(raw.Path) != "" {
		resolved, err := t.Policy.ResolveReadPath(raw.Path)
		if err != nil {
			return visualToolError(t.Name(), err.Error(), raw.Path), nil
		}
		request.Path = resolved
	}
	if request.RenderID == "" && request.Path == "" {
		return visualToolError(t.Name(), "inspect_visual requires render_id or path", ""), nil
	}

	inspection, err := t.Controller.InspectVisual(ctx, request)
	if err != nil {
		return visualToolError(t.Name(), err.Error(), visualInspectionInput(request)), nil
	}
	encoded, _ := json.Marshal(inspection)
	summary := inspection.Summary
	if inspection.Verdict == "passed" {
		summary = "Visual inspection passed: " + summary
	} else if inspection.Verdict == "changes_required" {
		summary = fmt.Sprintf("Visual inspection found %d issue(s): %s", len(inspection.Issues), summary)
	} else if inspection.Verdict == "degraded" {
		summary = "Visual inspection degraded to structural checks: " + summary
	}
	return Result{
		Summary: summary,
		Parts:   []ResultPart{{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: visualInspectionInput(request), Output: string(encoded)}},
	}, nil
}

func visualRenderInput(request ArtifactRenderRequest) string {
	if request.Source == VisualSourceFile {
		return request.Path
	}
	if request.Source == VisualSourceWindow {
		return strings.TrimSpace(request.Source + " " + request.WindowID)
	}
	return request.Source
}

func visualInspectionInput(request VisualInspectionRequest) string {
	if request.RenderID != "" {
		return request.RenderID
	}
	return request.Path
}

func visualToolError(name, message, input string) Result {
	return Result{
		Summary: message,
		IsError: true,
		Parts:   []ResultPart{{Kind: PartToolCall, Name: name, Status: "error", Input: input, Output: message}},
	}
}
