package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// browserToolBridge connects Agent tool calls to the same managed browser
// session used by the desktop UI.
type browserToolBridge struct {
	app *App
}

func (b *browserToolBridge) OpenURL(ctx context.Context, targetURL string) (string, error) {
	if b == nil || b.app == nil || b.app.browser == nil {
		return "", fmt.Errorf("内置浏览器不可用")
	}
	state, err := b.app.browser.Open(ctx, targetURL)
	if err != nil {
		return "", err
	}
	for _, tab := range state.Tabs {
		if tab.ID == state.ActiveTabID && tab.Error != "" {
			return "", fmt.Errorf("打开网页 %s 失败: %s", targetURL, tab.Error)
		}
	}
	if b.app.ctx != nil {
		runtime.EventsEmit(b.app.ctx, "browser:open", BrowserPreview{
			Name:    activeBrowserTitle(state),
			URL:     targetURL,
			TabID:   state.ActiveTabID,
			Managed: true,
		})
	}
	payload, _ := json.Marshal(map[string]string{
		"tabId": state.ActiveTabID,
		"url":   targetURL,
	})
	return string(payload), nil
}

func (b *browserToolBridge) SnapshotJSON(ctx context.Context) (string, error) {
	if b == nil || b.app == nil || b.app.browser == nil {
		return "", fmt.Errorf("内置浏览器不可用")
	}
	return b.app.browser.SnapshotJSON(ctx)
}

// ReadURLSnapshot opens a managed tab and snapshots that exact tab after the
// page has had a short window to hydrate. Keeping the tab id avoids reading a
// different page when multiple conversations browse concurrently.
func (b *browserToolBridge) ReadURLSnapshot(ctx context.Context, targetURL string) (string, error) {
	opened, err := b.OpenURL(ctx, targetURL)
	if err != nil {
		return "", err
	}
	var target struct {
		TabID string `json:"tabId"`
	}
	if err := json.Unmarshal([]byte(opened), &target); err != nil || strings.TrimSpace(target.TabID) == "" {
		return "", fmt.Errorf("内置浏览器没有返回可读取的标签页")
	}

	startedAt := time.Now()
	stableAfter := startedAt.Add(750 * time.Millisecond)
	deadline := startedAt.Add(5 * time.Second)
	var lastPayload string
	var lastErr error
	var lastContentSignature string
	stableSnapshots := 0
	for {
		snapshot, snapshotErr := b.app.browser.SnapshotTab(ctx, target.TabID)
		if snapshotErr == nil {
			payload, marshalErr := json.Marshal(snapshot)
			if marshalErr != nil {
				return "", marshalErr
			}
			lastPayload = string(payload)
			contentSignature := strings.TrimSpace(snapshot.Text)
			if contentSignature == "" {
				var semanticElements strings.Builder
				for _, element := range snapshot.Elements {
					semanticElements.WriteString(strings.TrimSpace(element.Name))
					semanticElements.WriteByte('\x00')
					semanticElements.WriteString(strings.TrimSpace(element.Text))
					semanticElements.WriteByte('\x00')
					semanticElements.WriteString(strings.TrimSpace(element.Href))
					semanticElements.WriteByte('\n')
				}
				contentSignature = strings.TrimSpace(semanticElements.String())
			}
			if contentSignature != "" {
				if contentSignature == lastContentSignature {
					stableSnapshots++
				} else {
					lastContentSignature = contentSignature
					stableSnapshots = 1
				}
			}
			if (time.Now().After(stableAfter) && stableSnapshots >= 2) || time.Now().After(deadline) {
				return lastPayload, nil
			}
		} else {
			lastErr = snapshotErr
		}
		if time.Now().After(deadline) {
			if lastPayload != "" {
				return lastPayload, nil
			}
			return "", lastErr
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (b *browserToolBridge) ClickSelector(ctx context.Context, selector string) error {
	if b == nil || b.app == nil || b.app.browser == nil {
		return fmt.Errorf("内置浏览器不可用")
	}
	return b.app.browser.ClickSelector(ctx, selector)
}

func (b *browserToolBridge) TypeSelector(ctx context.Context, selector, text string) error {
	if b == nil || b.app == nil || b.app.browser == nil {
		return fmt.Errorf("内置浏览器不可用")
	}
	return b.app.browser.TypeSelector(ctx, selector, text)
}

func (b *browserToolBridge) PressKey(ctx context.Context, key string) error {
	if b == nil || b.app == nil || b.app.browser == nil {
		return fmt.Errorf("内置浏览器不可用")
	}
	return b.app.browser.Key(ctx, "", key, false, false, false, false)
}

func (b *browserToolBridge) Back(ctx context.Context) error {
	if b == nil || b.app == nil || b.app.browser == nil {
		return fmt.Errorf("内置浏览器不可用")
	}
	return b.app.browser.Back(ctx, "")
}

func (b *browserToolBridge) Forward(ctx context.Context) error {
	if b == nil || b.app == nil || b.app.browser == nil {
		return fmt.Errorf("内置浏览器不可用")
	}
	return b.app.browser.Forward(ctx, "")
}

func (b *browserToolBridge) Reload(ctx context.Context) error {
	if b == nil || b.app == nil || b.app.browser == nil {
		return fmt.Errorf("内置浏览器不可用")
	}
	return b.app.browser.Reload(ctx, "")
}

func (b *browserToolBridge) Scroll(ctx context.Context, deltaX, deltaY float64) error {
	if b == nil || b.app == nil || b.app.browser == nil {
		return fmt.Errorf("内置浏览器不可用")
	}
	return b.app.browser.Scroll(ctx, "", deltaX, deltaY)
}

func (b *browserToolBridge) CloseTab(ctx context.Context) error {
	if b == nil || b.app == nil || b.app.browser == nil {
		return fmt.Errorf("内置浏览器不可用")
	}
	state := b.app.browser.State()
	if state.ActiveTabID == "" {
		return fmt.Errorf("当前没有浏览器标签页")
	}
	b.app.browser.CloseTab(state.ActiveTabID)
	return nil
}

func (b *browserToolBridge) SaveScreenshot(ctx context.Context, outputPath string) (string, error) {
	if b == nil || b.app == nil || b.app.browser == nil {
		return "", fmt.Errorf("内置浏览器不可用")
	}
	return b.app.browser.SaveScreenshot(ctx, "", outputPath)
}
