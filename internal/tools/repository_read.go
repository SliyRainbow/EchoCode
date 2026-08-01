package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	defaultGitHubAPIBase       = "https://api.github.com"
	maxRepositoryAPIResponse   = 4 * 1024 * 1024
	maxRepositoryFileBytes     = 512 * 1024
	defaultRepositoryEntries   = 200
	maximumRepositoryEntries   = 500
	maximumRepositoryReadme    = 64 * 1024
	maximumRepositoryGitOutput = 4 * 1024 * 1024
	maximumRepositoryArchive   = 80 * 1024 * 1024
)

var repositoryGitCacheMu sync.Mutex

// ReadRepositoryTool reads public GitHub repositories through the GitHub API.
// It returns real repository metadata, commit identity, trees, and file content
// instead of search-engine snippets.
type ReadRepositoryTool struct {
	Policy     SandboxPolicy
	Client     *http.Client
	APIBaseURL string
	Token      string
}

func (t ReadRepositoryTool) Name() string { return "read_repository" }

func (t ReadRepositoryTool) Description() string {
	return "读取公开 GitHub 仓库的真实内容。传入仓库、tree 或 blob 链接；不填 path 时返回元数据、提交 SHA、README 和目录树，填写 path 时读取具体文件或目录。GitHub 项目链接必须优先使用本工具，不能用 web_search 摘要代替源码读取。"
}

func (t ReadRepositoryTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":         map[string]any{"type": "string", "description": "GitHub 仓库、tree 或 blob URL"},
			"path":        map[string]any{"type": "string", "description": "仓库内文件或目录路径；URL 已包含 blob/tree 路径时可省略"},
			"ref":         map[string]any{"type": "string", "description": "分支、标签或提交；默认使用 URL 中的 ref 或仓库默认分支"},
			"max_entries": map[string]any{"type": "integer", "minimum": 20, "maximum": maximumRepositoryEntries, "description": "目录树最大条目数，默认 200"},
		},
		"required": []string{"url"},
	}
}

