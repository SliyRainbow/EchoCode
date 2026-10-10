# 模型中转站的自定义请求头

2026-10-10：修复 OpenAI 兼容与 Responses 通道的请求头优先级。过去先应用用户配置、再写入 EchoCode 客户端标识，导致 `User-Agent` 和 `originator` 的自定义值无效。现在按“默认请求头 → 用户配置 → 每轮内部上下文”的顺序发送。模型列表、普通/流式聊天和重试均使用这一规则；未配置时仍使用默认 EchoCode 标识。

## EchoCode 中的配置位置

打开 **设置 → 模型与连接 → 选择供应商 → 额外请求头**。每行填写 `Header: value`，不使用 JSON，也不填写 curl 的 `-H`。

用户提供的中转地址为 `https://ps.air-outer.com`，协议为 OpenAI Chat。按标准 `/v1` API 前缀配置：

| 字段 | 值 |
| --- | --- |
| 接入协议 | OpenAI 兼容 |
| API 类型 | Chat Completions |
| API 地址 | `https://ps.air-outer.com/v1` |
| 模型密钥 | 自己在该中转站取得的 API Key |
| 模型 ID | 该中转站支持的具体 ID |

EchoCode 会追加 `/models` 和 `/chat/completions`。因此 API 地址不填写完整聊天路径。经用户授权的真实凭据测试已确认这两个路径可用，结果见本文末尾。

## ZCode 静态标识参考

