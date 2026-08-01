package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

const (
	maxEditableFileBytes = 8 * 1024 * 1024
	maxDiffMatrixCells   = 4 * 1024 * 1024
	maxDiffPatchBytes    = 512 * 1024
)

// 写入工具：write_file / apply_patch。改动文件时产出 unified diff，点亮前端 diff 卡片。
// 全部走编码安全层（原子写、保留行尾/BOM），绝不经 shell。
//
// 为支持审批：写工具实现 MutatingTool，把执行拆成 Preview(算 diff 不落盘) + ApplyPending(落盘)。
// 审批闸门在两者之间插入；无需审批时 Execute 直接串联二者。

// PendingWrite 是预演产出、尚未落盘的一次文件写入。
type PendingWrite struct {
	AbsPath            string
	Content            string
	Meta               FileText
	Change             FileChange
	ExpectedExisted    bool
	ExpectedHash       string
	SourcePath         string
	ExpectedSourceHash string
	Delete             bool
}

// MutatingTool 是会改文件的工具，支持「预演」以便在落盘前审批。
type MutatingTool interface {
	Tool
	// Preview 计算改动但不落盘：返回可展示的 Result（含 diff）与待落盘写入。
	// 若无实际改动（内容相同）或校验失败，pending 为 nil、Result 描述原因。
	Preview(ctx context.Context, rawArgs json.RawMessage) (Result, *PendingWrite, error)
}

// ApplyPending 把预演产出的写入真正落盘（编码安全、原子写）。
func ApplyPending(p *PendingWrite) error {
	if p == nil {
		return nil
	}
	if err := verifyPendingBase(p); err != nil {
		return err
	}
	if p.Delete {
		return os.Remove(p.AbsPath)
	}
	return WriteFileTextAtomic(p.AbsPath, p.Content, p.Meta)
}

func verifyPendingBase(p *PendingWrite) error {
	if p.SourcePath != "" {
		source, err := ReadFileText(p.SourcePath)
		if err != nil {
			return fmt.Errorf("源文件在预演后已不可读取: %w", err)
		}
		if source.Binary || !hashMatches(p.ExpectedSourceHash, FileTextSHA256(source.Content)) {
			return fmt.Errorf("源文件在预演后已被修改，请重新读取后再复制")
		}
	}
	current, err := ReadFileText(p.AbsPath)
	if !p.ExpectedExisted {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("目标文件在预演后已被创建，请重新读取后再修改")
	}
	if err != nil {
		return fmt.Errorf("目标文件在预演后已不可读取: %w", err)
	}
	if current.Binary {
		return fmt.Errorf("目标文件已变为二进制文件，已取消写入")
	}
	if p.ExpectedHash != "" && !hashMatches(p.ExpectedHash, FileTextSHA256(current.Content)) {
		return fmt.Errorf("目标文件在预演后已被修改，请重新读取后再修改")
	}
	return nil
}

// CompletePending marks an applied mutation as complete and exposes the file as
// a first-class artifact for the desktop UI.
func CompletePending(result Result, pending *PendingWrite) Result {
	if pending == nil {
		return result
	}
	if pending.Delete {
		result.Summary = strings.Replace(result.Summary, "将删除", "已删除", 1)
		return result
	}
	result.Summary = strings.Replace(result.Summary, "将写入", "已写入", 1)
	result.Summary = strings.Replace(result.Summary, "将修改", "已修改", 1)
	result.Summary = strings.Replace(result.Summary, "将重写", "已重写", 1)
	result.Summary = strings.Replace(result.Summary, "将复制", "已复制", 1)
	fileAction := "modified"
	if !pending.Change.Existed {
		fileAction = "created"
	}
	result.Parts = append(result.Parts, ResultPart{
		Kind:       PartFile,
		Path:       pending.Change.Path,
		LineCount:  countLines(pending.Change.After),
		Created:    !pending.Change.Existed,
		FileAction: fileAction,
	})
	return result
}

