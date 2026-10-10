# EchoCode 对话展示与 Agent 协议检查

日期：2026-10-10，更新至 r4。保留工作区中此前所有界面、导航、可靠性和请求头修复。本轮统一使用可用的 `deepseek-v4-flash` 验证三种协议。

## 对话展示

准备权限、选择路由、组装上下文、连接耗时和首个响应计时是宿主诊断信息，默认对话不再逐条显示。过滤同时覆盖实时事件与已保存的历史。最终正文、模型实际返回的思考/思考摘要、文件改动、可展开的工具结果、审批和失败信息继续保留。后台诊断与持久化计时仍可用于排查问题，不再占据正文。

## 官方规范与修复

| 范围 | 发现的问题 | 当前处理 |
| --- | --- | --- |
| Chat Completions / DeepSeek | 内部工具选择与并行选项未发出；后续工具消息图片不符合 Chat 的工具消息格式 | 发送 tool_choice / parallel_tool_calls；上游拒绝并行字段时省略后重试；把工具图片放在连续工具结果之后 |
| 工具参数 | 不完整 JSON 会被替换成 `{}`，可选参数工具可能意外执行默认动作 | 保留原始坏参数并由宿主拒绝，回传工具错误；不执行默认空参数动作 |
| Responses 请求 | 发送 Codex 专用 client_metadata 请求体字段；未明确 strict 模式；空 output 被省略 | 使用标准 metadata，较大的原有诊断上下文保留在请求头；strict=false 保留工具参数可选性；空结果仍发送空字符串 |
| Responses 流与续接 | 未接思考摘要、分片参数与 incomplete；工具按随机 ID 字典顺序输出；丢失 assistant phase | 接入摘要/参数 delta 和 done，按 output_index 组织输出；重放有序消息、phase、函数调用和加密推理；兼容旧 continuation 格式及上下文裁剪 |
| Anthropic Messages | 一轮多个工具结果分散成多条 user 消息；执行错误未标 is_error；输入总量遗漏缓存部分 | 合并工具结果，透传错误语义；thinking 与签名继续原样回传；累计 usage 保留首次输入、缓存读取/写入及最终输出 |
| SSE 与终态 | 按每条 data 行单独解析；提前 EOF、长度截断可能被当作完成；部分错误被忽略 | 支持完整 SSE 事件边界与多行 data；区分成功、截断、连接中断与流内错误，保留已收到的文本和用量 |
| 失败转移 | 可能把上一供应商的私有续接状态带到下一家；非流式失败出口丢弃部分结果 | 按路由、模型及凭据绑定续接范围；跨路由只保留公共对话和工具结果；开始返回推理/用量后不再拼接另一家的流，失败保留部分输出 |
| 模型能力 | GPT-6 已知模型没有对应的推理能力和上下文条目 | 对照官方模型页补充精确模型 ID、上下文和支持档位；官方 OpenAI 的不匹配 Chat 工具配置给出 Responses 指引，自定义中转仍按其实际能力尝试 |
| Responses 思考正文（r4 实测发现） | 只接入 reasoning_summary_text，遗漏 reasoning_text 的 delta/done；reasoning item 的 content 被当成普通正文 | 接入两种思考事件，分别按 content_index/summary_index 去重；流式快照和非流式响应都把思考与最终正文分开，保留工具回合中的原始续接数据 |
| Messages 空思考块（r4 实测发现） | thinking 为空时被 omitempty 删去，工具结果回传后上游报 missing field thinking 的 400 | 只对 thinking 块显式保留 thinking/signature 字段及原值；兼容已保存的旧续接数据，不把签名展示成思考 |
| 目录结果（r4 实测发现） | 模型把 `f check.txt` 的类型前缀当成文件名，多次无效读取；子目录条目相对列举目录，不能直接用于 read_file | 返回 JSON entries，分开 type、path、size_bytes；path 可直接供文件工具使用，保留真实空格和类型前缀文件名，明确 truncated |

规范依据：

