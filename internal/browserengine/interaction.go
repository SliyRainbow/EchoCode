package browserengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

func (s *Service) Navigate(ctx context.Context, tabID, rawURL string) error {
	targetURL, err := normalizeAddress(rawURL)
	if err != nil {
		return err
	}
	if err := s.validateNavigation(targetURL); err != nil {
		return err
	}
	tab, err := s.tab(tabID)
	if err != nil {
		return err
	}
	tab.mu.Lock()
	tab.state.Loading = true
	tab.state.Error = ""
	tab.frameData = ""
	tab.frameCapturedAt = ""
	tab.frameElements = nil
	tab.mu.Unlock()

	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := operationContext(ctx, tab.ctx, 12*time.Second)
	defer cancel()
	s.mu.RLock()
	embeddedSurface, embedded := s.nativeSurface.(embeddedNativeBrowserSurface)
	embedded = embedded && s.nativeReady
	s.mu.RUnlock()
	if embedded {
		if err := embeddedSurface.NavigateTab(tabID, targetURL); err != nil {
			tab.mu.Lock()
			tab.state.Loading = false
			tab.state.Error = readableNavigationError(err)
			tab.mu.Unlock()
			return s.setError(fmt.Errorf("网页加载失败: %w", err))
		}
		tab.mu.Lock()
		tab.state.URL = targetURL
		tab.state.Error = ""
		tab.mu.Unlock()
		s.clearError()
		return nil
	}
	var navigationError string
	err = chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		_, _, errorText, navigateErr := page.Navigate(targetURL).Do(ctx)
		navigationError = strings.TrimSpace(errorText)
		return navigateErr
	}))
	if err == nil && navigationError != "" {
		err = fmt.Errorf("%s", navigationError)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil && tab.ctx.Err() == nil {
			// page.Navigate may time out while the document is already loading.
			// Keep the tab recoverable and let frame polling report real failures.
			tab.mu.Lock()
			tab.state.Loading = true
			tab.state.Error = ""
			if tab.state.URL == "" || tab.state.URL == "about:blank" {
				tab.state.URL = targetURL
			}
			tab.mu.Unlock()
			s.clearError()
			return nil
		}
		tab.mu.Lock()
		tab.state.Loading = false
		tab.state.Error = readableNavigationError(err)
		tab.mu.Unlock()
		return s.setError(fmt.Errorf("网页加载失败: %w", err))
	}
	tab.mu.Lock()
	tab.state.URL = targetURL
	tab.state.Error = ""
	tab.mu.Unlock()
	s.clearError()
	return nil
}

func (s *Service) Back(ctx context.Context, tabID string) error {
	return s.navigateHistory(ctx, tabID, -1)
}

func (s *Service) Forward(ctx context.Context, tabID string) error {
	return s.navigateHistory(ctx, tabID, 1)
}

func (s *Service) navigateHistory(_ context.Context, tabID string, offset int64) error {
	tab, err := s.tab(tabID)
	if err != nil {
		return err
	}
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 15*time.Second)
	defer cancel()
	err = chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		index, entries, historyErr := page.GetNavigationHistory().Do(ctx)
		if historyErr != nil {
			return historyErr
		}
		target := index + offset
		if target < 0 || target >= int64(len(entries)) {
			return nil
		}
		return page.NavigateToHistoryEntry(entries[target].ID).Do(ctx)
	}))
	if err != nil {
		return err
	}
	return s.refreshMetadataLocked(tab)
}

func (s *Service) Reload(_ context.Context, tabID string) error {
	tab, err := s.tab(tabID)
	if err != nil {
		return err
	}
	tab.mu.Lock()
	tab.state.Loading = true
	tab.mu.Unlock()
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 25*time.Second)
	defer cancel()
	if err := chromedp.Run(runCtx, chromedp.Reload()); err != nil {
		return err
	}
	return s.refreshMetadataLocked(tab)
}

