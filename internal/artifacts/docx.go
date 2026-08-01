package artifacts

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/beevik/etree"
)

const wordDocumentEntry = "word/document.xml"

func PreviewDocument(path string, options PreviewOptions) (*DocumentPreview, error) {
	entries, err := readPackage(path)
	if err != nil {
		return nil, err
	}
	data, ok := entries[wordDocumentEntry]
	if !ok {
		return nil, errors.New("DOCX 缺少 word/document.xml")
	}
	document, err := parseXML(data, "Word document")
	if err != nil {
		return nil, err
	}
	body := firstElementByLocalName(document.Root(), "body")
	if body == nil {
		return nil, errors.New("DOCX 正文结构无效")
	}
	preview := &DocumentPreview{Blocks: make([]DocumentBlock, 0)}
	remaining := options.MaxTextChars
	for _, child := range body.ChildElements() {
		if remaining <= 0 {
			preview.Truncated = true
			break
		}
		switch child.Tag {
		case "p":
			text := wordElementText(child)
			if text == "" {
				continue
			}
			text, truncated := boundedRunes(text, remaining)
			preview.Blocks = append(preview.Blocks, DocumentBlock{Type: "paragraph", Text: text, Style: wordParagraphStyle(child)})
			remaining -= len([]rune(text))
			preview.Truncated = preview.Truncated || truncated
		case "tbl":
			table := make([][]string, 0)
			for _, rowElement := range directChildrenByTag(child, "tr") {
				row := make([]string, 0)
				for _, cell := range directChildrenByTag(rowElement, "tc") {
					cellText := strings.TrimSpace(wordElementText(cell))
					cellText, truncated := boundedRunes(cellText, remaining)
					row = append(row, cellText)
					remaining -= len([]rune(cellText))
					preview.Truncated = preview.Truncated || truncated
					if remaining <= 0 {
						break
					}
				}
				if len(row) > 0 {
					table = append(table, row)
				}
				if remaining <= 0 {
					break
				}
			}
			if len(table) > 0 {
				preview.Blocks = append(preview.Blocks, DocumentBlock{Type: "table", Table: table})
			}
		}
	}
	return preview, nil
}

func CreateDocument(path string, spec DocumentSpec) error {
	if strings.ToLower(filepath.Ext(path)) != ".docx" {
		return errors.New("文档产物必须使用 .docx 扩展名")
	}
	paragraphs := make([]DocumentParagraph, 0, len(spec.Paragraphs)+1)
	if strings.TrimSpace(spec.Title) != "" {
		paragraphs = append(paragraphs, DocumentParagraph{Text: spec.Title, Style: "title"})
	}
	paragraphs = append(paragraphs, spec.Paragraphs...)
	if len(paragraphs) == 0 {
		return errors.New("文档内容不能为空")
	}
	var body strings.Builder
	for _, paragraph := range paragraphs {
		body.WriteString(wordParagraphXML(paragraph))
	}
	entries := map[string][]byte{
		"[Content_Types].xml":          []byte(wordContentTypesXML),
		"_rels/.rels":                  []byte(wordRootRelationshipsXML),
		"docProps/app.xml":             []byte(wordAppPropertiesXML),
		"docProps/core.xml":            []byte(corePropertiesXML("EchoCode document")),
		"word/_rels/document.xml.rels": []byte(wordDocumentRelationshipsXML),
		"word/document.xml":            []byte(wordDocumentXML(body.String())),
		"word/styles.xml":              []byte(wordStylesXML),
	}
	return writePackage(path, entries)
}

func ReplaceDocumentText(path, find, replace string) (int, error) {
	if find == "" {
		return 0, errors.New("查找文本不能为空")
	}
	entries, err := readPackage(path)
	if err != nil {
		return 0, err
	}
	data, ok := entries[wordDocumentEntry]
	if !ok {
		return 0, errors.New("DOCX 缺少 word/document.xml")
	}
	document, err := parseXML(data, "Word document")
	if err != nil {
		return 0, err
	}
	replacements := 0
	for _, paragraph := range elementsByLocalName(document.Root(), "p") {
		textNodes := elementsByLocalName(paragraph, "t")
		if len(textNodes) == 0 {
			continue
		}
		var original strings.Builder
		for _, node := range textNodes {
			original.WriteString(node.Text())
		}
		value := original.String()
		count := strings.Count(value, find)
		if count == 0 {
			continue
		}
		textNodes[0].SetText(strings.ReplaceAll(value, find, replace))
		textNodes[0].RemoveAttr("space")
		textNodes[0].CreateAttr("xml:space", "preserve")
		for _, node := range textNodes[1:] {
			node.SetText("")
		}
		replacements += count
	}
	if replacements == 0 {
		return 0, nil
	}
	updated, err := xmlBytes(document)
	if err != nil {
		return 0, fmt.Errorf("序列化 Word 文档失败: %w", err)
	}
	entries[wordDocumentEntry] = updated
	if err := writePackage(path, entries); err != nil {
		return 0, err
	}
	return replacements, nil
}

