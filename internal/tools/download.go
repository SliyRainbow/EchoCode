package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	defaultDownloadLimit = int64(512 * 1024 * 1024)
	maximumDownloadLimit = int64(2 * 1024 * 1024 * 1024)
)

type DownloadFileTool struct {
	Policy     SandboxPolicy
	Client     *http.Client
	RetryDelay time.Duration
}

func (t DownloadFileTool) Name() string { return "download_file" }

func (t DownloadFileTool) Description() string {
	return "Download an HTTP(S) resource directly to an authorized local path. Use this instead of curl, wget, PowerShell, or shell redirection. It streams progress, enforces a size limit, supports optional SHA-256 verification, and writes atomically."
}

func (t DownloadFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "Direct HTTP or HTTPS download URL",
			},
			"destination": map[string]any{
				"type":        "string",
				"description": "Exact authorized destination file path; use destination_directory instead when the filename should come from Content-Disposition or the URL",
			},
			"destination_directory": map[string]any{
				"type":        "string",
				"description": "Authorized destination directory. The server filename is used unless filename is supplied",
			},
			"filename": map[string]any{
				"type":        "string",
				"description": "Optional safe filename used with destination_directory",
			},
			"expected_sha256": map[string]any{
				"type":        "string",
				"description": "Optional expected SHA-256 hex digest",
			},
			"max_bytes": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"maximum":     maximumDownloadLimit,
				"description": "Optional size limit; defaults to 512 MiB and cannot exceed 2 GiB",
			},
			"overwrite": map[string]any{
				"type":        "boolean",
				"description": "Legacy alias for conflict_policy=overwrite",
			},
			"conflict_policy": map[string]any{
				"type":        "string",
				"enum":        []string{"error", "overwrite", "rename"},
				"description": "How to handle an existing file; defaults to error",
			},
			"max_retries": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"maximum":     3,
				"description": "Retries for transient connection, HTTP, or stream failures; defaults to 2",
			},
		},
		"required": []string{"url"},
	}
}

type downloadFileArguments struct {
	URL                  string `json:"url"`
	Destination          string `json:"destination"`
	DestinationDirectory string `json:"destination_directory"`
	Filename             string `json:"filename"`
	ExpectedSHA256       string `json:"expected_sha256"`
	MaxBytes             int64  `json:"max_bytes"`
	Overwrite            bool   `json:"overwrite"`
	ConflictPolicy       string `json:"conflict_policy"`
	MaxRetries           *int   `json:"max_retries"`
}

