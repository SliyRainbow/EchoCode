package artifacts

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/extrame/xls"
	"github.com/xuri/excelize/v2"
)

func PreviewSpreadsheet(path string, options PreviewOptions) (*SpreadsheetPreview, error) {
	if strings.EqualFold(filepath.Ext(path), ".xls") {
		return previewLegacySpreadsheet(path, options)
	}
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("打开 Excel 工作簿失败: %w", err)
	}
	defer workbook.Close()
	names := workbook.GetSheetList()
	preview := &SpreadsheetPreview{Sheets: make([]SpreadsheetSheet, 0, min(len(names), options.MaxSheets))}
	activeIndex := workbook.GetActiveSheetIndex()
	preview.ActiveSheet = workbook.GetSheetName(activeIndex)
	if len(names) > options.MaxSheets {
		names = names[:options.MaxSheets]
		preview.Truncated = true
	}
	for _, name := range names {
		rows, rowErr := workbook.GetRows(name)
		if rowErr != nil {
			return nil, fmt.Errorf("读取工作表 %s 失败: %w", name, rowErr)
		}
		sheet := trimSpreadsheetRows(name, rows, options)
		if dimension, dimensionErr := workbook.GetSheetDimension(name); dimensionErr == nil {
			_, _, endColumn, endRow, rangeErr := parseCellRange(dimension)
			if rangeErr == nil {
				sheet.RowCount = max(sheet.RowCount, endRow)
				sheet.ColumnCount = max(sheet.ColumnCount, endColumn)
			}
		}
		if formulaErr := fillSpreadsheetPreviewFormulas(workbook, name, &sheet, options); formulaErr != nil {
			return nil, formulaErr
		}
		preview.Truncated = preview.Truncated || sheet.Truncated
		preview.Sheets = append(preview.Sheets, sheet)
	}
	return preview, nil
}

func fillSpreadsheetPreviewFormulas(workbook *excelize.File, sheetName string, sheet *SpreadsheetSheet, options PreviewOptions) error {
	rowLimit := min(sheet.RowCount, options.MaxRows)
	columnLimit := min(sheet.ColumnCount, options.MaxColumns)
	for len(sheet.Rows) < rowLimit {
		sheet.Rows = append(sheet.Rows, []string{})
	}
	for row := 1; row <= rowLimit; row++ {
		for column := 1; column <= columnLimit; column++ {
			cell, _ := excelize.CoordinatesToCellName(column, row)
			formula, err := workbook.GetCellFormula(sheetName, cell)
			if err != nil {
				return fmt.Errorf("读取 %s!%s 公式失败: %w", sheetName, cell, err)
			}
			if formula == "" {
				continue
			}
			for len(sheet.Rows[row-1]) < columnLimit {
				sheet.Rows[row-1] = append(sheet.Rows[row-1], "")
			}
			if sheet.Rows[row-1][column-1] == "" {
				sheet.Rows[row-1][column-1] = "=" + formula
			}
		}
	}
	return nil
}

func SpreadsheetSummary(path string) (string, error) {
	preview, err := PreviewSpreadsheet(path, PreviewOptions{MaxSheets: 100, MaxRows: 1, MaxColumns: 1})
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	fmt.Fprintf(&builder, "工作簿：%s\n", filepath.Base(path))
	if strings.EqualFold(filepath.Ext(path), ".xls") {
		for _, sheet := range preview.Sheets {
			fmt.Fprintf(&builder, "- %s：%d 行 × %d 列（旧版 XLS，仅检查数据范围）\n", sheet.Name, sheet.RowCount, sheet.ColumnCount)
		}
		return strings.TrimSpace(builder.String()), nil
	}
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		return "", fmt.Errorf("打开 Excel 工作簿失败: %w", err)
	}
	defer workbook.Close()
	for _, sheet := range preview.Sheets {
		metrics, metricsErr := inspectSpreadsheetSheet(workbook, sheet)
		if metricsErr != nil {
			return "", metricsErr
		}
		fmt.Fprintf(&builder, "- %s：%d 行 × %d 列；公式 %s；合并 %d；样式 %s；数据验证 %d；冻结窗格 %s\n",
			sheet.Name, sheet.RowCount, sheet.ColumnCount, metrics.Formulas, metrics.Merges, metrics.Styles, metrics.Validations, metrics.Frozen)
	}
	return strings.TrimSpace(builder.String()), nil
}

