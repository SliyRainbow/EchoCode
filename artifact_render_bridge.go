package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const visualRenderRetention = 30 * 24 * time.Hour

// artifactRenderBridge converts user artifacts and live application surfaces
// into PNG files stored in EchoCode's private cache. It never opens or replaces a
// user-visible browser tab.
type artifactRenderBridge struct {
	app     *App
	cacheMu sync.Mutex
}

func (b *artifactRenderBridge) RenderArtifact(ctx context.Context, request tools.ArtifactRenderRequest) (tools.ArtifactRenderResult, error) {
	if b == nil || b.app == nil {
		return tools.ArtifactRenderResult{}, fmt.Errorf("artifact renderer is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return tools.ArtifactRenderResult{}, err
	}
	outputPath, err := b.newRenderPath(request.Source)
	if err != nil {
		return tools.ArtifactRenderResult{}, err
	}

	result := tools.ArtifactRenderResult{
		Source:    request.Source,
		Path:      request.Path,
		Reference: outputPath,
		MIMEType:  "image/png",
	}
	switch request.Source {
	case tools.VisualSourceFile:
		result.Renderer, err = b.renderFile(ctx, request, outputPath)
	case tools.VisualSourceBrowser:
		result.Renderer, err = b.renderBrowser(ctx, request, outputPath)
	case tools.VisualSourceWindow:
		result.Renderer, err = b.renderWindow(ctx, request, outputPath)
	case tools.VisualSourceEchoCode:
		result.Renderer, err = b.renderEchoCode(ctx, request, outputPath)
	default:
		err = fmt.Errorf("unsupported visual source: %s", request.Source)
	}
	if err != nil {
		_ = os.Remove(outputPath)
		return tools.ArtifactRenderResult{}, err
	}
	result.Width, result.Height, err = renderedImageDimensions(outputPath)
	if err != nil {
		_ = os.Remove(outputPath)
		return tools.ArtifactRenderResult{}, err
	}
	return result, nil
}

func (b *artifactRenderBridge) renderFile(ctx context.Context, request tools.ArtifactRenderRequest, outputPath string) (string, error) {
	path := filepath.Clean(strings.TrimSpace(request.Path))
	if path == "" {
		return "", fmt.Errorf("file render requires a path")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", fmt.Errorf("visual artifact is not a regular file")
	}

	if _, _, supported := artifacts.Detect(path); supported {
		preview, previewErr := artifacts.PreviewFile(path, artifacts.DefaultPreviewOptions())
		if previewErr != nil {
			return "", previewErr
		}
		content := artifacts.RenderPreviewHTML(preview, artifacts.HTMLRenderSelection{
			Sheet: request.Sheet,
			Page:  request.Page,
			Slide: request.Slide,
		})
		err = b.renderHTMLSurface(ctx, content, outputPath, request.Width, request.Height)
		return "echocode-office-structural-preview", err
	}

	extension := strings.ToLower(filepath.Ext(path))
	switch extension {
	case ".png", ".jpg", ".jpeg", ".gif":
		if err := renderRasterImage(path, outputPath, request.Width, request.Height); err == nil {
			return "raster-image", nil
		}
	case ".html", ".htm":
		err = b.renderHTMLFile(ctx, path, outputPath, request.Width, request.Height)
		return "chromium-html", err
	case ".pdf":
		err = b.renderLocalAsset(ctx, path, outputPath, request.Width, request.Height, request.Page, false)
		return "chromium-pdf", err
	}

	if strings.HasPrefix(artifactContentType(path), "image/") {
		err = b.renderLocalAsset(ctx, path, outputPath, request.Width, request.Height, 0, true)
		return "chromium-image", err
	}
	return "", fmt.Errorf("file type %s does not have a visual renderer", extension)
}

func (b *artifactRenderBridge) renderBrowser(ctx context.Context, request tools.ArtifactRenderRequest, outputPath string) (string, error) {
	if b.app.browser == nil {
		return "", fmt.Errorf("managed browser is unavailable")
	}
	state := b.app.browser.State()
	if state.ActiveTabID == "" {
		return "", fmt.Errorf("managed browser has no active tab")
	}
	if _, err := b.app.browser.SaveScreenshot(ctx, state.ActiveTabID, outputPath); err != nil {
		return "", err
	}
	if err := renderRasterImage(outputPath, outputPath, request.Width, request.Height); err != nil {
		return "", err
	}
	return "managed-browser-current-tab", nil
}

func (b *artifactRenderBridge) renderWindow(ctx context.Context, request tools.ArtifactRenderRequest, outputPath string) (string, error) {
	bridge := &computerToolBridge{app: b.app}
	if _, err := bridge.ScreenshotWindow(ctx, strings.TrimSpace(request.WindowID), outputPath); err != nil {
		return "", err
	}
	if err := renderRasterImage(outputPath, outputPath, request.Width, request.Height); err != nil {
		return "", err
	}
	return "authorized-desktop-window", nil
}

func (b *artifactRenderBridge) renderEchoCode(ctx context.Context, request tools.ArtifactRenderRequest, outputPath string) (string, error) {
	if b.app.computer == nil {
		return "", fmt.Errorf("desktop capture is unavailable")
	}
	windows, err := b.app.computer.ListWindows(ctx)
	if err != nil {
		return "", err
	}
	var selectedID string
	for _, window := range windows {
		if window.PID != uint32(os.Getpid()) {
			continue
		}
		selectedID = window.ID
		if window.Foreground {
			break
		}
	}
	if selectedID == "" {
		return "", fmt.Errorf("EchoCode application window was not found")
	}
	if _, err := b.app.computer.ScreenshotWindow(ctx, selectedID, outputPath); err != nil {
		return "", err
	}
	if err := renderRasterImage(outputPath, outputPath, request.Width, request.Height); err != nil {
		return "", err
	}
	return "echocode-application-window", nil
}

func (b *artifactRenderBridge) renderHTMLSurface(ctx context.Context, content, outputPath string, width, height int) error {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/surface" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, content)
	})
	return b.renderLoopbackURL(ctx, handler, "/surface", outputPath, width, height)
}

