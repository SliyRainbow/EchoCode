package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
	"github.com/SliyRainbow/EchoCode/internal/pathutil"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	maxWorkspacePreviewFileBytes = 16 << 20
	maxArtifactPreviewFileBytes  = 64 << 20
	maxWorkspacePreviewBytes     = 2 << 20
	maxWorkspacePreviewLines     = 5000
	maxWorkspaceDirectoryEntries = 1000
)

type WorkspaceFilePreview struct {
	Path       string             `json:"path"`
	Name       string             `json:"name"`
	Content    string             `json:"content"`
	Encoding   string             `json:"encoding"`
	LineEnding string             `json:"lineEnding"`
	LineCount  int                `json:"lineCount"`
	Size       int64              `json:"size"`
	Truncated  bool               `json:"truncated"`
	Binary     bool               `json:"binary"`
	TooLarge   bool               `json:"tooLarge"`
	Artifact   *artifacts.Preview `json:"artifact,omitempty"`
}

type WorkspaceDirectoryListing struct {
	Path      string                    `json:"path"`
	Entries   []WorkspaceDirectoryEntry `json:"entries"`
	Truncated bool                      `json:"truncated"`
}

type WorkspaceDirectoryEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDirectory bool   `json:"isDirectory"`
	IsSymlink   bool   `json:"isSymlink"`
	Size        int64  `json:"size,omitempty"`
}

// ListWorkspaceDirectory returns one bounded directory level. Loading folders
// lazily keeps large repositories responsive and avoids walking generated trees.
func (s *Service) ListWorkspaceDirectory(path string) (WorkspaceDirectoryListing, error) {
	s.stateMu.RLock()
	workspaceRoot := strings.TrimSpace(s.runtimeSettings.WorkspaceRoot)
	policy := s.sandboxPolicy()
	s.stateMu.RUnlock()
	if workspaceRoot == "" {
		return WorkspaceDirectoryListing{}, fmt.Errorf("请先选择项目工作区")
	}

	relative := filepath.Clean(filepath.FromSlash(strings.TrimSpace(path)))
	if relative == "" || relative == "." {
		relative = "."
	}
	if filepath.IsAbs(relative) {
		return WorkspaceDirectoryListing{}, fmt.Errorf("目录路径必须相对于当前工作区")
	}
	// The explorer is always project-scoped, even when the Agent itself has an
	// unrestricted filesystem policy.
	policy.FilesystemAccess = "read-only"
	policy.ExtraWritableRoots = nil
	abs, err := policy.ResolveReadPath(relative)
	if err != nil {
		return WorkspaceDirectoryListing{}, err
	}
	resolvedRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		return WorkspaceDirectoryListing{}, fmt.Errorf("无法访问工作区: %w", err)
	}
	resolvedPath, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return WorkspaceDirectoryListing{}, fmt.Errorf("目录不存在或无法访问: %w", err)
	}
	if within, withinErr := pathutil.Within(resolvedRoot, resolvedPath); withinErr != nil || !within {
		return WorkspaceDirectoryListing{}, fmt.Errorf("目录超出当前工作区")
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return WorkspaceDirectoryListing{}, fmt.Errorf("无法读取目录信息: %w", err)
	}
	if !info.IsDir() {
		return WorkspaceDirectoryListing{}, fmt.Errorf("目标不是目录")
	}

	directory, err := os.Open(resolvedPath)
	if err != nil {
		return WorkspaceDirectoryListing{}, fmt.Errorf("无法打开目录: %w", err)
	}
	defer directory.Close()
	entries, err := directory.ReadDir(maxWorkspaceDirectoryEntries + 1)
	if err != nil {
		return WorkspaceDirectoryListing{}, fmt.Errorf("无法列出目录: %w", err)
	}
	truncated := len(entries) > maxWorkspaceDirectoryEntries
	if truncated {
		entries = entries[:maxWorkspaceDirectoryEntries]
	}

	displayPath := ""
	if relative != "." {
		displayPath = filepath.ToSlash(relative)
	}
	result := make([]WorkspaceDirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		entryInfo, infoErr := entry.Info()
		if infoErr != nil {
			continue
		}
		entryPath := filepath.ToSlash(filepath.Join(displayPath, entry.Name()))
		isSymlink := entryInfo.Mode()&os.ModeSymlink != 0
		result = append(result, WorkspaceDirectoryEntry{
			Name:        entry.Name(),
			Path:        entryPath,
			IsDirectory: entryInfo.IsDir() && !isSymlink,
			IsSymlink:   isSymlink,
			Size:        entryInfo.Size(),
		})
	}
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].IsDirectory != result[j].IsDirectory {
			return result[i].IsDirectory
		}
		return strings.ToLower(result[i].Name) < strings.ToLower(result[j].Name)
	})
	return WorkspaceDirectoryListing{Path: displayPath, Entries: result, Truncated: truncated}, nil
}

