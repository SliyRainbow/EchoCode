# EchoCode OpenHands SDK 对照改造计划

更新时间：2026-07-30

本文档是对 `OpenHands Agent Canvas` 与 `software-agent-sdk 1.39.1` 的只读审计后形成的执行计划。它不引入 Python、LiteLLM、FastAPI、Electron 或 OpenHands 的 Docker 服务栈；目标是在保留 EchoCode 的 Go、Wails、原生协议、Windows 工具和现有事件树的前提下，吸收被验证有效的运行时设计。

相关长期清单：`docs/agent-reliability-plan.zh-CN.md`。本文件只跟踪本次事件内核、上下文视图、取消闭合、并发资源和子代理改造，完成后应同步更新两份文档。

## 已确认的基线

- EchoCode 已有 append-only 事件树、`Seq`、分支、rewind、产物登记、失败指纹、看门狗、独立会话运行时、子代理和原生模型协议。
- 现有主要风险是执行事实、模型上下文、运行时状态和 UI 事件仍有多条并行链路，切换会话、停止、压缩、恢复和流式更新时可能互相覆盖。
- OpenHands 最值得借鉴的是强类型事件、原始历史与 Context View 分离、事件式压缩、取消后补齐工具结果和资源感知并发；不是其技术栈或默认权限策略。

## 目标架构

```text
持久化 RawEvent（唯一事实源）
  -> ContextView（当前分支、模型可见、可压缩）
  -> UIPresentation（时间线、折叠组、侧栏、运行状态）
```

`RawEvent` 永不因压缩、停止或 UI 切换而删除。`ContextView` 不得拆开工具调用与结果。`UIPresentation` 可以被缓存和丢弃，但必须仅由 RawEvent 重建。

## 状态定义

- `未开始`：无实现。
- `进行中`：已有代码或当前正在修改，但验收未完成。
- `待验证`：实现和单元测试已完成，缺少端到端或真实桌面验证。
- `已完成`：实现、回归测试和异常路径均已验证。
- `阻塞`：需要外部环境或用户决策。

## 工作总览

| 编号 | 工作包 | 优先级 | 状态 | 验收核心 |
| --- | --- | --- | --- | --- |
| A | 版本化强类型执行事件 | P0 | 进行中 | 旧事件可读取；新事件包含稳定头和类型化 payload |
| B | Context View 与事件式压缩 | P0 | 进行中 | 压缩不破坏 tool call/result、分支、rewind 或产物登记 |
| C | 取消闭合与运行 generation | P0 | 进行中 | 停止后无孤立工具、无迟到写入、无重复终态 |
| D | 工具资源声明与结构化搜索 | P0 | 待验证 | 独立 grep/glob；资源协调器已通过全仓回归，待桌面端到端回归确认 |
| E | 子代理上下文、持久化与工作区协调 | P0 | 待验证 | 主代理可继续；重启可恢复状态；同文件不覆盖 |
| F | 事件恢复与桌面 E2E | P0 | 未开始 | 切换、断流、停止、压缩、重启和并行任务均可重放 |
| G | 渐进式 Skills、插件与 MCP 生命周期 | P1 | 未开始 | 按需加载、版本来源、健康检查、Secret 与动态 schema |
| H | 可选 Worktree/远程执行后端 | P1 | 未开始 | 保留本地默认模式，隔离模式可创建、回收和诊断 |

## A. 版本化强类型执行事件

### 目标

保留 `internal/eventlog` 的 JSONL、分支和严格 `Seq`，但引入公共事件头和类型化 payload，逐步替换大型扁平 `EventPayload`。

### 设计

公共头至少包含：

- `schemaVersion`
- `id`、`parentId`、`seq`
- `projectId`、`sessionId`、`branchId`
- `runId`、`generation`
- `causationId`、`toolCallId`
- `ts`、`kind`

首批事件种类：`user_message`、`assistant_delta`、`assistant_completed`、`tool_started`、`tool_output`、`tool_completed`、`tool_failed`、`turn_interrupted`、`context_condensed`、`task_terminal`。

### 验收

- [ ] 旧 `events.jsonl` 不迁移也可读取。
- [ ] 新事件未知字段可安全忽略，未知种类可保留而不导致会话失败。
- [ ] 前端按 `event ID + Seq` 去重，不依赖文本内容匹配。
- [ ] 同一 run 的迟到事件不会覆盖更高 generation 的状态。

## B. Context View 与事件式压缩

### 目标

