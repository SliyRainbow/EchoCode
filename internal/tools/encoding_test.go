package tools

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SliyRainbow/EchoCode/internal/pathutil"
)

func TestResolveWritePathRejectsTraversal(t *testing.T) {
	root := t.TempDir()
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}

	if _, err := policy.ResolveWritePath("../secret.txt"); err == nil {
		t.Fatal("期望目录穿越被拒绝，实际通过")
	}
	if _, err := policy.ResolveWritePath("sub/ok.txt"); err != nil {
		t.Fatalf("工作区内路径应通过，实际报错: %v", err)
	}
}

func TestResolveReadPathRejectsForeignAbsolutePath(t *testing.T) {
	root := t.TempDir()
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}
	foreign := "/home/example/project"
	if os.PathSeparator != '\\' {
		foreign = `C:\\Users\\example\\project`
	}
	_, err := policy.ResolveReadPath(foreign)
	if !errors.Is(err, ErrForeignAbsolutePath) {
		t.Fatalf("其他系统格式的路径应给出明确错误，实际: %v", err)
	}
}

func TestResolveWritePathReadOnly(t *testing.T) {
	root := t.TempDir()
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "read-only"}
	if _, err := policy.ResolveWritePath("a.txt"); err != ErrReadOnlyFilesystem {
		t.Fatalf("只读模式应返回 ErrReadOnlyFilesystem，实际: %v", err)
	}
	// 只读仍可读取。
	if _, err := policy.ResolveReadPath("a.txt"); err != nil {
		t.Fatalf("只读模式读取应通过，实际: %v", err)
	}
}

func TestResolveReadPathExtraRoot(t *testing.T) {
	root := t.TempDir()
	extra := t.TempDir()
	policy := SandboxPolicy{
		WorkspaceRoot:      root,
		ExtraWritableRoots: []string{extra},
		FilesystemAccess:   "workspace-write",
	}
	target := filepath.Join(extra, "x.txt")
	if _, err := policy.ResolveWritePath(target); err != nil {
		t.Fatalf("额外可写根内路径应通过，实际: %v", err)
	}
}

func TestResolveWritePathAcceptsCanonicalWorkspaceAlias(t *testing.T) {
	root := t.TempDir()
	canonicalRoot, err := pathutil.Canonical(root)
	if err != nil {
		t.Fatal(err)
	}
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}
	target := filepath.Join(canonicalRoot, "nested", "file.txt")
	if _, err := policy.ResolveWritePath(target); err != nil {
		t.Fatalf("canonical workspace path should be accepted: %v", err)
	}
}

func TestResolveReadPathRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "outside-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}
	if _, err := policy.ResolveReadPath(filepath.Join("outside-link", "secret.txt")); !errors.Is(err, ErrPathOutsideWorkspace) {
		t.Fatalf("symlink escape should be rejected, got %v", err)
	}
}

func TestResolveWritePathRejectsFinalSymlink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target.txt")
	if err := os.WriteFile(target, []byte("target"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	policy := SandboxPolicy{WorkspaceRoot: root, FilesystemAccess: "workspace-write"}
	if _, err := policy.ResolveWritePath("link.txt"); !errors.Is(err, ErrSymlinkMutation) {
		t.Fatalf("final symlink mutation should be rejected, got %v", err)
	}
}

func TestLocalPowerShellCommandDoesNotRequireNetworkPermission(t *testing.T) {
	policy := SandboxPolicy{WorkspaceRoot: t.TempDir(), FilesystemAccess: "workspace-write", NetworkAccess: false, ShellAccess: true}
	if err := policy.ValidateCommand(`powershell -NoProfile -Command "Write-Output ok"`); err != nil {
		t.Fatalf("local PowerShell command was treated as network access: %v", err)
	}
}

func TestLineEndingPreserved(t *testing.T) {
	crlf := []byte("line1\r\nline2\r\n")
	meta := decodeFileText(crlf)
	if meta.LineEnding != LineEndingCRLF {
		t.Fatalf("应探测为 CRLF，实际: %s", meta.LineEnding)
	}
	if strings.Contains(meta.Content, "\r") {
		t.Fatal("归一化后内容不应包含 \\r")
	}
	// 写回应恢复 CRLF。
	out := EncodeFileText(meta.Content, meta)
	if !strings.Contains(string(out), "\r\n") {
		t.Fatal("写回应恢复 CRLF")
	}
}

func TestBOMPreserved(t *testing.T) {
	withBOM := append(append([]byte{}, utf8BOM...), []byte("你好\nworld\n")...)
	meta := decodeFileText(withBOM)
	if !meta.HadBOM {
		t.Fatal("应探测到 BOM")
	}
	if strings.HasPrefix(meta.Content, string(utf8BOM)) {
		t.Fatal("Content 不应保留 BOM 字节")
	}
	out := EncodeFileText(meta.Content, meta)
	if !strings.HasPrefix(string(out), string(utf8BOM)) {
		t.Fatal("写回应恢复 BOM")
	}
}

func TestAtomicWriteRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "note.txt")
	content := "第一行\n第二行\n"
	meta := FileText{LineEnding: LineEndingLF}
	if err := WriteFileTextAtomic(path, content, meta); err != nil {
		t.Fatalf("原子写入失败: %v", err)
	}
	back, err := ReadFileText(path)
	if err != nil {
		t.Fatalf("回读失败: %v", err)
	}
	if back.Content != content {
		t.Fatalf("回读内容不一致: %q", back.Content)
	}
	// 确认没有残留临时文件。
	entries, _ := os.ReadDir(root)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".echocode-") {
			t.Fatalf("残留临时文件: %s", e.Name())
		}
	}
}

