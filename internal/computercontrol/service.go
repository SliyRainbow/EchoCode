package computercontrol

import "context"

// Window is a controllable top-level desktop window.
type Window struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	ProcessName string `json:"processName"`
	PID         uint32 `json:"pid"`
	X           int    `json:"x"`
	Y           int    `json:"y"`
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Minimized   bool   `json:"minimized"`
	Foreground  bool   `json:"foreground"`
}

type Service struct{}

func New() *Service { return &Service{} }

type Controller interface {
	ListWindows(context.Context) ([]Window, error)
	FocusWindow(context.Context, string) error
	ClickWindow(context.Context, string, int, int) error
	TypeText(context.Context, string, string) error
	PressKey(context.Context, string, string, bool, bool, bool) error
	ScreenshotWindow(context.Context, string, string) (string, error)
}
