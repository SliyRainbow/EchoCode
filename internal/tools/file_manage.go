package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CopyFileTool copies a text file through the same encoding-safe and
// transactional path used by write_file. Moves are intentionally composed as
// copy_file followed by delete_file so each filesystem change has its own
// approval and rewind snapshot.
type CopyFileTool struct{ Policy SandboxPolicy }

func (t CopyFileTool) Name() string { return "copy_file" }
func (t CopyFileTool) Description() string {
	return "复制工作区内的文本文件到一个尚不存在的目标路径，保留编码、BOM 与行尾。移动文件时先 copy_file，再 delete_file；不要使用 shell。"
}
func (t CopyFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"source":                 map[string]any{"type": "string"},
			"destination":            map[string]any{"type": "string"},
			"expected_source_sha256": map[string]any{"type": "string", "description": "可选；read_file 返回的源文件 sha256"},
		},
		"required": []string{"source", "destination"},
	}
}
func (t CopyFileTool) Preview(_ context.Context, rawArgs json.RawMessage) (Result, *PendingWrite, error) {
	var args struct {
		Source               string `json:"source"`
		Destination          string `json:"destination"`
		ExpectedSourceSHA256 string `json:"expected_source_sha256"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("参数解析失败: " + err.Error()), nil, nil
	}
	sourceAbs, err := t.Policy.ResolveReadPath(args.Source)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	destinationAbs, err := t.Policy.ResolveWritePath(args.Destination)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	if sameFilePath(sourceAbs, destinationAbs) {
		return errorResult("源文件与目标文件相同"), nil, nil
	}
	info, err := os.Stat(sourceAbs)
	if err != nil || !info.Mode().IsRegular() {
		return errorResult("源路径不是可读取的普通文件"), nil, nil
	}
	if info.Size() > maxEditableFileBytes {
		return errorResult("源文件超过 8 MiB 文本工具上限"), nil, nil
	}
	source, err := ReadFileText(sourceAbs)
	if err != nil {
		return errorResult("读取源文件失败: " + err.Error()), nil, nil
	}
	if source.Binary {
		return errorResult("copy_file 目前只复制文本文件；二进制文件请使用专用资源工具"), nil, nil
	}
	sourceHash := FileTextSHA256(source.Content)
	if args.ExpectedSourceSHA256 != "" && !hashMatches(args.ExpectedSourceSHA256, sourceHash) {
		return errorResult("expected_source_sha256 与当前源文件不一致"), nil, nil
	}
	if _, err := os.Stat(destinationAbs); err == nil {
		return errorResult("目标文件已存在；请先读取并使用 write_file/apply_patch，或选择新路径"), nil, nil
	} else if !os.IsNotExist(err) {
		return errorResult("无法检查目标文件: " + err.Error()), nil, nil
	}
	patch, additions, deletions := unifiedDiff(args.Destination, "", source.Content)
	change := FileChange{
		Path: args.Destination, Before: "", After: source.Content, Existed: false,
		LineEnding: string(source.LineEnding), Encoding: string(source.Encoding), HadBOM: source.HadBOM,
		AfterLineEnding: string(source.LineEnding), AfterEncoding: string(source.Encoding), AfterHadBOM: source.HadBOM,
	}
	result := Result{
		Summary: fmt.Sprintf("将复制 %s 到 %s（%s）", args.Source, args.Destination, sourceHash),
		Parts:   []ResultPart{{Kind: PartDiff, Path: args.Destination, Patch: patch, Additions: additions, Deletions: deletions}},
		Changes: []FileChange{change},
	}
	return result, &PendingWrite{
		AbsPath: destinationAbs, Content: source.Content, Meta: source, Change: change,
		ExpectedExisted: false, SourcePath: sourceAbs, ExpectedSourceHash: sourceHash,
	}, nil
}
func (t CopyFileTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	result, pending, err := t.Preview(ctx, rawArgs)
	if err != nil || pending == nil {
		return result, err
	}
	if err := ApplyPending(pending); err != nil {
		return errorResult("复制失败: " + err.Error()), nil
	}
	result.Summary = strings.Replace(result.Summary, "将复制", "已复制", 1)
	return CompletePending(result, pending), nil
}

type DeleteFileTool struct{ Policy SandboxPolicy }

func (t DeleteFileTool) Name() string { return "delete_file" }
func (t DeleteFileTool) Description() string {
	return "删除工作区内的单个文本文件。执行前生成删除 diff、校验 sha256，并记录快照以支持 rewind。不要使用 del/rm/Remove-Item。"
}
func (t DeleteFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":            map[string]any{"type": "string"},
			"expected_sha256": map[string]any{"type": "string", "description": "可选；read_file 返回的 sha256"},
		},
		"required": []string{"path"},
	}
}
func (t DeleteFileTool) Preview(_ context.Context, rawArgs json.RawMessage) (Result, *PendingWrite, error) {
	var args struct {
		Path           string `json:"path"`
		ExpectedSHA256 string `json:"expected_sha256"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("参数解析失败: " + err.Error()), nil, nil
	}
	abs, err := t.Policy.ResolveWritePath(args.Path)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	info, err := os.Stat(abs)
	if err != nil || !info.Mode().IsRegular() {
		return errorResult("目标不是可删除的普通文件"), nil, nil
	}
	if info.Size() > maxEditableFileBytes {
		return errorResult("目标文件超过 8 MiB 文本工具上限"), nil, nil
	}
	existing, err := ReadFileText(abs)
	if err != nil {
		return errorResult("读取目标文件失败: " + err.Error()), nil, nil
	}
	if existing.Binary {
		return errorResult("delete_file 目前只删除文本文件；二进制资源需要专用资源工具"), nil, nil
	}
	hash := FileTextSHA256(existing.Content)
	if args.ExpectedSHA256 != "" && !hashMatches(args.ExpectedSHA256, hash) {
		return errorResult("expected_sha256 与当前文件不一致"), nil, nil
	}
	patch, additions, deletions := unifiedDiff(args.Path, existing.Content, "")
	change := FileChange{
		Path: args.Path, Before: existing.Content, After: "", Existed: true, Deleted: true,
		LineEnding: string(existing.LineEnding), Encoding: string(existing.Encoding), HadBOM: existing.HadBOM,
	}
	result := Result{
		Summary: fmt.Sprintf("将删除 %s（%s）", args.Path, hash),
		Parts:   []ResultPart{{Kind: PartDiff, Path: args.Path, Patch: patch, Additions: additions, Deletions: deletions}},
		Changes: []FileChange{change},
	}
	return result, &PendingWrite{
		AbsPath: abs, Meta: existing, Change: change, Delete: true,
		ExpectedExisted: true, ExpectedHash: hash,
	}, nil
}
func (t DeleteFileTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	result, pending, err := t.Preview(ctx, rawArgs)
	if err != nil || pending == nil {
		return result, err
	}
	if err := ApplyPending(pending); err != nil {
		return errorResult("删除失败: " + err.Error()), nil
	}
	return CompletePending(result, pending), nil
}

func sameFilePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if os.PathSeparator == '\\' {
		return strings.EqualFold(left, right)
	}
	return left == right
}
