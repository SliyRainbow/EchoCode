package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
)

// 只读工具：read_file / list_dir / search。全部走编码安全层与沙盒守卫。

const (
	maxReadBytes    = 8 * 1024 * 1024
	maxReadLines    = 600
	maxSearchHits   = 200  // 搜索最多返回命中数
	maxListEntries  = 500  // 目录列举上限
	searchMaxFileKB = 1024 // 搜索时跳过超过此大小的文件
)

// ReadFileTool 读取文件内容（编码安全）。
type ReadFileTool struct{ Policy SandboxPolicy }

func (t ReadFileTool) Name() string { return "read_file" }
func (t ReadFileTool) Description() string {
	return "读取当前工作区内的文本或办公产物。DOCX、XLS/XLSX、PPTX 会自动结构化解析；文本支持 start_line/end_line 分段、可选行号，并返回 sha256、编码和行尾。"
}
func (t ReadFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":                 map[string]any{"type": "string", "description": "相对于当前工作区根目录的文件路径"},
			"start_line":           map[string]any{"type": "integer", "minimum": 1, "description": "可选，1-based 起始行"},
			"end_line":             map[string]any{"type": "integer", "minimum": 1, "description": "可选，包含在内的结束行"},
			"include_line_numbers": map[string]any{"type": "boolean", "description": "是否在返回内容前加行号"},
		},
		"required": []string{"path"},
	}
}

