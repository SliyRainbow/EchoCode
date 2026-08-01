//go:build windows

package browserengine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strconv"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
	"golang.org/x/sys/windows"
)

var (
	testCreateWindowEx = syscall.NewLazyDLL("user32.dll").NewProc("CreateWindowExW")
	testDestroyWindow  = syscall.NewLazyDLL("user32.dll").NewProc("DestroyWindow")
)

// TestEmbeddedWebView2CDP exercises the real WebView2 runtime only when
// explicitly requested. It isolates each run in a temporary user data folder
// and helps catch runtime upgrades that break the private CDP connection.
func TestEmbeddedWebView2CDP(t *testing.T) {
	if os.Getenv("ECHOCODE_WEBVIEW2_INTEGRATION") != "1" {
		t.Skip("set ECHOCODE_WEBVIEW2_INTEGRATION=1 to run the WebView2 integration test")
	}

	hwnd, closeWindow := startWebView2TestWindow(t)
	defer closeWindow()

	port, err := reserveLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	profileDir, err := os.MkdirTemp("", "echocode-webview2-integration-")
	if err != nil {
		t.Fatal(err)
	}
	defer removeWebView2TestProfile(t, profileDir)
	downloadDir := t.TempDir()
	options := embeddedBrowserOptions{
		ProfileDir: profileDir,
		AdditionalBrowserArgs: []string{
			"--disable-crash-reporter",
			"--disable-session-crashed-bubble",
			"--disable-sync",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-blink-features=AutomationControlled",
			"--remote-debugging-address=127.0.0.1",
			"--remote-debugging-port=" + strconv.Itoa(port),
		},
	}

	commands := make(chan embeddedSurfaceCommand)
	stop := make(chan struct{})
	done := make(chan struct{})
	ready := make(chan embeddedSurfaceStartResult, 1)
	go runEmbeddedSurfaceThread(hwnd, port, options, commands, stop, done, ready)
	defer func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}()

	started := <-ready
	if started.err != nil {
		t.Fatalf("create WebView2 controller: %v", started.err)
	}
	rootTargetID, err := waitForDevToolsTarget(
		started.endpoint,
		"about:blank#echocode-browser-bootstrap-"+strconv.Itoa(port),
		8*time.Second,
	)
	if err != nil {
		t.Fatalf("discover WebView2 bootstrap target: %v", err)
	}

	allocatorCtx, allocatorCancel := chromedp.NewRemoteAllocator(context.Background(), started.endpoint)
	defer allocatorCancel()
	rootCtx, rootCancel := chromedp.NewContext(
		allocatorCtx,
		chromedp.WithTargetID(rootTargetID),
	)
	defer rootCancel()
	if err := chromedp.Run(rootCtx,
		network.Enable(),
		page.Enable(),
		target.SetDiscoverTargets(true),
		browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).
			WithDownloadPath(downloadDir).
			WithEventsEnabled(true),
	); err != nil {
		t.Fatalf("WebView2 chromedp initialization: %v", err)
	}
}

