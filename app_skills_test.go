package main

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/SliyRainbow/EchoCode/internal/skills"
)

func TestBundledSkillsUseScopedExplicitTriggers(t *testing.T) {
	loader := skills.NewFSLoader(bundledSkills, "skills")
	index, err := loader.Index()
	if err != nil {
		t.Fatal(err)
	}
	entries := map[string]skills.IndexEntry{}
	for _, entry := range index {
		entries[entry.Name] = entry
	}
	for _, name := range []string{"echocode-agent-core", "echocode-office-artifacts"} {
		entry, ok := entries[name]
		if !ok {
			t.Fatalf("bundled skill %q is missing", name)
		}
		if entry.TriggerMode != "explicit" || len(strings.TrimSpace(entry.Trigger)) < 4 {
			t.Fatalf("bundled skill %q has an unsafe trigger: %#v", name, entry)
		}
	}

	core, err := loader.Load("echocode-agent-core")
	if err != nil {
		t.Fatal(err)
	}
	if lines := strings.Count(core.Content, "\n") + 1; lines > 50 {
		t.Fatalf("core skill expanded to %d lines; keep runtime instructions scoped", lines)
	}
	if characters := utf8.RuneCountInString(core.Content); characters > 2_000 {
		t.Fatalf("core skill expanded to %d characters; move internal design text to docs", characters)
	}
}
