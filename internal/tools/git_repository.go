package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/sandboxexec"
)

const maxGitRepositoryOutputBytes = 4 * 1024 * 1024

// GitRepositoryTool performs remote repository transfers without exposing a
// general-purpose shell. Local status/diff/commit operations remain on the
// separate git tool owned by workspacegit.
type GitRepositoryTool struct {
	Policy         SandboxPolicy
	RetryDelay     time.Duration
	commandFactory func(context.Context, []string) *exec.Cmd
}

func (t GitRepositoryTool) Name() string { return "git_repository" }

func (t GitRepositoryTool) Description() string {
	return "Clone a Git repository to an authorized local directory, or fetch/pull an existing authorized repository. Use this for real local repository transfers; use read_repository only for remote inspection and git for status, diff, staging, commits, and branches. Progress and process output are streamed, and no shell command is constructed."
}

func (t GitRepositoryTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"clone", "fetch", "pull"},
				"description": "clone creates a new local repository; fetch and pull update an existing repository",
			},
			"url": map[string]any{
				"type":        "string",
				"description": "Repository URL for clone. HTTP(S), SSH/scp-style, and authorized local repositories are supported",
			},
			"destination": map[string]any{
				"type":        "string",
				"description": "New authorized destination directory for clone",
			},
			"repository": map[string]any{
				"type":        "string",
				"description": "Authorized existing repository directory for fetch/pull; defaults to the current workspace",
			},
			"remote": map[string]any{
				"type":        "string",
				"description": "Remote name for fetch/pull; defaults to origin",
			},
			"branch": map[string]any{
				"type":        "string",
				"description": "Optional branch to clone, fetch, or pull",
			},
			"depth": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"maximum":     100000,
				"description": "Optional shallow clone depth; 0 keeps full history",
			},
			"prune": map[string]any{
				"type":        "boolean",
				"description": "Remove remote-tracking refs deleted upstream during fetch",
			},
			"strategy": map[string]any{
				"type":        "string",
				"enum":        []string{"ff-only", "rebase", "merge"},
				"description": "Pull integration strategy; defaults to ff-only",
			},
			"max_retries": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"maximum":     3,
				"description": "Retries for transient clone transport failures; defaults to 2",
			},
		},
		"required": []string{"action"},
	}
}

type gitRepositoryArguments struct {
	Action      string `json:"action"`
	URL         string `json:"url"`
	Destination string `json:"destination"`
	Repository  string `json:"repository"`
	Remote      string `json:"remote"`
	Branch      string `json:"branch"`
	Depth       int    `json:"depth"`
	Prune       bool   `json:"prune"`
	Strategy    string `json:"strategy"`
	MaxRetries  *int   `json:"max_retries"`
}

type gitRepositoryInvocation struct {
	args             []string
	displayArgs      []string
	workingDirectory string
	resultPath       string
	installClone     func() error
	cleanup          func()
}

