package browserengine

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	goruntime "runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/browser"
	"github.com/chromedp/cdproto/emulation"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	cdpruntime "github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

type pendingRequest struct {
	method string
	url    string
	typeID string
}

const embeddedBrowserExecutable = "echocode-webview2"

type tabSession struct {
	mu                   sync.RWMutex
	runMu                sync.Mutex
	ctx                  context.Context
	cancel               context.CancelFunc
	state                Tab
	console              []ConsoleEntry
	network              []NetworkEntry
	requests             map[network.RequestID]pendingRequest
	targetID             target.ID
	frameData            string
	frameCapturedAt      string
	frameElements        []Element
	frameDetailsAt       time.Time
	frameElementsAt      time.Time
	frameDetailsBusy     bool
	frameElementsPending bool
	isRoot               bool
}

type Service struct {
	mu                   sync.RWMutex
	startMu              sync.Mutex
	settings             Settings
	profileDir           string
	activeProfileDir     string
	downloadsDir         string
	executable           string
	executables          []string
	temporaryProfileDir  string
	allocatorCtx         context.Context
	allocatorCancel      context.CancelFunc
	rootCtx              context.Context
	rootCancel           context.CancelFunc
	tabs                 map[string]*tabSession
	targets              map[target.ID]string
	order                []string
	activeTabID          string
	downloads            map[string]Download
	lastError            string
	nativeSurface        nativeBrowserSurface
	nativeReady          bool
	nativeInsets         nativeWindowInsets
	nativeInsetsMeasured bool
	rootTargetID         target.ID
	rootTargetClaimed    bool
}

func New(profileDir, downloadsDir string) *Service {
	nativeSurface := newNativeBrowserSurface()
	executables := FindExecutables()
	executable := ""
	if _, embedded := nativeSurface.(embeddedNativeBrowserSurface); embedded {
		executable = embeddedBrowserExecutable
	} else if len(executables) > 0 {
		executable = executables[0]
	}
	return &Service{
		profileDir:    filepath.Clean(profileDir),
		downloadsDir:  filepath.Clean(downloadsDir),
		executable:    executable,
		executables:   executables,
		tabs:          map[string]*tabSession{},
		targets:       map[target.ID]string{},
		downloads:     map[string]Download{},
		nativeSurface: nativeSurface,
	}
}

func (s *Service) Configure(settings Settings) error {
	settings = normalizeSettings(settings)
	s.mu.Lock()
	previous := s.settings
	s.settings = settings
	running := s.rootCtx != nil
	s.mu.Unlock()

	if !settings.Enabled {
		return s.Stop(context.Background())
	}
	if running && (previous.PasswordManagerEnabled != settings.PasswordManagerEnabled ||
		previous.AutofillContactEnabled != settings.AutofillContactEnabled ||
		previous.NativePresentation != settings.NativePresentation) {
		return s.Stop(context.Background())
	}
	if running {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return s.applyPermissions(ctx)
	}
	return nil
}

func (s *Service) State() State {
	s.mu.RLock()
	state := State{
		Available:   s.executable != "",
		Running:     s.rootCtx != nil,
		Engine:      browserEngineName(s.executable),
		RenderMode:  s.renderModeLocked(),
		ActiveTabID: s.activeTabID,
		Tabs:        make([]Tab, 0, len(s.order)),
		Downloads:   make([]Download, 0, len(s.downloads)),
		LastError:   s.lastError,
		CDPEnabled:  s.settings.DeveloperCDPAccess,
	}
	order := append([]string(nil), s.order...)
	tabs := make(map[string]*tabSession, len(s.tabs))
	for id, tab := range s.tabs {
		tabs[id] = tab
	}
	for _, item := range s.downloads {
		state.Downloads = append(state.Downloads, item)
	}
	s.mu.RUnlock()

	for _, id := range order {
		if tab := tabs[id]; tab != nil {
			tab.mu.RLock()
			state.Tabs = append(state.Tabs, tab.state)
			tab.mu.RUnlock()
		}
	}
	sort.Slice(state.Downloads, func(i, j int) bool {
		return state.Downloads[i].StartedAt > state.Downloads[j].StartedAt
	})
	return state
}

