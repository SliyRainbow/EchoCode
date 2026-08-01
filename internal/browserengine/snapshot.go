package browserengine

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const (
	frameCaptureTimeout   = 8 * time.Second
	frameElementsTimeout  = 1500 * time.Millisecond
	frameMetadataInterval = 750 * time.Millisecond
	frameElementInterval  = time.Second
	frameMetadataTimeout  = 900 * time.Millisecond
	frameElementTimeout   = 700 * time.Millisecond
)

func (s *Service) CaptureFrame(ctx context.Context, tabID string, includeElements bool) (Frame, error) {
	tab, err := s.tab(tabID)
	if err != nil {
		return Frame{}, err
	}
	if frame, ok := cachedBrowserFrame(tab); ok {
		s.scheduleFrameDetails(tab, includeElements)
		s.clearError()
		return frame, nil
	}
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	if frame, ok := cachedBrowserFrame(tab); ok {
		s.scheduleFrameDetails(tab, includeElements)
		s.clearError()
		return frame, nil
	}
	runCtx, cancel := operationContext(ctx, tab.ctx, frameCaptureTimeout)
	defer cancel()

	var image []byte
	var title, location string
	var elements []Element
	var currentIndex int64
	var historyLength int
	actions := []chromedp.Action{
		chromedp.Title(&title),
		chromedp.Location(&location),
		chromedp.ActionFunc(func(ctx context.Context) error {
			index, entries, historyErr := page.GetNavigationHistory().Do(ctx)
			if historyErr == nil {
				currentIndex = index
				historyLength = len(entries)
			}
			var captureErr error
			image, captureErr = page.CaptureScreenshot().
				WithFormat(page.CaptureScreenshotFormatJpeg).
				WithQuality(72).
				WithCaptureBeyondViewport(false).
				WithOptimizeForSpeed(true).
				Do(ctx)
			return captureErr
		}),
	}
	if err := chromedp.Run(runCtx, actions...); err != nil {
		// Frame capture is a polling operation. A slow or mid-navigation page
		// must not poison the browser-wide error state for every later poll.
		return Frame{}, fmt.Errorf("捕获浏览器画面失败: %w", err)
	}
	if includeElements {
		elementsCtx, elementsCancel := operationContext(ctx, tab.ctx, frameElementsTimeout)
		_ = chromedp.Run(elementsCtx, chromedp.Evaluate(interactiveElementsScript, &elements))
		elementsCancel()
	}

	encodedImage := base64.StdEncoding.EncodeToString(image)
	capturedAt := time.Now().UTC().Format(time.RFC3339Nano)
	tab.mu.Lock()
	if title != "" {
		tab.state.Title = title
	}
	if location != "" {
		tab.state.URL = location
	}
	tab.state.CanGoBack = currentIndex > 0
	tab.state.CanGoForward = currentIndex+1 < int64(historyLength)
	tab.state.Error = ""
	tab.frameData = encodedImage
	tab.frameCapturedAt = capturedAt
	tab.frameElements = append([]Element(nil), elements...)
	state := tab.state
	tab.mu.Unlock()
	s.clearError()

	return Frame{
		Tab:          state,
		ImageDataURL: "data:image/jpeg;base64," + encodedImage,
		Width:        state.ViewportWidth,
		Height:       state.ViewportHeight,
		Elements:     elements,
		CapturedAt:   capturedAt,
	}, nil
}

func cachedBrowserFrame(tab *tabSession) (Frame, bool) {
	tab.mu.RLock()
	defer tab.mu.RUnlock()
	if tab.frameData == "" {
		return Frame{}, false
	}
	state := tab.state
	return Frame{
		Tab:          state,
		ImageDataURL: "data:image/jpeg;base64," + tab.frameData,
		Width:        state.ViewportWidth,
		Height:       state.ViewportHeight,
		Elements:     append([]Element(nil), tab.frameElements...),
		CapturedAt:   tab.frameCapturedAt,
	}, true
}

