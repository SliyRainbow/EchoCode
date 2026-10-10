# 发布说明发布流程

每个版本的 GitHub Release 正文必须来自 `docs/releases/vX.Y.Z.md`。这些文件统一使用 UTF-8 无 BOM 保存，禁止从 Windows PowerShell 直接把未指定编码的管道输出粘贴到发布页。

## Windows 程序包

同步 `version.go`、`wails.json`、前端 package/fallback、MCP 客户端版本、Windows 资源和 Inno Setup 脚本，再更新 `CHANGELOG.md` 与版本说明。

将验证后的提交推送到仓库后，手动触发 `Windows release build` 工作流，传入计划发布的标签 `vX.Y.Z`。工作流固定构建触发时的提交，使用 GitHub Windows Runner 预装的 Inno Setup，执行 Go/前端检查、生产构建、真实安装和卸载验证，并上传包含以下文件的构建产物：

- `EchoCode.exe` 与安装版 `EchoCode-setup-X.Y.Z.exe`。
- `EchoCode-X.Y.Z-windows-amd64-portable.zip`，其名称与内置更新器的回退地址一致。
- 三个包各自的单行 `.sha256` 和完整的 `SHA256SUMS.txt`。
- `release-build.json`：源码提交、版本、构建时间、文件大小与摘要，用于核对发布来源。

本地已配置工具时，可运行 `scripts/Build-WindowsRelease.ps1 -Version X.Y.Z`。脚本不会安装系统工具。安装/卸载冒烟脚本仅允许在隔离的 GitHub Runner 上运行，避免覆盖现有 EchoCode 安装。

构建通过后，核对 `release-build.json` 的源码提交与工作流提交一致，再在该提交上创建并推送 `vX.Y.Z` 标签。先创建 draft Release，上传并核对程序包和摘要，最后公开发布。现有源码提交和版本标签不得因重试而覆盖或移动。

## 发布说明

更新已有 Release：

```powershell
$env:GITHUB_TOKEN = "具有仓库内容写入权限的令牌"
.\scripts\Publish-GitHubRelease.ps1 -Tag v0.3.18
```

脚本会：

- 使用 UTF-8 明确读取发布说明；
- 检查文件为空或包含 Unicode 替换字符；
- 按版本标签定位已有 Release；
- 使用 UTF-8 JSON 正文更新 Release；
- 输出 Release 地址和正文字节数，不输出令牌。

发布前先在本地打开对应 Markdown 文件检查中文显示，再运行全量测试和 Windows 构建。