func (b *artifactRenderBridge) renderHTMLFile(ctx context.Context, path, outputPath string, width, height int) error {
	root := filepath.Dir(path)
	if workspaceRoot := strings.TrimSpace(b.app.runtimeSettingsSnapshot().WorkspaceRoot); pathWithinRoot(path, workspaceRoot) {
		root = workspaceRoot
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return fmt.Errorf("cannot resolve HTML preview path")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/files/") {
			http.NotFound(w, r)
			return
		}
		serveVisualRootFile(w, r, root, strings.TrimPrefix(r.URL.Path, "/files/"))
	})
	return b.renderLoopbackURL(ctx, handler, "/files/"+escapeURLPath(relative), outputPath, width, height)
}

func (b *artifactRenderBridge) renderLocalAsset(ctx context.Context, path, outputPath string, width, height, page int, imageSurface bool) error {
	handler := http.NewServeMux()
	handler.HandleFunc("/asset", func(w http.ResponseWriter, r *http.Request) {
		serveVisualFile(w, r, path)
	})
	route := "/asset"
	if imageSurface {
		handler.HandleFunc("/surface", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = io.WriteString(w, `<!doctype html><html><head><meta charset="utf-8"><style>`+
				`*{box-sizing:border-box}html,body{width:100%;height:100%;margin:0;background:#eef0f2}`+
				`body{display:grid;place-items:center;padding:24px}img{display:block;max-width:100%;max-height:100%;object-fit:contain}`+
				`</style></head><body><img src="./asset" alt="Rendered artifact"></body></html>`)
		})
		route = "/surface"
	} else if page > 0 {
		route += "#page=" + fmt.Sprint(page)
	}
	return b.renderLoopbackURL(ctx, handler, route, outputPath, width, height)
}

func (b *artifactRenderBridge) renderLoopbackURL(ctx context.Context, handler http.Handler, route, outputPath string, width, height int) error {
	if b.app.browser == nil {
		return fmt.Errorf("managed browser renderer is unavailable")
	}
	surface, err := startVisualSurface(handler)
	if err != nil {
		return err
	}
	defer surface.close()
	_, err = b.app.browser.RenderURLScreenshot(ctx, surface.baseURL+route, outputPath, width, height)
	return err
}

func (b *artifactRenderBridge) newRenderPath(source string) (string, error) {
	directory := visualRenderCacheDir()
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	var randomID [12]byte
	if _, err := rand.Read(randomID[:]); err != nil {
		return "", err
	}
	b.cacheMu.Lock()
	pruneVisualRenderCache(directory, time.Now())
	b.cacheMu.Unlock()
	name := strings.ToLower(strings.TrimSpace(source))
	if name == "" {
		name = "artifact"
	}
	return filepath.Join(directory, fmt.Sprintf("%s-%d-%s.png", name, time.Now().UnixMilli(), hex.EncodeToString(randomID[:]))), nil
}

type visualSurface struct {
	server  *http.Server
	baseURL string
}

func startVisualSurface(handler http.Handler) (*visualSurface, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	var tokenBytes [16]byte
	if _, err := rand.Read(tokenBytes[:]); err != nil {
		_ = listener.Close()
		return nil, err
	}
	token := hex.EncodeToString(tokenBytes[:])
	prefix := "/" + token
	wrapped := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, prefix+"/") && r.URL.Path != prefix {
			http.NotFound(w, r)
			return
		}
		clone := r.Clone(r.Context())
		clonedURL := *r.URL
		clonedURL.Path = strings.TrimPrefix(r.URL.Path, prefix)
		if clonedURL.Path == "" {
			clonedURL.Path = "/"
		}
		clone.URL = &clonedURL
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		handler.ServeHTTP(w, clone)
	})
	server := &http.Server{Handler: wrapped, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 15 * time.Second}
	surface := &visualSurface{server: server, baseURL: "http://" + listener.Addr().String() + prefix}
	go func() { _ = server.Serve(listener) }()
	return surface, nil
}

func (s *visualSurface) close() {
	if s == nil || s.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = s.server.Shutdown(ctx)
}