func (s *Service) Open(ctx context.Context, rawURL string) (State, error) {
	targetURL, err := normalizeAddress(rawURL)
	if err != nil {
		return s.State(), err
	}
	if err := s.validateNavigation(targetURL); err != nil {
		return s.State(), err
	}
	if err := s.ensureStarted(ctx); err != nil {
		return s.State(), err
	}

	id := newID("tab")
	s.mu.Lock()
	rootCtx := s.rootCtx
	embeddedSurface, embedded := s.nativeSurface.(embeddedNativeBrowserSurface)
	embedded = embedded && s.nativeReady
	useRootTarget := !embedded && s.nativeReady && !s.rootTargetClaimed && s.rootTargetID != ""
	if useRootTarget {
		s.rootTargetClaimed = true
	}
	s.mu.Unlock()

	tabCtx := rootCtx
	cancel := func() {}
	embeddedTargetID := target.ID("")
	if embedded {
		markerURL := embeddedTabMarkerURL(id)
		if err := embeddedSurface.CreateTab(id, markerURL); err != nil {
			return s.State(), s.setError(fmt.Errorf("create embedded browser tab: %w", err))
		}
		embeddedTargetID, err = waitForEmbeddedTarget(ctx, rootCtx, markerURL)
		if err != nil {
			embeddedSurface.CloseTab(id)
			return s.State(), s.setError(fmt.Errorf("connect embedded browser tab: %w", err))
		}
		tabCtx, cancel = chromedp.NewContext(rootCtx, chromedp.WithTargetID(embeddedTargetID))
	} else if !useRootTarget {
		tabCtx, cancel = chromedp.NewContext(rootCtx)
	}
	tab := &tabSession{
		ctx:      tabCtx,
		cancel:   cancel,
		targetID: embeddedTargetID,
		requests: map[network.RequestID]pendingRequest{},
		state: Tab{
			ID:             id,
			Title:          "新标签页",
			URL:            "about:blank",
			Loading:        true,
			ViewportWidth:  defaultViewportWidth,
			ViewportHeight: defaultViewportHeight,
		},
		isRoot: useRootTarget,
	}
	s.listenTarget(tab)

	s.mu.Lock()
	s.tabs[id] = tab
	s.order = append(s.order, id)
	s.activeTabID = id
	s.mu.Unlock()

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
	err = chromedp.Run(tabCtx, actions...)
	if err != nil {
		s.removeTab(id)
		return s.State(), s.setError(fmt.Errorf("启动浏览标签页失败: %w", err))
	}
	if chromedpContext := chromedp.FromContext(tabCtx); chromedpContext != nil && chromedpContext.Target != nil {
		tab.targetID = chromedpContext.Target.TargetID
		s.mu.Lock()
		s.targets[tab.targetID] = id
		s.mu.Unlock()
	}

	if err := s.Navigate(ctx, id, targetURL); err != nil {
		// Navigation errors remain visible on the tab so the user can edit the
		// address and retry without losing the browser session.
		return s.State(), nil
	}
	_ = s.bringTabToFront(tab)
	return s.State(), nil
}

func (s *Service) Activate(tabID string) (State, error) {
	s.mu.Lock()
	tab, ok := s.tabs[tabID]
	if !ok {
		s.mu.Unlock()
		return State{}, fmt.Errorf("浏览标签页不存在")
	}
	s.activeTabID = tabID
	state := s.stateLocked()
	s.mu.Unlock()
	_ = s.bringTabToFront(tab)
	return state, nil
}

func (s *Service) CloseTab(tabID string) State {
	s.mu.Lock()
	tab := s.tabs[tabID]
	delete(s.tabs, tabID)
	if tab != nil && tab.targetID != "" {
		delete(s.targets, tab.targetID)
	}
	for index, id := range s.order {
		if id == tabID {
			s.order = append(s.order[:index], s.order[index+1:]...)
			break
		}
	}
	if s.activeTabID == tabID {
		s.activeTabID = ""
		if len(s.order) > 0 {
			s.activeTabID = s.order[len(s.order)-1]
		}
	}
	if tab != nil && tab.isRoot {
		s.rootTargetClaimed = false
	}
	nextTab := s.tabs[s.activeTabID]
	embeddedSurface, embedded := s.nativeSurface.(embeddedNativeBrowserSurface)
	embedded = embedded && s.nativeReady
	s.mu.Unlock()
	if embedded {
		embeddedSurface.CloseTab(tabID)
	}
	if tab != nil {
		if tab.isRoot && !embedded {
			go func() {
				tab.runMu.Lock()
				defer tab.runMu.Unlock()
				ctx, cancel := context.WithTimeout(tab.ctx, 3*time.Second)
				defer cancel()
				_ = chromedp.Run(ctx, chromedp.Navigate("about:blank"))
			}()
		} else {
			tab.cancel()
		}
	}
	if nextTab != nil {
		_ = s.bringTabToFront(nextTab)
	} else {
		_ = s.nativeSurface.Hide()
	}
	return s.State()
}

