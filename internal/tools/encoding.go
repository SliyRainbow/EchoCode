// Package tools 提供 agent 的工具执行能力。
// 本文件是编码安全层：所有文件读写都经此处，绝不经过 shell，
// 以避免 Windows 上 shell 重定向/echo 造成的 GBK/UTF-8 混乱、BOM 丢失、
// CRLF/LF 被改写等编码损坏问题。
package tools

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// LineEnding 表示文本原有的行尾风格，写回时原样保留。
type LineEnding string

type TextEncoding string

const (
	LineEndingLF   LineEnding = "lf"   // \n
	LineEndingCRLF LineEnding = "crlf" // \r\n

	EncodingUTF8    TextEncoding = "utf-8"
	EncodingUTF16LE TextEncoding = "utf-16le"
	EncodingUTF16BE TextEncoding = "utf-16be"
	EncodingGB18030 TextEncoding = "gb18030"
	EncodingBinary  TextEncoding = "binary"
)

// utf8BOM 是 UTF-8 字节序标记。读入时探测并剥离，写回时按原文决定是否保留。
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}
var utf16LEBOM = []byte{0xFF, 0xFE}
var utf16BEBOM = []byte{0xFE, 0xFF}

// FileText 保存一次读入的文本及其编码元信息，写回时据此无损还原。
type FileText struct {
	Content    string     // 已归一化为 LF 的 UTF-8 文本（便于 diff 与编辑）
	LineEnding LineEnding // 原始行尾风格
	Encoding   TextEncoding
	HadBOM     bool
	Binary     bool
}

// ReadFileText 以编码安全方式读取文件：探测并剥离 BOM、探测行尾、
// 若非法 UTF-8 则尝试按 GBK 解码（Windows 中文环境常见）。
// 返回的 Content 统一为 LF，方便上层做 diff / patch。
func ReadFileText(path string) (FileText, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return FileText{}, err
	}
	return decodeFileText(raw), nil
}

// ReadFileSnapshot preserves exact bytes in the existing string-based event
// store. Binary snapshots are base64, including text files changed by plugins.
func ReadFileSnapshot(path string) (FileText, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return FileText{}, err
	}
	return FileText{Content: base64.StdEncoding.EncodeToString(raw), Encoding: EncodingBinary, Binary: true}, nil
}

func decodeFileText(raw []byte) FileText {
	result := FileText{LineEnding: LineEndingLF, Encoding: EncodingUTF8}
	var text string

	switch {
	case bytes.HasPrefix(raw, utf8BOM):
		result.HadBOM = true
		result.Encoding = EncodingUTF8
		raw = raw[len(utf8BOM):]
		if !utf8.Valid(raw) {
			result.Encoding = EncodingBinary
			result.Binary = true
			return result
		}
		text = string(raw)
	case bytes.HasPrefix(raw, utf16LEBOM):
		result.HadBOM = true
		result.Encoding = EncodingUTF16LE
		var ok bool
		text, ok = decodeUTF16(raw[len(utf16LEBOM):], binary.LittleEndian)
		if !ok {
			result.Encoding, result.Binary = EncodingBinary, true
			return result
		}
	case bytes.HasPrefix(raw, utf16BEBOM):
		result.HadBOM = true
		result.Encoding = EncodingUTF16BE
		var ok bool
		text, ok = decodeUTF16(raw[len(utf16BEBOM):], binary.BigEndian)
		if !ok {
			result.Encoding, result.Binary = EncodingBinary, true
			return result
		}
	case looksLikeUTF16(raw, binary.LittleEndian):
		result.Encoding = EncodingUTF16LE
		text, _ = decodeUTF16(raw, binary.LittleEndian)
	case looksLikeUTF16(raw, binary.BigEndian):
		result.Encoding = EncodingUTF16BE
		text, _ = decodeUTF16(raw, binary.BigEndian)
	case utf8.Valid(raw) && !looksBinary(raw):
		result.Encoding = EncodingUTF8
		text = string(raw)
	case !looksBinary(raw):
		if decoded, ok := tryDecodeGBK(raw); ok {
			result.Encoding = EncodingGB18030
			text = decoded
		} else {
			result.Encoding = EncodingBinary
			result.Binary = true
			return result
		}
	default:
		result.Encoding = EncodingBinary
		result.Binary = true
		return result
	}

	if strings.Contains(text, "\r\n") {
		result.LineEnding = LineEndingCRLF
	}
	result.Content = normalizeToLF(text)
	return result
}

