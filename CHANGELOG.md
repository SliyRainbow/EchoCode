# Changelog

本项目的显著变化记录在此文件中。

## v0.3.20 - 2026-08-04

### 思维链显示

- 新增“思考过程”可折叠区块：推理模型输出 `reasoning_content` 时，助手消息顶部实时显示“正在思考…”并滚动展示推理文本；正文开始输出后自动折叠，点击可随时回看。
- 用户手动展开或收起思考区块后，不再被流式状态强制覆盖。
- 推理区块跟随深浅主题，内容区最大高度 320px 并支持内部滚动；历史会话中已持久化的推理内容同样可见。
- 新增前端回归测试，防止思维链渲染链路回退。

### 验证

- 前端 103 项测试、TypeScript 检查与 Vite 生产构建通过。
- Windows amd64 Wails 正式构建通过。

## v0.3.19 - 2026-08-02

### 扩展中心热修复

- 修复扩展详情滚动时从固定工具栏顶部露出的问题，最终主题样式不再覆盖扩展页的零顶部间距。
- 修复窄桌面窗口下左右栏最小宽度超过容器、详情文字向右穿出工具栏的问题。
- 让扩展工作区按详情内容自然撑高，移除会横穿溢出内容的底部分隔线。
- 增加扩展中心布局回归测试，并在深色、浅色主题下完成实际滚动验证。
- 标题栏直接使用 Wails 注入的运行时接口，干净检出无需本地生成的 `wailsjs` 目录即可完成前端检查。
- 修复 Windows Runner 跨盘临时目录下结构化搜索路径失真，并改进 Job Object 子进程终止状态判断。

### 验证

- 前端 47 项 UI 回归测试通过。
- TypeScript 检查与 Vite 生产构建通过。
- Windows amd64 Wails 正式构建通过。

详细发布说明见 [docs/releases/v0.3.19.md](docs/releases/v0.3.19.md)。

## v0.3.18 - 2026-08-02

### 官方扩展中心

- 新增统一的扩展中心，支持浏览、搜索、安装、更新、卸载和查看 MCP、插件与 Skills 扩展。
- 接入官方扩展源 `SliyRainbow/EchoCode_Extensions`，首个可安装扩展为 CodeGraph 1.5.0。
- CodeGraph 安装后自动写入 MCP 配置并重新连接，可在扩展详情中建立、同步和检查当前项目索引。
- 扩展下载固定版本并校验 SHA-256，增加缓存回退、压缩包路径穿越防护、解压体积限制、健康检查和原子安装。
- Windows 正确启动 `.cmd` / `.bat` MCP，并隐藏子进程控制台窗口；卸载扩展时保留项目内 `.codegraph` 数据。

### Agent 与交互

- Agent 执行过程中实时显示权限确认、模型路由、上下文组装、连接、首个事件、首段文字和工具执行等阶段及耗时。
- CodeGraph 工具动作使用中文名称和摘要，并按当前会话工作区提供只读代码关系分析。
- 新增受保护的编码凭据解码工具，明文只进入本机安全卡片，不进入模型上下文、普通消息、Shell 或日志。
- 改进暂停的 AI 团队任务、工具时间线和阶段耗时在会话历史中的恢复。

### 发布与验证

- 新增 UTF-8 中文发布说明流程，避免 GitHub Release 更新内容乱码。
- Go 全量测试、前端 102 项测试、TypeScript 检查、Vite 生产构建和 CodeGraph 真实发布包集成测试通过。

详细发布说明见 [docs/releases/v0.3.18.md](docs/releases/v0.3.18.md)。

## v0.3.17 - 2026-08-01

### Windows 发布修复

- 恢复原版 EchoCode 应用图标，修复可执行文件和便携版压缩包中的图标显示问题。
- 同步更新应用版本号、Wails 产品版本和 MCP 客户端版本。
- 更新前端依赖版本信息，重新生成 Windows amd64 便携版发布产物及 SHA-256 校验文件。

详细发布说明见 [docs/releases/v0.3.17.md](docs/releases/v0.3.17.md)。

## v0.3.16 - 2026-08-01

### 使用统计、界面与 Agent

- 新增官方供应商 API Key 的使用量同步、费用统计和手动校准入口。
- 新增无边框标题栏，调整使用统计、设置页和动态缩放布局。
- 改进模型路由、推理强度、MCP 客户端版本和 Windows 路径权限处理。
- 通过 Go、前端和 Windows amd64 生产构建验证。

