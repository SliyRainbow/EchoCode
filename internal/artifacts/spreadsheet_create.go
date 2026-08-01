package artifacts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/xuri/excelize/v2"
)

type SpreadsheetCreateSpec struct {
	Sheet         string                          `json:"sheet"`
	StartCell     string                          `json:"startCell,omitempty"`
	Values        [][]any                         `json:"values"`
	Overwrite     bool                            `json:"overwrite,omitempty"`
	Merges        []string                        `json:"merges,omitempty"`
	Styles        []SpreadsheetStyleSpec          `json:"styles"`
	Columns       []SpreadsheetColumnSpec         `json:"columns"`
	Rows          []SpreadsheetRowSpec            `json:"rows,omitempty"`
	FreezeRows    int                             `json:"freezeRows,omitempty"`
	FreezeColumns int                             `json:"freezeColumns,omitempty"`
	ShowGridLines *bool                           `json:"showGridLines,omitempty"`
	ZoomScale     float64                         `json:"zoomScale,omitempty"`
	Page          *SpreadsheetPageSpec            `json:"page,omitempty"`
	Validations   []SpreadsheetValidationListSpec `json:"validations,omitempty"`
	AutoFilter    string                          `json:"autoFilter,omitempty"`
}

type SpreadsheetStyleSpec struct {
	Range        string  `json:"range"`
	FontName     string  `json:"fontName,omitempty"`
	FontSize     float64 `json:"fontSize,omitempty"`
	Bold         bool    `json:"bold,omitempty"`
	Italic       bool    `json:"italic,omitempty"`
	FontColor    string  `json:"fontColor,omitempty"`
	FillColor    string  `json:"fillColor,omitempty"`
	Horizontal   string  `json:"horizontal,omitempty"`
	Vertical     string  `json:"vertical,omitempty"`
	WrapText     bool    `json:"wrapText,omitempty"`
	ShrinkToFit  bool    `json:"shrinkToFit,omitempty"`
	TextRotation int     `json:"textRotation,omitempty"`
	BorderStyle  string  `json:"borderStyle,omitempty"`
	BorderColor  string  `json:"borderColor,omitempty"`
	NumberFormat string  `json:"numberFormat,omitempty"`
}

type SpreadsheetColumnSpec struct {
	Start string  `json:"start"`
	End   string  `json:"end,omitempty"`
	Width float64 `json:"width"`
}

type SpreadsheetRowSpec struct {
	Start  int     `json:"start"`
	End    int     `json:"end,omitempty"`
	Height float64 `json:"height"`
}

type SpreadsheetPageSpec struct {
	Orientation        string   `json:"orientation,omitempty"`
	PaperSize          *int     `json:"paperSize,omitempty"`
	FitToWidth         *int     `json:"fitToWidth,omitempty"`
	FitToHeight        *int     `json:"fitToHeight,omitempty"`
	PrintArea          string   `json:"printArea,omitempty"`
	RepeatRows         string   `json:"repeatRows,omitempty"`
	MarginTop          *float64 `json:"marginTop,omitempty"`
	MarginBottom       *float64 `json:"marginBottom,omitempty"`
	MarginLeft         *float64 `json:"marginLeft,omitempty"`
	MarginRight        *float64 `json:"marginRight,omitempty"`
	CenterHorizontally *bool    `json:"centerHorizontally,omitempty"`
	CenterVertically   *bool    `json:"centerVertically,omitempty"`
}

type SpreadsheetValidationListSpec struct {
	Range       string   `json:"range"`
	Values      []string `json:"values"`
	AllowBlank  bool     `json:"allowBlank,omitempty"`
	PromptTitle string   `json:"promptTitle,omitempty"`
	Prompt      string   `json:"prompt,omitempty"`
	ErrorTitle  string   `json:"errorTitle,omitempty"`
	Error       string   `json:"error,omitempty"`
}

var spreadsheetColorPattern = regexp.MustCompile(`^[0-9A-Fa-f]{6}([0-9A-Fa-f]{2})?$`)

