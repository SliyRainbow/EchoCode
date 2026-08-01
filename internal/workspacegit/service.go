package workspacegit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/pathutil"
)

const (
	commandTimeout         = 20 * time.Second
	worktreeCommandTimeout = 2 * time.Minute
	maxGitOutput           = 4 * 1024 * 1024
	maxDiffOutput          = 2 * 1024 * 1024
)

type Service struct{}

type Status struct {
	Available      bool         `json:"available"`
	RepositoryRoot string       `json:"repositoryRoot,omitempty"`
	Branch         string       `json:"branch,omitempty"`
	Upstream       string       `json:"upstream,omitempty"`
	Commit         string       `json:"commit,omitempty"`
	Ahead          int          `json:"ahead"`
	Behind         int          `json:"behind"`
	Clean          bool         `json:"clean"`
	Detached       bool         `json:"detached"`
	StagedCount    int          `json:"stagedCount"`
	ModifiedCount  int          `json:"modifiedCount"`
	UntrackedCount int          `json:"untrackedCount"`
	ConflictCount  int          `json:"conflictCount"`
	Files          []FileStatus `json:"files"`
	Branches       []Branch     `json:"branches"`
}

type FileStatus struct {
	Path           string `json:"path"`
	OriginalPath   string `json:"originalPath,omitempty"`
	IndexStatus    string `json:"indexStatus"`
	WorktreeStatus string `json:"worktreeStatus"`
	Staged         bool   `json:"staged"`
	Modified       bool   `json:"modified"`
	Untracked      bool   `json:"untracked"`
	Conflicted     bool   `json:"conflicted"`
}

type Branch struct {
	Name     string `json:"name"`
	Upstream string `json:"upstream,omitempty"`
	Current  bool   `json:"current"`
}

type Diff struct {
	Path      string `json:"path,omitempty"`
	Staged    bool   `json:"staged"`
	Patch     string `json:"patch"`
	Truncated bool   `json:"truncated"`
}

type Worktree struct {
	RepositoryRoot string `json:"repositoryRoot"`
	Path           string `json:"path"`
	Branch         string `json:"branch"`
}

type DiffOptions struct {
	Staged           bool
	IgnoreWhitespace bool
}

func (Service) Status(ctx context.Context, workspaceRoot string) (Status, error) {
	repoRoot, err := repositoryRoot(ctx, workspaceRoot)
	if err != nil {
		if errors.Is(err, ErrNotRepository) {
			return Status{Available: false, Clean: true, Files: []FileStatus{}, Branches: []Branch{}}, nil
		}
		return Status{}, err
	}

	raw, err := runGit(ctx, repoRoot, maxGitOutput, "status", "--porcelain=v2", "--branch", "-z")
	if err != nil {
		return Status{}, err
	}
	status := parseStatus(raw)
	status.Available = true
	status.RepositoryRoot = repoRoot
	status.Branches, err = listBranches(ctx, repoRoot)
	if err != nil {
		return Status{}, err
	}
	status.Clean = len(status.Files) == 0
	return status, nil
}

func (Service) Diff(ctx context.Context, workspaceRoot, path string, staged bool) (Diff, error) {
	return (Service{}).DiffWithOptions(ctx, workspaceRoot, path, DiffOptions{Staged: staged})
}

