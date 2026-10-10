package plugins

import (
	"fmt"
	"os"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const maxPluginSnapshotBytes = 64 << 20

type outputFileSnapshot struct {
	path    string
	content string
	existed bool
}

func (t *pluginTool) captureOutputSnapshots(arguments map[string]any) ([]outputFileSnapshot, error) {
	var snapshots []outputFileSnapshot
	seen := make(map[string]bool)
	for _, requirement := range t.descriptor.Paths {
		if requirement.Access != "write" {
			continue
		}
		path, ok := arguments[requirement.Argument].(string)
		if !ok || strings.TrimSpace(path) == "" || seen[path] {
			continue
		}
		seen[path] = true
		resolved, err := t.policy.ResolveWritePath(path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(resolved)
		if os.IsNotExist(err) {
			snapshots = append(snapshots, outputFileSnapshot{path: resolved})
			continue
		}
		if err != nil {
			return nil, err
		}
		// Directory permissions are scopes, not declarations of every file a
		// third-party executable may change. Only explicit files are snapshotted.
		if info.IsDir() {
			continue
		}
		if !info.Mode().IsRegular() || info.Size() > maxPluginSnapshotBytes {
			return nil, fmt.Errorf("插件输出文件无法建立回退快照（上限 64 MiB）: %s", resolved)
		}
		image, err := tools.ReadFileSnapshot(resolved)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, outputFileSnapshot{path: resolved, content: image.Content, existed: true})
	}
	return snapshots, nil
}

func (t *pluginTool) outputSnapshotChanges(before []outputFileSnapshot) ([]tools.FileChange, error) {
	var changes []tools.FileChange
	for _, original := range before {
		path, err := t.policy.ResolveReadPath(original.path)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		deleted := os.IsNotExist(err)
		if err != nil && !deleted {
			return nil, err
		}
		if !deleted && info.IsDir() && !original.existed {
			continue
		}
		if !deleted && (!info.Mode().IsRegular() || info.Size() > maxPluginSnapshotBytes) {
			return nil, fmt.Errorf("插件输出超出可快照的普通文件范围: %s", path)
		}
		after := ""
		if !deleted {
			image, err := tools.ReadFileSnapshot(path)
			if err != nil {
				return nil, err
			}
			after = image.Content
		}
		if (!original.existed && deleted) || (original.existed && !deleted && original.content == after) {
			continue
		}
		changes = append(changes, tools.FileChange{Path: path, Before: original.content, After: after, Existed: original.existed, Deleted: deleted, Encoding: string(tools.EncodingBinary), AfterEncoding: string(tools.EncodingBinary)})
	}
	return changes, nil
}
