//go:build windows

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestOpenDesktopFileRejectsMissingPath(t *testing.T) {
	if err := openDesktopFile(filepath.Join(t.TempDir(), "missing.html")); err == nil {
		t.Fatal("opening a missing file should fail")
	}
}

// This opt-in test exercises the real Windows file association without making
// ordinary test runs launch applications.
func TestOpenDesktopFileIntegration(t *testing.T) {
	path := os.Getenv("ECHOCODE_OPEN_INTEGRATION_FILE")
	if path == "" {
		t.Skip("ECHOCODE_OPEN_INTEGRATION_FILE is not set")
	}
	if err := openDesktopFile(path); err != nil {
		t.Fatal(err)
	}
}
