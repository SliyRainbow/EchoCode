//go:build windows

package browserengine

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/chromedp/cdproto/target"
	"github.com/wailsapp/go-webview2/pkg/edge"
	"golang.org/x/sys/windows"
)

const (
	pmRemove       = 0x0001
	wsChild        = 0x40000000
	wsClipSiblings = 0x04000000
	wsClipChildren = 0x02000000
	swpNoActivate  = 0x0010
	swpShowWindow  = 0x0040
	swHide         = 0

	embeddedSurfaceCommandTimeout = 15 * time.Second
)

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	procEnumWindows        = user32.NewProc("EnumWindows")
	procGetWindowThreadPID = user32.NewProc("GetWindowThreadProcessId")
	procGetClassName       = user32.NewProc("GetClassNameW")
	procIsWindowVisible    = user32.NewProc("IsWindowVisible")
	procGetWindowRect      = user32.NewProc("GetWindowRect")
	procGetClientRect      = user32.NewProc("GetClientRect")
	procPeekMessage        = user32.NewProc("PeekMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessage    = user32.NewProc("DispatchMessageW")
	procCreateWindowEx     = user32.NewProc("CreateWindowExW")
	procDestroyWindow      = user32.NewProc("DestroyWindow")
	procSetWindowPos       = user32.NewProc("SetWindowPos")
	procShowWindow         = user32.NewProc("ShowWindow")
)

type nativeRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type nativePoint struct {
	X int32
	Y int32
}

type nativeMessage struct {
	Window  uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	Point   nativePoint
	Private uint32
}

type embeddedSurfaceCommand struct {
	run    func(*embeddedSurfaceThread) error
	result chan error
}

type embeddedBrowserTab struct {
	hostWindow uintptr
	view       *edge.Chromium
}

type embeddedSurfaceThread struct {
	mainWindow  uintptr
	options     embeddedBrowserOptions
	bootstrap   *edge.Chromium
	tabs        map[string]*embeddedBrowserTab
	activeTabID string
	visible     bool
	bounds      NativeSurfaceBounds
}

type embeddedSurfaceStartResult struct {
	endpoint string
	err      error
}

type windowsNativeBrowserSurface struct {
	mu       sync.Mutex
	commands chan embeddedSurfaceCommand
	stop     chan struct{}
	done     chan struct{}
}

func newNativeBrowserSurface() nativeBrowserSurface { return &windowsNativeBrowserSurface{} }

func (*windowsNativeBrowserSurface) Supported() bool { return true }

// Attach remains on the common interface for the non-Windows compatibility
// renderer. WebView2 is already a child of the EchoCode window and needs no
// external process window to be attached.
func (*windowsNativeBrowserSurface) Attach(int) error { return nil }

func (s *windowsNativeBrowserSurface) Start(options embeddedBrowserOptions) (embeddedBrowserStart, error) {
	s.Close()
	mainWindow, err := waitForNativeWindow(uint32(os.Getpid()), isWailsWindow, 5*time.Second)
	if err != nil {
		return embeddedBrowserStart{}, fmt.Errorf("locate EchoCode window: %w", err)
	}
	port, err := reserveLoopbackPort()
	if err != nil {
		return embeddedBrowserStart{}, fmt.Errorf("reserve WebView2 debugging port: %w", err)
	}
	options.AdditionalBrowserArgs = append(
		append([]string(nil), options.AdditionalBrowserArgs...),
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port="+strconv.Itoa(port),
	)

	commands := make(chan embeddedSurfaceCommand)
	stop := make(chan struct{})
	done := make(chan struct{})
	ready := make(chan embeddedSurfaceStartResult, 1)
	s.mu.Lock()
	s.commands = commands
	s.stop = stop
	s.done = done
	s.mu.Unlock()

	go runEmbeddedSurfaceThread(mainWindow, port, options, commands, stop, done, ready)

	var started embeddedSurfaceStartResult
	select {
	case started = <-ready:
	case <-time.After(embeddedSurfaceCommandTimeout):
		return embeddedBrowserStart{}, errors.New("WebView2 initialization timed out")
	}
	if started.err != nil {
		s.Close()
		return embeddedBrowserStart{}, started.err
	}
	rootTargetID, err := waitForDevToolsTarget(
		started.endpoint,
		"about:blank#echocode-browser-bootstrap-"+strconv.Itoa(port),
		8*time.Second,
	)
	if err != nil {
		s.Close()
		return embeddedBrowserStart{}, err
	}
	return embeddedBrowserStart{Endpoint: started.endpoint, RootTargetID: rootTargetID}, nil
}