详细发布说明见 [docs/releases/v0.3.16.md](docs/releases/v0.3.16.md)。

## v0.3.15 - 2026-07-31

### Agent 执行与事件恢复

- Agent 的工具动作、进度、终态和子代理状态统一写入可恢复事件流，切换会话、停止后继续及应用重启后仍能还原当前工作状态。
- 子代理默认并行执行，主 Agent 可继续处理互不依赖的工作，仅在最终综合结果时等待；补充子代理注册、资源冲突和取消隔离测试。
- 上下文压缩保留计划、失败原因、当前产物、运行状态和未完成工作，避免长任务压缩后重新开始或丢失刚创建的文件。

### 工具与任务边界

- 新增结构化 `grep` 与 `glob` 工具，文件检索不再依赖容易受 Windows 引号和编码影响的 Shell 拼接。
- 为文件、Shell、Git、MCP 和插件调用补充本轮任务范围检查、资源锁与执行事件；创建或修改任务没有真实产物时不再伪报完成。
- 保留完整工具错误、退出码、耗时和产物证据，改进等价失败识别、无进展检测及停止后的任务收尾。

### 验证

- `go test -count=1 ./...` 与 `go vet ./...` 通过。
- 前端 96 项测试、TypeScript 检查和 Vite 生产构建通过。
- Wails Windows amd64 生产构建通过。

## v0.3.14 - 2026-07-30

### 内置浏览器

- 原生 WebView2 标签改为独立的子窗口承载，并在对应浏览区域显式置顶，避免被 EchoCode 自身 WebView 覆盖后出现地址栏已更新、内容区域纯白的问题。
- 原生模式下的导航直接交给可见 WebView2 标签执行，CDP 仍用于 Agent 检查和自动化，避免控制目标与实际可见页面脱节。
- 新增真实 WebView2 回归测试，覆盖创建可见标签、挂载原生窗口、导航本地网页及读取页面标题的完整路径。

### 输入区自适应

- 输入框底部的图片、Markdown、撤销、权限、计划和 AI 团队控件会按聊天栏实际宽度换行；右侧面板拖宽后不再相互覆盖或跑出输入框。
- 模型、推理和发送控件在窄聊天栏中独立占用稳定行，仍保持完整可点击区域。

### 验证

- 前端 96 项测试、TypeScript 检查和 Vite 生产构建通过。
- `go test ./...`、`go vet ./...` 及 Windows WebView2 真实集成测试通过。
- Wails Windows amd64 生产构建通过。

## v0.3.13 - 2026-07-30

### 对话活动归类

- 将连续的文件读取、目录查看、代码搜索、网络搜索、网页读取和仓库读取归入一条“查阅了 N 项资料”摘要，默认折叠，展开后仍可查看每一项真实工具操作及其详情。
- 大折叠使用首个工具调用的稳定身份保存展开状态；已有分组收到新输出时不会自动收起，新出现的分组仍保持默认折叠。

### 流式渲染

- 消息、内容块和活动批次按稳定 ID 复用原有 DOM，模型每生成一个字时不再重建整条消息。
- 修复生成圆圈和工具圆圈随每个 token 从头播放的问题，降低长回复期间的界面闪动与重复渲染。

### 验证

- 前端 95 项测试、TypeScript 检查和 Vite 生产构建通过。
- `go test ./...` 全部通过。
- Playwright 真实页面验证大折叠、展开明细及流式更新期间圆圈 DOM 身份保持不变，无控制台错误。

## v0.3.12 - 2026-07-29

### 内置浏览器热修复

- 修复 Microsoft Edge WebView2 150 中内置浏览器偶发报错 `start embedded WebView2 browser: connect to embedded WebView2: context canceled` 并无法打开的问题。
- 启动时先从 WebView2 调试端点识别 EchoCode 已创建的 bootstrap 页面，再让 `chromedp` 附加该页面，避免默认发送会导致 WebView2 进程访问冲突崩溃的 `Target.createTarget(newWindow=true)`。
- 新增 bootstrap 目标识别测试和可显式运行的真实 WebView2/CDP 集成测试，覆盖控制器创建、目标发现、附加及完整协议初始化。

### 验证

- `go test ./...` 全部通过。
- Windows WebView2 150 真实运行时集成测试通过。
- Linux amd64 浏览器引擎交叉编译通过。

## v0.3.11 - 2026-07-29

### Agent 执行与恢复