func TestGBKDecode(t *testing.T) {
	// "中文" 的 GBK 编码字节。
	gbk := []byte{0xD6, 0xD0, 0xCE, 0xC4}
	meta := decodeFileText(gbk)
	if meta.Content != "中文" {
		t.Fatalf("GBK 解码失败，得到: %q", meta.Content)
	}
	if meta.Encoding != EncodingGB18030 {
		t.Fatalf("GBK encoding = %q", meta.Encoding)
	}
	encoded := EncodeFileText(meta.Content, meta)
	if !bytes.Equal(encoded, gbk) {
		t.Fatalf("GB18030 round trip changed bytes: %x", encoded)
	}
}

func TestUTF16LERoundTrip(t *testing.T) {
	raw := append(append([]byte{}, utf16LEBOM...), encodeUTF16("中文\r\nhello\r\n", binary.LittleEndian)...)
	meta := decodeFileText(raw)
	if meta.Encoding != EncodingUTF16LE || !meta.HadBOM || meta.LineEnding != LineEndingCRLF {
		t.Fatalf("UTF-16 metadata = %#v", meta)
	}
	if meta.Content != "中文\nhello\n" {
		t.Fatalf("UTF-16 content = %q", meta.Content)
	}
	if encoded := EncodeFileText(meta.Content, meta); !bytes.Equal(encoded, raw) {
		t.Fatalf("UTF-16 round trip changed bytes: %x != %x", encoded, raw)
	}
}

func TestDecodeFileTextDistinguishesBinaryFromUTF8AndUTF16(t *testing.T) {
	binaryContent := decodeFileText([]byte{0, 1, 2, 3})
	if !binaryContent.Binary || binaryContent.Encoding != EncodingBinary {
		t.Fatalf("binary content misclassified: %+v", binaryContent)
	}

	utf16WithoutBOM := encodeUTF16("hello\r\n", binary.LittleEndian)
	text := decodeFileText(utf16WithoutBOM)
	if text.Binary || text.Encoding != EncodingUTF16LE || text.Content != "hello\n" {
		t.Fatalf("UTF-16 without BOM misclassified: %+v", text)
	}
}

func TestPowerShellScriptDefaultUsesUTF8BOMOnWindows(t *testing.T) {
	if os.PathSeparator != '\\' {
		t.Skip("Windows-specific script default")
	}
	meta := DefaultFileMetaForPath("script.ps1")
	if meta.Encoding != EncodingUTF8 || !meta.HadBOM || meta.LineEnding != LineEndingCRLF {
		t.Fatalf("PowerShell metadata = %#v", meta)
	}
}

func TestDecodeCommandOutputUTF16LE(t *testing.T) {
	raw := append(append([]byte{}, utf16LEBOM...), encodeUTF16("命令输出\r\n", binary.LittleEndian)...)
	if got := DecodeCommandOutput(raw); got != "命令输出\r\n" {
		t.Fatalf("decoded command output = %q", got)
	}
}
