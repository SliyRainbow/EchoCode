package plugins

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/SliyRainbow/EchoCode/internal/pathutil"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

const (
	maxPluginInstallFiles = 10_000
	maxPluginInstallBytes = 512 * 1024 * 1024
)

type Manager struct {
	mu         sync.RWMutex
	root       string
	appVersion string
	records    map[string]record
	order      []string
}

func (m *Manager) AppVersion() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.appVersion
}

type record struct {
	manifest Manifest
	dir      string
	source   string
	err      error
}

func NewManager(root, appVersion string) *Manager {
	manager := &Manager{
		root:       strings.TrimSpace(root),
		appVersion: strings.TrimSpace(appVersion),
		records:    map[string]record{},
	}
	manager.Refresh()
	return manager
}

func (m *Manager) Root() string {
	if m == nil {
		return ""
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.root
}

func (m *Manager) Refresh() {
	if m == nil {
		return
	}
	records := map[string]record{}
	for _, manifest := range builtinManifests() {
		normalizeManifest(&manifest)
		records[manifest.ID] = record{manifest: manifest, source: "builtin"}
	}

	root := strings.TrimSpace(m.root)
	if root != "" {
		entries, err := os.ReadDir(root)
		if err == nil {
			sort.Slice(entries, func(i, j int) bool { return strings.ToLower(entries[i].Name()) < strings.ToLower(entries[j].Name()) })
			for _, entry := range entries {
				if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				dir := filepath.Join(root, entry.Name())
				data, readErr := os.ReadFile(filepath.Join(dir, ManifestFileName))
				if readErr != nil {
					continue
				}
				manifest, decodeErr := DecodeManifest(data, false)
				if decodeErr != nil {
					id := safeIdentifier(strings.ToLower(entry.Name()))
					if id == "unnamed" {
						continue
					}
					records[id] = record{
						manifest: Manifest{ID: id, Name: entry.Name(), Version: "?"},
						dir:      dir,
						source:   "installed",
						err:      decodeErr,
					}
					continue
				}
				if _, builtin := records[manifest.ID]; builtin {
					continue
				}
				records[manifest.ID] = record{manifest: manifest, dir: dir, source: "installed"}
			}
		}
	}
	markToolNameConflicts(records)
	order := make([]string, 0, len(records))
	for id := range records {
		order = append(order, id)
	}
	sort.Strings(order)
	m.mu.Lock()
	m.records = records
	m.order = order
	m.mu.Unlock()
}

func (m *Manager) Statuses(settings Settings) []Status {
	if m == nil {
		return []Status{}
	}
	settings = NormalizeSettings(settings)
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]Status, 0, len(m.order))
	for _, id := range m.order {
		record := m.records[id]
		entry, configured := SettingFor(settings, id)
		status := statusForRecord(record, entry, configured)
		result = append(result, status)
	}
	return result
}

func statusForRecord(record record, setting Setting, configured bool) Status {
	manifest := record.manifest
	status := Status{
		ID:                 manifest.ID,
		Name:               manifest.Name,
		Version:            manifest.Version,
		Description:        manifest.Description,
		Author:             manifest.Author,
		Homepage:           manifest.Homepage,
		Source:             record.source,
		Path:               record.dir,
		ToolCount:          len(manifest.Tools),
		Permissions:        manifest.Permissions,
		GrantedPermissions: setting.Permissions,
		CanUninstall:       record.source == "installed",
		ManifestSchema:     manifest.SchemaVersion,
		ProtocolVersion:    ProtocolVersion,
		Tools:              make([]ToolStatus, 0, len(manifest.Tools)),
	}
	for _, descriptor := range manifest.Tools {
		status.Tools = append(status.Tools, ToolStatus{
			Name:        descriptor.Name,
			FullName:    namespacedToolName(manifest.ID, descriptor.Name),
			Description: descriptor.Description,
			ReadOnly:    descriptor.ReadOnly,
			Permissions: descriptor.Permissions,
		})
	}
	if record.err != nil {
		status.State = "error"
		status.Message = record.err.Error()
		return status
	}
	if !configured || !setting.Enabled {
		status.State = "disabled"
		if configured {
			status.Message = "插件已停用"
		} else {
			status.Message = "插件尚未授权；请在设置中启用并授予所需权限"
		}
		return status
	}
	for _, tool := range manifest.Tools {
		if grantContains(setting.Permissions, tool.Permissions) {
			status.AvailableToolCount++
		}
	}
	if status.AvailableToolCount == 0 {
		status.State = "unavailable"
		status.Message = "插件已启用，但尚未授予任何工具所需的权限"
		return status
	}
	if record.source == "installed" {
		if _, err := resolveRuntimeCommand(record); err != nil {
			status.State = "error"
			status.Message = err.Error()
			return status
		}
	}
	status.State = "ready"
	if record.source == "builtin" {
		status.Message = fmt.Sprintf("内置产物引擎已就绪，可用 %d/%d 个工具；不依赖本机 Office", status.AvailableToolCount, status.ToolCount)
	} else {
		status.Message = fmt.Sprintf("外部进程插件已就绪，可用 %d/%d 个工具", status.AvailableToolCount, status.ToolCount)
	}
	return status
}