- 明确“模型负责理解和决策、Agent 宿主负责受控执行”的边界，移除依赖普通对话关键词替模型选择流程或拼接答案的逻辑。
- 工具结果向模型保留脱敏参数、工作目录、退出码、耗时、标准输出、错误输出及已有文件、差异和产物证据，失败后可以真正分析原因并更换方案。
- 长任务不再使用固定工具调用次数上限；通过等价失败检测、无进展检测、工具超时、任务空闲看门狗和快速取消防止死循环与永久卡住。
- 修复只有私有推理却没有可见结果时仍保留错误气泡、完成后被迟到事件改回运行中，以及停止、切换会话或重启后任务状态丢失的问题。

### AI 团队、子代理与多会话

- 子代理支持受控并发、独立状态、只读权限、单独停止和父任务取消后的迟到输出隔离；主 Agent 可以继续执行不依赖子代理的工作。
- AI 团队使用明确审阅协议，角色失败会保存已有工作、计划和团队检查点，不再由宿主伪造成功结论。
- 暂停团队任务改为显式继续或放弃；恢复失败也会持久化并在切换会话、重启后正确显示。
- 多会话后台任务共享最新模型设置、协议兼容状态、MCP 资源和工作区写入门，允许并行推理和读取，同时避免冲突写入。

### Markdown、Skills 与上下文

- 对话输入区支持加载 `.md` 和 `.markdown` 作为当前任务参考资料，并支持排队、失败恢复、编辑重发和历史重建。
- 设置页支持导入长期 Markdown 规则；用户导入的规则每轮自动进入稳定上下文，旧版已导入文件无需重新导入。
- 明确区分“本轮参考资料”和“长期规则”，并在 Skills 页面显示来源、触发方式、正文查看、系统打开、文件定位和启停状态。
- Skill 正文改用按推理强度分配的 token 预算，显式点名优先，不再限制为固定数量。

### 远程操作、安全与界面

- 密码 SSH 使用本机托管的不透明凭据引用，支持在授权服务器上执行命令及安全找回账号、密码或令牌；账号与密码在界面合并为一张受保护登录卡片。
- 移除针对特定部署场景的硬编码流程，服务器检查、工具选择、重试和交付内容由模型根据真实证据决定。
- 对话时间线持续显示可核验进展、工具动作、命令详情、重试、失败和耗时，同时过滤私有推理、重复进度参数及无意义心跳。
- 工具部分失败时继续保留已经生成的文件与产物登记，后续轮次可以复用准确路径，不再重新遍历目录猜测位置。

### 验证

- `go test ./...` 全部通过。
- 前端 92 项测试、TypeScript 检查和 Vite 生产构建通过。
- Wails 2.12.0 Windows amd64 生产构建通过。

## v0.3.10 - 2026-07-28

### Cleaner live task status

- Keep a single `正在执行任务` status with elapsed time while a task is active.
- Hide provider heartbeat notices and routine setup phases from the conversation timeline without disabling timeout monitoring.
- Prevent heartbeat state from reappearing after switching conversations or restoring a background task.
- Filter routine status rows from existing history while preserving model progress, tool activity, retries, and failures.

### Verification

- All Go package tests, `go vet`, 84 frontend tests, TypeScript checks, Vite production build, and Wails production build pass.

## v0.3.9 - 2026-07-28

### Agent execution timeline and remote operations

- Keep provider wait heartbeats in the live task status instead of persisting a new conversation row every 8 seconds.
- Preserve streamed Agent progress text before each tool batch so the conversation shows the real explanation, action, and result order while work is still running.
- Hide raw `update_plan` calls from the conversation, keep the plan above the composer, and aggregate adjacent command and repository actions into compact expandable rows.
- Recognize Chinese credential-recovery wording such as `找回` and `秘钥`, automatically verify the authorized password-based SSH target, and require target-host evidence before local commands, web search, repository reads, or delegation.
- Stabilize Windows process-tree and persistent-terminal timing tests under loaded CI runners without weakening cancellation assertions.

### Windows artifact path identity

- Canonicalize existing and deleted artifact paths through their nearest existing ancestor so Windows 8.3 short names and long names resolve to one durable identity.
- Canonicalize the workspace root before deriving display paths, preserving relative labels when the workspace was supplied through a short-path alias.
- Keep artifact creation, deletion, external downloads, render restoration, and visual inspection on the same path key across restarts and clean CI environments.

### Verification