func (s *windowsNativeBrowserSurface) CreateTab(tabID, markerURL string) error {
	if strings.TrimSpace(tabID) == "" {
		return errors.New("WebView2 tab ID is empty")
	}
	return s.dispatch(embeddedSurfaceCommandTimeout, func(state *embeddedSurfaceThread) error {
		if _, exists := state.tabs[tabID]; exists {
			return nil
		}
		hostWindow, err := createEmbeddedTabHost(state.mainWindow)
		if err != nil {
			return err
		}
		view, err := createEmbeddedChromium(hostWindow, state.options)
		if err != nil {
			destroyEmbeddedTabHost(hostWindow)
			return err
		}
		view.Navigate(markerURL)
		tab := &embeddedBrowserTab{hostWindow: hostWindow, view: view}
		state.tabs[tabID] = tab
		if state.activeTabID == "" {
			state.activeTabID = tabID
		}
		if state.visible && state.activeTabID == tabID {
			return showEmbeddedTab(state, tabID)
		}
		return hideEmbeddedTab(tab)
	})
}

func (s *windowsNativeBrowserSurface) NavigateTab(tabID, targetURL string) error {
	if strings.TrimSpace(tabID) == "" {
		return errors.New("WebView2 tab ID is empty")
	}
	return s.dispatch(embeddedSurfaceCommandTimeout, func(state *embeddedSurfaceThread) error {
		tab := state.tabs[tabID]
		if tab == nil || tab.view == nil {
			return fmt.Errorf("WebView2 tab %q does not exist", tabID)
		}
		tab.view.Navigate(targetURL)
		return nil
	})
}

func (s *windowsNativeBrowserSurface) ActivateTab(tabID string) error {
	return s.dispatch(5*time.Second, func(state *embeddedSurfaceThread) error {
		if _, exists := state.tabs[tabID]; !exists {
			return fmt.Errorf("WebView2 tab %q does not exist", tabID)
		}
		state.activeTabID = tabID
		for id, tab := range state.tabs {
			if id != tabID {
				_ = hideEmbeddedTab(tab)
			}
		}
		if !state.visible {
			return nil
		}
		return showEmbeddedTab(state, tabID)
	})
}

func (s *windowsNativeBrowserSurface) CloseTab(tabID string) {
	_ = s.dispatch(5*time.Second, func(state *embeddedSurfaceThread) error {
		tab := state.tabs[tabID]
		if tab == nil {
			return nil
		}
		delete(state.tabs, tabID)
		if state.activeTabID == tabID {
			state.activeTabID = ""
			for id := range state.tabs {
				state.activeTabID = id
				break
			}
		}
		closeEmbeddedTab(tab)
		if state.visible && state.activeTabID != "" {
			return showEmbeddedTab(state, state.activeTabID)
		}
		return nil
	})
}

func (s *windowsNativeBrowserSurface) Show(bounds NativeSurfaceBounds, _ nativeWindowInsets) error {
	if err := validateNativeSurfaceBounds(bounds); err != nil {
		return err
	}
	return s.dispatch(5*time.Second, func(state *embeddedSurfaceThread) error {
		state.visible = true
		state.bounds = bounds
		if state.activeTabID == "" {
			return nil
		}
		return showEmbeddedTab(state, state.activeTabID)
	})
}

func (s *windowsNativeBrowserSurface) Hide() error {
	return s.dispatch(5*time.Second, func(state *embeddedSurfaceThread) error {
		state.visible = false
		for _, tab := range state.tabs {
			_ = hideEmbeddedTab(tab)
		}
		return nil
	})
}

