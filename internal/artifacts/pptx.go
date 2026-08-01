package artifacts

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/beevik/etree"
)

const (
	presentationEntry              = "ppt/presentation.xml"
	presentationRelationshipsEntry = "ppt/_rels/presentation.xml.rels"
)

func PreviewPresentation(filePath string, options PreviewOptions) (*PresentationPreview, error) {
	entries, err := readPackage(filePath)
	if err != nil {
		return nil, err
	}
	orderedSlides, err := orderedPresentationSlides(entries)
	if err != nil {
		return nil, err
	}
	preview := &PresentationPreview{Slides: make([]PresentationSlide, 0, min(len(orderedSlides), options.MaxSheets))}
	if len(orderedSlides) > options.MaxSheets {
		orderedSlides = orderedSlides[:options.MaxSheets]
		preview.Truncated = true
	}
	remaining := options.MaxTextChars
	for index, slidePath := range orderedSlides {
		data, ok := entries[slidePath]
		if !ok {
			return nil, fmt.Errorf("PPTX 缺少幻灯片条目 %s", slidePath)
		}
		document, parseErr := parseXML(data, slidePath)
		if parseErr != nil {
			return nil, parseErr
		}
		texts := presentationShapeTexts(document.Root())
		slide := PresentationSlide{Number: index + 1, Texts: []string{}}
		if len(texts) > 0 {
			slide.Title, _ = boundedRunes(texts[0], remaining)
			remaining -= len([]rune(slide.Title))
		}
		for _, text := range texts[1:] {
			if remaining <= 0 {
				preview.Truncated = true
				break
			}
			value, truncated := boundedRunes(text, remaining)
			slide.Texts = append(slide.Texts, value)
			remaining -= len([]rune(value))
			preview.Truncated = preview.Truncated || truncated
		}
		preview.Slides = append(preview.Slides, slide)
		if remaining <= 0 {
			preview.Truncated = true
			break
		}
	}
	return preview, nil
}

func CreatePresentation(filePath string, slides []SlideSpec) error {
	if !strings.EqualFold(filepath.Ext(filePath), ".pptx") {
		return errors.New("演示文稿产物必须使用 .pptx 扩展名")
	}
	if len(slides) == 0 {
		return errors.New("演示文稿至少需要一页幻灯片")
	}
	entries, err := readPackageBytes(blankPresentationTemplate)
	if err != nil {
		return err
	}
	entries["docProps/core.xml"] = []byte(corePropertiesXML("EchoCode presentation"))
	if err := preparePresentationTemplate(entries); err != nil {
		return err
	}
	for _, slide := range slides {
		if err := appendPresentationSlide(entries, slide); err != nil {
			return err
		}
	}
	return writePackage(filePath, entries)
}

func AddPresentationSlide(filePath string, slide SlideSpec) error {
	entries, err := readPackage(filePath)
	if err != nil {
		return err
	}
	if err := appendPresentationSlide(entries, slide); err != nil {
		return err
	}
	return writePackage(filePath, entries)
}

func preparePresentationTemplate(entries map[string][]byte) error {
	presentationDocument, err := parseXML(entries[presentationEntry], "PowerPoint presentation")
	if err != nil {
		return err
	}
	root := presentationDocument.Root()
	if firstElementByLocalName(root, "sldIdLst") == nil {
		slideList := etree.NewElement("p:sldIdLst")
		insertAt := 0
		if masterList := firstElementByLocalName(root, "sldMasterIdLst"); masterList != nil {
			insertAt = masterList.Index() + 1
		}
		root.InsertChildAt(insertAt, slideList)
	}
	if slideSize := firstElementByLocalName(root, "sldSz"); slideSize != nil {
		slideSize.RemoveAttr("cx")
		slideSize.RemoveAttr("cy")
		slideSize.RemoveAttr("type")
		slideSize.CreateAttr("cx", "12192000")
		slideSize.CreateAttr("cy", "6858000")
		slideSize.CreateAttr("type", "screen16x9")
	}
	entries[presentationEntry], err = xmlBytes(presentationDocument)
	if err != nil {
		return err
	}
	if appDocument, parseErr := parseXML(entries["docProps/app.xml"], "PowerPoint app properties"); parseErr == nil {
		if format := firstElementByLocalName(appDocument.Root(), "PresentationFormat"); format != nil {
			format.SetText("Widescreen")
		}
		if data, writeErr := xmlBytes(appDocument); writeErr == nil {
			entries["docProps/app.xml"] = data
		}
	}
	return nil
}

