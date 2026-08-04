package extensions

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	maxArchiveEntries = 250000
	maxExtractedBytes = 4 << 30
)

func extractArchive(source, archiveType, destination string) error {
	if err := os.MkdirAll(destination, 0o755); err != nil {
		return err
	}
	switch archiveType {
	case "zip":
		return extractZIP(source, destination)
	case "tar.gz":
		return extractTarGZ(source, destination)
	default:
		return fmt.Errorf("不支持的压缩格式：%s", archiveType)
	}
}

func extractZIP(source, destination string) error {
	archive, err := zip.OpenReader(source)
	if err != nil {
		return err
	}
	defer archive.Close()
	if len(archive.File) > maxArchiveEntries {
		return fmt.Errorf("压缩包条目超过 %d 个", maxArchiveEntries)
	}
	var extractedBytes int64
	for _, entry := range archive.File {
		target, err := safeJoin(destination, entry.Name)
		if err != nil {
			return err
		}
		if entry.FileInfo().Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("压缩包包含不支持的符号链接：%s", entry.Name)
		}
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		reader, err := entry.Open()
		if err != nil {
			return err
		}
		mode := entry.Mode().Perm()
		if mode == 0 {
			mode = 0o644
		}
		writer, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
		if err != nil {
			reader.Close()
			return err
		}
		written, copyErr := copyArchiveEntry(writer, reader, &extractedBytes)
		closeWriterErr := writer.Close()
		closeReaderErr := reader.Close()
		if copyErr != nil {
			return copyErr
		}
		if written != int64(entry.UncompressedSize64) {
			return fmt.Errorf("压缩包条目大小不匹配：%s", entry.Name)
		}
		if closeWriterErr != nil {
			return closeWriterErr
		}
		if closeReaderErr != nil {
			return closeReaderErr
		}
	}
	return nil
}

func extractTarGZ(source, destination string) error {
	file, err := os.Open(source)
	if err != nil {
		return err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	reader := tar.NewReader(gzipReader)
	entryCount := 0
	var extractedBytes int64
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		entryCount++
		if entryCount > maxArchiveEntries {
			return fmt.Errorf("压缩包条目超过 %d 个", maxArchiveEntries)
		}
		target, err := safeJoin(destination, header.Name)
		if err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(header.Mode).Perm()
			if mode == 0 {
				mode = 0o644
			}
			writer, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			written, copyErr := copyArchiveEntry(writer, reader, &extractedBytes)
			closeErr := writer.Close()
			if copyErr != nil {
				return copyErr
			}
			if written != header.Size {
				return fmt.Errorf("压缩包条目大小不匹配：%s", header.Name)
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf("压缩包包含不支持的条目：%s", header.Name)
		}
	}
}

func copyArchiveEntry(destination io.Writer, source io.Reader, total *int64) (int64, error) {
	remaining := int64(maxExtractedBytes) - *total
	if remaining < 0 {
		return 0, fmt.Errorf("解压内容超过 %d MiB 上限", maxExtractedBytes>>20)
	}
	written, err := io.Copy(destination, io.LimitReader(source, remaining+1))
	*total += written
	if err != nil {
		return written, err
	}
	if *total > maxExtractedBytes {
		return written, fmt.Errorf("解压内容超过 %d MiB 上限", maxExtractedBytes>>20)
	}
	return written, nil
}

func safeJoin(root, relative string) (string, error) {
	root = filepath.Clean(root)
	relative = filepath.Clean(filepath.FromSlash(strings.TrimSpace(relative)))
	if relative == "." || relative == "" || filepath.IsAbs(relative) || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径离开目标目录：%s", relative)
	}
	target := filepath.Join(root, relative)
	if err := ensureWithinRoot(root, target); err != nil {
		return "", err
	}
	return target, nil
}

func ensureWithinRoot(root, target string) error {
	root, err := filepath.Abs(filepath.Clean(root))
	if err != nil {
		return err
	}
	target, err = filepath.Abs(filepath.Clean(target))
	if err != nil {
		return err
	}
	relative, err := filepath.Rel(root, target)
	if err != nil {
		return err
	}
	if relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("路径 %s 不在扩展目录 %s 内", target, root)
	}
	return nil
}

func removeWithinRoot(root, target string) error {
	if strings.TrimSpace(root) == "" || strings.TrimSpace(target) == "" {
		return errors.New("拒绝删除空扩展路径")
	}
	if err := ensureWithinRoot(root, target); err != nil {
		return err
	}
	rootAbs, _ := filepath.Abs(filepath.Clean(root))
	targetAbs, _ := filepath.Abs(filepath.Clean(target))
	if strings.EqualFold(rootAbs, targetAbs) {
		return errors.New("拒绝删除扩展根目录")
	}
	if err := os.RemoveAll(targetAbs); err != nil {
		return fmt.Errorf("删除扩展目录失败: %w", err)
	}
	return nil
}