func (s *windowsNativeBrowserSurface) Close() {
	s.mu.Lock()
	stop := s.stop
	done := s.done
	s.commands = nil
	s.stop = nil
	s.done = nil
	s.mu.Unlock()
	if stop == nil {
		return
	}
	close(stop)
	if done != nil {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
}

func (s *windowsNativeBrowserSurface) dispatch(timeout time.Duration, run func(*embeddedSurfaceThread) error) error {
	s.mu.Lock()
	commands := s.commands
	done := s.done
	s.mu.Unlock()
	if commands == nil || done == nil {
		return errors.New("WebView2 surface is not running")
	}
	command := embeddedSurfaceCommand{run: run, result: make(chan error, 1)}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case commands <- command:
	case <-done:
		return errors.New("WebView2 surface stopped")
	case <-timer.C:
		return errors.New("WebView2 surface did not accept the operation")
	}
	select {
	case err := <-command.result:
		return err
	case <-done:
		return errors.New("WebView2 surface stopped")
	case <-timer.C:
		return errors.New("WebView2 surface operation timed out")
	}
}

func runEmbeddedSurfaceThread(
	mainWindow uintptr,
	port int,
	options embeddedBrowserOptions,
	commands <-chan embeddedSurfaceCommand,
	stop <-chan struct{},
	done chan<- struct{},
	ready chan<- embeddedSurfaceStartResult,
) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	defer close(done)

	coInitialized := false
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED); err == nil {
		coInitialized = true
	} else if !errors.Is(err, windows.Errno(windows.RPC_E_CHANGED_MODE)) {
		ready <- embeddedSurfaceStartResult{err: fmt.Errorf("initialize WebView2 COM apartment: %w", err)}
		return
	}
	if coInitialized {
		defer windows.CoUninitialize()
	}

	bootstrap, err := createEmbeddedChromium(mainWindow, options)
	if err != nil {
		ready <- embeddedSurfaceStartResult{err: err}
		return
	}
	bootstrap.Navigate("about:blank#echocode-browser-bootstrap-" + strconv.Itoa(port))
	_ = bootstrap.Hide()
	state := &embeddedSurfaceThread{
		mainWindow: mainWindow,
		options:    options,
		bootstrap:  bootstrap,
		tabs:       map[string]*embeddedBrowserTab{},
	}
	ready <- embeddedSurfaceStartResult{endpoint: "http://127.0.0.1:" + strconv.Itoa(port)}

	ticker := time.NewTicker(8 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case command := <-commands:
			command.result <- command.run(state)
			pumpWindowMessages()
		case <-ticker.C:
			pumpWindowMessages()
		case <-stop:
			for _, tab := range state.tabs {
				closeEmbeddedTab(tab)
			}
			closeEmbeddedChromium(state.bootstrap)
			pumpWindowMessages()
			return
		}
	}
}

func createEmbeddedChromium(mainWindow uintptr, options embeddedBrowserOptions) (*edge.Chromium, error) {
	view := edge.NewChromium()
	view.DataPath = options.ProfileDir
	view.AdditionalBrowserArgs = append([]string(nil), options.AdditionalBrowserArgs...)
	var callbackErr error
	view.SetErrorCallback(func(err error) {
		if callbackErr == nil {
			callbackErr = err
		}
	})
	if !view.Embed(mainWindow) {
		err := view.LastError()
		closeEmbeddedChromium(view)
		if err != nil {
			return nil, err
		}
		return nil, errors.New("WebView2 controller initialization failed")
	}
	if callbackErr != nil {
		closeEmbeddedChromium(view)
		return nil, callbackErr
	}
	settings, err := view.GetSettings()
	if err != nil {
		closeEmbeddedChromium(view)
		return nil, fmt.Errorf("read WebView2 settings: %w", err)
	}
	if err := settings.PutAreDevToolsEnabled(options.DeveloperToolsEnabled); err != nil {
		closeEmbeddedChromium(view)
		return nil, fmt.Errorf("configure WebView2 developer tools: %w", err)
	}
	_ = view.PutIsPasswordAutosaveEnabled(options.PasswordManagerEnabled)
	_ = view.PutIsGeneralAutofillEnabled(options.AutofillContactEnabled)
	view.SetBackgroundColour(255, 255, 255, 255)
	if callbackErr != nil {
		closeEmbeddedChromium(view)
		return nil, callbackErr
	}
	if err := view.Hide(); err != nil {
		closeEmbeddedChromium(view)
		return nil, fmt.Errorf("hide new WebView2 tab: %w", err)
	}
	return view, nil
}

