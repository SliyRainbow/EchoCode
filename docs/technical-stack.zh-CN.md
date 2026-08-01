# EchoCode 当前架构与开发工具链

本文档以当前源码为准，描述 EchoCode 的真实运行边界。早期文档把 Anthropic、Gemini、Git、浏览器和 Agent 团队写成未来计划；这些能力现在已经进入代码，但其中一部分仍需要真实供应商和 Windows 长时间运行验证。

## 总体结构

\`\`\`text
EchoCode/
├── app*.go, *_bridge.go           Wails 应用边界与前端事件桥
├── internal/
│   ├── agent/                     Agent 状态、路由、Plan、团队、记忆、rewind
│   ├── browserengine/             Chromium/CDP 浏览器服务与原生窗口表面
│   ├── cache/                     稳定前缀与用量命中指标
│   ├── computercontrol/           Windows 窗口与输入控制
│   ├── config/                    默认配置结构
│   ├── eventlog/                  append-only 会话事件树与快照
│   ├── mcp/                       MCP server、schema 快照和远程工具
│   ├── artifacts/                 独立 DOCX、XLS/XLSX 与 PPTX 产物引擎
│   ├── plugins/                   插件 Manifest、内置工具目录与外部宿主协议
│   ├── project/                   项目/会话清单与临时工作区迁移
│   ├── protocol/                  DeepSeek、OpenAI、Anthropic、Gemini 等协议
│   ├── sandboxexec/                子进程 containment 与资源限制
│   ├── skills/                    Skill 索引、frontmatter 和按需加载
│   ├── storage/                   SQLite 迁移和用量持久化
│   ├── terminal/                  持久终端会话
│   ├── tools/                     Agent 结构化工具和权限策略
│   ├── vault/                     系统密钥库适配
│   └── workspacegit/              Git status/diff/stage/commit/branch/worktree
├── frontend/src/
│   ├── app.tsx                    工作台状态和页面组合
│   ├── components/                对话、浏览器、审阅、时间线、设置组件
│   ├── services/workbench.ts      Wails 调用和事件订阅
│   ├── types.ts                   前端状态与后端 JSON 合约
│   └── styles*.css                工作台视觉层
├── skills/                        随仓库分发的运行时 Skills
├── docs/                          面向开发者的说明
└── wails.json                     Wails 构建配置
\`\`\`

## 请求执行流

一次普通任务大致按以下顺序运行：

\`\`\`text
用户输入
  → 选择供应商/模型与推理档位
  → 读取项目摘要、Skill 索引、MCP schema 快照
  → 按模型上下文窗口计算输入预算
  → 必要时自动压缩旧会话
  → Plan（显式开启时）或直接进入工具循环
  → 权限检查与审批
  → 结构化工具执行
  → provider 原生流式响应 / tool call
  → 记录用量、事件、文件快照和分支状态
  → 前端消费文本、工具、计划、团队和压缩事件
\`\`\`

\`internal/agent.Service\` 是流程编排者；Wails \`App\` 方法只负责桌面边界、状态查询和事件转发，不应把协议或文件业务重新写进前端。

## Agent 与工具边界

\`internal/tools\` 中的工具通过 \`Tool\` 接口注册：

\`\`\`go
type Tool interface {
    Name() string
    Description() string
    InputSchema() map[string]any
    Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error)
}
\`\`\`

当前内置工具包括：

- \`update_plan\`
- \`read_file\`、\`file_info\`、\`list_dir\`、\`search\`
- \`write_file\`、\`apply_patch\`、\`copy_file\`、\`delete_file\`
- \`open_file\`
- \`read_repository\`、\`web_search\`
- \`browser\`、\`computer\`
- \`run_command\`、\`terminal\`
- Git 工具、启用的 MCP 远程工具及 `plugin__<id>__<tool>` 插件工具

注册顺序是稳定前缀的一部分。新增工具时应在固定位置注册、为 schema 加测试，并确认只读 Plan 注册表不会意外得到写权限。

文件操作默认走结构化工具。\`run_command\` 只用于构建、测试、编译器和确实需要执行的程序；Agent 不应使用 Shell 代替文件读取、搜索、写入、复制或删除。单程序执行使用 `executable + args[]` 逐参数传递，只有管道等真实 Shell 语法才使用 `command` 字符串模式。

## 插件与办公产物

`internal/plugins` 提供独立进程插件 ABI。第三方插件通过严格的 `echocode-plugin.json` 声明工具、JSON Schema、读写属性、路径参数和权限；运行时使用 JSON-RPC 2.0 JSONL 完成 `initialize` 与 `tools.call`。插件不会作为 DLL 或 Go package 注入主进程。

插件工具按稳定名称 `plugin__<plugin-id>__<tool-name>` 注册到主 Agent、团队和子代理；Plan 只得到只读插件工具。写入型插件调用进入统一审批和工作区互斥，路径在启动进程前解析，生成文件在返回后再次校验并作为可打开产物显示。

内置 `office-artifacts` 工具目录直接调用 `internal/artifacts`：DOCX 与 PPTX 通过受限 ZIP/XML 解析和标准模板生成，XLSX 由 Excelize 读写，旧版 XLS 支持只读解析与转换为 XLSX。`spreadsheet_create` 用声明式结构生成正式工作簿，支持真实公式、样式、合并区域、列宽行高、冻结窗格、下拉验证、打印区域和页面适配；`spreadsheet_write_range` 只负责现有工作簿的小范围修改。生成后 `spreadsheet_inspect` 会返回公式、合并、样式、数据验证与冻结窗格计数，Agent 必须据此做质量复核。

这套能力不调用 COM、ADO 或系统 Office，也不需要 Python 运行时；生成文件已通过 Word、Excel、PowerPoint 兼容性验证。`read_file` 会自动提取办公产物正文，右侧栏使用文档、表格和幻灯片专用视图预览。Access 数据库当前不提供内置支持，后续必须选择独立兼容引擎后再接入，不能恢复为系统 Office 自动化。完整外部插件 ABI 见 [插件开发指南](plugins-development.zh-CN.md)。

## 权限、审批与沙箱

\`RuntimeSettings\` 是权限策略的入口，当前包含：

- \`SandboxMode\`、\`FilesystemAccess\`、\`WorkspaceRoot\`、额外可写目录。
- \`NetworkAccess\`、\`ShellAccess\`、破坏性操作开关。
- 命令超时、内存、CPU 和进程数上限。
- \`ApprovalPolicy\`：工具按请求、失败或策略自动审批。
- Git、浏览器、电脑操控、MCP、插件、团队和记忆设置。

Windows 使用 Job Object 管理命令进程树，可施加资源限制并尝试降低子进程权限。当前 \`Capabilities\` 明确报告：文件系统隔离和网络隔离为 \`false\`。因此这不是虚拟机或容器沙箱；任何危险操作仍必须依赖策略、路径校验和审批。

## 协议层

\`internal/protocol.Provider\` 统一模型列表和流式请求，具体协议负责自己的认证、请求体、SSE/JSON 解码和工具调用格式。当前实现/适配包括：

- DeepSeek 官方协议。
- OpenAI Chat Completions 与兼容供应商。
- OpenAI Responses 风格事件。
- Anthropic Messages 原生协议。
- Gemini \`generateContent\`/流式协议。
- Local provider 测试与本地兜底路径。

供应商配置保存在运行设置中，API Key 只保存到系统密钥库。模型列表返回的上下文窗口优先级为：上游返回值、手动值、精确目录、协议默认、供应商默认、安全兜底。新增或修正模型目录时必须同步 \`internal/agent/model_context.go\`、\`frontend/src/model-context.ts\` 和对应测试。

## 上下文、缓存和压缩

请求被拆成稳定前缀与易变尾部：

- 稳定前缀：系统身份、Agent 规则、推理档位、Skill 索引、MCP schema hash、项目摘要、路由策略。
- 易变尾部：本轮用户输入、最近 diff、工具参数、工具结果摘要、错误和重试信息。

工具结果默认摘要优先，原文留在本地引用。每轮记录输入/输出 tokens、缓存命中/未命中 tokens、命中率和估算费用。

上下文预算根据实际模型窗口扣除输出、工具和安全预留；达到当前推理策略阈值后触发自动压缩。压缩保留稳定 system 前缀、当前请求、完整的 tool call/result 对和最近对话组；原始事件日志不会被压缩破坏，Rewind 仍针对完整历史分支工作。

## 状态与持久化

- \`internal/project\` 保存项目、会话、活动分支和移除项目后的临时工作区迁移。
- \`internal/eventlog\` 使用 append-only JSONL 事件树；\`HEAD\` 指向当前对话线，rewind 后自然形成新分支。
- checkpoint 保存文件快照，支持回退和从消息分叉。
- SQLite 保存模型状态、用量和相关持久数据。
- 长期记忆按项目保存，可在设置中开关并限制会话数和字符数。

不要直接修改事件日志或 SQLite 作为功能实现方式；新增状态应先定义迁移、事件和恢复行为。

## Git、终端、浏览器和电脑操控

- \`workspacegit\` 只接受工作区相对路径，提供状态、diff、审阅 diff、暂存/取消暂存、提交、分支和 worktree 操作。
- \`terminal\` 管理最多 8 个持久会话，实时推送输出，支持停止和进程树清理。
- \`browserengine\` 通过 chromedp/CDP 驱动 Edge/Chrome，支持标签页、导航、点击、滚动、输入、对话框、快照、截图、下载和原生窗口表面。
- \`computercontrol\` 是 Windows 专用的窗口/鼠标/键盘控制层，必须受设置和审批限制。

浏览器不是截图假面：页面读取和交互由 Chromium/CDP 执行；原生窗口表面只是把真实浏览器窗口嵌入工作台的显示层。浏览器依赖本机 Edge/Chrome 和 WebView2，启动失败时 UI 应保留诊断信息。

## 前端与 Wails 合约

后端导出的 \`App\` 方法和 Wails 事件是前后端合约。新增字段时：

1. 先修改 Go 类型和服务测试。
2. 再同步 \`frontend/src/types.ts\` 与状态适配。
3. 为流式事件增加前端回归测试。
4. 运行 Wails 构建以验证绑定生成。

\`task_progress\`、\`context_compression\`、\`team\` 等运行事件不应直接混进助手正文；它们由前端作为独立活动状态或结构化卡片渲染。

## 开发命令

\`\`\`powershell
# 根目录
go mod download
go test ./... -count=1
go vet ./...

# 前端
cd frontend
bun install --frozen-lockfile
bun run check
cd ..

# 桌面
wails dev
wails build -clean
\`\`\`

Windows 下优先使用 \`bun\`。Go 代码变更后至少运行 \`go test ./... -count=1\`；涉及 Wails 或平台代码时追加 \`go vet ./...\` 和 \`wails build -clean\`。

## 已知边界

- 没有真实供应商密钥时只能运行协议 mock、单元测试和本地 provider，不能把 mock 结果当成上游 E2E 结论。
- Windows Job Object 不是文件系统/网络隔离；生产环境仍需外部 OS 沙箱或容器方案才能提供更强边界。
- 非 Windows 的浏览器原生表面、电脑操控和进程限制覆盖较少。
- 模型上下文目录目前由 Go 和前端各维护一份，修改必须双写并测试；后续应改为单一生成源.
- 办公产物会显示为可打开、可结构化预览的文件，但当前文本快照/rewind 不覆盖二进制文档内容。
- 第三方插件默认停用且无权限；当前还没有签名市场，安装本地插件表示用户信任其代码。