func (t ReadRepositoryTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args struct {
		URL        string `json:"url"`
		Path       string `json:"path"`
		Ref        string `json:"ref"`
		MaxEntries int    `json:"max_entries"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return repositoryReadError("参数解析失败: "+err.Error(), args.URL), nil
	}
	if err := t.Policy.CheckNetwork(); err != nil {
		return repositoryReadError(err.Error(), args.URL), nil
	}
	target, err := parseGitHubRepositoryURL(args.URL)
	if err != nil {
		return repositoryReadError(err.Error(), args.URL), nil
	}
	if strings.TrimSpace(args.Path) != "" {
		target.Path = cleanRepositoryPath(args.Path)
	}
	if strings.TrimSpace(args.Ref) != "" {
		target.Ref = strings.TrimSpace(args.Ref)
	}
	if args.MaxEntries <= 0 {
		args.MaxEntries = defaultRepositoryEntries
	}
	if args.MaxEntries > maximumRepositoryEntries {
		args.MaxEntries = maximumRepositoryEntries
	}

	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	apiBase := strings.TrimRight(strings.TrimSpace(t.APIBaseURL), "/")
	if apiBase == "" {
		apiBase = defaultGitHubAPIBase
	}
	if token := repositoryGitHubToken(t.Token, apiBase); token != "" {
		client = withGitHubToken(client, token)
	}

	repository, apiErr := fetchGitHubRepository(ctx, client, apiBase, target)
	if apiErr != nil {
		output, resolvedRef, fallbackErr := readGitHubRepositoryFallback(ctx, target, args.MaxEntries)
		if fallbackErr != nil {
			return repositoryReadError(fmt.Sprintf("%v; repository fallback failed: %v", apiErr, fallbackErr), args.URL), nil
		}
		return repositoryReadResult(t, target, resolvedRef, output), nil
	}
	if target.Ref == "" {
		target.Ref = repository.DefaultBranch
	}
	if target.Ref == "" {
		target.Ref = "HEAD"
	}

	var output string
	if target.Path == "" {
		output, err = readGitHubRepositoryOverview(ctx, client, apiBase, target, repository, args.MaxEntries)
	} else {
		output, err = readGitHubRepositoryPath(ctx, client, apiBase, target, repository, args.MaxEntries)
	}
	if err != nil {
		gitOutput, resolvedRef, fallbackErr := readGitHubRepositoryFallback(ctx, target, args.MaxEntries)
		if fallbackErr != nil {
			return repositoryReadError(fmt.Sprintf("%v; repository fallback failed: %v", err, fallbackErr), args.URL), nil
		}
		return repositoryReadResult(t, target, resolvedRef, gitOutput), nil
	}
	return repositoryReadResult(t, target, target.Ref, output), nil
}

func repositoryReadResult(t ReadRepositoryTool, target githubRepositoryTarget, ref, output string) Result {
	input := target.CanonicalURL()
	if target.Path != "" {
		input += "#" + target.Path
	}
	if strings.TrimSpace(ref) == "" {
		ref = "default branch"
	}
	return Result{
		Summary: fmt.Sprintf("已读取 GitHub 仓库 %s（%s）", target.FullName(), ref),
		Parts: []ResultPart{{
			Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: input, Output: output,
		}},
	}
}

type githubRepositoryTarget struct {
	Owner string
	Repo  string
	Ref   string
	Path  string
}

func (t githubRepositoryTarget) FullName() string { return t.Owner + "/" + t.Repo }

func (t githubRepositoryTarget) CanonicalURL() string {
	return "https://github.com/" + t.FullName()
}

func parseGitHubRepositoryURL(raw string) (githubRepositoryTarget, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return githubRepositoryTarget{}, fmt.Errorf("GitHub URL 无效: %w", err)
	}
	host := strings.ToLower(strings.TrimPrefix(parsed.Hostname(), "www."))
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || host != "github.com" {
		return githubRepositoryTarget{}, errors.New("当前仓库读取器仅支持 github.com 的 HTTP/HTTPS 链接")
	}
	segments := strings.Split(strings.Trim(parsed.EscapedPath(), "/"), "/")
	if len(segments) < 2 {
		return githubRepositoryTarget{}, errors.New("GitHub 链接缺少 owner/repository")
	}
	owner, ownerErr := url.PathUnescape(segments[0])
	repository, repoErr := url.PathUnescape(segments[1])
	if ownerErr != nil || repoErr != nil {
		return githubRepositoryTarget{}, errors.New("GitHub 仓库路径编码无效")
	}
	repository = strings.TrimSuffix(repository, ".git")
	if !validGitHubName(owner) || !validGitHubName(repository) {
		return githubRepositoryTarget{}, errors.New("GitHub owner 或 repository 名称无效")
	}
	target := githubRepositoryTarget{Owner: owner, Repo: repository}
	if len(segments) >= 4 && (segments[2] == "blob" || segments[2] == "tree") {
		target.Ref, _ = url.PathUnescape(segments[3])
		if len(segments) > 4 {
			decoded := make([]string, 0, len(segments)-4)
			for _, segment := range segments[4:] {
				value, decodeErr := url.PathUnescape(segment)
				if decodeErr != nil {
					return githubRepositoryTarget{}, errors.New("GitHub 文件路径编码无效")
				}
				decoded = append(decoded, value)
			}
			target.Path = cleanRepositoryPath(strings.Join(decoded, "/"))
		}
	}
	return target, nil
}

func validGitHubName(value string) bool {
	if value == "" || value == "." || value == ".." {
		return false
	}
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '_' || char == '.' {
			continue
		}
		return false
	}
	return true
}

func cleanRepositoryPath(value string) string {
	cleaned := strings.TrimPrefix(path.Clean("/"+strings.TrimSpace(value)), "/")
	if cleaned == "." {
		return ""
	}
	return cleaned
}

type githubRepository struct {
	FullName        string `json:"full_name"`
	HTMLURL         string `json:"html_url"`
	Description     string `json:"description"`
	DefaultBranch   string `json:"default_branch"`
	Language        string `json:"language"`
	StargazersCount int    `json:"stargazers_count"`
	ForksCount      int    `json:"forks_count"`
	Archived        bool   `json:"archived"`
	Private         bool   `json:"private"`
}

type githubCommit struct {
	SHA string `json:"sha"`
}

type githubTree struct {
	SHA       string           `json:"sha"`
	Truncated bool             `json:"truncated"`
	Tree      []githubTreeItem `json:"tree"`
}

type githubTreeItem struct {
	Path string `json:"path"`
	Type string `json:"type"`
	Size int    `json:"size"`
}

type githubContent struct {
	Type        string `json:"type"`
	Name        string `json:"name"`
	Path        string `json:"path"`
	SHA         string `json:"sha"`
	Size        int    `json:"size"`
	Encoding    string `json:"encoding"`
	Content     string `json:"content"`
	HTMLURL     string `json:"html_url"`
	DownloadURL string `json:"download_url"`
}

func fetchGitHubRepository(ctx context.Context, client *http.Client, apiBase string, target githubRepositoryTarget) (githubRepository, error) {
	var repository githubRepository
	endpoint := fmt.Sprintf("%s/repos/%s/%s", apiBase, url.PathEscape(target.Owner), url.PathEscape(target.Repo))
	if err := githubGetJSON(ctx, client, endpoint, &repository); err != nil {
		return githubRepository{}, fmt.Errorf("读取 GitHub 仓库元数据失败: %w", err)
	}
	if repository.Private {
		return githubRepository{}, errors.New("未配置 GitHub 身份验证，无法读取私有仓库")
	}
	return repository, nil
}

func readGitHubRepositoryOverview(
	ctx context.Context,
	client *http.Client,
	apiBase string,
	target githubRepositoryTarget,
	repository githubRepository,
	maxEntries int,
) (string, error) {
	commitEndpoint := fmt.Sprintf("%s/repos/%s/%s/commits/%s", apiBase, url.PathEscape(target.Owner), url.PathEscape(target.Repo), url.PathEscape(target.Ref))
	var commit githubCommit
	if err := githubGetJSON(ctx, client, commitEndpoint, &commit); err != nil {
		return "", fmt.Errorf("解析仓库 ref %q 失败: %w", target.Ref, err)
	}
	if commit.SHA == "" {
		return "", errors.New("GitHub 没有返回提交 SHA")
	}

	treeEndpoint := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1", apiBase, url.PathEscape(target.Owner), url.PathEscape(target.Repo), url.PathEscape(commit.SHA))
	var tree githubTree
	if err := githubGetJSON(ctx, client, treeEndpoint, &tree); err != nil {
		return "", fmt.Errorf("读取仓库目录树失败: %w", err)
	}

	readme := ""
	readmeEndpoint := fmt.Sprintf("%s/repos/%s/%s/readme?ref=%s", apiBase, url.PathEscape(target.Owner), url.PathEscape(target.Repo), url.QueryEscape(target.Ref))
	var readmeContent githubContent
	if err := githubGetJSON(ctx, client, readmeEndpoint, &readmeContent); err == nil {
		if decoded, decodeErr := decodeGitHubContent(readmeContent, maximumRepositoryReadme); decodeErr == nil {
			readme = decoded
		}
	}

	var output strings.Builder
	fmt.Fprintf(&output, "Repository: %s\nURL: %s\nRef: %s\nCommit: %s\n", target.FullName(), target.CanonicalURL(), target.Ref, commit.SHA)
	if repository.Description != "" {
		fmt.Fprintf(&output, "Description: %s\n", repository.Description)
	}
	fmt.Fprintf(&output, "Language: %s\nStars: %d\nForks: %d\nArchived: %t\n", repository.Language, repository.StargazersCount, repository.ForksCount, repository.Archived)
	if readme != "" {
		output.WriteString("\nREADME:\n")
		output.WriteString(readme)
		output.WriteString("\n")
	}
	output.WriteString("\nRepository tree:\n")
	limit := len(tree.Tree)
	if limit > maxEntries {
		limit = maxEntries
	}
	for _, item := range tree.Tree[:limit] {
		entryPath := item.Path
		if item.Type == "tree" {
			entryPath += "/"
		}
		if item.Size > 0 && item.Type == "blob" {
			fmt.Fprintf(&output, "%s (%d bytes)\n", entryPath, item.Size)
		} else {
			output.WriteString(entryPath + "\n")
		}
	}
	if len(tree.Tree) > limit || tree.Truncated {
		fmt.Fprintf(&output, "... tree truncated; showing %d of %d entries\n", limit, len(tree.Tree))
	}
	return strings.TrimSpace(output.String()), nil
}

func readGitHubRepositoryPath(
	ctx context.Context,
	client *http.Client,
	apiBase string,
	target githubRepositoryTarget,
	repository githubRepository,
	maxEntries int,
) (string, error) {
	endpoint := fmt.Sprintf("%s/repos/%s/%s/contents/%s?ref=%s", apiBase, url.PathEscape(target.Owner), url.PathEscape(target.Repo), escapeRepositoryPath(target.Path), url.QueryEscape(target.Ref))
	body, err := githubGet(ctx, client, endpoint)
	if err != nil {
		return "", fmt.Errorf("读取仓库路径 %q 失败: %w", target.Path, err)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "", errors.New("GitHub 返回了空内容")
	}
	if trimmed[0] == '[' {
		var entries []githubContent
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return "", fmt.Errorf("解析仓库目录失败: %w", err)
		}
		var output strings.Builder
		fmt.Fprintf(&output, "Repository: %s\nRef: %s\nPath: %s/\nEntries:\n", repository.FullName, target.Ref, target.Path)
		limit := len(entries)
		if limit > maxEntries {
			limit = maxEntries
		}
		for _, entry := range entries[:limit] {
			name := entry.Name
			if entry.Type == "dir" {
				name += "/"
			}
			if entry.Size > 0 && entry.Type == "file" {
				fmt.Fprintf(&output, "%s (%d bytes)\n", name, entry.Size)
			} else {
				output.WriteString(name + "\n")
			}
		}
		if len(entries) > limit {
			fmt.Fprintf(&output, "... showing %d of %d entries\n", limit, len(entries))
		}
		return strings.TrimSpace(output.String()), nil
	}

	var file githubContent
	if err := json.Unmarshal(trimmed, &file); err != nil {
		return "", fmt.Errorf("解析仓库文件失败: %w", err)
	}
	content, err := decodeGitHubContent(file, maxRepositoryFileBytes)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("Repository: %s\nRef: %s\nPath: %s\nSHA: %s\nSize: %d bytes\n\n%s", repository.FullName, target.Ref, file.Path, file.SHA, file.Size, content), nil
}

func readGitHubRepositoryFallback(ctx context.Context, target githubRepositoryTarget, maxEntries int) (string, string, error) {
	archiveOutput, archiveRef, archiveErr := readGitHubRepositoryArchive(ctx, target, maxEntries)
	if archiveErr == nil {
		return archiveOutput, archiveRef, nil
	}
	gitOutput, gitRef, gitErr := readGitHubRepositoryWithGit(ctx, target, maxEntries)
	if gitErr == nil {
		return gitOutput, gitRef, nil
	}
	return "", "", fmt.Errorf("archive fallback: %v; git fallback: %v", archiveErr, gitErr)
}

func readGitHubRepositoryArchive(ctx context.Context, target githubRepositoryTarget, maxEntries int) (string, string, error) {
	resolvedRef, commitSHA, err := resolveGitHubRepositoryRef(ctx, target)
	if err != nil {
		return "", "", err
	}
	archiveURL := fmt.Sprintf("https://codeload.github.com/%s/%s/zip/%s", url.PathEscape(target.Owner), url.PathEscape(target.Repo), url.PathEscape(resolvedRef))
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveURL, nil)
	if err != nil {
		return "", "", err
	}
	request.Header.Set("Accept", "application/zip")
	request.Header.Set("User-Agent", "EchoCode/1.0")
	client := &http.Client{Timeout: 60 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return "", "", fmt.Errorf("download repository archive: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", "", fmt.Errorf("repository archive HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maximumRepositoryArchive+1))
	if err != nil {
		return "", "", fmt.Errorf("read repository archive: %w", err)
	}
	if len(body) > maximumRepositoryArchive {
		return "", "", fmt.Errorf("repository archive exceeds %d bytes", maximumRepositoryArchive)
	}
	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return "", "", fmt.Errorf("open repository archive: %w", err)
	}
	entries := repositoryArchiveEntries(reader.File)
	if len(entries) == 0 {
		return "", "", errors.New("repository archive is empty")
	}
	if target.Path != "" {
		output, readErr := readRepositoryArchivePath(entries, target, resolvedRef, commitSHA, maxEntries)
		return output, resolvedRef, readErr
	}

	var output strings.Builder
	fmt.Fprintf(&output, "Repository: %s\nURL: %s\nRef: %s\nCommit: %s\nSource: GitHub archive fallback\n", target.FullName(), target.CanonicalURL(), resolvedRef, commitSHA)
	if readme := readRepositoryArchiveReadme(entries); readme != "" {
		output.WriteString("\nREADME:\n")
		output.WriteString(readme)
		output.WriteString("\n")
	}
	output.WriteString("\nRepository tree:\n")
	output.WriteString(formatRepositoryArchiveTree(entries, maxEntries))
	return strings.TrimSpace(output.String()), resolvedRef, nil
}

func resolveGitHubRepositoryRef(ctx context.Context, target githubRepositoryTarget) (string, string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", "", errors.New("Git executable was not found for repository ref resolution")
	}
	remoteURL := target.CanonicalURL() + ".git"
	if target.Ref != "" {
		output, err := runRepositoryGit(ctx, os.TempDir(), 256*1024, "ls-remote", remoteURL, target.Ref, "refs/heads/"+target.Ref, "refs/tags/"+target.Ref)
		if err != nil {
			return "", "", err
		}
		for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
			fields := strings.Fields(line)
			if len(fields) >= 2 {
				return target.Ref, fields[0], nil
			}
		}
		return "", "", fmt.Errorf("Git ref %q was not found", target.Ref)
	}
	output, err := runRepositoryGit(ctx, os.TempDir(), 256*1024, "ls-remote", "--symref", remoteURL, "HEAD")
	if err != nil {
		return "", "", err
	}
	resolvedRef := ""
	commitSHA := ""
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 3 && fields[0] == "ref:" && fields[2] == "HEAD" {
			resolvedRef = strings.TrimPrefix(fields[1], "refs/heads/")
		}
		if len(fields) >= 2 && fields[1] == "HEAD" && fields[0] != "ref:" {
			commitSHA = fields[0]
		}
	}
	if resolvedRef == "" || commitSHA == "" {
		return "", "", errors.New("GitHub default branch could not be resolved")
	}
	return resolvedRef, commitSHA, nil
}

type repositoryArchiveEntry struct {
	Path string
	File *zip.File
}

func repositoryArchiveEntries(files []*zip.File) []repositoryArchiveEntry {
	entries := make([]repositoryArchiveEntry, 0, len(files))
	for _, file := range files {
		name := strings.TrimPrefix(filepath.ToSlash(file.Name), "/")
		separator := strings.IndexByte(name, '/')
		if separator < 0 || separator == len(name)-1 {
			continue
		}
		name = strings.TrimPrefix(name[separator+1:], "/")
		if name == "" {
			continue
		}
		entries = append(entries, repositoryArchiveEntry{Path: strings.TrimSuffix(name, "/"), File: file})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries
}

func readRepositoryArchivePath(entries []repositoryArchiveEntry, target githubRepositoryTarget, ref, commit string, maxEntries int) (string, error) {
	requested := cleanRepositoryPath(target.Path)
	for _, entry := range entries {
		if entry.Path != requested || entry.File.FileInfo().IsDir() {
			continue
		}
		content, err := readRepositoryArchiveFile(entry.File, maxRepositoryFileBytes)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Repository: %s\nRef: %s\nCommit: %s\nPath: %s\nSource: GitHub archive fallback\n\n%s", target.FullName(), ref, commit, requested, content), nil
	}

	prefix := strings.TrimSuffix(requested, "/") + "/"
	children := map[string]repositoryArchiveEntry{}
	for _, entry := range entries {
		if !strings.HasPrefix(entry.Path, prefix) {
			continue
		}
		remainder := strings.TrimPrefix(entry.Path, prefix)
		if remainder == "" {
			continue
		}
		name := strings.SplitN(remainder, "/", 2)[0]
		childPath := prefix + name
		if strings.Contains(remainder, "/") {
			children[childPath] = repositoryArchiveEntry{Path: childPath}
		} else {
			children[childPath] = entry
		}
	}
	if len(children) == 0 {
		candidates := make([]string, 0, len(entries))
		for _, entry := range entries {
			candidates = append(candidates, entry.Path)
		}
		return "", repositoryPathNotFoundError(requested, candidates)
	}
	paths := make([]string, 0, len(children))
	for childPath := range children {
		paths = append(paths, childPath)
	}
	sort.Strings(paths)
	limit := len(paths)
	if limit > maxEntries {
		limit = maxEntries
	}
	var output strings.Builder
	fmt.Fprintf(&output, "Repository: %s\nRef: %s\nCommit: %s\nPath: %s/\nSource: GitHub archive fallback\nEntries:\n", target.FullName(), ref, commit, requested)
	for _, childPath := range paths[:limit] {
		entry := children[childPath]
		label := strings.TrimPrefix(childPath, prefix)
		if entry.File == nil || entry.File.FileInfo().IsDir() {
			label += "/"
		} else if entry.File.UncompressedSize64 > 0 {
			label += fmt.Sprintf(" (%d bytes)", entry.File.UncompressedSize64)
		}
		output.WriteString(label + "\n")
	}
	if len(paths) > limit {
		fmt.Fprintf(&output, "... showing %d of %d entries\n", limit, len(paths))
	}
	return strings.TrimSpace(output.String()), nil
}

func readRepositoryArchiveReadme(entries []repositoryArchiveEntry) string {
	for _, entry := range entries {
		if strings.Contains(entry.Path, "/") || !strings.HasPrefix(strings.ToLower(entry.Path), "readme") || entry.File.FileInfo().IsDir() {
			continue
		}
		if content, err := readRepositoryArchiveFile(entry.File, maximumRepositoryReadme); err == nil {
			return content
		}
	}
	return ""
}

func readRepositoryArchiveFile(file *zip.File, limit int) (string, error) {
	if file.UncompressedSize64 > uint64(limit) {
		return "", fmt.Errorf("repository file %s exceeds %d bytes", file.Name, limit)
	}
	reader, err := file.Open()
	if err != nil {
		return "", err
	}
	defer reader.Close()
	content, err := io.ReadAll(io.LimitReader(reader, int64(limit)+1))
	if err != nil {
		return "", err
	}
	if len(content) > limit {
		return "", fmt.Errorf("repository file %s exceeds %d bytes", file.Name, limit)
	}
	if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
		return "", fmt.Errorf("repository file %s is not UTF-8 text", file.Name)
	}
	return string(content), nil
}

func formatRepositoryArchiveTree(entries []repositoryArchiveEntry, maxEntries int) string {
	if maxEntries <= 0 || maxEntries > maximumRepositoryEntries {
		maxEntries = defaultRepositoryEntries
	}
	limit := len(entries)
	if limit > maxEntries {
		limit = maxEntries
	}
	lines := make([]string, 0, limit+1)
	for _, entry := range entries[:limit] {
		label := entry.Path
		if entry.File.FileInfo().IsDir() {
			label += "/"
		} else if entry.File.UncompressedSize64 > 0 {
			label += fmt.Sprintf(" (%d bytes)", entry.File.UncompressedSize64)
		}
		lines = append(lines, label)
	}
	if len(entries) > limit {
		lines = append(lines, fmt.Sprintf("... tree truncated; showing %d of %d entries", limit, len(entries)))
	}
	return strings.Join(lines, "\n")
}

func readGitHubRepositoryWithGit(ctx context.Context, target githubRepositoryTarget, maxEntries int) (string, string, error) {
	if _, err := exec.LookPath("git"); err != nil {
		return "", "", errors.New("Git executable was not found")
	}
	repositoryGitCacheMu.Lock()
	defer repositoryGitCacheMu.Unlock()

	repositoryDir, revision, resolvedRef, err := prepareGitRepositoryCache(ctx, target)
	if err != nil {
		return "", "", err
	}
	commit, err := runRepositoryGit(ctx, repositoryDir, 128*1024, "rev-parse", revision)
	if err != nil {
		return "", "", err
	}
	commitSHA := strings.TrimSpace(string(commit))

	if target.Path != "" {
		output, readErr := readGitRepositoryPath(ctx, repositoryDir, revision, target, resolvedRef, commitSHA, maxEntries)
		return output, resolvedRef, readErr
	}

	treeRaw, err := runRepositoryGit(ctx, repositoryDir, maximumRepositoryGitOutput, "ls-tree", "-r", "-t", "--long", revision)
	if err != nil {
		return "", "", err
	}
	readme := readGitRepositoryReadme(ctx, repositoryDir, revision, treeRaw)
	var output strings.Builder
	fmt.Fprintf(&output, "Repository: %s\nURL: %s\nRef: %s\nCommit: %s\nSource: git clone fallback\n", target.FullName(), target.CanonicalURL(), resolvedRef, commitSHA)
	if readme != "" {
		output.WriteString("\nREADME:\n")
		output.WriteString(readme)
		output.WriteString("\n")
	}
	output.WriteString("\nRepository tree:\n")
	output.WriteString(formatGitRepositoryTree(treeRaw, maxEntries))
	return strings.TrimSpace(output.String()), resolvedRef, nil
}

func prepareGitRepositoryCache(ctx context.Context, target githubRepositoryTarget) (string, string, string, error) {
	cacheRoot, err := repositoryCacheRoot()
	if err != nil {
		return "", "", "", err
	}
	if err := os.MkdirAll(cacheRoot, 0o700); err != nil {
		return "", "", "", fmt.Errorf("create repository cache: %w", err)
	}
	keyBytes := sha256.Sum256([]byte(target.CanonicalURL()))
	repositoryDir := filepath.Join(cacheRoot, hex.EncodeToString(keyBytes[:12]))
	gitDir := filepath.Join(repositoryDir, ".git")
	if info, statErr := os.Stat(gitDir); statErr != nil || !info.IsDir() {
		if err := os.RemoveAll(repositoryDir); err != nil {
			return "", "", "", fmt.Errorf("reset repository cache: %w", err)
		}
		cloneArgs := []string{"clone", "--depth", "1", "--filter=blob:none", "--no-checkout", target.CanonicalURL() + ".git", repositoryDir}
		if _, cloneErr := runRepositoryGit(ctx, cacheRoot, maximumRepositoryGitOutput, cloneArgs...); cloneErr != nil {
			_ = os.RemoveAll(repositoryDir)
			cloneArgs = []string{"clone", "--depth", "1", "--no-checkout", target.CanonicalURL() + ".git", repositoryDir}
			if _, retryErr := runRepositoryGit(ctx, cacheRoot, maximumRepositoryGitOutput, cloneArgs...); retryErr != nil {
				return "", "", "", retryErr
			}
		}
	}

	revision := "HEAD"
	resolvedRef := strings.TrimSpace(target.Ref)
	if resolvedRef != "" {
		if _, err := runRepositoryGit(ctx, repositoryDir, maximumRepositoryGitOutput, "fetch", "--depth", "1", "origin", resolvedRef); err != nil {
			return "", "", "", fmt.Errorf("fetch ref %q: %w", resolvedRef, err)
		}
		revision = "FETCH_HEAD"
	} else {
		if _, err := runRepositoryGit(ctx, repositoryDir, maximumRepositoryGitOutput, "fetch", "--depth", "1", "origin", "HEAD"); err == nil {
			revision = "FETCH_HEAD"
		}
		if branch, branchErr := runRepositoryGit(ctx, repositoryDir, 64*1024, "symbolic-ref", "--short", "HEAD"); branchErr == nil {
			resolvedRef = strings.TrimSpace(string(branch))
		}
		if resolvedRef == "" {
			resolvedRef = "default branch"
		}
	}
	return repositoryDir, revision, resolvedRef, nil
}

func repositoryCacheRoot() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(root) == "" {
		root = os.TempDir()
	}
	if strings.TrimSpace(root) == "" {
		return "", errors.New("repository cache directory is unavailable")
	}
	return filepath.Join(root, "EchoCode", "repositories"), nil
}

func readGitRepositoryPath(
	ctx context.Context,
	repositoryDir string,
	revision string,
	target githubRepositoryTarget,
	resolvedRef string,
	commitSHA string,
	maxEntries int,
) (string, error) {
	object := revision + ":" + target.Path
	typeRaw, err := runRepositoryGit(ctx, repositoryDir, 64*1024, "cat-file", "-t", object)
	if err != nil {
		candidates := gitRepositoryPathCandidates(ctx, repositoryDir, revision)
		if len(candidates) > 0 {
			return "", repositoryPathNotFoundError(target.Path, candidates)
		}
		return "", fmt.Errorf("read repository path %q: %w", target.Path, err)
	}
	switch strings.TrimSpace(string(typeRaw)) {
	case "tree":
		entries, err := runRepositoryGit(ctx, repositoryDir, maximumRepositoryGitOutput, "ls-tree", "--long", object)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("Repository: %s\nRef: %s\nCommit: %s\nPath: %s/\nSource: git clone fallback\nEntries:\n%s", target.FullName(), resolvedRef, commitSHA, target.Path, formatGitRepositoryTree(entries, maxEntries)), nil
	case "blob":
		content, err := runRepositoryGit(ctx, repositoryDir, maxRepositoryFileBytes+1, "show", object)
		if err != nil {
			return "", err
		}
		if len(content) > maxRepositoryFileBytes {
			return "", fmt.Errorf("repository file %s exceeds %d bytes", target.Path, maxRepositoryFileBytes)
		}
		if !utf8.Valid(content) || bytes.IndexByte(content, 0) >= 0 {
			return "", fmt.Errorf("repository file %s is not UTF-8 text", target.Path)
		}
		return fmt.Sprintf("Repository: %s\nRef: %s\nCommit: %s\nPath: %s\nSource: git clone fallback\n\n%s", target.FullName(), resolvedRef, commitSHA, target.Path, string(content)), nil
	default:
		return "", fmt.Errorf("repository path %s is not a file or directory", target.Path)
	}
}

func readGitRepositoryReadme(ctx context.Context, repositoryDir, revision string, treeRaw []byte) string {
	for _, line := range strings.Split(string(treeRaw), "\n") {
		separator := strings.IndexByte(line, '\t')
		if separator < 0 {
			continue
		}
		filePath := strings.TrimSpace(line[separator+1:])
		if strings.Contains(filePath, "/") || !strings.HasPrefix(strings.ToLower(filePath), "readme") {
			continue
		}
		content, err := runRepositoryGit(ctx, repositoryDir, maximumRepositoryReadme+1, "show", revision+":"+filePath)
		if err == nil && len(content) <= maximumRepositoryReadme && utf8.Valid(content) {
			return string(content)
		}
	}
	return ""
}

func formatGitRepositoryTree(raw []byte, maxEntries int) string {
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if maxEntries <= 0 || maxEntries > maximumRepositoryEntries {
		maxEntries = defaultRepositoryEntries
	}
	limit := len(lines)
	if limit > maxEntries {
		limit = maxEntries
	}
	formatted := make([]string, 0, limit+1)
	for _, line := range lines[:limit] {
		separator := strings.IndexByte(line, '\t')
		if separator < 0 {
			continue
		}
		metadata := strings.Fields(line[:separator])
		filePath := strings.TrimSpace(line[separator+1:])
		if len(metadata) >= 2 && metadata[1] == "tree" {
			filePath += "/"
		}
		if len(metadata) >= 4 && metadata[1] == "blob" && metadata[3] != "-" {
			filePath += " (" + metadata[3] + " bytes)"
		}
		formatted = append(formatted, filePath)
	}
	if len(lines) > limit {
		formatted = append(formatted, fmt.Sprintf("... tree truncated; showing %d of %d entries", limit, len(lines)))
	}
	return strings.Join(formatted, "\n")
}

func runRepositoryGit(ctx context.Context, directory string, limit int, args ...string) ([]byte, error) {
	runCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	command := exec.CommandContext(runCtx, "git", args...)
	command.Dir = directory
	hideConsoleWindow(command)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		if runCtx.Err() != nil {
			return nil, fmt.Errorf("git %s: %w", strings.Join(args, " "), runCtx.Err())
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("git %s failed: %s", strings.Join(args, " "), detail)
	}
	if limit > 0 && stdout.Len() > limit {
		return stdout.Bytes()[:limit], nil
	}
	return stdout.Bytes(), nil
}

func gitRepositoryPathCandidates(ctx context.Context, repositoryDir, revision string) []string {
	raw, err := runRepositoryGit(ctx, repositoryDir, maximumRepositoryGitOutput, "ls-tree", "-r", "-t", "--name-only", revision)
	if err != nil {
		return nil
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	paths := make([]string, 0, len(lines))
	for _, line := range lines {
		if candidate := cleanRepositoryPath(strings.TrimSpace(line)); candidate != "" {
			paths = append(paths, candidate)
		}
	}
	return paths
}

type repositoryPathSuggestion struct {
	path  string
	score int
}

func repositoryPathNotFoundError(requested string, candidates []string) error {
	requested = cleanRepositoryPath(requested)
	suggestions := repositoryPathSuggestions(requested, candidates, 5)
	if len(suggestions) == 0 {
		return fmt.Errorf("repository path %q was not found", requested)
	}
	var message strings.Builder
	fmt.Fprintf(&message, "repository path %q was not found; similar paths:", requested)
	for _, suggestion := range suggestions {
		message.WriteString("\n- ")
		message.WriteString(suggestion)
	}
	return errors.New(message.String())
}

func repositoryPathSuggestions(requested string, candidates []string, limit int) []string {
	requested = strings.ToLower(cleanRepositoryPath(requested))
	if requested == "" || limit <= 0 {
		return nil
	}
	requestedBase := path.Base(requested)
	requestedExt := strings.ToLower(path.Ext(requestedBase))
	seen := make(map[string]bool, len(candidates))
	ranked := make([]repositoryPathSuggestion, 0, len(candidates))
	for _, candidate := range candidates {
		candidate = cleanRepositoryPath(candidate)
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		lowerCandidate := strings.ToLower(candidate)
		score := repositoryPathEditDistance(requested, lowerCandidate) * 4
		candidateBase := path.Base(lowerCandidate)
		if candidateBase == requestedBase {
			score -= 100
		} else if strings.Contains(candidateBase, requestedBase) || strings.Contains(requestedBase, candidateBase) {
			score -= 30
		}
		if requestedExt != "" && path.Ext(candidateBase) == requestedExt {
			score -= 12
		}
		score -= repositoryCommonPathSegments(requested, lowerCandidate) * 18
		ranked = append(ranked, repositoryPathSuggestion{path: candidate, score: score})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score == ranked[j].score {
			return ranked[i].path < ranked[j].path
		}
		return ranked[i].score < ranked[j].score
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	result := make([]string, 0, len(ranked))
	for _, item := range ranked {
		result = append(result, item.path)
	}
	return result
}

func repositoryCommonPathSegments(left, right string) int {
	leftParts := strings.Split(left, "/")
	rightParts := strings.Split(right, "/")
	limit := len(leftParts)
	if len(rightParts) < limit {
		limit = len(rightParts)
	}
	count := 0
	for index := 0; index < limit && leftParts[index] == rightParts[index]; index++ {
		count++
	}
	return count
}

func repositoryPathEditDistance(left, right string) int {
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	previous := make([]int, len(rightRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for leftIndex, leftRune := range leftRunes {
		current := make([]int, len(rightRunes)+1)
		current[0] = leftIndex + 1
		for rightIndex, rightRune := range rightRunes {
			cost := 0
			if leftRune != rightRune {
				cost = 1
			}
			current[rightIndex+1] = minInt(
				current[rightIndex]+1,
				previous[rightIndex+1]+1,
				previous[rightIndex]+cost,
			)
		}
		previous = current
	}
	return previous[len(rightRunes)]
}

func minInt(values ...int) int {
	minimum := values[0]
	for _, value := range values[1:] {
		if value < minimum {
			minimum = value
		}
	}
	return minimum
}

type githubTokenTransport struct {
	base  http.RoundTripper
	token string
}

func (t githubTokenTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	clone.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(clone)
}

func withGitHubToken(client *http.Client, token string) *http.Client {
	clone := *client
	transport := client.Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	clone.Transport = githubTokenTransport{base: transport, token: token}
	return &clone
}

func repositoryGitHubToken(explicit, apiBase string) string {
	if token := strings.TrimSpace(explicit); token != "" {
		return token
	}
	if !strings.EqualFold(strings.TrimRight(apiBase, "/"), defaultGitHubAPIBase) {
		return ""
	}
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(os.Getenv("GH_TOKEN"))
}

func decodeGitHubContent(file githubContent, limit int) (string, error) {
	if file.Type != "file" && file.Type != "" {
		return "", fmt.Errorf("%s 不是可读取的文件", file.Path)
	}
	if file.Size > limit {
		return "", fmt.Errorf("仓库文件 %s 过大（%d bytes），读取上限为 %d bytes", file.Path, file.Size, limit)
	}
	if !strings.EqualFold(file.Encoding, "base64") {
		return "", fmt.Errorf("仓库文件 %s 使用了不支持的编码 %q", file.Path, file.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(file.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("解码仓库文件 %s 失败: %w", file.Path, err)
	}
	if len(decoded) > limit {
		return "", fmt.Errorf("仓库文件 %s 解码后超过读取上限", file.Path)
	}
	if !utf8.Valid(decoded) || bytes.IndexByte(decoded, 0) >= 0 {
		return "", fmt.Errorf("仓库文件 %s 不是 UTF-8 文本文件", file.Path)
	}
	return string(decoded), nil
}

func escapeRepositoryPath(value string) string {
	segments := strings.Split(cleanRepositoryPath(value), "/")
	for index := range segments {
		segments[index] = url.PathEscape(segments[index])
	}
	return strings.Join(segments, "/")
}

func githubGetJSON(ctx context.Context, client *http.Client, endpoint string, target any) error {
	body, err := githubGet(ctx, client, endpoint)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("解析 GitHub API 响应失败: %w", err)
	}
	return nil
}

func githubGet(ctx context.Context, client *http.Client, endpoint string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "EchoCode/1.0")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("GitHub 请求失败: %w", err)
	}
	defer response.Body.Close()
	body, readErr := io.ReadAll(io.LimitReader(response.Body, maxRepositoryAPIResponse+1))
	if readErr != nil {
		return nil, fmt.Errorf("读取 GitHub 响应失败: %w", readErr)
	}
	if len(body) > maxRepositoryAPIResponse {
		return nil, errors.New("GitHub 响应超过大小限制")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		detail := strings.TrimSpace(string(body))
		if len(detail) > 400 {
			detail = detail[:400]
		}
		return nil, fmt.Errorf("GitHub API HTTP %d: %s", response.StatusCode, detail)
	}
	return body, nil
}

func repositoryReadError(message, input string) Result {
	return Result{
		Summary: message,
		IsError: true,
		Parts: []ResultPart{{
			Kind: PartToolCall, Name: "read_repository", Status: "error", Input: strings.TrimSpace(input), Output: message,
		}},
	}
}
