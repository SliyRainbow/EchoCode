package agent

import (
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/protocol"
)

// TestSwitchModelKeepsHistory 验证切换模型后对话历史不丢失（曾经的 bug：换模型就忘记上文）。
func TestSwitchModelKeepsHistory(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})

	preview := svc.contextPreview()

	// 模型 A：初始化会话并追加一轮对话。
	routeA := chatRoute{Provider: ModelProviderSetting{ID: "provider-a", Name: "Provider A", Protocol: "openai-compatible"}, ModelID: "model-a"}
	svc.ensureProviderSession(routeA, preview, "disabled", "")
	svc.sessionMessages = append(svc.sessionMessages,
		protocol.Message{Role: "user", Content: "记住数字 42"},
		protocol.Message{Role: "assistant", Content: "好的，我记住了 42"},
	)
	svc.sessionState.TurnCount = 1

	historyBefore := 0
	for _, m := range svc.sessionMessages {
		if m.Role != "system" {
			historyBefore++
		}
	}
	if historyBefore != 2 {
		t.Fatalf("模型A应有2条历史, got %d", historyBefore)
	}

	// 切换到模型 B。
	routeB := chatRoute{Provider: ModelProviderSetting{ID: "provider-b", Name: "Provider B", Protocol: "openai-compatible"}, ModelID: "model-b"}
	svc.ensureProviderSession(routeB, preview, "disabled", "")

	// 历史必须保留（换模型不能丢上文）。
	var foundUser, foundAssistant bool
	systemCount := 0
	for _, m := range svc.sessionMessages {
		switch m.Role {
		case "system":
			systemCount++
		case "user":
			if m.Content == "记住数字 42" {
				foundUser = true
			}
		case "assistant":
			if m.Content == "好的，我记住了 42" {
				foundAssistant = true
			}
		}
	}
	if systemCount != 1 {
		t.Fatalf("切模型后应只有1条system前缀, got %d", systemCount)
	}
	if !foundUser || !foundAssistant {
		t.Fatal("切换模型后对话历史丢失（bug 回归）")
	}
	if svc.sessionState.Model != "model-b" {
		t.Fatalf("model 应更新为 model-b, got %q", svc.sessionState.Model)
	}
	if svc.sessionState.TurnCount != 1 {
		t.Fatalf("切模型应保留 turnCount=1, got %d", svc.sessionState.TurnCount)
	}
}

// TestColdStartNoHistory 验证冷启动（无历史）时不残留脏计数。
func TestColdStartNoHistory(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	preview := svc.contextPreview()
	route := chatRoute{Provider: ModelProviderSetting{ID: "p", Name: "P", Protocol: "openai-compatible"}, ModelID: "m"}
	svc.ensureProviderSession(route, preview, "disabled", "")
	if len(svc.sessionMessages) != 1 || svc.sessionMessages[0].Role != "system" {
		t.Fatalf("冷启动应只有1条system, got %d", len(svc.sessionMessages))
	}
	if svc.sessionState.TurnCount != 0 {
		t.Fatalf("冷启动 turnCount 应为0, got %d", svc.sessionState.TurnCount)
	}
}

func TestSwitchModelKeepsCompressedSummary(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	preview := svc.contextPreview()
	routeA := chatRoute{Provider: ModelProviderSetting{ID: "provider-a", Name: "Provider A", Protocol: "openai-compatible"}, ModelID: "model-a"}
	svc.ensureProviderSession(routeA, preview, "disabled", "")
	svc.sessionMessages = append(svc.sessionMessages,
		protocol.Message{Role: "system", Content: "compressed facts", InternalKind: contextSummaryKind},
		protocol.Message{Role: "user", Content: "continue"},
	)
	svc.sessionState.CompressionCount = 1
	svc.sessionState.CompressedMessageCount = 12

	routeB := chatRoute{Provider: ModelProviderSetting{ID: "provider-b", Name: "Provider B", Protocol: "anthropic"}, ModelID: "claude-test"}
	svc.ensureProviderSession(routeB, preview, "", "")

	stableCount := 0
	summaryCount := 0
	for index, message := range svc.sessionMessages {
		if index == 0 && message.Role == "system" && message.InternalKind == "" {
			stableCount++
		}
		if message.InternalKind == contextSummaryKind && message.Content == "compressed facts" {
			summaryCount++
		}
	}
	if stableCount != 1 || summaryCount != 1 {
		t.Fatalf("messages after switch = %#v", svc.sessionMessages)
	}
	if svc.sessionState.CompressionCount != 1 || svc.sessionState.CompressedMessageCount != 12 {
		t.Fatalf("compression telemetry = %#v", svc.sessionState)
	}
}

