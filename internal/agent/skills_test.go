package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/skills"
)

func writeSkill(t *testing.T, dir, folder, name, desc string) {
	t.Helper()
	skillDir := filepath.Join(dir, folder)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n正文\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeSkillWithTrigger(t *testing.T, dir, folder, name, desc, trigger string) {
	t.Helper()
	skillDir := filepath.Join(dir, folder)
	if err := os.MkdirAll(filepath.Join(skillDir, "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name + "\ndescription: " + desc + "\n---\n正文\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	metadata := "activation: auto\ntrigger: " + trigger + "\n"
	if err := os.WriteFile(filepath.Join(skillDir, "agents", "echocode.yaml"), []byte(metadata), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestTriggeredSkillLoadsFullInstructions(t *testing.T) {
	globalSkills := t.TempDir()
	writeSkill(t, globalSkills, "helper", "project-helper", "项目专属构建流程")
	svc := NewService(ServiceConfig{SkillsDir: globalSkills})
	index := svc.loadSkillsIndex()
	loaded := svc.loadTriggeredSkills("请调用 project-helper 完成构建", index)
	if len(loaded) != 1 || !strings.Contains(loaded[0], "正文") {
		t.Fatalf("triggered skills = %#v", loaded)
	}
	if unrelated := svc.loadTriggeredSkills("普通闲聊", index); len(unrelated) != 0 {
		t.Fatalf("unrelated prompt loaded skills: %#v", unrelated)
	}
}

func TestTriggeredSkillsUseTokenBudgetInsteadOfFixedCount(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"skill-a", "skill-b", "skill-c"} {
		writeSkillWithTrigger(t, root, name, name, "multi-skill helper", "multi-skill")
	}
	service := NewService(ServiceConfig{SkillsDir: root})
	defer service.Close()

	loaded := service.loadTriggeredSkills("use multi-skill for this task", service.loadSkillsIndex())
	names, _, tokens := triggeredSkillMetrics(loaded)
	if len(loaded) != 3 || len(names) != 3 {
		t.Fatalf("triggered skills = %#v, names = %#v", loaded, names)
	}
	if tokens <= 0 || tokens > triggeredSkillTokenBudget(service.reasoning) {
		t.Fatalf("triggered skill tokens = %d, budget = %d", tokens, triggeredSkillTokenBudget(service.reasoning))
	}
}

func TestExplicitlyNamedSkillWinsContextBudgetPriority(t *testing.T) {
	root := t.TempDir()
	writeSkillWithTrigger(t, root, "broad", "a-broad-skill", "broad helper", "shared-trigger")
	writeSkillWithTrigger(t, root, "critical", "z-critical-skill", "critical helper", "manual")
	service := NewService(ServiceConfig{SkillsDir: root})
	defer service.Close()
	service.reasoning = ReasoningLow

	loaded := service.loadTriggeredSkills("shared-trigger and $z-critical-skill", service.loadSkillsIndex())
	if len(loaded) < 2 || !strings.HasPrefix(loaded[0], "skill: z-critical-skill\n") {
		t.Fatalf("explicit skill was not prioritized: %#v", loaded)
	}
}

func TestCoreSkillDoesNotTriggerOnGenericTechnicalTerms(t *testing.T) {
	entry := skills.IndexEntry{
		Name:        "echocode-agent-core",
		Trigger:     "echocode agent | echocode 工具注册 | echocode 缓存策略",
		TriggerMode: "explicit",
		Description: "仅用于修改 EchoCode 自身 Agent 内核",
	}
	for _, prompt := range []string{
		"帮我给这个函数增加缓存",
		"设计一个 agent 并制定 plan",
		"实现协议解析和 token 统计",
		"这个项目要接入 MCP 和 Skill",
	} {
		if skillMatchesPrompt(entry, prompt) {
			t.Fatalf("generic prompt unexpectedly triggered core skill: %q", prompt)
		}
	}
	for _, prompt := range []string{
		"请检查 EchoCode Agent 的上下文",
		"修复 echocode 工具注册",
		"$echocode-agent-core",
	} {
		if !skillMatchesPrompt(entry, prompt) {
			t.Fatalf("explicit EchoCode prompt did not trigger core skill: %q", prompt)
		}
	}
}

func TestExplicitSkillTriggersUseBoundariesAndManualName(t *testing.T) {
	office := skills.IndexEntry{Name: "echocode-office-artifacts", Trigger: ".xlsx | excel 工作簿 | 考勤表", TriggerMode: "explicit"}
	for _, prompt := range []string{"生成 C:/reports/month.xlsx", "创建 Excel 工作簿", "做一份员工考勤表"} {
		if !skillMatchesPrompt(office, prompt) {
			t.Fatalf("office prompt did not trigger: %q", prompt)
		}
	}
	if skillMatchesPrompt(office, "修复 foo.xlsxwriter 依赖") {
		t.Fatal("ASCII trigger matched inside a larger token")
	}
	manual := skills.IndexEntry{Name: "private-helper", Trigger: "manual", TriggerMode: "manual"}
	if skillMatchesPrompt(manual, "please help") || !skillMatchesPrompt(manual, "$private-helper") {
		t.Fatal("manual skill activation did not require the complete skill name")
	}
}

func TestTriggeredSkillMetricsExposeOnlyActualInjection(t *testing.T) {
	root := t.TempDir()
	writeSkillWithTrigger(t, root, "core", "echocode-agent-core", "EchoCode Agent internals", "echocode agent")
	service := NewService(ServiceConfig{SkillsDir: root})
	defer service.Close()

	generic := service.contextPreviewForInput("给函数增加缓存")
	if len(generic.TriggeredSkillNames) != 0 || generic.TriggeredSkillCharacters != 0 || generic.TriggeredSkillTokens != 0 {
		t.Fatalf("generic context reported a skill injection: %#v", generic)
	}
	explicit := service.contextPreviewForInput("检查 EchoCode Agent")
	if len(explicit.TriggeredSkillNames) != 1 || explicit.TriggeredSkillNames[0] != "echocode-agent-core" {
		t.Fatalf("triggered skill names = %#v", explicit.TriggeredSkillNames)
	}
	if explicit.TriggeredSkillCharacters <= 0 || explicit.TriggeredSkillTokens <= 0 {
		t.Fatalf("triggered skill metrics = chars %d, tokens %d", explicit.TriggeredSkillCharacters, explicit.TriggeredSkillTokens)
	}
}

func TestRepositorySkillsKeepGenericRequestsAtZeroInjection(t *testing.T) {
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate repository skills")
	}
	skillsDir := filepath.Join(filepath.Dir(currentFile), "..", "..", "skills")
	service := NewService(ServiceConfig{SkillsDir: skillsDir})
	defer service.Close()

	generic := service.contextPreviewForInput("帮我给这个 agent 的协议解析增加缓存并制定 plan")
	if len(generic.TriggeredSkillNames) != 0 || generic.TriggeredSkillTokens != 0 {
		t.Fatalf("generic technical request injected repository skills: %#v", generic.TriggeredSkillNames)
	}
	core := service.contextPreviewForInput("修改 EchoCode Agent 的上下文组装")
	if len(core.TriggeredSkillNames) != 1 || core.TriggeredSkillNames[0] != "echocode-agent-core" {
		t.Fatalf("EchoCode core request triggered %#v", core.TriggeredSkillNames)
	}
	if core.TriggeredSkillTokens <= 0 || core.TriggeredSkillTokens > 900 {
		t.Fatalf("core skill injection estimate = %d tokens, want 1..900", core.TriggeredSkillTokens)
	}
	office := service.contextPreviewForInput("创建员工月度考勤表.xlsx")
	if len(office.TriggeredSkillNames) != 1 || office.TriggeredSkillNames[0] != "echocode-office-artifacts" {
		t.Fatalf("Office request triggered %#v", office.TriggeredSkillNames)
	}
}

// TestProjectSkillsAutoLoad 验证活动项目工作区下的 skills 会被自动合并加载。
func TestProjectSkillsAutoLoad(t *testing.T) {
	globalSkills := t.TempDir()
	workspace := t.TempDir()

	// 全局技能。
	writeSkill(t, globalSkills, "core", "echocode-agent-core", "全局核心技能")
	// 项目内技能（workspace/skills/xxx）。
	writeSkill(t, filepath.Join(workspace, "skills"), "proj-skill", "project-helper", "项目专属技能")

	svc := NewService(ServiceConfig{SkillsDir: globalSkills})
	svc.runtimeSettings.WorkspaceRoot = workspace

	index := svc.loadSkillsIndex()
	names := map[string]bool{}
	for _, e := range index {
		names[e.Name] = true
	}
	if !names["echocode-agent-core"] {
		t.Fatal("应加载全局技能 echocode-agent-core")
	}
	if !names["project-helper"] {
		t.Fatal("应自动加载项目内技能 project-helper")
	}
}

// TestProjectSkillsOverrideGlobal 验证同名技能项目内覆盖全局。
func TestProjectSkillsOverrideGlobal(t *testing.T) {
	globalSkills := t.TempDir()
	workspace := t.TempDir()
	writeSkill(t, globalSkills, "shared", "shared-skill", "全局版本")
	writeSkill(t, filepath.Join(workspace, "skills"), "shared", "shared-skill", "项目版本")

	svc := NewService(ServiceConfig{SkillsDir: globalSkills})
	svc.runtimeSettings.WorkspaceRoot = workspace

	index := svc.loadSkillsIndex()
	count := 0
	var desc string
	for _, e := range index {
		if e.Name == "shared-skill" {
			count++
			desc = e.Description
		}
	}
	if count != 1 {
		t.Fatalf("同名技能应去重为1个, got %d", count)
	}
	if desc != "项目版本" {
		t.Fatalf("同名应项目内覆盖, got %q", desc)
	}
}