func (Service) DiffWithOptions(ctx context.Context, workspaceRoot, path string, options DiffOptions) (Diff, error) {
	repoRoot, err := repositoryRoot(ctx, workspaceRoot)
	if err != nil {
		return Diff{}, err
	}
	path, err = cleanRelativePath(path)
	if err != nil {
		return Diff{}, err
	}

	args := []string{"diff", "--no-ext-diff", "--no-color", "--unified=3"}
	if options.IgnoreWhitespace {
		args = append(args, "--ignore-all-space")
	}
	if options.Staged {
		args = append(args, "--cached")
	}
	if path != "" {
		args = append(args, "--", filepath.ToSlash(path))
	}
	raw, err := runGit(ctx, repoRoot, maxDiffOutput+1, args...)
	if err != nil {
		return Diff{}, err
	}

	if len(raw) == 0 && path != "" && !options.Staged {
		tracked, trackErr := runGit(ctx, repoRoot, 64*1024, "ls-files", "--error-unmatch", "--", filepath.ToSlash(path))
		if trackErr != nil && len(tracked) == 0 {
			untrackedArgs := []string{"diff", "--no-index", "--no-color", "--unified=3"}
			if options.IgnoreWhitespace {
				untrackedArgs = append(untrackedArgs, "--ignore-all-space")
			}
			untrackedArgs = append(untrackedArgs, "--", "/dev/null", filepath.ToSlash(path))
			raw, err = runGitAllowExitOne(ctx, repoRoot, maxDiffOutput+1, untrackedArgs...)
			if err != nil {
				return Diff{}, err
			}
		}
	}

	truncated := len(raw) > maxDiffOutput
	if truncated {
		raw = raw[:maxDiffOutput]
	}
	return Diff{Path: path, Staged: options.Staged, Patch: string(raw), Truncated: truncated}, nil
}

func (Service) Stage(ctx context.Context, workspaceRoot string, paths []string) (Status, error) {
	repoRoot, err := repositoryRoot(ctx, workspaceRoot)
	if err != nil {
		return Status{}, err
	}
	cleaned, err := cleanPaths(paths)
	if err != nil {
		return Status{}, err
	}
	args := []string{"add", "-A", "--"}
	if len(cleaned) == 0 {
		args = append(args, ".")
	} else {
		args = append(args, cleaned...)
	}
	if _, err := runGit(ctx, repoRoot, maxGitOutput, args...); err != nil {
		return Status{}, err
	}
	return (Service{}).Status(ctx, workspaceRoot)
}

func (Service) Unstage(ctx context.Context, workspaceRoot string, paths []string) (Status, error) {
	repoRoot, err := repositoryRoot(ctx, workspaceRoot)
	if err != nil {
		return Status{}, err
	}
	cleaned, err := cleanPaths(paths)
	if err != nil {
		return Status{}, err
	}
	args := []string{"restore", "--staged", "--"}
	if len(cleaned) == 0 {
		args = append(args, ".")
	} else {
		args = append(args, cleaned...)
	}
	if _, err := runGit(ctx, repoRoot, maxGitOutput, args...); err != nil {
		if _, headErr := runGit(ctx, repoRoot, 64*1024, "rev-parse", "--verify", "HEAD"); headErr == nil {
			return Status{}, err
		}
		args = []string{"rm", "--cached", "-r", "--ignore-unmatch", "--"}
		if len(cleaned) == 0 {
			args = append(args, ".")
		} else {
			args = append(args, cleaned...)
		}
		if _, fallbackErr := runGit(ctx, repoRoot, maxGitOutput, args...); fallbackErr != nil {
			return Status{}, fallbackErr
		}
	}
	return (Service{}).Status(ctx, workspaceRoot)
}

func (Service) Commit(ctx context.Context, workspaceRoot, message string) (Status, error) {
	message = strings.TrimSpace(message)
	if message == "" {
		return Status{}, errors.New("commit message is required")
	}
	if len(message) > 8000 {
		return Status{}, errors.New("commit message is too long")
	}
	repoRoot, err := repositoryRoot(ctx, workspaceRoot)
	if err != nil {
		return Status{}, err
	}
	if _, err := runGit(ctx, repoRoot, maxGitOutput, "commit", "-m", message); err != nil {
		return Status{}, err
	}
	return (Service{}).Status(ctx, workspaceRoot)
}

