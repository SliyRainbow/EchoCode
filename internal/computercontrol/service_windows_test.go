//go:build windows

package computercontrol

import (
	"context"
	"os"
	"testing"
)

func TestListWindowsDesktopProbe(t *testing.T) {
	if os.Getenv("ECHOCODE_DESKTOP_PROBE") != "1" {
		t.Skip("set ECHOCODE_DESKTOP_PROBE=1 to enumerate the active Windows desktop")
	}
	windowsList, err := New().ListWindows(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(windowsList) == 0 {
		t.Fatal("desktop probe returned no visible top-level windows")
	}
	for _, window := range windowsList {
		if window.ID == "" || window.Title == "" || window.Width < 1 || window.Height < 1 {
			t.Fatalf("invalid window metadata: %+v", window)
		}
	}
	t.Logf("enumerated %d visible top-level windows", len(windowsList))
}
