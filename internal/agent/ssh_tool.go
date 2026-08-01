package agent

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/tools"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

const (
	maxSSHOutputBytes       = 4 * 1024 * 1024
	maxSSHUploadFileBytes   = 64 * 1024 * 1024
	maxSSHArchiveBytes      = 128 * 1024 * 1024
	maxSSHArchiveInputBytes = 512 * 1024 * 1024
)

var remoteModePattern = regexp.MustCompile(`^[0-7]{3,4}$`)

type SSHCredentialTool struct {
	Policy         tools.SandboxPolicy
	Resolve        func(string) (scopedSSHCredential, error)
	CaptureSecret  func(label, source, value string) (tools.ResultPart, error)
	KnownHostsPath string
}

type sshToolArguments struct {
	Action       string `json:"action"`
	CredentialID string `json:"credential_id"`
	Command      string `json:"command"`
	LocalPath    string `json:"local_path"`
	RemotePath   string `json:"remote_path"`
	Mode         string `json:"mode"`
	SecretLabel  string `json:"secret_label"`
	Timeout      int    `json:"timeout_seconds"`
}

func (t SSHCredentialTool) Name() string { return "ssh" }

func (t SSHCredentialTool) Description() string {
	return "Connect directly to a user-authorized SSH target with host, username, and password authentication through an opaque echocode-credential reference. No SSH key, ssh-agent, or external provider authorization entry is required. Use test to verify login, run to execute a remote command, upload_file or upload_directory to deploy workspace content, and capture_secret only when the user explicitly asks to retrieve a target-system credential or other sensitive value. capture_secret stores stdout in the host vault and returns only an opaque result ID; make its command print only one requested field. When the user requests multiple fields, such as an account and password, call capture_secret separately for every field and do not claim completion until every requested field has its own result. Never place a password in command text or tool arguments."
}

func (t SSHCredentialTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type": "string",
				"enum": []string{"test", "run", "upload_file", "upload_directory", "capture_secret"},
			},
			"credential_id": map[string]any{
				"type":        "string",
				"description": "Opaque password reference ID from a echocode-credential:// value in the user message; this is not an SSH key or external authorization entry.",
			},
			"command": map[string]any{
				"type":        "string",
				"description": "Remote shell command for action=run.",
			},
			"local_path": map[string]any{
				"type":        "string",
				"description": "Workspace-relative file or directory for an upload action.",
			},
			"remote_path": map[string]any{
				"type":        "string",
				"description": "Destination path on the authorized SSH host.",
			},
			"mode": map[string]any{
				"type":        "string",
				"description": "Optional POSIX mode for upload_file, such as 0644 or 0755.",
			},
			"secret_label": map[string]any{
				"type":        "string",
				"description": "Short user-facing label for capture_secret, such as sub2api administrator password.",
			},
			"timeout_seconds": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     1800,
				"description": "Optional connection and command timeout.",
			},
		},
		"required": []string{"action", "credential_id"},
	}
}