func (t GitRepositoryTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args gitRepositoryArguments
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("invalid Git repository arguments: " + err.Error()), nil
	}
	args.Action = strings.ToLower(strings.TrimSpace(args.Action))
	args.URL = strings.TrimSpace(args.URL)
	args.Destination = strings.TrimSpace(args.Destination)
	args.Repository = strings.TrimSpace(args.Repository)
	args.Remote = strings.TrimSpace(args.Remote)
	args.Branch = strings.TrimSpace(args.Branch)
	args.Strategy = strings.ToLower(strings.TrimSpace(args.Strategy))

	if !t.Policy.NetworkAccess {
		return errorResult(ErrNetworkDisabled.Error()), nil
	}
	if strings.EqualFold(strings.TrimSpace(t.Policy.FilesystemAccess), "read-only") {
		return errorResult(ErrReadOnlyFilesystem.Error()), nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return errorResult("Git executable was not found"), nil
	}

	maxRetries := 2
	if args.MaxRetries != nil {
		maxRetries = *args.MaxRetries
	}
	if maxRetries < 0 || maxRetries > 3 {
		return errorResult("max_retries must be between 0 and 3"), nil
	}
	if args.Action != "clone" {
		maxRetries = 0
	}
	startedAt := time.Now()
	var invocation gitRepositoryInvocation
	var displayCommand, stdoutOutput, stderrOutput, output string
	var runErr, prepareErr error
	var exitCode, finalAttempt int
	retryHistory := make([]string, 0, maxRetries)

	for attempt := 0; attempt <= maxRetries; attempt++ {
		invocation, prepareErr = t.prepareInvocation(args)
		if prepareErr != nil {
			return errorResult(prepareErr.Error()), nil
		}
		displayCommand = formatDirectCommand("git", invocation.displayArgs)
		var stdout, stderr cappedCommandOutput
		stdout.limit = maxGitRepositoryOutputBytes / 2
		stderr.limit = maxGitRepositoryOutputBytes / 2
		reporter := &commandProgressReporter{
			ctx: ctx, toolName: t.Name(), command: displayCommand,
			workDir: invocation.workingDirectory, startedAt: startedAt,
			stdout: &stdout, stderr: &stderr, minInterval: 120 * time.Millisecond,
			outputFilter: func(value string) string {
				return sanitizeGitRepositoryOutput(value, args.URL)
			},
		}
		stdout.notify = reporter.emit
		stderr.notify = reporter.emit
		reporter.emitNow()

		cmd := t.gitCommand(ctx, invocation.args)
		cmd.Dir = invocation.workingDirectory
		cmd.Env = gitRepositoryEnvironment()
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		process, startErr := sandboxexec.Start(cmd, t.Policy.ProcessLimits())
		runErr = startErr
		if startErr == nil {
			runErr = process.Wait()
		}
		if runErr == nil && invocation.installClone != nil {
			runErr = invocation.installClone()
		}
		if invocation.cleanup != nil {
			invocation.cleanup()
		}

		stdoutOutput = sanitizeGitRepositoryOutput(DecodeCommandOutput(stdout.Bytes()), args.URL)
		stderrOutput = sanitizeGitRepositoryOutput(DecodeCommandOutput(stderr.Bytes()), args.URL)
		output = commandOutputForDisplay(stdoutOutput, stderrOutput)
		if dropped := stdout.Dropped() + stderr.Dropped(); dropped > 0 {
			output += fmt.Sprintf("\n... [Git output truncated: %d bytes]", dropped)
		}
		exitCode = 0
		if runErr != nil {
			exitCode = -1
			var exitErr *exec.ExitError
			if errors.As(runErr, &exitErr) {
				exitCode = exitErr.ExitCode()
			}
			if strings.TrimSpace(output) == "" {
				output = runErr.Error()
			}
		}
		finalAttempt = attempt + 1
		if runErr == nil || ctx.Err() != nil || attempt >= maxRetries || !gitRepositoryRetryableFailure(runErr, output) {
			break
		}

		retryHistory = append(retryHistory, fmt.Sprintf("attempt %d: %s", attempt+1, compactToolError(output)))
		emitGitRepositoryRetry(ctx, displayCommand, invocation.workingDirectory, startedAt, attempt+1, maxRetries, output)
		if err := waitForDownloadRetry(ctx, t.retryDelay(attempt+1)); err != nil {
			runErr = err
			break
		}
	}

	completedAt := time.Now()
	if len(retryHistory) > 0 {
		output = "Retry history:\n- " + strings.Join(retryHistory, "\n- ") + "\n\n" + output
	}

	status := "ok"
	if runErr != nil {
		status = "error"
		if ctx.Err() != nil {
			output = strings.TrimSpace(output + "\nGit operation was cancelled")
		}
	} else {
		location := invocation.resultPath
		if location == "" {
			location = invocation.workingDirectory
		}
		output = strings.TrimSpace(output + "\nRepository: " + location)
	}

	part := ResultPart{
		Kind: PartToolCall, Name: t.Name(), Status: status,
		Input: displayCommand, Output: output, Stdout: stdoutOutput, Stderr: stderrOutput,
		WorkingDirectory: invocation.workingDirectory, ExitCode: intPointer(exitCode),
		StartedAt: startedAt.Format(time.RFC3339Nano), CompletedAt: completedAt.Format(time.RFC3339Nano),
		DurationMs: elapsedMilliseconds(startedAt, completedAt), Attempt: finalAttempt,
	}
	summary := fmt.Sprintf("Git %s completed in %s", args.Action, firstNonEmptyString(invocation.resultPath, invocation.workingDirectory))
	if status == "error" {
		summary = fmt.Sprintf("Git %s failed: %s", args.Action, compactToolError(output))
	}
	return Result{Summary: summary, IsError: status == "error", Parts: []ResultPart{part}}, nil
}

