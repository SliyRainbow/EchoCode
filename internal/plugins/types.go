package plugins

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

const (
	ManifestFileName = "echocode-plugin.json"
	ProtocolVersion  = "1.0"
)

var identifierPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,63}$`)

type Settings struct {
	MaxExecutionSeconds int       `json:"maxExecutionSeconds"`
	MaxOutputBytes      int       `json:"maxOutputBytes"`
	Entries             []Setting `json:"entries"`
}

type Setting struct {
	ID          string          `json:"id"`
	Enabled     bool            `json:"enabled"`
	Permissions PermissionGrant `json:"permissions"`
}

type PermissionGrant struct {
	FileRead  bool `json:"fileRead"`
	FileWrite bool `json:"fileWrite"`
	Network   bool `json:"network"`
}

type Manifest struct {
	SchemaVersion int            `json:"schemaVersion"`
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Version       string         `json:"version"`
	Description   string         `json:"description"`
	Author        string         `json:"author,omitempty"`
	Homepage      string         `json:"homepage,omitempty"`
	Runtime       Runtime        `json:"runtime"`
	Permissions   PermissionSpec `json:"permissions"`
	Tools         []ToolManifest `json:"tools"`
}

type Runtime struct {
	Transport string   `json:"transport"`
	Command   string   `json:"command"`
	Args      []string `json:"args,omitempty"`
}

type PermissionSpec struct {
	FileRead  bool `json:"fileRead"`
	FileWrite bool `json:"fileWrite"`
	Network   bool `json:"network"`
}

type ToolManifest struct {
	Name           string            `json:"name"`
	Description    string            `json:"description"`
	InputSchema    map[string]any    `json:"inputSchema"`
	ReadOnly       bool              `json:"readOnly"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"`
	Permissions    PermissionSpec    `json:"permissions"`
	Paths          []PathRequirement `json:"paths,omitempty"`
}

type PathRequirement struct {
	Argument string `json:"argument"`
	Access   string `json:"access"` // read | write
	Optional bool   `json:"optional,omitempty"`
}

type Status struct {
	ID                 string          `json:"id"`
	Name               string          `json:"name"`
	Version            string          `json:"version"`
	Description        string          `json:"description"`
	Author             string          `json:"author,omitempty"`
	Homepage           string          `json:"homepage,omitempty"`
	Source             string          `json:"source"` // builtin | installed
	State              string          `json:"state"`  // ready | disabled | unavailable | error
	Message            string          `json:"message"`
	Path               string          `json:"path,omitempty"`
	ToolCount          int             `json:"toolCount"`
	AvailableToolCount int             `json:"availableToolCount"`
	Permissions        PermissionSpec  `json:"permissions"`
	GrantedPermissions PermissionGrant `json:"grantedPermissions"`
	CanUninstall       bool            `json:"canUninstall"`
	ManifestSchema     int             `json:"manifestSchema"`
	ProtocolVersion    string          `json:"protocolVersion"`
	Tools              []ToolStatus    `json:"tools"`
}

type ToolStatus struct {
	Name        string         `json:"name"`
	FullName    string         `json:"fullName"`
	Description string         `json:"description"`
	ReadOnly    bool           `json:"readOnly"`
	Permissions PermissionSpec `json:"permissions"`
}

func DefaultSettings() Settings {
	return Settings{
		MaxExecutionSeconds: 120,
		MaxOutputBytes:      1024 * 1024,
		Entries: []Setting{
			{
				ID:      ArtifactPluginID,
				Enabled: true,
				Permissions: PermissionGrant{
					FileRead: true, FileWrite: true,
				},
			},
		},
	}
}

