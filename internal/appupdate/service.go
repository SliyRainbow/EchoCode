package appupdate

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultOwner      = "SliyRainbow"
	defaultRepository = "EchoCode"
	checkCacheTTL     = 6 * time.Hour
	maxChecksumBytes  = 1 << 20
)

type Options struct {
	CurrentVersion string
	Commit         string
	BuildDate      string
	Owner          string
	Repository     string
	CacheDir       string
	HTTPClient     *http.Client
}

type AppInfo struct {
	Name            string `json:"name"`
	Version         string `json:"version"`
	Commit          string `json:"commit,omitempty"`
	BuildDate       string `json:"buildDate,omitempty"`
	GoVersion       string `json:"goVersion"`
	OperatingSystem string `json:"operatingSystem"`
	Architecture    string `json:"architecture"`
	ExecutablePath  string `json:"executablePath"`
	ConfigPath      string `json:"configPath"`
	RepositoryURL   string `json:"repositoryUrl"`
}

type State struct {
	CurrentVersion   string  `json:"currentVersion"`
	LatestVersion    string  `json:"latestVersion,omitempty"`
	UpdateAvailable  bool    `json:"updateAvailable"`
	Status           string  `json:"status"`
	Message          string  `json:"message"`
	Progress         float64 `json:"progress"`
	DownloadedBytes  int64   `json:"downloadedBytes"`
	TotalBytes       int64   `json:"totalBytes"`
	ReleaseName      string  `json:"releaseName,omitempty"`
	ReleaseNotes     string  `json:"releaseNotes,omitempty"`
	ReleaseURL       string  `json:"releaseUrl,omitempty"`
	PublishedAt      string  `json:"publishedAt,omitempty"`
	AssetName        string  `json:"assetName,omitempty"`
	DownloadURL      string  `json:"downloadUrl,omitempty"`
	ChecksumURL      string  `json:"checksumUrl,omitempty"`
	ChecksumVerified bool    `json:"checksumVerified"`
	DownloadPath     string  `json:"downloadPath,omitempty"`
	CheckedAt        string  `json:"checkedAt,omitempty"`
}

type Service struct {
	mu        sync.RWMutex
	opMu      sync.Mutex
	options   Options
	client    *http.Client
	state     State
	notify    func(State)
	statePath string
}

type githubRelease struct {
	TagName     string        `json:"tag_name"`
	Name        string        `json:"name"`
	Body        string        `json:"body"`
	HTMLURL     string        `json:"html_url"`
	PublishedAt string        `json:"published_at"`
	Assets      []githubAsset `json:"assets"`
}

type githubAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Size               int64  `json:"size"`
}

type release struct {
	Version     string
	Name        string
	Notes       string
	URL         string
	PublishedAt string
	AssetName   string
	DownloadURL string
	ChecksumURL string
	AssetSize   int64
}

func New(options Options) *Service {
	options.CurrentVersion = cleanVersion(options.CurrentVersion)
	if options.CurrentVersion == "" {
		options.CurrentVersion = "0.0.0"
	}
	if strings.TrimSpace(options.Owner) == "" {
		options.Owner = defaultOwner
	}
	if strings.TrimSpace(options.Repository) == "" {
		options.Repository = defaultRepository
	}
	if strings.TrimSpace(options.CacheDir) == "" {
		cacheDir, _ := os.UserCacheDir()
		options.CacheDir = filepath.Join(cacheDir, "EchoCode", "updates")
	}
	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	s := &Service{
		options:   options,
		client:    client,
		statePath: filepath.Join(options.CacheDir, "update-state.json"),
		state: State{
			CurrentVersion: options.CurrentVersion,
			Status:         "idle",
			Message:        "尚未检查更新。",
		},
	}
	s.loadState()
	return s
}

