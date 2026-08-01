# EchoCode 插件开发指南

EchoCode 插件 ABI 用于把 Office、数据库、企业系统或其他本地能力作为 Agent 工具接入 EchoCode。第三方代码不会加载进 EchoCode 主进程；每次工具调用都会启动一个受控外部进程，通过 stdin/stdout 交换 JSON-RPC 2.0 JSONL 消息。

当前 ABI 版本是 `1.0`，Manifest schema 版本是 `1`。

## 1. 运行模型

```text
Agent / Plan / AI 团队 / 子代理
  -> 稳定工具名 plugin__<plugin-id>__<tool-name>
  -> EchoCode 权限、路径和审批检查
  -> 启动插件进程
  -> initialize
  -> tools.call
  -> 收集结构化结果
  -> 进程退出或由 EchoCode 终止
```

- 一个工具调用对应一个新进程，不保留跨调用内存状态。
- 用户停止任务、调用超时或 EchoCode 退出时，宿主会终止插件进程树。
- Plan 只注册 `readOnly: true` 的插件工具。
- 主 Agent、AI 团队和子代理使用同一份插件目录与权限配置。
- 写入工具进入 EchoCode 的统一审批策略，并与其他工作区写操作串行执行。
- 第三方插件安装后默认停用，且默认不授予任何权限。

## 2. 插件目录

一个最小插件目录如下：

```text
example-plugin/
  echocode-plugin.json
  plugin.exe
```

Node.js 插件可以使用：

```text
example-plugin/
  echocode-plugin.json
  plugin.mjs
```

在“设置 -> 插件”中选择插件目录或其中的 `echocode-plugin.json` 安装。EchoCode 会把整个目录复制到应用配置目录的 `plugins/` 下。安装包最多包含 10,000 个普通文件，总大小不超过 512 MiB，不允许符号链接。

## 3. Manifest

文件名固定为 `echocode-plugin.json`。解析器会拒绝未知字段，防止拼写错误被静默忽略。

```json
{
  "schemaVersion": 1,
  "id": "example-office-helper",
  "name": "Example Office Helper",
  "version": "1.0.0",
  "description": "读取报表并生成摘要。",
  "author": "Example Studio",
  "homepage": "https://example.com/plugin",
  "runtime": {
    "transport": "stdio",
    "command": "plugin.exe",
    "args": []
  },
  "permissions": {
    "fileRead": true,
    "fileWrite": true,
    "network": false
  },
  "tools": [
    {
      "name": "read_report",
      "description": "读取工作区内的报表并返回摘要。",
      "inputSchema": {
        "type": "object",
        "properties": {
          "path": { "type": "string", "description": "报表路径" }
        },
        "required": ["path"],
        "additionalProperties": false
      },
      "readOnly": true,
      "timeoutSeconds": 60,
      "permissions": {
        "fileRead": true,
        "fileWrite": false,
        "network": false
      },
      "paths": [
        { "argument": "path", "access": "read" }
      ]
    }
  ]
}
```

### 顶层字段

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `schemaVersion` | 是 | 当前只能是 `1` |
| `id` | 是 | 小写稳定标识，匹配 `[a-z0-9][a-z0-9._-]{1,63}`；与工具名组合后的完整名称不能超过 64 字符 |
| `name` | 是 | 设置页显示名称 |
| `version` | 是 | 插件版本；建议使用 SemVer |
| `description` | 否 | 插件用途 |
| `author` | 否 | 开发者名称 |
| `homepage` | 否 | 项目主页 |
| `runtime` | 是 | 外部进程启动方式 |
| `permissions` | 是 | 插件可能请求的权限上限 |
| `tools` | 是 | 至少声明一个工具 |