func TestSavedRouteAndReasoningChangesKeepHistory(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	providerA := ModelProviderSetting{ID: "provider-a", Name: "Provider A", Protocol: "openai-compatible", Enabled: true, Models: []ProviderModel{{ID: "model-a"}}}
	providerB := ModelProviderSetting{ID: "provider-b", Name: "Provider B", Protocol: "anthropic", Enabled: true, Models: []ProviderModel{{ID: "claude-test"}}}
	settings := svc.WorkbenchState().RuntimeSettings
	settings.Model = ModelSettings{SelectedProviderID: providerA.ID, SelectedModelID: "model-a", Providers: []ModelProviderSetting{providerA, providerB}}
	if _, err := svc.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	preview := svc.contextPreview()
	svc.ensureProviderSession(chatRoute{Provider: providerA, ModelID: "model-a"}, preview, "", "")
	svc.sessionMessages = append(svc.sessionMessages,
		protocol.Message{Role: "user", Content: "remember 42"},
		protocol.Message{Role: "assistant", Content: "remembered"},
	)
	svc.sessionState.TurnCount = 1

	settings = svc.WorkbenchState().RuntimeSettings
	settings.Model.SelectedProviderID = providerB.ID
	settings.Model.SelectedModelID = "claude-test"
	if _, err := svc.SaveRuntimeSettings(settings); err != nil {
		t.Fatal(err)
	}
	if len(svc.sessionMessages) < 3 {
		t.Fatalf("route save cleared history: %#v", svc.sessionMessages)
	}
	if _, err := svc.SetReasoningLevel(ReasoningHigh); err != nil {
		t.Fatal(err)
	}
	if len(svc.sessionMessages) < 3 {
		t.Fatalf("reasoning change cleared history: %#v", svc.sessionMessages)
	}
	svc.ensureProviderSession(chatRoute{Provider: providerB, ModelID: "claude-test"}, svc.contextPreview(), "", "")

	foundUser := false
	for _, message := range svc.sessionMessages {
		if message.Role == "user" && message.Content == "remember 42" {
			foundUser = true
		}
	}
	if !foundUser || svc.sessionState.TurnCount != 1 {
		t.Fatalf("history after route/reasoning switch = %#v, state = %#v", svc.sessionMessages, svc.sessionState)
	}
}

func TestEnsureProviderSessionRepairsMissingSystemPrefix(t *testing.T) {
	svc := NewService(ServiceConfig{SkillsDir: t.TempDir()})
	preview := svc.contextPreview()
	route := chatRoute{Provider: ModelProviderSetting{ID: "provider", Name: "Provider", Protocol: "openai-compatible"}, ModelID: "model"}
	svc.sessionMessages = []protocol.Message{{Role: "user", Content: "restored history"}}
	svc.sessionState = DeepSeekSessionState{
		ProviderID: route.Provider.ID,
		Protocol:   route.Provider.Protocol,
		Model:      route.ModelID,
		Reasoning:  svc.reasoning,
		PrefixHash: preview.PrefixHash,
		TurnCount:  1,
	}

	svc.ensureProviderSession(route, preview, "", "")
	if len(svc.sessionMessages) != 2 || svc.sessionMessages[0].Role != "system" || svc.sessionMessages[1].Content != "restored history" {
		t.Fatalf("repaired messages = %#v", svc.sessionMessages)
	}
}