func TestEmbeddedWebView2VisibleTabNavigation(t *testing.T) {
	if os.Getenv("ECHOCODE_WEBVIEW2_INTEGRATION") != "1" {
		t.Skip("set ECHOCODE_WEBVIEW2_INTEGRATION=1 to run the WebView2 integration test")
	}

	hwnd, closeWindow := startWebView2TestWindow(t)
	defer closeWindow()
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("<!doctype html><title>EchoCode native navigation probe</title><main>embedded browser probe</main>"))
	}))
	defer server.Close()

	port, err := reserveLoopbackPort()
	if err != nil {
		t.Fatal(err)
	}
	profileDir, err := os.MkdirTemp("", "echocode-webview2-visible-tab-")
	if err != nil {
		t.Fatal(err)
	}
	defer removeWebView2TestProfile(t, profileDir)
	options := embeddedBrowserOptions{
		ProfileDir: profileDir,
		AdditionalBrowserArgs: []string{
			"--disable-crash-reporter",
			"--disable-session-crashed-bubble",
			"--disable-sync",
			"--no-first-run",
			"--no-default-browser-check",
			"--remote-debugging-address=127.0.0.1",
			"--remote-debugging-port=" + strconv.Itoa(port),
		},
	}
	commands := make(chan embeddedSurfaceCommand)
	stop := make(chan struct{})
	done := make(chan struct{})
	ready := make(chan embeddedSurfaceStartResult, 1)
	go runEmbeddedSurfaceThread(hwnd, port, options, commands, stop, done, ready)
	defer func() {
		close(stop)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}()

	started := <-ready
	if started.err != nil {
		t.Fatalf("create WebView2 controller: %v", started.err)
	}
	markerURL := embeddedTabMarkerURL("visible-navigation")
	surface := &windowsNativeBrowserSurface{commands: commands, done: done}
	if err := surface.CreateTab("visible-navigation", markerURL); err != nil {
		t.Fatalf("create visible WebView2 tab: %v", err)
	}
	defer surface.CloseTab("visible-navigation")
	if err := surface.Show(NativeSurfaceBounds{
		X:              40,
		Y:              30,
		Width:          640,
		Height:         420,
		ViewportWidth:  800,
		ViewportHeight: 600,
	}, nativeWindowInsets{}); err != nil {
		t.Fatalf("show visible WebView2 tab: %v", err)
	}

	targetID, err := waitForDevToolsTarget(started.endpoint, markerURL, 8*time.Second)
	if err != nil {
		t.Fatalf("discover visible WebView2 tab: %v", err)
	}
	allocatorCtx, allocatorCancel := chromedp.NewRemoteAllocator(context.Background(), started.endpoint)
	defer allocatorCancel()
	tabCtx, tabCancel := chromedp.NewContext(allocatorCtx, chromedp.WithTargetID(targetID))
	defer tabCancel()
	if err := chromedp.Run(tabCtx, page.Enable()); err != nil {
		t.Fatalf("initialize visible WebView2 tab: %v", err)
	}
	if err := surface.NavigateTab("visible-navigation", server.URL); err != nil {
		t.Fatalf("navigate visible WebView2 tab: %v", err)
	}

	deadline := time.Now().Add(8 * time.Second)
	for {
		var title string
		err = chromedp.Run(tabCtx, chromedp.Title(&title))
		if err == nil && title == "EchoCode native navigation probe" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("visible WebView2 tab did not receive native navigation, title = %q, err = %v", title, err)
		}
		time.Sleep(80 * time.Millisecond)
	}
}

func removeWebView2TestProfile(t *testing.T, profileDir string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	var lastErr error
	for {
		if err := os.RemoveAll(profileDir); err == nil {
			return
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			t.Errorf("remove WebView2 integration profile: %v", lastErr)
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func startWebView2TestWindow(t *testing.T) (uintptr, func()) {
	t.Helper()
	type result struct {
		hwnd uintptr
		err  error
	}
	ready := make(chan result, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)

		className, _ := windows.UTF16PtrFromString("STATIC")
		windowName, _ := windows.UTF16PtrFromString("EchoCode WebView2 integration test")
		hwnd, _, callErr := testCreateWindowEx.Call(
			0,
			uintptr(unsafe.Pointer(className)),
			uintptr(unsafe.Pointer(windowName)),
			0x00CF0000,
			0,
			0,
			800,
			600,
			0,
			0,
			0,
			0,
		)
		if hwnd == 0 {
			ready <- result{err: callErr}
			return
		}
		ready <- result{hwnd: hwnd}
		ticker := time.NewTicker(8 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				pumpWindowMessages()
			case <-stop:
				testDestroyWindow.Call(hwnd)
				pumpWindowMessages()
				return
			}
		}
	}()
	started := <-ready
	if started.err != nil || started.hwnd == 0 {
		t.Fatalf("create WebView2 test window: %v", started.err)
	}
	return started.hwnd, func() {
		close(stop)
		<-done
	}
}
