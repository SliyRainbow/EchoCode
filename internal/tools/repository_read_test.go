package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestReadRepositoryToolReturnsRealOverview(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/acme/demo":
			_, _ = w.Write([]byte(`{"full_name":"acme/demo","html_url":"https://github.com/acme/demo","description":"Demo repository","default_branch":"main","language":"Go","stargazers_count":42,"forks_count":7}`))
		case "/repos/acme/demo/commits/main":
			_, _ = w.Write([]byte(`{"sha":"abc123"}`))
		case "/repos/acme/demo/git/trees/abc123":
			if request.URL.Query().Get("recursive") != "1" {
				t.Fatalf("recursive tree query missing: %s", request.URL.RawQuery)
			}
			_, _ = w.Write([]byte(`{"sha":"tree123","truncated":false,"tree":[{"path":"README.md","type":"blob","size":21},{"path":"cmd","type":"tree"},{"path":"cmd/main.go","type":"blob","size":30}]}`))
		case "/repos/acme/demo/readme":
			if request.URL.Query().Get("ref") != "main" {
				t.Fatalf("README ref = %q", request.URL.Query().Get("ref"))
			}
			content := base64.StdEncoding.EncodeToString([]byte("# Demo\nReal README content."))
			_ = json.NewEncoder(w).Encode(githubContent{Type: "file", Path: "README.md", Size: 27, Encoding: "base64", Content: content})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	tool := ReadRepositoryTool{
		Policy: toolsNetworkPolicy(), Client: server.Client(), APIBaseURL: server.URL,
	}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"https://github.com/acme/demo","max_entries":50}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if len(result.Parts) != 1 || result.Parts[0].Name != "read_repository" {
		t.Fatalf("parts=%+v", result.Parts)
	}
	output := result.Parts[0].Output
	for _, expected := range []string{"acme/demo", "Commit: abc123", "Real README content", "cmd/main.go (30 bytes)"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("overview missing %q:\n%s", expected, output)
		}
	}
}

func TestReadRepositoryToolReadsBlobURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/acme/demo":
			_, _ = w.Write([]byte(`{"full_name":"acme/demo","default_branch":"main"}`))
		case "/repos/acme/demo/contents/src/main.go":
			if request.URL.Query().Get("ref") != "dev" {
				t.Fatalf("file ref = %q", request.URL.Query().Get("ref"))
			}
			content := base64.StdEncoding.EncodeToString([]byte("package main\n\nfunc main() {}\n"))
			_ = json.NewEncoder(w).Encode(githubContent{Type: "file", Path: "src/main.go", SHA: "file123", Size: 29, Encoding: "base64", Content: content})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	tool := ReadRepositoryTool{Policy: toolsNetworkPolicy(), Client: server.Client(), APIBaseURL: server.URL}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"https://github.com/acme/demo/blob/dev/src/main.go"}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	output := result.Parts[0].Output
	for _, expected := range []string{"Ref: dev", "Path: src/main.go", "package main", "SHA: file123"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("file output missing %q:\n%s", expected, output)
		}
	}
}

func TestRepositoryPathSuggestionsPreferMatchingBasenameAndParent(t *testing.T) {
	suggestions := repositoryPathSuggestions("frontend/src/styles/styles.css", []string{
		"README.md",
		"frontend/src/styles/chat.css",
		"frontend/src/styles.css",
		"frontend/src/styles/polish.css",
		"internal/tools/styles.css",
	}, 3)
	if len(suggestions) == 0 || suggestions[0] != "frontend/src/styles.css" {
		t.Fatalf("suggestions=%v", suggestions)
	}
	err := repositoryPathNotFoundError("frontend/src/styles/styles.css", suggestions)
	if !strings.Contains(err.Error(), "frontend/src/styles.css") {
		t.Fatalf("error=%q", err)
	}
}

func TestReadRepositoryToolUsesExplicitGitHubTokenWithoutLeakingIt(t *testing.T) {
	const token = "github-test-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Fatalf("authorization=%q", request.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/repos/acme/demo":
			_, _ = w.Write([]byte(`{"full_name":"acme/demo","default_branch":"main"}`))
		case "/repos/acme/demo/contents/README.md":
			content := base64.StdEncoding.EncodeToString([]byte("# private-looking public fixture"))
			_ = json.NewEncoder(w).Encode(githubContent{Type: "file", Path: "README.md", SHA: "file123", Size: 32, Encoding: "base64", Content: content})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	tool := ReadRepositoryTool{Policy: toolsNetworkPolicy(), Client: server.Client(), APIBaseURL: server.URL, Token: token}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"https://github.com/acme/demo","path":"README.md"}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	serialized, _ := json.Marshal(result)
	if strings.Contains(string(serialized), token) {
		t.Fatalf("token leaked into result: %s", serialized)
	}
}

func TestReadRepositoryToolRequiresNetworkPermission(t *testing.T) {
	tool := ReadRepositoryTool{Policy: SandboxPolicy{NetworkAccess: false}}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"https://github.com/acme/demo"}`))
	if err != nil || !result.IsError || !strings.Contains(result.Summary, "网络") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestReadRepositoryToolGitHubIntegration(t *testing.T) {
	if os.Getenv("ECHOCODE_GITHUB_INTEGRATION") != "1" {
		t.Skip("set ECHOCODE_GITHUB_INTEGRATION=1 to read a real public GitHub repository")
	}
	tool := ReadRepositoryTool{Policy: toolsNetworkPolicy()}
	result, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"https://github.com/chenyme/grok2api","max_entries":80}`))
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	output := result.Parts[0].Output
	for _, expected := range []string{"Repository: chenyme/grok2api", "Commit:", "Repository tree:"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("integration output missing %q:\n%s", expected, output)
		}
	}
}

func toolsNetworkPolicy() SandboxPolicy {
	return SandboxPolicy{NetworkAccess: true}
}
