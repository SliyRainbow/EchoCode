package browserengine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNormalizeAddress(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{input: "", want: "about:blank"},
		{input: "about:blank", want: "about:blank"},
		{input: "https://example.com/path", want: "https://example.com/path"},
		{input: "example.com", want: "https://example.com"},
	}
	for _, test := range tests {
		got, err := normalizeAddress(test.input)
		if err != nil {
			t.Fatalf("normalizeAddress(%q): %v", test.input, err)
		}
		if got != test.want {
			t.Fatalf("normalizeAddress(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	if _, err := normalizeAddress("file:///etc/passwd"); err == nil {
		t.Fatal("file URL should be rejected")
	}
}

func TestLocalAddress(t *testing.T) {
	for _, value := range []string{"http://127.0.0.1:8080", "http://localhost:3000", "http://[::1]:9000"} {
		if !isLocalAddress(value) {
			t.Fatalf("%s should be local", value)
		}
	}
	if isLocalAddress("https://example.com") {
		t.Fatal("external URL should not be local")
	}
}

func TestDismissErrorClearsBrowserAndTabState(t *testing.T) {
	tabCtx, tabCancel := context.WithCancel(context.Background())
	defer tabCancel()
	service := New(t.TempDir(), t.TempDir())
	service.tabs["tab-1"] = &tabSession{
		ctx:    tabCtx,
		cancel: tabCancel,
		state:  Tab{ID: "tab-1", Error: "context deadline exceeded"},
	}
	service.order = []string{"tab-1"}
	service.activeTabID = "tab-1"
	service.lastError = "context deadline exceeded"

	state := service.DismissError("tab-1")
	if state.LastError != "" || len(state.Tabs) != 1 || state.Tabs[0].Error != "" {
		t.Fatalf("browser errors were not cleared: %+v", state)
	}
}

func TestCaptureFrameCancellationDoesNotPersistGlobalError(t *testing.T) {
	tabCtx, tabCancel := context.WithCancel(context.Background())
	service := New(t.TempDir(), t.TempDir())
	service.tabs["tab-1"] = &tabSession{
		ctx:    tabCtx,
		cancel: tabCancel,
		state:  Tab{ID: "tab-1", ViewportWidth: 800, ViewportHeight: 600},
	}
	tabCancel()

	if _, err := service.CaptureFrame(context.Background(), "tab-1", false); err == nil {
		t.Fatal("capture on a cancelled tab should fail")
	}
	if state := service.State(); state.LastError != "" {
		t.Fatalf("transient frame error leaked into global state: %q", state.LastError)
	}
}

func TestCloseLastTabReturnsEmptyCollections(t *testing.T) {
	tabCtx, tabCancel := context.WithCancel(context.Background())
	service := New(t.TempDir(), t.TempDir())
	service.tabs["tab-1"] = &tabSession{ctx: tabCtx, cancel: tabCancel, state: Tab{ID: "tab-1"}}
	service.order = []string{"tab-1"}
	service.activeTabID = "tab-1"

	state := service.CloseTab("tab-1")
	if state.Tabs == nil || state.Downloads == nil || len(state.Tabs) != 0 || state.ActiveTabID != "" {
		t.Fatalf("unexpected empty browser state: %+v", state)
	}
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"tabs":[]`) || !strings.Contains(string(data), `"downloads":[]`) {
		t.Fatalf("empty collections must encode as arrays: %s", data)
	}
}

func TestOperationContextFollowsParentCancellation(t *testing.T) {
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, cancel := operationContext(parent, context.Background(), time.Minute)
	defer cancel()
	cancelParent()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("operation context did not follow parent cancellation")
	}
}

func TestManagedBrowserIntegration(t *testing.T) {
	if os.Getenv("ECHOCODE_BROWSER_INTEGRATION") != "1" {
		t.Skip("set ECHOCODE_BROWSER_INTEGRATION=1 to run the Edge/Chrome smoke test")
	}
	if FindExecutable() == "" {
		t.Skip("no Edge, Chrome, or Chromium executable found")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/slow-image" {
			time.Sleep(3 * time.Second)
			w.Header().Set("Content-Type", "image/gif")
			_, _ = w.Write([]byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;"))
			return
		}
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>EchoCode Browser Smoke</title><style>
			@keyframes drift { from { transform: translateX(0); background: #d97706; } to { transform: translateX(220px); background: #2563eb; } }
			#motion { width: 40px; height: 12px; animation: drift .8s linear infinite alternate; }
		</style></head><body>
			<div id="motion"></div>
			<img src="/slow-image" alt="slow">
			<label>Name <input id="name"></label><button id="submit" onclick="document.querySelector('#result').textContent='Hello '+document.querySelector('#name').value">Run</button>
			<form><input id="login" autocomplete="username"><input id="password" type="password" autocomplete="current-password"><button id="login-button" type="button" onclick="document.querySelector('#credential-result').textContent=document.querySelector('#login').value+':'+document.querySelector('#password').value.length">Login</button></form>
			<button id="popup" onclick="window.open(location.href, '_blank')">Popup</button><button id="alert" onclick="alert('EchoCode alert')">Alert</button>
			<p id="result">Waiting</p><p id="credential-result">No credential</p><script>console.log('smoke-ready')</script></body></html>`))
	}))
	defer server.Close()

	profileDir := filepath.Join(t.TempDir(), "profile")
	crashDir := filepath.Join(profileDir, "Crashpad")
	if err := os.MkdirAll(crashDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(crashDir, "settings.dat"), []byte("not-a-valid-crashpad-settings-file"), 0o600); err != nil {
		t.Fatal(err)
	}
	service := New(profileDir, filepath.Join(t.TempDir(), "downloads"))
	if err := service.Configure(Settings{
		Enabled: true, AllowNetwork: true, ScreenshotAnnotations: "always",
		SitePermissions: []SitePermission{{Origin: server.URL, Camera: "block", Microphone: "block", Clipboard: "allow"}},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Stop(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	state, err := service.Open(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tabs) != 1 || state.ActiveTabID == "" {
		t.Fatalf("unexpected state: %+v", state)
	}
	frameStarted := time.Now()
	initialFrame, err := service.CaptureFrame(ctx, state.ActiveTabID, false)
	if err != nil {
		t.Fatalf("capture while a page resource is loading: %v", err)
	}
	if !strings.HasPrefix(initialFrame.ImageDataURL, "data:image/jpeg;base64,") {
		t.Fatalf("unexpected initial frame: image=%d", len(initialFrame.ImageDataURL))
	}
	if elapsed := time.Since(frameStarted); elapsed >= frameCaptureTimeout {
		t.Fatalf("viewport capture took too long: %v", elapsed)
	}
	frameTimes := map[string]struct{}{initialFrame.CapturedAt: {}}
	frameDeadline := time.Now().Add(2 * time.Second)
	var slowestFrameRead time.Duration
	for time.Now().Before(frameDeadline) {
		started := time.Now()
		currentFrame, frameErr := service.CaptureFrame(ctx, state.ActiveTabID, false)
		elapsed := time.Since(started)
		if frameErr != nil {
			t.Fatalf("read screencast frame: %v", frameErr)
		}
		if elapsed > slowestFrameRead {
			slowestFrameRead = elapsed
		}
		frameTimes[currentFrame.CapturedAt] = struct{}{}
		time.Sleep(50 * time.Millisecond)
	}
	if len(frameTimes) < 8 {
		t.Fatalf("screencast produced only %d distinct frames in 2s", len(frameTimes))
	}
	if slowestFrameRead > 250*time.Millisecond {
		t.Fatalf("cached frame read took too long: %v", slowestFrameRead)
	}
	t.Logf("screencast frames=%d/2s slowest-read=%v", len(frameTimes), slowestFrameRead)
	if err := service.TypeSelector(ctx, "#name", "EchoCode"); err != nil {
		t.Fatal(err)
	}
	if err := service.ClickSelector(ctx, "#submit"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot.Text, "Hello EchoCode") {
		t.Fatalf("snapshot text = %q", snapshot.Text)
	}
	if count, err := service.FillCredential(ctx, state.ActiveTabID, server.URL, "alice", "secret"); err != nil || count != 2 {
		t.Fatalf("FillCredential count=%d err=%v", count, err)
	}
	if err := service.ClickSelector(ctx, "#login-button"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = service.Snapshot(ctx)
	if err != nil || !strings.Contains(snapshot.Text, "alice:6") {
		t.Fatalf("credential snapshot=%q err=%v", snapshot.Text, err)
	}
	frame, err := service.CaptureFrame(ctx, state.ActiveTabID, true)
	if err != nil {
		t.Fatal(err)
	}
	elementsDeadline := time.Now().Add(2 * time.Second)
	for len(frame.Elements) < 2 && time.Now().Before(elementsDeadline) {
		time.Sleep(50 * time.Millisecond)
		frame, err = service.CaptureFrame(ctx, state.ActiveTabID, true)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !strings.HasPrefix(frame.ImageDataURL, "data:image/jpeg;base64,") || len(frame.Elements) < 2 {
		t.Fatalf("unexpected frame: image=%d elements=%d", len(frame.ImageDataURL), len(frame.Elements))
	}
	shotPath := filepath.Join(t.TempDir(), "browser.png")
	if _, err := service.SaveScreenshot(ctx, state.ActiveTabID, shotPath); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(shotPath); err != nil || info.Size() == 0 {
		t.Fatalf("screenshot not saved: %v", err)
	}
	if err := service.ClickSelector(ctx, "#popup"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for len(service.State().Tabs) < 2 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if len(service.State().Tabs) < 2 {
		t.Fatal("popup was not adopted as a managed tab")
	}
	service.CloseTab(service.State().ActiveTabID)
	clickResult := make(chan error, 1)
	go func() { clickResult <- service.ClickSelector(ctx, "#alert") }()
	deadline = time.Now().Add(2 * time.Second)
	for service.State().Tabs[0].Dialog == nil && time.Now().Before(deadline) {
		time.Sleep(25 * time.Millisecond)
	}
	active := service.State().ActiveTabID
	if service.State().Tabs[0].Dialog == nil {
		t.Fatal("JavaScript dialog was not exposed")
	}
	if err := service.HandleDialog(ctx, active, true, ""); err != nil {
		t.Fatal(err)
	}
	if err := <-clickResult; err != nil {
		t.Fatal(err)
	}
}

func TestManagedBrowserRecoversFromLockedProfile(t *testing.T) {
	if os.Getenv("ECHOCODE_BROWSER_INTEGRATION") != "1" {
		t.Skip("set ECHOCODE_BROWSER_INTEGRATION=1 to run the Edge/Chrome profile recovery test")
	}
	if FindExecutable() == "" {
		t.Skip("no Edge, Chrome, or Chromium executable found")
	}
	profileDir := filepath.Join(t.TempDir(), "shared-profile")
	first := New(profileDir, filepath.Join(t.TempDir(), "downloads-1"))
	second := New(profileDir, filepath.Join(t.TempDir(), "downloads-2"))
	for _, service := range []*Service{first, second} {
		if err := service.Configure(Settings{Enabled: true, AllowNetwork: true}); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = second.Stop(ctx)
		_ = first.Stop(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if state, err := first.Open(ctx, "about:blank"); err != nil || len(state.Tabs) != 1 {
		t.Fatalf("first browser state=%+v err=%v", state, err)
	}
	state, err := second.Open(ctx, "about:blank")
	if err != nil || len(state.Tabs) != 1 {
		t.Fatalf("recovered browser state=%+v err=%v", state, err)
	}
	if filepath.Clean(second.activeProfileDir) == filepath.Clean(profileDir) {
		t.Fatalf("second browser reused locked profile %q", second.activeProfileDir)
	}
}

func TestFallbackBrowserUsesSeparatePersistentProfile(t *testing.T) {
	root := filepath.Join(t.TempDir(), "browser-profile")
	edge := `C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`
	chrome := `C:\Program Files\Google\Chrome\Application\chrome.exe`
	if got := persistentBrowserProfileDir(root, edge, 0); got != filepath.Clean(root) {
		t.Fatalf("primary profile = %q, want %q", got, filepath.Clean(root))
	}
	got := persistentBrowserProfileDir(root, chrome, 1)
	if got == filepath.Clean(root) || !strings.HasSuffix(filepath.ToSlash(got), "browser-profile-chrome") {
		t.Fatalf("fallback profile = %q", got)
	}
}

func TestBrowserStartupMessageHidesRawCrashpadDiagnostics(t *testing.T) {
	diagnostic := `C:\Chrome\chrome.exe: chrome failed to start: Settings version is not 1; browser output: crashpad private details`
	message := browserStartupMessage(diagnostic)
	if !strings.Contains(message, "Crashpad 配置异常") {
		t.Fatalf("message = %q", message)
	}
	for _, private := range []string{"C:\\Chrome", "private details", "Settings version is not 1"} {
		if strings.Contains(message, private) {
			t.Fatalf("message leaked %q: %s", private, message)
		}
	}
}

func TestManagedBrowserChromeLaunch(t *testing.T) {
	if os.Getenv("ECHOCODE_BROWSER_INTEGRATION") != "1" {
		t.Skip("set ECHOCODE_BROWSER_INTEGRATION=1 to run the Chrome launch test")
	}
	chrome := ""
	for _, executable := range FindExecutables() {
		name := strings.ToLower(filepath.Base(executable))
		if strings.Contains(name, "chrome") && !strings.Contains(name, "chromium") {
			chrome = executable
			break
		}
	}
	if chrome == "" {
		t.Skip("Google Chrome is not installed")
	}
	service := New(filepath.Join(t.TempDir(), "profile"), filepath.Join(t.TempDir(), "downloads"))
	service.executable = chrome
	service.executables = []string{chrome}
	if err := service.Configure(Settings{Enabled: true, AllowNetwork: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		_ = service.Stop(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	state, err := service.Open(ctx, "about:blank")
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Tabs) != 1 || !strings.Contains(state.Engine, "Google Chrome") {
		t.Fatalf("unexpected Chrome state: %+v", state)
	}
}

func TestManagedBrowserExternalURL(t *testing.T) {
	targetURL := strings.TrimSpace(os.Getenv("ECHOCODE_BROWSER_TEST_URL"))
	if targetURL == "" {
		t.Skip("set ECHOCODE_BROWSER_TEST_URL to run an external URL diagnostic")
	}
	if FindExecutable() == "" {
		t.Skip("no Edge, Chrome, or Chromium executable found")
	}

	service := New(filepath.Join(t.TempDir(), "profile"), filepath.Join(t.TempDir(), "downloads"))
	if err := service.Configure(Settings{Enabled: true, AllowNetwork: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = service.Stop(ctx)
	})
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	openedAt := time.Now()
	state, err := service.Open(ctx, targetURL)
	t.Logf("open duration=%v state=%+v err=%v", time.Since(openedAt), state, err)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		started := time.Now()
		frame, captureErr := service.CaptureFrame(ctx, state.ActiveTabID, attempt > 1)
		state = service.State()
		t.Logf("capture=%d duration=%v image=%d elements=%d state=%+v err=%v", attempt, time.Since(started), len(frame.ImageDataURL), len(frame.Elements), state, captureErr)
		if captureErr == nil && frame.ImageDataURL != "" {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatal("external page did not produce a frame after 5 attempts")
}
