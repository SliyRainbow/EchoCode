package artifacts

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestDocumentRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.docx")
	err := CreateDocument(path, DocumentSpec{
		Title: "EchoCode Report",
		Paragraphs: []DocumentParagraph{
			{Text: "First paragraph"},
			{Text: "Section", Style: "heading1"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertPackageEntries(t, path, "[Content_Types].xml", "word/document.xml", "word/styles.xml")
	text, err := DocumentText(path, 10000)
	if err != nil || !strings.Contains(text, "EchoCode Report") || !strings.Contains(text, "First paragraph") {
		t.Fatalf("document text = %q, err = %v", text, err)
	}
	count, err := ReplaceDocumentText(path, "First", "Updated")
	if err != nil || count != 1 {
		t.Fatalf("replace count = %d, err = %v", count, err)
	}
	text, err = DocumentText(path, 10000)
	if err != nil || !strings.Contains(text, "Updated paragraph") {
		t.Fatalf("updated document text = %q, err = %v", text, err)
	}
}

func TestSpreadsheetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.xlsx")
	if err := WriteSpreadsheetRange(path, "Report", "A1", [][]any{{"Name", "Value"}, {"EchoCode", 42}}); err != nil {
		t.Fatal(err)
	}
	if err := AddSpreadsheetSheet(path, "Archive"); err != nil {
		t.Fatal(err)
	}
	rows, err := ReadSpreadsheetRange(path, "Report", "A1:B2", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || len(rows[1]) != 2 || rows[1][0] != "EchoCode" || rows[1][1] != "42" {
		t.Fatalf("rows = %#v", rows)
	}
	preview, err := PreviewSpreadsheet(path, DefaultPreviewOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Sheets) != 2 || preview.Sheets[0].Name != "Report" {
		t.Fatalf("preview = %#v", preview)
	}
}

func TestRenderPreviewHTMLEscapesContentAndHonorsSelections(t *testing.T) {
	document := RenderPreviewHTML(Preview{
		Kind:     KindDocument,
		Document: &DocumentPreview{Blocks: []DocumentBlock{{Type: "paragraph", Style: "title", Text: `<unsafe & title>`}}},
	}, HTMLRenderSelection{})
	if !strings.Contains(document, "&lt;unsafe &amp; title&gt;") || strings.Contains(document, `<unsafe & title>`) {
		t.Fatalf("document HTML did not escape content: %s", document)
	}

	spreadsheet := RenderPreviewHTML(Preview{
		Kind: KindSpreadsheet,
		Spreadsheet: &SpreadsheetPreview{ActiveSheet: "First", Sheets: []SpreadsheetSheet{
			{Name: "First", Rows: [][]string{{"hidden value"}}, RowCount: 1, ColumnCount: 1},
			{Name: "Selected <sheet>", Rows: [][]string{{"visible & safe"}}, RowCount: 1, ColumnCount: 1},
		}},
	}, HTMLRenderSelection{Sheet: "Selected <sheet>"})
	if !strings.Contains(spreadsheet, "visible &amp; safe") || strings.Contains(spreadsheet, "hidden value") || !strings.Contains(spreadsheet, "Selected &lt;sheet&gt;") {
		t.Fatalf("spreadsheet selection HTML=%s", spreadsheet)
	}

	presentation := RenderPreviewHTML(Preview{
		Kind: KindPresentation,
		Presentation: &PresentationPreview{Slides: []PresentationSlide{
			{Number: 1, Title: "First", Texts: []string{"not selected"}},
			{Number: 2, Title: "Second", Texts: []string{"selected body"}},
		}},
	}, HTMLRenderSelection{Slide: 2})
	if !strings.Contains(presentation, "selected body") || strings.Contains(presentation, "not selected") {
		t.Fatalf("presentation selection HTML=%s", presentation)
	}
}

func TestSpreadsheetWriteRangePreservesFormulasBlanksAndLiteralEquals(t *testing.T) {
	path := filepath.Join(t.TempDir(), "formula.xlsx")
	values := [][]any{{"=SUM(B1:C1)", 2, 3, "", "'=literal"}}
	if err := WriteSpreadsheetRange(path, "Report", "A1", values); err != nil {
		t.Fatal(err)
	}
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer workbook.Close()
	formula, err := workbook.GetCellFormula("Report", "A1")
	if err != nil || formula != "SUM(B1:C1)" {
		t.Fatalf("formula = %q, err = %v", formula, err)
	}
	if value, _ := workbook.GetCellValue("Report", "D1"); value != "" {
		t.Fatalf("blank cell = %q", value)
	}
	if value, _ := workbook.GetCellValue("Report", "E1"); value != "=literal" {
		t.Fatalf("literal equals = %q", value)
	}
	if formula, _ := workbook.GetCellFormula("Report", "E1"); formula != "" {
		t.Fatalf("literal cell unexpectedly has formula %q", formula)
	}
	rows, err := ReadSpreadsheetRange(path, "Report", "A1:E1", 10)
	if err != nil || len(rows) != 1 || rows[0][0] != "=SUM(B1:C1)" {
		t.Fatalf("formula fallback rows = %#v, err = %v", rows, err)
	}
	preview, err := PreviewSpreadsheet(path, DefaultPreviewOptions())
	if err != nil || len(preview.Sheets) != 1 || preview.Sheets[0].Rows[0][0] != "=SUM(B1:C1)" {
		t.Fatalf("formula fallback preview = %#v, err = %v", preview, err)
	}
	summary, err := SpreadsheetSummary(path)
	if err != nil || !strings.Contains(summary, "公式 1") {
		t.Fatalf("summary = %q, err = %v", summary, err)
	}
}

func TestCreateSpreadsheetAppliesProfessionalLayout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "professional.xlsx")
	showGridLines := false
	paperSize, fitToWidth, fitToHeight := 9, 1, 0
	centerHorizontally := true
	spec := SpreadsheetCreateSpec{
		Sheet:     "考勤表",
		StartCell: "A1",
		Values: [][]any{
			{"员工月度考勤表", "", "", "", ""},
			{"姓名", "1日", "2日", "3日", "出勤天数"},
			{"张三", "√", "迟", "√", "=COUNTIF(B3:D3,\"√\")"},
			{"李四", "√", "√", "休", "=COUNTIF(B4:D4,\"√\")"},
		},
		Merges: []string{"A1:E1"},
		Styles: []SpreadsheetStyleSpec{
			{Range: "A1:E1", FontSize: 16, Bold: true, FontColor: "FFFFFF", FillColor: "166534", Horizontal: "center"},
			{Range: "A2:E2", Bold: true, FillColor: "DCFCE7", Horizontal: "center", WrapText: true, BorderStyle: "thin"},
			{Range: "A3:E4", Horizontal: "center", BorderStyle: "thin", BorderColor: "D1D5DB"},
		},
		Columns: []SpreadsheetColumnSpec{
			{Start: "A", Width: 14},
			{Start: "B", End: "D", Width: 7},
			{Start: "E", Width: 12},
		},
		Rows:          []SpreadsheetRowSpec{{Start: 1, Height: 28}, {Start: 2, End: 4, Height: 22}},
		FreezeRows:    2,
		FreezeColumns: 1,
		ShowGridLines: &showGridLines,
		ZoomScale:     90,
		Page: &SpreadsheetPageSpec{
			Orientation: "landscape", PaperSize: &paperSize,
			FitToWidth: &fitToWidth, FitToHeight: &fitToHeight,
			PrintArea: "A1:E4", RepeatRows: "1:2", CenterHorizontally: &centerHorizontally,
		},
		Validations: []SpreadsheetValidationListSpec{{
			Range: "B3:D4", Values: []string{"√", "迟", "早", "休", "请", "旷"}, AllowBlank: true,
		}},
		AutoFilter: "A2:E4",
	}
	if err := CreateSpreadsheet(path, spec); err != nil {
		t.Fatal(err)
	}
	if err := CreateSpreadsheet(path, spec); err == nil || !strings.Contains(err.Error(), "overwrite=true") {
		t.Fatalf("expected overwrite guard, got %v", err)
	}

	workbook, err := excelize.OpenFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer workbook.Close()
	if formula, _ := workbook.GetCellFormula("考勤表", "E3"); formula != "COUNTIF(B3:D3,\"√\")" {
		t.Fatalf("formula = %q", formula)
	}
	merges, err := workbook.GetMergeCells("考勤表", true)
	if err != nil || len(merges) != 1 {
		t.Fatalf("merges = %#v, err = %v", merges, err)
	}
	if styleID, _ := workbook.GetCellStyle("考勤表", "A1"); styleID == 0 {
		t.Fatal("title style was not applied")
	}
	if width, _ := workbook.GetColWidth("考勤表", "A"); width < 13.9 || width > 14.1 {
		t.Fatalf("column A width = %v", width)
	}
	if height, _ := workbook.GetRowHeight("考勤表", 1); height < 27.9 || height > 28.1 {
		t.Fatalf("row 1 height = %v", height)
	}
	panes, err := workbook.GetPanes("考勤表")
	if err != nil || !panes.Freeze || panes.XSplit != 1 || panes.YSplit != 2 {
		t.Fatalf("panes = %#v, err = %v", panes, err)
	}
	validations, err := workbook.GetDataValidations("考勤表")
	if err != nil || len(validations) != 1 {
		t.Fatalf("validations = %#v, err = %v", validations, err)
	}
	summary, err := SpreadsheetSummary(path)
	for _, marker := range []string{"公式 2", "合并 1", "数据验证 1", "冻结窗格 是"} {
		if err != nil || !strings.Contains(summary, marker) {
			t.Fatalf("summary = %q, err = %v, missing %q", summary, err, marker)
		}
	}
}

func TestPresentationRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "deck.pptx")
	if err := CreatePresentation(path, []SlideSpec{{Title: "EchoCode", Body: "First line\nSecond line"}}); err != nil {
		t.Fatal(err)
	}
	assertPackageEntries(t, path, "[Content_Types].xml", "ppt/presentation.xml", "ppt/slides/slide1.xml", "ppt/theme/theme1.xml")
	if err := AddPresentationSlide(path, SlideSpec{Title: "Review", Body: "Ready"}); err != nil {
		t.Fatal(err)
	}
	preview, err := PreviewPresentation(path, DefaultPreviewOptions())
	if err != nil {
		t.Fatal(err)
	}
	if len(preview.Slides) != 2 || preview.Slides[0].Title != "EchoCode" || preview.Slides[1].Title != "Review" {
		t.Fatalf("preview = %#v", preview)
	}
	count, err := ReplacePresentationText(path, "Ready", "Verified")
	if err != nil || count != 1 {
		t.Fatalf("replace count = %d, err = %v", count, err)
	}
	text, err := PresentationText(path, 10000)
	if err != nil || !strings.Contains(text, "Verified") {
		t.Fatalf("presentation text = %q, err = %v", text, err)
	}
}

