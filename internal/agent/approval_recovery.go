package agent

import (
	"fmt"

	"github.com/SliyRainbow/EchoCode/internal/eventlog"
	"github.com/SliyRainbow/EchoCode/internal/pathutil"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

// Timeline operations may restore only the exact external paths previously
// approved by the host. This grants no new Agent tool access after restart.
func (s *Service) policyForRecordedChanges(events []eventlog.Event) (tools.SandboxPolicy, error) {
	policy := s.sandboxPolicy()
	if policy.FilesystemAccess == "read-only" || policy.SandboxMode == "read-only" {
		return policy, nil
	}
	for _, event := range events {
		grant := event.Payload.ApprovedExternalPath
		if event.Type != eventlog.EventFileSnapshot || grant == "" {
			continue
		}
		if !sameWorkspacePath(grant, event.Payload.Path) {
			return policy, fmt.Errorf("已授权文件的快照路径不一致")
		}
		canonical, err := pathutil.Canonical(grant)
		if err != nil || !sameWorkspacePath(canonical, grant) {
			return policy, fmt.Errorf("已授权文件的实际位置发生变化，无法自动回退: %s", grant)
		}
		if err := validateExplicitTurnPathGrant(grant); err != nil {
			return policy, err
		}
		policy.ExtraWritableRoots = append(append([]string(nil), policy.ExtraWritableRoots...), grant)
	}
	return policy, nil
}
