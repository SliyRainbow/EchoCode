//go:build !windows

package extensions

import "os"

func replaceFileAtomic(source, destination string) error {
	return os.Rename(source, destination)
}
