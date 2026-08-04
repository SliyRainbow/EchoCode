//go:build windows

package extensions

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestRunHealthCheckSupportsCommandFiles(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "health check.cmd")
	content := "@echo off\r\nif \"%~1\"==\"version\" exit /b 0\r\nexit /b 7\r\n"
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := runHealthCheck(context.Background(), path, HealthCheck{Args: []string{"version"}, TimeoutSeconds: 5}); err != nil {
		t.Fatalf("command health check failed: %v", err)
	}
}
