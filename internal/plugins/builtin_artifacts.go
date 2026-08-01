package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/SliyRainbow/EchoCode/internal/artifacts"
	"github.com/SliyRainbow/EchoCode/internal/tools"
)

func executeBuiltin(ctx context.Context, pluginID, toolName string, rawArgs json.RawMessage) (tools.Result, error) {
	if pluginID != ArtifactPluginID {
		return tools.Result{}, fmt.Errorf("unknown built-in plugin %q", pluginID)
	}
	if err := ctx.Err(); err != nil {
		return tools.Result{}, err
	}
	result, err := executeArtifactTool(toolName, rawArgs)
	if err != nil {
		return tools.Result{}, err
	}
	if err := ctx.Err(); err != nil {
		return tools.Result{}, err
	}
	return result, nil
}

func executeArtifactTool(toolName string, rawArgs json.RawMessage) (tools.Result, error) {
	switch toolName {
	case "document_inspect":
		var args struct {
			Path     string `json:"path"`
			MaxChars int    `json:"maxChars"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		if args.MaxChars <= 0 || args.MaxChars > 1<<20 {
			args.MaxChars = 128 << 10
		}
		text, err := artifacts.DocumentText(args.Path, args.MaxChars)
		return artifactResult(toolName, text, err)
	case "document_create":
		var args struct {
			Path       string                        `json:"path"`
			Title      string                        `json:"title"`
			Text       string                        `json:"text"`
			Paragraphs []artifacts.DocumentParagraph `json:"paragraphs"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		if strings.TrimSpace(args.Text) != "" {
			for _, value := range strings.Split(strings.ReplaceAll(args.Text, "\r\n", "\n"), "\n") {
				args.Paragraphs = append(args.Paragraphs, artifacts.DocumentParagraph{Text: value})
			}
		}
		err := artifacts.CreateDocument(args.Path, artifacts.DocumentSpec{Title: args.Title, Paragraphs: args.Paragraphs})
		return artifactResult(toolName, "已创建标准 DOCX："+args.Path, err)
	case "document_replace_text":
		var args struct {
			Path    string `json:"path"`
			Find    string `json:"find"`
			Replace string `json:"replace"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		count, err := artifacts.ReplaceDocumentText(args.Path, args.Find, args.Replace)
		return artifactResult(toolName, fmt.Sprintf("DOCX 已替换 %d 处文本", count), err)
	case "spreadsheet_inspect":
		var args pathArgs
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		summary, err := artifacts.SpreadsheetSummary(args.Path)
		return artifactResult(toolName, summary, err)
	case "spreadsheet_create":
		var args struct {
			Path string `json:"path"`
			artifacts.SpreadsheetCreateSpec
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		err := artifacts.CreateSpreadsheet(args.Path, args.SpreadsheetCreateSpec)
		return artifactResult(toolName, fmt.Sprintf("已创建专业 XLSX：%s（工作表 %s，%d 行）", args.Path, args.Sheet, len(args.Values)), err)
	case "spreadsheet_read_range":
		var args struct {
			Path     string `json:"path"`
			Sheet    string `json:"sheet"`
			Range    string `json:"range"`
			MaxCells int    `json:"maxCells"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		rows, err := artifacts.ReadSpreadsheetRange(args.Path, args.Sheet, args.Range, args.MaxCells)
		if err != nil {
			return artifactResult(toolName, "", err)
		}
		encoded, _ := json.Marshal(rows)
		return artifactResult(toolName, fmt.Sprintf("读取 %d 行\n%s", len(rows), encoded), nil)
	case "spreadsheet_write_range":
		var args struct {
			Path      string  `json:"path"`
			Sheet     string  `json:"sheet"`
			StartCell string  `json:"startCell"`
			Values    [][]any `json:"values"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		err := artifacts.WriteSpreadsheetRange(args.Path, args.Sheet, args.StartCell, args.Values)
		return artifactResult(toolName, fmt.Sprintf("已向 %s 的 %s!%s 写入 %d 行", args.Path, args.Sheet, args.StartCell, len(args.Values)), err)
	case "spreadsheet_add_sheet":
		var args struct {
			Path  string `json:"path"`
			Sheet string `json:"sheet"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		err := artifacts.AddSpreadsheetSheet(args.Path, args.Sheet)
		return artifactResult(toolName, fmt.Sprintf("已向 %s 添加工作表 %s", args.Path, args.Sheet), err)
	case "spreadsheet_import_xls":
		var args struct {
			Path       string `json:"path"`
			OutputPath string `json:"outputPath"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		err := artifacts.ConvertLegacySpreadsheet(args.Path, args.OutputPath)
		return artifactResult(toolName, "已将旧 XLS 导入为 "+args.OutputPath, err)
	case "presentation_inspect":
		var args struct {
			Path     string `json:"path"`
			MaxChars int    `json:"maxChars"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		if args.MaxChars <= 0 || args.MaxChars > 1<<20 {
			args.MaxChars = 128 << 10
		}
		text, err := artifacts.PresentationText(args.Path, args.MaxChars)
		return artifactResult(toolName, text, err)
	case "presentation_create":
		var args struct {
			Path   string                `json:"path"`
			Slides []artifacts.SlideSpec `json:"slides"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		err := artifacts.CreatePresentation(args.Path, args.Slides)
		return artifactResult(toolName, fmt.Sprintf("已创建标准 PPTX：%s（%d 页）", args.Path, len(args.Slides)), err)
	case "presentation_add_slide":
		var args struct {
			Path  string `json:"path"`
			Title string `json:"title"`
			Body  string `json:"body"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		err := artifacts.AddPresentationSlide(args.Path, artifacts.SlideSpec{Title: args.Title, Body: args.Body})
		return artifactResult(toolName, "已追加 PPTX 幻灯片："+args.Title, err)
	case "presentation_replace_text":
		var args struct {
			Path    string `json:"path"`
			Find    string `json:"find"`
			Replace string `json:"replace"`
		}
		if err := decodeBuiltinArgs(rawArgs, &args); err != nil {
			return pluginErrorResult(toolName, err.Error()), nil
		}
		count, err := artifacts.ReplacePresentationText(args.Path, args.Find, args.Replace)
		return artifactResult(toolName, fmt.Sprintf("PPTX 已替换 %d 处文本", count), err)
	default:
		return tools.Result{}, fmt.Errorf("unknown artifact tool %q", toolName)
	}
}

type pathArgs struct {
	Path string `json:"path"`
}

func decodeBuiltinArgs(raw json.RawMessage, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("参数解析失败: %w", err)
	}
	if decoder.More() {
		return errors.New("参数包含多个 JSON 值")
	}
	return nil
}

func artifactResult(toolName, summary string, err error) (tools.Result, error) {
	if err != nil {
		return pluginErrorResult(toolName, err.Error()), nil
	}
	return tools.Result{Summary: summary}, nil
}
