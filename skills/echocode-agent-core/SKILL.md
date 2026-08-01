---
name: echocode-agent-core
description: 仅在修改 EchoCode 自身的 Agent 内核、上下文组装、工具注册、模型协议、Plan、团队、子代理、MCP 或缓存策略时使用。
---

# EchoCode Agent 内核

## 适用边界

本 Skill 只服务于 EchoCode 项目自身的 Agent 内核开发。普通编程任务即使提到 Agent、Plan、缓存、协议或 Skill，也不得套用 EchoCode 的内部实现规则。

修改前读取 `docs/agent-internal-design.zh-CN.md` 和 `docs/agent-reliability-plan.zh-CN.md`，以代码、测试和当前文档为准。

## 内核约束

- 权限、路径、审批、超时和终态由宿主代码强制执行，不能只依赖提示词。
- 文件、搜索和补丁操作使用结构化工具；Shell 只用于构建、测试、编译器及确需执行的程序。
- 用户输入、工具结果、错误和临时状态只进入易变上下文；稳定前缀及工具 schema 保持确定顺序。
- Plan、团队和子代理共享结构化状态，不重复注入完整角色对话；取消、失败、重启和 rewind 必须可恢复。
- 工具循环不使用固定调用次数截断，仍受停滞检测、超时、审批、沙箱和资源限制。
- 协议差异留在 `internal/protocol`；供应商能力、推理字段、流式终态和 usage/cache 映射必须测试。
- 新状态必须同时考虑事件持久化、会话切换、分支、重启恢复和前端呈现。

## 验证

共享 Agent 链路变更至少运行：

```powershell
go test ./... -count=1
go vet ./...
cd frontend
bun run check
```

涉及 Wails、浏览器或 Windows 进程控制时追加 `wails build -clean`。