func CreateSpreadsheet(path string, spec SpreadsheetCreateSpec) error {
	if !strings.EqualFold(filepath.Ext(path), ".xlsx") {
		return errors.New("创建表格必须使用 .xlsx")
	}
	if len(spec.Values) == 0 {
		return errors.New("创建表格的数据不能为空")
	}
	if len(spec.Styles) == 0 || len(spec.Columns) == 0 {
		return errors.New("正式工作簿必须提供 styles 和 columns，不能只生成裸数据")
	}
	if len(spec.Styles) > 256 || len(spec.Columns) > 256 || len(spec.Rows) > 2048 || len(spec.Merges) > 256 || len(spec.Validations) > 256 {
		return errors.New("工作簿布局配置超过安全上限")
	}
	if info, err := os.Stat(path); err == nil {
		if info.IsDir() {
			return errors.New("输出路径是目录")
		}
		if !spec.Overwrite {
			return errors.New("输出文件已存在；确认替换时请设置 overwrite=true")
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	sheetName := strings.TrimSpace(spec.Sheet)
	if sheetName == "" {
		sheetName = "Sheet1"
	}
	startCell := strings.TrimSpace(spec.StartCell)
	if startCell == "" {
		startCell = "A1"
	}
	startColumn, startRow, err := excelize.CellNameToCoordinates(startCell)
	if err != nil {
		return fmt.Errorf("起始单元格无效: %w", err)
	}
	endColumn, endRow, cellCount, err := spreadsheetMatrixBounds(spec.Values, startColumn, startRow)
	if err != nil {
		return err
	}
	if cellCount > 5_000_000 {
		return errors.New("单次创建超过 500 万个单元格")
	}

	workbook := excelize.NewFile()
	defer workbook.Close()
	if sheetName != "Sheet1" {
		if err := workbook.SetSheetName("Sheet1", sheetName); err != nil {
			return fmt.Errorf("工作表名称无效: %w", err)
		}
	}
	containsFormula, err := writeSpreadsheetMatrix(workbook, sheetName, startColumn, startRow, spec.Values)
	if err != nil {
		return err
	}

	endCell, _ := excelize.CoordinatesToCellName(endColumn, endRow)
	defaultStyle, err := workbook.NewStyle(&excelize.Style{
		Font:      &excelize.Font{Family: "Microsoft YaHei", Size: 10.5, Color: "1F2937"},
		Alignment: &excelize.Alignment{Vertical: "center"},
	})
	if err != nil {
		return fmt.Errorf("创建默认样式失败: %w", err)
	}
	if err := workbook.SetCellStyle(sheetName, startCell, endCell, defaultStyle); err != nil {
		return fmt.Errorf("应用默认样式失败: %w", err)
	}

	for _, cellRange := range spec.Merges {
		first, last, err := normalizeSpreadsheetRange(cellRange)
		if err != nil {
			return fmt.Errorf("合并区域 %q 无效: %w", cellRange, err)
		}
		if err := workbook.MergeCell(sheetName, first, last); err != nil {
			return fmt.Errorf("合并 %s 失败: %w", cellRange, err)
		}
	}
	for _, styleSpec := range spec.Styles {
		first, last, err := normalizeSpreadsheetRange(styleSpec.Range)
		if err != nil {
			return fmt.Errorf("样式区域 %q 无效: %w", styleSpec.Range, err)
		}
		style, err := spreadsheetStyle(styleSpec)
		if err != nil {
			return fmt.Errorf("样式区域 %s: %w", styleSpec.Range, err)
		}
		styleID, err := workbook.NewStyle(style)
		if err != nil {
			return fmt.Errorf("创建 %s 样式失败: %w", styleSpec.Range, err)
		}
		if err := workbook.SetCellStyle(sheetName, first, last, styleID); err != nil {
			return fmt.Errorf("应用 %s 样式失败: %w", styleSpec.Range, err)
		}
	}
	if err := applySpreadsheetDimensions(workbook, sheetName, spec.Columns, spec.Rows); err != nil {
		return err
	}
	if err := applySpreadsheetView(workbook, sheetName, spec); err != nil {
		return err
	}
	if err := applySpreadsheetPage(workbook, sheetName, spec.Page); err != nil {
		return err
	}
	if err := applySpreadsheetValidations(workbook, sheetName, spec.Validations); err != nil {
		return err
	}
	if strings.TrimSpace(spec.AutoFilter) != "" {
		first, last, err := normalizeSpreadsheetRange(spec.AutoFilter)
		if err != nil {
			return fmt.Errorf("筛选区域无效: %w", err)
		}
		if err := workbook.AutoFilter(sheetName, first+":"+last, []excelize.AutoFilterOptions{}); err != nil {
			return fmt.Errorf("设置自动筛选失败: %w", err)
		}
	}
	if containsFormula {
		if err := setSpreadsheetCalculation(workbook); err != nil {
			return err
		}
	}
	index, err := workbook.GetSheetIndex(sheetName)
	if err != nil {
		return err
	}
	workbook.SetActiveSheet(index)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建输出目录失败: %w", err)
	}
	if err := workbook.SaveAs(path); err != nil {
		return fmt.Errorf("保存 Excel 工作簿失败: %w", err)
	}
	return nil
}

func spreadsheetMatrixBounds(values [][]any, startColumn, startRow int) (endColumn, endRow, cellCount int, err error) {
	maxColumns := 0
	for _, row := range values {
		maxColumns = max(maxColumns, len(row))
		cellCount += len(row)
	}
	if maxColumns == 0 {
		return 0, 0, 0, errors.New("创建表格的数据行不能为空")
	}
	return startColumn + maxColumns - 1, startRow + len(values) - 1, cellCount, nil
}

func spreadsheetStyle(spec SpreadsheetStyleSpec) (*excelize.Style, error) {
	fontColor, err := normalizeSpreadsheetColor(spec.FontColor, "1F2937")
	if err != nil {
		return nil, fmt.Errorf("字体颜色无效: %w", err)
	}
	fontName := strings.TrimSpace(spec.FontName)
	if fontName == "" {
		fontName = "Microsoft YaHei"
	}
	fontSize := spec.FontSize
	if fontSize == 0 {
		fontSize = 10.5
	}
	if fontSize < 6 || fontSize > 72 {
		return nil, errors.New("字体大小必须在 6 到 72 之间")
	}
	horizontal := strings.TrimSpace(spec.Horizontal)
	if horizontal != "" && !oneOf(horizontal, "left", "center", "right", "fill", "justify", "centerContinuous", "distributed") {
		return nil, fmt.Errorf("不支持的水平对齐方式 %q", horizontal)
	}
	vertical := strings.TrimSpace(spec.Vertical)
	if vertical == "" {
		vertical = "center"
	}
	if !oneOf(vertical, "top", "center", "justify", "distributed") {
		return nil, fmt.Errorf("不支持的垂直对齐方式 %q", vertical)
	}
	if spec.TextRotation < -90 || spec.TextRotation > 90 {
		return nil, errors.New("文字旋转角度必须在 -90 到 90 之间")
	}
	style := &excelize.Style{
		Font: &excelize.Font{
			Family: fontName,
			Size:   fontSize,
			Bold:   spec.Bold,
			Italic: spec.Italic,
			Color:  fontColor,
		},
		Alignment: &excelize.Alignment{
			Horizontal:   horizontal,
			Vertical:     vertical,
			WrapText:     spec.WrapText,
			ShrinkToFit:  spec.ShrinkToFit,
			TextRotation: spec.TextRotation,
		},
	}
	if strings.TrimSpace(spec.FillColor) != "" {
		fillColor, err := normalizeSpreadsheetColor(spec.FillColor, "")
		if err != nil {
			return nil, fmt.Errorf("填充颜色无效: %w", err)
		}
		style.Fill = excelize.Fill{Type: "pattern", Pattern: 1, Color: []string{fillColor}}
	}
	if strings.TrimSpace(spec.BorderStyle) != "" && !strings.EqualFold(spec.BorderStyle, "none") {
		borderStyle, ok := spreadsheetBorderStyle(strings.TrimSpace(spec.BorderStyle))
		if !ok {
			return nil, fmt.Errorf("不支持的边框样式 %q", spec.BorderStyle)
		}
		borderColor, err := normalizeSpreadsheetColor(spec.BorderColor, "D1D5DB")
		if err != nil {
			return nil, fmt.Errorf("边框颜色无效: %w", err)
		}
		for _, side := range []string{"left", "top", "right", "bottom"} {
			style.Border = append(style.Border, excelize.Border{Type: side, Color: borderColor, Style: borderStyle})
		}
	}
	if numberFormat := strings.TrimSpace(spec.NumberFormat); numberFormat != "" {
		style.CustomNumFmt = &numberFormat
	}
	return style, nil
}

func applySpreadsheetDimensions(workbook *excelize.File, sheet string, columns []SpreadsheetColumnSpec, rows []SpreadsheetRowSpec) error {
	for _, column := range columns {
		start := strings.ToUpper(strings.TrimSpace(column.Start))
		end := strings.ToUpper(strings.TrimSpace(column.End))
		if end == "" {
			end = start
		}
		startNumber, startErr := excelize.ColumnNameToNumber(start)
		endNumber, endErr := excelize.ColumnNameToNumber(end)
		if startErr != nil || endErr != nil || endNumber < startNumber {
			return fmt.Errorf("列范围 %s:%s 无效", start, end)
		}
		if column.Width < 1 || column.Width > 255 {
			return fmt.Errorf("列 %s:%s 宽度必须在 1 到 255 之间", start, end)
		}
		if err := workbook.SetColWidth(sheet, start, end, column.Width); err != nil {
			return fmt.Errorf("设置列 %s:%s 宽度失败: %w", start, end, err)
		}
	}
	for _, row := range rows {
		end := row.End
		if end == 0 {
			end = row.Start
		}
		if row.Start < 1 || end < row.Start || end > 1_048_576 {
			return fmt.Errorf("行范围 %d:%d 无效", row.Start, end)
		}
		if row.Height < 1 || row.Height > 409 {
			return fmt.Errorf("行 %d:%d 高度必须在 1 到 409 之间", row.Start, end)
		}
		for index := row.Start; index <= end; index++ {
			if err := workbook.SetRowHeight(sheet, index, row.Height); err != nil {
				return fmt.Errorf("设置第 %d 行高度失败: %w", index, err)
			}
		}
	}
	return nil
}

func applySpreadsheetView(workbook *excelize.File, sheet string, spec SpreadsheetCreateSpec) error {
	if spec.FreezeRows < 0 || spec.FreezeRows > 1_048_575 || spec.FreezeColumns < 0 || spec.FreezeColumns > 16_383 {
		return errors.New("冻结行列数量无效")
	}
	if spec.FreezeRows > 0 || spec.FreezeColumns > 0 {
		column, _ := excelize.ColumnNumberToName(spec.FreezeColumns + 1)
		topLeft := column + strconv.Itoa(spec.FreezeRows+1)
		activePane := "bottomRight"
		if spec.FreezeRows == 0 {
			activePane = "topRight"
		} else if spec.FreezeColumns == 0 {
			activePane = "bottomLeft"
		}
		if err := workbook.SetPanes(sheet, &excelize.Panes{
			Freeze: true, XSplit: spec.FreezeColumns, YSplit: spec.FreezeRows,
			TopLeftCell: topLeft, ActivePane: activePane,
			Selection: []excelize.Selection{{SQRef: topLeft, ActiveCell: topLeft, Pane: activePane}},
		}); err != nil {
			return fmt.Errorf("设置冻结窗格失败: %w", err)
		}
	}
	if spec.ZoomScale != 0 && (spec.ZoomScale < 10 || spec.ZoomScale > 400) {
		return errors.New("工作表缩放比例必须在 10 到 400 之间")
	}
	if spec.ShowGridLines != nil || spec.ZoomScale != 0 {
		options := &excelize.ViewOptions{ShowGridLines: spec.ShowGridLines}
		if spec.ZoomScale != 0 {
			options.ZoomScale = &spec.ZoomScale
		}
		if err := workbook.SetSheetView(sheet, -1, options); err != nil {
			return fmt.Errorf("设置工作表视图失败: %w", err)
		}
	}
	return nil
}

func applySpreadsheetPage(workbook *excelize.File, sheet string, page *SpreadsheetPageSpec) error {
	if page == nil {
		return nil
	}
	orientation := strings.ToLower(strings.TrimSpace(page.Orientation))
	if orientation != "" && orientation != "portrait" && orientation != "landscape" {
		return fmt.Errorf("不支持的页面方向 %q", page.Orientation)
	}
	if page.PaperSize != nil && (*page.PaperSize < 1 || *page.PaperSize > 118) {
		return errors.New("纸张编号必须在 1 到 118 之间")
	}
	for _, fit := range []*int{page.FitToWidth, page.FitToHeight} {
		if fit != nil && (*fit < 0 || *fit > 100) {
			return errors.New("页面适配数量必须在 0 到 100 之间")
		}
	}
	layout := &excelize.PageLayoutOptions{Size: page.PaperSize, FitToWidth: page.FitToWidth, FitToHeight: page.FitToHeight}
	if orientation != "" {
		layout.Orientation = &orientation
	}
	if err := workbook.SetPageLayout(sheet, layout); err != nil {
		return fmt.Errorf("设置页面布局失败: %w", err)
	}
	if page.FitToWidth != nil || page.FitToHeight != nil {
		enabled := true
		if err := workbook.SetSheetProps(sheet, &excelize.SheetPropsOptions{FitToPage: &enabled}); err != nil {
			return fmt.Errorf("启用页面适配失败: %w", err)
		}
	}
	if page.MarginTop != nil || page.MarginBottom != nil || page.MarginLeft != nil || page.MarginRight != nil || page.CenterHorizontally != nil || page.CenterVertically != nil {
		margins := &excelize.PageLayoutMarginsOptions{
			Top: page.MarginTop, Bottom: page.MarginBottom, Left: page.MarginLeft, Right: page.MarginRight,
			Horizontally: page.CenterHorizontally, Vertically: page.CenterVertically,
		}
		if err := workbook.SetPageMargins(sheet, margins); err != nil {
			return fmt.Errorf("设置页边距失败: %w", err)
		}
	}
	if strings.TrimSpace(page.PrintArea) != "" {
		first, last, err := normalizeSpreadsheetRange(page.PrintArea)
		if err != nil {
			return fmt.Errorf("打印区域无效: %w", err)
		}
		firstColumn, firstRow, _ := excelize.CellNameToCoordinates(first)
		lastColumn, lastRow, _ := excelize.CellNameToCoordinates(last)
		absoluteFirst, _ := excelize.CoordinatesToCellName(firstColumn, firstRow, true)
		absoluteLast, _ := excelize.CoordinatesToCellName(lastColumn, lastRow, true)
		if err := workbook.SetDefinedName(&excelize.DefinedName{
			Name: "_xlnm.Print_Area", Scope: sheet,
			RefersTo: fmt.Sprintf("'%s'!%s:%s", escapeSpreadsheetSheetName(sheet), absoluteFirst, absoluteLast),
		}); err != nil {
			return fmt.Errorf("设置打印区域失败: %w", err)
		}
	}
	if strings.TrimSpace(page.RepeatRows) != "" {
		start, end, err := parseSpreadsheetRowRange(page.RepeatRows)
		if err != nil {
			return fmt.Errorf("重复标题行无效: %w", err)
		}
		if err := workbook.SetDefinedName(&excelize.DefinedName{
			Name: "_xlnm.Print_Titles", Scope: sheet,
			RefersTo: fmt.Sprintf("'%s'!$%d:$%d", escapeSpreadsheetSheetName(sheet), start, end),
		}); err != nil {
			return fmt.Errorf("设置重复标题行失败: %w", err)
		}
	}
	return nil
}

func applySpreadsheetValidations(workbook *excelize.File, sheet string, specs []SpreadsheetValidationListSpec) error {
	for _, spec := range specs {
		first, last, err := normalizeSpreadsheetRange(spec.Range)
		if err != nil {
			return fmt.Errorf("数据验证区域 %q 无效: %w", spec.Range, err)
		}
		if len(spec.Values) == 0 {
			return fmt.Errorf("数据验证区域 %s 的选项不能为空", spec.Range)
		}
		validation := excelize.NewDataValidation(spec.AllowBlank)
		validation.SetSqref(first + ":" + last)
		if err := validation.SetDropList(spec.Values); err != nil {
			return fmt.Errorf("设置 %s 下拉选项失败: %w", spec.Range, err)
		}
		if strings.TrimSpace(spec.Prompt) != "" {
			validation.SetInput(strings.TrimSpace(spec.PromptTitle), strings.TrimSpace(spec.Prompt))
		}
		if strings.TrimSpace(spec.Error) != "" {
			validation.SetError(excelize.DataValidationErrorStyleStop, strings.TrimSpace(spec.ErrorTitle), strings.TrimSpace(spec.Error))
		}
		if err := workbook.AddDataValidation(sheet, validation); err != nil {
			return fmt.Errorf("应用 %s 数据验证失败: %w", spec.Range, err)
		}
	}
	return nil
}

func normalizeSpreadsheetRange(value string) (string, string, error) {
	startColumn, startRow, endColumn, endRow, err := parseCellRange(value)
	if err != nil || endColumn == 0 || endRow == 0 {
		if err == nil {
			err = errors.New("范围不能为空")
		}
		return "", "", err
	}
	first, _ := excelize.CoordinatesToCellName(startColumn, startRow)
	last, _ := excelize.CoordinatesToCellName(endColumn, endRow)
	return first, last, nil
}

func normalizeSpreadsheetColor(value, fallback string) (string, error) {
	value = strings.TrimPrefix(strings.TrimSpace(value), "#")
	if value == "" {
		value = fallback
	}
	if !spreadsheetColorPattern.MatchString(value) {
		return "", fmt.Errorf("%q 不是 RRGGBB 或 AARRGGBB 颜色", value)
	}
	return strings.ToUpper(value), nil
}

func spreadsheetBorderStyle(value string) (int, bool) {
	styles := map[string]int{
		"thin": 1, "medium": 2, "dashed": 3, "dotted": 4, "thick": 5,
		"double": 6, "hair": 7, "mediumDashed": 8, "dashDot": 9,
		"mediumDashDot": 10, "dashDotDot": 11, "mediumDashDotDot": 12, "slantDashDot": 13,
	}
	style, ok := styles[value]
	return style, ok
}

func parseSpreadsheetRowRange(value string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) == 0 || len(parts) > 2 {
		return 0, 0, errors.New("应使用 1:3 形式")
	}
	start, err := strconv.Atoi(strings.TrimPrefix(parts[0], "$"))
	if err != nil {
		return 0, 0, errors.New("起始行无效")
	}
	end := start
	if len(parts) == 2 {
		end, err = strconv.Atoi(strings.TrimPrefix(parts[1], "$"))
		if err != nil {
			return 0, 0, errors.New("结束行无效")
		}
	}
	if start < 1 || end < start || end > 1_048_576 {
		return 0, 0, errors.New("行范围越界")
	}
	return start, end, nil
}

func escapeSpreadsheetSheetName(value string) string {
	return strings.ReplaceAll(value, "'", "''")
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