func TestPreviewFileDetectsSupportedArtifacts(t *testing.T) {
	if kind, _, ok := Detect("report.XLS"); !ok || kind != KindSpreadsheet {
		t.Fatalf("legacy XLS detection = %q, %v", kind, ok)
	}
	if _, _, ok := Detect("report.pdf"); ok {
		t.Fatal("PDF should not be detected as an editable office artifact")
	}
}

func TestGenerateCompatibilityFixtures(t *testing.T) {
	root := os.Getenv("ECHOCODE_ARTIFACT_FIXTURE_DIR")
	if root == "" {
		t.Skip("set ECHOCODE_ARTIFACT_FIXTURE_DIR to keep compatibility fixtures")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := CreateDocument(filepath.Join(root, "echocode-compat.docx"), DocumentSpec{Title: "EchoCode 兼容性验证", Paragraphs: []DocumentParagraph{{Text: "Microsoft Word 应能直接打开此文档。"}, {Text: "结构化产物", Style: "heading1"}}}); err != nil {
		t.Fatal(err)
	}
	if err := WriteSpreadsheetRange(filepath.Join(root, "echocode-compat.xlsx"), "兼容性", "A1", [][]any{{"产物", "状态"}, {"EchoCode", "可由 Excel 打开"}}); err != nil {
		t.Fatal(err)
	}
	if err := CreatePresentation(filepath.Join(root, "echocode-compat.pptx"), []SlideSpec{{Title: "EchoCode 兼容性验证", Body: "Microsoft PowerPoint 应能直接打开此演示文稿。"}}); err != nil {
		t.Fatal(err)
	}
}

func assertPackageEntries(t *testing.T, path string, names ...string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil || info.Size() < 1000 {
		t.Fatalf("artifact %s info = %#v, err = %v", path, info, err)
	}
	reader, err := zip.OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	entries := map[string]bool{}
	for _, item := range reader.File {
		entries[item.Name] = true
	}
	for _, name := range names {
		if !entries[name] {
			t.Fatalf("artifact %s is missing %s", path, name)
		}
	}
}
