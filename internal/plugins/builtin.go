package plugins

const ArtifactPluginID = "office-artifacts"

const (
	legacyOfficePluginID = "microsoft-office"
	legacyAccessPluginID = "microsoft-access"
)

func builtinManifests() []Manifest {
	object := func(properties map[string]any, required ...string) map[string]any {
		schema := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
		if len(required) > 0 {
			schema["required"] = required
		}
		return schema
	}
	path := map[string]any{"type": "string", "description": "工作区内的文件路径"}
	text := map[string]any{"type": "string"}
	positiveInteger := map[string]any{"type": "integer", "minimum": 1}
	nonNegativeInteger := map[string]any{"type": "integer", "minimum": 0}
	number := map[string]any{"type": "number"}
	boolean := map[string]any{"type": "boolean"}
	readPermission := PermissionSpec{FileRead: true}
	writePermission := PermissionSpec{FileRead: true, FileWrite: true}
	paragraph := object(map[string]any{
		"text":  text,
		"style": map[string]any{"type": "string", "enum": []string{"normal", "title", "heading1", "heading2", "list"}},
	}, "text")
	slide := object(map[string]any{"title": text, "body": text}, "title")
	spreadsheetStyle := object(map[string]any{
		"range":        text,
		"fontName":     text,
		"fontSize":     map[string]any{"type": "number", "minimum": 6, "maximum": 72},
		"bold":         boolean,
		"italic":       boolean,
		"fontColor":    text,
		"fillColor":    text,
		"horizontal":   map[string]any{"type": "string", "enum": []string{"left", "center", "right", "fill", "justify", "centerContinuous", "distributed"}},
		"vertical":     map[string]any{"type": "string", "enum": []string{"top", "center", "justify", "distributed"}},
		"wrapText":     boolean,
		"shrinkToFit":  boolean,
		"textRotation": map[string]any{"type": "integer", "minimum": -90, "maximum": 90},
		"borderStyle":  map[string]any{"type": "string", "enum": []string{"none", "thin", "medium", "dashed", "dotted", "thick", "double", "hair", "mediumDashed", "dashDot", "mediumDashDot", "dashDotDot", "mediumDashDotDot", "slantDashDot"}},
		"borderColor":  text,
		"numberFormat": text,
	}, "range")
	spreadsheetColumn := object(map[string]any{
		"start": text,
		"end":   text,
		"width": map[string]any{"type": "number", "minimum": 1, "maximum": 255},
	}, "start", "width")
	spreadsheetRow := object(map[string]any{
		"start":  positiveInteger,
		"end":    positiveInteger,
		"height": map[string]any{"type": "number", "minimum": 1, "maximum": 409},
	}, "start", "height")
	spreadsheetPage := object(map[string]any{
		"orientation":        map[string]any{"type": "string", "enum": []string{"portrait", "landscape"}},
		"paperSize":          map[string]any{"type": "integer", "minimum": 1, "maximum": 118},
		"fitToWidth":         map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
		"fitToHeight":        map[string]any{"type": "integer", "minimum": 0, "maximum": 100},
		"printArea":          text,
		"repeatRows":         text,
		"marginTop":          number,
		"marginBottom":       number,
		"marginLeft":         number,
		"marginRight":        number,
		"centerHorizontally": boolean,
		"centerVertically":   boolean,
	})
	spreadsheetValidation := object(map[string]any{
		"range":       text,
		"values":      map[string]any{"type": "array", "minItems": 1, "items": text},
		"allowBlank":  boolean,
		"promptTitle": text,
		"prompt":      text,
		"errorTitle":  text,
		"error":       text,
	}, "range", "values")
	spreadsheetValues := map[string]any{
		"type":        "array",
		"minItems":    1,
		"description": "二维单元格数据；以 = 开头的字符串写成公式，以 '= 开头可写入等号开头的普通文本。",
		"items":       map[string]any{"type": "array", "items": map[string]any{}},
	}

	manifest := Manifest{
		SchemaVersion: 1,
		ID:            ArtifactPluginID,
		Name:          "办公产物",
		Version:       "1.0.0",
		Description:   "不依赖本机 Office，读取、创建和编辑标准 DOCX、XLS/XLSX 与 PPTX 文件。",
		Author:        "EchoCode",
		Runtime:       Runtime{Transport: "builtin"},
		Permissions:   PermissionSpec{FileRead: true, FileWrite: true},
		Tools: []ToolManifest{
			{Name: "document_inspect", Description: "读取 DOCX 的段落和表格内容。", ReadOnly: true, Permissions: readPermission, Paths: []PathRequirement{{Argument: "path", Access: "read"}}, InputSchema: object(map[string]any{"path": path, "maxChars": positiveInteger}, "path")},
			{Name: "document_create", Description: "创建能被 Microsoft Word 打开的标准 DOCX 文档。", Permissions: writePermission, Paths: []PathRequirement{{Argument: "path", Access: "write"}}, InputSchema: object(map[string]any{"path": path, "title": text, "text": text, "paragraphs": map[string]any{"type": "array", "items": paragraph}}, "path")},
			{Name: "document_replace_text", Description: "替换 DOCX 正文和表格中的文本。", Permissions: writePermission, Paths: []PathRequirement{{Argument: "path", Access: "write"}}, InputSchema: object(map[string]any{"path": path, "find": text, "replace": text}, "path", "find", "replace")},
			{Name: "spreadsheet_inspect", Description: "读取 XLS/XLSX 的范围、公式、合并、样式、数据验证和冻结窗格摘要；生成后必须调用并核对质量。", ReadOnly: true, Permissions: readPermission, Paths: []PathRequirement{{Argument: "path", Access: "read"}}, InputSchema: object(map[string]any{"path": path}, "path")},
			{Name: "spreadsheet_create", Description: "声明式创建一份排版完整、可由 Excel 打开的专业 XLSX。新建考勤表、报表、清单、报价单等正式文件必须使用此工具，并提供样式、列宽以及需要的合并、冻结、下拉验证和打印布局；不要用裸数据代替成品。", Permissions: writePermission, Paths: []PathRequirement{{Argument: "path", Access: "write"}}, InputSchema: object(map[string]any{
				"path":          path,
				"sheet":         text,
				"startCell":     text,
				"values":        spreadsheetValues,
				"overwrite":     boolean,
				"merges":        map[string]any{"type": "array", "items": text},
				"styles":        map[string]any{"type": "array", "minItems": 1, "items": spreadsheetStyle},
				"columns":       map[string]any{"type": "array", "minItems": 1, "items": spreadsheetColumn},
				"rows":          map[string]any{"type": "array", "items": spreadsheetRow},
				"freezeRows":    nonNegativeInteger,
				"freezeColumns": nonNegativeInteger,
				"showGridLines": boolean,
				"zoomScale":     map[string]any{"type": "number", "minimum": 10, "maximum": 400},
				"page":          spreadsheetPage,
				"validations":   map[string]any{"type": "array", "items": spreadsheetValidation},
				"autoFilter":    text,
			}, "path", "sheet", "values", "styles", "columns")},
			{Name: "spreadsheet_read_range", Description: "读取 XLS/XLSX 的指定单元格范围；省略 range 时读取已使用区域。", ReadOnly: true, Permissions: readPermission, Paths: []PathRequirement{{Argument: "path", Access: "read"}}, InputSchema: object(map[string]any{"path": path, "sheet": text, "range": text, "maxCells": positiveInteger}, "path")},
			{Name: "spreadsheet_write_range", Description: "只用于修改现有 XLSX 的小块数据；以 = 开头的字符串写成公式，以 '= 开头写入等号开头的普通文本。创建正式工作簿请使用 spreadsheet_create。", Permissions: writePermission, Paths: []PathRequirement{{Argument: "path", Access: "write"}}, InputSchema: object(map[string]any{"path": path, "sheet": text, "startCell": text, "values": spreadsheetValues}, "path", "sheet", "startCell", "values")},
			{Name: "spreadsheet_add_sheet", Description: "向 XLSX 工作簿添加工作表。", Permissions: writePermission, Paths: []PathRequirement{{Argument: "path", Access: "write"}}, InputSchema: object(map[string]any{"path": path, "sheet": text}, "path", "sheet")},
			{Name: "spreadsheet_import_xls", Description: "把旧版二进制 XLS 完整导入为可继续编辑的标准 XLSX。", Permissions: writePermission, Paths: []PathRequirement{{Argument: "path", Access: "read"}, {Argument: "outputPath", Access: "write"}}, InputSchema: object(map[string]any{"path": path, "outputPath": path}, "path", "outputPath")},
			{Name: "presentation_inspect", Description: "按顺序读取 PPTX 每页的标题和文本。", ReadOnly: true, Permissions: readPermission, Paths: []PathRequirement{{Argument: "path", Access: "read"}}, InputSchema: object(map[string]any{"path": path, "maxChars": positiveInteger}, "path")},
			{Name: "presentation_create", Description: "创建能被 Microsoft PowerPoint 打开的标准 PPTX 演示文稿。", Permissions: writePermission, Paths: []PathRequirement{{Argument: "path", Access: "write"}}, InputSchema: object(map[string]any{"path": path, "slides": map[string]any{"type": "array", "minItems": 1, "items": slide}}, "path", "slides")},
			{Name: "presentation_add_slide", Description: "向 PPTX 追加标题和正文页。", Permissions: writePermission, Paths: []PathRequirement{{Argument: "path", Access: "write"}}, InputSchema: object(map[string]any{"path": path, "title": text, "body": text}, "path", "title")},
			{Name: "presentation_replace_text", Description: "替换 PPTX 幻灯片中的文本。", Permissions: writePermission, Paths: []PathRequirement{{Argument: "path", Access: "write"}}, InputSchema: object(map[string]any{"path": path, "find": text, "replace": text}, "path", "find", "replace")},
		},
	}
	for index := range manifest.Tools {
		manifest.Tools[index].TimeoutSeconds = 120
	}
	normalizeManifest(&manifest)
	return []Manifest{manifest}
}