// DismissError clears browser and tab errors after the user dismisses the
// notice. Future failures can still publish a new error normally.
func (s *Service) DismissError(tabID string) State {
	s.mu.Lock()
	s.lastError = ""
	tab := s.tabs[tabID]
	s.mu.Unlock()
	if tab != nil {
		tab.mu.Lock()
		tab.state.Error = ""
		tab.mu.Unlock()
	}
	return s.State()
}

func (s *Service) Stop(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.Lock()
	tabs := make([]*tabSession, 0, len(s.tabs))
	for _, tab := range s.tabs {
		tabs = append(tabs, tab)
	}
	rootCancel := s.rootCancel
	allocatorCancel := s.allocatorCancel
	temporaryProfileDir := s.temporaryProfileDir
	nativeSurface := s.nativeSurface
	s.tabs = map[string]*tabSession{}
	s.targets = map[target.ID]string{}
	s.order = nil
	s.activeTabID = ""
	s.rootCtx = nil
	s.rootCancel = nil
	s.allocatorCtx = nil
	s.allocatorCancel = nil
	s.temporaryProfileDir = ""
	s.activeProfileDir = ""
	s.nativeReady = false
	s.nativeInsets = nativeWindowInsets{}
	s.nativeInsetsMeasured = false
	s.rootTargetID = ""
	s.rootTargetClaimed = false
	s.mu.Unlock()

	nativeSurface.Close()
	for _, tab := range tabs {
		tab.cancel()
	}
	if rootCancel != nil {
		rootCancel()
	}
	if allocatorCancel != nil {
		allocatorCancel()
	}
	if temporaryProfileDir != "" {
		_ = os.RemoveAll(temporaryProfileDir)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func (s *Service) ClearData(ctx context.Context) error {
	if err := s.Stop(ctx); err != nil && ctx.Err() != nil {
		return err
	}
	if s.profileDir == "" || filepath.Clean(s.profileDir) == "." {
		return fmt.Errorf("浏览器数据目录无效")
	}
	if err := os.RemoveAll(s.profileDir); err != nil {
		return fmt.Errorf("清理浏览器数据失败: %w", err)
	}
	if err := os.MkdirAll(s.profileDir, 0o700); err != nil {
		return fmt.Errorf("重新创建浏览器数据目录失败: %w", err)
	}
	s.mu.Lock()
	s.downloads = map[string]Download{}
	s.lastError = ""
	s.mu.Unlock()
	return nil
}

func (s *Service) ensureStarted(ctx context.Context) error {
	s.startMu.Lock()
	defer s.startMu.Unlock()

	s.mu.RLock()
	if s.rootCtx != nil {
		s.mu.RUnlock()
		return nil
	}
	settings := s.settings
	executables := append([]string(nil), s.executables...)
	s.mu.RUnlock()
	if !settings.Enabled {
		return fmt.Errorf("内置浏览器已在设置中关闭")
	}
	_, hasEmbeddedSurface := s.nativeSurface.(embeddedNativeBrowserSurface)
	if len(executables) == 0 && !(hasEmbeddedSurface && settings.NativePresentation) {
		return s.setError(fmt.Errorf("未找到可用的 Microsoft Edge、Google Chrome 或 Chromium"))
	}
	if err := os.MkdirAll(s.profileDir, 0o700); err != nil {
		return s.setError(fmt.Errorf("创建浏览器数据目录失败: %w", err))
	}
	if err := os.MkdirAll(s.downloadsDir, 0o755); err != nil {
		return s.setError(fmt.Errorf("创建浏览器下载目录失败: %w", err))
	}

	if embeddedSurface, embedded := s.nativeSurface.(embeddedNativeBrowserSurface); embedded && settings.NativePresentation {
		if err := s.startEmbeddedBrowser(ctx, embeddedSurface, settings); err != nil {
			return s.setError(fmt.Errorf("start embedded WebView2 browser: %w", err))
		}
		return nil
	}

	attempts := make([]string, 0, len(executables)*2)
	for index, executable := range executables {
		profileDir := persistentBrowserProfileDir(s.profileDir, executable, index)
		allocatorCtx, allocatorCancel, rootCtx, rootCancel, err := s.startBrowser(executable, profileDir, settings)
		if err == nil {
			err = s.installBrowserRuntime(ctx, executable, profileDir, false, allocatorCtx, allocatorCancel, rootCtx, rootCancel)
			if err == nil {
				return nil
			}
		}
		attempts = append(attempts, formatBrowserLaunchAttempt(executable, err))
	}

	// A stale browser process can keep the persistent profile locked. Retry in a
	// process-scoped profile so the internal browser remains usable and the
	// persistent profile can be recovered on the next clean launch.
	for _, executable := range executables {
		recoveryProfile, recoveryErr := os.MkdirTemp(
			filepath.Dir(s.profileDir),
			filepath.Base(s.profileDir)+"-"+browserExecutableSlug(executable)+"-recovery-",
		)
		if recoveryErr != nil {
			attempts = append(attempts, "recovery profile: "+recoveryErr.Error())
			continue
		}
		allocatorCtx, allocatorCancel, rootCtx, rootCancel, err := s.startBrowser(executable, recoveryProfile, settings)
		if err == nil {
			err = s.installBrowserRuntime(ctx, executable, recoveryProfile, true, allocatorCtx, allocatorCancel, rootCtx, rootCancel)
			if err == nil {
				return nil
			}
		}
		attempts = append(attempts, formatBrowserLaunchAttempt(executable+" (recovery profile)", err))
		_ = os.RemoveAll(recoveryProfile)
	}
	diagnostic := strings.Join(attempts, "; ")
	log.Printf("managed browser startup failed: %s", diagnostic)
	return s.setError(errors.New(browserStartupMessage(diagnostic)))
}

func (s *Service) startEmbeddedBrowser(
	ctx context.Context,
	surface embeddedNativeBrowserSurface,
	settings Settings,
) error {
	started, err := surface.Start(embeddedBrowserOptions{
		ProfileDir: s.profileDir,
		AdditionalBrowserArgs: []string{
			"--disable-crash-reporter",
			"--disable-session-crashed-bubble",
			"--disable-sync",
			"--no-first-run",
			"--no-default-browser-check",
			"--disable-blink-features=AutomationControlled",
		},
		DeveloperToolsEnabled:  settings.DeveloperCDPAccess,
		PasswordManagerEnabled: settings.PasswordManagerEnabled,
		AutofillContactEnabled: settings.AutofillContactEnabled,
	})
	if err != nil {
		return err
	}

	allocatorCtx, allocatorCancel := chromedp.NewRemoteAllocator(context.Background(), started.Endpoint)
	rootCtx, rootCancel := chromedp.NewContext(
		allocatorCtx,
		chromedp.WithTargetID(started.RootTargetID),
		chromedp.WithErrorf(browserProtocolErrorf),
	)
	chromedp.ListenBrowser(rootCtx, s.handleBrowserEvent)
	err = chromedp.Run(rootCtx,
		network.Enable(),
		page.Enable(),
		target.SetDiscoverTargets(true),
		browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).
			WithDownloadPath(s.downloadsDir).
			WithEventsEnabled(true),
	)
	if err != nil {
		rootCancel()
		allocatorCancel()
		surface.Close()
		return fmt.Errorf("connect to embedded WebView2: %w", err)
	}

	rootTargetID := started.RootTargetID
	if details := chromedp.FromContext(rootCtx); details != nil && details.Target != nil {
		rootTargetID = details.Target.TargetID
	}
	s.mu.Lock()
	s.executable = embeddedBrowserExecutable
	s.allocatorCtx = allocatorCtx
	s.allocatorCancel = allocatorCancel
	s.rootCtx = rootCtx
	s.rootCancel = rootCancel
	s.activeProfileDir = s.profileDir
	s.temporaryProfileDir = ""
	s.lastError = ""
	s.nativeReady = true
	s.nativeInsets = nativeWindowInsets{}
	s.nativeInsetsMeasured = true
	s.rootTargetID = rootTargetID
	s.rootTargetClaimed = true
	s.mu.Unlock()

	permissionCtx, permissionCancel := context.WithTimeout(ctx, 5*time.Second)
	defer permissionCancel()
	if err := s.applyPermissions(permissionCtx); err != nil {
		rootCancel()
		allocatorCancel()
		surface.Close()
		s.mu.Lock()
		s.allocatorCtx = nil
		s.allocatorCancel = nil
		s.rootCtx = nil
		s.rootCancel = nil
		s.activeProfileDir = ""
		s.nativeReady = false
		s.nativeInsets = nativeWindowInsets{}
		s.nativeInsetsMeasured = false
		s.rootTargetID = ""
		s.rootTargetClaimed = false
		s.mu.Unlock()
		return err
	}
	return nil
}

func (s *Service) startBrowser(
	executable string,
	profileDir string,
	settings Settings,
) (context.Context, context.CancelFunc, context.Context, context.CancelFunc, error) {
	if err := os.MkdirAll(profileDir, 0o700); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create browser profile: %w", err)
	}
	crashDir := filepath.Join(profileDir, "Crashpad")
	if err := os.MkdirAll(crashDir, 0o700); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("create browser crash directory: %w", err)
	}
	launchLog := &tailLog{limit: 12 * 1024}
	nativePresentation := settings.NativePresentation && s.nativeSurface.Supported()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.ExecPath(executable),
		chromedp.UserDataDir(profileDir),
		chromedp.Flag("headless", !nativePresentation),
		chromedp.Flag("disable-gpu", !nativePresentation),
		chromedp.Flag("disable-crash-reporter", true),
		chromedp.Flag("disable-session-crashed-bubble", true),
		chromedp.Flag("hide-crash-restore-bubble", true),
		chromedp.Flag("noerrdialogs", true),
		chromedp.Flag("crash-dumps-dir", crashDir),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.Flag("disable-sync", true),
		chromedp.Flag("no-first-run", true),
		chromedp.Flag("no-default-browser-check", true),
		chromedp.Env("BREAKPAD_DUMP_LOCATION="+crashDir),
		chromedp.WindowSize(defaultViewportWidth, defaultViewportHeight),
		chromedp.CombinedOutput(launchLog),
	)
	if nativePresentation {
		opts = append(opts, chromedp.Flag("window-position", "-32000,-32000"))
	}
	if !settings.PasswordManagerEnabled {
		opts = append(opts,
			chromedp.Flag("disable-save-password-bubble", true),
			chromedp.Flag("password-store", "basic"),
		)
	}
	if !settings.AutofillContactEnabled {
		opts = append(opts, chromedp.Flag("disable-features", "AutofillServerCommunication,AutofillAddressProfileSavePrompt"))
	}

	allocatorCtx, allocatorCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	rootCtx, rootCancel := chromedp.NewContext(allocatorCtx, chromedp.WithErrorf(browserProtocolErrorf))
	chromedp.ListenBrowser(rootCtx, s.handleBrowserEvent)
	err := chromedp.Run(rootCtx,
		network.Enable(),
		page.Enable(),
		target.SetDiscoverTargets(true),
		browser.SetDownloadBehavior(browser.SetDownloadBehaviorBehaviorAllow).
			WithDownloadPath(s.downloadsDir).
			WithEventsEnabled(true),
	)
	if err != nil {
		rootCancel()
		allocatorCancel()
		detail := strings.TrimSpace(launchLog.String())
		if detail != "" {
			err = fmt.Errorf("%w; browser output: %s", err, detail)
		}
		return nil, nil, nil, nil, err
	}
	return allocatorCtx, allocatorCancel, rootCtx, rootCancel, nil
}

