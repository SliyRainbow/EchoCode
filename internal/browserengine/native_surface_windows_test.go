//go:build windows

package browserengine

import (
	"math"
	"testing"
)

func TestScaleEmbeddedSurfaceBoundsUsesWindowDPI(t *testing.T) {
	rect, scale, err := scaleEmbeddedSurfaceBounds(nativeRect{Right: 1920, Bottom: 1080}, NativeSurfaceBounds{
		X: 300, Y: 100, Width: 600, Height: 400,
		ViewportWidth: 1280, ViewportHeight: 720,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rect.Left != 450 || rect.Top != 150 || rect.Right != 1350 || rect.Bottom != 750 {
		t.Fatalf("scaled bounds = %+v", rect)
	}
	if math.Abs(scale-1.5) > 0.001 {
		t.Fatalf("rasterization scale = %v, want 1.5", scale)
	}
}

func TestScaleEmbeddedSurfaceBoundsClampsToClientArea(t *testing.T) {
	rect, _, err := scaleEmbeddedSurfaceBounds(nativeRect{Right: 1000, Bottom: 700}, NativeSurfaceBounds{
		X: -20, Y: 650, Width: 1200, Height: 200,
		ViewportWidth: 1000, ViewportHeight: 700,
	})
	if err != nil {
		t.Fatal(err)
	}
	if rect.Left != 0 || rect.Top != 650 || rect.Right != 1000 || rect.Bottom != 700 {
		t.Fatalf("clamped bounds = %+v", rect)
	}
}

func TestEmbeddedBrowserIdentity(t *testing.T) {
	if got := browserEngineName(embeddedBrowserExecutable); got != "WebView2 embedded" {
		t.Fatalf("engine name = %q", got)
	}
	marker := embeddedTabMarkerURL("tab-123")
	if marker != "about:blank#echocode-embedded-tab-tab-123" {
		t.Fatalf("marker URL = %q", marker)
	}
}

func TestFindDevToolsTargetSelectsBootstrapPage(t *testing.T) {
	payload := []byte(`[
		{"id":"other","type":"page","url":"about:blank"},
		{"id":"bootstrap","type":"page","url":"about:blank#echocode-browser-bootstrap-9222"},
		{"id":"worker","type":"worker","url":"about:blank#echocode-browser-bootstrap-9222"}
	]`)
	targetID, err := findDevToolsTarget(payload, "about:blank#echocode-browser-bootstrap-9222")
	if err != nil {
		t.Fatal(err)
	}
	if targetID != "bootstrap" {
		t.Fatalf("target ID = %q, want bootstrap", targetID)
	}
}

func TestFindDevToolsTargetRejectsMissingBootstrapPage(t *testing.T) {
	if _, err := findDevToolsTarget([]byte(`[{"id":"other","type":"page","url":"about:blank"}]`), "about:blank#missing"); err == nil {
		t.Fatal("expected missing bootstrap target error")
	}
}