func appendPresentationSlide(entries map[string][]byte, slide SlideSpec) error {
	presentationDocument, err := parseXML(entries[presentationEntry], "PowerPoint presentation")
	if err != nil {
		return err
	}
	relationshipsDocument, err := parseXML(entries[presentationRelationshipsEntry], "PowerPoint relationships")
	if err != nil {
		return err
	}
	contentTypesDocument, err := parseXML(entries["[Content_Types].xml"], "PowerPoint content types")
	if err != nil {
		return err
	}
	slideList := firstElementByLocalName(presentationDocument.Root(), "sldIdLst")
	if slideList == nil {
		return errors.New("PPTX 缺少幻灯片列表")
	}
	layoutTarget, err := presentationSlideLayoutTarget(entries)
	if err != nil {
		return err
	}
	slideNumber := nextPresentationSlideNumber(entries)
	relationshipID := nextRelationshipID(relationshipsDocument.Root())
	maxSlideID := 255
	for _, slideID := range directChildrenByTag(slideList, "sldId") {
		if value, parseErr := strconv.Atoi(attributeByLocalName(slideID, "id")); parseErr == nil {
			maxSlideID = max(maxSlideID, value)
		}
	}
	item := slideList.CreateElement("p:sldId")
	item.CreateAttr("id", strconv.Itoa(maxSlideID+1))
	item.CreateAttr("r:id", relationshipID)
	relationship := relationshipsDocument.Root().CreateElement("Relationship")
	relationship.CreateAttr("Id", relationshipID)
	relationship.CreateAttr("Type", "http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide")
	relationship.CreateAttr("Target", fmt.Sprintf("slides/slide%d.xml", slideNumber))
	override := contentTypesDocument.Root().CreateElement("Override")
	override.CreateAttr("PartName", fmt.Sprintf("/ppt/slides/slide%d.xml", slideNumber))
	override.CreateAttr("ContentType", "application/vnd.openxmlformats-officedocument.presentationml.slide+xml")

	entries[presentationEntry], err = xmlBytes(presentationDocument)
	if err != nil {
		return err
	}
	entries[presentationRelationshipsEntry], err = xmlBytes(relationshipsDocument)
	if err != nil {
		return err
	}
	entries["[Content_Types].xml"], err = xmlBytes(contentTypesDocument)
	if err != nil {
		return err
	}
	entries[fmt.Sprintf("ppt/slides/slide%d.xml", slideNumber)] = []byte(presentationSlideXML(slide, slideNumber))
	entries[fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", slideNumber)] = []byte(presentationSlideRelationshipsXML(layoutTarget))
	updatePresentationSlideCount(entries, len(directChildrenByTag(slideList, "sldId")))
	return nil
}

func ReplacePresentationText(filePath, find, replace string) (int, error) {
	if find == "" {
		return 0, errors.New("查找文本不能为空")
	}
	entries, err := readPackage(filePath)
	if err != nil {
		return 0, err
	}
	replacements := 0
	for name, data := range entries {
		if !strings.HasPrefix(name, "ppt/slides/slide") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		document, parseErr := parseXML(data, name)
		if parseErr != nil {
			return 0, parseErr
		}
		changed := false
		for _, textNode := range elementsByLocalName(document.Root(), "t") {
			value := textNode.Text()
			count := strings.Count(value, find)
			if count == 0 {
				continue
			}
			textNode.SetText(strings.ReplaceAll(value, find, replace))
			replacements += count
			changed = true
		}
		if changed {
			entries[name], err = xmlBytes(document)
			if err != nil {
				return 0, err
			}
		}
	}
	if replacements > 0 {
		if err := writePackage(filePath, entries); err != nil {
			return 0, err
		}
	}
	return replacements, nil
}

func PresentationText(filePath string, maxChars int) (string, error) {
	preview, err := PreviewPresentation(filePath, PreviewOptions{MaxTextChars: maxChars, MaxSheets: 1000})
	if err != nil {
		return "", err
	}
	var builder strings.Builder
	for _, slide := range preview.Slides {
		fmt.Fprintf(&builder, "第 %d 页", slide.Number)
		if slide.Title != "" {
			fmt.Fprintf(&builder, "：%s", slide.Title)
		}
		builder.WriteByte('\n')
		for _, text := range slide.Texts {
			fmt.Fprintf(&builder, "- %s\n", text)
		}
	}
	if preview.Truncated {
		builder.WriteString("... [预览已截断]\n")
	}
	return strings.TrimSpace(builder.String()), nil
}

func orderedPresentationSlides(entries map[string][]byte) ([]string, error) {
	presentationData, ok := entries[presentationEntry]
	if !ok {
		return nil, errors.New("PPTX 缺少 ppt/presentation.xml")
	}
	relationshipData, ok := entries[presentationRelationshipsEntry]
	if !ok {
		return nil, errors.New("PPTX 缺少 presentation.xml.rels")
	}
	presentationDocument, err := parseXML(presentationData, "PowerPoint presentation")
	if err != nil {
		return nil, err
	}
	relationshipDocument, err := parseXML(relationshipData, "PowerPoint relationships")
	if err != nil {
		return nil, err
	}
	relationships := map[string]string{}
	for _, relationship := range directChildrenByTag(relationshipDocument.Root(), "Relationship") {
		if !strings.HasSuffix(attributeByLocalName(relationship, "Type"), "/slide") {
			continue
		}
		relationships[attributeByLocalName(relationship, "Id")] = attributeByLocalName(relationship, "Target")
	}
	slideList := firstElementByLocalName(presentationDocument.Root(), "sldIdLst")
	if slideList == nil {
		return []string{}, nil
	}
	result := make([]string, 0)
	for _, slideID := range directChildrenByTag(slideList, "sldId") {
		relationshipID := relationshipAttribute(slideID)
		target := relationships[relationshipID]
		if target == "" {
			continue
		}
		result = append(result, path.Clean(path.Join("ppt", target)))
	}
	return result, nil
}

func presentationShapeTexts(root *etree.Element) []string {
	result := make([]string, 0)
	for _, shape := range elementsByLocalName(root, "sp") {
		var paragraphs []string
		for _, paragraph := range elementsByLocalName(shape, "p") {
			var builder strings.Builder
			for _, text := range elementsByLocalName(paragraph, "t") {
				builder.WriteString(text.Text())
			}
			if value := strings.TrimSpace(builder.String()); value != "" {
				paragraphs = append(paragraphs, value)
			}
		}
		if len(paragraphs) > 0 {
			result = append(result, strings.Join(paragraphs, "\n"))
		}
	}
	return result
}

func relationshipAttribute(element *etree.Element) string {
	for _, attribute := range element.Attr {
		if attribute.Key == "id" && (attribute.Space == "r" || strings.Contains(attribute.FullKey(), ":")) {
			return attribute.Value
		}
	}
	return ""
}

func nextPresentationSlideNumber(entries map[string][]byte) int {
	numbers := make([]int, 0)
	for name := range entries {
		if !strings.HasPrefix(name, "ppt/slides/slide") || !strings.HasSuffix(name, ".xml") {
			continue
		}
		value := strings.TrimSuffix(strings.TrimPrefix(name, "ppt/slides/slide"), ".xml")
		if number, err := strconv.Atoi(value); err == nil {
			numbers = append(numbers, number)
		}
	}
	sort.Ints(numbers)
	if len(numbers) == 0 {
		return 1
	}
	return numbers[len(numbers)-1] + 1
}

func nextRelationshipID(root *etree.Element) string {
	maximum := 0
	for _, relationship := range directChildrenByTag(root, "Relationship") {
		value := strings.TrimPrefix(attributeByLocalName(relationship, "Id"), "rId")
		if number, err := strconv.Atoi(value); err == nil {
			maximum = max(maximum, number)
		}
	}
	return fmt.Sprintf("rId%d", maximum+1)
}

func presentationSlideLayoutTarget(entries map[string][]byte) (string, error) {
	names := make([]string, 0)
	for name := range entries {
		if strings.HasPrefix(name, "ppt/slideLayouts/slideLayout") && strings.HasSuffix(name, ".xml") {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	if len(names) == 0 {
		return "", errors.New("PPTX 缺少幻灯片版式")
	}
	selected := names[0]
	for _, name := range names {
		document, err := parseXML(entries[name], name)
		if err != nil {
			return "", err
		}
		if strings.EqualFold(attributeByLocalName(document.Root(), "type"), "blank") {
			selected = name
			break
		}
	}
	return "../slideLayouts/" + path.Base(selected), nil
}

func updatePresentationSlideCount(entries map[string][]byte, count int) {
	document, err := parseXML(entries["docProps/app.xml"], "PowerPoint app properties")
	if err != nil {
		return
	}
	if slides := firstElementByLocalName(document.Root(), "Slides"); slides != nil {
		slides.SetText(strconv.Itoa(count))
		if data, writeErr := xmlBytes(document); writeErr == nil {
			entries["docProps/app.xml"] = data
		}
	}
}

func presentationSlideXML(slide SlideSpec, number int) string {
	title := strings.TrimSpace(slide.Title)
	if title == "" {
		title = fmt.Sprintf("幻灯片 %d", number)
	}
	bodyLines := strings.Split(strings.ReplaceAll(slide.Body, "\r\n", "\n"), "\n")
	var bodyParagraphs strings.Builder
	for _, line := range bodyLines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		bodyParagraphs.WriteString(`<a:p><a:pPr lvl="0"/><a:r><a:rPr lang="zh-CN" sz="2200"/><a:t>` + escapeXMLText(line) + `</a:t></a:r><a:endParaRPr lang="zh-CN" sz="2200"/></a:p>`)
	}
	if bodyParagraphs.Len() == 0 {
		bodyParagraphs.WriteString(`<a:p><a:endParaRPr lang="zh-CN" sz="2200"/></a:p>`)
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sld xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="2" name="Title ` + strconv.Itoa(number) + `"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="685800" y="457200"/><a:ext cx="10820400" cy="1143000"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr wrap="square"/><a:lstStyle/><a:p><a:r><a:rPr lang="zh-CN" sz="3200" b="1"/><a:t>` + escapeXMLText(title) + `</a:t></a:r><a:endParaRPr lang="zh-CN" sz="3200"/></a:p></p:txBody></p:sp>` +
		`<p:sp><p:nvSpPr><p:cNvPr id="3" name="Content ` + strconv.Itoa(number) + `"/><p:cNvSpPr txBox="1"/><p:nvPr/></p:nvSpPr><p:spPr><a:xfrm><a:off x="685800" y="1905000"/><a:ext cx="10820400" cy="4114800"/></a:xfrm><a:prstGeom prst="rect"><a:avLst/></a:prstGeom><a:noFill/><a:ln><a:noFill/></a:ln></p:spPr><p:txBody><a:bodyPr wrap="square"/><a:lstStyle/>` + bodyParagraphs.String() + `</p:txBody></p:sp>` +
		`</p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sld>`
}

func presentationContentTypesXML(slideCount int) string {
	var slides strings.Builder
	for index := 1; index <= slideCount; index++ {
		fmt.Fprintf(&slides, `<Override PartName="/ppt/slides/slide%d.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slide+xml"/>`, index)
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/ppt/presentation.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"/><Override PartName="/ppt/slideMasters/slideMaster1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideMaster+xml"/><Override PartName="/ppt/slideLayouts/slideLayout1.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.slideLayout+xml"/><Override PartName="/ppt/theme/theme1.xml" ContentType="application/vnd.openxmlformats-officedocument.theme+xml"/><Override PartName="/ppt/presProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.presProps+xml"/><Override PartName="/ppt/viewProps.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.viewProps+xml"/><Override PartName="/ppt/tableStyles.xml" ContentType="application/vnd.openxmlformats-officedocument.presentationml.tableStyles+xml"/><Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/><Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>` + slides.String() + `</Types>`
}

func presentationXML(slideCount int) string {
	var slideIDs strings.Builder
	for index := 1; index <= slideCount; index++ {
		fmt.Fprintf(&slideIDs, `<p:sldId id="%d" r:id="rId%d"/>`, 255+index, 4+index)
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentation xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:sldMasterIdLst><p:sldMasterId id="2147483648" r:id="rId1"/></p:sldMasterIdLst><p:sldIdLst>` + slideIDs.String() + `</p:sldIdLst><p:sldSz cx="12192000" cy="6858000" type="screen16x9"/><p:notesSz cx="6858000" cy="9144000"/><p:defaultTextStyle><a:defPPr><a:defRPr lang="zh-CN"/></a:defPPr></p:defaultTextStyle></p:presentation>`
}

func presentationRelationshipsXML(slideCount int) string {
	var relationships strings.Builder
	relationships.WriteString(`<Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="slideMasters/slideMaster1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/presProps" Target="presProps.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/viewProps" Target="viewProps.xml"/><Relationship Id="rId4" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/tableStyles" Target="tableStyles.xml"/>`)
	for index := 1; index <= slideCount; index++ {
		fmt.Fprintf(&relationships, `<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slide" Target="slides/slide%d.xml"/>`, 4+index, index)
	}
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">` + relationships.String() + `</Relationships>`
}

func presentationAppPropertiesXML(slideCount int) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes"><Application>EchoCode</Application><PresentationFormat>Widescreen</PresentationFormat><Slides>` + strconv.Itoa(slideCount) + `</Slides><Notes>0</Notes><HiddenSlides>0</HiddenSlides><MMClips>0</MMClips><ScaleCrop>false</ScaleCrop><AppVersion>1.0</AppVersion></Properties>`
}

const presentationRootRelationshipsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="ppt/presentation.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/><Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/></Relationships>`

const presentationMasterRelationshipsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="../slideLayouts/slideLayout1.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/theme" Target="../theme/theme1.xml"/></Relationships>`

const presentationLayoutRelationshipsXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideMaster" Target="../slideMasters/slideMaster1.xml"/></Relationships>`

func presentationSlideRelationshipsXML(layoutTarget string) string {
	return `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/slideLayout" Target="` + escapeXMLAttribute(layoutTarget) + `"/></Relationships>`
}

const presentationMasterXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldMaster xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"><p:cSld name="EchoCode"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree></p:cSld><p:clrMap accent1="accent1" accent2="accent2" accent3="accent3" accent4="accent4" accent5="accent5" accent6="accent6" bg1="lt1" bg2="lt2" folHlink="folHlink" hlink="hlink" tx1="dk1" tx2="dk2"/><p:sldLayoutIdLst><p:sldLayoutId id="1" r:id="rId1"/></p:sldLayoutIdLst><p:txStyles><p:titleStyle><a:lvl1pPr algn="l"><a:defRPr sz="3200" b="1"/></a:lvl1pPr></p:titleStyle><p:bodyStyle><a:lvl1pPr marL="342900" indent="-285750"><a:buChar char="•"/><a:defRPr sz="2200"/></a:lvl1pPr></p:bodyStyle><p:otherStyle><a:defPPr><a:defRPr lang="zh-CN"/></a:defPPr></p:otherStyle></p:txStyles></p:sldMaster>`

const presentationLayoutXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:sldLayout xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" type="blank" preserve="1"><p:cSld name="Blank"><p:spTree><p:nvGrpSpPr><p:cNvPr id="1" name=""/><p:cNvGrpSpPr/><p:nvPr/></p:nvGrpSpPr><p:grpSpPr><a:xfrm><a:off x="0" y="0"/><a:ext cx="0" cy="0"/><a:chOff x="0" y="0"/><a:chExt cx="0" cy="0"/></a:xfrm></p:grpSpPr></p:spTree></p:cSld><p:clrMapOvr><a:masterClrMapping/></p:clrMapOvr></p:sldLayout>`

const presentationPropertiesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:presentationPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"/>`

const presentationViewPropertiesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><p:viewPr xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships" xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" lastView="sldView"><p:normalViewPr/><p:slideViewPr><p:cSldViewPr><p:cViewPr varScale="1"><p:scale><a:sx n="100" d="100"/><a:sy n="100" d="100"/></p:scale><p:origin x="0" y="0"/></p:cViewPr><p:guideLst/></p:cSldViewPr></p:slideViewPr><p:notesTextViewPr><p:cViewPr><p:scale><a:sx n="100" d="100"/><a:sy n="100" d="100"/></p:scale><p:origin x="0" y="0"/></p:cViewPr></p:notesTextViewPr><p:gridSpacing cx="78028800" cy="78028800"/></p:viewPr>`

const presentationTableStylesXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:tblStyleLst xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" def="{5C22544A-7EE6-4342-B048-85BDC9FD1C3A}"/>`

const presentationThemeXML = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><a:theme xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main" name="EchoCode"><a:themeElements><a:clrScheme name="EchoCode"><a:dk1><a:sysClr val="windowText" lastClr="000000"/></a:dk1><a:lt1><a:sysClr val="window" lastClr="FFFFFF"/></a:lt1><a:dk2><a:srgbClr val="1F2937"/></a:dk2><a:lt2><a:srgbClr val="F3F4F6"/></a:lt2><a:accent1><a:srgbClr val="2563EB"/></a:accent1><a:accent2><a:srgbClr val="0F766E"/></a:accent2><a:accent3><a:srgbClr val="B45309"/></a:accent3><a:accent4><a:srgbClr val="7C3AED"/></a:accent4><a:accent5><a:srgbClr val="DC2626"/></a:accent5><a:accent6><a:srgbClr val="4B5563"/></a:accent6><a:hlink><a:srgbClr val="0563C1"/></a:hlink><a:folHlink><a:srgbClr val="954F72"/></a:folHlink></a:clrScheme><a:fontScheme name="EchoCode"><a:majorFont><a:latin typeface="Aptos Display"/><a:ea typeface="Microsoft YaHei"/><a:cs typeface="Arial"/></a:majorFont><a:minorFont><a:latin typeface="Aptos"/><a:ea typeface="Microsoft YaHei"/><a:cs typeface="Arial"/></a:minorFont></a:fontScheme><a:fmtScheme name="EchoCode"><a:fillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:gradFill rotWithShape="1"><a:gsLst><a:gs pos="0"><a:schemeClr val="phClr"/></a:gs><a:gs pos="100000"><a:schemeClr val="phClr"><a:tint val="50000"/></a:schemeClr></a:gs></a:gsLst><a:lin ang="5400000" scaled="0"/></a:gradFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:fillStyleLst><a:lnStyleLst><a:ln w="6350"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln><a:ln w="12700"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln><a:ln w="19050"><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:prstDash val="solid"/></a:ln></a:lnStyleLst><a:effectStyleLst><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle><a:effectStyle><a:effectLst/></a:effectStyle></a:effectStyleLst><a:bgFillStyleLst><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill><a:solidFill><a:schemeClr val="phClr"/></a:solidFill></a:bgFillStyleLst></a:fmtScheme></a:themeElements></a:theme>`
