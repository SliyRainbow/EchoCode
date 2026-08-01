package agent

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
	"golang.org/x/crypto/ssh"
)

func TestSSHCredentialToolUsesPasswordReferenceWithoutExposingSecret(t *testing.T) {
	const password = "test-only-password"
	server := startSSHTestServer(t, password)
	defer server.Close()
	host, portText, err := net.SplitHostPort(server.Address())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	workspace := t.TempDir()
	credential := scopedSSHCredential{
		ID:       "ssh-test-reference",
		Kind:     "ssh_password",
		Host:     host,
		Port:     port,
		Username: "root",
		Password: password,
	}
	tool := SSHCredentialTool{
		Policy: tools.SandboxPolicy{
			SandboxMode:         "workspace-write",
			WorkspaceRoot:       workspace,
			FilesystemAccess:    "workspace-write",
			NetworkAccess:       true,
			ShellAccess:         true,
			AllowDestructiveOps: true,
			MaxCommandSeconds:   5,
		},
		Resolve: func(id string) (scopedSSHCredential, error) {
			if id != credential.ID {
				return scopedSSHCredential{}, fmt.Errorf("unexpected credential ID: %s", id)
			}
			return credential, nil
		},
		CaptureSecret: func(label, source, value string) (tools.ResultPart, error) {
			expectedValue := "application-secret"
			secretID := "secret-test-result"
			if label == "与连接凭据相同的应用管理员密码" {
				expectedValue = password
				secretID = "secret-password-match-result"
			} else if label != "应用管理员密码" {
				return tools.ResultPart{}, fmt.Errorf("unexpected secret label: %q", label)
			}
			if value != expectedValue {
				return tools.ResultPart{}, fmt.Errorf("unexpected captured value for %q", label)
			}
			return tools.ResultPart{
				Kind:         tools.PartSecretResult,
				SecretID:     secretID,
				SecretLabel:  label,
				SecretSource: source,
			}, nil
		},
		KnownHostsPath: filepath.Join(t.TempDir(), "known_hosts"),
	}

	testResult := executeSSHTestTool(t, tool, sshToolArguments{Action: "test", CredentialID: scopedCredentialScheme + credential.ID})
	if testResult.IsError || len(testResult.Parts) != 1 || testResult.Parts[0].ExitCode == nil || *testResult.Parts[0].ExitCode != 0 {
		t.Fatalf("test result = %#v", testResult)
	}
	if knownHosts, err := os.ReadFile(tool.KnownHostsPath); err != nil || len(strings.TrimSpace(string(knownHosts))) == 0 {
		t.Fatalf("known hosts was not saved: data=%q err=%v", knownHosts, err)
	}

	runResult := executeSSHTestTool(t, tool, sshToolArguments{Action: "run", CredentialID: credential.ID, Command: "printf hello"})
	if runResult.IsError || !strings.Contains(runResult.Parts[0].Stdout, "hello") {
		t.Fatalf("run result = %#v", runResult)
	}
	if strings.Contains(runResult.Summary, "hello") {
		t.Fatalf("SSH summary duplicated stdout: %q", runResult.Summary)
	}

	captureResult := executeSSHTestTool(t, tool, sshToolArguments{
		Action:       "capture_secret",
		CredentialID: credential.ID,
		Command:      "read-app-secret",
		SecretLabel:  "应用管理员密码",
	})
	encodedCaptureResult, _ := json.Marshal(captureResult)
	if captureResult.IsError || !strings.Contains(string(encodedCaptureResult), "secret-test-result") {
		t.Fatalf("capture result = %s", encodedCaptureResult)
	}
	if strings.Contains(string(encodedCaptureResult), "application-secret") {
		t.Fatalf("capture result leaked target secret: %s", encodedCaptureResult)
	}

	secretResult := executeSSHTestTool(t, tool, sshToolArguments{Action: "run", CredentialID: credential.ID, Command: "echo-secret"})
	encodedSecretResult, _ := json.Marshal(secretResult)
	if strings.Contains(string(encodedSecretResult), password) || !strings.Contains(string(encodedSecretResult), "已隐藏") {
		t.Fatalf("SSH result leaked the password: %s", encodedSecretResult)
	}
	matchingTargetPasswordCapture := executeSSHTestTool(t, tool, sshToolArguments{
		Action:       "capture_secret",
		CredentialID: credential.ID,
		Command:      "echo-secret",
		SecretLabel:  "与连接凭据相同的应用管理员密码",
	})
	encodedMatchingTargetPasswordCapture, _ := json.Marshal(matchingTargetPasswordCapture)
	if matchingTargetPasswordCapture.IsError || !strings.Contains(string(encodedMatchingTargetPasswordCapture), "secret-password-match-result") {
		t.Fatalf("matching target password was not captured: %s", encodedMatchingTargetPasswordCapture)
	}
	if strings.Contains(string(encodedMatchingTargetPasswordCapture), password) {
		t.Fatalf("matching target password leaked into the tool result: %s", encodedMatchingTargetPasswordCapture)
	}

	localFile := filepath.Join(workspace, "index.html")
	if err := os.WriteFile(localFile, []byte("<h1>deployed</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	uploadResult := executeSSHTestTool(t, tool, sshToolArguments{
		Action:       "upload_file",
		CredentialID: credential.ID,
		LocalPath:    "index.html",
		RemotePath:   "/var/www/html/index.html",
	})
	if uploadResult.IsError {
		t.Fatalf("upload result = %#v", uploadResult)
	}
	select {
	case uploaded := <-server.Uploads():
		if string(uploaded) != "<h1>deployed</h1>" {
			t.Fatalf("uploaded data = %q", uploaded)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SSH server did not receive the uploaded file")
	}
}

func TestBuildSSHDirectoryArchivePreservesWorkspaceFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("home"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "assets", "app.js"), []byte("app"), 0o644); err != nil {
		t.Fatal(err)
	}
	archive, stats, err := buildSSHDirectoryArchive(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		archive.Close()
		_ = os.Remove(archive.Name())
	}()
	if stats.Files != 2 || stats.SourceBytes != 7 {
		t.Fatalf("archive stats = %#v", stats)
	}
	gzipReader, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	contents := map[string]string{}
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.FileInfo().IsDir() {
			continue
		}
		data, err := io.ReadAll(tarReader)
		if err != nil {
			t.Fatal(err)
		}
		contents[header.Name] = string(data)
	}
	if contents["index.html"] != "home" || contents["assets/app.js"] != "app" {
		t.Fatalf("archive contents = %#v", contents)
	}
}

