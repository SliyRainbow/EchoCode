package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/SliyRainbow/EchoCode/internal/agent"
	"github.com/SliyRainbow/EchoCode/internal/browserengine"
)

func (a *App) GetBrowserState() browserengine.State {
	if a.browser == nil {
		return browserengine.State{LastError: "内置浏览器不可用"}
	}
	return a.browser.State()
}

func (a *App) OpenBrowserURL(targetURL string) (browserengine.State, error) {
	if a.browser == nil {
		return browserengine.State{}, fmt.Errorf("内置浏览器不可用")
	}
	return a.browser.Open(a.browserContext(), targetURL)
}

func (a *App) BrowserActivateTab(tabID string) (browserengine.State, error) {
	return a.browser.Activate(tabID)
}

func (a *App) BrowserCloseTab(tabID string) browserengine.State {
	return a.browser.CloseTab(tabID)
}

func (a *App) BrowserDismissError(tabID string) browserengine.State {
	return a.browser.DismissError(tabID)
}

func (a *App) BrowserNavigate(tabID, targetURL string) error {
	return a.browser.Navigate(a.browserContext(), tabID, targetURL)
}

func (a *App) BrowserBack(tabID string) error {
	return a.browser.Back(a.browserContext(), tabID)
}

func (a *App) BrowserForward(tabID string) error {
	return a.browser.Forward(a.browserContext(), tabID)
}

func (a *App) BrowserReload(tabID string) error {
	return a.browser.Reload(a.browserContext(), tabID)
}

func (a *App) BrowserResize(tabID string, width, height int) error {
	return a.browser.Resize(a.browserContext(), tabID, width, height)
}

func (a *App) BrowserShowNativeSurface(tabID string, x, y, width, height, viewportWidth, viewportHeight float64) (bool, error) {
	return a.browser.ShowNativeSurface(a.browserContext(), tabID, browserengine.NativeSurfaceBounds{
		X:              x,
		Y:              y,
		Width:          width,
		Height:         height,
		ViewportWidth:  viewportWidth,
		ViewportHeight: viewportHeight,
	})
}

func (a *App) BrowserHideNativeSurface() error {
	return a.browser.HideNativeSurface()
}

func (a *App) GetBrowserFrame(tabID string, includeAnnotations bool) (browserengine.Frame, error) {
	return a.browser.CaptureFrame(a.browserContext(), tabID, includeAnnotations)
}

func (a *App) GetBrowserFrameDelta(tabID string, includeAnnotations bool, capturedAt string) (browserengine.Frame, error) {
	frame, err := a.browser.CaptureFrame(a.browserContext(), tabID, includeAnnotations)
	if err != nil {
		return frame, err
	}
	if capturedAt != "" && frame.CapturedAt == capturedAt {
		frame.ImageDataURL = ""
		if !includeAnnotations {
			frame.Elements = nil
		}
	}
	return frame, nil
}

func (a *App) BrowserClick(tabID string, x, y float64, clickCount int) error {
	return a.browser.Click(a.browserContext(), tabID, x, y, clickCount)
}

func (a *App) BrowserScroll(tabID string, deltaX, deltaY float64) error {
	return a.browser.Scroll(a.browserContext(), tabID, deltaX, deltaY)
}

func (a *App) BrowserType(tabID, text string) error {
	return a.browser.Type(a.browserContext(), tabID, text)
}

func (a *App) BrowserKey(tabID, key string, ctrl, alt, shift, meta bool) error {
	return a.browser.Key(a.browserContext(), tabID, key, ctrl, alt, shift, meta)
}

func (a *App) BrowserHandleDialog(tabID string, accept bool, promptText string) error {
	return a.browser.HandleDialog(a.browserContext(), tabID, accept, promptText)
}

func (a *App) GetBrowserInspector(tabID string) (browserengine.Inspector, error) {
	return a.browser.Inspector(a.browserContext(), tabID)
}

func (a *App) BrowserSaveScreenshot(tabID string) (string, error) {
	return a.browser.SaveScreenshot(a.browserContext(), tabID, "")
}

func (a *App) BrowserEvaluate(tabID, expression string) (string, error) {
	result, err := a.browser.Evaluate(a.browserContext(), tabID, expression)
	if err != nil {
		return "", err
	}
	data, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func (a *App) BrowserAutofill(tabID string) (int, error) {
	return a.browser.Autofill(a.browserContext(), tabID)
}

func (a *App) SaveBrowserCredential(credentialID, origin, username, password string) (agent.WorkbenchState, error) {
	state, err := a.service.SaveBrowserCredential(credentialID, origin, username, password)
	if err == nil {
		err = a.configureBrowser(state.RuntimeSettings)
	}
	return state, err
}

func (a *App) DeleteBrowserCredential(credentialID string) (agent.WorkbenchState, error) {
	return a.service.DeleteBrowserCredential(credentialID)
}

func (a *App) BrowserFillCredential(tabID, credentialID string) (int, error) {
	credential, password, err := a.service.BrowserCredentialSecret(credentialID)
	if err != nil {
		return 0, err
	}
	return a.browser.FillCredential(a.browserContext(), tabID, credential.Origin, credential.Username, password)
}

func (a *App) BrowserOpenDownload(downloadID string) error {
	path, err := a.browser.DownloadPath(downloadID)
	if err != nil {
		return err
	}
	return openDesktopFile(path)
}

func (a *App) BrowserRevealDownload(downloadID string) error {
	path, err := a.browser.DownloadPath(downloadID)
	if err != nil {
		return err
	}
	return revealDesktopFile(path)
}

func (a *App) BrowserClearData() (browserengine.State, error) {
	err := a.browser.ClearData(a.browserContext())
	return a.browser.State(), err
}

func (a *App) OpenURLInSystemBrowser(targetURL string) error {
	return openDesktopFile(targetURL)
}

func (a *App) browserContext() context.Context {
	if a.ctx != nil {
		return a.ctx
	}
	return context.Background()
}

func activeBrowserTitle(state browserengine.State) string {
	for _, tab := range state.Tabs {
		if tab.ID == state.ActiveTabID && tab.Title != "" {
			return tab.Title
		}
	}
	return "浏览器"
}
