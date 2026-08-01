package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

const (
	defaultWebSearchEndpoint  = "https://search.brave.com/search"
	fallbackWebSearchEndpoint = "https://www.bing.com/search"
	maxWebSearchResponse      = 2 * 1024 * 1024
)

// WebSearchTool 使用公开 RSS 搜索端点获取当前网络信息，不依赖浏览器进程。
type WebSearchTool struct {
	Policy   SandboxPolicy
	Client   *http.Client
	Endpoint string
}

func (t WebSearchTool) Name() string { return "web_search" }

func (t WebSearchTool) Description() string {
	return "搜索公开互联网中的当前信息，返回标题、摘要和完整可引用链接。搜索摘要不是网页正文；需要核验具体页面、比较同类网站或分析用户给出的 URL 时，继续调用 read_webpage。read_webpage 会自动渲染依赖 JavaScript 的页面；仅在用户要求交互时直接使用 browser。最终答案应直接整理结论并列出实际使用的来源标题与完整 URL。"
}

func (t WebSearchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":       map[string]any{"type": "string", "description": "搜索关键词"},
			"max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "description": "返回结果数，默认 6"},
		},
		"required": []string{"query"},
	}
}

func (t WebSearchTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return webSearchError("参数解析失败: "+err.Error(), ""), nil
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return webSearchError("query 不能为空", ""), nil
	}
	if err := t.Policy.CheckNetwork(); err != nil {
		return webSearchError(err.Error(), args.Query), nil
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 6
	}
	if args.MaxResults > 10 {
		args.MaxResults = 10
	}

	endpoint := strings.TrimSpace(t.Endpoint)
	if endpoint == "" {
		endpoint = defaultWebSearchEndpoint
	}
	client := t.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	results, searchErr := fetchWebSearchResults(ctx, client, endpoint, args.Query, args.MaxResults)
	if strings.TrimSpace(t.Endpoint) == "" && (searchErr != nil || len(results) == 0) {
		fallbackResults, fallbackErr := fetchWebSearchResults(ctx, client, fallbackWebSearchEndpoint, args.Query, args.MaxResults)
		if len(fallbackResults) > 0 {
			results = fallbackResults
			searchErr = nil
		} else if searchErr == nil {
			searchErr = fallbackErr
		}
	}
	if searchErr != nil {
		return webSearchError(searchErr.Error(), args.Query), nil
	}
	if len(results) == 0 {
		return Result{
			Summary: fmt.Sprintf("网络搜索 %q 没有返回结果", args.Query),
			Parts:   []ResultPart{{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: args.Query, Output: "（无搜索结果）"}},
		}, nil
	}

	lines := make([]string, 0, len(results)*3)
	sources := make([]SearchSource, 0, len(results))
	for index, result := range results {
		lines = append(lines, fmt.Sprintf("%d. %s", index+1, result.Title), "   "+result.Link)
		if result.Description != "" {
			lines = append(lines, "   "+result.Description)
		}
		sources = append(sources, SearchSource{
			Title:   result.Title,
			URL:     result.Link,
			Snippet: result.Description,
		})
	}
	return Result{
		Summary: fmt.Sprintf("网络搜索 %q 返回 %d 条结果", args.Query, len(results)),
		Parts: []ResultPart{
			{Kind: PartToolCall, Name: t.Name(), Status: "ok", Input: args.Query, Output: strings.Join(lines, "\n")},
			{Kind: PartWebSearch, Query: args.Query, Sources: sources},
		},
	}, nil
}

func fetchWebSearchResults(
	ctx context.Context,
	client *http.Client,
	endpoint string,
	queryText string,
	limit int,
) ([]webSearchResult, error) {
	requestURL, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return nil, fmt.Errorf("搜索地址无效: %w", err)
	}
	query := requestURL.Query()
	query.Set("q", queryText)
	if strings.Contains(strings.ToLower(requestURL.Hostname()), "brave.com") {
		query.Set("source", "web")
	} else {
		query.Set("format", "rss")
		query.Set("mkt", "zh-CN")
		query.Set("setlang", "zh-hans")
		query.Set("cc", "cn")
	}
	requestURL.RawQuery = query.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("创建搜索请求失败: %w", err)
	}
	req.Header.Set("Accept", "text/html, application/rss+xml, application/xml;q=0.9, text/xml;q=0.8")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.7")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/126.0 Safari/537.36")
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("网络搜索请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("网络搜索返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebSearchResponse+1))
	if err != nil {
		return nil, fmt.Errorf("读取搜索结果失败: %w", err)
	}
	if len(body) > maxWebSearchResponse {
		return nil, errors.New("搜索结果超过大小限制")
	}

	trimmed := bytes.TrimSpace(body)
	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	if strings.Contains(contentType, "xml") || bytes.HasPrefix(trimmed, []byte("<?xml")) || bytes.HasPrefix(trimmed, []byte("<rss")) {
		var feed searchRSS
		if err := xml.NewDecoder(bytes.NewReader(body)).Decode(&feed); err != nil {
			return nil, fmt.Errorf("解析搜索结果失败: %w", err)
		}
		return normalizeSearchResults(feed.Channel.Items, limit), nil
	}
	results, err := parseBraveSearchResults(bytes.NewReader(body), limit)
	if err != nil {
		return nil, fmt.Errorf("解析搜索结果失败: %w", err)
	}
	return results, nil
}

