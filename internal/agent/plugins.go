package agent

import (
	"errors"
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/plugins"
)

func (s *Service) pluginStatuses(settings RuntimeSettings) []plugins.Status {
	if s.pluginManager == nil {
		return []plugins.Status{}
	}
	return s.pluginManager.Statuses(settings.Plugins)
}

func (s *Service) RefreshPlugins() WorkbenchState {
	release, err := s.beginActivity("refreshing plugins")
	if err != nil {
		return s.WorkbenchState()
	}
	defer release()
	if s.pluginManager != nil {
		s.pluginManager.Refresh()
	}
	s.invalidateProviderSession("插件目录已刷新；下一轮会重建工具前缀。")
	return s.workbenchStateLocked()
}

func (s *Service) InstallPlugin(source string) (WorkbenchState, error) {
	release, err := s.beginActivity("installing a plugin")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.pluginManager == nil {
		return s.workbenchStateLocked(), errors.New("插件管理器不可用")
	}
	manifest, err := s.pluginManager.Install(source)
	if err != nil {
		return s.workbenchStateLocked(), err
	}
	settings := s.runtimeSettings.Normalized()
	settings.Plugins = plugins.UpsertSetting(settings.Plugins, plugins.Setting{ID: manifest.ID, Enabled: false})
	if err := saveRuntimeSettings(s.settingsPath, settings); err != nil {
		rollbackErr := s.pluginManager.Uninstall(manifest.ID)
		return s.workbenchStateLocked(), errors.Join(fmt.Errorf("保存插件配置失败: %w", err), rollbackErr)
	}
	s.runtimeSettings = settings
	s.invalidateProviderSession("已安装插件 " + manifest.Name + "；启用并授权后会加入下一轮工具前缀。")
	return s.workbenchStateLocked(), nil
}

func (s *Service) UninstallPlugin(id string) (WorkbenchState, error) {
	release, err := s.beginActivity("uninstalling a plugin")
	if err != nil {
		return s.WorkbenchState(), err
	}
	defer release()
	if s.pluginManager == nil {
		return s.workbenchStateLocked(), errors.New("插件管理器不可用")
	}
	id = strings.ToLower(strings.TrimSpace(id))
	settings := s.runtimeSettings.Normalized()
	settings.Plugins = plugins.RemoveSetting(settings.Plugins, id)
	committed := false
	err = s.pluginManager.UninstallWithCommit(id, func() error {
		if err := saveRuntimeSettings(s.settingsPath, settings); err != nil {
			return fmt.Errorf("保存卸载配置失败: %w", err)
		}
		s.runtimeSettings = settings
		committed = true
		return nil
	})
	if committed {
		s.invalidateProviderSession("插件已卸载；下一轮会重建工具前缀。")
	}
	if err != nil {
		return s.workbenchStateLocked(), err
	}
	return s.workbenchStateLocked(), nil
}

func (s *Service) RevealPlugin(id string) error {
	if s.pluginManager == nil {
		return errors.New("插件管理器不可用")
	}
	path, ok := s.pluginManager.Path(id)
	if !ok {
		return fmt.Errorf("插件 %q 没有可打开的安装目录", id)
	}
	if s.config.RevealFile == nil {
		return errors.New("当前宿主不支持在文件管理器中显示插件")
	}
	return s.config.RevealFile(path)
}
