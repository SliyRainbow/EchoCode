# Contributing to EchoCode

感谢你愿意改进 EchoCode。仓库接受 Issue、文档修正、测试和代码 Pull Request；所有合并内容都需要维护者审核。

## 提交流程

1. Fork 仓库，并从最新 `main` 创建独立分支。
2. 一个 Pull Request 只处理一个明确问题，避免混入无关格式化或重构。
3. 修改前先阅读 `README.md`、`docs/technical-stack.zh-CN.md` 和 `skills/echocode-agent-core/SKILL.md`。
4. 为行为变化补测试，并在 Pull Request 中写明实际执行过的命令。
5. 等待 CI 通过和 `Exist`、`Lava` 审核；维护者可以要求修改或拒绝合并。

推荐分支名：

```text
feature/short-description
fix/short-description
docs/short-description
```

## 本地检查

```powershell
cd frontend
bun install --frozen-lockfile
bun run check
cd ..

go test ./... -count=1
go vet ./...
```

涉及 Wails API、浏览器原生表面、Windows 进程控制或打包资源时，再运行：

```powershell
wails build -clean
```

## Pull Request 要求

- 说明问题、解决方式、风险和用户可见变化。
- 列出实际执行的测试；没有执行的测试必须明确写出原因。
- UI 变化提供浅色/深色和关键尺寸截图。
- 不提交 API Key、Cookie、浏览器配置、数据库、日志、构建产物或个人路径。
- 不提交 `node_modules`、`frontend/dist`、`build/bin` 或第二份前端锁文件。
- 引入第三方代码、模型目录或视觉资源时说明来源和许可证。
- 使用 AI 生成代码时，提交者仍需理解、审阅并对结果负责。

## Agent 与安全边界

- 文件操作优先使用结构化工具，不要退化为 Shell 文本处理。
- 新工具必须经过工作区路径、权限和审批策略，并覆盖拒绝/取消/失败测试。
- Plan 阶段和团队审阅角色保持只读。
- Windows Job Object 不是文件系统或网络隔离，不得在文档和 UI 中夸大安全保证。
- API Key 和浏览器密码只能进入系统密钥库，不能写入 JSON、SQLite 或日志。

## 提交许可

提交 Pull Request 即表示你有权提交相关内容，并同意该贡献按照仓库的 [MIT License](LICENSE) 分发。第三方内容仍受其原始许可证约束。