func serveVisualFile(w http.ResponseWriter, r *http.Request, path string) {
	file, err := os.Open(path)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", artifactContentType(path))
	http.ServeContent(w, r, filepath.Base(path), info.ModTime(), file)
}

func serveVisualRootFile(w http.ResponseWriter, r *http.Request, root, relativeURLPath string) {
	if relativeURLPath == "" || strings.Contains(relativeURLPath, "\\") || strings.ContainsRune(relativeURLPath, '\x00') {
		http.NotFound(w, r)
		return
	}
	cleanURLPath := strings.TrimPrefix(path.Clean("/"+relativeURLPath), "/")
	if cleanURLPath == "" || cleanURLPath == "." {
		http.NotFound(w, r)
		return
	}
	resolved, info, err := resolveServedPreviewPath(root, filepath.Join(root, filepath.FromSlash(cleanURLPath)))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if info.IsDir() {
		resolved, info, err = resolveServedPreviewPath(root, filepath.Join(resolved, "index.html"))
		if err != nil {
			http.NotFound(w, r)
			return
		}
	}
	if !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	serveVisualFile(w, r, resolved)
}

func artifactContentType(path string) string {
	extension := strings.ToLower(filepath.Ext(path))
	if extension == ".svg" || extension == ".svgz" {
		return "image/svg+xml"
	}
	if extension == ".pdf" {
		return "application/pdf"
	}
	if value := http.DetectContentType(readFileHeader(path)); value != "application/octet-stream" {
		return value
	}
	return "application/octet-stream"
}

func readFileHeader(path string) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	buffer := make([]byte, 512)
	read, _ := file.Read(buffer)
	return buffer[:read]
}

func renderRasterImage(sourcePath, outputPath string, maxWidth, maxHeight int) error {
	file, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	source, _, err := image.Decode(file)
	_ = file.Close()
	if err != nil {
		return err
	}
	width, height := boundedImageSize(source.Bounds().Dx(), source.Bounds().Dy(), maxWidth, maxHeight)
	if width < 1 || height < 1 {
		return fmt.Errorf("image has invalid dimensions")
	}
	target := image.NewNRGBA(image.Rect(0, 0, width, height))
	if width == source.Bounds().Dx() && height == source.Bounds().Dy() {
		draw.Draw(target, target.Bounds(), source, source.Bounds().Min, draw.Src)
	} else {
		for y := 0; y < height; y++ {
			sourceY := source.Bounds().Min.Y + y*source.Bounds().Dy()/height
			for x := 0; x < width; x++ {
				sourceX := source.Bounds().Min.X + x*source.Bounds().Dx()/width
				target.Set(x, y, source.At(sourceX, sourceY))
			}
		}
	}
	temporary, err := os.CreateTemp(filepath.Dir(outputPath), ".echocode-render-*.png")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	ok := false
	defer func() {
		_ = temporary.Close()
		if !ok {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := png.Encode(temporary, target); err != nil {
		return err
	}
	if err := temporary.Sync(); err != nil {
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	_ = os.Remove(outputPath)
	if err := os.Rename(temporaryPath, outputPath); err != nil {
		return err
	}
	ok = true
	return nil
}

func boundedImageSize(width, height, maxWidth, maxHeight int) (int, int) {
	if width < 1 || height < 1 {
		return 0, 0
	}
	if maxWidth <= 0 {
		maxWidth = 1440
	}
	if maxHeight <= 0 {
		maxHeight = 1200
	}
	if width <= maxWidth && height <= maxHeight {
		return width, height
	}
	widthRatio := float64(maxWidth) / float64(width)
	heightRatio := float64(maxHeight) / float64(height)
	ratio := widthRatio
	if heightRatio < ratio {
		ratio = heightRatio
	}
	return max(1, int(float64(width)*ratio)), max(1, int(float64(height)*ratio))
}

func renderedImageDimensions(path string) (int, int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer file.Close()
	configuration, _, err := image.DecodeConfig(file)
	if err != nil {
		return 0, 0, fmt.Errorf("render output is not a readable image: %w", err)
	}
	return configuration.Width, configuration.Height, nil
}

func pathWithinRoot(path, root string) bool {
	if strings.TrimSpace(root) == "" {
		return false
	}
	absolutePath, pathErr := filepath.Abs(path)
	absoluteRoot, rootErr := filepath.Abs(root)
	if pathErr != nil || rootErr != nil {
		return false
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	return err == nil && relative != "." && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(os.PathSeparator))
}

func escapeURLPath(path string) string {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func pruneVisualRenderCache(directory string, now time.Time) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return
	}
	type cacheEntry struct {
		path    string
		modTime time.Time
	}
	stale := make([]cacheEntry, 0)
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".png") {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr == nil && now.Sub(info.ModTime()) > visualRenderRetention {
			stale = append(stale, cacheEntry{path: filepath.Join(directory, entry.Name()), modTime: info.ModTime()})
		}
	}
	sort.Slice(stale, func(left, right int) bool { return stale[left].modTime.Before(stale[right].modTime) })
	for _, entry := range stale {
		_ = os.Remove(entry.path)
	}
}

var _ tools.ArtifactRenderer = (*artifactRenderBridge)(nil)
