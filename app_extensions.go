package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/agent"
	"github.com/SliyRainbow/EchoCode/internal/extensions"
)

const extensionOperationTimeout = 60 * time.Minute

type ExtensionOperationResult struct {
	Catalog extensions.CatalogState `json:"catalog"`
	State   agent.WorkbenchState    `json:"state"`
}

func newExtensionService() *extensions.Service {
	root := extensionInstallRoot()
	return extensions.New(extensions.Options{
		RegistryURL: strings.TrimSpace(os.Getenv("ECHOCODE_EXTENSION_REGISTRY_URL")),
		CachePath:   filepath.Join(root, "catalog-cache.json"),
		InstallRoot: root,
	})
}

func extensionInstallRoot() string {
	if runtime.GOOS == "windows" {
		if local := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); local != "" {
			return filepath.Join(local, "EchoCode", "extensions")
		}
	}
	configDir, err := os.UserConfigDir()
	if err == nil && strings.TrimSpace(configDir) != "" {
		return filepath.Join(configDir, "EchoCode", "extensions")
	}
	return filepath.Join(os.TempDir(), "EchoCode", "extensions")
}

func (a *App) GetExtensionCatalog() (extensions.CatalogState, error) {
	return a.extensionCatalog(false)
}

func (a *App) RefreshExtensionCatalog() (extensions.CatalogState, error) {
	return a.extensionCatalog(true)
}

func (a *App) extensionCatalog(refresh bool) (extensions.CatalogState, error) {
	if a.extensions == nil {
		return extensions.CatalogState{}, errors.New("扩展服务不可用")
	}
	ctx, cancel := context.WithTimeout(appContext(a.ctx), 45*time.Second)
	defer cancel()
	return a.extensions.Catalog(ctx, refresh)
}

func (a *App) InstallExtension(id string) (ExtensionOperationResult, error) {
	if a.extensions == nil {
		return ExtensionOperationResult{}, errors.New("扩展服务不可用")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return ExtensionOperationResult{}, errors.New("扩展 ID 不能为空")
	}
	_, hadPrevious, lookupErr := a.extensions.LookupInstalled(id)
	if lookupErr != nil {
		return ExtensionOperationResult{}, lookupErr
	}
	settings := a.runtimeSettingsSnapshot()
	if !hadPrevious {
		if conflict, ok := findMCPServerSetting(settings.MCP.Servers, id); ok && strings.TrimSpace(conflict.Command) != "" {
			return ExtensionOperationResult{}, fmt.Errorf("已存在同名 MCP 配置 %s；请先在 MCP 设置中重命名或删除", id)
		}
	}

	ctx, cancel := context.WithTimeout(appContext(a.ctx), extensionOperationTimeout)
	defer cancel()
	state := a.service.WorkbenchState()
	_, err := a.extensions.InstallAndActivate(ctx, id, func(result extensions.InstallResult) error {
		if result.Manifest.Type != "mcp" || result.Manifest.MCP == nil {
			return nil
		}
		// Download can take minutes. Merge into current settings rather than
		// overwriting edits or the selected workspace from the old snapshot.
		settings := a.runtimeSettingsSnapshot()
		next, settingErr := extensionMCPServerSetting(result, settings.WorkspaceRoot)
		if settingErr != nil {
			return settingErr
		}
		settings.MCP.Servers = upsertExtensionMCPServer(settings.MCP.Servers, next)
		var saveErr error
		state, saveErr = a.saveExtensionMCPSettings(settings)
		if saveErr != nil {
			return fmt.Errorf("MCP 配置保存失败，扩展安装已取消: %w", saveErr)
		}
		return nil
	})
	if err != nil {
		return ExtensionOperationResult{}, err
	}
	catalog, err := a.extensions.Catalog(context.Background(), false)
	return ExtensionOperationResult{Catalog: catalog, State: state}, err
}

func (a *App) UninstallExtension(id string) (ExtensionOperationResult, error) {
	if a.extensions == nil {
		return ExtensionOperationResult{}, errors.New("扩展服务不可用")
	}
	id = strings.TrimSpace(id)
	installed, ok, lookupErr := a.extensions.LookupInstalled(id)
	if lookupErr != nil {
		return ExtensionOperationResult{}, lookupErr
	}
	if !ok {
		catalog, err := a.extensions.Catalog(context.Background(), false)
		return ExtensionOperationResult{Catalog: catalog, State: a.service.WorkbenchState()}, err
	}
	settings := a.runtimeSettingsSnapshot()
	originalSettings := settings
	state := a.service.WorkbenchState()
	if installed.Type == "mcp" {
		settings.MCP.Servers = removeOwnedExtensionMCPServer(settings.MCP.Servers, installed)
		var err error
		state, err = a.saveExtensionMCPSettings(settings)
		if err != nil {
			return ExtensionOperationResult{}, fmt.Errorf("移除 MCP 配置失败: %w", err)
		}
	}
	if err := a.extensions.Uninstall(id); err != nil {
		if installed.Type == "mcp" {
			// The extension service restores its files on failure. Restore only
			// this MCP entry, keeping any unrelated settings changed meanwhile.
			if original, ok := findMCPServerSetting(originalSettings.MCP.Servers, id); ok {
				settings := a.runtimeSettingsSnapshot()
				settings.MCP.Servers = upsertExtensionMCPServer(settings.MCP.Servers, original)
				if _, restoreErr := a.saveExtensionMCPSettings(settings); restoreErr != nil {
					return ExtensionOperationResult{}, errors.Join(err, fmt.Errorf("恢复 MCP 配置失败: %w", restoreErr))
				}
			}
		}
		return ExtensionOperationResult{}, err
	}
	catalog, err := a.extensions.Catalog(context.Background(), false)
	return ExtensionOperationResult{Catalog: catalog, State: state}, err
}