func (Service) CreateBranch(ctx context.Context, workspaceRoot, name string) (Status, error) {
	repoRoot, err := repositoryRoot(ctx, workspaceRoot)
	if err != nil {
		return Status{}, err
	}
	name, err = validateBranchName(ctx, repoRoot, name)
	if err != nil {
		return Status{}, err
	}
	if _, err := runGit(ctx, repoRoot, maxGitOutput, "switch", "-c", name); err != nil {
		return Status{}, err
	}
	return (Service{}).Status(ctx, workspaceRoot)
}

func (Service) SwitchBranch(ctx context.Context, workspaceRoot, name string) (Status, error) {
	repoRoot, err := repositoryRoot(ctx, workspaceRoot)
	if err != nil {
		return Status{}, err
	}
	name, err = validateBranchName(ctx, repoRoot, name)
	if err != nil {
		return Status{}, err
	}
	if _, err := runGit(ctx, repoRoot, maxGitOutput, "switch", name); err != nil {
		return Status{}, err
	}
	return (Service{}).Status(ctx, workspaceRoot)
}

// CreateWorktree creates a new branch from HEAD in a durable linked worktree.
func (Service) CreateWorktree(ctx context.Context, workspaceRoot, branchName, destination string) (Worktree, error) {
	repoRoot, err := repositoryRoot(ctx, workspaceRoot)
	if err != nil {
		return Worktree{}, err
	}
	branchName, err = validateBranchName(ctx, repoRoot, branchName)
	if err != nil {
		return Worktree{}, err
	}
	destination, err = validateWorktreeDestination(repoRoot, destination)
	if err != nil {
		return Worktree{}, err
	}
	existing, err := runGit(ctx, repoRoot, 64*1024, "for-each-ref", "--format=%(refname:short)", "refs/heads/"+branchName)
	if err != nil {
		return Worktree{}, err
	}
	if strings.TrimSpace(string(existing)) != "" {
		return Worktree{}, fmt.Errorf("branch already exists: %s", branchName)
	}
	if _, err := runGitWithTimeout(ctx, repoRoot, maxGitOutput, worktreeCommandTimeout, "worktree", "add", "-b", branchName, destination, "HEAD"); err != nil {
		return Worktree{}, err
	}
	info, err := os.Stat(destination)
	if err != nil || !info.IsDir() {
		return Worktree{}, fmt.Errorf("worktree was created but its directory is unavailable: %s", destination)
	}
	return Worktree{RepositoryRoot: repoRoot, Path: destination, Branch: branchName}, nil
}

var ErrNotRepository = errors.New("workspace is not a Git repository")

func repositoryRoot(ctx context.Context, workspaceRoot string) (string, error) {
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return "", errors.New("workspace root is required")
	}
	absWorkspace, err := pathutil.Canonical(workspaceRoot)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(absWorkspace)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("workspace root is unavailable: %s", absWorkspace)
	}
	raw, err := runGit(ctx, absWorkspace, 64*1024, "rev-parse", "--show-toplevel")
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "not a git repository") {
			return "", ErrNotRepository
		}
		return "", err
	}
	repoRoot := strings.TrimSpace(string(raw))
	if repoRoot == "" {
		return "", ErrNotRepository
	}
	repoRoot, err = pathutil.Canonical(repoRoot)
	if err != nil {
		return "", err
	}
	within, err := pathWithin(absWorkspace, repoRoot)
	if err != nil {
		return "", err
	}
	if !within {
		return "", fmt.Errorf("workspace must be the repository root or contain it: repository is %s", repoRoot)
	}
	return repoRoot, nil
}

func pathWithin(root, target string) (bool, error) {
	return pathutil.Within(root, target)
}

func cleanRelativePath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", nil
	}
	if filepath.IsAbs(path) {
		return "", errors.New("Git path must be relative to the repository")
	}
	cleaned := filepath.Clean(filepath.FromSlash(path))
	if cleaned == "." {
		return "", nil
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", errors.New("Git path escapes the repository")
	}
	return filepath.ToSlash(cleaned), nil
}

