package agent

import (
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type visualRendererProbe struct {
	directory string
	mu        sync.Mutex
	calls     int
}

func (r *visualRendererProbe) RenderArtifact(_ context.Context, request tools.ArtifactRenderRequest) (tools.ArtifactRenderResult, error) {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	path := filepath.Join(r.directory, "render-"+string(rune('0'+call))+".png")
	writeVisualTestPNG(path, color.NRGBA{R: 38, G: 120, B: 210, A: 255})
	return tools.ArtifactRenderResult{
		Source: request.Source, Path: request.Path, Reference: path,
		Renderer: "test-renderer", MIMEType: "image/png", Width: 12, Height: 8,
	}, nil
}

type visualStreamProvider struct {
	content string
	calls   atomic.Int32
}

func (p *visualStreamProvider) Name() string { return "visual-test" }

func (p *visualStreamProvider) ListModels(context.Context) ([]protocol.Model, error) {
	return []protocol.Model{{ID: "gpt-4o"}}, nil
}

func (p *visualStreamProvider) Stream(_ context.Context, request protocol.ChatRequest) (<-chan protocol.StreamEvent, error) {
	p.calls.Add(1)
	if len(request.Messages) == 0 || len(request.Messages[len(request.Messages)-1].Attachments) != 1 {
		panic("visual inspection request did not include exactly one image")
	}
	stream := make(chan protocol.StreamEvent, 2)
	stream <- protocol.StreamEvent{Type: "delta", Delta: p.content}
	stream <- protocol.StreamEvent{Type: "finish", FinishReason: "stop"}
	close(stream)
	return stream, nil
}

func TestVisualInspectionPersistsPassAndChangesRequired(t *testing.T) {
	tests := []struct {
		name          string
		payload       string
		wantVerdict   string
		wantPersisted string
	}{
		{name: "passed", payload: `{"verdict":"passed","summary":"layout is readable","issues":[]}`, wantVerdict: "passed", wantPersisted: "passed"},
		{name: "changes required", payload: `{"verdict":"changes_required","summary":"header is clipped","issues":[{"severity":"major","location":"header","description":"text is clipped","suggestion":"increase height"}]}`, wantVerdict: "changes_required", wantPersisted: "failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, path, provider := newVisualInspectionTestService(t, test.payload, "gpt-4o")
			defer service.Close()
			rendered, err := service.RenderArtifact(context.Background(), tools.ArtifactRenderRequest{Source: tools.VisualSourceFile, Path: path})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.InspectVisual(context.Background(), tools.VisualInspectionRequest{RenderID: rendered.ID, Criteria: "readable layout"})
			if err != nil || result.Verdict != test.wantVerdict || result.Mode != "vision" {
				t.Fatalf("inspection=%#v err=%v", result, err)
			}
			if provider.calls.Load() != 1 {
				t.Fatalf("provider calls=%d", provider.calls.Load())
			}
			records := service.ListSessionArtifacts()
			if len(records) != 1 || records[0].VisualVerification != test.wantPersisted || records[0].RenderReference != rendered.Reference {
				t.Fatalf("persisted records=%#v", records)
			}
		})
	}
}

func TestVisualInspectionMalformedJSONAndTextOnlyRouteDegradeExplicitly(t *testing.T) {
	for _, test := range []struct {
		name      string
		payload   string
		model     string
		wantCalls int32
	}{
		{name: "malformed JSON", payload: `not-json`, model: "gpt-4o", wantCalls: 1},
		{name: "known text-only route", payload: `unused`, model: "deepseek-chat", wantCalls: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, path, provider := newVisualInspectionTestService(t, test.payload, test.model)
			defer service.Close()
			rendered, err := service.RenderArtifact(context.Background(), tools.ArtifactRenderRequest{Source: tools.VisualSourceFile, Path: path})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.InspectVisual(context.Background(), tools.VisualInspectionRequest{RenderID: rendered.ID})
			if err != nil || result.Verdict != "degraded" || result.Mode != "structural" {
				t.Fatalf("inspection=%#v err=%v", result, err)
			}
			if provider.calls.Load() != test.wantCalls {
				t.Fatalf("provider calls=%d want=%d", provider.calls.Load(), test.wantCalls)
			}
			if records := service.ListSessionArtifacts(); len(records) != 1 || records[0].VisualVerification != "degraded" {
				t.Fatalf("degraded records=%#v", records)
			}
		})
	}
}

