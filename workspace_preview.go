package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// BrowserPreview describes a workspace file served to the embedded browser.
type BrowserPreview struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	URL     string `json:"url"`
	Ask     bool   `json:"ask,omitempty"`
	TabID   string `json:"tabId,omitempty"`
	Managed bool   `json:"managed,omitempty"`
}

// workspacePreviewServer serves one active workspace through an unguessable
// URL. A new preview invalidates the previous token, so stale panels cannot
// keep reading a workspace after the user opens another artifact.
type workspacePreviewServer struct {
	mu       sync.RWMutex
	server   *http.Server
	listener net.Listener
	baseURL  string
	token    string
	root     string
}

func newWorkspacePreviewServer() *workspacePreviewServer {
	return &workspacePreviewServer{}
}

func (p *workspacePreviewServer) Preview(workspaceRoot, filePath string) (BrowserPreview, error) {
	root, file, relativePath, err := resolvePreviewTarget(workspaceRoot, filePath)
	if err != nil {
		return BrowserPreview{}, err
	}
	token, err := randomPreviewToken()
	if err != nil {
		return BrowserPreview{}, fmt.Errorf("创建预览令牌失败: %w", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.startLocked(); err != nil {
		return BrowserPreview{}, err
	}
	p.token = token
	p.root = root

	previewURL := p.baseURL + "/" + token + "/" + escapePreviewPath(relativePath)
	if info, statErr := os.Stat(file); statErr == nil {
		previewURL += "?v=" + fmt.Sprintf("%d", info.ModTime().UnixNano())
	}
	return BrowserPreview{
		Path: file,
		Name: filepath.Base(file),
		URL:  previewURL,
	}, nil
}

func (p *workspacePreviewServer) Reset() {
	p.mu.Lock()
	p.token = ""
	p.root = ""
	p.mu.Unlock()
}

func (p *workspacePreviewServer) Close(ctx context.Context) error {
	p.mu.Lock()
	server := p.server
	p.server = nil
	p.listener = nil
	p.baseURL = ""
	p.token = ""
	p.root = ""
	p.mu.Unlock()
	if server == nil {
		return nil
	}
	return server.Shutdown(ctx)
}

func (p *workspacePreviewServer) startLocked() error {
	if p.server != nil {
		return nil
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("启动内置浏览器预览服务失败: %w", err)
	}
	server := &http.Server{
		Handler:           http.HandlerFunc(p.serveHTTP),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       30 * time.Second,
	}
	p.listener = listener
	p.server = server
	p.baseURL = "http://" + listener.Addr().String()
	go func() {
		_ = server.Serve(listener)
	}()
	return nil
}

func (p *workspacePreviewServer) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	p.mu.RLock()
	token, root := p.token, p.root
	p.mu.RUnlock()
	prefix := "/" + token + "/"
	if token == "" || root == "" || !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	relativeURLPath := strings.TrimPrefix(r.URL.Path, prefix)
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

	file, err := os.Open(resolved)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer file.Close()

	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	contentType := mime.TypeByExtension(strings.ToLower(filepath.Ext(resolved)))
	if contentType == "" {
		var sample [512]byte
		n, readErr := file.Read(sample[:])
		if readErr != nil && readErr != io.EOF {
			http.NotFound(w, r)
			return
		}
		contentType = http.DetectContentType(sample[:n])
		_, _ = file.Seek(0, io.SeekStart)
	}
	w.Header().Set("Content-Type", contentType)
	http.ServeContent(w, r, filepath.Base(resolved), info.ModTime(), file)
}

func resolvePreviewTarget(workspaceRoot, filePath string) (string, string, string, error) {
	root, err := filepath.Abs(strings.TrimSpace(workspaceRoot))
	if err != nil || strings.TrimSpace(workspaceRoot) == "" {
		return "", "", "", fmt.Errorf("工作区根目录无效")
	}
	root, err = filepath.EvalSymlinks(filepath.Clean(root))
	if err != nil {
		return "", "", "", fmt.Errorf("工作区根目录无法访问: %w", err)
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return "", "", "", fmt.Errorf("工作区根目录不是可访问的目录")
	}

	file, err := filepath.Abs(strings.TrimSpace(filePath))
	if err != nil || strings.TrimSpace(filePath) == "" {
		return "", "", "", fmt.Errorf("预览文件路径无效")
	}
	file, err = filepath.EvalSymlinks(filepath.Clean(file))
	if err != nil {
		return "", "", "", fmt.Errorf("预览文件无法访问: %w", err)
	}
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		return "", "", "", fmt.Errorf("预览目标不是普通文件")
	}
	if !previewPathWithinRoot(file, root) {
		return "", "", "", fmt.Errorf("预览文件超出当前工作区")
	}
	relativePath, err := filepath.Rel(root, file)
	if err != nil || relativePath == "." {
		return "", "", "", fmt.Errorf("无法计算预览文件的工作区路径")
	}
	return root, file, filepath.ToSlash(relativePath), nil
}

func resolveServedPreviewPath(root, requested string) (string, os.FileInfo, error) {
	resolved, err := filepath.EvalSymlinks(filepath.Clean(requested))
	if err != nil {
		return "", nil, err
	}
	if !previewPathWithinRoot(resolved, root) {
		return "", nil, fmt.Errorf("requested path leaves workspace")
	}
	info, err := os.Stat(resolved)
	return resolved, info, err
}

func previewPathWithinRoot(target, root string) bool {
	relativePath, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relativePath == "." || (relativePath != ".." && !strings.HasPrefix(relativePath, ".."+string(os.PathSeparator)))
}

func randomPreviewToken() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func escapePreviewPath(relativePath string) string {
	parts := strings.Split(filepath.ToSlash(relativePath), "/")
	for index, part := range parts {
		parts[index] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func isHTMLFile(filePath string) bool {
	extension := strings.ToLower(filepath.Ext(filePath))
	return extension == ".html" || extension == ".htm"
}