func (s *Service) installBrowserRuntime(
	ctx context.Context,
	executable string,
	profileDir string,
	temporaryProfile bool,
	allocatorCtx context.Context,
	allocatorCancel context.CancelFunc,
	rootCtx context.Context,
	rootCancel context.CancelFunc,
) error {
	nativeReady := false
	rootTargetID := target.ID("")
	s.mu.RLock()
	settings := s.settings
	s.mu.RUnlock()
	details := chromedp.FromContext(rootCtx)
	if details != nil {
		if details.Target != nil {
			rootTargetID = details.Target.TargetID
		}
	}
	if settings.NativePresentation && s.nativeSurface.Supported() {
		var attachErr error
		if details == nil || details.Browser == nil || details.Browser.Process() == nil {
			attachErr = fmt.Errorf("browser process details are unavailable")
		} else {
			attachErr = s.nativeSurface.Attach(details.Browser.Process().Pid)
		}
		if attachErr != nil {
			rootCancel()
			allocatorCancel()
			s.nativeSurface.Close()
			if temporaryProfile {
				_ = os.RemoveAll(profileDir)
			}
			return fmt.Errorf("native browser presentation failed: %w", attachErr)
		}
		nativeReady = true
	}
	s.mu.Lock()
	s.executable = executable
	s.allocatorCtx = allocatorCtx
	s.allocatorCancel = allocatorCancel
	s.rootCtx = rootCtx
	s.rootCancel = rootCancel
	s.activeProfileDir = profileDir
	if temporaryProfile {
		s.temporaryProfileDir = profileDir
	} else {
		s.temporaryProfileDir = ""
	}
	s.lastError = ""
	s.nativeReady = nativeReady
	s.nativeInsets = defaultNativeWindowInsets()
	s.nativeInsetsMeasured = false
	s.rootTargetID = rootTargetID
	s.rootTargetClaimed = false
	s.mu.Unlock()

	permissionCtx, permissionCancel := context.WithTimeout(ctx, 5*time.Second)
	defer permissionCancel()
	if err := s.applyPermissions(permissionCtx); err != nil {
		rootCancel()
		allocatorCancel()
		s.mu.Lock()
		s.allocatorCtx = nil
		s.allocatorCancel = nil
		s.rootCtx = nil
		s.rootCancel = nil
		s.activeProfileDir = ""
		s.nativeReady = false
		s.nativeInsets = nativeWindowInsets{}
		s.nativeInsetsMeasured = false
		s.rootTargetID = ""
		s.rootTargetClaimed = false
		temporaryProfileDir := s.temporaryProfileDir
		s.temporaryProfileDir = ""
		s.mu.Unlock()
		if temporaryProfileDir != "" {
			_ = os.RemoveAll(temporaryProfileDir)
		}
		s.nativeSurface.Close()
		return err
	}
	return nil
}

