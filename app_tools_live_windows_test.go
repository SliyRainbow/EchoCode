//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/agent"
	"github.com/SliyRainbow/EchoCode/internal/browserengine"
	"github.com/SliyRainbow/EchoCode/internal/computercontrol"
	"github.com/SliyRainbow/EchoCode/internal/vault"
)

// Paid, opt-in acceptance: the actual model drives the same host bridges as the
// desktop app. Nothing from the user's projects or other windows is sent.
func liveToolsApp(t *testing.T, dialect, allowedTitle string) (*App, string, string) {
	t.Helper()
	if os.Getenv("ECHOCODE_LIVE_ACCEPTANCE") != "1" {
		t.Skip("explicit live acceptance opt-in required")
	}
	id, endpoint, model := os.Getenv("ECHOCODE_LIVE_PROVIDER_ID"), os.Getenv("ECHOCODE_LIVE_BASE_URL"), os.Getenv("ECHOCODE_LIVE_MODEL")
	if id == "" || endpoint == "" || model == "" {
		t.Fatal("explicit provider, endpoint and model required")
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "EchoCode", "runtime-settings.json"))
	if err != nil {
		t.Fatal("saved provider settings unavailable")
	}
	var saved agent.RuntimeSettings
	if json.Unmarshal(data, &saved) != nil {
		t.Fatal("saved settings invalid")
	}
	var provider agent.ModelProviderSetting
	for _, candidate := range saved.Model.Providers {
		if candidate.ID == id && strings.TrimRight(candidate.BaseURL, "/") == strings.TrimRight(endpoint, "/") {
			provider = candidate
			break
		}
	}
	if provider.ID == "" {
		t.Fatal("endpoint is outside explicit scope")
	}
	key, err := vault.NewOSVault().Get("EchoCode", "model-provider:"+id+":api-key")
	if err != nil || key == "" {
		t.Fatal("scoped credential unavailable")
	}
	if os.Getenv("ECHOCODE_LIVE_WIRE_SHAPES") == "1" {
		endpoint = observeLiveWireShapes(t, endpoint)
	}
	root, workspace := t.TempDir(), t.TempDir()
	settings := agent.DefaultRuntimeSettings()
	settings.WorkspaceRoot, settings.ExtraWritableRoots = workspace, nil
	settings.ShellAccess, settings.Team.Enabled, settings.Memory.Enabled = false, false, false
	settings.NetworkAccess, settings.Browser.Enabled = allowedTitle == "", allowedTitle == ""
	settings.ComputerControl.AnyAppEnabled, settings.ComputerControl.ChromeEnabled = false, false
	settings.ComputerControl.AlwaysAllowedApps = nil
	if allowedTitle != "" {
		settings.ComputerControl.AlwaysAllowedApps = []string{allowedTitle}
	}
	settings.ApprovalPolicy = "on-request"
	protocolName, apiType := "openai-compatible", "chat-completions"
	if dialect == "responses" {
		apiType = "responses"
	}
	if dialect == "messages" {
		protocolName, apiType = "anthropic-compatible", "anthropic-messages"
	}
	settings.Model = agent.ModelSettings{SelectedProviderID: "live-tools", SelectedModelID: model, Providers: []agent.ModelProviderSetting{{ID: "live-tools", Name: "Live fixture", Enabled: true, Protocol: protocolName, APIType: apiType, BaseURL: endpoint, ExtraHeaders: provider.ExtraHeaders, DefaultModelID: model, Models: []agent.ProviderModel{{ID: model, MaxOutputTokens: 4096}}}}}
	data, _ = json.Marshal(settings)
	settingsPath := filepath.Join(root, "settings.json")
	if err := os.WriteFile(settingsPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	memory := vault.NewMemoryVault()
	_ = memory.Set("EchoCode", "model-provider:live-tools:api-key", key)
	a := &App{runtimeSettings: settings, computer: computercontrol.New(), browser: browserengine.New(filepath.Join(root, "browser"), filepath.Join(root, "downloads"))}
	if err := a.browser.Configure(browserengine.Settings{Enabled: settings.Browser.Enabled, AllowNetwork: settings.NetworkAccess}); err != nil {
		t.Fatal(err)
	}
	a.service = agent.NewService(agent.ServiceConfig{AppVersion: "live-acceptance", Vault: memory, SettingsPath: settingsPath, SkillsDir: filepath.Join(root, "skills"), UserSkillsDir: filepath.Join(root, "user-skills"), PluginsDir: filepath.Join(root, "plugins"), SessionsDir: filepath.Join(root, "sessions"), ProjectsPath: filepath.Join(root, "projects.json"), TemporaryWorkspaceRoot: workspace, Browser: &browserToolBridge{app: a}, Computer: &computerToolBridge{app: a}})
	a.service.SetApprovalNotify(func(request agent.ApprovalRequest) {
		allowed := request.Tool == "browser" || (request.Tool == "computer" && allowedTitle != "")
		go func() { _ = a.service.RespondApproval(request.ID, request.Tool, allowed, "once") }()
	})
	if _, err := a.service.SetReasoningLevel(agent.ReasoningLow); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { a.service.Close(); _ = a.browser.Stop(context.Background()) })
	return a, key, workspace
}