func (t GitRepositoryTool) gitCommand(ctx context.Context, args []string) *exec.Cmd {
	if t.commandFactory != nil {
		return t.commandFactory(ctx, args)
	}
	return buildDirectCommand(ctx, "git", args)
}

func (t GitRepositoryTool) retryDelay(attempt int) time.Duration {
	if t.RetryDelay > 0 {
		return t.RetryDelay
	}
	return time.Duration(attempt) * 500 * time.Millisecond
}

func gitRepositoryRetryableFailure(runErr error, output string) bool {
	if runErr == nil {
		return false
	}
	detail := strings.ToLower(strings.TrimSpace(output + "\n" + runErr.Error()))
	for _, marker := range []string{
		"authentication failed", "repository not found", "access denied", "permission denied",
		"could not read username", "http 401", "http 403", "error: 401", "error: 403",
	} {
		if strings.Contains(detail, marker) {
			return false
		}
	}
	for _, marker := range []string{
		"schannel:", "ssl", "tls", "handshake", "could not resolve host",
		"failed to connect", "connection reset", "connection timed out", "network is unreachable",
		"remote end hung up unexpectedly", "early eof", "unexpected disconnect", "http/2 stream",
		"error: 429", "error: 500", "error: 502", "error: 503", "error: 504",
	} {
		if strings.Contains(detail, marker) {
			return true
		}
	}
	return false
}

func emitGitRepositoryRetry(ctx context.Context, command, workDir string, startedAt time.Time, attempt, maxRetries int, output string) {
	EmitProgress(ctx, ResultPart{
		Kind: PartToolCall, Name: "git_repository", Status: "retrying",
		Input: command, Output: fmt.Sprintf("%s; retry %d of %d", compactToolError(output), attempt, maxRetries),
		WorkingDirectory: workDir, StartedAt: startedAt.Format(time.RFC3339Nano), Attempt: attempt + 1,
	})
}

func (t GitRepositoryTool) prepareInvocation(args gitRepositoryArguments) (gitRepositoryInvocation, error) {
	switch args.Action {
	case "clone":
		return t.prepareClone(args)
	case "fetch", "pull":
		return t.prepareUpdate(args)
	default:
		return gitRepositoryInvocation{}, fmt.Errorf("unsupported Git repository action: %s", args.Action)
	}
}

