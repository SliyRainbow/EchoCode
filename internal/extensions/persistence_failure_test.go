package extensions

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFailedActivationRestoresPreviousVersion(t *testing.T) {
	for _, version := range []string{"1.0.0", "2.0.0"} {
		t.Run(version, func(t *testing.T) {
			service, _, root := testInstallService(t, nil)
			previousDir := filepath.Join(root, "mcp.test", "1.0.0", platformKey())
			if err := os.MkdirAll(previousDir, 0o700); err != nil {
				t.Fatal(err)
			}
			oldFile := filepath.Join(previousDir, "old.txt")
			if err := os.WriteFile(oldFile, []byte("old version still usable"), 0o600); err != nil {
				t.Fatal(err)
			}
			previous := InstalledPackage{ID: "mcp.test", Version: "1.0.0", InstallDir: previousDir, Executable: oldFile}
			state := installedState{Packages: []InstalledPackage{previous}}
			if err := service.writeInstalledState(state); err != nil {
				t.Fatal(err)
			}
			nextDir := filepath.Join(root, "mcp.test", version, platformKey())
			if err := os.MkdirAll(filepath.Dir(nextDir), 0o700); err != nil {
				t.Fatal(err)
			}
			staged := filepath.Join(root, "new-package")
			if err := os.MkdirAll(staged, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(staged, "new.txt"), []byte("new"), 0o600); err != nil {
				t.Fatal(err)
			}
			next := InstalledPackage{ID: previous.ID, Version: version, InstallDir: nextDir}
			activationErr := errors.New("MCP configuration save failed")
			err := service.commitInstallAndActivate(staged, next, state, func() error { return activationErr })
			if !errors.Is(err, activationErr) {
				t.Fatalf("activation error = %v", err)
			}
			if got, ok := service.Installed(previous.ID); !ok || got.Version != previous.Version {
				t.Fatalf("previous state not restored: %#v", got)
			}
			if data, err := os.ReadFile(oldFile); err != nil || string(data) != "old version still usable" {
				t.Fatalf("old version lost: %q %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(nextDir, "new.txt")); !os.IsNotExist(err) {
				t.Fatal("failed installation left replacement files")
			}
		})
	}
}

func TestPackageFilesSurviveFailedMetadataCommit(t *testing.T) {
	for _, action := range []string{"repair", "uninstall"} {
		t.Run(action, func(t *testing.T) {
			service, _, root := testInstallService(t, nil)
			target := filepath.Join(root, "mcp.test", "1.0.0", platformKey())
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatal(err)
			}
			original := filepath.Join(target, "old.txt")
			if err := os.WriteFile(original, []byte("original package"), 0o600); err != nil {
				t.Fatal(err)
			}
			current := InstalledPackage{ID: "mcp.test", Version: "1.0.0", InstallDir: target, Executable: original}
			state := installedState{Packages: []InstalledPackage{current}}
			if err := service.writeInstalledState(state); err != nil {
				t.Fatal(err)
			}
			// Simulate a write failure after the metadata read, before commit.
			metadata := filepath.Join(root, "installed.json")
			if err := os.Rename(metadata, metadata+".original"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(metadata, 0o700); err != nil {
				t.Fatal(err)
			}
			var err error
			if action == "repair" {
				staged := filepath.Join(root, "new-package")
				if err := os.Mkdir(staged, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(staged, "new.txt"), []byte("replacement"), 0o600); err != nil {
					t.Fatal(err)
				}
				err = service.commitInstall(staged, current, state)
			} else {
				err = service.commitUninstall(current, state)
			}
			if err == nil {
				t.Fatal("expected commit error")
			}
			if data, err := os.ReadFile(original); err != nil || string(data) != "original package" {
				t.Fatalf("original files were lost: %q, %v", data, err)
			}
			if _, err := os.Stat(filepath.Join(target, "new.txt")); !os.IsNotExist(err) {
				t.Fatalf("replacement survived failed commit: %v", err)
			}
			if err := os.Remove(metadata); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(metadata+".original", metadata); err != nil {
				t.Fatal(err)
			}
			if installed, ok := service.Installed(current.ID); !ok || installed.Version != current.Version {
				t.Fatal("original metadata was not preserved")
			}
		})
	}
}

func TestInstallRejectsCorruptInstalledMetadata(t *testing.T) {
	service, cache, root := testInstallService(t, nil)
	writeTestCatalog(t, service, cache, "https://example.invalid/package.zip", strings.Repeat("a", 64))
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "installed.json")
	if err := os.WriteFile(path, []byte("broken metadata"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Install(context.Background(), "mcp.test"); err == nil || !strings.Contains(err.Error(), "安装状态") {
		t.Fatalf("expected metadata error before download, got %v", err)
	}
	if err := service.Uninstall("mcp.test"); err == nil {
		t.Fatal("corrupt metadata allowed uninstall")
	}
	if data, _ := os.ReadFile(path); string(data) != "broken metadata" {
		t.Fatal("corrupt file was overwritten")
	}
	state, err := service.Catalog(context.Background(), false)
	if err != nil || !strings.Contains(state.Warning, "安装状态") {
		t.Fatalf("catalog hid metadata failure: %#v, %v", state, err)
	}
}