从 RawEvent 派生当前分支的模型上下文，不再由多个调用点直接拼接历史。压缩写入 `context_condensed` 事件，摘要保存来源事件范围与关键结构化事实。

### 验收

- [ ] 原始消息、工具结果、文件快照和产物记录永久保留。
- [ ] Context View 不允许在 tool call/result、并行批次或 provider thinking/tool 边界中间切割。
- [ ] 压缩、rewind、分叉和“继续”后可重建相同 Context View。
- [ ] 稳定 prompt 前缀、Skills 索引和 MCP schema 快照不因压缩重排。

## C. 取消闭合与运行 generation

### 目标

将运行状态转换集中到宿主状态机：

```text
running -> waiting/retrying -> cancelling -> interrupted|failed|completed
```

停止必须立即使 generation 失效，随后取消模型流、Shell/终端进程树、MCP、插件、浏览器和子孙任务。没有返回结果的工具调用必须写入合成 `interrupted` 结果，保证恢复后的模型历史完整。

### 验收

- [ ] 停止在 200ms 内显示 `cancelling`，最终只有一个持久化终态。
- [ ] 取消后迟到的输出、文件写入、子代理回传和终态全部被拒绝。
- [ ] 继续任务能看到最后一个完整工具结果、计划、产物和中断原因。
- [ ] 进程不响应 Context 时仍会尝试强制清理其进程树或连接。

## D. 工具资源声明与结构化搜索

### 目标

工具声明资源键，例如 `file:<canonical-path>`、`terminal:<session>`、`browser:<tab>`、`remote:<host>`。调度器允许读和互不冲突的资源并行，同资源写入必须串行。

### 验收

- [ ] `grep`、`glob` 成为一等结构化工具，优先 `rg`，提供路径、行号、片段、截断和超时信息。
- [ ] 文件、Shell、终端、Browser、Computer、MCP 和插件全部经过统一能力、路径、审批和取消检查。
- [ ] 不同文件的安全读写可并行；相同文件、同一终端、同一浏览器标签不得并发破坏状态。

## E. 子代理上下文、持久化与工作区协调

### 目标

保留 EchoCode 的非阻塞 `delegate_task` 优势，但不再复制完整父会话。子代理使用独立任务上下文，并持久化定义、父子关系、事件、产物、尝试、结果和 checkpoint。

### 验收

- [ ] 主代理和多个子代理可真实同步推进；仅在显式等待或综合时 join。
- [ ] 单独停止子代理不影响兄弟任务，父任务停止传播到全部后代。
- [ ] 应用重启后运行中的子代理明确恢复为可继续或已暂停，不可静默丢失。
- [ ] 同一工作区写入经过资源锁；高风险任务可选独立 Git worktree。

## F. 事件恢复与桌面 E2E

### 必测场景

- [ ] 流式输出中切换会话再切回。
- [ ] 模型已有输出后停止，用户气泡不回退输入框。
- [ ] 停止 Shell、浏览器、MCP、插件和子代理后的迟到事件。
- [ ] `finish_reason` 后未 `[DONE]`、断线重连、慢订阅者和重复事件。
- [ ] 压缩、rewind、分叉和项目移除后重新挂载。
- [ ] 多会话同时运行、多个同名新对话、同路径项目恢复。
- [ ] 子代理并行、同文件冲突、独立停止和重启恢复。

## 实施顺序

1. 先完成 A 的兼容事件头和 D 的结构化 grep/glob，避免后续功能继续把状态塞进大 payload。
2. 完成 C 的 generation、取消闭合和孤立工具结果，先消除最危险的停止恢复错误。
3. 实现 B 的 Context View 和安全压缩边界。
4. 实现 E 的最小子代理注册表、上下文裁剪和工作区协调。
5. 用 F 建立 Mock Provider + Wails 端到端矩阵，再逐项把状态升级为 `已完成`。

## 本轮执行记录