- Add Windows short/long path alias, remote SSH routing, provider-heartbeat, live timeline, process-tree, and persistent-terminal regressions.
- All local Go package tests, `go vet`, 83 frontend tests, and the production frontend build pass; GitHub Actions verification is required before publishing this patch release.

## v0.3.8 - 2026-07-28

### Agent reliability and recoverability

- Add durable task runtime states, watchdog timeouts, fast cancellation, crash recovery, and progress-aware strategy switching so failed tools cannot leave a conversation permanently running.
- Preserve project identity, sessions, branches, plans, team checkpoints, long-term memory, and prompt-cache continuity when a project is removed and later re-added.
- Register created and modified artifacts with stable paths, tool ownership, validation state, previews, and branch-aware rewind/restore context.
- Restore live progress notes, tool activity, reasoning, elapsed time, and terminal states when switching conversations or restarting the application.

### Artifacts, plugins, and visual verification

- Add built-in DOCX, XLS/XLSX, and PPTX creation, editing, inspection, conversion, and right-side structured previews without requiring Microsoft Office.
- Add a permissioned plugin ABI and management surface for bundled and external JSON-RPC/JSONL workers.
- Add isolated artifact rendering and visual inspection for images, HTML, PDF, Office documents, browser pages, and authorized windows, with source-hash invalidation and explicit degraded states.

### Protocols, tools, and Windows safety

- Add Anthropic model capability discovery and persistence, legal thinking/effort mapping, unsupported-parameter learning, and model-specific context/output limits.
- Add structured downloads and Git clone/fetch/pull with progress, cancellation, retries, checksums, atomic writes, credential redaction, and authorized external-drive paths.
- Harden Windows command argument handling, process-tree cancellation, structured errors, DPAPI-backed secret recovery, and task-level duplicate-failure detection.

### Skills and interface

- Replace broad Skill keyword matching with explicit, manual, and legacy-compatible activation modes; generic Agent, Plan, cache, protocol, and MCP requests no longer inject EchoCode internals.
- Reduce the core Agent Skill from about 3882 to 631 estimated tokens, split Office guidance into its own Skill, and expose per-turn Skill injection diagnostics.
- Add artifact viewing, richer execution disclosures, plugin and Skill controls, task-state restoration, and context diagnostics across the desktop interface.

### Verification

- Both bundled Skills pass the standard Skill validator.
- All Go package tests, `go vet`, TypeScript checking, 79 frontend tests, the production Vite build, and a clean Wails Windows/amd64 build pass.

## v0.3.7 - 2026-07-25

### Reliable stream finalization

- Treat a provider `finish_reason` as a semantic completion fallback when a broken OpenAI-compatible relay omits `[DONE]` and leaves the HTTP connection open.
- Keep the normal `[DONE]` and EOF paths immediate, preserve a short grace period for trailing usage, and cancel the stalled transport afterward.
- Preserve typed provider errors after semantic finish while tolerating transport errors caused by closing an already completed stream.
- Honor Gemini `finishReason` and merge fragmented usage fields without losing prompt, completion, or cache token counts.

### Live request usage

- Refresh token, cache-health, DeepSeek-session, diagnostics, and usage-ledger state after every model request in normal Agent, Plan, tool-loop, and AI-team execution.
- Count every dynamic-subagent model request instead of retaining only its final usage sample.
- Patch only usage-related frontend state so background task events cannot replace the visible conversation state.

### Verification

- Add real HTTP/SSE regressions for a finished stream that never sends `[DONE]`, trailing fragmented usage, immediate normal completion, and typed post-finish errors.
- All Go package tests, `go vet`, frontend type checking, 73 frontend tests, and the production Vite build pass.

## v0.3.6 - 2026-07-24

### True parent-child parallelism

- Make `delegate_task` start dynamic subagents in the background and return task IDs immediately instead of blocking the primary Agent.
- Add `await_subagents` for explicit status queries and final collection, with automatic collection before synthesis when the model omits it.
- Let the primary Agent continue independent reads, analysis, and tool work while up to three subagents run concurrently.
- Keep fixed cumulative tool-call limits removed; the three-worker cap only bounds concurrent model fan-out.

### Lifecycle and live status safety

- Join every child before parent commit or rollback, cancel children with the parent, and preserve independent per-child cancellation.
- Serialize workspace-mutating tools while leaving read-only work and model inference concurrent.
- Stream subagent progress as dedicated events so a completed `delegate_task` cannot regress to a long-running tool card.
- Preserve terminal subagent and tool states against late progress events, while returning child artifacts and usage exactly once.