func (s *Service) Info(configPath string) AppInfo {
	executable, _ := os.Executable()
	return AppInfo{
		Name:            "EchoCode",
		Version:         s.options.CurrentVersion,
		Commit:          strings.TrimSpace(s.options.Commit),
		BuildDate:       strings.TrimSpace(s.options.BuildDate),
		GoVersion:       runtime.Version(),
		OperatingSystem: runtime.GOOS,
		Architecture:    runtime.GOARCH,
		ExecutablePath:  executable,
		ConfigPath:      configPath,
		RepositoryURL:   s.repositoryURL(),
	}
}

func (s *Service) State() State {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.state
}

func (s *Service) SetNotify(notify func(State)) {
	s.mu.Lock()
	s.notify = notify
	s.mu.Unlock()
}

func (s *Service) Check(ctx context.Context, force bool) (State, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	if !force {
		current := s.State()
		if checkedAt, err := time.Parse(time.RFC3339, current.CheckedAt); err == nil && time.Since(checkedAt) < checkCacheTTL && current.LatestVersion != "" {
			return current, nil
		}
	}
	s.update(func(state *State) {
		state.Status = "checking"
		state.Message = "正在检查最新版本..."
		state.Progress = 0
	})

	latest, apiErr := s.checkGitHubAPI(ctx)
	if apiErr != nil {
		var fallbackErr error
		latest, fallbackErr = s.checkLatestRedirect(ctx)
		if fallbackErr != nil {
			err := errors.Join(apiErr, fallbackErr)
			state := s.fail(fmt.Errorf("检查更新失败: %w", err))
			return state, err
		}
	}

	available := compareVersions(latest.Version, s.options.CurrentVersion) > 0
	checkedAt := time.Now().UTC().Format(time.RFC3339)
	state := s.update(func(state *State) {
		state.LatestVersion = latest.Version
		state.UpdateAvailable = available
		state.ReleaseName = latest.Name
		state.ReleaseNotes = latest.Notes
		state.ReleaseURL = latest.URL
		state.PublishedAt = latest.PublishedAt
		state.AssetName = latest.AssetName
		state.DownloadURL = latest.DownloadURL
		state.ChecksumURL = latest.ChecksumURL
		state.TotalBytes = latest.AssetSize
		state.CheckedAt = checkedAt
		state.Progress = 0
		state.DownloadedBytes = 0
		state.ChecksumVerified = false
		if available {
			state.Status = "available"
			if latest.DownloadURL == "" {
				state.Message = fmt.Sprintf("发现 %s，请打开发布页下载安装。", latest.Version)
			} else {
				state.Message = fmt.Sprintf("发现新版本 %s。", latest.Version)
			}
		} else {
			state.Status = "current"
			state.Message = "当前已是最新稳定版。"
		}
	})
	s.saveState(state)
	return state, nil
}

