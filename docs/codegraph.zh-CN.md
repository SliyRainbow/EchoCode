# CodeGraph 接入

EchoCode 可以通过 MCP 搭配 CodeGraph。CodeGraph 负责建立本地代码关系图，EchoCode Agent 负责理解任务、选择工具、修改文件和验证结果；两者不是二选一。

## 推荐安装方式

1. 打开 EchoCode 的“设置 → 扩展中心”。

2. 选择 CodeGraph，查看发布者、源码、许可证、权限和固定版本下载来源。

3. 点击“安装”。EchoCode 会自动选择当前系统和架构的官方发布包，校验 SHA-256、执行版本健康检查，并写入 MCP 配置后重新连接。

4. 打开需要分析的项目，在 CodeGraph 扩展详情中点击“建立索引”。该操作会在项目内创建 `.codegraph` 目录。

5. 索引建立后，Agent 可使用 CodeGraph 查询模块关系、调用链、符号源码和影响范围；精确读写文件仍使用 EchoCode 自身的结构化工具。

扩展安装在用户目录中，不会增大 EchoCode 主程序。卸载 CodeGraph 时不会删除项目内 `.codegraph`，避免用户误丢索引数据。

## 手工安装与配置

无法使用扩展中心时，也可以自行安装 CodeGraph CLI，然后在“设置 → MCP”点击 CodeGraph 预设。EchoCode 会自动填入：

   ```text
   传输：stdio
   命令：codegraph
   参数：serve
        --mcp
   工作目录：留空，跟随当前项目工作区
   环境变量：CODEGRAPH_NO_DAEMON=1
            CODEGRAPH_TELEMETRY=0
   ```

   `CODEGRAPH_NO_DAEMON=1` 让 CodeGraph 进程由 EchoCode 管理，避免 Windows 上额外的共享后台进程和索引目录锁；`CODEGRAPH_TELEMETRY=0` 默认关闭匿名遥测。已有 CodeGraph 配置再次点击预设按钮时，只会补充缺失的默认环境变量，不会覆盖用户已经填写的值。

保存并重新连接后即可使用。默认 MCP 工具是 `codegraph_explore`。

## 手工配置

如果不使用预设，也可以新建一个 stdio MCP 服务，配置等价于：

```json
{
  "name": "CodeGraph",
  "transport": "stdio",
  "command": "codegraph",
  "args": ["serve", "--mcp"],
  "env": [
    { "key": "CODEGRAPH_NO_DAEMON", "value": "1" },
    { "key": "CODEGRAPH_TELEMETRY", "value": "0" }
  ],
  "workingDirectory": "",
  "enabled": true,
  "toolResultPolicy": "summary-first"
}
```

## 多项目与后台任务

- EchoCode 的 MCP 连接可以由多个会话共享，但每个工具包装器都绑定自己的会话工作区。
- 调用本地 CodeGraph 时，EchoCode 会自动注入当前会话的规范化绝对路径作为 `projectPath`，因此主 Agent、子代理和后台会话可以同时分析不同项目。
- 如果模型或用户已经显式提供 `projectPath`，EchoCode 不会覆盖它，可用于分析另一个已授权且已建立索引的项目。
- 自动路径注入只对本机 `stdio` 且启动命令确认为 `codegraph serve --mcp` 的服务启用，不会把本地路径发送给普通 MCP 或远程 HTTP/SSE 服务。
- Windows 上不需要为项目切换反复重启 CodeGraph。若索引提示过期，可在对应项目中执行 `codegraph sync` 后继续。

## Agent 使用边界

- 已建立 `.codegraph` 索引时，结构关系问题优先使用 `codegraph_explore`。
- 普通文件读取、精确文本搜索、写入、补丁和测试仍由 EchoCode 的结构化工具完成。
- 没有索引或索引过期时，CodeGraph 应返回明确提示；Agent 可以改用 `read_file`、`search` 和其他真实工具核验。
- Agent 不会自行执行 `codegraph init`。是否创建 `.codegraph` 索引由用户决定。
- CodeGraph 只提供代码理解证据，不自动获得 EchoCode 的写文件、Shell、Git 或网络权限。
- `.codegraph` 是项目产物，是否提交到 Git 按项目自身规则决定，通常不应把本地数据库提交进仓库。

官方项目：`colbymchenry/codegraph`。
