package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	defaultStructuredSearchLimit       = 200
	maximumStructuredSearchLimit       = 500
	defaultStructuredSearchTimeout     = 10 * time.Second
	maximumStructuredSearchTimeout     = 2 * time.Minute
	defaultStructuredSearchSnippetSize = 300
	maximumStructuredSearchSnippetSize = 2000
	maximumStructuredSearchFileSize    = 1024 * 1024
	maximumRGJSONLineSize              = 8 * 1024 * 1024
	maximumStructuredSearchQuerySize   = 64 * 1024
	maximumStructuredSearchGlobSize    = 4096
	maximumStructuredSearchGlobCount   = 32
)

var defaultSearchExcludedDirectories = map[string]struct{}{
	".git":         {},
	".cache":       {},
	"build":        {},
	"dist":         {},
	"node_modules": {},
	"vendor":       {},
}

// GrepMatch is one stable, workspace-addressable text match.
type GrepMatch struct {
	Path             string `json:"path"`
	Line             int    `json:"line"`
	Column           int    `json:"column"`
	Snippet          string `json:"snippet"`
	SnippetTruncated bool   `json:"snippet_truncated"`
}

// GrepOutput is the machine-readable payload returned by GrepTool.
type GrepOutput struct {
	Query        string      `json:"query"`
	Root         string      `json:"root"`
	Engine       string      `json:"engine"`
	Matches      []GrepMatch `json:"matches"`
	Count        int         `json:"count"`
	Truncated    bool        `json:"truncated"`
	TimedOut     bool        `json:"timed_out"`
	SkippedFiles int         `json:"skipped_files"`
	Error        string      `json:"error,omitempty"`
}

// GlobEntry is one path returned by GlobTool.
type GlobEntry struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int64  `json:"size"`
}

// GlobOutput is the machine-readable payload returned by GlobTool.
type GlobOutput struct {
	Pattern   string      `json:"pattern"`
	Root      string      `json:"root"`
	Entries   []GlobEntry `json:"entries"`
	Count     int         `json:"count"`
	Truncated bool        `json:"truncated"`
	TimedOut  bool        `json:"timed_out"`
	Error     string      `json:"error,omitempty"`
}

type structuredSearchArguments struct {
	Path            string   `json:"path"`
	Query           string   `json:"query"`
	Regex           bool     `json:"regex"`
	CaseSensitive   bool     `json:"case_sensitive"`
	IncludeGlobs    []string `json:"include_globs"`
	ExcludeGlobs    []string `json:"exclude_globs"`
	IncludeHidden   bool     `json:"include_hidden"`
	IncludeIgnored  bool     `json:"include_ignored"`
	MaxResults      int      `json:"max_results"`
	MaxSnippetChars int      `json:"max_snippet_chars"`
	TimeoutMS       int      `json:"timeout_ms"`
}

// GrepTool searches text without routing file inspection through a shell.
type GrepTool struct {
	Policy         SandboxPolicy
	lookPath       func(string) (string, error)
	commandFactory func(context.Context, string, ...string) *exec.Cmd
}

func (t GrepTool) Name() string { return "grep" }

func (t GrepTool) Description() string {
	return "Search text files inside an authorized workspace path. Returns stable JSON matches with path, line, column, snippet, truncation, timeout, and engine metadata. Uses ripgrep when available and a built-in reader otherwise; never constructs a shell command."
}