func formatBrowserLaunchAttempt(executable string, err error) string {
	detail := strings.TrimSpace(err.Error())
	if len(detail) > 1200 {
		detail = detail[len(detail)-1200:]
	}
	return executable + ": " + detail
}

func persistentBrowserProfileDir(root, executable string, index int) string {
	root = filepath.Clean(root)
	if index == 0 {
		return root
	}
	return filepath.Join(filepath.Dir(root), filepath.Base(root)+"-"+browserExecutableSlug(executable))
}

func browserExecutableSlug(executable string) string {
	name := strings.ToLower(filepath.Base(executable))
	switch {
	case strings.Contains(name, "edge"):
		return "edge"
	case strings.Contains(name, "chromium"):
		return "chromium"
	case strings.Contains(name, "chrome"):
		return "chrome"
	default:
		name = strings.TrimSuffix(name, filepath.Ext(name))
		name = strings.Map(func(r rune) rune {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				return r
			}
			return '-'
		}, name)
		name = strings.Trim(name, "-")
		if name == "" {
			return "browser"
		}
		return name
	}
}

func browserStartupMessage(diagnostic string) string {
	base := "内置浏览器启动失败。EchoCode 已尝试备用浏览器和隔离配置目录。"
	lower := strings.ToLower(diagnostic)
	switch {
	case strings.Contains(lower, "crashpad") || strings.Contains(lower, "settings version is not 1"):
		return base + " 检测到浏览器 Crashpad 配置异常；请重启 EchoCode 后重试，仍失败时重启 Windows。"
	case strings.Contains(lower, "user data directory is already in use") || strings.Contains(lower, "profile") && strings.Contains(lower, "lock"):
		return base + " 浏览器配置目录正在被其他进程占用；请关闭其他 EchoCode 实例后重试。"
	default:
		return base + " 请重启 EchoCode 后重试；详细诊断已写入应用日志。"
	}
}

