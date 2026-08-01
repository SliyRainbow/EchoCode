package agent

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestNormalizeChatAttachments(t *testing.T) {
	attachments, err := normalizeChatAttachments([]ChatAttachment{{
		Name: "capture.png", MIMEType: "IMAGE/PNG", Data: "aGVsbG8=",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 || attachments[0].MIMEType != "image/png" || attachments[0].Name != "capture.png" {
		t.Fatalf("attachments = %#v", attachments)
	}

	_, err = normalizeChatAttachments([]ChatAttachment{{
		Name: "capture.bmp", MIMEType: "image/bmp", Data: "aGVsbG8=",
	}})
	if err == nil || !strings.Contains(err.Error(), "不支持图片格式") {
		t.Fatalf("unsupported image error = %v", err)
	}
}

func TestNormalizeMarkdownAttachmentUsesPrivateTextContext(t *testing.T) {
	markdown := "\ufeff# Deployment notes\n\nUse the staging environment."
	attachments, err := normalizeChatAttachments([]ChatAttachment{{
		Kind: "document", Name: "notes.md", MIMEType: "text/plain",
		Data: base64.StdEncoding.EncodeToString([]byte(markdown)),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 1 {
		t.Fatalf("attachments = %#v", attachments)
	}
	attachment := attachments[0]
	if attachment.Kind != chatAttachmentKindDocument || attachment.MIMEType != "text/markdown" || attachment.Size == 0 || attachment.CharacterCount == 0 {
		t.Fatalf("normalized Markdown attachment = %#v", attachment)
	}
	if images := protocolAttachments(attachments); len(images) != 0 {
		t.Fatalf("Markdown leaked into provider image attachments: %#v", images)
	}
	context := markdownReferenceRequestContext(attachments)
	for _, expected := range []string{"[reference_documents]", "notes.md", "Deployment notes", "user-provided data"} {
		if !strings.Contains(context, expected) {
			t.Fatalf("reference context missing %q: %q", expected, context)
		}
	}
	if strings.Contains(context, "\ufeff") {
		t.Fatalf("UTF-8 BOM was not removed: %q", context)
	}
}

func TestNormalizeMarkdownAttachmentRejectsWrongExtension(t *testing.T) {
	_, err := normalizeChatAttachments([]ChatAttachment{{
		Kind: "document", Name: "notes.txt", MIMEType: "text/plain",
		Data: base64.StdEncoding.EncodeToString([]byte("notes")),
	}})
	if err == nil || !strings.Contains(err.Error(), "扩展名无效") {
		t.Fatalf("wrong extension error = %v", err)
	}
}

func TestChatAttachmentsPersistAcrossSessionReload(t *testing.T) {
	sessions := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: sessions}
	svc := NewService(config)
	attachment := ChatAttachment{Name: "capture.png", MIMEType: "image/png", Data: "aGVsbG8="}
	svc.recordUserEventWithAttachments("看看这个", []ChatAttachment{attachment})

	history := svc.GetSessionMessages()
	if len(history) != 1 || len(history[0].Attachments) != 1 || history[0].Attachments[0] != attachment {
		t.Fatalf("history attachments = %#v", history)
	}

	reloaded := NewService(config)
	if len(reloaded.sessionMessages) != 1 || len(reloaded.sessionMessages[0].Attachments) != 1 {
		t.Fatalf("restored session = %#v", reloaded.sessionMessages)
	}
	restored := reloaded.sessionMessages[0].Attachments[0]
	if restored.Name != attachment.Name || restored.MIMEType != attachment.MIMEType || restored.Data != attachment.Data {
		t.Fatalf("restored attachment = %#v", restored)
	}
}

func TestMarkdownAttachmentPersistsAndRebuildsPrivateContext(t *testing.T) {
	sessions := t.TempDir()
	config := ServiceConfig{SkillsDir: t.TempDir(), SessionsDir: sessions}
	service := NewService(config)
	attachments, err := normalizeChatAttachments([]ChatAttachment{{
		Kind: "document", Name: "requirements.md", MIMEType: "text/markdown",
		Data: base64.StdEncoding.EncodeToString([]byte("# Requirements\n\nKeep the API stable.")),
	}})
	if err != nil {
		t.Fatal(err)
	}
	service.recordUserEventWithAttachments("请按文档修改", attachments)

	reloaded := NewService(config)
	if len(reloaded.sessionMessages) != 2 {
		t.Fatalf("restored protocol messages = %#v", reloaded.sessionMessages)
	}
	if reloaded.sessionMessages[0].InternalKind != contextRequestKind || !strings.Contains(reloaded.sessionMessages[0].Content, "Keep the API stable") {
		t.Fatalf("restored private document context = %#v", reloaded.sessionMessages[0])
	}
	if len(reloaded.sessionMessages[1].Attachments) != 0 || reloaded.sessionMessages[1].Content != "请按文档修改" {
		t.Fatalf("visible protocol user message = %#v", reloaded.sessionMessages[1])
	}
	history := reloaded.GetSessionMessages()
	if len(history) != 1 || len(history[0].Attachments) != 1 || history[0].Attachments[0].CharacterCount == 0 {
		t.Fatalf("restored visible history = %#v", history)
	}
}