func (t SSHCredentialTool) Execute(ctx context.Context, rawArgs json.RawMessage) (tools.Result, error) {
	var args sshToolArguments
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return toolError(t.Name(), "invalid SSH arguments: "+err.Error()), nil
	}
	args.Action = strings.ToLower(strings.TrimSpace(args.Action))
	args.CredentialID = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args.CredentialID), scopedCredentialScheme))
	if err := t.Policy.CheckNetwork(); err != nil {
		return toolError(t.Name(), err.Error()), nil
	}
	if err := t.Policy.CheckShell(); err != nil {
		return toolError(t.Name(), err.Error()), nil
	}
	if t.Resolve == nil {
		return toolError(t.Name(), "SSH credential resolver is unavailable"), nil
	}
	credential, err := t.Resolve(args.CredentialID)
	if err != nil {
		return toolError(t.Name(), err.Error()), nil
	}

	timeout := t.commandTimeout(args.Timeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	startedAt := time.Now()
	displayInput := sshDisplayInput(args, credential)
	tools.EmitProgress(ctx, tools.ResultPart{
		Kind:      tools.PartToolCall,
		Name:      t.Name(),
		Status:    "running",
		Input:     displayInput,
		StartedAt: startedAt.Format(time.RFC3339Nano),
	})

	client, hostKey, err := t.dial(runCtx, credential, timeout)
	if err != nil {
		return sshExecutionResult(credential, displayInput, startedAt, "", redactSSHText(err.Error(), credential.Password), -1, err), nil
	}
	defer client.Close()

	switch args.Action {
	case "test":
		output := "SSH 已连接 " + credential.displayTarget()
		if hostKey.Fingerprint != "" {
			output += "\n主机密钥指纹: " + hostKey.Fingerprint
		}
		if hostKey.AcceptedNew {
			output += "\n已保存主机密钥，后续连接将校验该指纹。"
		}
		return sshExecutionResult(credential, displayInput, startedAt, output, "", 0, nil), nil
	case "run":
		command := strings.TrimSpace(args.Command)
		if command == "" {
			return sshExecutionResult(credential, displayInput, startedAt, "", "command cannot be empty", -1, errors.New("command cannot be empty")), nil
		}
		if !t.Policy.AllowDestructiveOps && remoteCommandLooksDestructive(command) {
			message := "destructive remote command is disabled by the current runtime policy"
			return sshExecutionResult(credential, displayInput, startedAt, "", message, -1, errors.New(message)), nil
		}
		stdout, stderr, exitCode, runErr := runSSHCommand(runCtx, client, command, nil)
		return sshExecutionResult(
			credential,
			displayInput,
			startedAt,
			redactSSHText(stdout, credential.Password),
			redactSSHText(stderr, credential.Password),
			exitCode,
			runErr,
		), nil
	case "capture_secret":
		command := strings.TrimSpace(args.Command)
		if command == "" {
			return sshExecutionResult(credential, displayInput, startedAt, "", "command cannot be empty", -1, errors.New("command cannot be empty")), nil
		}
		if t.CaptureSecret == nil {
			message := "host secret-result storage is unavailable"
			return sshExecutionResult(credential, displayInput, startedAt, "", message, -1, errors.New(message)), nil
		}
		if !t.Policy.AllowDestructiveOps && remoteCommandLooksDestructive(command) {
			message := "destructive remote command is disabled by the current runtime policy"
			return sshExecutionResult(credential, displayInput, startedAt, "", message, -1, errors.New(message)), nil
		}
		stdout, stderr, exitCode, runErr := runSSHCommand(runCtx, client, command, nil)
		if runErr != nil || exitCode != 0 {
			return sshExecutionResult(
				credential,
				displayInput,
				startedAt,
				redactSSHText(stdout, credential.Password),
				redactSSHText(stderr, credential.Password),
				exitCode,
				runErr,
			), nil
		}
		secretValue := strings.TrimSpace(stdout)
		if secretValue == "" {
			message := "captured output was empty; make the command print only the requested value"
			return sshExecutionResult(credential, displayInput, startedAt, "", message, -1, errors.New(message)), nil
		}
		secretPart, captureErr := t.CaptureSecret(args.SecretLabel, "ssh://"+credential.displayTarget(), secretValue)
		if captureErr != nil {
			return sshExecutionResult(credential, displayInput, startedAt, "", captureErr.Error(), -1, captureErr), nil
		}
		return sshSecretCaptureResult(credential, displayInput, startedAt, secretPart), nil
	case "upload_file":
		return t.uploadFile(runCtx, client, credential, args, displayInput, startedAt), nil
	case "upload_directory":
		return t.uploadDirectory(runCtx, client, credential, args, displayInput, startedAt), nil
	default:
		message := "unsupported SSH action: " + args.Action
		return sshExecutionResult(credential, displayInput, startedAt, "", message, -1, errors.New(message)), nil
	}
}

func (t SSHCredentialTool) commandTimeout(requested int) time.Duration {
	seconds := requested
	if seconds <= 0 {
		seconds = t.Policy.MaxCommandSeconds
	}
	if seconds <= 0 {
		seconds = 120
	}
	if seconds > 1800 {
		seconds = 1800
	}
	return time.Duration(seconds) * time.Second
}

