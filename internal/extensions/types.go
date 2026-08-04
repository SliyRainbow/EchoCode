package extensions

type Registry struct {
	SchemaVersion int             `json:"schemaVersion"`
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Packages      []RegistryEntry `json:"packages"`
}

type RegistryEntry struct {
	ID             string `json:"id"`
	Type           string `json:"type"`
	Name           string `json:"name"`
	Summary        string `json:"summary"`
	Publisher      string `json:"publisher"`
	Manifest       string `json:"manifest"`
	Featured       bool   `json:"featured"`
	SourceVerified bool   `json:"sourceVerified"`
}

type Manifest struct {
	SchemaVersion  int             `json:"schemaVersion"`
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	Name           string          `json:"name"`
	Version        string          `json:"version"`
	Channel        string          `json:"channel"`
	Summary        string          `json:"summary"`
	Description    string          `json:"description"`
	Publisher      Publisher       `json:"publisher"`
	Source         Source          `json:"source"`
	License        License         `json:"license"`
	Categories     []string        `json:"categories"`
	Capabilities   []string        `json:"capabilities"`
	Permissions    []Permission    `json:"permissions"`
	Artifacts      []Artifact      `json:"artifacts"`
	Verification   Verification    `json:"verification"`
	Install        InstallSpec     `json:"install"`
	MCP            *MCPConfig      `json:"mcp,omitempty"`
	ProjectActions []ProjectAction `json:"projectActions,omitempty"`
	Uninstall      UninstallSpec   `json:"uninstall"`
}

type Publisher struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Source struct {
	Repository string `json:"repository"`
	Release    string `json:"release"`
	ThirdParty bool   `json:"thirdParty"`
}

type License struct {
	SPDX string `json:"spdx"`
	File string `json:"file"`
}

type Permission struct {
	ID       string `json:"id"`
	Required bool   `json:"required"`
	Reason   string `json:"reason"`
}

type Artifact struct {
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	Archive     string `json:"archive"`
	URL         string `json:"url"`
	SHA256      string `json:"sha256"`
	ArchiveRoot string `json:"archiveRoot"`
	Executable  string `json:"executable"`
}

type Verification struct {
	Checksums             string `json:"checksums"`
	AttestationRepository string `json:"attestationRepository"`
}

type InstallSpec struct {
	Directory   string      `json:"directory"`
	HealthCheck HealthCheck `json:"healthCheck"`
}

type HealthCheck struct {
	Args           []string `json:"args"`
	TimeoutSeconds int      `json:"timeoutSeconds"`
}

type MCPConfig struct {
	Transport        string     `json:"transport"`
	Command          string     `json:"command"`
	Args             []string   `json:"args"`
	Env              []KeyValue `json:"env"`
	WorkingDirectory string     `json:"workingDirectory"`
	ToolResultPolicy string     `json:"toolResultPolicy"`
}

type KeyValue struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type ProjectAction struct {
	ID                   string   `json:"id"`
	Label                string   `json:"label"`
	Args                 []string `json:"args"`
	RequiresConfirmation bool     `json:"requiresConfirmation"`
	Writes               []string `json:"writes,omitempty"`
}

type UninstallSpec struct {
	RemoveInstallDirectory bool     `json:"removeInstallDirectory"`
	PreserveProjectPaths   []string `json:"preserveProjectPaths"`
}

type InstalledPackage struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	Platform    string `json:"platform"`
	Arch        string `json:"arch"`
	InstallDir  string `json:"installDir"`
	Executable  string `json:"executable"`
	InstalledAt string `json:"installedAt"`
}

type CatalogPackage struct {
	ID                string            `json:"id"`
	Type              string            `json:"type"`
	Name              string            `json:"name"`
	Summary           string            `json:"summary"`
	Publisher         string            `json:"publisher"`
	Featured          bool              `json:"featured"`
	SourceVerified    bool              `json:"sourceVerified"`
	ManifestURL       string            `json:"manifestUrl"`
	Manifest          Manifest          `json:"manifest"`
	Installed         *InstalledPackage `json:"installed,omitempty"`
	UpdateAvailable   bool              `json:"updateAvailable"`
	PlatformAvailable bool              `json:"platformAvailable"`
}

type CatalogState struct {
	RegistryURL string           `json:"registryUrl"`
	Source      string           `json:"source"`
	CheckedAt   string           `json:"checkedAt"`
	Warning     string           `json:"warning,omitempty"`
	Packages    []CatalogPackage `json:"packages"`
}

type InstallResult struct {
	Package  InstalledPackage `json:"package"`
	Manifest Manifest         `json:"manifest"`
}

type ActionResult struct {
	ID         string `json:"id"`
	Output     string `json:"output"`
	ExitCode   int    `json:"exitCode"`
	DurationMs int64  `json:"durationMs"`
}

type installedState struct {
	Packages []InstalledPackage `json:"packages"`
}