func showEmbeddedTab(state *embeddedSurfaceThread, tabID string) error {
	tab := state.tabs[tabID]
	if tab == nil || tab.view == nil || tab.hostWindow == 0 {
		return fmt.Errorf("WebView2 tab %q does not exist", tabID)
	}
	bounds, rasterizationScale, err := calculateEmbeddedSurfaceMetrics(state.mainWindow, state.bounds)
	if err != nil {
		return err
	}
	if err := placeEmbeddedTabHost(tab.hostWindow, bounds); err != nil {
		return err
	}
	if controller := tab.view.GetController(); controller != nil {
		if controller3 := controller.GetICoreWebView2Controller3(); controller3 != nil {
			defer controller3.Release()
			if err := controller3.PutRasterizationScale(rasterizationScale); err != nil {
				return fmt.Errorf("set WebView2 rasterization scale: %w", err)
			}
		}
	}
	viewBounds := edge.Rect{Right: bounds.Right - bounds.Left, Bottom: bounds.Bottom - bounds.Top}
	tab.view.ResizeWithBounds(&viewBounds)
	_ = tab.view.NotifyParentWindowPositionChanged()
	return tab.view.Show()
}

func hideEmbeddedTab(tab *embeddedBrowserTab) error {
	if tab == nil {
		return nil
	}
	var hideErr error
	if tab.view != nil {
		hideErr = tab.view.Hide()
	}
	if tab.hostWindow != 0 {
		procShowWindow.Call(tab.hostWindow, swHide)
	}
	return hideErr
}

func closeEmbeddedTab(tab *embeddedBrowserTab) {
	if tab == nil {
		return
	}
	closeEmbeddedChromium(tab.view)
	destroyEmbeddedTabHost(tab.hostWindow)
}

func createEmbeddedTabHost(parent uintptr) (uintptr, error) {
	className, err := windows.UTF16PtrFromString("STATIC")
	if err != nil {
		return 0, err
	}
	hostWindow, _, callErr := procCreateWindowEx.Call(
		0,
		uintptr(unsafe.Pointer(className)),
		0,
		uintptr(wsChild|wsClipSiblings|wsClipChildren),
		0,
		0,
		1,
		1,
		parent,
		0,
		0,
		0,
	)
	if hostWindow == 0 {
		return 0, fmt.Errorf("create embedded browser host window: %w", callErr)
	}
	return hostWindow, nil
}

func placeEmbeddedTabHost(hostWindow uintptr, bounds edge.Rect) error {
	width := max(int32(1), bounds.Right-bounds.Left)
	height := max(int32(1), bounds.Bottom-bounds.Top)
	result, _, callErr := procSetWindowPos.Call(
		hostWindow,
		0,
		uintptr(bounds.Left),
		uintptr(bounds.Top),
		uintptr(width),
		uintptr(height),
		uintptr(swpNoActivate|swpShowWindow),
	)
	if result == 0 {
		return fmt.Errorf("place embedded browser host window: %w", callErr)
	}
	return nil
}

func destroyEmbeddedTabHost(hostWindow uintptr) {
	if hostWindow != 0 {
		procDestroyWindow.Call(hostWindow)
	}
}

func calculateEmbeddedSurfaceMetrics(mainWindow uintptr, bounds NativeSurfaceBounds) (edge.Rect, float64, error) {
	var client nativeRect
	if result, _, callErr := procGetClientRect.Call(mainWindow, uintptr(unsafe.Pointer(&client))); result == 0 {
		return edge.Rect{}, 0, fmt.Errorf("read EchoCode client bounds: %w", callErr)
	}
	return scaleEmbeddedSurfaceBounds(client, bounds)
}

func scaleEmbeddedSurfaceBounds(client nativeRect, bounds NativeSurfaceBounds) (edge.Rect, float64, error) {
	if bounds.ViewportWidth < 1 || bounds.ViewportHeight < 1 {
		return edge.Rect{}, 0, errors.New("EchoCode viewport size is invalid")
	}
	clientWidth := float64(client.Right - client.Left)
	clientHeight := float64(client.Bottom - client.Top)
	scaleX := clientWidth / bounds.ViewportWidth
	scaleY := clientHeight / bounds.ViewportHeight
	if scaleX <= 0 || scaleY <= 0 {
		return edge.Rect{}, 0, errors.New("EchoCode window scale is invalid")
	}
	left := int32(math.Round(bounds.X * scaleX))
	top := int32(math.Round(bounds.Y * scaleY))
	right := left + int32(math.Round(bounds.Width*scaleX))
	bottom := top + int32(math.Round(bounds.Height*scaleY))
	left = max(int32(0), left)
	top = max(int32(0), top)
	right = min(client.Right, max(left+1, right))
	bottom = min(client.Bottom, max(top+1, bottom))
	rasterizationScale := math.Max(0.25, math.Min(8, (scaleX+scaleY)/2))
	return edge.Rect{Left: left, Top: top, Right: right, Bottom: bottom}, rasterizationScale, nil
}