func (t GrepTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"query":             map[string]any{"type": "string", "description": "Text or regular expression to find"},
			"path":              map[string]any{"type": "string", "description": "Authorized file or directory; defaults to the workspace root"},
			"regex":             map[string]any{"type": "boolean", "description": "Interpret query as a regular expression"},
			"case_sensitive":    map[string]any{"type": "boolean"},
			"include_globs":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": maximumStructuredSearchGlobCount, "description": "Optional globs relative to the selected search path"},
			"exclude_globs":     map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": maximumStructuredSearchGlobCount, "description": "Optional exclusion globs relative to the selected search path"},
			"include_hidden":    map[string]any{"type": "boolean"},
			"include_ignored":   map[string]any{"type": "boolean", "description": "Include normally skipped dependency and build directories"},
			"max_results":       map[string]any{"type": "integer", "minimum": 1, "maximum": maximumStructuredSearchLimit},
			"max_snippet_chars": map[string]any{"type": "integer", "minimum": 40, "maximum": maximumStructuredSearchSnippetSize},
			"timeout_ms":        map[string]any{"type": "integer", "minimum": 1, "maximum": int(maximumStructuredSearchTimeout / time.Millisecond)},
		},
		"required": []string{"query"},
	}
}

func (t GrepTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args structuredSearchArguments
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("invalid grep arguments: " + err.Error()), nil
	}
	if args.Query == "" {
		return errorResult("query cannot be empty"), nil
	}
	if len(args.Query) > maximumStructuredSearchQuerySize {
		return errorResult("query is too large"), nil
	}
	if strings.ContainsRune(args.Query, '\x00') {
		return errorResult("query cannot contain a NUL byte"), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}
	root, err := resolveStructuredSearchRoot(t.Policy, args.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	include, err := compileGlobPatterns(args.IncludeGlobs)
	if err != nil {
		return errorResult("invalid include_globs: " + err.Error()), nil
	}
	exclude, err := compileGlobPatterns(args.ExcludeGlobs)
	if err != nil {
		return errorResult("invalid exclude_globs: " + err.Error()), nil
	}
	limit := normalizeStructuredSearchLimit(args.MaxResults)
	snippetLimit := normalizeSnippetLimit(args.MaxSnippetChars)
	runCtx, cancel := context.WithTimeout(ctx, normalizeStructuredSearchTimeout(args.TimeoutMS))
	defer cancel()

	output := GrepOutput{
		Query:   args.Query,
		Root:    stableSearchPath(t.Policy, root),
		Matches: make([]GrepMatch, 0),
	}
	if !t.Policy.TaskScopeEnabled {
		if rgOutput, handled, rgErr := t.executeRipgrep(runCtx, root, args, limit, snippetLimit); handled {
			rgOutput.Query = args.Query
			rgOutput.Root = output.Root
			return buildGrepResult(t.Name(), args.Query, rgOutput, rgErr), nil
		}
	}

	output.Engine = "go"
	fallbackErr := grepWithGo(runCtx, t.Policy, root, args, include, exclude, limit, snippetLimit, &output)
	return buildGrepResult(t.Name(), args.Query, output, fallbackErr), nil
}

// GlobTool enumerates matching workspace paths without invoking a shell.
type GlobTool struct{ Policy SandboxPolicy }

func (t GlobTool) Name() string { return "glob" }

func (t GlobTool) Description() string {
	return "Find files or directories inside an authorized workspace path. Supports ** globs and returns stable JSON path/type/size entries with truncation and timeout metadata."
}

func (t GlobTool) InputSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"pattern":         map[string]any{"type": "string", "description": "Glob relative to the selected search path, such as **/*.go or internal/**"},
			"path":            map[string]any{"type": "string", "description": "Authorized directory to search; defaults to the workspace root"},
			"kind":            map[string]any{"type": "string", "enum": []string{"any", "file", "directory"}},
			"exclude_globs":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "maxItems": maximumStructuredSearchGlobCount},
			"include_hidden":  map[string]any{"type": "boolean"},
			"include_ignored": map[string]any{"type": "boolean", "description": "Include normally skipped dependency and build directories"},
			"max_results":     map[string]any{"type": "integer", "minimum": 1, "maximum": maximumStructuredSearchLimit},
			"timeout_ms":      map[string]any{"type": "integer", "minimum": 1, "maximum": int(maximumStructuredSearchTimeout / time.Millisecond)},
		},
		"required": []string{"pattern"},
	}
}

