package browserengine

import (
	"fmt"

	"github.com/chromedp/cdproto/target"
)

type NativeSurfaceBounds struct {
	X              float64 `json:"x"`
	Y              float64 `json:"y"`
	Width          float64 `json:"width"`
	Height         float64 `json:"height"`
	ViewportWidth  float64 `json:"viewportWidth"`
	ViewportHeight float64 `json:"viewportHeight"`
}

type nativeWindowInsets struct {
	Left   float64
	Top    float64
	Right  float64
	Bottom float64
}

type nativeBrowserSurface interface {
	Supported() bool
	Attach(processID int) error
	Show(bounds NativeSurfaceBounds, insets nativeWindowInsets) error
	Hide() error
	Close()
}

// embeddedNativeBrowserSurface hosts WebView2 controllers directly inside the
// EchoCode window. The CDP endpoint is private and only used by the agent so the
// visible page and the page being inspected are always the same target.
type embeddedNativeBrowserSurface interface {
	nativeBrowserSurface
	Start(options embeddedBrowserOptions) (embeddedBrowserStart, error)
	CreateTab(tabID, markerURL string) error
	NavigateTab(tabID, targetURL string) error
	ActivateTab(tabID string) error
	CloseTab(tabID string)
}

type embeddedBrowserStart struct {
	Endpoint     string
	RootTargetID target.ID
}

type embeddedBrowserOptions struct {
	ProfileDir             string
	AdditionalBrowserArgs  []string
	DeveloperToolsEnabled  bool
	PasswordManagerEnabled bool
	AutofillContactEnabled bool
}

func validateNativeSurfaceBounds(bounds NativeSurfaceBounds) error {
	if bounds.Width < 1 || bounds.Height < 1 {
		return fmt.Errorf("浏览器显示区域尺寸无效")
	}
	if bounds.ViewportWidth < 1 || bounds.ViewportHeight < 1 {
		return fmt.Errorf("应用窗口尺寸无效")
	}
	return nil
}
