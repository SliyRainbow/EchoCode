//go:build !windows

package computercontrol

import (
	"context"
	"errors"
)

var errUnsupported = errors.New("其他窗口操控当前仅支持 Windows")

func (*Service) ListWindows(context.Context) ([]Window, error)       { return nil, errUnsupported }
func (*Service) FocusWindow(context.Context, string) error           { return errUnsupported }
func (*Service) ClickWindow(context.Context, string, int, int) error { return errUnsupported }
func (*Service) TypeText(context.Context, string, string) error      { return errUnsupported }
func (*Service) PressKey(context.Context, string, string, bool, bool, bool) error {
	return errUnsupported
}
func (*Service) ScreenshotWindow(context.Context, string, string) (string, error) {
	return "", errUnsupported
}