`runtime.transport` 当前只能是 `stdio`。`runtime.command` 可以是插件目录内的相对路径、绝对路径，或可从 `PATH` 找到的命令。带 `/` 或 `\` 的相对命令不能逃出插件目录。

### 工具字段

| 字段 | 必填 | 说明 |
| --- | --- | --- |
| `name` | 是 | 插件内唯一工具名；最终名称由宿主加命名空间 |
| `description` | 是 | 给模型看的明确用途说明 |
| `inputSchema` | 是 | 根类型必须为 JSON Schema `object` |
| `readOnly` | 是 | 是否保证不修改外部状态 |
| `timeoutSeconds` | 否 | 单工具超时，仍受用户设置的宿主上限约束 |
| `permissions` | 是 | 该工具实际需要的权限 |
| `paths` | 否 | 需要宿主解析和检查的顶层路径参数 |

`paths[].argument` 目前只支持顶层参数名，不支持 `input.file.path` 这类嵌套路径。`access` 只能是 `read` 或 `write`；可选路径设置 `optional: true`。

如果工具声明 `readOnly: true`，它不能请求 `fileWrite`，也不能声明写入路径。工具权限必须是插件顶层权限的子集。

## 4. 权限模型

| 权限 | 含义 |
| --- | --- |
| `fileRead` | 读取工作区或用户额外允许目录中的文件 |
| `fileWrite` | 写入工作区或用户额外允许目录中的文件 |
| `network` | 使用网络；同时受 EchoCode 全局网络开关约束 |

权限需要同时满足三层条件：Manifest 声明、用户在设置页授权、当前 EchoCode 沙箱策略允许。插件收到的是最终授权集合，不应自行假定权限存在。

路径参数在进程启动前已由宿主转换为经过校验的绝对路径。插件必须只操作收到的路径，不要自行拼接路径绕开声明。写入路径调用成功后，如果文件真实存在且仍位于允许范围，EchoCode 会在对话中生成可点击的文件产物卡片。

第三方插件是用户安装的可信本地组件。当前 Windows 宿主提供进程树终止、资源限制、降低子进程权限、路径检查和审批，但不宣称提供完整的文件系统或网络隔离。

## 5. JSONL 协议

stdin 和 stdout 每行只能包含一个完整 JSON 对象。stdout 不能输出日志、banner 或调试文字；所有日志写入 stderr。宿主会限制 stdout 和 stderr 大小。

### initialize

宿主首先发送：

```json
{"jsonrpc":"2.0","id":"initialize-1","method":"initialize","params":{"protocolVersion":"1.0","host":{"name":"EchoCode","version":"0.3.15"},"plugin":{"id":"example-office-helper","version":"1.0.0"}}}
```

插件必须返回相同 `id`：

```json
{"jsonrpc":"2.0","id":"initialize-1","result":{"protocolVersion":"1.0","name":"Example Office Helper","version":"1.0.0"}}
```

协议版本不一致时应返回 JSON-RPC error，不能继续处理调用。

### tools.call

初始化成功后宿主发送：

```json
{"jsonrpc":"2.0","id":"call-1","method":"tools.call","params":{"name":"read_report","arguments":{"path":"C:\\work\\report.xlsx"},"context":{"workspaceRoot":"C:\\work","permissions":{"fileRead":true,"fileWrite":false,"network":false}}}}
```

成功响应：

```json
{"jsonrpc":"2.0","id":"call-1","result":{"summary":"读取 42 行，发现 3 条异常记录","structuredContent":{"rows":42,"warnings":3},"isError":false}}
```

业务失败可以使用 `isError: true`，让 Agent 获得可处理的工具结果：

```json
{"jsonrpc":"2.0","id":"call-1","result":{"summary":"工作表 Sheet1 不存在","isError":true}}
```

协议、参数或运行时错误使用 JSON-RPC error：

```json
{"jsonrpc":"2.0","id":"call-1","error":{"code":-32602,"message":"path is required"}}
```

结果字段：

| 字段 | 说明 |
| --- | --- |
| `summary` | 回传给模型的首选短摘要 |
| `content` | `summary` 为空时可用于生成摘要的文本或 JSON |
| `structuredContent` | `summary` 和 `content` 都为空时的结构化结果 |
| `isError` | 业务层失败标记 |
| `attachments` | 小型 base64 附件；不适合返回大型 Office 文件 |

附件格式：

```json
{"name":"preview.png","mimeType":"image/png","data":"iVBORw0KGgo..."}
```

`data` 必须是标准 base64。大型输出应写入 Manifest 声明的工作区路径，让 EchoCode 生成文件卡片，不要内联进 JSON。

## 6. 生命周期与取消

1. EchoCode 启动插件进程并设置工作目录为插件安装目录。
2. 宿主发送一次 `initialize`。
3. 宿主发送一次 `tools.call`，随后关闭 stdin。
4. 插件返回结果并主动退出。
5. 用户取消、超时或宿主关闭时，EchoCode 直接终止整个插件进程树。

ABI 1.0 不发送单独的 `notifications/cancelled`。插件应让子进程属于自己的进程树，并在 stdin 关闭或父进程终止后尽快退出。不要启动脱离父进程的后台服务。

默认单次执行上限为 120 秒，默认 stdout 结果上限为 1 MiB；用户可在设置页调整为 5-3600 秒和 64 KiB-16 MiB。工具自己的 `timeoutSeconds` 不能突破宿主上限。

## 7. Node.js 最小示例

Manifest runtime：

```json
{"transport":"stdio","command":"node","args":["plugin.mjs"]}
```

`plugin.mjs`：

```js
import readline from "node:readline";

