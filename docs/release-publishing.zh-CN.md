# 发布说明发布流程

每个版本的 GitHub Release 正文必须来自 `docs/releases/vX.Y.Z.md`。这些文件统一使用 UTF-8 无 BOM 保存，禁止从 Windows PowerShell 直接把未指定编码的管道输出粘贴到发布页。

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
