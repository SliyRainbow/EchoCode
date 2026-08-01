package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
	"github.com/SliyRainbow/EchoCode/internal/browserengine"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func TestArtifactRenderBridgeDesktopAcceptance(t *testing.T) {
	if os.Getenv("ECHOCODE_ARTIFACT_RENDER_ACCEPTANCE") != "1" {
		t.Skip("set ECHOCODE_ARTIFACT_RENDER_ACCEPTANCE=1 to exercise the installed browser renderer")
	}

	root := t.TempDir()
	service := browserengine.New(filepath.Join(root, "browser-profile"), filepath.Join(root, "downloads"))
	if err := service.Configure(browserengine.Settings{Enabled: true, AllowNetwork: false, NativePresentation: false}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Stop(ctx)
	})
	bridge := &artifactRenderBridge{app: &App{browser: service}}

	documentPath := filepath.Join(root, "acceptance.docx")
	if err := artifacts.CreateDocument(documentPath, artifacts.DocumentSpec{
		Title: "EchoCode artifact acceptance",
		Paragraphs: []artifacts.DocumentParagraph{
			{Text: "Rendered through the private background browser surface."},
			{Text: "Source bytes must remain unchanged.", Style: "heading1"},
		},
	}); err != nil {
		t.Fatal(err)
	}

	spreadsheetPath := filepath.Join(root, "acceptance.xlsx")
	if err := artifacts.WriteSpreadsheetRange(spreadsheetPath, "Report", "A1", [][]any{
		{"Item", "Status", "Count"},
		{"DOCX", "ready", 1},
		{"XLSX", "ready", 2},
	}); err != nil {
		t.Fatal(err)
	}

	presentationPath := filepath.Join(root, "acceptance.pptx")
	if err := artifacts.CreatePresentation(presentationPath, []artifacts.SlideSpec{
		{Title: "EchoCode acceptance", Body: "Private rendering\nNo visible browser tab"},
		{Title: "Verification", Body: "PNG output\nImmutable source"},
	}); err != nil {
		t.Fatal(err)
	}

	htmlPath := filepath.Join(root, "acceptance.html")
	html := `<!doctype html><html><head><meta charset="utf-8"><style>` +
		`html,body{margin:0;background:#f7f8fa;font-family:Arial,sans-serif}` +
		`main{margin:48px;padding:32px;border-left:12px solid #0f766e;background:#fff}` +
		`h1{color:#17324d}.signal{width:220px;height:96px;background:#d9485f}` +
		`</style></head><body><main><h1>EchoCode HTML acceptance</h1><div class="signal"></div></main></body></html>`
	if err := os.WriteFile(htmlPath, []byte(html), 0o600); err != nil {
		t.Fatal(err)
	}

	pdfPath := filepath.Join(root, "acceptance.pdf")
	if err := writeAcceptancePDF(pdfPath); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		path     string
		renderer string
		slide    int
	}{
		{name: "docx", path: documentPath, renderer: "echocode-office-structural-preview"},
		{name: "xlsx", path: spreadsheetPath, renderer: "echocode-office-structural-preview"},
		{name: "pptx", path: presentationPath, renderer: "echocode-office-structural-preview", slide: 2},
		{name: "html", path: htmlPath, renderer: "chromium-html"},
		{name: "pdf", path: pdfPath, renderer: "chromium-pdf"},
	}
	if existing := os.Getenv("ECHOCODE_ARTIFACT_ACCEPTANCE_EXISTING_XLSX"); existing != "" {
		tests = append(tests, struct {
			name     string
			path     string
			renderer string
			slide    int
		}{name: "existing-xlsx", path: existing, renderer: "echocode-office-structural-preview"})
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := fileSHA256(t, test.path)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			result, err := bridge.RenderArtifact(ctx, tools.ArtifactRenderRequest{
				Source: tools.VisualSourceFile,
				Path:   test.path,
				Width:  960,
				Height: 720,
				Slide:  test.slide,
			})
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Remove(result.Reference) })
			if result.Renderer != test.renderer {
				t.Fatalf("renderer=%q, want %q", result.Renderer, test.renderer)
			}
			if result.Width != 960 || result.Height != 720 {
				t.Fatalf("render dimensions=%dx%d", result.Width, result.Height)
			}
			assertRenderedImageIsNotBlank(t, result.Reference)
			if after := fileSHA256(t, test.path); after != before {
				t.Fatalf("source file changed during rendering: before=%x after=%x", before, after)
			}
			state := service.State()
			if len(state.Tabs) != 0 || state.ActiveTabID != "" {
				t.Fatalf("background render leaked into managed tabs: %#v", state)
			}
		})
	}
}

func fileSHA256(t *testing.T, path string) [sha256.Size]byte {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(content)
}

func assertRenderedImageIsNotBlank(t *testing.T, path string) {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	pixels, _, err := image.Decode(file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	first := pixels.At(pixels.Bounds().Min.X, pixels.Bounds().Min.Y)
	different := false
	for y := pixels.Bounds().Min.Y; y < pixels.Bounds().Max.Y && !different; y += 4 {
		for x := pixels.Bounds().Min.X; x < pixels.Bounds().Max.X; x += 4 {
			if pixels.At(x, y) != first {
				different = true
				break
			}
		}
	}
	if !different {
		t.Fatal("rendered image contains only one sampled color")
	}
}

func writeAcceptancePDF(path string) error {
	stream := "BT /F1 24 Tf 72 700 Td (EchoCode PDF acceptance) Tj ET"
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%s\nendstream", len(stream), stream),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>",
	}
	var output bytes.Buffer
	output.WriteString("%PDF-1.4\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = output.Len()
		fmt.Fprintf(&output, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := output.Len()
	fmt.Fprintf(&output, "xref\n0 %d\n", len(offsets))
	output.WriteString("0000000000 65535 f \n")
	for _, offset := range offsets[1:] {
		fmt.Fprintf(&output, "%010d 00000 n \n", offset)
	}
	fmt.Fprintf(&output, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(offsets), xrefOffset)
	return os.WriteFile(path, output.Bytes(), 0o600)
}