func TestSSHToolRegistrationAndStablePromptGuidance(t *testing.T) {
	service := NewService(ServiceConfig{SkillsDir: t.TempDir(), SettingsPath: filepath.Join(t.TempDir(), "runtime-settings.json")})
	defer service.Close()
	settings := DefaultRuntimeSettings()
	settings.WorkspaceRoot = t.TempDir()
	settings.FilesystemAccess = "workspace-write"
	settings.SandboxMode = "workspace-write"
	settings.NetworkAccess = true
	settings.ShellAccess = true
	service.runtimeSettings = settings.Normalized()
	if _, ok := service.buildToolRegistry().Get("ssh"); !ok {
		t.Fatal("SSH tool was not registered when network and shell access are enabled")
	}
	service.runtimeSettings.NetworkAccess = false
	if _, ok := service.buildToolRegistry().Get("ssh"); ok {
		t.Fatal("SSH tool remained registered with network access disabled")
	}
	prompt := formatStablePrompt(RequestContext{})
	for _, expected := range []string{
		"echocode-credential://",
		"No SSH key, ssh-agent, or external provider authorization entry is required.",
		"not an SSH key or an external authorization entry",
		"Password-based SSH authentication does not use ssh-add or ssh-agent.",
		"choose ssh test, ssh run",
		"Call it separately for every requested field",
		"a stored account does not satisfy a requested password",
		"happens to equal the SSH connection password",
		"without asking the user to reply with 'continue'",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("stable prompt is missing %q", expected)
		}
	}
	preview := service.contextPreviewForInput("")
	if !strings.Contains(stableSection(preview, "system_rules", ""), "不需要 SSH Key") {
		t.Fatal("password SSH policy is not part of the stable context hash")
	}
	if !strings.Contains(stableSection(preview, "system_rules", ""), "每个请求字段必须分别生成独立的受保护结果") {
		t.Fatal("per-field protected result policy is not part of the stable context hash")
	}
}

