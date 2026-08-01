package main

import (
	"os"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/computercontrol"
)

func TestComputerControlEnabled(t *testing.T) {
	tests := []struct {
		name        string
		anyApp      bool
		chrome      bool
		allowedApps []string
		want        bool
	}{
		{name: "disabled", want: false},
		{name: "blank allowlist", allowedApps: []string{"", "  "}, want: false},
		{name: "any app", anyApp: true, want: true},
		{name: "chrome", chrome: true, want: true},
		{name: "allowlist", allowedApps: []string{"Code.exe"}, want: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := computerControlEnabled(test.anyApp, test.chrome, test.allowedApps); got != test.want {
				t.Fatalf("computerControlEnabled() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestAllowedComputerWindow(t *testing.T) {
	tests := []struct {
		name        string
		window      computercontrol.Window
		anyApp      bool
		chrome      bool
		allowedApps []string
		want        bool
	}{
		{
			name:   "current process is always denied",
			window: computercontrol.Window{PID: uint32(os.Getpid()), Title: "EchoCode", ProcessName: "unknown.exe"},
			anyApp: true,
			want:   false,
		},
		{
			name:   "echocode name is always denied",
			window: computercontrol.Window{PID: 999999, Title: "EchoCode", ProcessName: "EchoCode.exe"},
			anyApp: true,
			want:   false,
		},
		{
			name:   "any app allows external window",
			window: computercontrol.Window{PID: 999999, Title: "Notepad", ProcessName: "notepad.exe"},
			anyApp: true,
			want:   true,
		},
		{
			name:   "chrome permission only allows chrome",
			window: computercontrol.Window{PID: 999999, Title: "Docs", ProcessName: "Chrome.EXE"},
			chrome: true,
			want:   true,
		},
		{
			name:   "chrome permission rejects another process",
			window: computercontrol.Window{PID: 999999, Title: "Editor", ProcessName: "Code.exe"},
			chrome: true,
			want:   false,
		},
		{
			name:        "process allowlist is case insensitive",
			window:      computercontrol.Window{PID: 999999, Title: "Editor", ProcessName: `C:\\Program Files\\Code.exe`},
			allowedApps: []string{"code.EXE"},
			want:        true,
		},
		{
			name:        "title allowlist supports a descriptive fragment",
			window:      computercontrol.Window{PID: 999999, Title: "Quarterly report - Notepad", ProcessName: "notepad.exe"},
			allowedApps: []string{"Quarterly report"},
			want:        true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := allowedComputerWindow(test.window, test.anyApp, test.chrome, test.allowedApps); got != test.want {
				t.Fatalf("allowedComputerWindow() = %v, want %v", got, test.want)
			}
		})
	}
}