智谱官方仓库的 [model-config.ts](https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/packages/bootstrap/src/model-config.ts) 定义了客户端标识。以下以 [官网当前下载页](https://zcode.z.ai/cn/docs/install) 展示的 **3.14.5** 为例，可粘贴到“额外请求头”：

```text
User-Agent: ZCode/3.14.5
X-ZCode-App-Version: 3.14.5
X-ZCode-Agent: glm
X-Title: Z Code@electron
HTTP-Referer: https://zcode.z.ai
X-Release-Channel: production
X-Client-Language: zh-CN
X-Client-Timezone: Asia/Shanghai
X-Platform: win32-x64
X-Os-Category: windows
```

这是按公开源码整理的 Windows 桌面端静态字段示例。已对用户指定的中转站完成一次连通性验证，不代表网关全部校验规则已知。语言与时区可按实际环境调整；版本号随要兼容的客户端版本调整。

默认来源地址和环境来自 [zcodeEndpoint.ts](https://github.com/zai-org/ZCode/blob/main/packages/shared/src/zcodeEndpoint.ts)，平台值来自 [runtime-platform-headers.ts](https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/packages/bootstrap/src/runtime-platform-headers.ts)。这些是请求头里的字符串，不会把 API 请求改发到 ZCode 官网。

ZCode 还按请求动态生成追踪/会话字段；[runner-attribution.ts](https://github.com/zai-org/ZCode/blob/main/apps/zcode-cli/packages/adapters/src/model/runner-attribution.ts) 中的 request、trace、session、query ID 不应复制成静态值。上面的示例只设置客户端静态标识。API Key 仍通过 EchoCode 的密钥栏保存。

## 验证步骤

1. 退出先前的 EchoCode 进程，打开本次带请求头修复的新程序。
2. 保存供应商设置，再点“测试并获取”。
3. 选择中转站支持的模型，发送一句简单消息验证 Chat Completions。若中转站不提供模型列表，可手动添加已知模型 ID。
4. 若仍出现 `unauthorized client detected`，仅能确认网关继续拒绝请求；它可能校验其他字段、账号权限或客户端规则，不能据此断定 API Key 错误，也不能保证复制标识就能通过。

请求头优先级修复已通过本地 HTTP 测试服务、Go 全量测试、go vet、106 项前端单测与前端构建，以及 Windows Wails 生产构建。

## 用户授权的真实连通性验证

2026-10-10，用户提供凭据并授权测试后，仅使用该中转站对应的 EchoCode 已保存凭据，向 `ps.air-outer.com` 发起请求；未发送项目内容。密钥未写入脚本、报告或命令行。

| 路径与客户端配置 | 结果 |
| --- | --- |
| `/v1/models`，默认 EchoCode 标识 | HTTP 401，`unauthorized_client_error` |
| `/v1/models`，上方六个基础 ZCode 标识，同时保留 `originator: echocode` | HTTP 200，4 个模型 |
| `/v1/models`，补充 Windows/语言/时区标识，并省略 originator | HTTP 200，同样 4 个模型 |
| `/v1/chat/completions`，ZCode 基础标识，非流式，32 tokens 上限 | HTTP 200；预算在推理阶段用完，最终正文为空。该测试只证明请求被接受 |
| EchoCode 实际 `OpenAICompatibleProvider.ListModels`，直接读取应用当前保存的额外请求头 | 成功获取 4 个模型 |
| EchoCode 实际 `OpenAICompatibleProvider.Stream`，同一份已保存配置，`deepseek-v4-flash` | 完整返回 `OK`；此次请求上游报告 51 tokens |

当次模型列表为 `claude-opus-4-8`、`claude-opus-5`、`deepseek-v4-flash`、`gpt-6-astra`。只验证了 DeepSeek 模型的一条简短流式消息，其他模型和工具调用尚未通过这次真实测试验证。

结论：这组基础请求头在此次测试中可用，`originator: echocode` 并未导致拒绝。若桌面端仍报 401，应先核对可执行文件是否为请求头修复版，以及实际选中的供应商/模型；不能据此继续堆叠未经验证的身份字段。

## 后续工具调用 400 与完整 Agent 验证

用户在真实桌面端发起任务后，出现 `Invalid schema for function 'list_dir': null is not of type "array"`。这与请求头 401 是不同阶段的问题。

已用同一上游复现：原始 `list_dir` schema 的参数全部可选，因此省略了 `required`。省略在 JSON Schema 中合法，但该网关的转换链对其报 null-array 错误；显式加 `required: []` 后成功返回工具调用。依据 [JSON Schema 对象规范](https://json-schema.org/understanding-json-schema/reference/object)，工具导出现在统一显式表示空必填列表，保留原本可选性、有效 null 数据和共享 schema。

完整 Agent 验证进一步发现：流式工具参数在宿主内部为 JSON 对象字节，回传时未经转换，导致 `arguments` 在请求体中成为对象，引发下一轮 400/422。按照 [OpenAI 官方 Chat Completions API 文档](https://developers.openai.com/api/reference/resources/chat/subresources/completions/methods/create)，回传参数应是 JSON 字符串。现在在协议发送边界转换，宿主执行使用的原始参数不变；同时覆盖 DeepSeek 与 Responses 的参数编码。

修复后，以用户授权的该站凭据运行 EchoCode 的真实 `Service.SendChatMessage`：在新建的只读临时工作区里列目录、读取唯一测试文件，并最终回复 `ECHOCODE_TOOL_CHAIN_OK`；`list_dir`、`read_file` 和最终提交都成功。验证没有发送用户项目内容，临时工作区已清理。其他模型和写操作仍不在这次真实验证范围内。

`headers` 是先前测试包的标记，不是产品分支。请求头修复与工具链修复都合入相同源码；程序没有硬编码 ZCode 标识、中转域名或 API Key。所有供应商继续使用各自的配置。

## 同一 DeepSeek 模型的三协议验收（r4）

2026-10-10 按用户确认的模型可用范围，统一使用 `deepseek-v4-flash` 验证 `/v1/chat/completions`、`/v1/responses` 和 `/v1/messages`。三路沿用相同的已保存请求头和本中转的凭据，分别运行真实 Agent 的列目录、读取随机测试文件、结果回传与最终回复；修复后全部通过，最终正文精确匹配文件标识，思考单独展示。

补测修复了 Responses 的 `reasoning_text` 事件漏接/混入正文，以及 Messages 空 `thinking` 字段被省略造成的后续 400。目录结果也改为明确的类型与可直接使用的路径字段，避免模型将 `f ` 当成文件名。Astra/Opus 的旧 402 不代表这两个协议不可用。完整记录见 [协议检查](protocol-review-2026-10-10.zh-CN.md)。
