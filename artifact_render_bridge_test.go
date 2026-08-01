package main

import (
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestRenderRasterImageProducesBoundedPNG(t *testing.T) {
	directory := t.TempDir()
	source := filepath.Join(directory, "source.png")
	target := filepath.Join(directory, "target.png")
	input := image.NewNRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			input.SetNRGBA(x, y, color.NRGBA{R: uint8(x % 255), G: uint8(y % 255), B: 90, A: 255})
		}
	}
	file, err := os.Create(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(file, input); err != nil {
		t.Fatal(err)
	}
	_ = file.Close()
	if err := renderRasterImage(source, target, 100, 100); err != nil {
		t.Fatal(err)
	}
	width, height, err := renderedImageDimensions(target)
	if err != nil || width != 100 || height != 50 {
		t.Fatalf("rendered dimensions=%dx%d err=%v", width, height, err)
	}
}

func TestVisualSurfaceRequiresUnguessablePrefix(t *testing.T) {
	surface, err := startVisualSurface(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("visible"))
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer surface.close()
	response, err := http.Get(surface.baseURL + "/surface")
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("authorized surface status=%d", response.StatusCode)
	}
	parsed, err := url.Parse(surface.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	unauthorized, err := http.Get(parsed.Scheme + "://" + parsed.Host + "/surface")
	if err != nil {
		t.Fatal(err)
	}
	_ = unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusNotFound {
		t.Fatalf("unguessable prefix bypass status=%d", unauthorized.StatusCode)
	}
}

func TestVisualRootFileRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("private"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "outside")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	request := httptest.NewRequest(http.MethodGet, "http://localhost/files/outside/secret.txt", nil)
	response := httptest.NewRecorder()
	serveVisualRootFile(response, request, root, "outside/secret.txt")
	if response.Code != http.StatusNotFound {
		t.Fatalf("symlink escape status=%d body=%q", response.Code, response.Body.String())
	}
}
