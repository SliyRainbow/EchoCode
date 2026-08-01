package agent

import (
	"encoding/json"
	"testing"
)

func TestBrowserNavigationApproval(t *testing.T) {
	if browserNavigationNeedsApproval(json.RawMessage(`{"action":"snapshot"}`)) {
		t.Fatal("snapshot should not require website approval")
	}
	if browserNavigationNeedsApproval(json.RawMessage(`{"action":"open","url":"http://127.0.0.1:8080/page"}`)) {
		t.Fatal("loopback preview should not require website approval")
	}
	if !browserNavigationNeedsApproval(json.RawMessage(`{"action":"open","url":"https://example.com"}`)) {
		t.Fatal("external website should require approval")
	}
}