func (s *Service) Download(ctx context.Context) (State, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	current := s.State()
	if !current.UpdateAvailable || strings.TrimSpace(current.LatestVersion) == "" {
		return current, errors.New("当前没有可下载的更新")
	}
	if strings.TrimSpace(current.DownloadURL) == "" {
		return current, errors.New("该版本没有适用于当前系统的便携版资源")
	}
	assetName := filepath.Base(strings.TrimSpace(current.AssetName))
	if assetName == "." || assetName == "" {
		assetName = fmt.Sprintf("EchoCode-%s-%s-%s-portable.zip", current.LatestVersion, runtime.GOOS, runtime.GOARCH)
	}
	destinationDir := filepath.Join(s.options.CacheDir, "downloads", safeVersionDirectory(current.LatestVersion))
	if err := os.MkdirAll(destinationDir, 0o700); err != nil {
		return s.fail(err), err
	}
	destination := filepath.Join(destinationDir, assetName)
	if info, err := os.Stat(destination); err == nil && info.Size() > 0 && (current.TotalBytes <= 0 || info.Size() == current.TotalBytes) {
		verified, verifyErr := s.verifyChecksum(ctx, destination, current.ChecksumURL)
		if verifyErr == nil {
			state := s.update(func(state *State) {
				state.Status = "downloaded"
				state.Message = "更新已下载，可以重启安装。"
				state.Progress = 1
				state.DownloadedBytes = info.Size()
				state.TotalBytes = info.Size()
				state.DownloadPath = destination
				state.ChecksumVerified = verified
			})
			s.saveState(state)
			return state, nil
		}
		_ = os.Remove(destination)
	}

	s.update(func(state *State) {
		state.Status = "downloading"
		state.Message = "正在后台下载更新..."
		state.Progress = 0
		state.DownloadedBytes = 0
		state.ChecksumVerified = false
	})
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, current.DownloadURL, nil)
	if err != nil {
		return s.fail(err), err
	}
	request.Header.Set("User-Agent", "EchoCode/"+s.options.CurrentVersion)
	response, err := s.client.Do(request)
	if err != nil {
		return s.fail(err), err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		err = fmt.Errorf("下载更新 HTTP %d: %s", response.StatusCode, readHTTPError(response.Body))
		return s.fail(err), err
	}
	if response.ContentLength > 0 {
		current.TotalBytes = response.ContentLength
	}
	partPath := destination + ".part"
	file, err := os.OpenFile(partPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return s.fail(err), err
	}
	downloaded, copyErr := s.copyWithProgress(ctx, file, response.Body, current.TotalBytes)
	closeErr := file.Close()
	if copyErr != nil {
		_ = os.Remove(partPath)
		return s.fail(copyErr), copyErr
	}
	if closeErr != nil {
		_ = os.Remove(partPath)
		return s.fail(closeErr), closeErr
	}
	if err := os.Rename(partPath, destination); err != nil {
		_ = os.Remove(partPath)
		return s.fail(err), err
	}
	verified, err := s.verifyChecksum(ctx, destination, current.ChecksumURL)
	if err != nil {
		_ = os.Remove(destination)
		return s.fail(err), err
	}
	state := s.update(func(state *State) {
		state.Status = "downloaded"
		state.Message = "更新已下载，可以重启安装。"
		state.Progress = 1
		state.DownloadedBytes = downloaded
		state.TotalBytes = downloaded
		state.DownloadPath = destination
		state.ChecksumVerified = verified
	})
	s.saveState(state)
	return state, nil
}

func (s *Service) LaunchInstaller() (State, error) {
	s.opMu.Lock()
	defer s.opMu.Unlock()

	current := s.State()
	if current.Status != "downloaded" || strings.TrimSpace(current.DownloadPath) == "" {
		return current, errors.New("请先下载更新")
	}
	staged, err := s.prepareExecutable(current.DownloadPath, current.LatestVersion)
	if err != nil {
		return s.fail(err), err
	}
	target, err := os.Executable()
	if err != nil {
		return s.fail(err), err
	}
	if err := launchReplacement(staged, target, s.options.CacheDir); err != nil {
		return s.fail(err), err
	}
	state := s.update(func(state *State) {
		state.Status = "installing"
		state.Message = "安装程序已准备，EchoCode 将退出并更新。"
	})
	s.saveState(state)
	return state, nil
}

func (s *Service) copyWithProgress(ctx context.Context, destination io.Writer, source io.Reader, total int64) (int64, error) {
	buffer := make([]byte, 128*1024)
	var downloaded int64
	lastNotify := time.Time{}
	for {
		if err := ctx.Err(); err != nil {
			return downloaded, err
		}
		count, readErr := source.Read(buffer)
		if count > 0 {
			written, writeErr := destination.Write(buffer[:count])
			downloaded += int64(written)
			if writeErr != nil {
				return downloaded, writeErr
			}
			if written != count {
				return downloaded, io.ErrShortWrite
			}
			if time.Since(lastNotify) >= 150*time.Millisecond {
				s.update(func(state *State) {
					state.DownloadedBytes = downloaded
					if total > 0 {
						state.TotalBytes = total
						state.Progress = minFloat(1, float64(downloaded)/float64(total))
					}
				})
				lastNotify = time.Now()
			}
		}
		if errors.Is(readErr, io.EOF) {
			return downloaded, nil
		}
		if readErr != nil {
			return downloaded, readErr
		}
	}
}

