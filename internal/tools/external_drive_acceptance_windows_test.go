//go:build windows

package tools

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const externalDriveAcceptanceEnvironment = "ECHOCODE_EXTERNAL_DRIVE_ACCEPTANCE"

func TestExternalDriveDownloadAndGitAcceptance(t *testing.T) {
	if os.Getenv(externalDriveAcceptanceEnvironment) != "1" {
		t.Skip("set " + externalDriveAcceptanceEnvironment + "=1 to run cross-drive network acceptance")
	}
	requireGitExecutable(t)

	for _, drive := range []string{"D", "E", "F"} {
		drive := drive
		t.Run(drive, func(t *testing.T) {
			root := externalDriveAcceptanceRoot(t, drive+`:\`)
			policy := SandboxPolicy{
				WorkspaceRoot: root, FilesystemAccess: "workspace-write",
				NetworkAccess: true, ShellAccess: false,
			}

			downloadPath := filepath.Join(root, "中文 下载目录", "EchoCode README.md")
			downloadArguments, _ := json.Marshal(downloadFileArguments{
				URL:         "https://raw.githubusercontent.com/SliyRainbow/EchoCode/main/README.md",
				Destination: downloadPath,
			})
			downloadResult, err := (DownloadFileTool{Policy: policy}).Execute(context.Background(), downloadArguments)
			if err != nil || downloadResult.IsError {
				t.Fatalf("download result=%#v err=%v", downloadResult, err)
			}
			content, err := os.ReadFile(downloadPath)
			if err != nil || !strings.Contains(string(content), "# EchoCode") {
				t.Fatalf("downloaded README bytes=%d err=%v", len(content), err)
			}
			if len(downloadResult.Parts) != 2 || filepath.Clean(downloadResult.Parts[1].Path) != filepath.Clean(downloadPath) {
				t.Fatalf("download parts=%#v", downloadResult.Parts)
			}

			repositoryPath := filepath.Join(root, "Git 项目", "EchoCode 浅克隆")
			cloneArguments, _ := json.Marshal(gitRepositoryArguments{
				Action: "clone", URL: "https://github.com/SliyRainbow/EchoCode.git",
				Destination: repositoryPath, Depth: 1,
			})
			cloneResult, err := (GitRepositoryTool{Policy: policy}).Execute(context.Background(), cloneArguments)
			if err != nil || cloneResult.IsError {
				t.Fatalf("clone result=%#v err=%v", cloneResult, err)
			}
			for _, expected := range []string{".git", "README.md", "go.mod"} {
				if _, err := os.Stat(filepath.Join(repositoryPath, expected)); err != nil {
					t.Fatalf("cloned repository is missing %s: %v", expected, err)
				}
			}

			pullArguments, _ := json.Marshal(gitRepositoryArguments{
				Action: "pull", Repository: repositoryPath, Remote: "origin", Strategy: "ff-only",
			})
			pullResult, err := (GitRepositoryTool{Policy: policy}).Execute(context.Background(), pullArguments)
			if err != nil || pullResult.IsError {
				t.Fatalf("pull result=%#v err=%v", pullResult, err)
			}
			if matches, _ := filepath.Glob(filepath.Join(filepath.Dir(repositoryPath), ".echocode-clone-*")); len(matches) != 0 {
				t.Fatalf("temporary clone directories remain: %v", matches)
			}
		})
	}
}

func externalDriveAcceptanceRoot(t *testing.T, driveRoot string) string {
	t.Helper()
	volumeRoot := filepath.Clean(driveRoot)
	if info, err := os.Stat(volumeRoot); err != nil || !info.IsDir() {
		t.Skipf("drive %s is unavailable", volumeRoot)
	}
	parent := filepath.Join(volumeRoot, "EchoCodeAcceptance")
	if err := os.MkdirAll(parent, 0o755); err != nil {
		t.Fatalf("create acceptance root on %s: %v", volumeRoot, err)
	}
	root, err := os.MkdirTemp(parent, "跨盘验收-")
	if err != nil {
		t.Fatalf("create acceptance directory on %s: %v", volumeRoot, err)
	}
	t.Cleanup(func() {
		cleanRoot := filepath.Clean(root)
		relative, relErr := filepath.Rel(parent, cleanRoot)
		if relErr != nil || relative == "." || relative == ".." || filepath.IsAbs(relative) || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
			t.Errorf("refusing to clean unsafe acceptance path %q", cleanRoot)
			return
		}
		if err := os.RemoveAll(cleanRoot); err != nil {
			t.Errorf("clean acceptance path %q: %v", cleanRoot, err)
		}
		_ = os.Remove(parent)
	})
	return root
}
