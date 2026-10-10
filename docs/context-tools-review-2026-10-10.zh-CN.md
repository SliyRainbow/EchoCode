# EchoCode r7：上下文、导航与真实模型操控验收

日期：2026-10-10。平台：Windows x64。版本：0.3.20 r7 预览包。

## 交付内容

- 对话右上角显示上下文预算占用；弹层提供输入 tokens、模型窗口及来源、自动压缩阈值、压缩次数和手动压缩。空闲时显示会话视图估算，运行中有上游 usage 时显示最近一次请求报告的输入用量，包含工具结果，不与累计账单混用。
- 自动压缩继续使用现有本地摘要与裁剪引擎。手动压缩可在阈值之前执行；短历史或没有实际节省时返回无需压缩。保存的是模型上下文视图，原始聊天和文件检查点继续保留。
- 手动操作绑定项目与会话身份，宿主串行化会话切换/任务启动，拒绝压缩运行中的同一会话；保存失败恢复内存状态，迟到的前端返回不能覆盖另一会话。
- 恢复及切换模型后重新计算预算；将工具错误标志加入持久化上下文。上下文估算包含推理文本及协议续传字段。压缩事件不再错误地取消当前文件检查点的标记。
- 长任务先裁剪过长工具输出；仍超目标时压缩旧消息，保留当前用户要求及最近完整 tool call/result。若仍超模型输入上限，明确失败，避免盲目发送过长请求。
- 移除设置/扩展页重复的“返回工作区 / EchoCode 设置 / 关闭”一行及对应样式；应用顶栏随当前分类显示名称，继续使用已有后退、前进和工作台导航，保留草稿离开保护。

## 真实模型测试与修复

使用用户此前授权的供应商、`deepseek-v4-flash` 和已保存的请求头。密钥仅从指定系统凭据条目读入内存，不写入测试日志、命令行或包。没有改写用户的供应商设置、项目或会话。

| 场景 | 实际证据 | 最终结果 |
| --- | --- | --- |
| 内置浏览器 / Chat | 模型调用 open、snapshot、type、click，再 snapshot；本地 HTTP 服务独立收到指定表单值，回答含页面确认标记 | 通过 |
| 内置浏览器 / Responses | 相同操作及服务端独立断言 | 通过 |
| 内置浏览器 / Messages | 相同操作及服务端独立断言 | 通过 |
| Windows 电脑操控底层 | 仅对自行创建的窗口连续 focus、click、type、key；独立读取窗口提交结果 | 通过 |
| 电脑操控 / Messages | 模型实际枚举、聚焦、点击、输入、按键；文本与截图识别两个用例 | 通过 |
| 电脑操控 / Chat | 模型实际截图、读出未在提示中提供的随机六位数字、点击输入并提交；独立校验值与图片文件 | 通过 |

每次测试只允许操控具有唯一标题的测试窗口，并在结束时通过所创建进程的句柄关闭。用户看到的白色 “Visual code / Confirm” 窗口就是此测试页面，不是登录验证码。浏览器使用同一个生产 bridge 与 managed browser engine；真实 Wails 另行验证嵌入式浏览器呈现。

发现并修复：

1. Windows 绝对路径提取把 `http:` / `https:` 末尾的 `p:/` / `s:/` 当作驱动器授权，使网页任务被误判为必须修改本地文件。现拒绝 URI 中的假盘符，并排除 URL 内的相对路径片段；真正的本地输出路径仍受原有约束。
2. DeepSeek 的无推理文本工具子轮次在 JSON 序列化时丢失空 `reasoning_content`。DeepSeek Chat 路由保留明确的空字段，普通模型不额外获得此字段；Stream 与 Complete、原生与兼容路由均有线协议测试。处理依据为 [DeepSeek 思考模式文档](https://api-docs.deepseek.com/guides/thinking_mode/) 对工具请求回传推理字段的要求。最初电脑 Chat 验收仍观察到一次上游 400；最终保留字段的多轮真实截图用例通过，日志记录字段长度而不记录推理正文。
3. 后台 Go 线程对已聚焦窗口重复 SetForegroundWindow，以及受 Windows 前台线程限制时，会让电脑操控中断。现先查询实际前台；必要时临时连接前台输入队列进行允许的切换，然后解除连接并核验目标窗口。未取得目标焦点则拒绝输入。实现参考 [SetForegroundWindow](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-setforegroundwindow) 与 [AttachThreadInput](https://learn.microsoft.com/en-us/windows/win32/api/winuser/nf-winuser-attachthreadinput) 的官方说明。

## 回归记录

- 常规 Go 全量：1,049 个测试/子测试通过；35 个条件跳过项（包括收费和原生桌面测试）与普通回归分开运行。
- 全量 race 与 `go vet`：通过；最终结果见 `%TEMP%/echocode-context-tools-20261010`。
- 前端 TypeScript、105 项单测和 Vite 生产构建：通过。
- 浏览器界面：原 63 项全量通过；随后新增 2 项上下文深浅主题/窄屏/忙碌禁用断言，上下文套件 5 项全部通过，合计覆盖 65 个用例。
- 真实 Wails：11 个场景通过，新增上下文压缩、聊天记录保留、桌面重启恢复、实际弹层和动态标题检查。
- Windows Wails 生产 clean build；诊断构建与交付程序分离，交付 EXE 不包含验收调试端口/隔离目录开关。

核心新增测试：`internal/agent/context_manual_test.go`、`context_window_test.go`、`turn_paths_test.go`、`internal/protocol/deepseek_empty_reasoning_test.go`、`app_tools_live_windows_test.go`、`frontend/e2e/context.e2e.ts`。桌面验收脚本为 `scripts/test-desktop-acceptance.cjs`。

## 验收边界

- 本轮证明指定 DeepSeek 中转模型能调用内置浏览器和 Windows 窗口工具并理解测试截图；不代表所有模型、任意网站或全部第三方应用都已验收。电脑 Responses 组合没有单独执行。
- 压缩是本地提取摘要和裁剪，不是额外调用模型生成语义总结；较早细节可能不再进入模型请求，用户原始历史仍可查看。长工具循环的临时请求整理不计入持久会话压缩次数。
- 电脑操作仍受用户允许应用列表、审批、Windows 权限和前台限制；没有放宽到任意程序，也没有启用更高权限或更改全局环境。
- 此包是本地预览，未创建 Git 提交、发布 Release 或安装新 Python/Go 环境。