func (s *Service) scheduleFrameDetails(tab *tabSession, includeElements bool) {
	now := time.Now()
	tab.mu.Lock()
	if includeElements {
		tab.frameElementsPending = true
	}
	if tab.frameDetailsBusy {
		tab.mu.Unlock()
		return
	}
	wantElements := tab.frameElementsPending
	if wantElements {
		if !tab.frameElementsAt.IsZero() && now.Sub(tab.frameElementsAt) < frameElementInterval {
			tab.frameElementsPending = false
			tab.mu.Unlock()
			return
		}
	} else if !tab.frameDetailsAt.IsZero() && now.Sub(tab.frameDetailsAt) < frameMetadataInterval {
		tab.mu.Unlock()
		return
	}
	tab.frameDetailsBusy = true
	tab.frameElementsPending = false
	tab.mu.Unlock()

	go s.refreshFrameDetails(tab, wantElements)
}

func (s *Service) refreshFrameDetails(tab *tabSession, includeElements bool) {
	defer func() {
		tab.mu.Lock()
		tab.frameDetailsBusy = false
		pending := tab.frameElementsPending
		tab.mu.Unlock()
		if pending && tab.ctx.Err() == nil {
			s.scheduleFrameDetails(tab, true)
		}
	}()

	var title, location string
	var currentIndex int64
	var historyLength int
	metadataCtx, metadataCancel := context.WithTimeout(tab.ctx, frameMetadataTimeout)
	metadataErr := chromedp.Run(metadataCtx,
		chromedp.Title(&title),
		chromedp.Location(&location),
		chromedp.ActionFunc(func(ctx context.Context) error {
			index, entries, err := page.GetNavigationHistory().Do(ctx)
			if err == nil {
				currentIndex = index
				historyLength = len(entries)
			}
			return err
		}),
	)
	metadataCancel()

	var elements []Element
	elementsOK := false
	if includeElements && tab.ctx.Err() == nil {
		elementsCtx, elementsCancel := context.WithTimeout(tab.ctx, frameElementTimeout)
		elementsOK = chromedp.Run(elementsCtx, chromedp.Evaluate(interactiveElementsScript, &elements)) == nil
		elementsCancel()
	}

	now := time.Now()
	tab.mu.Lock()
	if metadataErr == nil {
		if title != "" {
			tab.state.Title = title
		}
		if location != "" {
			tab.state.URL = location
		}
		tab.state.CanGoBack = currentIndex > 0
		tab.state.CanGoForward = currentIndex+1 < int64(historyLength)
		tab.state.Error = ""
	}
	tab.frameDetailsAt = now
	if elementsOK {
		tab.frameElements = append([]Element(nil), elements...)
		tab.frameElementsAt = now
	}
	tab.mu.Unlock()
}

func (s *Service) Snapshot(ctx context.Context) (Snapshot, error) {
	return s.SnapshotTab(ctx, "")
}

func (s *Service) SnapshotTab(_ context.Context, tabID string) (Snapshot, error) {
	tab, err := s.tab(tabID)
	if err != nil {
		return Snapshot{}, err
	}
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 10*time.Second)
	defer cancel()
	var snapshot Snapshot
	if err := chromedp.Run(runCtx, chromedp.Evaluate(pageSnapshotScript, &snapshot)); err != nil {
		return Snapshot{}, s.setError(fmt.Errorf("读取页面结构失败: %w", err))
	}
	snapshot.CapturedAt = nowRFC3339()
	return snapshot, nil
}

func (s *Service) Inspector(ctx context.Context, tabID string) (Inspector, error) {
	snapshot, err := s.SnapshotTab(ctx, tabID)
	if err != nil {
		return Inspector{}, err
	}
	tab, err := s.tab(tabID)
	if err != nil {
		return Inspector{}, err
	}
	tab.mu.RLock()
	consoleEntries := append([]ConsoleEntry(nil), tab.console...)
	networkEntries := append([]NetworkEntry(nil), tab.network...)
	tab.mu.RUnlock()
	return Inspector{Snapshot: snapshot, Console: consoleEntries, Network: networkEntries}, nil
}