// WriteFileTool 覆盖写入整个文件内容。
type WriteFileTool struct{ Policy SandboxPolicy }

func (t WriteFileTool) Name() string { return "write_file" }
func (t WriteFileTool) Description() string {
	return "把给定文本写入当前工作区内的相对路径（覆盖）。默认保留原编码与行尾；新建 PowerShell 脚本会使用兼容 Windows PowerShell 5 的 UTF-8 BOM。支持 expected_sha256 防止覆盖并发修改。"
}
func (t WriteFileTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":            map[string]any{"type": "string", "description": "目标文件路径"},
			"content":         map[string]any{"type": "string", "description": "要写入的完整文本内容"},
			"expected_sha256": map[string]any{"type": "string", "description": "可选；read_file 返回的 sha256，用于防止覆盖外部修改"},
			"encoding":        map[string]any{"type": "string", "enum": []string{"preserve", "auto", "utf-8", "utf-8-bom", "utf-16le", "utf-16be", "gb18030"}},
			"line_ending":     map[string]any{"type": "string", "enum": []string{"preserve", "auto", "lf", "crlf"}},
		},
		"required": []string{"path", "content"},
	}
}

func (t WriteFileTool) Preview(_ context.Context, rawArgs json.RawMessage) (Result, *PendingWrite, error) {
	var args struct {
		Path           string `json:"path"`
		Content        string `json:"content"`
		ExpectedSHA256 string `json:"expected_sha256"`
		Encoding       string `json:"encoding"`
		LineEnding     string `json:"line_ending"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("参数解析失败: " + err.Error()), nil, nil
	}
	abs, err := t.Policy.ResolveWritePath(args.Path)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}

	// 读取旧内容（若存在）以生成 diff 并保留编码风格。
	var oldContent string
	existed := false
	meta := DefaultFileMetaForPath(args.Path)
	beforeMeta := meta
	if existing, readErr := ReadFileText(abs); readErr == nil {
		if existing.Binary {
			return errorResult("目标是二进制文件，write_file 只允许文本文件"), nil, nil
		}
		oldContent = existing.Content
		meta = existing
		beforeMeta = existing
		existed = true
	} else if !os.IsNotExist(readErr) {
		return errorResult("读取原文件失败: " + readErr.Error()), nil, nil
	}
	if args.ExpectedSHA256 != "" && (!existed || !hashMatches(args.ExpectedSHA256, FileTextSHA256(oldContent))) {
		return errorResult("expected_sha256 与当前文件不一致，请重新读取文件"), nil, nil
	}
	resolvedMeta, formatErr := resolveRequestedFileMeta(args.Path, meta, existed, args.Encoding, args.LineEnding)
	if formatErr != nil {
		return errorResult(formatErr.Error()), nil, nil
	}
	meta = resolvedMeta

	newContent := normalizeToLF(args.Content)
	if len([]byte(newContent)) > maxEditableFileBytes {
		return errorResult("file content exceeds the 8 MiB editable-file limit"), nil, nil
	}
	if len([]byte(oldContent)) > maxEditableFileBytes {
		return errorResult("the existing file exceeds the 8 MiB editable-file limit"), nil, nil
	}
	metaChanged := meta.LineEnding != beforeMeta.LineEnding || meta.Encoding != beforeMeta.Encoding || meta.HadBOM != beforeMeta.HadBOM
	if newContent == oldContent && !metaChanged {
		return Result{Summary: fmt.Sprintf("%s 内容无变化", args.Path)}, nil, nil
	}

	patch, adds, dels := unifiedDiff(args.Path, oldContent, newContent)
	change := FileChange{
		Path: args.Path, Before: oldContent, After: newContent, Existed: existed,
		LineEnding: string(beforeMeta.LineEnding), Encoding: string(beforeMeta.Encoding), HadBOM: beforeMeta.HadBOM,
		AfterLineEnding: string(meta.LineEnding), AfterEncoding: string(meta.Encoding), AfterHadBOM: meta.HadBOM,
	}
	summary := fmt.Sprintf("将写入 %s（+%d -%d）", args.Path, adds, dels)
	if newContent == oldContent && metaChanged {
		summary = fmt.Sprintf("将重写 %s 的文本格式（%s/%s）", args.Path, meta.Encoding, meta.LineEnding)
	}
	result := Result{
		Summary: summary,
		Parts:   []ResultPart{{Kind: PartDiff, Path: args.Path, Patch: patch, Additions: adds, Deletions: dels}},
		Changes: []FileChange{change},
	}
	return result, &PendingWrite{
		AbsPath: abs, Content: newContent, Meta: meta, Change: change,
		ExpectedExisted: existed, ExpectedHash: FileTextSHA256(oldContent),
	}, nil
}

func (t WriteFileTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	result, pending, err := t.Preview(ctx, rawArgs)
	if err != nil || pending == nil {
		return result, err
	}
	if applyErr := ApplyPending(pending); applyErr != nil {
		return errorResult("写入失败: " + applyErr.Error()), nil
	}
	return CompletePending(result, pending), nil
}

// ApplyPatchTool 对文件做「查找-替换」式修改（比整文件覆盖更省 tokens）。
type ApplyPatchTool struct{ Policy SandboxPolicy }

func (t ApplyPatchTool) Name() string { return "apply_patch" }
func (t ApplyPatchTool) Description() string {
	return "对当前工作区文本文件执行一个或多个精确字符串替换。默认每段 old_string 必须唯一；可显式 replace_all。支持 expected_sha256，原子写入并保留编码。"
}
func (t ApplyPatchTool) InputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":            map[string]any{"type": "string", "description": "目标文件路径"},
			"old_string":      map[string]any{"type": "string", "description": "单次编辑要替换的原文"},
			"new_string":      map[string]any{"type": "string", "description": "单次编辑的替换文本"},
			"replace_all":     map[string]any{"type": "boolean"},
			"expected_sha256": map[string]any{"type": "string", "description": "可选；read_file 返回的 sha256"},
			"edits": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"old_string":  map[string]any{"type": "string"},
						"new_string":  map[string]any{"type": "string"},
						"replace_all": map[string]any{"type": "boolean"},
					},
					"required": []string{"old_string", "new_string"},
				},
			},
		},
		"required": []string{"path"},
	}
}

func (t ApplyPatchTool) Preview(_ context.Context, rawArgs json.RawMessage) (Result, *PendingWrite, error) {
	var args struct {
		Path           string `json:"path"`
		OldString      string `json:"old_string"`
		NewString      string `json:"new_string"`
		ReplaceAll     bool   `json:"replace_all"`
		ExpectedSHA256 string `json:"expected_sha256"`
		Edits          []struct {
			OldString  string `json:"old_string"`
			NewString  string `json:"new_string"`
			ReplaceAll bool   `json:"replace_all"`
		} `json:"edits"`
	}
	if err := json.Unmarshal(rawArgs, &args); err != nil {
		return errorResult("参数解析失败: " + err.Error()), nil, nil
	}
	abs, err := t.Policy.ResolveWritePath(args.Path)
	if err != nil {
		return errorResult(err.Error()), nil, nil
	}
	existing, err := ReadFileText(abs)
	if err != nil {
		return errorResult("读取文件失败: " + err.Error()), nil, nil
	}
	if existing.Binary {
		return errorResult("目标是二进制文件，apply_patch 只允许文本文件"), nil, nil
	}
	if len([]byte(existing.Content)) > maxEditableFileBytes {
		return errorResult("the existing file exceeds the 8 MiB editable-file limit"), nil, nil
	}

	if args.ExpectedSHA256 != "" && !hashMatches(args.ExpectedSHA256, FileTextSHA256(existing.Content)) {
		return errorResult("expected_sha256 与当前文件不一致，请重新读取文件"), nil, nil
	}
	type edit struct {
		oldText    string
		newText    string
		replaceAll bool
	}
	edits := make([]edit, 0, len(args.Edits)+1)
	if len(args.Edits) == 0 {
		edits = append(edits, edit{oldText: args.OldString, newText: args.NewString, replaceAll: args.ReplaceAll})
	} else {
		for _, item := range args.Edits {
			edits = append(edits, edit{oldText: item.OldString, newText: item.NewString, replaceAll: item.ReplaceAll})
		}
	}
	newContent := existing.Content
	for index, item := range edits {
		oldNorm := normalizeToLF(item.oldText)
		newNorm := normalizeToLF(item.newText)
		if oldNorm == "" {
			return errorResult(fmt.Sprintf("第 %d 个 edit 的 old_string 不能为空", index+1)), nil, nil
		}
		occurrences := strings.Count(newContent, oldNorm)
		if occurrences == 0 {
			return errorResult(fmt.Sprintf("第 %d 个 edit 的 old_string 未找到", index+1)), nil, nil
		}
		if occurrences > 1 && !item.replaceAll {
			return errorResult(fmt.Sprintf("第 %d 个 edit 的 old_string 出现 %d 次；请提供更多上下文或设置 replace_all", index+1, occurrences)), nil, nil
		}
		limit := 1
		if item.replaceAll {
			limit = -1
		}
		newContent = strings.Replace(newContent, oldNorm, newNorm, limit)
	}
	if len([]byte(newContent)) > maxEditableFileBytes {
		return errorResult("patched file content exceeds the 8 MiB editable-file limit"), nil, nil
	}
	patch, adds, dels := unifiedDiff(args.Path, existing.Content, newContent)
	change := FileChange{
		Path: args.Path, Before: existing.Content, After: newContent, Existed: true,
		LineEnding: string(existing.LineEnding), Encoding: string(existing.Encoding), HadBOM: existing.HadBOM,
		AfterLineEnding: string(existing.LineEnding), AfterEncoding: string(existing.Encoding), AfterHadBOM: existing.HadBOM,
	}
	result := Result{
		Summary: fmt.Sprintf("将修改 %s（+%d -%d）", args.Path, adds, dels),
		Parts:   []ResultPart{{Kind: PartDiff, Path: args.Path, Patch: patch, Additions: adds, Deletions: dels}},
		Changes: []FileChange{change},
	}
	return result, &PendingWrite{
		AbsPath: abs, Content: newContent, Meta: existing, Change: change,
		ExpectedExisted: true, ExpectedHash: FileTextSHA256(existing.Content),
	}, nil
}

func (t ApplyPatchTool) Execute(ctx context.Context, rawArgs json.RawMessage) (Result, error) {
	result, pending, err := t.Preview(ctx, rawArgs)
	if err != nil || pending == nil {
		return result, err
	}
	if applyErr := ApplyPending(pending); applyErr != nil {
		return errorResult("写入失败: " + applyErr.Error()), nil
	}
	return CompletePending(result, pending), nil
}

func resolveRequestedFileMeta(path string, current FileText, existed bool, encoding, lineEnding string) (FileText, error) {
	meta := current
	if !existed {
		meta = DefaultFileMetaForPath(path)
	}
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "preserve":
		if !existed {
			meta = DefaultFileMetaForPath(path)
		}
	case "auto":
		meta = DefaultFileMetaForPath(path)
	case "utf-8":
		meta.Encoding, meta.HadBOM = EncodingUTF8, false
	case "utf-8-bom":
		meta.Encoding, meta.HadBOM = EncodingUTF8, true
	case "utf-16le":
		meta.Encoding, meta.HadBOM = EncodingUTF16LE, true
	case "utf-16be":
		meta.Encoding, meta.HadBOM = EncodingUTF16BE, true
	case "gb18030", "gbk":
		meta.Encoding, meta.HadBOM = EncodingGB18030, false
	default:
		return FileText{}, fmt.Errorf("unsupported text encoding: %s", encoding)
	}
	switch strings.ToLower(strings.TrimSpace(lineEnding)) {
	case "", "preserve":
		if !existed {
			meta.LineEnding = DefaultFileMetaForPath(path).LineEnding
		}
	case "auto":
		meta.LineEnding = DefaultFileMetaForPath(path).LineEnding
	case "lf":
		meta.LineEnding = LineEndingLF
	case "crlf":
		meta.LineEnding = LineEndingCRLF
	default:
		return FileText{}, fmt.Errorf("unsupported line ending: %s", lineEnding)
	}
	return meta, nil
}

func hashMatches(expected, actual string) bool {
	normalize := func(value string) string {
		value = strings.ToLower(strings.TrimSpace(value))
		return strings.TrimPrefix(value, "sha256:")
	}
	return normalize(expected) != "" && normalize(expected) == normalize(actual)
}

// unifiedDiff 生成简化的 unified diff 文本，并统计增删行数。
// 采用逐行 LCS，足以驱动前端 diff 卡片的绿/红渲染。
func unifiedDiff(path, oldText, newText string) (patch string, additions int, deletions int) {
	oldLines := splitLinesKeep(oldText)
	newLines := splitLinesKeep(newText)
	ops := diffLines(oldLines, newLines)

	var b strings.Builder
	fmt.Fprintf(&b, "diff --git a/%s b/%s\n", path, path)
	fmt.Fprintf(&b, "--- a/%s\n+++ b/%s\n", path, path)
	truncated := false
	for _, op := range ops {
		switch op.kind {
		case diffEqual:
			appendDiffLine(&b, " "+op.line+"\n", &truncated)
		case diffAdd:
			appendDiffLine(&b, "+"+op.line+"\n", &truncated)
			additions++
		case diffDel:
			appendDiffLine(&b, "-"+op.line+"\n", &truncated)
			deletions++
		}
	}
	if truncated {
		b.WriteString("... [diff truncated]\n")
	}
	return b.String(), additions, deletions
}

func appendDiffLine(builder *strings.Builder, line string, truncated *bool) {
	if *truncated {
		return
	}
	if builder.Len()+len(line) > maxDiffPatchBytes {
		*truncated = true
		return
	}
	builder.WriteString(line)
}

func splitLinesKeep(s string) []string {
	if s == "" {
		return []string{}
	}
	// 丢弃末尾换行产生的空段，使「2 行内容 + 末尾换行」按 2 行计（与 git 一致）。
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

type diffKind int

const (
	diffEqual diffKind = iota
	diffAdd
	diffDel
)

type diffOp struct {
	kind diffKind
	line string
}

// diffLines 用经典 LCS 动态规划产出逐行差异。行数受读/写上限约束，规模可控。
func diffLines(a, b []string) []diffOp {
	n, m := len(a), len(b)
	if n > 0 && m > maxDiffMatrixCells/n {
		ops := make([]diffOp, 0, n+m)
		for _, line := range a {
			ops = append(ops, diffOp{kind: diffDel, line: line})
		}
		for _, line := range b {
			ops = append(ops, diffOp{kind: diffAdd, line: line})
		}
		return ops
	}
	// lcs[i][j] = a[i:] 与 b[j:] 的最长公共子序列长度。
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var ops []diffOp
	i, j := 0, 0
	for i < n && j < m {
		if a[i] == b[j] {
			ops = append(ops, diffOp{diffEqual, a[i]})
			i++
			j++
		} else if lcs[i+1][j] >= lcs[i][j+1] {
			ops = append(ops, diffOp{diffDel, a[i]})
			i++
		} else {
			ops = append(ops, diffOp{diffAdd, b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, diffOp{diffDel, a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, diffOp{diffAdd, b[j]})
	}
	return ops
}