func (t DownloadFileTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args downloadFileArguments
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("invalid download arguments: " + err.Error()), nil
	}
	args.URL = strings.TrimSpace(args.URL)
	args.Destination = strings.TrimSpace(args.Destination)
	args.DestinationDirectory = strings.TrimSpace(args.DestinationDirectory)
	args.Filename = strings.TrimSpace(args.Filename)
	args.ExpectedSHA256 = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(args.ExpectedSHA256), "sha256:"))
	args.ConflictPolicy = strings.ToLower(strings.TrimSpace(args.ConflictPolicy))
	if !t.Policy.NetworkAccess {
		return errorResult(ErrNetworkDisabled.Error()), nil
	}
	parsedURL, err := url.Parse(args.URL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return errorResult("download URL must use HTTP or HTTPS"), nil
	}
	if args.ExpectedSHA256 != "" {
		if decoded, err := hex.DecodeString(args.ExpectedSHA256); err != nil || len(decoded) != sha256.Size {
			return errorResult("expected_sha256 must be a 64-character hexadecimal digest"), nil
		}
	}
	maxBytes := args.MaxBytes
	if maxBytes <= 0 {
		maxBytes = defaultDownloadLimit
	}
	if maxBytes > maximumDownloadLimit {
		return errorResult(fmt.Sprintf("max_bytes exceeds the %d-byte safety limit", maximumDownloadLimit)), nil
	}
	target, err := t.prepareDownloadTarget(args)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	maxRetries := 2
	if args.MaxRetries != nil {
		maxRetries = *args.MaxRetries
	}
	if maxRetries < 0 || maxRetries > 3 {
		return errorResult("max_retries must be between 0 and 3"), nil
	}
	displayURL := SafeDownloadURLForDisplay(args.URL)
	startedAt := time.Now()
	destination := target.exactPath
	destinationExisted := false
	var lastError string

	for attempt := 0; attempt <= maxRetries; attempt++ {
		request, requestErr := http.NewRequestWithContext(ctx, http.MethodGet, parsedURL.String(), nil)
		if requestErr != nil {
			return errorResult("create download request: " + requestErr.Error()), nil
		}
		request.Header.Set("User-Agent", "EchoCode/1 download_file")
		response, requestErr := t.httpClient().Do(request)
		if requestErr != nil {
			lastError = "request failed: " + sanitizeDownloadError(requestErr.Error(), args.URL)
			if attempt < maxRetries && ctx.Err() == nil {
				emitDownloadRetry(ctx, displayURL, target.displayPath(), startedAt, attempt+1, maxRetries, lastError)
				if waitErr := waitForDownloadRetry(ctx, t.retryDelay(attempt+1)); waitErr == nil {
					continue
				}
			}
			return downloadErrorResult(args, target.displayPath(), startedAt, lastError), nil
		}

		if response.StatusCode < 200 || response.StatusCode >= 300 {
			lastError = fmt.Sprintf("HTTP %d %s", response.StatusCode, response.Status)
			retryable := transientDownloadStatus(response.StatusCode)
			retryDelay := downloadRetryAfter(response, t.retryDelay(attempt+1))
			_ = response.Body.Close()
			if retryable && attempt < maxRetries && ctx.Err() == nil {
				emitDownloadRetry(ctx, displayURL, target.displayPath(), startedAt, attempt+1, maxRetries, lastError)
				if waitErr := waitForDownloadRetry(ctx, retryDelay); waitErr == nil {
					continue
				}
			}
			return downloadErrorResult(args, target.displayPath(), startedAt, lastError), nil
		}

		if destination == "" {
			destination, err = target.resolve(response)
			if err != nil {
				_ = response.Body.Close()
				return downloadErrorResult(args, target.displayPath(), startedAt, err.Error()), nil
			}
		}
		destination, destinationExisted, err = chooseDownloadDestination(destination, target.conflictPolicy)
		if err != nil {
			_ = response.Body.Close()
			return downloadErrorResult(args, destination, startedAt, err.Error()), nil
		}
		if response.ContentLength > maxBytes {
			_ = response.Body.Close()
			return downloadErrorResult(args, destination, startedAt, fmt.Sprintf("response is %d bytes, above the %d-byte limit", response.ContentLength, maxBytes)), nil
		}
		if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
			_ = response.Body.Close()
			return downloadErrorResult(args, destination, startedAt, "create destination directory: "+err.Error()), nil
		}
		temporary, createErr := os.CreateTemp(filepath.Dir(destination), ".echocode-download-*.tmp")
		if createErr != nil {
			_ = response.Body.Close()
			return downloadErrorResult(args, destination, startedAt, "create temporary file: "+createErr.Error()), nil
		}
		temporaryPath := temporary.Name()
		cleanupTemporary := func() {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}

		hasher := sha256.New()
		contentHeader := &downloadPrefixWriter{limit: 512}
		reader := &downloadProgressReader{
			ctx: ctx, reader: io.LimitReader(response.Body, maxBytes+1), total: response.ContentLength,
			url: displayURL, destination: destination, startedAt: startedAt, minInterval: 200 * time.Millisecond,
		}
		reader.emit(true)
		written, copyErr := io.Copy(io.MultiWriter(temporary, hasher, contentHeader), reader)
		bodyCloseErr := response.Body.Close()
		if copyErr == nil {
			copyErr = bodyCloseErr
		}
		if copyErr != nil {
			cleanupTemporary()
			lastError = "download stream failed: " + copyErr.Error()
			if attempt < maxRetries && ctx.Err() == nil {
				emitDownloadRetry(ctx, displayURL, destination, startedAt, attempt+1, maxRetries, lastError)
				if waitErr := waitForDownloadRetry(ctx, t.retryDelay(attempt+1)); waitErr == nil {
					continue
				}
			}
			return downloadErrorResult(args, destination, startedAt, lastError), nil
		}
		if written > maxBytes {
			cleanupTemporary()
			return downloadErrorResult(args, destination, startedAt, fmt.Sprintf("download exceeded the %d-byte limit", maxBytes)), nil
		}
		if downloadResponseIsUnexpectedHTML(response, destination, contentHeader.Bytes()) {
			cleanupTemporary()
			return downloadErrorResult(args, destination, startedAt, "the download URL returned an HTML page instead of the requested file; inspect the official download page or browser request and use the final asset URL"), nil
		}
		digest := hex.EncodeToString(hasher.Sum(nil))
		if args.ExpectedSHA256 != "" && digest != args.ExpectedSHA256 {
			cleanupTemporary()
			return downloadErrorResult(args, destination, startedAt, fmt.Sprintf("SHA-256 mismatch: got %s", digest)), nil
		}
		if err := temporary.Sync(); err != nil {
			cleanupTemporary()
			return downloadErrorResult(args, destination, startedAt, "flush temporary file: "+err.Error()), nil
		}
		if err := temporary.Close(); err != nil {
			cleanupTemporary()
			return downloadErrorResult(args, destination, startedAt, "close temporary file: "+err.Error()), nil
		}
		if target.conflictPolicy != "overwrite" {
			if _, statErr := os.Stat(destination); statErr == nil {
				_ = os.Remove(temporaryPath)
				return downloadErrorResult(args, destination, startedAt, "download destination was created while the transfer was running"), nil
			} else if !os.IsNotExist(statErr) {
				_ = os.Remove(temporaryPath)
				return downloadErrorResult(args, destination, startedAt, "recheck download destination: "+statErr.Error()), nil
			}
		}
		if err := installDownloadedFile(temporaryPath, destination, target.conflictPolicy == "overwrite"); err != nil {
			_ = os.Remove(temporaryPath)
			return downloadErrorResult(args, destination, startedAt, "install downloaded file: "+err.Error()), nil
		}
		completedAt := time.Now()
		output := fmt.Sprintf("Downloaded %d bytes\nSHA-256 %s\nSaved to %s", written, digest, destination)
		action := "created"
		if destinationExisted {
			action = "modified"
		}
		return Result{
			Summary: fmt.Sprintf("Downloaded %s to %s (%d bytes, sha256:%s)", displayURL, destination, written, digest),
			Parts: []ResultPart{
				{
					Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: displayURL + " -> " + destination,
					Output: output, WorkingDirectory: filepath.Dir(destination),
					StartedAt: startedAt.Format(time.RFC3339Nano), CompletedAt: completedAt.Format(time.RFC3339Nano),
					DurationMs: elapsedMilliseconds(startedAt, completedAt),
				},
				{Kind: PartFile, Path: destination, Created: !destinationExisted, FileAction: action},
			},
		}, nil
	}

	return downloadErrorResult(args, target.displayPath(), startedAt, lastError), nil
}

