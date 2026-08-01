package artifacts

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
)

func ArtifactText(path string, maxChars int) (string, error) {
	if maxChars <= 0 || maxChars > 2<<20 {
		maxChars = 256 << 10
	}
	kind, _, ok := Detect(path)
	if !ok {
		return "", fmt.Errorf("不支持的办公产物格式: %s", filepath.Ext(path))
	}
	switch kind {
	case KindDocument:
		return DocumentText(path, maxChars)
	case KindPresentation:
		return PresentationText(path, maxChars)
	case KindSpreadsheet:
		preview, err := PreviewSpreadsheet(path, PreviewOptions{MaxSheets: 20, MaxRows: 100, MaxColumns: 40})
		if err != nil {
			return "", err
		}
		var builder strings.Builder
		fmt.Fprintf(&builder, "工作簿：%s\n", filepath.Base(path))
		for _, sheet := range preview.Sheets {
			fmt.Fprintf(&builder, "\n工作表 %s（%d 行 × %d 列）\n", sheet.Name, sheet.RowCount, sheet.ColumnCount)
			for _, row := range sheet.Rows {
				encoded, _ := json.Marshal(row)
				builder.Write(encoded)
				builder.WriteByte('\n')
				if builder.Len() >= maxChars {
					value, _ := boundedRunes(builder.String(), maxChars)
					return value + "\n... [预览已截断]", nil
				}
			}
			if sheet.Truncated {
				builder.WriteString("... [工作表预览已限制行列]\n")
			}
		}
		return strings.TrimSpace(builder.String()), nil
	default:
		return "", fmt.Errorf("无法读取办公产物 %s", path)
	}
}