func ReadSpreadsheetRange(path, sheetName, cellRange string, maxCells int) ([][]string, error) {
	if maxCells <= 0 || maxCells > 100_000 {
		maxCells = 10_000
	}
	if strings.EqualFold(filepath.Ext(path), ".xls") {
		return readLegacySpreadsheetRange(path, sheetName, cellRange, maxCells)
	}
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		return nil, fmt.Errorf("打开 Excel 工作簿失败: %w", err)
	}
	defer workbook.Close()
	if strings.TrimSpace(sheetName) == "" {
		sheetName = workbook.GetSheetName(workbook.GetActiveSheetIndex())
	}
	if sheetName == "" {
		return nil, errors.New("工作簿没有可读取的工作表")
	}
	startColumn, startRow, endColumn, endRow, err := parseCellRange(cellRange)
	if err != nil {
		return nil, err
	}
	if endColumn == 0 || endRow == 0 {
		dimension, dimensionErr := workbook.GetSheetDimension(sheetName)
		if dimensionErr != nil {
			return nil, fmt.Errorf("读取工作表范围失败: %w", dimensionErr)
		}
		startColumn, startRow, endColumn, endRow, err = parseCellRange(dimension)
		if err != nil {
			return nil, err
		}
	}
	endColumn, endRow = clampCellRange(startColumn, startRow, endColumn, endRow, maxCells)
	rows := make([][]string, 0, endRow-startRow+1)
	for row := startRow; row <= endRow; row++ {
		values := make([]string, 0, endColumn-startColumn+1)
		for column := startColumn; column <= endColumn; column++ {
			cell, _ := excelize.CoordinatesToCellName(column, row)
			value, valueErr := workbook.GetCellValue(sheetName, cell)
			if valueErr != nil {
				return nil, fmt.Errorf("读取 %s!%s 失败: %w", sheetName, cell, valueErr)
			}
			if value == "" {
				if formula, formulaErr := workbook.GetCellFormula(sheetName, cell); formulaErr == nil && formula != "" {
					value = "=" + formula
				}
			}
			values = append(values, value)
		}
		rows = append(rows, values)
	}
	return rows, nil
}

func WriteSpreadsheetRange(path, sheetName, startCell string, values [][]any) error {
	if !strings.EqualFold(filepath.Ext(path), ".xlsx") {
		return errors.New("写入表格必须使用 .xlsx；旧 .xls 请先转换为 .xlsx")
	}
	if len(values) == 0 {
		return errors.New("写入数据不能为空")
	}
	startColumn, startRow, err := excelize.CellNameToCoordinates(strings.TrimSpace(startCell))
	if err != nil {
		return fmt.Errorf("起始单元格无效: %w", err)
	}
	if strings.TrimSpace(sheetName) == "" {
		sheetName = "Sheet1"
	}
	workbook, created, err := openWritableWorkbook(path)
	if err != nil {
		return err
	}
	defer workbook.Close()
	index, indexErr := workbook.GetSheetIndex(sheetName)
	if indexErr != nil || index == -1 {
		index, err = workbook.NewSheet(sheetName)
		if err != nil {
			return fmt.Errorf("创建工作表失败: %w", err)
		}
	}
	containsFormula, err := writeSpreadsheetMatrix(workbook, sheetName, startColumn, startRow, values)
	if err != nil {
		return err
	}
	if containsFormula {
		if err := setSpreadsheetCalculation(workbook); err != nil {
			return err
		}
	}
	workbook.SetActiveSheet(index)
	if created && sheetName != "Sheet1" {
		_ = workbook.DeleteSheet("Sheet1")
	}
	return saveWorkbook(workbook, path, created)
}

func writeSpreadsheetMatrix(workbook *excelize.File, sheetName string, startColumn, startRow int, values [][]any) (bool, error) {
	containsFormula := false
	for rowOffset, row := range values {
		for columnOffset, value := range row {
			cell, _ := excelize.CoordinatesToCellName(startColumn+columnOffset, startRow+rowOffset)
			if text, ok := value.(string); ok {
				switch {
				case text == "":
					value = nil
				case strings.HasPrefix(text, "'="):
					value = text[1:]
				case strings.HasPrefix(text, "=") && len(text) > 1:
					if err := workbook.SetCellFormula(sheetName, cell, text[1:]); err != nil {
						return false, fmt.Errorf("写入 %s!%s 公式失败: %w", sheetName, cell, err)
					}
					containsFormula = true
					continue
				}
			}
			if err := workbook.SetCellValue(sheetName, cell, value); err != nil {
				return false, fmt.Errorf("写入 %s!%s 失败: %w", sheetName, cell, err)
			}
		}
	}
	return containsFormula, nil
}