type tailLog struct {
	mu    sync.Mutex
	data  []byte
	limit int
}

func (w *tailLog) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.data = append(w.data, data...)
	if w.limit > 0 && len(w.data) > w.limit {
		w.data = append([]byte(nil), w.data[len(w.data)-w.limit:]...)
	}
	return len(data), nil
}

func (w *tailLog) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return string(append([]byte(nil), w.data...))
}

func (s *Service) applyPermissions(ctx context.Context) error {
	s.mu.RLock()
	rootCtx := s.rootCtx
	permissions := append([]SitePermission(nil), s.settings.SitePermissions...)
	s.mu.RUnlock()
	if rootCtx == nil {
		return nil
	}
	runCtx, cancel := context.WithTimeout(rootCtx, 5*time.Second)
	defer cancel()
	actions := []chromedp.Action{
		chromedp.ActionFunc(func(ctx context.Context) error { return browser.ResetPermissions().Do(ctx) }),
	}
	for _, permission := range permissions {
		for _, item := range []struct {
			name  string
			value string
		}{
			{name: "camera", value: permission.Camera},
			{name: "microphone", value: permission.Microphone},
			{name: "clipboard-read", value: permission.Clipboard},
			{name: "clipboard-write", value: permission.Clipboard},
		} {
			setting, ok := permissionSetting(item.value)
			if !ok {
				continue
			}
			origin := permission.Origin
			name := item.name
			actions = append(actions, chromedp.ActionFunc(func(ctx context.Context) error {
				return browser.SetPermission(&browser.PermissionDescriptor{Name: name}, setting).WithOrigin(origin).Do(ctx)
			}))
		}
	}
	if err := chromedp.Run(runCtx, actions...); err != nil {
		return s.setError(fmt.Errorf("应用网站权限失败: %w", err))
	}
	return nil
}

func (s *Service) tab(tabID string) (*tabSession, error) {
	s.mu.RLock()
	if tabID == "" {
		tabID = s.activeTabID
	}
	tab := s.tabs[tabID]
	s.mu.RUnlock()
	if tab == nil {
		return nil, fmt.Errorf("浏览标签页不存在")
	}
	return tab, nil
}

func (s *Service) removeTab(tabID string) {
	s.CloseTab(tabID)
}

