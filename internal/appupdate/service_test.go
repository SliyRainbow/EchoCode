package appupdate

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func TestCheckSelectsCurrentPlatformRelease(t *testing.T) {
	assetName := fmt.Sprintf("EchoCode-1.2.0-%s-%s-portable.zip", runtime.GOOS, runtime.GOARCH)
	wrongName := fmt.Sprintf("EchoCode-1.2.0-%s-%s-portable.zip", runtime.GOOS, wrongArchitecture())
	payload, err := json.Marshal(githubRelease{
		TagName:     "v1.2.0",
		Name:        "EchoCode 1.2.0",
		Body:        "Release notes",
		HTMLURL:     "https://example.test/releases/v1.2.0",
		PublishedAt: "2026-07-22T00:00:00Z",
		Assets: []githubAsset{
			{Name: wrongName, BrowserDownloadURL: "https://example.test/wrong.zip", Size: 20},
			{Name: assetName, BrowserDownloadURL: "https://example.test/right.zip", Size: 42},
			{Name: assetName + ".sha256", BrowserDownloadURL: "https://example.test/right.zip.sha256"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return httpResponse(request, http.StatusOK, payload, "application/json"), nil
	})}
	service := New(Options{CurrentVersion: "1.0.0", CacheDir: t.TempDir(), HTTPClient: client})
	statuses := []string{}
	service.SetNotify(func(state State) { statuses = append(statuses, state.Status) })

	state, err := service.Check(context.Background(), true)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if !state.UpdateAvailable || state.LatestVersion != "1.2.0" {
		t.Fatalf("unexpected update state: %+v", state)
	}
	if state.AssetName != assetName || state.DownloadURL != "https://example.test/right.zip" {
		t.Fatalf("selected wrong release asset: %+v", state)
	}
	if state.ChecksumURL != "https://example.test/right.zip.sha256" {
		t.Fatalf("checksum URL = %q", state.ChecksumURL)
	}
	if strings.Join(statuses, ",") != "checking,available" {
		t.Fatalf("notifications = %v", statuses)
	}
}

func TestAssetScoreRejectsOtherArchitecture(t *testing.T) {
	matching := fmt.Sprintf("EchoCode-%s-%s-portable.zip", runtime.GOOS, runtime.GOARCH)
	wrong := fmt.Sprintf("EchoCode-%s-%s-portable.zip", runtime.GOOS, wrongArchitecture())
	if score := assetScore(matching); score <= 0 {
		t.Fatalf("matching asset score = %d", score)
	}
	if score := assetScore(wrong); score != 0 {
		t.Fatalf("wrong architecture asset score = %d", score)
	}
}

func TestDownloadVerifiesChecksumAndPersistsPackage(t *testing.T) {
	packageData := []byte("portable update package")
	digest := sha256.Sum256(packageData)
	checksum := []byte(hex.EncodeToString(digest[:]) + "  EchoCode.zip\n")
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if strings.HasSuffix(request.URL.Path, ".sha256") {
			return httpResponse(request, http.StatusOK, checksum, "text/plain"), nil
		}
		return httpResponse(request, http.StatusOK, packageData, "application/zip"), nil
	})}
	cacheDir := t.TempDir()
	service := New(Options{CurrentVersion: "1.0.0", CacheDir: cacheDir, HTTPClient: client})
	service.state = State{
		CurrentVersion:  "1.0.0",
		LatestVersion:   "1.1.0",
		UpdateAvailable: true,
		Status:          "available",
		AssetName:       "EchoCode-1.1.0-portable.zip",
		DownloadURL:     "https://example.test/EchoCode.zip",
		ChecksumURL:     "https://example.test/EchoCode.zip.sha256",
		TotalBytes:      int64(len(packageData)),
	}

	state, err := service.Download(context.Background())
	if err != nil {
		t.Fatalf("Download() error = %v", err)
	}
	if state.Status != "downloaded" || !state.ChecksumVerified || state.Progress != 1 {
		t.Fatalf("unexpected download state: %+v", state)
	}
	got, err := os.ReadFile(state.DownloadPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, packageData) {
		t.Fatalf("downloaded package = %q", got)
	}
	if _, err := os.Stat(filepath.Join(cacheDir, "update-state.json")); err != nil {
		t.Fatalf("update state was not persisted: %v", err)
	}
}

func TestPrepareExecutableFromPortableZip(t *testing.T) {
	archivePath := filepath.Join(t.TempDir(), "EchoCode-portable.zip")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entry, err := writer.Create("dist/EchoCode.exe")
	if err != nil {
		t.Fatal(err)
	}
	executable := []byte("test executable")
	if _, err := entry.Write(executable); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	service := New(Options{CurrentVersion: "1.0.0", CacheDir: t.TempDir()})
	staged, err := service.prepareExecutable(archivePath, "1.1.0")
	if err != nil {
		t.Fatalf("prepareExecutable() error = %v", err)
	}
	got, err := os.ReadFile(staged)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, executable) {
		t.Fatalf("staged executable = %q", got)
	}
}

func TestVersionComparison(t *testing.T) {
	tests := []struct {
		left  string
		right string
		want  int
	}{
		{"1.2.0", "1.1.9", 1},
		{"v1.2", "1.2.0", 0},
		{"1.2.0-beta.1", "1.2.0", 0},
		{"1.0.0", "1.0.1", -1},
	}
	for _, test := range tests {
		if got := compareVersions(test.left, test.right); got != test.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", test.left, test.right, got, test.want)
		}
	}
}

func wrongArchitecture() string {
	if runtime.GOARCH == "arm64" {
		return "amd64"
	}
	return "arm64"
}

func httpResponse(request *http.Request, status int, body []byte, contentType string) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
		Header:        http.Header{"Content-Type": []string{contentType}},
		Request:       request,
	}
}