func setSpreadsheetCalculation(workbook *excelize.File) error {
	mode := "auto"
	fullCalcOnLoad, forceFullCalc, calcOnSave := true, true, true
	if err := workbook.SetCalcProps(&excelize.CalcPropsOptions{
		CalcMode: &mode, FullCalcOnLoad: &fullCalcOnLoad, ForceFullCalc: &forceFullCalc, CalcOnSave: &calcOnSave,
	}); err != nil {
		return fmt.Errorf("设置公式自动计算失败: %w", err)
	}
	return nil
}

type spreadsheetSheetMetrics struct {
	Formulas    string
	Styles      string
	Merges      int
	Validations int
	Frozen      string
}

func inspectSpreadsheetSheet(workbook *excelize.File, sheet SpreadsheetSheet) (spreadsheetSheetMetrics, error) {
	metrics := spreadsheetSheetMetrics{Formulas: "0", Styles: "0", Frozen: "否"}
	merges, err := workbook.GetMergeCells(sheet.Name, true)
	if err != nil {
		return metrics, fmt.Errorf("读取工作表 %s 合并区域失败: %w", sheet.Name, err)
	}
	metrics.Merges = len(merges)
	validations, err := workbook.GetDataValidations(sheet.Name)
	if err != nil {
		return metrics, fmt.Errorf("读取工作表 %s 数据验证失败: %w", sheet.Name, err)
	}
	metrics.Validations = len(validations)
	if panes, panesErr := workbook.GetPanes(sheet.Name); panesErr == nil && panes.Freeze {
		metrics.Frozen = fmt.Sprintf("是（%d 行，%d 列）", panes.YSplit, panes.XSplit)
	}
	cellCount := sheet.RowCount * sheet.ColumnCount
	if cellCount > 100_000 {
		metrics.Formulas = "未扫描（区域过大）"
		metrics.Styles = "未扫描（区域过大）"
		return metrics, nil
	}
	formulaCount, styleCount := 0, 0
	for row := 1; row <= sheet.RowCount; row++ {
		for column := 1; column <= sheet.ColumnCount; column++ {
			cell, _ := excelize.CoordinatesToCellName(column, row)
			if formula, formulaErr := workbook.GetCellFormula(sheet.Name, cell); formulaErr == nil && formula != "" {
				formulaCount++
			}
			if styleID, styleErr := workbook.GetCellStyle(sheet.Name, cell); styleErr == nil && styleID != 0 {
				styleCount++
			}
		}
	}
	metrics.Formulas = strconv.Itoa(formulaCount)
	metrics.Styles = strconv.Itoa(styleCount)
	return metrics, nil
}

func AddSpreadsheetSheet(path, sheetName string) error {
	if !strings.EqualFold(filepath.Ext(path), ".xlsx") {
		return errors.New("添加工作表仅支持 .xlsx")
	}
	if strings.TrimSpace(sheetName) == "" {
		return errors.New("工作表名称不能为空")
	}
	workbook, err := excelize.OpenFile(path)
	if err != nil {
		return fmt.Errorf("打开 Excel 工作簿失败: %w", err)
	}
	defer workbook.Close()
	if index, _ := workbook.GetSheetIndex(sheetName); index >= 0 {
		return fmt.Errorf("工作表 %q 已存在", sheetName)
	}
	index, err := workbook.NewSheet(sheetName)
	if err != nil {
		return fmt.Errorf("添加工作表失败: %w", err)
	}
	workbook.SetActiveSheet(index)
	return workbook.Save()
}