// ResolveWorkspaceFile validates a UI-requested artifact path against the
// active workspace policy before the desktop shell opens or reveals it.
func (s *Service) ResolveWorkspaceFile(path string) (string, error) {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if strings.TrimSpace(path) == "" {
		return "", fmt.Errorf("文件路径不能为空")
	}
	abs, err := s.sandboxPolicy().ResolveReadPath(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("文件不存在或无法访问: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("目标是目录，不是文件")
	}
	return abs, nil
}

func (s *Service) OpenWorkspaceFile(path string) error {
	abs, err := s.ResolveWorkspaceFile(path)
	if err != nil {
		return err
	}
	if s.config.OpenFile == nil {
		return fmt.Errorf("当前桌面环境不支持打开文件")
	}
	return s.config.OpenFile(abs)
}

func (s *Service) ReadWorkspaceFile(path string) (WorkspaceFilePreview, error) {
	abs, err := s.ResolveWorkspaceFile(path)
	if err != nil {
		return WorkspaceFilePreview{}, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return WorkspaceFilePreview{}, fmt.Errorf("无法读取文件信息: %w", err)
	}

	s.stateMu.RLock()
	workspaceRoot := s.runtimeSettings.WorkspaceRoot
	s.stateMu.RUnlock()
	displayPath := filepath.Base(abs)
	if relative, relativeErr := filepath.Rel(workspaceRoot, abs); relativeErr == nil {
		displayPath = filepath.ToSlash(relative)
	}
	preview := WorkspaceFilePreview{
		Path: displayPath,
		Name: filepath.Base(abs),
		Size: info.Size(),
	}
	if _, _, supported := artifacts.Detect(abs); supported {
		preview.Binary = true
		if info.Size() > maxArtifactPreviewFileBytes {
			preview.Truncated = true
			preview.TooLarge = true
			return preview, nil
		}
		artifact, artifactErr := artifacts.PreviewFile(abs, artifacts.DefaultPreviewOptions())
		if artifactErr != nil {
			return WorkspaceFilePreview{}, fmt.Errorf("读取办公产物失败: %w", artifactErr)
		}
		preview.Artifact = &artifact
		switch artifact.Kind {
		case artifacts.KindDocument:
			preview.Truncated = artifact.Document != nil && artifact.Document.Truncated
		case artifacts.KindSpreadsheet:
			preview.Truncated = artifact.Spreadsheet != nil && artifact.Spreadsheet.Truncated
		case artifacts.KindPresentation:
			preview.Truncated = artifact.Presentation != nil && artifact.Presentation.Truncated
		}
		return preview, nil
	}
	if info.Size() > maxWorkspacePreviewFileBytes {
		preview.Truncated = true
		preview.TooLarge = true
		return preview, nil
	}

	text, err := tools.ReadFileText(abs)
	if err != nil {
		return WorkspaceFilePreview{}, fmt.Errorf("读取文件失败: %w", err)
	}
	preview.Encoding = string(text.Encoding)
	preview.LineEnding = string(text.LineEnding)
	preview.Binary = text.Binary
	if text.Binary {
		return preview, nil
	}
	preview.LineCount = workspacePreviewLineCount(text.Content)
	preview.Content, preview.Truncated = truncateWorkspacePreview(text.Content)
	return preview, nil
}

func (s *Service) RevealWorkspaceFile(path string) error {
	abs, err := s.ResolveWorkspaceFile(path)
	if err != nil {
		return err
	}
	if s.config.RevealFile == nil {
		return fmt.Errorf("当前桌面环境不支持定位文件")
	}
	return s.config.RevealFile(abs)
}

func workspacePreviewLineCount(content string) int {
	if content == "" {
		return 0
	}
	count := strings.Count(content, "\n")
	if !strings.HasSuffix(content, "\n") {
		count++
	}
	return count
}

func truncateWorkspacePreview(content string) (string, bool) {
	end := len(content)
	if end > maxWorkspacePreviewBytes {
		end = maxWorkspacePreviewBytes
		for end > 0 && !utf8.ValidString(content[:end]) {
			end--
		}
	}
	lines := 0
	for index := 0; index < end; index++ {
		if content[index] != '\n' {
			continue
		}
		lines++
		if lines >= maxWorkspacePreviewLines {
			end = index + 1
			break
		}
	}
	return content[:end], end < len(content)
}