func reserveLoopbackPort() (int, error) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		return 0, err
	}
	return port, nil
}

func waitForDevToolsTarget(endpoint, markerURL string, timeout time.Duration) (target.ID, error) {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 600 * time.Millisecond}
	targetsURL := strings.TrimRight(endpoint, "/") + "/json/list"
	lastErr := errors.New("WebView2 target list is empty")
	for time.Now().Before(deadline) {
		response, err := client.Get(targetsURL)
		if err == nil {
			body, readErr := io.ReadAll(io.LimitReader(response.Body, 256*1024))
			_ = response.Body.Close()
			if readErr == nil && response.StatusCode == http.StatusOK {
				if targetID, findErr := findDevToolsTarget(body, markerURL); findErr == nil {
					return targetID, nil
				} else {
					lastErr = findErr
				}
			} else if readErr != nil {
				lastErr = readErr
			} else {
				lastErr = fmt.Errorf("HTTP %d", response.StatusCode)
			}
		} else {
			lastErr = err
		}
		time.Sleep(80 * time.Millisecond)
	}
	return "", fmt.Errorf("WebView2 bootstrap target did not become ready: %w", lastErr)
}

func findDevToolsTarget(payload []byte, markerURL string) (target.ID, error) {
	var targets []struct {
		ID   target.ID `json:"id"`
		Type string    `json:"type"`
		URL  string    `json:"url"`
	}
	if err := json.Unmarshal(payload, &targets); err != nil {
		return "", fmt.Errorf("decode WebView2 targets: %w", err)
	}
	for _, candidate := range targets {
		if candidate.ID != "" && candidate.Type == "page" && candidate.URL == markerURL {
			return candidate.ID, nil
		}
	}
	return "", fmt.Errorf("WebView2 bootstrap target %q was not found", markerURL)
}

func pumpWindowMessages() {
	var message nativeMessage
	for {
		result, _, _ := procPeekMessage.Call(uintptr(unsafe.Pointer(&message)), 0, 0, 0, pmRemove)
		if result == 0 {
			return
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&message)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&message)))
	}
}

func closeEmbeddedChromium(view *edge.Chromium) {
	if view == nil {
		return
	}
	_ = view.Close()
}

func waitForNativeWindow(processID uint32, accept func(uintptr, string) bool, timeout time.Duration) (uintptr, error) {
	deadline := time.Now().Add(timeout)
	for {
		if window := findNativeWindow(processID, accept); window != 0 {
			return window, nil
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("no window found for process %d", processID)
		}
		time.Sleep(80 * time.Millisecond)
	}
}

func findNativeWindow(processID uint32, accept func(uintptr, string) bool) uintptr {
	var best uintptr
	var bestArea int64
	callback := windows.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		var candidatePID uint32
		procGetWindowThreadPID.Call(hwnd, uintptr(unsafe.Pointer(&candidatePID)))
		if candidatePID != processID {
			return 1
		}
		className := nativeWindowClass(hwnd)
		if !accept(hwnd, className) {
			return 1
		}
		var rect nativeRect
		if result, _, _ := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); result == 0 {
			return 1
		}
		area := int64(rect.Right-rect.Left) * int64(rect.Bottom-rect.Top)
		if area > bestArea {
			best = hwnd
			bestArea = area
		}
		return 1
	})
	procEnumWindows.Call(callback, 0)
	return best
}

func isWailsWindow(hwnd uintptr, className string) bool {
	return strings.EqualFold(className, "wailsWindow") && nativeIsWindowVisible(hwnd)
}

func nativeWindowClass(hwnd uintptr) string {
	buffer := make([]uint16, 128)
	length, _, _ := procGetClassName.Call(hwnd, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)))
	if length == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer[:length])
}

func nativeIsWindowVisible(hwnd uintptr) bool {
	result, _, _ := procIsWindowVisible.Call(hwnd)
	return result != 0
}
