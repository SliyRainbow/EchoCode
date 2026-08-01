package skills

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
)

type Loader struct {
	root     string
	source   fs.FS
	diskRoot string
	origin   string
}

func NewLoader(root string) Loader {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	return Loader{root: ".", source: os.DirFS(root), diskRoot: root, origin: "local"}
}

func NewFSLoader(source fs.FS, root string) Loader {
	if strings.TrimSpace(root) == "" {
		root = "."
	}
	return Loader{root: filepath.ToSlash(root), source: source, origin: "bundled"}
}

// WithOrigin labels a loader for the UI without changing its filesystem
// behavior. The label is also useful when a project skill overrides a global
// skill with the same name.
func (l Loader) WithOrigin(origin string) Loader {
	l.origin = strings.TrimSpace(origin)
	if l.origin == "" {
		l.origin = "local"
	}
	return l
}

func (l Loader) Index() ([]IndexEntry, error) {
	entries := []IndexEntry{}
	if l.source == nil {
		return entries, nil
	}
	err := fs.WalkDir(l.source, l.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		data, err := fs.ReadFile(l.source, path)
		if err != nil {
			return err
		}
		entry := l.applyRuntimeMetadata(path, parseSkillData(path, data))
		entry.Source = l.origin
		entry.Path = filepath.ToSlash(path)
		entries = append(entries, entry)
		return nil
	})
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []IndexEntry{}, nil
		}
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

type LoadedSkill struct {
	Name        string
	Version     int
	Trigger     string
	TriggerMode string
	Summary     string
	SHA256      string
	Description string
	Source      string
	Path        string
	FilePath    string
	Content     string
}

func (l Loader) Load(name string) (LoadedSkill, error) {
	name = strings.TrimSpace(name)
	if name == "" || l.source == nil {
		return LoadedSkill{}, fs.ErrNotExist
	}
	var loaded LoadedSkill
	err := fs.WalkDir(l.source, l.root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "SKILL.md" {
			return nil
		}
		data, err := fs.ReadFile(l.source, path)
		if err != nil {
			return err
		}
		entry := l.applyRuntimeMetadata(path, parseSkillData(path, data))
		entry.Source = l.origin
		entry.Path = filepath.ToSlash(path)
		if entry.Name != name {
			return nil
		}
		if len(data) > 256*1024 {
			return errors.New("skill file exceeds 256 KiB")
		}
		loaded = LoadedSkill{
			Name:        entry.Name,
			Version:     entry.Version,
			Trigger:     entry.Trigger,
			TriggerMode: entry.TriggerMode,
			Summary:     entry.Summary,
			SHA256:      entry.SHA256,
			Description: entry.Description,
			Source:      entry.Source,
			Path:        entry.Path,
			FilePath:    l.safeDiskPath(path),
			Content:     string(data),
		}
		return fs.SkipAll
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return LoadedSkill{}, err
	}
	if loaded.Name == "" {
		return LoadedSkill{}, fs.ErrNotExist
	}
	return loaded, nil
}

// safeDiskPath returns a physical path only for a loader rooted on disk and
// only when symlink resolution keeps the file inside that root. Embedded
// skills intentionally return an empty path.
func (l Loader) safeDiskPath(path string) string {
	if strings.TrimSpace(l.diskRoot) == "" {
		return ""
	}
	root, err := filepath.Abs(l.diskRoot)
	if err != nil {
		return ""
	}
	target := filepath.Join(root, filepath.FromSlash(path))
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		return ""
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedTarget)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return ""
	}
	return resolvedTarget
}

func parseSkillData(path string, data []byte) IndexEntry {
	sum := sha256.Sum256(data)
	meta := parseFrontmatter(string(data))
	name := meta["name"]
	description := meta["description"]
	if name == "" {
		name = filepath.Base(filepath.Dir(path))
	}
	return IndexEntry{
		Name:        name,
		Version:     1,
		Trigger:     summarizeTrigger(description),
		TriggerMode: "description",
		Summary:     summarizeSkill(name),
		SHA256:      "sha256:" + hex.EncodeToString(sum[:]),
		Description: description,
	}
}

// applyRuntimeMetadata reads EchoCode-specific activation metadata from a
// product sidecar without adding non-standard keys to SKILL.md frontmatter.
func (l Loader) applyRuntimeMetadata(skillPath string, entry IndexEntry) IndexEntry {
	if l.source == nil {
		return entry
	}
	metadataPath := pathpkg.Join(pathpkg.Dir(skillPath), "agents", "echocode.yaml")
	data, err := fs.ReadFile(l.source, metadataPath)
	if err != nil {
		return entry
	}
	metadata := parseKeyValueData(string(data))
	activation := strings.ToLower(strings.TrimSpace(metadata["activation"]))
	trigger := strings.TrimSpace(metadata["trigger"])
	if activation == "always" {
		entry.Trigger = "always"
		entry.TriggerMode = "always"
		return entry
	}
	if activation == "manual" || strings.EqualFold(trigger, "manual") {
		entry.Trigger = "manual"
		entry.TriggerMode = "manual"
		return entry
	}
	if trigger != "" {
		entry.Trigger = trigger
		entry.TriggerMode = "explicit"
	}
	return entry
}

func parseFrontmatter(content string) map[string]string {
	meta := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	if !scanner.Scan() || scanner.Text() != "---" {
		return meta
	}
	for scanner.Scan() {
		line := scanner.Text()
		if line == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		meta[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"`)
	}
	return meta
}

func parseKeyValueData(content string) map[string]string {
	metadata := map[string]string{}
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		metadata[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return metadata
}

func summarizeTrigger(description string) string {
	if description == "" {
		return "manual"
	}
	parts := strings.Split(description, "。")
	first := strings.TrimSpace(parts[0])
	if first == "" {
		return "manual"
	}
	return first
}

func summarizeSkill(name string) string {
	switch name {
	case "echocode-agent-core":
		return "修改 EchoCode 自身 Agent 内核时使用的架构与验证约束"
	case "echocode-office-artifacts":
		return "创建和修改 DOCX、XLSX、PPTX 办公产物时使用的质量约束"
	default:
		return "按需加载 Skill 正文并生成稳定索引"
	}
}
