//go:build !windows

package pathutil

import "path/filepath"

func canonicalExistingPath(path string) (string, error) { return filepath.EvalSymlinks(path) }