func (t SSHCredentialTool) uploadFile(
	ctx context.Context,
	client *ssh.Client,
	credential scopedSSHCredential,
	args sshToolArguments,
	displayInput string,
	startedAt time.Time,
) tools.Result {
	localPath, err := t.Policy.ResolveReadPath(args.LocalPath)
	if err != nil {
		return sshExecutionResult(credential, displayInput, startedAt, "", err.Error(), -1, err)
	}
	file, err := os.Open(localPath)
	if err != nil {
		return sshExecutionResult(credential, displayInput, startedAt, "", err.Error(), -1, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		if err == nil {
			err = fmt.Errorf("local path is not a regular file")
		}
		return sshExecutionResult(credential, displayInput, startedAt, "", err.Error(), -1, err)
	}
	if info.Size() > maxSSHUploadFileBytes {
		err = fmt.Errorf("file is too large for SSH upload: %d bytes", info.Size())
		return sshExecutionResult(credential, displayInput, startedAt, "", err.Error(), -1, err)
	}
	remotePath, err := normalizeRemotePath(args.RemotePath)
	if err != nil {
		return sshExecutionResult(credential, displayInput, startedAt, "", err.Error(), -1, err)
	}
	mode := strings.TrimSpace(args.Mode)
	if mode == "" {
		mode = "0644"
		if info.Mode()&0o111 != 0 {
			mode = "0755"
		}
	}
	if !remoteModePattern.MatchString(mode) {
		err = fmt.Errorf("invalid remote file mode: %s", mode)
		return sshExecutionResult(credential, displayInput, startedAt, "", err.Error(), -1, err)
	}
	remoteDir := pathpkg.Dir(remotePath)
	command := "mkdir -p -- " + quotePOSIX(remoteDir) + " && cat > " + quotePOSIX(remotePath) + " && chmod " + mode + " -- " + quotePOSIX(remotePath)
	stdout, stderr, exitCode, runErr := runSSHCommand(ctx, client, command, file)
	if runErr == nil {
		stdout = strings.TrimSpace(stdout + fmt.Sprintf("\nUploaded %d bytes to %s", info.Size(), remotePath))
	}
	return sshExecutionResult(credential, displayInput, startedAt, redactSSHText(stdout, credential.Password), redactSSHText(stderr, credential.Password), exitCode, runErr)
}

func (t SSHCredentialTool) uploadDirectory(
	ctx context.Context,
	client *ssh.Client,
	credential scopedSSHCredential,
	args sshToolArguments,
	displayInput string,
	startedAt time.Time,
) tools.Result {
	localPath, err := t.Policy.ResolveReadPath(args.LocalPath)
	if err != nil {
		return sshExecutionResult(credential, displayInput, startedAt, "", err.Error(), -1, err)
	}
	info, err := os.Stat(localPath)
	if err != nil || !info.IsDir() {
		if err == nil {
			err = fmt.Errorf("local path is not a directory")
		}
		return sshExecutionResult(credential, displayInput, startedAt, "", err.Error(), -1, err)
	}
	remotePath, err := normalizeRemotePath(args.RemotePath)
	if err != nil {
		return sshExecutionResult(credential, displayInput, startedAt, "", err.Error(), -1, err)
	}
	archive, stats, err := buildSSHDirectoryArchive(ctx, localPath)
	if err != nil {
		return sshExecutionResult(credential, displayInput, startedAt, "", err.Error(), -1, err)
	}
	defer func() {
		archive.Close()
		_ = os.Remove(archive.Name())
	}()
	command := "mkdir -p -- " + quotePOSIX(remotePath) + " && tar -xzf - -C " + quotePOSIX(remotePath)
	stdout, stderr, exitCode, runErr := runSSHCommand(ctx, client, command, archive)
	if runErr == nil {
		stdout = strings.TrimSpace(stdout + fmt.Sprintf("\nUploaded %d files (%d source bytes) to %s", stats.Files, stats.SourceBytes, remotePath))
	}
	return sshExecutionResult(credential, displayInput, startedAt, redactSSHText(stdout, credential.Password), redactSSHText(stderr, credential.Password), exitCode, runErr)
}

type sshArchiveStats struct {
	Files       int
	SourceBytes int64
}

func buildSSHDirectoryArchive(ctx context.Context, root string) (*os.File, sshArchiveStats, error) {
	temporary, err := os.CreateTemp("", "echocode-ssh-upload-*.tar.gz")
	if err != nil {
		return nil, sshArchiveStats{}, err
	}
	cleanup := func(cause error) (*os.File, sshArchiveStats, error) {
		temporary.Close()
		_ = os.Remove(temporary.Name())
		return nil, sshArchiveStats{}, cause
	}

	limited := &maxBytesWriter{Writer: temporary, Limit: maxSSHArchiveBytes}
	gzipWriter := gzip.NewWriter(limited)
	tarWriter := tar.NewWriter(gzipWriter)
	stats := sshArchiveStats{}
	walkErr := filepath.WalkDir(root, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, current)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if entry.IsDir() && entry.Name() == ".git" {
			return filepath.SkipDir
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to archive symbolic link: %s", relative)
		}
		if info.Mode().IsRegular() {
			stats.SourceBytes += info.Size()
			if stats.SourceBytes > maxSSHArchiveInputBytes {
				return fmt.Errorf("directory is too large for SSH upload")
			}
			stats.Files++
		}
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		header.Name = filepath.ToSlash(relative)
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return nil
		}
		file, err := os.Open(current)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tarWriter, file)
		closeErr := file.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
	if walkErr == nil {
		walkErr = tarWriter.Close()
	} else {
		_ = tarWriter.Close()
	}
	if gzipErr := gzipWriter.Close(); walkErr == nil {
		walkErr = gzipErr
	}
	if walkErr != nil {
		return cleanup(walkErr)
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return cleanup(err)
	}
	return temporary, stats, nil
}