func (s *Service) Resize(_ context.Context, tabID string, width, height int) error {
	tab, err := s.tab(tabID)
	if err != nil {
		return err
	}
	width = clamp(width, 320, 1920)
	height = clamp(height, 240, 1200)
	tab.mu.RLock()
	unchanged := tab.state.ViewportWidth == width && tab.state.ViewportHeight == height
	tab.mu.RUnlock()
	if unchanged {
		return nil
	}
	s.mu.RLock()
	nativeReady := s.nativeReady
	s.mu.RUnlock()
	if nativeReady {
		tab.mu.Lock()
		tab.state.ViewportWidth = width
		tab.state.ViewportHeight = height
		tab.mu.Unlock()
		return nil
	}
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 5*time.Second)
	defer cancel()
	if err := chromedp.Run(runCtx, emulation.SetDeviceMetricsOverride(int64(width), int64(height), 1, false)); err != nil {
		return err
	}
	tab.mu.Lock()
	tab.state.ViewportWidth = width
	tab.state.ViewportHeight = height
	tab.mu.Unlock()
	return nil
}

func (s *Service) Click(_ context.Context, tabID string, x, y float64, clickCount int) error {
	tab, err := s.tab(tabID)
	if err != nil {
		return err
	}
	if clickCount < 1 {
		clickCount = 1
	}
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 2*time.Minute)
	defer cancel()
	return chromedp.Run(runCtx,
		input.DispatchMouseEvent(input.MousePressed, x, y).WithButton(input.Left).WithClickCount(int64(clickCount)),
		input.DispatchMouseEvent(input.MouseReleased, x, y).WithButton(input.Left).WithClickCount(int64(clickCount)),
	)
}

func (s *Service) Scroll(_ context.Context, tabID string, deltaX, deltaY float64) error {
	tab, err := s.tab(tabID)
	if err != nil {
		return err
	}
	tab.mu.RLock()
	x := float64(tab.state.ViewportWidth) / 2
	y := float64(tab.state.ViewportHeight) / 2
	tab.mu.RUnlock()
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 5*time.Second)
	defer cancel()
	return chromedp.Run(runCtx,
		input.DispatchMouseEvent(input.MouseWheel, x, y).WithDeltaX(deltaX).WithDeltaY(deltaY),
	)
}

func (s *Service) Type(_ context.Context, tabID, text string) error {
	tab, err := s.tab(tabID)
	if err != nil {
		return err
	}
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 8*time.Second)
	defer cancel()
	return chromedp.Run(runCtx, input.InsertText(text))
}

func (s *Service) Key(_ context.Context, tabID, key string, ctrl, alt, shift, meta bool) error {
	tab, err := s.tab(tabID)
	if err != nil {
		return err
	}
	encoded := browserKey(key)
	modifiers := make([]input.Modifier, 0, 4)
	if ctrl {
		modifiers = append(modifiers, input.ModifierCtrl)
	}
	if alt {
		modifiers = append(modifiers, input.ModifierAlt)
	}
	if shift {
		modifiers = append(modifiers, input.ModifierShift)
	}
	if meta {
		modifiers = append(modifiers, input.ModifierMeta)
	}
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 5*time.Second)
	defer cancel()
	return chromedp.Run(runCtx, chromedp.KeyEvent(encoded, chromedp.KeyModifiers(modifiers...)))
}

func (s *Service) ClickSelector(ctx context.Context, selector string) error {
	tab, err := s.tab("")
	if err != nil {
		return err
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return fmt.Errorf("selector 不能为空")
	}
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 2*time.Minute)
	defer cancel()
	return chromedp.Run(runCtx,
		chromedp.ScrollIntoView(selector, chromedp.ByQuery),
		chromedp.Click(selector, chromedp.ByQuery),
	)
}

func (s *Service) TypeSelector(ctx context.Context, selector, text string) error {
	tab, err := s.tab("")
	if err != nil {
		return err
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return fmt.Errorf("selector 不能为空")
	}
	encodedSelector, _ := json.Marshal(selector)
	clearScript := fmt.Sprintf(`(() => { const el = document.querySelector(%s); if (!el) throw new Error('element not found'); el.focus(); if ('value' in el) { el.value = ''; el.dispatchEvent(new Event('input', {bubbles:true})); } else { const range = document.createRange(); range.selectNodeContents(el); const selection = window.getSelection(); selection.removeAllRanges(); selection.addRange(range); } })()`, encodedSelector)
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 12*time.Second)
	defer cancel()
	return chromedp.Run(runCtx,
		chromedp.Evaluate(clearScript, nil),
		input.InsertText(text),
	)
}