func (s *Service) checkGitHubAPI(ctx context.Context) (release, error) {
	endpoint := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", s.options.Owner, s.options.Repository)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return release{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("User-Agent", "EchoCode/"+s.options.CurrentVersion)
	if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return release{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("GitHub API HTTP %d: %s", response.StatusCode, readHTTPError(response.Body))
	}
	var payload githubRelease
	if err := json.NewDecoder(io.LimitReader(response.Body, 4<<20)).Decode(&payload); err != nil {
		return release{}, err
	}
	return s.releaseFromGitHub(payload)
}

func (s *Service) checkLatestRedirect(ctx context.Context) (release, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, s.repositoryURL()+"/releases/latest", nil)
	if err != nil {
		return release{}, err
	}
	request.Header.Set("User-Agent", "EchoCode/"+s.options.CurrentVersion)
	response, err := s.client.Do(request)
	if err != nil {
		return release{}, err
	}
	defer response.Body.Close()
	finalURL := response.Request.URL.String()
	marker := "/releases/tag/"
	index := strings.Index(finalURL, marker)
	if index < 0 {
		return release{}, fmt.Errorf("发布页没有返回最新版本标签: %s", finalURL)
	}
	tag := strings.Trim(strings.TrimSpace(finalURL[index+len(marker):]), "/")
	version := cleanVersion(tag)
	if version == "" {
		return release{}, errors.New("无法识别最新版本号")
	}
	assetName := fmt.Sprintf("EchoCode-%s-%s-%s-portable.zip", version, runtime.GOOS, runtime.GOARCH)
	downloadURL := fmt.Sprintf("%s/releases/download/%s/%s", s.repositoryURL(), tag, assetName)
	return release{
		Version:     version,
		Name:        "EchoCode " + version,
		URL:         finalURL,
		AssetName:   assetName,
		DownloadURL: downloadURL,
	}, nil
}

func (s *Service) releaseFromGitHub(payload githubRelease) (release, error) {
	version := cleanVersion(payload.TagName)
	if version == "" {
		return release{}, errors.New("GitHub Release 缺少可识别的版本号")
	}
	assets := append([]githubAsset(nil), payload.Assets...)
	sort.SliceStable(assets, func(i, j int) bool { return assetScore(assets[i].Name) > assetScore(assets[j].Name) })
	selected := githubAsset{}
	for _, asset := range assets {
		if assetScore(asset.Name) > 0 {
			selected = asset
			break
		}
	}
	checksumURL := ""
	if selected.Name != "" {
		for _, asset := range payload.Assets {
			lower := strings.ToLower(asset.Name)
			if strings.Contains(lower, strings.ToLower(selected.Name)) && (strings.HasSuffix(lower, ".sha256") || strings.HasSuffix(lower, ".sha256sum")) {
				checksumURL = asset.BrowserDownloadURL
				break
			}
		}
	}
	return release{
		Version:     version,
		Name:        strings.TrimSpace(payload.Name),
		Notes:       strings.TrimSpace(payload.Body),
		URL:         strings.TrimSpace(payload.HTMLURL),
		PublishedAt: strings.TrimSpace(payload.PublishedAt),
		AssetName:   selected.Name,
		DownloadURL: selected.BrowserDownloadURL,
		ChecksumURL: checksumURL,
		AssetSize:   selected.Size,
	}, nil
}

func assetScore(name string) int {
	lower := strings.ToLower(strings.TrimSpace(name))
	if lower == "" || strings.Contains(lower, "sha256") || strings.Contains(lower, "checksum") {
		return 0
	}
	if !strings.HasSuffix(lower, ".zip") && !strings.HasSuffix(lower, ".exe") {
		return 0
	}
	if strings.Contains(lower, "setup") || strings.Contains(lower, "installer") {
		return 0
	}
	score := 1
	if strings.Contains(lower, "echocode") {
		score += 20
	}
	if strings.Contains(lower, runtime.GOOS) || (runtime.GOOS == "windows" && strings.Contains(lower, "win")) {
		score += 40
	} else {
		return 0
	}
	if assetArch := architectureForAsset(lower); assetArch != "" && assetArch != runtime.GOARCH {
		return 0
	}
	archAliases := []string{runtime.GOARCH}
	if runtime.GOARCH == "amd64" {
		archAliases = append(archAliases, "x86_64", "x64", "win64")
	} else if runtime.GOARCH == "arm64" {
		archAliases = append(archAliases, "aarch64")
	} else if runtime.GOARCH == "386" {
		archAliases = append(archAliases, "i386", "win32")
	}
	for _, alias := range archAliases {
		if strings.Contains(lower, alias) {
			score += 40
			break
		}
	}
	if strings.Contains(lower, "portable") {
		score += 15
	}
	if strings.HasSuffix(lower, ".zip") {
		score += 5
	}
	return score
}

