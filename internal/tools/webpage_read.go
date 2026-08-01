package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/charset"
)

const (
	defaultWebpageCharacters = 9000
	maximumWebpageCharacters = 24000
	maximumWebpageResponse   = 5 * 1024 * 1024
	maximumWebpageLinks      = 32
)

var errWebpageRequiresBrowser = errors.New("网页没有可读取的正文；页面可能依赖 JavaScript")

// WebpageBrowserRenderer renders one URL and snapshots that exact browser tab.
// It is intentionally smaller than BrowserController so read_webpage can use a
// read-only fallback without depending on interactive browser actions.
type WebpageBrowserRenderer interface {
	ReadURLSnapshot(context.Context, string) (string, error)
}

// ReadWebpageTool reads the actual response body of a public webpage. It is
// intentionally separate from web_search so models can distinguish snippets
// from source text and use the managed browser only for JavaScript-only pages.
type ReadWebpageTool struct {
	Policy  SandboxPolicy
	Client  *http.Client
	Browser WebpageBrowserRenderer
}

func (t ReadWebpageTool) Name() string { return "read_webpage" }

func (t ReadWebpageTool) Description() string {
	return "读取 HTTP/HTTPS 网页的真实正文，而不是搜索摘要。返回最终 URL、标题、正文和页面中的可引用链接；用户给出具体网页、要求分析页面或比较同类网站时使用。静态响应没有正文时会自动通过 EchoCode 内置浏览器渲染 JavaScript 并读取对应标签页快照。"
}

func (t ReadWebpageTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url":       map[string]any{"type": "string", "description": "要读取的完整 HTTP/HTTPS URL"},
			"max_chars": map[string]any{"type": "integer", "minimum": 1000, "maximum": maximumWebpageCharacters, "description": "返回的最大正文字符数，默认 9000"},
		},
		"required": []string{"url"},
	}
}

