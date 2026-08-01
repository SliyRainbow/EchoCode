//go:build windows

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var windowsDriveMentionPattern = regexp.MustCompile(`(?i)([a-z])(?:盘符|盘)`)

func explicitTurnPathGrants(prompt string) []string {
	grants := make([]string, 0, 4)
	seen := map[string]bool{}
	add := func(candidate string) {
		candidate = strings.TrimSpace(strings.Trim(candidate, "`'\"[](){}<>"))
		candidate = strings.TrimRight(candidate, ".,;:!?，。；：！？")
		if len(candidate) < 3 || candidate[1] != ':' || (candidate[2] != '\\' && candidate[2] != '/') {
			return
		}
		candidate = filepath.Clean(candidate)
		key := strings.ToLower(candidate)
		if !seen[key] {
			seen[key] = true
			grants = append(grants, candidate)
		}
	}

	runes := []rune(prompt)
	for index := 0; index+2 < len(runes); index++ {
		if !isASCIILetter(runes[index]) || runes[index+1] != ':' || (runes[index+2] != '\\' && runes[index+2] != '/') {
			continue
		}
		start := index
		end := index + 3
		quote := rune(0)
		if start > 0 && strings.ContainsRune("`'\"", runes[start-1]) {
			quote = runes[start-1]
		}
		for end < len(runes) {
			current := runes[end]
			if quote != 0 {
				if current == quote || current == '\r' || current == '\n' {
					break
				}
			} else if unicode.IsSpace(current) || strings.ContainsRune("`'\"<>|?*，。；：！？!?,;", current) {
				break
			}
			end++
		}
		add(string(runes[start:end]))
		index = end - 1
	}

	for _, match := range windowsDriveMentionPattern.FindAllStringSubmatch(prompt, -1) {
		if len(match) == 2 {
			add(strings.ToUpper(match[1]) + `:\`)
		}
	}
	return grants
}

func validateExplicitTurnPathGrant(path string) error {
	cleaned := filepath.Clean(path)
	volume := filepath.VolumeName(cleaned)
	systemRoot := filepath.Clean(os.Getenv("SystemRoot"))
	systemVolume := filepath.VolumeName(systemRoot)
	if systemVolume != "" && strings.EqualFold(volume, systemVolume) && strings.EqualFold(cleaned, systemVolume+`\`) {
		return fmt.Errorf("temporary path access refuses the system drive root %s; select a specific user directory or enable unrestricted filesystem access", cleaned)
	}
	protected := []string{
		systemRoot,
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("ProgramData"),
		filepath.Join(systemVolume+`\`, "$Recycle.Bin"),
		filepath.Join(systemVolume+`\`, "System Volume Information"),
	}
	for _, root := range protected {
		root = strings.TrimSpace(root)
		if root != "" && windowsPathWithin(cleaned, filepath.Clean(root)) {
			return fmt.Errorf("temporary path access refuses protected system location %s; enable unrestricted filesystem access only when this is intentional", cleaned)
		}
	}
	return nil
}

func windowsPathWithin(target, root string) bool {
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, `..\`))
}

func isASCIILetter(value rune) bool {
	return value >= 'a' && value <= 'z' || value >= 'A' && value <= 'Z'
}
