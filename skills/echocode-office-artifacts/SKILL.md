---
name: echocode-office-artifacts
description: 创建或修改 DOCX、XLSX、XLSM、PPTX 等办公产物时使用，确保结构、排版、公式和预览可验证。
---

# EchoCode 办公产物

- 使用 `office-artifacts` 第一方结构化工具创建和修改文件，不通过 Shell 拼接 OOXML、CSV 或脚本冒充正式产物。
- 正式 XLSX 使用 `spreadsheet_create`，设置与内容匹配的标题、样式、列宽、行高、冻结窗格、数据验证和打印布局。
- `spreadsheet_write_range` 只适合修改现有工作簿的小块数据；以 `=` 开头表示公式，普通等号文本使用 `'=`。
- DOCX 使用明确的标题、段落层级和表格结构；PPTX 保持可读字号、稳定版式和每页单一重点。
- 创建后调用对应 inspect/preview 工具验证结构；涉及视觉质量时继续调用 `render_artifact` 和 `inspect_visual`。
- 发现公式、样式、合并、裁切或分页与任务要求不符时，修正并重新验证，不能只确认文件存在。
- 最终回答保留规范化绝对路径，并明确区分结构验证、EchoCode 预览和原生 Office 保真验收。