func NormalizeSettings(settings Settings) Settings {
	defaults := DefaultSettings()
	if settings.MaxExecutionSeconds < 5 {
		settings.MaxExecutionSeconds = defaults.MaxExecutionSeconds
	}
	if settings.MaxExecutionSeconds > 3600 {
		settings.MaxExecutionSeconds = 3600
	}
	if settings.MaxOutputBytes < 64*1024 {
		settings.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if settings.MaxOutputBytes > 16*1024*1024 {
		settings.MaxOutputBytes = 16 * 1024 * 1024
	}
	if len(settings.Entries) == 0 {
		settings.Entries = defaults.Entries
	}
	entries := make([]Setting, 0, len(settings.Entries))
	seen := map[string]bool{}
	for _, entry := range settings.Entries {
		entry.ID = strings.ToLower(strings.TrimSpace(entry.ID))
		if entry.ID == legacyOfficePluginID || entry.ID == legacyAccessPluginID {
			continue
		}
		if !identifierPattern.MatchString(entry.ID) || seen[entry.ID] {
			continue
		}
		seen[entry.ID] = true
		entries = append(entries, entry)
	}
	if !seen[ArtifactPluginID] {
		entries = append(entries, defaults.Entries[0])
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	settings.Entries = entries
	return settings
}

func SettingFor(settings Settings, id string) (Setting, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, entry := range settings.Entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return Setting{ID: id}, false
}

func UpsertSetting(settings Settings, entry Setting) Settings {
	settings = NormalizeSettings(settings)
	entry.ID = strings.ToLower(strings.TrimSpace(entry.ID))
	found := false
	for index := range settings.Entries {
		if settings.Entries[index].ID == entry.ID {
			settings.Entries[index] = entry
			found = true
			break
		}
	}
	if !found && identifierPattern.MatchString(entry.ID) {
		settings.Entries = append(settings.Entries, entry)
	}
	sort.Slice(settings.Entries, func(i, j int) bool { return settings.Entries[i].ID < settings.Entries[j].ID })
	return settings
}

func RemoveSetting(settings Settings, id string) Settings {
	settings = NormalizeSettings(settings)
	id = strings.ToLower(strings.TrimSpace(id))
	entries := make([]Setting, 0, len(settings.Entries))
	for _, entry := range settings.Entries {
		if entry.ID != id {
			entries = append(entries, entry)
		}
	}
	settings.Entries = entries
	return settings
}

func ValidateManifest(manifest Manifest, builtin bool) error {
	manifest.ID = strings.ToLower(strings.TrimSpace(manifest.ID))
	if manifest.SchemaVersion != 1 {
		return fmt.Errorf("unsupported plugin manifest schemaVersion %d", manifest.SchemaVersion)
	}
	if !identifierPattern.MatchString(manifest.ID) {
		return fmt.Errorf("invalid plugin id %q", manifest.ID)
	}
	if strings.TrimSpace(manifest.Name) == "" {
		return errors.New("plugin name is required")
	}
	if strings.TrimSpace(manifest.Version) == "" {
		return errors.New("plugin version is required")
	}
	if len(manifest.Tools) == 0 {
		return errors.New("plugin must declare at least one tool")
	}
	if !builtin {
		if manifest.Runtime.Transport != "stdio" {
			return fmt.Errorf("unsupported plugin transport %q", manifest.Runtime.Transport)
		}
		if strings.TrimSpace(manifest.Runtime.Command) == "" {
			return errors.New("stdio plugin runtime command is required")
		}
	}
	seen := map[string]bool{}
	seenFullNames := map[string]string{}
	for index, tool := range manifest.Tools {
		tool.Name = strings.ToLower(strings.TrimSpace(tool.Name))
		if !identifierPattern.MatchString(tool.Name) {
			return fmt.Errorf("tool %d has invalid name %q", index, tool.Name)
		}
		if seen[tool.Name] {
			return fmt.Errorf("duplicate plugin tool %q", tool.Name)
		}
		seen[tool.Name] = true
		fullName := namespacedToolName(manifest.ID, tool.Name)
		if len(fullName) > 64 {
			return fmt.Errorf("tool %q produces a namespaced name longer than 64 characters: %q", tool.Name, fullName)
		}
		if previous, exists := seenFullNames[fullName]; exists {
			return fmt.Errorf("tools %q and %q produce the same namespaced name %q", previous, tool.Name, fullName)
		}
		seenFullNames[fullName] = tool.Name
		if strings.TrimSpace(tool.Description) == "" {
			return fmt.Errorf("tool %q description is required", tool.Name)
		}
		if tool.InputSchema == nil || tool.InputSchema["type"] != "object" {
			return fmt.Errorf("tool %q inputSchema must be a JSON object schema", tool.Name)
		}
		if !permissionContains(manifest.Permissions, tool.Permissions) {
			return fmt.Errorf("tool %q requests permissions not declared by the plugin", tool.Name)
		}
		if tool.ReadOnly && (tool.Permissions.FileWrite || len(writePathRequirements(tool.Paths)) > 0) {
			return fmt.Errorf("read-only tool %q cannot request write access", tool.Name)
		}
		pathNames := map[string]bool{}
		for _, path := range tool.Paths {
			path.Argument = strings.TrimSpace(path.Argument)
			if path.Argument == "" || strings.ContainsAny(path.Argument, ".[]") {
				return fmt.Errorf("tool %q has invalid top-level path argument %q", tool.Name, path.Argument)
			}
			if path.Access != "read" && path.Access != "write" {
				return fmt.Errorf("tool %q path %q has invalid access %q", tool.Name, path.Argument, path.Access)
			}
			if pathNames[path.Argument] {
				return fmt.Errorf("tool %q repeats path argument %q", tool.Name, path.Argument)
			}
			pathNames[path.Argument] = true
			if path.Access == "read" && !tool.Permissions.FileRead {
				return fmt.Errorf("tool %q path %q requires fileRead", tool.Name, path.Argument)
			}
			if path.Access == "write" && !tool.Permissions.FileWrite {
				return fmt.Errorf("tool %q path %q requires fileWrite", tool.Name, path.Argument)
			}
		}
	}
	return nil
}

func DecodeManifest(data []byte, builtin bool) (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode plugin manifest: %w", err)
	}
	normalizeManifest(&manifest)
	if err := ValidateManifest(manifest, builtin); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func normalizeManifest(manifest *Manifest) {
	manifest.ID = strings.ToLower(strings.TrimSpace(manifest.ID))
	manifest.Name = strings.TrimSpace(manifest.Name)
	manifest.Version = strings.TrimSpace(manifest.Version)
	manifest.Description = strings.TrimSpace(manifest.Description)
	manifest.Author = strings.TrimSpace(manifest.Author)
	manifest.Homepage = strings.TrimSpace(manifest.Homepage)
	manifest.Runtime.Transport = strings.ToLower(strings.TrimSpace(manifest.Runtime.Transport))
	manifest.Runtime.Command = strings.TrimSpace(manifest.Runtime.Command)
	for index := range manifest.Runtime.Args {
		manifest.Runtime.Args[index] = strings.TrimSpace(manifest.Runtime.Args[index])
	}
	for index := range manifest.Tools {
		manifest.Tools[index].Name = strings.ToLower(strings.TrimSpace(manifest.Tools[index].Name))
		manifest.Tools[index].Description = strings.TrimSpace(manifest.Tools[index].Description)
	}
	sort.Slice(manifest.Tools, func(i, j int) bool { return manifest.Tools[i].Name < manifest.Tools[j].Name })
}

func permissionContains(parent, child PermissionSpec) bool {
	return (!child.FileRead || parent.FileRead) &&
		(!child.FileWrite || parent.FileWrite) &&
		(!child.Network || parent.Network)
}

func grantContains(grant PermissionGrant, required PermissionSpec) bool {
	return (!required.FileRead || grant.FileRead) &&
		(!required.FileWrite || grant.FileWrite) &&
		(!required.Network || grant.Network)
}

func writePathRequirements(paths []PathRequirement) []PathRequirement {
	result := make([]PathRequirement, 0, len(paths))
	for _, path := range paths {
		if path.Access == "write" {
			result = append(result, path)
		}
	}
	return result
}