type preparedDownloadTarget struct {
	policy         SandboxPolicy
	exactPath      string
	directory      string
	filename       string
	conflictPolicy string
}

func (t DownloadFileTool) prepareDownloadTarget(args downloadFileArguments) (preparedDownloadTarget, error) {
	if (args.Destination == "") == (args.DestinationDirectory == "") {
		return preparedDownloadTarget{}, errors.New("provide exactly one of destination or destination_directory")
	}
	conflictPolicy := args.ConflictPolicy
	if conflictPolicy == "" {
		if args.Overwrite {
			conflictPolicy = "overwrite"
		} else {
			conflictPolicy = "error"
		}
	}
	if conflictPolicy != "error" && conflictPolicy != "overwrite" && conflictPolicy != "rename" {
		return preparedDownloadTarget{}, errors.New("conflict_policy must be error, overwrite, or rename")
	}
	if args.Overwrite && conflictPolicy != "overwrite" {
		return preparedDownloadTarget{}, errors.New("overwrite=true conflicts with the selected conflict_policy")
	}
	target := preparedDownloadTarget{policy: t.Policy, filename: args.Filename, conflictPolicy: conflictPolicy}
	var err error
	if args.Destination != "" {
		target.exactPath, err = t.Policy.ResolveWritePath(args.Destination)
		if err != nil {
			return preparedDownloadTarget{}, err
		}
		if args.Filename != "" {
			return preparedDownloadTarget{}, errors.New("filename can only be used with destination_directory")
		}
		return target, nil
	}
	target.directory, err = t.Policy.ResolveWritePath(args.DestinationDirectory)
	if err != nil {
		return preparedDownloadTarget{}, err
	}
	if info, statErr := os.Stat(target.directory); statErr == nil && !info.IsDir() {
		return preparedDownloadTarget{}, errors.New("destination_directory points to a file")
	} else if statErr != nil && !os.IsNotExist(statErr) {
		return preparedDownloadTarget{}, statErr
	}
	if args.Filename != "" {
		safe := sanitizeDownloadFilename(args.Filename)
		if safe == "" || safe != args.Filename {
			return preparedDownloadTarget{}, errors.New("filename must be a plain filename without path separators or reserved characters")
		}
		target.filename = safe
	}
	return target, nil
}

