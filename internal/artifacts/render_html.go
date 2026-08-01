package artifacts

import (
	"fmt"
	"html"
	"strings"
)

type HTMLRenderSelection struct {
	Sheet string
	Page  int
	Slide int
}

// RenderPreviewHTML produces a deterministic, read-only visual surface from
// the Office structure parser. Structural validity and visual QA remain
// separate: callers must rasterize this HTML and send the pixels to a vision
// model before claiming visual approval.
func RenderPreviewHTML(preview Preview, selection HTMLRenderSelection) string {
	var body strings.Builder
	switch preview.Kind {
	case KindDocument:
		renderDocumentHTML(&body, preview.Document)
	case KindSpreadsheet:
		renderSpreadsheetHTML(&body, preview.Spreadsheet, selection.Sheet)
	case KindPresentation:
		renderPresentationHTML(&body, preview.Presentation, selection.Slide)
	default:
		body.WriteString(`<div class="empty">No preview is available.</div>`)
	}
	return `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">` +
		`<style>` + officePreviewCSS + `</style></head><body><main class="surface">` + body.String() + `</main></body></html>`
}

func renderDocumentHTML(out *strings.Builder, document *DocumentPreview) {
	if document == nil || len(document.Blocks) == 0 {
		out.WriteString(`<article class="paper"><div class="empty">The document has no visible content.</div></article>`)
		return
	}
	out.WriteString(`<article class="paper document">`)
	for _, block := range document.Blocks {
		if block.Type == "table" {
			out.WriteString(`<div class="table-wrap"><table>`)
			for _, row := range block.Table {
				out.WriteString(`<tr>`)
				for _, cell := range row {
					out.WriteString(`<td>` + html.EscapeString(cell) + `</td>`)
				}
				out.WriteString(`</tr>`)
			}
			out.WriteString(`</table></div>`)
			continue
		}
		text := html.EscapeString(block.Text)
		if text == "" {
			out.WriteString(`<p class="blank">&nbsp;</p>`)
			continue
		}
		className := "paragraph"
		style := strings.ToLower(block.Style)
		switch {
		case strings.Contains(style, "title"):
			className = "doc-title"
		case strings.Contains(style, "heading 1") || strings.Contains(style, "heading1"):
			className = "heading-one"
		case strings.Contains(style, "heading"):
			className = "heading-two"
		}
		out.WriteString(`<p class="` + className + `">` + text + `</p>`)
	}
	out.WriteString(`</article>`)
}

func renderSpreadsheetHTML(out *strings.Builder, workbook *SpreadsheetPreview, requestedSheet string) {
	if workbook == nil || len(workbook.Sheets) == 0 {
		out.WriteString(`<section class="workbook"><div class="empty">The workbook has no worksheets.</div></section>`)
		return
	}
	active := workbook.Sheets[0]
	wanted := strings.TrimSpace(requestedSheet)
	if wanted == "" {
		wanted = workbook.ActiveSheet
	}
	for _, sheet := range workbook.Sheets {
		if strings.EqualFold(sheet.Name, wanted) {
			active = sheet
			break
		}
	}
	out.WriteString(`<section class="workbook"><header class="sheet-meta"><strong>` + html.EscapeString(active.Name) + `</strong><span>` +
		fmt.Sprintf("%d rows x %d columns", active.RowCount, active.ColumnCount) + `</span></header>`)
	out.WriteString(`<div class="sheet-grid"><table><thead><tr><th class="corner"></th>`)
	columnCount := active.ColumnCount
	if columnCount <= 0 {
		for _, row := range active.Rows {
			if len(row) > columnCount {
				columnCount = len(row)
			}
		}
	}
	for column := 0; column < columnCount; column++ {
		out.WriteString(`<th>` + spreadsheetColumnName(column+1) + `</th>`)
	}
	out.WriteString(`</tr></thead><tbody>`)
	for rowIndex, row := range active.Rows {
		out.WriteString(`<tr><th class="row-number">` + fmt.Sprint(rowIndex+1) + `</th>`)
		for column := 0; column < columnCount; column++ {
			value := ""
			if column < len(row) {
				value = row[column]
			}
			out.WriteString(`<td>` + html.EscapeString(value) + `</td>`)
		}
		out.WriteString(`</tr>`)
	}
	out.WriteString(`</tbody></table></div><footer class="sheet-tabs">`)
	for _, sheet := range workbook.Sheets {
		className := ""
		if sheet.Name == active.Name {
			className = ` class="active"`
		}
		out.WriteString(`<span` + className + `>` + html.EscapeString(sheet.Name) + `</span>`)
	}
	out.WriteString(`</footer></section>`)
}

