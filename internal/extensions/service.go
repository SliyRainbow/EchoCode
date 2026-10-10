package extensions

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

const DefaultRegistryURL = "https://raw.githubusercontent.com/SliyRainbow/EchoCode_Extensions/main/registry.json"

const (
	maxCatalogBytes  = 4 << 20
	maxDownloadBytes = 1 << 30
)

type Options struct {
	RegistryURL string
	CachePath   string
	InstallRoot string
	HTTPClient  *http.Client
}

type Service struct {
	registryURL    string
	cachePath      string
	installRoot    string
	httpClient     *http.Client
	cleanupWarning string
	mu             sync.Mutex
}

func New(options Options) *Service {
	registryURL := strings.TrimSpace(options.RegistryURL)
	if registryURL == "" {
		registryURL = DefaultRegistryURL
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	return &Service{
		registryURL: registryURL,
		cachePath:   cleanOptionalPath(options.CachePath),
		installRoot: cleanOptionalPath(options.InstallRoot),
		httpClient:  client,
	}
}

func (s *Service) Catalog(ctx context.Context, refresh bool) (CatalogState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !refresh {
		if cached, err := s.readCatalogCache(); err == nil && len(cached.Packages) > 0 {
			return s.attachInstalled(cached), nil
		}
	}
	state, err := s.fetchCatalog(ctx)
	if err == nil {
		_ = s.writeJSONAtomic(s.cachePath, state)
		return s.attachInstalled(state), nil
	}
	cached, cacheErr := s.readCatalogCache()
	if cacheErr == nil && len(cached.Packages) > 0 {
		cached.Source = "cache"
		cached.Warning = fmt.Sprintf("扩展目录刷新失败，正在使用缓存：%v", err)
		return s.attachInstalled(cached), nil
	}
	return CatalogState{RegistryURL: s.registryURL}, err
}

func (s *Service) Install(ctx context.Context, id string) (InstallResult, error) {
	return s.InstallAndActivate(ctx, id, nil)
}

// InstallAndActivate keeps the previous version until the host has durably
// switched its MCP configuration. activate must not call back into Service.
func (s *Service) InstallAndActivate(ctx context.Context, id string, activate func(InstallResult) error) (InstallResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.catalogForInstall(ctx)
	if err != nil {
		return InstallResult{}, err
	}
	item, ok := findCatalogPackage(state.Packages, id)
	if !ok {
		return InstallResult{}, fmt.Errorf("扩展不存在：%s", id)
	}
	if safeSegment(item.ID) != item.ID || item.ID == "." || item.ID == ".." ||
		strings.TrimSpace(item.Manifest.Version) == "" || item.Manifest.Version == "." || item.Manifest.Version == ".." || strings.ContainsAny(item.Manifest.Version, `/\:`) {
		return InstallResult{}, errors.New("扩展 ID 或版本包含无效路径字符")
	}
	artifact, ok := artifactForCurrentPlatform(item.Manifest.Artifacts)
	if !ok {
		return InstallResult{}, fmt.Errorf("扩展 %s 不支持当前平台 %s/%s", id, runtime.GOOS, runtime.GOARCH)
	}
	installed, err := s.readInstalledState()
	if err != nil {
		return InstallResult{}, err
	}
	if current, found := findInstalled(installed.Packages, id); found && current.Version == item.Manifest.Version {
		if info, statErr := os.Stat(current.Executable); statErr == nil && !info.IsDir() {
			result := InstallResult{Package: current, Manifest: item.Manifest}
			if activate != nil {
				if err := activate(result); err != nil {
					return InstallResult{}, err
				}
			}
			return result, nil
		}
	}
	if s.installRoot == "" {
		return InstallResult{}, errors.New("扩展安装目录未配置")
	}
	if err := os.MkdirAll(filepath.Join(s.installRoot, ".staging"), 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("创建扩展暂存目录失败: %w", err)
	}
	staging, err := os.MkdirTemp(filepath.Join(s.installRoot, ".staging"), safeSegment(id)+"-")
	if err != nil {
		return InstallResult{}, fmt.Errorf("创建扩展暂存目录失败: %w", err)
	}
	defer os.RemoveAll(staging)

	archivePath := filepath.Join(staging, archiveFileName(artifact))
	if err := s.downloadAndVerify(ctx, artifact, archivePath); err != nil {
		return InstallResult{}, err
	}
	unpacked := filepath.Join(staging, "unpacked")
	if err := extractArchive(archivePath, artifact.Archive, unpacked); err != nil {
		return InstallResult{}, fmt.Errorf("解压扩展失败: %w", err)
	}
	packageRoot, err := safeJoin(unpacked, artifact.ArchiveRoot)
	if err != nil {
		return InstallResult{}, fmt.Errorf("扩展根目录无效: %w", err)
	}
	executable, err := safeJoin(packageRoot, artifact.Executable)
	if err != nil {
		return InstallResult{}, fmt.Errorf("扩展启动文件无效: %w", err)
	}
	if info, statErr := os.Stat(executable); statErr != nil || info.IsDir() {
		return InstallResult{}, fmt.Errorf("扩展启动文件不存在：%s", artifact.Executable)
	}
	if err := runHealthCheck(ctx, executable, item.Manifest.Install.HealthCheck); err != nil {
		return InstallResult{}, fmt.Errorf("扩展健康检查失败: %w", err)
	}

	target := filepath.Join(s.installRoot, safeSegment(id), item.Manifest.Version, platformKey())
	if err := ensureWithinRoot(s.installRoot, target); err != nil {
		return InstallResult{}, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return InstallResult{}, fmt.Errorf("创建扩展版本目录失败: %w", err)
	}
	finalExecutable := filepath.Join(target, filepath.FromSlash(artifact.Executable))
	result := InstalledPackage{
		ID:          item.ID,
		Type:        item.Type,
		Name:        item.Name,
		Version:     item.Manifest.Version,
		Platform:    artifact.Platform,
		Arch:        artifact.Arch,
		InstallDir:  target,
		Executable:  finalExecutable,
		InstalledAt: time.Now().UTC().Format(time.RFC3339),
	}
	if err := ctx.Err(); err != nil {
		return InstallResult{}, err
	}
	if err := s.commitInstallAndActivate(packageRoot, result, installed, func() error {
		if activate != nil {
			return activate(InstallResult{Package: result, Manifest: item.Manifest})
		}
		return nil
	}); err != nil {
		return InstallResult{}, err
	}
	return InstallResult{Package: result, Manifest: item.Manifest}, nil
}

// Keep the old files recoverable until installed.json commits. A failed repair
// of an existing version must not destroy the package it was meant to repair.
func (s *Service) commitInstall(packageRoot string, result InstalledPackage, installed installedState) error {
	return s.commitInstallAndActivate(packageRoot, result, installed, nil)
}

func (s *Service) commitInstallAndActivate(packageRoot string, result InstalledPackage, installed installedState, activate func() error) error {
	backup, err := s.stageInstalledDirectory(result.InstallDir)
	if err != nil {
		return err
	}
	rollback := func(cause error) error {
		if backup == "" {
			return cause
		}
		if err := os.Rename(backup, result.InstallDir); err != nil {
			return errors.Join(cause, fmt.Errorf("恢复旧扩展失败，备份保留在 %s: %w", backup, err))
		}
		_ = os.Remove(filepath.Dir(backup))
		return cause
	}
	if err := os.Rename(packageRoot, result.InstallDir); err != nil {
		return rollback(fmt.Errorf("提交扩展安装失败: %w", err))
	}
	previous, hadPrevious := findInstalled(installed.Packages, result.ID)
	next := installedState{Packages: upsertInstalled(installed.Packages, result)}
	if err := s.writeInstalledState(next); err != nil {
		if removeErr := removeWithinRoot(s.installRoot, result.InstallDir); removeErr != nil {
			return errors.Join(err, removeErr, fmt.Errorf("旧扩展备份保留在 %s", backup))
		}
		return rollback(err)
	}
	if activate != nil {
		if err := activate(); err != nil {
			if stateErr := s.writeInstalledState(installed); stateErr != nil {
				return errors.Join(err, fmt.Errorf("恢复安装状态失败，旧版本文件仍保留（修复备份：%s）: %w", backup, stateErr))
			}
			if removeErr := removeWithinRoot(s.installRoot, result.InstallDir); removeErr != nil {
				return errors.Join(err, removeErr, fmt.Errorf("旧扩展备份保留在 %s", backup))
			}
			return rollback(err)
		}
	}
	if backup != "" {
		s.cleanupDirectory(filepath.Dir(backup))
	}
	if hadPrevious && filepath.Clean(previous.InstallDir) != filepath.Clean(result.InstallDir) {
		s.cleanupDirectory(previous.InstallDir)
	}
	return nil
}

// Stage on the same volume so moving an installed directory is atomic. The
// caller owns cleanup only after metadata commits or the directory is restored.
func (s *Service) stageInstalledDirectory(target string) (string, error) {
	if strings.TrimSpace(s.installRoot) == "" || strings.TrimSpace(target) == "" {
		return "", errors.New("扩展安装路径为空")
	}
	if err := ensureWithinRoot(s.installRoot, target); err != nil {
		return "", err
	}
	rootAbs, _ := filepath.Abs(s.installRoot)
	targetAbs, _ := filepath.Abs(target)
	if strings.EqualFold(rootAbs, targetAbs) {
		return "", errors.New("拒绝移动扩展根目录")
	}
	if _, err := os.Lstat(target); errors.Is(err, os.ErrNotExist) {
		return "", nil
	} else if err != nil {
		return "", err
	}
	stageRoot := filepath.Join(s.installRoot, ".staging")
	if err := os.MkdirAll(stageRoot, 0o755); err != nil {
		return "", err
	}
	stage, err := os.MkdirTemp(stageRoot, "rollback-")
	if err != nil {
		return "", err
	}
	backup := filepath.Join(stage, "package")
	if err := os.Rename(target, backup); err != nil {
		_ = os.Remove(stage)
		return "", fmt.Errorf("暂存旧扩展失败: %w", err)
	}
	return backup, nil
}

func (s *Service) Uninstall(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	installed, err := s.readInstalledState()
	if err != nil {
		return err
	}
	current, ok := findInstalled(installed.Packages, id)
	if !ok {
		return nil
	}
	return s.commitUninstall(current, installed)
}

func (s *Service) commitUninstall(current InstalledPackage, installed installedState) error {
	backup, err := s.stageInstalledDirectory(current.InstallDir)
	if err != nil {
		return err
	}
	next := make([]InstalledPackage, 0, len(installed.Packages))
	for _, item := range installed.Packages {
		if item.ID != current.ID {
			next = append(next, item)
		}
	}
	installed.Packages = next
	if err := s.writeInstalledState(installed); err != nil {
		if backup != "" {
			if restoreErr := os.Rename(backup, current.InstallDir); restoreErr != nil {
				return errors.Join(err, fmt.Errorf("恢复扩展失败，备份保留在 %s: %w", backup, restoreErr))
			}
			_ = os.Remove(filepath.Dir(backup))
		}
		return err
	}
	if backup != "" {
		s.cleanupDirectory(filepath.Dir(backup))
	}
	return nil
}

func (s *Service) cleanupDirectory(path string) {
	if err := removeWithinRoot(s.installRoot, path); err != nil {
		s.cleanupWarning = fmt.Sprintf("扩展状态已保存，但旧文件清理失败（%s）：%v", path, err)
	}
}

func (s *Service) Installed(id string) (InstalledPackage, bool) {
	installed, found, _ := s.LookupInstalled(id)
	return installed, found
}

func (s *Service) LookupInstalled(id string) (InstalledPackage, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readInstalledState()
	if err != nil {
		return InstalledPackage{}, false, err
	}
	installed, found := findInstalled(state.Packages, id)
	return installed, found, nil
}

func (s *Service) RunProjectAction(ctx context.Context, id, actionID, workspaceRoot string) (ActionResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	workspaceRoot = strings.TrimSpace(workspaceRoot)
	if workspaceRoot == "" {
		return ActionResult{}, errors.New("当前项目工作区为空")
	}
	absolute, err := filepath.Abs(workspaceRoot)
	if err != nil {
		return ActionResult{}, fmt.Errorf("解析项目路径失败: %w", err)
	}
	installed, err := s.readInstalledState()
	if err != nil {
		return ActionResult{}, err
	}
	current, ok := findInstalled(installed.Packages, id)
	if !ok {
		return ActionResult{}, fmt.Errorf("扩展尚未安装：%s", id)
	}
	state, err := s.catalogForInstall(ctx)
	if err != nil {
		return ActionResult{}, err
	}
	item, ok := findCatalogPackage(state.Packages, id)
	if !ok {
		return ActionResult{}, fmt.Errorf("扩展目录中不存在：%s", id)
	}
	var action ProjectAction
	found := false
	for _, candidate := range item.Manifest.ProjectActions {
		if candidate.ID == actionID {
			action = candidate
			found = true
			break
		}
	}
	if !found {
		return ActionResult{}, fmt.Errorf("扩展操作不存在：%s", actionID)
	}
	args := make([]string, len(action.Args))
	for index, value := range action.Args {
		args[index] = strings.ReplaceAll(value, "${workspaceRoot}", absolute)
	}
	startedAt := time.Now()
	command := exec.CommandContext(ctx, current.Executable, args...)
	command.Dir = absolute
	output, commandErr := command.CombinedOutput()
	exitCode := 0
	if commandErr != nil {
		exitCode = -1
		if exitErr := new(exec.ExitError); errors.As(commandErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}
	result := ActionResult{
		ID:         action.ID,
		Output:     truncateOutput(string(output), 64<<10),
		ExitCode:   exitCode,
		DurationMs: time.Since(startedAt).Milliseconds(),
	}
	if commandErr != nil {
		return result, fmt.Errorf("扩展操作失败: %w", commandErr)
	}
	return result, nil
}

func (s *Service) fetchCatalog(ctx context.Context) (CatalogState, error) {
	var registry Registry
	if err := s.fetchJSON(ctx, s.registryURL, &registry); err != nil {
		return CatalogState{}, fmt.Errorf("读取扩展注册表失败: %w", err)
	}
	if registry.SchemaVersion != 1 {
		return CatalogState{}, fmt.Errorf("不支持的扩展注册表版本：%d", registry.SchemaVersion)
	}
	packages := make([]CatalogPackage, 0, len(registry.Packages))
	seen := map[string]bool{}
	for _, entry := range registry.Packages {
		if err := validateRegistryEntry(entry, seen); err != nil {
			return CatalogState{}, err
		}
		manifestURL, err := resolveURL(s.registryURL, entry.Manifest)
		if err != nil {
			return CatalogState{}, fmt.Errorf("扩展 %s 清单地址无效: %w", entry.ID, err)
		}
		var manifest Manifest
		if err := s.fetchJSON(ctx, manifestURL, &manifest); err != nil {
			return CatalogState{}, fmt.Errorf("读取扩展 %s 清单失败: %w", entry.ID, err)
		}
		if err := validateManifest(entry, manifest); err != nil {
			return CatalogState{}, err
		}
		_, platformAvailable := artifactForCurrentPlatform(manifest.Artifacts)
		packages = append(packages, CatalogPackage{
			ID:                entry.ID,
			Type:              entry.Type,
			Name:              entry.Name,
			Summary:           entry.Summary,
			Publisher:         entry.Publisher,
			Featured:          entry.Featured,
			SourceVerified:    entry.SourceVerified,
			ManifestURL:       manifestURL,
			Manifest:          manifest,
			PlatformAvailable: platformAvailable,
		})
	}
	sort.SliceStable(packages, func(i, j int) bool {
		if packages[i].Featured != packages[j].Featured {
			return packages[i].Featured
		}
		return strings.ToLower(packages[i].Name) < strings.ToLower(packages[j].Name)
	})
	return CatalogState{
		RegistryURL: s.registryURL,
		Source:      "network",
		CheckedAt:   time.Now().UTC().Format(time.RFC3339),
		Packages:    packages,
	}, nil
}

func (s *Service) fetchJSON(ctx context.Context, source string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "EchoCode extension registry")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d", response.StatusCode)
	}
	if response.ContentLength > maxCatalogBytes {
		return fmt.Errorf("响应超过 %d MiB 上限", maxCatalogBytes>>20)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxCatalogBytes+1))
	if err != nil {
		return err
	}
	if len(payload) > maxCatalogBytes {
		return fmt.Errorf("响应超过 %d MiB 上限", maxCatalogBytes>>20)
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return err
	}
	return nil
}