func (t preparedDownloadTarget) resolve(response *http.Response) (string, error) {
	if t.exactPath != "" {
		return t.exactPath, nil
	}
	filename := t.filename
	if filename == "" {
		filename = downloadResponseFilename(response)
	}
	if filename == "" {
		filename = "download.bin"
	}
	destination, err := t.policy.ResolveWritePath(filepath.Join(t.directory, filename))
	if err != nil {
		return "", err
	}
	return destination, nil
}

func (t preparedDownloadTarget) displayPath() string {
	return firstNonEmptyString(t.exactPath, t.directory)
}

func chooseDownloadDestination(destination, policy string) (string, bool, error) {
	info, err := os.Stat(destination)
	if os.IsNotExist(err) {
		return destination, false, nil
	}
	if err != nil {
		return destination, false, fmt.Errorf("cannot inspect download destination: %w", err)
	}
	if info.IsDir() {
		return destination, false, errors.New("download destination is a directory")
	}
	switch policy {
	case "overwrite":
		return destination, true, nil
	case "rename":
		extension := filepath.Ext(destination)
		base := strings.TrimSuffix(destination, extension)
		for index := 1; index <= 9999; index++ {
			candidate := fmt.Sprintf("%s (%d)%s", base, index, extension)
			if _, statErr := os.Stat(candidate); os.IsNotExist(statErr) {
				return candidate, false, nil
			} else if statErr != nil {
				return candidate, false, statErr
			}
		}
		return destination, false, errors.New("cannot find an available renamed destination")
	default:
		return destination, true, errors.New("download destination already exists; choose overwrite or rename only when intended")
	}
}

func downloadResponseFilename(response *http.Response) string {
	if response == nil {
		return ""
	}
	if disposition := response.Header.Get("Content-Disposition"); disposition != "" {
		if _, parameters, err := mime.ParseMediaType(disposition); err == nil {
			if filename := sanitizeDownloadFilename(parameters["filename"]); filename != "" {
				return filename
			}
		}
	}
	if response.Request != nil && response.Request.URL != nil {
		if filename := sanitizeDownloadFilename(path.Base(response.Request.URL.Path)); filename != "" {
			return filename
		}
	}
	return ""
}

func sanitizeDownloadFilename(value string) string {
	value = strings.TrimSpace(value)
	value = path.Base(strings.ReplaceAll(value, `\`, "/"))
	if value == "" || value == "." || value == ".." || strings.ContainsAny(value, "\x00\r\n") {
		return ""
	}
	if runtime.GOOS == "windows" && strings.ContainsAny(value, `<>:"/\|?*`) {
		return ""
	}
	if len([]rune(value)) > 240 {
		return ""
	}
	return value
}

func downloadResponseIsUnexpectedHTML(response *http.Response, destination string, contentHeader []byte) bool {
	mediaType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
	headerSaysHTML := strings.EqualFold(mediaType, "text/html") || strings.EqualFold(mediaType, "application/xhtml+xml")
	sniffedType, _, _ := mime.ParseMediaType(http.DetectContentType(contentHeader))
	contentSaysHTML := strings.EqualFold(sniffedType, "text/html") || strings.EqualFold(sniffedType, "application/xhtml+xml")
	if !headerSaysHTML && !contentSaysHTML {
		return false
	}
	extension := strings.ToLower(filepath.Ext(destination))
	return extension != ".html" && extension != ".htm" && extension != ".xhtml"
}

func transientDownloadStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout, http.StatusTooEarly, http.StatusTooManyRequests,
		http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func (t DownloadFileTool) retryDelay(attempt int) time.Duration {
	if t.RetryDelay > 0 {
		return t.RetryDelay
	}
	return time.Duration(attempt) * 500 * time.Millisecond
}

func downloadRetryAfter(response *http.Response, fallback time.Duration) time.Duration {
	seconds, err := strconv.Atoi(strings.TrimSpace(response.Header.Get("Retry-After")))
	if err != nil || seconds < 0 {
		return fallback
	}
	delay := time.Duration(seconds) * time.Second
	if delay > 5*time.Second {
		return 5 * time.Second
	}
	return delay
}

func waitForDownloadRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func emitDownloadRetry(ctx context.Context, source, destination string, startedAt time.Time, attempt, maxRetries int, message string) {
	EmitProgress(ctx, ResultPart{
		Kind: PartToolCall, Name: "download_file", Status: "retrying",
		Input:            source + " -> " + destination,
		Output:           fmt.Sprintf("%s; retry %d of %d", message, attempt, maxRetries),
		WorkingDirectory: filepath.Dir(destination), StartedAt: startedAt.Format(time.RFC3339Nano),
	})
}

func SafeDownloadURLForDisplay(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "[invalid URL]"
	}
	if parsed.Scheme == "" {
		return strings.TrimSpace(raw)
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String()
}

func DownloadFileInputForDisplay(rawArgs json.RawMessage) string {
	var args downloadFileArguments
	if json.Unmarshal(rawArgs, &args) != nil {
		return ""
	}
	destination := firstNonEmptyString(args.Destination, args.DestinationDirectory)
	return strings.TrimSpace(SafeDownloadURLForDisplay(args.URL) + " -> " + destination)
}

func sanitizeDownloadError(message, rawURL string) string {
	return strings.TrimSpace(redactKnownTransferURL(message, rawURL, SafeDownloadURLForDisplay(rawURL)))
}

func (t DownloadFileTool) httpClient() *http.Client {
	if t.Client != nil {
		return t.Client
	}
	transport, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return &http.Client{Timeout: 10 * time.Minute}
	}
	clone := transport.Clone()
	clone.ResponseHeaderTimeout = 30 * time.Second
	clone.TLSHandshakeTimeout = 15 * time.Second
	return &http.Client{
		Transport: clone,
		CheckRedirect: func(request *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			if request.URL.Scheme != "http" && request.URL.Scheme != "https" {
				return fmt.Errorf("redirected to unsupported scheme %s", request.URL.Scheme)
			}
			return nil
		},
	}
}

