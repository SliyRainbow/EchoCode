package plugins

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
	"github.com/xuri/excelize/v2"
)

func TestBuiltinSpreadsheetCreateProducesFormulaAndLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.xlsx")
	args := map[string]any{
		"path":   path,
		"sheet":  "Report",
		"values": [][]any{{"Title", ""}, {"Value", "=1+1"}},
		"merges": []string{"A1:B1"},
		"styles": []map[string]any{
			{"range": "A1:B1", "bold": true, "fillColor": "166534", "fontColor": "FFFFFF", "horizontal": "center"},
			{"range": "A2:B2", "borderStyle": "thin"},
		},
		"columns": []map[string]any{{"start": "A", "end": "B", "width": 14}},
	}
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	result, err := executeArtifactTool("spreadsheet_create", raw)
	if err != nil || result.IsError || !strings.Contains(result.Summary, "专业 XLSX") {
		t.Fatalf("result = %#v, err = %v", result, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer workbook.Close()
	if formula, _ := workbook.GetCellFormula("Report", "B2"); formula != "1+1" {
		t.Fatalf("formula = %q", formula)
	}
	if style, _ := workbook.GetCellStyle("Report", "A1"); style == 0 {
		t.Fatal("spreadsheet_create did not apply styles")
	}
}

func TestArtifactVerificationReopensGeneratedSpreadsheet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "verified.xlsx")
	args := map[string]any{
		"path":    path,
		"sheet":   "Report",
		"values":  [][]any{{"Name", "Score"}, {"EchoCode", 100}},
		"styles":  []map[string]any{{"range": "A1:B1", "bold": true, "fillColor": "1F4E78", "fontColor": "FFFFFF"}},
		"columns": []map[string]any{{"start": "A", "end": "B", "width": 16}},
	}
	raw, _ := json.Marshal(args)
	result, err := executeArtifactTool("spreadsheet_create", raw)
	if err != nil || result.IsError {
		t.Fatalf("create result = %#v, err = %v", result, err)
	}

	summary, err := artifactVerificationSummary(path, artifacts.KindSpreadsheet)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"工作簿：verified.xlsx", "Report", "样式"} {
		if !strings.Contains(summary, expected) {
			t.Fatalf("verification summary missing %q: %s", expected, summary)
		}
	}
}