func (t GitRepositoryTool) prepareClone(args gitRepositoryArguments) (gitRepositoryInvocation, error) {
	if args.URL == "" {
		return gitRepositoryInvocation{}, errors.New("repository url is required for clone")
	}
	if args.Destination == "" {
		return gitRepositoryInvocation{}, errors.New("destination is required for clone")
	}
	if args.Depth < 0 || args.Depth > 100000 {
		return gitRepositoryInvocation{}, errors.New("clone depth must be between 0 and 100000")
	}
	if err := validateGitRefArgument("branch", args.Branch); err != nil {
		return gitRepositoryInvocation{}, err
	}
	source, displaySource, err := t.resolveRepositorySource(args.URL)
	if err != nil {
		return gitRepositoryInvocation{}, err
	}
	destination, err := t.Policy.ResolveWritePath(args.Destination)
	if err != nil {
		return gitRepositoryInvocation{}, err
	}
	if _, statErr := os.Stat(destination); statErr == nil {
		return gitRepositoryInvocation{}, fmt.Errorf("clone destination already exists: %s", destination)
	} else if !os.IsNotExist(statErr) {
		return gitRepositoryInvocation{}, fmt.Errorf("cannot inspect clone destination: %w", statErr)
	}
	parent := filepath.Dir(destination)
	if _, err := t.Policy.ResolveCreateParentPath(parent); err != nil {
		return gitRepositoryInvocation{}, err
	}
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return gitRepositoryInvocation{}, fmt.Errorf("create clone parent directory: %w", err)
	}
	temporary, err := os.MkdirTemp(parent, ".echocode-clone-*")
	if err != nil {
		return gitRepositoryInvocation{}, fmt.Errorf("create temporary clone directory: %w", err)
	}

	commandArgs := []string{"clone", "--progress"}
	displayArgs := []string{"clone", "--progress"}
	if args.Depth > 0 {
		commandArgs = append(commandArgs, "--depth", fmt.Sprintf("%d", args.Depth))
		displayArgs = append(displayArgs, "--depth", fmt.Sprintf("%d", args.Depth))
	}
	if args.Branch != "" {
		commandArgs = append(commandArgs, "--branch", args.Branch, "--single-branch")
		displayArgs = append(displayArgs, "--branch", args.Branch, "--single-branch")
	}
	commandArgs = append(commandArgs, "--", source, temporary)
	displayArgs = append(displayArgs, "--", displaySource, destination)
	installed := false
	return gitRepositoryInvocation{
		args: commandArgs, displayArgs: displayArgs, workingDirectory: parent, resultPath: destination,
		installClone: func() error {
			if _, err := os.Stat(destination); err == nil {
				return fmt.Errorf("clone destination was created while Git was running: %s", destination)
			} else if !os.IsNotExist(err) {
				return err
			}
			if err := os.Rename(temporary, destination); err != nil {
				return fmt.Errorf("install cloned repository: %w", err)
			}
			installed = true
			return nil
		},
		cleanup: func() {
			if !installed {
				_ = os.RemoveAll(temporary)
			}
		},
	}, nil
}

func (t GitRepositoryTool) prepareUpdate(args gitRepositoryArguments) (gitRepositoryInvocation, error) {
	repository := args.Repository
	if repository == "" {
		repository = "."
	}
	repository, err := t.Policy.ResolveWritePath(repository)
	if err != nil {
		return gitRepositoryInvocation{}, err
	}
	info, err := os.Stat(repository)
	if err != nil || !info.IsDir() {
		return gitRepositoryInvocation{}, fmt.Errorf("repository directory is unavailable: %s", repository)
	}
	remote := args.Remote
	if remote == "" {
		remote = "origin"
	}
	if err := validateGitRefArgument("remote", remote); err != nil {
		return gitRepositoryInvocation{}, err
	}
	if err := validateGitRefArgument("branch", args.Branch); err != nil {
		return gitRepositoryInvocation{}, err
	}

	commandArgs := []string{args.Action, "--progress"}
	if args.Action == "fetch" {
		if args.Prune {
			commandArgs = append(commandArgs, "--prune")
		}
	} else {
		strategy := args.Strategy
		if strategy == "" {
			strategy = "ff-only"
		}
		switch strategy {
		case "ff-only":
			commandArgs = append(commandArgs, "--ff-only")
		case "rebase":
			commandArgs = append(commandArgs, "--rebase")
		case "merge":
			commandArgs = append(commandArgs, "--no-rebase")
		default:
			return gitRepositoryInvocation{}, fmt.Errorf("unsupported pull strategy: %s", strategy)
		}
	}
	commandArgs = append(commandArgs, remote)
	if args.Branch != "" {
		commandArgs = append(commandArgs, args.Branch)
	}
	return gitRepositoryInvocation{
		args: commandArgs, displayArgs: append([]string(nil), commandArgs...),
		workingDirectory: repository, resultPath: repository,
	}, nil
}

