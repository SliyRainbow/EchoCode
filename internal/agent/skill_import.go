package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/SliyRainbow/EchoCode/internal/skills"
)

const maxImportedSkillBytes = 256 * 1024

type SkillImportResult struct {
	Name  string         `json:"name"`
	Skill SkillDetail    `json:"skill"`
	State WorkbenchState `json:"state"`
}

// ImportSkillMarkdown installs a user-authored Markdown file as a durable
// local Skill. User Skills live outside the executable so application updates
// cannot overwrite them.
func (s *Service) ImportSkillMarkdown(fileName, content string) (SkillImportResult, error) {
	release, err := s.beginActivity("importing a Skill")
	if err != nil {
		return SkillImportResult{}, err
	}
	defer release()

	fileName = filepath.Base(strings.TrimSpace(fileName))
	extension := strings.ToLower(filepath.Ext(fileName))
	if extension != ".md" && extension != ".markdown" {
		return SkillImportResult{}, fmt.Errorf("请选择 .md 或 .markdown 文件")
	}
	content = strings.TrimPrefix(content, "\ufeff")
	if strings.TrimSpace(content) == "" {
		return SkillImportResult{}, fmt.Errorf("Markdown 文件为空")
	}
	if len([]byte(content)) > maxImportedSkillBytes {
		return SkillImportResult{}, fmt.Errorf("Skill 文件不能超过 256 KiB")
	}
	if !utf8.ValidString(content) || strings.ContainsRune(content, '\x00') {
		return SkillImportResult{}, fmt.Errorf("Skill 文件必须是有效的 UTF-8 文本")
	}

	metadata, _ := importedSkillFrontmatter(content)
	requestedName := metadata["name"]
	if requestedName == "" {
		requestedName = strings.TrimSuffix(fileName, filepath.Ext(fileName))
	}
	name := normalizeImportedSkillName(requestedName)
	if name == "" {
		sum := sha256.Sum256([]byte(content))
		name = "user-skill-" + hex.EncodeToString(sum[:4])
	}
	description := strings.TrimSpace(metadata["description"])
	if description == "" {
		description = importedSkillDescription(content, fileName)
	}

	existing := map[string]bool{}
	for _, entry := range s.loadSkillsIndex() {
		existing[entry.Name] = true
	}
	name = uniqueImportedSkillName(name, existing)
	content = normalizeImportedSkillFrontmatter(content, name, description)

	root := strings.TrimSpace(s.config.UserSkillsDir)
	if root == "" {
		root = strings.TrimSpace(s.config.SkillsDir)
	}
	if root == "" {
		return SkillImportResult{}, fmt.Errorf("用户 Skills 目录未配置")
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return SkillImportResult{}, fmt.Errorf("解析用户 Skills 目录失败: %w", err)
	}
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return SkillImportResult{}, fmt.Errorf("创建 Skill 目录失败: %w", err)
	}
	if err := verifyImportedSkillDirectory(root, directory); err != nil {
		return SkillImportResult{}, err
	}
	metadataDirectory := filepath.Join(directory, "agents")
	if err := os.MkdirAll(metadataDirectory, 0o755); err != nil {
		return SkillImportResult{}, fmt.Errorf("创建 Skill 激活配置目录失败: %w", err)
	}
	if err := verifyImportedSkillDirectory(root, metadataDirectory); err != nil {
		return SkillImportResult{}, err
	}
	if err := writeImportedSkillAtomically(filepath.Join(metadataDirectory, "echocode.yaml"), []byte("activation: always\n")); err != nil {
		return SkillImportResult{}, fmt.Errorf("写入 Skill 激活配置失败: %w", err)
	}
	target := filepath.Join(directory, "SKILL.md")
	if err := writeImportedSkillAtomically(target, []byte(content)); err != nil {
		return SkillImportResult{}, err
	}

	loaded, err := skills.NewLoader(root).WithOrigin("user").Load(name)
	if err != nil {
		return SkillImportResult{}, fmt.Errorf("重新加载导入的 Skill 失败: %w", err)
	}
	s.invalidateProviderSession("用户 Skills 已更新；下一轮会重建 Skills 索引。")
	detail := SkillDetail{
		Name: loaded.Name, Version: loaded.Version, Trigger: loaded.Trigger, TriggerMode: loaded.TriggerMode,
		Summary: loaded.Summary, SHA256: loaded.SHA256, Description: loaded.Description,
		Source: loaded.Source, Path: loaded.Path, Content: loaded.Content, CanOpen: loaded.FilePath != "",
	}
	return SkillImportResult{Name: name, Skill: detail, State: s.workbenchStateLocked()}, nil
}

