# EchoCode Skills 与 Agent 核心维护

本文档面向修改 EchoCode Agent、工具和上下文组装的开发者。它与 `skills/echocode-agent-core/SKILL.md` 分工不同：

- 本文档解释项目如何加载、测试和演进 Skills。
- `SKILL.md` 是运行时给 Agent 的短规则，必须保持可执行、稳定和少重复。
- 项目架构和桌面边界见 [技术栈与开发工具链](technical-stack.zh-CN.md)。

## 当前 Skill 结构

仓库随项目分发的 Skill 位于：

```text
skills/
├── echocode-agent-core/
│   ├── SKILL.md
│   └── agents/
│       └── openai.yaml
└── echocode-office-artifacts/
    ├── SKILL.md
    └── agents/
        └── openai.yaml
```

`echocode-agent-core` 只在明确修改 EchoCode 自身 Agent 内核时触发；`echocode-office-artifacts` 只负责办公产物质量。以后新增 Skill 必须有清晰边界，不要把内部开发说明、通用系统规则或同一套约束复制到多个 Skill。

## 加载生命周期

`internal/skills` 负责：

1. 扫描 Skill 目录并解析 frontmatter。
2. 生成稳定的 Skill 索引，只把名称、版本、触发条件和能力摘要放入常规上下文。
3. EchoCode 专用触发条件放在 `agents/echocode.yaml`；显式 `trigger` 按 `|`、逗号或分号分隔的完整短语匹配，任务真正命中时才加载完整 `SKILL.md`。
4. 记录名称、版本、hash、注入字符数和估算 token，便于发现误触发和上下文膨胀。
5. 目录或内容变化时刷新索引，避免每轮重复注入长文本。

修改 loader 时必须验证：目录缺失、frontmatter 无效、重复名称、文件变更、hash 稳定性和并发读取。

## Agent 执行不变量

### 1. 权限先于工具

任何工具都必须通过 `SandboxPolicy` 和审批策略。工具注册不能绕过：

- 工作区根目录和额外可写目录检查。
- 只读/工作区写入/更宽权限模式。
- 网络与 Shell 开关。
- 破坏性操作开关。
- 超时、内存、CPU、进程数限制。
- 用户审批和取消。

Plan 阶段必须使用只读注册表；审阅者默认只读。不要为了让测试通过把全局审批改成自动批准。

### 2. 文件操作必须结构化

Agent 读取、搜索、写入、补丁、复制、删除文本文件时，使用：

- `read_file`
- `file_info`
- `list_dir`
- `search`
- `write_file`
- `apply_patch`
- `copy_file`
- `delete_file`

`run_command` 只用于构建、测试、编译器和确实需要执行的程序。不要把文件操作退化成 Shell 字符串；这样会破坏编码、路径安全、快照和 rewind。Windows 文本工具必须保留 UTF-8/BOM、UTF-16、GB18030 与 CRLF/LF 信息。

### 3. 工具 schema 和结果要稳定

工具注册顺序、schema 字段顺序和 MCP schema 快照会影响模型前缀缓存。新增工具时：

- 在固定位置注册。
- 输入 schema 明确、最小且可验证。
- 结果返回 `Summary`、结构化 `Parts` 和必要的本地引用。
- 长日志、JSON、diff 采用摘要优先，原文不直接重复塞入上下文。
- 为成功、拒绝、取消、超时和部分成功写测试。

### 4. 保持稳定前缀

稳定前缀包含产品身份、系统规则、推理档位、Skill 索引、MCP schema hash、项目摘要和路由策略。用户输入、工具参数、工具结果、错误和重试只能放在易变尾部。

不要每轮重排工具 schema、重复注入完整 Skill、把临时判断写进项目摘要，或因为切换推理档位而重建无关前缀。

### 5. 上下文压缩不能丢历史

根据实际模型上下文窗口扣除输出、工具和安全预留，超过策略阈值时自动压缩。压缩必须：

- 发出 `context_compression/running` 和完成/失败事件。
- 保留当前用户请求、完整 tool call/result 对、最近对话组和稳定 system 前缀。
- 失败时回滚本轮，不静默删除消息。
- 只改变发送给模型的上下文，不破坏完整事件日志、checkpoint、rewind 和分支。
- 记录 before/after tokens、移除消息数和目标预算。

### 6. Plan 与团队是编排层

Plan 是显式能力，不应默认让每轮请求翻倍。高/超高档位且用户开启 Plan 时，先用只读工具探索、请求批准，再执行计划；计划进度作为结构化事件显示在输入框上方，不能混入助手正文。

团队模式的角色顺序和权限边界是：

```text
planner → implementer → tester / reviewer → synthesizer
```

实现者可以修改工作区；测试者和审阅者默认只读；审阅意见触发有限轮次修订；取消、角色失败和费用上限必须让整次运行进入明确终态。

### 7. 协议适配保持统一

协议差异放在 `internal/protocol`，不要在前端为某个供应商复制 Agent 循环。新增协议至少覆盖：

- 认证和模型列表。
- 流式文本和结束事件。
- 原生工具调用的参数累积与错误。
- usage/cache 字段映射。
- 上下文窗口来源。
- 超时、EOF、限流和空响应。

### 8. 状态要可恢复

项目、会话、事件、模型设置、记忆和团队状态都必须考虑重启恢复。新增状态要同时定义：

- JSON/SQLite 字段与 schema 迁移。
- 事件类型和前端消费方式。
- rewind、分叉、删除项目时的行为。
- 旧版本设置的默认值。

## 修改流程

1. 先定位真实拥有该行为的模块，不从 UI 文案倒推实现。
2. 修改 Go 类型/服务和测试。
3. 修改前端类型、状态、加载态、错误态和空态。
4. 检查工具 schema、稳定前缀和上下文预算。
5. 更新 `SKILL.md` 或本文档，删除已经不适用的旧描述。
6. 运行完整检查：

```powershell
cd frontend
bun run check
cd ..
go test ./... -count=1
go vet ./...
```

涉及 Wails API、平台代码或浏览器原生表面时追加 `wails build -clean`。

## Skill 编写规则

- frontmatter 必须有小写 `name` 和清楚的 `description`。
- `SKILL.md` frontmatter 只使用标准 `name` 和 `description`；不要加入 EchoCode 私有字段。
- 自动触发的 Skill 必须在 `agents/echocode.yaml` 提供显式 `trigger`，多个短语使用 `|` 分隔；`activation: manual` 表示只接受完整 Skill 名称显式调用。
- 触发短语必须描述清晰任务边界，禁止使用孤立的 `agent`、`plan`、`文档`、`缓存` 等高频泛词。
- 完整 Skill 名称始终可以显式触发；运行时不会再把名称拆成 `agent`、`core` 等碎片进行模糊匹配。
- 正文使用命令式规则，避免把项目历史、营销文案和长教程塞给 Agent。
- 稳定规则优先写入 Skill；具体 API 细节放代码和测试。
- 超过约 500 行时拆到 `references/`，不要让每轮加载变长。
- 不要在 Skill 中硬编码本机绝对路径、API Key、临时目录或当前截图中的状态。
- 规则与代码冲突时，以代码中的权限检查、测试和版本迁移为准，并立即修正文档。

## 验证

本地可使用 Codex Skill Creator 的校验脚本（路径随安装位置变化）：

```powershell
python -X utf8 "$env:USERPROFILE\.codex\skills\.system\skill-creator\scripts\quick_validate.py" ".\skills\echocode-agent-core"
```

另外应运行 `go test ./internal/skills/...`，确认 Skill 索引和 hash 行为没有回归。
