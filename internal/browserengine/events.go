package browserengine

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

const (
	maxConsoleEntries = 200
	maxNetworkEntries = 300
	screencastQuality = 68
)

func (s *Service) listenTarget(tab *tabSession) {
	chromedp.ListenTarget(tab.ctx, func(event any) {
		switch value := event.(type) {
		case *page.EventScreencastFrame:
			tab.mu.Lock()
			tab.frameData = value.Data
			tab.frameCapturedAt = time.Now().UTC().Format(time.RFC3339Nano)
			tab.mu.Unlock()
			go acknowledgeScreencastFrame(tab, value.SessionID)
		case *page.EventFrameStartedLoading:
			tab.mu.Lock()
			tab.state.Loading = true
			tab.mu.Unlock()
		case *page.EventFrameStoppedLoading:
			tab.mu.Lock()
			tab.state.Loading = false
			tab.state.Error = ""
			tab.mu.Unlock()
			s.clearError()
		case *page.EventFrameNavigated:
			if value.Frame != nil && value.Frame.ParentID == "" {
				tab.mu.Lock()
				tab.state.URL = value.Frame.URL
				tab.state.Error = ""
				tab.mu.Unlock()
				s.clearError()
			}
		case *page.EventJavascriptDialogOpening:
			tab.mu.Lock()
			tab.state.Dialog = &Dialog{Type: string(value.Type), Message: value.Message, DefaultValue: value.DefaultPrompt}
			tab.mu.Unlock()
		case *page.EventJavascriptDialogClosed:
			tab.mu.Lock()
			tab.state.Dialog = nil
			tab.mu.Unlock()
		case *cdpruntime.EventConsoleAPICalled:
			entry := ConsoleEntry{
				Level:     string(value.Type),
				Message:   consoleMessage(value),
				Timestamp: nowRFC3339(),
			}
			tab.mu.Lock()
			tab.console = appendCapped(tab.console, entry, maxConsoleEntries)
			tab.mu.Unlock()
		case *cdpruntime.EventExceptionThrown:
			message := "JavaScript exception"
			if value.ExceptionDetails != nil {
				message = value.ExceptionDetails.Text
				if value.ExceptionDetails.Exception != nil && value.ExceptionDetails.Exception.Description != "" {
					message = value.ExceptionDetails.Exception.Description
				}
			}
			tab.mu.Lock()
			tab.console = appendCapped(tab.console, ConsoleEntry{Level: "error", Message: message, Timestamp: nowRFC3339()}, maxConsoleEntries)
			tab.mu.Unlock()
		case *network.EventRequestWillBeSent:
			if value.Request == nil {
				return
			}
			tab.mu.Lock()
			tab.requests[value.RequestID] = pendingRequest{
				method: value.Request.Method,
				url:    value.Request.URL,
				typeID: string(value.Type),
			}
			tab.mu.Unlock()
		case *network.EventResponseReceived:
			if value.Response == nil {
				return
			}
			tab.mu.Lock()
			request := tab.requests[value.RequestID]
			delete(tab.requests, value.RequestID)
			entry := NetworkEntry{
				Method:    request.method,
				URL:       value.Response.URL,
				Status:    int64(value.Response.Status),
				Type:      string(value.Type),
				Timestamp: nowRFC3339(),
			}
			tab.network = appendCapped(tab.network, entry, maxNetworkEntries)
			tab.mu.Unlock()
		case *network.EventLoadingFailed:
			tab.mu.Lock()
			request := tab.requests[value.RequestID]
			delete(tab.requests, value.RequestID)
			entry := NetworkEntry{
				Method:    request.method,
				URL:       request.url,
				Type:      request.typeID,
				Failed:    true,
				Error:     value.ErrorText,
				Timestamp: nowRFC3339(),
			}
			tab.network = appendCapped(tab.network, entry, maxNetworkEntries)
			tab.mu.Unlock()
		}
	})
}

func (s *Service) handleBrowserEvent(event any) {
	switch value := event.(type) {
	case *browser.EventDownloadWillBegin:
		filename := safeDownloadName(value.SuggestedFilename)
		s.mu.Lock()
		s.downloads[value.GUID] = Download{
			ID:        value.GUID,
			URL:       value.URL,
			Filename:  filename,
			State:     "inProgress",
			StartedAt: nowRFC3339(),
		}
		s.mu.Unlock()
	case *browser.EventDownloadProgress:
		s.mu.Lock()
		item := s.downloads[value.GUID]
		item.ID = value.GUID
		item.ReceivedBytes = value.ReceivedBytes
		item.TotalBytes = value.TotalBytes
		item.State = string(value.State)
		if item.State == "completed" && item.Filename != "" {
			item.Path = filepath.Join(s.downloadsDir, item.Filename)
		}
		if item.StartedAt == "" {
			item.StartedAt = nowRFC3339()
		}
		s.downloads[value.GUID] = item
		s.mu.Unlock()
	case *target.EventTargetCreated:
		if value.TargetInfo != nil && string(value.TargetInfo.Type) == "page" && value.TargetInfo.OpenerID != "" {
			go s.adoptPopup(value.TargetInfo)
		}
	case *target.EventTargetDestroyed:
		s.mu.RLock()
		tabID := s.targets[value.TargetID]
		s.mu.RUnlock()
		if tabID != "" {
			s.CloseTab(tabID)
		}
	}
}