func cleanPaths(paths []string) ([]string, error) {
	cleaned := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, path := range paths {
		path, err := cleanRelativePath(path)
		if err != nil {
			return nil, err
		}
		if path == "" || seen[path] {
			continue
		}
		seen[path] = true
		cleaned = append(cleaned, path)
	}
	return cleaned, nil
}

func validateBranchName(ctx context.Context, repoRoot, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("branch name is required")
	}
	if _, err := runGit(ctx, repoRoot, 64*1024, "check-ref-format", "--branch", name); err != nil {
		return "", fmt.Errorf("invalid branch name %q: %w", name, err)
	}
	return name, nil
}

func validateWorktreeDestination(repoRoot, destination string) (string, error) {
	destination = strings.TrimSpace(destination)
	if destination == "" {
		return "", errors.New("worktree destination is required")
	}
	absDestination, err := pathutil.Canonical(destination)
	if err != nil {
		return "", err
	}
	absDestination = filepath.Clean(absDestination)
	if samePath(absDestination, repoRoot) {
		return "", errors.New("worktree destination must differ from the repository root")
	}
	insideRepository, err := pathWithin(repoRoot, absDestination)
	if err != nil {
		return "", err
	}
	if insideRepository {
		return "", errors.New("worktree destination must be outside the source repository")
	}
	if _, err := os.Stat(absDestination); err == nil {
		return "", fmt.Errorf("worktree destination already exists: %s", absDestination)
	} else if !os.IsNotExist(err) {
		return "", err
	}
	parent := filepath.Dir(absDestination)
	info, err := os.Stat(parent)
	if err != nil || !info.IsDir() {
		return "", fmt.Errorf("worktree parent directory is unavailable: %s", parent)
	}
	return absDestination, nil
}

func samePath(left, right string) bool {
	leftWithinRight, err := pathutil.Within(left, right)
	if err != nil || !leftWithinRight {
		return false
	}
	rightWithinLeft, err := pathutil.Within(right, left)
	return err == nil && rightWithinLeft
}

func listBranches(ctx context.Context, repoRoot string) ([]Branch, error) {
	raw, err := runGit(ctx, repoRoot, maxGitOutput, "for-each-ref", "--format=%(refname:short)%09%(upstream:short)%09%(HEAD)", "refs/heads")
	if err != nil {
		return nil, err
	}
	branches := []Branch{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := strings.SplitN(strings.TrimSuffix(line, "\r"), "\t", 3)
		branch := Branch{Name: fields[0]}
		if len(fields) > 1 {
			branch.Upstream = fields[1]
		}
		if len(fields) > 2 {
			branch.Current = strings.TrimSpace(fields[2]) == "*"
		}
		branches = append(branches, branch)
	}
	return branches, nil
}

func parseStatus(raw []byte) Status {
	status := Status{Files: []FileStatus{}, Branches: []Branch{}}
	records := bytes.Split(raw, []byte{0})
	for index := 0; index < len(records); index++ {
		record := string(records[index])
		if record == "" {
			continue
		}
		if strings.HasPrefix(record, "# ") {
			parseBranchHeader(&status, record)
			continue
		}

		file := FileStatus{}
		switch record[0] {
		case '?':
			file.Path = strings.TrimPrefix(record, "? ")
			file.IndexStatus = "?"
			file.WorktreeStatus = "?"
			file.Untracked = true
			file.Modified = true
		case '1':
			fields := strings.SplitN(record, " ", 9)
			if len(fields) < 9 {
				continue
			}
			file = fileStatus(fields[1], fields[8])
		case '2':
			fields := strings.SplitN(record, " ", 10)
			if len(fields) < 10 {
				continue
			}
			file = fileStatus(fields[1], fields[9])
			if index+1 < len(records) {
				index++
				file.OriginalPath = string(records[index])
			}
		case 'u':
			fields := strings.SplitN(record, " ", 11)
			if len(fields) < 11 {
				continue
			}
			file = fileStatus(fields[1], fields[10])
			file.Conflicted = true
		default:
			continue
		}
		status.Files = append(status.Files, file)
	}

	for _, file := range status.Files {
		if file.Staged {
			status.StagedCount++
		}
		if file.Modified && !file.Untracked {
			status.ModifiedCount++
		}
		if file.Untracked {
			status.UntrackedCount++
		}
		if file.Conflicted {
			status.ConflictCount++
		}
	}
	sort.SliceStable(status.Files, func(i, j int) bool { return status.Files[i].Path < status.Files[j].Path })
	return status
}