func (s *Service) Autofill(_ context.Context, tabID string) (int, error) {
	s.mu.RLock()
	settings := s.settings
	s.mu.RUnlock()
	if !settings.AutofillContactEnabled {
		return 0, fmt.Errorf("联系信息自动填充已在设置中关闭")
	}
	tab, err := s.tab(tabID)
	if err != nil {
		return 0, err
	}
	profile, _ := json.Marshal(settings.AutofillProfile)
	script := fmt.Sprintf(`(() => {
		const profile = %s;
		const fields = {
			name: profile.fullName, fullname: profile.fullName, 'given-name': profile.fullName,
			email: profile.email, tel: profile.phone, phone: profile.phone,
			organization: profile.organization, company: profile.organization,
			'street-address': profile.streetAddress, address: profile.streetAddress,
			'address-level2': profile.city, city: profile.city,
			'address-level1': profile.region, state: profile.region, province: profile.region,
			'postal-code': profile.postalCode, zip: profile.postalCode,
			country: profile.country, 'country-name': profile.country
		};
		let count = 0;
		for (const el of document.querySelectorAll('input, textarea, select')) {
			if (el.disabled || el.readOnly || el.type === 'password' || el.type === 'hidden') continue;
			const hint = [el.autocomplete, el.name, el.id, el.placeholder].join(' ').toLowerCase();
			let value = '';
			for (const [key, candidate] of Object.entries(fields)) {
				if (candidate && hint.includes(key)) { value = candidate; break; }
			}
			if (!value) continue;
			el.value = value;
			el.dispatchEvent(new Event('input', {bubbles:true}));
			el.dispatchEvent(new Event('change', {bubbles:true}));
			count++;
		}
		return count;
	})()`, profile)
	var count int
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 8*time.Second)
	defer cancel()
	if err := chromedp.Run(runCtx, chromedp.Evaluate(script, &count)); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Service) FillCredential(_ context.Context, tabID, origin, username, password string) (int, error) {
	tab, err := s.tab(tabID)
	if err != nil {
		return 0, err
	}
	payload, _ := json.Marshal(map[string]string{
		"origin":   strings.TrimSpace(origin),
		"username": username,
		"password": password,
	})
	script := fmt.Sprintf(`(() => {
		const credential = %s;
		if (location.origin.toLowerCase() !== credential.origin.toLowerCase()) {
			throw new Error('credential origin mismatch');
		}
		const visible = el => {
			const rect = el.getBoundingClientRect();
			const style = getComputedStyle(el);
			return rect.width > 1 && rect.height > 1 && style.display !== 'none' && style.visibility !== 'hidden' && !el.disabled && !el.readOnly;
		};
		const setValue = (el, value) => {
			const descriptor = Object.getOwnPropertyDescriptor(Object.getPrototypeOf(el), 'value');
			if (descriptor && descriptor.set) descriptor.set.call(el, value); else el.value = value;
			el.dispatchEvent(new Event('input', {bubbles:true}));
			el.dispatchEvent(new Event('change', {bubbles:true}));
		};
		const passwords = [...document.querySelectorAll('input[type="password"]')].filter(visible);
		if (!passwords.length) return 0;
		const passwordInput = passwords.find(el => el.autocomplete === 'current-password') || passwords[0];
		const scope = passwordInput.form || document;
		const usernames = [...scope.querySelectorAll('input')].filter(el => visible(el) && el !== passwordInput && (el.autocomplete === 'username' || el.type === 'email' || el.type === 'text' || /user|email|login|account/i.test([el.name, el.id, el.placeholder].join(' '))));
		let count = 0;
		if (usernames.length) { setValue(usernames[0], credential.username); count++; }
		setValue(passwordInput, credential.password); count++;
		passwordInput.focus();
		return count;
	})()`, payload)
	var count int
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 8*time.Second)
	defer cancel()
	if err := chromedp.Run(runCtx, chromedp.Evaluate(script, &count)); err != nil {
		return 0, err
	}
	return count, nil
}