func (s *Service) stateLocked() State {
	state := State{
		Available:   s.executable != "",
		Running:     s.rootCtx != nil,
		Engine:      browserEngineName(s.executable),
		RenderMode:  s.renderModeLocked(),
		ActiveTabID: s.activeTabID,
		Tabs:        make([]Tab, 0, len(s.order)),
		Downloads:   make([]Download, 0, len(s.downloads)),
		LastError:   s.lastError,
		CDPEnabled:  s.settings.DeveloperCDPAccess,
	}
	for _, id := range s.order {
		if tab := s.tabs[id]; tab != nil {
			tab.mu.RLock()
			state.Tabs = append(state.Tabs, tab.state)
			tab.mu.RUnlock()
		}
	}
	for _, item := range s.downloads {
		state.Downloads = append(state.Downloads, item)
	}
	return state
}

func (s *Service) renderModeLocked() string {
	if s.nativeReady {
		return "native"
	}
	return "stream"
}

func defaultNativeWindowInsets() nativeWindowInsets {
	return nativeWindowInsets{Left: 8, Top: 80, Right: 8, Bottom: 8}
}

func (s *Service) ShowNativeSurface(ctx context.Context, tabID string, bounds NativeSurfaceBounds) (bool, error) {
	if err := validateNativeSurfaceBounds(bounds); err != nil {
		return false, err
	}
	tab, err := s.tab(tabID)
	if err != nil {
		return false, err
	}
	s.mu.RLock()
	ready := s.nativeReady
	insets := s.nativeInsets
	measured := s.nativeInsetsMeasured
	_, embedded := s.nativeSurface.(embeddedNativeBrowserSurface)
	s.mu.RUnlock()
	if !ready {
		return false, nil
	}
	if err := s.bringTabToFront(tab); err != nil {
		return false, err
	}
	if embedded {
		insets = nativeWindowInsets{}
		measured = true
	}
	if !measured {
		if nextInsets, measureErr := measureNativeWindowInsets(ctx, tab); measureErr == nil {
			insets = nextInsets
		}
		s.mu.Lock()
		if s.nativeReady {
			s.nativeInsets = insets
			s.nativeInsetsMeasured = true
		}
		s.mu.Unlock()
	}
	if err := s.nativeSurface.Show(bounds, insets); err != nil {
		return false, err
	}
	tab.mu.Lock()
	tab.state.ViewportWidth = max(1, int(math.Round(bounds.Width)))
	tab.state.ViewportHeight = max(1, int(math.Round(bounds.Height)))
	tab.mu.Unlock()
	return true, nil
}

func (s *Service) HideNativeSurface() error {
	return s.nativeSurface.Hide()
}

func (s *Service) bringTabToFront(tab *tabSession) error {
	if tab == nil {
		return fmt.Errorf("浏览标签页不存在")
	}
	s.mu.RLock()
	embeddedSurface, embedded := s.nativeSurface.(embeddedNativeBrowserSurface)
	embedded = embedded && s.nativeReady
	s.mu.RUnlock()
	if embedded {
		if err := embeddedSurface.ActivateTab(tab.state.ID); err != nil {
			return err
		}
	}
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := context.WithTimeout(tab.ctx, 3*time.Second)
	defer cancel()
	return chromedp.Run(runCtx, chromedp.ActionFunc(func(ctx context.Context) error {
		return page.BringToFront().Do(ctx)
	}))
}

type nativeWindowMetrics struct {
	OuterWidth  float64 `json:"outerWidth"`
	OuterHeight float64 `json:"outerHeight"`
	InnerWidth  float64 `json:"innerWidth"`
	InnerHeight float64 `json:"innerHeight"`
}

func measureNativeWindowInsets(parent context.Context, tab *tabSession) (nativeWindowInsets, error) {
	tab.runMu.Lock()
	defer tab.runMu.Unlock()
	runCtx, cancel := operationContext(parent, tab.ctx, 3*time.Second)
	defer cancel()
	var metrics nativeWindowMetrics
	if err := chromedp.Run(runCtx, chromedp.Evaluate(`({
		outerWidth: window.outerWidth,
		outerHeight: window.outerHeight,
		innerWidth: window.innerWidth,
		innerHeight: window.innerHeight
	})`, &metrics)); err != nil {
		return defaultNativeWindowInsets(), err
	}
	horizontal := math.Max(0, metrics.OuterWidth-metrics.InnerWidth)
	vertical := math.Max(0, metrics.OuterHeight-metrics.InnerHeight)
	if horizontal > 80 || vertical < 24 || vertical > 220 {
		return defaultNativeWindowInsets(), nil
	}
	defaults := defaultNativeWindowInsets()
	left := math.Max(defaults.Left, horizontal/2)
	right := math.Max(defaults.Right, horizontal-horizontal/2)
	bottom := math.Max(defaults.Bottom, math.Min(12, horizontal/2))
	top := math.Max(defaults.Top, vertical-bottom)
	return nativeWindowInsets{Left: left, Top: top, Right: right, Bottom: bottom}, nil
}

