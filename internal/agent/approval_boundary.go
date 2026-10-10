package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/pathutil"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// boundaryFileTool operates only on concrete host-enforced file tools. External
// tools and commands never gain an automatic exemption by claiming a name.
func boundaryFileTool(tool tools.Tool, raw json.RawMessage) (tools.SandboxPolicy, func(tools.SandboxPolicy) tools.Tool, []string, bool) {
	var args struct{ Path, Source, Destination string }
	if json.Unmarshal(raw, &args) != nil {
		return tools.SandboxPolicy{}, nil, nil, false
	}
	switch t := tool.(type) {
	case tools.WriteFileTool:
		return t.Policy, func(p tools.SandboxPolicy) tools.Tool { t.Policy = p; return t }, []string{args.Path}, true
	case tools.ApplyPatchTool:
		return t.Policy, func(p tools.SandboxPolicy) tools.Tool { t.Policy = p; return t }, []string{args.Path}, true
	case tools.CopyFileTool:
		return t.Policy, func(p tools.SandboxPolicy) tools.Tool { t.Policy = p; return t }, []string{args.Destination, args.Source}, true
	case tools.DeleteFileTool:
		return t.Policy, func(p tools.SandboxPolicy) tools.Tool { t.Policy = p; return t }, []string{args.Path}, true
	}
	return tools.SandboxPolicy{}, nil, nil, false
}

// runBoundaryFileTool returns handled=false for policies/tools owned by the
// normal approval path. Grants live in one copied tool, never runtime settings.
func (s *Service) runBoundaryFileTool(ctx context.Context, tool tools.Tool, raw json.RawMessage) (tools.Result, bool, error) {
	if s.runtimeSettings.ApprovalPolicy != "on-request" {
		return tools.Result{}, false, nil
	}
	policy, withPolicy, paths, ok := boundaryFileTool(tool, raw)
	if !ok || policy.FilesystemAccess == "read-only" || policy.SandboxMode == "read-only" {
		return tools.Result{}, false, nil
	}
	boundary := policy
	boundary.ExtraWritableRoots = append([]string(nil), s.runtimeSettings.ExtraWritableRoots...)
	grants := make([]string, 0, len(paths))
	grantInputs := make([]string, 0, len(paths))
	for index, path := range paths {
		if strings.TrimSpace(path) == "" {
			return tools.Result{}, false, nil
		}
		resolve := boundary.ResolveWritePath
		if index > 0 {
			resolve = boundary.ResolveReadPath
		}
		_, err := resolve(path)
		if err == nil {
			continue
		}
		if !errors.Is(err, tools.ErrPathOutsideWorkspace) {
			return tools.Result{}, false, nil
		}
		// Unrestricted resolution here is validation only. It still enforces
		// task scope, path format and final symlink mutation restrictions.
		candidate := policy
		candidate.FilesystemAccess = "unrestricted"
		resolve = candidate.ResolveWritePath
		if index > 0 {
			resolve = candidate.ResolveReadPath
		}
		abs, err := resolve(path)
		if err != nil {
			return tools.Result{}, false, nil
		}
		abs, err = pathutil.Canonical(abs)
		if err != nil {
			return tools.Result{}, true, err
		}
		if err := validateExplicitTurnPathGrant(abs); err != nil {
			return rejectedResult(tool.Name(), err.Error()), true, nil
		}
		grants = append(grants, abs)
		grantInputs = append(grantInputs, path)
	}
	if len(grants) == 0 {
		result, err := tool.Execute(ctx, raw)
		return result, true, err
	}
	if s.approvals == nil {
		return rejectedResult(tool.Name(), "工作区外访问需要批准"), true, nil
	}
	name := tool.Name()
	// Include canonical targets in the fingerprint, so a changed junction
	// cannot inherit an earlier session grant for a different destination.
	fingerprint := approvalFingerprint(name, raw) + ":" + strings.Join(grants, "|")
	if !s.approvals.fingerprintAllowed(fingerprint) {
		emitApprovalProgress(ctx, name, raw, "waiting", "等待批准工作区外文件访问")
		decision, err := s.approvals.request(ctx, ApprovalRequest{
			Tool: name, Kind: "file", Path: strings.Join(grants, "\n"), Command: truncateApprovalArgs(raw),
			Summary: fmt.Sprintf("允许 %s 访问以下工作区外文件？本次授权仅用于这些文件。", name), Fingerprint: fingerprint,
		})
		if err != nil {
			return tools.Result{}, true, err
		}
		if !decision.Approved {
			return rejectedResult(name, "用户拒绝了工作区外文件访问"), true, nil
		}
	}
	// A junction/alias may have changed while the approval dialog was open.
	// Revalidate against the displayed canonical target before granting access.
	for index, input := range grantInputs {
		candidate := policy
		candidate.FilesystemAccess = "unrestricted"
		abs, err := candidate.ResolveReadPath(input)
		if err != nil {
			return rejectedResult(name, err.Error()), true, nil
		}
		resolved, err := pathutil.Canonical(abs)
		if err != nil || !sameWorkspacePath(resolved, grants[index]) {
			return rejectedResult(name, "文件的实际目标已变化，请重新请求授权"), true, nil
		}
	}
	policy.ExtraWritableRoots = append(append([]string(nil), policy.ExtraWritableRoots...), grants...)
	emitApprovalProgress(ctx, name, raw, "running", "已批准本次工作区外文件操作")
	result, err := withPolicy(policy).Execute(ctx, raw)
	for index := range result.Changes {
		abs, resolveErr := policy.ResolveReadPath(result.Changes[index].Path)
		if resolveErr != nil {
			continue
		}
		canonical, canonicalErr := pathutil.Canonical(abs)
		if canonicalErr != nil {
			continue
		}
		for _, grant := range grants {
			if sameWorkspacePath(canonical, grant) {
				result.Changes[index].Path = grant
				result.Changes[index].ApprovedExternalPath = grant
			}
		}
	}
	return result, true, err
}
