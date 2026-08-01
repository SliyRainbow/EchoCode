package artifacts

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/beevik/etree"
)

const (
	maxPackageEntryBytes = 64 << 20
	maxPackageBytes      = 256 << 20
	maxPackageEntries    = 20_000
)

func readPackage(path string) (map[string][]byte, error) {
	reader, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("打开 OOXML 文件失败: %w", err)
	}
	defer reader.Close()
	return readPackageEntries(reader.File)
}

func readPackageBytes(data []byte) (map[string][]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("打开内嵌 OOXML 模板失败: %w", err)
	}
	return readPackageEntries(reader.File)
}

func readPackageEntries(files []*zip.File) (map[string][]byte, error) {
	if len(files) > maxPackageEntries {
		return nil, fmt.Errorf("OOXML 文件包含过多条目: %d", len(files))
	}
	entries := make(map[string][]byte, len(files))
	var total int64
	for _, file := range files {
		if file.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(file.Name)
		if name == "" || strings.HasPrefix(name, "/") || strings.Contains(name, "../") {
			return nil, fmt.Errorf("OOXML 文件包含不安全条目: %q", file.Name)
		}
		if file.UncompressedSize64 > maxPackageEntryBytes {
			return nil, fmt.Errorf("OOXML 条目过大: %s", name)
		}
		total += int64(file.UncompressedSize64)
		if total > maxPackageBytes {
			return nil, errors.New("OOXML 解包后的内容超过 256 MiB 上限")
		}
		stream, openErr := file.Open()
		if openErr != nil {
			return nil, fmt.Errorf("读取 OOXML 条目 %s: %w", name, openErr)
		}
		data, readErr := io.ReadAll(io.LimitReader(stream, maxPackageEntryBytes+1))
		closeErr := stream.Close()
		if readErr != nil {
			return nil, fmt.Errorf("读取 OOXML 条目 %s: %w", name, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("关闭 OOXML 条目 %s: %w", name, closeErr)
		}
		if len(data) > maxPackageEntryBytes {
			return nil, fmt.Errorf("OOXML 条目过大: %s", name)
		}
		entries[name] = data
	}
	return entries, nil
}

func writePackage(path string, entries map[string][]byte) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("文件路径不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建产物目录失败: %w", err)
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".echocode-ooxml-*.tmp")
	if err != nil {
		return fmt.Errorf("创建临时产物失败: %w", err)
	}
	temporaryPath := temporary.Name()
	ok := false
	defer func() {
		_ = temporary.Close()
		if !ok {
			_ = os.Remove(temporaryPath)
		}
	}()

	archive := zip.NewWriter(temporary)
	names := make([]string, 0, len(entries))
	for name := range entries {
		names = append(names, filepath.ToSlash(name))
	}
	sort.Strings(names)
	for _, name := range names {
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(time.Now())
		writer, createErr := archive.CreateHeader(header)
		if createErr != nil {
			_ = archive.Close()
			return fmt.Errorf("创建 OOXML 条目 %s: %w", name, createErr)
		}
		if _, writeErr := writer.Write(entries[name]); writeErr != nil {
			_ = archive.Close()
			return fmt.Errorf("写入 OOXML 条目 %s: %w", name, writeErr)
		}
	}
	if err := archive.Close(); err != nil {
		return fmt.Errorf("完成 OOXML 压缩失败: %w", err)
	}
	if err := temporary.Sync(); err != nil {
		return fmt.Errorf("同步 OOXML 文件失败: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("关闭 OOXML 文件失败: %w", err)
	}
	if err := replaceFile(temporaryPath, path); err != nil {
		return err
	}
	ok = true
	return nil
}

func replaceFile(source, destination string) error {
	if _, err := os.Stat(destination); os.IsNotExist(err) {
		if renameErr := os.Rename(source, destination); renameErr != nil {
			return fmt.Errorf("保存产物失败: %w", renameErr)
		}
		return nil
	} else if err != nil {
		return fmt.Errorf("检查目标产物失败: %w", err)
	}
	backup := destination + fmt.Sprintf(".echocode-backup-%d", time.Now().UnixNano())
	if err := os.Rename(destination, backup); err != nil {
		return fmt.Errorf("准备替换产物失败: %w", err)
	}
	if err := os.Rename(source, destination); err != nil {
		_ = os.Rename(backup, destination)
		return fmt.Errorf("替换产物失败: %w", err)
	}
	_ = os.Remove(backup)
	return nil
}

func parseXML(data []byte, label string) (*etree.Document, error) {
	document := etree.NewDocument()
	if err := document.ReadFromBytes(data); err != nil {
		return nil, fmt.Errorf("解析 %s XML 失败: %w", label, err)
	}
	return document, nil
}

func xmlBytes(document *etree.Document) ([]byte, error) {
	document.WriteSettings.CanonicalText = false
	document.WriteSettings.CanonicalAttrVal = false
	var buffer bytes.Buffer
	if _, err := document.WriteTo(&buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func elementsByLocalName(root *etree.Element, name string) []*etree.Element {
	if root == nil {
		return nil
	}
	result := make([]*etree.Element, 0)
	var visit func(*etree.Element)
	visit = func(element *etree.Element) {
		if element.Tag == name {
			result = append(result, element)
		}
		for _, child := range element.ChildElements() {
			visit(child)
		}
	}
	visit(root)
	return result
}

func firstElementByLocalName(root *etree.Element, name string) *etree.Element {
	items := elementsByLocalName(root, name)
	if len(items) == 0 {
		return nil
	}
	return items[0]
}

func attributeByLocalName(element *etree.Element, name string) string {
	if element == nil {
		return ""
	}
	for _, attribute := range element.Attr {
		if attribute.Key == name {
			return attribute.Value
		}
	}
	return ""
}

func escapeXMLText(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")
	return replacer.Replace(value)
}

func escapeXMLAttribute(value string) string {
	replacer := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;", "'", "&apos;")
	return replacer.Replace(value)
}
