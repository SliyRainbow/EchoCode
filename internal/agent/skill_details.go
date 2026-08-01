package agent

import (
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/skills"
)

// SkillDetail is the bounded source view exposed to the settings UI. A skill
// is addressed by its indexed name rather than an arbitrary path, so the UI
// cannot use this endpoint to escape the configured skill roots.
type SkillDetail struct {
	Name        string `json:"name"`
	Version     int    `json:"version"`
	Trigger     string `json:"trigger"`
	TriggerMode string `json:"triggerMode,omitempty"`
	Summary     string `json:"summary"`
	SHA256      string `json:"sha256"`
	Description string `json:"description"`
	Disabled    bool   `json:"disabled"`
	Source      string `json:"source,omitempty"`
	Path        string `json:"path,omitempty"`
	Content     string `json:"content"`
	CanOpen     bool   `json:"canOpen"`
}

// ReadSkillDetail reads the selected skill from the same source that wins the
// runtime's duplicate-name resolution (project over local over bundled).
func (s *Service) ReadSkillDetail(name string) (SkillDetail, error) {
	loaded, disabled, _, _, err := s.loadSkillSource(name)
	if err != nil {
		return SkillDetail{}, err
	}
	return SkillDetail{
		Name:        loaded.Name,
		Version:     loaded.Version,
		Trigger:     loaded.Trigger,
		TriggerMode: loaded.TriggerMode,
		Summary:     loaded.Summary,
		SHA256:      loaded.SHA256,
		Description: loaded.Description,
		Disabled:    disabled,
		Source:      loaded.Source,
		Path:        loaded.Path,
		Content:     loaded.Content,
		CanOpen:     strings.TrimSpace(loaded.FilePath) != "",
	}, nil
}

// OpenSkillFile opens a disk-backed skill through the host callback. Bundled
// skills are intentionally view-only because their source lives in the
// executable's embedded filesystem.
func (s *Service) OpenSkillFile(name string) error {
	loaded, _, openFile, _, err := s.loadSkillSource(name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(loaded.FilePath) == "" {
		return fmt.Errorf("该 Skill 为内置资源，只能在 EchoCode 中查看")
	}
	if openFile == nil {
		return fmt.Errorf("当前桌面环境不支持打开 Skill 文件")
	}
	return openFile(loaded.FilePath)
}

// RevealSkillFile selects a disk-backed skill in the host file manager.
func (s *Service) RevealSkillFile(name string) error {
	loaded, _, _, revealFile, err := s.loadSkillSource(name)
	if err != nil {
		return err
	}
	if strings.TrimSpace(loaded.FilePath) == "" {
		return fmt.Errorf("该 Skill 为内置资源，没有可定位的磁盘文件")
	}
	if revealFile == nil {
		return fmt.Errorf("当前桌面环境不支持定位 Skill 文件")
	}
	return revealFile(loaded.FilePath)
}

func (s *Service) loadSkillSource(name string) (skills.LoadedSkill, bool, func(string) error, func(string) error, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 128 {
		return skills.LoadedSkill{}, false, nil, nil, fmt.Errorf("Skill 名称无效")
	}

	s.stateMu.RLock()
	loaders := s.skillLoaders()
	disabled := false
	for _, disabledName := range s.runtimeSettings.Skills.Disabled {
		if disabledName == name {
			disabled = true
			break
		}
	}
	openFile := s.config.OpenFile
	revealFile := s.config.RevealFile
	s.stateMu.RUnlock()

	for index := len(loaders) - 1; index >= 0; index-- {
		loaded, err := loaders[index].Load(name)
		if err == nil {
			return loaded, disabled, openFile, revealFile, nil
		}
	}
	return skills.LoadedSkill{}, false, nil, nil, fmt.Errorf("未找到 Skill：%s", name)
}
