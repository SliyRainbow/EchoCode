package browserengine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// RenderURLScreenshot captures an isolated background target. It does not add
// a user-visible browser tab or replace the active tab in the desktop UI.
func (s *Service) RenderURLScreenshot(ctx context.Context, rawURL, outputPath string, width, height int) (string, error) {
	targetURL, err := normalizeAddress(rawURL)
	if err != nil {
		return "", err
	}
	if err := s.validateNavigation(targetURL); err != nil {
		return "", err
	}
	if err := s.ensureStarted(ctx); err != nil {
		return "", err
	}
	if width < 320 || width > 2560 {
		width = 1440
	}
	if height < 240 || height > 4096 {
		height = 1200
	}
	outputPath, err = filepath.Abs(filepath.Clean(outputPath))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", err
	}

	s.mu.RLock()
	rootCtx := s.rootCtx
	s.mu.RUnlock()
	if rootCtx == nil {
		return "", fmt.Errorf("browser runtime is unavailable")
	}
	tabCtx, tabCancel := chromedp.NewContext(rootCtx)
	defer tabCancel()
	runCtx, runCancel := operationContext(ctx, tabCtx, 35*time.Second)
	defer runCancel()
	var image []byte
	err = chromedp.Run(runCtx,
		network.Enable(),
		page.Enable(),
		emulation.SetDeviceMetricsOverride(int64(width), int64(height), 1, false),
		chromedp.Navigate(targetURL),
		chromedp.WaitReady("body", chromedp.ByQuery),
		chromedp.Sleep(900*time.Millisecond),
		chromedp.ActionFunc(func(targetCtx context.Context) error {
			var captureErr error
			image, captureErr = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatPng).
				WithCaptureBeyondViewport(false).
				Do(targetCtx)
			return captureErr
		}),
	)
	if err != nil {
		return "", fmt.Errorf("render page screenshot: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(outputPath), ".echocode-render-*.png")
	if err != nil {
		return "", err
	}
	temporaryPath := temporary.Name()
	ok := false
	defer func() {
		_ = temporary.Close()
		if !ok {
			_ = os.Remove(temporaryPath)
		}
	}()
	if _, err := temporary.Write(image); err != nil {
		return "", err
	}
	if err := temporary.Sync(); err != nil {
		return "", err
	}
	if err := temporary.Close(); err != nil {
		return "", err
	}
	_ = os.Remove(outputPath)
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return "", err
	}
	ok = true
	return outputPath, nil
}
