package pathutil

import (
	"os"
	"path/filepath"
	"strings"
)

// Canonical returns an absolute path with aliases and symbolic links resolved.
// If the final path does not exist, its nearest existing ancestor is resolved.
func Canonical(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	resolved, err := filepath.EvalSymlinks(abs)
	if err == nil {
		return filepath.Clean(resolved), nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}

	missing := make([]string, 0, 4)
	probe := abs
	for {
		parent := filepath.Dir(probe)
		if parent == probe {
			return "", err
		}
		missing = append(missing, filepath.Base(probe))
		probe = parent

		resolved, resolveErr := filepath.EvalSymlinks(probe)
		if resolveErr == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return filepath.Clean(resolved), nil
		}
		if !os.IsNotExist(resolveErr) {
			return "", resolveErr
		}
	}
}

// Within reports whether target is root or is located beneath root after both
// paths have been canonicalized.
func Within(root, target string) (bool, error) {
	canonicalRoot, err := Canonical(root)
	if err != nil {
		return false, err
	}
	canonicalTarget, err := Canonical(target)
	if err != nil {
		return false, err
	}
	relative, err := filepath.Rel(canonicalRoot, canonicalTarget)
	if err != nil {
		return false, err
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))), nil
}
