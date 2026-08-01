package artifacts

import (
	"fmt"
	"path/filepath"
	"strings"
)

type Kind string

const (
	KindDocument     Kind = "document"
	KindSpreadsheet  Kind = "spreadsheet"
	KindPresentation Kind = "presentation"
)

type PreviewOptions struct {
	MaxTextChars int
	MaxSheets    int
	MaxRows      int
	MaxColumns   int
}

type Preview struct {
	Kind         Kind                 `json:"kind"`
	MIMEType     string               `json:"mimeType"`
	Document     *DocumentPreview     `json:"document,omitempty"`
	Spreadsheet  *SpreadsheetPreview  `json:"spreadsheet,omitempty"`
	Presentation *PresentationPreview `json:"presentation,omitempty"`
}

type DocumentPreview struct {
	Blocks    []DocumentBlock `json:"blocks"`
	Truncated bool            `json:"truncated"`
}

type DocumentBlock struct {
	Type  string     `json:"type"`
	Text  string     `json:"text,omitempty"`
	Style string     `json:"style,omitempty"`
	Table [][]string `json:"table,omitempty"`
}

type SpreadsheetPreview struct {
	Sheets      []SpreadsheetSheet `json:"sheets"`
	ActiveSheet string             `json:"activeSheet,omitempty"`
	Truncated   bool               `json:"truncated"`
}

type SpreadsheetSheet struct {
	Name        string     `json:"name"`
	Rows        [][]string `json:"rows"`
	RowCount    int        `json:"rowCount"`
	ColumnCount int        `json:"columnCount"`
	Truncated   bool       `json:"truncated"`
}

type PresentationPreview struct {
	Slides    []PresentationSlide `json:"slides"`
	Truncated bool                `json:"truncated"`
}

type PresentationSlide struct {
	Number int      `json:"number"`
	Title  string   `json:"title,omitempty"`
	Texts  []string `json:"texts"`
}

type DocumentParagraph struct {
	Text  string `json:"text"`
	Style string `json:"style,omitempty"`
}

type DocumentSpec struct {
	Title      string
	Paragraphs []DocumentParagraph
}

type SlideSpec struct {
	Title string `json:"title"`
	Body  string `json:"body,omitempty"`
}

func DefaultPreviewOptions() PreviewOptions {
	return PreviewOptions{MaxTextChars: 1 << 20, MaxSheets: 12, MaxRows: 250, MaxColumns: 60}
}

func normalizePreviewOptions(options PreviewOptions) PreviewOptions {
	defaults := DefaultPreviewOptions()
	if options.MaxTextChars <= 0 || options.MaxTextChars > 8<<20 {
		options.MaxTextChars = defaults.MaxTextChars
	}
	if options.MaxSheets <= 0 || options.MaxSheets > 100 {
		options.MaxSheets = defaults.MaxSheets
	}
	if options.MaxRows <= 0 || options.MaxRows > 10_000 {
		options.MaxRows = defaults.MaxRows
	}
	if options.MaxColumns <= 0 || options.MaxColumns > 1_000 {
		options.MaxColumns = defaults.MaxColumns
	}
	return options
}

func Detect(path string) (Kind, string, bool) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".docx":
		return KindDocument, "application/vnd.openxmlformats-officedocument.wordprocessingml.document", true
	case ".xls":
		return KindSpreadsheet, "application/vnd.ms-excel", true
	case ".xlsx", ".xlsm":
		return KindSpreadsheet, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", true
	case ".pptx":
		return KindPresentation, "application/vnd.openxmlformats-officedocument.presentationml.presentation", true
	default:
		return "", "", false
	}
}

func PreviewFile(path string, options PreviewOptions) (Preview, error) {
	options = normalizePreviewOptions(options)
	kind, mimeType, ok := Detect(path)
	if !ok {
		return Preview{}, fmt.Errorf("不支持的办公产物格式: %s", filepath.Ext(path))
	}
	preview := Preview{Kind: kind, MIMEType: mimeType}
	var err error
	switch kind {
	case KindDocument:
		preview.Document, err = PreviewDocument(path, options)
	case KindSpreadsheet:
		preview.Spreadsheet, err = PreviewSpreadsheet(path, options)
	case KindPresentation:
		preview.Presentation, err = PreviewPresentation(path, options)
	}
	return preview, err
}
