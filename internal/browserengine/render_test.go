package browserengine

import (
	"context"
	"image"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRenderURLScreenshotDesktopProbe(t *testing.T) {
	if os.Getenv("ECHOCODE_BROWSER_RENDER_PROBE") != "1" {
		t.Skip("set ECHOCODE_BROWSER_RENDER_PROBE=1 to exercise an installed Edge, Chrome, or Chromium")
	}
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = response.Write([]byte(`<!doctype html><html><style>html,body{margin:0;background:#fff}.probe{width:420px;height:260px;background:#1976d2;color:#fff;font:32px sans-serif;padding:40px}.accent{width:180px;height:90px;background:#e53935}</style><body><div class="probe">EchoCode render probe<div class="accent"></div></div></body></html>`))
	}))
	defer server.Close()

	service := New(filepath.Join(t.TempDir(), "profile"), filepath.Join(t.TempDir(), "downloads"))
	if err := service.Configure(Settings{Enabled: true, AllowNetwork: false, NativePresentation: false}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Stop(ctx)
	}()
	outputPath := filepath.Join(t.TempDir(), "render.png")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := service.RenderURLScreenshot(ctx, server.URL, outputPath, 800, 600); err != nil {
		t.Fatal(err)
	}
	if state := service.State(); len(state.Tabs) != 0 || state.ActiveTabID != "" {
		t.Fatalf("background render leaked into managed tabs: %#v", state)
	}
	file, err := os.Open(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	pixels, _, err := image.Decode(file)
	_ = file.Close()
	if err != nil {
		t.Fatal(err)
	}
	if pixels.Bounds().Dx() != 800 || pixels.Bounds().Dy() != 600 {
		t.Fatalf("screenshot dimensions=%dx%d", pixels.Bounds().Dx(), pixels.Bounds().Dy())
	}
	if !imageContainsColor(pixels, func(red, green, blue uint32) bool {
		return blue > 45_000 && green > 20_000 && red < 15_000
	}) || !imageContainsColor(pixels, func(red, green, blue uint32) bool {
		return red > 45_000 && green < 20_000 && blue < 20_000
	}) {
		t.Fatalf("screenshot did not contain the expected blue and red surfaces")
	}
}

func imageContainsColor(pixels image.Image, matches func(uint32, uint32, uint32) bool) bool {
	for y := pixels.Bounds().Min.Y; y < pixels.Bounds().Max.Y; y++ {
		for x := pixels.Bounds().Min.X; x < pixels.Bounds().Max.X; x++ {
			red, green, blue, _ := pixels.At(x, y).RGBA()
			if matches(red, green, blue) {
				return true
			}
		}
	}
	return false
}
