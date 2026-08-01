package skills

import (
	"fmt"
	"strings"
)

type IndexEntry struct {
	Name        string `json:"name"`
	Version     int    `json:"version"`
	Trigger     string `json:"trigger"`
	TriggerMode string `json:"triggerMode,omitempty"`
	Summary     string `json:"summary"`
	SHA256      string `json:"sha256"`
	Description string `json:"description"`
	// Disabled is a host-side setting. Disabled skills remain visible in the
	// workbench, but are omitted from the model context and trigger matching.
	Disabled bool   `json:"disabled"`
	Source   string `json:"source,omitempty"`
	Path     string `json:"path,omitempty"`
}

func FormatStableIndex(entries []IndexEntry) string {
	if len(entries) == 0 {
		return ""
	}
	lines := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.Disabled {
			continue
		}
		lines = append(lines, fmt.Sprintf(
			"skill: %s\nversion: %d\ntrigger: %s\nsummary: %s\nsha256: %s",
			entry.Name,
			entry.Version,
			entry.Trigger,
			entry.Summary,
			entry.SHA256,
		))
	}
	return strings.Join(lines, "\n---\n")
}