- [OpenAI Chat Completions 请求](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)：工具参数、tool_choice、并行调用、图片与工具消息类型。
- [OpenAI Function calling](https://developers.openai.com/api/docs/guides/function-calling)：工具结果与 strict/non-strict 语义。
- [OpenAI Responses 请求](https://developers.openai.com/api/reference/resources/responses/methods/create)：metadata 的字段限制、输出长度等标准参数。页面过大时使用同地址的 `.md` 文档核验。
- [OpenAI Responses 流事件](https://developers.openai.com/api/reference/resources/responses/streaming-events)：思考摘要、函数参数与 incomplete / failed 等事件。
- [OpenAI Reasoning](https://developers.openai.com/api/docs/guides/reasoning)：加密推理、输出重放与 assistant phase。
- [Anthropic 流式消息](https://platform.claude.com/docs/en/build-with-claude/streaming)：事件边界、累计 usage、错误和 thinking/signature。
- [Anthropic 思考块](https://platform.claude.com/docs/en/build-with-claude/thinking)：thinking 文本可以为空，工具续接仍须保留该块及其签名。
- [Anthropic 工具结果](https://platform.claude.com/docs/en/agents-and-tools/tool-use/handle-tool-calls)、[工具选择](https://platform.claude.com/docs/en/agents-and-tools/tool-use/define-tools)：多个工具结果的分组、is_error、工具选择与 thinking 限制。
- 官方模型能力：[GPT-6 Astra](https://developers.openai.com/api/docs/models/gpt-6-astra)、[GPT-6.1 Sol](https://developers.openai.com/api/docs/models/gpt-6.1-sol)、[GPT-6 Sol](https://developers.openai.com/api/docs/models/gpt-6-sol)、[GPT-6 Luna](https://developers.openai.com/api/docs/models/gpt-6-luna)。没有更换用户已选模型。

## 验证

- `agent_contract_test.go`：SSE 多行、提前断流、错误、截断、Responses 摘要去重与有序重放、phase、空结果、Anthropic 多工具分组、缓存统计、工具图像、工具选择与并行控制。
- `protocol_safety_test.go`：坏参数不能调用工具，错误标记可序列化恢复，结构化不可重试错误不会因文字中出现“中断”而被重试。
- `protocol_roundtrip_test.go`：通过本地 HTTP 服务，分别运行真实 `Service.SendChatMessage` 的 Chat、Responses、Messages 工具回路，验证实际工具执行、结果回传、推理续接和最终提交。
- `responses_reasoning_text_test.go`：覆盖 reasoning_text 实时增量、done、item/response 完整快照及非流式响应；摘要和思考正文的索引不会冲突，重复快照不重复展示，续接保留加密字段。实际 Agent 回路也加入 reasoning_text 覆盖。
- `anthropic_thinking_test.go`：覆盖流式/非流式空 thinking、分片签名、持久化续接恢复及旧记录；修复前稳定复现缺失字段的 400。
- `list_dir_test.go`：将列举结果中的路径直接交给 read_file，覆盖子目录、中文、空格、真实 `f ` 前缀文件、额外授权目录、空文件/空目录及截断边界。
- 浏览器测试覆盖实时/历史计时隐藏，以及思考过程和文件编辑摘要保留。
- r4 重新通过 `go test ./... -count=1`、`go vet ./...`，以及前端 TypeScript、106 项单测和 Vite 构建。前一版已通过 54 项浏览器回归；本轮未修改前端，未重复该组测试。
- r4 Windows amd64 的 `wails build -clean` 已通过；统一测试包为 `EchoCode-0.3.20-windows-x64-preview-20261010-r4.zip`，包含构建时间。

## 真实上游范围

使用用户此前授权的、已保存于系统凭据库的中转站凭据及已保存请求头；密钥只在测试进程内使用，未写入源码、日志或包。每个协议使用新的只读临时目录及测试文件，文件内容为随机标识，不在提示词中告知标识；不发送用户项目内容，不改用户供应商配置和所选模型，测试结束清理临时目录。

最终验收直接运行 EchoCode 的真实 `Service.SendChatMessageWithEvents`，不使用人工拼装的对话替代 Agent 执行：

| 协议 | 模型 | 工具执行与回传 | 最终正文 | 可展示思考 | 回合提交 |
| --- | --- | --- | --- | --- | --- |
| Chat Completions | deepseek-v4-flash | list_dir、read_file 均 ok，无工具错误 | 精确匹配文件随机标识 | 484 字符，独立于正文 | 成功 |
| Responses | deepseek-v4-flash | list_dir、read_file 均 ok，无工具错误 | 精确匹配文件随机标识 | 19 字符，独立于正文 | 成功 |
| Anthropic Messages | deepseek-v4-flash | list_dir、read_file 均 ok，无工具错误 | 精确匹配文件随机标识 | 187 字符，独立于正文 | 成功 |

三种协议使用同一提示词、模型、2048 输出上限和只读权限。思考长度是此次请求的观测值，不是模型质量或性能比较。最终正文按完整字符串匹配，避免把“包含正确标识但混入思考”误判为成功。

对 Responses 另做了原始事件类型核对：修复前中转实际发出了 `response.reasoning_text.delta/done`，客户端没有识别，并在 item 快照阶段将其混入正文。修复后相同补充探针只返回要求的最终数值，思考单独输出且 delta/done/item/completed 不重复。记录仅保留事件名称和字符数，不输出思考正文或不透明签名。

此前 Responses/Astra 与 Messages/Opus 的 402 属于所选模型在该中转不可用。用户已确认这两个模型不可用；这些旧测试不代表协议不兼容。本次 DeepSeek 同模型验收替代此前两项未完成结论。

## 体验与能力边界

本次统一的是 EchoCode 已支持的客户端 Agent 核心：文本、可展示思考、函数工具、工具图片回传、多轮续接、错误与终态。不能据此宣称与 Codex/ZCode 的所有功能完全相同。OAuth/订阅登录、厂商托管工具、Realtime/WebSocket、后台 Responses 生命周期、音视频及平台专有能力不属于这次实现；不同模型的推理内容可见性也由上游决定。

只展示上游明确返回的思考文本或摘要；签名、加密推理和其他不透明续接数据保留在协议层，不作为正文解码展示。程序继续使用供应商各自的配置，不硬编码中转地址、ZCode 标识或密钥。

## 测试环境清理

沿用隔离的 Go 工具目录和 Conda 测试环境，没有安装新的 Python 或修改全局 PATH。临时工作区已清理，临时 Go 测试源码已移除。自动审批检查拒绝删除 `%TEMP%/echocode-ui-review/protocol-live-check.exe`，仅返回 `blocked by policy`；未绕过该拒绝。该文件不含测试密钥，也不进入桌面发布包。