func (s *Service) setError(err error) error {
	if err == nil {
		return nil
	}
	s.mu.Lock()
	s.lastError = err.Error()
	s.mu.Unlock()
	return err
}

func (s *Service) clearError() {
	s.mu.Lock()
	s.lastError = ""
	s.mu.Unlock()
}

func operationContext(parent, base context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(base, timeout)
	if parent == nil {
		return ctx, cancel
	}
	stop := context.AfterFunc(parent, cancel)
	return ctx, func() {
		stop()
		cancel()
	}
}

func normalizeSettings(settings Settings) Settings {
	settings.ClearDataPolicy = strings.ToLower(strings.TrimSpace(settings.ClearDataPolicy))
	settings.ScreenshotAnnotations = strings.ToLower(strings.TrimSpace(settings.ScreenshotAnnotations))
	cleaned := make([]SitePermission, 0, len(settings.SitePermissions))
	seen := map[string]bool{}
	for _, permission := range settings.SitePermissions {
		permission.Origin = strings.TrimSpace(permission.Origin)
		if permission.Origin == "" || seen[permission.Origin] {
			continue
		}
		seen[permission.Origin] = true
		cleaned = append(cleaned, permission)
	}
	settings.SitePermissions = cleaned
	return settings
}

func permissionSetting(value string) (browser.PermissionSetting, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "allow":
		return browser.PermissionSettingGranted, true
	case "block":
		return browser.PermissionSettingDenied, true
	default:
		return "", false
	}
}

func FindExecutable() string {
	executables := FindExecutables()
	if len(executables) == 0 {
		return ""
	}
	return executables[0]
}

func FindExecutables() []string {
	candidates := []string{}
	if goruntime.GOOS == "windows" {
		candidates = append(candidates,
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles(x86)"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("ProgramFiles"), "Chromium", "Application", "chrome.exe"),
			filepath.Join(os.Getenv("LOCALAPPDATA"), "Chromium", "Application", "chrome.exe"),
			"msedge.exe",
			"chrome.exe",
			"chromium.exe",
		)
	} else if goruntime.GOOS == "darwin" {
		candidates = append(candidates,
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		)
	} else {
		candidates = append(candidates, "google-chrome", "microsoft-edge", "chromium", "chromium-browser")
	}
	result := make([]string, 0, len(candidates))
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		if path, err := exec.LookPath(candidate); err == nil {
			path = filepath.Clean(path)
			key := path
			if goruntime.GOOS == "windows" {
				key = strings.ToLower(key)
			}
			if !seen[key] {
				seen[key] = true
				result = append(result, path)
			}
		}
	}
	return result
}

func browserEngineName(executable string) string {
	if executable == embeddedBrowserExecutable {
		return "WebView2 embedded"
	}
	name := strings.ToLower(filepath.Base(executable))
	switch {
	case strings.Contains(name, "edge"):
		return "Microsoft Edge (CDP)"
	case strings.Contains(name, "chrome"):
		return "Google Chrome (CDP)"
	case strings.Contains(name, "chromium"):
		return "Chromium (CDP)"
	default:
		return ""
	}
}

func browserProtocolErrorf(format string, args ...any) {
	message := fmt.Sprintf(format, args...)
	if strings.Contains(message, "unknown IPAddressSpace value: Loopback") {
		return
	}
	log.Printf("browser CDP: %s", message)
}

func embeddedTabMarkerURL(tabID string) string {
	return "about:blank#echocode-embedded-tab-" + tabID
}

func waitForEmbeddedTarget(parent, rootCtx context.Context, markerURL string) (target.ID, error) {
	waitCtx, cancel := operationContext(parent, rootCtx, 10*time.Second)
	defer cancel()
	for {
		targets, err := chromedp.Targets(waitCtx)
		if err == nil {
			for _, info := range targets {
				if info != nil && string(info.Type) == "page" && info.URL == markerURL {
					return info.TargetID, nil
				}
			}
		} else if waitCtx.Err() != nil {
			return "", waitCtx.Err()
		}
		select {
		case <-waitCtx.Done():
			return "", fmt.Errorf("embedded WebView2 target %q was not discovered: %w", markerURL, waitCtx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func newID(prefix string) string {
	var value [8]byte
	if _, err := rand.Read(value[:]); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return prefix + "-" + hex.EncodeToString(value[:])
}