- [x] 完成 Agent Canvas 与 software-agent-sdk 静态审计。
- [x] 确认不迁移 Electron/Python/FastAPI/Docker 服务栈。
- [x] 确认 EchoCode 现有 `Seq` 优于时间戳游标，应作为事件恢复游标。
- [x] A：为旧 JSONL 保持兼容地新增 `schemaVersion/projectId/sessionId/branchId/runId/generation/causationId/toolCallId` 事件头；Agent 的消息、计划、团队、产物、视觉和分支事件已通过统一入口写入真实身份。
- [x] A：新增 10 类强类型执行事件的 envelope、payload 校验与未知 kind 回读；尚未把所有旧 `EventPayload` 写入点迁移为 typed payload。
- [x] B：自动压缩写入 `context_condensed`，内容寻址快照保存 Context View；重启/切换分支时从当前链上的快照重建，原始消息事件不删除。
- [x] C：任务 generation 进入运行快照；取消先持久化 `cancelling`，迟到流事件被拒绝；工具取消写入明确的 `cancelled` 终态。
- [x] D：`grep`/`glob` 已成为主 Agent、Worker 和 Plan 的一等结构化只读工具，支持 `rg` 优先、Go 回退、超时和稳定 JSON。
- [x] D：资源协调器已覆盖文件、目录、工作区、浏览器、电脑、终端、SSH、计划和子代理；Shell、Git、MCP 与插件对副作用不透明时保留工作区级保守锁。
- [x] E：子代理使用有界父上下文并将定义、状态、产物、generation、checkpoint 和恢复信息原子写入会话登记表；重启后显式标为待恢复，不静默丢失。
- [ ] F：Mock Provider 与 Wails 桌面端 E2E 矩阵尚未建立。

## 2026-07-30 增量验收

- [x] `go test -count=1 ./internal/tools`、`./internal/agent`、`./internal/eventlog` 与应用层定向回归通过（在资源锁子任务收尾前）。
- [x] `go test ./...` 和 `go vet ./...` 已在第一批基础改造后通过。
- [x] 已执行全仓测试、`go vet`、前端检查和 Windows 生产构建；Mock Provider/Wails 桌面 E2E 未完成前，当前仍不标记发布就绪。

## 2026-07-30 资源协调器验收补充

- [x] D：资源协调器已接入统一工具执行入口，并由同一工作区的 detached runtime 共享。
- [x] D：`file:`、`dir:` 和 `workspace:` 采用父子路径冲突判断。不同文件可以并行；同一文件、目录扫描与其子文件写入会串行。
- [x] D：`download_file` 在文件名确定时锁定精确目标文件，文件名由服务端决定时锁定目标目录；`git_repository` 只按本地 clone/pull 目标锁定，不会把远程 URL 当成本地路径。
- [x] D：已覆盖不同文件并行、同文件串行、目录读/子文件写冲突、等待资源锁取消不执行和 detached runtime 共享协调器。
- [x] C：取消前已进入 `delegate_task` 的调用会保留一次同步收尾机会，持久化“已取消”的子代理状态，不再丢失子代理卡片。
- [x] 验证：`go test -count=1 ./...`、`go vet ./...`、`bun run check` 和 `wails build -clean` 均通过。
- [ ] F：Mock Provider 与 Wails 桌面 E2E 矩阵仍未实现，不能用单元测试替代真实桌面切换、流断开和浏览器回归。

## 2026-07-31 任务范围与真实产物验收

- [x] 用户明确指定目录或文件时生成本轮 `task_scope`，并随私有上下文、detached runtime 和子代理传递；目标不存在时允许在目标内创建，不允许读取或写入兄弟项目。
- [x] `read_file/list_dir/search/grep/glob/write_file/apply_patch/copy_file/delete_file/download_file/git_repository` 均通过统一 `SandboxPolicy` 校验本轮范围。
- [x] `run_command` 必须从目标目录执行；工作区根目录、兄弟目录、父级穿越和范围外绝对路径会在启动进程前被拒绝。该约束在全局文件权限为 `unrestricted` 时仍生效。
- [x] 工作区级 Git 与持久终端在局部任务中不注册；缺少宿主可验证路径边界的 MCP 工具暂不暴露；文件类插件必须为每种文件权限声明路径参数并经过同一策略校验。
- [x] Git clone 可创建声明目标的祖先目录，但不能借此创建兄弟目录。
- [x] 子代理查询默认非阻塞；主 Agent 可继续独立工作，仅在最终综合阶段显式等待尚未结束的子代理。
- [x] 创建或修改类请求必须产生目标范围内的真实文件变化，否则进入失败终态，不接受模型文本中的“已完成”作为产物证据。
- [x] `internal/tools`、Agent 任务范围/MCP/插件定向回归与 `internal/plugins` 全量测试通过。
- [x] 本批次 `go test -count=1 ./...`、`go vet ./...`、前端 96 项测试/类型检查/Vite 构建及 `wails build -clean` 全部通过；Windows 产物为 `build/bin/EchoCode.exe`。
- [ ] F：Mock Provider 与 Wails 桌面 E2E 矩阵仍未实现。