func executeSSHTestTool(t *testing.T, tool SSHCredentialTool, args sshToolArguments) tools.Result {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := tool.Execute(context.Background(), raw)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

type sshTestServer struct {
	listener net.Listener
	config   *ssh.ServerConfig
	password string
	uploads  chan []byte
	closed   chan struct{}
	once     sync.Once
}

func startSSHTestServer(t *testing.T, password string) *sshTestServer {
	t.Helper()
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		t.Fatal(err)
	}
	config := &ssh.ServerConfig{
		PasswordCallback: func(metadata ssh.ConnMetadata, supplied []byte) (*ssh.Permissions, error) {
			if metadata.User() != "root" || string(supplied) != password {
				return nil, fmt.Errorf("authentication failed")
			}
			return nil, nil
		},
	}
	config.AddHostKey(signer)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &sshTestServer{
		listener: listener,
		config:   config,
		password: password,
		uploads:  make(chan []byte, 4),
		closed:   make(chan struct{}),
	}
	go server.accept()
	return server
}

func (server *sshTestServer) Address() string        { return server.listener.Addr().String() }
func (server *sshTestServer) Uploads() <-chan []byte { return server.uploads }

func (server *sshTestServer) Close() {
	server.once.Do(func() {
		close(server.closed)
		_ = server.listener.Close()
	})
}

func (server *sshTestServer) accept() {
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		go server.handleConnection(connection)
	}
}

func (server *sshTestServer) handleConnection(connection net.Conn) {
	sshConnection, channels, requests, err := ssh.NewServerConn(connection, server.config)
	if err != nil {
		_ = connection.Close()
		return
	}
	defer sshConnection.Close()
	go ssh.DiscardRequests(requests)
	for newChannel := range channels {
		if newChannel.ChannelType() != "session" {
			_ = newChannel.Reject(ssh.UnknownChannelType, "session channel required")
			continue
		}
		channel, requests, err := newChannel.Accept()
		if err != nil {
			continue
		}
		go server.handleSession(channel, requests)
	}
}

func (server *sshTestServer) handleSession(channel ssh.Channel, requests <-chan *ssh.Request) {
	defer channel.Close()
	for request := range requests {
		if request.Type != "exec" {
			_ = request.Reply(false, nil)
			continue
		}
		var payload struct{ Command string }
		if err := ssh.Unmarshal(request.Payload, &payload); err != nil {
			_ = request.Reply(false, nil)
			return
		}
		_ = request.Reply(true, nil)
		switch {
		case payload.Command == "printf hello":
			_, _ = io.WriteString(channel, "hello")
		case payload.Command == "echo-secret":
			_, _ = io.WriteString(channel, server.password)
		case payload.Command == "read-app-secret":
			_, _ = io.WriteString(channel, "application-secret")
		case strings.Contains(payload.Command, "cat >") || strings.Contains(payload.Command, "tar -xzf"):
			data, _ := io.ReadAll(channel)
			server.uploads <- data
		default:
			_, _ = io.WriteString(channel, "ok")
		}
		_, _ = channel.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{Status: 0}))
		return
	}
}