func (s *Service) downloadAndVerify(ctx context.Context, artifact Artifact, destination string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "EchoCode extension installer")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("下载扩展失败: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("下载扩展失败: HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("创建扩展下载文件失败: %w", err)
	}
	hash := sha256.New()
	written, copyErr := io.Copy(io.MultiWriter(file, hash), io.LimitReader(response.Body, maxDownloadBytes+1))
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("保存扩展下载失败: %w", copyErr)
	}
	if closeErr != nil {
		return fmt.Errorf("关闭扩展下载文件失败: %w", closeErr)
	}
	if written > maxDownloadBytes {
		return fmt.Errorf("扩展下载超过 %d MiB 上限", maxDownloadBytes>>20)
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, artifact.SHA256) {
		return fmt.Errorf("扩展 SHA-256 校验失败：expected=%s actual=%s", artifact.SHA256, actual)
	}
	return nil
}

func (s *Service) attachInstalled(state CatalogState) CatalogState {
	state.Warning = strings.TrimSpace(state.Warning + "\n" + s.cleanupWarning)
	installed, err := s.readInstalledState()
	if err != nil {
		state.Warning = strings.TrimSpace(state.Warning + "\n扩展安装状态读取失败，已保留原文件：" + err.Error())
		return state
	}
	for index := range state.Packages {
		if current, ok := findInstalled(installed.Packages, state.Packages[index].ID); ok {
			copy := current
			state.Packages[index].Installed = &copy
			state.Packages[index].UpdateAvailable = current.Version != state.Packages[index].Manifest.Version
		}
	}
	return state
}