func installDownloadedFile(source, destination string, overwrite bool) error {
	if overwrite {
		return replaceFileAtomic(source, destination)
	}
	if err := os.Link(source, destination); err != nil {
		return err
	}
	return os.Remove(source)
}

type downloadProgressReader struct {
	ctx         context.Context
	reader      io.Reader
	total       int64
	read        int64
	url         string
	destination string
	startedAt   time.Time
	lastEmit    time.Time
	minInterval time.Duration
}

type downloadPrefixWriter struct {
	buffer bytes.Buffer
	limit  int
}

func (w *downloadPrefixWriter) Write(data []byte) (int, error) {
	written := len(data)
	remaining := w.limit - w.buffer.Len()
	if remaining > 0 {
		if len(data) > remaining {
			data = data[:remaining]
		}
		_, _ = w.buffer.Write(data)
	}
	return written, nil
}

func (w *downloadPrefixWriter) Bytes() []byte {
	return w.buffer.Bytes()
}

func (r *downloadProgressReader) Read(buffer []byte) (int, error) {
	count, err := r.reader.Read(buffer)
	r.read += int64(count)
	r.emit(err == io.EOF)
	return count, err
}

func (r *downloadProgressReader) emit(force bool) {
	now := time.Now()
	if !force && !r.lastEmit.IsZero() && now.Sub(r.lastEmit) < r.minInterval {
		return
	}
	r.lastEmit = now
	output := fmt.Sprintf("Downloaded %d bytes", r.read)
	if r.total >= 0 {
		output = fmt.Sprintf("Downloaded %d of %d bytes", r.read, r.total)
	}
	if elapsed := now.Sub(r.startedAt).Seconds(); elapsed > 0 && r.read > 0 {
		output += fmt.Sprintf(" · %.1f MiB/s", float64(r.read)/(1024*1024)/elapsed)
	}
	EmitProgress(r.ctx, ResultPart{
		Kind:             PartToolCall,
		Name:             "download_file",
		Status:           "running",
		Input:            r.url + " -> " + r.destination,
		Output:           output,
		WorkingDirectory: filepath.Dir(r.destination),
		StartedAt:        r.startedAt.Format(time.RFC3339Nano),
	})
}

func downloadErrorResult(args downloadFileArguments, destination string, startedAt time.Time, message string) Result {
	completedAt := time.Now()
	return Result{
		Summary: "Download failed: " + message,
		IsError: true,
		Parts: []ResultPart{{
			Kind:             PartToolCall,
			Name:             "download_file",
			Status:           "error",
			Input:            SafeDownloadURLForDisplay(args.URL) + " -> " + destination,
			Output:           message,
			WorkingDirectory: filepath.Dir(destination),
			StartedAt:        startedAt.Format(time.RFC3339Nano),
			CompletedAt:      completedAt.Format(time.RFC3339Nano),
			DurationMs:       elapsedMilliseconds(startedAt, completedAt),
		}},
	}
}