func architectureForAsset(lower string) string {
	switch {
	case strings.Contains(lower, "arm64"), strings.Contains(lower, "aarch64"):
		return "arm64"
	case strings.Contains(lower, "amd64"), strings.Contains(lower, "x86_64"), strings.Contains(lower, "x64"), strings.Contains(lower, "win64"):
		return "amd64"
	case strings.Contains(lower, "i386"), strings.Contains(lower, "win32"):
		return "386"
	default:
		return ""
	}
}

func (s *Service) verifyChecksum(ctx context.Context, filePath, checksumURL string) (bool, error) {
	if strings.TrimSpace(checksumURL) == "" {
		return false, nil
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, checksumURL, nil)
	if err != nil {
		return false, err
	}
	request.Header.Set("User-Agent", "EchoCode/"+s.options.CurrentVersion)
	response, err := s.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("下载校验文件: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Errorf("下载校验文件 HTTP %d", response.StatusCode)
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxChecksumBytes))
	if err != nil {
		return false, err
	}
	expected := checksumFromText(string(payload))
	if expected == "" {
		return false, errors.New("校验文件中没有 SHA-256")
	}
	file, err := os.Open(filePath)
	if err != nil {
		return false, err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return false, err
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actual, expected) {
		return false, fmt.Errorf("更新包 SHA-256 不匹配: got %s", actual)
	}
	return true, nil
}

func (s *Service) prepareExecutable(downloadPath, version string) (string, error) {
	extension := strings.ToLower(filepath.Ext(downloadPath))
	if extension == ".exe" {
		return downloadPath, nil
	}
	if extension != ".zip" {
		return "", fmt.Errorf("不支持的更新包格式: %s", extension)
	}
	reader, err := zip.OpenReader(downloadPath)
	if err != nil {
		return "", err
	}
	defer reader.Close()
	var executable *zip.File
	for _, file := range reader.File {
		name := strings.ToLower(filepath.Base(strings.ReplaceAll(file.Name, "\\", "/")))
		if file.FileInfo().IsDir() || !strings.HasSuffix(name, ".exe") {
			continue
		}
		if name == "echocode.exe" {
			executable = file
			break
		}
		if executable == nil && strings.Contains(name, "echocode") && !strings.Contains(name, "setup") {
			executable = file
		}
	}
	if executable == nil {
		return "", errors.New("更新包中没有找到 EchoCode.exe")
	}
	if executable.UncompressedSize64 > 1<<30 {
		return "", errors.New("更新包中的可执行文件异常过大")
	}
	stageDir := filepath.Join(s.options.CacheDir, "staged", safeVersionDirectory(version))
	if err := os.MkdirAll(stageDir, 0o700); err != nil {
		return "", err
	}
	destination := filepath.Join(stageDir, "EchoCode.exe")
	temporary := destination + ".part"
	source, err := executable.Open()
	if err != nil {
		return "", err
	}
	defer source.Close()
	target, err := os.OpenFile(temporary, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o700)
	if err != nil {
		return "", err
	}
	_, copyErr := io.Copy(target, io.LimitReader(source, 1<<30))
	closeErr := target.Close()
	if copyErr != nil {
		_ = os.Remove(temporary)
		return "", copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temporary)
		return "", closeErr
	}
	_ = os.Remove(destination)
	if err := os.Rename(temporary, destination); err != nil {
		_ = os.Remove(temporary)
		return "", err
	}
	return destination, nil
}

