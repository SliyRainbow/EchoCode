package tools

import (
	"encoding/base64"
	"errors"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

const maxToolAttachmentBytes = 8 * 1024 * 1024

func AttachmentFromFile(path string) (Attachment, error) {
	info, err := os.Stat(path)
	if err != nil {
		return Attachment{}, err
	}
	if info.Size() > maxToolAttachmentBytes {
		return Attachment{}, errors.New("tool attachment exceeds 8 MiB")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return Attachment{}, err
	}
	mimeType := mime.TypeByExtension(strings.ToLower(filepath.Ext(path)))
	if mimeType == "" {
		mimeType = "application/octet-stream"
	}
	return Attachment{
		Name:     filepath.Base(path),
		MIMEType: mimeType,
		Data:     base64.StdEncoding.EncodeToString(data),
	}, nil
}