func runLiveTools(t *testing.T, a *App, key, prompt string) (agent.ChatResult, map[string]int, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	actions := map[string]int{}
	var mu sync.Mutex
	result, err := a.service.SendChatMessageWithEvents(ctx, prompt, func(event agent.ChatStreamEvent) {
		if event.Type != "tool" || event.Status != "running" {
			return
		}
		input := strings.Fields(event.ToolInput)
		if len(input) > 0 {
			action := input[0]
			if event.ToolName == "browser" && strings.HasPrefix(action, "http") {
				action = "open"
			}
			mu.Lock()
			actions[event.ToolName+":"+action]++
			mu.Unlock()
		}
	})
	if err != nil {
		err = fmt.Errorf("%s", strings.ReplaceAll(err.Error(), key, "[REDACTED]"))
	}
	t.Logf("observed model actions: %v; committed=%t", actions, result.TurnCommitted)
	for _, part := range result.Parts {
		if part.Kind == "tool_call" && part.Status == "error" {
			t.Logf("tool failure %s: %.600s", part.Name, strings.ReplaceAll(part.Output, key, "[REDACTED]"))
		}
	}
	return result, actions, err
}

func TestLiveAgentBrowser(t *testing.T) {
	for _, dialect := range []string{"chat", "responses", "messages"} {
		t.Run(dialect, func(t *testing.T) {
			a, key, _ := liveToolsApp(t, dialect, "")
			var mu sync.Mutex
			received := ""
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/submit" {
					mu.Lock()
					received = r.URL.Query().Get("value")
					mu.Unlock()
					_, _ = w.Write([]byte("BROWSER_CONFIRMED"))
					return
				}
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				fmt.Fprint(w, `<!doctype html><title>EchoCode browser fixture</title><main><h1>Browser acceptance</h1><label>Test value<input id="value"></label><button id="confirm" onclick="fetch('/submit?value='+encodeURIComponent(document.querySelector('#value').value)).then(r=>r.text()).then(t=>document.querySelector('#result').textContent=t)">Confirm</button><p id="result"></p></main>`)
			}))
			defer server.Close()
			result, actions, err := runLiveTools(t, a, key, "仅使用内置 browser 工具完成验收：open 打开 "+server.URL+"，snapshot 读取元素，向输入框 type 输入 ECHO_BROWSER_OK，click 点击 Confirm，再 snapshot 验证 BROWSER_CONFIRMED。不要用其他工具或访问其他地址。最后回复已确认的结果。")
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"open", "snapshot", "type", "click"} {
				if actions["browser:"+action] == 0 {
					t.Errorf("model did not invoke browser %s", action)
				}
			}
			mu.Lock()
			value := received
			mu.Unlock()
			if value != "ECHO_BROWSER_OK" || !strings.Contains(result.Content, "BROWSER_CONFIRMED") {
				t.Fatalf("actual form submission or result inspection failed: value=%q", value)
			}
		})
	}
}