func decodeUTF16(raw []byte, order binary.ByteOrder) (string, bool) {
	if len(raw)%2 != 0 {
		return "", false
	}
	units := make([]uint16, len(raw)/2)
	for index := range units {
		units[index] = order.Uint16(raw[index*2 : index*2+2])
	}
	return string(utf16.Decode(units)), true
}

func encodeUTF16(value string, order binary.ByteOrder) []byte {
	units := utf16.Encode([]rune(value))
	out := make([]byte, len(units)*2)
	for index, unit := range units {
		order.PutUint16(out[index*2:index*2+2], unit)
	}
	return out
}

func looksLikeUTF16(raw []byte, order binary.ByteOrder) bool {
	if len(raw) < 4 || len(raw)%2 != 0 {
		return false
	}
	zeroEven, zeroOdd := 0, 0
	for index, value := range raw {
		if value != 0 {
			continue
		}
		if index%2 == 0 {
			zeroEven++
		} else {
			zeroOdd++
		}
	}
	threshold := max(2, len(raw)/8)
	if order == binary.LittleEndian {
		return zeroOdd >= threshold && zeroOdd > zeroEven*2
	}
	return zeroEven >= threshold && zeroEven > zeroOdd*2
}

func looksBinary(raw []byte) bool {
	if len(raw) == 0 {
		return false
	}
	controls := 0
	for _, value := range raw {
		if value == 0 {
			return true
		}
		if value < 0x20 && value != '\n' && value != '\r' && value != '\t' && value != '\f' {
			controls++
		}
	}
	return controls*20 > len(raw)
}

func tryDecodeGBK(raw []byte) (string, bool) {
	decoded, _, err := transform.Bytes(simplifiedchinese.GB18030.NewDecoder(), raw)
	if err != nil {
		return "", false
	}
	if !utf8.Valid(decoded) {
		return "", false
	}
	return string(decoded), true
}

func tryEncodeGB18030(value string) ([]byte, bool) {
	encoded, _, err := transform.Bytes(simplifiedchinese.GB18030.NewEncoder(), []byte(value))
	return encoded, err == nil
}

func normalizeToLF(text string) string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	return text
}

// EncodeFileText 把已编辑（LF、UTF-8）的内容按原文件的编码风格还原为字节，
// 用于写回：恢复原始行尾，必要时补回 BOM。始终以 UTF-8 落盘。
func EncodeFileText(content string, meta FileText) []byte {
	normalized := normalizeToLF(content)
	if meta.LineEnding == LineEndingCRLF {
		normalized = strings.ReplaceAll(normalized, "\n", "\r\n")
	}
	var out []byte
	switch meta.Encoding {
	case EncodingUTF16LE:
		out = encodeUTF16(normalized, binary.LittleEndian)
		if meta.HadBOM {
			out = append(append([]byte{}, utf16LEBOM...), out...)
		}
	case EncodingUTF16BE:
		out = encodeUTF16(normalized, binary.BigEndian)
		if meta.HadBOM {
			out = append(append([]byte{}, utf16BEBOM...), out...)
		}
	case EncodingGB18030:
		if encoded, ok := tryEncodeGB18030(normalized); ok {
			out = encoded
		} else {
			out = []byte(normalized)
		}
	default:
		out = []byte(normalized)
		if meta.HadBOM {
			out = append(append([]byte{}, utf8BOM...), out...)
		}
	}
	return out
}

// WriteFileTextAtomic 原子写入：先写临时文件再 rename，避免写一半损坏原文件。
// content 为 LF/UTF-8 文本，meta 决定行尾与 BOM。
func WriteFileTextAtomic(path string, content string, meta FileText) error {
	return WriteBytesAtomic(path, EncodeFileText(content, meta), 0o644)
}