func ConvertLegacySpreadsheet(sourcePath, outputPath string) error {
	if !strings.EqualFold(filepath.Ext(sourcePath), ".xls") || !strings.EqualFold(filepath.Ext(outputPath), ".xlsx") {
		return errors.New("旧表格转换要求输入为 .xls、输出为 .xlsx")
	}
	legacy, err := xls.Open(sourcePath, "utf-8")
	if err != nil {
		return fmt.Errorf("打开旧 XLS 工作簿失败: %w", err)
	}
	workbook := excelize.NewFile()
	defer workbook.Close()
	createdSheets := 0
	cellCount := 0
	for sheetIndex := 0; sheetIndex < legacy.NumSheets(); sheetIndex++ {
		sheet := legacy.GetSheet(sheetIndex)
		if sheet == nil {
			continue
		}
		name := strings.TrimSpace(sheet.Name)
		if name == "" {
			name = fmt.Sprintf("Sheet%d", sheetIndex+1)
		}
		if createdSheets == 0 {
			if err := workbook.SetSheetName("Sheet1", name); err != nil {
				return err
			}
		} else if _, err := workbook.NewSheet(name); err != nil {
			return err
		}
		createdSheets++
		for rowIndex := 0; rowIndex <= int(sheet.MaxRow); rowIndex++ {
			row := safeLegacyRow(sheet, rowIndex)
			if row == nil {
				continue
			}
			lastColumn := row.LastCol()
			cellCount += lastColumn
			if cellCount > 5_000_000 {
				return errors.New("旧 XLS 包含超过 500 万个单元格，拒绝一次性转换")
			}
			for columnIndex := 0; columnIndex < lastColumn; columnIndex++ {
				cell, _ := excelize.CoordinatesToCellName(columnIndex+1, rowIndex+1)
				if err := workbook.SetCellValue(name, cell, row.Col(columnIndex)); err != nil {
					return err
				}
			}
		}
	}
	if createdSheets == 0 {
		return errors.New("旧 XLS 中没有可转换的工作表")
	}
	return workbook.SaveAs(outputPath)
}

func previewLegacySpreadsheet(path string, options PreviewOptions) (*SpreadsheetPreview, error) {
	workbook, err := xls.Open(path, "utf-8")
	if err != nil {
		return nil, fmt.Errorf("打开旧 XLS 工作簿失败: %w", err)
	}
	preview := &SpreadsheetPreview{Sheets: make([]SpreadsheetSheet, 0, min(workbook.NumSheets(), options.MaxSheets))}
	limit := min(workbook.NumSheets(), options.MaxSheets)
	preview.Truncated = workbook.NumSheets() > limit
	for sheetIndex := 0; sheetIndex < limit; sheetIndex++ {
		sheet := workbook.GetSheet(sheetIndex)
		if sheet == nil {
			continue
		}
		result := SpreadsheetSheet{Name: sheet.Name, RowCount: int(sheet.MaxRow) + 1, Rows: make([][]string, 0)}
		rowLimit := min(result.RowCount, options.MaxRows)
		for rowIndex := 0; rowIndex < rowLimit; rowIndex++ {
			row := safeLegacyRow(sheet, rowIndex)
			if row == nil {
				result.Rows = append(result.Rows, []string{})
				continue
			}
			columnCount := row.LastCol()
			result.ColumnCount = max(result.ColumnCount, columnCount)
			columnLimit := min(columnCount, options.MaxColumns)
			values := make([]string, columnLimit)
			for columnIndex := 0; columnIndex < columnLimit; columnIndex++ {
				values[columnIndex] = row.Col(columnIndex)
			}
			result.Rows = append(result.Rows, values)
			result.Truncated = result.Truncated || columnCount > columnLimit
		}
		result.Truncated = result.Truncated || result.RowCount > rowLimit
		preview.Truncated = preview.Truncated || result.Truncated
		preview.Sheets = append(preview.Sheets, result)
	}
	if len(preview.Sheets) > 0 {
		preview.ActiveSheet = preview.Sheets[0].Name
	}
	return preview, nil
}