func (m *Manager) Tools(settings Settings, policy tools.SandboxPolicy, readOnlyOnly bool) []tools.Tool {
	if m == nil {
		return nil
	}
	settings = NormalizeSettings(settings)
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]tools.Tool, 0)
	for _, id := range m.order {
		record := m.records[id]
		setting, configured := SettingFor(settings, id)
		status := statusForRecord(record, setting, configured)
		if status.State != "ready" {
			continue
		}
		for _, descriptor := range record.manifest.Tools {
			if readOnlyOnly && !descriptor.ReadOnly {
				continue
			}
			if !grantContains(setting.Permissions, descriptor.Permissions) {
				continue
			}
			if policy.TaskScopeEnabled && !pluginToolDeclaresScopedPaths(descriptor) {
				continue
			}
			result = append(result, &pluginTool{
				manager:    m,
				record:     record,
				setting:    setting,
				descriptor: descriptor,
				policy:     policy,
				limits: runnerLimits{
					maxExecutionSeconds: settings.MaxExecutionSeconds,
					maxOutputBytes:      settings.MaxOutputBytes,
				},
			})
		}
	}
	return result
}

// pluginToolDeclaresScopedPaths allows network/compute-only plugins through,
// but requires every requested filesystem capability to be represented by a
// manifest path argument. pluginTool.resolvePaths then applies the same
// SandboxPolicy used by built-in tools before the plugin process starts.
func pluginToolDeclaresScopedPaths(descriptor ToolManifest) bool {
	if !descriptor.Permissions.FileRead && !descriptor.Permissions.FileWrite {
		return true
	}
	declaresRead := false
	declaresWrite := false
	for _, requirement := range descriptor.Paths {
		switch requirement.Access {
		case "read":
			declaresRead = true
		case "write":
			declaresWrite = true
		}
	}
	return (!descriptor.Permissions.FileRead || declaresRead) &&
		(!descriptor.Permissions.FileWrite || declaresWrite)
}

func (m *Manager) Install(source string) (Manifest, error) {
	if m == nil {
		return Manifest{}, errors.New("plugin manager is unavailable")
	}
	source = strings.TrimSpace(source)
	if source == "" {
		return Manifest{}, errors.New("plugin source directory is required")
	}
	info, err := os.Stat(source)
	if err != nil {
		return Manifest{}, fmt.Errorf("inspect plugin source: %w", err)
	}
	if !info.IsDir() {
		if !strings.EqualFold(filepath.Base(source), ManifestFileName) {
			return Manifest{}, fmt.Errorf("select a plugin directory or %s", ManifestFileName)
		}
		source = filepath.Dir(source)
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return Manifest{}, err
	}
	data, err := os.ReadFile(filepath.Join(source, ManifestFileName))
	if err != nil {
		return Manifest{}, fmt.Errorf("read plugin manifest: %w", err)
	}
	manifest, err := DecodeManifest(data, false)
	if err != nil {
		return Manifest{}, err
	}
	m.mu.RLock()
	existing, exists := m.records[manifest.ID]
	m.mu.RUnlock()
	if exists && existing.source == "builtin" {
		return Manifest{}, fmt.Errorf("plugin id %q is reserved by EchoCode", manifest.ID)
	}
	if err := m.checkToolNameConflicts(manifest); err != nil {
		return Manifest{}, err
	}
	root := strings.TrimSpace(m.Root())
	if root == "" {
		return Manifest{}, errors.New("plugin installation directory is not configured")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Manifest{}, err
	}
	destination := filepath.Join(root, manifest.ID)
	if _, err := os.Stat(destination); err == nil {
		return Manifest{}, fmt.Errorf("plugin %q is already installed", manifest.ID)
	} else if !os.IsNotExist(err) {
		return Manifest{}, err
	}
	if within, withinErr := pathutil.Within(source, destination); withinErr == nil && within {
		return Manifest{}, errors.New("plugin installation destination cannot be inside its source directory")
	}
	stage := filepath.Join(root, fmt.Sprintf(".install-%s-%d", manifest.ID, time.Now().UnixNano()))
	if err := copyPluginDirectory(source, stage); err != nil {
		_ = os.RemoveAll(stage)
		return Manifest{}, err
	}
	if err := os.Rename(stage, destination); err != nil {
		_ = os.RemoveAll(stage)
		return Manifest{}, fmt.Errorf("activate plugin: %w", err)
	}
	m.Refresh()
	return manifest, nil
}