func fileStatus(code, path string) FileStatus {
	if len(code) < 2 {
		code += "."
	}
	indexStatus := string(code[0])
	worktreeStatus := string(code[1])
	return FileStatus{
		Path:           path,
		IndexStatus:    indexStatus,
		WorktreeStatus: worktreeStatus,
		Staged:         indexStatus != "." && indexStatus != " ",
		Modified:       worktreeStatus != "." && worktreeStatus != " ",
		Conflicted:     strings.Contains(code, "U") || code == "AA" || code == "DD",
	}
}

func parseBranchHeader(status *Status, record string) {
	value := func(prefix string) string { return strings.TrimSpace(strings.TrimPrefix(record, prefix)) }
	switch {
	case strings.HasPrefix(record, "# branch.oid "):
		status.Commit = value("# branch.oid ")
	case strings.HasPrefix(record, "# branch.head "):
		status.Branch = value("# branch.head ")
		status.Detached = status.Branch == "(detached)"
	case strings.HasPrefix(record, "# branch.upstream "):
		status.Upstream = value("# branch.upstream ")
	case strings.HasPrefix(record, "# branch.ab "):
		fields := strings.Fields(value("# branch.ab "))
		for _, field := range fields {
			if strings.HasPrefix(field, "+") {
				status.Ahead, _ = strconv.Atoi(strings.TrimPrefix(field, "+"))
			}
			if strings.HasPrefix(field, "-") {
				status.Behind, _ = strconv.Atoi(strings.TrimPrefix(field, "-"))
			}
		}
	}
}

func runGit(ctx context.Context, repoRoot string, limit int, args ...string) ([]byte, error) {
	return runGitWithExitPolicy(ctx, repoRoot, limit, false, args...)
}

func runGitAllowExitOne(ctx context.Context, repoRoot string, limit int, args ...string) ([]byte, error) {
	return runGitWithExitPolicy(ctx, repoRoot, limit, true, args...)
}

func runGitWithExitPolicy(ctx context.Context, repoRoot string, limit int, allowExitOne bool, args ...string) ([]byte, error) {
	return runGitWithExitPolicyTimeout(ctx, repoRoot, limit, allowExitOne, commandTimeout, args...)
}

func runGitWithTimeout(ctx context.Context, repoRoot string, limit int, timeout time.Duration, args ...string) ([]byte, error) {
	return runGitWithExitPolicyTimeout(ctx, repoRoot, limit, false, timeout, args...)
}

func runGitWithExitPolicyTimeout(ctx context.Context, repoRoot string, limit int, allowExitOne bool, timeout time.Duration, args ...string) ([]byte, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return nil, errors.New("Git executable was not found")
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(runCtx, "git", args...)
	configureCommand(cmd)
	cmd.Dir = repoRoot
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if runCtx.Err() == context.DeadlineExceeded {
		return nil, errors.New("Git command timed out")
	}
	if err != nil {
		if allowExitOne {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
				return stdout.Bytes(), nil
			}
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return stdout.Bytes(), fmt.Errorf("git %s failed: %s", strings.Join(args, " "), detail)
	}
	if limit > 0 && stdout.Len() > limit {
		return stdout.Bytes()[:limit], nil
	}
	return stdout.Bytes(), nil
}