type maxBytesWriter struct {
	Writer  io.Writer
	Limit   int64
	Written int64
}

func (writer *maxBytesWriter) Write(data []byte) (int, error) {
	if writer.Written+int64(len(data)) > writer.Limit {
		return 0, fmt.Errorf("SSH upload archive exceeds %d bytes", writer.Limit)
	}
	written, err := writer.Writer.Write(data)
	writer.Written += int64(written)
	return written, err
}

type sshHostKeyState struct {
	Fingerprint string
	AcceptedNew bool
}

var sshKnownHostsMu sync.Mutex

func (t SSHCredentialTool) dial(ctx context.Context, credential scopedSSHCredential, timeout time.Duration) (*ssh.Client, sshHostKeyState, error) {
	if strings.TrimSpace(t.KnownHostsPath) == "" {
		return nil, sshHostKeyState{}, fmt.Errorf("SSH known-hosts storage is unavailable")
	}
	hostKey := sshHostKeyState{}
	config := &ssh.ClientConfig{
		User: credential.Username,
		Auth: []ssh.AuthMethod{
			ssh.Password(credential.Password),
			ssh.KeyboardInteractive(func(_ string, _ string, questions []string, echoes []bool) ([]string, error) {
				answers := make([]string, len(questions))
				for index := range questions {
					if index < len(echoes) && echoes[index] {
						return nil, fmt.Errorf("SSH server requested unsupported visible keyboard-interactive input")
					}
					answers[index] = credential.Password
				}
				return answers, nil
			}),
		},
		HostKeyCallback: trustOnFirstUseHostKeyCallback(t.KnownHostsPath, &hostKey),
		Timeout:         timeout,
	}
	dialer := net.Dialer{Timeout: timeout}
	connection, err := dialer.DialContext(ctx, "tcp", credential.address())
	if err != nil {
		return nil, hostKey, err
	}
	_ = connection.SetDeadline(time.Now().Add(timeout))
	closed := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = connection.Close()
		case <-closed:
		}
	}()
	sshConnection, channels, requests, err := ssh.NewClientConn(connection, credential.address(), config)
	close(closed)
	if err != nil {
		_ = connection.Close()
		return nil, hostKey, err
	}
	_ = connection.SetDeadline(time.Time{})
	return ssh.NewClient(sshConnection, channels, requests), hostKey, nil
}