// WriteBytesAtomic writes through a same-directory temporary file. The
// platform replace operation keeps the previous destination intact on error.
func WriteBytesAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if info, err := os.Stat(path); err == nil {
		perm = info.Mode().Perm()
	}
	if perm == 0 {
		perm = 0o600
	}

	tmp, err := os.CreateTemp(dir, ".echocode-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// 出错路径上清理临时文件。
	cleanup := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := replaceFileAtomic(tmpName, path); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return nil
}

// DecodeCommandOutput 解码命令 stdout/stderr：命令输出与文件是两条独立通道，
// Windows 控制台常吐 GBK，这里单独探测，避免污染到文件编码逻辑。
func DecodeCommandOutput(raw []byte) string {
	if bytes.HasPrefix(raw, utf16LEBOM) {
		if decoded, ok := decodeUTF16(raw[len(utf16LEBOM):], binary.LittleEndian); ok {
			return decoded
		}
	}
	if bytes.HasPrefix(raw, utf16BEBOM) {
		if decoded, ok := decodeUTF16(raw[len(utf16BEBOM):], binary.BigEndian); ok {
			return decoded
		}
	}
	if looksLikeUTF16(raw, binary.LittleEndian) {
		if decoded, ok := decodeUTF16(raw, binary.LittleEndian); ok {
			return decoded
		}
	}
	if looksLikeUTF16(raw, binary.BigEndian) {
		if decoded, ok := decodeUTF16(raw, binary.BigEndian); ok {
			return decoded
		}
	}
	raw = bytes.TrimPrefix(raw, utf8BOM)
	if utf8.Valid(raw) {
		return string(raw)
	}
	if decoded, ok := tryDecodeGBK(raw); ok {
		return decoded
	}
	return string(raw)
}

// DefaultFileMeta 返回新建文件时的默认编码风格：随宿主平台选择行尾、无 BOM。
func DefaultFileMeta() FileText {
	return DefaultFileMetaForPath("")
}

// DefaultFileMetaForPath gives Windows script formats encoding defaults that
// their native interpreters can read reliably. Existing files always preserve
// their detected encoding instead.
func DefaultFileMetaForPath(path string) FileText {
	le := LineEndingLF
	if os.PathSeparator == '\\' {
		le = LineEndingCRLF
	}
	meta := FileText{LineEnding: le, Encoding: EncodingUTF8}
	if os.PathSeparator == '\\' {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".ps1", ".psm1", ".psd1":
			meta.HadBOM = true
		case ".cmd", ".bat":
			meta.Encoding = EncodingGB18030
		}
	}
	return meta
}

// ParseLineEnding 把字符串还原为 LineEnding（用于从事件日志恢复元信息）。
func ParseLineEnding(s string) LineEnding {
	if s == string(LineEndingCRLF) {
		return LineEndingCRLF
	}
	return LineEndingLF
}

func ParseTextEncoding(value string) TextEncoding {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(EncodingUTF16LE), "utf16le":
		return EncodingUTF16LE
	case string(EncodingUTF16BE), "utf16be":
		return EncodingUTF16BE
	case string(EncodingGB18030), "gbk":
		return EncodingGB18030
	case string(EncodingBinary):
		return EncodingBinary
	default:
		return EncodingUTF8
	}
}

func FileTextSHA256(content string) string {
	sum := sha256.Sum256([]byte(normalizeToLF(content)))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// RestoreFile 用于 Rewind：把文件恢复到给定内容（编码安全、原子写）。
// 若 existed=false（表示改动前文件不存在），则删除该文件。
func RestoreFile(policy SandboxPolicy, path string, content string, existed bool, lineEnding string, encoding string, hadBOM bool) error {
	abs, err := policy.ResolveWritePath(path)
	if err != nil {
		return err
	}
	if !existed {
		if _, statErr := os.Stat(abs); statErr == nil {
			return os.Remove(abs)
		}
		return nil
	}
	if ParseTextEncoding(encoding) == EncodingBinary {
		data, err := base64.StdEncoding.DecodeString(content)
		if err != nil {
			return err
		}
		return WriteBytesAtomic(abs, data, 0600)
	}
	meta := FileText{LineEnding: ParseLineEnding(lineEnding), Encoding: ParseTextEncoding(encoding), HadBOM: hadBOM}
	return WriteFileTextAtomic(abs, content, meta)
}
