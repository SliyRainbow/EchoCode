//go:build windows

package pathutil

import (
	"fmt"
	"os"
	"strings"

	"golang.org/x/sys/windows"
)

// Resolve by handle so junctions and other Windows name-surrogate reparse
// points receive the same boundary checks as symbolic links and 8.3 aliases.
func canonicalExistingPath(path string) (string, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(name, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		if os.IsNotExist(err) {
			if attributes, attrErr := windows.GetFileAttributes(name); attrErr == nil && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
				// A dangling junction must not be mistaken for an ordinary
				// missing directory and reattached beneath its lexical parent.
				return "", fmt.Errorf("cannot resolve Windows reparse point %q: %v", path, err)
			}
		}
		return "", err
	}
	defer windows.CloseHandle(handle)
	buffer := make([]uint16, 512)
	for {
		n, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if n >= uint32(len(buffer)) {
			if n > 32768 {
				return "", fmt.Errorf("resolved Windows path exceeds supported length")
			}
			buffer = make([]uint16, n+1)
			continue
		}
		resolved := windows.UTF16ToString(buffer[:n])
		if strings.HasPrefix(resolved, `\\?\UNC\`) {
			return `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`), nil
		}
		if len(resolved) >= 7 && strings.HasPrefix(resolved, `\\?\`) && resolved[5] == ':' {
			return resolved[4:], nil
		}
		return resolved, nil
	}
}