func (s *Service) update(apply func(*State)) State {
	s.mu.Lock()
	apply(&s.state)
	s.state.CurrentVersion = s.options.CurrentVersion
	state := s.state
	notify := s.notify
	s.mu.Unlock()
	if notify != nil {
		notify(state)
	}
	return state
}

func (s *Service) fail(err error) State {
	return s.update(func(state *State) {
		state.Status = "error"
		state.Message = err.Error()
	})
}

func (s *Service) repositoryURL() string {
	return fmt.Sprintf("https://github.com/%s/%s", s.options.Owner, s.options.Repository)
}

func (s *Service) loadState() {
	payload, err := os.ReadFile(s.statePath)
	if err != nil {
		return
	}
	var cached State
	if json.Unmarshal(payload, &cached) != nil || cleanVersion(cached.CurrentVersion) != s.options.CurrentVersion {
		return
	}
	if cached.Status == "checking" || cached.Status == "downloading" || cached.Status == "installing" {
		cached.Status = "idle"
		cached.Message = "上次更新操作已中断，可以重新检查。"
	}
	if cached.DownloadPath != "" {
		if _, err := os.Stat(cached.DownloadPath); err != nil {
			cached.DownloadPath = ""
			cached.ChecksumVerified = false
			if cached.Status == "downloaded" {
				cached.Status = "available"
			}
		}
	}
	s.state = cached
}

func (s *Service) saveState(state State) {
	if err := os.MkdirAll(filepath.Dir(s.statePath), 0o700); err != nil {
		return
	}
	payload, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return
	}
	temporary := s.statePath + ".tmp"
	if os.WriteFile(temporary, payload, 0o600) != nil {
		return
	}
	_ = os.Remove(s.statePath)
	_ = os.Rename(temporary, s.statePath)
}

func cleanVersion(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "v"))
	if value == "" {
		return ""
	}
	return value
}

func compareVersions(left, right string) int {
	leftParts, leftOK := numericVersion(left)
	rightParts, rightOK := numericVersion(right)
	if !leftOK || !rightOK {
		return 0
	}
	length := maxInt(len(leftParts), len(rightParts))
	for index := 0; index < length; index++ {
		leftValue, rightValue := 0, 0
		if index < len(leftParts) {
			leftValue = leftParts[index]
		}
		if index < len(rightParts) {
			rightValue = rightParts[index]
		}
		if leftValue > rightValue {
			return 1
		}
		if leftValue < rightValue {
			return -1
		}
	}
	return 0
}

func numericVersion(value string) ([]int, bool) {
	value = cleanVersion(value)
	if index := strings.IndexAny(value, "-+"); index >= 0 {
		value = value[:index]
	}
	segments := strings.Split(value, ".")
	if len(segments) == 0 {
		return nil, false
	}
	parts := make([]int, 0, len(segments))
	for _, segment := range segments {
		parsed, err := strconv.Atoi(segment)
		if err != nil || parsed < 0 {
			return nil, false
		}
		parts = append(parts, parsed)
	}
	return parts, true
}

func checksumFromText(value string) string {
	for _, field := range strings.Fields(value) {
		candidate := strings.TrimSpace(field)
		if len(candidate) != 64 {
			continue
		}
		if _, err := hex.DecodeString(candidate); err == nil {
			return strings.ToLower(candidate)
		}
	}
	return ""
}

func safeVersionDirectory(version string) string {
	var builder strings.Builder
	for _, char := range cleanVersion(version) {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '.' || char == '-' || char == '_' {
			builder.WriteRune(char)
		}
	}
	if builder.Len() == 0 {
		return "unknown"
	}
	return builder.String()
}

func readHTTPError(reader io.Reader) string {
	payload, _ := io.ReadAll(io.LimitReader(reader, 64<<10))
	message := strings.TrimSpace(string(payload))
	if len(message) > 500 {
		message = message[:500]
	}
	return message
}

func minFloat(left, right float64) float64 {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