func (s *Service) catalogForInstall(ctx context.Context) (CatalogState, error) {
	if cached, err := s.readCatalogCache(); err == nil && len(cached.Packages) > 0 {
		return cached, nil
	}
	state, err := s.fetchCatalog(ctx)
	if err != nil {
		return CatalogState{}, err
	}
	_ = s.writeJSONAtomic(s.cachePath, state)
	return state, nil
}

func (s *Service) readCatalogCache() (CatalogState, error) {
	var state CatalogState
	if strings.TrimSpace(s.cachePath) == "" {
		return state, os.ErrNotExist
	}
	data, err := os.ReadFile(s.cachePath)
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return CatalogState{}, err
	}
	state.Source = "cache"
	return state, nil
}

func (s *Service) readInstalledState() (installedState, error) {
	state := installedState{Packages: []InstalledPackage{}}
	if s.installRoot == "" {
		return state, nil
	}
	data, err := os.ReadFile(filepath.Join(s.installRoot, "installed.json"))
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, err
	}
	if err := json.Unmarshal(data, &state); err != nil {
		return installedState{}, fmt.Errorf("读取扩展安装状态失败: %w", err)
	}
	return state, nil
}

func (s *Service) writeInstalledState(state installedState) error {
	return s.writeJSONAtomic(filepath.Join(s.installRoot, "installed.json"), state)
}