// The only permitted window is created by this test. It contains no personal
// data, sits above other windows, and is torn down by its exact process handle.
func startComputerFixture(t *testing.T, title, code string) string {
	t.Helper()
	root := t.TempDir()
	output, script := filepath.Join(root, "submitted.txt"), filepath.Join(root, "fixture.ps1")
	const source = `param([string]$Title, [string]$Code, [string]$OutputPath)
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
$form = New-Object System.Windows.Forms.Form
$form.Text = $Title
$form.FormBorderStyle = 'None'
$form.StartPosition = 'CenterScreen'
$form.Size = New-Object System.Drawing.Size(520, 280)
$form.BackColor = 'White'
$form.TopMost = $true
$form.AutoScaleMode = 'None'
$label = New-Object System.Windows.Forms.Label
$label.Text = 'Visual code: ' + $Code
$label.Location = New-Object System.Drawing.Point(24, 24)
$label.Size = New-Object System.Drawing.Size(480, 44)
$label.Font = New-Object System.Drawing.Font('Segoe UI', 22)
$inputBox = New-Object System.Windows.Forms.TextBox
$inputBox.Location = New-Object System.Drawing.Point(24, 100)
$inputBox.Size = New-Object System.Drawing.Size(460, 38)
$inputBox.Font = New-Object System.Drawing.Font('Segoe UI', 18)
$button = New-Object System.Windows.Forms.Button
$button.Text = 'Confirm'
$button.Location = New-Object System.Drawing.Point(24, 180)
$button.Size = New-Object System.Drawing.Size(180, 50)
$button.Add_Click({ [System.IO.File]::WriteAllText($OutputPath, $inputBox.Text) })
$form.Controls.AddRange(@($label, $inputBox, $button))
$form.AcceptButton = $button
$form.Add_Shown({ $form.Activate(); $inputBox.Focus() })
[System.Windows.Forms.Application]::Run($form)
`
	if err := os.WriteFile(script, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass", "-File", script, "-Title", title, "-Code", code, "-OutputPath", output)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		windows, _ := computercontrol.New().ListWindows(context.Background())
		for _, window := range windows {
			if window.Title == title {
				return output
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("fixture window did not appear")
	return ""
}

func TestLiveAgentComputer(t *testing.T) {
	for _, visual := range []bool{false, true} {
		t.Run(fmt.Sprintf("visual-%t", visual), func(t *testing.T) {
			title := fmt.Sprintf("EchoCode acceptance %d", time.Now().UnixNano())
			dialect := os.Getenv("ECHOCODE_LIVE_COMPUTER_API")
			if dialect == "" {
				dialect = "chat"
			}
			if dialect != "chat" && dialect != "responses" && dialect != "messages" {
				t.Fatal("unknown computer test dialect")
			}
			a, key, workspace := liveToolsApp(t, dialect, title)
			code := fmt.Sprintf("%06d", time.Now().UnixNano()%1000000)
			output := startComputerFixture(t, title, code)
			prompt := "仅使用 computer 工具操作标题为 " + title + " 的测试窗口：list_windows，focus，然后 click 窗口坐标(140,115)聚焦输入框，type 输入 ECHO_DESKTOP_OK，再 key Enter 提交。这是固定坐标的键盘验收，无需截图。不要使用其他工具、切换其他应用或写入文件。"
			expected := "ECHO_DESKTOP_OK"
			if visual {
				expected = code
				prompt = "仅使用 computer 工具操作标题为 " + title + " 的测试窗口：先 list_windows、focus，然后 screenshot 保存 fixture.png，读取截图中 Visual code 的六位数字，click 输入框并 type 输入这六位数字，最后 key Enter 提交。只能从截图识别数字，不允许猜测或使用其他工具读取文件。"
			}
			_, actions, err := runLiveTools(t, a, key, prompt)
			if err != nil {
				t.Fatal(err)
			}
			for _, action := range []string{"list_windows", "focus", "click", "type", "key"} {
				if actions["computer:"+action] == 0 {
					t.Errorf("model did not invoke computer %s", action)
				}
			}
			if visual {
				if actions["computer:screenshot"] == 0 {
					t.Fatal("model did not request fixture screenshot")
				}
				if _, err := os.Stat(filepath.Join(workspace, "fixture.png")); err != nil {
					t.Fatal("screenshot not saved")
				}
			}
			data, err := os.ReadFile(output)
			if err != nil || strings.TrimSpace(string(data)) != expected {
				t.Fatalf("fixture did not receive expected input (visual=%t); screenshot understanding is unverified", visual)
			}
		})
	}
}

func TestDesktopComputerFixture(t *testing.T) {
	if os.Getenv("ECHOCODE_DESKTOP_FIXTURE") != "1" {
		t.Skip("explicit isolated desktop fixture opt-in required")
	}
	title := fmt.Sprintf("EchoCode native fixture %d", time.Now().UnixNano())
	output := startComputerFixture(t, title, "123456")
	controller := computercontrol.New()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	windows, err := controller.ListWindows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var window computercontrol.Window
	for _, candidate := range windows {
		if candidate.Title == title {
			window = candidate
			break
		}
	}
	if window.ID == "" {
		t.Fatal("own fixture missing")
	}
	t.Logf("fixture bounds: %dx%d; foreground=%t", window.Width, window.Height, window.Foreground)
	for i := 0; i < 3; i++ {
		if err := controller.FocusWindow(ctx, window.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := controller.ClickWindow(ctx, window.ID, window.Width*140/520, window.Height*115/280); err != nil {
		t.Fatal(err)
	}
	if err := controller.TypeText(ctx, window.ID, "NATIVE_COMPUTER_OK"); err != nil {
		t.Fatal(err)
	}
	if err := controller.PressKey(ctx, window.ID, "Enter", false, false, false); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(output)
		if err == nil && string(data) == "NATIVE_COMPUTER_OK" {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("fixture did not receive native keyboard input")
}