func (s *Service) SaveScreenshot(_ context.Context, tabID, outputPath string) (string, error) {
	tab, err := s.tab(tabID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(outputPath) == "" {
		outputPath = filepath.Join(s.downloadsDir, "echocode-screenshot-"+time.Now().Format("20060102-150405")+".png")
	}
	outputPath, err = filepath.Abs(filepath.Clean(outputPath))
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return "", err
	}

	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 12*time.Second)
	defer cancel()
	var image []byte
	if err := chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		var captureErr error
		image, captureErr = page.CaptureScreenshot().WithFormat(page.CaptureScreenshotFormatPng).Do(ctx)
		return captureErr
	})); err != nil {
		return "", err
	}
	if err := os.WriteFile(outputPath, image, 0o644); err != nil {
		return "", err
	}
	return outputPath, nil
}

func (s *Service) Evaluate(_ context.Context, tabID, expression string) (any, error) {
	s.mu.RLock()
	enabled := s.settings.DeveloperCDPAccess
	s.mu.RUnlock()
	if !enabled {
		return nil, fmt.Errorf("完整 CDP 访问权限未开启")
	}
	tab, err := s.tab(tabID)
	if err != nil {
		return nil, err
	}
	expression = strings.TrimSpace(expression)
	if expression == "" {
		return nil, fmt.Errorf("表达式不能为空")
	}
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 8*time.Second)
	defer cancel()
	var result any
	if err := chromedp.Run(runCtx, chromedp.Evaluate(expression, &result)); err != nil {
		return nil, err
	}
	return result, nil
}

func (s *Service) SnapshotJSON(ctx context.Context) (string, error) {
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

const interactiveElementsScript = `(() => {
	const clean = value => String(value || '').replace(/\s+/g, ' ').trim().slice(0, 240);
	const escapeCSS = value => window.CSS && CSS.escape ? CSS.escape(value) : value.replace(/[^a-zA-Z0-9_-]/g, '\\$&');
	const selectorFor = el => {
		if (el.id) return '#' + escapeCSS(el.id);
		for (const attr of ['data-testid', 'data-test', 'aria-label', 'name']) {
			const value = el.getAttribute(attr);
			if (value) return el.tagName.toLowerCase() + '[' + attr + '=' + JSON.stringify(value) + ']';
		}
		const parts = [];
		let current = el;
		while (current && current.nodeType === 1 && parts.length < 6) {
			let part = current.tagName.toLowerCase();
			const siblings = current.parentElement ? [...current.parentElement.children].filter(item => item.tagName === current.tagName) : [];
			if (siblings.length > 1) part += ':nth-of-type(' + (siblings.indexOf(current) + 1) + ')';
			parts.unshift(part);
			current = current.parentElement;
		}
		return parts.join(' > ');
	};
	const candidates = [...document.querySelectorAll('a,button,input,textarea,select,summary,[role],[contenteditable="true"],[tabindex]')];
	const result = [];
	for (const el of candidates) {
		const rect = el.getBoundingClientRect();
		const style = getComputedStyle(el);
		if (rect.width < 2 || rect.height < 2 || rect.bottom < 0 || rect.right < 0 || rect.top > innerHeight || rect.left > innerWidth || style.visibility === 'hidden' || style.display === 'none') continue;
		result.push({
			index: result.length + 1,
			selector: selectorFor(el),
			tag: el.tagName.toLowerCase(),
			role: clean(el.getAttribute('role')),
			name: clean(el.getAttribute('aria-label') || el.getAttribute('name') || el.title),
			text: clean(el.innerText || el.value || el.textContent),
			type: clean(el.getAttribute('type')),
			placeholder: clean(el.getAttribute('placeholder')),
			href: clean(el.href),
			x: Math.max(0, rect.left), y: Math.max(0, rect.top),
			width: Math.min(rect.width, innerWidth - Math.max(0, rect.left)),
			height: Math.min(rect.height, innerHeight - Math.max(0, rect.top))
		});
		if (result.length >= 160) break;
	}
	return result;
})()`

const pageSnapshotScript = `(() => {
	const elements = ` + interactiveElementsScript + `;
	return {
		title: document.title || '',
		url: location.href,
		text: String(document.body ? document.body.innerText : '').replace(/\n{3,}/g, '\n\n').trim().slice(0, 12000),
		elements
	};
})()`