func DocumentText(path string, maxChars int) (string, error) {
	preview, err := PreviewDocument(path, PreviewOptions{MaxTextChars: maxChars})
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	for _, block := range preview.Blocks {
		switch block.Type {
		case "paragraph":
			if block.Style != "" {
				fmt.Fprintf(&builder, "[%s] ", block.Style)
			}
			builder.WriteString(block.Text)
			builder.WriteByte('\n')
		case "table":
			for _, row := range block.Table {
				builder.WriteString("| ")
				builder.WriteString(strings.Join(row, " | "))
				builder.WriteString(" |\n")
			}
		}
	}
	if preview.Truncated {
		builder.WriteString("... [预览已截断]\n")
	}
	return strings.TrimSpace(builder.String()), nil
}

func wordParagraphXML(paragraph DocumentParagraph) string {
	style := normalizeWordStyle(paragraph.Style)
	properties := ""
	if style != "Normal" {
		properties = `<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`
	}
	return `<w:p>` + properties + `<w:r><w:t xml:space="preserve">` + escapeXMLText(paragraph.Text) + `</w:t></w:r></w:p>`
}

func normalizeWordStyle(style string) string {
	switch strings.ToLower(strings.TrimSpace(style)) {
	case "title":
		return "Title"
	case "heading1", "heading 1", "h1":
		return "Heading1"
	case "heading2", "heading 2", "h2":
		return "Heading2"
	case "list", "bullet", "listparagraph":
		return "ListParagraph"
	default:
		return "Normal"
	}
}

func wordElementText(element *etree.Element) string {
	var builder strings.Builder
	var visit func(*etree.Element)
	visit = func(current *etree.Element) {
		for _, child := range current.Child {
			switch node := child.(type) {
			case *etree.Element:
				switch node.Tag {
				case "t", "instrText":
					builder.WriteString(node.Text())
				case "tab":
					builder.WriteByte('\t')
				case "br", "cr":
					builder.WriteByte('\n')
				default:
					visit(node)
				}
			}
		}
	}
	visit(element)
	return strings.TrimSpace(builder.String())
}

func wordParagraphStyle(paragraph *etree.Element) string {
	for _, child := range paragraph.ChildElements() {
		if child.Tag != "pPr" {
			continue
		}
		style := firstElementByLocalName(child, "pStyle")
		return attributeByLocalName(style, "val")
	}
	return "Normal"
}

func directChildrenByTag(element *etree.Element, tag string) []*etree.Element {
	result := make([]*etree.Element, 0)
	for _, child := range element.ChildElements() {
		if child.Tag == tag {
			result = append(result, child)
		}
	}
	return result
}

func boundedRunes(value string, limit int) (string, bool) {
	runes := []rune(value)
	if limit < 0 {
		limit = 0
	}
	if len(runes) <= limit {
		return value, false
	}
	return string(runes[:limit]), true
}

func wordDocumentXML(body string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">` +
		`<w:body>` + body + `<w:sectPr><w:pgSz w:w="11906" w:h="16838"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440" w:header="708" w:footer="708" w:gutter="0"/></w:sectPr></w:body></w:document>`
}

func corePropertiesXML(title string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>` +
		`<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">` +
		`<dc:title>` + escapeXMLText(title) + `</dc:title><dc:creator>EchoCode</dc:creator><cp:lastModifiedBy>EchoCode</cp:lastModifiedBy></cp:coreProperties>`
}

const wordContentTypesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/></Types>`

const wordRootRelationshipsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/></Relationships>`

const wordDocumentRelationshipsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/></Relationships>`

const wordAppPropertiesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>EchoCode</Application><AppVersion>1.0</AppVersion></Properties>`

const wordStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:styles xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:docDefaults><w:rPrDefault><w:rPr><w:rFonts w:ascii="Aptos" w:eastAsia="Microsoft YaHei" w:hAnsi="Aptos"/><w:sz w:val="22"/><w:szCs w:val="22"/></w:rPr></w:rPrDefault><w:pPrDefault/></w:docDefaults><w:style w:type="paragraph" w:default="1" w:styleId="Normal"><w:name w:val="Normal"/></w:style><w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:pPr><w:spacing w:after="240"/></w:pPr><w:rPr><w:b/><w:sz w:val="36"/><w:szCs w:val="36"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading1"><w:name w:val="heading 1"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:rPr><w:b/><w:sz w:val="30"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="Heading2"><w:name w:val="heading 2"/><w:basedOn w:val="Normal"/><w:next w:val="Normal"/><w:rPr><w:b/><w:sz w:val="26"/></w:rPr></w:style><w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:pPr><w:ind w:left="720"/></w:pPr></w:style></w:styles>`