func renderPresentationHTML(out *strings.Builder, presentation *PresentationPreview, requestedSlide int) {
	if presentation == nil || len(presentation.Slides) == 0 {
		out.WriteString(`<section class="presentation"><div class="empty">The presentation has no slides.</div></section>`)
		return
	}
	index := requestedSlide - 1
	if index < 0 || index >= len(presentation.Slides) {
		index = 0
	}
	slide := presentation.Slides[index]
	out.WriteString(`<section class="presentation"><div class="slide-frame"><div class="slide-number">` +
		fmt.Sprintf("%d / %d", index+1, len(presentation.Slides)) + `</div>`)
	if slide.Title != "" {
		out.WriteString(`<h1>` + html.EscapeString(slide.Title) + `</h1>`)
	}
	out.WriteString(`<div class="slide-body">`)
	for _, text := range slide.Texts {
		if strings.TrimSpace(text) == "" || text == slide.Title {
			continue
		}
		out.WriteString(`<p>` + html.EscapeString(text) + `</p>`)
	}
	out.WriteString(`</div></div><div class="slide-strip">`)
	for itemIndex, item := range presentation.Slides {
		className := "thumb"
		if itemIndex == index {
			className += " active"
		}
		out.WriteString(`<div class="` + className + `"><span>` + fmt.Sprint(item.Number) + `</span><strong>` + html.EscapeString(item.Title) + `</strong></div>`)
	}
	out.WriteString(`</div></section>`)
}

func spreadsheetColumnName(column int) string {
	if column <= 0 {
		return ""
	}
	name := ""
	for column > 0 {
		column--
		name = string(rune('A'+column%26)) + name
		column /= 26
	}
	return name
}

const officePreviewCSS = `
*{box-sizing:border-box}html,body{margin:0;min-height:100%;background:#e9ecef;color:#202124;font-family:"Segoe UI",Arial,sans-serif;font-size:14px;letter-spacing:0}.surface{min-height:100vh;padding:32px}.empty{display:grid;min-height:240px;place-items:center;color:#666}.paper{width:min(920px,100%);min-height:1120px;margin:0 auto;padding:82px 88px;background:#fff;box-shadow:0 3px 18px rgba(0,0,0,.16)}.document p{margin:0 0 12px;line-height:1.72;white-space:pre-wrap;overflow-wrap:anywhere}.document .doc-title{margin:0 0 34px;text-align:center;font-size:30px;font-weight:700}.document .heading-one{margin:30px 0 15px;font-size:22px;font-weight:700}.document .heading-two{margin:24px 0 12px;font-size:18px;font-weight:650}.document .blank{height:18px}.table-wrap{max-width:100%;margin:18px 0;overflow:hidden}.document table{width:100%;border-collapse:collapse;table-layout:fixed}.document td{padding:8px 10px;border:1px solid #9aa0a6;vertical-align:top;overflow-wrap:anywhere}.workbook{width:100%;min-height:calc(100vh - 64px);background:#fff;border:1px solid #c7cbd1;box-shadow:0 2px 12px rgba(0,0,0,.12);display:flex;flex-direction:column}.sheet-meta{height:48px;padding:0 16px;display:flex;align-items:center;gap:14px;border-bottom:1px solid #d4d7dc}.sheet-meta span{color:#666;font-size:12px}.sheet-grid{overflow:auto;flex:1}.sheet-grid table{border-collapse:collapse;min-width:100%;table-layout:fixed}.sheet-grid th,.sheet-grid td{height:27px;min-width:96px;padding:4px 7px;border-right:1px solid #d9dce1;border-bottom:1px solid #d9dce1;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}.sheet-grid thead th,.row-number,.corner{position:sticky;background:#f3f4f6;color:#555;font-weight:500;text-align:center}.sheet-grid thead th{top:0;z-index:2}.row-number,.corner{left:0;min-width:44px!important;width:44px;z-index:1}.corner{z-index:3}.sheet-tabs{height:38px;display:flex;align-items:end;gap:2px;padding:0 12px;border-top:1px solid #cfd3d8;background:#f7f8f9}.sheet-tabs span{min-width:88px;padding:8px 14px 7px;text-align:center;color:#5f6368}.sheet-tabs .active{border-bottom:3px solid #16825d;color:#155d45;font-weight:600}.presentation{display:grid;grid-template-columns:minmax(0,1fr) 220px;gap:24px;align-items:start}.slide-frame{position:relative;aspect-ratio:16/9;background:#fff;box-shadow:0 3px 18px rgba(0,0,0,.2);padding:7% 8%;overflow:hidden}.slide-frame h1{max-width:90%;margin:0 0 5%;font-size:40px;line-height:1.18}.slide-body{font-size:24px;line-height:1.45}.slide-body p{margin:0 0 18px}.slide-number{position:absolute;right:18px;bottom:14px;color:#777;font-size:12px}.slide-strip{display:flex;flex-direction:column;gap:9px;max-height:calc(100vh - 64px);overflow:auto}.thumb{min-height:72px;padding:10px;background:#fff;border:2px solid transparent;box-shadow:0 1px 5px rgba(0,0,0,.12);display:grid;grid-template-columns:24px 1fr;gap:7px;align-items:start}.thumb.active{border-color:#3f6fcd}.thumb span{color:#777;font-size:11px}.thumb strong{font-size:12px;line-height:1.3;overflow-wrap:anywhere}@media(max-width:800px){.surface{padding:14px}.paper{padding:44px 30px}.presentation{grid-template-columns:1fr}.slide-strip{display:none}.slide-frame h1{font-size:28px}.slide-body{font-size:18px}}
`