func (s *Service) HandleDialog(_ context.Context, tabID string, accept bool, promptText string) error {
	tab, err := s.tab(tabID)
	if err != nil {
		return err
	}
	runCtx, cancel := context.WithTimeout(tab.ctx, 5*time.Second)
	defer cancel()
	action := page.HandleJavaScriptDialog(accept)
	if accept && promptText != "" {
		action = action.WithPromptText(promptText)
	}
	if err := chromedp.Run(runCtx, action); err != nil {
		return err
	}
	tab.mu.Lock()
	tab.state.Dialog = nil
	tab.mu.Unlock()
	return nil
}

func (s *Service) refreshMetadataLocked(tab *tabSession) error {
	runCtx, cancel := context.WithTimeout(tab.ctx, 5*time.Second)
	defer cancel()
	var title, location string
	var currentIndex int64
	var historyLength int
	err := chromedp.Run(runCtx,
		chromedp.Title(&title),
		chromedp.Location(&location),
		chromedp.ActionFunc(func(ctx context.Context) error {
			index, entries, err := page.GetNavigationHistory().Do(ctx)
			if err != nil {
				return err
			}
			currentIndex = index
			historyLength = len(entries)
			return nil
		}),
	)
	if err != nil {
		return err
	}
	tab.mu.Lock()
	if strings.TrimSpace(title) != "" {
		tab.state.Title = title
	}
	if strings.TrimSpace(location) != "" {
		tab.state.URL = location
	}
	tab.state.Loading = false
	tab.state.CanGoBack = currentIndex > 0
	tab.state.CanGoForward = currentIndex+1 < int64(historyLength)
	tab.state.Error = ""
	tab.mu.Unlock()
	s.clearError()
	return nil
}

func (s *Service) validateNavigation(targetURL string) error {
	s.mu.RLock()
	settings := s.settings
	s.mu.RUnlock()
	if settings.AllowNetwork || isLocalAddress(targetURL) || targetURL == "about:blank" {
		return nil
	}
	return fmt.Errorf("网络访问已在环境设置中关闭")
}

func normalizeAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "about:blank") {
		return "about:blank", nil
	}
	parsed, err := url.Parse(raw)
	if err == nil && parsed.Scheme != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https":
			if parsed.Host == "" {
				return "", fmt.Errorf("网址缺少主机名")
			}
			return parsed.String(), nil
		default:
			return "", fmt.Errorf("只允许打开 HTTP 或 HTTPS 地址")
		}
	}
	if !strings.ContainsAny(raw, " \t\r\n") && (strings.Contains(raw, ".") || strings.Contains(raw, ":")) {
		candidate := "https://" + raw
		if parsed, err := url.Parse(candidate); err == nil && parsed.Host != "" {
			return parsed.String(), nil
		}
	}
	return "https://www.google.com/search?q=" + url.QueryEscape(raw), nil
}

func isLocalAddress(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.Trim(strings.ToLower(parsed.Hostname()), "[]")
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func browserKey(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "enter", "return":
		return kb.Enter
	case "backspace":
		return kb.Backspace
	case "delete":
		return kb.Delete
	case "tab":
		return kb.Tab
	case "escape", "esc":
		return kb.Escape
	case "arrowup", "up":
		return kb.ArrowUp
	case "arrowdown", "down":
		return kb.ArrowDown
	case "arrowleft", "left":
		return kb.ArrowLeft
	case "arrowright", "right":
		return kb.ArrowRight
	case "home":
		return kb.Home
	case "end":
		return kb.End
	case "pageup":
		return kb.PageUp
	case "pagedown":
		return kb.PageDown
	case "space", " ":
		return " "
	default:
		return key
	}
}

func readableNavigationError(err error) string {
	message := strings.TrimSpace(err.Error())
	message = strings.TrimPrefix(message, "page load error ")
	if message == "" {
		return "网页加载失败"
	}
	return message
}

func clamp(value, min, max int) int {
	if value < min {
		return min
	}
	if value > max {
		return max
	}
	return value
}