func (t ReadWebpageTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args struct {
		URL      string `json:"url"`
		MaxChars int    `json:"max_chars"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return webpageReadError("参数解析失败: "+err.Error(), args.URL), nil
	}
	args.URL = strings.TrimSpace(args.URL)
	parsed, err := validateWebpageURL(args.URL)
	if err != nil {
		return webpageReadError(err.Error(), args.URL), nil
	}
	if err := t.Policy.CheckNetwork(); err != nil {
		return webpageReadError(err.Error(), args.URL), nil
	}
	if args.MaxChars <= 0 {
		args.MaxChars = defaultWebpageCharacters
	}
	if args.MaxChars > maximumWebpageCharacters {
		args.MaxChars = maximumWebpageCharacters
	}

	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return webpageReadError("创建网页请求失败: "+err.Error(), args.URL), nil
	}
	request.Header.Set("Accept", "text/html, application/xhtml+xml, text/plain;q=0.9, application/json;q=0.6, */*;q=0.2")
	request.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	request.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36 EchoCode/1.0")

	response, err := client.Do(request)
	if err != nil {
		return webpageReadError("网页请求失败: "+err.Error(), args.URL), nil
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return webpageReadError(fmt.Sprintf("网页返回 HTTP %d", response.StatusCode), args.URL), nil
	}
	if response.Request != nil && response.Request.URL != nil {
		if _, err := validateWebpageURL(response.Request.URL.String()); err != nil {
			return webpageReadError("网页重定向到了不受支持的地址", args.URL), nil
		}
	}
	rawBody, err := io.ReadAll(io.LimitReader(response.Body, maximumWebpageResponse+1))
	if err != nil {
		return webpageReadError("读取网页响应失败: "+err.Error(), args.URL), nil
	}
	if len(rawBody) > maximumWebpageResponse {
		return webpageReadError(fmt.Sprintf("网页响应超过 %d MB 限制", maximumWebpageResponse/(1024*1024)), args.URL), nil
	}
	if len(bytes.TrimSpace(rawBody)) == 0 {
		return t.readRenderedWebpage(ctx, args.URL, args.MaxChars, "网页返回了空内容")
	}

	finalURL := parsed
	if response.Request != nil && response.Request.URL != nil {
		finalURL = response.Request.URL
	}
	page, err := parseWebpage(rawBody, response.Header.Get("Content-Type"), finalURL, args.MaxChars)
	if err != nil {
		if errors.Is(err, errWebpageRequiresBrowser) {
			return t.readRenderedWebpage(ctx, args.URL, args.MaxChars, err.Error())
		}
		return webpageReadError(err.Error(), args.URL), nil
	}
	output := formatWebpageReadOutput(page)
	return Result{
		Summary: fmt.Sprintf("已读取网页正文：%s（%d 字符，%d 个链接）", page.DisplayTitle(), utf8.RuneCountInString(page.Text), len(page.Links)),
		Parts: []ResultPart{{
			Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: args.URL, Output: output,
		}},
	}, nil
}

type renderedWebpageSnapshot struct {
	Title    string                           `json:"title"`
	URL      string                           `json:"url"`
	Text     string                           `json:"text"`
	Elements []renderedWebpageSnapshotElement `json:"elements"`
}

type renderedWebpageSnapshotElement struct {
	Name string `json:"name"`
	Text string `json:"text"`
	Href string `json:"href"`
}

func (t ReadWebpageTool) readRenderedWebpage(ctx context.Context, rawURL string, maxChars int, staticReason string) (Result, error) {
	if t.Browser == nil {
		return webpageReadError(staticReason+"；无法自动读取 JavaScript 页面，请在设置中启用内置浏览器", rawURL), nil
	}
	rawSnapshot, err := t.Browser.ReadURLSnapshot(ctx, rawURL)
	if err != nil {
		return webpageReadError(staticReason+"；内置浏览器自动读取失败: "+err.Error(), rawURL), nil
	}
	var snapshot renderedWebpageSnapshot
	if err := json.Unmarshal([]byte(rawSnapshot), &snapshot); err != nil {
		return webpageReadError(staticReason+"；解析内置浏览器快照失败: "+err.Error(), rawURL), nil
	}
	text := strings.TrimSpace(snapshot.Text)
	if text == "" {
		text = renderedWebpageElementText(snapshot.Elements)
	}
	text, truncated := clipWebpageText(text, maxChars)
	if text == "" {
		return webpageReadError(staticReason+"；内置浏览器完成渲染后仍没有可读取的正文", rawURL), nil
	}
	pageURL := rawURL
	if parsed, validateErr := validateWebpageURL(snapshot.URL); validateErr == nil {
		pageURL = parsed.String()
	}
	page := webpageDocument{
		URL:       pageURL,
		Title:     strings.TrimSpace(snapshot.Title),
		Text:      text,
		Truncated: truncated,
		Links:     renderedWebpageLinks(snapshot.Elements, pageURL, maximumWebpageLinks),
	}
	output := "Read mode: EchoCode managed browser snapshot\n\n" + formatWebpageReadOutput(page)
	return Result{
		Summary: fmt.Sprintf("静态正文为空，已通过内置浏览器读取：%s（%d 字符，%d 个链接）", page.DisplayTitle(), utf8.RuneCountInString(page.Text), len(page.Links)),
		Parts: []ResultPart{{
			Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: rawURL, Output: output,
		}},
	}, nil
}

func renderedWebpageElementText(elements []renderedWebpageSnapshotElement) string {
	seen := make(map[string]bool)
	lines := make([]string, 0, len(elements))
	for _, element := range elements {
		value := compactSearchText(element.Text)
		if value == "" {
			value = compactSearchText(element.Name)
		}
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		lines = append(lines, value)
	}
	return strings.Join(lines, "\n")
}

func renderedWebpageLinks(elements []renderedWebpageSnapshotElement, pageURL string, limit int) []webpageLink {
	base, _ := url.Parse(pageURL)
	links := make([]webpageLink, 0, limit)
	seen := make(map[string]bool)
	for _, element := range elements {
		if len(links) >= limit || strings.TrimSpace(element.Href) == "" || base == nil {
			continue
		}
		resolved, err := base.Parse(strings.TrimSpace(element.Href))
		if err != nil || !validHTTPURL(resolved.String()) {
			continue
		}
		resolved.Fragment = ""
		address := resolved.String()
		if seen[address] {
			continue
		}
		seen[address] = true
		title := compactSearchText(element.Text)
		if title == "" {
			title = compactSearchText(element.Name)
		}
		if title == "" {
			title = resolved.Hostname()
		}
		links = append(links, webpageLink{Title: title, URL: address})
	}
	return links
}

type webpageDocument struct {
	URL         string
	Title       string
	Description string
	Text        string
	Truncated   bool
	Links       []webpageLink
}

type webpageLink struct {
	Title string
	URL   string
}

func (p webpageDocument) DisplayTitle() string {
	if strings.TrimSpace(p.Title) != "" {
		return strings.TrimSpace(p.Title)
	}
	if parsed, err := url.Parse(p.URL); err == nil && parsed.Hostname() != "" {
		return parsed.Hostname()
	}
	return p.URL
}

func validateWebpageURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return nil, errors.New("网页 URL 无效")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("只支持 HTTP 或 HTTPS 网页")
	}
	if parsed.User != nil {
		return nil, errors.New("网页 URL 不能包含用户名或密码")
	}
	return parsed, nil
}

func parseWebpage(raw []byte, contentType string, finalURL *url.URL, maxChars int) (webpageDocument, error) {
	mediaType := strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if mediaType != "" && !strings.Contains(mediaType, "html") && !strings.HasPrefix(mediaType, "text/") && mediaType != "application/json" && !strings.Contains(mediaType, "xml") {
		return webpageDocument{}, fmt.Errorf("网页返回了不支持的内容类型 %q", mediaType)
	}
	decodedReader, err := charset.NewReader(bytes.NewReader(raw), contentType)
	if err != nil {
		return webpageDocument{}, fmt.Errorf("识别网页字符集失败: %w", err)
	}
	if mediaType == "application/json" || strings.HasPrefix(mediaType, "text/plain") || strings.HasPrefix(mediaType, "text/markdown") {
		decoded, readErr := io.ReadAll(decodedReader)
		if readErr != nil {
			return webpageDocument{}, fmt.Errorf("解码网页文本失败: %w", readErr)
		}
		text, truncated := clipWebpageText(string(decoded), maxChars)
		if text == "" {
			return webpageDocument{}, errWebpageRequiresBrowser
		}
		return webpageDocument{URL: finalURL.String(), Text: text, Truncated: truncated}, nil
	}

	document, err := xhtml.Parse(decodedReader)
	if err != nil {
		return webpageDocument{}, fmt.Errorf("解析网页 HTML 失败: %w", err)
	}
	baseURL := webpageBaseURL(document, finalURL)
	text, truncated := clipWebpageText(extractWebpageText(document), maxChars)
	if text == "" {
		return webpageDocument{}, errWebpageRequiresBrowser
	}
	return webpageDocument{
		URL:         finalURL.String(),
		Title:       webpageTitle(document),
		Description: webpageDescription(document),
		Text:        text,
		Truncated:   truncated,
		Links:       webpageLinks(document, baseURL, maximumWebpageLinks),
	}, nil
}

func webpageTitle(document *xhtml.Node) string {
	if value := webpageMetaContent(document, "property", "og:title"); value != "" {
		return value
	}
	var title string
	walkWebpageNodes(document, func(node *xhtml.Node) bool {
		if node.Type == xhtml.ElementNode && node.Data == "title" {
			title = compactSearchText(nodeText(node))
			return false
		}
		return title == ""
	})
	return title
}

func webpageDescription(document *xhtml.Node) string {
	if value := webpageMetaContent(document, "name", "description"); value != "" {
		return value
	}
	return webpageMetaContent(document, "property", "og:description")
}

func webpageMetaContent(document *xhtml.Node, key, expected string) string {
	result := ""
	walkWebpageNodes(document, func(node *xhtml.Node) bool {
		if node.Type == xhtml.ElementNode && node.Data == "meta" && strings.EqualFold(nodeAttribute(node, key), expected) {
			result = compactSearchText(nodeAttribute(node, "content"))
			return false
		}
		return result == ""
	})
	return result
}

func webpageBaseURL(document *xhtml.Node, fallback *url.URL) *url.URL {
	base := *fallback
	walkWebpageNodes(document, func(node *xhtml.Node) bool {
		if node.Type != xhtml.ElementNode || node.Data != "base" {
			return true
		}
		candidate, err := fallback.Parse(strings.TrimSpace(nodeAttribute(node, "href")))
		if err == nil && candidate.Host != "" && (candidate.Scheme == "http" || candidate.Scheme == "https") {
			base = *candidate
		}
		return false
	})
	return &base
}

func webpageLinks(document *xhtml.Node, baseURL *url.URL, limit int) []webpageLink {
	links := make([]webpageLink, 0, limit)
	seen := make(map[string]bool)
	walkWebpageNodes(document, func(node *xhtml.Node) bool {
		if len(links) >= limit {
			return false
		}
		if node.Type != xhtml.ElementNode || node.Data != "a" {
			return true
		}
		href := strings.TrimSpace(nodeAttribute(node, "href"))
		if href == "" || strings.HasPrefix(href, "#") {
			return true
		}
		resolved, err := baseURL.Parse(href)
		if err != nil || !validHTTPURL(resolved.String()) {
			return true
		}
		resolved.Fragment = ""
		address := resolved.String()
		if seen[address] {
			return true
		}
		seen[address] = true
		title := compactSearchText(nodeText(node))
		if title == "" {
			title = resolved.Hostname()
		}
		links = append(links, webpageLink{Title: title, URL: address})
		return true
	})
	return links
}

func extractWebpageText(document *xhtml.Node) string {
	var output strings.Builder
	var lastByte byte
	appendBreak := func() {
		if output.Len() > 0 && lastByte != '\n' {
			output.WriteByte('\n')
			lastByte = '\n'
		}
	}
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.ElementNode && webpageHiddenElement(node.Data) {
			return
		}
		block := node.Type == xhtml.ElementNode && webpageBlockElement(node.Data)
		if block {
			appendBreak()
		}
		if node.Type == xhtml.TextNode {
			text := strings.Join(strings.Fields(node.Data), " ")
			if text != "" {
				if output.Len() > 0 && lastByte != '\n' && lastByte != ' ' {
					output.WriteByte(' ')
				}
				output.WriteString(text)
				lastByte = text[len(text)-1]
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
		if block {
			appendBreak()
		}
	}
	walk(document)
	lines := strings.Split(strings.ReplaceAll(output.String(), "\r", ""), "\n")
	normalized := make([]string, 0, len(lines))
	previous := ""
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" || line == previous {
			continue
		}
		normalized = append(normalized, line)
		previous = line
	}
	return strings.Join(normalized, "\n")
}

func webpageHiddenElement(name string) bool {
	switch name {
	case "head", "script", "style", "noscript", "template", "svg", "canvas", "nav", "footer", "aside", "form":
		return true
	default:
		return false
	}
}

func webpageBlockElement(name string) bool {
	switch name {
	case "article", "main", "section", "div", "p", "li", "h1", "h2", "h3", "h4", "h5", "h6", "pre", "blockquote", "tr", "br", "header":
		return true
	default:
		return false
	}
}

func walkWebpageNodes(root *xhtml.Node, visit func(*xhtml.Node) bool) bool {
	if root == nil || !visit(root) {
		return false
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if !walkWebpageNodes(child, visit) {
			return false
		}
	}
	return true
}

func clipWebpageText(value string, maxChars int) (string, bool) {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if maxChars <= 0 || len(runes) <= maxChars {
		return value, false
	}
	return strings.TrimSpace(string(runes[:maxChars])) + "\n... [正文已截取]", true
}

func formatWebpageReadOutput(page webpageDocument) string {
	var output strings.Builder
	fmt.Fprintf(&output, "URL: %s\nTitle: %s\n", page.URL, page.DisplayTitle())
	if page.Description != "" {
		fmt.Fprintf(&output, "Description: %s\n", page.Description)
	}
	output.WriteString("\nPage content:\n")
	output.WriteString(page.Text)
	if len(page.Links) > 0 {
		output.WriteString("\n\nPage links:\n")
		for _, link := range page.Links {
			fmt.Fprintf(&output, "- %s\n  %s\n", link.Title, link.URL)
		}
	}
	return strings.TrimSpace(output.String())
}

func webpageReadError(message, input string) Result {
	return Result{
		Summary: message,
		IsError: true,
		Parts: []ResultPart{{
			Kind: PartToolCall, Name: "read_webpage", Status: "error", Input: strings.TrimSpace(input), Output: message,
		}},
	}
}