func readLegacySpreadsheetRange(path, sheetName, cellRange string, maxCells int) ([][]string, error) {
	workbook, err := xls.Open(path, "utf-8")
	if err != nil {
		return nil, fmt.Errorf("打开旧 XLS 工作簿失败: %w", err)
	}
	var sheet *xls.WorkSheet
	for index := 0; index < workbook.NumSheets(); index++ {
		candidate := workbook.GetSheet(index)
		if candidate != nil && (sheetName == "" || strings.EqualFold(candidate.Name, sheetName)) {
			sheet = candidate
			break
		}
	}
	if sheet == nil {
		return nil, fmt.Errorf("找不到工作表 %q", sheetName)
	}
	startColumn, startRow, endColumn, endRow, err := parseCellRange(cellRange)
	if err != nil {
		return nil, err
	}
	if endColumn == 0 || endRow == 0 {
		startColumn, startRow, endRow = 1, 1, int(sheet.MaxRow)+1
		endColumn = 1
		for rowIndex := 0; rowIndex < endRow; rowIndex++ {
			if row := safeLegacyRow(sheet, rowIndex); row != nil {
				endColumn = max(endColumn, row.LastCol())
			}
		}
	}
	endColumn, endRow = clampCellRange(startColumn, startRow, endColumn, endRow, maxCells)
	rows := make([][]string, 0, endRow-startRow+1)
	for rowNumber := startRow; rowNumber <= endRow; rowNumber++ {
		row := safeLegacyRow(sheet, rowNumber-1)
		values := make([]string, endColumn-startColumn+1)
		if row != nil {
			for column := startColumn; column <= endColumn; column++ {
				if column-1 < row.LastCol() {
					values[column-startColumn] = row.Col(column - 1)
				}
			}
		}
		rows = append(rows, values)
	}
	return rows, nil
}

func trimSpreadsheetRows(name string, rows [][]string, options PreviewOptions) SpreadsheetSheet {
	result := SpreadsheetSheet{Name: name, RowCount: len(rows), Rows: make([][]string, 0, min(len(rows), options.MaxRows))}
	for _, row := range rows {
		result.ColumnCount = max(result.ColumnCount, len(row))
	}
	rowLimit := min(len(rows), options.MaxRows)
	for rowIndex := 0; rowIndex < rowLimit; rowIndex++ {
		columnLimit := min(len(rows[rowIndex]), options.MaxColumns)
		result.Rows = append(result.Rows, append([]string(nil), rows[rowIndex][:columnLimit]...))
		result.Truncated = result.Truncated || len(rows[rowIndex]) > columnLimit
	}
	result.Truncated = result.Truncated || len(rows) > rowLimit
	return result
}

func openWritableWorkbook(path string) (*excelize.File, bool, error) {
	if _, err := os.Stat(path); err == nil {
		workbook, openErr := excelize.OpenFile(path)
		if openErr != nil {
			return nil, false, fmt.Errorf("打开 Excel 工作簿失败: %w", openErr)
		}
		return workbook, false, nil
	} else if !os.IsNotExist(err) {
		return nil, false, err
	}
	return excelize.NewFile(), true, nil
}

func saveWorkbook(workbook *excelize.File, path string, created bool) error {
	if created {
		return workbook.SaveAs(path)
	}
	return workbook.Save()
}

func parseCellRange(value string) (startColumn, startRow, endColumn, endRow int, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 1, 1, 0, 0, nil
	}
	parts := strings.Split(value, ":")
	if len(parts) > 2 {
		return 0, 0, 0, 0, fmt.Errorf("单元格范围无效: %s", value)
	}
	startColumn, startRow, err = excelize.CellNameToCoordinates(parts[0])
	if err != nil {
		return 0, 0, 0, 0, fmt.Errorf("单元格范围无效: %w", err)
	}
	endColumn, endRow = startColumn, startRow
	if len(parts) == 2 {
		endColumn, endRow, err = excelize.CellNameToCoordinates(parts[1])
		if err != nil {
			return 0, 0, 0, 0, fmt.Errorf("单元格范围无效: %w", err)
		}
	}
	if endColumn < startColumn || endRow < startRow {
		return 0, 0, 0, 0, fmt.Errorf("单元格范围方向无效: %s", value)
	}
	return startColumn, startRow, endColumn, endRow, nil
}

func clampCellRange(startColumn, startRow, endColumn, endRow, maxCells int) (int, int) {
	columns := max(1, endColumn-startColumn+1)
	rows := max(1, endRow-startRow+1)
	if columns*rows <= maxCells {
		return endColumn, endRow
	}
	if columns > maxCells {
		return startColumn + maxCells - 1, startRow
	}
	return endColumn, startRow + maxCells/columns - 1
}

func safeLegacyRow(sheet *xls.WorkSheet, index int) (row *xls.Row) {
	defer func() {
		if recover() != nil {
			row = nil
		}
	}()
	return sheet.Row(index)
}

func sortedSheetNames(names []string) []string {
	result := append([]string(nil), names...)
	sort.Strings(result)
	return result
}