func (t ReadFileTool) Execute(_ context.Context, rawArgs json.RawMessage) (Result, error) {
	var args struct {
		Path               string `json:"path"`
		StartLine          int    `json:"start_line"`
		EndLine            int    `json:"end_line"`
		IncludeLineNumbers bool   `json:"include_line_numbers"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("参数解析失败: " + err.Error()), nil
	}
	abs, err := t.Policy.ResolveReadPath(args.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		return errorResult("无法访问文件: " + err.Error()), nil
	}
	if info.IsDir() {
		return errorResult("目标是目录，请使用 list_dir"), nil
	}
	if _, _, supportedArtifact := artifacts.Detect(abs); supportedArtifact {
		if info.Size() > 64<<20 {
			return errorResult(fmt.Sprintf("办公产物过大（%d 字节），超过解析上限 %d", info.Size(), 64<<20)), nil
		}
		content, artifactErr := artifacts.ArtifactText(abs, 256<<10)
		if artifactErr != nil {
			return errorResult("办公产物读取失败: " + artifactErr.Error()), nil
		}
		return Result{
			Summary: fmt.Sprintf("已结构化读取办公产物 %s\n%s", args.Path, content),
			Parts: []ResultPart{
				{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: args.Path, Output: content},
				{Kind: PartFile, Path: args.Path, FileAction: "available"},
			},
		}, nil
	}
	if info.Size() > maxReadBytes {
		return errorResult(fmt.Sprintf("文件过大（%d 字节），超过读取上限 %d", info.Size(), maxReadBytes)), nil
	}
	text, err := ReadFileText(abs)
	if err != nil {
		return errorResult("读取失败: " + err.Error()), nil
	}
	if text.Binary {
		return errorResult("目标是二进制文件，read_file 只读取文本；请使用 open_file 或专用媒体工具"), nil
	}
	lines := splitTextLines(text.Content)
	totalLines := len(lines)
	start, end := args.StartLine, args.EndLine
	if start <= 0 {
		start = 1
	}
	if end <= 0 || end > totalLines {
		end = totalLines
	}
	if totalLines == 0 {
		start, end = 0, 0
	} else if start > totalLines || end < start {
		return errorResult(fmt.Sprintf("请求行范围 %d-%d 超出文件总行数 %d", start, end, totalLines)), nil
	}
	truncated := false
	if end-start+1 > maxReadLines {
		end = start + maxReadLines - 1
		truncated = true
	}
	selected := ""
	if totalLines > 0 {
		selectedLines := append([]string(nil), lines[start-1:end]...)
		if args.IncludeLineNumbers {
			width := len(strconv.Itoa(end))
			for index := range selectedLines {
				selectedLines[index] = fmt.Sprintf("%*d | %s", width, start+index, selectedLines[index])
			}
		}
		selected = strings.Join(selectedLines, "\n")
		if strings.HasSuffix(text.Content, "\n") && end == totalLines {
			selected += "\n"
		}
	}
	if truncated {
		selected += fmt.Sprintf("\n... [仅返回前 %d 行，请继续从第 %d 行读取]", maxReadLines, end+1)
	}
	hash := FileTextSHA256(text.Content)
	summary := fmt.Sprintf("已读取 %s 第 %d-%d/%d 行（%s，%s，%s）", args.Path, start, end, totalLines, text.Encoding, text.LineEnding, hash)
	return Result{
		Summary: summary,
		Parts: []ResultPart{
			{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: fmt.Sprintf("%s:%d-%d", args.Path, start, end), Output: selected},
			{Kind: PartFile, Path: args.Path, LineCount: totalLines, FileAction: "available"},
		},
	}, nil
}

// ListDirTool 列出目录内容。
type ListDirTool struct{ Policy SandboxPolicy }

func (t ListDirTool) Name() string { return "list_dir" }
func (t ListDirTool) Description() string {
	return "列出当前工作区内的目录树，返回文件/目录类型与文件大小。首次探索使用 path='.'；可用 max_depth 控制递归深度。"
}
func (t ListDirTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":           map[string]any{"type": "string", "description": "相对于当前工作区的目录路径；. 表示工作区根"},
			"max_depth":      map[string]any{"type": "integer", "minimum": 1, "maximum": 6},
			"include_hidden": map[string]any{"type": "boolean"},
			"max_entries":    map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
		},
	}
}

func (t ListDirTool) Execute(_ context.Context, rawArgs json.RawMessage) (Result, error) {
	var args struct {
		Path          string `json:"path"`
		MaxDepth      int    `json:"max_depth"`
		IncludeHidden bool   `json:"include_hidden"`
		MaxEntries    int    `json:"max_entries"`
	}
	_ = json.Unmarshal(rawArgs, &args)
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}
	abs, err := t.Policy.ResolveReadPath(args.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return errorResult("目标不是可读取目录"), nil
	}
	if args.MaxDepth <= 0 {
		args.MaxDepth = 1
	}
	if args.MaxDepth > 6 {
		args.MaxDepth = 6
	}
	limit := args.MaxEntries
	if limit <= 0 || limit > maxListEntries {
		limit = maxListEntries
	}
	var lines []string
	count := 0
	rootDepth := strings.Count(filepath.Clean(abs), string(os.PathSeparator))
	walkErr := filepath.WalkDir(abs, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		if path == abs {
			return nil
		}
		if entry.IsDir() && !t.Policy.TaskScopeAllowsTraversal(path) {
			return filepath.SkipDir
		}
		if !entry.IsDir() && !t.Policy.TaskScopeAllowsPath(path) {
			return nil
		}
		depth := strings.Count(filepath.Clean(path), string(os.PathSeparator)) - rootDepth
		name := entry.Name()
		if entry.IsDir() && (depth > args.MaxDepth || (!args.IncludeHidden && strings.HasPrefix(name, ".")) || skipDir(name)) {
			return filepath.SkipDir
		}
		if depth > args.MaxDepth || (!args.IncludeHidden && strings.HasPrefix(name, ".")) {
			return nil
		}
		if count >= limit {
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(abs, path)
		rel = filepath.ToSlash(rel)
		if entry.IsDir() {
			lines = append(lines, "d "+rel+"/")
			count++
			if depth >= args.MaxDepth {
				return filepath.SkipDir
			}
			return nil
		} else if itemInfo, statErr := entry.Info(); statErr == nil {
			lines = append(lines, fmt.Sprintf("f %s (%d bytes)", rel, itemInfo.Size()))
		} else {
			lines = append(lines, "f "+rel)
		}
		count++
		return nil
	})
	if walkErr != nil {
		return errorResult("读取目录失败: " + walkErr.Error()), nil
	}
	if count >= limit {
		lines = append(lines, fmt.Sprintf("... [已达到 %d 项上限]", limit))
	}
	body := strings.Join(lines, "\n")
	return Result{
		Summary: fmt.Sprintf("目录 %s 已列出 %d 项（深度 %d）", args.Path, count, args.MaxDepth),
		Parts: []ResultPart{
			{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: args.Path, Output: body},
		},
	}, nil
}

// SearchTool 在工作区内按子串搜索文本。
type SearchTool struct{ Policy SandboxPolicy }

func (t SearchTool) Name() string { return "search" }
func (t SearchTool) Description() string {
	return "在当前工作区文本文件中搜索。支持正则、大小写、include_glob 和结果上限；返回工作区相对路径、行号与列号。不要用 shell 的 rg/grep/Select-String。"
}
func (t SearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":          map[string]any{"type": "string", "description": "要搜索的文本或正则"},
			"path":           map[string]any{"type": "string", "description": "搜索根目录，默认工作区根"},
			"regex":          map[string]any{"type": "boolean"},
			"case_sensitive": map[string]any{"type": "boolean"},
			"include_glob":   map[string]any{"type": "string", "description": "可选文件过滤，如 *.go 或 frontend/*.tsx"},
			"max_results":    map[string]any{"type": "integer", "minimum": 1, "maximum": 500},
		},
		"required": []string{"query"},
	}
}

func (t SearchTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args struct {
		Query         string `json:"query"`
		Path          string `json:"path"`
		Regex         bool   `json:"regex"`
		CaseSensitive bool   `json:"case_sensitive"`
		IncludeGlob   string `json:"include_glob"`
		MaxResults    int    `json:"max_results"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("参数解析失败: " + err.Error()), nil
	}
	if strings.TrimSpace(args.Query) == "" {
		return errorResult("query 不能为空"), nil
	}
	if strings.TrimSpace(args.Path) == "" {
		args.Path = "."
	}
	root, err := t.Policy.ResolveReadPath(args.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	limit := args.MaxResults
	if limit <= 0 || limit > 500 {
		limit = maxSearchHits
	}
	var matcher *regexp.Regexp
	if args.Regex {
		pattern := args.Query
		if !args.CaseSensitive {
			pattern = "(?i)" + pattern
		}
		matcher, err = regexp.Compile(pattern)
		if err != nil {
			return errorResult("正则表达式无效: " + err.Error()), nil
		}
	}

	var hits []string
	count := 0
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 跳过无法访问的项
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if d.IsDir() {
			if !t.Policy.TaskScopeAllowsTraversal(p) {
				return filepath.SkipDir
			}
			if skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !t.Policy.TaskScopeAllowsPath(p) {
			return nil
		}
		relWorkspace, relErr := filepath.Rel(t.Policy.WorkspaceRoot, p)
		if relErr != nil {
			relWorkspace = p
		}
		relWorkspace = filepath.ToSlash(relWorkspace)
		if args.IncludeGlob != "" && !matchesSearchGlob(args.IncludeGlob, relWorkspace) {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() > searchMaxFileKB*1024 {
			return nil
		}
		text, err := ReadFileText(p)
		if err != nil || text.Binary {
			return nil
		}
		for i, line := range strings.Split(text.Content, "\n") {
			column := -1
			if matcher != nil {
				if location := matcher.FindStringIndex(line); location != nil {
					column = location[0]
				}
			} else if args.CaseSensitive {
				column = strings.Index(line, args.Query)
			} else {
				column = strings.Index(strings.ToLower(line), strings.ToLower(args.Query))
			}
			if column >= 0 {
				displayLine := strings.TrimSpace(line)
				if len([]rune(displayLine)) > 300 {
					displayLine = string([]rune(displayLine)[:300]) + "..."
				}
				hits = append(hits, fmt.Sprintf("%s:%d:%d: %s", relWorkspace, i+1, column+1, displayLine))
				count++
				if count >= limit {
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	if walkErr != nil && ctx.Err() != nil {
		return errorResult("搜索被取消"), nil
	}
	body := strings.Join(hits, "\n")
	if body == "" {
		body = "（无匹配）"
	}
	return Result{
		Summary: fmt.Sprintf("搜索 %q 命中 %d 处", args.Query, count),
		Parts: []ResultPart{
			{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: args.Query, Output: body},
		},
	}, nil
}

// FileInfoTool reports metadata without reading file content into the model.
type FileInfoTool struct{ Policy SandboxPolicy }

func (t FileInfoTool) Name() string { return "file_info" }
func (t FileInfoTool) Description() string {
	return "获取工作区文件或目录的信息。文本文件会返回行数、编码、行尾和 sha256；用于修改前校验。"
}
func (t FileInfoTool) InputSchema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{"path": map[string]any{"type": "string"}},
		"required":   []string{"path"},
	}
}
func (t FileInfoTool) Execute(_ context.Context, rawArgs json.RawMessage) (Result, error) {
	var args struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("参数解析失败: " + err.Error()), nil
	}
	abs, err := t.Policy.ResolveReadPath(args.Path)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	info, err := os.Stat(abs)
	if err != nil {
		return errorResult("无法访问路径: " + err.Error()), nil
	}
	output := fmt.Sprintf("path: %s\ntype: file\nsize: %d\nmodified: %s", args.Path, info.Size(), info.ModTime().UTC().Format("2006-01-02T15:04:05Z"))
	if info.IsDir() {
		output = fmt.Sprintf("path: %s\ntype: directory\nmodified: %s", args.Path, info.ModTime().UTC().Format("2006-01-02T15:04:05Z"))
	} else if info.Size() <= maxReadBytes {
		if text, readErr := ReadFileText(abs); readErr == nil && !text.Binary {
			output += fmt.Sprintf("\nlines: %d\nencoding: %s\nline_ending: %s\nsha256: %s", countLines(text.Content), text.Encoding, text.LineEnding, FileTextSHA256(text.Content))
		} else {
			output += "\ntext: false"
		}
	}
	return Result{
		Summary: fmt.Sprintf("已获取 %s 的文件信息", args.Path),
		Parts:   []ResultPart{{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: args.Path, Output: output}},
	}, nil
}

// ---- 共享辅助 ----

func errorResult(msg string) Result {
	return Result{
		Summary: msg,
		IsError: true,
		Parts:   []ResultPart{{Kind: PartText, Text: msg}},
	}
}

func countLines(s string) int {
	if s == "" {
		return 0
	}
	count := strings.Count(s, "\n")
	if strings.HasSuffix(s, "\n") {
		return count
	}
	return count + 1
}

func splitTextLines(value string) []string {
	if value == "" {
		return nil
	}
	value = strings.TrimSuffix(value, "\n")
	return strings.Split(value, "\n")
}

func matchesSearchGlob(pattern, relativePath string) bool {
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	relativePath = filepath.ToSlash(relativePath)
	if pattern == "" {
		return true
	}
	if matched, _ := pathpkg.Match(pattern, relativePath); matched {
		return true
	}
	if matched, _ := pathpkg.Match(pattern, pathpkg.Base(relativePath)); matched {
		return true
	}
	if strings.HasPrefix(pattern, "**/") {
		trimmed := strings.TrimPrefix(pattern, "**/")
		matched, _ := pathpkg.Match(trimmed, relativePath)
		if matched {
			return true
		}
		matched, _ = pathpkg.Match(trimmed, pathpkg.Base(relativePath))
		return matched
	}
	return false
}

func truncateForDisplay(s string) string {
	const limit = 4000
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n...（内容已截断）"
}

func skipDir(name string) bool {
	switch name {
	case ".git", "node_modules", "dist", "build", ".cache", "vendor":
		return true
	}
	return false
}