func (a *App) RevealExtension(id string) error {
	if a.extensions == nil {
		return errors.New("扩展服务不可用")
	}
	installed, ok := a.extensions.Installed(strings.TrimSpace(id))
	if !ok {
		return fmt.Errorf("扩展尚未安装：%s", id)
	}
	return openDesktopFile(installed.InstallDir)
}

// Extension transactions only change MCP configuration. Do not run unrelated
// browser reconfiguration or data-clearing actions after this durable commit.
func (a *App) saveExtensionMCPSettings(settings agent.RuntimeSettings) (agent.WorkbenchState, error) {
	state, err := a.service.SaveRuntimeSettings(settings)
	if err != nil {
		return state, err
	}
	a.setRuntimeSettings(state.RuntimeSettings)
	ctx, cancel := context.WithTimeout(appContext(a.ctx), 20*time.Second)
	defer cancel()
	return a.service.ConfigureMCP(ctx, ""), nil
}

func (a *App) RunExtensionProjectAction(id, actionID string) (extensions.ActionResult, error) {
	if a.extensions == nil {
		return extensions.ActionResult{}, errors.New("扩展服务不可用")
	}
	workspaceRoot := strings.TrimSpace(a.runtimeSettingsSnapshot().WorkspaceRoot)
	if workspaceRoot == "" {
		return extensions.ActionResult{}, errors.New("请先打开或添加一个项目")
	}
	ctx, cancel := context.WithTimeout(appContext(a.ctx), extensionOperationTimeout)
	defer cancel()
	return a.extensions.RunProjectAction(ctx, strings.TrimSpace(id), strings.TrimSpace(actionID), workspaceRoot)
}

func extensionMCPServerSetting(result extensions.InstallResult, workspaceRoot string) (agent.MCPServerSetting, error) {
	config := result.Manifest.MCP
	if config == nil {
		return agent.MCPServerSetting{}, errors.New("扩展缺少 MCP 配置")
	}
	resolve := func(value string) string {
		replacer := strings.NewReplacer(
			"${artifactExecutable}", result.Package.Executable,
			"${installDir}", result.Package.InstallDir,
			"${workspaceRoot}", strings.TrimSpace(workspaceRoot),
		)
		return replacer.Replace(value)
	}
	command := resolve(config.Command)
	if strings.TrimSpace(command) == "" {
		command = result.Package.Executable
	}
	if !filepath.IsAbs(command) {
		return agent.MCPServerSetting{}, fmt.Errorf("扩展 MCP 启动命令不是绝对路径：%s", command)
	}
	args := make([]string, len(config.Args))
	for index, value := range config.Args {
		args[index] = resolve(value)
	}
	env := make([]agent.KeyValue, 0, len(config.Env))
	for _, item := range config.Env {
		if key := strings.TrimSpace(item.Key); key != "" {
			env = append(env, agent.KeyValue{Key: key, Value: resolve(item.Value)})
		}
	}
	return agent.MCPServerSetting{
		ID:               result.Package.ID,
		Name:             result.Package.Name,
		Transport:        config.Transport,
		Command:          command,
		Args:             args,
		Env:              env,
		WorkingDirectory: resolve(config.WorkingDirectory),
		Enabled:          true,
		ToolResultPolicy: config.ToolResultPolicy,
	}, nil
}

func upsertExtensionMCPServer(servers []agent.MCPServerSetting, next agent.MCPServerSetting) []agent.MCPServerSetting {
	result := make([]agent.MCPServerSetting, 0, len(servers)+1)
	replaced := false
	for _, current := range servers {
		if current.ID != next.ID {
			result = append(result, current)
			continue
		}
		if !replaced {
			next.Enabled = current.Enabled
			if strings.TrimSpace(current.ToolResultPolicy) != "" {
				next.ToolResultPolicy = current.ToolResultPolicy
			}
			next.SchemaSnapshotHash = current.SchemaSnapshotHash
			next.LastSnapshotAt = current.LastSnapshotAt
			result = append(result, next)
			replaced = true
		}
	}
	if !replaced {
		result = append(result, next)
	}
	return result
}

func removeOwnedExtensionMCPServer(servers []agent.MCPServerSetting, installed extensions.InstalledPackage) []agent.MCPServerSetting {
	result := make([]agent.MCPServerSetting, 0, len(servers))
	for _, server := range servers {
		if server.ID == installed.ID && sameExtensionPath(server.Command, installed.Executable) {
			continue
		}
		result = append(result, server)
	}
	return result
}

func findMCPServerSetting(servers []agent.MCPServerSetting, id string) (agent.MCPServerSetting, bool) {
	for _, server := range servers {
		if server.ID == id {
			return server, true
		}
	}
	return agent.MCPServerSetting{}, false
}

func sameExtensionPath(left, right string) bool {
	left = filepath.Clean(strings.Trim(strings.TrimSpace(left), "\"'"))
	right = filepath.Clean(strings.Trim(strings.TrimSpace(right), "\"'"))
	if runtime.GOOS == "windows" {
		return strings.EqualFold(left, right)
	}
	return left == right
}

func appContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}