func (s *Service) writeJSONAtomic(destination string, value any) error {
	if strings.TrimSpace(destination) == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(destination), ".echocode-extension-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	keepTemporary := true
	defer func() {
		if keepTemporary {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(append(encoded, '\n')); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	if err := replaceFileAtomic(temporaryPath, destination); err != nil {
		return err
	}
	keepTemporary = false
	return nil
}

func validateRegistryEntry(entry RegistryEntry, seen map[string]bool) error {
	entry.ID = strings.TrimSpace(entry.ID)
	if entry.ID == "" || seen[entry.ID] {
		return fmt.Errorf("扩展注册表包含空 ID 或重复 ID：%s", entry.ID)
	}
	seen[entry.ID] = true
	if entry.Type != "mcp" && entry.Type != "plugin" && entry.Type != "skill" {
		return fmt.Errorf("扩展 %s 类型无效：%s", entry.ID, entry.Type)
	}
	if strings.TrimSpace(entry.Manifest) == "" {
		return fmt.Errorf("扩展 %s 缺少 manifest", entry.ID)
	}
	return nil
}

func validateManifest(entry RegistryEntry, manifest Manifest) error {
	if manifest.SchemaVersion != 1 || manifest.ID != entry.ID || manifest.Type != entry.Type {
		return fmt.Errorf("扩展 %s manifest 身份或版本不匹配", entry.ID)
	}
	if strings.TrimSpace(manifest.Version) == "" || strings.EqualFold(manifest.Version, "latest") {
		return fmt.Errorf("扩展 %s 必须使用固定版本", entry.ID)
	}
	if manifest.Type == "mcp" && (manifest.MCP == nil || manifest.MCP.Transport != "stdio") {
		return fmt.Errorf("扩展 %s 缺少受支持的 stdio MCP 配置", entry.ID)
	}
	for _, artifact := range manifest.Artifacts {
		if _, err := url.ParseRequestURI(artifact.URL); err != nil || !strings.HasPrefix(strings.ToLower(artifact.URL), "https://") {
			return fmt.Errorf("扩展 %s 包含无效下载地址", entry.ID)
		}
		if len(artifact.SHA256) != 64 {
			return fmt.Errorf("扩展 %s 包含无效 SHA-256", entry.ID)
		}
	}
	return nil
}

func resolveURL(base, reference string) (string, error) {
	baseURL, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	referenceURL, err := url.Parse(reference)
	if err != nil {
		return "", err
	}
	resolved := baseURL.ResolveReference(referenceURL)
	if resolved.Scheme != "https" && resolved.Scheme != "http" {
		return "", fmt.Errorf("不支持的 URL scheme：%s", resolved.Scheme)
	}
	return resolved.String(), nil
}

func artifactForCurrentPlatform(artifacts []Artifact) (Artifact, bool) {
	platform := runtime.GOOS
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	for _, artifact := range artifacts {
		if artifact.Platform == platform && artifact.Arch == arch {
			return artifact, true
		}
	}
	return Artifact{}, false
}

func platformKey() string {
	arch := runtime.GOARCH
	if arch == "amd64" {
		arch = "x64"
	}
	return runtime.GOOS + "-" + arch
}

func archiveFileName(artifact Artifact) string {
	parsed, err := url.Parse(artifact.URL)
	if err == nil {
		if name := pathBase(parsed.Path); name != "" {
			return name
		}
	}
	return "extension." + strings.ReplaceAll(artifact.Archive, ".", "-")
}

func pathBase(value string) string {
	value = strings.TrimRight(strings.ReplaceAll(value, "\\", "/"), "/")
	if index := strings.LastIndex(value, "/"); index >= 0 {
		return value[index+1:]
	}
	return value
}

func safeSegment(value string) string {
	var builder strings.Builder
	for _, char := range strings.TrimSpace(value) {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '-' || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "extension"
	}
	return builder.String()
}

func findCatalogPackage(packages []CatalogPackage, id string) (CatalogPackage, bool) {
	for _, item := range packages {
		if item.ID == strings.TrimSpace(id) {
			return item, true
		}
	}
	return CatalogPackage{}, false
}

func findInstalled(packages []InstalledPackage, id string) (InstalledPackage, bool) {
	for _, item := range packages {
		if item.ID == strings.TrimSpace(id) {
			return item, true
		}
	}
	return InstalledPackage{}, false
}

func upsertInstalled(packages []InstalledPackage, next InstalledPackage) []InstalledPackage {
	result := make([]InstalledPackage, 0, len(packages)+1)
	replaced := false
	for _, item := range packages {
		if item.ID == next.ID {
			if !replaced {
				result = append(result, next)
				replaced = true
			}
			continue
		}
		result = append(result, item)
	}
	if !replaced {
		result = append(result, next)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

func runHealthCheck(parent context.Context, executable string, check HealthCheck) error {
	if check.TimeoutSeconds <= 0 {
		return nil
	}
	timeout := time.Duration(check.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	command := extensionCommandContext(ctx, executable, check.Args...)
	command.Dir = filepath.Dir(executable)
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, truncateOutput(string(output), 4096))
	}
	return nil
}

func cleanOptionalPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return filepath.Clean(value)
}

func truncateOutput(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[:limit] + fmt.Sprintf("\n... [truncated %d bytes]", len(value)-limit)
}
