package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/computercontrol"
)

type computerToolBridge struct {
	app *App
}

func (b *computerToolBridge) ListWindows(ctx context.Context) ([]computercontrol.Window, error) {
	if b == nil || b.app == nil || b.app.computer == nil {
		return nil, fmt.Errorf("电脑操控服务不可用")
	}
	settings := b.app.runtimeSettingsSnapshot().ComputerControl
	if !computerControlEnabled(settings.AnyAppEnabled, settings.ChromeEnabled, settings.AlwaysAllowedApps) {
		return nil, fmt.Errorf("电脑操控未在设置中启用")
	}
	windowsList, err := b.app.computer.ListWindows(ctx)
	if err != nil {
		return nil, err
	}
	allowed := make([]computercontrol.Window, 0, len(windowsList))
	for _, window := range windowsList {
		if allowedComputerWindow(window, settings.AnyAppEnabled, settings.ChromeEnabled, settings.AlwaysAllowedApps) {
			allowed = append(allowed, window)
		}
	}
	return allowed, nil
}

func (b *computerToolBridge) FocusWindow(ctx context.Context, id string) error {
	if _, err := b.requireAllowedWindow(ctx, id); err != nil {
		return err
	}
	return b.app.computer.FocusWindow(ctx, id)
}

func (b *computerToolBridge) ClickWindow(ctx context.Context, id string, x, y int) error {
	if _, err := b.requireAllowedWindow(ctx, id); err != nil {
		return err
	}
	return b.app.computer.ClickWindow(ctx, id, x, y)
}

func (b *computerToolBridge) TypeText(ctx context.Context, id, text string) error {
	if _, err := b.requireAllowedWindow(ctx, id); err != nil {
		return err
	}
	return b.app.computer.TypeText(ctx, id, text)
}

func (b *computerToolBridge) PressKey(ctx context.Context, id, key string, ctrl, alt, shift bool) error {
	if _, err := b.requireAllowedWindow(ctx, id); err != nil {
		return err
	}
	return b.app.computer.PressKey(ctx, id, key, ctrl, alt, shift)
}

func (b *computerToolBridge) ScreenshotWindow(ctx context.Context, id, outputPath string) (string, error) {
	if _, err := b.requireAllowedWindow(ctx, id); err != nil {
		return "", err
	}
	return b.app.computer.ScreenshotWindow(ctx, id, outputPath)
}

func (b *computerToolBridge) requireAllowedWindow(ctx context.Context, id string) (computercontrol.Window, error) {
	windowsList, err := b.ListWindows(ctx)
	if err != nil {
		return computercontrol.Window{}, err
	}
	for _, window := range windowsList {
		if strings.EqualFold(window.ID, strings.TrimSpace(id)) {
			return window, nil
		}
	}
	return computercontrol.Window{}, fmt.Errorf("窗口不在允许操控列表中或已经关闭: %s", id)
}

func computerControlEnabled(anyApp, chrome bool, allowedApps []string) bool {
	if anyApp || chrome {
		return true
	}
	for _, allowed := range allowedApps {
		if strings.TrimSpace(allowed) != "" {
			return true
		}
	}
	return false
}

func allowedComputerWindow(window computercontrol.Window, anyApp, chrome bool, allowedApps []string) bool {
	processName := strings.ToLower(strings.TrimSpace(filepath.Base(window.ProcessName)))
	if window.PID == uint32(os.Getpid()) || processName == "echocode.exe" || processName == "echocode" {
		return false
	}
	if anyApp {
		return true
	}
	if chrome && processName == "chrome.exe" {
		return true
	}
	title := strings.ToLower(strings.TrimSpace(window.Title))
	for _, allowed := range allowedApps {
		allowed = strings.ToLower(strings.TrimSpace(allowed))
		if allowed == "" {
			continue
		}
		if processName == strings.ToLower(filepath.Base(allowed)) || title == allowed || (len(allowed) >= 3 && strings.Contains(title, allowed)) {
			return true
		}
	}
	return false
}