func (t GitRepositoryTool) resolveRepositorySource(raw string) (source, display string, err error) {
	if filepath.IsAbs(raw) {
		resolved, resolveErr := t.Policy.ResolveReadPath(raw)
		if resolveErr != nil {
			return "", "", resolveErr
		}
		return resolved, resolved, nil
	}
	if strings.HasPrefix(strings.ToLower(raw), "file://") {
		parsed, parseErr := url.Parse(raw)
		if parseErr != nil {
			return "", "", fmt.Errorf("invalid local repository URL: %w", parseErr)
		}
		path, pathErr := url.PathUnescape(parsed.Path)
		if pathErr != nil {
			return "", "", pathErr
		}
		if parsed.Host != "" && !strings.EqualFold(parsed.Host, "localhost") {
			path = `\\` + parsed.Host + filepath.FromSlash(path)
		}
		path = filepath.FromSlash(path)
		resolved, resolveErr := t.Policy.ResolveReadPath(path)
		if resolveErr != nil {
			return "", "", resolveErr
		}
		return resolved, resolved, nil
	}
	if parsed, parseErr := url.Parse(raw); parseErr == nil && parsed.Scheme != "" {
		switch strings.ToLower(parsed.Scheme) {
		case "http", "https", "ssh", "git":
			if strings.TrimSpace(parsed.Host) == "" {
				return "", "", errors.New("repository URL host is required")
			}
			return raw, safeGitRepositoryURLForDisplay(raw), nil
		default:
			return "", "", fmt.Errorf("unsupported repository URL scheme: %s", parsed.Scheme)
		}
	}
	if isSCPStyleGitURL(raw) {
		return raw, raw, nil
	}
	return "", "", errors.New("repository URL must use HTTP(S), SSH, scp-style syntax, or an authorized absolute local path")
}

func validateGitRefArgument(label, value string) error {
	if strings.ContainsRune(value, '\x00') || strings.ContainsAny(value, "\r\n") {
		return fmt.Errorf("%s contains invalid characters", label)
	}
	if strings.HasPrefix(strings.TrimSpace(value), "-") {
		return fmt.Errorf("%s cannot start with '-'", label)
	}
	return nil
}

func isSCPStyleGitURL(value string) bool {
	if strings.ContainsAny(value, "\r\n\x00 ") || strings.HasPrefix(value, "-") {
		return false
	}
	at := strings.IndexByte(value, '@')
	colon := strings.IndexByte(value, ':')
	return at > 0 && colon > at+1 && colon < len(value)-1
}

func safeGitRepositoryURLForDisplay(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "[invalid repository URL]"
	}
	if parsed.Scheme == "" {
		return strings.TrimSpace(raw)
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func sanitizeGitRepositoryOutput(value, rawURL string) string {
	return strings.TrimSpace(redactKnownTransferURL(value, rawURL, safeGitRepositoryURLForDisplay(rawURL)))
}

func GitRepositoryInputForDisplay(rawArgs json.RawMessage) string {
	var args gitRepositoryArguments
	if json.Unmarshal(rawArgs, &args) != nil {
		return ""
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	switch action {
	case "clone":
		return strings.TrimSpace("git clone " + safeGitRepositoryURLForDisplay(args.URL) + " -> " + args.Destination)
	case "fetch", "pull":
		return strings.TrimSpace("git " + action + " " + firstNonEmptyString(args.Repository, "."))
	default:
		return strings.TrimSpace("git " + action)
	}
}

func gitRepositoryEnvironment() []string {
	overrides := map[string]bool{
		"GIT_TERMINAL_PROMPT": true,
		"GCM_INTERACTIVE":     true,
		"GIT_SSH_COMMAND":     true,
	}
	environment := make([]string, 0, len(os.Environ())+3)
	for _, item := range safeCommandEnvironment() {
		name, _, ok := strings.Cut(item, "=")
		if ok && overrides[strings.ToUpper(strings.TrimSpace(name))] {
			continue
		}
		environment = append(environment, item)
	}
	return append(environment,
		"GIT_TERMINAL_PROMPT=0",
		"GCM_INTERACTIVE=Never",
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes",
	)
}

func compactToolError(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown error"
	}
	lines := strings.Split(value, "\n")
	return strings.TrimSpace(lines[len(lines)-1])
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