### Verification

- Add regressions for immediate delegation, simultaneous workers, parent work during a blocked child, fan-out limits, released slots, cancellation, automatic collection, event ordering, and artifact deduplication.
- All Go package tests, `go vet`, frontend type checking, 72 frontend tests, and the production Vite build pass.

## v0.3.5 - 2026-07-24

### Progress-aware loop safety

- Keep the fixed cumulative tool-call limit removed for the main Agent, Plan mode, AI team, and dynamic subagents.
- Detect repeated no-progress tool rounds using normalized call arguments and stable result fingerprints rather than a task-length budget.
- Stop identical rounds and repeating two- or three-round cycles after three repetitions, while allowing long polling tasks whose results continue to change.
- Disable tools and request a final summary when a cycle is detected; preserve all partial output, file changes, and task activity.
- Stop safely if an upstream provider ignores `tool_choice: none` instead of executing another tool call.

### Verification

- Add regressions for identical loops, alternating loops, changing polling results, and providers that ignore disabled tools.
- Retain the 40-call long-task and 20-update plan regressions; all Go package tests and `go vet` pass.

## v0.3.4 - 2026-07-24

### Parallel subagents

- Start explore, review, and implement subagents concurrently within one delegation, while requiring non-overlapping file ownership for parallel implementation work.
- Add independent child cancellation so one subagent can be stopped without interrupting siblings or the coordinating parent task; cancelling the parent still stops every child.
- Stream and persist each subagent's output, reasoning, tool activity, status, timing, and file-change statistics across conversation switches and reloads.
- Show running subagents above the composer and provide a dedicated read-only output tab in the right-side workspace panel.

### Open-ended Agent execution

- Remove fixed tool-call counts from the main Agent, Plan mode, AI team roles, dynamic subagents, and managed remote credential tasks.
- Keep reasoning levels focused on model reasoning, context, cache, and planner policy instead of silently shortening long tasks.
- Continue to enforce cancellation, per-tool timeouts, context compression, approvals, sandbox policy, and duplicate-call guards.

### Verification

- All Go package tests and `go vet` pass, including concurrent child execution, independent cancellation, parent cancellation, event-log round trips, and a 40-call long-task regression.
- Frontend type checking, 72 tests, production Vite build, and Wails Windows/amd64 production build pass.

## v0.3.3 - 2026-07-24

### Dynamic subagents and Agent execution

- Add true dynamic subagents through `delegate_task`, with independent context, model routing, cancellation, persistence, and bounded summaries returned to the primary Agent.
- Run read-only exploration and review workers concurrently while serializing implementation workers to avoid workspace write conflicts.
- Render live subagent status, current actions, steps, model information, duration, and changed-file statistics independently from the fixed AI team workflow.
- Preserve expanded command and artifact disclosures while new streamed activity remains collapsed by default.

### Authorized remote deployment

- Prefer direct managed SSH operations for authorized deployment tasks instead of wasting turns on unrelated local discovery.
- Add host-managed sensitive-result capture so requested remote account values can be revealed or copied by the user without exposing plaintext to the model, prompts, or event logs.
- Improve SSH command classification, progress reporting, cancellation, deployment guidance, and regression coverage.

### Skills and interface

- Add a full Skill detail viewer with document and source modes, safe file actions, and persistent sliding enable controls.
- Improve code and diff disclosure behavior, structured execution cards, and conversation timeline readability.

### Verification

- All Go package tests and `go vet` pass.
- Frontend type checking, regression tests, and the production Vite build pass.

## v0.3.2 - 2026-07-24

### Secure password-based SSH

- Support direct password-based SSH login from host, username, and password input without requiring an SSH key, `ssh-agent`, or an external provider authorization entry.
- Store the password in Windows Credential Manager and pass only an opaque host-managed reference to the Agent and SSH tool.
- Restore valid credential references across compressed history and short follow-up messages while keeping the password out of prompts, logs, plans, and conversation history.
- Accept compact input such as `P:host username:user password:secret` and redact the secret in the composer preview.

### Verification

- Go tests and vet, frontend type checking and tests, production frontend build, and Wails Windows build pass.

## v0.3.1 - 2026-07-24

### Agent execution and remote operations

- Add host-managed SSH connection testing, remote command execution, and file/directory upload tools. Passwords are stored in Windows Credential Manager and are never exposed to the model or command line.
- Make task cancellation immediate, with forced cleanup after a short grace period so stalled provider or tool calls cannot leave a conversation permanently stopping.
- Preserve structured progress, tool results, provider events, and terminal details in the conversation timeline.