func (m *Manager) Uninstall(id string) error {
	if m == nil {
		return errors.New("plugin manager is unavailable")
	}
	id = strings.ToLower(strings.TrimSpace(id))
	m.mu.RLock()
	record, ok := m.records[id]
	root := m.root
	m.mu.RUnlock()
	if ok && record.source == "builtin" {
		return errors.New("built-in plugins cannot be uninstalled")
	}
	if !ok || record.source != "installed" || strings.TrimSpace(record.dir) == "" {
		return fmt.Errorf("installed plugin %q was not found", id)
	}
	within, err := pathutil.Within(root, record.dir)
	if err != nil || !within || filepath.Clean(root) == filepath.Clean(record.dir) {
		return errors.New("refusing to remove a plugin outside the plugin directory")
	}
	if err := os.RemoveAll(record.dir); err != nil {
		return fmt.Errorf("uninstall plugin: %w", err)
	}
	m.Refresh()
	return nil
}

func (m *Manager) Path(id string) (string, bool) {
	if m == nil {
		return "", false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	record, ok := m.records[strings.ToLower(strings.TrimSpace(id))]
	if !ok || record.source != "installed" || record.dir == "" {
		return "", false
	}
	return record.dir, true
}

func copyPluginDirectory(source, destination string) error {
	files := 0
	var total int64
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		if relative == "." {
			return os.MkdirAll(destination, 0o700)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("plugin packages cannot contain symbolic links: %s", relative)
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("plugin package contains a non-regular file: %s", relative)
		}
		files++
		total += info.Size()
		if files > maxPluginInstallFiles || total > maxPluginInstallBytes {
			return errors.New("plugin package exceeds the installation size limit")
		}
		input, err := os.Open(path)
		if err != nil {
			return err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm()&0o755)
		if err != nil {
			_ = input.Close()
			return err
		}
		_, copyErr := io.Copy(output, input)
		inputCloseErr := input.Close()
		closeErr := output.Close()
		if copyErr != nil {
			return copyErr
		}
		if inputCloseErr != nil {
			return inputCloseErr
		}
		return closeErr
	})
}

func safeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	var builder strings.Builder
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '_' || char == '-' {
			builder.WriteRune(char)
		} else {
			builder.WriteByte('_')
		}
	}
	if builder.Len() == 0 {
		return "unnamed"
	}
	return builder.String()
}

func namespacedToolName(pluginID, toolName string) string {
	return "plugin__" + safeIdentifier(pluginID) + "__" + safeIdentifier(toolName)
}

func markToolNameConflicts(records map[string]record) {
	type owner struct {
		pluginID string
		toolName string
	}
	owners := make(map[string][]owner)
	ids := make([]string, 0, len(records))
	for id := range records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		record := records[id]
		if record.err != nil {
			continue
		}
		for _, descriptor := range record.manifest.Tools {
			fullName := namespacedToolName(record.manifest.ID, descriptor.Name)
			owners[fullName] = append(owners[fullName], owner{pluginID: id, toolName: descriptor.Name})
		}
	}

	conflicts := make(map[string][]string)
	fullNames := make([]string, 0, len(owners))
	for fullName := range owners {
		fullNames = append(fullNames, fullName)
	}
	sort.Strings(fullNames)
	for _, fullName := range fullNames {
		matches := owners[fullName]
		if len(matches) < 2 {
			continue
		}
		pluginIDs := make([]string, 0, len(matches))
		seen := make(map[string]bool)
		for _, match := range matches {
			if !seen[match.pluginID] {
				seen[match.pluginID] = true
				pluginIDs = append(pluginIDs, match.pluginID)
			}
		}
		if len(pluginIDs) < 2 {
			continue
		}
		sort.Strings(pluginIDs)
		message := fmt.Sprintf("namespaced tool name %q conflicts across plugins %s", fullName, quoteList(pluginIDs))
		for _, pluginID := range pluginIDs {
			conflicts[pluginID] = append(conflicts[pluginID], message)
		}
	}
	for pluginID, messages := range conflicts {
		record := records[pluginID]
		record.err = errors.New(strings.Join(messages, "; "))
		records[pluginID] = record
	}
}

func (m *Manager) checkToolNameConflicts(candidate Manifest) error {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	owners := make(map[string]string)
	for id, record := range m.records {
		if id == candidate.ID {
			continue
		}
		for _, descriptor := range record.manifest.Tools {
			owners[namespacedToolName(record.manifest.ID, descriptor.Name)] = id
		}
	}
	for _, descriptor := range candidate.Tools {
		fullName := namespacedToolName(candidate.ID, descriptor.Name)
		if ownerID := owners[fullName]; ownerID != "" {
			return fmt.Errorf("cannot install plugin %q: namespaced tool name %q conflicts with plugin %q", candidate.ID, fullName, ownerID)
		}
	}
	return nil
}

func quoteList(values []string) string {
	quoted := make([]string, len(values))
	for index, value := range values {
		quoted[index] = fmt.Sprintf("%q", value)
	}
	return strings.Join(quoted, ", ")
}