func trustOnFirstUseHostKeyCallback(knownHostsPath string, state *sshHostKeyState) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		sshKnownHostsMu.Lock()
		defer sshKnownHostsMu.Unlock()
		state.Fingerprint = ssh.FingerprintSHA256(key)

		data, readErr := os.ReadFile(knownHostsPath)
		if readErr != nil && !os.IsNotExist(readErr) {
			return readErr
		}
		if len(bytes.TrimSpace(data)) > 0 {
			verifier, err := knownhosts.New(knownHostsPath)
			if err != nil {
				return err
			}
			if err := verifier(hostname, remote, key); err == nil {
				return nil
			} else {
				var keyError *knownhosts.KeyError
				if !errors.As(err, &keyError) || len(keyError.Want) > 0 {
					return fmt.Errorf("SSH host key verification failed for %s: %w", hostname, err)
				}
			}
		}

		if err := os.MkdirAll(filepath.Dir(knownHostsPath), 0o700); err != nil {
			return err
		}
		line := knownhosts.Line([]string{knownhosts.Normalize(hostname)}, key) + "\n"
		if err := tools.WriteBytesAtomic(knownHostsPath, append(data, []byte(line)...), 0o600); err != nil {
			return err
		}
		state.AcceptedNew = true
		return nil
	}
}

func runSSHCommand(ctx context.Context, client *ssh.Client, command string, stdin io.Reader) (string, string, int, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", "", -1, err
	}
	defer session.Close()
	var stdout, stderr limitedSSHBuffer
	stdout.Limit = maxSSHOutputBytes / 2
	stderr.Limit = maxSSHOutputBytes / 2
	session.Stdout = &stdout
	session.Stderr = &stderr
	if stdin != nil {
		session.Stdin = stdin
	}
	if err := session.Start(command); err != nil {
		return "", "", -1, err
	}
	done := make(chan error, 1)
	go func() { done <- session.Wait() }()
	select {
	case <-ctx.Done():
		_ = session.Close()
		return stdout.String(), stderr.String(), -1, ctx.Err()
	case waitErr := <-done:
		exitCode := 0
		if waitErr != nil {
			var exitError *ssh.ExitError
			if errors.As(waitErr, &exitError) {
				exitCode = exitError.ExitStatus()
			} else {
				exitCode = -1
			}
		}
		return stdout.String(), stderr.String(), exitCode, waitErr
	}
}

type limitedSSHBuffer struct {
	Buffer  bytes.Buffer
	Limit   int
	Dropped int
}

func (buffer *limitedSSHBuffer) Write(data []byte) (int, error) {
	remaining := buffer.Limit - buffer.Buffer.Len()
	if remaining > 0 {
		if remaining > len(data) {
			remaining = len(data)
		}
		_, _ = buffer.Buffer.Write(data[:remaining])
	}
	if remaining < len(data) {
		buffer.Dropped += len(data) - remaining
	}
	return len(data), nil
}

func (buffer *limitedSSHBuffer) String() string {
	value := buffer.Buffer.String()
	if buffer.Dropped > 0 {
		value += fmt.Sprintf("\n... [SSH output truncated: %d bytes]", buffer.Dropped)
	}
	return value
}

func sshExecutionResult(
	credential scopedSSHCredential,
	input string,
	startedAt time.Time,
	stdout string,
	stderr string,
	exitCode int,
	runErr error,
) tools.Result {
	completedAt := time.Now()
	status := "ok"
	if runErr != nil || exitCode != 0 {
		status = "error"
	}
	output := strings.TrimSpace(stdout)
	if strings.TrimSpace(stderr) != "" {
		if output != "" {
			output += "\n[stderr]\n"
		}
		output += strings.TrimSpace(stderr)
	}
	if output == "" && runErr != nil {
		output = runErr.Error()
	}
	part := tools.ResultPart{
		Kind:             tools.PartToolCall,
		Name:             "ssh",
		Status:           status,
		Input:            input,
		Output:           output,
		Stdout:           strings.TrimSpace(stdout),
		Stderr:           strings.TrimSpace(stderr),
		WorkingDirectory: "ssh://" + credential.displayTarget(),
		ExitCode:         sshIntPointer(exitCode),
		StartedAt:        startedAt.Format(time.RFC3339Nano),
		CompletedAt:      completedAt.Format(time.RFC3339Nano),
		DurationMs:       sshElapsedMilliseconds(startedAt, completedAt),
	}
	summary := fmt.Sprintf("SSH 操作已完成（%s，退出码 %d）", credential.displayTarget(), exitCode)
	if status == "error" {
		summary = fmt.Sprintf("SSH 操作失败（%s，退出码 %d）", credential.displayTarget(), exitCode)
	}
	return tools.Result{Summary: summary, Parts: []tools.ResultPart{part}, IsError: status == "error"}
}

