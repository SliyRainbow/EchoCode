//go:build !windows

package agent

import (
	"path/filepath"
	"strings"
)

func explicitTurnPathGrants(prompt string) []string {
	grants := make([]string, 0, 2)
	seen := map[string]bool{}
	for _, field := range strings.Fields(prompt) {
		candidate := strings.Trim(field, "`'\"[](){}<>,;:")
		if !filepath.IsAbs(candidate) {
			continue
		}
		candidate = filepath.Clean(candidate)
		if !seen[candidate] {
			seen[candidate] = true
			grants = append(grants, candidate)
		}
	}
	return grants
}

func validateExplicitTurnPathGrant(string) error { return nil }
