package agent

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/tools"
)

type toolResourcePlan struct {
	Requests []ResourceRequest
	Precise  bool
}

func (s *Service) resourcesForTool(name string, tool tools.Tool, rawArgs json.RawMessage) toolResourcePlan {
	name = strings.TrimSpace(name)
	args := make(map[string]any)
	_ = json.Unmarshal(normalizeToolArgs(rawArgs), &args)
	pathRequest := func(field string, mode ResourceMode) ResourceRequest {
		return ResourceRequest{Key: "file:" + s.canonicalToolResourcePath(stringArgument(args, field)), Mode: mode}
	}
	directoryRequest := func(field string, mode ResourceMode) ResourceRequest {
		return ResourceRequest{Key: "dir:" + s.canonicalToolResourcePath(stringArgument(args, field)), Mode: mode}
	}
	workspace := ResourceRequest{Key: s.workspaceResourceKey(), Mode: ResourceWrite}
	plan := toolResourcePlan{Precise: true}

	switch name {
	case "read_file", "file_info", "open_file", "render_artifact", "inspect_visual":
		plan.Requests = []ResourceRequest{pathRequest("path", ResourceRead)}
	case "list_dir", "search", "grep", "glob":
		plan.Requests = []ResourceRequest{directoryRequest("path", ResourceRead)}
	case "write_file", "apply_patch", "delete_file":
		plan.Requests = []ResourceRequest{pathRequest("path", ResourceWrite)}
	case "copy_file":
		plan.Requests = []ResourceRequest{pathRequest("source", ResourceRead), pathRequest("destination", ResourceWrite)}
	case "download_file":
		if destination := stringArgument(args, "destination"); destination != "" {
			plan.Requests = []ResourceRequest{{Key: "file:" + s.canonicalToolResourcePath(destination), Mode: ResourceWrite}}
			break
		}
		directory := stringArgument(args, "destination_directory")
		filename := stringArgument(args, "filename")
		if directory != "" && filename != "" {
			path := filepath.Join(s.canonicalToolResourcePath(directory), filename)
			plan.Requests = []ResourceRequest{{Key: "file:" + s.canonicalToolResourcePath(path), Mode: ResourceWrite}}
		} else {
			plan.Requests = []ResourceRequest{{Key: "dir:" + s.canonicalToolResourcePath(directory), Mode: ResourceWrite}}
		}
	case "git_repository":
		action := strings.ToLower(stringArgument(args, "action"))
		if action == "clone" && stringArgument(args, "destination") != "" {
			plan.Requests = []ResourceRequest{{Key: "dir:" + s.canonicalToolResourcePath(stringArgument(args, "destination")), Mode: ResourceWrite}}
		} else if (action == "fetch" || action == "pull") && stringArgument(args, "repository") != "" {
			plan.Requests = []ResourceRequest{{Key: "dir:" + s.canonicalToolResourcePath(stringArgument(args, "repository")), Mode: ResourceWrite}}
		} else {
			plan.Requests = []ResourceRequest{workspace}
			plan.Precise = false
		}
	case "browser":
		plan.Requests = []ResourceRequest{{Key: "browser:embedded", Mode: ResourceWrite}}
		if stringArgument(args, "path") != "" {
			plan.Requests = append(plan.Requests, pathRequest("path", ResourceWrite))
		}
	case "computer":
		plan.Requests = []ResourceRequest{{Key: "computer:desktop", Mode: ResourceWrite}}
	case "terminal":
		sessionID := stringArgument(args, "session_id")
		if sessionID == "" {
			sessionID = "default"
		}
		plan.Requests = []ResourceRequest{{Key: "terminal:" + sessionID, Mode: ResourceWrite}, workspace}
	case "ssh":
		credentialID := stringArgument(args, "credential_id")
		if credentialID == "" {
			credentialID = "default"
		}
		plan.Requests = []ResourceRequest{{Key: "remote:" + credentialID, Mode: ResourceWrite}}
	case "update_plan":
		plan.Requests = []ResourceRequest{{Key: "session:" + s.resourceSessionID() + ":plan", Mode: ResourceWrite}}
	case "delegate_task", "await_subagents":
		plan.Requests = []ResourceRequest{{Key: "session:" + s.resourceSessionID() + ":subagents", Mode: ResourceWrite}}
	case "run_command", "git":
		plan.Requests = []ResourceRequest{workspace}
		plan.Precise = false
	default:
		if toolNeedsExclusiveWorkspaceAccess(name, tool) {
			plan.Requests = []ResourceRequest{workspace}
			plan.Precise = false
		}
	}
	return plan
}

func (s *Service) resourceCoordinatorForWorkspace() *ResourceCoordinator {
	if s == nil {
		return NewResourceCoordinator()
	}
	if s.resourceCoordinators == nil {
		return &s.resourceCoordinator
	}
	key := s.workspaceResourceKey()
	coordinator, _ := s.resourceCoordinators.LoadOrStore(key, NewResourceCoordinator())
	return coordinator.(*ResourceCoordinator)
}

func (s *Service) workspaceResourceKey() string {
	root := "workspace"
	if s != nil && strings.TrimSpace(s.runtimeSettings.WorkspaceRoot) != "" {
		root = s.canonicalToolResourcePath(s.runtimeSettings.WorkspaceRoot)
	}
	return "workspace:" + root
}

func (s *Service) canonicalToolResourcePath(value string) string {
	value = strings.TrimSpace(value)
	root := ""
	if s != nil {
		root = strings.TrimSpace(s.runtimeSettings.WorkspaceRoot)
	}
	if value == "" || value == "." {
		value = root
	} else if !filepath.IsAbs(value) && root != "" {
		value = filepath.Join(root, value)
	}
	if absolute, err := filepath.Abs(value); err == nil {
		value = absolute
	}
	value = filepath.Clean(value)
	if runtime.GOOS == "windows" {
		value = strings.ToLower(value)
	}
	return value
}

func (s *Service) resourceSessionID() string {
	if s != nil && strings.TrimSpace(s.sessionID) != "" {
		return strings.TrimSpace(s.sessionID)
	}
	return "default"
}

func stringArgument(args map[string]any, name string) string {
	value, _ := args[name].(string)
	return strings.TrimSpace(value)
}