func TestVisualInspectionRejectsRenderAfterArtifactSHAChanges(t *testing.T) {
	service, path, provider := newVisualInspectionTestService(t, `{"verdict":"passed","summary":"ok","issues":[]}`, "gpt-4o")
	defer service.Close()
	rendered, err := service.RenderArtifact(context.Background(), tools.ArtifactRenderRequest{Source: tools.VisualSourceFile, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	writeVisualTestPNG(path, color.NRGBA{R: 220, G: 40, B: 50, A: 255})
	_, err = service.InspectVisual(context.Background(), tools.VisualInspectionRequest{RenderID: rendered.ID})
	if err == nil || !strings.Contains(err.Error(), "changed after rendering") {
		t.Fatalf("SHA invalidation err=%v", err)
	}
	if provider.calls.Load() != 0 {
		t.Fatalf("provider should not receive stale pixels, calls=%d", provider.calls.Load())
	}
}

func TestVisualRenderReferenceRestoresAcrossServiceRestart(t *testing.T) {
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "artifact.png")
	writeVisualTestPNG(path, color.NRGBA{R: 10, G: 80, B: 120, A: 255})
	renderer := &visualRendererProbe{directory: base}
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions"), ArtifactRenderer: renderer}
	service := NewService(config)
	configureVisualTestService(service, workspace, "gpt-4o", &visualStreamProvider{content: `{"verdict":"passed","summary":"ok","issues":[]}`})
	service.recordUserEvent("create image")
	_, err := service.recordToolArtifacts("write_file", "create-image", tools.Result{Parts: []tools.ResultPart{{Kind: tools.PartFile, Path: path, FileAction: "created", Created: true}}})
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := service.RenderArtifact(context.Background(), tools.ArtifactRenderRequest{Source: tools.VisualSourceFile, Path: path})
	if err != nil {
		t.Fatal(err)
	}
	service.Close()

	restarted := NewService(config)
	defer restarted.Close()
	configureVisualTestService(restarted, workspace, "gpt-4o", &visualStreamProvider{content: `{"verdict":"passed","summary":"ok","issues":[]}`})
	restored, err := restarted.resolveVisualRender(tools.VisualInspectionRequest{Path: path})
	if err != nil || restored.Reference != rendered.Reference || restored.SourceSHA256 != rendered.SourceSHA256 {
		t.Fatalf("restored render=%#v err=%v", restored, err)
	}
}

func TestToolLoopRequestsVisualVerificationOnceThenDisclosesIncompleteQA(t *testing.T) {
	service, path, _ := newVisualInspectionTestService(t, `{"verdict":"passed","summary":"ok","issues":[]}`, "gpt-4o")
	defer service.Close()
	calls := 0
	outcome, err := service.runToolLoopWithCompletion(context.Background(), service.buildToolRegistry(), protocol.ChatRequest{
		Messages: []protocol.Message{{Role: "user", Content: "create the image"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		calls++
		if calls == 2 {
			last := request.Messages[len(request.Messages)-1]
			if last.InternalKind != visualVerificationRecoveryKind || !strings.Contains(last.Content, filepath.Base(path)) {
				t.Fatalf("visual recovery message=%#v", last)
			}
		}
		return protocol.CompletionResult{Content: "artifact created"}, nil
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !strings.Contains(outcome.Content, "视觉验收未完成") {
		t.Fatalf("calls=%d outcome=%#v", calls, outcome)
	}
}

func TestToolLoopCompletesRenderInspectCycleWithoutIncompleteWarning(t *testing.T) {
	service, path, _ := newVisualInspectionTestService(t, `{"verdict":"passed","summary":"layout is readable","issues":[]}`, "gpt-4o")
	defer service.Close()
	calls := 0
	outcome, err := service.runToolLoopWithCompletion(context.Background(), service.buildToolRegistry(), protocol.ChatRequest{
		Messages: []protocol.Message{{Role: "user", Content: "create and verify the image"}},
	}, func(_ context.Context, request protocol.ChatRequest) (protocol.CompletionResult, error) {
		calls++
		switch calls {
		case 1:
			return protocol.CompletionResult{Content: "done"}, nil
		case 2:
			if request.Messages[len(request.Messages)-1].InternalKind != visualVerificationRecoveryKind {
				t.Fatalf("missing recovery request")
			}
			arguments, _ := json.Marshal(map[string]any{"source": "file", "path": path})
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{ID: "render", Type: "function", Function: protocol.ToolCallFunction{Name: "render_artifact", Arguments: arguments}}}}, nil
		case 3:
			arguments, _ := json.Marshal(map[string]any{"path": path})
			return protocol.CompletionResult{ToolCalls: []protocol.ToolCall{{ID: "inspect", Type: "function", Function: protocol.ToolCallFunction{Name: "inspect_visual", Arguments: arguments}}}}, nil
		default:
			return protocol.CompletionResult{Content: "visual verification passed"}, nil
		}
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 4 || strings.Contains(outcome.Content, "视觉验收未完成") || !strings.Contains(outcome.Content, "visual verification passed") {
		t.Fatalf("calls=%d outcome=%#v", calls, outcome)
	}
	if records := service.ListSessionArtifacts(); len(records) != 1 || records[0].VisualVerification != "passed" {
		t.Fatalf("verified records=%#v", records)
	}
}

func newVisualInspectionTestService(t *testing.T, payload, model string) (*Service, string, *visualStreamProvider) {
	t.Helper()
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	if err := os.MkdirAll(workspace, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(workspace, "artifact.png")
	writeVisualTestPNG(path, color.NRGBA{R: 30, G: 90, B: 160, A: 255})
	provider := &visualStreamProvider{content: payload}
	service := NewService(ServiceConfig{
		SkillsDir: t.TempDir(), SessionsDir: filepath.Join(base, "sessions"),
		ArtifactRenderer: &visualRendererProbe{directory: base},
	})
	configureVisualTestService(service, workspace, model, provider)
	service.recordUserEvent("create image")
	_, err := service.recordToolArtifacts("write_file", "create-image", tools.Result{
		Parts: []tools.ResultPart{{Kind: tools.PartFile, Path: path, FileAction: "created", Created: true}},
	})
	if err != nil {
		service.Close()
		t.Fatal(err)
	}
	return service, path, provider
}

func configureVisualTestService(service *Service, workspace, model string, provider protocol.Provider) {
	service.runtimeSettings.WorkspaceRoot = workspace
	service.runtimeSettings.FilesystemAccess = "workspace-write"
	service.runtimeSettings.ApprovalPolicy = "never"
	service.runtimeSettings.Model = ModelSettings{
		SelectedProviderID: "visual-local", SelectedModelID: model,
		Providers: []ModelProviderSetting{{
			ID: "visual-local", Name: "Visual Local", Protocol: "local", APIType: "chat-completions",
			BaseURL: "http://127.0.0.1:11434/v1", Enabled: true, DefaultModelID: model,
			Models: []ProviderModel{{ID: model, DisplayName: model, Provider: "visual-local", ContextWindowTokens: 128000}},
		}},
	}
	service.providerFactory = func(chatRoute) (protocol.Provider, error) { return provider, nil }
}

func writeVisualTestPNG(path string, fill color.NRGBA) {
	imageData := image.NewNRGBA(image.Rect(0, 0, 12, 8))
	for y := 0; y < imageData.Bounds().Dy(); y++ {
		for x := 0; x < imageData.Bounds().Dx(); x++ {
			imageData.SetNRGBA(x, y, fill)
		}
	}
	file, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	if err := png.Encode(file, imageData); err != nil {
		_ = file.Close()
		panic(err)
	}
	if err := file.Close(); err != nil {
		panic(err)
	}
}
