//go:build !windows

package tools

import "os"

func replaceFileAtomic(source, destination string) error {
	return os.Rename(source, destination)
}