### Conversation safety and recovery

- Redact SSH credentials from optimistic messages, queued messages, session history, and Agent context while retaining an opaque credential reference for authorized operations.
- Correct failed and interrupted turn handling so uncommitted messages return to the composer only when the model produced no usable output.
- Keep ordinary provider failures distinct from upstream security-policy errors and preserve the correct terminal state across conversation switches.

### Verification

- Go tests and vet, frontend type checking and tests, production frontend build, and Wails Windows build pass.

## v0.3.0 - 2026-07-22

### Agent 与模型协议

- 推理强度扩展为关闭、轻度、中、高、很高和极高，并按供应商协议与模型名称动态映射；OpenAI Responses、DeepSeek、Anthropic、Gemini 和 xAI 分别发送受支持的原生字段。
- 完善 Anthropic/Gemini 原生工具循环、自动上下文压缩、AI 团队任务恢复和中断后继续执行。
- 工具调用增加持久化耗时、工作目录、退出码和完整 Shell 输出；对话中默认折叠详细执行记录。
- 新增自动化任务调度、手动运行/停止，以及可持久化的项目、会话、供应商和模型路由。

### 多项目与会话

- 对话任务、历史、草稿、消息队列、团队状态和右侧面板统一按 `projectID + sessionID` 隔离，可在后台生成时切换并继续其他会话。
- 修复不同项目存在同名或重复 ID 会话时无法打开、历史清空或操作错会话的问题。
- 会话新增右键菜单，支持重命名、归档、打开项目目录、复制工作目录/会话 ID 和永久删除。
- 发送失败会回滚乐观消息并恢复输入内容；停止、编辑重发、rewind 和分叉流程补齐状态恢复。

### 工作区与界面

- 新增右侧项目文件树、CodeMirror 代码查看器、对话内代码/差异预览和文件修改摘要。
- 计划与 AI 团队状态改为输入框上方独立居中展示，并完善滚动到底、主题化确认框、推理菜单定位和动态 DPI/字体设置。
- 设置中心新增自动化任务和关于页面，支持版本检查、自动下载与应用更新。
- 内置浏览器改用可交互的原生 WebView2 表面，改进标签管理、输入法定位、缩放、页面清晰度和设置页遮挡行为。

### 验证

- 全部 Go 包测试通过。
- 前端类型检查、61 项测试和生产构建通过。
- Wails Windows/amd64 正式构建及隔离配置冷启动冒烟通过。

## v0.2.1 - 2026-07-20

### 改进

- 新增基于 CodeMirror 的只读代码查看器，非 Git 工作区也可从对话和工具结果直接查看文件及定位行号。
- 浏览器与文件查看器合并为统一右侧标签面板，切换文件时保留网页、浏览器标签和页面状态。
- 生成文件改为可折叠条目，HTML 文件支持内置预览或外部浏览器打开。
- 优化工作区文件路径识别、结构化文件读取及 Windows 文本编码处理。
- 修复 Windows Git 操作弹出控制台窗口的问题，并补充相关回归测试。

### 验证

- 前端类型检查、42 项测试和生产构建通过。
- 全部 Go 包测试通过，并完成浏览器/文件标签切换、宽度拖拽及独立关闭的运行态验证。

## v0.1.0 Preview - 2026-07-20

EchoCode 首个公开 Windows 预发行版。

### 主要能力

- DeepSeek、OpenAI Compatible/Responses、Anthropic 和 Gemini 多协议模型接入。
- 自定义供应商、密钥库、上游模型发现和模型上下文窗口管理。
- 结构化文件工具、Git 工作区、持久终端、网络搜索和远程仓库读取。
- Plan 模式、AI 团队、任务进度、运行中引导和消息排队。
- 项目长期记忆、checkpoint、rewind、对话分支和自动上下文压缩。
- Chromium/CDP 内置浏览器、文件预览、图片粘贴与 Windows 窗口控制。
- Windows Job Object 进程树管理、资源限制和权限审批。

### 已知限制

- 这是 Preview 版本，尚未完成安装包签名、自动升级和真实多供应商长期压测。
- 当前主要验证 Windows；其他平台的原生浏览器表面和电脑操控能力有限。
- OS 级沙箱尚不提供文件系统或网络隔离，这两项由路径策略和用户审批约束。
- 可执行文件暂未进行代码签名，Windows 可能显示未知发布者提示。