func verifyImportedSkillDirectory(root, directory string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("验证用户 Skills 目录失败: %w", err)
	}
	resolvedDirectory, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return fmt.Errorf("验证 Skill 目录失败: %w", err)
	}
	relative, err := filepath.Rel(resolvedRoot, resolvedDirectory)
	if err != nil || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return fmt.Errorf("Skill 目录超出用户 Skills 根目录")
	}
	return nil
}

func writeImportedSkillAtomically(path string, data []byte) error {
	directory := filepath.Dir(path)
	temporary, err := os.CreateTemp(directory, ".SKILL-*.tmp")
	if err != nil {
		return fmt.Errorf("创建 Skill 临时文件失败: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("写入 Skill 失败: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("同步 Skill 文件失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭 Skill 文件失败: %w", err)
	}
	if err := os.Rename(temporaryPath, path); err != nil {
		return fmt.Errorf("安装 Skill 失败: %w", err)
	}
	return nil
}

func importedSkillFrontmatter(content string) (map[string]string, int) {
	metadata := map[string]string{}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return metadata, 0
	}
	for index := 1; index < len(lines); index++ {
		line := lines[index]
		if strings.TrimSpace(line) == "---" {
			return metadata, index
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		metadata[strings.TrimSpace(key)] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	return map[string]string{}, 0
}

func normalizeImportedSkillFrontmatter(content, name, description string) string {
	normalized := strings.ReplaceAll(strings.TrimPrefix(content, "\ufeff"), "\r\n", "\n")
	_, closing := importedSkillFrontmatter(normalized)
	description = strings.Join(strings.Fields(description), " ")
	description = strings.ReplaceAll(description, `"`, "'")
	if len([]rune(description)) > 240 {
		description = string([]rune(description)[:240])
	}
	nameLine := "name: " + name
	descriptionLine := `description: "` + description + `"`
	if closing == 0 {
		return strings.Join([]string{"---", nameLine, descriptionLine, "---", "", strings.TrimSpace(normalized), ""}, "\n")
	}
	lines := strings.Split(normalized, "\n")
	frontmatter := make([]string, 0, closing+2)
	frontmatter = append(frontmatter, "---")
	nameWritten := false
	descriptionWritten := false
	for _, line := range lines[1:closing] {
		key, _, ok := strings.Cut(line, ":")
		switch strings.TrimSpace(key) {
		case "name":
			frontmatter = append(frontmatter, nameLine)
			nameWritten = true
		case "description":
			frontmatter = append(frontmatter, descriptionLine)
			descriptionWritten = true
		default:
			if ok || strings.TrimSpace(line) != "" {
				frontmatter = append(frontmatter, line)
			}
		}
	}
	if !nameWritten {
		frontmatter = append(frontmatter, nameLine)
	}
	if !descriptionWritten {
		frontmatter = append(frontmatter, descriptionLine)
	}
	frontmatter = append(frontmatter, "---", "")
	body := strings.TrimSpace(strings.Join(lines[closing+1:], "\n"))
	return strings.Join(frontmatter, "\n") + body + "\n"
}

func normalizeImportedSkillName(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	separator := false
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsDigit(character) {
			if separator && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			builder.WriteRune(character)
			separator = false
			continue
		}
		if character == '-' || character == '_' || unicode.IsSpace(character) {
			separator = builder.Len() > 0
		}
	}
	runes := []rune(strings.Trim(builder.String(), "-"))
	if len(runes) > 64 {
		runes = runes[:64]
	}
	name := strings.Trim(string(runes), "-")
	switch strings.ToUpper(name) {
	case "CON", "PRN", "AUX", "NUL", "COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9", "LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9":
		return "skill-" + name
	default:
		return name
	}
}

func uniqueImportedSkillName(name string, existing map[string]bool) string {
	if !existing[name] {
		return name
	}
	for suffix := 2; suffix < 10_000; suffix++ {
		candidate := fmt.Sprintf("%s-%d", name, suffix)
		if !existing[candidate] {
			return candidate
		}
	}
	return name + "-imported"
}

func importedSkillDescription(content, fileName string) string {
	_, closing := importedSkillFrontmatter(content)
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if closing > 0 && closing+1 < len(lines) {
		lines = lines[closing+1:]
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "```") || strings.HasPrefix(line, "<!--") {
			continue
		}
		line = strings.TrimSpace(strings.TrimLeft(line, "#>*-0123456789. `"))
		if line != "" {
			return line
		}
	}
	return "用户导入的 Skill：" + fileName
}