func (s *Service) adoptPopup(info *target.Info) {
	time.Sleep(120 * time.Millisecond)
	s.mu.RLock()
	if _, exists := s.targets[info.TargetID]; exists {
		s.mu.RUnlock()
		return
	}
	rootCtx := s.rootCtx
	s.mu.RUnlock()
	if rootCtx == nil {
		return
	}
	tabCtx, cancel := chromedp.NewContext(rootCtx, chromedp.WithTargetID(info.TargetID))
	id := newID("tab")
	tab := &tabSession{
		ctx:      tabCtx,
		cancel:   cancel,
		targetID: info.TargetID,
		requests: map[network.RequestID]pendingRequest{},
		state: Tab{
			ID:             id,
			Title:          info.Title,
			URL:            info.URL,
			Loading:        true,
			ViewportWidth:  defaultViewportWidth,
			ViewportHeight: defaultViewportHeight,
		},
	}
	s.listenTarget(tab)
	actions := []chromedp.Action{cdpruntime.Enable(), network.Enable(), page.Enable()}
	s.mu.RLock()
	nativeReady := s.nativeReady
	s.mu.RUnlock()
	if !nativeReady {
		actions = append(actions,
			emulation.SetDeviceMetricsOverride(defaultViewportWidth, defaultViewportHeight, 1, false),
			startScreencastAction(),
		)
	}
	if err := chromedp.Run(tabCtx, actions...); err != nil {
		cancel()
		return
	}
	tab.runMu.Lock()
	_ = s.refreshMetadataLocked(tab)
	tab.runMu.Unlock()
	s.mu.Lock()
	if s.rootCtx == nil {
		s.mu.Unlock()
		cancel()
		return
	}
	s.tabs[id] = tab
	s.targets[info.TargetID] = id
	s.order = append(s.order, id)
	s.activeTabID = id
	s.mu.Unlock()
	_ = s.bringTabToFront(tab)
}

func startScreencastAction() *page.StartScreencastParams {
	return page.StartScreencast().
		WithFormat(page.ScreencastFormatJpeg).
		WithQuality(screencastQuality).
		WithMaxWidth(1920).
		WithMaxHeight(1200).
		WithEveryNthFrame(1)
}

func acknowledgeScreencastFrame(tab *tabSession, sessionID int64) {
	ctx, cancel := context.WithTimeout(tab.ctx, 2*time.Second)
	defer cancel()
	_ = chromedp.Run(ctx, page.ScreencastFrameAck(sessionID))
}

func (s *Service) DownloadPath(downloadID string) (string, error) {
	s.mu.RLock()
	item, ok := s.downloads[downloadID]
	s.mu.RUnlock()
	if !ok || item.Path == "" {
		return "", fmt.Errorf("下载文件尚不可用")
	}
	path := filepath.Clean(item.Path)
	relative, err := filepath.Rel(s.downloadsDir, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("下载文件路径无效")
	}
	return path, nil
}

func consoleMessage(event *cdpruntime.EventConsoleAPICalled) string {
	parts := make([]string, 0, len(event.Args))
	for _, argument := range event.Args {
		if argument == nil {
			continue
		}
		value := strings.TrimSpace(string(argument.Value))
		if value == "" {
			value = strings.TrimSpace(argument.Description)
		}
		value = strings.Trim(value, `"`)
		if value != "" {
			parts = append(parts, value)
		}
	}
	if len(parts) == 0 {
		return string(event.Type)
	}
	return strings.Join(parts, " ")
}

func safeDownloadName(name string) string {
	name = filepath.Base(strings.TrimSpace(name))
	name = strings.Map(func(r rune) rune {
		switch r {
		case '<', '>', ':', '"', '/', '\\', '|', '?', '*', '\x00':
			return '_'
		default:
			return r
		}
	}, name)
	if name == "" || name == "." {
		return fmt.Sprintf("download-%d", time.Now().Unix())
	}
	return name
}

func appendCapped[T any](items []T, item T, limit int) []T {
	items = append(items, item)
	if len(items) > limit {
		items = append([]T(nil), items[len(items)-limit:]...)
	}
	return items
}