func (t GlobTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args struct {
		Pattern        string   `json:"pattern"`
		Path           string   `json:"path"`
		Kind           string   `json:"kind"`
		ExcludeGlobs   []string `json:"exclude_globs"`
		IncludeHidden  bool     `json:"include_hidden"`
		IncludeIgnored bool     `json:"include_ignored"`
		MaxResults     int      `json:"max_results"`
		TimeoutMS      int      `json:"timeout_ms"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("invalid glob arguments: " + err.Error()), nil
	}
	matcher, err := compileGlobPattern(args.Pattern)
	if err != nil {
		return errorResult("invalid pattern: " + err.Error()), nil
	}
	exclude, err := compileGlobPatterns(args.ExcludeGlobs)
	if err != nil {
		return errorResult("invalid exclude_globs: " + err.Error()), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}
	root, err := resolveStructuredSearchRoot(t.Policy, args.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return errorResult("glob path must be a readable directory"), nil
	}
	kind := strings.ToLower(strings.TrimSpace(args.Kind))
	if kind == "" {
		kind = "any"
	}
	if kind != "any" && kind != "file" && kind != "directory" {
		return errorResult("kind must be any, file, or directory"), nil
	}

	runCtx, cancel := context.WithTimeout(ctx, normalizeStructuredSearchTimeout(args.TimeoutMS))
	defer cancel()
	output := GlobOutput{
		Pattern: args.Pattern,
		Root:    stableSearchPath(t.Policy, root),
		Entries: make([]GlobEntry, 0),
	}
	limit := normalizeStructuredSearchLimit(args.MaxResults)
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := runCtx.Err(); err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if entry.IsDir() && !t.Policy.TaskScopeAllowsTraversal(path) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && !t.Policy.TaskScopeAllowsPath(path) {
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() && shouldSkipSearchDirectory(entry.Name(), args.IncludeHidden, args.IncludeIgnored) {
			return filepath.SkipDir
		}
		if !args.IncludeHidden && strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		relative, relErr := filepath.Rel(root, path)
		if relErr != nil {
			return nil
		}
		relative = filepath.ToSlash(relative)
		if !matcher.Match(relative) || matchesAnyGlob(exclude, relative) {
			return nil
		}
		entryKind := "file"
		if entry.IsDir() {
			entryKind = "directory"
		}
		if kind != "any" && kind != entryKind {
			return nil
		}
		entryInfo, infoErr := entry.Info()
		if infoErr != nil {
			return nil
		}
		output.Entries = append(output.Entries, GlobEntry{
			Path: stableSearchPath(t.Policy, path),
			Type: entryKind,
			Size: entryInfo.Size(),
		})
		if len(output.Entries) >= limit {
			output.Truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		output.TimedOut = true
		output.Truncated = true
		walkErr = errors.New("glob timed out")
	} else if errors.Is(runCtx.Err(), context.Canceled) {
		output.Truncated = true
		walkErr = errors.New("glob cancelled")
	} else if errors.Is(walkErr, filepath.SkipAll) {
		walkErr = nil
	}
	sort.Slice(output.Entries, func(i, j int) bool { return output.Entries[i].Path < output.Entries[j].Path })
	output.Count = len(output.Entries)
	return buildGlobResult(t.Name(), args.Pattern, output, walkErr), nil
}

func (t GrepTool) executeRipgrep(ctx context.Context, root string, args structuredSearchArguments, limit, snippetLimit int) (GrepOutput, bool, error) {
	lookPath := t.lookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	rgPath, err := lookPath("rg")
	if err != nil {
		return GrepOutput{}, false, nil
	}
	info, err := os.Stat(root)
	if err != nil {
		return GrepOutput{}, true, err
	}
	workingDirectory := root
	target := "."
	if !info.IsDir() {
		workingDirectory = filepath.Dir(root)
		target = filepath.Base(root)
	}
	rgArgs := []string{"--json", "--no-config", "--color=never", "--line-number", "--column", "--max-columns=4096", "--max-columns-preview", "--max-filesize=1M"}
	if args.Regex {
		rgArgs = append(rgArgs, "--regexp", args.Query)
	} else {
		rgArgs = append(rgArgs, "--fixed-strings", "--regexp", args.Query)
	}
	if args.CaseSensitive {
		rgArgs = append(rgArgs, "--case-sensitive")
	} else {
		rgArgs = append(rgArgs, "--ignore-case")
	}
	if args.IncludeHidden {
		rgArgs = append(rgArgs, "--hidden")
	}
	if args.IncludeIgnored {
		rgArgs = append(rgArgs, "--no-ignore")
	}
	for _, pattern := range args.IncludeGlobs {
		rgArgs = append(rgArgs, "--glob", filepath.ToSlash(pattern))
	}
	for _, pattern := range args.ExcludeGlobs {
		rgArgs = append(rgArgs, "--glob", "!"+filepath.ToSlash(pattern))
	}
	if !args.IncludeIgnored {
		for _, name := range sortedExcludedDirectoryNames() {
			rgArgs = append(rgArgs, "--glob", "!**/"+name+"/**")
		}
	}
	rgArgs = append(rgArgs, "--", target)

	factory := t.commandFactory
	if factory == nil {
		factory = exec.CommandContext
	}
	commandCtx, stopCommand := context.WithCancel(ctx)
	defer stopCommand()
	cmd := factory(commandCtx, rgPath, rgArgs...)
	cmd.Dir = workingDirectory
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return GrepOutput{}, true, err
	}
	var stderr cappedBuffer
	stderr.limit = 32 * 1024
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return GrepOutput{}, false, nil
	}

	output := GrepOutput{Engine: "rg", Matches: make([]GrepMatch, 0)}
	limitReached := false
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64*1024), maximumRGJSONLineSize)
	for scanner.Scan() {
		var event ripgrepEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			stopCommand()
			_ = cmd.Wait()
			return output, true, fmt.Errorf("parse ripgrep output: %w", err)
		}
		if event.Type != "match" {
			continue
		}
		match, ok := ripgrepMatchFromEvent(event, workingDirectory, root, t.Policy, snippetLimit)
		if !ok {
			continue
		}
		output.Matches = append(output.Matches, match)
		if len(output.Matches) >= limit {
			output.Truncated = true
			limitReached = true
			stopCommand()
			break
		}
	}
	if scanErr := scanner.Err(); scanErr != nil && !limitReached {
		stopCommand()
		_ = cmd.Wait()
		return output, true, fmt.Errorf("read ripgrep output: %w", scanErr)
	}
	waitErr := cmd.Wait()
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		output.TimedOut = true
		output.Truncated = true
		return output, true, errors.New("grep timed out")
	}
	if errors.Is(ctx.Err(), context.Canceled) && !limitReached {
		output.Truncated = true
		return output, true, errors.New("grep cancelled")
	}
	if limitReached {
		sortGrepMatches(output.Matches)
		output.Count = len(output.Matches)
		return output, true, nil
	}
	if waitErr != nil {
		var exitError *exec.ExitError
		if errors.As(waitErr, &exitError) && exitError.ExitCode() == 1 {
			waitErr = nil
		} else {
			message := strings.TrimSpace(stderr.String())
			if message == "" {
				message = waitErr.Error()
			}
			return output, true, errors.New(message)
		}
	}
	sortGrepMatches(output.Matches)
	output.Count = len(output.Matches)
	return output, true, nil
}

func grepWithGo(ctx context.Context, policy SandboxPolicy, root string, args structuredSearchArguments, include, exclude []*globPattern, limit, snippetLimit int, output *GrepOutput) error {
	var matcher *regexp.Regexp
	if args.Regex {
		pattern := args.Query
		if !args.CaseSensitive {
			pattern = "(?i:" + pattern + ")"
		}
		compiled, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("invalid regular expression: %w", err)
		}
		matcher = compiled
	} else if !args.CaseSensitive {
		matcher = regexp.MustCompile("(?i:" + regexp.QuoteMeta(args.Query) + ")")
	}
	rootInfo, rootInfoErr := os.Stat(root)
	rootIsDirectory := rootInfoErr == nil && rootInfo.IsDir()
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.IsDir() {
			if !policy.TaskScopeAllowsTraversal(path) {
				return filepath.SkipDir
			}
			if path != root && shouldSkipSearchDirectory(entry.Name(), args.IncludeHidden, args.IncludeIgnored) {
				return filepath.SkipDir
			}
			return nil
		}
		if !policy.TaskScopeAllowsPath(path) {
			return nil
		}
		if !args.IncludeHidden && strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		relative := filepath.Base(path)
		if rootIsDirectory {
			if rel, relErr := filepath.Rel(root, path); relErr == nil {
				relative = filepath.ToSlash(rel)
			}
		}
		if len(include) > 0 && !matchesAnyGlob(include, relative) {
			return nil
		}
		if matchesAnyGlob(exclude, relative) {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > maximumStructuredSearchFileSize {
			output.SkippedFiles++
			return nil
		}
		text, err := ReadFileText(path)
		if err != nil || text.Binary {
			output.SkippedFiles++
			return nil
		}
		for lineIndex, line := range splitTextLines(text.Content) {
			location := findSearchLocation(line, args.Query, matcher, args.CaseSensitive)
			if location < 0 {
				continue
			}
			snippet, truncated := truncateSearchSnippet(line, location, snippetLimit)
			output.Matches = append(output.Matches, GrepMatch{
				Path:             stableSearchPath(policy, path),
				Line:             lineIndex + 1,
				Column:           utf8.RuneCountInString(line[:location]) + 1,
				Snippet:          snippet,
				SnippetTruncated: truncated,
			})
			if len(output.Matches) >= limit {
				output.Truncated = true
				return filepath.SkipAll
			}
		}
		return nil
	})
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		output.TimedOut = true
		output.Truncated = true
		return errors.New("grep timed out")
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		output.Truncated = true
		return errors.New("grep cancelled")
	}
	if walkErr != nil && !errors.Is(walkErr, filepath.SkipAll) {
		return walkErr
	}
	sortGrepMatches(output.Matches)
	output.Count = len(output.Matches)
	return nil
}

type ripgrepText struct {
	Text  string `json:"text"`
	Bytes string `json:"bytes"`
}

func (t ripgrepText) value() (string, error) {
	if t.Bytes == "" {
		return t.Text, nil
	}
	decoded, err := base64.StdEncoding.DecodeString(t.Bytes)
	return string(decoded), err
}

type ripgrepSubmatch struct {
	Start int `json:"start"`
}

type ripgrepEvent struct {
	Type string `json:"type"`
	Data struct {
		Path       ripgrepText       `json:"path"`
		Lines      ripgrepText       `json:"lines"`
		LineNumber int               `json:"line_number"`
		Submatches []ripgrepSubmatch `json:"submatches"`
	} `json:"data"`
}

func ripgrepMatchFromEvent(event ripgrepEvent, workingDirectory, root string, policy SandboxPolicy, snippetLimit int) (GrepMatch, bool) {
	pathText, err := event.Data.Path.value()
	if err != nil || pathText == "" || len(event.Data.Submatches) == 0 {
		return GrepMatch{}, false
	}
	absPath := pathText
	if !filepath.IsAbs(absPath) {
		absPath = filepath.Join(workingDirectory, filepath.FromSlash(pathText))
	}
	absPath, err = filepath.Abs(absPath)
	if err != nil || !pathWithinRoot(filepath.Clean(absPath), filepath.Clean(root)) {
		return GrepMatch{}, false
	}
	if !policy.TaskScopeAllowsPath(absPath) {
		return GrepMatch{}, false
	}
	line, err := event.Data.Lines.value()
	if err != nil {
		return GrepMatch{}, false
	}
	line = strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	byteColumn := event.Data.Submatches[0].Start
	if byteColumn < 0 || byteColumn > len(line) {
		return GrepMatch{}, false
	}
	snippet, truncated := truncateSearchSnippet(line, byteColumn, snippetLimit)
	return GrepMatch{
		Path:             stableSearchPath(policy, absPath),
		Line:             event.Data.LineNumber,
		Column:           utf8.RuneCountInString(line[:byteColumn]) + 1,
		Snippet:          snippet,
		SnippetTruncated: truncated,
	}, true
}

func resolveStructuredSearchRoot(policy SandboxPolicy, input string) (string, error) {
	root, err := policy.ResolveReadPath(input)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("cannot access search path: %w", err)
	}
	resolved = normalizeResolvedSearchRoot(root, resolved)
	resolved, err = policy.ResolveReadPath(resolved)
	if err != nil {
		return "", fmt.Errorf("resolved search path is outside the allowed roots: %w", err)
	}
	return filepath.Clean(resolved), nil
}

func normalizeResolvedSearchRoot(original, resolved string) string {
	resolved = filepath.Clean(resolved)
	if filepath.IsAbs(resolved) {
		return resolved
	}
	return filepath.Clean(original)
}

func stableSearchPath(policy SandboxPolicy, absolutePath string) string {
	workspace, workspaceErr := filepath.Abs(policy.WorkspaceRoot)
	if workspaceErr == nil {
		if relative, ok := relativeSearchDisplayPath(workspace, absolutePath); ok {
			return relative
		}
		canonicalWorkspace, canonicalWorkspaceErr := canonicalSearchDisplayPath(workspace)
		canonicalTarget, canonicalTargetErr := canonicalSearchDisplayPath(absolutePath)
		if canonicalWorkspaceErr == nil && canonicalTargetErr == nil {
			if relative, ok := relativeSearchDisplayPath(canonicalWorkspace, canonicalTarget); ok {
				return relative
			}
		}
	}
	return filepath.ToSlash(filepath.Clean(absolutePath))
}

func relativeSearchDisplayPath(workspace, target string) (string, bool) {
	relative, err := filepath.Rel(workspace, target)
	if err != nil || !searchRelativePathStaysWithinRoot(relative) {
		return "", false
	}
	if relative == "." {
		return ".", true
	}
	return filepath.ToSlash(relative), true
}

func canonicalSearchDisplayPath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return filepath.Clean(absolute), nil
	}
	return normalizeResolvedSearchRoot(absolute, resolved), nil
}

func searchRelativePathStaysWithinRoot(relative string) bool {
	relative = filepath.Clean(relative)
	return !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func normalizeStructuredSearchLimit(value int) int {
	if value <= 0 {
		return defaultStructuredSearchLimit
	}
	if value > maximumStructuredSearchLimit {
		return maximumStructuredSearchLimit
	}
	return value
}

func normalizeSnippetLimit(value int) int {
	if value <= 0 {
		return defaultStructuredSearchSnippetSize
	}
	if value < 40 {
		return 40
	}
	if value > maximumStructuredSearchSnippetSize {
		return maximumStructuredSearchSnippetSize
	}
	return value
}

func normalizeStructuredSearchTimeout(milliseconds int) time.Duration {
	if milliseconds <= 0 {
		return defaultStructuredSearchTimeout
	}
	duration := time.Duration(milliseconds) * time.Millisecond
	if duration > maximumStructuredSearchTimeout {
		return maximumStructuredSearchTimeout
	}
	return duration
}

func findSearchLocation(line, query string, matcher *regexp.Regexp, caseSensitive bool) int {
	if matcher != nil {
		location := matcher.FindStringIndex(line)
		if location == nil {
			return -1
		}
		return location[0]
	}
	if caseSensitive {
		return strings.Index(line, query)
	}
	return -1
}

func truncateSearchSnippet(value string, matchByteOffset, limit int) (string, bool) {
	runes := []rune(value)
	if len(runes) <= limit {
		return value, false
	}
	if matchByteOffset < 0 {
		matchByteOffset = 0
	}
	if matchByteOffset > len(value) {
		matchByteOffset = len(value)
	}
	matchRuneOffset := utf8.RuneCountInString(value[:matchByteOffset])
	start := matchRuneOffset - limit/3
	if start < 0 {
		start = 0
	}
	end := start + limit
	if end > len(runes) {
		end = len(runes)
		start = end - limit
	}
	prefix := ""
	suffix := ""
	if start > 0 {
		prefix = "..."
	}
	if end < len(runes) {
		suffix = "..."
	}
	return prefix + string(runes[start:end]) + suffix, true
}

func sortGrepMatches(matches []GrepMatch) {
	sort.Slice(matches, func(i, j int) bool {
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		if matches[i].Line != matches[j].Line {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Column < matches[j].Column
	})
}

func shouldSkipSearchDirectory(name string, includeHidden, includeIgnored bool) bool {
	if !includeHidden && strings.HasPrefix(name, ".") {
		return true
	}
	if includeIgnored {
		return false
	}
	_, skip := defaultSearchExcludedDirectories[name]
	return skip
}

func sortedExcludedDirectoryNames() []string {
	names := make([]string, 0, len(defaultSearchExcludedDirectories))
	for name := range defaultSearchExcludedDirectories {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

type globPattern struct{ expression *regexp.Regexp }

func (p *globPattern) Match(relativePath string) bool {
	return p != nil && p.expression.MatchString(filepath.ToSlash(relativePath))
}

func compileGlobPatterns(patterns []string) ([]*globPattern, error) {
	if len(patterns) > maximumStructuredSearchGlobCount {
		return nil, fmt.Errorf("at most %d glob patterns are allowed", maximumStructuredSearchGlobCount)
	}
	compiled := make([]*globPattern, 0, len(patterns))
	for _, pattern := range patterns {
		item, err := compileGlobPattern(pattern)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, item)
	}
	return compiled, nil
}

func compileGlobPattern(pattern string) (*globPattern, error) {
	normalized := filepath.ToSlash(strings.TrimSpace(pattern))
	normalized = strings.TrimPrefix(normalized, "./")
	if normalized == "" {
		return nil, errors.New("glob cannot be empty")
	}
	if len(normalized) > maximumStructuredSearchGlobSize {
		return nil, errors.New("glob is too large")
	}
	if strings.ContainsRune(normalized, '\x00') {
		return nil, errors.New("glob cannot contain a NUL byte")
	}
	if strings.HasPrefix(normalized, "!") {
		return nil, errors.New("glob cannot start with !; use exclude_globs for exclusions")
	}
	if filepath.IsAbs(pattern) || looksLikeForeignAbsolutePath(pattern) || strings.HasPrefix(normalized, "/") {
		return nil, errors.New("glob must be relative to the selected search path")
	}
	for _, segment := range strings.Split(normalized, "/") {
		if segment == ".." {
			return nil, errors.New("glob cannot traverse to a parent directory")
		}
	}

	var expression strings.Builder
	if runtime.GOOS == "windows" {
		expression.WriteString("(?i)")
	}
	expression.WriteString("^")
	if !strings.Contains(normalized, "/") {
		expression.WriteString("(?:.*/)?")
	}
	for index := 0; index < len(normalized); {
		character := normalized[index]
		switch character {
		case '*':
			if index+1 < len(normalized) && normalized[index+1] == '*' {
				index += 2
				for index < len(normalized) && normalized[index] == '*' {
					index++
				}
				if index < len(normalized) && normalized[index] == '/' {
					expression.WriteString("(?:.*/)?")
					index++
				} else {
					expression.WriteString(".*")
				}
				continue
			}
			expression.WriteString("[^/]*")
			index++
		case '?':
			expression.WriteString("[^/]")
			index++
		case '[':
			end := strings.IndexByte(normalized[index+1:], ']')
			if end < 0 {
				return nil, errors.New("unterminated character class")
			}
			end += index + 1
			class := normalized[index+1 : end]
			if class == "" || strings.Contains(class, "/") {
				return nil, errors.New("invalid character class")
			}
			expression.WriteByte('[')
			if class[0] == '!' {
				expression.WriteByte('^')
				class = class[1:]
			}
			if class == "" {
				return nil, errors.New("invalid character class")
			}
			for classIndex := 0; classIndex < len(class); classIndex++ {
				if class[classIndex] == '\\' || class[classIndex] == ']' {
					expression.WriteByte('\\')
				}
				expression.WriteByte(class[classIndex])
			}
			expression.WriteByte(']')
			index = end + 1
		default:
			if strings.ContainsRune(`.+()|{}$^\\`, rune(character)) {
				expression.WriteByte('\\')
			}
			expression.WriteByte(character)
			index++
		}
	}
	expression.WriteString("$")
	compiled, err := regexp.Compile(expression.String())
	if err != nil {
		return nil, err
	}
	return &globPattern{expression: compiled}, nil
}

func matchesAnyGlob(patterns []*globPattern, relativePath string) bool {
	for _, pattern := range patterns {
		if pattern.Match(relativePath) {
			return true
		}
	}
	return false
}

func buildGrepResult(toolName, input string, output GrepOutput, resultErr error) Result {
	sortGrepMatches(output.Matches)
	output.Count = len(output.Matches)
	status := "ok"
	if resultErr != nil {
		status = "error"
		output.Error = resultErr.Error()
	}
	payload, marshalErr := json.Marshal(output)
	if marshalErr != nil {
		return errorResult("cannot encode grep result: " + marshalErr.Error())
	}
	summary := fmt.Sprintf("grep found %d match(es) using %s", output.Count, output.Engine)
	if output.Truncated {
		summary += "; results truncated"
	}
	if output.TimedOut {
		summary += "; search timed out"
	}
	if resultErr != nil {
		summary += "; " + resultErr.Error()
	}
	return Result{
		Summary: summary,
		IsError: resultErr != nil,
		Parts: []ResultPart{{
			Kind:   PartToolCall,
			Name:   toolName,
			Status: status,
			Input:  input,
			Output: string(payload),
		}},
	}
}

func buildGlobResult(toolName, input string, output GlobOutput, resultErr error) Result {
	output.Count = len(output.Entries)
	status := "ok"
	if resultErr != nil {
		status = "error"
		output.Error = resultErr.Error()
	}
	payload, marshalErr := json.Marshal(output)
	if marshalErr != nil {
		return errorResult("cannot encode glob result: " + marshalErr.Error())
	}
	summary := fmt.Sprintf("glob found %d path(s)", output.Count)
	if output.Truncated {
		summary += "; results truncated"
	}
	if output.TimedOut {
		summary += "; search timed out"
	}
	if resultErr != nil {
		summary += "; " + resultErr.Error()
	}
	return Result{
		Summary: summary,
		IsError: resultErr != nil,
		Parts: []ResultPart{{
			Kind:   PartToolCall,
			Name:   toolName,
			Status: status,
			Input:  input,
			Output: string(payload),
		}},
	}
}

type cappedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *cappedBuffer) Write(value []byte) (int, error) {
	originalLength := len(value)
	remaining := b.limit - b.buffer.Len()
	if remaining > 0 {
		if len(value) > remaining {
			value = value[:remaining]
		}
		_, _ = b.buffer.Write(value)
	}
	return originalLength, nil
}

func (b *cappedBuffer) String() string { return b.buffer.String() }
