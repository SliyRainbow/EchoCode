# EchoCode

EchoCode 提供 MCP、插件和 Skills 扩展能力。社区扩展源仍在建设中，当前可使用本地扩展及自行配置的服务；CodeGraph 接入说明见 [docs/codegraph.zh-CN.md](docs/codegraph.zh-CN.md)。

EchoCode 是一个本地优先的 AI 编程 Agent 工作台。它把多模型协议、结构化开发工具、权限审批、Plan、AI 团队、项目记忆、对话分支、Git、持久终端和内置浏览器放进同一个桌面应用。

当前版本为 **0.3.21**，Windows x64 是主要发布和验证平台。可从 [GitHub Releases](https://github.com/SliyRainbow/EchoCode/releases/latest) 下载单文件 EXE 或安装版；完整更新内容见 [v0.3.21 发布说明](docs/releases/v0.3.21.md)。

> Windows 后端提供受限令牌及进程资源限制，尚无完整的操作系统文件/网络隔离；“请求批准”模式下命令仍需额外批准。客户端和第三方模型能力仍在持续完善，具体测试范围见发布说明。

## 当前能力

- 模型协议：DeepSeek 官方、OpenAI Chat Completions、OpenAI Responses、Anthropic Messages、Gemini 原生协议及自定义兼容供应商。
- 模型管理：密钥保存、连接测试、上游模型发现、模型删除、协议兼容参数、上下文窗口来源与手动覆盖。
- Agent：流式工具循环、低/中/高/超高推理档位、显式 Plan、任务进度、消息排队与运行中引导。
- AI 团队：规划、实现、测试、审阅和汇总角色，可为每个角色选择供应商与模型。
- 结构化工具：文件读取/搜索/写入/补丁/复制/删除、命令、Git、终端、网络搜索、远程仓库读取、浏览器和其他窗口操控。
- 办公产物：无需安装 Office，可读取、创建和编辑 DOCX、XLS/XLSX 与 PPTX；XLSX 支持真实公式、样式、合并、宽高、冻结窗格、下拉验证和打印布局，`read_file` 自动解析并在右侧结构化预览，生成文件可由 Microsoft Office 打开。
- 插件系统：独立进程 JSON-RPC/JSONL ABI、本地安装、逐项授权、超时/取消/输出限制，以及主 Agent、Plan、AI 团队和子代理统一注册。
- 上下文：稳定前缀、MCP schema 快照、工具结果摘要、自动与手动压缩，以及工作区内的上下文用量、缓存命中率和费用观测。
- 工作区：项目与会话持久化、长期记忆、checkpoint、rewind、对话分叉、Git 审阅和持久终端。
- 桌面能力：Chromium 内核浏览器、网页检查与截图、文件预览、图片粘贴和预览、Windows 窗口控制。

## 技术栈

- Go `1.26.5` 工具链
- Wails `v2.12.0`
- SolidJS + TypeScript + Vite
- Bun `1.3.x`
- SQLite（纯 Go 驱动）
- Windows Credential Manager 密钥存储
- Chrome DevTools Protocol + Edge/Chrome 浏览器内核

详细模块边界见 [技术栈与开发工具链](docs/technical-stack.zh-CN.md)。

## 本地开发

### 前置条件

1. Go。`go.mod` 会选择 `go1.26.5` 工具链。
2. Bun。Windows PowerShell 中统一调用 `bun`，避免执行策略拦截 `bun.ps1`。
3. Git。
4. Wails CLI：

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.12.0
```

5. Windows WebView2 Runtime；内置浏览器还需要 Microsoft Edge 或 Google Chrome。

### 安装与检查

```powershell
git clone https://github.com/SliyRainbow/EchoCode.git
cd EchoCode

go mod download

cd frontend
bun install --frozen-lockfile
bun run check
cd ..

go test ./... -count=1
go vet ./...
```

### 运行与构建

```powershell
# 开发模式
wails dev

# Windows 生产构建，输出到 build/bin/EchoCode.exe
wails build -clean
```

`frontend/wailsjs` 和 `frontend/dist` 是生成目录，不要手工编辑或提交。前端只使用 `frontend/bun.lock`，不要新增第二份包管理器锁文件。

## 本地数据与密钥

Windows 默认数据目录为 `%AppData%\EchoCode`：

- `runtime-settings.json`：非敏感运行配置和供应商元数据。
- `projects.json`：项目与会话清单。
- `sessions/`：事件日志、checkpoint、分支和项目记忆。
- `echocode.db`：用量、供应商状态等 SQLite 数据。
- `browser-profile/`：内置浏览器配置。
- `plugins/`：本地安装的第三方插件；插件设置本身保存在 `runtime-settings.json`。

API Key 和浏览器密码不写入 JSON 或 SQLite，Windows 下保存到 Credential Manager。移除最后一个项目后，应用会使用 `%USERPROFILE%\EchoCodeProject` 作为临时工作区。

## 安全边界

- 工作区读写、额外可写目录、网络、Shell、破坏性操作和审批策略由运行设置共同控制。
- Windows 命令进程使用 Job Object 管理进程树，并可限制内存、CPU、进程数和子进程权限。
- 当前操作系统层沙箱**不提供文件系统或网络命名空间隔离**；这两项仍由路径校验、命令策略和用户审批约束。不要把 UI 中的“沙箱”理解为虚拟机级隔离。
- 非 Windows 平台目前只有基础进程管理，尚未达到 Windows 的验证程度。

## 修改基线

提交前至少运行：

```powershell
cd frontend
bun run check
cd ..
go test ./... -count=1
go vet ./...
```

涉及 Wails API、浏览器原生表面、Windows 进程控制或打包资源时，再运行 `wails build -clean`。涉及模型上下文目录时，当前必须同步修改 Go 与前端目录并补测试，直到该目录改为单一生成源。

## 文档

- [当前架构与开发工具链](docs/technical-stack.zh-CN.md)
- [开发状态与发布门槛](docs/development-plan.zh-CN.md)
- [Skills 与 Agent 核心维护](docs/skills-development.zh-CN.md)
- [插件 ABI 与开发指南](docs/plugins-development.zh-CN.md)
- [EchoCode Agent 内核 Skill](skills/echocode-agent-core/SKILL.md)
- [办公产物 Skill](skills/echocode-office-artifacts/SKILL.md)
- [Agent 内部设计](docs/agent-internal-design.zh-CN.md)
- [贡献与 Pull Request 流程](CONTRIBUTING.md)
- [版本变更记录](CHANGELOG.md)
- [发布说明发布流程](docs/release-publishing.zh-CN.md)

## 许可证

EchoCode 使用 [MIT License](LICENSE)。发布衍生版本时请保留原版权和许可证声明。
