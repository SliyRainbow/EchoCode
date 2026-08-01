package agent

import (
	"strings"
	"unicode/utf8"

	"github.com/SliyRainbow/EchoCode/internal/cache"
	"github.com/SliyRainbow/EchoCode/internal/mcp"
	"github.com/SliyRainbow/EchoCode/internal/skills"
)

type ContextSection struct {
	Name    string `json:"name"`
	Content string `json:"content"`
}

type StableContext struct {
	ProductIdentity  string               `json:"productIdentity"`
	SystemRules      []string             `json:"systemRules"`
	RuntimePolicy    string               `json:"runtimePolicy"`
	Reasoning        ReasoningProfile     `json:"reasoning"`
	SkillsIndex      []skills.IndexEntry  `json:"skillsIndex"`
	PersistentSkills []string             `json:"persistentSkills,omitempty"`
	MCPSnapshots     []mcp.ServerSnapshot `json:"mcpSnapshots"`
	ProjectSummary   string               `json:"projectSummary"`
	RoutingPolicy    string               `json:"routingPolicy"`
}

type VolatileContext struct {
	UserInput          string                  `json:"userInput"`
	TriggeredSkills    []string                `json:"triggeredSkills,omitempty"`
	ProjectContext     string                  `json:"projectContext,omitempty"`
	ExecutionState     string                  `json:"executionState,omitempty"`
	RecentDiffSummary  string                  `json:"recentDiffSummary,omitempty"`
	ToolCallSummaries  []mcp.ToolResultSummary `json:"toolCallSummaries,omitempty"`
	OutputRequirements []string                `json:"outputRequirements"`
}

type RequestContext struct {
	StablePrefix             []ContextSection `json:"stablePrefix"`
	VolatileTail             []ContextSection `json:"volatileTail"`
	PrefixHash               string           `json:"prefixHash"`
	TriggeredSkillNames      []string         `json:"triggeredSkillNames"`
	TriggeredSkillCharacters int              `json:"triggeredSkillCharacters"`
	TriggeredSkillTokens     int              `json:"triggeredSkillTokens"`
}

type ContextBuilder struct{}

func NewContextBuilder() ContextBuilder {
	return ContextBuilder{}
}

func (ContextBuilder) Build(stable StableContext, volatile VolatileContext) RequestContext {
	triggeredSkillNames, triggeredSkillCharacters, triggeredSkillTokens := triggeredSkillMetrics(volatile.TriggeredSkills)
	prefix := []ContextSection{
		{Name: "product_identity", Content: stable.ProductIdentity},
		{Name: "system_rules", Content: joinLines(stable.SystemRules)},
		{Name: "runtime_policy", Content: stable.RuntimePolicy},
		{Name: "reasoning", Content: string(stable.Reasoning.ID) + ":" + stable.Reasoning.Budget.CachePolicy},
		{Name: "skills_index", Content: skills.FormatStableIndex(stable.SkillsIndex)},
		{Name: "persistent_user_rules", Content: joinLines(stable.PersistentSkills)},
		{Name: "mcp_schema_snapshot", Content: mcp.FormatSnapshots(stable.MCPSnapshots)},
		{Name: "project_summary", Content: stable.ProjectSummary},
		{Name: "routing_policy", Content: stable.RoutingPolicy},
	}
	tail := []ContextSection{
		{Name: "user_input", Content: volatile.UserInput},
		{Name: "triggered_skills", Content: joinLines(volatile.TriggeredSkills)},
		{Name: "project_context", Content: volatile.ProjectContext},
		{Name: "execution_state", Content: volatile.ExecutionState},
		{Name: "recent_diff", Content: volatile.RecentDiffSummary},
		{Name: "tool_results", Content: mcp.FormatToolResults(volatile.ToolCallSummaries)},
		{Name: "output_requirements", Content: joinLines(volatile.OutputRequirements)},
	}

	return RequestContext{
		StablePrefix:             prefix,
		VolatileTail:             tail,
		PrefixHash:               cache.HashStablePrefix(prefix),
		TriggeredSkillNames:      triggeredSkillNames,
		TriggeredSkillCharacters: triggeredSkillCharacters,
		TriggeredSkillTokens:     triggeredSkillTokens,
	}
}

func triggeredSkillMetrics(contents []string) ([]string, int, int) {
	names := make([]string, 0, len(contents))
	seen := map[string]bool{}
	characters := 0
	tokens := 0
	for _, content := range contents {
		content = strings.TrimSpace(content)
		if content == "" {
			continue
		}
		characters += utf8.RuneCountInString(content)
		tokens += estimatePromptTokens(content)
		for _, line := range strings.Split(content, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "skill:") {
				name := strings.TrimSpace(strings.TrimPrefix(line, "skill:"))
				if name != "" && !seen[name] {
					seen[name] = true
					names = append(names, name)
				}
			}
			if line != "" {
				break
			}
		}
	}
	return names, characters, tokens
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	out := lines[0]
	for _, line := range lines[1:] {
		out += "\n" + line
	}
	return out
}