const rl = readline.createInterface({ input: process.stdin, crlfDelay: Infinity });
let initialized = false;

function reply(id, result) {
  process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id, result })}\n`);
}

for await (const line of rl) {
  if (!line.trim()) continue;
  const request = JSON.parse(line);
  if (request.method === "initialize") {
    if (request.params?.protocolVersion !== "1.0") {
      process.stdout.write(`${JSON.stringify({ jsonrpc: "2.0", id: request.id, error: { code: -32001, message: "unsupported protocol version" } })}\n`);
      continue;
    }
    initialized = true;
    reply(request.id, { protocolVersion: "1.0", name: "Example", version: "1.0.0" });
    continue;
  }
  if (request.method === "tools.call" && initialized) {
    const { name, arguments: args } = request.params;
    reply(request.id, { summary: `${name}: ${args.path}` });
  }
}
```

## 8. Go 最小示例

```go
package main

import (
    "bufio"
    "encoding/json"
    "os"
)

type Request struct {
    JSONRPC string          `json:"jsonrpc"`
    ID      string          `json:"id"`
    Method  string          `json:"method"`
    Params  json.RawMessage `json:"params"`
}

func main() {
    scanner := bufio.NewScanner(os.Stdin)
    scanner.Buffer(make([]byte, 64*1024), 16*1024*1024)
    encoder := json.NewEncoder(os.Stdout)
    initialized := false
    for scanner.Scan() {
        var request Request
        if json.Unmarshal(scanner.Bytes(), &request) != nil {
            continue
        }
        response := map[string]any{"jsonrpc": "2.0", "id": request.ID}
        switch request.Method {
        case "initialize":
            initialized = true
            response["result"] = map[string]any{"protocolVersion": "1.0", "name": "Example", "version": "1.0.0"}
        case "tools.call":
            if !initialized {
                response["error"] = map[string]any{"code": -32002, "message": "not initialized"}
            } else {
                response["result"] = map[string]any{"summary": "tool completed"}
            }
        default:
            response["error"] = map[string]any{"code": -32601, "message": "method not found"}
        }
        _ = encoder.Encode(response)
    }
}
```

编译后把可执行文件与 Manifest 放在同一插件目录：

```powershell
go build -trimpath -o plugin.exe .
```

## 9. 调试与验收

1. 先在终端手工逐行发送 `initialize` 和 `tools.call`，确认 stdout 只有 JSONL。
2. 在“设置 -> 插件”安装目录，插件此时应显示“已停用”。
3. 显式启用插件并逐项授权，保存设置。
4. 刷新插件目录，确认工具状态与完整名称。
5. 分别验证成功、业务失败、无权限、路径越界、超时和用户取消。
6. 写入工具应触发当前审批策略；只读工具在 Plan 中应可用。
7. 生成文件时确认对话中出现文件卡片，并能通过系统应用打开。

仓库内协议测试位于 `internal/plugins/protocol_test.go`，Manifest、安装和权限测试位于同目录其他 `*_test.go`。修改 ABI 后必须保持工具排序稳定，避免无意义破坏模型请求前缀缓存。

## 10. 当前边界

- `office-artifacts` 是宿主内置工具目录，不是需要启动外部进程的第三方插件；它不依赖本机 Office、COM、ADO 或 Python 运行时。
- 新建正式 XLSX 使用内置 `spreadsheet_create`，必须提供样式和列宽；它支持公式、合并、行列尺寸、冻结窗格、列表验证和打印布局。`spreadsheet_write_range` 仅用于现有文件的小块修改。
- XLSX 生成后必须调用 `spreadsheet_inspect`，核对预期公式、合并、样式、验证和冻结窗格是否真实写入，不能只凭文件存在就宣称完成。
- 当前支持 DOCX、XLS/XLSX 与 PPTX；旧版 XLS 只读并可转换为 XLSX，Access、DOC、PPT 等格式尚未实现。
- 办公产物是二进制文件。当前对话会显示可打开、可结构化预览的产物，但文本 checkpoint/rewind 不会伪装成能够恢复二进制文档内容。
- ABI 1.0 没有常驻插件进程、插件间依赖、签名市场、流式插件通知或嵌套路径参数。
- 安装第三方插件等同于信任其本地代码；只安装来源可审计的插件。