type searchRSS struct {
	Channel struct {
		Items []searchRSSItem `xml:"item"`
	} `xml:"channel"`
}

type searchRSSItem struct {
	Title       string `xml:"title"`
	Link        string `xml:"link"`
	Description string `xml:"description"`
}

type webSearchResult struct {
	Title       string
	Link        string
	Description string
}

func parseBraveSearchResults(reader io.Reader, limit int) ([]webSearchResult, error) {
	document, err := xhtml.Parse(reader)
	if err != nil {
		return nil, err
	}
	results := make([]webSearchResult, 0, limit)
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if len(results) >= limit {
			return
		}
		if node.Type == xhtml.ElementNode && node.Data == "div" && nodeAttribute(node, "data-type") == "web" {
			if result, ok := braveSearchResult(node); ok {
				results = append(results, result)
			}
			return
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return normalizeWebSearchResults(results, limit), nil
}

func braveSearchResult(root *xhtml.Node) (webSearchResult, bool) {
	var result webSearchResult
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if result.Link == "" && node.Type == xhtml.ElementNode && node.Data == "a" {
			titleNode := findNodeByClass(node, "title")
			title := nodeText(titleNode)
			href := strings.TrimSpace(nodeAttribute(node, "href"))
			if title != "" && validHTTPURL(href) {
				result.Title = title
				result.Link = href
			}
		}
		if result.Description == "" && nodeHasClass(node, "generic-snippet") {
			result.Description = nodeText(node)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	result.Title = compactSearchText(result.Title)
	result.Description = compactSearchText(result.Description)
	return result, result.Title != "" && result.Link != ""
}

func findNodeByClass(root *xhtml.Node, className string) *xhtml.Node {
	if root == nil {
		return nil
	}
	if nodeHasClass(root, className) {
		return root
	}
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if match := findNodeByClass(child, className); match != nil {
			return match
		}
	}
	return nil
}

func nodeHasClass(node *xhtml.Node, className string) bool {
	if node == nil || node.Type != xhtml.ElementNode {
		return false
	}
	for _, value := range strings.Fields(nodeAttribute(node, "class")) {
		if value == className {
			return true
		}
	}
	return false
}

func nodeAttribute(node *xhtml.Node, key string) string {
	if node == nil {
		return ""
	}
	for _, attribute := range node.Attr {
		if attribute.Key == key {
			return attribute.Val
		}
	}
	return ""
}

func nodeText(root *xhtml.Node) string {
	if root == nil {
		return ""
	}
	var text strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.TextNode {
			text.WriteByte(' ')
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(root)
	return strings.Join(strings.Fields(text.String()), " ")
}

func validHTTPURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != ""
}

func normalizeSearchResults(items []searchRSSItem, limit int) []webSearchResult {
	raw := make([]webSearchResult, 0, len(items))
	for _, item := range items {
		link := strings.TrimSpace(item.Link)
		title := compactSearchText(item.Title)
		if title == "" {
			title = link
		}
		raw = append(raw, webSearchResult{
			Title:       title,
			Link:        link,
			Description: compactSearchText(item.Description),
		})
	}
	return normalizeWebSearchResults(raw, limit)
}

func normalizeWebSearchResults(items []webSearchResult, limit int) []webSearchResult {
	seen := map[string]bool{}
	results := make([]webSearchResult, 0, limit)
	for _, item := range items {
		item.Link = strings.TrimSpace(item.Link)
		if !validHTTPURL(item.Link) || seen[item.Link] {
			continue
		}
		seen[item.Link] = true
		item.Title = compactSearchText(item.Title)
		item.Description = compactSearchText(item.Description)
		if item.Title == "" {
			item.Title = item.Link
		}
		results = append(results, item)
		if len(results) >= limit {
			break
		}
	}
	return results
}

func compactSearchText(fragment string) string {
	nodes, err := xhtml.ParseFragment(strings.NewReader(fragment), nil)
	if err != nil {
		return strings.Join(strings.Fields(fragment), " ")
	}
	var text strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.TextNode {
			text.WriteString(" ")
			text.WriteString(node.Data)
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	for _, node := range nodes {
		walk(node)
	}
	return strings.Join(strings.Fields(text.String()), " ")
}

func webSearchError(message, query string) Result {
	return Result{
		Summary: message,
		IsError: true,
		Parts:   []ResultPart{{Kind: PartToolCall, Name: "web_search", Status: "error", Input: query, Output: message}},
	}
}