func sshSecretCaptureResult(
	credential scopedSSHCredential,
	input string,
	startedAt time.Time,
	secretPart tools.ResultPart,
) tools.Result {
	completedAt := time.Now()
	toolPart := tools.ResultPart{
		Kind:             tools.PartToolCall,
		Name:             "ssh",
		Status:           "ok",
		Input:            input,
		Output:           "敏感结果已保存到本机凭据库，内容未发送给模型。",
		WorkingDirectory: "ssh://" + credential.displayTarget(),
		ExitCode:         sshIntPointer(0),
		StartedAt:        startedAt.Format(time.RFC3339Nano),
		CompletedAt:      completedAt.Format(time.RFC3339Nano),
		DurationMs:       sshElapsedMilliseconds(startedAt, completedAt),
	}
	return tools.Result{
		Summary: fmt.Sprintf("已将请求字段 %q 保存为本机受保护结果。若用户还要求其他字段，必须逐项捕获后再汇总；不得声称已交付尚未捕获的字段。", secretPart.SecretLabel),
		Parts:   []tools.ResultPart{toolPart, secretPart},
	}
}

func sshDisplayInput(args sshToolArguments, credential scopedSSHCredential) string {
	target := credential.displayTarget()
	switch args.Action {
	case "test":
		return "ssh test " + target
	case "run":
		return strings.TrimSpace(args.Command)
	case "capture_secret":
		return strings.TrimSpace("capture secret: " + args.Command)
	case "upload_file", "upload_directory":
		return strings.TrimSpace(args.Action + " " + args.LocalPath + " -> " + target + ":" + args.RemotePath)
	default:
		return strings.TrimSpace("ssh " + args.Action + " " + target)
	}
}

func sshToolInputForDisplay(rawArgs json.RawMessage) string {
	var args sshToolArguments
	if json.Unmarshal(rawArgs, &args) != nil {
		return "SSH operation"
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	switch action {
	case "run":
		if command := strings.TrimSpace(args.Command); command != "" {
			return command
		}
	case "capture_secret":
		if command := strings.TrimSpace(args.Command); command != "" {
			return "capture secret: " + command
		}
	case "upload_file", "upload_directory":
		return strings.TrimSpace(action + " " + args.LocalPath + " -> " + args.RemotePath)
	}
	return strings.TrimSpace("ssh " + action + " " + args.CredentialID)
}

func normalizeRemotePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("remote_path cannot be empty")
	}
	if strings.ContainsAny(value, "\x00\r\n") {
		return "", fmt.Errorf("remote_path contains invalid characters")
	}
	return value, nil
}

func quotePOSIX(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func remoteCommandLooksDestructive(command string) bool {
	normalized := strings.ToLower(strings.TrimSpace(command))
	for _, marker := range []string{
		"rm -", "rm ", "rmdir ", "mkfs", "fdisk", "parted ", "shutdown", "reboot", "poweroff",
		"systemctl disable", "truncate ", "dd if=", "iptables -f", "nft flush", "git reset --hard", "git clean -f",
	} {
		if strings.Contains(" "+normalized, " "+marker) {
			return true
		}
	}
	return false
}

func redactSSHText(value, password string) string {
	if password != "" {
		value = strings.ReplaceAll(value, password, "[已隐藏]")
	}
	return redactSensitiveText(value)
}

func sshIntPointer(value int) *int {
	return &value
}

func sshElapsedMilliseconds(startedAt, completedAt time.Time) int64 {
	duration := completedAt.Sub(startedAt).Milliseconds()
	if duration < 1 {
		return 1
	}
	return duration
}
